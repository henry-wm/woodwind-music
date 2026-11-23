//go:build darwin && !woodwind_quiet

package nowplaying

import (
	"time"

	"github.com/nsgo-dev/nsgo/core"
)

func Bind(cx *core.Scope, onCommand func(Command, time.Duration)) Controls {
	return bindNative(cx, onCommand)
}
