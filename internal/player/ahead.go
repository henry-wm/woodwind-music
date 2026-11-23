package player

import (
	"github.com/HengWh/woodwind-music/internal/music"
	"github.com/HengWh/woodwind-music/internal/offline"
)

type ahead struct {
	source, id, quality string
	handle              *offline.Handle
}

func (a *ahead) matches(t music.Track, quality string) bool {
	return a.source == t.Source && a.id == t.ID && a.quality == quality
}

// upNext is the track that plays when the current one ends on its own.
func (p *Player) upNext() (music.Track, bool) {
	if p.at < 0 || p.at >= len(p.order) || p.repeat == RepeatOne {
		return music.Track{}, false
	}
	at := p.at + 1
	if at >= len(p.order) {
		if p.repeat != RepeatAll || p.feed != nil {
			return music.Track{}, false
		}
		at = 0
	}
	if at == p.at {
		return music.Track{}, false
	}
	return p.queue[p.order[at]], true
}

func (p *Player) getAhead(quality string) {
	t, ok := p.upNext()
	if p.ahead != nil && ok && p.ahead.matches(t, quality) {
		return
	}
	p.dropAhead()
	if !p.Prefetch || p.Library == nil || !ok || p.track.LocalOnly || t.LocalOnly || !storedTrack(t) || p.Library.Has(t) {
		return
	}
	src := p.sourceOf(&t)
	if src == nil || src.Info().Playback == music.PlaybackRemote {
		return
	}
	if h, err := p.Library.Acquire(t, quality); err == nil {
		p.ahead = &ahead{source: t.Source, id: t.ID, quality: quality, handle: h}
	}
}

func (p *Player) takeAhead(t music.Track, quality string) *offline.Handle {
	a := p.ahead
	if a != nil && !t.LocalOnly && a.matches(t, quality) {
		p.ahead = nil
		return a.handle
	}
	p.dropAhead()
	return nil
}

func (p *Player) dropAhead() {
	if a := p.ahead; a != nil {
		p.ahead = nil
		a.handle.Close()
	}
}
