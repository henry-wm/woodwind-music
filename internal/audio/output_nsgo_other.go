//go:build (!darwin && !(windows && (amd64 || arm64))) || woodwind_quiet

package audio

import "github.com/nsgo-dev/nsgo/core"

// BindDesktopOutput has no effect without the desktop PCM backend.
func BindDesktopOutput(*core.Scope) {}
