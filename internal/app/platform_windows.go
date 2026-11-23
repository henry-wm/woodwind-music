//go:build windows

package app

import (
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"

	"github.com/HengWh/woodwind-music/internal/winshell"
)

type windowsTray struct{}

func (*windowsTray) Remove()          { winshell.RemoveTray() }
func (*windowsTray) Registered() bool { return winshell.TrayRegistered() }

func newPlatformTray(cx *core.Scope, opts desktop.TrayOptions) trayIcon {
	winshell.ConfigureTray(opts.Tooltip,
		func() {
			cx.Post(func() {
				if opts.OnClick != nil {
					opts.OnClick()
				}
			})
		},
		func() {
			cx.Post(func() {
				if opts.Menu == nil {
					return
				}
				var items []winshell.MenuItem
				for _, entry := range opts.Menu() {
					selected := entry.OnSelect
					items = append(items, winshell.MenuItem{Label: entry.Label, Separator: entry.Separator,
						OnSelect: func() {
							if selected != nil {
								cx.Post(selected)
							}
						}})
				}
				winshell.ShowMenu(items)
			})
		})
	cx.Cleanup(winshell.RemoveTray)
	return &windowsTray{}
}
