//go:build (darwin || (windows && (amd64 || arm64))) && !woodwind_quiet

package audio

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	nsgoaudio "github.com/nsgo-dev/nsgo/audio"
	"github.com/nsgo-dev/nsgo/core"
)

type desktopOutputOwner struct {
	scope *core.Scope
	ctx   context.Context
}

var desktopOutput atomic.Pointer[desktopOutputOwner]

// BindDesktopOutput keeps PCM streams in the app scope so hiding or closing
// the main window leaves the audio device running.
func BindDesktopOutput(cx *core.Scope) {
	owner := &desktopOutputOwner{scope: cx, ctx: cx.Context()}
	desktopOutput.Store(owner)
	cx.Cleanup(func() { desktopOutput.CompareAndSwap(owner, nil) })
}

type pcmDevice struct {
	pcm       *pcmBuffer
	output    *nsgoaudio.Output
	scope     *core.Scope
	ctx       context.Context
	interrupt chan error
	started   bool
}

func openDevice(ahead time.Duration) (device, int, error) {
	owner := desktopOutput.Load()
	if owner == nil {
		return nil, 0, errUnsupported
	}
	const chunk = sampleRate / 100
	hardware := LatencyInteractive.ahead()
	capacity := max(2*chunk, int((ahead-hardware)*sampleRate/time.Second))
	// Fill whole mixer chunks so priming can fill the buffer exactly.
	capacity = (capacity + chunk - 1) / chunk * chunk
	type result struct {
		device *pcmDevice
		err    error
	}
	opened := make(chan result, 1)
	owner.scope.Post(func() {
		scope := owner.scope.Child()
		// Open under the child's current owner so disposing this device
		// releases its native stream and its callback before the next one.
		scope.Post(func() {
			d := &pcmDevice{
				pcm: newPCMBuffer(capacity), scope: scope, ctx: scope.Context(),
				interrupt: make(chan error, 1),
			}
			var err error
			d.output, err = nsgoaudio.NewOutput(scope, nsgoaudio.OutputConfig{
				SampleRate: sampleRate, Channels: channels, Latency: hardware, Render: d.pcm.render,
				OnInterrupt: func(err error) {
					select {
					case d.interrupt <- err:
					default:
					}
				},
			})
			if err != nil {
				scope.Dispose()
				opened <- result{err: err}
				return
			}
			opened <- result{device: d}
		})
	})
	select {
	case <-owner.ctx.Done():
		return nil, 0, owner.ctx.Err()
	case r := <-opened:
		if errors.Is(r.err, nsgoaudio.ErrUnsupported) {
			return nil, 0, errUnsupported
		}
		if r.err != nil {
			return nil, 0, r.err
		}
		return r.device, chunk, nil
	}
}

var errPCMStalled = errors.New("audio: the output is no longer pulling PCM")

func (d *pcmDevice) write(samples []float32) error {
	deadline := time.Now().Add(time.Second)
	for {
		select {
		case <-d.ctx.Done():
			return d.ctx.Err()
		case err := <-d.interrupt:
			if err != nil {
				return err
			}
			return errPCMStalled
		default:
		}
		if d.pcm.write(samples) {
			break
		}
		if time.Now().After(deadline) {
			return errPCMStalled
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !d.started && d.pcm.written.Load() == uint64(len(d.pcm.samples)) {
		if err := d.output.Start(); err != nil {
			// The mixer has already advanced through the primed PCM.
			// Pace the discarded chunks; the caller paces the final one.
			primed := int64((len(d.pcm.samples) - len(samples)) / channels)
			time.Sleep(frames(primed, sampleRate))
			return err
		}
		d.started = true
	}
	return nil
}

func (d *pcmDevice) pendingFrames() int64 {
	var played int64
	if d.started {
		played, _ = d.output.Position()
	}
	return d.pcm.pendingFrames(played)
}

func (d *pcmDevice) close() { d.scope.Post(d.scope.Dispose) }
