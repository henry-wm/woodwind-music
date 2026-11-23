//go:build darwin || (windows && (amd64 || arm64)) || woodwind_quiet

package audio

import "sync/atomic"

// pcmBuffer is written by the mixer and read by the device callback. Each
// side publishes its own cursor only after copying its portion of the ring.
type pcmBuffer struct {
	samples       []float32
	read, written atomic.Uint64
	silent        atomic.Int64
	heard         int64 // updated only by the mixer
}

func newPCMBuffer(frames int) *pcmBuffer {
	return &pcmBuffer{samples: make([]float32, frames*channels)}
}

func (b *pcmBuffer) write(src []float32) bool {
	at := b.written.Load()
	size := uint64(len(b.samples))
	if size-(at-b.read.Load()) < uint64(len(src)) {
		return false
	}
	n := copy(b.samples[at%size:], src)
	copy(b.samples, src[n:])
	b.written.Store(at + uint64(len(src)))
	return true
}

// render is called in real time. The device fills any unwritten frames
// with silence, so this callback needs only copies and atomic operations.
func (b *pcmBuffer) render(dst []float32) int {
	at := b.read.Load()
	n := min(uint64(len(dst)), b.written.Load()-at)
	first := copy(dst[:n], b.samples[at%uint64(len(b.samples)):])
	copy(dst[first:n], b.samples[:int(n)-first])
	b.read.Store(at + n)
	b.silent.Add(int64(len(dst)-int(n)) / channels)
	return int(n) / channels
}

// pendingFrames follows the device's presentation clock, which counts silent
// underruns as well. Never move backward while a silent buffer reaches it.
func (b *pcmBuffer) pendingFrames(played int64) int64 {
	rendered := int64(b.read.Load()) / channels
	heard := min(played-b.silent.Load(), rendered)
	b.heard = max(b.heard, heard)
	return int64(b.written.Load())/channels - b.heard
}
