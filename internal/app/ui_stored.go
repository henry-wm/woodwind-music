package app

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/HengWh/woodwind-music/internal/i18n"
	"github.com/HengWh/woodwind-music/internal/music"
	"github.com/HengWh/woodwind-music/internal/offline"
)

type storedUI struct {
	query                   *core.Signal[string]
	mode, sort, view, group string
	selecting               bool
	selected                map[string]bool
	offset                  core.Point
	tasks                   bool
	details                 string
	deleteIDs               []string
}

func (a *app) openStored() { a.open(route{page: pageStored}) }
func (a *app) storedWork(run func() error) {
	go func() {
		err := run()
		a.post(func() {
			if err != nil {
				a.say(err.Error())
			}
			a.bump()
		})
	}()
}
func (a *app) pinTracks(tracks []music.Track, q string) {
	if a.disk == nil {
		return
	}
	copies := slices.Clone(tracks)
	source := a.source().Info().ID
	quality := q
	defaultQuality := a.set.Quality
	for i := range copies {
		if copies[i].Source == "" && !copies[i].LocalOnly {
			copies[i].Source = source
		}
	}
	a.storedWork(func() error {
		var failures []error
		for _, t := range copies {
			target := quality
			if target == "" && !a.disk.Has(t) {
				target = defaultQuality
			}
			if err := a.disk.Pin(t, target, false); err != nil {
				failures = append(failures, err)
			}
		}
		if len(failures) == 0 {
			a.post(func() { a.say(i18n.T("Offline requests added. View progress in Saved Music.")) })
		}
		return errors.Join(failures...)
	})
}
func (a *app) storedTracks(records []offline.Record) []music.Track {
	tracks := make([]music.Track, len(records))
	for i, r := range records {
		tracks[i] = a.disk.Track(r)
	}
	return tracks
}
func (a *app) selectedStored(records []offline.Record) []offline.Record {
	var out []offline.Record
	for _, r := range records {
		if a.diskUI.selected[r.ID] {
			out = append(out, r)
		}
	}
	return out
}
func (a *app) storedFilter(records []offline.Record) []offline.Record {
	u := &a.diskUI
	q := ""
	if u.query != nil {
		q = strings.ToLower(strings.TrimSpace(u.query.Get()))
	}
	var out []offline.Record
	for _, r := range records {
		if u.mode == "pinned" && !r.Pinned() || u.mode == "automatic" && r.Pinned() {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(r.Track.Name+" "+r.Track.ArtistNames()+" "+r.Track.Album.Name), q) {
			continue
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b offline.Record) int {
		switch u.sort {
		case "title":
			return strings.Compare(strings.ToLower(a.Track.Name), strings.ToLower(b.Track.Name))
		case "artist":
			return strings.Compare(a.Track.ArtistNames(), b.Track.ArtistNames())
		case "size":
			if a.Size() > b.Size() {
				return -1
			}
			if a.Size() < b.Size() {
				return 1
			}
		case "played":
			if a.Played > b.Played {
				return -1
			}
			if a.Played < b.Played {
				return 1
			}
		default:
			if a.Created > b.Created {
				return -1
			}
			if a.Created < b.Created {
				return 1
			}
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func (a *app) storedVisible(all []offline.Record) ([]offline.Record, *storedGroup) {
	u := &a.diskUI
	records := a.storedFilter(all)
	if u.view == "" || u.group == "" {
		return records, nil
	}
	for _, g := range a.storedGroupsWithArt(records, u.view) {
		if g.key == u.group {
			copy := g
			return copy.records, &copy
		}
	}
	return nil, nil
}

func (a *app) storedHasActiveFilters() bool {
	u := &a.diskUI
	q := ""
	if u.query != nil {
		q = strings.TrimSpace(u.query.Get())
	}
	return q != "" || u.mode != "" || u.view != "" || u.sort != ""
}

func (a *app) storedActiveTaskCount() int {
	if a.disk == nil {
		return 0
	}
	n := 0
	for _, t := range a.disk.Tasks() {
		if t.State != "saved" {
			n++
		}
	}
	return n
}

func (a *app) storedPage(cx *core.Scope, p palette) core.Node {
	if a.disk == nil {
		return a.emptyNote(p, i18n.T("Saved Music"), a.diskErr)
	}
	if a.diskUI.query == nil {
		a.diskUI.query = core.NewSignal(a.cx, "")
	}
	if a.diskUI.selected == nil {
		a.diskUI.selected = map[string]bool{}
	}
	return storedScreen{a: a, p: p}
}

type storedScreen struct {
	a *app
	p palette
}

func (s storedScreen) Render(cx *core.Scope) core.Node {
	a := s.a
	phone := a.phoneLayout(cx)
	searchRef := new(core.Ref)
	if phone {
		return s.renderPhone(cx, searchRef)
	}
	return s.renderDesktop(cx, searchRef)
}

func (s storedScreen) renderDesktop(cx *core.Scope, searchRef *core.Ref) core.Node {
	a, p := s.a, s.p
	return core.View{TestID: "stored-page", Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Direction: core.Column}, Children: []core.Node{
		core.View{Style: core.Style{FlexShrink: 0, Direction: core.Column, Gap: 12,
			Padding: core.Edges{Top: 14, Right: pagePad + 4, Bottom: 12, Left: pagePad + 4},
			Borders: core.Edges{Bottom: 1}, BorderColor: p.hairline}, Children: []core.Node{
			core.Dynamic(func(cx *core.Scope) core.Node {
				a.rev.Get()
				a.diskRev.Get()
				a.diskUI.query.Get()
				return a.storedDesktopHeader(p)
			}),
			core.View{TestID: "stored-filter-bar", StyleFunc: func() core.Style {
				a.rev.Get()
				st := core.Style{Direction: core.Row, Wrap: core.Wrap, AlignItems: core.AlignCenter, Gap: 8}
				if a.diskUI.tasks {
					st.Display = core.DisplayNone
				}
				return st
			}, Children: []core.Node{
				a.storedSearchField(p, searchRef, false),
				core.Dynamic(func(cx *core.Scope) core.Node {
					a.rev.Get()
					a.diskUI.query.Get()
					return a.storedDesktopConditions(p)
				}),
			}},
			core.Dynamic(func(cx *core.Scope) core.Node {
				a.rev.Get()
				a.diskRev.Get()
				a.diskUI.query.Get()
				return a.storedPinnedBatchBar(p, false)
			}),
		}},
		core.Dynamic(func(cx *core.Scope) core.Node {
			a.rev.Get()
			a.diskRev.Get()
			a.diskUI.query.Get()
			return a.storedBody(cx, p, false)
		}),
	}}
}

func (s storedScreen) renderPhone(cx *core.Scope, searchRef *core.Ref) core.Node {
	a, p := s.a, s.p
	return core.View{TestID: "stored-page", Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Direction: core.Column}, Children: []core.Node{
		core.View{TestID: "stored-filter-bar", Style: core.Style{FlexShrink: 0, Direction: core.Column, Gap: 8,
			Padding: core.Edges{Top: 4, Right: 16, Bottom: 8, Left: 16},
			Borders: core.Edges{Bottom: 1}, BorderColor: p.hairline}, Children: []core.Node{
			core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6}, Children: []core.Node{
				a.storedSearchField(p, searchRef, true),
				core.Dynamic(func(cx *core.Scope) core.Node {
					a.rev.Get()
					a.diskRev.Get()
					a.diskUI.query.Get()
					return a.storedPhoneBarActions(p)
				}),
			}},
			core.View{StyleFunc: func() core.Style {
				a.rev.Get()
				st := core.Style{Direction: core.Row}
				if a.diskUI.tasks {
					st.Display = core.DisplayNone
				}
				return st
			}, Children: []core.Node{
				core.Dynamic(func(cx *core.Scope) core.Node {
					a.rev.Get()
					a.diskUI.query.Get()
					return a.storedPhoneConditions(p)
				}),
			}},
			core.Dynamic(func(cx *core.Scope) core.Node {
				a.rev.Get()
				a.diskRev.Get()
				a.diskUI.query.Get()
				return a.storedPinnedBatchBar(p, true)
			}),
		}},
		core.Dynamic(func(cx *core.Scope) core.Node {
			a.rev.Get()
			a.diskRev.Get()
			a.diskUI.query.Get()
			return a.storedBody(cx, p, true)
		}),
	}}
}

// storedSearchField is the compact keyword input shared by desktop and phone
// inside the unified conditional search bar.
func (a *app) storedSearchField(p palette, ref *core.Ref, phone bool) core.Node {
	height := core.Points(32)
	inputHeight := core.Points(24)
	fontSize := core.Points(12.5)
	radius := core.Points(8)
	bg := p.hover
	iconSize := core.Points(13.5)
	boxStyle := core.Style{
		Width: core.Pt(220), Height: core.Pt(height), FlexShrink: 0,
		Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6,
		Padding: core.Symmetric(0, 8), CornerRadius: radius, Background: bg,
		BorderWidth: 1, BorderColor: p.hairline,
		FocusWithin: &core.Style{Background: p.bg, BorderColor: p.accent, OutlineWidth: 3, OutlineColor: ui.Alpha(p.accent, 0.25)},
	}
	if phone {
		height = 38
		inputHeight = 32
		fontSize = phoneFontBody
		radius = 10
		bg = p.sidebar
		iconSize = 15
		boxStyle = core.Style{
			FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Height: core.Pt(height),
			Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6,
			Padding: core.Edges{Left: 10, Right: 6}, CornerRadius: radius, Background: bg,
			BorderWidth: 1, BorderColor: p.hairline,
		}
	}
	return core.View{TestID: "stored-search-box", OnPress: func(core.PressEvent) { ref.Focus() }, Style: boxStyle, Children: []core.Node{
		icon(lucideSearch, iconSize, p.muted),
		core.Input{
			Ref: ref, TestID: "stored-search",
			Label: i18n.T("Search saved music"), Placeholder: i18n.T("Search saved music"),
			Value: a.diskUI.query.Get, NoAutoCorrect: true,
			OnChange: func(v string) { a.diskUI.resetBrowse(); a.diskUI.query.Set(v) },
			Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Height: core.Pt(inputHeight),
				Background: core.Color(0), Color: p.text, PlaceholderColor: p.faint, FontSize: fontSize,
				BorderWidth: 0, OutlineWidth: 0},
		},
		core.Show{When: func() bool { return a.diskUI.query.Get() != "" }, Then: func(*core.Scope) core.Node {
			return base.Button{TestID: "stored-search-clear", Label: i18n.T("Clear Search"), NoFocus: true,
				OnPress: func(core.PressEvent) { a.diskUI.resetBrowse(); a.diskUI.query.Set("") },
				Style: core.Style{Width: core.Pt(18), Height: core.Pt(18), FlexShrink: 0, Direction: core.Row,
					AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, CornerRadius: 9, Background: p.faint},
				Children: []core.Node{icon(lucideX, 10, p.bg)}}
		}},
	}}
}

func (a *app) storedDesktopHeader(p palette) core.Node {
	u := &a.diskUI
	all := a.disk.List()
	records, chosen := a.storedVisible(all)
	var size int64
	for _, r := range records {
		size += r.Size()
	}
	tracks := a.storedTracks(records)
	showingSongs := !u.tasks && (u.view == "" || u.group != "")

	var heading []core.Node
	if !u.tasks && u.view != "" && u.group != "" {
		heading = append(heading, base.Button{TestID: "stored-group-back", Label: i18n.T("Back"),
			OnPress: func(core.PressEvent) { u.group = ""; u.resetBrowse(); a.bump() },
			Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, AlignSelf: core.AlignStart,
				Padding: core.Symmetric(3, 8), Margin: core.Edges{Left: -8, Bottom: 2}, CornerRadius: 6,
				Hover: &core.Style{Background: p.hover}},
			Children: []core.Node{icon(lucideChevronLeft, 14, p.muted), text(i18n.T("Back"), 12.5, core.FontWeightMedium, p.muted)}})
		groupTitle := i18n.T("Saved Music")
		if chosen != nil {
			groupTitle = chosen.name
		}
		heading = append(heading,
			core.Text{Content: i18n.Tf("%d songs available on this device", len(records)) + " · " + bytesLabel(size),
				Style: core.Style{FontSize: 12, FontWeight: core.FontWeightSemibold, LetterSpacing: 0.4, Color: p.muted}},
			core.Text{Content: groupTitle, MaxLines: 1,
				Style: core.Style{FontSize: 28, FontWeight: core.FontWeightBold, LetterSpacing: -0.6, Color: p.text}})
	} else if u.tasks {
		heading = append(heading,
			core.Text{Content: i18n.T("Saved Music"),
				Style: core.Style{FontSize: 12, FontWeight: core.FontWeightSemibold, LetterSpacing: 0.4, Color: p.muted}},
			core.Text{Content: i18n.T("Download Tasks"),
				Style: core.Style{FontSize: 30, FontWeight: core.FontWeightBold, LetterSpacing: -0.6, Color: p.text}})
	} else {
		heading = append(heading,
			core.Text{Content: i18n.Tf("%d songs available on this device", len(records)) + " · " + bytesLabel(size),
				Style: core.Style{FontSize: 12, FontWeight: core.FontWeightSemibold, LetterSpacing: 0.4, Color: p.muted}},
			core.Text{Content: i18n.T("Saved Music"),
				Style: core.Style{FontSize: 30, FontWeight: core.FontWeightBold, LetterSpacing: -0.6, Color: p.text}})
	}

	var actions []core.Node
	if showingSongs {
		actions = append(actions,
			a.storedPillButton(p, "stored-play-all", lucidePlay, i18n.T("Play"), true, false, len(records) == 0, func() { a.playTracks(tracks, 0) }),
			a.storedPillButton(p, "stored-shuffle", lucideShuffle, i18n.T("Shuffle"), false, false, len(records) == 0, func() { a.pl.PlayShuffled(tracks); a.heartbeat() }),
		)
		selectLabel := i18n.T("Select")
		if u.selecting {
			selectLabel = i18n.T("Done")
		}
		actions = append(actions, a.storedPillButton(p, "stored-select-mode", lucideListChecks, selectLabel, false, u.selecting, len(records) == 0 && !u.selecting, func() {
			u.selecting = !u.selecting
			if !u.selecting {
				u.selected = map[string]bool{}
			}
			a.bump()
		}))
	}
	taskLabelText := i18n.T("Download Tasks")
	if active := a.storedActiveTaskCount(); active > 0 {
		taskLabelText = fmt.Sprintf("%s (%d)", taskLabelText, active)
	}
	actions = append(actions, a.storedPillButton(p, "stored-tasks", lucideDownload, taskLabelText, false, u.tasks, false, func() {
		u.tasks = !u.tasks
		u.resetBrowse()
		a.bump()
	}))

	return core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, AlignItems: core.AlignEnd, JustifyContent: core.JustifySpaceBetween, Gap: 12}, Children: []core.Node{
		core.View{Style: core.Style{FlexShrink: 1, MinWidth: core.Pt(0), Gap: 4}, Children: heading},
		core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, AlignItems: core.AlignCenter, Gap: 8}, Children: actions},
	}}
}

func (a *app) storedPillButton(p palette, id string, svg *core.SVG, label string, primary, active, disabled bool, onPress func()) core.Node {
	bg, fg := p.hover, p.text
	hoverBg := p.press
	if primary {
		bg, fg = p.accent, p.onAccent
		hoverBg = blend(p.accent, core.RGB(0xffffff), 0.12)
	} else if active {
		bg, fg = p.accent.Faded(0.12), p.accent
		hoverBg = p.accent.Faded(0.18)
	}
	style := core.Style{Height: core.Pt(34), Padding: core.Symmetric(0, 14), Gap: 6, Direction: core.Row,
		AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, CornerRadius: 999, Background: bg, Color: fg}
	if disabled {
		style.Opacity = core.Alpha(0.45)
	} else {
		style.Hover = &core.Style{Background: hoverBg}
	}
	var children []core.Node
	if svg != nil {
		children = append(children, icon(svg, 14, fg))
	}
	children = append(children, text(label, 13, core.FontWeightMedium, fg))
	return base.Button{TestID: id, Label: label, Selected: func() bool { return active }, Disabled: func() bool { return disabled },
		OnPress: func(core.PressEvent) {
			if !disabled && onPress != nil {
				onPress()
			}
		}, Style: style, Children: children}
}

// storedDesktopConditions renders the filter conditions beside the search box
// in the unified conditional search bar.
func (a *app) storedDesktopConditions(p palette) core.Node {
	u := &a.diskUI
	var segments []core.Node
	for _, it := range []struct{ value, label string }{
		{"", i18n.T("All")},
		{"pinned", i18n.T("Keep Offline")},
		{"automatic", i18n.T("Automatic Cache")},
	} {
		it := it
		selected := u.mode == it.value && !u.tasks
		bg, fg := core.Color(0), p.muted
		weight := core.FontWeightNormal
		if selected {
			bg, fg, weight = p.bg, p.text, core.FontWeightMedium
		}
		segments = append(segments, base.Button{
			TestID: "stored-tab-" + it.value, Label: it.label, Selected: func() bool { return selected },
			OnPress: func(core.PressEvent) { u.mode = it.value; u.tasks = false; u.group = ""; u.resetBrowse(); a.bump() },
			Style: core.Style{Height: core.Pt(26), Padding: core.Symmetric(0, 10), CornerRadius: 6,
				Direction: core.Row, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
				Background: bg, Hover: &core.Style{Background: p.sidebar}},
			Children: []core.Node{text(it.label, 12.5, weight, fg)},
		})
	}
	scopeBar := core.View{Style: core.Style{Height: core.Pt(32), Direction: core.Row, AlignItems: core.AlignCenter,
		Gap: 2, Padding: core.All(2), Background: p.hover, BorderWidth: 1, BorderColor: p.hairline, CornerRadius: 8},
		Children: segments}

	nodes := []core.Node{
		scopeBar,
		a.storedFilterSelect(p, "stored-view", i18n.T("View"), &u.view, false, []ui.Option[string]{
			{Value: "", Label: i18n.T("Songs")},
			{Value: "albums", Label: i18n.T("Albums")},
			{Value: "artists", Label: i18n.T("Artists")},
		}),
		a.storedFilterSelect(p, "stored-sort", i18n.T("Sort"), &u.sort, false, []ui.Option[string]{
			{Value: "", Label: i18n.T("Recently Saved")},
			{Value: "played", Label: i18n.T("Recently Played")},
			{Value: "title", Label: i18n.T("Name")},
			{Value: "artist", Label: i18n.T("Artist")},
			{Value: "size", Label: i18n.T("Storage Used")},
		}),
	}
	if a.storedHasActiveFilters() {
		nodes = append(nodes, base.Button{TestID: "stored-filter-reset", Label: i18n.T("Reset"),
			OnPress: func(core.PressEvent) {
				u.mode, u.view, u.sort, u.group = "", "", "", ""
				u.resetBrowse()
				u.query.Set("")
				a.bump()
			},
			Style: core.Style{Height: core.Pt(32), Padding: core.Symmetric(0, 10), Gap: 5, Direction: core.Row,
				AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, CornerRadius: 8,
				Hover: &core.Style{Background: p.hover}},
			Children: []core.Node{icon(lucideRotateCcw, 13, p.muted), text(i18n.T("Reset"), 12.5, core.FontWeightMedium, p.muted)}})
	}
	return core.Group(nodes)
}

func (a *app) storedPhoneBarActions(p palette) core.Node {
	u := &a.diskUI
	all := a.disk.List()
	records, _ := a.storedVisible(all)
	showingSongs := !u.tasks && (u.view == "" || u.group != "")

	btn := func(id, label string, svg *core.SVG, active, disabled bool, badge int, onPress func()) core.Node {
		bg, fg := p.sidebar, p.muted
		if active {
			bg, fg = p.accent.Faded(0.14), p.accent
		}
		style := core.Style{Height: core.Pt(38), MinWidth: core.Pt(38), Padding: core.Symmetric(0, 10), Gap: 4,
			FlexShrink: 0, Direction: core.Row, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
			CornerRadius: 10, Background: bg, BorderWidth: 1, BorderColor: p.hairline}
		if disabled {
			style.Opacity = core.Alpha(0.45)
		} else {
			style.Active = &core.Style{Background: p.press}
		}
		children := []core.Node{icon(svg, 16, fg)}
		if badge > 0 {
			children = append(children, text(strconv.Itoa(badge), 11, core.FontWeightSemibold, fg))
		}
		return base.Button{TestID: id, Label: label, Selected: func() bool { return active }, Disabled: func() bool { return disabled },
			OnPress: func(core.PressEvent) {
				if !disabled {
					onPress()
				}
			}, Style: style, Children: children}
	}

	var nodes []core.Node
	if showingSongs {
		selectLabel := i18n.T("Select")
		if u.selecting {
			selectLabel = i18n.T("Done")
		}
		nodes = append(nodes, btn("stored-select-mode", selectLabel, lucideListChecks, u.selecting, len(records) == 0 && !u.selecting, 0, func() {
			u.selecting = !u.selecting
			if !u.selecting {
				u.selected = map[string]bool{}
			}
			a.bump()
		}))
	}
	nodes = append(nodes, btn("stored-tasks", i18n.T("Download Tasks"), lucideDownload, u.tasks, false, a.storedActiveTaskCount(), func() {
		u.tasks = !u.tasks
		u.resetBrowse()
		a.bump()
	}))
	return core.Group(nodes)
}

func (a *app) storedPhoneConditions(p palette) core.Node {
	u := &a.diskUI
	var chips []core.Node
	for _, it := range []struct{ value, label string }{
		{"", i18n.T("All")},
		{"pinned", i18n.T("Keep Offline")},
		{"automatic", i18n.T("Automatic Cache")},
	} {
		it := it
		selected := u.mode == it.value && !u.tasks
		bg, fg := p.sidebar, p.muted
		border := p.hairline
		weight := core.FontWeightNormal
		if selected {
			bg, fg, border, weight = p.accent.Faded(0.12), p.accent, p.accent.Faded(0.35), core.FontWeightMedium
		}
		chips = append(chips, base.Button{
			TestID: "stored-tab-" + it.value, Label: it.label, Selected: func() bool { return selected },
			OnPress: func(core.PressEvent) { u.mode = it.value; u.tasks = false; u.group = ""; u.resetBrowse(); a.bump() },
			Style: core.Style{Height: core.Pt(32), FlexShrink: 0, Padding: core.Symmetric(0, 12), CornerRadius: 999,
				Direction: core.Row, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
				Background: bg, BorderWidth: 1, BorderColor: border, Active: &core.Style{Background: p.press}},
			Children: []core.Node{text(it.label, phoneFontBody, weight, fg)},
		})
	}
	chips = append(chips,
		a.storedFilterSelect(p, "stored-view", i18n.T("View"), &u.view, true, []ui.Option[string]{
			{Value: "", Label: i18n.T("Songs")},
			{Value: "albums", Label: i18n.T("Albums")},
			{Value: "artists", Label: i18n.T("Artists")},
		}),
		a.storedFilterSelect(p, "stored-sort", i18n.T("Sort"), &u.sort, true, []ui.Option[string]{
			{Value: "", Label: i18n.T("Recently Saved")},
			{Value: "played", Label: i18n.T("Recently Played")},
			{Value: "title", Label: i18n.T("Name")},
			{Value: "artist", Label: i18n.T("Artist")},
			{Value: "size", Label: i18n.T("Storage Used")},
		}),
	)
	if a.storedHasActiveFilters() {
		chips = append(chips, base.Button{TestID: "stored-filter-reset", Label: i18n.T("Reset"),
			OnPress: func(core.PressEvent) {
				u.mode, u.view, u.sort, u.group = "", "", "", ""
				u.resetBrowse()
				u.query.Set("")
				a.bump()
			},
			Style: core.Style{Height: core.Pt(32), FlexShrink: 0, Padding: core.Symmetric(0, 10), Gap: 4,
				Direction: core.Row, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
				CornerRadius: 999, Background: p.hover, Active: &core.Style{Background: p.press}},
			Children: []core.Node{icon(lucideRotateCcw, 12, p.muted), text(i18n.T("Reset"), phoneFontBody, core.FontWeightMedium, p.muted)}})
	}
	return core.Scroll{Horizontal: true, HideIndicator: true, Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6}, Children: chips},
	}}
}

// storedFilterSelect is a compact condition dropdown matching the search field
// and segmented filter bar on desktop and the chip bar on phone.
func (a *app) storedFilterSelect(p palette, id, label string, value *string, phone bool, options []ui.Option[string]) core.Node {
	chosen := -1
	for i, o := range options {
		if o.Value == *value {
			chosen = i
			break
		}
	}
	title := label
	if chosen >= 0 {
		title = options[chosen].Label
	}
	active := *value != ""
	height := core.Points(32)
	itemHeight := core.Points(30)
	radius := core.Points(8)
	bg := p.hover
	border := p.hairline
	fg := p.text
	fontSize := core.Points(12.5)
	padH := core.Points(10)
	if phone {
		height = 32
		itemHeight = 44
		radius = 999
		bg = p.sidebar
		fontSize = phoneFontBody
		padH = 11
	}
	if active {
		bg = p.accent.Faded(0.10)
		border = p.accent.Faded(0.32)
		fg = p.accent
	}
	return base.Select{
		Label: label, TestID: id,
		Items: func() []base.SelectItem {
			items := make([]base.SelectItem, len(options))
			for i, o := range options {
				items[i] = base.SelectItem{Value: strconv.Itoa(i), Title: o.Label}
			}
			return items
		},
		Value: func() *string {
			if chosen < 0 {
				return nil
			}
			v := strconv.Itoa(chosen)
			return &v
		},
		OnChange: func(v *string) {
			if v != nil {
				if i, err := strconv.Atoi(*v); err == nil && i >= 0 && i < len(options) {
					*value = options[i].Value
					a.diskUI.group = ""
					a.diskUI.resetBrowse()
					a.bump()
				}
			}
		},
		RootStyle: core.Style{FlexShrink: 0},
		Style: core.Style{Height: core.Pt(height), JustifyContent: core.JustifyCenter, BorderWidth: 1, BorderColor: border, CornerRadius: radius,
			Background: bg, Hover: &core.Style{Background: p.press},
			FocusVisible: &core.Style{OutlineWidth: 3, OutlineColor: ui.Alpha(p.accent, .25)}},
		Trigger: core.View{Style: core.Style{FlexGrow: 1, Direction: core.Row, AlignItems: core.AlignCenter,
			Padding: core.Symmetric(0, padH), Gap: 6}, Children: []core.Node{
			core.Text{Content: title, MaxLines: 1, Style: core.Style{FontSize: fontSize, FontWeight: core.FontWeightMedium, Color: fg}},
			icon(lucideChevronDown, 11, fg),
		}},
		PopupStyle: core.Style{MinWidth: core.Pt(150), Background: p.sidebar, BorderWidth: 1, BorderColor: p.hairline,
			CornerRadius: 10, Padding: core.All(4), ShadowColor: p.cardShade, ShadowRadius: 16, ShadowOffset: core.Point{Y: 6}},
		ListStyle: core.Style{MaxHeight: core.Pt(280)}, Margin: 8,
		ItemStyle: core.Style{MinHeight: core.Pt(itemHeight), JustifyContent: core.JustifyCenter, Padding: core.Symmetric(4, 8), CornerRadius: 6,
			Focus: &core.Style{Background: p.accent, Color: p.onAccent}, Color: p.text},
		RenderItem: func(e base.SelectEntry) core.Node {
			checked := e.Index == chosen
			return core.View{TestID: id + "-" + strconv.Itoa(e.Index),
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

func (a *app) storedBody(cx *core.Scope, p palette, phone bool) core.Node {
	u := &a.diskUI
	if u.tasks {
		return a.storedScroll(p, a.storedTasks(p, phone))
	}
	all := a.disk.List()
	filtered := a.storedFilter(all)
	var body []core.Node

	if u.view != "" && u.group == "" {
		groups := a.storedGroupsWithArt(filtered, u.view)
		if phone {
			var size int64
			for _, r := range filtered {
				size += r.Size()
			}
			body = append(body, core.Text{Content: i18n.Tf("%d songs available on this device", len(filtered)) + " · " + bytesLabel(size),
				Style: core.Style{FontSize: phoneFontCaption, Color: p.muted, Margin: core.Edges{Bottom: 6}}})
		}
		if len(groups) == 0 {
			if len(all) == 0 {
				body = append(body, a.emptyNote(p, i18n.T("No saved music yet"), i18n.T("Play online music to cache it, or choose Keep Offline from a song's menu.")))
			} else {
				body = append(body, a.emptyNote(p, i18n.T("No saved songs match"), i18n.T("Try another search.")))
			}
		} else {
			body = append(body, a.storedGroupGrid(p, groups, u.view))
		}
		return a.storedScroll(p, body...)
	}

	records, chosen := a.storedVisible(all)
	tracks := a.storedTracks(records)
	var size int64
	for _, r := range records {
		size += r.Size()
	}

	if phone {
		if u.view != "" && u.group != "" {
			groupName := i18n.T("Saved Music")
			if chosen != nil {
				groupName = chosen.name
			}
			body = append(body, core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, Margin: core.Edges{Bottom: 10}}, Children: []core.Node{
				base.Button{TestID: "stored-group-back", Label: i18n.T("Back"),
					OnPress: func(core.PressEvent) { u.group = ""; u.resetBrowse(); a.bump() },
					Style: core.Style{Height: core.Pt(34), Padding: core.Symmetric(0, 10), Gap: 4, Direction: core.Row,
						AlignItems: core.AlignCenter, CornerRadius: 999, Background: p.sidebar, Active: &core.Style{Background: p.press}},
					Children: []core.Node{icon(lucideChevronLeft, 15, p.text), text(i18n.T("Back"), phoneFontBody, core.FontWeightMedium, p.text)}},
				core.Text{Content: groupName, MaxLines: 1, Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0),
					FontSize: phoneFontSection, FontWeight: core.FontWeightSemibold, Color: p.text}},
			}})
		}
		body = append(body, core.Text{Content: i18n.Tf("%d songs available on this device", len(records)) + " · " + bytesLabel(size),
			Style: core.Style{FontSize: phoneFontCaption, Color: p.muted, Margin: core.Edges{Bottom: 10}}})
		if len(records) > 0 {
			body = append(body, core.View{Style: core.Style{Margin: core.Edges{Bottom: 12}}, Children: []core.Node{
				a.storedPhonePlayButtons(p, tracks),
			}})
		}
	}

	if !phone && u.details != "" {
		for _, r := range all {
			if r.ID == u.details {
				body = append(body, a.storedInfo(p, r))
			}
		}
	}

	if len(records) == 0 {
		if len(all) == 0 {
			body = append(body, a.emptyNote(p, i18n.T("No saved music yet"), i18n.T("Play online music to cache it, or choose Keep Offline from a song's menu.")))
		} else {
			body = append(body, a.emptyNote(p, i18n.T("No saved songs match"), i18n.T("Try another search.")))
		}
		return a.storedScroll(p, body...)
	}

	body = append(body, core.List{
		Count:         func() int { return len(records) },
		Key:           func(i int) any { return records[i].ID },
		EstimatedSize: a.trackRowHeight(),
		Viewport:      core.ListViewportParent,
		Row: func(cx *core.Scope, row core.ListRow) core.Node {
			return core.Dynamic(func(cx *core.Scope) core.Node {
				return a.storedRow(p, records, tracks, row.Index(), phone)
			})
		},
	})
	return a.storedScroll(p, body...)
}

func (a *app) storedPhonePlayButtons(p palette, tracks []music.Track) core.Node {
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 10}, Children: []core.Node{
		base.Button{TestID: "stored-play-all", Label: i18n.T("Play"), Disabled: func() bool { return len(tracks) == 0 },
			OnPress: func(core.PressEvent) { a.playTracks(tracks, 0) },
			Style: core.Style{FlexGrow: 1, Height: core.Pt(44), Direction: core.Row, AlignItems: core.AlignCenter,
				JustifyContent: core.JustifyCenter, Gap: 8, CornerRadius: 22, Background: p.accent, Active: &core.Style{Opacity: core.Alpha(.8)}},
			Children: []core.Node{icon(lucidePlay, 17, p.onAccent), text(i18n.T("Play"), 14, core.FontWeightSemibold, p.onAccent)}},
		base.Button{TestID: "stored-shuffle", Label: i18n.T("Shuffle"), Disabled: func() bool { return len(tracks) == 0 },
			OnPress: func(core.PressEvent) { a.pl.PlayShuffled(tracks); a.heartbeat() },
			Style: core.Style{Width: core.Pt(44), Height: core.Pt(44), FlexShrink: 0, CornerRadius: 22, Background: p.sidebar,
				AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Active: &core.Style{Background: p.press}},
			Children: []core.Node{icon(lucideShuffle, 19, p.text)}},
	}}
}

func (a *app) storedPinnedBatchBar(p palette, phone bool) core.Node {
	u := &a.diskUI
	if !u.selecting || u.tasks || (u.view != "" && u.group == "") {
		return core.Group{}
	}
	records, _ := a.storedVisible(a.disk.List())
	return a.storedBatchBar(p, records, phone)
}

func (a *app) storedBatchBar(p palette, records []offline.Record, phone bool) core.Node {
	u := &a.diskUI
	selected := a.selectedStored(records)
	targetTracks := a.storedTracks(selected)
	ids := make([]string, len(selected))
	for i, r := range selected {
		ids[i] = r.ID
	}
	var children []core.Node
	if len(selected) == 0 {
		children = append(children, core.Text{Content: i18n.T("Select songs to manage"),
			Style: core.Style{FlexGrow: 1, FontSize: 12.5, Color: p.muted}})
	} else {
		children = append(children,
			text(i18n.Tf("%d selected", len(selected)), 12.5, core.FontWeightSemibold, p.text),
			a.storedBatchAction(p, "stored-enqueue", lucideListPlus, i18n.T("Add to Queue"), false, func() { a.pl.Enqueue(targetTracks); a.heartbeat() }),
			a.storedBatchAction(p, "stored-pin", lucidePin, i18n.T("Keep Offline"), false, func() { a.pinTracks(targetTracks, "") }),
			a.storedBatchAction(p, "stored-unpin", nil, i18n.T("Cancel Offline Retention"), false, func() { a.cancelStoredRetention(ids) }),
			a.storedBatchAction(p, "stored-delete", lucideTrash, i18n.T("Remove Local Copies"), true, func() { u.deleteIDs = slices.Clone(ids); a.bump() }),
		)
	}
	return core.View{TestID: "stored-batch-bar", Style: core.Style{
		Direction: core.Row, Wrap: core.Wrap, AlignItems: core.AlignCenter, Gap: 8,
		Padding: core.Symmetric(8, 12), CornerRadius: 10,
		Background: p.sidebar, BorderWidth: 1, BorderColor: p.hairline,
	}, Children: children}
}

func (a *app) storedBatchAction(p palette, id string, svg *core.SVG, label string, danger bool, onPress func()) core.Node {
	fg, bg := p.text, p.hover
	hoverBg := p.press
	if danger {
		fg, bg = p.accent, p.accent.Faded(0.10)
		hoverBg = p.accent.Faded(0.16)
	}
	var children []core.Node
	if svg != nil {
		children = append(children, icon(svg, 13, fg))
	}
	children = append(children, text(label, 12.5, core.FontWeightMedium, fg))
	return base.Button{TestID: id, Label: label, OnPress: func(core.PressEvent) { onPress() },
		Style: core.Style{Height: core.Pt(30), Padding: core.Symmetric(0, 10), Gap: 5, Direction: core.Row,
			AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, CornerRadius: 7,
			Background: bg, Hover: &core.Style{Background: hoverBg}},
		Children: children}
}

func (a *app) storedGroupGrid(p palette, groups []storedGroup, view string) core.Node {
	u := &a.diskUI
	return a.cardGrid(p, len(groups), func(i int) core.Node {
		g := groups[i]
		open := func() { u.group = g.key; u.resetBrowse(); a.bump() }
		sub := i18n.Songs(len(g.records))
		if view == "artists" {
			box := a.cardBox(p, 999, a.coverArt(p, g.coverURL, 999, lucideUser))
			box.Style.MaxWidth = core.Pt(artistAvatarSize)
			return base.Button{TestID: "stored-group-" + g.key, Label: g.name, OnPress: func(core.PressEvent) { open() },
				Style: core.Style{Direction: core.Column, Gap: 10, AlignItems: core.AlignCenter, Group: "card"},
				Children: []core.Node{
					box,
					core.Text{Content: g.name, MaxLines: 1, Style: core.Style{Width: core.Pct(100), TextAlign: core.TextAlignCenter,
						FontSize: 13, FontWeight: core.FontWeightMedium, LineHeightMultiple: 1.35, Color: p.text}},
					core.Text{Content: sub, MaxLines: 1, Style: core.Style{Width: core.Pct(100), TextAlign: core.TextAlignCenter,
						FontSize: 12, Color: p.muted, Margin: core.Edges{Top: -6}}},
				}}
		}
		card := a.cardFrame(p, g.name, sub, 10, a.coverArt(p, g.coverURL, 10, lucideDisc), open).(base.Button)
		card.TestID = "stored-group-" + g.key
		return card
	})
}

type storedGroup struct {
	key, name, coverURL string
	records             []offline.Record
}

func (a *app) storedGroupsWithArt(records []offline.Record, view string) []storedGroup {
	groups := storedGroups(records, view)
	if a.disk == nil {
		return groups
	}
	for i := range groups {
		for _, r := range groups[i].records {
			if url := a.disk.Track(r).Album.ImageURL; url != "" {
				groups[i].coverURL = url
				break
			}
		}
	}
	return groups
}

func storedGroups(records []offline.Record, view string) []storedGroup {
	groups := map[string]*storedGroup{}
	add := func(r offline.Record, id, name string) {
		if name == "" {
			name = i18n.T("Unknown")
		}
		key := offline.Key(r.Track.Source, id)
		g := groups[key]
		if g == nil {
			g = &storedGroup{key: key, name: name}
			groups[key] = g
		}
		g.records = append(g.records, r)
	}
	for _, r := range records {
		if view == "albums" {
			id := r.Track.Album.ID
			if id == "" {
				id = r.Track.Album.Name + "/" + r.Track.ArtistNames()
			}
			add(r, "album:"+id, r.Track.Album.Name)
		} else {
			if len(r.Track.Artists) == 0 {
				add(r, "artist:unknown", "")
			}
			for _, artist := range r.Track.Artists {
				id := artist.ID
				if id == "" {
					id = artist.Name
				}
				add(r, "artist:"+id, artist.Name)
			}
		}
	}
	var out []storedGroup
	for _, g := range groups {
		out = append(out, *g)
	}
	slices.SortFunc(out, func(a, b storedGroup) int { return strings.Compare(a.name, b.name) })
	return out
}
func savedName(r offline.Record) string {
	if r.Legacy {
		return i18n.T("Unidentified cached song")
	}
	return r.Track.Name
}
func bytesLabel(n int64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}
func variantLabel(v offline.Variant) string {
	quality := i18n.T("Unknown Quality")
	switch v.Stream.Quality {
	case "standard":
		quality = i18n.T("Standard")
	case "high":
		quality = i18n.T("High")
	case "lossless":
		quality = i18n.T("Lossless")
	}
	return strings.ToUpper(v.Stream.Type) + " · " + quality
}

func (a *app) storedSourceLabel(r offline.Record) string {
	if r.Legacy {
		return i18n.T("Unknown source")
	}
	source := r.SourceName
	if source == "" {
		source = i18n.T("Unknown source")
	}
	if a.sourceByID(r.Track.Source) == nil {
		source += " · " + i18n.T("Plugin Removed")
	}
	return source
}

func (a *app) toggleStoredSelection(id string) {
	if a.diskUI.selected == nil {
		a.diskUI.selected = map[string]bool{}
	}
	if a.diskUI.selected[id] {
		delete(a.diskUI.selected, id)
	} else {
		a.diskUI.selected[id] = true
	}
	a.bump()
}

func (a *app) storedRow(p palette, records []offline.Record, tracks []music.Track, index int, phone bool) core.Node {
	if phone {
		return a.storedPhoneRow(p, records, tracks, index)
	}
	return a.storedDesktopRow(p, records, tracks, index)
}

func (a *app) storedDesktopRow(p palette, records []offline.Record, tracks []music.Track, index int) core.Node {
	r, tr := records[index], tracks[index]
	selecting := a.diskUI.selecting
	selected := selecting && a.diskUI.selected[r.ID]
	showAlbum := a.cx.Env().Window().Width-sidebarWidth > 820
	isCurrent := func() bool {
		if t := a.pl.Current(); t != nil {
			return t.ID == tr.ID && t.Source == tr.Source
		}
		return false
	}

	var leading core.Node
	if selecting {
		checkIcon, checkColor := lucideSquare, p.muted
		if selected {
			checkIcon, checkColor = lucideSquareCheck, p.accent
		}
		leading = base.Button{
			TestID: "stored-select-" + r.ID, Label: i18n.Tf("Select %s", savedName(r)),
			Selected: func() bool { return a.diskUI.selected[r.ID] },
			OnPress:  func(core.PressEvent) { a.toggleStoredSelection(r.ID) },
			Style: core.Style{Width: core.Pt(26), Height: core.Pt(32), FlexShrink: 0,
				Direction: core.Row, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, CornerRadius: 6},
			Children: []core.Node{icon(checkIcon, 16, checkColor)},
		}
	}

	var playChildren []core.Node
	if !selecting {
		number := func() core.Style {
			a.playRev.Get()
			hovered := core.DisplayNone
			if isCurrent() {
				hovered = core.DisplayFlex
			}
			return core.Style{Display: core.DisplayFlex,
				GroupStyle: &core.GroupStyle{Name: trackRowGroup, Hover: &core.Style{Display: hovered}}}
		}
		play := func() core.Style {
			a.playRev.Get()
			hovered := core.DisplayFlex
			if isCurrent() {
				hovered = core.DisplayNone
			}
			return core.Style{Display: core.DisplayNone,
				GroupStyle: &core.GroupStyle{Name: trackRowGroup, Hover: &core.Style{Display: hovered}}}
		}
		playChildren = append(playChildren, core.View{Style: core.Style{Width: core.Pt(26), FlexShrink: 0, Direction: core.Row,
			AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter}, Children: []core.Node{
			core.View{StyleFunc: number, Children: []core.Node{core.Show{When: func() bool { a.playRev.Get(); return isCurrent() },
				Then: func(*core.Scope) core.Node { return icon(lucideVolume, 14, p.accent) },
				Else: func(*core.Scope) core.Node { return text(fmt.Sprint(index+1), 13, core.FontWeightNormal, p.faint) }}}},
			core.View{StyleFunc: play, Children: []core.Node{icon(lucidePlay, 13, p.text)}},
		}})
	}
	playChildren = append(playChildren,
		core.View{Style: core.Style{Width: core.Pt(38), FlexShrink: 0}, Children: []core.Node{a.cover(p, tr.Album.ImageURL, 38, 6)}},
		core.View{Style: core.Style{FlexGrow: 3, FlexBasis: core.Pt(0), FlexShrink: 1, MinWidth: core.Pt(0), Direction: core.Column, Gap: 3}, Children: []core.Node{
			core.Text{Content: savedName(r), MaxLines: 1, StyleFunc: func() core.Style {
				a.playRev.Get()
				c := p.text
				if isCurrent() {
					c = p.accent
				}
				return core.Style{FontSize: 14, FontWeight: core.FontWeightMedium, Color: c}
			}},
			core.Text{Content: r.Track.ArtistNames(), MaxLines: 1, Style: core.Style{FontSize: 12.5, Color: p.muted}},
		}},
	)
	if showAlbum {
		playChildren = append(playChildren, core.Text{Content: r.Track.Album.Name, MaxLines: 1,
			Style: core.Style{FlexGrow: 2, FlexBasis: core.Pt(0), FlexShrink: 1, MinWidth: core.Pt(0), FontSize: 12.5, Color: p.muted}})
	}
	if r.Pinned() {
		playChildren = append(playChildren, core.View{Style: core.Style{FlexShrink: 0, Direction: core.Row, AlignItems: core.AlignCenter,
			Gap: 4, Padding: core.Symmetric(2, 7), CornerRadius: 999, Background: p.accent.Faded(0.10)},
			Children: []core.Node{icon(lucidePin, 10.5, p.accent), text(i18n.T("Keep Offline"), 11, core.FontWeightMedium, p.accent)}})
	}
	playChildren = append(playChildren, core.Text{Content: clock(tr.Length()),
		Style: core.Style{Width: core.Pt(44), FlexShrink: 0, TextAlign: core.TextAlignEnd, FontSize: 12.5, Color: p.faint}})

	playBtn := base.Button{
		TestID: "stored-play-" + r.ID, Label: savedName(r),
		OnPress: func(core.PressEvent) {
			if selecting {
				a.toggleStoredSelection(r.ID)
				return
			}
			a.playTracks(tracks, index)
		},
		Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Height: core.Pt(54),
			Direction: core.Row, AlignItems: core.AlignCenter, Gap: 14},
		Children: playChildren,
	}

	var rowChildren []core.Node
	if leading != nil {
		rowChildren = append(rowChildren, leading)
	}
	rowChildren = append(rowChildren, playBtn)
	if !selecting {
		rowChildren = append(rowChildren, base.Menu{
			TestID: "stored-more-" + r.ID, Label: i18n.T("More"),
			Trigger: icon(lucideMore, 16, p.muted),
			Style: core.Style{Width: core.Pt(30), Height: core.Pt(30), FlexShrink: 0,
				Direction: core.Row, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
				CornerRadius: 8, Hover: &core.Style{Background: p.press}},
			PopupStyle: core.Style{Background: p.sidebar, BorderWidth: 1, BorderColor: p.hairline,
				Padding: core.All(4), CornerRadius: 10, ShadowColor: p.cardShade, ShadowRadius: 16, ShadowOffset: core.Point{Y: 6}},
			Children: a.trackMenu(tr, "", -1),
		})
	}

	row := core.View{
		TestID: "stored-row-" + r.ID,
		StyleFunc: func() core.Style {
			s := core.Style{Height: core.Pt(54), Direction: core.Row, AlignItems: core.AlignCenter, Gap: 10,
				Padding: core.Symmetric(0, 12), CornerRadius: 10, Color: p.text, Group: trackRowGroup,
				Hover: &core.Style{Background: p.hover}}
			a.playRev.Get()
			if selected {
				s.Background = p.accent.Faded(0.08)
				s.Hover = &core.Style{Background: p.accent.Faded(0.12)}
			} else if isCurrent() {
				s.Background = ui.Alpha(p.accent, 0.09)
				s.Hover = &core.Style{Background: s.Background}
			}
			return s
		},
		Children: rowChildren,
	}
	if selecting {
		return row
	}
	return ui.ContextMenu{Trigger: row, Children: a.trackMenu(tr, "", -1)}
}

func (a *app) storedPhoneRow(p palette, records []offline.Record, tracks []music.Track, index int) core.Node {
	r, tr := records[index], tracks[index]
	selecting := a.diskUI.selecting
	selected := selecting && a.diskUI.selected[r.ID]
	height := a.trackRowHeight()

	subtitle := r.Track.ArtistNames()
	if r.Pinned() {
		if subtitle != "" {
			subtitle += " · " + i18n.T("Keep Offline")
		} else {
			subtitle = i18n.T("Keep Offline")
		}
	}

	var children []core.Node
	if selecting {
		checkIcon, checkColor := lucideSquare, p.muted
		if selected {
			checkIcon, checkColor = lucideSquareCheck, p.accent
		}
		children = append(children, base.Button{
			TestID: "stored-select-" + r.ID, Label: i18n.Tf("Select %s", savedName(r)),
			Selected: func() bool { return a.diskUI.selected[r.ID] },
			OnPress:  func(core.PressEvent) { a.toggleStoredSelection(r.ID) },
			Style: core.Style{Width: core.Pt(38), Height: core.Pt(48), FlexShrink: 0,
				AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, CornerRadius: 10},
			Children: []core.Node{icon(checkIcon, 20, checkColor)},
		})
	}

	children = append(children, base.Button{
		TestID: "stored-play-" + r.ID, Label: savedName(r),
		OnPress: func(core.PressEvent) {
			if selecting {
				a.toggleStoredSelection(r.ID)
				return
			}
			a.playTracks(tracks, index)
		},
		Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Height: core.Pt(height),
			Direction: core.Row, AlignItems: core.AlignCenter, Gap: 12, Padding: core.Symmetric(8, 0),
			CornerRadius: 10, Active: &core.Style{Background: p.hover}},
		Children: []core.Node{
			a.cover(p, tr.Album.ImageURL, 44, 9),
			core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: 5}, Children: []core.Node{
				core.Text{Content: savedName(r), MaxLines: 1, StyleFunc: func() core.Style {
					a.playRev.Get()
					color := p.text
					if a.isCurrentTrack(tr) {
						color = p.accent
					}
					return core.Style{FontSize: phoneFontSong, FontWeight: core.FontWeightNormal, Color: color}
				}},
				core.Text{Content: subtitle, MaxLines: 1, Style: core.Style{FontSize: phoneFontCaption, Color: p.muted}},
			}},
		},
	})

	if !selecting {
		children = append(children, a.phoneIcon(p, "stored-more-"+r.ID, i18n.T("More"), lucideMore, p.muted, func() {
			a.openPhoneTrack(tr, "", index)
		}))
	}

	return core.View{
		TestID: "stored-row-" + r.ID,
		Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, MinWidth: core.Pt(0), Height: core.Pt(height),
			CornerRadius: 10, Background: func() core.Color {
				if selected {
					return p.accent.Faded(0.08)
				}
				return core.Color(0)
			}()},
		Children: children,
	}
}

func (a *app) storedMenu(tr music.Track) []core.Node {
	if a.disk == nil {
		return nil
	}
	var items []core.Node
	r, exists := a.disk.Get(tr)
	if tr.Source != "local" && (tr.Source != "" || exists) {
		items = append(items, ui.MenuItem{Text: i18n.T("Keep Offline"), Icon: lucide.Pin, OnSelect: func() { a.pinTracks([]music.Track{tr}, "") }})
	}
	if exists && len(r.Variants) > 0 {
		items = append(items,
			ui.MenuItem{Text: i18n.T("Add to Queue"), Icon: lucide.ListPlus, OnSelect: func() { a.pl.Enqueue([]music.Track{a.disk.Track(r)}); a.heartbeat() }},
			ui.MenuItem{Text: i18n.T("Song Info"), Icon: lucide.Music, OnSelect: func() { a.diskUI.details = r.ID; a.openStored() }},
		)
		if r.Pinned() {
			items = append(items, ui.MenuItem{Text: i18n.T("Cancel Offline Retention"), Icon: lucide.PinOff, OnSelect: func() { a.cancelStoredRetention([]string{r.ID}) }})
		}
		if src := a.sourceByID(r.Track.Source); src != nil {
			items = append(items,
				ui.MenuItem{Text: i18n.T("Keep in Lossless Quality"), Icon: lucide.Download, OnSelect: func() { a.pinTracks([]music.Track{r.Track}, "lossless") }},
				ui.MenuItem{Text: i18n.T("Fetch Missing Details"), Icon: lucide.RotateCcw, OnSelect: func() {
					t := r.Track
					t.LocalOnly = false
					if original := a.pl.Current(); original != nil && original.ID == t.ID && original.Source == t.Source {
						t.Album.ImageURL = original.Album.ImageURL
					}
					go func() { <-a.disk.Fill(t, src, true); a.post(a.bump) }()
				}},
			)
		}
		items = append(items, ui.MenuItem{Text: i18n.T("Remove Local Copies"), Icon: lucide.Trash, Variant: ui.MenuItemDanger, OnSelect: func() { a.diskUI.deleteIDs = []string{r.ID}; a.bump() }})
	}
	return items
}

func (a *app) storedInfo(p palette, r offline.Record) core.Node {
	tr := a.disk.Track(r)
	var metaLines []core.Node
	metaLines = append(metaLines, text(i18n.T("Music source")+" · "+a.storedSourceLabel(r), 12.5, core.FontWeightNormal, p.muted))
	for _, v := range r.Variants {
		metaLines = append(metaLines, text(variantLabel(v)+" · "+bytesLabel(v.Stream.Size), 12.5, core.FontWeightNormal, p.muted))
	}
	var missing []string
	if r.Cover == "" {
		missing = append(missing, i18n.T("Missing Cover"))
	}
	if r.Lyrics == nil {
		missing = append(missing, i18n.T("Missing Lyrics"))
	}
	if len(missing) > 0 {
		metaLines = append(metaLines, text(strings.Join(missing, " · "), 12.5, core.FontWeightNormal, p.accent))
	}
	return core.View{TestID: "stored-info", Style: core.Style{
		Padding: core.All(14), Background: p.sidebar, BorderWidth: 1, BorderColor: p.hairline,
		CornerRadius: 12, Gap: 10, Margin: core.Edges{Left: 12, Right: 12, Bottom: 12},
	}, Children: []core.Node{
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 12}, Children: []core.Node{
			a.cover(p, tr.Album.ImageURL, 44, 8),
			core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: 2}, Children: []core.Node{
				core.Text{Content: savedName(r), MaxLines: 1, Style: core.Style{FontSize: 14, FontWeight: core.FontWeightSemibold, Color: p.text}},
				core.Text{Content: r.Track.ArtistNames() + " · " + r.Track.Album.Name, MaxLines: 1, Style: core.Style{FontSize: 12.5, Color: p.muted}},
			}},
			base.Button{TestID: "stored-info-close", Label: i18n.T("Close"), OnPress: func(core.PressEvent) { a.diskUI.details = ""; a.bump() },
				Style: core.Style{Width: core.Pt(28), Height: core.Pt(28), AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
					CornerRadius: 7, Hover: &core.Style{Background: p.hover}},
				Children: []core.Node{icon(lucideX, 14, p.muted)}},
		}},
		core.View{Style: core.Style{Direction: core.Column, Gap: 4}, Children: metaLines},
	}}
}

func taskLabel(state string) string {
	switch state {
	case "queued":
		return i18n.T("Queued")
	case "resolving":
		return i18n.T("Finding Audio")
	case "downloading":
		return i18n.T("Downloading")
	case "saved":
		return i18n.T("Saved Offline")
	case "waiting-plugin":
		return i18n.T("Waiting for Plugin")
	case "waiting-login":
		return i18n.T("Waiting for Sign In")
	case "waiting-network":
		return i18n.T("Waiting for Wi-Fi")
	case "waiting-space":
		return i18n.T("Waiting for Storage Space")
	case "interrupted":
		return i18n.T("Interrupted")
	case "paused":
		return i18n.T("Paused")
	default:
		return i18n.T("Download Failed")
	}
}

func (a *app) storedTasks(p palette, phone bool) core.Node {
	tasks := a.disk.Tasks()
	if len(tasks) == 0 {
		return a.emptyNote(p, i18n.T("No download tasks"), i18n.T("Songs you choose to keep offline will show their progress here."))
	}
	inset := core.Points(12)
	if phone {
		inset = 0
	}
	var children []core.Node
	for _, task := range tasks {
		t := task
		progress := bytesLabel(t.Received)
		if t.Total > 0 {
			progress += " / " + bytesLabel(t.Total)
		}
		var actions []core.Node
		if t.State == "queued" || t.State == "resolving" || t.State == "downloading" {
			actions = append(actions, a.storedBatchAction(p, "stored-task-pause-"+t.ID, lucidePause, i18n.T("Pause"), false, func() {
				a.storedWork(func() error { a.disk.Pause(t.ID); return nil })
			}))
		} else if t.State != "saved" {
			actions = append(actions, a.storedBatchAction(p, "stored-task-retry-"+t.ID, lucideRotateCcw, i18n.T("Retry"), false, func() {
				a.storedWork(func() error { return a.disk.Retry(t.ID, false) })
			}))
			if t.State == "waiting-network" {
				actions = append(actions, a.storedBatchAction(p, "stored-task-mobile-"+t.ID, nil, i18n.T("Allow Mobile Data"), false, func() {
					a.storedWork(func() error { return a.disk.Retry(t.ID, true) })
				}))
			}
		}
		actions = append(actions, a.storedBatchAction(p, "stored-task-remove-"+t.ID, lucideTrash, i18n.T("Remove"), true, func() {
			a.storedWork(func() error { a.disk.Cancel(t.ID); return nil })
		}))

		meta := []core.Node{
			core.Text{Content: t.Track.Name, MaxLines: 1, Style: core.Style{FontSize: 14, FontWeight: core.FontWeightMedium, Color: p.text}},
			core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, AlignItems: core.AlignCenter, Gap: 6}, Children: []core.Node{
				core.View{Style: core.Style{Padding: core.Symmetric(2, 7), CornerRadius: 999, Background: p.hover},
					Children: []core.Node{text(taskLabel(t.State), 11.5, core.FontWeightMedium, p.text)}},
				text(progress, 12, core.FontWeightNormal, p.muted),
			}},
		}
		if t.Error != "" {
			meta = append(meta, core.Text{Content: i18n.T(t.Error), MaxLines: 2, Style: core.Style{FontSize: 12, Color: p.accent}})
		}

		children = append(children, core.View{TestID: "stored-task-" + t.ID, Style: core.Style{
			Direction: core.Row, Wrap: core.Wrap, AlignItems: core.AlignCenter, JustifyContent: core.JustifySpaceBetween,
			Gap: 12, Padding: core.Symmetric(12, 14), Margin: core.Edges{Left: inset, Right: inset, Bottom: 8},
			CornerRadius: 10, Background: p.sidebar, BorderWidth: 1, BorderColor: p.hairline,
		}, Children: []core.Node{
			core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(180), Direction: core.Row, AlignItems: core.AlignCenter, Gap: 12}, Children: []core.Node{
				a.cover(p, t.Track.Album.ImageURL, 40, 8),
				core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: 5}, Children: meta},
			}},
			core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, AlignItems: core.AlignCenter, Gap: 6}, Children: actions},
		}})
	}
	return core.View{Style: core.Style{Direction: core.Column}, Children: children}
}

func (a *app) storedDeleteDialog(cx *core.Scope, p palette) core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		a.rev.Get()
		dialog := ui.Dialog{Open: func() bool { return len(a.diskUI.deleteIDs) > 0 }, OnOpenChange: func(open bool, _ base.Reason) {
			if !open {
				a.diskUI.deleteIDs = nil
				a.bump()
			}
		}, Title: i18n.T("Remove Local Copies"), Description: i18n.T("Remove these audio files and their saved details from this device? Offline copies are included. Online songs and playlists are unchanged."), Footer: []core.Node{ui.Button{Text: i18n.T("Cancel"), OnPress: func() { a.diskUI.deleteIDs = nil; a.bump() }}, ui.Button{Text: i18n.T("Remove"), TestID: "stored-delete-confirm", OnPress: func() {
			ids := slices.Clone(a.diskUI.deleteIDs)
			a.diskUI.deleteIDs = nil
			a.storedWork(func() error {
				for _, id := range ids {
					if err := a.disk.Delete(id); err != nil {
						return err
					}
				}
				return nil
			})
		}}}}
		return a.fitPhoneDialog(cx, p, dialog)
	})
}

func (a *app) cancelStoredRetention(ids []string) {
	copies := slices.Clone(ids)
	a.storedWork(func() error {
		for _, id := range copies {
			if err := a.disk.SetPinned(id, false); err != nil {
				return err
			}
		}
		a.post(func() { a.say(i18n.T("Offline retention canceled. These copies can now be automatically removed.")) })
		return nil
	})
}

func (a *app) storedScroll(p palette, children ...core.Node) core.Node {
	scroll := a.pageScroll(p, children...).(core.Scroll)
	scroll.TestID = "stored-scroll"
	// Both the page and this viewport must shrink below the list's intrinsic height.
	scroll.Style.FlexShrink = 1
	scroll.ContentOffset = a.diskUI.offset
	scroll.OnScroll = func(e core.ScrollEvent) { a.diskUI.offset = e.Offset }
	scroll.OnScrollEnd = scroll.OnScroll
	scroll.ScrollEventThrottle = 0
	return scroll
}

func (u *storedUI) resetBrowse() {
	u.offset = core.Point{}
	u.selecting = false
	u.selected = map[string]bool{}
}
