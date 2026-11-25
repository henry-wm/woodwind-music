package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HengWh/woodwind-music/internal/i18n"
	"github.com/HengWh/woodwind-music/internal/music"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
)

func TestMusicVideoFromSongList(t *testing.T) {
	for _, platform := range []core.OS{core.OSMacOS, core.OSWindows, core.OSAndroid} {
		t.Run(string(platform), func(t *testing.T) {
			compact := platform == core.OSAndroid
			dir := t.TempDir()
			t.Setenv("WOOD_WIND_MUSIC_DATA_DIR", dir)
			media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture" {
					t.Error("missing video request headers")
				}
				w.Header().Set("Content-Type", "video/mp4")
				http.ServeContent(w, r, "test.mp4", time.Time{}, strings.NewReader("test video"))
			}))
			defer media.Close()
			pluginDir := filepath.Join(dir, "plugins", "mv-test")
			if err := os.MkdirAll(pluginDir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(`{"id":"mv-test"}`), 0600); err != nil {
				t.Fatal(err)
			}
			script := fmt.Sprintf(`
export function search() {
	return {tracks: [{id:"has-mv",name:"With MV"}, {id:"no-mv",name:"Without video"}, {id:"broken",name:"Broken video"}]};
}
export function trackMVs({ids}) {
	return Object.fromEntries(ids.filter(id => id !== "no-mv").map(id => [id, "video-" + id]));
}
export function mvStream({id}) {
	if (id !== "video-has-mv") throw new Error("no stream");
	return {url:%q, type:"mp4", headers:{Authorization:"Bearer fixture"}};
}`, media.URL)
			if err := os.WriteFile(filepath.Join(pluginDir, "index.js"), []byte(script), 0600); err != nil {
				t.Fatal(err)
			}
			folder := t.TempDir()
			writeToneWAV(t, filepath.Join(folder, "Music.wav"))
			host := core.NewHost()
			if err := host.SetParams(map[string]any{"music.android": compact}); err != nil {
				t.Fatal(err)
			}
			host.Handle("playback.update", func(call *core.HostCall) { call.Resolve(nil) })
			size := nsgotest.Size(1180, 780)
			env := core.Environment{OS: platform}
			if compact {
				size = nsgotest.Size(390, 844)
			} else if platform == core.OSWindows {
				env.TitleBar = core.Edges{Top: 40, Right: 100}
			}
			var a *app
			ui := nsgotest.Start(t, func(cx *core.Scope) core.Node {
				a = newApp(cx)
				a.set.Language = i18n.English
				a.applyLanguage()
				return a.view(cx)
			}, size, nsgotest.Environment(env), nsgotest.Host(host))
			a.local.AddFolders(folder)
			ui.WaitUntil(2*time.Second, func() bool { return a.local.TrackCount() == 1 && !a.local.Scanning() })
			audio, err := a.local.Search("", music.SearchTracks, 0)
			if err != nil || len(audio.Tracks) != 1 {
				t.Fatalf("test audio search: %+v, %v", audio, err)
			}
			a.post(func() {
				a.playTracks(audio.Tracks, 0)
				a.activateSource("mv-test")
				a.search("test")
			})
			if compact {
				ui.WaitUntil(time.Second, func() bool { return a.video.request.GetUntracked() != nil })
				nativeAudio := ui.FindMounted("id:android-audio")
				ui.Report(nsgotest.Event{Kind: nsgotest.EventLoad, ID: nativeAudio.ID, Duration: time.Minute})
			}
			ui.WaitUntil(time.Second, a.pl.Ready)
			a.post(func() { a.pl.Seek(350 * time.Millisecond) })
			ui.Settle()
			position := a.pl.Position()
			if position != 350*time.Millisecond {
				t.Fatalf("test setup: music position = %v", position)
			}
			ui.WaitUntil(2*time.Second, func() bool { return ui.Exists("label:Play MV for With MV") })
			if ui.Exists("label:Play MV for Without video") {
				t.Fatal("MV button for a song with no MV")
			}
			mvBtnView := ui.Find("label:Play MV for With MV")
			if len(mvBtnView.Children) == 0 {
				t.Fatal("MV button should render a play icon")
			}
			var hasText func(*nsgotest.View) bool
			hasText = func(n *nsgotest.View) bool {
				if n.Text != "" {
					return true
				}
				for _, ch := range n.Children {
					if hasText(ch) {
						return true
					}
				}
				return false
			}
			if hasText(mvBtnView) {
				t.Fatal("MV button should render an icon instead of MV text")
			}
			if !compact {
				withMVLike := ui.Find("id:track-like-cell-has-mv").Bounds()
				withoutMVLike := ui.Find("id:track-like-cell-no-mv").Bounds()
				if withMVLike.Origin.X != withoutMVLike.Origin.X {
					t.Fatalf("like cell misaligned between MV and non-MV rows: has-mv X=%v, no-mv X=%v", withMVLike.Origin.X, withoutMVLike.Origin.X)
				}
				mvButton := mvBtnView.Bounds()
				mvPlaceholder := ui.Find("id:track-mv-no-mv-placeholder").Bounds()
				if mvButton.Size.Width != mvPlaceholder.Size.Width || mvButton.Origin.X != mvPlaceholder.Origin.X {
					t.Fatalf("MV placeholder mismatch: button=%+v placeholder=%+v", mvButton, mvPlaceholder)
				}
			}
			ui.Press("label:Play MV for With MV")
			ui.WaitUntil(2*time.Second, func() bool { return ui.Exists("id:mv-video") })
			v := ui.FindMounted("id:mv-video")
			if !v.Video.Paused || !a.pl.Playing() || !ui.Exists("id:mv-progress") {
				t.Fatal("video should be held while loading and expose its own controls")
			}
			req, err := http.NewRequest(http.MethodGet, v.Video.Source, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Range", "bytes=2-3")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != http.StatusPartialContent {
				t.Fatalf("relayed video range: %v", res.Status)
			}
			ui.Report(nsgotest.Event{Kind: nsgotest.EventLoad, ID: v.ID, Duration: time.Minute})
			ui.WaitUntil(time.Second, func() bool { return !v.Video.Paused && !a.pl.Playing() })
			if compact {
				if err := host.Send("android.media.command", map[string]any{"command": "seek", "position": float64(24000)}); err != nil {
					t.Fatal(err)
				}
				ui.Settle()
				if len(v.Seeks) != 1 || v.Seeks[0] != 24*time.Second || a.pl.Position() != position {
					t.Fatalf("MV seek reached music or failed: %v", v.Seeks)
				}
				ui.Back()
			} else {
				const progress = "id:mv-progress"
				width := ui.Find(progress).Frame.Size.Width
				ui.Drag(progress, core.Point{X: width / 4})
				if len(v.Seeks) != 1 || v.Seeks[0] < 44*time.Second || v.Seeks[0] > 46*time.Second || a.pl.Position() != position {
					t.Fatalf("MV progress drag reached music or failed: %v", v.Seeks)
				}
				ui.Report(nsgotest.Event{Kind: nsgotest.EventProgress, ID: v.ID, Time: 45 * time.Second, Duration: time.Minute})
				ui.WaitUntil(time.Second, func() bool { return ui.Find("id:mv-elapsed").Text == "0:45" })
				ui.Press("id:mv-close")
			}
			ui.WaitUntil(time.Second, func() bool { return !ui.Exists("id:mv-player") && a.pl.Playing() })
			if len(a.pl.Queue()) != 1 || a.pl.Current().ID != audio.Tracks[0].ID || a.pl.Position() != position {
				t.Fatal("closing the MV disturbed the music queue or position")
			}
			a.post(a.toggle)
			ui.Settle()
			ui.Press("label:Play MV for With MV")
			ui.WaitUntil(time.Second, func() bool { return ui.Exists("id:mv-video") })
			v = ui.FindMounted("id:mv-video")
			ui.Report(nsgotest.Event{Kind: nsgotest.EventLoad, ID: v.ID, Duration: time.Minute})
			ui.WaitUntil(time.Second, func() bool { return !v.Video.Paused })
			if compact {
				if err := host.Send("android.lifecycle", map[string]any{"background": true}); err != nil {
					t.Fatal(err)
				}
			} else {
				ui.Press("id:mv-close")
			}
			ui.WaitUntil(time.Second, func() bool { return !ui.Exists("id:mv-player") })
			if a.pl.Playing() {
				t.Fatal("closing video started previously paused music")
			}
			ui.Press("label:Play MV for Broken video")
			ui.WaitUntil(time.Second, func() bool { return ui.Exists("text:Could not play this MV.") })
			if ui.Exists("id:mv-video") || a.pl.Playing() {
				t.Fatal("failed video changed music playback")
			}
		})
	}
}
