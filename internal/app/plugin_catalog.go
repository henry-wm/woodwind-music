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

func (a *app) pluginCatalog() settingsGroupDef {
	group := settingsGroupDef{title: i18n.T("Compatible Third-Party Plugins")}
	for _, entry := range supportedPlugins {
		repoURL := "https://github.com/" + entry.repo
		sub := i18n.Tf("Source: %s · Verified version %s", entry.repo, entry.version)
		sub += "\n" + i18n.T(entry.verification)
		group.rows = append(group.rows, settingsRowDef{
			label: i18n.T(entry.name),
			sub:   sub,
			control: func(p palette) core.Node {
				label := i18n.T("Install")
				installed := a.sourceByID(entry.id) != nil
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
						a.preparePlugin(func() (*plugin.Pending, error) {
							return plugin.FetchPinned(entry.repo, entry.commit, entry.id, entry.version, a.pluginDir())
						})
					}),
				}}
			},
		})
	}
	return group
}
