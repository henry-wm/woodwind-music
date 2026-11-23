//go:build woodwind_quiet

package app

import (
	"time"

	"github.com/nsgo-dev/nsgo/core"

	"github.com/HengWh/woodwind-music/internal/player"
)

func newDesktopOutput(*core.Scope) player.NativeOutput { return quietAudioOutput{} }

// The quiet suite exercises the shared player without opening a native device.
// NSGo adapter tests use its headless driver to assert the platform commands.
type quietAudioOutput struct{}

func (quietAudioOutput) Open(_ string, _ bool, events player.NativeEvents) { events.Loaded(0) }
func (quietAudioOutput) SetPaused(bool)                                    {}
func (quietAudioOutput) SetVolume(float64)                                 {}
func (quietAudioOutput) SetSpeed(float64)                                  {}
func (quietAudioOutput) Seek(time.Duration)                                {}
func (quietAudioOutput) Close()                                            {}
