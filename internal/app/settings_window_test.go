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
	ui.Post(first.Hide)
	ui.Settle()
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

type testWindowsTray struct{ registered bool }

func (*testWindowsTray) Remove()            {}
func (t *testWindowsTray) Registered() bool { return t.registered }

func TestWindowsMainWindowCloseToTray(t *testing.T) {
	t.Setenv("WOOD_WIND_MUSIC_DATA_DIR", t.TempDir())
	var a *app
	var w *desktop.Window
	tray := &testWindowsTray{registered: true}
	ui := nsgotest.StartApp(t, func(cx *core.Scope) {
		desktop.SetShutdownMode(cx, desktop.ShutdownOnExplicit)
		a = newApp(cx)
		a.tray = tray
		cx.SetMainView(a.view)
		w = desktop.MainWindow(cx)
		a.bindMainWindow(w)
	}, nsgotest.Environment(core.Environment{OS: core.OSWindows}), nsgotest.Size(1180, 780))
	if ui.CloseWindow() || w.Visible() || ui.Quits != 0 {
		t.Fatal("close destroyed or retained a visible window with a registered tray")
	}
	ui.Post(a.showMain)
	ui.Settle()
	if !w.Visible() || w.Closed() || desktop.MainWindow(a.cx) != w {
		t.Fatal("tray restore did not reuse the preserved main window")
	}
	tray.registered = false
	ui.CloseWindow()
	if ui.Quits != 1 {
		t.Fatal("close without a registered tray did not ask the app to quit")
	}
}
