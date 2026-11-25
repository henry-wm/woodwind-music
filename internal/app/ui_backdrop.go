package app

import (
	"sync"
	"time"

	"github.com/nsgo-dev/nsgo/core"
)

// fogWGSLSource paints Now Playing's drifting fog: five-octave domain-warped
// fBM rising slowly through a deep cover shade, with glinting dust specks and
// a soft vignette. Prev and current colors crossfade on the GPU when tracks
// change.
const fogWGSLSource = `struct Uniforms {
	prevDeep: vec4f,
	prevFog: vec4f,
	deep: vec4f,
	fog: vec4f,
	blend: f32,
	time: f32,
}

fn fog_hash(p: vec2f) -> f32 {
	return fract(sin(dot(p, vec2f(127.1, 311.7))) * 43758.5453);
}

fn fog_noise(p: vec2f) -> f32 {
	let i = floor(p);
	let f = fract(p);
	let u = f * f * (3.0 - 2.0 * f);
	let a = fog_hash(i);
	let b = fog_hash(i + vec2f(1.0, 0.0));
	let c = fog_hash(i + vec2f(0.0, 1.0));
	let d = fog_hash(i + vec2f(1.0, 1.0));
	return mix(mix(a, b, u.x), mix(c, d, u.x), u.y);
}

fn fog_fbm(p_in: vec2f) -> f32 {
	var p = p_in;
	var v = 0.0;
	var amp = 0.5;
	for (var i = 0; i < 5; i = i + 1) {
		v = v + amp * fog_noise(p);
		p = p * 2.03 + vec2f(1.7, 9.2);
		amp = amp * 0.5;
	}
	return v;
}

fn shade(uv: vec2f) -> vec4f {
	let size = max(nsh.size, vec2f(1.0, 1.0));
	let pos = uv * size;
	let p = pos / size.y * 2.4;
	let t = uniforms.time + 2.4;
	let rise = vec2f(0.0, t * 0.1);
	let q = vec2f(
		fog_fbm(p + rise + vec2f(0.0, t * 0.04)),
		fog_fbm(p + rise + vec2f(5.2, 1.3) - vec2f(t * 0.05, 0.0))
	);
	let f = fog_fbm(p + rise + 0.9 * q);
	let bank = fog_fbm(p * 0.45 + rise * 0.6 + vec2f(3.7, 0.0));
	let cloud = smoothstep(0.3, 0.78, f) * (0.45 + 0.75 * smoothstep(0.3, 0.65, bank));
	let m = smoothstep(-0.1, 0.4, uv.y) * (1.0 - 0.5 * smoothstep(0.65, 1.0, uv.y));
	let deep = mix(uniforms.prevDeep.rgb, uniforms.deep.rgb, uniforms.blend);
	let fog = mix(uniforms.prevFog.rgb, uniforms.fog.rgb, uniforms.blend);
	var c = mix(deep, fog, min(cloud * m * 0.85, 1.0));
	let g = length((uv - vec2f(0.5, 0.42)) * vec2f(1.0, 1.25));
	c = c * (1.08 - 0.45 * smoothstep(0.2, 0.85, g));
	let cell = size.y / 22.0;
	let sp = (pos + vec2f(0.0, t * cell * 0.9)) / cell;
	let id = floor(sp);
	let h = fog_hash(id + vec2f(3.1, 7.7));
	if (h > 0.93) {
		let at = vec2f(fog_hash(id + vec2f(1.3, 2.9)), fog_hash(id + vec2f(8.1, 4.3))) * 0.8 + 0.1;
		let d = length((fract(sp) - at) * cell) / max(cell * 0.05, 0.7);
		let glint = 0.5 + 0.5 * sin(t * (1.5 + 2.0 * h) + h * 40.0);
		c = c + vec3f(0.75, 0.82, 1.0) * exp(-d * d) * glint * (0.25 + 0.3 * m);
	}
	return vec4f(clamp(c, vec3f(0.0), vec3f(1.0)), 1.0);
}
`

var fogShader = sync.OnceValue(func() *core.ShaderProgram {
	return core.MustCompileWGSL(fogWGSLSource)
})

const (
	fogFadeDuration = 700 * time.Millisecond
	fogClockSpan    = time.Hour
	fogFrameRate    = 30
)

// fogColors returns the deep backdrop shade and the lighter fog color for a
// cover tint, keeping readability behind white foreground text. Neutral covers
// stay neutral.
func fogColors(tint core.Color) (deep, fog core.Color) {
	r, g, b, _ := tint.Components()
	h, s, _ := hsv(r, g, b)
	if s > 0.12 {
		s = min(max(s*1.3, 0.6), 0.85)
	}
	return hsvColor(h, s, 0.3), hsvColor(h, s*0.6, 0.72)
}

// nowPlayingTint returns the current cover's tint, retaining the previous
// track's tint while a new cover is still loading so the backdrop does not
// flash gray between tracks.
func (a *app) nowPlayingTint() core.Color {
	fallback := core.RGB(0x787882)
	t := a.pl.Current()
	if t == nil {
		return fallback
	}
	if t.Album.ImageURL == "" {
		a.npTint = 0
		return fallback
	}
	if c := a.coverTint(t.Album.ImageURL); c != 0 {
		a.npTint = c
		return c
	}
	if a.npTint != 0 {
		return a.npTint
	}
	return fallback
}

// fogBackdrop fills Now Playing with the GPU fog shader. Its clock runs on the
// platform animation driver at 30 fps while music plays and pauses on pause or
// Reduce Motion, leaving the full display rate for scrolls and transitions.
func (a *app) fogBackdrop(cx *core.Scope) core.Node {
	blend := core.NewAnimatedValue(cx, 1)
	clock := core.NewAnimatedValue(cx, 0)
	var initialized bool
	var prevDeep, prevFog, curDeep, curFog core.Color
	var fadeRun *core.AnimationRun

	paused := core.NewMemo(cx, func() bool {
		a.playRev.Get()
		return !a.pl.Playing() || cx.Env().ReduceMotion()
	})
	var clockRun *core.AnimationRun
	var elapsed float64
	var since time.Time
	running := false
	cx.Effect(func() {
		if paused.Get() {
			if running {
				if dt := cx.Now().Sub(since).Seconds(); dt > 0 {
					elapsed += dt
				}
				running = false
			}
			if clockRun != nil {
				clockRun.Stop()
				clockRun = nil
			}
			clock.Set(elapsed)
			return
		}
		if running {
			return
		}
		running = true
		since = cx.Now()
		clock.Set(elapsed)
		span := fogClockSpan.Seconds()
		anim := core.AtFrameRate(
			core.Loop(core.Timing(clock, elapsed+span, core.TimingConfig{
				Duration: fogClockSpan,
				Easing:   core.Easing{Bezier: core.Linear},
			}), 0),
			fogFrameRate,
		)
		// Keep the run on the backdrop's scope so effect re-runs never cancel it.
		cx.Run(func() {
			clockRun = cx.StartAnimation(anim, nil)
		})
	})

	return core.Shader{
		TestID:  "np-fog",
		Program: fogShader(),
		UniformsFunc: func() core.Uniforms {
			a.playRev.Get()
			a.artRev.Get()
			deep, fog := fogColors(a.nowPlayingTint())
			if !initialized {
				initialized = true
				prevDeep, prevFog, curDeep, curFog = deep, fog, deep, fog
				blend.Set(1)
			} else if deep != curDeep || fog != curFog {
				prevDeep, prevFog = curDeep, curFog
				curDeep, curFog = deep, fog
				if fadeRun != nil {
					fadeRun.Stop()
					fadeRun = nil
				}
				if core.Untracked(cx, cx.Env().ReduceMotion) {
					blend.Set(1)
				} else {
					blend.Set(0)
					// Keep the crossfade on the backdrop's scope rather than this
					// UniformsFunc render effect, so playRev/artRev updates do not
					// cancel an in-flight fade.
					cx.Run(func() {
						fadeRun = cx.StartAnimation(core.Timing(blend, 1, core.TimingConfig{
							Duration: fogFadeDuration,
							Easing:   core.Easing{Bezier: core.EaseOut},
						}), nil)
					})
				}
			}
			return core.Uniforms{
				"prevDeep": prevDeep,
				"prevFog":  prevFog,
				"deep":     curDeep,
				"fog":      curFog,
				"blend":    blend,
				"time":     clock,
			}
		},
		Style: core.Style{
			Position:      core.Absolute,
			Top:           core.Pt(0),
			Right:         core.Pt(0),
			Bottom:        core.Pt(0),
			Left:          core.Pt(0),
			PointerEvents: core.PointerEventsNone,
		},
	}
}
