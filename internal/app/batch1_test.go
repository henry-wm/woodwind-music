package app

import (
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"github.com/HengWh/woodwind-music/internal/i18n"
	"github.com/HengWh/woodwind-music/internal/lyrics"
	"github.com/HengWh/woodwind-music/internal/music"
	"github.com/HengWh/woodwind-music/internal/player"
)

type batch1TestSource struct {
	info          music.Info
	caps          map[string]bool
	mu            sync.Mutex
	suggestFn     func(q string) ([]string, error)
	commentsFn    func(trackID string, offset int) (*music.CommentPage, error)
	likeCommentFn func(trackID, commentID string, on bool) error
	postCommentFn func(trackID, text string) (*music.Comment, error)
	lyricsFn      func(trackID string) (*music.Lyrics, error)
	showFn        func(id string, offset int) (*music.ShowPage, error)
	browseFn      func(id string) (*music.Browse, error)
	homeFn        func() (*music.Browse, error)
	likedCalls    []struct {
		id string
		on bool
	}
}

func (s *batch1TestSource) Info() music.Info               { return s.info }
func (s *batch1TestSource) Can(what string) bool           { return s.caps[what] }
func (s *batch1TestSource) Setting(string) string          { return "" }
func (s *batch1TestSource) SetSetting(string, string) error { return nil }
func (s *batch1TestSource) SignIn() (*music.Account, error) { return nil, music.ErrUnsupported }
func (s *batch1TestSource) SignOut() error                  { return music.ErrUnsupported }
func (s *batch1TestSource) Account() (*music.Account, error) { return nil, nil }
func (s *batch1TestSource) Search(query string, kind music.SearchKind, offset int) (*music.SearchResult, error) {
	return &music.SearchResult{}, nil
}
func (s *batch1TestSource) Suggest(query string) ([]string, error) {
	if s.suggestFn != nil {
		return s.suggestFn(query)
	}
	return nil, music.ErrUnsupported
}
func (s *batch1TestSource) Playlists() ([]music.Playlist, error)             { return nil, nil }
func (s *batch1TestSource) Playlist(string) (*music.Playlist, error)         { return nil, music.ErrUnsupported }
func (s *batch1TestSource) Album(string) (*music.AlbumPage, error)           { return nil, music.ErrUnsupported }
func (s *batch1TestSource) Artist(string) (*music.ArtistPage, error)         { return nil, music.ErrUnsupported }
func (s *batch1TestSource) Liked(int) (*music.Page, error)                   { return &music.Page{}, nil }
func (s *batch1TestSource) Recent() ([]music.Track, error)                   { return nil, nil }
func (s *batch1TestSource) Home() (*music.Browse, error) {
	if s.homeFn != nil {
		return s.homeFn()
	}
	return &music.Browse{}, nil
}
func (s *batch1TestSource) Browse(id string) (*music.Browse, error) {
	if s.browseFn != nil {
		return s.browseFn(id)
	}
	return &music.Browse{Title: id}, nil
}
func (s *batch1TestSource) IsLiked([]string) (map[string]bool, error) { return nil, nil }
func (s *batch1TestSource) Like(id string, on bool) error {
	s.mu.Lock()
	s.likedCalls = append(s.likedCalls, struct {
		id string
		on bool
	}{id: id, on: on})
	s.mu.Unlock()
	return nil
}
func (s *batch1TestSource) LikeEpisode(string, bool) error          { return nil }
func (s *batch1TestSource) CreatePlaylist(string) (*music.Playlist, error) { return nil, music.ErrUnsupported }
func (s *batch1TestSource) RenamePlaylist(string, string) error     { return music.ErrUnsupported }
func (s *batch1TestSource) DeletePlaylist(string) error             { return music.ErrUnsupported }
func (s *batch1TestSource) AddToPlaylist(string, []string) error    { return music.ErrUnsupported }
func (s *batch1TestSource) RemoveFromPlaylist(string, []string) error { return music.ErrUnsupported }
func (s *batch1TestSource) MovePlaylistTrack(string, int, int) error { return music.ErrUnsupported }
func (s *batch1TestSource) SavePlaylist(string, bool) error         { return music.ErrUnsupported }
func (s *batch1TestSource) FollowArtist(string, bool) error         { return music.ErrUnsupported }
func (s *batch1TestSource) SaveAlbum(string, bool) error            { return music.ErrUnsupported }
func (s *batch1TestSource) SavedAlbums(int) (*music.AlbumList, error) { return &music.AlbumList{}, nil }
func (s *batch1TestSource) Shows() ([]music.Show, error)            { return nil, nil }
func (s *batch1TestSource) Show(id string, offset int) (*music.ShowPage, error) {
	if s.showFn != nil {
		return s.showFn(id, offset)
	}
	return nil, music.ErrUnsupported
}
func (s *batch1TestSource) SubscribeShow(string, bool) error { return music.ErrUnsupported }
func (s *batch1TestSource) Stations() ([]music.Station, error) { return nil, music.ErrUnsupported }
func (s *batch1TestSource) StationNext(string) ([]music.Track, error) { return nil, music.ErrUnsupported }
func (s *batch1TestSource) Comments(trackID string, offset int) (*music.CommentPage, error) {
	if s.commentsFn != nil {
		return s.commentsFn(trackID, offset)
	}
	return nil, music.ErrUnsupported
}
func (s *batch1TestSource) LikeComment(trackID, commentID string, on bool) error {
	if s.likeCommentFn != nil {
		return s.likeCommentFn(trackID, commentID, on)
	}
	return nil
}
func (s *batch1TestSource) PostComment(trackID, text string) (*music.Comment, error) {
	if s.postCommentFn != nil {
		return s.postCommentFn(trackID, text)
	}
	return &music.Comment{ID: "posted", Author: "Me", Text: text}, nil
}
func (s *batch1TestSource) Lyrics(id string) (*music.Lyrics, error) {
	if s.lyricsFn != nil {
		return s.lyricsFn(id)
	}
	return nil, music.ErrUnsupported
}
func (s *batch1TestSource) Stream(string, string) (*music.Stream, error) { return nil, music.ErrUnsupported }
func (s *batch1TestSource) RemotePlay([]string, int, int64) error        { return music.ErrUnsupported }
func (s *batch1TestSource) RemotePause() error                           { return music.ErrUnsupported }
func (s *batch1TestSource) RemoteResume() error                          { return music.ErrUnsupported }
func (s *batch1TestSource) RemoteSeek(int64) error                       { return music.ErrUnsupported }
func (s *batch1TestSource) RemoteVolume(int) error                       { return music.ErrUnsupported }
func (s *batch1TestSource) RemoteState() (*music.RemoteState, error)     { return nil, music.ErrUnsupported }
func (s *batch1TestSource) RemoteDevices() ([]music.Device, error)       { return nil, music.ErrUnsupported }
func (s *batch1TestSource) RemoteUse(string) error                       { return music.ErrUnsupported }

func registerTestSource(a *app, src music.Source) {
	a.sources.Lock()
	a.sources.items[src.Info().ID] = src
	a.sources.Unlock()
}

func TestSearchSuggestDesktopAndPhone(t *testing.T) {
	prevDelay := suggestDelay
	suggestDelay = 10 * time.Millisecond
	t.Cleanup(func() { suggestDelay = prevDelay })

	t.Setenv("WOOD_WIND_MUSIC_DATA_DIR", t.TempDir())
	src := &batch1TestSource{
		info: music.Info{ID: "mock", Name: "Mock"},
		caps: map[string]bool{"home": true, "search": true, "suggest": true},
		suggestFn: func(q string) ([]string, error) {
			return []string{q + " live", q + " acoustic"}, nil
		},
	}

	var a *app
	ui := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a = newApp(cx)
		a.set.Language = i18n.English
		a.applyLanguage()
		registerTestSource(a, src)
		return a.view(cx)
	}, nsgotest.Size(1180, 780), nsgotest.Environment(core.Environment{Locale: "en", OS: core.OSMacOS, Idiom: core.IdiomDesktop}))

	a.post(func() { a.switchSource("mock") })
	ui.Settle()

	ui.Type("id:search-input", "haru")
	ui.WaitUntil(2*time.Second, func() bool {
		return ui.Exists(`text:"haru live"`) && ui.Exists(`text:"haru acoustic"`)
	})

	// Clicking a suggestion runs the search and closes the popup.
	ui.Press(`text:"haru live"`)
	ui.WaitUntil(2*time.Second, func() bool {
		r := a.route.GetUntracked()
		return r.page == pageSearch && r.query == "haru live"
	})
	if a.searchOpen.GetUntracked() {
		t.Fatal("search popup stayed open after selecting a suggestion")
	}

	// Phone layout also requests and displays remote suggestions.
	var phoneApp *app
	phoneUI := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		phoneApp = newApp(cx)
		phoneApp.set.Language = i18n.English
		phoneApp.applyLanguage()
		registerTestSource(phoneApp, src)
		return phoneApp.view(cx)
	}, nsgotest.Size(390, 844), nsgotest.Environment(core.Environment{Locale: "en", OS: core.OSAndroid, Idiom: core.IdiomPhone}))

	phoneApp.post(func() {
		phoneApp.switchSource("mock")
		phoneApp.selectPhoneTab(2)
	})
	phoneUI.Settle()
	phoneUI.Type("id:search-input", "sora")
	phoneUI.WaitUntil(2*time.Second, func() bool {
		return phoneUI.Exists(`text:"sora live"`) && phoneUI.Exists(`text:"sora acoustic"`)
	})
}

func TestNowPlayingCommentsHotCommentsAndPodcastAbout(t *testing.T) {
	t.Setenv("WOOD_WIND_MUSIC_DATA_DIR", t.TempDir())

	likeFail := false
	src := &batch1TestSource{
		info: music.Info{ID: "cloud", Name: "Cloud"},
		caps: map[string]bool{
			"home": true, "comments": true, "likeComment": true, "postComment": true, "lyrics": true, "like": true,
		},
		commentsFn: func(trackID string, offset int) (*music.CommentPage, error) {
			if offset == 0 {
				return &music.CommentPage{
					Total: 2,
					More:  false,
					Comments: []music.Comment{
						{ID: "c1", Author: "Alice", Text: "Best intro ever", Likes: 42},
						{ID: "c2", Author: "Bob", Text: "Second comment", Likes: 5},
					},
				}, nil
			}
			return &music.CommentPage{}, nil
		},
		likeCommentFn: func(trackID, commentID string, on bool) error {
			if likeFail {
				return errors.New("denied")
			}
			return nil
		},
		postCommentFn: func(trackID, text string) (*music.Comment, error) {
			return &music.Comment{ID: "c3", Author: "Tester", Text: text, Likes: 0}, nil
		},
	}

	var a *app
	ui := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a = newApp(cx)
		a.set.Language = i18n.English
		a.applyLanguage()
		registerTestSource(a, src)
		return a.view(cx)
	}, nsgotest.Size(1180, 780), nsgotest.Environment(core.Environment{Locale: "en", OS: core.OSMacOS, Idiom: core.IdiomDesktop}))

	song := music.Track{ID: "s1", Source: "cloud", Name: "Intro Song", Duration: 60000}
	a.post(func() {
		a.switchSource("cloud")
		a.pl.Cue([]music.Track{song})
		a.openNowPlaying()
	})
	ui.Settle()

	// Comments tab loads count and opens comments pane.
	ui.WaitUntil(2*time.Second, func() bool { return a.npComments.loaded && a.npComments.total == 2 })
	ui.Find("id:np-tab-4")
	ui.Press("id:np-tab-4")
	ui.WaitUntil(2*time.Second, func() bool { return ui.Exists(`text:"Best intro ever"`) })

	// Like comment toggles optimistically; rollback works on failure.
	ui.Press("id:np-comment-like-c1")
	ui.WaitUntil(2*time.Second, func() bool { return a.npComments.find("c1").Likes == 43 && a.npComments.find("c1").Liked })

	likeFail = true
	ui.Press("id:np-comment-like-c1")
	ui.WaitUntil(2*time.Second, func() bool { return a.npComments.find("c1").Likes == 43 && a.npComments.find("c1").Liked })
	likeFail = false

	// Posting a comment prepends it and increments total.
	ui.Type("id:np-comment-input", "Hello world")
	ui.Press("id:np-comment-send")
	ui.WaitUntil(2*time.Second, func() bool { return ui.Exists(`text:"Hello world"`) && a.npComments.total == 3 })

	// Hot comments in quiet parts: enable setting and verify card appears during quiet intro/instrumental track.
	a.post(func() {
		a.set.HotComments = true
		a.pl.Restore(player.Session{Queue: []music.Track{song}, Order: []int{0}, At: 0, Position: 3000})
		a.npPane.Set(npPaneLyrics)
		a.bump()
		a.tick.Update(func(n int) int { return n + 1 })
	})
	ui.Settle()
	// Verify quietComment returns top-liked comment at 3s into a 15s intro.
	lrc := lyrics.FromSource(&music.Lyrics{LRC: "[00:15.00]First sung line"})
	cm := a.quietComment(&song, lrc, true, 3*time.Second)
	if cm == nil || cm.ID != "c1" {
		t.Fatalf("quietComment(3s) = %+v, want c1", cm)
	}
	if got := a.quietComment(&song, lrc, true, 16*time.Second); got != nil {
		t.Fatalf("quietComment(16s) = %+v, want nil during singing", got)
	}

	// Verify instrumental track (no lyrics) at 3s shows top comment card in Now Playing and clicking it opens Comments.
	ui.WaitUntil(2*time.Second, func() bool { return ui.Exists("id:np-hot-comment") })
	ui.Press("id:np-hot-comment")
	ui.WaitUntil(2*time.Second, func() bool { return a.npPane.GetUntracked() == npPaneComments })

	// Switch to a podcast episode: About tab becomes active and renders description.
	ep := music.Track{ID: "ep1", Source: "cloud", ShowID: "show1", Name: "Episode 1", Description: "Deep dive into Go audio.", Duration: 120000}
	a.post(func() {
		a.pl.Cue([]music.Track{ep})
		a.npPane.Set(npPaneLyrics)
		a.playRev.Update(func(n int) int { return n + 1 })
	})
	ui.Settle()
	ui.Find("id:np-tab-5")
	ui.Find("id:np-show-about")
	ui.Find(`text:"Deep dive into Go audio."`)
}

func TestBatch1PolishFeatures(t *testing.T) {
	t.Setenv("WOOD_WIND_MUSIC_DATA_DIR", t.TempDir())
	src := &batch1TestSource{
		info: music.Info{
			ID:   "polish",
			Name: "Polish",
			Navigation: []music.NavItem{
				{Builtin: "home"},
				{Builtin: "liked", Title: "My Favorites"},
				{Builtin: "savedAlbums", Title: "Collected Albums"},
				{Builtin: "shows"},
			},
		},
		caps: map[string]bool{"home": true, "liked": true, "savedAlbums": true, "shows": true, "show": true, "browse": true, "like": true},
		homeFn: func() (*music.Browse, error) {
			return &music.Browse{
				Sections: []music.Section{{Title: "Empty Picks", Empty: "Nothing published today"}},
			}, nil
		},
		showFn: func(id string, offset int) (*music.ShowPage, error) {
			return &music.ShowPage{
				Show: music.Show{
					ID:            id,
					Name:          "Tech Radio",
					Publisher:     "Studio FM",
					PublisherPage: "host/studio-fm",
					EpisodeCount:  12,
				},
			}, nil
		},
		browseFn: func(id string) (*music.Browse, error) {
			return &music.Browse{Title: "Studio FM Profile"}, nil
		},
	}

	var a *app
	ui := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a = newApp(cx)
		a.set.Language = i18n.English
		a.applyLanguage()
		registerTestSource(a, src)
		return a.view(cx)
	}, nsgotest.Size(1180, 780), nsgotest.Environment(core.Environment{Locale: "en", OS: core.OSMacOS, Idiom: core.IdiomDesktop}))

	a.post(func() { a.switchSource("polish") })
	ui.WaitUntil(2*time.Second, func() bool { return ui.Exists(`text:"Nothing published today"`) })

	// Custom sidebar title applies to Liked page header.
	a.post(func() { a.open(route{page: pageLiked}) })
	ui.Settle()
	ui.Find(`text:"My Favorites" >> nth=1`)

	// Custom sidebar title applies to Saved Albums page header.
	a.post(func() { a.open(route{page: pageSavedAlbums}) })
	ui.WaitUntil(2*time.Second, func() bool { return ui.Exists(`text:"Collected Albums" >> nth=1`) })

	// Show PublisherPage opens host profile browse page.
	a.post(func() { a.open(route{page: pageShow, id: "s1"}) })
	ui.WaitUntil(2*time.Second, func() bool { return ui.Exists("id:show-publisher") })
	ui.Press("id:show-publisher")
	ui.WaitUntil(2*time.Second, func() bool {
		r := a.route.GetUntracked()
		return r.page == pageBrowse && r.id == "host/studio-fm"
	})

	// Lossless badge in desktop Now Playing.
	track := music.Track{ID: "t1", Source: "polish", Name: "HiRes Song", Duration: 180000}
	a.post(func() {
		a.pl.Cue([]music.Track{track})
		a.pl.SetStream(&music.Stream{Type: "flac", Quality: "lossless"})
		a.openNowPlaying()
	})
	ui.Settle()
	ui.Find("id:np-lossless")

	// Cmd/Ctrl+D likes current track.
	a.post(func() { a.likeCurrent() })
	ui.WaitUntil(2*time.Second, func() bool {
		src.mu.Lock()
		defer src.mu.Unlock()
		return len(src.likedCalls) == 1 && src.likedCalls[0].id == "t1" && src.likedCalls[0].on
	})

	// Mouse-wheel volume uses fixed step per notch and clamps to [0, 1].
	a.post(func() {
		a.set.Volume = 0.5
		a.pl.SetVolume(0.5)
		a.adjustVolumeByWheel(core.WheelEvent{Delta: core.Point{Y: 120}})
	})
	ui.Settle()
	if got := a.pl.Volume(); got < 0.529 || got > 0.531 {
		t.Fatalf("volume after wheel up = %v, want 0.53", got)
	}
	a.post(func() {
		a.adjustVolumeByWheel(core.WheelEvent{Delta: core.Point{Y: -40}})
	})
	ui.Settle()
	if got := a.pl.Volume(); got < 0.499 || got > 0.501 {
		t.Fatalf("volume after wheel down = %v, want 0.50", got)
	}

	// Animated WebP dominantColor decodes first frame cleanly.
	realStill := []byte{
		0x52, 0x49, 0x46, 0x46, 0x24, 0x00, 0x00, 0x00,
		0x57, 0x45, 0x42, 0x50, 0x56, 0x50, 0x38, 0x20,
		0x18, 0x00, 0x00, 0x00, 0x30, 0x01, 0x00, 0x9d,
		0x01, 0x2a, 0x01, 0x00, 0x01, 0x00, 0x02, 0x00,
		0x34, 0x25, 0xa4, 0x00, 0x03, 0x70, 0x00, 0xfe,
		0xfb, 0xfd, 0x50, 0x00,
	}
	vp8x := []byte{'V', 'P', '8', 'X', 10, 0, 0, 0, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	anmfBody := append(make([]byte, 16), realStill[12:]...)
	anmf := append([]byte("ANMF"), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(anmf[4:8], uint32(len(anmfBody)))
	anmf = append(anmf, anmfBody...)
	body := append([]byte("WEBP"), vp8x...)
	body = append(body, anmf...)
	anim := binary.LittleEndian.AppendUint32([]byte("RIFF"), uint32(len(body)))
	anim = append(anim, body...)
	if dominantColor(anim) == 0 {
		t.Fatal("dominantColor returned 0 for animated WebP")
	}
}
