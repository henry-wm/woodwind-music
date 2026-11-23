//go:build !windows

package app

import (
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"
)

func newPlatformTray(cx *core.Scope, opts desktop.TrayOptions) trayIcon {
	tr := desktop.NewTray(cx, opts)
	if tr == nil {
		return nil
	}
	return tr
}
