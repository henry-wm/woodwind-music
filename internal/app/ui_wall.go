package app

import (
	"fmt"
	"math"
	"time"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/HengWh/woodwind-music/internal/i18n"
	"github.com/HengWh/woodwind-music/internal/lyrics"
	"github.com/HengWh/woodwind-music/internal/music"
)

// The wall lays the queue out as a free-dragging field of covers under the
// playing song: dragging moves it in 2D and flings glide smoothly to rest,
// two-finger trackpad and wheel scrolls pan it immediately, rows sway gently
// in alternating directions while playing, and clicking a cover jumps to it.

const (
	wallTile     float32 = 112
	wallGap      float32 = 14
	wallPitch    float32 = wallTile + wallGap
	wallSway     float64 = 36
	wallSwayHalf         = 7 * time.Second
	wallFrost    float32 = 72
	wallFoot     float32 = 112
	wallPad      float32 = 48
)

var (
	wallGlideDecel  = 0.996
	wallGlideSpring = core.SpringConfig{Stiffness: 70, Damping: 17, Mass: 1}
)

// wallState persists the wall's resting offset and last auto-centered track
// across reactive updates of Now Playing.
type wallState struct {
	offX, offY float64
	track      string
}

// wallLayout describes the grid dimensions and valid resting offset bounds
// for n covers inside a viewport of w×h between top and foot insets.
type wallLayout struct {
	cols, rows int
	minX, maxX float64
	minY, maxY float64
}

type wallRange struct {
	r0, r1, c0, c1 int
}

// layWall lays n covers out in an area w×h, keeping the first row below the
// top frost and the last row above the bottom controls. Long queues spread
// wider than tall so the wall pans freely along both axes; short queues stay
// horizontally centered.
func layWall(n int, w, h, top, foot float32) wallLayout {
	if n <= 0 {
		return wallLayout{}
	}
	fit := max(1, int((w-2*wallPad+wallGap)/wallPitch))
	cols := max(min(n, fit), int(math.Ceil(math.Sqrt(float64(n)*2.5))))
	cols = max(1, min(cols, n))
	rows := (n + cols - 1) / cols
	cw := float32(cols)*wallPitch - wallGap
	ch := float32(rows)*wallPitch - wallGap

	bw := cw + 2*wallPad - w
	minX := float64(w - wallPad - cw)
	maxX := minX + float64(bw)
	if bw < 0 {
		minX = float64((w - cw) / 2)
		maxX = minX
	}

	bh := ch + top + foot - h
	minY := float64(h - foot - ch)
	maxY := minY + float64(bh)
	if bh < 0 {
		minY = float64(top)
		maxY = minY
	}
	return wallLayout{cols: cols, rows: rows, minX: minX, maxX: maxX, minY: minY, maxY: maxY}
}

func (l wallLayout) clamp(x, y float64) (float64, float64) {
	return min(max(x, l.minX), l.maxX), min(max(y, l.minY), l.maxY)
}

func (l wallLayout) cell(i int) (float64, float64) {
	if l.cols <= 0 {
		return 0, 0
	}
	return float64(i%l.cols) * float64(wallPitch), float64(i/l.cols) * float64(wallPitch)
}

func (l wallLayout) centre(i int, w, h, top, foot float32) (float64, float64) {
	cx, cy := l.cell(i)
	x := float64(w)/2 - cx - float64(wallTile)/2
	y := float64(top+h-foot)/2 - cy - float64(wallTile)/2
	return l.clamp(x, y)
}

func (l wallLayout) shows(i int, x, y float64, w, h, top, foot float32) bool {
	cx, cy := l.cell(i)
	px, py := x+cx, y+cy
	return px >= 0 && px+float64(wallTile) <= float64(w) && py >= float64(top) && py+float64(wallTile) <= float64(h-foot)
}

func (l wallLayout) visibleRange(x, y float64, w, h float32) wallRange {
	if l.cols <= 0 || l.rows <= 0 {
		return wallRange{r0: 0, r1: -1, c0: 0, c1: -1}
	}
	pad := float64(wallPitch)
	c0 := max(0, int(math.Floor((-x-wallSway-pad)/float64(wallPitch))))
	c1 := min(l.cols-1, int(math.Floor((float64(w)-x+wallSway+pad)/float64(wallPitch))))
	r0 := max(0, int(math.Floor((-y-pad)/float64(wallPitch))))
	r1 := min(l.rows-1, int(math.Floor((float64(h)-y+pad)/float64(wallPitch))))
	return wallRange{r0: r0, r1: r1, c0: c0, c1: c1}
}

func unionWallRange(a, b wallRange, maxSpanCols, maxSpanRows int) wallRange {
	if a.r1 < a.r0 {
		return b
	}
	if b.r1 < b.r0 {
		return a
	}
	out := wallRange{
		r0: min(a.r0, b.r0),
		r1: max(a.r1, b.r1),
		c0: min(a.c0, b.c0),
		c1: max(a.c1, b.c1),
	}
	// Keep union bounded on very long flings so we never mount hundreds of tiles at once.
	if out.c1-out.c0 > maxSpanCols {
		out.c0, out.c1 = b.c0, b.c1
	}
	if out.r1-out.r0 > maxSpanRows {
		out.r0, out.r1 = b.r0, b.r1
	}
	return out
}

// wallView fills Now Playing with the free-dragging cover wall, the playing
// track above it and the transport bar along the bottom.
func (a *app) wallView(cx *core.Scope, p palette, white core.Color) core.Node {
	size := cx.Env().Window()
	w := float32(max(360, size.Width))
	h := float32(max(420, size.Height-40)) // minus topbar
	const tabsH float32 = 34
	songH := min(max(h*0.34, 184), 280)
	wallY := tabsH + songH - wallFrost/2
	wallH := max(200, h-wallY)

	queue := a.pl.Queue()
	playing := -1
	if order, at := a.pl.PlayOrder(); at >= 0 && at < len(order) {
		playing = order[at]
	}
	layout := layWall(len(queue), w, wallH, wallFrost, wallFoot)

	ws := &a.wall
	tr := a.pl.Current()
	trackKey := ""
	if tr != nil {
		trackKey = tr.Source + "\x00" + tr.ID
	}

	var glideTo *core.Point
	if playing >= 0 && trackKey != "" && trackKey != ws.track {
		if ws.track == "" {
			ws.offX, ws.offY = layout.centre(playing, w, wallH, wallFrost, wallFoot)
		} else if !layout.shows(playing, ws.offX, ws.offY, w, wallH, wallFrost, wallFoot) {
			tx, ty := layout.centre(playing, w, wallH, wallFrost, wallFoot)
			glideTo = &core.Point{X: core.Points(tx), Y: core.Points(ty)}
		}
		ws.track = trackKey
	} else {
		ws.offX, ws.offY = layout.clamp(ws.offX, ws.offY)
	}

	basePos := core.NewSignal(cx, core.Point{X: core.Points(ws.offX), Y: core.Points(ws.offY)})
	animX := core.NewAnimatedValue(cx, 0)
	animY := core.NewAnimatedValue(cx, 0)
	vis := core.NewSignal(cx, layout.visibleRange(ws.offX, ws.offY, w, wallH))
	dragging := core.NewSignal(cx, false)

	var glideRun *core.AnimationRun
	stopGlide := func() {
		if glideRun != nil {
			glideRun.Stop()
			glideRun = nil
		}
	}

	setVis := func(next wallRange) {
		if vis.GetUntracked() != next {
			vis.Set(next)
		}
	}

	commitBase := func(x, y float64) {
		ws.offX, ws.offY = x, y
		basePos.Set(core.Point{X: core.Points(x), Y: core.Points(y)})
		animX.Set(0)
		animY.Set(0)
		setVis(layout.visibleRange(x, y, w, wallH))
	}

	animateTo := func(fromX, fromY, toX, toY, vx, vy float64) {
		stopGlide()
		toX, toY = layout.clamp(toX, toY)
		ws.offX, ws.offY = toX, toY
		dstVis := layout.visibleRange(toX, toY, w, wallH)
		if core.Untracked(cx, cx.Env().ReduceMotion) {
			commitBase(toX, toY)
			return
		}
		bp := basePos.GetUntracked()
		srcVis := layout.visibleRange(fromX, fromY, w, wallH)
		setVis(unionWallRange(srcVis, dstVis, 14, 10))
		sx, sy := wallGlideSpring, wallGlideSpring
		sx.Velocity, sy.Velocity = vx, vy
		glideRun = cx.StartAnimation(core.Parallel(
			core.Spring(animX, toX-float64(bp.X), sx),
			core.Spring(animY, toY-float64(bp.Y), sy),
		), func(finished bool) {
			if finished {
				glideRun = nil
				commitBase(toX, toY)
			}
		})
	}

	if glideTo != nil {
		animateTo(ws.offX, ws.offY, float64(glideTo.X), float64(glideTo.Y), 0, 0)
	}

	// Row sway: even and odd rows drift smoothly in opposite directions while
	// playing, settling back to center when paused or under Reduce Motion.
	sway := core.NewAnimatedValue(cx, 0)
	swayNeg := core.Mul(sway, core.Constant(-1))
	var swayRun *core.AnimationRun
	swayPlaying := false
	cx.Effect(func() {
		a.playRev.Get()
		want := a.pl.Playing() && !cx.Env().ReduceMotion()
		if want == swayPlaying && swayRun != nil {
			return
		}
		swayPlaying = want
		if swayRun != nil {
			swayRun.Stop()
			swayRun = nil
		}
		if want {
			// Start with a smooth loop between +wallSway and -wallSway; infinite
			// Loop is marked endless so tests settle without waiting.
			swayRun = cx.StartAnimation(core.Loop(core.Sequence(
				core.Timing(sway, wallSway, core.TimingConfig{Duration: wallSwayHalf, Easing: core.Easing{Bezier: core.EaseInOut}}),
				core.Timing(sway, -wallSway, core.TimingConfig{Duration: wallSwayHalf, Easing: core.Easing{Bezier: core.EaseInOut}}),
			), 0), nil)
		} else if !cx.Env().ReduceMotion() {
			swayRun = cx.StartAnimation(core.Spring(sway, 0, core.SpringConfig{Stiffness: 60, Damping: 16, Mass: 1}), nil)
		} else {
			sway.Set(0)
		}
	})

	// Pan (mouse drag & touch drag) with rubber-banding at edges and inertial
	// spring glide on release.
	var panStartX, panStartY, panCurX, panCurY float64
	onPan := func(ev core.PanEvent) {
		switch ev.Phase {
		case core.GestureBegan:
			stopGlide()
			dragging.Set(true)
			panStartX, panStartY = ws.offX, ws.offY
			panCurX, panCurY = panStartX, panStartY
		case core.GestureChanged:
			rawX := panStartX + float64(ev.Translation.X)
			rawY := panStartY + float64(ev.Translation.Y)
			panCurX = core.RubberBandClamp(rawX, layout.minX, layout.maxX, float64(w))
			panCurY = core.RubberBandClamp(rawY, layout.minY, layout.maxY, float64(wallH))
			ws.offX, ws.offY = panCurX, panCurY
			bp := basePos.GetUntracked()
			animX.Set(panCurX - float64(bp.X))
			animY.Set(panCurY - float64(bp.Y))
			setVis(layout.visibleRange(panCurX, panCurY, w, wallH))
		case core.GestureEnded, core.GestureCancelled:
			dragging.Set(false)
			vx, vy := float64(ev.Velocity.X), float64(ev.Velocity.Y)
			projX := core.ProjectDecay(panCurX, core.DecayConfig{Velocity: vx, Deceleration: wallGlideDecel})
			projY := core.ProjectDecay(panCurY, core.DecayConfig{Velocity: vy, Deceleration: wallGlideDecel})
			animateTo(panCurX, panCurY, projX, projY, vx, vy)
		}
	}

	// Trackpad two-finger scroll and mouse wheel pan the wall immediately.
	onWheel := func(ev core.WheelEvent) {
		stopGlide()
		nx, ny := layout.clamp(ws.offX+float64(ev.Delta.X), ws.offY+float64(ev.Delta.Y))
		if nx != ws.offX || ny != ws.offY {
			commitBase(nx, ny)
		}
	}

	// Plane holding virtualized rows of cover tiles.
	plane := core.View{
		TestID: "wall-plane",
		Style: core.Style{
			Position: core.Absolute,
			Width:    core.Pt(core.Points(float32(layout.cols) * wallPitch)),
			Height:   core.Pt(core.Points(float32(layout.rows) * wallPitch)),
			Animated: &core.AnimatedStyle{TranslateX: animX, TranslateY: animY},
		},
		StyleFunc: func() core.Style {
			bp := basePos.Get()
			return core.Style{Left: core.Pt(bp.X), Top: core.Pt(bp.Y)}
		},
		Children: []core.Node{
			core.Dynamic(func(cx *core.Scope) core.Node {
				vr := vis.Get()
				if vr.r1 < vr.r0 || vr.c1 < vr.c0 {
					return core.Group{}
				}
				rows := make([]core.Node, 0, vr.r1-vr.r0+1)
				for r := vr.r0; r <= vr.r1; r++ {
					rowSway := core.AnimatedNode(sway)
					if r%2 != 0 {
						rowSway = swayNeg
					}
					tiles := make([]core.Node, 0, vr.c1-vr.c0+1)
					for col := vr.c0; col <= vr.c1; col++ {
						idx := r*layout.cols + col
						if idx >= len(queue) {
							break
						}
						tiles = append(tiles, wallTileNode{
							app:      a,
							track:    queue[idx],
							index:    idx,
							x:        core.Points(float32(col) * wallPitch),
							playing:  idx == playing,
							dragging: dragging,
							white:    white,
						})
					}
					rows = append(rows, core.View{
						Style: core.Style{
							Position: core.Absolute,
							Top:      core.Pt(core.Points(float32(r) * wallPitch)),
							Left:     core.Pt(0),
							Width:    core.Pt(core.Points(float32(layout.cols) * wallPitch)),
							Height:   core.Pt(core.Points(wallTile)),
							Animated: &core.AnimatedStyle{TranslateX: rowSway},
						},
						Children: tiles,
					})
				}
				return core.Group(rows)
			}),
		},
	}

	area := core.View{
		TestID:  "wall-area",
		OnPan:   onPan,
		OnWheel: onWheel,
		Style: core.Style{
			Position: core.Absolute,
			Top:      core.Pt(core.Points(wallY)),
			Left:     core.Pt(0),
			Right:    core.Pt(0),
			Bottom:   core.Pt(0),
			Overflow: core.Hidden,
		},
		Children: []core.Node{plane},
	}

	topMask := core.LinearGradient(180, core.RGB(0x000000), core.RGBA(0x000000, 0))
	frostOverlay := core.View{
		Style: core.Style{
			Position:        core.Absolute,
			Top:             core.Pt(core.Points(wallY)),
			Left:            core.Pt(0),
			Right:           core.Pt(0),
			Height:          core.Pt(core.Points(wallFrost)),
			PointerEvents:   core.PointerEventsNone,
			BackdropFilters: []core.Filter{core.Blur(16)},
			Mask:            &topMask,
			Gradients:       []core.Gradient{core.LinearGradient(180, core.RGBA(0x0b0b0d, 0.55), core.RGBA(0x0b0b0d, 0))},
		},
	}

	footMask := core.LinearGradient(0, core.RGB(0x000000), core.RGBA(0x000000, 0))
	footOverlay := core.View{
		Style: core.Style{
			Position:        core.Absolute,
			Left:            core.Pt(0),
			Right:           core.Pt(0),
			Bottom:          core.Pt(0),
			Height:          core.Pt(core.Points(wallFoot)),
			PointerEvents:   core.PointerEventsNone,
			BackdropFilters: []core.Filter{core.Blur(14)},
			Mask:            &footMask,
			Gradients:       []core.Gradient{core.LinearGradient(0, core.RGBA(0x0b0b0d, 0.65), core.RGBA(0x0b0b0d, 0))},
		},
	}

	tabsHeader := core.View{
		Style: core.Style{
			Position: core.Absolute,
			Top:      core.Pt(0),
			Left:     core.Pt(0),
			Right:    core.Pt(0),
			Height:   core.Pt(core.Points(tabsH)),
			Padding:  core.Symmetric(0, max(core.Points(44), (size.Width-1280)/2)),
		},
		Children: []core.Node{a.paneTabs(cx, white)},
	}

	return core.View{
		TestID: "wall-view",
		Style: core.Style{
			FlexGrow:   1,
			FlexShrink: 1,
			FlexBasis:  core.Pt(0),
			MinHeight:  core.Pt(0),
			Position:   core.Relative,
		},
		Children: []core.Node{
			area,
			frostOverlay,
			footOverlay,
			a.wallSongHeader(cx, p, tr, tabsH, songH, white),
			a.wallTransportBar(cx, p, white),
			tabsHeader,
		},
	}
}

// wallSongHeader shows the currently playing song centered above the wall:
// its cover (breathing gently on play/pause), title, like & menu controls,
// artists, and the active lyric line with the next line beneath it.
func (a *app) wallSongHeader(cx *core.Scope, p palette, tr *music.Track, top, h float32, white core.Color) core.Node {
	if tr == nil {
		return core.Group{}
	}
	coverSize := core.Points(max(120, h-44))
	coverPal := palette{sidebar: core.RGBA(0xffffff, 0.08), faint: core.RGBA(0xffffff, 0.3), hover: core.RGBA(0xffffff, 0.08)}

	breathe := core.NewAnimatedValue(cx, 1)
	if !a.pl.Playing() {
		breathe.Set(0.92)
	}
	cx.Effect(func() {
		a.playRev.Get()
		target := 1.0
		if !a.pl.Playing() {
			target = 0.92
		}
		if core.Untracked(cx, cx.Env().ReduceMotion) {
			breathe.Set(target)
			return
		}
		cx.StartAnimation(core.Spring(breathe, target, core.SpringConfig{Stiffness: 110, Damping: 14, Mass: 1}), nil)
	})

	cover := core.View{
		TestID: "wall-playing-cover",
		Style: core.Style{
			Width:        core.Pt(coverSize),
			Height:       core.Pt(coverSize),
			FlexShrink:   0,
			CornerRadius: 16,
			ShadowColor:  core.RGBA(0x000000, 0.55),
			ShadowRadius: 24,
			ShadowOffset: core.Point{Y: 12},
			Animated:     &core.AnimatedStyle{Scale: breathe},
		},
		Children: []core.Node{a.cover(coverPal, tr.Album.ImageURL, coverSize, 16)},
	}

	lyricLines := core.Dynamic(func(cx *core.Scope) core.Node {
		a.tick.Get()
		a.playRev.Get()
		lines, _ := a.pl.Lyrics()
		if len(lines) == 0 {
			return core.Group{}
		}
		cur := lyrics.Index(lines, a.pl.Position())
		var children []core.Node
		if cur >= 0 {
			children = append(children, core.Text{
				Content:  lines[cur].Text,
				MaxLines: 2,
				Style:    core.Style{FontSize: 16.5, FontWeight: core.FontWeightBold, Color: core.RGBA(0xffffff, 0.85), Margin: core.Edges{Top: 12}},
			})
		}
		if cur+1 < len(lines) {
			children = append(children, core.Text{
				Content:  lines[cur+1].Text,
				MaxLines: 1,
				Style:    core.Style{FontSize: 13.5, FontWeight: core.FontWeightSemibold, Color: core.RGBA(0xffffff, 0.38), Margin: core.Edges{Top: 2}},
			})
		}
		if len(children) == 0 {
			return core.Group{}
		}
		return core.View{Style: core.Style{Direction: core.Column}, Children: children}
	})

	info := core.View{
		Style: core.Style{Width: core.Pt(440), FlexShrink: 1, MinWidth: core.Pt(0), Direction: core.Column, Gap: 4},
		Children: []core.Node{
			a.radioChip(),
			core.View{
				Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8},
				Children: []core.Node{
					core.Text{
						TestID:   "wall-title",
						Content:  tr.Name,
						MaxLines: 1,
						Style:    core.Style{FlexShrink: 1, MinWidth: core.Pt(0), FontSize: 24, FontWeight: core.FontWeightBold, LetterSpacing: -0.4, Color: white},
					},
					a.npLikeButton(p, *tr, 20, core.Style{Width: core.Pt(30), Height: core.Pt(30), Direction: core.Row, AlignItems: core.AlignCenter,
						JustifyContent: core.JustifyCenter, CornerRadius: 15, Hover: &core.Style{Background: core.RGBA(0xffffff, 0.1)}}),
					ui.Menu{
						Trigger:  ui.Button{Label: i18n.T("More"), Variant: ui.ButtonGhost, Side: 30},
						Face:     icon(lucideMore, 20, core.RGBA(0xffffff, 0.7)),
						Children: a.trackMenu(*tr, "", -1),
					},
				},
			},
			core.Text{
				Spans:    a.artistSpans(palette{white: white}, *tr, 14, core.RGBA(0xffffff, 0.6)),
				MaxLines: 1,
				Style:    core.Style{FontSize: 14, Color: core.RGBA(0xffffff, 0.6)},
			},
			lyricLines,
		},
	}

	return core.View{
		TestID: "wall-song",
		Style: core.Style{
			Position:       core.Absolute,
			Top:            core.Pt(core.Points(top)),
			Left:           core.Pt(0),
			Right:          core.Pt(0),
			Height:         core.Pt(core.Points(h)),
			Padding:        core.Symmetric(0, 72),
			Gap:            36,
			Direction:      core.Row,
			JustifyContent: core.JustifyCenter,
			AlignItems:     core.AlignCenter,
		},
		Children: []core.Node{cover, info},
	}
}

// wallTransportBar renders the progress bar and transport controls floating
// along the bottom of the wall.
func (a *app) wallTransportBar(_ *core.Scope, p palette, white core.Color) core.Node {
	dark := palette{
		accent: white, press: core.RGBA(0xffffff, 0.18), text: white,
		muted: core.RGBA(0xffffff, 0.5), faint: core.RGBA(0xffffff, 0.35),
		hover: core.RGBA(0xffffff, 0.1), onAccent: p.onAccent,
	}
	return core.View{
		TestID: "wall-bar",
		Style: core.Style{
			Position:  core.Absolute,
			Left:      core.Pt(0),
			Right:     core.Pt(0),
			Bottom:    core.Pt(0),
			Direction: core.Column,
			Gap:       10,
			Padding:   core.Edges{Left: 48, Right: 48, Bottom: 24},
		},
		Children: []core.Node{
			a.progress(dark, true),
			core.View{
				Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 14},
				Children: []core.Node{
					core.View{
						Style: core.Style{FlexGrow: 1, FlexBasis: core.Pt(0), MinWidth: core.Pt(0), Direction: core.Row},
						Children: []core.Node{
							core.Text{
								ContentFunc: func() string { a.tick.Get(); return clock(a.pl.Position()) },
								Style:       core.Style{FontSize: 11.5, Color: core.RGBA(0xffffff, 0.5)},
							},
						},
					},
					core.View{
						Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter},
						Children: a.transport(palette{
							accent: p.accent, hover: core.RGBA(0xffffff, 0.1), press: core.RGBA(0xffffff, 0.16),
							text: white, muted: core.RGBA(0xffffff, 0.5), onAccent: p.onAccent,
						}, core.RGBA(0xffffff, 0.55), 22),
					},
					core.View{
						Style: core.Style{FlexGrow: 1, FlexBasis: core.Pt(0), MinWidth: core.Pt(0), Direction: core.Row, JustifyContent: core.JustifyEnd},
						Children: []core.Node{
							core.Text{
								ContentFunc: func() string { a.tick.Get(); return "-" + clock(a.pl.Duration()-a.pl.Position()) },
								Style:       core.Style{FontSize: 11.5, Color: core.RGBA(0xffffff, 0.5)},
							},
						},
					},
				},
			},
		},
	}
}

// wallTileNode renders a single cover tile on the wall with its own hover
// state so hovering a tile only updates that tile.
type wallTileNode struct {
	app      *app
	track    music.Track
	index    int
	x        core.Points
	playing  bool
	dragging *core.Signal[bool]
	white    core.Color
}

func (n wallTileNode) Render(cx *core.Scope) core.Node {
	hovered := core.NewSignal(cx, false)
	tr := n.track
	coverPal := palette{faint: core.RGBA(0xffffff, 0.3), hover: core.RGBA(0xffffff, 0.08)}

	trigger := core.View{
		OnMouseEnter: func(core.MouseEvent) {
			if !n.dragging.GetUntracked() {
				hovered.Set(true)
			}
		},
		OnMouseLeave: func(core.MouseEvent) { hovered.Set(false) },
		Style: core.Style{
			Width:  core.Pt(core.Points(wallTile)),
			Height: core.Pt(core.Points(wallTile)),
		},
		Children: []core.Node{
			base.Button{
				Label:  tr.Name,
				TestID: "wall-" + tr.ID,
				OnPress: func(core.PressEvent) {
					if !tr.Unavailable && !n.playing {
						n.app.pl.Jump(n.index)
					}
				},
				StyleFunc: func() core.Style {
					isHovered := hovered.Get() && !n.dragging.Get()
					scale := float32(1)
					switch {
					case n.playing:
						scale = 1.08
					case isHovered:
						scale = 1.04
					}
					s := core.Style{
						Width:        core.Pt(core.Points(wallTile)),
						Height:       core.Pt(core.Points(wallTile)),
						CornerRadius: 12,
						Overflow:     core.Hidden,
						Transform:    core.Transform{ScaleX: scale, ScaleY: scale},
						Transition:   core.Transition{Duration: 220 * time.Millisecond, Easing: core.EaseOut, Properties: core.TransitionTransform},
					}
					if tr.Unavailable {
						s.Opacity = core.Alpha(0.4)
					}
					if n.playing {
						s.ShadowColor = core.RGBA(0x000000, 0.6)
						s.ShadowRadius = 18
						s.ShadowOffset = core.Point{Y: 10}
						s.BorderWidth = 2
						s.BorderColor = core.RGBA(0xffffff, 0.9)
					}
					return s
				},
				Children: []core.Node{
					n.app.cover(coverPal, tr.Album.ImageURL, core.Points(wallTile), 12),
					core.Show{
						When: func() bool { return hovered.Get() && !n.dragging.Get() },
						Then: func(*core.Scope) core.Node {
							var lines []core.Node
							lines = append(lines, core.Text{
								Content:  tr.Name,
								MaxLines: 1,
								Style:    core.Style{FontSize: 12, FontWeight: core.FontWeightBold, Color: n.white},
							})
							if names := tr.ArtistNames(); names != "" {
								lines = append(lines, core.Text{
									Content:  names,
									MaxLines: 1,
									Style:    core.Style{FontSize: 10.5, Color: core.RGBA(0xffffff, 0.7)},
								})
							}
							return core.View{
								Style: core.Style{
									Position:       core.Absolute,
									Top:            core.Pt(0),
									Left:           core.Pt(0),
									Right:          core.Pt(0),
									Bottom:         core.Pt(0),
									CornerRadius:   12,
									Padding:        core.All(8),
									Gap:            1,
									Direction:      core.Column,
									JustifyContent: core.JustifyEnd,
									PointerEvents:  core.PointerEventsNone,
									Gradients:      []core.Gradient{core.LinearGradient(0, core.RGBA(0x000000, 0.78), core.RGBA(0x000000, 0))},
								},
								Children: lines,
							}
						},
					},
				},
			},
		},
	}
	return core.View{
		TestID: fmt.Sprintf("wall-tile-%d", n.index),
		Style: core.Style{
			Position: core.Absolute,
			Left:     core.Pt(n.x),
			Top:      core.Pt(0),
			Width:    core.Pt(core.Points(wallTile)),
			Height:   core.Pt(core.Points(wallTile)),
		},
		Children: []core.Node{ui.ContextMenu{Trigger: trigger, Children: n.app.trackMenu(tr, "", -1)}},
	}
}
