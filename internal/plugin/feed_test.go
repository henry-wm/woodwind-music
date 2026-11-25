package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/HengWh/woodwind-music/internal/i18n"
)

func sha256HexOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestValidateFeedURL(t *testing.T) {
	for _, good := range []string{
		"https://example.com/plugins.json",
		"http://127.0.0.1:8080/plugins.json",
		"http://localhost:3000/feed.json",
	} {
		if _, err := ValidateFeedURL(good); err != nil {
			t.Errorf("ValidateFeedURL(%q) failed: %v", good, err)
		}
	}
	for _, bad := range []string{
		"",
		"http://example.com/plugins.json",
		"file:///tmp/plugins.json",
		"not a url",
	} {
		if _, err := ValidateFeedURL(bad); err == nil {
			t.Errorf("ValidateFeedURL(%q) succeeded, want error", bad)
		}
	}
}

func TestFeedDirectZipInstallAndUpdate(t *testing.T) {
	v1Zip := makeZip(t,
		zipped{name: "plugin.json", body: `{"id":"feedplug","name":"Feed Plugin","version":"1.0.0","permissions":{"network":["api.example.com"]}}`},
		zipped{name: "index.js", body: `export function suggest() { return ["v1"]; }`},
	)
	v2Zip := makeZip(t,
		zipped{name: "plugin.json", body: `{"id":"feedplug","name":"Feed Plugin","version":"1.1.0","permissions":{"network":["api.example.com"]}}`},
		zipped{name: "index.js", body: `export function suggest() { return ["v2"]; }`},
	)
	currentZip := v1Zip
	currentVer := "1.0.0"

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feed.json":
			pure := true
			json.NewEncoder(w).Encode(Feed{
				Name: "Test Feed",
				Plugins: []FeedEntry{{
					ID:          "feedplug",
					Name:        "Feed Plugin",
					Version:     currentVer,
					DownloadURL: srv.URL + "/plugin.zip",
					SHA256:      sha256HexOf(currentZip),
					PureJS:      &pure,
				}},
			})
		case "/plugin.zip":
			w.Write(currentZip)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	feed, err := FetchFeed(srv.URL + "/feed.json")
	if err != nil {
		t.Fatal(err)
	}
	if feed.Name != "Test Feed" || len(feed.Plugins) != 1 || !feed.Plugins[0].IsPureJS() || feed.Plugins[0].DesktopOnly() {
		t.Fatalf("unexpected feed: %+v", feed)
	}

	dir, data := t.TempDir(), t.TempDir()
	pending, err := FetchFeedEntry(feed.URL, feed.Plugins[0], dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := pending.Install(dir); err != nil {
		t.Fatal(err)
	}
	loaded := Discover(dir, data)
	if len(loaded) != 1 {
		t.Fatalf("got %d plugins", len(loaded))
	}
	defer loaded[0].Close()
	o := loaded[0].Origin()
	if o == nil || o.FeedURL != feed.URL || o.Version != "1.0.0" || o.DownloadURL != srv.URL+"/plugin.zip" {
		t.Fatalf("unexpected origin: %+v", o)
	}

	// Bad checksum must be rejected before install.
	badEntry := feed.Plugins[0]
	badEntry.SHA256 = stringsRepeat("0", 64)
	if p, err := FetchFeedEntry(feed.URL, badEntry, dir); err == nil {
		p.Discard()
		t.Fatal("accepted mismatched sha256")
	}

	// Update from the same feed succeeds and preserves data.
	currentZip = v2Zip
	currentVer = "1.1.0"
	feed2, err := FetchFeed(srv.URL + "/feed.json")
	if err != nil {
		t.Fatal(err)
	}
	pending2, err := FetchFeedEntry(feed2.URL, feed2.Plugins[0], dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := pending2.Install(dir); err != nil {
		t.Fatal(err)
	}
	reloaded, err := loaded[0].Reloaded(data)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	if s, err := reloaded.Suggest(""); err != nil || len(s) != 1 || s[0] != "v2" {
		t.Fatalf("reloaded suggest: %v, %v", s, err)
	}
	if reloaded.Origin().Version != "1.1.0" {
		t.Fatalf("updated origin: %+v", reloaded.Origin())
	}
}

func TestSettingOptionTranslations(t *testing.T) {
	defer i18n.SetLanguage(i18n.Language())
	dir := t.TempDir()
	manifest := `{
		"id": "opts",
		"name": "Options",
		"settings": [{
			"key": "mode",
			"label": "Mode",
			"type": "select",
			"options": [{"value": "auto", "label": "Automatic"}]
		}]
	}`
	p, err := Open(makePluginFolder(t, map[string]string{
		"plugin.json":        manifest,
		"index.js":           `export function suggest() { return []; }`,
		"locales/zh-CN.json": `{"Mode":"模式","Automatic":"自动"}`,
	}), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Install(dir); err != nil {
		t.Fatal(err)
	}
	loaded := Discover(dir, t.TempDir())
	if len(loaded) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(loaded))
	}
	defer loaded[0].Close()

	i18n.SetLanguage(i18n.Chinese)
	info := loaded[0].Info()
	if info.Settings[0].Label != "模式" {
		t.Fatalf("label = %q", info.Settings[0].Label)
	}
	if opt := info.Settings[0].Options[0]; opt.Value != "auto" || opt.Label != "自动" {
		t.Fatalf("option = %+v, value must stay untranslated and label translated", opt)
	}
}

func stringsRepeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for range n {
		out = append(out, s...)
	}
	return string(out)
}

func makePluginFolder(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
