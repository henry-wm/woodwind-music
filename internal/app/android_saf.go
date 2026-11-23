package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"

	"github.com/HengWh/woodwind-music/internal/local"
)

// androidSAFStore implements local.TreeStore over Wood & Wind Music's Android
// host channel (DocumentLibrary.java), allowing Android folders picked via SAF
// to be scanned and played directly without copying files into private storage.
type androidSAFStore struct {
	cx     *core.Scope
	appCtx context.Context
	post   func(func())
}

type hostCallResult struct {
	res any
	err error
}

func newAndroidSAFStore(cx *core.Scope, post func(func())) *androidSAFStore {
	if cx == nil || cx.HostParams()["music.saf"] != true {
		return nil
	}
	return &androidSAFStore{cx: cx, appCtx: cx.Context(), post: post}
}

// Pick opens Android's persistent document tree picker on the host. Must be
// called on the engine goroutine.
func (s *androidSAFStore) Pick(done func(uri, name string, cancelled bool, err error)) {
	if s == nil || s.cx.Disposed() {
		done("", "", true, nil)
		return
	}
	s.cx.CallHostTimeout("saf.pick", nil, 0, func(res any, err error) {
		if err != nil {
			done("", "", false, wrapSAFError(err))
			return
		}
		m, _ := res.(map[string]any)
		if cancelled, _ := m["cancelled"].(bool); cancelled {
			done("", "", true, nil)
			return
		}
		uri, _ := m["uri"].(string)
		name, _ := m["name"].(string)
		if uri == "" {
			done("", "", true, nil)
			return
		}
		done(uri, name, false, nil)
	})
}

func (s *androidSAFStore) callHost(ctx context.Context, name string, data any, timeout time.Duration) (any, error) {
	if s == nil {
		return nil, errors.New("android SAF host unavailable")
	}
	appCtx := s.appCtx
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := appCtx.Err(); err != nil {
		return nil, err
	}
	ch := make(chan hostCallResult, 1)
	s.post(func() {
		if s.cx.Disposed() {
			ch <- hostCallResult{err: context.Canceled}
			return
		}
		s.cx.CallHostTimeout(name, data, timeout, func(res any, err error) {
			ch <- hostCallResult{res: res, err: err}
		})
	})
	select {
	case r := <-ch:
		return r.res, wrapSAFError(r.err)
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-appCtx.Done():
		return nil, appCtx.Err()
	}
}

func (s *androidSAFStore) Walk(ctx context.Context, treeURI string) ([]local.TreeEntry, error) {
	res, err := s.callHost(ctx, "saf.walk", map[string]any{"uri": treeURI}, 5*time.Minute)
	if err != nil {
		return nil, err
	}
	m, _ := res.(map[string]any)
	rawEntries, _ := m["entries"].([]any)
	entries := make([]local.TreeEntry, 0, len(rawEntries))
	for _, raw := range rawEntries {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		rel, _ := item["rel"].(string)
		docID, _ := item["docId"].(string)
		docURI, _ := item["docUri"].(string)
		size := hostInt64(item["size"])
		modMs := hostInt64(item["mod"])
		entries = append(entries, local.TreeEntry{
			RelPath: rel,
			DocID:   docID,
			DocURI:  docURI,
			Size:    size,
			Mod:     modMs * int64(time.Millisecond),
		})
	}
	return entries, nil
}

func (s *androidSAFStore) Open(ctx context.Context, treeURI, docID string, size int64) (local.TreeReader, error) {
	res, err := s.callHost(ctx, "saf.open", map[string]any{"uri": treeURI, "docId": docID}, 30*time.Second)
	if err != nil {
		return nil, err
	}
	m, _ := res.(map[string]any)
	handle := hostInt64(m["handle"])
	if handle <= 0 {
		return nil, errors.New("invalid SAF document handle")
	}
	openedSize := hostInt64(m["size"])
	if openedSize > 0 {
		size = openedSize
	}
	readAt := func(offset int64, length int) ([]byte, error) {
		r, err := s.callHost(ctx, "saf.read", map[string]any{
			"handle": handle,
			"offset": offset,
			"length": length,
		}, 30*time.Second)
		if err != nil {
			return nil, err
		}
		rm, _ := r.(map[string]any)
		encoded, _ := rm["data"].(string)
		if encoded == "" {
			return nil, nil
		}
		return base64.StdEncoding.DecodeString(encoded)
	}
	closeFn := func() error {
		s.post(func() {
			if !s.cx.Disposed() {
				s.cx.PostHost("saf.close", map[string]any{"handle": handle})
			}
		})
		return nil
	}
	return local.NewBlockReader(size, readAt, closeFn), nil
}

func (s *androidSAFStore) ReadFile(ctx context.Context, treeURI, docID string, maxBytes int64) ([]byte, error) {
	res, err := s.callHost(ctx, "saf.readFile", map[string]any{
		"uri":      treeURI,
		"docId":    docID,
		"maxBytes": maxBytes,
	}, 30*time.Second)
	if err != nil {
		return nil, err
	}
	m, _ := res.(map[string]any)
	encoded, _ := m["data"].(string)
	if encoded == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(encoded)
}

func (s *androidSAFStore) Stat(ctx context.Context, treeURI, docID string) (int64, bool) {
	res, err := s.callHost(ctx, "saf.stat", map[string]any{"uri": treeURI, "docId": docID}, 15*time.Second)
	if err != nil {
		return 0, false
	}
	m, _ := res.(map[string]any)
	exists, _ := m["exists"].(bool)
	if !exists {
		return 0, false
	}
	return hostInt64(m["size"]), true
}

func (s *androidSAFStore) Release(treeURI string) {
	if s == nil || treeURI == "" {
		return
	}
	s.post(func() {
		if !s.cx.Disposed() {
			s.cx.PostHost("saf.release", map[string]any{"uri": treeURI})
		}
	})
}

func wrapSAFError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.HasPrefix(msg, "permission:") {
		detail := strings.TrimSpace(strings.TrimPrefix(msg, "permission:"))
		if detail == "" {
			return local.ErrPermissionLost
		}
		return fmt.Errorf("%w: %s", local.ErrPermissionLost, detail)
	}
	return err
}

func hostInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	default:
		return 0
	}
}

var _ local.TreeStore = (*androidSAFStore)(nil)
