package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/nsgo-dev/nsgo/core"
	_ "golang.org/x/image/webp"

	"github.com/HengWh/woodwind-music/internal/i18n"
	"github.com/HengWh/woodwind-music/internal/music"
)

const maxPlaylistCoverBytes = 20 << 20 // 20 MiB

var (
	errCoverTooLarge = errors.New("cover image is too large")
	errCoverInvalid  = errors.New("unsupported image format")
)

// playlistCoverStore persists user-chosen playlist covers in the app data
// directory, keyed by source ID and playlist ID.
type playlistCoverStore struct {
	mu        sync.RWMutex
	dir       string
	indexPath string
	items     map[string]map[string]string
}

func loadPlaylistCovers(dataDir string) *playlistCoverStore {
	s := &playlistCoverStore{
		items: map[string]map[string]string{},
	}
	if dataDir == "" {
		return s
	}
	s.dir = filepath.Join(dataDir, "playlist-covers")
	s.indexPath = filepath.Join(s.dir, "index.json")
	raw, err := os.ReadFile(s.indexPath)
	if err != nil {
		return s
	}
	var loaded map[string]map[string]string
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return s
	}
	for srcID, entries := range loaded {
		for plID, name := range entries {
			if name == "" || filepath.Base(name) != name {
				continue
			}
			if _, err := os.Stat(filepath.Join(s.dir, name)); err != nil {
				continue
			}
			if s.items[srcID] == nil {
				s.items[srcID] = map[string]string{}
			}
			s.items[srcID][plID] = name
		}
	}
	return s
}

func (s *playlistCoverStore) URL(sourceID, playlistID string) string {
	if s == nil || s.dir == "" || sourceID == "" || playlistID == "" {
		return ""
	}
	s.mu.RLock()
	name := s.items[sourceID][playlistID]
	s.mu.RUnlock()
	if name == "" {
		return ""
	}
	return music.FileURL(filepath.Join(s.dir, name))
}

func (s *playlistCoverStore) Has(sourceID, playlistID string) bool {
	return s.URL(sourceID, playlistID) != ""
}

func (s *playlistCoverStore) SetBytes(sourceID, playlistID string, data []byte) (string, error) {
	if s == nil || s.dir == "" {
		return "", errors.New("storage unavailable")
	}
	if sourceID == "" || playlistID == "" {
		return "", errors.New("missing playlist identity")
	}
	if len(data) == 0 {
		return "", errCoverInvalid
	}
	if len(data) > maxPlaylistCoverBytes {
		return "", errCoverTooLarge
	}
	_, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", errCoverInvalid
	}
	ext := coverExtension(format)
	if ext == "" {
		return "", errCoverInvalid
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	filename := "cover-" + hex.EncodeToString(sum[:12]) + ext
	fullPath := filepath.Join(s.dir, filename)
	if _, err := os.Stat(fullPath); err != nil {
		if err := writeAtomicFile(fullPath, data, 0o600); err != nil {
			return "", err
		}
	}

	s.mu.Lock()
	oldName := s.items[sourceID][playlistID]
	if s.items[sourceID] == nil {
		s.items[sourceID] = map[string]string{}
	}
	s.items[sourceID][playlistID] = filename
	saveErr := s.saveLocked()
	var removeOld string
	if saveErr == nil && oldName != "" && oldName != filename && !s.referencedLocked(oldName) {
		removeOld = filepath.Join(s.dir, oldName)
	}
	s.mu.Unlock()

	if saveErr != nil {
		return "", saveErr
	}
	if removeOld != "" {
		_ = os.Remove(removeOld)
	}
	return music.FileURL(fullPath), nil
}

func (s *playlistCoverStore) Clear(sourceID, playlistID string) error {
	if s == nil || s.dir == "" || sourceID == "" || playlistID == "" {
		return nil
	}
	s.mu.Lock()
	entries := s.items[sourceID]
	oldName := entries[playlistID]
	if oldName == "" {
		s.mu.Unlock()
		return nil
	}
	delete(entries, playlistID)
	if len(entries) == 0 {
		delete(s.items, sourceID)
	}
	saveErr := s.saveLocked()
	var removeOld string
	if saveErr == nil && !s.referencedLocked(oldName) {
		removeOld = filepath.Join(s.dir, oldName)
	}
	s.mu.Unlock()

	if saveErr != nil {
		return saveErr
	}
	if removeOld != "" {
		_ = os.Remove(removeOld)
	}
	return nil
}

func (s *playlistCoverStore) referencedLocked(filename string) bool {
	for _, entries := range s.items {
		for _, name := range entries {
			if name == filename {
				return true
			}
		}
	}
	return false
}

func (s *playlistCoverStore) saveLocked() error {
	if s.indexPath == "" {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.items, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicFile(s.indexPath, b, 0o600)
}

func writeAtomicFile(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func coverExtension(format string) string {
	switch format {
	case "png":
		return ".png"
	case "jpeg":
		return ".jpg"
	case "webp":
		return ".webp"
	default:
		return ""
	}
}

func readPickedCover(file core.File) ([]byte, error) {
	if file.Path != "" {
		f, err := os.Open(file.Path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return readLimitedCover(f)
	}
	in, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer in.Close()
	return readLimitedCover(in)
}

func readLimitedCover(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxPlaylistCoverBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxPlaylistCoverBytes {
		return nil, errCoverTooLarge
	}
	return data, nil
}

func (a *app) playlistCoverURL(pl music.Playlist) string {
	if a.playlistCovers != nil && a.source() != nil {
		if u := a.playlistCovers.URL(a.source().Info().ID, pl.ID); u != "" {
			return u
		}
	}
	return pl.ImageURL
}

func (a *app) hasCustomPlaylistCover(pl music.Playlist) bool {
	if a.playlistCovers == nil || a.source() == nil {
		return false
	}
	return a.playlistCovers.Has(a.source().Info().ID, pl.ID)
}

func (a *app) canChangePlaylistCover(pl music.Playlist) bool {
	return !pl.Fixed && pl.ID != "" && a.playlistCovers != nil && a.dataDir != ""
}

func (a *app) pickPlaylistCover(pl music.Playlist) {
	if !a.canChangePlaylistCover(pl) {
		return
	}
	src := a.source()
	sourceID := src.Info().ID
	playlistID := pl.ID
	a.cx.PickFiles(core.PickFilesOptions{
		Limit:     1,
		Types:     []string{"image/png", "image/jpeg", "image/webp", ".png", ".jpg", ".jpeg", ".webp"},
		TypesName: i18n.T("Images"),
		Title:     i18n.T("Choose Playlist Cover"),
		Button:    i18n.T("Choose"),
	}, func(files []core.File, err error) {
		if err != nil {
			a.post(func() { a.say(err.Error()) })
			return
		}
		if len(files) == 0 {
			return
		}
		file := files[0]
		go func() {
			if file.Temporary && file.Path != "" {
				defer os.Remove(file.Path)
			}
			data, readErr := readPickedCover(file)
			var setErr error
			if readErr == nil {
				_, setErr = a.playlistCovers.SetBytes(sourceID, playlistID, data)
			} else {
				setErr = readErr
			}
			a.post(func() {
				if setErr != nil {
					switch {
					case errors.Is(setErr, errCoverTooLarge):
						a.say(i18n.T("Image is too large (max 20 MB)"))
					case errors.Is(setErr, errCoverInvalid):
						a.say(i18n.T("Please choose a PNG, JPEG, or WebP image"))
					default:
						a.say(setErr.Error())
					}
					return
				}
				a.say(i18n.T("Playlist cover updated"))
				a.bump()
			})
		}()
	})
}

func (a *app) resetPlaylistCover(pl music.Playlist) {
	if a.playlistCovers == nil || a.source() == nil || pl.ID == "" {
		return
	}
	sourceID := a.source().Info().ID
	if err := a.playlistCovers.Clear(sourceID, pl.ID); err != nil {
		a.say(err.Error())
		return
	}
	a.say(i18n.T("Playlist cover reset"))
	a.bump()
}
