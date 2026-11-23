//go:build windows && !woodwind_quiet

package nowplaying

import (
	"time"

	"github.com/nsgo-dev/nsgo/core"

	"github.com/HengWh/woodwind-music/internal/winshell"
)

func Initialize() error { return winshell.Start(core.App().ID) }
func Shutdown()         { winshell.Stop() }

func Bind(cx *core.Scope, onCommand func(Command, time.Duration)) Controls {
	winshell.SetCommandHandler(func(cmd int, pos time.Duration) { onCommand(Command(cmd), pos) })
	cx.Cleanup(func() { winshell.SetCommandHandler(nil) })
	return Controls{
		Set: func(info Info) {
			winshell.SetMedia(winshell.MediaInfo{Title: info.Title, Artist: info.Artist, Album: info.Album,
				Artwork: info.Artwork, Duration: info.Duration, Position: info.Position, Playing: info.Playing})
		},
		Position: func(pos time.Duration, playing bool, _ float64) { winshell.MoveMedia(pos, playing) },
		Clear:    winshell.ClearMedia,
	}
}
