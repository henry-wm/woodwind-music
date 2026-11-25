package app

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/HengWh/woodwind-music/internal/i18n"
	"github.com/HengWh/woodwind-music/internal/music"
	"github.com/HengWh/woodwind-music/internal/plugin"
)

// settingEdit belongs to one plugin/key. Only the engine goroutine changes it;
// pending writes disable the control until their posted completion arrives.
type settingEdit struct {
	source string
	value  string
	saving bool
	failed bool
}

type settingWrite struct{ value string }

// revealPluginDir opens a plugin folder in the OS file manager on desktop.
// Tests may replace it.
var revealPluginDir = func(cx *core.Scope, dir string) {
	if cx == nil || cx.Env().OS() == core.OSAndroid {
		return
	}
	desktop.RevealFile(cx, dir, nil)
}

// trashPluginDir moves a removed plugin folder to the Trash on desktop when
// available; returning an error lets plugin.Remove fall back to os.RemoveAll.
var trashPluginDir = func(cx *core.Scope, dir string) error {
	if cx == nil || cx.Env().OS() == core.OSAndroid {
		return fmt.Errorf("no trash on this platform")
	}
	done := make(chan error, 1)
	desktop.TrashFile(cx, dir, func(_ string, err error) {
		done <- err
	})
	return <-done
}

func pluginFrom(p *plugin.Plugin) (from, href string) {
	if p == nil {
		return "", ""
	}
	o := p.Origin()
	if o == nil {
		return i18n.T("From this computer"), ""
	}
	if o.Repo != "" {
		return o.Repo, "https://github.com/" + o.Repo
	}
	if o.FeedURL != "" {
		if u, err := url.Parse(o.FeedURL); err == nil && u.Host != "" {
			return u.Host, ""
		}
		return o.FeedURL, ""
	}
	if o.DownloadURL != "" {
		if u, err := url.Parse(o.DownloadURL); err == nil && u.Host != "" {
			return u.Host, ""
		}
	}
	return i18n.T("From this computer"), ""
}

func (a *app) pluginSettingsBlock(p *plugin.Plugin) settingsBlockDef {
	info := p.Info()
	var search strings.Builder
	search.WriteString(info.Name)
	search.WriteByte(' ')
	search.WriteString(info.ID)
	if o := p.Origin(); o != nil {
		search.WriteByte(' ')
		search.WriteString(o.Repo)
		search.WriteByte(' ')
		search.WriteString(o.FeedURL)
	}
	for _, st := range info.Settings {
		search.WriteByte(' ')
		search.WriteString(st.Label)
		search.WriteByte(' ')
		search.WriteString(st.Help)
	}
	return settingsBlockDef{
		searchText: search.String(),
		render: func(pal palette) core.Node {
			return a.pluginSettingsCardRow(pal, p)
		},
	}
}

// pluginSettingsCardRow mirrors MyGo Music's compact installed-plugin row:
// header with version, origin/account, inline update action, sign-in/out, more
// menu and enable switch; followed by permissions and typed settings when on.
func (a *app) pluginSettingsCardRow(pal palette, p *plugin.Plugin) core.Node {
	info := p.Info()
	enabled := a.pluginEnabled(info.ID)
	phone := a.settingsInMain && a.phoneLayout(a.cx)
	state := a.accountState(p)
	if enabled && state.account == nil && !state.asked && p.Can("account") {
		a.loadAccount(p)
	}

	title := info.Name
	if info.Version != "" {
		title += " " + info.Version
	}

	from, href := pluginFrom(p)
	signedIn := ""
	if enabled && state.account != nil {
		signedIn = i18n.Tf("Signed in as %s", state.account.Name)
	}

	var metaChildren []core.Node
	if href != "" {
		metaChildren = append(metaChildren, base.Button{
			Label:   i18n.T("Open on GitHub"),
			TestID:  "plugin-origin-" + info.ID,
			OnPress: func(core.PressEvent) { a.cx.OpenURL(href, nil) },
			Style: core.Style{
				FlexShrink: 0,
				Hover:      &core.Style{Color: pal.text},
			},
			Children: []core.Node{
				core.Text{Content: from, MaxLines: 1, Style: core.Style{FontSize: 12, Color: pal.muted}},
			},
		})
	} else if from != "" {
		metaChildren = append(metaChildren, core.Text{Content: from, MaxLines: 1, Style: core.Style{FontSize: 12, Color: pal.muted, FlexShrink: 0}})
	}
	if signedIn != "" {
		if from != "" {
			signedIn = " · " + signedIn
		}
		metaChildren = append(metaChildren, core.Text{Content: signedIn, MaxLines: 1, Style: core.Style{FontSize: 12, Color: pal.muted, FlexShrink: 1, MinWidth: core.Pt(0)}})
	}

	infoChildren := []core.Node{
		core.Text{Content: title, Style: core.Style{FontSize: 13.5, FontWeight: core.FontWeightMedium, Color: pal.text}},
	}
	if len(metaChildren) > 0 {
		infoChildren = append(infoChildren, core.View{
			Style:    core.Style{Direction: core.Row, AlignItems: core.AlignCenter, MinWidth: core.Pt(0)},
			Children: metaChildren,
		})
	}
	if statusNode := a.pluginUpdateStatusNode(pal, info.ID); statusNode != nil {
		infoChildren = append(infoChildren, statusNode)
	}

	var actions []core.Node
	if updateBtn := a.pluginUpdateButtonNode(pal, p); updateBtn != nil {
		actions = append(actions, updateBtn)
	}
	if enabled {
		switch {
		case state.account != nil && p.Can("signOut"):
			actions = append(actions, a.settingsAction(pal, i18n.T("Sign Out"), "plugin-signout-"+info.ID, a.signingIn || state.signingOut, func() {
				a.signOutSource(p)
			}))
		case state.account == nil && p.Can("signIn") && (!p.Can("account") || state.asked):
			actions = append(actions, a.settingsPrimaryAction(pal, i18n.T("Sign In"), "plugin-signin-"+info.ID, a.signingIn, func() {
				a.signInSource(p)
			}))
		}
	}
	actions = append(actions,
		a.pluginMoreMenu(pal, p, enabled),
		settingsSwitch{
			palette:  pal,
			Touch:    phone,
			Label:    i18n.Tf("Enable %s", info.Name),
			TestID:   "plugin-enabled-" + info.ID,
			Checked:  func() bool { return enabled },
			Disabled: func() bool { return a.installer.updating[info.ID] },
			OnChange: func(on bool) { a.setPluginEnabled(p, on) },
		},
	)

	headerStyle := core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 10, MinHeight: core.Pt(28)}
	actionsStyle := core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, FlexShrink: 0}
	if phone {
		headerStyle.Direction = core.Column
		headerStyle.AlignItems = core.AlignStretch
		headerStyle.Gap = 8
		actionsStyle.Wrap = core.Wrap
		actionsStyle.JustifyContent = core.JustifyEnd
	}
	header := core.View{
		Style: headerStyle,
		Children: []core.Node{
			core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Direction: core.Column, Gap: 3}, Children: infoChildren},
			core.View{Style: actionsStyle, Children: actions},
		},
	}

	body := []core.Node{header}
	if pending := a.installer.pendingUpdates[info.ID]; pending != nil {
		body = append(body, a.inlineUpdateConfirmView(pal, info.ID, pending))
	}
	if enabled {
		if perms := a.pluginPermissionsView(pal, p); perms != nil {
			body = append(body, perms)
		}
		for _, st := range info.Settings {
			body = append(body, a.sourceSettingView(pal, p, st))
		}
	}

	pad := core.Symmetric(12, 16)
	if phone {
		pad = core.Symmetric(12, 12)
	}
	return core.View{
		TestID:   "plugin-card-" + info.ID,
		Style:    core.Style{Direction: core.Column, Padding: pad, Gap: 10},
		Children: body,
	}
}

func (a *app) pluginUpdateStatusNode(pal palette, id string) core.Node {
	if msg := a.installer.updateErr[id]; msg != "" {
		return core.Text{TestID: "plugin-status-" + id, Content: msg, Style: core.Style{FontSize: 12, Color: pal.accent, LineHeightMultiple: 1.4}}
	}
	switch a.installer.checked[id] {
	case checkChecking:
		return core.Text{TestID: "plugin-status-" + id, Content: i18n.T("Checking for updates…"), Style: core.Style{FontSize: 12, Color: pal.muted}}
	case checkUpToDate:
		return core.Text{TestID: "plugin-status-" + id, Content: i18n.T("Up to date"), Style: core.Style{FontSize: 12, Color: pal.muted}}
	case checkFailed:
		return core.Text{TestID: "plugin-status-" + id, Content: i18n.T("Couldn't check for updates"), Style: core.Style{FontSize: 12, Color: pal.accent}}
	}
	return nil
}

func (a *app) pluginUpdateButtonNode(pal palette, p *plugin.Plugin) core.Node {
	id := p.Info().ID
	if a.installer.pendingUpdates[id] != nil {
		return nil
	}
	ver := a.pluginAvailableUpdate(p)
	if ver == "" {
		return nil
	}
	updating := a.installer.updating[id]
	label := i18n.Tf("Update to %s", ver)
	if updating {
		label = i18n.T("Updating…")
	}
	return a.settingsAction(pal, label, "plugin-update-"+id, updating, func() {
		a.updatePlugin(p)
	})
}

func (a *app) inlineUpdateConfirmView(pal palette, id string, pending *plugin.Pending) core.Node {
	perms := pending.Manifest.Permissions
	var details []string
	switch {
	case slices.Contains(perms.Network, "*"):
		details = append(details, i18n.T("Can reach any website"))
	case len(perms.Network) > 0:
		details = append(details, i18n.Tf("Can reach %s", strings.Join(perms.Network, ", ")))
	default:
		details = append(details, i18n.T("Asks for no network"))
	}
	if len(perms.Process) > 0 {
		details = append(details, programsWarning(perms.Process))
	}
	updating := a.installer.updating[id]
	return core.View{
		TestID: "plugin-update-prompt-" + id,
		Style: core.Style{
			Direction:    core.Column,
			Gap:          6,
			Padding:      core.All(10),
			CornerRadius: 10,
			Background:   pal.hover,
			BorderWidth:  1,
			BorderColor:  pal.hairline,
		},
		Children: []core.Node{
			core.Text{Content: i18n.Tf("Update to %s requires additional permissions", pending.Version), Style: core.Style{FontSize: 12.5, FontWeight: core.FontWeightMedium, Color: pal.text}},
			core.Text{Content: strings.Join(details, "\n"), Style: core.Style{FontSize: 12, Color: pal.muted, LineHeightMultiple: 1.4}},
			core.View{
				Style: core.Style{Direction: core.Row, Gap: 8},
				Children: []core.Node{
					a.settingsPrimaryAction(pal, i18n.T("Confirm Update"), "plugin-update-confirm-"+id, updating, func() {
						a.confirmPluginUpdate(id)
					}),
					a.settingsAction(pal, i18n.T("Cancel"), "plugin-update-cancel-"+id, updating, func() {
						a.cancelPluginUpdate(id)
					}),
				},
			},
		},
	}
}

func (a *app) pluginMoreMenu(pal palette, p *plugin.Plugin, enabled bool) core.Node {
	info := p.Info()
	phone := a.settingsInMain && a.phoneLayout(a.cx)
	var items []core.Node
	if p.Origin() != nil {
		busy := a.installer.checked[info.ID] == checkChecking || a.installer.updating[info.ID]
		items = append(items, a.pluginMenuItem(pal, "plugin-check-update-"+info.ID, i18n.T("Check for Updates"), lucide.RefreshCw(), busy, func() {
			a.checkPluginUpdate(p)
		}))
	}
	if enabled {
		items = append(items, a.pluginMenuItem(pal, "plugin-open-"+info.ID, i18n.T("Open"), lucide.ExternalLink(), false, func() {
			a.switchSource(info.ID)
			a.closeSettings()
			if a.settingsWin != nil {
				a.settingsWin.Hide()
			}
			a.showMain()
		}))
	}
	if a.cx.Env().OS() != core.OSAndroid {
		items = append(items, a.pluginMenuItem(pal, "plugin-show-folder-"+info.ID, i18n.T("Show Folder"), lucide.FolderOpen(), false, func() {
			revealPluginDir(a.cx, p.Dir())
		}))
	}
	items = append(items,
		ui.MenuSeparator{},
		a.pluginMenuItem(pal, "plugin-remove-"+info.ID, i18n.T("Remove…"), lucideTrash, a.installer.busy || a.signingIn || a.installer.updating[info.ID], func() {
			a.installer.remove = info.ID
			a.installer.open = true
			a.installer.err = ""
			a.bump()
		}),
	)

	side := core.Points(28)
	if phone {
		side = 48
	}
	return base.Menu{
		TestID: "plugin-more-" + info.ID,
		Label:  i18n.Tf("More for %s", info.Name),
		Trigger: core.View{
			Style:    core.Style{Width: core.Pt(side), Height: core.Pt(side), AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
			Children: []core.Node{icon(lucideMore, 16, pal.muted)},
		},
		Style: core.Style{
			CornerRadius: 7,
			Hover:        &core.Style{Background: pal.hover},
			Expanded:     &core.Style{Background: pal.press},
		},
		Placement:  core.PlaceBottom,
		Align:      core.AlignEnd,
		Offset:     4,
		Margin:     8,
		PopupStyle: a.settingsMenuPopupStyle(pal),
		Children:   items,
	}
}

func (a *app) settingsMenuPopupStyle(pal palette) core.Style {
	fill, edge := core.RGB(0xf6f6f6), core.RGBA(0x000000, 0.12)
	if pal.dark {
		fill, edge = core.RGB(0x2b2b2e), core.RGBA(0xffffff, 0.14)
	}
	return core.Style{
		MinWidth:     core.Pt(188),
		Direction:    core.Column,
		Background:   fill,
		Color:        pal.text,
		Padding:      core.All(5),
		BorderWidth:  0.5,
		BorderColor:  edge,
		CornerRadius: 12,
		Shadows:      []core.BoxShadow{{Offset: core.Point{Y: 8}, Blur: 24, Spread: -4, Color: core.RGBA(0x000000, 0.2)}},
	}
}

func (a *app) pluginMenuItem(pal palette, testID, label string, svg *core.SVG, disabled bool, onSelect func()) core.Node {
	fg := pal.text
	iconColor := pal.muted
	if disabled {
		fg = pal.faint
		iconColor = pal.faint
	}
	minH := core.Points(30)
	if a.settingsInMain && a.phoneLayout(a.cx) {
		minH = 44
	}
	return base.MenuItem{
		TestID:   testID,
		Label:    label,
		Disabled: func() bool { return disabled },
		OnSelect: func() {
			if !disabled && onSelect != nil {
				onSelect()
			}
		},
		Style: core.Style{
			MinHeight:    core.Pt(minH),
			Direction:    core.Row,
			AlignItems:   core.AlignCenter,
			Padding:      core.Symmetric(0, 8),
			Gap:          8,
			CornerRadius: 7,
			Hover:        &core.Style{Background: pal.press},
			Focus:        &core.Style{Background: pal.press},
		},
		Children: []core.Node{
			icon(svg, 15, iconColor),
			core.Text{Content: label, MaxLines: 1, Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), FontSize: 13, Color: fg}},
		},
	}
}

func (a *app) pluginPermissionsView(pal palette, p *plugin.Plugin) core.Node {
	perms := p.Permissions()
	if len(perms.Network) == 0 && len(perms.Process) == 0 {
		return nil
	}
	var children []core.Node
	if len(perms.Network) > 0 {
		reach := i18n.Tf("Can reach %s", strings.Join(perms.Network, ", "))
		if slices.Contains(perms.Network, "*") {
			reach = i18n.T("Can reach any website")
		}
		children = append(children, core.Text{Content: reach, Style: core.Style{FontSize: 12, Color: pal.muted, LineHeightMultiple: 1.4}})
	}
	if len(perms.Process) > 0 {
		warning := programsWarning(perms.Process)
		android := a.cx.Env().OS() == core.OSAndroid
		if android {
			warning = i18n.T("Plugins that start desktop programs are unavailable on Android")
		}
		label := i18n.T("Allow Programs")
		if p.Granted() {
			label = i18n.T("Stop Allowing")
		}
		rowStyle := core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 10}
		if a.settingsInMain && a.phoneLayout(a.cx) {
			rowStyle.Direction = core.Column
			rowStyle.AlignItems = core.AlignStretch
			rowStyle.Gap = 8
		}
		children = append(children, core.View{
			Style: rowStyle,
			Children: []core.Node{
				core.Text{Content: warning, Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), FontSize: 12, Color: pal.muted, LineHeightMultiple: 1.4}},
				a.settingsAction(pal, label, "plugin-grant-"+p.Info().ID, android, func() {
					on := !p.Granted()
					a.sourceAction(p, func() error { return p.Grant(on) }, nil)
				}),
			},
		})
	}
	return core.View{Style: core.Style{Direction: core.Column, Gap: 6}, Children: children}
}

func programsWarning(programs plugin.Programs) string {
	if programs.Any() {
		return i18n.T("This plugin wants to start programs, which can do anything you can. Only allow it if you trust it.")
	}
	return i18n.Tf("This plugin wants to start %s, which can do anything you can. Only allow it if you trust it.", strings.Join(programs, ", "))
}

// sourceSettingView draws the native control declared by st.Type, falling back
// to a text field saved on submit or blur for legacy/unsupported metadata.
func (a *app) sourceSettingView(pal palette, p *plugin.Plugin, st music.Setting) core.Node {
	if node := a.typedSourceSettingView(pal, p, st); node != nil {
		return node
	}
	id := p.Info().ID
	mapKey := id + "\x00" + st.Key
	if a.settingText == nil {
		a.settingText = map[string]*string{}
	}
	draft := a.settingText[mapKey]
	if draft == nil {
		v := p.Setting(st.Key)
		draft = &v
		a.settingText[mapKey] = draft
	}
	inputHeight := core.Points(34)
	if a.settingsInMain && a.phoneLayout(a.cx) {
		inputHeight = 44
	}
	commit := func() {
		if *draft != a.pendingPluginSetting(p, st.Key) {
			a.keepPluginSetting(p, st.Key, *draft)
		}
	}
	children := []core.Node{
		core.Text{Content: st.Label, Style: core.Style{FontSize: 12.5, FontWeight: core.FontWeightMedium, Color: pal.text}},
		base.Input{
			TestID:   "plugin-setting-" + id + "/" + st.Key,
			Label:    st.Label,
			Value:    func() string { return *draft },
			OnChange: func(v string) { *draft = v },
			OnSubmit: func(string) { commit() },
			OnBlur:   func() { commit() },
			Style: core.Style{
				Width:        core.Pt(320),
				MaxWidth:     core.Pct(100),
				Height:       core.Pt(inputHeight),
				Padding:      core.Symmetric(0, 10),
				Background:   pal.hover,
				Color:        pal.text,
				BorderWidth:  1,
				BorderColor:  pal.hairline,
				CornerRadius: 8,
				FontSize:     13,
			},
		},
	}
	children = append(children, a.settingMetaNodes(pal, id, st)...)
	return core.View{Style: core.Style{Direction: core.Column, Gap: 4}, Children: children}
}

func (a *app) typedSourceSettingView(pal palette, p *plugin.Plugin, st music.Setting) core.Node {
	source := p.Setting(st.Key)
	switch st.Type {
	case "password":
	case "boolean":
		if source != "" && source != "true" && source != "false" {
			return nil
		}
	case "select":
		if !validSettingOptions(st.Options) {
			return nil
		}
	default:
		return nil
	}
	id := p.Info().ID
	mapKey := id + "\x00" + st.Key
	if a.settingEdits == nil {
		a.settingEdits = map[string]*settingEdit{}
	}
	draft := a.settingEdits[mapKey]
	if draft == nil || !draft.saving && !draft.failed && draft.value == draft.source && source != draft.source {
		draft = &settingEdit{source: source, value: source}
		a.settingEdits[mapKey] = draft
	}
	phone := a.settingsInMain && a.phoneLayout(a.cx)
	testID := "plugin-setting-" + id + "/" + st.Key
	var children []core.Node
	switch st.Type {
	case "boolean":
		children = append(children, core.View{
			Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 10},
			Children: []core.Node{
				settingsSwitch{
					palette:  pal,
					Touch:    phone,
					Label:    st.Label,
					TestID:   testID,
					Checked:  func() bool { return draft.value == "true" },
					Disabled: func() bool { return draft.saving },
					OnChange: func(on bool) {
						draft.value = strconv.FormatBool(on)
						a.keepTypedSetting(p, st.Key, draft)
					},
				},
				core.Text{Content: st.Label, Style: core.Style{FontSize: 12.5, FontWeight: core.FontWeightMedium, Color: pal.text}},
			},
		})
	case "select":
		options := make([]ui.Option[string], 0, len(st.Options)+1)
		found := false
		for _, opt := range st.Options {
			options = append(options, ui.Option[string]{Value: opt.Value, Label: opt.Label})
			if opt.Value == draft.value {
				found = true
			}
		}
		if !found {
			legacyLabel := fmt.Sprintf("%s [%s]", i18n.T("Current value"), draft.value)
			options = append([]ui.Option[string]{{Value: draft.value, Label: legacyLabel}}, options...)
		}
		children = append(children,
			core.Text{Content: st.Label, Style: core.Style{FontSize: 12.5, FontWeight: core.FontWeightMedium, Color: pal.text}},
			settingsChoice[string]{
				palette:  pal,
				Touch:    phone,
				Label:    st.Label,
				TestID:   testID,
				Options:  options,
				Value:    func() string { return draft.value },
				Disabled: func() bool { return draft.saving },
				OnChange: func(v string) {
					draft.value = v
					a.keepTypedSetting(p, st.Key, draft)
				},
				Style: core.Style{Width: core.Pt(240), MaxWidth: core.Pct(100)},
			},
		)
	case "password":
		inputHeight := core.Points(34)
		if phone {
			inputHeight = 44
		}
		children = append(children,
			core.Text{Content: st.Label, Style: core.Style{FontSize: 12.5, FontWeight: core.FontWeightMedium, Color: pal.text}},
			base.Input{
				TestID:   testID,
				Label:    st.Label,
				Masked:   true,
				Disabled: func() bool { return draft.saving },
				Value:    func() string { return draft.value },
				OnChange: func(v string) {
					draft.value = v
					if a.settingDraftRev != nil {
						a.settingDraftRev.Update(func(n int) int { return n + 1 })
					}
				},
				Style: core.Style{
					Width:        core.Pt(320),
					MaxWidth:     core.Pct(100),
					Height:       core.Pt(inputHeight),
					Padding:      core.Symmetric(0, 10),
					Background:   pal.hover,
					Color:        pal.text,
					BorderWidth:  1,
					BorderColor:  pal.hairline,
					CornerRadius: 8,
					FontSize:     13,
				},
			},
		)
	}
	if st.Type == "password" || draft.failed {
		children = append(children, core.Dynamic(func(*core.Scope) core.Node {
			if a.settingDraftRev != nil {
				a.settingDraftRev.Get()
			}
			dirty := draft.failed || draft.value != draft.source
			return core.View{
				Style: core.Style{Direction: core.Row, Gap: 8},
				Children: []core.Node{
					a.settingsAction(pal, i18n.T("Save changes"), "plugin-save-"+id+"/"+st.Key, draft.saving || !dirty, func() {
						a.keepTypedSetting(p, st.Key, draft)
					}),
					a.settingsAction(pal, i18n.T("Discard changes"), "plugin-discard-"+id+"/"+st.Key, draft.saving || !dirty, func() {
						if draft.failed {
							draft.value = draft.source
							a.keepTypedSetting(p, st.Key, draft)
						} else {
							delete(a.settingEdits, mapKey)
							a.bump()
						}
					}),
				},
			}
		}))
	}
	children = append(children, a.settingMetaNodes(pal, id, st)...)
	return core.View{Style: core.Style{Direction: core.Column, Gap: 4}, Children: children}
}

func (a *app) settingMetaNodes(pal palette, id string, st music.Setting) []core.Node {
	var nodes []core.Node
	if st.Help != "" {
		nodes = append(nodes, core.Text{Content: st.Help, Style: core.Style{FontSize: 11.5, Color: pal.muted, LineHeightMultiple: 1.4}})
	}
	if st.Link != "" {
		link := st.Link
		nodes = append(nodes, base.Button{
			TestID:   "plugin-help-" + id + "-" + st.Key,
			Label:    link,
			OnPress:  func(core.PressEvent) { a.cx.OpenURL(link, nil) },
			Style:    core.Style{AlignSelf: core.AlignStart},
			Children: []core.Node{core.Text{Content: link, Style: core.Style{FontSize: 11.5, Color: pal.accent}}},
		})
	}
	return nodes
}

// Ambiguous or incomplete selects fall back to text, never coercing a value.
func validSettingOptions(options []music.SettingOption) bool {
	if len(options) == 0 {
		return false
	}
	values, labels := map[string]bool{}, map[string]bool{}
	for _, option := range options {
		if option.Label == "" || values[option.Value] || labels[option.Label] {
			return false
		}
		values[option.Value], labels[option.Label] = true, true
	}
	return true
}

func (a *app) keepTypedSetting(p *plugin.Plugin, key string, draft *settingEdit) {
	if draft.saving || !draft.failed && draft.value == draft.source {
		return
	}
	draft.saving = true
	value := draft.value
	a.bump()
	a.savePluginSetting(p, key, value, func(err error) {
		draft.saving, draft.failed = false, err != nil
		if err == nil {
			draft.source = value
		}
	})
}

func (a *app) keepPluginSetting(p *plugin.Plugin, key, value string) {
	a.savePluginSetting(p, key, value, nil)
}

func (a *app) pendingPluginSetting(p *plugin.Plugin, key string) string {
	if pending := a.settingPending[p.Info().ID+"\x00"+key]; pending != nil {
		return pending.value
	}
	return p.Setting(key)
}

// savePluginSetting serializes writes per plugin so concurrent UI controls
// cannot race settings.json on disk.
func (a *app) savePluginSetting(p *plugin.Plugin, key, value string, done func(error)) {
	if done == nil && a.pendingPluginSetting(p, key) == value {
		return
	}
	if a.settingPending == nil {
		a.settingPending = map[string]*settingWrite{}
	}
	id := p.Info().ID
	pendingKey := id + "\x00" + key
	pending := &settingWrite{value: value}
	a.settingPending[pendingKey] = pending
	if a.settingSaves == nil {
		a.settingSaves = map[*plugin.Plugin]<-chan struct{}{}
	}
	previous := a.settingSaves[p]
	finished := make(chan struct{})
	a.settingSaves[p] = finished
	go func() {
		if previous != nil {
			<-previous
		}
		err := p.SetSetting(key, value)
		close(finished)
		a.post(func() {
			if a.settingSaves[p] == finished {
				delete(a.settingSaves, p)
			}
			if a.settingPending[pendingKey] == pending {
				delete(a.settingPending, pendingKey)
			}
			if done != nil {
				done(err)
			}
			if err != nil {
				a.say(i18n.Tf("Couldn't keep the setting: %s", err.Error()))
				a.bump()
				return
			}
			if a.source() == p {
				a.reload()
			}
			a.invalidateAccount(p)
			a.loadAccount(p)
			a.bump()
		})
	}()
}

func (a *app) settingsPrimaryAction(p palette, label, testID string, disabled bool, onPress func()) core.Node {
	bg, fg := p.accent, p.onAccent
	style := core.Style{Padding: core.Symmetric(4, 10), CornerRadius: 7, Background: bg}
	fontSize := core.Points(12)
	if a.phoneLayout(a.cx) {
		style.MinHeight = core.Pt(48)
		style.AlignItems = core.AlignCenter
		style.JustifyContent = core.JustifyCenter
		fontSize = phoneFontBody
	}
	if disabled {
		fg = p.faint
		style.Background = p.hover
	} else {
		style.Hover = &core.Style{Background: blend(bg, core.RGB(0xffffff), 0.12)}
		style.Active = &core.Style{Background: blend(bg, core.RGB(0x000000), 0.12)}
	}
	return base.Button{Label: label, TestID: testID, Disabled: func() bool { return disabled },
		OnPress: func(core.PressEvent) {
			if !disabled && onPress != nil {
				onPress()
			}
		}, Style: style, Children: []core.Node{text(label, fontSize, core.FontWeightMedium, fg)}}
}
