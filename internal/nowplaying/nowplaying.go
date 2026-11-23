// Package nowplaying shows what the app plays where the system shows
// media, and reports the media keys and its controls on macOS and Windows.
package nowplaying

import (
	"github.com/nsgo-dev/nsgo/core"
	"time"
)

// Info is what the system shows of what plays.
type Info struct {
	Title, Artist, Album string
	// Artwork is the cover's bytes, a PNG or JPEG.
	Artwork  []byte
	Duration time.Duration
	Position time.Duration
	Playing  bool
	Speed    float64
}

// Command is a command of the media keys or the system's controls.
type Command int

const (
	Play Command = iota
	Pause
	Next
	Previous
	Seek
	Toggle
)

// Controls publishes the current track to the platform's media controls.
type Controls struct {
	Set      func(Info)
	Position func(time.Duration, bool, float64)
	Clear    func()
}

var nativeCommands = []core.MediaCommandKind{
	core.MediaPlay, core.MediaPause, core.MediaTogglePlayPause,
	core.MediaNext, core.MediaPrevious, core.MediaSeek,
}

func bindNative(cx *core.Scope, onCommand func(Command, time.Duration)) Controls {
	if !cx.Env().CanShowNowPlaying() {
		return Controls{}
	}
	cx.OnMediaCommand(func(c core.MediaCommand) {
		var command Command
		switch c.Kind {
		case core.MediaPlay:
			command = Play
		case core.MediaPause:
			command = Pause
		case core.MediaTogglePlayPause:
			command = Toggle
		case core.MediaNext:
			command = Next
		case core.MediaPrevious:
			command = Previous
		case core.MediaSeek:
			command = Seek
		default:
			return
		}
		onCommand(command, c.Position)
	})
	var current *core.NowPlaying
	clear := func() {
		current = nil
		cx.ClearNowPlaying()
	}
	cx.Cleanup(clear)
	return Controls{
		Set: func(info Info) {
			current = &core.NowPlaying{
				Title: info.Title, Artist: info.Artist, Album: info.Album,
				Duration: info.Duration, Position: info.Position, Rate: playbackRate(info.Playing, info.Speed),
				Commands: nativeCommands,
			}
			if len(info.Artwork) > 0 {
				current.Artwork = &core.ImageSource{Data: info.Artwork}
			}
			cx.SetNowPlaying(*current)
		},
		Position: func(pos time.Duration, playing bool, speed float64) {
			if current != nil {
				current.Position, current.Rate = pos, playbackRate(playing, speed)
				cx.SetNowPlaying(*current)
			}
		},
		Clear: clear,
	}
}

func playbackRate(playing bool, speed float64) float32 {
	if !playing {
		return 0
	}
	if speed <= 0 {
		return 1
	}
	return float32(speed)
}
