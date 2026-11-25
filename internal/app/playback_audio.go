package app

import (
	"time"

	"github.com/nsgo-dev/nsgo/audio"
	"github.com/nsgo-dev/nsgo/core"

	"github.com/HengWh/woodwind-music/internal/music"
	"github.com/HengWh/woodwind-music/internal/player"
)

// audioOutput uses NSGo's long-form player. Each track owns its player and
// fade timer, so replacing a track releases both before the next one opens.
type audioOutput struct {
	cx      *core.Scope
	track   *core.Scope
	current *audio.Player
	relay   *music.StreamRelay
	volume  float64
	speed   float64
	level   float64
	paused  bool
	stop    func() bool
}

func newAudioOutput(cx *core.Scope) *audioOutput {
	return &audioOutput{cx: cx, volume: 1, speed: 1, level: 1}
}

func (o *audioOutput) Open(source string, paused bool, events player.NativeEvents) {
	o.OpenWithHeaders(source, nil, paused, events)
}

func (o *audioOutput) OpenWithHeaders(source string, headers map[string]string, paused bool, events player.NativeEvents) {
	o.Close()
	if len(headers) > 0 {
		relay, err := music.NewStreamRelay(source, headers)
		if err != nil {
			events.Error(err)
			return
		}
		o.relay = relay
		source = relay.URL
	}
	o.cx.Run(func() { o.track = o.cx.Child() })
	o.current = audio.NewPlayer(o.track, audio.Source{URI: source}, audio.PlayerOptions{
		ProgressInterval: 200 * time.Millisecond,
		OnLoad:           events.Loaded,
		OnProgress:       events.Progress,
		OnBuffer:         events.Buffer,
		OnEnd:            events.Ended,
		OnError:          events.Error,
	})
	o.level, o.paused = 1, paused
	o.applyVolume()
	o.current.SetRate(float32(o.speed))
	if paused {
		o.current.Preload()
	} else {
		o.current.Play(nil)
	}
}

func (o *audioOutput) SetPaused(paused bool) {
	p := o.current
	if p == nil || o.paused == paused {
		return
	}
	o.paused = paused
	o.cancelFade()
	if paused {
		if p.State() == audio.PlayerPlaying {
			o.fade(0, 180*time.Millisecond, p.Pause)
		} else {
			p.Pause()
		}
		return
	}
	if p.State() != audio.PlayerPlaying {
		o.level = 0
		o.applyVolume()
	}
	p.Play(nil)
	o.fade(1, 260*time.Millisecond, nil)
}

func (o *audioOutput) fade(to float64, duration time.Duration, done func()) {
	track := o.track
	start, from := track.Now(), o.level
	var tick func()
	tick = func() {
		fraction := min(1, float64(track.Now().Sub(start))/float64(duration))
		o.level = from + (to-from)*fraction
		o.applyVolume()
		if fraction == 1 {
			o.stop = nil
			if done != nil {
				done()
			}
			return
		}
		// Transport buttons rebuild when playRev changes. Keep the fade timer
		// on the track scope so disposing that button does not cancel pausing
		// or leave resumed audio muted.
		track.Run(func() { o.stop = track.AfterFunc(16*time.Millisecond, tick) })
	}
	tick()
}

func (o *audioOutput) cancelFade() {
	if o.stop != nil {
		o.stop()
		o.stop = nil
	}
}

func (o *audioOutput) SetVolume(volume float64) {
	o.volume = volume
	o.applyVolume()
}

func (o *audioOutput) applyVolume() {
	if o.current == nil {
		return
	}
	volume := float32(o.volume * o.level)
	// NSGo's SetVolume treats zero as the default; use the mute control.
	if volume == 0 {
		o.current.SetMuted(true)
		return
	}
	o.current.SetVolume(volume)
	o.current.SetMuted(false)
}

func (o *audioOutput) SetSpeed(speed float64) {
	o.speed = speed
	if o.current != nil {
		o.current.SetRate(float32(speed))
	}
}

func (o *audioOutput) Seek(position time.Duration) {
	if o.current != nil {
		o.current.Seek(position)
	}
}

func (o *audioOutput) Close() {
	o.cancelFade()
	if o.track != nil {
		o.track.Dispose()
		o.track, o.current = nil, nil
	}
	if o.relay != nil {
		o.relay.Close()
		o.relay = nil
	}
}
