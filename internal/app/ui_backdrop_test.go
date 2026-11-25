package app

import (
	"math"
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"github.com/HengWh/woodwind-music/internal/i18n"
	"github.com/HengWh/woodwind-music/internal/music"
)

func TestFogShaderCompilesAndHasUniforms(t *testing.T) {
	prog, err := core.CompileWGSL(fogWGSLSource)
	if err != nil {
		t.Fatalf("fog WGSL failed to compile: %v", err)
	}
	for _, name := range []string{"prevDeep", "prevFog", "deep", "fog", "blend"} {
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
