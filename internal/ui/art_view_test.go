package ui

import (
	"bytes"
	"image"
	"image/color"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/paint"
)

// sameAsWhole fails when the view, drawn piece by piece as the picture changed, differs from the view drawn whole,
// but where it is still to be drawn.
func (x *harness) sameAsWhole(ad *artDoc, step int) {
	x.t.Helper()
	l := x.a.artLayout(x.a.layout().body)
	rp := l.view.Pixels()
	ox, oy := ad.origin(l.view)
	ox, oy = ox-float64(rp.Min.X), oy-float64(rp.Min.Y)
	drawn := ad.view
	ad.view = image.NewRGBA(drawn.Rect)
	x.a.renderView(ad, ad.view.Rect, ox, oy)
	whole := ad.view
	ad.view = drawn
	st := ad.stale
	pending := image.Rect(int(ox+float64(st.Min.X)*ad.zoom)-2, int(oy+float64(st.Min.Y)*ad.zoom)-2,
		int(ox+float64(st.Max.X)*ad.zoom)+2, int(oy+float64(st.Max.Y)*ad.zoom)+2)
	for y := range whole.Rect.Dy() {
		for vx := range whole.Rect.Dx() {
			i := whole.PixOffset(vx, y)
			if !bytes.Equal(drawn.Pix[i:i+4], whole.Pix[i:i+4]) && !(image.Point{vx, y}).In(pending) {
				x.t.Fatalf("step %d: at %d,%d the view shows %v, drawn whole %v", step, vx, y, drawn.Pix[i:i+4], whole.Pix[i:i+4])
			}
		}
	}
}

// Painting across artboards, the view drawn as the stroke goes is the view drawn whole: the pasteboard's shade
// stays beside the boards (it once spilled into a board from a piece drawn beside another).
func TestPaintingAcrossBoardsDrawsTrue(t *testing.T) {
	x, ad := canvasHarness(t)
	d := ad.d
	d.Frame = image.Rectangle{}
	d.AddBoard(image.Rect(0, 0, 300, 250), "B1")
	d.AddLayer("Ink")
	d.AddBoard(image.Rect(330, 0, 600, 250), "B2")
	d.Choose(d.Node(d.Boards()[0].ID).Children[0].ID)
	ad.zoom, ad.panX, ad.panY, ad.fit = 1, 300, 125, false
	ad.changedAll()
	x.frame(Input{})
	x.key('B', false)
	x.a.brush = paint.Brush{Size: 30, Hardness: 1, Opacity: 1, Spacing: 0.1, Flow: 1, Round: 1, Color: color.NRGBA{R: 160, G: 100, B: 100, A: 255}}
	fx, fy := x.viewPoint(ad, 100, 100)
	tx, ty := x.viewPoint(ad, 500, 150)
	x.mx, x.my = fx, fy
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: fx, Y: fy, Down: true}}})
	for i := 1; i <= 8; i++ {
		f := float32(i) / 8
		x.mx, x.my = fx+(tx-fx)*f, fy+(ty-fy)*f
		x.frame(Input{Down: true})
		x.sameAsWhole(ad, i)
	}
	x.frame(Input{Pointer: []Pointer{{X: tx, Y: ty}}})
	x.frame(Input{})
	x.sameAsWhole(ad, 9)
}

// The shade beyond the pages covers only the area given, whatever pages lie beside it.
func TestShadeOutsideStaysInItsArea(t *testing.T) {
	view := image.NewRGBA(image.Rect(0, 0, 100, 10))
	for i := range view.Pix {
		view.Pix[i] = 255
	}
	shadeOutside(view, image.Rect(10, 0, 20, 10), []image.Rectangle{image.Rect(0, 0, 50, 10), image.Rect(60, 0, 90, 10)})
	for x := range 100 {
		if c := view.RGBAAt(x, 5); c != (color.RGBA{255, 255, 255, 255}) {
			t.Fatalf("at %d the view was shaded: %v", x, c)
		}
	}
}

// The marching ants are a whole line at any zoom: zoomed out, every column across an elliptical selection has its
// top and its bottom edge (drawn canvas pixel by canvas pixel, the slanted parts once fell between screen pixels).
func TestMarchingAntsAreWholeAtAnyZoom(t *testing.T) {
	x, ad := canvasHarness(t)
	d := ad.d
	d.SelectShape(d.EllipseMask(image.Rect(40, 30, 360, 270)), paint.SelectNew)
	for _, z := range []float64{0.48, 1, 3.3} {
		ad.zoom, ad.panX, ad.panY, ad.fit = z, 200, 150, false
		x.frame(Input{})
		ants := ad.ants
		if ants.img == nil {
			t.Fatalf("zoom %v: no ants", z)
		}
		l := x.a.artLayout(x.a.layout().body)
		sx, _ := ad.origin(l.view)
		// Columns well inside the ellipse's width, on the screen.
		x0, x1 := int(sx+60*z), int(sx+340*z)
		for vx := x0; vx < x1; vx++ {
			edges := 0
			for y := ants.img.Rect.Min.Y; y < ants.img.Rect.Max.Y; y++ {
				if ants.img.RGBAAt(vx-ants.at.X, y).A != 0 {
					edges++
				}
			}
			if edges < 2 {
				t.Fatalf("zoom %v: column %d crosses %d edge pixels", z, vx, edges)
			}
		}
		x.shot("canvas-ants")
	}
}

// Along an edge running any way, the ants are black and white by turns: a 45° edge too, however the view lies on the
// window's pixels.
func TestMarchingAntsAlternateOnEveryEdge(t *testing.T) {
	x, ad := canvasHarness(t)
	d := ad.d
	for _, edge := range []struct {
		name         string
		shape        [][2]float64
		fromX, fromY int
		stepX, stepY int
	}{
		{"rising", [][2]float64{{50, 250}, {250, 50}, {350, 250}}, 50, 250, 1, -1},
		{"falling", [][2]float64{{50, 50}, {250, 250}, {50, 250}}, 50, 50, 1, 1},
		{"level", [][2]float64{{50, 50}, {250, 50}, {250, 250}, {50, 250}}, 50, 50, 1, 0},
		{"upright", [][2]float64{{50, 50}, {250, 50}, {250, 250}, {50, 250}}, 50, 50, 0, 1},
	} {
		d.SelectShape(d.PolygonMask(edge.shape), paint.SelectNew)
		for pan := range 40 { // more than a whole turn of the pattern, at any dash
			ad.zoom, ad.panX, ad.panY, ad.fit = 1, 200+float64(pan), 150, false
			x.frame(Input{})
			ants := ad.ants
			l := x.a.artLayout(x.a.layout().body)
			sx, sy := ad.origin(l.view) // on the window, where the ants lie
			// Along the edge, a little from its ends: count the colour changes.
			changes, last := 0, color.RGBA{}
			for k := 10; k < 190; k++ {
				vx, vy := int(sx)+edge.fromX+k*edge.stepX, int(sy)+edge.fromY+k*edge.stepY
				for o := -3; o <= 3; o++ { // the edge pixel nearest
					px, py := vx+o, vy
					if edge.stepY == 0 {
						px, py = vx, vy+o
					}
					c := ants.img.RGBAAt(px-ants.at.X, py-ants.at.Y)
					if c.A != 0 {
						if c != last && last.A != 0 {
							changes++
						}
						last = c
						break
					}
				}
			}
			if changes < 10 {
				t.Errorf("%s edge, panned by %d: the ants change colour %d times", edge.name, pan, changes)
			}
		}
	}
}
