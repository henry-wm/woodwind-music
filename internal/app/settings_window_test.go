package app

import (
	"testing"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"
	"github.com/nsgo-dev/nsgo/nsgotest"
)

// Unlike a settings view mounted in a single test surface, this exercises
// the desktop window lifetime through the real sidebar button and redraws.
func TestSettingsWindowSurvivesSidebarUpdates(t *testing.T) {
	t.Setenv("WOOD_WIND_MUSIC_DATA_DIR", t.TempDir())
	var a *app
	ui := nsgotest.StartApp(t, func(cx *core.Scope) {
		desktop.SetShutdownMode(cx, desktop.ShutdownOnExplicit)
		a = newApp(cx)
		cx.SetMainView(a.view)
	}, nsgotest.Environment(core.Environment{OS: core.OSMacOS}), nsgotest.Size(1180, 780))
	pressSettings := func() {
		t.Helper()
		ui.Press("id:open-settings")
		if a.settingsWin == nil || a.settingsWin.Closed() || !a.settingsWin.Visible() {
			t.Fatal("clicking Settings did not leave a visible settings window")
		}
	}
	pressSettings()
	first := a.settingsWin
	a.post(a.bump)
	ui.Settle()
	if first.Closed() {
		t.Fatal("a sidebar update closed the settings window")
	}
	first.Hide()
	pressSettings()
	if a.settingsWin != first || len(ui.Surfaces()) != 2 {
		t.Fatal("opening Settings again did not reuse its window")
	}
	// Closing with the settings window's shortcut must allow opening a fresh
	// window from the button, without retaining old surfaces.
	ui.Surface("index:1").Key("mod+w")
	ui.Settle()
	if !first.Closed() {
		t.Fatal("the settings window did not close")
	}
	pressSettings()
	if a.settingsWin == first || len(ui.Surfaces()) != 2 {
		t.Fatal("Settings did not reopen as a new window")
	}
}
