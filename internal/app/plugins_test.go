package app

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/HengWh/woodwind-music/internal/music"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/HengWh/woodwind-music/internal/plugin"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
)

func demoPluginPath() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "examples", "demo-plugin")
}

func installDemo(t *testing.T, dir string) {
	t.Helper()
	p, err := plugin.Open(demoPluginPath(), filepath.Join(dir, "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Install(filepath.Join(dir, "plugins")); err != nil {
		t.Fatal(err)
	}
}
func TestPluginSourcesOnDesktopAndPhone(t *testing.T) {
	for _, phone := range []bool{false, true} {
		name := "desktop"
		if phone {
			name = "phone"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("WOOD_WIND_MUSIC_DATA_DIR", dir)
			installDemo(t, dir)
			var a *app
			width, height := core.Points(1180), core.Points(780)
			environment := core.Environment{OS: core.OSMacOS}
			if phone {
				width, height = 390, 844
				environment = core.Environment{OS: core.OSAndroid, SafeArea: core.Edges{Top: 24, Bottom: 24}}
			}
			ui := nsgotest.Start(t, func(cx *core.Scope) core.Node { a = newApp(cx); return a.view(cx) }, nsgotest.Size(width, height), nsgotest.Environment(environment))
			t.Cleanup(a.closePlugins)
			ui.WaitUntil(time.Second, func() bool { return !a.homeBusy })
			if phone {
				ui.Press("id:phone-tab-library")
				ui.Press("id:phone-sources")
				ui.Press("id:source-demo")
			} else {
				ui.Press("id:source-switcher")
				ui.Press("text:Demo")
			}
			ui.WaitUntil(2*time.Second, func() bool { return a.source().Info().ID == "demo" && !a.homeBusy && a.home != nil })
			if len(a.home.Sections) == 0 || a.home.Sections[0].Tracks[0].Source != "demo" {
				t.Fatal("plugin home not stamped with source")
			}
			if phone {
				ui.Press("id:phone-tab-library")
			}
			ui.Find("id:source-nav-1")
			a.post(func() { a.search("song") })
			ui.WaitUntil(2*time.Second, func() bool { d := a.searches[a.route.GetUntracked()]; return d != nil && d.loaded })
			d := a.searches[a.route.GetUntracked()]
			if len(d.res.Tracks) != 3 {
				t.Fatal("search did not use plugin")
			}
			tr := d.res.Tracks[0]
			a.post(func() { a.toggleTrackLike(tr) })
			ui.WaitUntil(2*time.Second, func() bool { return a.likedSet[tr.ID] && !a.homeBusy })
			for _, r := range []route{{page: pageShows}, {page: pageStations}, {page: pageComments, id: tr.ID}} {
				a.post(func() { a.open(r) })
				ui.WaitUntil(2*time.Second, func() bool { d := a.extra[a.route.GetUntracked()]; return d != nil && d.loaded })
				if e := a.extra[a.route.GetUntracked()].err; e != "" {
					t.Fatal(e)
				}
			}
			a.post(func() { a.switchSource("local") })
			ui.WaitUntil(2*time.Second, func() bool { return !a.homeBusy && a.source() == a.local })
			if len(a.liked) > 0 {
				t.Fatal("plugin favorites leaked into local source")
			}
			if a.sourceByID("missing") != nil {
				t.Fatal("missing plugin resolved to local source")
			}
			if a.sourceByID("demo") == nil {
				t.Fatal("switching source unloaded player source")
			}
		})
	}
}

func TestPhonePluginInstallReviewAndRemove(t *testing.T) {
	ui, a := startPhone(t)
	a.post(func() {
		a.preparePlugin(func() (*plugin.Pending, error) {
			return plugin.Open(demoPluginPath(), a.pluginDir())
		})
	})
	ui.WaitUntil(2*time.Second, func() bool { return a.installer.pending != nil && !a.installer.busy })
	if len(a.plugins) != 0 {
		t.Fatal("installed before review")
	}
	ui.Press("id:plugin-confirm")
	ui.WaitUntil(2*time.Second, func() bool { return len(a.plugins) == 1 && !a.installer.open })
	ui.Press("id:phone-tab-library")
	ui.Press("id:open-settings")
	ui.Press("id:phone-settings-plugins")
	ui.Find("id:plugin-setting-demo/greeting")
	ui.Type("id:plugin-setting-demo/greeting", "Hello wood wind")
	ui.Key("enter")
	ui.WaitUntil(time.Second, func() bool { return a.plugins[0].Setting("greeting") == "Hello wood wind" })
	a.post(func() { a.installer.open = true; a.installer.remove = "demo"; a.bump() })
	ui.Settle()
	ui.Press("id:plugin-remove-confirm")
	ui.WaitUntil(2*time.Second, func() bool { return len(a.plugins) == 0 && !a.installer.busy })
	if _, err := os.Stat(filepath.Join(a.pluginDataDir(), "demo", "settings.json")); err != nil {
		t.Fatal("uninstall discarded settings", err)
	}
}

func TestPhonePluginCatalog(t *testing.T) {
	ui, _ := startPhone(t)
	ui.Press("id:phone-tab-library")
	ui.Press("id:open-settings")
	ui.Press("id:phone-settings-plugins")
	ui.Find("id:plugin-install")
	for _, id := range []string{"kugou-lite", "bilibili"} {
		for _, action := range []string{"repo", "license", "install"} {
			testID := "plugin-catalog-" + action + "-" + id
			if height := ui.Find("id:" + testID).Frame.Size.Height; height < 48 {
				t.Errorf("%s has a %g-point touch target, want at least 48", testID, height)
			}
		}
	}
}

func TestPluginOldSearchCannotReplaceNewSource(t *testing.T) {
	ui, a := startPhone(t)
	// A real async JS plugin deliberately finishes after switching sources.
	dir := filepath.Join(a.pluginDir(), "slow")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"id":"slow"}`), 0600)
	os.WriteFile(filepath.Join(dir, "index.js"), []byte(`let finished=false;export function account(){return {id:finished?"finished":"waiting"}};export async function search() { await new Promise(r=>setTimeout(r,80)); finished=true; return {tracks:[{id:"same",name:"Old Source"}]}; }`), 0600)
	p, err := plugin.Load(dir, a.pluginDataDir())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	a.post(func() {
		a.plugins = append(a.plugins, p)
		a.registerPlugin(p)
		a.switchSource("slow")
		a.search("same")
		a.switchSource("local")
		a.search("same")
	})
	ui.WaitUntil(2*time.Second, func() bool { d := a.searches[a.route.GetUntracked()]; return d != nil && d.loaded })
	ui.WaitUntil(time.Second, func() bool { return !a.homeBusy })
	ui.WaitUntil(time.Second, func() bool { account, err := p.Account(); return err == nil && account.ID == "finished" })
	if a.source() != a.local || len(a.searches[a.route.GetUntracked()].res.Tracks) != 0 {
		t.Fatal("old plugin result replaced local search")
	}
}

func TestPhonePluginPageActionsHaveTouchTargets(t *testing.T) {
	ui, a := startPhone(t)
	p, err := plugin.Load(demoPluginPath(), a.pluginDataDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.closePlugins)
	a.post(func() {
		a.plugins = append(a.plugins, p)
		a.registerPlugin(p)
		a.switchSource("demo")
	})
	for _, test := range []struct {
		r   route
		ids []string
	}{
		{route{page: pageComments, id: "t1"}, []string{"comment-like-c1", "comment-send"}},
		{route{page: pageShow, id: "s1"}, []string{"show-subscribe"}},
	} {
		a.post(func() { a.open(test.r) })
		ui.WaitUntil(2*time.Second, func() bool {
			d := a.extra[a.route.GetUntracked()]
			return d != nil && d.loaded
		})
		for _, id := range test.ids {
			if height := ui.Find("id:" + id).Frame.Size.Height; height < 48 {
				t.Errorf("%s has a %g-point touch target, want at least 48", id, height)
			}
		}
	}
}

func TestAndroidPluginAuthenticatedStream(t *testing.T) {
	ui, a := startPhone(t)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer song-token" {
			t.Error("missing stream token")
		}
		// One upstream transfer serves all native range requests from disk.
		if r.Header.Get("Range") != "" {
			t.Error("native seek caused another upstream range")
		}
		audio, err := os.ReadFile("../player/testdata/seek.mp3")
		if err != nil {
			t.Error(err)
			return
		}
		w.Write(audio)
	}))
	defer origin.Close()
	dir := filepath.Join(a.pluginDir(), "private")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"id":"private"}`), 0600)
	script := fmt.Sprintf(`export function stream(){return {url:%q,type:"mp3",headers:{Authorization:"Bearer song-token"}}}`, origin.URL)
	os.WriteFile(filepath.Join(dir, "index.js"), []byte(script), 0600)
	p, err := plugin.Load(dir, a.pluginDataDir())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	a.post(func() {
		a.plugins = append(a.plugins, p)
		a.registerPlugin(p)
		a.playTracks([]music.Track{{ID: "song", Source: "private", Name: "Private Song"}}, 0)
	})
	ui.WaitUntil(time.Second, func() bool { return a.video.request.GetUntracked() != nil })
	video := ui.FindMounted("id:android-audio")
	if strings.Contains(video.Video.Source, "song-token") || !strings.HasPrefix(video.Video.Source, "http://127.0.0.1:") {
		t.Fatal("native player received unadapted stream", video.Video.Source)
	}
	request, _ := http.NewRequest("GET", video.Video.Source, nil)
	request.Header.Set("Range", "bytes=8-15")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 206 {
		t.Fatal(response.Status)
	}
	ui.Report(nsgotest.Event{Kind: nsgotest.EventLoad, ID: video.ID, Duration: time.Minute})
	ui.WaitUntil(time.Second, a.pl.Ready)
	a.post(func() { a.openSettings() })
	ui.Settle()
	if current := ui.FindMounted("id:android-audio"); current.ID != video.ID {
		t.Fatal("settings replaced plugin audio node")
	}
	a.post(a.pl.Clear)
	ui.Settle()
	if response, err := http.Get(video.Video.Source); err == nil {
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatal("clearing playback left the saved transfer accessible")
		}
	}
}

func TestPluginShots(t *testing.T) {
	if os.Getenv("WOOD_WIND_PLUGIN_SHOTS") == "" {
		t.Skip("set WOOD_WIND_PLUGIN_SHOTS=1 for visual review")
	}
	dir := t.TempDir()
	t.Setenv("WOOD_WIND_MUSIC_DATA_DIR", dir)
	installDemo(t, dir)
	var a *app
	ui := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a = newApp(cx)
		a.set.Language = "zh-CN"
		a.applyLanguage()
		return a.view(cx)
	}, nsgotest.Size(1180, 780))
	defer a.closePlugins()
	a.post(func() { a.switchSource("demo") })
	ui.WaitUntil(2*time.Second, func() bool { return !a.homeBusy && a.home != nil })
	ui.Screenshot("plugins-desktop")
	ui.Resize(390, 844)
	ui.Press("id:phone-tab-library")
	ui.Screenshot("plugins-phone-library")
	a.post(func() { a.settingsPage = "plugins"; a.settingsInMain = true; a.bump() })
	ui.Settle()
	ui.Screenshot("plugins-phone-settings")
	ui.Resize(320, 568)
	ui.Screenshot("plugins-phone-settings-small")
	category := reviewBounds(ui.Find("id:phone-settings-plugins"))
	content := reviewBounds(ui.Find("id:settings-content"))
	if category.Origin.Y+category.Size.Height > content.Origin.Y+1 {
		t.Fatalf("categories overlap content: %+v / %+v", category, content)
	}
}

func TestDesktopPluginPlaybackErrorIsVisible(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WOOD_WIND_MUSIC_DATA_DIR", dir)
	source := filepath.Join(dir, "plugins", "restricted")
	os.MkdirAll(source, 0700)
	os.WriteFile(filepath.Join(source, "plugin.json"), []byte(`{"id":"restricted"}`), 0600)
	os.WriteFile(filepath.Join(source, "index.js"), []byte(`export function stream(){const e=new Error("This account cannot play the full track");e.code="unavailable";throw e;}`), 0600)
	var a *app
	ui := nsgotest.Start(t, func(cx *core.Scope) core.Node { a = newApp(cx); return a.view(cx) }, nsgotest.Size(1180, 780))
	defer a.closePlugins()
	a.post(func() { a.playTracks([]music.Track{{ID: "sample", Source: "restricted", Name: "Restricted track"}}, 0) })
	ui.WaitUntil(time.Second, func() bool { return a.pl.Err() != "" })
	if ui.Find("id:bar-error").Text != "This account cannot play the full track" {
		t.Fatal("plugin's access explanation was hidden")
	}
	ui.Press("id:bar-open")
	if ui.Find("id:np-status").Text != "This account cannot play the full track" {
		t.Fatal("Now Playing hid the playback error")
	}
}

// A custom menu trigger must still have a popup surface. Check the visible
// geometry as well as selection, since a transparent menu remains clickable.
func TestSourceSwitcherMenuSurface(t *testing.T) {
	for _, platform := range []struct {
		name string
		env  core.Environment
	}{
		{"macos", core.Environment{OS: core.OSMacOS, TitleBar: core.Edges{Top: 40, Left: 78}}},
		{"windows", core.Environment{OS: core.OSWindows, TitleBar: core.Edges{Top: 40, Right: 100}}},
	} {
		for _, appearance := range []string{"light", "dark"} {
			t.Run(platform.name+"/"+appearance, func(t *testing.T) {
				dir := t.TempDir()
				t.Setenv("WOOD_WIND_MUSIC_DATA_DIR", dir)
				installDemo(t, dir)
				var a *app
				ui := nsgotest.Start(t, func(cx *core.Scope) core.Node {
					a = newApp(cx)
					a.set.Appearance = appearance
					if appearance == "dark" {
						cx.SetAppearance(core.AppearanceDark)
					} else {
						cx.SetAppearance(core.AppearanceLight)
					}
					return a.view(cx)
				}, nsgotest.Size(880, 580), nsgotest.Environment(platform.env))
				defer a.closePlugins()
				ui.WaitUntil(time.Second, func() bool { return !a.homeBusy })
				ui.Press("id:source-switcher")
				menu := ui.Find("role:menu")
				popup, trigger := reviewBounds(menu), reviewBounds(ui.Find("id:source-switcher"))
				// The card used to push search down; the Mac selector must instead
				// fit beside the window controls and leave the search row clear.
				search := reviewBounds(ui.Find("id:search-box"))
				if trigger.Origin.Y+trigger.Size.Height > search.Origin.Y || trigger.Origin.X+trigger.Size.Width > sidebarWidth-10 {
					t.Fatalf("source selector overlaps search or leaves sidebar: trigger=%+v search=%+v", trigger, search)
				}
				if nsgotest.Target() == "" && platform.env.TitleBar.Left > 0 && (trigger.Origin.X < platform.env.TitleBar.Left+12 || trigger.Origin.Y+trigger.Size.Height > platform.env.TitleBar.Top) {
					t.Fatalf("source selector does not fit beside window controls: %+v", trigger)
				}
				if menu.Style.Background&0xff != 0xff || menu.Style.BorderWidth <= 0 {
					t.Fatal("source menu has no opaque popup surface")
				}
				if popup.Size.Width < trigger.Size.Width || popup.Origin.Y < trigger.Origin.Y+trigger.Size.Height || popup.Origin.X+popup.Size.Width > 880 {
					t.Fatalf("source menu is misplaced: popup=%+v trigger=%+v", popup, trigger)
				}
				for _, id := range []string{"source-option-local", "source-option-demo"} {
					option := reviewBounds(ui.Find("id:" + id))
					if option.Origin.X < popup.Origin.X || option.Origin.Y < popup.Origin.Y || option.Origin.X+option.Size.Width > popup.Origin.X+popup.Size.Width || option.Origin.Y+option.Size.Height > popup.Origin.Y+popup.Size.Height {
						t.Fatalf("%s escapes its popup: option=%+v popup=%+v", id, option, popup)
					}
				}
				if !ui.Find("id:source-option-local").Props.Checked {
					t.Fatal("current source is not marked")
				}
				if os.Getenv("WOOD_WIND_PLUGIN_SHOTS") != "" {
					ui.Screenshot("source-menu-" + platform.name + "-" + appearance)
				}
				ui.Key("escape")
				ui.WaitForGone("role:menu", time.Second)
				ui.Press("id:source-switcher")
				ui.Press("id:source-option-demo")
				ui.WaitUntil(time.Second, func() bool { return a.source().Info().ID == "demo" && !a.homeBusy })
				ui.WaitForGone("role:menu", time.Second)
				ui.Press("id:source-switcher")
				if !ui.Find("id:source-option-demo").Props.Checked {
					t.Fatal("selected plugin is not marked after switching")
				}
				ui.Key("escape")
			})
		}
	}
}

func testPluginZip(t *testing.T, manifest, script string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"plugin.json": manifest,
		"index.js":    script,
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPluginMyGoSettingsUIAndDisableSwitch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WOOD_WIND_MUSIC_DATA_DIR", dir)
	plugDir := filepath.Join(dir, "plugins", "typed")
	if err := os.MkdirAll(plugDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"id": "typed",
		"name": "Typed Source",
		"version": "1.0.0",
		"permissions": {"network": ["api.example.com"]},
		"settings": [
			{"key": "server", "label": "Server URL"},
			{"key": "secret", "label": "API Token", "type": "password"},
			{"key": "history", "label": "Save History", "type": "boolean"},
			{"key": "mode", "label": "Auth Mode", "type": "select", "options": [
				{"value": "token", "label": "Token"},
				{"value": "legacy", "label": "Legacy"}
			]}
		]
	}`
	if err := os.WriteFile(filepath.Join(plugDir, "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugDir, "index.js"), []byte(`export function suggest() { return ["ok"]; }`), 0o600); err != nil {
		t.Fatal(err)
	}

	var a *app
	ui := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a = newApp(cx)
		a.settingsPage = "plugins"
		return a.settingsView(cx)
	}, nsgotest.Size(960, 680), nsgotest.Environment(core.Environment{OS: core.OSMacOS}))
	t.Cleanup(a.closePlugins)
	ui.Settle()

	// Text setting saves automatically on Enter without a Save button.
	ui.Type("id:plugin-setting-typed/server", "https://music.local")
	ui.Key("enter")
	ui.WaitUntil(time.Second, func() bool { return a.plugins[0].Setting("server") == "https://music.local" })

	// Password setting is masked and does not save until Save changes is pressed.
	ui.Type("id:plugin-setting-typed/secret", "top-secret")
	ui.Settle()
	if got := a.plugins[0].Setting("secret"); got != "" {
		t.Fatalf("password saved before Save changes: %q", got)
	}
	ui.Press("id:plugin-save-typed/secret")
	ui.WaitUntil(time.Second, func() bool { return a.plugins[0].Setting("secret") == "top-secret" })

	// Boolean switch saves automatically.
	ui.Press("id:plugin-setting-typed/history")
	ui.WaitUntil(time.Second, func() bool { return a.plugins[0].Setting("history") == "true" })

	// Select saves automatically (index 0 is unlisted empty current value, index 2 is Legacy).
	ui.Press("id:plugin-setting-typed/mode")
	ui.Press("id:plugin-setting-typed/mode-2")
	ui.WaitUntil(time.Second, func() bool { return a.plugins[0].Setting("mode") == "legacy" })

	// Disabling collapses settings and removes plugin from enabled sources.
	ui.Press("id:plugin-enabled-typed")
	ui.WaitUntil(time.Second, func() bool { return !a.pluginEnabled("typed") })
	if ui.Exists("id:plugin-setting-typed/server") {
		t.Fatal("disabled plugin still rendered its settings")
	}
	if len(a.enabledPlugins()) != 0 {
		t.Fatal("disabled plugin remained in enabledPlugins")
	}

	// Re-enabling restores settings.
	ui.Press("id:plugin-enabled-typed")
	ui.WaitUntil(time.Second, func() bool { return a.pluginEnabled("typed") })
	ui.Find("id:plugin-setting-typed/server")
}

func TestPluginFeedSubscriptionAndInlineNoPopupUpdates(t *testing.T) {
	v1Zip := testPluginZip(t,
		`{"id":"cloud","name":"Cloud Source","version":"1.0.0","permissions":{"network":["a.example.com"]}}`,
		`export function suggest() { return ["v1"]; }`,
	)
	v2Zip := testPluginZip(t,
		`{"id":"cloud","name":"Cloud Source","version":"1.1.0","permissions":{"network":["a.example.com"]}}`,
		`export function suggest() { return ["v2"]; }`,
	)
	v3Zip := testPluginZip(t,
		`{"id":"cloud","name":"Cloud Source","version":"1.2.0","permissions":{"network":["a.example.com","b.example.com"]}}`,
		`export function suggest() { return ["v3"]; }`,
	)

	currentVer := "1.0.0"
	currentZip := v1Zip
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feed.json":
			sum := sha256.Sum256(currentZip)
			pure := true
			json.NewEncoder(w).Encode(plugin.Feed{
				Name: "Community Feed",
				Plugins: []plugin.FeedEntry{{
					ID:          "cloud",
					Name:        "Cloud Source",
					Version:     currentVer,
					DownloadURL: srv.URL + "/cloud.zip",
					SHA256:      hex.EncodeToString(sum[:]),
					PureJS:      &pure,
				}},
			})
		case "/cloud.zip":
			w.Write(currentZip)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	t.Setenv("WOOD_WIND_MUSIC_DATA_DIR", dir)
	var a *app
	ui := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a = newApp(cx)
		a.settingsPage = "plugins"
		return a.settingsView(cx)
	}, nsgotest.Size(960, 720), nsgotest.Environment(core.Environment{OS: core.OSMacOS}))
	t.Cleanup(a.closePlugins)
	ui.Settle()

	// Subscribe to JSON feed.
	ui.Type("id:plugin-feed-url", srv.URL+"/feed.json")
	ui.Press("id:plugin-feed-add")
	ui.WaitUntil(2*time.Second, func() bool { return len(a.set.PluginFeeds) == 1 })
	ui.Find("id:plugin-feed-install-cloud")

	// Install from subscribed feed and confirm permissions.
	ui.Press("id:plugin-feed-install-cloud")
	ui.WaitUntil(2*time.Second, func() bool { return a.installer.pending != nil && !a.installer.busy })
	ui.Press("id:plugin-confirm")
	ui.WaitUntil(2*time.Second, func() bool { return len(a.plugins) == 1 && !a.installer.open })

	// Feed publishes v1.1.0 with unchanged permissions -> quiet check shows badge + inline button, NO popup.
	currentVer = "1.1.0"
	currentZip = v2Zip
	ui.Press("id:plugin-check-all")
	ui.WaitUntil(2*time.Second, func() bool { return !a.installer.checkingAll && a.pluginUpdateCount() == 1 })
	if a.installer.open {
		t.Fatal("update check opened a popup dialog")
	}
	if badge := ui.Find("id:settings-badge-plugins").Text; badge != "1" {
		t.Fatalf("settings badge = %q, want 1", badge)
	}
	ui.Press("id:plugin-update-cloud")
	ui.WaitUntil(2*time.Second, func() bool { return a.plugins[0].Info().Version == "1.1.0" && a.pluginUpdateCount() == 0 })
	if a.installer.open {
		t.Fatal("same-permission update opened a popup dialog")
	}

	// Feed publishes v1.2.0 asking for a new network host -> inline confirmation inside card, still NO popup.
	currentVer = "1.2.0"
	currentZip = v3Zip
	ui.Press("id:plugin-check-all")
	ui.WaitUntil(2*time.Second, func() bool { return !a.installer.checkingAll && a.pluginUpdateCount() == 1 })
	ui.Press("id:plugin-update-cloud")
	ui.WaitUntil(2*time.Second, func() bool { return a.installer.pendingUpdates["cloud"] != nil })
	if a.installer.open {
		t.Fatal("permission-expanding update opened a popup instead of inline confirmation")
	}
	ui.Find("id:plugin-update-prompt-cloud")
	ui.Press("id:plugin-update-confirm-cloud")
	ui.WaitUntil(2*time.Second, func() bool { return a.plugins[0].Info().Version == "1.2.0" && a.pluginUpdateCount() == 0 })
}
