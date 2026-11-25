package app

import (
	"time"

	"github.com/HengWh/woodwind-music/internal/nowplaying"
	"github.com/HengWh/woodwind-music/internal/player"
)

func (a *app) setupDesktopMedia() {
	controls := nowplaying.Bind(a.cx, func(cmd nowplaying.Command, pos time.Duration) {
		a.post(func() {
			if a.mvCommand(cmd, pos) {
				return
			}
			switch cmd {
			case nowplaying.Play:
				if !a.pl.Playing() {
					a.pl.TogglePlay()
				}
			case nowplaying.Pause:
				if a.pl.Playing() {
					a.pl.TogglePlay()
				}
			case nowplaying.Toggle:
				a.pl.TogglePlay()
			case nowplaying.Next:
				a.pl.Next()
			case nowplaying.Previous:
				a.pl.Prev()
			case nowplaying.Seek:
				a.pl.Seek(pos)
			}
			a.heartbeat()
		})
	})

	if controls.Set == nil {
		return
	}
	a.pl.System = player.SystemMedia{
		Set: func(info player.NowPlaying) {
			controls.Set(nowplaying.Info{Title: info.Title, Artist: info.Artist, Album: info.Album,
				Artwork: info.Artwork, Duration: info.Duration, Position: info.Position, Playing: info.Playing, Speed: info.Speed})
		},
		Position: func(pos time.Duration, playing bool) { controls.Position(pos, playing, a.pl.Speed()) },
		Clear:    controls.Clear,
	}
}
