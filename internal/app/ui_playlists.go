package app

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/HengWh/woodwind-music/internal/i18n"
	"github.com/HengWh/woodwind-music/internal/music"
)

type playlistsUI struct {
	query *core.Signal[string]
	scope string // "", "mine", "saved"
	sort  string // "", "name", "tracks"
	view  string // "", "list"
}

func (u *playlistsUI) reset() {
	u.scope, u.sort = "", ""
	if u.query != nil {
		u.query.Set("")
	}
}

func (u *playlistsUI) hasActiveFilters() bool {
	q := ""
	if u.query != nil {
		q = strings.TrimSpace(u.query.Get())
	}
	return q != "" || u.scope != "" || u.sort != ""
}

func (a *app) playlistsPage(cx *core.Scope, p palette) core.Node {
	if a.playlistsUI.query == nil {
		a.playlistsUI.query = core.NewSignal(a.cx, "")
	}
	return playlistsScreen{a: a, p: p}
}

type playlistsScreen struct {
	a *app
	p palette
}

func (s playlistsScreen) Render(cx *core.Scope) core.Node {
	a, p := s.a, s.p
	searchRef := new(core.Ref)
	return core.View{TestID: "playlists-page", Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Direction: core.Column}, Children: []core.Node{
		core.View{Style: core.Style{FlexShrink: 0, Direction: core.Column, Gap: 12,
			Padding: core.Edges{Top: 14, Right: pagePad + 4, Bottom: 12, Left: pagePad + 4},
			Borders: core.Edges{Bottom: 1}, BorderColor: p.hairline}, Children: []core.Node{
			core.Dynamic(func(cx *core.Scope) core.Node {
				a.rev.Get()
				a.playlistsUI.query.Get()
				return a.playlistsDesktopHeader(p)
			}),
			core.View{TestID: "playlists-filter-bar", Style: core.Style{Direction: core.Row, Wrap: core.Wrap, AlignItems: core.AlignCenter, Gap: 8}, Children: []core.Node{
				a.playlistsSearchField(p, searchRef),
				core.Dynamic(func(cx *core.Scope) core.Node {
					a.rev.Get()
					a.playlistsUI.query.Get()
					return a.playlistsDesktopConditions(p)
				}),
			}},
		}},
		core.Dynamic(func(cx *core.Scope) core.Node {
			a.rev.Get()
			a.playlistsUI.query.Get()
			return a.playlistsBody(cx, p)
		}),
	}}
}

func (a *app) splitPlaylists() (mineCount, savedCount int) {
	for _, pl := range a.mine {
		if pl.Mine {
			mineCount++
		} else {
			savedCount++
		}
	}
	return mineCount, savedCount
}

func (a *app) filteredPlaylists() []music.Playlist {
	u := &a.playlistsUI
	q := ""
	if u.query != nil {
		q = strings.ToLower(strings.TrimSpace(u.query.Get()))
	}
	out := make([]music.Playlist, 0, len(a.mine))
	for _, pl := range a.mine {
		if u.scope == "mine" && !pl.Mine {
			continue
		}
		if u.scope == "saved" && pl.Mine {
			continue
		}
		if q != "" {
			haystack := strings.ToLower(pl.Name + " " + pl.Owner + " " + pl.Description)
			if !strings.Contains(haystack, q) {
				continue
			}
		}
		out = append(out, pl)
	}
	switch u.sort {
	case "name":
		slices.SortStableFunc(out, func(x, y music.Playlist) int {
			return cmp.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
		})
	case "tracks":
		slices.SortStableFunc(out, func(x, y music.Playlist) int {
			if c := cmp.Compare(y.TrackCount, x.TrackCount); c != 0 {
				return c
			}
			return cmp.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
		})
	}
	return out
}

func (a *app) playlistsSummaryText(filteredCount int) string {
	total := len(a.mine)
	if a.playlistsBusy && total == 0 {
		return i18n.T("Loading…")
	}
	if a.playlistsUI.hasActiveFilters() && total > 0 {
		return i18n.Tf("Showing %d of %d playlists", filteredCount, total)
	}
	mineCount, savedCount := a.splitPlaylists()
	if mineCount > 0 && savedCount > 0 {
		return i18n.Tf("%d playlists · %d created · %d saved", total, mineCount, savedCount)
	}
	return i18n.Tf("%d playlists", total)
}

func (a *app) playlistsDesktopHeader(p palette) core.Node {
	filtered := a.filteredPlaylists()
	heading := []core.Node{
		core.Text{TestID: "playlists-summary", Content: a.playlistsSummaryText(len(filtered)),
			Style: core.Style{FontSize: 12, FontWeight: core.FontWeightSemibold, LetterSpacing: 0.4, Color: p.muted}},
		core.Text{Content: i18n.T("Playlists"),
			Style: core.Style{FontSize: 30, FontWeight: core.FontWeightBold, LetterSpacing: -0.6, Color: p.text}},
	}
	var actions []core.Node
	if a.can("createPlaylist") {
		actions = append(actions, a.storedPillButton(p, "new-playlist", lucidePlus, i18n.T("New Playlist"), true, false, false, a.newPlaylist))
	}
	if a.source() != a.local {
		actions = append(actions, a.storedPillButton(p, "playlists-refresh", lucideRefreshCw, i18n.T("Refresh"), false, false, a.playlistsBusy, a.reload))
	}
	return core.View{TestID: "playlists-header", Style: core.Style{Direction: core.Row, Wrap: core.Wrap, AlignItems: core.AlignEnd, JustifyContent: core.JustifySpaceBetween, Gap: 12}, Children: []core.Node{
		core.View{Style: core.Style{FlexShrink: 1, MinWidth: core.Pt(0), Gap: 4}, Children: heading},
		core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, AlignItems: core.AlignCenter, Gap: 8}, Children: actions},
	}}
}

func (a *app) playlistsSearchField(p palette, ref *core.Ref) core.Node {
	boxStyle := core.Style{
		Width: core.Pt(220), Height: core.Pt(32), FlexShrink: 0,
		Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6,
		Padding: core.Symmetric(0, 8), CornerRadius: 8, Background: p.hover,
		BorderWidth: 1, BorderColor: p.hairline,
		FocusWithin: &core.Style{Background: p.bg, BorderColor: p.accent, OutlineWidth: 3, OutlineColor: ui.Alpha(p.accent, 0.25)},
	}
	return core.View{TestID: "playlists-search-box", OnPress: func(core.PressEvent) { ref.Focus() }, Style: boxStyle, Children: []core.Node{
		icon(lucideSearch, 13.5, p.muted),
		core.Input{
			Ref: ref, TestID: "playlists-search",
			Label: i18n.T("Search playlists"), Placeholder: i18n.T("Search playlists"),
			Value: a.playlistsUI.query.Get, NoAutoCorrect: true,
			OnChange: func(v string) { a.playlistsUI.query.Set(v) },
			Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Height: core.Pt(24),
				Background: core.Color(0), Color: p.text, PlaceholderColor: p.faint, FontSize: 12.5,
				BorderWidth: 0, OutlineWidth: 0},
		},
		core.Show{When: func() bool { return a.playlistsUI.query.Get() != "" }, Then: func(*core.Scope) core.Node {
			return base.Button{TestID: "playlists-search-clear", Label: i18n.T("Clear Search"), NoFocus: true,
				OnPress: func(core.PressEvent) { a.playlistsUI.query.Set("") },
				Style: core.Style{Width: core.Pt(18), Height: core.Pt(18), FlexShrink: 0, Direction: core.Row,
					AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, CornerRadius: 9, Background: p.faint},
				Children: []core.Node{icon(lucideX, 10, p.bg)}}
		}},
	}}
}

func (a *app) playlistsDesktopConditions(p palette) core.Node {
	u := &a.playlistsUI
	var segments []core.Node
	for _, it := range []struct{ id, value, label string }{
		{"all", "", i18n.T("All")},
		{"mine", "mine", i18n.T("My Playlists")},
		{"saved", "saved", i18n.T("Saved Playlists")},
	} {
		it := it
		selected := u.scope == it.value
		bg, fg := core.Color(0), p.muted
		weight := core.FontWeightNormal
		if selected {
			bg, fg, weight = p.bg, p.text, core.FontWeightMedium
		}
		segments = append(segments, base.Button{
			TestID: "playlists-tab-" + it.id, Label: it.label, Selected: func() bool { return selected },
			OnPress: func(core.PressEvent) { u.scope = it.value; a.bump() },
			Style: core.Style{Height: core.Pt(26), Padding: core.Symmetric(0, 10), CornerRadius: 6,
				Direction: core.Row, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
				Background: bg, Hover: &core.Style{Background: p.sidebar}},
			Children: []core.Node{text(it.label, 12.5, weight, fg)},
		})
	}
	scopeBar := core.View{Style: core.Style{Height: core.Pt(32), Direction: core.Row, AlignItems: core.AlignCenter,
		Gap: 2, Padding: core.All(2), Background: p.hover, BorderWidth: 1, BorderColor: p.hairline, CornerRadius: 8},
		Children: segments}

	viewBtn := func(id, value, label string, svg *core.SVG) core.Node {
		selected := u.view == value
		bg, fg := core.Color(0), p.muted
		if selected {
			bg, fg = p.bg, p.text
		}
		return base.Button{
			TestID: id, Label: label, Selected: func() bool { return selected },
			OnPress: func(core.PressEvent) { u.view = value; a.bump() },
			Style: core.Style{Width: core.Pt(28), Height: core.Pt(26), CornerRadius: 6,
				Direction: core.Row, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
				Background: bg, Hover: &core.Style{Background: p.sidebar}},
			Children: []core.Node{icon(svg, 14, fg)},
		}
	}
	viewBar := core.View{Style: core.Style{Height: core.Pt(32), Direction: core.Row, AlignItems: core.AlignCenter,
		Gap: 2, Padding: core.All(2), Background: p.hover, BorderWidth: 1, BorderColor: p.hairline, CornerRadius: 8},
		Children: []core.Node{
			viewBtn("playlists-view-grid", "", i18n.T("Grid View"), lucideLayoutGrid),
			viewBtn("playlists-view-list", "list", i18n.T("List View"), lucideListRows),
		}}

	nodes := []core.Node{
		scopeBar,
		a.playlistsSortSelect(p, []ui.Option[string]{
			{Value: "", Label: i18n.T("Default Order")},
			{Value: "name", Label: i18n.T("Name")},
			{Value: "tracks", Label: i18n.T("Most Songs")},
		}),
		viewBar,
	}
	if u.hasActiveFilters() {
		nodes = append(nodes, base.Button{TestID: "playlists-filter-reset", Label: i18n.T("Reset"),
			OnPress: func(core.PressEvent) { u.reset(); a.bump() },
			Style: core.Style{Height: core.Pt(32), Padding: core.Symmetric(0, 10), Gap: 5, Direction: core.Row,
				AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, CornerRadius: 8,
				Hover: &core.Style{Background: p.hover}},
			Children: []core.Node{icon(lucideRotateCcw, 13, p.muted), text(i18n.T("Reset"), 12.5, core.FontWeightMedium, p.muted)}})
	}
	return core.Group(nodes)
}

func (a *app) playlistsSortSelect(p palette, options []ui.Option[string]) core.Node {
	u := &a.playlistsUI
	chosen := 0
	for i, o := range options {
		if o.Value == u.sort {
			chosen = i
			break
		}
	}
	active := u.sort != ""
	bg, border, fg := p.hover, p.hairline, p.text
	if active {
		bg = p.accent.Faded(0.10)
		border = p.accent.Faded(0.32)
		fg = p.accent
	}
	return base.Select{
		Label: i18n.T("Sort"), TestID: "playlists-sort",
		Items: func() []base.SelectItem {
			items := make([]base.SelectItem, len(options))
			for i, o := range options {
				items[i] = base.SelectItem{Value: strconv.Itoa(i), Title: o.Label}
			}
			return items
		},
		Value: func() *string {
			v := strconv.Itoa(chosen)
			return &v
		},
		OnChange: func(v *string) {
			if v != nil {
				if i, err := strconv.Atoi(*v); err == nil && i >= 0 && i < len(options) {
					u.sort = options[i].Value
					a.bump()
				}
			}
		},
		RootStyle: core.Style{FlexShrink: 0},
		Style: core.Style{Height: core.Pt(32), BorderWidth: 1, BorderColor: border, CornerRadius: 8,
			Background: bg, Hover: &core.Style{Background: p.press},
			FocusVisible: &core.Style{OutlineWidth: 3, OutlineColor: ui.Alpha(p.accent, .25)}},
		Trigger: core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter,
			Padding: core.Symmetric(0, 10), Gap: 6}, Children: []core.Node{
			core.Text{Content: options[chosen].Label, MaxLines: 1, Style: core.Style{FontSize: 12.5, FontWeight: core.FontWeightMedium, Color: fg}},
			icon(lucideChevronDown, 11, fg),
		}},
		PopupStyle: core.Style{MinWidth: core.Pt(150), Background: p.sidebar, BorderWidth: 1, BorderColor: p.hairline,
			CornerRadius: 10, Padding: core.All(4), ShadowColor: p.cardShade, ShadowRadius: 16, ShadowOffset: core.Point{Y: 6}},
		ListStyle: core.Style{MaxHeight: core.Pt(240)}, Margin: 8,
		ItemStyle: core.Style{MinHeight: core.Pt(30), Padding: core.Symmetric(4, 8), CornerRadius: 6,
			Focus: &core.Style{Background: p.accent, Color: p.onAccent}, Color: p.text},
		RenderItem: func(e base.SelectEntry) core.Node {
			checked := e.Index == chosen
			return core.View{TestID: "playlists-sort-" + strconv.Itoa(e.Index),
				Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 7}, Children: []core.Node{
					core.View{Style: core.Style{Width: core.Pt(12)}, Children: []core.Node{
						core.Show{When: func() bool { return checked }, Then: func(*core.Scope) core.Node {
							return core.Icon{SVG: lucideCheck, Style: core.Style{Width: core.Pt(12), Height: core.Pt(12)}}
						}},
					}},
					core.Text{Content: e.Title, MaxLines: 1, Style: core.Style{FontSize: 13}},
				}}
		},
	}
}

func (a *app) playlistsBody(cx *core.Scope, p palette) core.Node {
	if a.playlistsBusy && len(a.mine) == 0 {
		return a.loadingPage(p)
	}
	if a.playlistsErr != "" && len(a.mine) == 0 {
		return core.View{Style: core.Style{FlexGrow: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Padding: core.All(40), Gap: 10}, Children: []core.Node{
			text(i18n.T("Couldn't refresh your playlists"), 18, core.FontWeightSemibold, p.text),
			core.Text{Content: a.playlistsErr, Style: core.Style{MaxWidth: core.Pt(420), FontSize: 13.5, Color: p.muted, TextAlign: core.TextAlignCenter}},
			a.storedPillButton(p, "playlists-retry", lucideRefreshCw, i18n.T("Retry"), true, false, false, a.reload),
		}}
	}
	if len(a.mine) == 0 {
		var action core.Node = core.Group{}
		if a.can("createPlaylist") {
			action = a.storedPillButton(p, "playlists-empty-new", lucidePlus, i18n.T("New Playlist"), true, false, false, a.newPlaylist)
		}
		return core.View{Style: core.Style{FlexGrow: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Padding: core.All(40), Gap: 10}, Children: []core.Node{
			core.View{Style: core.Style{Width: core.Pt(64), Height: core.Pt(64), CornerRadius: 18, Background: p.sidebar,
				Direction: core.Row, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Margin: core.Edges{Bottom: 6}},
				Children: []core.Node{icon(lucideList, 28, p.muted)}},
			text(i18n.T("Make room for your favorites"), 18, core.FontWeightSemibold, p.text),
			core.Text{Content: i18n.T("Create a playlist for your favorite moments"), Style: core.Style{MaxWidth: core.Pt(380), FontSize: 13.5, Color: p.muted, TextAlign: core.TextAlignCenter}},
			action,
		}}
	}

	filtered := a.filteredPlaylists()
	var body []core.Node
	if a.playlistsErr != "" {
		body = append(body, core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, JustifyContent: core.JustifySpaceBetween,
			Gap: 12, Padding: core.Symmetric(10, 14), Margin: core.Edges{Bottom: 14}, CornerRadius: 10, Background: p.sidebar, BorderWidth: 1, BorderColor: p.hairline}, Children: []core.Node{
			core.Text{Content: a.playlistsErr, MaxLines: 2, Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), FontSize: 12.5, Color: p.muted}},
			a.storedPillButton(p, "playlists-retry", lucideRefreshCw, i18n.T("Retry"), false, false, false, a.reload),
		}})
	}
	if len(filtered) == 0 {
		body = append(body, core.View{Style: core.Style{AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Padding: core.Symmetric(64, 24), Gap: 10}, Children: []core.Node{
			text(i18n.T("No matching playlists"), 17, core.FontWeightSemibold, p.text),
			core.Text{Content: i18n.T("Try a different keyword or filter."), Style: core.Style{FontSize: 13, Color: p.muted}},
			a.storedPillButton(p, "playlists-empty-reset", lucideRotateCcw, i18n.T("Reset"), false, false, false, func() { a.playlistsUI.reset(); a.bump() }),
		}})
	} else if a.playlistsUI.view == "list" {
		var rows []core.Node
		for _, pl := range filtered {
			rows = append(rows, a.playlistListRow(p, pl))
		}
		body = append(body, core.View{TestID: "playlists-list", Style: core.Style{Direction: core.Column, Gap: 4}, Children: rows})
	} else {
		var cards []core.Node
		for _, pl := range filtered {
			cards = append(cards, a.playlistOverviewCard(p, pl))
		}
		body = append(body, core.View{TestID: "playlists-grid", Style: core.Style{Display: core.DisplayGrid, Width: core.Pct(100),
			GridTemplateColumns: "repeat(auto-fill, minmax(172px, 1fr))",
			Gap:                 20, RowGap: 26, Margin: core.Edges{Top: 4}}, Children: cards})
	}

	return core.Scroll{TestID: "playlists-scroll", Style: core.Style{FlexGrow: 1, MinHeight: core.Pt(0)}, Children: []core.Node{
		core.View{Style: core.Style{Direction: core.Column, Padding: core.Edges{Top: 18, Right: pagePad + 4, Bottom: 48, Left: pagePad + 4}}, Children: body},
	}}
}

func (a *app) playlistMetaLine(pl music.Playlist) string {
	var meta []string
	if pl.TrackCount > 0 || pl.Mine {
		meta = append(meta, i18n.Songs(pl.TrackCount))
	}
	if pl.Owner != "" {
		meta = append(meta, pl.Owner)
	} else if !pl.Mine {
		meta = append(meta, i18n.T("Saved"))
	}
	return strings.Join(meta, " · ")
}

func (a *app) playlistOverviewCard(p palette, pl music.Playlist) core.Node {
	id := pl.ID
	coverURL := a.playlistCoverURL(pl)
	art := core.View{Style: core.Style{Width: core.Pct(100), Height: core.Pct(100)}, Children: []core.Node{
		a.coverArt(p, coverURL, 12, lucideList),
		a.playlistCoverPlayButton(p, "playlist-play-"+id, func() { a.playPlaylist(id) }),
	}}
	coverBtn := base.Button{TestID: "playlist-" + id, Label: pl.Name,
		OnPress: func(core.PressEvent) { a.open(route{page: pagePlaylist, id: id}) },
		Style:   core.Style{Direction: core.Column, AlignItems: core.AlignStretch, Group: "card"},
		Children: []core.Node{
			a.cardBox(p, 12, art),
		}}

	titleBtn := base.Button{Label: pl.Name,
		OnPress: func(core.PressEvent) { a.open(route{page: pagePlaylist, id: id}) },
		Style:   core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Direction: core.Column, Gap: 2},
		Children: []core.Node{
			core.Text{Content: pl.Name, MaxLines: 2, Style: core.Style{FontSize: 13.5, FontWeight: core.FontWeightMedium, LineHeightMultiple: 1.3, Color: p.text}},
			core.Text{Content: a.playlistMetaLine(pl), MaxLines: 1, Style: core.Style{FontSize: 12, Color: p.muted}},
		}}

	headerRowChildren := []core.Node{titleBtn}
	menuItems := a.playlistDesktopMenu(pl, true)
	if len(menuItems) > 0 {
		headerRowChildren = append(headerRowChildren, ui.Menu{
			TestID:   "playlist-more-" + id,
			Trigger:  ui.Button{Label: i18n.T("More"), Variant: ui.ButtonGhost, Side: 28},
			Face:     icon(lucideMore, 15, p.muted),
			Children: menuItems,
		})
	}

	children := []core.Node{
		coverBtn,
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignStart, Gap: 4}, Children: headerRowChildren},
	}
	if desc := strings.Join(strings.Fields(strings.TrimSpace(pl.Description)), " "); desc != "" {
		children = append(children, core.Text{Content: desc, MaxLines: 1, Style: core.Style{FontSize: 11.5, Color: p.faint, Margin: core.Edges{Top: -4}}})
	}
	card := core.View{Style: core.Style{Direction: core.Column, Gap: 9, AlignItems: core.AlignStretch}, Children: children}
	if len(menuItems) == 0 {
		return card
	}
	return ui.ContextMenu{Trigger: card, Children: menuItems}
}

func (a *app) playlistListRow(p palette, pl music.Playlist) core.Node {
	id := pl.ID
	coverURL := a.playlistCoverURL(pl)
	tagLabel := i18n.T("My Playlists")
	tagBg, tagFg := p.hover, p.muted
	if !pl.Mine {
		tagLabel = i18n.T("Saved")
		tagBg, tagFg = p.accent.Faded(0.10), p.accent
	}

	infoChildren := []core.Node{
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8}, Children: []core.Node{
			core.Text{Content: pl.Name, MaxLines: 1, Style: core.Style{FlexShrink: 1, MinWidth: core.Pt(0), FontSize: 14, FontWeight: core.FontWeightMedium, Color: p.text}},
			core.View{Style: core.Style{FlexShrink: 0, Padding: core.Symmetric(2, 7), CornerRadius: 999, Background: tagBg}, Children: []core.Node{
				text(tagLabel, 11, core.FontWeightMedium, tagFg),
			}},
		}},
		core.Text{Content: a.playlistMetaLine(pl), MaxLines: 1, Style: core.Style{FontSize: 12, Color: p.muted}},
	}
	if desc := strings.Join(strings.Fields(strings.TrimSpace(pl.Description)), " "); desc != "" {
		infoChildren = append(infoChildren, core.Text{Content: desc, MaxLines: 1, Style: core.Style{FontSize: 12, Color: p.faint}})
	}

	mainBtn := base.Button{TestID: "playlist-" + id, Label: pl.Name,
		OnPress: func(core.PressEvent) { a.open(route{page: pagePlaylist, id: id}) },
		Style:   core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Direction: core.Row, AlignItems: core.AlignCenter, Gap: 14, Padding: core.Symmetric(8, 10), CornerRadius: 10},
		Children: []core.Node{
			core.View{Style: core.Style{Width: core.Pt(52), Height: core.Pt(52), FlexShrink: 0, CornerRadius: 10, Overflow: core.Hidden}, Children: []core.Node{
				a.coverArt(p, coverURL, 10, lucideList),
			}},
			core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: 3}, Children: infoChildren},
		}}

	actions := []core.Node{
		base.Button{TestID: "playlist-play-" + id, Label: i18n.T("Play"),
			OnPress: func(core.PressEvent) { a.playPlaylist(id) },
			Style: core.Style{Width: core.Pt(32), Height: core.Pt(32), CornerRadius: 16,
				Direction: core.Row, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
				Hover: &core.Style{Background: p.press}},
			Children: []core.Node{icon(lucidePlay, 14, p.text)}},
	}
	if a.canChangePlaylistCover(pl) {
		plCopy := pl
		actions = append(actions, base.Button{TestID: "playlist-cover-btn-" + id, Label: i18n.T("Change Cover…"),
			OnPress: func(core.PressEvent) { a.pickPlaylistCover(plCopy) },
			Style: core.Style{Width: core.Pt(32), Height: core.Pt(32), CornerRadius: 16,
				Direction: core.Row, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
				Hover: &core.Style{Background: p.press}},
			Children: []core.Node{icon(lucideImage, 15, p.muted)}})
	}
	menuItems := a.playlistDesktopMenu(pl, true)
	if len(menuItems) > 0 {
		actions = append(actions, ui.Menu{
			TestID:   "playlist-more-" + id,
			Trigger:  ui.Button{Label: i18n.T("More"), Variant: ui.ButtonGhost, Side: 32},
			Face:     icon(lucideMore, 16, p.muted),
			Children: menuItems,
		})
	}

	row := core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, Padding: core.Edges{Right: 8},
		CornerRadius: 10, Hover: &core.Style{Background: p.hover}}, Children: []core.Node{
		mainBtn,
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 2, FlexShrink: 0}, Children: actions},
	}}
	if len(menuItems) == 0 {
		return row
	}
	return ui.ContextMenu{Trigger: row, Children: menuItems}
}

func (a *app) playlistCoverPlayButton(p palette, id string, play func()) core.Node {
	white := core.RGB(0xffffff)
	return core.View{Style: core.Style{Position: core.Absolute, Bottom: core.Pt(10), Right: core.Pt(10),
		Opacity: core.Alpha(0), Transform: core.Transform{Scale: 0.8, TranslateY: 6},
		Transition: core.Transition{Duration: 180 * time.Millisecond, Easing: core.EaseOut,
			Properties: core.TransitionTransform | core.TransitionOpacity},
		GroupStyle: &core.GroupStyle{Name: "card", Hover: &core.Style{Opacity: core.Alpha(1),
			Transform: core.Transform{Scale: 1}}}},
		Children: []core.Node{base.Button{TestID: id, Label: i18n.T("Play"), OnPress: func(core.PressEvent) { play() },
			Style: core.Style{Width: core.Pt(38), Height: core.Pt(38), Direction: core.Row, AlignItems: core.AlignCenter,
				JustifyContent: core.JustifyCenter, CornerRadius: 999, Background: p.accent,
				ShadowColor: core.RGBA(0x000000, 0.3), ShadowRadius: 12, ShadowOffset: core.Point{Y: 4},
				Hover: &core.Style{Background: blend(p.accent, white, 0.15)}},
			Children: []core.Node{icon(lucidePlay, 15, white)}}}}
}
