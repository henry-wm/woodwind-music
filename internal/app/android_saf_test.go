package app

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"github.com/HengWh/woodwind-music/internal/music"
)

func TestAndroidSAFDirectFolderPlaybackAndManagement(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("WOOD_WIND_MUSIC_DATA_DIR", dataDir)

	musicDir := t.TempDir()
	writeToneWAV(t, filepath.Join(musicDir, "Haruhikage.wav"))
	if err := os.WriteFile(filepath.Join(musicDir, "Haruhikage.lrc"), []byte("[00:00.50]Spring sunlight\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	treeURI := "content://com.android.externalstorage.documents/tree/primary%3AMusic%2FWoodwind"
	host := core.NewHost()
	if err := host.SetParams(map[string]any{"music.android": true, "music.saf": true}); err != nil {
		t.Fatal(err)
	}
	host.Handle("playback.update", func(call *core.HostCall) { call.Resolve(nil) })

	var mu sync.Mutex
	var nextHandle atomic.Int64
	openFiles := map[int64]*os.File{}
	var released []string
	var walkErr string

	host.Handle("saf.pick", func(call *core.HostCall) {
		call.Resolve(map[string]any{"uri": treeURI, "name": "Music/Woodwind"})
	})
	host.Handle("saf.walk", func(call *core.HostCall) {
		mu.Lock()
		errMsg := walkErr
		mu.Unlock()
		if errMsg != "" {
			call.Reject(errors.New(errMsg))
			return
		}
		var entries []map[string]any
		_ = filepath.WalkDir(musicDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(musicDir, p)
			rel = filepath.ToSlash(rel)
			info, err := d.Info()
			if err != nil {
				return nil
			}
			item := map[string]any{
				"rel":   rel,
				"docId": "doc:" + rel,
				"size":  info.Size(),
				"mod":   info.ModTime().UnixMilli(),
			}
			if strings.ToLower(path.Ext(rel)) == ".wav" {
				item["docUri"] = treeURI + "/document/" + rel
			}
			entries = append(entries, item)
			return nil
		})
		call.Resolve(map[string]any{"entries": entries})
	})
	host.Handle("saf.open", func(call *core.HostCall) {
		m := call.Data.(map[string]any)
		rel := strings.TrimPrefix(m["docId"].(string), "doc:")
		f, err := os.Open(filepath.Join(musicDir, filepath.FromSlash(rel)))
		if err != nil {
			call.Reject(err)
			return
		}
		info, _ := f.Stat()
		h := nextHandle.Add(1)
		mu.Lock()
		openFiles[h] = f
		mu.Unlock()
		call.Resolve(map[string]any{"handle": h, "size": info.Size()})
	})
	host.Handle("saf.read", func(call *core.HostCall) {
		m := call.Data.(map[string]any)
		h := hostInt64(m["handle"])
		off := hostInt64(m["offset"])
		length := int(hostInt64(m["length"]))
		mu.Lock()
		f := openFiles[h]
		mu.Unlock()
		if f == nil {
			call.Reject(errors.New("closed"))
			return
		}
		buf := make([]byte, length)
		n, err := f.ReadAt(buf, off)
		if err != nil && !errors.Is(err, io.EOF) {
			call.Reject(err)
			return
		}
		call.Resolve(map[string]any{"data": base64.StdEncoding.EncodeToString(buf[:n])})
	})
	host.Handle("saf.close", func(call *core.HostCall) {
		m := call.Data.(map[string]any)
		h := hostInt64(m["handle"])
		mu.Lock()
		f := openFiles[h]
		delete(openFiles, h)
		mu.Unlock()
		if f != nil {
			_ = f.Close()
		}
		call.Resolve(nil)
	})
	host.Handle("saf.readFile", func(call *core.HostCall) {
		m := call.Data.(map[string]any)
		rel := strings.TrimPrefix(m["docId"].(string), "doc:")
		b, err := os.ReadFile(filepath.Join(musicDir, filepath.FromSlash(rel)))
		if err != nil {
			call.Reject(err)
			return
		}
		call.Resolve(map[string]any{"data": base64.StdEncoding.EncodeToString(b)})
	})
	host.Handle("saf.stat", func(call *core.HostCall) {
		m := call.Data.(map[string]any)
		rel := strings.TrimPrefix(m["docId"].(string), "doc:")
		info, err := os.Stat(filepath.Join(musicDir, filepath.FromSlash(rel)))
		if err != nil {
			call.Resolve(map[string]any{"exists": false})
			return
		}
		call.Resolve(map[string]any{"exists": true, "size": info.Size()})
	})
	host.Handle("saf.release", func(call *core.HostCall) {
		m := call.Data.(map[string]any)
		mu.Lock()
		released = append(released, m["uri"].(string))
		mu.Unlock()
		call.Resolve(nil)
	})

	var a *app
	ui := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a = newApp(cx)
		return a.view(cx)
	}, nsgotest.Size(390, 844), nsgotest.Environment(core.Environment{
		OS: core.OSAndroid, SafeArea: core.Edges{Top: 24, Bottom: 24},
	}), nsgotest.Host(host))

	ui.Press("id:phone-add-folder")
	ui.WaitUntil(3*time.Second, func() bool {
		return a.local.TrackCount() == 1 && !a.local.Scanning() && !a.homeBusy
	})

	// Must not copy files into private imports/ storage.
	if _, err := os.Stat(filepath.Join(dataDir, "imports")); !os.IsNotExist(err) {
		t.Fatal("Android SAF direct mode unexpectedly copied files into imports/")
	}

	res, err := a.local.Search("", music.SearchTracks, 0)
	if err != nil || len(res.Tracks) != 1 {
		t.Fatalf("tracks: %v, %v", res, err)
	}
	track := res.Tracks[0]

	// Playback streams the content:// URI directly through core.Video and loads
	// .lrc lyrics on demand from the SAF folder without copying files.
	ui.WaitUntil(2*time.Second, func() bool { return ui.Exists(fmt.Sprintf("label:%q", track.Name)) })
	ui.Press(fmt.Sprintf("label:%q", track.Name))
	ui.WaitUntil(2*time.Second, func() bool { return a.video.request.GetUntracked() != nil })
	video := ui.FindMounted("id:android-audio")
	if video.Video.Paused || !strings.HasPrefix(video.Video.Source, "content://") {
		t.Fatalf("expected direct content:// playback source, got %+v", video.Video)
	}
	ui.WaitUntil(2*time.Second, func() bool {
		lines, loaded := a.pl.Lyrics()
		return loaded && len(lines) == 1 && lines[0].Text == "Spring sunlight"
	})

	// Settings shows the friendly folder label and permission-loss warning when revoked.
	a.settingsPage = "library"
	a.openSettings()
	ui.Settle()
	ui.Find("text:Music/Woodwind")

	mu.Lock()
	walkErr = "permission: revoked"
	mu.Unlock()
	a.local.Rescan()
	ui.WaitUntil(3*time.Second, func() bool {
		return !a.local.Scanning() && a.local.FolderError(treeURI) != nil
	})
	ui.Settle()
	ui.Find("text:Folder access lost. Remove and add it again.")

	// Removing the folder releases persisted permission.
	ui.Press("id:settings-remove-folder")
	ui.WaitUntil(2*time.Second, func() bool { return len(a.local.Folders()) == 0 && !a.local.Scanning() })
	mu.Lock()
	gotReleased := append([]string(nil), released...)
	mu.Unlock()
	if len(gotReleased) != 1 || gotReleased[0] != treeURI {
		t.Fatalf("released URIs = %v, want [%s]", gotReleased, treeURI)
	}
}
