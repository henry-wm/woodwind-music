//go:build (!darwin && !windows) || woodwind_quiet

package nowplaying

import (
	"time"

	"github.com/nsgo-dev/nsgo/core"
)

func Bind(*core.Scope, func(Command, time.Duration)) Controls { return Controls{} }
