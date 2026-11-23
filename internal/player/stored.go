package player

import "github.com/HengWh/woodwind-music/internal/i18n"

// DetachSource stops only online work; complete local playback keeps its lease.
func (p *Player) DetachSource(id string) {
	if p.ahead != nil && p.ahead.source == id {
		p.dropAhead()
	}
	if p.track != nil && p.track.Source == id {
		p.endRadio()
	}
	if p.track != nil && p.track.Source == id && (p.Library == nil || !p.Library.Has(*p.track)) {
		p.stop()
		p.gen++
		p.playing, p.loading = false, false
		p.err = i18n.T("The plugin was removed and this song has no complete local copy")
		p.sync()
	}
}
