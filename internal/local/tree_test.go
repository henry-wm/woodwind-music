package local

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HengWh/woodwind-music/internal/music"
)

type dirTreeStore struct {
	mu          sync.Mutex
	roots       map[string]string // treeURI -> disk dir
	walkErr     map[string]error
	released    []string
	openCalls   atomic.Int64
	readFileHit map[string]int
}

func newDirTreeStore() *dirTreeStore {
	return &dirTreeStore{
		roots:       map[string]string{},
		walkErr:     map[string]error{},
		readFileHit: map[string]int{},
	}
}

func (d *dirTreeStore) add(treeURI, diskDir string) {
	d.mu.Lock()
	d.roots[treeURI] = diskDir
	d.mu.Unlock()
}

func (d *dirTreeStore) setWalkErr(treeURI string, err error) {
	d.mu.Lock()
	if err == nil {
		delete(d.walkErr, treeURI)
	} else {
		d.walkErr[treeURI] = err
	}
	d.mu.Unlock()
}

func (d *dirTreeStore) Walk(ctx context.Context, treeURI string) ([]TreeEntry, error) {
	d.mu.Lock()
	err := d.walkErr[treeURI]
	root := d.roots[treeURI]
	d.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if root == "" {
		return nil, ErrPermissionLost
	}
	var out []TreeEntry
	walkErr := filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		docID := "doc:" + rel
		docURI := ""
		switch strings.ToLower(path.Ext(rel)) {
		case ".mp3", ".flac", ".ogg", ".wav", ".m4a", ".aac":
			docURI = treeURI + "/document/" + rel
		}
		out = append(out, TreeEntry{
			RelPath: rel,
			DocID:   docID,
			DocURI:  docURI,
			Size:    info.Size(),
			Mod:     info.ModTime().UnixNano(),
		})
		return nil
	})
	return out, walkErr
}

func (d *dirTreeStore) resolve(treeURI, docID string) (string, error) {
	d.mu.Lock()
	root := d.roots[treeURI]
	d.mu.Unlock()
	if root == "" {
		return "", ErrPermissionLost
	}
	rel := strings.TrimPrefix(docID, "doc:")
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

func (d *dirTreeStore) Open(_ context.Context, treeURI, docID string, size int64) (TreeReader, error) {
	d.openCalls.Add(1)
	p, err := d.resolve(treeURI, docID)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	if info, err := f.Stat(); err == nil {
		size = info.Size()
	}
	return NewBlockReader(size, func(offset int64, length int) ([]byte, error) {
		buf := make([]byte, length)
		n, err := f.ReadAt(buf, offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		return buf[:n], nil
	}, f.Close), nil
}

func (d *dirTreeStore) ReadFile(_ context.Context, treeURI, docID string, maxBytes int64) ([]byte, error) {
	d.mu.Lock()
	d.readFileHit[docID]++
	d.mu.Unlock()
	p, err := d.resolve(treeURI, docID)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > maxBytes {
		b = b[:maxBytes]
	}
	return b, nil
}

func (d *dirTreeStore) Stat(_ context.Context, treeURI, docID string) (int64, bool) {
	p, err := d.resolve(treeURI, docID)
	if err != nil {
		return 0, false
	}
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() {
		return 0, false
	}
	return info.Size(), true
}

func (d *dirTreeStore) Release(treeURI string) {
	d.mu.Lock()
	d.released = append(d.released, treeURI)
	d.mu.Unlock()
}

func makePNGBytes(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := range 8 {
		for x := range 8 {
			im.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, im); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestBlockReaderCachesRangesAndSeeks(t *testing.T) {
	payload := bytes.Repeat([]byte("0123456789abcdef"), 20000) // 320 KiB
	var calls atomic.Int32
	var closed atomic.Bool
	r := NewBlockReader(int64(len(payload)), func(offset int64, length int) ([]byte, error) {
		calls.Add(1)
		if offset >= int64(len(payload)) {
			return nil, io.EOF
		}
		end := min(offset+int64(length), int64(len(payload)))
		return append([]byte(nil), payload[offset:end]...), nil
	}, func() error {
		closed.Store(true)
		return nil
	})
	// Multiple small reads within the first 128 KiB block should trigger only one fetch.
	buf := make([]byte, 16)
	for i := range 20 {
		n, err := r.Read(buf)
		if err != nil || n != 16 {
			t.Fatalf("read %d: n=%d err=%v", i, n, err)
		}
		want := payload[i*16 : (i+1)*16]
		if !bytes.Equal(buf, want) {
			t.Fatalf("read %d mismatch", i)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 block fetch, got %d", calls.Load())
	}
	// Seek from end and verify ReadAt / Read.
	pos, err := r.Seek(-16, io.SeekEnd)
	if err != nil || pos != int64(len(payload)-16) {
		t.Fatalf("seek end: pos=%d err=%v", pos, err)
	}
	n, err := r.Read(buf)
	if err != nil || n != 16 || !bytes.Equal(buf, payload[len(payload)-16:]) {
		t.Fatalf("tail read: n=%d err=%v", n, err)
	}
	if _, err := r.Read(buf); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF at end, got %v", err)
	}
	if err := r.Close(); err != nil || !closed.Load() {
		t.Fatalf("close failed: err=%v closed=%v", err, closed.Load())
	}
	// Second Close is a no-op.
	if err := r.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestTreeStoreDirectScanLyricsCoverStreamAndSync(t *testing.T) {
	dataDir := t.TempDir()
	treeDir := t.TempDir()
	treeURI := "content://com.android.externalstorage.documents/tree/primary%3AMusic%2FAlbums"

	albumDir := filepath.Join(treeDir, "AlbumA")
	if err := os.MkdirAll(albumDir, 0o755); err != nil {
		t.Fatal(err)
	}
	flacBytes, err := os.ReadFile(filepath.Join("..", "player", "testdata", "seek.flac"))
	if err != nil {
		t.Fatal(err)
	}
	mp3Bytes, err := os.ReadFile(filepath.Join("..", "player", "testdata", "seek.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(albumDir, "01 - Dawn.flac"), flacBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(albumDir, "02 - Dusk.mp3"), mp3Bytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(albumDir, "01 - Dawn.lrc"), []byte("[00:01.00]Morning light\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(albumDir, "cover.png"), makePNGBytes(t, color.RGBA{R: 200, G: 60, B: 40, A: 255}), 0o644); err != nil {
		t.Fatal(err)
	}

	store := newDirTreeStore()
	store.add(treeURI, treeDir)

	s := New(dataDir)
	t.Cleanup(s.Close)
	s.SetTreeStore(store)
	s.AddTreeFolder(treeURI, "Music/Albums")
	waitScan(t, s)

	if s.TrackCount() != 2 {
		t.Fatalf("TrackCount = %d, want 2", s.TrackCount())
	}
	if got := s.FolderLabel(treeURI); got != "Music/Albums" {
		t.Fatalf("FolderLabel = %q, want Music/Albums", got)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "imports")); !os.IsNotExist(err) {
		t.Fatalf("direct tree scan must not create imports directory")
	}

	res, err := s.Search("", music.SearchTracks, 0)
	if err != nil || len(res.Tracks) != 2 {
		t.Fatalf("Search: %v, %v", res, err)
	}
	var dawn, dusk music.Track
	for _, tr := range res.Tracks {
		if strings.Contains(tr.Name, "Dawn") {
			dawn = tr
		} else if strings.Contains(tr.Name, "Dusk") {
			dusk = tr
		}
	}
	if dawn.ID == "" || dusk.ID == "" {
		t.Fatalf("missing expected tracks: %+v", res.Tracks)
	}

	// Stream returns the original content:// URI directly with lossless quality for FLAC.
	st, err := s.Stream(dawn.ID, "")
	if err != nil {
		t.Fatalf("Stream(dawn): %v", err)
	}
	if !strings.HasPrefix(st.URL, "content://") {
		t.Fatalf("Stream URL = %q, want content:// URI", st.URL)
	}
	if st.Type != "flac" || st.Quality != "lossless" {
		t.Fatalf("Stream metadata = %+v, want flac/lossless", st)
	}

	// Sidecar .lrc is read on demand from the TreeStore.
	lyr, err := s.Lyrics(dawn.ID)
	if err != nil || !strings.Contains(lyr.LRC, "Morning light") {
		t.Fatalf("Lyrics(dawn) = %+v, %v", lyr, err)
	}

	// Sidecar cover.png is cached once in covers/ and served as file:// for both tracks.
	if dawn.Album.ImageURL == "" || dawn.Album.ImageURL != dusk.Album.ImageURL {
		t.Fatalf("shared sidecar cover mismatch: dawn=%q dusk=%q", dawn.Album.ImageURL, dusk.Album.ImageURL)
	}
	coverPath, ok := music.PathOf(dawn.Album.ImageURL)
	if !ok || filepath.Dir(coverPath) != filepath.Join(dataDir, "covers") {
		t.Fatalf("cover path = %q (ok=%v), want inside covers/", coverPath, ok)
	}
	store.mu.Lock()
	coverReads := store.readFileHit["doc:AlbumA/cover.png"]
	store.mu.Unlock()
	if coverReads != 1 {
		t.Fatalf("sidecar cover read %d times, want 1 deduplicated read", coverReads)
	}

	// Save like & playlist, reload Source from disk, and verify stable Track IDs + zero re-reads on unchanged rescan.
	if err := s.Like(dawn.ID, true); err != nil {
		t.Fatal(err)
	}
	opensBefore := store.openCalls.Load()
	s2 := New(dataDir)
	t.Cleanup(s2.Close)
	s2.SetTreeStore(store)
	if s2.TrackCount() != 2 {
		t.Fatalf("reloaded TrackCount = %d, want 2", s2.TrackCount())
	}
	liked, _ := s2.Liked(0)
	if len(liked.Tracks) != 1 || liked.Tracks[0].ID != dawn.ID {
		t.Fatalf("likes did not survive reload: %+v", liked)
	}
	s2.Rescan()
	waitScan(t, s2)
	if got := store.openCalls.Load(); got != opensBefore {
		t.Fatalf("unchanged rescan opened %d files, want 0", got-opensBefore)
	}

	// Modify directory: remove Dusk, add a new track and update Dawn's .lrc.
	if err := os.Remove(filepath.Join(albumDir, "02 - Dusk.mp3")); err != nil {
		t.Fatal(err)
	}
	// Deleted track's Stream fails before rescan.
	if _, err := s2.Stream(dusk.ID, ""); !errors.Is(err, music.ErrUnavailable) {
		t.Fatalf("Stream(deleted dusk) = %v, want ErrUnavailable", err)
	}
	if err := os.WriteFile(filepath.Join(albumDir, "03 - Noon.mp3"), mp3Bytes, 0o644); err != nil {
		t.Fatal(err)
	}
	s2.Rescan()
	waitScan(t, s2)
	if s2.TrackCount() != 2 {
		t.Fatalf("TrackCount after sync = %d, want 2", s2.TrackCount())
	}
	if _, err := s2.Stream(dusk.ID, ""); !errors.Is(err, music.ErrUnavailable) {
		t.Fatalf("removed track should stay unavailable: %v", err)
	}

	// Simulated permission loss records FolderError and clears playable tracks.
	store.setWalkErr(treeURI, ErrPermissionLost)
	s2.Rescan()
	waitScan(t, s2)
	if !errors.Is(s2.FolderError(treeURI), ErrPermissionLost) {
		t.Fatalf("FolderError = %v, want ErrPermissionLost", s2.FolderError(treeURI))
	}
	if s2.TrackCount() != 0 {
		t.Fatalf("TrackCount after permission loss = %d, want 0", s2.TrackCount())
	}

	// RemoveFolder releases persisted tree permission and clears error.
	s2.RemoveFolder(treeURI)
	waitScan(t, s2)
	store.mu.Lock()
	released := append([]string(nil), store.released...)
	store.mu.Unlock()
	if len(released) != 1 || released[0] != treeURI {
		t.Fatalf("released = %v, want [%s]", released, treeURI)
	}
	if s2.FolderError(treeURI) != nil {
		t.Fatalf("FolderError after remove = %v, want nil", s2.FolderError(treeURI))
	}
}

func waitScan(t *testing.T, s *Source) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for s.Scanning() {
		if time.Now().After(deadline) {
			t.Fatal("scan timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
