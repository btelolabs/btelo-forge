package render3d

import (
	"math"

	s3 "github.com/btelolabs/btelo-forge/internal/scene3d"
)

// The camera's feel. Turning and dragging are measured against a screen of feelScreen pixels, so that it stays the
// same at any size of the view; all motion is first noted as still to go, then eases out at the same rate.
const (
	feelScreen  = 1440
	feelRate    = 24.0 // the share of what is left that goes each second, as 1 - e^(-rate·dt)
	turnStep    = .25 * math.Pi / 180
	maxPitch    = 89 * math.Pi / 180
	wheelStep   = -.2  // a notch of the wheel changes the distance by 2 to this power
	keyRate     = .9   // keys move this many times the viewing distance each second
	fastKeys    = 3.0  // and this many times more with Shift
	relock      = .5   // turning again within this many seconds keeps the same centre
	settled     = 1e-5 // radians, and shares of the distance, that count as no motion
	maxZoomStep = 1.0
)

// Camera is a place to look from, moved by the mouse and keys. Yaw turns around the up axis; Pitch is above the
// horizon. Pick, when set, finds the distance to the surface along a ray.
type Camera struct {
	Pos        s3.Vec3
	Yaw, Pitch float32
	FovY       float32
	Distance   float32 // to what is being looked at
	Pick       func(origin, dir s3.Vec3) (float32, bool)

	nearest   float32 // the shortest distance that counts, so that nothing slows to a stop close to a surface
	center    s3.Vec3 // what is in the scene
	radius    float32
	pending   s3.Vec3
	turns     []turn
	holding   bool
	pivot     s3.Vec3
	sinceTurn float32
}

// turn is one turning around a centre: dir is the centre's direction in the camera's own axes (right, up, ahead), radius
// eases to goal, and yaw and pitch are the angles still to go.
type turn struct {
	dir          s3.Vec3
	radius, goal float32
	yaw, pitch   float32
}

// NewCamera looks at bounds from the front and a little above.
func NewCamera(bounds s3.Box) *Camera {
	c := &Camera{FovY: .7, Yaw: .55, Pitch: -.3, sinceTurn: relock}
	c.Frame(bounds)
	return c
}

func basis(yaw, pitch float32) (right, up, ahead s3.Vec3) {
	sy, cy := sincos(yaw)
	sp, cp := sincos(pitch)
	ahead = s3.Vec3{-sy * cp, sp, -cy * cp}
	right = s3.Vec3{cy, 0, -sy}
	return right, right.Cross(ahead), ahead
}

func sincos(a float32) (float32, float32) {
	s, c := math.Sincos(float64(a))
	return float32(s), float32(c)
}

func toWorld(yaw, pitch float32, v s3.Vec3) s3.Vec3 {
	r, u, f := basis(yaw, pitch)
	return r.Scale(v[0]).Add(u.Scale(v[1])).Add(f.Scale(v[2]))
}

func toLocal(yaw, pitch float32, v s3.Vec3) s3.Vec3 {
	r, u, f := basis(yaw, pitch)
	return s3.Vec3{v.Dot(r), v.Dot(u), v.Dot(f)}
}

// SetScene says what the scene holds, which sets how fast the camera goes where it finds no surface.
func (c *Camera) SetScene(bounds s3.Box) {
	c.radius, c.center = 1, s3.Vec3{}
	if !bounds.Empty() {
		c.radius, c.center = max(bounds.Size().Length()/2, 1e-4), bounds.Center()
	}
	c.nearest = c.radius * .02
}

// Frame moves back until bounds fit the view, and stops all motion.
func (c *Camera) Frame(bounds s3.Box) {
	c.SetScene(bounds)
	sin := float32(math.Sin(float64(c.FovY) / 2))
	c.Distance = c.radius / sin * 1.15
	_, _, ahead := basis(c.Yaw, c.Pitch)
	c.Pos = c.center.Sub(ahead.Scale(c.Distance))
	c.pending, c.turns, c.holding, c.sinceTurn = s3.Vec3{}, c.turns[:0], false, relock
}

// Ray is the direction through the point (x, y) of a view w×h.
func (c *Camera) Ray(x, y, w, h float32) s3.Vec3 {
	t := float32(math.Tan(float64(c.FovY) / 2))
	right, up, ahead := basis(c.Yaw, c.Pitch)
	return ahead.Add(right.Scale((2*x/w - 1) * t * w / h)).Add(up.Scale((1 - 2*y/h) * t)).Norm()
}

// depth is the distance worth working to from here: to the surface in the middle of the view, or else to the scene, so
// that going away speeds up and coming back slows down.
func (c *Camera) depth() float32 {
	_, _, ahead := basis(c.Yaw, c.Pitch)
	if c.Pick != nil {
		if d, ok := c.Pick(c.Pos, ahead); ok {
			return max(d, c.nearest)
		}
	}
	return max(c.center.Sub(c.Pos).Length()-c.radius, c.radius*.25)
}

// aim is the point of the surface under (x, y), or the point at the viewing distance when there is none.
func (c *Camera) aim(x, y, w, h float32) s3.Vec3 {
	dir := c.Ray(x, y, w, h)
	if c.Pick != nil {
		if d, ok := c.Pick(c.Pos, dir); ok {
			return c.Pos.Add(dir.Scale(d))
		}
	}
	return c.Pos.Add(dir.Scale(c.Distance))
}

// Grab starts a turn at (x, y): around the point under it, or on the spot when around is false. Pressing again soon
// after letting go keeps the centre, so that a long turn can be made in several drags.
func (c *Camera) Grab(x, y, w, h float32, around bool) {
	c.holding = true
	t := turn{dir: s3.Vec3{0, 0, 1}}
	if around {
		if c.sinceTurn >= relock {
			c.pivot = c.aim(x, y, w, h)
		}
		offset := c.pivot.Sub(c.Pos)
		if d := offset.Length(); d > 1e-6 {
			t.dir, t.radius, t.goal = toLocal(c.Yaw, c.Pitch, offset.Scale(1/d)), d, d
			_, _, ahead := basis(c.Yaw, c.Pitch)
			if depth := offset.Dot(ahead); depth > 0 {
				c.Distance = depth
			}
		}
	}
	c.turns = append(c.turns, t)
}

// Release ends the turn.
func (c *Camera) Release() { c.holding = false }

// Turn adds a drag of (dx, dy) pixels, in a view h high, to the turn being held.
func (c *Camera) Turn(dx, dy, h float32) {
	if !c.holding || len(c.turns) == 0 {
		return
	}
	scale := feelScreen / h * turnStep
	final := c.Pitch
	for i := range c.turns {
		final += c.turns[i].pitch
	}
	goal := min(max(final-dy*scale, -maxPitch), maxPitch)
	t := &c.turns[len(c.turns)-1]
	t.yaw -= dx * scale
	t.pitch += goal - final
	c.sinceTurn = 0
}

// Pan moves the camera sideways with a drag of (dx, dy) pixels in a view h high, so that what is at the viewing
// distance follows the pointer.
func (c *Camera) Pan(dx, dy, h float32) {
	unit := 2 * float32(math.Tan(float64(c.FovY)/2)) * c.Distance / h
	right, up, _ := basis(c.Yaw, c.Pitch)
	c.pending = c.pending.Add(right.Scale(-dx * unit)).Add(up.Scale(dy * unit))
}

// Move goes by the keys for dt seconds: right and forward are -1, 0 or 1, along the ground.
func (c *Camera) Move(right, forward, dt float32, fast bool) {
	if right == 0 && forward == 0 {
		return
	}
	step := keyRate * c.Distance * dt / float32(math.Hypot(float64(right), float64(forward)))
	if fast {
		step *= fastKeys
	}
	r, _, _ := basis(c.Yaw, c.Pitch)
	ahead := s3.Vec3{r[2], 0, -r[0]}
	c.pending = c.pending.Add(r.Scale(right * step)).Add(ahead.Scale(forward * step))
}

// Zoom moves towards the point under (x, y), notches of the wheel forward, or away; while a turn is held it changes
// the turn's radius instead.
func (c *Camera) Zoom(notches, x, y, w, h float32) {
	if notches == 0 {
		return
	}
	factor := float32(math.Exp2(float64(min(max(wheelStep*notches, -maxZoomStep), maxZoomStep))))
	if c.holding && len(c.turns) > 0 {
		if t := &c.turns[len(c.turns)-1]; t.radius > 0 {
			t.goal = max(t.goal*factor, c.nearest)
			return
		}
	}
	aim := c.aim(x, y, w, h)
	offset := c.Pos.Add(c.pending).Sub(aim)
	next := offset.Scale(factor)
	if d := next.Length(); factor < 1 && d < c.nearest {
		// Too close to the point to come closer by a share of the way: go on by a fixed step, through the surface if
		// that is where the way leads.
		dir := c.Ray(x, y, w, h)
		c.pending = c.pending.Add(dir.Scale(c.nearest * (1 - factor) * 4))
		return
	}
	c.pending = c.pending.Add(next.Sub(offset))
	_, _, ahead := basis(c.Yaw, c.Pitch)
	if depth := -next.Dot(ahead); depth > 0 {
		c.Distance = depth
	}
}

// Moving reports whether any motion is left to go.
func (c *Camera) Moving() bool { return c.pending != (s3.Vec3{}) || len(c.turns) > 0 }

// Advance eases dt seconds of what is still to go, and reports whether anything is left.
func (c *Camera) Advance(dt float32) bool {
	c.sinceTurn += dt
	if c.pending == (s3.Vec3{}) && len(c.turns) == 0 {
		return false
	}
	k := 1 - float32(math.Exp(-feelRate*float64(dt)))
	kept := c.turns[:0]
	for i := range c.turns {
		t := c.turns[i]
		c.step(&t, k)
		if (c.holding && i == len(c.turns)-1) || !t.settled() {
			kept = append(kept, t)
			continue
		}
		c.step(&t, 1)
	}
	c.turns = kept
	move := c.pending.Scale(k)
	c.Pos = c.Pos.Add(move)
	c.pending = c.pending.Sub(move)
	if c.pending.Length() < c.Distance*settled {
		c.Pos = c.Pos.Add(c.pending)
		c.pending = s3.Vec3{}
	}
	c.Yaw = float32(math.Mod(float64(c.Yaw), 2*math.Pi))
	c.Distance += (c.depth() - c.Distance) * k
	return c.pending != (s3.Vec3{}) || len(c.turns) > 0
}

// step takes share k of what turn t has left: the centre stays put in the world and the eye goes around it.
func (c *Camera) step(t *turn, k float32) {
	before := toWorld(c.Yaw, c.Pitch, t.dir).Scale(t.radius)
	yaw, pitch := t.yaw*k, t.pitch*k
	t.yaw -= yaw
	t.pitch -= pitch
	t.radius += (t.goal - t.radius) * k
	c.Yaw += yaw
	c.Pitch += pitch
	c.Pos = c.Pos.Add(before).Sub(toWorld(c.Yaw, c.Pitch, t.dir).Scale(t.radius))
}

func (t *turn) settled() bool {
	abs := func(v float32) float32 { return float32(math.Abs(float64(v))) }
	return abs(t.yaw) < settled && abs(t.pitch) < settled && abs(t.goal-t.radius) < settled*max(t.goal, 1e-3)
}

// Axes is the camera's right, up and ahead directions.
func (c *Camera) Axes() (right, up, ahead s3.Vec3) { return basis(c.Yaw, c.Pitch) }

// Project is where p falls in a view w×h, if it is in front of the camera.
func (c *Camera) Project(p s3.Vec3, w, h float32) (x, y float32, ok bool) {
	right, up, ahead := basis(c.Yaw, c.Pitch)
	d := p.Sub(c.Pos)
	z := d.Dot(ahead)
	if z <= 0 {
		return 0, 0, false
	}
	t := float32(math.Tan(float64(c.FovY)/2)) * z
	return (d.Dot(right)/(t*w/h) + 1) * w / 2, (1 - d.Dot(up)/t) * h / 2, true
}

// View is the transform from the world to the camera.
func (c *Camera) View() s3.Mat4 {
	r, u, f := basis(c.Yaw, c.Pitch)
	return s3.Mat4{r[0], u[0], -f[0], 0, r[1], u[1], -f[1], 0, r[2], u[2], -f[2], 0, -r.Dot(c.Pos), -u.Dot(c.Pos), f.Dot(c.Pos), 1}
}

// Projection is a perspective for a view of the given shape, with depth from 0 to 1 as WebGPU has it.
func (c *Camera) Projection(aspect, near, far float32) s3.Mat4 {
	t := 1 / float32(math.Tan(float64(c.FovY)/2))
	return s3.Mat4{t / aspect, 0, 0, 0, 0, t, 0, 0, 0, 0, far / (near - far), -1, 0, 0, near * far / (near - far), 0}
}
