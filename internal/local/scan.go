package local

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"maps"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/dhowden/tag"
	"golang.org/x/text/encoding/simplifiedchinese"
)

const (
	// indexVersion changes when what's kept of a file does, for all of them to
	// be read again.
	indexVersion = 2
	// scanBatch is how many newly read files make a scan show what it has.
	scanBatch = 300
	// indexEvery is how often a long scan saves what it has, for quitting not to
	// lose it all.
	indexEvery = 5 * time.Second
)

type indexFile struct {
	Version int     `json:"version"`
	Tracks  []entry `json:"tracks"`
}

func (s *Source) indexPath() string { return filepath.Join(s.dir, "index.json") }
func (s *Source) coverDir() string  { return filepath.Join(s.dir, "covers") }

// loadIndex returns what the last scan found in the folders.
func (s *Source) loadIndex(folders []string) []entry {
	b, err := os.ReadFile(s.indexPath())
	if err != nil {
		return nil
	}
	var index indexFile
	if err := json.Unmarshal(b, &index); err != nil || index.Version != indexVersion {
		return nil
	}
	return slices.DeleteFunc(index.Tracks, func(e entry) bool {
		return !slices.ContainsFunc(folders, func(f string) bool { return within(e.Path, f) })
	})
}

func within(p, folder string) bool {
	if IsTreeURI(folder) {
		prefix := strings.TrimRight(folder, "/") + "/"
		return strings.HasPrefix(p, prefix)
	}
	if IsTreeURI(p) {
		return false
	}
	rel, err := filepath.Rel(folder, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *Source) saveIndex(entries map[string]entry) {
	tracks := slices.Collect(maps.Values(entries))
	slices.SortFunc(tracks, func(a, b entry) int { return strings.Compare(a.Path, b.Path) })
	b, err := json.Marshal(indexFile{Version: indexVersion, Tracks: tracks})
	if err == nil {
		err = writeAtomic(s.indexPath(), b)
	}
	if err != nil {
		log.Printf("local: saving index: %v", err)
	}
}

// Rescan starts a scan on its own goroutine, in place of one under way.
func (s *Source) Rescan() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.mu.Lock()
	if s.cancelScan != nil {
		s.cancelScan()
	}
	prev := s.scanDone
	s.cancelScan, s.scanDone = cancel, done
	s.scanGen++
	gen := s.scanGen
	s.scanning = true
	folders := slices.Clone(s.lib.Folders)
	s.mu.Unlock()

	go func() {
		defer close(done)
		defer cancel()
		// One at a time: the last one in charge of index.json is the latest.
		if prev != nil {
			<-prev
		}
		s.scan(ctx, gen, folders)
	}()
}

// found is a file a walk came across.
type found struct {
	path         string
	size         int64
	mod          int64
	sidecar      string
	treeURI      string
	docID        string
	docURI       string
	lyricsID     string
	sidecarDocID string
	sidecarKey   string
}

// picture is an image beside the music, named for a track or for its folder.
type picture struct {
	path  string
	docID string
	key   string
	base  string // its name without the picture extension, in lower case
	clean string // base without the extensions of what it was downloaded as
	ext   int    // which extension, for picking a jpg over a png
}

type treeLyric struct {
	exact string
	lower string
	docID string
}

type sidecarCache struct {
	mu     sync.Mutex
	paths  map[string]string
	active map[string]chan struct{}
}

func (c *sidecarCache) fetch(ctx context.Context, key string, fn func() string) string {
	for {
		c.mu.Lock()
		if p, ok := c.paths[key]; ok {
			c.mu.Unlock()
			return p
		}
		if wait := c.active[key]; wait != nil {
			c.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return ""
			}
		}
		done := make(chan struct{})
		if c.active == nil {
			c.active = map[string]chan struct{}{}
		}
		c.active[key] = done
		c.mu.Unlock()

		stored := fn()

		c.mu.Lock()
		if stored != "" {
			if c.paths == nil {
				c.paths = map[string]string{}
			}
			c.paths[key] = stored
		}
		delete(c.active, key)
		close(done)
		c.mu.Unlock()
		return stored
	}
}

// mediaExts are the extensions a track or picture may have been downloaded as,
// one after another: "Song.mp3.flac".
var mediaExts = []string{".mp3", ".flac", ".ogg", ".wav", ".m4a", ".aac"}

func (s *Source) scan(ctx context.Context, gen int64, folders []string) {
	trees := s.treeStore()
	files, folderErrs := walk(ctx, folders, trees)
	if ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	if gen == s.scanGen {
		s.folderErrs = folderErrs
	}
	s.mu.Unlock()

	// What hasn't changed needs no reading. What has stays as it was until read,
	// for it not to blink out of what's listed.
	prev := s.cat.Load()
	current := make(map[string]entry, len(files))
	var fresh []found
	metaChanged := false
	covers := map[string]bool{}
	var scCache sidecarCache
	for _, f := range files {
		old := prev.byPath[f.path]
		if old != nil {
			current[f.path] = old.entry
		}
		if f.treeURI == "" {
			if old == nil || old.Size != f.size || old.Mod != f.mod || !coverKept(old.Cover, covers) {
				fresh = append(fresh, f)
				continue
			}
			if old.Sidecar != f.sidecar {
				e := old.entry
				e.Sidecar = f.sidecar
				current[f.path] = e
				metaChanged = true
			}
			continue
		}
		if old == nil || old.TreeURI != f.treeURI || old.DocID != f.docID || old.Size != f.size || old.Mod != f.mod || old.FileType == "" || !coverKept(old.Cover, covers) {
			fresh = append(fresh, f)
			continue
		}
		e := old.entry
		changed := false
		if e.DocURI != f.docURI {
			e.DocURI = f.docURI
			changed = true
		}
		if e.LyricsID != f.lyricsID {
			e.LyricsID = f.lyricsID
			changed = true
		}
		if e.Cover == "" {
			if f.sidecarDocID == "" {
				if e.Sidecar != "" || e.SidecarKey != "" {
					e.Sidecar = ""
					e.SidecarKey = ""
					changed = true
				}
			} else if e.SidecarKey != f.sidecarKey || !coverKept(e.Sidecar, covers) {
				cached := s.cacheTreeSidecar(ctx, trees, f.treeURI, f.sidecarDocID, &scCache)
				if ctx.Err() != nil {
					return
				}
				e.Sidecar = cached
				e.SidecarKey = f.sidecarKey
				changed = true
			}
		}
		if changed {
			current[f.path] = e
			metaChanged = true
		}
	}
	gone := len(current) < len(prev.items)
	if gone || metaChanged {
		s.publish(gen, current, false)
	}

	lastSave := time.Now()
	read := 0
	for e := range s.readFiles(ctx, trees, fresh, &scCache) {
		current[e.Path] = e
		read++
		if read%scanBatch != 0 || ctx.Err() != nil {
			continue
		}
		s.publish(gen, current, false)
		if time.Since(lastSave) >= indexEvery {
			s.saveIndex(current)
			lastSave = time.Now()
		}
	}
	if ctx.Err() != nil {
		return
	}
	if gone || metaChanged || len(fresh) > 0 {
		s.saveIndex(current)
	}
	s.publish(gen, current, true)
}

func (s *Source) cacheTreeSidecar(ctx context.Context, trees TreeStore, treeURI, docID string, cache *sidecarCache) string {
	if trees == nil || docID == "" {
		return ""
	}
	key := treeURI + "\x00" + docID
	return cache.fetch(ctx, key, func() string {
		data, err := trees.ReadFile(ctx, treeURI, docID, maxTreeCoverBytes)
		if err != nil || len(data) == 0 {
			return ""
		}
		return s.keepCoverBytes(data, "")
	})
}

// publish makes the entries what's listed, and says so, unless a newer scan has
// taken over. The last one of a scan ends it.
func (s *Source) publish(gen int64, entries map[string]entry, last bool) {
	s.catalogMu.Lock()
	cat := s.buildCatalog(slices.Collect(maps.Values(entries)))
	s.mu.Lock()
	ok := gen == s.scanGen
	if ok {
		s.cat.Store(cat)
		if last {
			s.scanning = false
		}
	}
	s.mu.Unlock()
	s.catalogMu.Unlock()
	if ok {
		s.notify()
	}
}

// coverKept reports whether the kept picture of a file is still there, asking
// the disk once for each.
func coverKept(path string, seen map[string]bool) bool {
	if path == "" {
		return true
	}
	kept, ok := seen[path]
	if !ok {
		_, err := os.Stat(path)
		kept = err == nil
		seen[path] = kept
	}
	return kept
}

// walk finds the music in the folders, and the picture beside it if any.
// Hidden files and folders are left out, as are links to folders, which could
// lead round in circles; a folder chosen may be a link or hidden itself.
func walk(ctx context.Context, folders []string, trees TreeStore) ([]found, map[string]error) {
	var files []found
	seen := map[string]bool{}
	sidecars := map[string]string{}
	ranks := map[string]int{}
	pictures := map[string][]picture{}
	var folderErrs map[string]error
	diskEnd := 0
	for _, root := range folders {
		if ctx.Err() != nil {
			break
		}
		if IsTreeURI(root) {
			continue
		}
		start := root
		if !strings.HasSuffix(start, string(filepath.Separator)) {
			start += string(filepath.Separator) // WalkDir follows it, if it's a link
		}
		filepath.WalkDir(start, func(path string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return filepath.SkipAll
			}
			if err != nil {
				return nil // what can't be read is as good as gone
			}
			name := d.Name()
			if d.IsDir() {
				if path != start && strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasPrefix(name, ".") {
				return nil // such as macOS's ._song.mp3
			}
			if pic, ok := pictureNamed(name, path); ok {
				dir := filepath.Dir(path)
				pictures[dir] = append(pictures[dir], pic)
				if rank := sidecarRank(name); rank >= 0 {
					if have, ok := ranks[dir]; !ok || rank < have {
						ranks[dir] = rank
						sidecars[dir] = path
					}
				}
				return nil
			}
			switch strings.ToLower(filepath.Ext(name)) {
			case ".mp3", ".flac", ".ogg", ".wav", ".m4a", ".aac":
			default:
				return nil
			}
			if seen[path] {
				return nil // a folder within another also chosen
			}
			seen[path] = true
			info, err := os.Stat(path) // through a link, if it's one
			if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
				return nil
			}
			files = append(files, found{path: path, size: info.Size(), mod: info.ModTime().UnixNano()})
			return nil
		})
	}
	diskEnd = len(files)
	for i := range diskEnd {
		dir := filepath.Dir(files[i].path)
		files[i].sidecar = trackSidecar(files[i].path, pictures[dir], sidecars[dir])
	}

	for _, root := range folders {
		if ctx.Err() != nil {
			break
		}
		if !IsTreeURI(root) {
			continue
		}
		if trees == nil {
			if folderErrs == nil {
				folderErrs = map[string]error{}
			}
			folderErrs[root] = ErrPermissionLost
			continue
		}
		entries, err := trees.Walk(ctx, root)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			if folderErrs == nil {
				folderErrs = map[string]error{}
			}
			folderErrs[root] = err
			continue
		}
		treeFiles := walkTreeEntries(root, entries, seen)
		files = append(files, treeFiles...)
	}
	return files, folderErrs
}

func walkTreeEntries(root string, entries []TreeEntry, seen map[string]bool) []found {
	var files []found
	treePics := map[string][]picture{}
	treeFolderPic := map[string]picture{}
	treeFolderRank := map[string]int{}
	treeLyrics := map[string][]treeLyric{}
	for _, te := range entries {
		rel := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(te.RelPath, "\\", "/")), "/")
		if rel == "" || rel == "." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
			continue
		}
		hidden := false
		for seg := range strings.SplitSeq(rel, "/") {
			if strings.HasPrefix(seg, ".") {
				hidden = true
				break
			}
		}
		if hidden {
			continue
		}
		name := path.Base(rel)
		dir := path.Dir(rel)
		if pic, ok := pictureNamed(name, rel); ok {
			pic.docID = te.DocID
			pic.key = fmt.Sprintf("%s\x00%d\x00%d", te.DocID, te.Size, te.Mod)
			treePics[dir] = append(treePics[dir], pic)
			if rank := sidecarRank(name); rank >= 0 {
				if have, ok := treeFolderRank[dir]; !ok || rank < have || (rank == have && pic.path < treeFolderPic[dir].path) {
					treeFolderRank[dir] = rank
					treeFolderPic[dir] = pic
				}
			}
			continue
		}
		ext := strings.ToLower(path.Ext(name))
		if ext == ".lrc" {
			exact := strings.TrimSuffix(name, path.Ext(name))
			treeLyrics[dir] = append(treeLyrics[dir], treeLyric{
				exact: exact,
				lower: strings.ToLower(exact),
				docID: te.DocID,
			})
			continue
		}
		switch ext {
		case ".mp3", ".flac", ".ogg", ".wav", ".m4a", ".aac":
		default:
			continue
		}
		if te.Size <= 0 || te.DocID == "" {
			continue
		}
		fullPath := treeTrackPath(root, rel)
		if seen[fullPath] {
			continue
		}
		seen[fullPath] = true
		files = append(files, found{
			path:    fullPath,
			size:    te.Size,
			mod:     te.Mod,
			treeURI: root,
			docID:   te.DocID,
			docURI:  te.DocURI,
		})
	}
	for i := range files {
		rel := strings.TrimPrefix(files[i].path, strings.TrimRight(root, "/")+"/")
		dir := path.Dir(rel)
		name := path.Base(rel)
		if pic := trackSidecarPicture(name, treePics[dir], treeFolderPic[dir]); pic.docID != "" {
			files[i].sidecarDocID = pic.docID
			files[i].sidecarKey = pic.key
		}
		files[i].lyricsID = matchTreeLyric(name, treeLyrics[dir])
	}
	return files
}

func matchTreeLyric(name string, lyrics []treeLyric) string {
	stem := strings.TrimSuffix(name, path.Ext(name))
	lower := strings.ToLower(stem)
	best := ""
	bestExact := false
	for _, l := range lyrics {
		if l.exact == stem {
			if !bestExact || l.docID < best {
				best = l.docID
				bestExact = true
			}
		} else if !bestExact && l.lower == lower {
			if best == "" || l.docID < best {
				best = l.docID
			}
		}
	}
	return best
}

func trackSidecarPicture(name string, pictures []picture, folder picture) picture {
	stem := strings.ToLower(strings.TrimSuffix(name, path.Ext(name)))
	clean := cleanName(stem)
	best, bestRank := picture{}, math.MaxInt
	for _, pic := range pictures {
		match := -1
		switch {
		case pic.base == stem:
			match = 0
		case pic.clean == clean:
			match = 1
		}
		if match < 0 {
			continue
		}
		if rank := match*3 + pic.ext; rank < bestRank || (rank == bestRank && pic.path < best.path) {
			best, bestRank = pic, rank
		}
	}
	if best.docID != "" {
		return best
	}
	return folder
}

// pictureNamed makes a picture of an image file, if it's one the app can show.
func pictureNamed(name, path string) (picture, bool) {
	lower := strings.ToLower(name)
	ex := slices.Index([]string{".jpg", ".jpeg", ".png"}, filepath.Ext(lower))
	if ex < 0 {
		return picture{}, false
	}
	base := strings.TrimSuffix(lower, filepath.Ext(lower))
	return picture{path: path, base: base, clean: cleanName(base), ext: ex}, true
}

// trackSidecar is the picture beside a track: the one named after the track if
// there is one, as downloads leave them, or else the folder's cover. Names are
// compared with the extensions of what they were downloaded as taken off, for
// "Artist - Song.jpg" to stand for "Artist - Song.mp3.flac".
func trackSidecar(path string, pictures []picture, folder string) string {
	name := filepath.Base(path)
	stem := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
	clean := cleanName(stem)
	best, bestRank := "", math.MaxInt
	for _, pic := range pictures {
		match := -1
		switch {
		case pic.base == stem:
			match = 0 // the track's own name, extension and all
		case pic.clean == clean:
			match = 1
		}
		if match < 0 {
			continue
		}
		if rank := match*3 + pic.ext; rank < bestRank || (rank == bestRank && pic.path < best) {
			best, bestRank = pic.path, rank
		}
	}
	if best != "" {
		return best
	}
	return folder
}

// cleanName takes the extensions of what a picture or track was downloaded as
// off its name: "Song.mp3.flac" and "Song.mp3.jpg" are both "song".
func cleanName(name string) string {
	name = strings.ToLower(name)
	for {
		cut := false
		for _, ext := range mediaExts {
			if strings.HasSuffix(name, ext) {
				name = strings.TrimSuffix(name, ext)
				cut = true
				break
			}
		}
		if !cut {
			return name
		}
	}
}

// sidecarRank says how much a file's name makes it a folder's cover, lowest
// first, or -1 if it doesn't.
func sidecarRank(name string) int {
	name = strings.ToLower(name)
	ext := filepath.Ext(name)
	e := slices.Index([]string{".jpg", ".jpeg", ".png"}, ext)
	if e < 0 {
		return -1
	}
	n := slices.Index([]string{"cover", "folder", "front", "album"}, strings.TrimSuffix(name, ext))
	if n < 0 {
		return -1
	}
	return n*3 + e
}

// readFiles reads the files on a few goroutines, answering each as it's done.
// The channel closes once all are read, or soon after ctx ends.
func (s *Source) readFiles(ctx context.Context, trees TreeStore, files []found, scCache *sidecarCache) <-chan entry {
	jobs := make(chan found)
	out := make(chan entry)
	go func() {
		defer close(jobs)
		for _, f := range files {
			select {
			case jobs <- f:
			case <-ctx.Done():
				return
			}
		}
	}()
	var workers sync.WaitGroup
	for range min(runtime.NumCPU(), 4) {
		workers.Go(func() {
			for f := range jobs {
				if e, ok := s.readFile(ctx, trees, f, scCache); ok {
					out <- e
				}
			}
		})
	}
	go func() {
		workers.Wait()
		close(out)
	}()
	return out
}

type audioReader interface {
	io.ReaderAt
	io.ReadSeeker
}

// readFile reads what's known of a file, as far as there is any.
func (s *Source) readFile(ctx context.Context, trees TreeStore, file found, scCache *sidecarCache) (entry, bool) {
	var f audioReader
	var closer io.Closer
	if file.treeURI != "" {
		if trees == nil {
			return entry{}, false
		}
		r, err := trees.Open(ctx, file.treeURI, file.docID, file.size)
		if err != nil {
			return entry{}, false
		}
		f, closer = r, r
	} else {
		of, err := os.Open(file.path)
		if err != nil {
			return entry{}, false
		}
		f, closer = of, of
	}
	defer closer.Close()

	typ := fileType(f, file.path)
	var track m4aInfo
	if typ == "m4a" {
		var ok bool
		if track, ok = m4aTrack(f, file.size); !ok {
			return entry{}, false
		}
	}
	t := readTags(f, file.path, typ, file.size)
	e := entry{
		Path: file.path, Size: file.size, Mod: file.mod,
		TreeURI: file.treeURI, DocID: file.docID, DocURI: file.docURI, LyricsID: file.lyricsID,
		FileType: typ, Lossless: typ == "flac" || typ == "wav" || (typ == "m4a" && track.lossless),
		Title: t.title, Artist: t.artist, Album: t.album, AlbumArtist: t.albumArtist,
		Year: t.year, Track: t.track, Disc: t.disc,
		Sidecar: file.sidecar,
		Gain:    t.gain, Peak: t.peak,
	}
	if e.Title == "" {
		title, artist := nameOfFile(file.path)
		e.Title = title
		if e.Artist == "" {
			e.Artist = artist
		}
	}
	switch typ {
	case "flac":
		e.Duration = flacDuration(f)
	case "ogg":
		e.Duration = oggDuration(f, file.size)
	case "wav":
		e.Duration = wavDuration(f, file.size)
	case "m4a":
		e.Duration = track.duration
	case "aac":
		e.Duration = adtsDuration(f, file.size)
	default:
		e.Duration = mp3Duration(f, file.size)
	}
	if t.picture != nil {
		e.Cover = s.keepCover(t.picture)
	}
	if file.treeURI != "" && e.Cover == "" && file.sidecarDocID != "" {
		e.Sidecar = s.cacheTreeSidecar(ctx, trees, file.treeURI, file.sidecarDocID, scCache)
		e.SidecarKey = file.sidecarKey
	}
	return e, true
}

// nameOfFile makes a title of a file's name, and an artist if it reads
// "Artist - Title". "01 - Title" names no artist.
func nameOfFile(p string) (title, artist string) {
	base := entryBase(p)
	name := strings.TrimSuffix(base, path.Ext(base))
	before, after, ok := strings.Cut(name, " - ")
	before, after = strings.TrimSpace(before), strings.TrimSpace(after)
	if !ok || before == "" || after == "" {
		return name, ""
	}
	if strings.TrimFunc(before, func(r rune) bool { return unicode.IsDigit(r) || r == '.' }) == "" {
		return name, ""
	}
	return after, before
}

// tags is what a file's tags say.
type tags struct {
	title, artist, album, albumArtist string
	year, track, disc                 int
	gain, peak                        float64
	lyrics                            string
	picture                           *tag.Picture
}

// readTags reads a file's tags. The library panics on some malformed files, and
// answers nothing if it can't make them out: either way, what's found so far
// stands.
func readTags(f audioReader, name, typ string, size int64) (t tags) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("local: reading tags of %s: %v", name, r)
		}
	}()
	var src io.ReadSeeker = f
	// The library takes a FLAC behind an ID3 tag for an MP3.
	start := int64(0)
	switch typ {
	case "flac":
		start = id3v2Size(f)
	case "wav":
		// It reads no WAV: its tags are an ID3 tag in a chunk, or else
		// RIFF's own.
		id3 := wavID3(f, size)
		if id3 == nil {
			return wavInfo(f, size)
		}
		src = id3
	}
	if _, err := src.Seek(start, io.SeekStart); err != nil {
		return t
	}
	m, err := tag.ReadFrom(src)
	if err != nil {
		return t
	}
	t.title = cleanTag(m.Title())
	t.artist = cleanTag(m.Artist())
	t.album = cleanTag(m.Album())
	t.albumArtist = cleanTag(m.AlbumArtist())
	t.track, _ = m.Track()
	t.disc, _ = m.Disc()
	raw := m.Raw()
	t.year = yearOf(m.Year(), raw)
	t.gain, t.peak = replayGain(raw)
	t.lyrics = m.Lyrics()
	if t.lyrics == "" {
		t.lyrics, _ = raw["unsyncedlyrics"].(string)
	}
	t.lyrics = decodeLegacy(t.lyrics)
	t.picture = m.Picture()
	return t
}

func cleanTag(s string) string {
	return strings.Trim(decodeLegacy(s), " \t\r\n\x00")
}

// decodeLegacy undoes text the library read in the wrong encoding. ID3 has no
// way to say GBK, so Chinese taggers wrote it where ISO-8859-1 belongs, and the
// library hands it back a rune a byte; ID3v1 comes back as the bytes
// themselves. Some taggers wrote UTF-8 there too. A byte that's ASCII after a
// high one is taken for Latin-1, for "Motörhead" not to turn Chinese: GB2312,
// which nearly every such tag keeps to, has both bytes high.
func decodeLegacy(s string) string {
	var b []byte
	if utf8.ValidString(s) {
		high := false
		for _, r := range s {
			if r > 0xff {
				return s
			}
			high = high || r >= 0x80
		}
		if !high {
			return s
		}
		b = make([]byte, 0, len(s))
		for _, r := range s {
			b = append(b, byte(r))
		}
	} else {
		b = []byte(s)
	}
	if utf8.Valid(b) {
		return string(b)
	}
	if gbkPairs(b) {
		if out, err := simplifiedchinese.GBK.NewDecoder().Bytes(b); err == nil && utf8.Valid(out) && !bytes.ContainsRune(out, utf8.RuneError) {
			return string(out)
		}
	}
	// Latin-1 after all.
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

// gbkPairs tells whether every high byte of b is in a pair of two.
func gbkPairs(b []byte) bool {
	for i := 0; i < len(b); i++ {
		if b[i] < 0x80 {
			continue
		}
		if b[i] == 0x80 || b[i] == 0xff || i+1 == len(b) || b[i+1] < 0x80 || b[i+1] == 0xff {
			return false
		}
		i++
	}
	return true
}

// yearOf is the year the library found, or else from a date it can't parse, of
// which it may say the year 1.
func yearOf(year int, raw map[string]any) int {
	if year >= 1000 {
		return year
	}
	for _, key := range []string{"date", "year", "TDRC", "TYER", "TYE"} {
		if s, ok := raw[key].(string); ok && len(s) >= 4 {
			if y, err := strconv.Atoi(s[:4]); err == nil && y >= 1000 {
				return y
			}
		}
	}
	return 0
}

// replayGain finds the track's replay gain, in decibels, and peak: under their
// own names in Vorbis comments, and in a TXXX frame of ID3.
func replayGain(raw map[string]any) (gain, peak float64) {
	for key, v := range raw {
		var text string
		switch v := v.(type) {
		case string:
			text = v
		case *tag.Comm:
			key, text = v.Description, v.Text
		default:
			continue
		}
		switch strings.ToLower(key) {
		case "replaygain_track_gain":
			gain = parseGain(text)
		case "replaygain_track_peak":
			peak = parseGain(text)
		}
	}
	return gain, peak
}

// parseGain reads the number of "-6.5 dB".
func parseGain(text string) float64 {
	text = strings.TrimRightFunc(strings.TrimSpace(text), func(r rune) bool { return unicode.IsLetter(r) || unicode.IsSpace(r) })
	v, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// keepCover writes a picture to where covers are, under what's in it, for a
// whole album's copies to be one file. It returns the path, or "" if it's not
// a picture the app can show.
func (s *Source) keepCover(pic *tag.Picture) string {
	return s.keepCoverBytes(pic.Data, pic.MIMEType)
}

func (s *Source) keepCoverBytes(data []byte, mimeHint string) string {
	ext := coverExtBytes(data, mimeHint)
	if ext == "" {
		return ""
	}
	sum := sha256.Sum256(data)
	p := filepath.Join(s.coverDir(), hex.EncodeToString(sum[:8])+"."+ext)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if err := writeAtomic(p, data); err != nil {
		log.Printf("local: keeping cover: %v", err)
		return ""
	}
	return p
}

// coverExtBytes goes by what the picture holds, since taggers often say wrong,
// and else by what it says it is.
func coverExtBytes(data []byte, mimeHint string) string {
	if len(data) == 0 {
		return ""
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		mime = mimeHint
	}
	switch strings.ToLower(mime) {
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	}
	return ""
}

// fileType says whether a file is an mp3, a flac, an ogg, a wav, an m4a or an
// aac: by what it holds if that tells, and else by its name.
func fileType(f io.ReaderAt, p string) string {
	var head [12]byte
	if _, err := f.ReadAt(head[:], 0); err == nil {
		if string(head[4:8]) == "ftyp" {
			return "m4a"
		}
		if (string(head[:4]) == "RIFF" || string(head[:4]) == "RF64") && string(head[8:12]) == "WAVE" {
			return "wav"
		}
	}
	clear(head[:])
	if _, err := f.ReadAt(head[:4], id3v2Size(f)); err == nil && string(head[:4]) == "fLaC" {
		return "flac"
	}
	if string(head[:4]) == "OggS" {
		return "ogg"
	}
	if head[0] == 0xff && head[1]&0xf6 == 0xf0 {
		return "aac" // ADTS: MP3's layer bits are never 0
	}
	if _, ok := parseFrameHeader(head[:4]); ok {
		return "mp3"
	}
	return extFileType(p)
}

func extFileType(p string) string {
	switch ext := strings.ToLower(path.Ext(entryBase(p))); ext {
	case ".flac", ".ogg", ".wav", ".m4a", ".aac":
		return ext[1:]
	}
	return "mp3"
}
