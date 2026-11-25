package app

import (
	"fmt"
	"math"
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"github.com/HengWh/woodwind-music/internal/i18n"
	"github.com/HengWh/woodwind-music/internal/music"
)

func wallTestTracks(n int) []music.Track {
	tracks := make([]music.Track, n)
	for i := range tracks {
		tracks[i] = music.Track{
			ID:       fmt.Sprintf("song-%03d", i),
			Name:     fmt.Sprintf("Song %03d", i),
			Artists:  []music.Artist{{Name: fmt.Sprintf("Artist %03d", i)}},
			Duration: 180000,
		}
	}
	return tracks
}

func startWallApp(t *testing.T, n, at int) (*app, *nsgotest.App) {
	t.Helper()
	t.Setenv("WOOD_WIND_MUSIC_DATA_DIR", t.TempDir())
	var a *app
	ui := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a = newApp(cx)
		a.set.Language = i18n.English
		a.applyLanguage()
		return a.view(cx)
	}, nsgotest.Size(1180, 780), nsgotest.Environment(core.Environment{Locale: "en", OS: core.OSMacOS, Idiom: core.IdiomDesktop}))

	tracks := wallTestTracks(n)
	a.post(func() {
		a.pl.Cue(tracks)
		if at > 0 && at < len(tracks) {
			a.pl.Jump(at)
			if a.pl.Playing() {
				a.pl.TogglePlay()
			}
		}
		a.openNowPlaying()
		a.npPane.Set(2)
	})
	ui.Settle()
	return a, ui
}

func TestWallShowsPlayingTrackAndVirtualizes(t *testing.T) {
	a, ui := startWallApp(t, 300, 250)

	ui.Find("id:wall-view")
	ui.Find("id:wall-area")
	ui.Find("id:wall-song-250")

	// Off-screen covers far from track 250 must not be mounted.
	if len(ui.QueryMounted("id:wall-song-000")) > 0 {
		t.Fatal("track 000 was mounted even though wall opened centered on track 250")
	}

	// Jumping to a track far away brings it into view.
	a.post(func() {
		a.pl.Jump(10)
		if a.pl.Playing() {
			a.pl.TogglePlay()
		}
	})
	ui.Settle()
	ui.Find("id:wall-song-010")
}

func TestWallClickPlaysCoverAndContextMenuWorks(t *testing.T) {
	a, ui := startWallApp(t, 60, 0)

	ui.Press("id:wall-song-003")
	ui.Settle()
	if cur := a.pl.Current(); cur == nil || cur.ID != "song-003" {
		t.Fatalf("clicked cover did not play song-003, got %v", cur)
	}

	ui.RightClick("id:wall-song-004")
	ui.Press(`text:"Copy Title"`)
	if ui.Clipboard != "Song 004" {
		t.Fatalf("context menu copied %q, want Song 004", ui.Clipboard)
	}
}

func TestWallPanAndWheelMoveIn2DWithinBounds(t *testing.T) {
	a, ui := startWallApp(t, 300, 150)

	startX, startY := a.wall.offX, a.wall.offY

	// Dragging diagonally pans along both axes and glides smoothly.
	ui.Pan("id:wall-area", core.Point{X: -60, Y: -40}, core.Point{X: -140, Y: -90})
	ui.Settle()
	if math.Abs(a.wall.offX-startX) < 40 || math.Abs(a.wall.offY-startY) < 30 {
		t.Fatalf("2D drag barely moved wall: from (%.1f, %.1f) to (%.1f, %.1f)", startX, startY, a.wall.offX, a.wall.offY)
	}

	// Trackpad/wheel pans immediately in 2D.
	beforeX, beforeY := a.wall.offX, a.wall.offY
	ui.Wheel("id:wall-area", core.Point{X: 80, Y: 50}, core.Modifiers{})
	ui.Settle()
	if a.wall.offX == beforeX || a.wall.offY == beforeY {
		t.Fatalf("wheel did not pan wall in 2D: before (%.1f, %.1f), after (%.1f, %.1f)", beforeX, beforeY, a.wall.offX, a.wall.offY)
	}
}

func TestLayWallShortAndLongQueues(t *testing.T) {
	short := layWall(4, 1180, 500, wallFrost, wallFoot)
	if short.cols != 4 || short.rows != 1 {
		t.Fatalf("short wall = %dx%d, want 4x1", short.cols, short.rows)
	}
	if short.minX != short.maxX {
		t.Errorf("short wall should be centered with zero horizontal pan range: [%.1f, %.1f]", short.minX, short.maxX)
	}

	long := layWall(300, 1180, 500, wallFrost, wallFoot)
	if long.maxX <= long.minX || long.maxY <= long.minY {
		t.Fatalf("long wall should pan along both axes: x=[%.1f, %.1f] y=[%.1f, %.1f]", long.minX, long.maxX, long.minY, long.maxY)
	}
}
