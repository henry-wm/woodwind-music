package local

import (
	"context"
	"errors"
	"io"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// ErrPermissionLost reports that a persisted document tree can no longer be
// read because its OS permission was revoked or its storage is unavailable.
var ErrPermissionLost = errors.New("folder access permission lost")

// TreeEntry is a music, lyric or cover image file found inside a persisted
// document tree such as an Android SAF folder.
type TreeEntry struct {
	RelPath string
	DocID   string
	DocURI  string
	Size    int64
	Mod     int64 // Unix nanoseconds
}

// TreeReader provides random-access reading over a document tree file.
type TreeReader interface {
	io.ReaderAt
	io.ReadSeeker
	io.Closer
}

// TreeStore accesses persisted document trees (such as Android SAF folders)
// without copying their media into app-private storage.
type TreeStore interface {
	Walk(ctx context.Context, treeURI string) ([]TreeEntry, error)
	Open(ctx context.Context, treeURI, docID string, size int64) (TreeReader, error)
	ReadFile(ctx context.Context, treeURI, docID string, maxBytes int64) ([]byte, error)
	Stat(ctx context.Context, treeURI, docID string) (size int64, ok bool)
	Release(treeURI string)
}

// IsTreeURI reports whether folder identifies a persisted document tree URI
// rather than a local filesystem path.
func IsTreeURI(folder string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(folder)), "content://")
}

// treeTrackPath builds the stable canonical key for a file inside treeURI.
func treeTrackPath(treeURI, relPath string) string {
	return strings.TrimRight(treeURI, "/") + "/" + strings.TrimLeft(relPath, "/")
}

// entryDir returns the parent directory key for album grouping.
func entryDir(e *entry) string {
	if e.TreeURI != "" || IsTreeURI(e.Path) {
		if i := strings.LastIndexByte(e.Path, '/'); i >= 0 {
			return e.Path[:i]
		}
		return e.TreeURI
	}
	return filepath.Dir(e.Path)
}

// entryBase returns the file name component of either a disk path or tree path.
func entryBase(p string) string {
	if IsTreeURI(p) {
		if i := strings.LastIndexByte(p, '/'); i >= 0 {
			return p[i+1:]
		}
		return p
	}
	return filepath.Base(p)
}

// prettyTreeLabel extracts a human-readable fallback name from an Android SAF
// tree URI when no display name was stored.
func prettyTreeLabel(treeURI string) string {
	raw := strings.TrimSpace(treeURI)
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	part := u.Path
	if idx := strings.Index(part, "/tree/"); idx >= 0 {
		part = part[idx+len("/tree/"):]
	}
	if decoded, err := url.PathUnescape(part); err == nil && decoded != "" {
		part = decoded
	}
	if vol, rest, ok := strings.Cut(part, ":"); ok {
		rest = strings.Trim(rest, "/")
		if rest != "" {
			if strings.EqualFold(vol, "primary") {
				return rest
			}
			if vol != "" {
				return vol + "/" + rest
			}
			return rest
		}
		if vol != "" {
			return vol
		}
	}
	base := path.Base(strings.TrimRight(part, "/"))
	if base != "" && base != "." && base != "/" {
		return base
	}
	return raw
}

const (
	blockReaderSize  = 128 << 10 // 128 KiB per IPC read
	blockReaderSlots = 8         // up to 1 MiB cached per opened file
)

type cachedBlock struct {
	index int64
	data  []byte
	seq   uint64
}

type blockReader struct {
	mu      sync.Mutex
	size    int64
	off     int64
	readAt  func(offset int64, length int) ([]byte, error)
	closeFn func() error
	closed  bool
	blocks  []cachedBlock
	seq     uint64
}

// NewBlockReader wraps a range-reading function with a small block cache so
// tag and duration parsers (which issue many small Read/ReadAt calls) make
// only a few roundtrips to the platform host.
func NewBlockReader(size int64, readAt func(offset int64, length int) ([]byte, error), closeFn func() error) TreeReader {
	return &blockReader{
		size:    size,
		readAt:  readAt,
		closeFn: closeFn,
	}
}

func (r *blockReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	r.mu.Lock()
	off := r.off
	r.mu.Unlock()
	n, err := r.ReadAt(p, off)
	r.mu.Lock()
	r.off += int64(n)
	r.mu.Unlock()
	if n > 0 && errors.Is(err, io.EOF) {
		return n, nil
	}
	return n, err
}

func (r *blockReader) Seek(offset int64, whence int) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var target int64
	switch whence {
	case io.SeekStart:
		target = offset
	case io.SeekCurrent:
		target = r.off + offset
	case io.SeekEnd:
		if r.size < 0 {
			return 0, errors.New("unknown document size")
		}
		target = r.size + offset
	default:
		return 0, errors.New("invalid seek whence")
	}
	if target < 0 {
		return 0, errors.New("negative seek position")
	}
	r.off = target
	return target, nil
}

func (r *blockReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	if len(p) == 0 {
		return 0, nil
	}
	total := 0
	for total < len(p) {
		pos := off + int64(total)
		if r.size >= 0 && pos >= r.size {
			return total, io.EOF
		}
		blockIdx := pos / blockReaderSize
		inBlock := int(pos % blockReaderSize)
		data, err := r.loadBlock(blockIdx)
		if err != nil {
			return total, err
		}
		if inBlock >= len(data) {
			return total, io.EOF
		}
		n := copy(p[total:], data[inBlock:])
		total += n
		if len(data) < blockReaderSize && inBlock+n >= len(data) {
			if total < len(p) {
				return total, io.EOF
			}
			break
		}
	}
	return total, nil
}

func (r *blockReader) loadBlock(index int64) ([]byte, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, errors.New("reader closed")
	}
	for i := range r.blocks {
		if r.blocks[i].index == index {
			r.seq++
			r.blocks[i].seq = r.seq
			data := r.blocks[i].data
			r.mu.Unlock()
			return data, nil
		}
	}
	r.mu.Unlock()

	offset := index * blockReaderSize
	length := blockReaderSize
	if r.size >= 0 && offset+int64(length) > r.size {
		length = int(r.size - offset)
	}
	if length <= 0 {
		return nil, nil
	}
	data, err := r.readAt(offset, length)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errors.New("reader closed")
	}
	r.seq++
	for i := range r.blocks {
		if r.blocks[i].index == index {
			r.blocks[i].data = data
			r.blocks[i].seq = r.seq
			return data, nil
		}
	}
	if len(r.blocks) < blockReaderSlots {
		r.blocks = append(r.blocks, cachedBlock{index: index, data: data, seq: r.seq})
	} else {
		oldest := 0
		for i := 1; i < len(r.blocks); i++ {
			if r.blocks[i].seq < r.blocks[oldest].seq {
				oldest = i
			}
		}
		r.blocks[oldest] = cachedBlock{index: index, data: data, seq: r.seq}
	}
	return data, nil
}

func (r *blockReader) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.blocks = nil
	closeFn := r.closeFn
	r.mu.Unlock()
	if closeFn != nil {
		return closeFn()
	}
	return nil
}
