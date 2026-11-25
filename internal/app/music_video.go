package app

import (
	"net/url"
	"time"

	"github.com/HengWh/woodwind-music/internal/i18n"
	"github.com/HengWh/woodwind-music/internal/music"
	"github.com/HengWh/woodwind-music/internal/nowplaying"
	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
)

type mvLookup struct {
	source     music.MVSource
	tracks     map[string]string
	pending    []string
	busy       bool
	retryAfter time.Time
}

type mvPlayback struct {
	track       music.Track
	source      *core.Signal[string]
	status      *core.Signal[string]
	paused      *core.Signal[bool]
	ref         core.Ref
	started     bool
	resumeTrack string
	relay       *music.StreamRelay
}

func (a *app) mvSource(track music.Track) music.MVSource {
	if track.LocalOnly || track.ID == "" {
		return nil
	}
	src, ok := a.sourceByID(track.Source).(music.MVSource)
	if !ok || !src.Can("trackMVs") || !src.Can("mvStream") {
		return nil
	}
	return src
}

// Coalesce mounted rows into a request per source. Virtual rows ask again
// when recycled, so scrolling a long playlist only checks the rows it needs.
func (a *app) trackMV(src music.MVSource, id string) string {
	sourceID := src.Info().ID
	lookup := a.mvLookups[sourceID]
	if lookup == nil || lookup.source != src {
		lookup = &mvLookup{source: src, tracks: map[string]string{}}
		a.mvLookups[sourceID] = lookup
	}
	if mv, known := lookup.tracks[id]; known || time.Now().Before(lookup.retryAfter) {
		return mv
	}
	lookup.tracks[id] = ""
	lookup.pending = append(lookup.pending, id)
	if !lookup.busy {
		lookup.busy = true
		a.post(func() { a.fetchTrackMVs(sourceID, lookup) })
	}
	return ""
}

func (a *app) fetchTrackMVs(sourceID string, lookup *mvLookup) {
	if a.mvLookups[sourceID] != lookup || a.sourceByID(sourceID) != lookup.source {
		return
	}
	ids := lookup.pending
	lookup.pending = nil
	go func() {
		mvs, err := lookup.source.TrackMVs(ids)
		a.post(func() {
			if a.mvLookups[sourceID] != lookup || a.sourceByID(sourceID) != lookup.source {
				return
			}
			lookup.busy = false
			if err != nil {
				for _, id := range ids {
					delete(lookup.tracks, id)
				}
				for _, id := range lookup.pending {
					delete(lookup.tracks, id)
				}
				lookup.pending = nil
				lookup.retryAfter = time.Now().Add(30 * time.Second)
				return
			}
			for _, id := range ids {
				lookup.tracks[id] = mvs[id]
			}
			if len(lookup.pending) > 0 {
				lookup.busy = true
				a.fetchTrackMVs(sourceID, lookup)
			}
			a.mvRev.Update(func(n int) int { return n + 1 })
		})
	}()
}

func (a *app) trackMVButton(p palette, track func() music.Track, testID string, compact bool) core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		t := track()
		src := a.mvSource(t)
		if src == nil {
			return nil
		}
		a.mvRev.Get()
		id := a.trackMV(src, t.ID)
		if id == "" {
			return nil
		}
		width, height := core.Pt(36), core.Pt(30)
		if compact {
			width, height = core.Pt(48), core.Pt(48)
		}
		return base.Button{TestID: testID, Label: i18n.Tf("Play MV for %s", t.Name),
			OnPress: func(core.PressEvent) { a.openMV(t, src, id) },
			Style: core.Style{Width: width, Height: height, FlexShrink: 0, CornerRadius: 8,
				AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
				Hover: &core.Style{Background: p.hover}, Active: &core.Style{Background: p.press}},
			Children: []core.Node{text("MV", 11, core.FontWeightMedium, p.accent)}}
	})
}

func (a *app) openMV(track music.Track, src music.MVSource, id string) {
	if a.mv.GetUntracked() != nil || a.mvSource(track) != src {
		return
	}
	r := &mvPlayback{
		track:  track,
		source: core.NewSignal(a.cx, ""),
		status: core.NewSignal(a.cx, i18n.T("Loading MV…")),
		paused: core.NewSignal(a.cx, true),
	}
	a.mv.Set(r)
	go func() {
		stream, err := src.MVStream(id)
		a.post(func() {
			if a.mv.GetUntracked() != r {
				return
			}
			if err != nil || stream == nil || a.mvSource(track) != src {
				a.failMV(r)
				return
			}
			u, err := url.Parse(stream.URL)
			if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
				a.failMV(r)
				return
			}
			if u.Scheme == "http" || len(stream.Headers) > 0 {
				relay, err := music.NewStreamRelay(stream.URL, stream.Headers)
				if err != nil {
					a.failMV(r)
					return
				}
				r.relay = relay
				r.source.Set(relay.URL)
				return
			}
			r.source.Set(stream.URL)
		})
	}()
}

func (a *app) failMV(r *mvPlayback) {
	r.paused.Set(true)
	r.source.Set("")
	r.status.Set(i18n.T("Could not play this MV."))
	if r.relay != nil {
		r.relay.Close()
		r.relay = nil
	}
}

func (a *app) startMV(r *mvPlayback) {
	if r.started {
		return
	}
	r.started = true
	a.cancelFocusResume()
	if a.pl.Playing() {
		r.resumeTrack = a.focusTrack()
		a.pl.TogglePlay()
	}
	r.status.Set("")
	r.paused.Set(false)
}

func (a *app) closeMV(resume bool) bool {
	r := a.mv.GetUntracked()
	if r == nil {
		return false
	}
	a.mv.Set(nil)
	r.paused.Set(true)
	if r.relay != nil {
		r.relay.Close()
	}
	if resume && r.resumeTrack != "" && r.resumeTrack == a.focusTrack() && !a.pl.Playing() {
		a.pl.TogglePlay()
	}
	return true
}

func (a *app) mvCommand(command nowplaying.Command, pos time.Duration) bool {
	r := a.mv.GetUntracked()
	if r == nil || !r.started || r.source.GetUntracked() == "" {
		return false
	}
	switch command {
	case nowplaying.Play:
		r.paused.Set(false)
	case nowplaying.Pause:
		r.paused.Set(true)
	case nowplaying.Toggle:
		r.paused.Update(func(paused bool) bool { return !paused })
	case nowplaying.Seek:
		r.ref.Seek(pos)
	}
	return true
}

func (a *app) mvView() core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		r := a.mv.Get()
		if r == nil {
			return nil
		}
		p := a.palette(cx)
		onVideo := func(fn func()) {
			a.post(func() {
				if a.mv.GetUntracked() == r && r.source.GetUntracked() != "" {
					fn()
				}
			})
		}
		top := core.Points(0)
		if a.video == nil {
			top = 36
		}
		return core.View{TestID: "mv-player", Role: core.RoleDialog, Label: r.track.Name,
			OnPress: func(core.PressEvent) {},
			Style: core.Style{Position: core.Absolute, Top: core.Pt(0), Right: core.Pt(0), Bottom: core.Pt(0), Left: core.Pt(0),
				ZIndex: 24, Background: p.bg, Direction: core.Column, Padding: core.Edges{Top: top}, PaddingEnv: core.EnvAll(core.EnvSafeArea)},
			Children: []core.Node{
				core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 12, Padding: core.Symmetric(4, 12)}, Children: []core.Node{
					a.phoneIcon(p, "mv-close", i18n.T("Close"), lucideX, p.text, func() { a.closeMV(true) }),
					core.Text{Content: r.track.Name, MaxLines: 1, Style: core.Style{MinWidth: core.Pt(0), FlexShrink: 1, FontSize: 16, FontWeight: core.FontWeightMedium, Color: p.text}},
				}},
				core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Background: core.RGB(0x000000),
					AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter}, Children: []core.Node{
					core.Dynamic(func(*core.Scope) core.Node {
						source := r.source.Get()
						if source == "" {
							return nil
						}
						return core.Video{TestID: "mv-video", Source: source, Ref: &r.ref, Controls: true, PausedFunc: r.paused.Get,
							AudioSession: core.AudioSessionPlayback,
							MutedFunc:    func() bool { a.rev.Get(); return a.pl.Volume() == 0 },
							VolumeFunc: func() float32 {
								a.rev.Get()
								return float32(a.pl.Volume() * a.pl.Volume())
							},
							Style:   core.Style{Width: core.Pct(100), Height: core.Pct(100)},
							OnLoad:  func(core.VideoInfo) { onVideo(func() { a.startMV(r) }) },
							OnEnd:   func() { onVideo(func() { r.paused.Set(true) }) },
							OnPlay:  func() { onVideo(func() { r.paused.Set(false) }) },
							OnPause: func(core.VideoPauseReason) { onVideo(func() { r.paused.Set(true) }) },
							OnError: func(error) { onVideo(func() { a.failMV(r) }) }}
					}),
					core.Show{When: func() bool { return r.status.Get() != "" }, Then: func(*core.Scope) core.Node {
						return core.Text{TestID: "mv-status", ContentFunc: r.status.Get,
							Style: core.Style{Position: core.Absolute, FontSize: 14, Color: core.RGB(0xffffff)}}
					}},
				}},
			}}
	})
}
