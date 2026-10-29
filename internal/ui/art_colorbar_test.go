package ui

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// stopsBar is where the vector gradient's options put the stops, as drawVectorGradientOptions lays them out.
func (x *harness) stopsBar() gfx.Rect {
	u := &x.a.u
	r := x.barArea()
	bx := r.X + u.buttonWidth("", u.t("Fill")) + u.px(4) + u.buttonWidth("", u.t("Stroke")) + u.px(4) + u.px(10) +
		float32(len(paintKinds))*u.px(32) + u.px(10)
	return gfx.Rect{X: bx, Y: r.Y + u.px(3), W: u.px(220), H: r.H - u.px(14)}
}

// The options bar's stops: a click adds one, a drag moves it, a right-click removes it; the colour panel colours
// the chosen one, its opacity too.
func TestGradientStopsInTheOptionsBar(t *testing.T) {
	x, ad := canvasHarness(t)
	a, d := x.a, ad.d
	n := d.AddVector(solidSquare(50, 50, 200, red), "Shape")
	x.key('G', false)
	a.setPaintKind(ad, d.Node(n.ID), paint.PaintLinear)
	x.frame(Input{})
	bar := x.stopsBar()
	stops := func() []paint.Stop { return d.Node(n.ID).Vector.Fill.Stops }
	if len(stops()) != 2 {
		t.Fatalf("stops %v", stops())
	}
	// A click at a quarter adds a stop there.
	x.click(gfx.Rect{X: bar.X + bar.W/4 - 1, Y: bar.Y, W: 2, H: bar.H})
	if s := stops(); len(s) != 3 || math.Abs(s[1].At-0.25) > 0.02 || ad.gradStop != 1 {
		t.Fatalf("after a click on the bar: %+v, chosen %d", s, ad.gradStop)
	}
	// The colour panel colours it, half transparent.
	a.setPickerColor(color.NRGBA{B: 255, A: 128})
	x.frame(Input{})
	if c := stops()[1].Color; c != (color.NRGBA{B: 255, A: 128}) {
		t.Errorf("the chosen stop is %v", c)
	}
	// Its marker drags it to three quarters.
	mx, my := bar.X+bar.W/4, bar.Bottom()+a.u.px(6)
	x.drag(mx, my, bar.X+bar.W*3/4, my)
	if s := stops(); len(s) != 3 || math.Abs(s[1].At-0.75) > 0.02 || s[1].Color.B != 255 {
		t.Fatalf("after dragging the stop: %+v", s)
	}
	x.frame(Input{})
	x.shot("canvas-gradient-stops")
	// A right-click removes it.
	rx := bar.X + bar.W*float32(stops()[1].At)
	x.frame(Input{Pointer: []Pointer{{X: rx, Y: my, Right: true, Down: true}}})
	if s := stops(); len(s) != 2 {
		t.Errorf("after the right-click: %+v", s)
	}
}

// The colour panel's opacity colours the brush's colour, the background's, and a vector's fill.
func TestColourOpacity(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	a.pickBackground = false
	a.setPickerColor(color.NRGBA{R: 255, A: 100})
	if a.brush.Color.A != 100 || a.settings.Paint.Color != "#ff000064" {
		t.Fatalf("the foreground %v, kept as %q", a.brush.Color, a.settings.Paint.Color)
	}
	if b := a.settings.Paint.brush(); b.Color.A != 100 {
		t.Errorf("read back %v", b.Color)
	}
	// The brush paints with it: half as strong, about.
	x.key('B', false)
	a.brush.Size, a.brush.Hardness, a.brush.Opacity = 30, 1, 1
	fx, fy := x.viewPoint(ad, 200, 150)
	x.drag(fx, fy, fx+1, fy)
	if c := ad.look(200, 150); c.G < 140 || c.G > 175 || c.R != 255 {
		t.Errorf("a 40%% red on white shows %v", c)
	}
	if c, err := parseNRGBA("#11223380"); err != nil || c != (color.NRGBA{0x11, 0x22, 0x33, 0x80}) {
		t.Errorf("parsing: %v, %v", c, err)
	}
	if hexColor(color.NRGBA{1, 2, 3, 255}) != "#010203" {
		t.Error("an opaque colour is written #rrggbb")
	}
}

// The colour panel's chosen colour is drawn in front: the background's when it is chosen.
func TestChosenSwatchInFront(t *testing.T) {
	x, _ := canvasHarness(t)
	a := x.a
	a.brush.Color = color.NRGBA{R: 255, A: 255}
	a.settings.Paint.Background = "#0000ff"
	picker, _ := a.sideLayout(a.artLayout(a.layout().body).side)
	tools := a.colourSwatchArea(picker).Pixels()
	// The span of the pixels of a colour in the tool box.
	span := func(c color.RGBA) image.Rectangle {
		var r image.Rectangle
		for y := tools.Min.Y; y < tools.Max.Y; y++ {
			for vx := tools.Min.X; vx < tools.Max.X; vx++ {
				if x.c.Img.RGBAAt(vx, y) == c {
					r = r.Union(image.Rect(vx, y, vx+1, y+1))
				}
			}
		}
		return r
	}
	red, blue := color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}
	a.pickBackground = false
	x.frame(Input{})
	fg := span(red)
	a.pickBackground = true
	x.frame(Input{})
	bg := span(blue)
	both := fg.Intersect(bg)
	if both.Empty() {
		t.Fatalf("the swatches %v and %v do not overlap", fg, bg)
	}
	mid := image.Pt((both.Min.X+both.Max.X)/2, (both.Min.Y+both.Max.Y)/2)
	if c := x.c.Img.RGBAAt(mid.X, mid.Y); c != blue {
		t.Errorf("with the background chosen the overlap shows %v", c)
	}
	a.pickBackground = false
	x.frame(Input{})
	if c := x.c.Img.RGBAAt(mid.X, mid.Y); c != red {
		t.Errorf("with the foreground chosen the overlap shows %v", c)
	}
}

// Swapping the colours leaves each where it is shown and brings the other to the front: the swatches trade places in
// depth, and D puts black back in front at the top left.
func TestSwappedSwatchesTradePlaces(t *testing.T) {
	x, _ := canvasHarness(t)
	a := x.a
	a.pickBackground = false
	a.brush.Color = color.NRGBA{R: 255, A: 255}
	a.settings.Paint.Background = "#0000ff"
	picker, _ := a.sideLayout(a.artLayout(a.layout().body).side)
	tools := a.colourSwatchArea(picker).Pixels()
	at := func(dx, dy int) color.RGBA { return x.c.Img.RGBAAt(tools.Min.X+dx, tools.Min.Y+dy) }
	px := func(v float32) int { return int(a.u.px(v)) }
	topLeft, bottomRight, overlap := [2]int{px(5), px(5)}, [2]int{px(38), px(35)}, [2]int{px(22), px(20)}
	red, blue := color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}
	x.frame(Input{})
	if at(topLeft[0], topLeft[1]) != red || at(bottomRight[0], bottomRight[1]) != blue || at(overlap[0], overlap[1]) != red {
		t.Fatalf("before the swap: %v, %v, the overlap %v", at(topLeft[0], topLeft[1]), at(bottomRight[0], bottomRight[1]), at(overlap[0], overlap[1]))
	}
	x.key('X', false)
	x.frame(Input{})
	if a.brush.Color != (color.NRGBA{B: 255, A: 255}) || !a.settings.Paint.ColoursSwapped {
		t.Fatalf("X left the foreground %v", a.brush.Color)
	}
	if at(topLeft[0], topLeft[1]) != red || at(bottomRight[0], bottomRight[1]) != blue || at(overlap[0], overlap[1]) != blue {
		t.Errorf("after the swap: %v, %v, the overlap %v", at(topLeft[0], topLeft[1]), at(bottomRight[0], bottomRight[1]), at(overlap[0], overlap[1]))
	}
	x.key('D', false)
	x.frame(Input{})
	if a.settings.Paint.ColoursSwapped || at(topLeft[0], topLeft[1]) != (color.RGBA{A: 255}) {
		t.Errorf("D left the top left %v", at(topLeft[0], topLeft[1]))
	}
}
