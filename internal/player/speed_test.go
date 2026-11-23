package player

import (
	"math"
	"testing"
	"time"

	"github.com/HengWh/woodwind-music/internal/music"
)

func TestPlaybackSpeedRestoresPausedSession(t *testing.T) {
	p, output, ch := newNativeTest()
	t.Cleanup(p.Close)
	output.autoLoad = true
	p.SetSpeed(1.5)
	p.PlayQueue([]music.Track{{ID: "one"}, {ID: "two"}}, 0)
	nativeUntil(t, ch, p.Ready)
	if output.speed != 1.5 {
		t.Fatal("loaded output lost its playback rate")
	}
	p.Next()
	nativeUntil(t, ch, p.Ready)
	if output.speed != 1.5 {
		t.Fatal("track change reset speed")
	}
	p.TogglePlay()
	p.Seek(time.Second)
	p.SetSpeed(0.75)
	session, ok := p.Session()
	if !ok || session.Speed != 0.75 || session.Position != 1000 {
		t.Fatalf("session: %+v", session)
	}
	p.Restore(session)
	nativeUntil(t, ch, p.Ready)
	if p.Speed() != 0.75 || output.speed != 0.75 || !output.paused || p.Playing() || p.Position() != time.Second {
		t.Fatal("restore lost speed, pause state or source position")
	}
}

func TestPlaybackSpeedNativeAndRepeat(t *testing.T) {
	p, out, ch := newNativeTest()
	t.Cleanup(p.Close)
	p.SetSpeed(1.75)
	p.PlayQueue([]music.Track{{ID: "one"}}, 0)
	nativeUntil(t, ch, func() bool { return out.source != "" })
	if out.speed != 1.75 {
		t.Fatal("native open lost speed")
	}
	out.events.Loaded(time.Minute)
	nativeUntil(t, ch, p.Ready)
	p.TogglePlay()
	p.Seek(12 * time.Second)
	p.SetSpeed(0.5)
	if out.speed != 0.5 || !out.paused || p.Position() != 12*time.Second {
		t.Fatal("native rate change altered pause or position")
	}
	p.TogglePlay()
	p.SetPlaybackMode(ModeSingleLoop)
	before := p.native
	out.events.Ended()
	nativeUntil(t, ch, func() bool { return p.native != before && out.source != "" })
	if out.speed != 0.5 || out.paused {
		t.Fatal("single repeat did not retain speed")
	}
}

func TestPlaybackSpeedValidationAndRemote(t *testing.T) {
	p := New(func(fn func()) { fn() })
	t.Cleanup(p.Close)
	if p.Speed() != 1 {
		t.Fatal("default speed")
	}
	out := &nativeOutput{paused: true}
	p.Native = out
	p.Seek(500 * time.Millisecond)
	for _, speed := range Speeds() {
		p.SetSpeed(speed)
		if out.speed != speed || !out.paused || p.Position() != 500*time.Millisecond {
			t.Fatalf("speed %v altered transport", speed)
		}
	}
	for _, speed := range []float64{0, -1, 0.49, 2.01, math.NaN(), math.Inf(1)} {
		p.SetSpeed(speed)
		if p.Speed() != 2 || out.speed != 2 {
			t.Fatalf("accepted invalid speed %v", speed)
		}
	}
	p.Cue([]music.Track{{ID: "remote", Source: "remote"}})
	p.Source = func(string) music.Source { return &remoteFake{} }
	if p.CanSetSpeed() || p.Speed() != 1 {
		t.Fatal("remote advertised local tempo control")
	}
	p.SetSpeed(1.5)
	if p.speed != 2 {
		t.Fatal("remote selection overwrote the local speed preference")
	}
}

func TestPlaybackSpeedLegacySession(t *testing.T) {
	for _, speed := range []float64{0, -1, 3, math.NaN(), math.Inf(1)} {
		p, out, ch := newNativeTest()
		p.Restore(Session{Queue: []music.Track{{ID: "one"}}, Order: []int{0}, Speed: speed})
		nativeUntil(t, ch, func() bool { return out.source != "" })
		if p.Speed() != 1 || out.speed != 1 || !out.paused {
			t.Fatalf("invalid session speed %v was not restored at normal speed", speed)
		}
		p.Close()
	}
}
