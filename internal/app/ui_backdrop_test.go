package app

import (
	"math"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"github.com/HengWh/woodwind-music/internal/i18n"
	"github.com/HengWh/woodwind-music/internal/music"
)

func TestFogShaderCompilesAndHasUniforms(t *testing.T) {
	prog, err := core.CompileWGSL(fogWGSLSource)
	if err != nil {
		t.Fatalf("fog WGSL failed to compile: %v", err)
	}
	for _, name := range []string{"prevDeep", "prevFog", "deep", "fog", "blend", "time"} {
		if !prog.HasUniform(name) {
			t.Errorf("fog shader missing uniform %q", name)
		}
	}
}

func TestFogColorsKeepNeutralGrayAndRichTint(t *testing.T) {
	deep, fog := fogColors(core.RGB(0x808080))
	for name, c := range map[string]core.Color{"deep": deep, "fog": fog} {
		r, g, b, _ := c.Components()
		if math.Abs(r-g) > 0.02 || math.Abs(g-b) > 0.02 {
			t.Errorf("neutral cover %s should stay gray, got (%.2f, %.2f, %.2f)", name, r, g, b)
		}
	}

	rDeep, rFog := fogColors(core.RGB(0xd04030))
	rr, rg, rb, _ := rDeep.Components()
	if rr <= rg || rr <= rb {
		t.Errorf("red cover deep shade lost red dominance: (%.2f, %.2f, %.2f)", rr, rg, rb)
	}
	fr, fg, fb, _ := rFog.Components()
	if fr <= rr {
		t.Errorf("fog shade should be lighter than deep shade: fog=%.2f deep=%.2f", fr, rr)
	}
	if fr <= fg || fr <= fb {
		t.Errorf("red cover fog shade lost red dominance: (%.2f, %.2f, %.2f)", fr, fg, fb)
	}
}

func TestFogBackdropToggleAndExperimentsCleanup(t *testing.T) {
	t.Setenv("WOOD_WIND_MUSIC_DATA_DIR", t.TempDir())
	var a *app
	ui := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a = newApp(cx)
		a.set.Language = i18n.English
		a.applyLanguage()
		a.settingsInMain = true
		return a.view(cx)
	}, nsgotest.Size(1180, 780), nsgotest.Environment(core.Environment{Locale: "en", OS: core.OSMacOS, Idiom: core.IdiomDesktop}))

	tracks := []music.Track{
		{ID: "t1", Name: "First Track", Artists: []music.Artist{{Name: "Artist One"}}, Duration: 180000},
		{ID: "t2", Name: "Second Track", Artists: []music.Artist{{Name: "Artist Two"}}, Duration: 180000},
	}
	a.post(func() {
		a.pl.Cue(tracks)
		a.openSettings()
		a.settingsPage = "experiments"
		a.bump()
	})
	ui.Settle()

	// Fog Backdrop remains in Experiments; Smoke and Grow From the Cover are removed.
	ui.Find(`text:"Fog Backdrop"`)
	if ui.Exists(`text:"Smoke on the Progress Bar"`) {
		t.Fatal("removed Smoke switch still appears in settings")
	}
	if ui.Exists(`text:"Grow From the Cover"`) {
		t.Fatal("removed Grow From the Cover switch still appears in settings")
	}

	// Open Now Playing with Fog off: no fog shader mounted.
	a.post(func() {
		a.closeSettings()
		a.openNowPlaying()
	})
	ui.Settle()
	if ui.Exists("id:np-fog") {
		t.Fatal("fog shader mounted while Fog setting is off")
	}

	// Turn Fog on: GPU fog shader mounts immediately behind Now Playing.
	a.post(func() {
		a.set.Fog = true
		a.bump()
	})
	ui.Settle()
	ui.Find("id:np-fog")

	// Turn Fog back off: fog shader unmounts cleanly.
	a.post(func() {
		a.set.Fog = false
		a.bump()
	})
	ui.Settle()
	if ui.Exists("id:np-fog") {
		t.Fatal("fog shader stayed mounted after turning Fog off")
	}
}

func fogUniformNode(t *testing.T, v *nsgotest.View, lane int) uint32 {
	t.Helper()
	if v == nil || v.Shader == nil {
		t.Fatal("np-fog missing Shader data")
	}
	for _, n := range v.Shader.Nodes {
		if n.Lane == lane {
			return n.Node
		}
	}
	t.Fatalf("np-fog missing animated node in lane %d: %+v", lane, v.Shader.Nodes)
	return 0
}

func TestFogClockSurvivesPlayRevAndPausesCleanly(t *testing.T) {
	t.Setenv("WOOD_WIND_MUSIC_DATA_DIR", t.TempDir())
	var a *app
	ui := nsgotest.StartApp(t, func(cx *core.Scope) {
		desktop.SetShutdownMode(cx, desktop.ShutdownOnExplicit)
		a = newApp(cx)
		a.set.Language = i18n.English
		a.applyLanguage()
		cx.SetMainView(a.view)
	}, nsgotest.Size(1180, 780), nsgotest.Environment(core.Environment{Locale: "en", OS: core.OSMacOS, Idiom: core.IdiomDesktop}))

	a.post(func() {
		reviewTrack(a)
		a.heartbeat()
	})
	ui.Settle()

	// Open Settings in a separate macOS window and toggle Fog Backdrop on.
	a.post(func() {
		a.settingsPage = "experiments"
		a.openSettings()
	})
	ui.Settle()
	ui.Surface("index:1").Press("id:settings-fog")
	ui.Settle()

	if !a.set.Fog {
		t.Fatal("clicking settings-fog did not enable Fog")
	}
	// Uniform layout: builtinLanes(3) + prevDeep(4) + prevFog(4) + deep(4) + fog(4) + blend(1) + time(1) -> time is lane 20.
	clockID := fogUniformNode(t, ui.Find("id:np-fog"), 20)

	t0 := ui.AnimatedValue(clockID)
	ui.Advance(500 * time.Millisecond)
	t1 := ui.AnimatedValue(clockID)
	if t1 <= t0+0.3 {
		t.Fatalf("fog clock did not advance while playing: t0=%.3f t1=%.3f", t0, t1)
	}

	// Playback state updates (Loaded / Buffer / lyrics refresh) bump playRev while Playing stays true.
	a.post(func() {
		a.playRev.Update(func(n int) int { return n + 1 })
	})
	ui.Settle()
	t2 := ui.AnimatedValue(clockID)
	ui.Advance(500 * time.Millisecond)
	t3 := ui.AnimatedValue(clockID)
	if t3 <= t2+0.3 {
		t.Fatalf("fog clock froze after playRev bump while still playing: t2=%.3f t3=%.3f", t2, t3)
	}

	// Pause playback: fog clock freezes in place.
	a.post(func() {
		a.pl.Clear()
	})
	ui.Settle()
	pausedAt := ui.AnimatedValue(clockID)
	ui.Advance(500 * time.Millisecond)
	afterPause := ui.AnimatedValue(clockID)
	if math.Abs(afterPause-pausedAt) > 0.05 {
		t.Fatalf("fog clock kept moving while paused: pausedAt=%.3f afterPause=%.3f", pausedAt, afterPause)
	}

	// Resume playback: fog clock continues smoothly from where it paused.
	a.post(func() {
		reviewTrack(a)
		a.playRev.Update(func(n int) int { return n + 1 })
	})
	ui.Settle()
	ui.Advance(500 * time.Millisecond)
	resumed := ui.AnimatedValue(clockID)
	if resumed <= pausedAt+0.3 {
		t.Fatalf("fog clock did not resume from paused time: pausedAt=%.3f resumed=%.3f", pausedAt, resumed)
	}
}

func TestLyricsAutoScrollAndWordFramesWithFogEnabled(t *testing.T) {
	t.Setenv("WOOD_WIND_MUSIC_DATA_DIR", t.TempDir())
	var a *app
	ui := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a = newApp(cx)
		a.set.Language = i18n.English
		a.applyLanguage()
		a.set.Fog = true
		return a.view(cx)
	}, nsgotest.Size(1180, 780), nsgotest.Environment(core.Environment{Locale: "en", OS: core.OSMacOS, Idiom: core.IdiomDesktop}))

	a.post(func() {
		reviewTrack(a)
		a.heartbeat()
	})
	ui.Settle()
	// Advance past several 200ms heartbeat ticks so any effect-owned 40ms timer bug would have triggered.
	ui.Advance(600 * time.Millisecond)

	ui.Find("id:np-fog")
	beforeY := ui.Find("id:lyrics-list").Offset.Y

	// Advance playback position across lines without bumping playRev (live playback path).
	// Wait only 80ms (< 200ms heartbeat) to prove the 40ms lyrics frame loop is still running.
	a.post(func() {
		a.pl.Hold(74 * time.Second)
	})
	ui.Advance(80 * time.Millisecond)
	ui.Settle()
	afterY := ui.Find("id:lyrics-list").Offset.Y
	if afterY <= beforeY {
		t.Fatalf("lyrics did not auto-scroll on 40ms word-frame loop with Fog enabled: before=%v after=%v", beforeY, afterY)
	}
}

func TestStageTickerSurvivesPlayRevWithFogEnabled(t *testing.T) {
	t.Setenv("WOOD_WIND_MUSIC_DATA_DIR", t.TempDir())
	var a *app
	ui := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a = newApp(cx)
		a.set.Language = i18n.English
		a.applyLanguage()
		a.set.Fog = true
		return a.view(cx)
	}, nsgotest.Size(1180, 780), nsgotest.Environment(core.Environment{Locale: "en", OS: core.OSMacOS, Idiom: core.IdiomDesktop}))

	a.post(func() {
		reviewTrack(a)
		a.npPane.Set(3)
		a.heartbeat()
	})
	ui.Settle()

	// Bump playRev to rebuild nowPlayingBody/stageView while playing.
	a.post(func() {
		a.playRev.Update(func(n int) int { return n + 1 })
	})
	ui.Settle()

	before := a.stageTick.GetUntracked()
	ui.Advance(200 * time.Millisecond)
	after := a.stageTick.GetUntracked()
	if after <= before {
		t.Fatalf("stage lyrics ticker froze after playRev bump: before=%d after=%d", before, after)
	}
}
