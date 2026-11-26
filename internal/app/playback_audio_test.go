package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"github.com/HengWh/woodwind-music/internal/music"
	"github.com/HengWh/woodwind-music/internal/player"
)

func TestDesktopNSGoAudioTransport(t *testing.T) {
	t.Setenv("WOOD_WIND_MUSIC_DATA_DIR", t.TempDir())
	const source = "file:///music/song.mp3"
	for _, platform := range []core.OS{core.OSMacOS, core.OSWindows} {
		t.Run(string(platform), func(t *testing.T) {
			var a *app
			var output *audioOutput
			var window *desktop.Window
			ui := nsgotest.StartApp(t, func(cx *core.Scope) {
				desktop.SetShutdownMode(cx, desktop.ShutdownOnExplicit)
				a = newApp(cx)
				output = newAudioOutput(cx)
				a.tray = &testWindowsTray{registered: true}
				cx.SetMainView(a.view)
				window = desktop.MainWindow(cx)
				a.bindMainWindow(window)
			}, nsgotest.Environment(core.Environment{OS: platform, Audio: true}), nsgotest.Size(1180, 780))
			ui.AudioDurations = map[string]time.Duration{source: time.Minute}
			var loaded time.Duration
			ui.Post(func() {
				output.SetVolume(0)
				output.SetSpeed(1.5)
				output.Open(source, true, player.NativeEvents{
					Loaded:   func(duration time.Duration) { loaded = duration },
					Progress: func(time.Duration, time.Duration) {},
					Buffer:   func(bool) {},
					Ended:    func() {},
					Error:    func(err error) { t.Error(err) },
				})
			})
			ui.Settle()
			if len(ui.Players) != 1 {
				t.Fatalf("NSGo opened %d players", len(ui.Players))
			}
			native := ui.Players[0]
			if loaded != time.Minute || native.Playing || native.Volume != 0 || native.Rate != 1.5 {
				t.Fatalf("unexpected preloaded player: %+v", native)
			}
			ui.Post(func() {
				output.Seek(12 * time.Second)
				output.SetVolume(0.5)
				output.SetPaused(false)
			})
			ui.Settle()
			if !native.Playing || native.Position != 12*time.Second {
				t.Fatalf("did not resume from seek: %+v", native)
			}
			if ui.CloseWindow() || window.Visible() || native.Closed || !native.Playing {
				t.Fatal("closing the main window interrupted playback")
			}
			ui.Post(a.showMain)
			ui.Settle()
			if !window.Visible() {
				t.Fatal("main window was not restored")
			}
			ui.Advance(300 * time.Millisecond)
			if native.Volume != 0.5 {
				t.Fatalf("resume fade ended at %v", native.Volume)
			}
			ui.Post(func() { output.SetPaused(true) })
			ui.Settle()
			if !native.Playing {
				t.Fatal("pause skipped the fade")
			}
			ui.Advance(200 * time.Millisecond)
			if native.Playing || native.Volume != 0 {
				t.Fatalf("pause fade left audio active: %+v", native)
			}
			ui.Post(output.Close)
			ui.Settle()
			if !native.Closed {
				t.Fatal("closing a track did not release its NSGo player")
			}
		})
	}
}

func TestDesktopPlayPauseButtonFadesAndPausesNSGoAudio(t *testing.T) {
	t.Setenv("WOOD_WIND_MUSIC_DATA_DIR", t.TempDir())
	musicDir := t.TempDir()
	trackPath := filepath.Join(musicDir, "Haruhikage.wav")
	writeToneWAV(t, trackPath)

	for _, platform := range []core.OS{core.OSMacOS, core.OSWindows} {
		t.Run(string(platform), func(t *testing.T) {
			var a *app
			ui := nsgotest.StartApp(t, func(cx *core.Scope) {
				desktop.SetShutdownMode(cx, desktop.ShutdownOnExplicit)
				a = newApp(cx)
				a.pl.Native = newAudioOutput(cx)
				a.pl.SetVolume(1)
				a.tray = &testWindowsTray{registered: true}
				cx.SetMainView(a.view)
			}, nsgotest.Environment(core.Environment{OS: platform, Audio: true}), nsgotest.Size(1180, 780))
			ui.AudioDurations = map[string]time.Duration{music.FileURL(trackPath): time.Minute}

			ui.Post(func() { a.local.AddFolders(musicDir) })
			ui.WaitUntil(5*time.Second, func() bool {
				return a.local.TrackCount() == 1 && !a.local.Scanning() && !a.homeBusy && a.home != nil && len(a.home.Sections) > 0
			})

			ui.Press("id:daily")
			ui.WaitUntil(2*time.Second, func() bool { return ui.Exists("text:Haruhikage") })
			ui.Press("text:Haruhikage >> nth=0")
			ui.WaitUntil(5*time.Second, func() bool {
				return len(ui.Players) == 1 && ui.Players[0].Playing && a.pl.Playing()
			})
			native := ui.Players[0]

			// Pressing the player bar play/pause button rebuilds that button via playRev.
			// Its pause fade timer must stay alive on the track scope and pause the audio player.
			ui.Press("id:play-pause")
			ui.Settle()
			if a.pl.Playing() {
				t.Fatal("player state stayed playing after pressing play-pause")
			}
			ui.Advance(250 * time.Millisecond)
			if native.Playing || native.Volume != 0 {
				t.Fatalf("player bar pause left native audio active: %+v", native)
			}

			// Pressing play/pause again must resume and complete its fade-in to full volume.
			ui.Press("id:play-pause")
			ui.Settle()
			if !a.pl.Playing() || !native.Playing {
				t.Fatalf("player bar resume did not restart audio: playing=%v native=%+v", a.pl.Playing(), native)
			}
			ui.Advance(300 * time.Millisecond)
			if native.Volume != 1 {
				t.Fatalf("player bar resume fade stayed at volume %v, want 1", native.Volume)
			}

			// The Now Playing overlay play/pause button rebuilds with playRev as well.
			ui.Press("id:bar-open")
			ui.Settle()
			ui.Press("id:np-controls >> id:play-pause")
			ui.Settle()
			ui.Advance(250 * time.Millisecond)
			if native.Playing || native.Volume != 0 {
				t.Fatalf("now playing pause left native audio active: %+v", native)
			}

			ui.Press("id:np-controls >> id:play-pause")
			ui.Settle()
			ui.Advance(300 * time.Millisecond)
			if !native.Playing || native.Volume != 1 {
				t.Fatalf("now playing resume left native audio in wrong state: %+v", native)
			}

			// Resuming mid-fade cancels the pending pause and restores full volume.
			ui.Press("id:np-controls >> id:play-pause")
			ui.Settle()
			ui.Advance(80 * time.Millisecond)
			if !native.Playing || native.Volume <= 0 || native.Volume >= 1 {
				t.Fatalf("pause did not fade midway: %+v", native)
			}
			ui.Press("id:np-controls >> id:play-pause")
			ui.Settle()
			ui.Advance(300 * time.Millisecond)
			if !native.Playing || native.Volume != 1 {
				t.Fatalf("mid-fade resume did not restore playback: %+v", native)
			}
		})
	}
}
