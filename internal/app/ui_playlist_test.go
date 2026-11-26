package app

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"github.com/HengWh/woodwind-music/internal/music"
)

func playlistReview(t *testing.T) (*app, *nsgotest.App) {
	t.Helper()
	t.Setenv("WOOD_WIND_MUSIC_DATA_DIR", t.TempDir())
	var a *app
	ui := nsgotest.Start(t, func(cx *core.Scope) core.Node { a = newApp(cx); return a.view(cx) },
		nsgotest.Size(1180, 780), nsgotest.Environment(core.Environment{Locale: "en", OS: core.OSMacOS, Idiom: core.IdiomDesktop}))
	ui.WaitUntil(time.Second, func() bool { return !a.homeBusy })
	a.post(func() {
		var tracks []music.Track
		for i, name := range []string{"Northern Lights", "Slow Tide", "A Long Way North", "Paper Boats", "The Harbor at Night", "Quiet Hours", "Last Ferry Home", "Snowfield"} {
			tracks = append(tracks, music.Track{ID: fmt.Sprint(i), Name: name,
				Artists: []music.Artist{{ID: "band", Name: "The Polar Lights"}},
				Album:   music.Album{ID: "polar-night", Name: "Polar Night"}, Duration: int64(180000 + i*21000)})
		}
		a.mine = []music.Playlist{{ID: "walks", Name: "Evening Walks", Mine: true}, {ID: "focus", Name: "Focus", Mine: true}, {ID: "picks", Name: "Staff Picks", Saved: true}}
		a.playlists["walks"] = &playlistData{playlist: &music.Playlist{ID: "walks", Name: "Evening Walks", Owner: "Alex", Description: "For the long way home.", Mine: true, Tracks: tracks}}
		a.route.Set(route{page: pagePlaylist, id: "walks"})
		a.bump()
	})
	ui.Settle()
	return a, ui
}

// Desktop sidebar consolidates playlists into a single nav tab placed right after Liked Songs.
func TestDesktopSidebarPlaylistsNavOrder(t *testing.T) {
	a, ui := playlistReview(t)
	if ui.Exists("id:my-playlists-toggle") || ui.Exists("id:saved-playlists-toggle") {
		t.Fatal("sidebar still renders collapsible playlist disclosures")
	}
	liked := ui.Find("id:nav-liked")
	playlists := ui.Find("id:nav-playlists")
	if playlists.Frame.Origin.Y <= liked.Frame.Origin.Y {
		t.Fatalf("nav-playlists (%v) should appear after nav-liked (%v)", playlists.Frame.Origin.Y, liked.Frame.Origin.Y)
	}
	ui.Press("id:nav-playlists")
	if got := a.route.GetUntracked(); got.page != pagePlaylists {
		t.Fatalf("pressing nav-playlists navigated to %+v", got)
	}
	ui.Find("id:playlists-page")

	// Plugin sources with custom Navigation also place Playlists immediately after Liked Songs.
	plug := &navOrderSource{Source: a.local}
	a.post(func() {
		a.sources.Lock()
		a.sources.items[plug.Info().ID] = plug
		a.sources.Unlock()
		a.switchSource(plug.Info().ID)
	})
	ui.WaitUntil(time.Second, func() bool { return a.source().Info().ID == plug.Info().ID })
	likedPlug := ui.Find("id:source-nav-1")
	playlistsPlug := ui.Find("id:nav-playlists")
	recentPlug := ui.Find("id:source-nav-2")
	if !(likedPlug.Frame.Origin.Y < playlistsPlug.Frame.Origin.Y && playlistsPlug.Frame.Origin.Y < recentPlug.Frame.Origin.Y) {
		t.Fatalf("plugin nav-playlists Y=%v not between liked Y=%v and recent Y=%v",
			playlistsPlug.Frame.Origin.Y, likedPlug.Frame.Origin.Y, recentPlug.Frame.Origin.Y)
	}
}

type navOrderSource struct{ music.Source }

func (s *navOrderSource) Info() music.Info {
	return music.Info{
		ID:   "nav-order-plugin",
		Name: "Nav Order Plugin",
		Navigation: []music.NavItem{
			{Builtin: "home"},
			{Builtin: "liked"},
			{Title: "Recent", Page: "recent", Icon: "star"},
		},
	}
}
func (s *navOrderSource) Can(what string) bool {
	return what == "home" || what == "liked" || what == "browse" || what == "playlists" || what == "createPlaylist"
}
func (s *navOrderSource) Home() (*music.Browse, error) { return &music.Browse{}, nil }
func (s *navOrderSource) Playlists() ([]music.Playlist, error) {
	return []music.Playlist{{ID: "p1", Name: "Plugin List", Mine: true}}, nil
}
func (s *navOrderSource) Liked(int) (*music.Page, error) { return &music.Page{}, nil }
func (s *navOrderSource) Recent() ([]music.Track, error) { return nil, nil }

// The dedicated desktop Playlists page filters by ownership/search, sorts, switches views, and opens playlists.
func TestDesktopPlaylistsPageFeatures(t *testing.T) {
	a, ui := playlistReview(t)
	a.post(func() {
		a.mine = []music.Playlist{
			{ID: "walks", Name: "Evening Walks", Owner: "Alex", Description: "For the long way home.", TrackCount: 8, Mine: true},
			{ID: "focus", Name: "Ambient Focus", Owner: "Alex", Description: "Deep work soundtrack", TrackCount: 24, Mine: true},
			{ID: "picks", Name: "Staff Picks", Owner: "Editors", Description: "Weekly highlights", TrackCount: 15, Saved: true},
		}
		a.open(route{page: pagePlaylists})
	})
	ui.Settle()

	ui.Find("id:playlists-grid")
	if !ui.Exists("id:playlist-walks") || !ui.Exists("id:playlist-focus") || !ui.Exists("id:playlist-picks") {
		t.Fatal("playlists grid did not show all playlists")
	}

	// Scope filtering: created vs saved.
	ui.Press("id:playlists-tab-mine")
	if !ui.Exists("id:playlist-walks") || !ui.Exists("id:playlist-focus") || ui.Exists("id:playlist-picks") {
		t.Fatal("My Playlists filter did not hide saved playlists")
	}
	ui.Press("id:playlists-tab-saved")
	if ui.Exists("id:playlist-walks") || !ui.Exists("id:playlist-picks") {
		t.Fatal("Saved Playlists filter did not isolate saved playlists")
	}
	ui.Press("id:playlists-tab-all")

	// Search matches description and owner as well as title.
	ui.Type("id:playlists-search", "editors")
	if !ui.Exists("id:playlist-picks") || ui.Exists("id:playlist-walks") {
		t.Fatal("search did not match playlist owner")
	}
	ui.Press("id:playlists-filter-reset")
	if !ui.Exists("id:playlist-walks") || !ui.Exists("id:playlist-picks") {
		t.Fatal("reset did not restore all playlists")
	}

	// Sort by most songs puts Ambient Focus (24) before Staff Picks (15) and Evening Walks (8).
	a.post(func() { a.playlistsUI.sort = "tracks"; a.bump() })
	ui.Settle()
	ordered := a.filteredPlaylists()
	if len(ordered) != 3 || ordered[0].ID != "focus" || ordered[1].ID != "picks" || ordered[2].ID != "walks" {
		t.Fatalf("sort by tracks = %+v", ordered)
	}

	// Switch to list view and verify rich list controls exist.
	ui.Press("id:playlists-view-list")
	ui.Find("id:playlists-list")
	ui.Find("id:playlist-cover-btn-walks")
	ui.Find("id:playlist-play-walks")

	// New Playlist dialog opens from the header button.
	ui.Press("id:new-playlist")
	if !a.naming || !a.namingNew {
		t.Fatal("new playlist button did not open creation dialog")
	}
	ui.Press("id:name-cancel")

	// Clicking a playlist opens its detail page.
	ui.Press("id:playlist-walks")
	if got := a.route.GetUntracked(); got.page != pagePlaylist || got.id != "walks" {
		t.Fatalf("clicking playlist navigated to %+v", got)
	}
}

func writeTestPNG(t *testing.T, dir, name string, c color.RGBA) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := range 32 {
		for x := range 32 {
			img.SetRGBA(x, y, c)
		}
	}
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// Desktop playlists support choosing a custom cover image, persisting it across reloads, and resetting to default.
func TestPlaylistCoverChangeAndReset(t *testing.T) {
	a, ui := playlistReview(t)
	tmp := t.TempDir()
	firstPNG := writeTestPNG(t, tmp, "first.png", color.RGBA{R: 220, G: 60, B: 80, A: 255})
	secondPNG := writeTestPNG(t, tmp, "second.png", color.RGBA{R: 40, G: 120, B: 230, A: 255})
	badFile := filepath.Join(tmp, "not-an-image.txt")
	if err := os.WriteFile(badFile, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Change cover from Playlists overview page menu.
	a.post(func() { a.open(route{page: pagePlaylists}) })
	ui.Settle()

	pickedPath := firstPNG
	ui.PickFiles = func(nsgotest.FilePick) []core.File {
		return []core.File{{Name: filepath.Base(pickedPath), Path: pickedPath}}
	}
	ui.Press("id:playlist-more-walks")
	ui.Press("id:playlist-change-cover")
	ui.WaitUntil(2*time.Second, func() bool { return a.hasCustomPlaylistCover(music.Playlist{ID: "walks"}) })

	firstURL := a.playlistCoverURL(music.Playlist{ID: "walks", ImageURL: "https://example.com/default.jpg"})
	firstStoredPath, ok := music.PathOf(firstURL)
	if !ok {
		t.Fatalf("custom cover URL is not a file URL: %q", firstURL)
	}
	if _, err := os.Stat(firstStoredPath); err != nil {
		t.Fatalf("stored cover file missing: %v", err)
	}

	// Reloading the store from disk preserves the cover.
	reloaded := loadPlaylistCovers(a.dataDir)
	if got := reloaded.URL(a.source().Info().ID, "walks"); got != firstURL {
		t.Fatalf("reloaded cover URL = %q, want %q", got, firstURL)
	}

	// Change cover again from Playlist detail hero overlay button; old file is replaced.
	pickedPath = secondPNG
	ui.Press("id:playlist-walks")
	ui.Press("id:playlist-cover-edit")
	ui.WaitUntil(2*time.Second, func() bool {
		return a.playlistCoverURL(music.Playlist{ID: "walks"}) != firstURL
	})
	secondURL := a.playlistCoverURL(music.Playlist{ID: "walks"})
	secondStoredPath, ok := music.PathOf(secondURL)
	if !ok {
		t.Fatalf("updated cover URL is not a file URL: %q", secondURL)
	}
	if _, err := os.Stat(firstStoredPath); !os.IsNotExist(err) {
		t.Fatalf("replaced cover file was not cleaned up: %v", err)
	}
	if _, err := os.Stat(secondStoredPath); err != nil {
		t.Fatalf("new cover file missing: %v", err)
	}

	// Invalid image is rejected without overwriting the existing cover.
	pickedPath = badFile
	ui.Press("id:playlist-cover-edit")
	ui.WaitUntil(2*time.Second, func() bool { return a.toast.GetUntracked() != "" })
	if got := a.playlistCoverURL(music.Playlist{ID: "walks"}); got != secondURL {
		t.Fatalf("invalid file overwrote cover: got %q, want %q", got, secondURL)
	}

	// Reset Cover restores default artwork and removes the stored file.
	ui.Press("id:playlist-more")
	ui.Press("id:playlist-reset-cover")
	ui.Settle()
	if a.hasCustomPlaylistCover(music.Playlist{ID: "walks"}) {
		t.Fatal("custom cover still active after reset")
	}
	if got := a.playlistCoverURL(music.Playlist{ID: "walks", ImageURL: "https://example.com/default.jpg"}); got != "https://example.com/default.jpg" {
		t.Fatalf("cover after reset = %q, want default", got)
	}
	if _, err := os.Stat(secondStoredPath); !os.IsNotExist(err) {
		t.Fatalf("reset did not remove stored cover file: %v", err)
	}
}

// The visible More button edits the selected playlist; fixed lists offer no editing actions.
func TestPlaylistHeaderActions(t *testing.T) {
	a, ui := playlistReview(t)
	ui.Press("id:playlist-more")
	ui.Press("id:playlist-rename")
	if !a.naming || a.namingID != "walks" || a.namingText != "Evening Walks" {
		t.Fatal("More renamed a different playlist")
	}
	ui.Press("id:name-cancel")
	ui.Press("id:playlist-more")
	ui.Press("id:playlist-delete")
	if !a.deleting || a.deletingID != "walks" {
		t.Fatal("More deleted a different playlist")
	}
	ui.Key("escape")
	a.post(func() { a.playlists["walks"].playlist.Fixed = true; a.bump() })
	ui.Settle()
	if ui.Exists("id:playlist-more") {
		t.Fatal("a fixed playlist retained its edit menu")
	}
	ui.RightClick("id:track-0")
	if ui.Exists(`text:"Remove from Playlist"`) || ui.Exists(`text:"Move Down"`) {
		t.Fatal("a fixed playlist allowed editing songs")
	}
}

// Album links remain clickable after the playlist switches between wide and narrow layouts.
func TestPlaylistAlbumColumnResizes(t *testing.T) {
	a, ui := playlistReview(t)
	ui.Find("id:track-album-0")
	ui.Resize(880, 780)
	if ui.Exists("id:track-album-0") {
		t.Fatal("the album column crowded a narrow playlist")
	}
	ui.Resize(1180, 780)
	ui.Press("id:track-album-0")
	if r := a.route.GetUntracked(); r.page != pageAlbum || r.id != "polar-night" {
		t.Fatalf("album column navigated to %+v", r)
	}
}

// Optional real-window captures compare playlist layout with MyGo at desktop and phone sizes.
func TestPlaylistUIShots(t *testing.T) {
	if os.Getenv("WOOD_WIND_PLAYLIST_SHOTS") == "" {
		t.Skip("set WOOD_WIND_PLAYLIST_SHOTS=1 to review playlists")
	}
	a, ui := playlistReview(t)
	art := image.NewRGBA(image.Rect(0, 0, 320, 320))
	for y := range 320 {
		for x := range 320 {
			art.SetRGBA(x, y, color.RGBA{R: uint8(40 + y/2), G: uint8(90 + x/8), B: uint8(170 - y/12), A: 255})
		}
	}
	path := filepath.Join(t.TempDir(), "cover.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, art); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	a.post(func() {
		for i := range a.mine {
			a.mine[i].ImageURL = path
		}
		pl := a.playlists["walks"].playlist
		pl.ImageURL = path
		for i := range pl.Tracks {
			pl.Tracks[i].Album.ImageURL = path
		}
		a.bump()
	})
	ui.Settle()
	for _, dark := range []bool{false, true} {
		a.post(func() {
			appearance := core.AppearanceLight
			if dark {
				appearance = core.AppearanceDark
			}
			a.cx.SetAppearance(appearance)
		})
		ui.Settle()
		for _, width := range []core.Points{1180, 880, 390} {
			ui.Resize(width, 780)
			ui.Screenshot(fmt.Sprintf("playlist-%v-dark-%t", width, dark))
		}
	}
}
