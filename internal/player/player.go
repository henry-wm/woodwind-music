package player

import (
	"errors"
	"math"
	"math/rand/v2"
	"time"

	"github.com/HengWh/woodwind-music/internal/i18n"
	"github.com/HengWh/woodwind-music/internal/lyrics"
	"github.com/HengWh/woodwind-music/internal/music"
	"github.com/HengWh/woodwind-music/internal/offline"
)

type RepeatMode int

const (
	RepeatOff RepeatMode = iota
	RepeatAll
	RepeatOne
)

// PlaybackMode combines shuffle and repeat into one mutually exclusive choice.
type PlaybackMode int

const (
	ModeListLoop PlaybackMode = iota
	ModeSingleLoop
	ModeShuffle
)

// NowPlaying is what the system shows of what plays: its title, its artist,
// its album, its cover, its duration and where it is, and whether it plays.
// The app sets it, on a platform that shows one.
type NowPlaying struct {
	Title, Artist, Album string
	Artwork              []byte
	Duration             time.Duration
	Position             time.Duration
	Playing              bool
	Speed                float64
}

// SystemMedia receives the player's metadata, transport state and clear events.
type SystemMedia struct {
	Set      func(NowPlaying)
	Position func(time.Duration, bool)
	Clear    func()
}

// Player plays a queue of tracks, from whichever source each is of. Its
// methods run on the main thread; loading happens on goroutines, whose results
// post brings back there.
type Player struct {
	post func(func())
	// Quiet is post without a redraw, for what mostly changes nothing, if set.
	Quiet func(func())
	// System receives updates from this player only.
	System SystemMedia
	// Source finds the source of an ID, or nil if it's gone.
	Source func(id string) music.Source
	// Failed, if set, hears that a source said the listener has something to
	// do first: ErrSignIn or ErrSetup.
	Failed func(source string, err error)
	// Quality returns the quality to ask sources for, as the settings say.
	Quality func() string
	// Normalize reports whether to even out the loudness of tracks, as
	// the settings say.
	Normalize func() bool
	// Library supplies complete local copies and shared downloads on every platform.
	Library *offline.Store
	// CompleteLocal obtains missing local-track companions off the UI thread.
	CompleteLocal func(music.Track) (music.Track, *music.Lyrics)
	completeGen   int
	// Changed, if set, hears that what plays, where it is or whether it
	// plays changed.
	Changed func()
	// Native plays local and streaming audio through NSGo.
	Native NativeOutput
	// Prefetch reserves the next desktop transfer while the current song plays.
	Prefetch bool

	// Sleep callbacks run on the main thread. Expired also fires when
	// already paused, so the app can cancel automatic focus resumption.
	SleepTimerChanged func()
	SleepTimerExpired func()
	sleepTimer        *time.Timer
	sleepUntil        time.Time
	sleepGen          int

	queue   []music.Track
	order   []int // the queue's indices in play order: shuffled or not
	at      int   // where in order the current track is, -1 for none
	shuffle bool
	repeat  RepeatMode
	volume  float64
	speed   float64

	// A radio goes on with the tracks its feed gives, as the queue nears
	// its end, for as long as nothing else is played.
	radio    string
	feed     func(got func([]music.Track))
	radioGen int  // counts radios, to drop what an old one gives
	feeding  bool // the feed is asked
	awaiting bool // at the queue's end, for what the feed gives

	track   *music.Track
	info    *music.Stream // what the current track streams
	gain    float64       // the current track's normalizing gain
	loading bool
	playing bool // what the listener wants: false while paused
	err     string
	gen     int // counts loads, to drop the results of stale ones

	lyrics       []lyrics.Line
	lyricsLoaded bool

	// pending holds a seek asked for while the track loads.
	pending time.Duration

	// remote is set while the current track is a PlaybackRemote source's,
	// which plays it elsewhere: see remote.go.
	remote *remote
	native *nativePlayback
	// after runs fn after d, on another goroutine; tests replace it.
	after func(d time.Duration, fn func())
	now   func() time.Time
	cmds  commands
	// The volume last asked of a remote source, and whether that's on its
	// way: guarded by cmds.mu.
	volumeAsked  int
	volumeQueued bool

	recorded string // the track last told its source it played

	ahead *ahead // the next track's audio, asked for before its turn

	closed bool
}

func New(post func(func())) *Player {
	p := &Player{post: post, at: -1, volume: 0.8, gain: 1}
	p.after = func(d time.Duration, fn func()) { time.AfterFunc(d, fn) }
	p.now = time.Now
	return p
}

// sync shows the system, and Changed, where playback is.
func (p *Player) sync() {
	if p.Changed != nil {
		defer p.Changed()
	}
	if p.track == nil {
		if p.System.Clear != nil {
			p.System.Clear()
		}
		return
	}
	if p.System.Position != nil {
		p.System.Position(p.Position(), p.playing)
	}
}

// announce shows the system the track, and its cover once loaded.
func (p *Player) announce(t *music.Track, artwork []byte) {
	if p.System.Set != nil {
		p.System.Set(NowPlaying{
			Title:    t.Name,
			Artist:   t.ArtistNames(),
			Album:    t.Album.Name,
			Artwork:  artwork,
			Duration: t.Length(),
			Position: p.Position(),
			Playing:  p.playing,
			Speed:    p.Speed(),
		})
	}
}

func (p *Player) ended() {
	if !p.playing {
		return
	}
	if p.repeat == RepeatOne {
		// Reopen completed platform players, including Android MediaPlayer.
		p.load(p.at)
		return
	}

	if p.at+1 >= len(p.order) && p.feed != nil {
		p.awaitFeed()
		return
	}
	if p.at+1 >= len(p.order) && p.repeat == RepeatOff {
		p.playing = false
		p.sync()
		return
	}
	p.Next()
}

// PlayQueue replaces the queue with tracks and plays the one at start, or the
// first after it that can play.
func (p *Player) PlayQueue(tracks []music.Track, start int) {
	p.playQueue(tracks, start, forward)
}

// PlayTrack replaces the queue with tracks and plays the one at i, the
// listener's pick: one that can't play stays, saying why, rather than another
// playing in its place.
func (p *Player) PlayTrack(tracks []music.Track, i int) {
	p.playQueue(tracks, i, stay)
}

// PlayShuffled replaces the queue with tracks in a random order, any of them
// first, and plays it with shuffle on.
func (p *Player) PlayShuffled(tracks []music.Track) {
	if len(tracks) == 0 {
		return
	}
	p.shuffle = true
	p.repeat = RepeatAll
	p.playQueue(tracks, -1, forward)
}

// playQueue plays tracks from start; a start below 0, when shuffled, lets
// any track come first.
func (p *Player) playQueue(tracks []music.Track, start, dir int) {
	if p.closed || len(tracks) == 0 {
		return
	}
	p.endRadio()
	start = min(start, len(tracks)-1)
	p.queue = append([]music.Track(nil), tracks...)
	p.order = make([]int, len(p.queue))
	for i := range p.order {
		p.order[i] = i
	}
	if p.shuffle {
		p.shuffleOrder(start)
		start = 0
	}
	p.loadSkipping(max(start, 0), 0, dir, true, 0)
}

// Clear stops playback and forgets its queue, for a source being removed.
func (p *Player) Clear() {
	if r := p.remote; r != nil && r.started && p.playing {
		p.command(r.src, r.src.RemotePause)
	}
	p.dropAhead()
	p.pending = 0
	p.lyrics, p.lyricsLoaded = nil, false
	p.gen++
	p.endRadio()
	p.stop()
	p.queue, p.order, p.track = nil, nil, nil
	p.at = -1
	p.playing, p.loading = false, false
	p.sync()
}

// PlayRadio plays tracks in their order, then goes on with those feed
// gives as the queue nears its end, until something else is played. The
// radio's name says which it is. feed calls got on the main thread.
func (p *Player) PlayRadio(name string, tracks []music.Track, feed func(got func([]music.Track))) {
	if len(tracks) == 0 {
		return
	}
	shuffle := p.shuffle
	p.shuffle = false
	p.PlayQueue(tracks, 0)
	p.shuffle = shuffle
	p.AttachRadio(name, feed)
}

// AttachRadio makes the queue the radio name's, which feed goes on with,
// as after restoring a session that played it.
func (p *Player) AttachRadio(name string, feed func(got func([]music.Track))) {
	p.endRadio()
	p.radio, p.feed = name, feed
	p.refill()
}

// EndRadio stops the radio playing: the song playing plays on, but the
// songs the radio queued after it go, and it adds no more.
func (p *Player) EndRadio() {
	if p.radio == "" && p.feed == nil {
		return
	}
	awaiting := p.awaiting
	p.endRadio()
	if p.at >= 0 && p.at+1 < len(p.order) {
		kept := make([]music.Track, p.at+1)
		for i, k := range p.order[:p.at+1] {
			kept[i] = p.queue[k]
		}
		p.queue, p.order = kept, make([]int, len(kept))
		for i := range p.order {
			p.order[i] = i
		}
	}
	if awaiting {
		// At its end, waiting for more: nothing plays on.
		p.playing = false
		p.sync()
	}
}

func (p *Player) endRadio() {
	p.radioGen++
	p.radio, p.feed, p.feeding, p.awaiting = "", nil, false, false
}

// Radio returns the name of the radio playing, or "".
func (p *Player) Radio() string { return p.radio }

// refill asks the radio for more once the queue is near its end.
func (p *Player) refill() {
	if p.feed == nil || p.feeding || p.at < len(p.order)-2 {
		return
	}
	p.feeding = true
	gen := p.radioGen
	p.feed(func(more []music.Track) {
		if gen != p.radioGen {
			return
		}
		p.feeding = false
		p.appendTracks(more)
		switch {
		case !p.awaiting:
		case p.at+1 < len(p.order):
			p.awaiting = false
			p.load(p.at + 1)
		default:
			// The radio has nothing more: it stops at its end.
			p.awaiting, p.playing = false, false
			p.err = i18n.T("The station has nothing more right now")
			p.sync()
		}
	})
}

// awaitFeed waits at the end of a radio's queue for what its feed gives.
func (p *Player) awaitFeed() {
	p.awaiting = true
	p.refill()
}

// appendTracks adds tracks to the end of the queue, but those in it.
func (p *Player) appendTracks(tracks []music.Track) {
	type key struct{ source, id string }
	in := make(map[key]bool, len(p.queue))
	for _, t := range p.queue {
		in[key{t.Source, t.ID}] = true
	}
	for _, t := range tracks {
		if k := (key{t.Source, t.ID}); !in[k] {
			in[k] = true
			p.order = append(p.order, len(p.queue))
			p.queue = append(p.queue, t)
		}
	}
}

// shuffleOrder puts the order in a random one with first at its head, or
// with any at its head when first is below 0.
func (p *Player) shuffleOrder(first int) {
	rand.Shuffle(len(p.order), func(i, j int) { p.order[i], p.order[j] = p.order[j], p.order[i] })
	for i, v := range p.order {
		if v == first {
			p.order[0], p.order[i] = p.order[i], p.order[0]
		}
	}
}

func (p *Player) ToggleShuffle() {
	p.shuffle = !p.shuffle
	if len(p.order) == 0 {
		return
	}
	cur := p.order[max(p.at, 0)]
	if p.shuffle {
		p.shuffleOrder(cur)
		p.at = 0
	} else {
		for i := range p.order {
			p.order[i] = i
		}
		p.at = cur
	}
}

func (p *Player) CycleRepeat() { p.repeat = (p.repeat + 1) % 3 }

// PlaybackMode maps legacy independent settings to one playback mode.
func (p *Player) PlaybackMode() PlaybackMode {
	if p.repeat == RepeatOne {
		return ModeSingleLoop
	}
	if p.shuffle {
		return ModeShuffle
	}
	return ModeListLoop
}

// SetPlaybackMode preserves the current track while updating the queue's order.
func (p *Player) SetPlaybackMode(mode PlaybackMode) {
	if mode < ModeListLoop || mode > ModeShuffle {
		return
	}
	p.repeat = RepeatAll
	if mode == ModeSingleLoop {
		p.repeat = RepeatOne
	}
	if p.shuffle != (mode == ModeShuffle) {
		p.ToggleShuffle()
	}
}

// CyclePlaybackMode cycles list loop, single-track loop, then shuffle.
func (p *Player) CyclePlaybackMode() {
	p.SetPlaybackMode((p.PlaybackMode() + 1) % 3)
}

// Current returns the track playing or loading, or nil.
func (p *Player) Current() *music.Track { return p.track }

// Queue returns the tracks queued, in the order they were given.
func (p *Player) Queue() []music.Track { return p.queue }

// Playing reports what the listener wants: false while paused.
func (p *Player) Playing() bool { return p.playing }

// Loading reports whether audio or the next radio track is pending.
func (p *Player) Loading() bool { return p.loading || p.awaiting }

// Ready reports whether the current track has loaded, to play.
func (p *Player) Ready() bool {
	return p.native != nil && p.native.ready || p.remote != nil && p.remote.started
}

// Err returns why the current track did not play, or "".
func (p *Player) Err() string { return p.err }

// Stream returns what the current track streams, once loaded, or nil.
func (p *Player) Stream() *music.Stream { return p.info }

func (p *Player) Shuffle() bool      { return p.shuffle }
func (p *Player) Repeat() RepeatMode { return p.repeat }

// Lyrics returns the current track's lyrics, and whether they have loaded.
func (p *Player) Lyrics() ([]lyrics.Line, bool) { return p.lyrics, p.lyricsLoaded }

// PlayOrder returns the queue's indices in play order, and where the
// current track is among them, -1 for none.
func (p *Player) PlayOrder() ([]int, int) { return p.order, p.at }

func (p *Player) Next() {
	if len(p.order) == 0 {
		return
	}
	at := p.at + 1
	if at >= len(p.order) {
		if p.feed != nil {
			p.stopForFeed()
			p.awaitFeed()
			return
		}
		at = 0
	}
	p.load(at)
}

// Prev plays the track before this one, however far this one has played:
// once a few seconds in, going back to its start only kept the listener
// pressing twice.
func (p *Player) Prev() {
	if len(p.order) == 0 {
		return
	}
	at := p.at - 1
	if at < 0 {
		at = len(p.order) - 1
	}
	// One that can't play is passed going back, as going forward.
	p.loadSkipping(at, 0, back, true, 0)
}

// Jump plays the queue's track i, the listener's pick: one that can't play
// stays, saying why.
func (p *Player) Jump(i int) {
	for k, v := range p.order {
		if v == i {
			p.loadSkipping(k, 0, stay, true, 0)
			return
		}
	}
}

// TogglePlay pauses or resumes the current native or remote player.
func (p *Player) TogglePlay() {
	switch {
	case p.remote != nil && p.remote.started:
		p.remoteToggle()
	case p.native != nil:
		p.nativeToggle()
	case p.loading:
		p.playing = !p.playing
		p.sync()
	case len(p.order) > 0:
		p.loadSkipping(max(p.at, 0), 0, stay, true, p.pending)
	}
}

// Seek moves the playhead, retaining a request made before the track loads.
func (p *Player) Seek(t time.Duration) {
	switch {
	case p.remote != nil && p.remote.started:
		p.remoteSeek(t)
	case p.native != nil:
		p.nativeSeek(t)
	default:
		p.pending = t
	}
}

// Volume returns the volume the listener hears, from 0 to 1.
func (p *Player) Volume() float64 { return p.volume }

func (p *Player) SetVolume(v float64) {
	p.volume = v
	p.applyGain()
	if p.remote != nil {
		p.remoteVolume(v)
	}
}

// applyGain sets the volume of what plays: the listener's, which the ear
// hears in powers, and the track's gain, if normalizing.
func (p *Player) applyGain() {
	g := p.volume * p.volume
	if p.Normalize != nil && p.Normalize() {
		g *= p.gain
	}
	if p.Native != nil {
		p.Native.SetVolume(min(1, max(0, g)))
	}
}

// normPreamp lifts normalized tracks, which their gains bring to the quiet
// loudness of ReplayGain, nearer to how loud music plays elsewhere.
const normPreamp = 4

// normGain is the gain that brings a track as loud as others, as its source
// measured it, but never so loud that its peaks would clip.
func normGain(st *music.Stream) float64 {
	if st == nil || st.Gain == 0 && st.Peak == 0 {
		return 1
	}
	g := math.Pow(10, (st.Gain+normPreamp)/20)
	if st.Peak > 0 {
		g = min(g, 1/st.Peak)
	}
	return g
}

func (p *Player) Position() time.Duration {
	switch {
	case p.native != nil:
		return p.native.position
	case p.remote != nil && p.remote.started:
		return p.remotePosition()
	default:
		return p.pending
	}
}

func (p *Player) Duration() time.Duration {
	switch {
	case p.track == nil:
		return 0
	case p.native != nil && p.native.duration > 0:
		return p.native.duration
	case p.remote != nil && p.remote.duration > 0:
		return p.remote.duration
	default:
		return p.track.Length()
	}
}

func (p *Player) stop() {
	if p.Native != nil {
		p.Native.Close()
	}
	if p.native != nil {
		p.native.release()
	}
	p.native, p.info, p.remote = nil, nil, nil
}

// Which way a load goes past a track that can't play: on to the next, back to
// the one before, or nowhere, staying on it to say why.
const (
	forward = 1
	back    = -1
	stay    = 0
)

// load plays the track at order[at], skipping those that cannot play.
func (p *Player) load(at int) {
	p.loadSkipping(at, 0, forward, true, 0)
}

// stopForFeed stops what plays, at the end of a radio's queue, while its feed
// gives more.
func (p *Player) stopForFeed() {
	if was := p.remote; was != nil && was.started && p.playing {
		p.command(was.src, was.src.RemotePause)
	}
	p.stop()
}

// sourceOf returns the source of track t, or nil if it's gone.
func (p *Player) sourceOf(t *music.Track) music.Source {
	if p.Source == nil {
		return nil
	}
	return p.Source(t.Source)
}

// record tells the source of the current track, if it wants to know, that it
// plays, once for each time it's played.
func (p *Player) record() {
	if p.track == nil || !p.playing {
		return
	}
	p.completeLocal()
	if p.Library != nil {
		p.Library.RememberPlayed(*p.track)
	}
	if p.track.LocalOnly {
		return
	}
	src, ok := p.sourceOf(p.track).(music.Recorder)
	key := p.track.Source + "\x00" + p.track.ID
	if !ok || key == p.recorded {
		return
	}
	p.recorded = key
	t := *p.track
	go src.Played(t)
}

// loadSkipping loads the track at order[at], to play or not, from from,
// skipping those that cannot play the way dir goes: skipped of them so far.
func (p *Player) loadSkipping(at, skipped, dir int, play bool, from time.Duration) {
	if p.closed {
		return
	}
	t := p.queue[p.order[at]]
	src := p.sourceOf(&t)
	if p.Library != nil && (t.StoredID != "" || t.Source != "local" && t.Source != "") {
		if !t.LocalOnly && src != nil {
			_ = p.Library.Remember(t, src.Info().Name)
		}
		if r, ok := p.Library.Get(t); ok && len(r.Variants) > 0 {
			local := p.Library.Track(r)
			local.LocalOnly = t.LocalOnly
			t = local
			p.queue[p.order[at]] = t
		}
	}
	if was := p.remote; was != nil && was.started && p.playing && (src == nil || src.Info().ID != was.src.Info().ID) {
		// Whatever plays next isn't its to play.
		p.command(was.src, was.src.RemotePause)
	}
	p.stop()
	p.gen++
	gen := p.gen
	p.at = at
	p.track = &t
	p.loading, p.playing, p.err = true, play, ""
	p.awaiting = false
	p.lyrics, p.lyricsLoaded = nil, false
	p.pending = from
	p.recorded = ""
	p.announce(&t, nil)
	p.sync()
	p.refill()

	p.loadCover(gen, &t)
	p.loadLyrics(gen, src, t)
	if !t.LocalOnly && src != nil && src.Info().Playback == music.PlaybackRemote && (p.Library == nil || !p.Library.Has(t)) {
		p.loadRemote(gen, src, at, skipped, dir, play, from)
		return
	}
	quality := music.Qualities[len(music.Qualities)/2]
	if p.Quality != nil {
		quality = p.Quality()
	}
	if p.Native == nil {
		p.loadFailed(t.Source, music.ErrUnsupported, at, skipped, dir)
		return
	}
	p.loadNative(gen, src, t, quality, at, skipped, dir)
}

// loadCover shows the system the cover of t, once it has it.
func (p *Player) loadCover(gen int, t *music.Track) {
	url := t.Album.ImageURL
	if url == "" && p.Library == nil {
		return
	}
	go func() {
		if p.Library != nil && (t.StoredID != "" || t.Source != "local" && t.Source != "") {
			if !t.LocalOnly {
				<-p.Library.Fill(*t, p.sourceOf(t), false)
			}
			url = p.Library.Cover(*t)
		}
		if url == "" {
			return
		}
		// The system shows it small, as Now Playing does.
		cover, err := music.Fetch(music.SizedImage(url, 600))
		if err == nil {
			p.post(func() {
				if gen == p.gen {
					p.announce(t, cover)
				}
			})
		}
	}()
}

// loadLyrics asks the source of t for its lyrics, if it has any.
func (p *Player) loadLyrics(gen int, src music.Source, t music.Track) {
	go func() {
		var lines []lyrics.Line
		if p.Library != nil && (t.StoredID != "" || t.Source != "local" && t.Source != "") {
			if !t.LocalOnly {
				<-p.Library.Fill(t, src, false)
			}
			if l, ok := p.Library.Lyrics(t); ok {
				lines = lyrics.FromSource(l)
			}
		} else if src != nil && src.Can("lyrics") {
			if l, err := src.Lyrics(t.ID); err == nil && l != nil {
				lines = lyrics.FromSource(l)
			}
		}
		p.post(func() {
			if gen == p.gen {
				if len(p.lyrics) == 0 {
					p.lyrics = lines
				}
				p.lyricsLoaded = true
				p.sync()
			}
		})
	}()
}

// loadFailed says why the track at order[at] can't play, and skips it the way
// dir goes, unless it stays, nothing in the queue can play, or the listener has
// to do something first, which would stop the next track too.
func (p *Player) loadFailed(source string, err error, at, skipped, dir int) {
	p.err = errorText(err)
	if errors.Is(err, music.ErrSignIn) || errors.Is(err, music.ErrSetup) {
		p.playing = false
		if p.Failed != nil {
			p.Failed(source, err)
		}
		p.sync()
		return
	}
	if dir != stay && skipped+1 < len(p.order) {
		next := (at + dir + len(p.order)) % len(p.order)
		p.loadSkipping(next, skipped+1, dir, p.playing, 0)
		return
	}
	p.playing = false
	p.sync()
}

// errorText says what went wrong with a track, in the listener's terms where
// the source's error is one the app knows.
func errorText(err error) string {
	switch {
	case errors.Is(err, offline.ErrMissing):
		return i18n.T("The local audio file is missing or incomplete")
	case errors.Is(err, offline.ErrSource):
		return i18n.T("The original plugin is unavailable and there is no local audio copy")
	case errors.Is(err, errNoSource):
		return i18n.T("The plugin of this song is gone")
	case errors.Is(err, music.ErrUnavailable):
		if reason := music.Reason(err); reason != "" {
			return reason
		}
		return i18n.T("This song isn't available")
	case errors.Is(err, music.ErrSignIn):
		return i18n.T("Sign in to play this song")
	case errors.Is(err, music.ErrSetup):
		if reason := music.Reason(err); reason != "" {
			return reason
		}
		return i18n.T("The plugin needs setting up")
	}
	return err.Error()
}

var errNoSource = errors.New("player: no such source")

// Session is where playback was left, to pick up again.
type Session struct {
	Queue    []music.Track `json:"queue"`
	Order    []int         `json:"order"`
	At       int           `json:"at"`
	Shuffle  bool          `json:"shuffle"`
	Repeat   RepeatMode    `json:"repeat"`
	Position int64         `json:"position"` // milliseconds
	Radio    string        `json:"radio,omitempty"`
	Speed    float64       `json:"speed,omitempty"`
}

// Session returns where playback is, or false with nothing queued.
func (p *Player) Session() (Session, bool) {
	if p.track == nil || p.at < 0 {
		return Session{}, false
	}
	return Session{
		Queue:    p.queue,
		Order:    p.order,
		At:       p.at,
		Shuffle:  p.shuffle,
		Repeat:   p.repeat,
		Position: p.Position().Milliseconds(),
		Radio:    p.radio,
		Speed:    p.speed,
	}, true
}

// Restore picks up a session where it was left, paused, and loads its
// track to play at once when asked.
func (p *Player) Restore(s Session) {
	n := len(s.Queue)
	if n == 0 || len(s.Order) != n || s.At < 0 || s.At >= n {
		return
	}
	seen := make([]bool, n)
	for _, i := range s.Order {
		if i < 0 || i >= n || seen[i] {
			return
		}
		seen[i] = true
	}
	p.speed = normalizedSpeed(s.Speed)
	p.queue, p.order = s.Queue, s.Order
	p.shuffle, p.repeat = s.Shuffle, s.Repeat%3
	p.loadSkipping(s.At, 0, stay, false, time.Duration(s.Position)*time.Millisecond)
}
