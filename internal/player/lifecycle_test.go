package player

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/HengWh/woodwind-music/internal/music"
	"github.com/HengWh/woodwind-music/internal/offline"
)

func TestCloseNativeIgnoresLateEvents(t *testing.T) {
	p, out, ch := newNativeTest()
	p.PlayQueue([]music.Track{{ID: "one"}}, 0)
	nativeUntil(t, ch, func() bool { return out.source != "" })
	events := out.events
	p.Close()
	events.Loaded(time.Minute)
	events.Progress(time.Second, time.Minute)
	events.Ended()
	events.Error(errors.New("late error"))
	for len(ch) > 0 {
		(<-ch)()
	}
	if out.source != "" || p.Ready() || p.Playing() || p.Current() != nil {
		t.Fatal("native events revived the closed player")
	}
}

type closingSource struct {
	nothing
	stream func(string) (*music.Stream, error)
}

func (s closingSource) Stream(id, _ string) (*music.Stream, error) { return s.stream(id) }

// A slow plugin cannot open a native player after the app closes.
func TestCloseDuringSourceRequest(t *testing.T) {
	p, out, ch := newNativeTest()
	started, release := make(chan struct{}), make(chan struct{})
	p.Source = func(string) music.Source {
		return closingSource{stream: func(string) (*music.Stream, error) {
			close(started)
			<-release
			return &music.Stream{URL: "file:///music/song.mp3"}, nil
		}}
	}
	p.PlayTrack([]music.Track{{ID: "one"}}, 0)
	<-started
	p.Close()
	close(release)
	// The posted completion must observe the closed generation.
	select {
	case fn := <-ch:
		fn()
	case <-time.After(2 * time.Second):
		t.Fatal("the canceled load did not settle")
	}
	if out.source != "" || p.Current() != nil || p.Playing() {
		t.Fatal("a closed player accepted a late source result")
	}
}

// Cancel inputs whose headers have arrived but whose audio is still missing,
// including the next song's prefetch, and release their cache files.
func TestCloseCancelsDownloads(t *testing.T) {
	for _, prefetch := range []bool{false, true} {
		name := "loading"
		if prefetch {
			name = "prefetch"
		}
		t.Run(name, func(t *testing.T) {
			started, canceled := make(chan struct{}, 1), make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				started <- struct{}{}
				<-r.Context().Done()
				close(canceled)
			}))
			defer srv.Close()
			ch := make(chan func(), 64)
			p := New(func(fn func()) { ch <- fn })
			defer p.Close()
			p.SetVolume(0)
			p.Native = &nativeOutput{autoLoad: true}
			p.Prefetch = prefetch
			store, err := offline.Open(t.TempDir(), 1<<20, true)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(store.Close)
			p.Library = store
			path, err := filepath.Abs("testdata/seek.mp3")
			if err != nil {
				t.Fatal(err)
			}
			p.Source = func(string) music.Source {
				return closingSource{stream: func(id string) (*music.Stream, error) {
					if prefetch && id == "one" {
						return &music.Stream{URL: music.FileURL(path)}, nil
					}
					return &music.Stream{URL: srv.URL, Type: "mp3", Quality: "high"}, nil
				}}
			}
			store.SetSource(p.Source)
			q := []music.Track{{ID: "one", Source: "disk"}}
			if prefetch {
				q = append(q, music.Track{ID: "two", Source: "disk"})
			}
			p.PlayQueue(q, 0)
			if prefetch {
				settle(t, ch, p)
			}
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("download did not start")
			}
			p.Close()
			select {
			case <-canceled:
			case <-time.After(2 * time.Second):
				t.Fatal("closing playback did not cancel the transfer")
			}
		})
	}
}
