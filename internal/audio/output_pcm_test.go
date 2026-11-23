//go:build woodwind_quiet

package audio

import (
	"slices"
	"testing"
)

func TestPCMBufferPreservesStereoAcrossWrap(t *testing.T) {
	b := newPCMBuffer(4)
	if !b.write([]float32{1, -1, 2, -2, 3, -3, 4, -4}) || b.write([]float32{9, -9}) {
		t.Fatal("PCM ring did not enforce its capacity")
	}
	part := make([]float32, 6)
	if n := b.render(part); n != 3 || !slices.Equal(part, []float32{1, -1, 2, -2, 3, -3}) {
		t.Fatalf("first read: %d frames, %v", n, part)
	}
	if !b.write([]float32{5, -5, 6, -6, 7, -7}) {
		t.Fatal("PCM ring did not reuse space freed by the callback")
	}
	wrapped := make([]float32, 8)
	if n := b.render(wrapped); n != 4 || !slices.Equal(wrapped, []float32{4, -4, 5, -5, 6, -6, 7, -7}) {
		t.Fatalf("wrapped read: %d frames, %v", n, wrapped)
	}

	if allocs := testing.AllocsPerRun(100, func() {
		b.write(wrapped)
		b.render(wrapped)
	}); allocs != 0 {
		t.Fatalf("PCM transfer allocated %v times", allocs)
	}
}

func TestPCMClockWaitsForPresentationAndSkipsSilence(t *testing.T) {
	b := newPCMBuffer(4)
	b.write(make([]float32, 4))
	if got := b.pendingFrames(0); got != 2 {
		t.Fatalf("before rendering: %d pending frames", got)
	}
	if n := b.render(make([]float32, 8)); n != 2 {
		t.Fatalf("underrun rendered %d frames", n)
	}
	if got := b.pendingFrames(4); got != 0 {
		t.Fatalf("after underrun: %d pending frames", got)
	}
	b.write(make([]float32, 4))
	if got := b.pendingFrames(4); got != 2 {
		t.Fatalf("new queue: %d pending frames", got)
	}
	b.render(make([]float32, 4))
	if got := b.pendingFrames(5); got != 1 {
		t.Fatalf("after one presentation: %d pending frames", got)
	}
	b.render(make([]float32, 4))
	if got := b.pendingFrames(5); got != 1 {
		t.Fatalf("second underrun moved clock backward: %d pending frames", got)
	}
	if got := b.pendingFrames(8); got != 0 {
		t.Fatalf("after all samples: %d pending frames", got)
	}
}
