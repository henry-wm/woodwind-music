package nowplaying

import (
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
)

func TestNativeMediaPublishesStateAndRoutesCommands(t *testing.T) {
	var controls Controls
	var commands []Command
	var seek time.Duration
	ui := nsgotest.StartApp(t, func(cx *core.Scope) {
		controls = bindNative(cx, func(command Command, pos time.Duration) {
			commands = append(commands, command)
			seek = pos
		})
		controls.Set(Info{Title: "Track", Artist: "Artist", Album: "Album",
			Artwork: []byte{1, 2, 3}, Duration: time.Minute, Position: time.Second, Playing: true, Speed: 1.5})
	}, nsgotest.Environment(core.Environment{OS: core.OSMacOS, NowPlaying: true}))
	if got := ui.NowPlaying(); got == nil || got.Title != "Track" || got.Artist != "Artist" || got.Album != "Album" ||
		got.Artwork == nil || got.Duration != time.Minute || got.Position != time.Second || got.Rate != 1.5 {
		t.Fatalf("initial system metadata: %+v", got)
	}
	ui.Post(func() { controls.Position(15*time.Second, false, 2) })
	ui.Settle()
	if got := ui.NowPlaying(); got == nil || got.Position != 15*time.Second || got.Rate != 0 || got.Artwork == nil {
		t.Fatalf("paused system metadata: %+v", got)
	}
	ui.MediaCommand(core.MediaCommand{Kind: core.MediaTogglePlayPause})
	ui.MediaCommand(core.MediaCommand{Kind: core.MediaSeek, Position: 16 * time.Second})
	if len(commands) != 2 || commands[0] != Toggle || commands[1] != Seek || seek != 16*time.Second {
		t.Fatalf("system commands %v, seek %v", commands, seek)
	}
	ui.Post(controls.Clear)
	ui.Settle()
	if got := ui.NowPlaying(); got != nil {
		t.Fatalf("clear retained system metadata: %+v", got)
	}
}
