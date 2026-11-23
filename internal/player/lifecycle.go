package player

// Close releases playback resources before returning. Call it on the main
// thread when the app is disposed, after saving the playback session.
func (p *Player) Close() {
	if p.closed {
		return
	}
	p.closed = true
	p.cancelSleepTimer()
	p.gen++
	p.endRadio()
	p.dropAhead()
	p.stop()
	p.queue, p.order, p.track = nil, nil, nil
	p.at = -1
	p.playing, p.loading, p.awaiting = false, false, false
	p.pending = 0
}
