package app

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"
	"rsc.io/qr"

	"github.com/HengWh/woodwind-music/internal/i18n"
	"github.com/HengWh/woodwind-music/internal/local"
	"github.com/HengWh/woodwind-music/internal/plugin"
)

// pluginCheckEvery is how often plugins and feeds are quietly checked for
// updates: once a day to stay well within GitHub's unauthenticated rate limit.
const pluginCheckEvery = 24 * time.Hour

// Network and filesystem hooks for installing, updating, and reading feeds;
// tests replace these to avoid real network access.
var (
	fetchPlugin     = plugin.Fetch
	fetchPinned     = plugin.FetchPinned
	fetchRelease    = plugin.FetchRelease
	latestPlugin    = plugin.Latest
	openPlugin      = plugin.Open
	fetchPluginFeed = plugin.FetchFeed
	fetchFeedEntry  = plugin.FetchFeedEntry
)

type pluginCheckStatus int

const (
	checkChecking pluginCheckStatus = iota + 1
	checkUpToDate
	checkFailed
)

// pluginFeedSub persists a user-subscribed JSON plugin feed and its last
// fetched entries so Settings stays responsive offline.
type pluginFeedSub struct {
	URL         string             `json:"url"`
	Name        string             `json:"name,omitempty"`
	Description string             `json:"description,omitempty"`
	Homepage    string             `json:"homepage,omitempty"`
	CheckedAt   time.Time          `json:"checkedAt,omitzero"`
	Plugins     []plugin.FeedEntry `json:"plugins,omitempty"`
}

type pluginInstaller struct {
	open, github, busy bool
	repo, err, remove  string
	pending            *plugin.Pending

	feedURL        string
	feedBusy       bool
	feedErr        string
	feedRef        *core.Ref
	feedRefreshing map[string]bool
	feedErrors     map[string]string

	checkingAll    bool
	updating       map[string]bool
	updateErr      map[string]string
	checked        map[string]pluginCheckStatus
	found          map[string]*plugin.Release
	pendingUpdates map[string]*plugin.Pending
}

func (a *app) cancelPluginDialog() {
	if a.installer.busy {
		return
	}
	if p := a.installer.pending; p != nil {
		p.Discard()
	}
	a.installer.pending = nil
	a.installer.open = false
	a.installer.github = false
	a.installer.remove = ""
	a.installer.err = ""
	a.bump()
}

func (a *app) beginInstall() {
	a.installer.open = true
	a.installer.github = true
	a.installer.remove = ""
	a.installer.err = ""
	a.bump()
}

func (a *app) preparePlugin(work func() (*plugin.Pending, error)) {
	if a.installer.busy {
		return
	}
	if p := a.installer.pending; p != nil {
		p.Discard()
		a.installer.pending = nil
	}
	a.installer.open = true
	a.installer.busy = true
	a.installer.remove = ""
	a.installer.err = ""
	a.bump()
	go func() {
		pending, err := work()
		a.post(func() {
			a.installer.busy = false
			if err != nil {
				a.installer.err = installErrorText(err)
			} else if pending.Manifest.ID == local.ID {
				pending.Discard()
				a.installer.err = i18n.T("This plugin ID is reserved")
			} else {
				a.installer.pending = pending
			}
			a.bump()
		})
	}()
}

func installErrorText(err error) string {
	if errors.Is(err, plugin.ErrNoRepo) {
		return i18n.T("There's no such public repository on GitHub")
	}
	return err.Error()
}

func (a *app) installRepo() {
	raw := strings.TrimSpace(a.installer.repo)
	if raw == "" {
		return
	}
	repo, err := plugin.ParseRepo(raw)
	if err != nil {
		a.installer.err = i18n.T("Write a repository as owner/repo, or its address on GitHub")
		a.bump()
		return
	}
	a.preparePlugin(func() (*plugin.Pending, error) { return fetchPlugin(repo, a.pluginDir()) })
}

func (a *app) pickPluginZip() {
	a.post(func() {
		a.cx.PickFiles(core.PickFilesOptions{Types: []string{"application/zip", ".zip"}, Limit: 1}, func(files []core.File, err error) {
			if err != nil {
				a.say(err.Error())
				return
			}
			if len(files) == 0 {
				return
			}
			file := files[0]
			a.installer.github = false
			a.preparePlugin(func() (*plugin.Pending, error) {
				if file.Path != "" {
					return openPlugin(file.Path, a.pluginDir())
				}
				// Android's document provider grants a reader, not a filesystem path.
				in, err := file.Open()
				if err != nil {
					return nil, err
				}
				defer in.Close()
				tmp, err := os.CreateTemp(a.dir, "plugin-*.zip")
				if err != nil {
					return nil, err
				}
				defer os.Remove(tmp.Name())
				n, err := io.Copy(tmp, io.LimitReader(in, (1<<30)+1))
				closeErr := tmp.Close()
				if err != nil {
					return nil, err
				}
				if closeErr != nil {
					return nil, closeErr
				}
				if n > 1<<30 {
					return nil, fmt.Errorf("plugin archive is too large")
				}
				return openPlugin(tmp.Name(), a.pluginDir())
			})
		})
	})
}

func (a *app) pickPluginFolder() {
	if a.cx.Env().OS() == core.OSAndroid {
		return
	}
	a.post(func() {
		a.cx.PickDirectories(core.PickDirectoriesOptions{Multiple: false}, func(dirs []core.Directory, err error) {
			if err != nil {
				a.say(err.Error())
				return
			}
			if len(dirs) == 0 || dirs[0].Path == "" {
				return
			}
			path := dirs[0].Path
			a.installer.github = false
			a.preparePlugin(func() (*plugin.Pending, error) {
				return openPlugin(path, a.pluginDir())
			})
		})
	})
}

func (a *app) confirmPluginInstall() {
	pending := a.installer.pending
	if pending == nil || a.installer.busy {
		return
	}
	a.installer.busy = true
	a.bump()
	go func() {
		err := pending.Install(a.pluginDir())
		var p *plugin.Plugin
		if err == nil {
			p, err = plugin.Load(filepath.Join(a.pluginDir(), pending.Manifest.ID), a.pluginDataDir())
		}
		a.post(func() {
			a.installer.pending = nil
			a.installer.busy = false
			if err != nil {
				a.installer.err = installErrorText(err)
				a.bump()
				return
			}
			a.adoptInstalledPlugin(p)
			a.installer.repo = ""
			a.installer.github = false
			a.installer.open = false
			a.bump()
			a.say(i18n.T("Plugin installed"))
		})
	}()
}

func (a *app) adoptInstalledPlugin(p *plugin.Plugin) {
	id := p.Info().ID
	delete(a.set.PluginUpdates, id)
	a.set.save()
	delete(a.installer.updating, id)
	delete(a.installer.updateErr, id)
	delete(a.installer.checked, id)
	delete(a.installer.found, id)
	if oldPending := a.installer.pendingUpdates[id]; oldPending != nil {
		oldPending.Discard()
		delete(a.installer.pendingUpdates, id)
	}
	wasActive := a.source().Info().ID == id
	replaced := false
	for i, old := range a.plugins {
		if old.Info().ID == id {
			go old.Close()
			a.plugins[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		a.plugins = append(a.plugins, p)
	}
	if a.pluginEnabled(id) {
		a.registerPlugin(p)
		if wasActive {
			a.active = a.local
			a.activateSource(id)
		}
	}
}

func (a *app) removePlugin(id string) {
	for i, p := range a.plugins {
		if p.Info().ID == id {
			if a.disk != nil {
				a.disk.RemoveSource(id)
			}
			a.pl.DetachSource(id)
			if a.source().Info().ID == id {
				a.switchSource(local.ID)
			}
			a.sources.Lock()
			delete(a.sources.items, id)
			a.sources.Unlock()
			delete(a.accounts, id)
			delete(a.sourceHomes, id)
			delete(a.set.PluginUpdates, id)
			a.set.DisabledPlugins = slices.DeleteFunc(slices.Clone(a.set.DisabledPlugins), func(s string) bool { return s == id })
			a.set.save()
			delete(a.installer.found, id)
			delete(a.installer.checked, id)
			delete(a.installer.updateErr, id)
			if oldPending := a.installer.pendingUpdates[id]; oldPending != nil {
				oldPending.Discard()
				delete(a.installer.pendingUpdates, id)
			}
			a.plugins = append(a.plugins[:i], a.plugins[i+1:]...)
			a.installer.remove = ""
			a.installer.open = false
			a.installer.busy = true
			a.bump()
			cx := a.cx
			go func() {
				err := p.Remove(func(path string) error { return trashPluginDir(cx, path) })
				a.post(func() {
					a.installer.busy = false
					if err != nil {
						a.say(err.Error())
					}
					a.bump()
				})
			}()
			return
		}
	}
}

// --- Plugin Feeds (JSON subscriptions) ---

func (a *app) subscribePluginFeed() {
	if a.installer.feedBusy {
		return
	}
	raw := strings.TrimSpace(a.installer.feedURL)
	cleanURL, err := plugin.ValidateFeedURL(raw)
	if err != nil {
		a.installer.feedErr = err.Error()
		a.bump()
		return
	}
	for _, existing := range a.set.PluginFeeds {
		if existing.URL == cleanURL {
			a.installer.feedErr = i18n.T("Already subscribed to this plugin feed")
			a.bump()
			return
		}
	}
	a.installer.feedBusy = true
	a.installer.feedErr = ""
	a.bump()
	go func() {
		feed, err := fetchPluginFeed(cleanURL)
		a.post(func() {
			a.installer.feedBusy = false
			if err != nil {
				a.installer.feedErr = err.Error()
				a.bump()
				return
			}
			sub := pluginFeedSub{
				URL:         feed.URL,
				Name:        feed.Name,
				Description: feed.Description,
				Homepage:    feed.Homepage,
				CheckedAt:   time.Now(),
				Plugins:     feed.Plugins,
			}
			a.set.PluginFeeds = append(a.set.PluginFeeds, sub)
			a.installer.feedURL = ""
			a.installer.feedErr = ""
			a.applyFeedUpdates(feed)
			a.set.save()
			a.bump()
		})
	}()
}

func (a *app) refreshPluginFeed(feedURL string) {
	if a.installer.feedRefreshing == nil {
		a.installer.feedRefreshing = map[string]bool{}
	}
	if a.installer.feedErrors == nil {
		a.installer.feedErrors = map[string]string{}
	}
	if a.installer.feedRefreshing[feedURL] {
		return
	}
	a.installer.feedRefreshing[feedURL] = true
	delete(a.installer.feedErrors, feedURL)
	a.bump()
	go func() {
		feed, err := fetchPluginFeed(feedURL)
		a.post(func() {
			delete(a.installer.feedRefreshing, feedURL)
			if err != nil {
				a.installer.feedErrors[feedURL] = err.Error()
				a.bump()
				return
			}
			for i := range a.set.PluginFeeds {
				if a.set.PluginFeeds[i].URL == feedURL {
					a.set.PluginFeeds[i].Name = feed.Name
					a.set.PluginFeeds[i].Description = feed.Description
					a.set.PluginFeeds[i].Homepage = feed.Homepage
					a.set.PluginFeeds[i].CheckedAt = time.Now()
					a.set.PluginFeeds[i].Plugins = feed.Plugins
					break
				}
			}
			a.applyFeedUpdates(feed)
			a.set.save()
			a.bump()
		})
	}()
}

func (a *app) removePluginFeed(feedURL string) {
	a.set.PluginFeeds = slices.DeleteFunc(slices.Clone(a.set.PluginFeeds), func(f pluginFeedSub) bool {
		return f.URL == feedURL
	})
	delete(a.installer.feedRefreshing, feedURL)
	delete(a.installer.feedErrors, feedURL)
	a.set.save()
	a.bump()
}

// applyFeedUpdates inspects a freshly fetched feed and records any newer
// versions for installed plugins that belong to it, without hitting GitHub.
func (a *app) applyFeedUpdates(feed *plugin.Feed) {
	if feed == nil {
		return
	}
	entries := make(map[string]plugin.FeedEntry, len(feed.Plugins))
	for _, e := range feed.Plugins {
		entries[e.ID] = e
	}
	for _, p := range a.plugins {
		id := p.Info().ID
		o := p.Origin()
		if o == nil {
			continue
		}
		entry, ok := entries[id]
		if !ok || entry.Version == "" {
			continue
		}
		if o.FeedURL != feed.URL && (o.Repo == "" || o.Repo != entry.Repo) {
			continue
		}
		if plugin.IsNewer(entry.Version, o.Version) {
			if a.set.PluginUpdates == nil {
				a.set.PluginUpdates = map[string]string{}
			}
			a.set.PluginUpdates[id] = entry.Version
			if rel, err := plugin.ReleaseFromFeedEntry(feed.URL, entry); err == nil {
				if a.installer.found == nil {
					a.installer.found = map[string]*plugin.Release{}
				}
				a.installer.found[id] = rel
			}
		} else if o.FeedURL == feed.URL {
			delete(a.set.PluginUpdates, id)
			delete(a.installer.found, id)
		}
	}
}

// --- Quiet Background & Inline Update Checks (No Popups) ---

func (a *app) schedulePluginUpdateChecks() {
	if a.dir == "" {
		return
	}
	a.checkPluginUpdates(false)
	a.pluginCheckTimer = time.AfterFunc(pluginCheckEvery, func() {
		a.post(func() {
			if !a.cx.Disposed() {
				a.schedulePluginUpdateChecks()
			}
		})
	})
}

func (a *app) pluginAvailableUpdate(p *plugin.Plugin) string {
	if p == nil {
		return ""
	}
	o := p.Origin()
	if o == nil {
		return ""
	}
	ver, ok := a.set.PluginUpdates[p.Info().ID]
	if !ok || ver == "" || !plugin.IsNewer(ver, o.Version) {
		return ""
	}
	return ver
}

func (a *app) pluginUpdateCount() int {
	n := 0
	for _, p := range a.plugins {
		if a.pluginAvailableUpdate(p) != "" {
			n++
		}
	}
	return n
}

// checkPluginUpdates quietly checks subscribed feeds and installed plugin
// origins for newer versions, updating inline state only (never opening dialogs).
func (a *app) checkPluginUpdates(force bool) {
	if a.installer.checkingAll {
		return
	}
	if !force && !a.set.PluginsChecked.IsZero() && time.Since(a.set.PluginsChecked) < pluginCheckEvery {
		return
	}
	origins := map[string]plugin.Origin{}
	for _, p := range a.plugins {
		if o := p.Origin(); o != nil {
			origins[p.Info().ID] = *o
		}
	}
	feedURLs := make([]string, 0, len(a.set.PluginFeeds))
	for _, f := range a.set.PluginFeeds {
		feedURLs = append(feedURLs, f.URL)
	}
	if len(origins) == 0 && len(feedURLs) == 0 {
		return
	}
	a.installer.checkingAll = true
	a.set.PluginsChecked = time.Now()
	a.set.save()
	a.bump()

	go func() {
		fetchedFeeds := map[string]*plugin.Feed{}
		feedErrs := map[string]string{}
		for _, u := range feedURLs {
			feed, err := fetchPluginFeed(u)
			if err != nil {
				log.Printf("checking plugin feed %s: %v", u, err)
				feedErrs[u] = err.Error()
				continue
			}
			fetchedFeeds[u] = feed
		}

		found := map[string]string{}
		releases := map[string]*plugin.Release{}
		for id, o := range origins {
			rel, err := latestForOrigin(id, o, fetchedFeeds)
			if err != nil {
				log.Printf("checking plugin %s: %v", id, err)
				continue
			}
			if rel != nil && plugin.IsNewer(rel.Version, o.Version) {
				found[id] = rel.Version
				releases[id] = rel
			}
		}

		a.post(func() {
			a.installer.checkingAll = false
			if a.installer.feedErrors == nil {
				a.installer.feedErrors = map[string]string{}
			}
			for i := range a.set.PluginFeeds {
				u := a.set.PluginFeeds[i].URL
				if feed, ok := fetchedFeeds[u]; ok {
					a.set.PluginFeeds[i].Name = feed.Name
					a.set.PluginFeeds[i].Description = feed.Description
					a.set.PluginFeeds[i].Homepage = feed.Homepage
					a.set.PluginFeeds[i].CheckedAt = time.Now()
					a.set.PluginFeeds[i].Plugins = feed.Plugins
					delete(a.installer.feedErrors, u)
				} else if msg := feedErrs[u]; msg != "" {
					a.installer.feedErrors[u] = msg
				}
			}
			a.set.PluginUpdates = found
			a.installer.found = releases
			a.set.save()
			a.bump()
		})
	}()
}

// latestForOrigin resolves the latest release for an installed plugin. Plugins
// installed from a subscribed feed check the feed JSON first so they do not
// consume GitHub's anonymous API quota.
func latestForOrigin(id string, o plugin.Origin, feeds map[string]*plugin.Feed) (*plugin.Release, error) {
	if o.FeedURL != "" {
		feed := feeds[o.FeedURL]
		if feed == nil {
			var err error
			feed, err = fetchPluginFeed(o.FeedURL)
			if err != nil {
				if o.Repo != "" {
					return latestPlugin(o.Repo)
				}
				return nil, err
			}
		}
		for _, entry := range feed.Plugins {
			if entry.ID == id {
				if entry.DownloadURL != "" || entry.Commit != "" {
					return plugin.ReleaseFromFeedEntry(feed.URL, entry)
				}
				if entry.Version != "" && !plugin.IsNewer(entry.Version, o.Version) {
					return &plugin.Release{Repo: entry.Repo, Version: entry.Version, FeedURL: feed.URL}, nil
				}
				if entry.Repo != "" {
					rel, err := latestPlugin(entry.Repo)
					if err != nil {
						return nil, err
					}
					rel.FeedURL = feed.URL
					return rel, nil
				}
			}
		}
	}
	if o.Repo != "" {
		return latestPlugin(o.Repo)
	}
	return nil, nil
}

// checkPluginUpdate checks one installed plugin now and displays the result
// inline in its settings row (never via popup).
func (a *app) checkPluginUpdate(p *plugin.Plugin) {
	if p == nil || p.Origin() == nil {
		return
	}
	id := p.Info().ID
	o := *p.Origin()
	in := &a.installer
	if in.checked == nil {
		in.checked = map[string]pluginCheckStatus{}
	}
	if in.checked[id] == checkChecking {
		return
	}
	in.checked[id] = checkChecking
	delete(in.updateErr, id)
	a.bump()
	go func() {
		rel, err := latestForOrigin(id, o, nil)
		a.post(func() {
			switch {
			case err != nil:
				log.Printf("checking plugin %s: %v", id, err)
				in.checked[id] = checkFailed
			case rel != nil && plugin.IsNewer(rel.Version, o.Version):
				delete(in.checked, id)
				if a.set.PluginUpdates == nil {
					a.set.PluginUpdates = map[string]string{}
				}
				a.set.PluginUpdates[id] = rel.Version
				if in.found == nil {
					in.found = map[string]*plugin.Release{}
				}
				in.found[id] = rel
				a.set.save()
			default:
				in.checked[id] = checkUpToDate
				delete(a.set.PluginUpdates, id)
				delete(in.found, id)
				a.set.save()
			}
			a.bump()
		})
	}()
}

// updatePlugin downloads and applies an update inline. If the new version asks
// for permissions the old version did not have, it shows an inline confirmation
// banner inside the plugin's settings card instead of opening a popup.
func (a *app) updatePlugin(p *plugin.Plugin) {
	if p == nil {
		return
	}
	o := p.Origin()
	if o == nil {
		return
	}
	id := p.Info().ID
	in := &a.installer
	if in.updating == nil {
		in.updating = map[string]bool{}
	}
	if in.updating[id] {
		return
	}
	if in.updateErr == nil {
		in.updateErr = map[string]string{}
	}
	in.updating[id] = true
	delete(in.updateErr, id)
	delete(in.checked, id)
	had := p.Permissions()
	rel := in.found[id]
	if rel != nil && rel.Version != a.set.PluginUpdates[id] {
		rel = nil
	}
	originCopy := *o
	a.bump()
	go func() {
		var pending *plugin.Pending
		var err error
		if rel == nil {
			rel, err = latestForOrigin(id, originCopy, nil)
		}
		if err == nil && rel != nil {
			pending, err = fetchRelease(rel, a.pluginDir())
		} else if err == nil {
			err = fmt.Errorf("no update source available")
		}
		if err == nil && asksForMore(had, pending.Manifest.Permissions) {
			a.post(func() {
				delete(in.updating, id)
				if in.pendingUpdates == nil {
					in.pendingUpdates = map[string]*plugin.Pending{}
				}
				if old := in.pendingUpdates[id]; old != nil {
					old.Discard()
				}
				in.pendingUpdates[id] = pending
				a.bump()
			})
			return
		}
		var loaded *plugin.Plugin
		if err == nil {
			err = pending.Install(a.pluginDir())
		}
		if err == nil {
			loaded, err = plugin.Load(filepath.Join(a.pluginDir(), pending.Manifest.ID), a.pluginDataDir())
		}
		a.post(func() {
			delete(in.updating, id)
			if err != nil {
				in.updateErr[id] = installErrorText(err)
			} else {
				a.adoptInstalledPlugin(loaded)
			}
			a.bump()
		})
	}()
}

func (a *app) confirmPluginUpdate(id string) {
	pending := a.installer.pendingUpdates[id]
	if pending == nil || a.installer.updating[id] {
		return
	}
	if a.installer.updating == nil {
		a.installer.updating = map[string]bool{}
	}
	delete(a.installer.pendingUpdates, id)
	a.installer.updating[id] = true
	delete(a.installer.updateErr, id)
	a.bump()
	go func() {
		err := pending.Install(a.pluginDir())
		var loaded *plugin.Plugin
		if err == nil {
			loaded, err = plugin.Load(filepath.Join(a.pluginDir(), pending.Manifest.ID), a.pluginDataDir())
		}
		a.post(func() {
			delete(a.installer.updating, id)
			if err != nil {
				if a.installer.updateErr == nil {
					a.installer.updateErr = map[string]string{}
				}
				a.installer.updateErr[id] = installErrorText(err)
			} else {
				a.adoptInstalledPlugin(loaded)
			}
			a.bump()
		})
	}()
}

func (a *app) cancelPluginUpdate(id string) {
	if pending := a.installer.pendingUpdates[id]; pending != nil {
		pending.Discard()
		delete(a.installer.pendingUpdates, id)
		a.bump()
	}
}

func (a *app) updateAllPlugins() {
	for _, p := range a.plugins {
		if a.pluginAvailableUpdate(p) != "" {
			a.updatePlugin(p)
		}
	}
}

// asksForMore reports whether a new version asks for anything the old didn't.
func asksForMore(had, wants plugin.Permissions) bool {
	return morePerms(had.Process, wants.Process) || morePerms(had.Network, wants.Network)
}

func morePerms(had, wants []string) bool {
	if slices.Contains(had, "*") {
		return false
	}
	for _, w := range wants {
		if !slices.Contains(had, w) {
			return true
		}
	}
	return false
}

// --- Settings > Plugins Page Layout ---

func (a *app) pluginHeaderActions(pal palette) core.Node {
	phone := a.settingsInMain && a.phoneLayout(a.cx)
	side := core.Points(28)
	if phone {
		side = 48
	}
	var children []core.Node
	if n := a.pluginUpdateCount(); n >= 2 {
		children = append(children, a.settingsAction(pal, i18n.Tf("Update All (%d)", n), "plugin-update-all", false, a.updateAllPlugins))
	}
	children = append(children,
		base.Button{
			TestID:   "plugin-check-all",
			Label:    i18n.T("Check for Updates"),
			Disabled: func() bool { return a.installer.checkingAll },
			OnPress: func(core.PressEvent) {
				if !a.installer.checkingAll {
					a.checkPluginUpdates(true)
				}
			},
			Style: core.Style{
				Width:          core.Pt(side),
				Height:         core.Pt(side),
				CornerRadius:   7,
				AlignItems:     core.AlignCenter,
				JustifyContent: core.JustifyCenter,
				Hover:          &core.Style{Background: pal.hover},
				Active:         &core.Style{Background: pal.press},
			},
			Children: []core.Node{icon(lucide.RefreshCw(), 15, pal.muted)},
		},
		a.pluginInstallMenu(pal, side),
	)
	return core.View{
		Style:    core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, FlexShrink: 0},
		Children: children,
	}
}

func (a *app) pluginInstallMenu(pal palette, side core.Points) core.Node {
	in := &a.installer
	items := []core.Node{
		a.pluginMenuItem(pal, "plugin-install-github", i18n.T("From GitHub…"), lucide.Globe(), in.busy, a.beginInstall),
	}
	if a.cx.Env().OS() != core.OSAndroid {
		items = append(items, a.pluginMenuItem(pal, "plugin-install-folder", i18n.T("From a Folder…"), lucideFolder, in.busy, a.pickPluginFolder))
	}
	items = append(items,
		a.pluginMenuItem(pal, "plugin-install-zip", i18n.T("From a Zip…"), lucide.FileArchive(), in.busy, a.pickPluginZip),
		a.pluginMenuItem(pal, "plugin-install-feed", i18n.T("Subscribe to Plugin Feed…"), lucide.Rss(), false, func() {
			if a.installer.feedRef != nil {
				a.installer.feedRef.Focus()
			}
		}),
	)
	if a.cx.Env().OS() != core.OSAndroid {
		items = append(items,
			ui.MenuSeparator{},
			a.pluginMenuItem(pal, "plugin-show-folder", i18n.T("Show Plugins Folder"), lucide.FolderOpen(), false, func() {
				os.MkdirAll(a.pluginDir(), 0o755)
				revealPluginDir(a.cx, a.pluginDir())
			}),
		)
	}
	return base.Menu{
		TestID: "plugin-install",
		Label:  i18n.T("Install a Plugin"),
		Trigger: core.View{
			Style:    core.Style{Width: core.Pt(side), Height: core.Pt(side), AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
			Children: []core.Node{icon(lucidePlus, 16, pal.muted)},
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

func (a *app) pluginCategories() settingsCatDef {
	var groups []settingsGroupDef
	if len(a.plugins) > 0 {
		var blocks []settingsBlockDef
		for _, plug := range a.plugins {
			blocks = append(blocks, a.pluginSettingsBlock(plug))
		}
		groups = append(groups, settingsGroupDef{blocks: blocks})
	} else {
		groups = append(groups, settingsGroupDef{
			rows: []settingsRowDef{{
				label: i18n.T("No plugins installed"),
				sub:   i18n.T("Use + above to install from GitHub or a ZIP file, or choose a plugin below."),
			}},
		})
	}
	groups = append(groups, a.pluginFeedsGroup())
	groups = append(groups, a.pluginCatalog())
	groups = append(groups, a.subscribedCatalogGroups()...)

	badge := ""
	if n := a.pluginUpdateCount(); n > 0 {
		badge = strconv.Itoa(n)
	}
	return settingsCatDef{
		id:           "plugins",
		label:        i18n.T("Plugins"),
		icon:         lucide.Puzzle(),
		badge:        badge,
		headerAction: a.pluginHeaderActions,
		groups:       groups,
	}
}

func (a *app) pluginFeedsGroup() settingsGroupDef {
	if a.installer.feedRef == nil {
		a.installer.feedRef = new(core.Ref)
	}
	blocks := []settingsBlockDef{{
		searchText: i18n.T("Plugin Feeds") + " " + i18n.T("Subscribe to a JSON plugin catalog URL"),
		render: func(pal palette) core.Node {
			phone := a.settingsInMain && a.phoneLayout(a.cx)
			inputHeight := core.Points(34)
			pad := core.Symmetric(12, 16)
			if phone {
				inputHeight = 44
				pad = core.Symmetric(10, 12)
			}
			rowStyle := core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8}
			if phone {
				rowStyle.Direction = core.Column
				rowStyle.AlignItems = core.AlignStretch
			}
			label := i18n.T("Subscribe")
			if a.installer.feedBusy {
				label = i18n.T("Loading…")
			}
			children := []core.Node{
				core.Text{Content: i18n.T("Subscribe to a JSON plugin catalog URL"), Style: core.Style{FontSize: 12, Color: pal.muted}},
				core.View{
					Style: rowStyle,
					Children: []core.Node{
						base.Input{
							Ref:         a.installer.feedRef,
							TestID:      "plugin-feed-url",
							Label:       i18n.T("Plugin Feed URL"),
							Placeholder: "https://example.com/plugins.json",
							Value:       func() string { return a.installer.feedURL },
							OnChange:    func(v string) { a.installer.feedURL = v; a.installer.feedErr = "" },
							OnSubmit:    func(string) { a.subscribePluginFeed() },
							Style: core.Style{
								FlexGrow:     1,
								FlexShrink:   1,
								MinWidth:     core.Pt(0),
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
						a.settingsAction(pal, label, "plugin-feed-add", a.installer.feedBusy, a.subscribePluginFeed),
					},
				},
			}
			if a.installer.feedErr != "" {
				children = append(children, core.Text{TestID: "plugin-feed-error", Content: a.installer.feedErr, Style: core.Style{FontSize: 12, Color: pal.accent, LineHeightMultiple: 1.4}})
			}
			return core.View{Style: core.Style{Direction: core.Column, Padding: pad, Gap: 8}, Children: children}
		},
	}}

	var rows []settingsRowDef
	for i, sub := range a.set.PluginFeeds {
		sub := sub
		idx := strconv.Itoa(i)
		name := sub.Name
		if name == "" {
			name = sub.URL
		}
		meta := sub.URL
		if len(sub.Plugins) > 0 {
			meta += " · " + i18n.Tf("%d plugins", len(sub.Plugins))
		}
		if !sub.CheckedAt.IsZero() {
			meta += " · " + i18n.Ago(sub.CheckedAt, time.Now())
		}
		if errMsg := a.installer.feedErrors[sub.URL]; errMsg != "" {
			meta += "\n" + errMsg
		}
		rows = append(rows, settingsRowDef{
			label: name,
			sub:   meta,
			control: func(pal palette) core.Node {
				refreshing := a.installer.feedRefreshing[sub.URL]
				refreshLabel := i18n.T("Refresh")
				if refreshing {
					refreshLabel = i18n.T("Loading…")
				}
				return core.View{
					Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8},
					Children: []core.Node{
						a.settingsAction(pal, refreshLabel, "plugin-feed-refresh-"+idx, refreshing, func() {
							a.refreshPluginFeed(sub.URL)
						}),
						a.settingsAction(pal, i18n.T("Remove"), "plugin-feed-remove-"+idx, refreshing, func() {
							a.removePluginFeed(sub.URL)
						}),
					},
				}
			},
		})
	}
	return settingsGroupDef{title: i18n.T("Plugin Feeds"), blocks: blocks, rows: rows}
}

func (a *app) pluginDialogs(cx *core.Scope, settings bool) core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		a.rev.Get()
		if settings && a.settingsInMain || !settings && a.settingsWin != nil && a.settingsWin.Visible() {
			return core.Group{}
		}
		p := a.palette(cx)
		in := &a.installer
		dialog := ui.Dialog{TestID: "plugin-dialog", Open: func() bool { return in.open }, OnOpenChange: func(open bool, _ base.Reason) {
			if !open {
				a.cancelPluginDialog()
			}
		}, Title: i18n.T("Install a Plugin")}
		if in.remove != "" {
			id := in.remove
			name := id
			if src := a.sourceByID(id); src != nil {
				name = src.Info().Name
			}
			dialog.Title = i18n.Tf("Remove %s?", name)
			dialog.Description = i18n.T("Plugin settings and downloaded music are kept. Manage local copies in Downloaded Music.")
			dialog.Footer = []core.Node{ui.Button{Text: i18n.T("Cancel"), OnPress: a.cancelPluginDialog}, ui.Button{Text: i18n.T("Remove"), TestID: "plugin-remove-confirm", OnPress: func() { a.removePlugin(id) }}}
		} else if pending := in.pending; pending != nil {
			dialog.Title = i18n.Tf("Install %s?", strings.TrimSpace(pending.Manifest.Name+" "+pending.Version))
			permissions := pending.Manifest.Permissions
			var lines []string
			if from := pending.Repo; from != "" {
				lines = append(lines, i18n.Tf("From %s", from))
			} else if pending.FeedURL != "" {
				lines = append(lines, i18n.Tf("From %s", pending.FeedURL))
			} else if pending.Path != "" {
				lines = append(lines, i18n.Tf("From %s", pending.Path))
			}
			switch {
			case slices.Contains(permissions.Network, "*"):
				lines = append(lines, i18n.T("Can reach any website"))
			case len(permissions.Network) > 0:
				lines = append(lines, i18n.Tf("Can reach %s", strings.Join(permissions.Network, ", ")))
			default:
				lines = append(lines, i18n.T("Asks for no network"))
			}
			if len(permissions.Process) > 0 {
				lines = append(lines, programsWarning(permissions.Process))
			}
			dialog.Description = strings.Join(lines, "\n")
			dialog.Footer = []core.Node{ui.Button{Text: i18n.T("Cancel"), OnPress: a.cancelPluginDialog}, ui.Button{Text: i18n.T("Install"), TestID: "plugin-confirm", Disabled: func() bool { return in.busy }, OnPress: a.confirmPluginInstall}}
		} else if in.github {
			dialog.Title = i18n.T("Install from GitHub")
			dialog.Description = i18n.T("A repository with a plugin, as owner/repo or its address on GitHub")
			dialog.Children = []core.Node{base.Input{TestID: "plugin-repo", Label: i18n.T("Install from GitHub"), Placeholder: i18n.T("owner/repo or URL"), Value: func() string { return in.repo }, OnChange: func(v string) { in.repo = v }, OnSubmit: func(string) { a.installRepo() }, Style: core.Style{Height: core.Pt(40), Padding: core.All(8), Background: p.hover, Color: p.text, CornerRadius: 8}},
				a.settingsAction(p, i18n.T("Choose ZIP File…"), "plugin-pick", in.busy, a.pickPluginZip)}
			dialog.Footer = []core.Node{ui.Button{Text: i18n.T("Cancel"), Disabled: func() bool { return in.busy }, OnPress: a.cancelPluginDialog}, ui.Button{Text: i18n.T("Install"), TestID: "plugin-fetch", Disabled: func() bool { return in.busy }, OnPress: a.installRepo}}
		} else {
			dialog.Footer = []core.Node{ui.Button{Text: i18n.T("Cancel"), Disabled: func() bool { return in.busy }, OnPress: a.cancelPluginDialog}}
		}
		if in.busy {
			dialog.Children = append(dialog.Children, text(i18n.T("Opening the plugin…"), 13, core.FontWeightNormal, p.muted))
		}
		if in.err != "" {
			dialog.Children = append(dialog.Children, text(in.err, 13, core.FontWeightNormal, p.accent))
		}
		sign := ui.Dialog{TestID: "plugin-signin", Title: i18n.T("Sign In"), Open: func() bool { return a.signingIn }, OnOpenChange: func(open bool, _ base.Reason) {
			if !open {
				a.cancelSignIn()
			}
		}, Description: a.signPrompt.Text, Footer: []core.Node{ui.Button{Text: i18n.T("Cancel"), OnPress: a.cancelSignIn}}}
		if a.signPrompt.QR != "" {
			if code, err := qr.Encode(a.signPrompt.QR, qr.M); err == nil {
				sign.Children = []core.Node{core.Image{Source: core.ImageSource{Data: code.PNG()}, Style: core.Style{Width: core.Pt(220), Height: core.Pt(220)}}}
			}
		}
		return core.Group{a.fitPhoneDialog(cx, p, dialog), a.fitPhoneDialog(cx, p, sign)}
	})
}
