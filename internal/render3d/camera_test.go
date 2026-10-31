package render3d

import (
	"math"
	"testing"

	s3 "github.com/btelolabs/btelo-forge/internal/scene3d"
)

const testW, testH = 1280.0, 720.0

func settle(c *Camera) {
	for range 600 {
		if !c.Advance(1.0 / 60) {
			return
		}
	}
}

func unitBox() s3.Box { return s3.Box{Min: s3.Vec3{-1, -1, -1}, Max: s3.Vec3{1, 1, 1}} }

// project is where a point falls in the view.
func project(c *Camera, p s3.Vec3) (float32, float32) {
	m := c.Projection(testW/testH, .01, 1000).Mul(c.View())
	x, y, w := m[0]*p[0]+m[4]*p[1]+m[8]*p[2]+m[12], m[1]*p[0]+m[5]*p[1]+m[9]*p[2]+m[13], m[3]*p[0]+m[7]*p[1]+m[11]*p[2]+m[15]
	return (x/w*.5 + .5) * testW, (.5 - y/w*.5) * testH
}

func TestFrameShowsTheBox(t *testing.T) {
	c := NewCamera(unitBox())
	x, y := project(c, s3.Vec3{})
	if math.Abs(float64(x-testW/2)) > 1 || math.Abs(float64(y-testH/2)) > 1 {
		t.Fatalf("the centre shows at %v, %v", x, y)
	}
	for _, p := range []s3.Vec3{{-1, -1, -1}, {1, 1, 1}, {1, -1, 1}, {-1, 1, -1}} {
		if x, y := project(c, p); x < 0 || y < 0 || x > testW || y > testH {
			t.Fatalf("corner %v shows at %v, %v", p, x, y)
		}
	}
	dir := c.Ray(testW/2, testH/2, testW, testH)
	if dir.Sub(c.Ray(testW/2, testH/2, testW, testH)).Length() != 0 || dir.Dot(s3.Vec3{}.Sub(c.Pos).Norm()) < .999 {
		t.Fatalf("the middle of the view looks along %v", dir)
	}
}

// A ray through a pixel falls back on that pixel when projected.
func TestRayMatchesProjection(t *testing.T) {
	c := NewCamera(unitBox())
	for _, at := range [][2]float32{{100, 80}, {1100, 650}, {640, 20}} {
		x, y := project(c, c.Pos.Add(c.Ray(at[0], at[1], testW, testH).Scale(5)))
		if math.Abs(float64(x-at[0])) > .5 || math.Abs(float64(y-at[1])) > .5 {
			t.Fatalf("pixel %v came back at %v, %v", at, x, y)
		}
	}
}

// Turning keeps the point under the cursor at rest in the view's centre of rotation: its distance stays the same.
func TestTurnGoesAroundThePointUnderTheCursor(t *testing.T) {
	pivot := s3.Vec3{.4, .2, 0}
	c := NewCamera(unitBox())
	x, y := project(c, pivot)
	d0 := pivot.Sub(c.Pos).Length()
	c.Pick = func(o, d s3.Vec3) (float32, bool) { return pivot.Sub(o).Dot(d), true }
	c.Grab(x, y, testW, testH, true)
	for range 40 {
		c.Turn(9, 3, testH)
		c.Advance(1.0 / 60)
	}
	c.Release()
	settle(c)
	if d := pivot.Sub(c.Pos).Length(); math.Abs(float64(d-d0)) > float64(d0)*1e-3 {
		t.Fatalf("distance %v became %v", d0, d)
	}
	if nx, ny := project(c, pivot); math.Abs(float64(nx-x)) > 2 || math.Abs(float64(ny-y)) > 2 {
		t.Fatalf("the point moved from %v,%v to %v,%v", x, y, nx, ny)
	}
}

func TestTurnOnTheSpotKeepsTheEye(t *testing.T) {
	c := NewCamera(unitBox())
	pos := c.Pos
	c.Grab(100, 100, testW, testH, false)
	c.Turn(200, 0, testH)
	c.Release()
	settle(c)
	if c.Pos.Sub(pos).Length() > 1e-4 {
		t.Fatalf("the eye moved from %v to %v", pos, c.Pos)
	}
}

func TestPitchStopsShortOfTheTop(t *testing.T) {
	c := NewCamera(unitBox())
	c.Grab(0, 0, testW, testH, false)
	c.Turn(0, -1e5, testH)
	c.Release()
	settle(c)
	if float64(c.Pitch) > maxPitch+1e-3 {
		t.Fatalf("pitch %v", c.Pitch)
	}
}

// A drag of the pan moves what is at the viewing distance by as many pixels.
func TestPanFollowsThePointer(t *testing.T) {
	c := NewCamera(unitBox())
	x0, y0 := project(c, s3.Vec3{})
	c.Pan(120, -60, testH)
	settle(c)
	x, y := project(c, s3.Vec3{})
	if math.Abs(float64(x-x0-120)) > 1 || math.Abs(float64(y-y0+60)) > 1 {
		t.Fatalf("moved by %v, %v", x-x0, y-y0)
	}
}

// The point under the cursor stays there as the wheel zooms.
func TestZoomGoesToTheCursor(t *testing.T) {
	target := s3.Vec3{.5, .3, .2}
	c := NewCamera(unitBox())
	c.Pick = func(o, d s3.Vec3) (float32, bool) { return target.Sub(o).Dot(d), true }
	x, y := project(c, target)
	d0 := target.Sub(c.Pos).Length()
	c.Zoom(1, x, y, testW, testH)
	settle(c)
	nx, ny := project(c, target)
	if math.Abs(float64(nx-x)) > 1 || math.Abs(float64(ny-y)) > 1 {
		t.Fatalf("the point moved from %v,%v to %v,%v", x, y, nx, ny)
	}
	want := d0 * float32(math.Exp2(wheelStep))
	if d := target.Sub(c.Pos).Length(); math.Abs(float64(d-want)) > float64(want)*.01 {
		t.Fatalf("distance %v, want %v", d, want)
	}
	for range 200 {
		c.Zoom(1, x, y, testW, testH)
		settle(c)
	}
	if d := target.Sub(c.Pos).Length(); d < c.nearest*.9 {
		t.Fatalf("zoomed in to %v, nearest %v", d, c.nearest)
	}
}

// However close the camera has come, moving and zooming go on at a speed that gets it somewhere.
func TestMovingNeverRunsOutCloseToASurface(t *testing.T) {
	box := s3.Box{Min: s3.Vec3{-1, -1, -1}, Max: s3.Vec3{1, 1, 1}}
	c := NewCamera(box)
	plane := func(o, d s3.Vec3) (float32, bool) {
		if d[2] >= 0 || o[2] <= 1 {
			return 0, false
		}
		return (o[2] - 1) / -d[2], true
	}
	c.Yaw, c.Pitch, c.Pos, c.Pick = 0, 0, s3.Vec3{0, 0, 6}, plane
	for range 200 {
		c.Zoom(1, testH/2, testH/2, testH, testH)
		settle(c)
	}
	if c.Pos[2] > 1 {
		t.Fatalf("two hundred notches in stopped at %v", c.Pos)
	}
	before := c.Pos
	c.Pan(100, 0, testH)
	settle(c)
	if moved := c.Pos.Sub(before).Length(); moved < 1e-3 {
		t.Fatalf("a pan moved %v with the distance at %v", moved, c.Distance)
	}
}

func TestDistanceFollowsTheScene(t *testing.T) {
	c := NewCamera(s3.Box{Min: s3.Vec3{-1, -1, -1}, Max: s3.Vec3{1, 1, 1}})
	c.Pos = s3.Vec3{0, 0, 500}
	c.Pan(1, 0, testH)
	settle(c)
	if c.Distance < 100 {
		t.Fatalf("far from the scene the distance is %v", c.Distance)
	}
}

func TestKeysMoveAlongTheGround(t *testing.T) {
	c := NewCamera(unitBox())
	pos := c.Pos
	c.Move(0, 1, .5, false)
	settle(c)
	d := c.Pos.Sub(pos)
	_, _, ahead := basis(c.Yaw, c.Pitch)
	if d[1] != 0 || d.Dot(ahead) <= 0 {
		t.Fatalf("forward went %v, looking along %v", d, ahead)
	}
	slow := d.Length()
	c = NewCamera(unitBox()) // the distance depends on where it is
	pos = c.Pos
	c.Move(1, 1, .5, true)
	settle(c)
	if fast := c.Pos.Sub(pos).Length(); math.Abs(float64(fast-slow*fastKeys)) > float64(slow)*.01 {
		t.Fatalf("slow %v, fast %v", slow, fast)
	}
}
