package player

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/HengWh/woodwind-music/internal/music"
	"github.com/HengWh/woodwind-music/internal/offline"
)

// NativeOutput is a platform media player. Calls and event callbacks run on
// the app's engine goroutine. Each Open owns its events until Close.
type NativeOutput interface {
	Open(source string, paused bool, events NativeEvents)
	SetPaused(bool)
	SetVolume(float64)
	SetSpeed(float64)
	Seek(time.Duration)
	Close()
}

type NativeEvents struct {
	Loaded   func(time.Duration)
	Progress func(position, duration time.Duration)
	Buffer   func(bool)
	Ended    func()
	Error    func(error)
}

type nativePlayback struct {
	ready              bool
	position, duration time.Duration
	ended              bool
	mu                 sync.Mutex
	ctx                context.Context
	cancel             context.CancelFunc
	handle             *offline.Handle
	unroute            func()
	closed             bool
}

func (n *nativePlayback) release() {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return
	}
	n.closed = true
	if n.cancel != nil {
		n.cancel()
	}
	h, unroute := n.handle, n.unroute
	n.handle, n.unroute = nil, nil
	n.mu.Unlock()
	if unroute != nil {
		unroute()
	}
	if h != nil {
		h.Close()
	}
}

func storedTrack(t music.Track) bool {
	return t.StoredID != "" || t.Source != "" && t.Source != "local"
}

func (n *nativePlayback) setHandle(h *offline.Handle) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		h.Close()
		return false
	}
	n.handle = h
	return true
}

func (n *nativePlayback) storeStream(store *offline.Store, t music.Track, quality string, pre *offline.Handle) (*music.Stream, error) {
	h := pre
	if h != nil {
		if _, err := h.Info(n.ctx); err != nil {
			h.Close()
			h = nil
		}
	}
	if h == nil {
		if err := n.ctx.Err(); err != nil {
			return nil, err
		}
		var err error
		h, err = store.Acquire(t, quality)
		if err != nil {
			return nil, err
		}
		if !n.setHandle(h) {
			return nil, context.Canceled
		}
	}
	info, err := h.Info(n.ctx)
	if err != nil {
		return nil, err
	}
	url, remove, err := h.URL()
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		remove()
		return nil, context.Canceled
	}
	n.unroute = remove
	info.URL = url
	return info, nil
}

type headerOutput interface {
	OpenWithHeaders(string, map[string]string, bool, NativeEvents)
}

func (p *Player) loadNative(gen int, src music.Source, t music.Track, quality string, at, skipped, dir int) {
	n := &nativePlayback{position: p.pending, handle: p.takeAhead(t, quality)}
	n.ctx, n.cancel = context.WithCancel(context.Background())
	p.native = n
	pre, output, store := n.handle, p.Native, p.Library
	go func() {
		var info *music.Stream
		var err error
		switch {
		case store != nil && storedTrack(t):
			info, err = n.storeStream(store, t, quality, pre)
		case src == nil:
			err = errNoSource
		default:
			info, err = src.Stream(t.ID, quality)
		}
		if err == nil && (info == nil || info.URL == "") {
			err = music.ErrUnavailable
		}
		if _, ok := output.(headerOutput); err == nil && len(info.Headers) > 0 && !ok {
			err = errors.New("native playback: stream request headers are not supported")
		}
		p.post(func() {
			if gen != p.gen || p.native != n {
				n.release()
				return
			}
			if err != nil {
				n.release()
				p.native = nil
				p.loading = false
				p.loadFailed(t.Source, err, at, skipped, dir)
				return
			}
			p.info, p.gain = info, normGain(info)
			// Posting events also prevents queue changes during NSGo's
			// dispatch of a Video event. Old tracks cannot change new ones.
			current := func(fn func()) {
				p.post(func() {
					if gen == p.gen && p.native == n {
						fn()
					}
				})
			}
			events := NativeEvents{
				Loaded: func(d time.Duration) {
					current(func() {
						n.ready, n.duration = true, max(0, d)
						p.loading = false
						p.nativeSeek(p.pending)
						p.pending = 0
						p.record()
						p.getAhead(quality)
						p.sync()
					})
				},
				Progress: func(pos, d time.Duration) {
					current(func() {
						n.position = max(0, pos)
						if d > 0 {
							n.duration = d
						}
					})
				},
				Buffer: func(waiting bool) {
					current(func() {
						p.loading = !n.ready || waiting
						p.sync()
					})
				},
				Ended: func() {
					current(func() {
						if n.ended {
							return
						}
						p.Native.SetPaused(true)
						n.ended = true
						if n.duration > 0 {
							n.position = n.duration
						}
						p.loading = false
						p.ended()
					})
				},
				Error: func(err error) {
					current(func() {
						p.Native.Close()
						n.release()
						p.dropAhead()
						p.native = nil
						p.loading = false
						p.loadFailed(t.Source, err, at, skipped, dir)
					})
				},
			}
			p.applyGain()
			p.applySpeed()
			if withHeaders, ok := output.(headerOutput); ok {
				withHeaders.OpenWithHeaders(info.URL, info.Headers, !p.playing, events)
			} else {
				output.Open(info.URL, !p.playing, events)
			}
			p.sync()
		})
	}()
}

func (p *Player) nativeToggle() {
	n := p.native
	p.playing = !p.playing
	if p.playing {
		if n.ended {
			p.nativeSeek(0)
		}
		if n.ready {
			p.record()
		}
	}
	p.Native.SetPaused(!p.playing)
	p.sync()
}

func (p *Player) nativeSeek(t time.Duration) {
	n := p.native
	t = max(0, t)
	if d := p.Duration(); d > 0 {
		t = min(t, d)
	}
	n.position, n.ended = t, false
	if n.ready {
		p.Native.Seek(t)
	} else {
		p.pending = t
	}
	p.sync()
}
