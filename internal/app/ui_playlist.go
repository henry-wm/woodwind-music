package app

import (
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/HengWh/woodwind-music/internal/i18n"
	"github.com/HengWh/woodwind-music/internal/music"
)

// playlistScroll uses the original list's insets: the header sits twelve
// points outside the text of a song, aligned with the row's rounded edge.
func (a *app) playlistScroll(p palette, children ...core.Node) core.Node {
	scroll := a.pageScroll(p, children...).(core.Scroll)
	if !a.phoneLayout(a.cx) {
		content := scroll.Children[0].(core.View)
		content.Style.Padding.Left = pagePad - 12
		content.Style.Padding.Right = pagePad - 12
		scroll.Children[0] = content
	}
	return scroll
}

func (a *app) canEditPlaylistTracks(id string) bool {
	d := a.playlists[id]
	return d != nil && d.playlist != nil && a.playlistEditable(*d.playlist)
}

// playlistEditable follows the source's ownership and fixed-list flags.
func (a *app) playlistEditable(pl music.Playlist) bool {
	if pl.Fixed {
		return false
	}
	if pl.Mine {
		return true
	}
	for _, own := range a.mine {
		if own.ID == pl.ID && own.Mine {
			return !own.Fixed
		}
	}
	return false
}

func (a *app) playlistMenu(pl music.Playlist) []core.Node {
	if !a.playlistEditable(pl) {
		return nil
	}
	var items []core.Node
	if a.can("renamePlaylist") {
		items = append(items, ui.MenuItem{Text: i18n.T("Rename Playlist…"), TestID: "playlist-rename", OnSelect: func() { a.renamePlaylist(pl.ID, pl.Name) }})
	}
	if a.can("deletePlaylist") {
		if len(items) > 0 {
			items = append(items, ui.MenuSeparator{})
		}
		items = append(items, ui.MenuItem{Text: i18n.T("Delete Playlist"), TestID: "playlist-delete", Variant: ui.MenuItemDanger, OnSelect: func() { a.confirmDelete(pl.ID, pl.Name) }})
	}
	return items
}

func (a *app) playlistDesktopMenu(pl music.Playlist, overview bool) []core.Node {
	var items []core.Node
	plCopy := pl
	if overview {
		items = append(items, ui.MenuItem{Text: i18n.T("Play"), Icon: lucide.Play, TestID: "playlist-menu-play", OnSelect: func() { a.playPlaylist(plCopy.ID) }})
		if a.disk != nil && a.source() != a.local {
			items = append(items, ui.MenuItem{Text: i18n.T("Keep Offline"), Icon: lucide.Download, TestID: "playlist-menu-offline", OnSelect: func() { a.pinPlaylist(plCopy.ID) }})
		}
	}
	var coverItems []core.Node
	if a.canChangePlaylistCover(plCopy) {
		coverItems = append(coverItems, ui.MenuItem{Text: i18n.T("Change Cover…"), Icon: lucide.Image, TestID: "playlist-change-cover", OnSelect: func() { a.pickPlaylistCover(plCopy) }})
		if a.hasCustomPlaylistCover(plCopy) {
			coverItems = append(coverItems, ui.MenuItem{Text: i18n.T("Reset Cover"), Icon: lucide.RotateCcw, TestID: "playlist-reset-cover", OnSelect: func() { a.resetPlaylistCover(plCopy) }})
		}
	}
	if len(coverItems) > 0 {
		if len(items) > 0 {
			items = append(items, ui.MenuSeparator{})
		}
		items = append(items, coverItems...)
	}
	editItems := a.playlistMenu(plCopy)
	if len(editItems) > 0 {
		if len(items) > 0 {
			items = append(items, ui.MenuSeparator{})
		}
		items = append(items, editItems...)
	} else if overview && !plCopy.Mine && a.can("savePlaylist") {
		if len(items) > 0 {
			items = append(items, ui.MenuSeparator{})
		}
		items = append(items, ui.MenuItem{Text: i18n.T("Remove from Saved"), TestID: "playlist-unsave", Variant: ui.MenuItemDanger, OnSelect: func() { a.setPlaylistSaved(plCopy.ID, false) }})
	}
	return items
}

func (a *app) pinPlaylist(id string) {
	if a.disk == nil {
		return
	}
	if d := a.playlists[id]; d != nil && d.playlist != nil {
		a.pinTracks(d.playlist.Tracks, "")
		return
	}
	src := a.source()
	go func() {
		pl, err := src.Playlist(id)
		a.post(func() {
			if err != nil {
				a.say(err.Error())
				return
			}
			if a.source() != src || pl == nil {
				return
			}
			d := a.playlists[id]
			if d == nil {
				d = &playlistData{}
				a.playlists[id] = d
			}
			d.playlist = pl
			a.bump()
			a.pinTracks(pl.Tracks, "")
		})
	}()
}

func (a *app) playlistSaved(pl music.Playlist) bool {
	if pl.Saved {
		return true
	}
	for _, list := range a.mine {
		if list.ID == pl.ID && !list.Mine {
			return true
		}
	}
	return false
}

func (a *app) setPlaylistSaved(id string, saved bool) {
	src := a.source()
	a.sourceAction(src, func() error { return src.SavePlaylist(id, saved) }, nil)
}

// playlistHero ports MyGo's cover, metadata and inline actions together.
func (a *app) playlistHero(p palette, pl *music.Playlist) core.Node {
	var meta []string
	if pl.Owner != "" {
		meta = append(meta, pl.Owner)
	}
	meta = append(meta, i18n.Songs(len(pl.Tracks)))
	details := []core.Node{
		text(i18n.T("Playlist"), 12, core.FontWeightSemibold, p.accent),
		core.Text{TestID: "playlist-title", Content: pl.Name, MaxLines: 2, Style: core.Style{FontSize: 30, FontWeight: core.FontWeight(800), LetterSpacing: -0.6, LineHeightMultiple: 1.2, Color: p.text}},
		text(strings.Join(meta, " · "), 13, core.FontWeightNormal, p.muted),
	}
	if description := strings.TrimSpace(pl.Description); description != "" {
		details = append(details, core.Text{Content: strings.Join(strings.Fields(description), " "), MaxLines: 2,
			Style: core.Style{FontSize: 12.5, Color: p.muted, LineHeightMultiple: 1.45, Margin: core.Edges{Top: 2}}})
	}
	var actions []core.Node
	if a.disk != nil && a.source() != a.local && len(pl.Tracks) > 0 {
		actions = append(actions, a.settingsAction(p, i18n.T("Keep Offline"), "playlist-keep-offline", false, func() { a.pinTracks(pl.Tracks, "") }))
	}
	if len(pl.Tracks) > 0 || !a.phoneLayout(a.cx) {
		play := a.playButtons(p, pl.Tracks).(core.View)
		actions = append(actions, play.Children...)
	}
	own := pl.Mine
	for _, list := range a.mine {
		if list.ID == pl.ID && list.Mine {
			own = true
			break
		}
	}
	if a.can("savePlaylist") && !own {
		saved := a.playlistSaved(*pl)
		glyph, label := lucidePlus, i18n.TC("Save", "library")
		if saved {
			glyph, label = lucideCheck, i18n.T("Saved")
		}
		button := a.pillButton(p, glyph, label, blend(p.bg, p.text, 0.04), p.text, func() { a.setPlaylistSaved(pl.ID, !saved) }).(base.Button)
		button.TestID = "playlist-save"
		actions = append(actions, button)
	}
	if a.phoneLayout(a.cx) {
		if items := a.playlistMenu(*pl); len(items) > 0 {
			actions = append(actions, a.phoneIcon(p, "phone-playlist-more", i18n.T("More"), lucideMore, p.muted, func() { a.phonePlaylist = pl.ID; a.openPhoneSheet("playlist") }))
		}
	} else {
		if items := a.playlistDesktopMenu(*pl, false); len(items) > 0 {
			actions = append(actions, ui.Menu{TestID: "playlist-more", Trigger: ui.Button{Label: i18n.T("More"), Variant: ui.ButtonGhost, Side: 34},
				Face: icon(lucideMore, 18, p.muted), Children: items})
		}
	}
	if len(actions) > 0 {
		details = append(details, core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, AlignItems: core.AlignCenter, Gap: 10, Margin: core.Edges{Top: 10}}, Children: actions})
	}
	if a.phoneLayout(a.cx) {
		return core.View{TestID: "playlist-header", Style: core.Style{Gap: 14, Margin: core.Edges{Top: 8, Bottom: 18}}, Children: []core.Node{
			core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 16}, Children: []core.Node{
				core.View{TestID: "playlist-cover", Style: core.Style{Width: core.Pt(112), Height: core.Pt(112), FlexShrink: 0, CornerRadius: 14, Overflow: core.Hidden}, Children: []core.Node{a.cover(p, pl.ImageURL, 112, 14)}},
				core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: 8}, Children: []core.Node{
					text(i18n.T("Playlist"), 11, core.FontWeightMedium, p.muted), core.Text{TestID: "playlist-title", Content: pl.Name, MaxLines: 3, Style: core.Style{FontSize: phoneFontTitle, FontWeight: core.FontWeightSemibold, LineHeightMultiple: 1.2, Color: p.text}}, text(strings.Join(meta, " · "), 12, core.FontWeightNormal, p.muted),
				}},
			}}, core.Show{When: func() bool { return strings.TrimSpace(pl.Description) != "" }, Then: func(*core.Scope) core.Node {
				return core.Text{Content: strings.Join(strings.Fields(pl.Description), " "), MaxLines: 2, Style: core.Style{FontSize: 12, Color: p.muted, LineHeightMultiple: 1.4}}
			}}, core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8}, Children: actions},
		}}
	}

	coverURL := a.playlistCoverURL(*pl)
	var pic core.Node = a.cover(p, coverURL, 212, 14)
	if coverURL == "" {
		pic = core.View{Style: core.Style{Width: core.Pt(212), Height: core.Pt(212), CornerRadius: 14, Background: p.hover,
			Direction: core.Row, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
			Children: []core.Node{icon(lucideList, 44, p.faint)}}
	}
	coverChildren := []core.Node{pic}
	if a.canChangePlaylistCover(*pl) {
		plCopy := *pl
		white := core.RGB(0xffffff)
		coverChildren = append(coverChildren, base.Button{
			TestID: "playlist-cover-edit", Label: i18n.T("Change Cover…"),
			OnPress: func(core.PressEvent) { a.pickPlaylistCover(plCopy) },
			Style: core.Style{
				Position: core.Absolute, Top: core.Pt(0), Right: core.Pt(0), Bottom: core.Pt(0), Left: core.Pt(0),
				CornerRadius: 14, Direction: core.Column, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Gap: 6,
				Background: core.RGBA(0x000000, 0.48), Opacity: core.Alpha(0),
				Transition: core.Transition{Duration: 160 * time.Millisecond, Easing: core.EaseOut, Properties: core.TransitionOpacity},
				GroupStyle: &core.GroupStyle{Name: "playlist-cover", Hover: &core.Style{Opacity: core.Alpha(1)}},
			},
			Children: []core.Node{
				icon(lucideCamera, 24, white),
				text(i18n.T("Change Cover…"), 12.5, core.FontWeightMedium, white),
			},
		})
	}
	cover := core.View{TestID: "playlist-cover", Style: core.Style{Width: core.Pt(212), Height: core.Pt(212), FlexShrink: 0, CornerRadius: 14,
		Group:   "playlist-cover",
		Shadows: []core.BoxShadow{{Offset: core.Point{Y: 18}, Blur: 40, Spread: -10, Color: p.cardShade}}}, Children: coverChildren}

	return core.View{TestID: "playlist-header", Style: core.Style{Direction: core.Row, Gap: 28, AlignItems: core.AlignEnd, Padding: core.Edges{Bottom: 26}}, Children: []core.Node{
		cover,
		core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: 8}, Children: details},
	}}
}
