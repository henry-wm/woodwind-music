package app

import (
	"github.com/nsgo-dev/nsgo/core"

	"github.com/HengWh/woodwind-music/internal/i18n"
	"github.com/HengWh/woodwind-music/internal/plugin"
)

// Keep only metadata for reviewed upstream versions. The commit supplies both
// the install archive and the license linked in Settings.
var supportedPlugins = []struct {
	id, name, repo, version, commit, license, verification string
}{
	{
		id:           "kugou-lite",
		name:         i18n.N("Kugou Concept"),
		repo:         "bitbyte404/mygo-music-kugou-lite",
		version:      "1.3.2",
		commit:       "b964ac03274163d3bf64bb530a452332be244635",
		license:      "MIT",
		verification: i18n.N("Android tested: search, lyrics and playback"),
	},
	{
		id:           "bilibili",
		name:         i18n.N("Bilibili"),
		repo:         "GentleM4dman/mygo-music-bilibili",
		version:      "0.4.2",
		commit:       "43d15cca5f2eb3616bb7718a752b8e240b6c5cc5",
		license:      "MIT",
		verification: i18n.N("macOS tested: search and standard audio playback"),
	},
}

func (a *app) installedPluginByID(id string) *plugin.Plugin {
	for _, p := range a.plugins {
		if p.Info().ID == id {
			return p
		}
	}
	return nil
}

func (a *app) pluginCatalog() settingsGroupDef {
	group := settingsGroupDef{title: i18n.T("Compatible Third-Party Plugins")}
	for _, entry := range supportedPlugins {
		entry := entry
		repoURL := "https://github.com/" + entry.repo
		sub := i18n.Tf("Source: %s · Verified version %s", entry.repo, entry.version)
		sub += "\n" + i18n.T(entry.verification)
		group.rows = append(group.rows, settingsRowDef{
			label: i18n.T(entry.name),
			sub:   sub,
			control: func(p palette) core.Node {
				label := i18n.T("Install")
				installed := a.installedPluginByID(entry.id) != nil
				if installed {
					label = i18n.T("Installed")
				}
				return core.View{Style: core.Style{Direction: core.Row, Gap: 8}, Children: []core.Node{
					a.settingsAction(p, "GitHub", "plugin-catalog-repo-"+entry.id, false, func() {
						a.cx.OpenURL(repoURL, nil)
					}),
					a.settingsAction(p, i18n.Tf("%s License", entry.license), "plugin-catalog-license-"+entry.id, false, func() {
						a.cx.OpenURL(repoURL+"/blob/"+entry.commit+"/LICENSE", nil)
					}),
					a.settingsAction(p, label, "plugin-catalog-install-"+entry.id, a.installer.busy || installed, func() {
						a.installer.github = false
						a.preparePlugin(func() (*plugin.Pending, error) {
							return fetchPinned(entry.repo, entry.commit, entry.id, entry.version, a.pluginDir())
						})
					}),
				}}
			},
		})
	}
	return group
}

func (a *app) subscribedCatalogGroups() []settingsGroupDef {
	var groups []settingsGroupDef
	for _, sub := range a.set.PluginFeeds {
		sub := sub
		if len(sub.Plugins) == 0 {
			continue
		}
		title := sub.Name
		if title == "" {
			title = sub.URL
		}
		group := settingsGroupDef{title: title}
		for _, entry := range sub.Plugins {
			entry := entry
			var lines []string
			meta := ""
			if entry.Repo != "" {
				meta = entry.Repo
			} else {
				meta = sub.Name
			}
			if entry.Version != "" {
				if meta != "" {
					meta += " · "
				}
				meta += entry.Version
			}
			if entry.DesktopOnly() {
				if meta != "" {
					meta += " · "
				}
				meta += i18n.T("Desktop only")
			} else if entry.IsPureJS() {
				if meta != "" {
					meta += " · "
				}
				meta += i18n.T("Pure JS")
			}
			if meta != "" {
				lines = append(lines, meta)
			}
			if entry.Description != "" {
				lines = append(lines, entry.Description)
			}
			if entry.Verification != "" {
				lines = append(lines, entry.Verification)
			}
			subText := ""
			for i, l := range lines {
				if i > 0 {
					subText += "\n"
				}
				subText += l
			}
			group.rows = append(group.rows, settingsRowDef{
				label: entry.Name,
				sub:   subText,
				control: func(p palette) core.Node {
					var buttons []core.Node
					if entry.Repo != "" {
						repoURL := "https://github.com/" + entry.Repo
						buttons = append(buttons, a.settingsAction(p, "GitHub", "plugin-feed-repo-"+entry.ID, false, func() {
							a.cx.OpenURL(repoURL, nil)
						}))
					} else if entry.Homepage != "" {
						hp := entry.Homepage
						buttons = append(buttons, a.settingsAction(p, i18n.T("Homepage"), "plugin-feed-home-"+entry.ID, false, func() {
							a.cx.OpenURL(hp, nil)
						}))
					}
					existing := a.installedPluginByID(entry.ID)
					androidUnsupported := entry.DesktopOnly() && a.cx.Env().OS() == core.OSAndroid
					switch {
					case existing != nil:
						ver := a.pluginAvailableUpdate(existing)
						if ver != "" {
							updating := a.installer.updating[entry.ID]
							lbl := i18n.Tf("Update to %s", ver)
							if updating {
								lbl = i18n.T("Updating…")
							}
							buttons = append(buttons, a.settingsAction(p, lbl, "plugin-feed-install-"+entry.ID, updating, func() {
								a.updatePlugin(existing)
							}))
						} else {
							buttons = append(buttons, a.settingsAction(p, i18n.T("Installed"), "plugin-feed-install-"+entry.ID, true, nil))
						}
					default:
						lbl := i18n.T("Install")
						if androidUnsupported {
							lbl = i18n.T("Desktop only")
						}
						buttons = append(buttons, a.settingsAction(p, lbl, "plugin-feed-install-"+entry.ID, a.installer.busy || androidUnsupported, func() {
							a.installer.github = false
							a.preparePlugin(func() (*plugin.Pending, error) {
								return fetchFeedEntry(sub.URL, entry, a.pluginDir())
							})
						}))
					}
					return core.View{Style: core.Style{Direction: core.Row, Gap: 8}, Children: buttons}
				},
			})
		}
		groups = append(groups, group)
	}
	return groups
}
