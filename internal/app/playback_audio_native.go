//go:build !woodwind_quiet

package app

import (
	"github.com/nsgo-dev/nsgo/core"

	"github.com/HengWh/woodwind-music/internal/player"
)

func newDesktopOutput(cx *core.Scope) player.NativeOutput { return newAudioOutput(cx) }
