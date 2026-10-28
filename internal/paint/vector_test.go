package paint

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"math"
	"testing"
)

var (
	solidRed  = VectorPaint{Kind: PaintSolid, Color: color.NRGBA{R: 255, A: 255}}
	solidBlue = VectorPaint{Kind: PaintSolid, Color: color.NRGBA{B: 255, A: 255}}
)

// rendered draws an object at full size over r.
func rendered(v Vector, r image.Rectangle) *image.RGBA {
	img := image.NewRGBA(r)
	v.Render(img, r, 1)
	return img
}

// coveredArea is how many pixels' worth of alpha an image holds.
func coveredArea(img *image.RGBA) float64 {
	sum := 0.0
	for i := 3; i < len(img.Pix); i += 4 {
		sum += float64(img.Pix[i]) / 255
	}
	return sum
}

func TestShapesFillExactly(t *testing.T) {
	r := image.Rect(0, 0, 200, 200)
	rect := Vector{Kind: VectorRect, Size: Pt{100, 50}, M: Translate(10.5, 20), Fill: solidRed}
	img := rendered(rect, r)
	if c := img.RGBAAt(50, 40); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("inside the rectangle %v", c)
	}
	if c := img.RGBAAt(10, 40); c.A < 120 || c.A > 135 { // half a pixel across its left edge
		t.Errorf("the rectangle's soft edge %v", c)
	}
	if a := coveredArea(img); math.Abs(a-5000) > 3 {
		t.Errorf("the rectangle covers %.1f pixels, want 5000", a)
	}
	ell := Vector{Kind: VectorEllipse, Size: Pt{100, 60}, M: Translate(20, 20), Fill: solidRed}
	if a := coveredArea(rendered(ell, r)); math.Abs(a-math.Pi*50*30)/(math.Pi*50*30) > 0.005 {
		t.Errorf("the ellipse covers %.1f pixels, want %.1f", a, math.Pi*50*30)
	}
	// Rounded corners take a corner away: (4−π)r² for the four.
	round := Vector{Kind: VectorRect, Size: Pt{100, 100}, Radius: 20, M: Translate(20, 20), Fill: solidRed}
	if a, want := coveredArea(rendered(round, r)), 10000-(4-math.Pi)*400; math.Abs(a-want) > 15 {
		t.Errorf("rounded rectangle %.1f, want %.1f", a, want)
	}
	star := Vector{Kind: VectorStar, Size: Pt{100, 100}, Sides: 5, Inner: 0.4, M: Translate(20, 20), Fill: solidRed}
	if img := rendered(star, r); img.RGBAAt(70, 70).A != 255 || img.RGBAAt(22, 22).A != 0 {
		t.Error("the star's middle or corner")
	}
}

func TestFillRules(t *testing.T) {
	// Two squares, one inside the other, wound the same way: non-zero fills the inner, even-odd leaves a hole.
	outer := polyline(true, Pt{0, 0}, Pt{100, 0}, Pt{100, 100}, Pt{0, 100})
	inner := polyline(true, Pt{30, 30}, Pt{70, 30}, Pt{70, 70}, Pt{30, 70})
	v := Vector{Kind: VectorCurve, Path: []Subpath{outer, inner}, M: Identity, Fill: solidRed}
	if c := rendered(v, image.Rect(0, 0, 100, 100)).RGBAAt(50, 50); c.A != 255 {
		t.Errorf("non-zero %v", c)
	}
	v.Rule = EvenOdd
	if c := rendered(v, image.Rect(0, 0, 100, 100)).RGBAAt(50, 50); c.A != 0 {
		t.Errorf("even-odd %v", c)
	}
}

func TestStrokesAlignJoinCapAndDash(t *testing.T) {
	r := image.Rect(0, 0, 200, 200)
	square := Vector{Kind: VectorRect, Size: Pt{100, 100}, M: Translate(50, 50),
		Stroke: VectorStroke{Paint: solidBlue, Width: 10}}
	// Centred: 5 each side of the edge, the outer corners filled by the miter join.
	img := rendered(square, r)
	if img.RGBAAt(46, 100).A != 255 || img.RGBAAt(54, 100).A != 255 || img.RGBAAt(56, 100).A != 0 || img.RGBAAt(44, 100).A != 0 {
		t.Errorf("centred stroke across the left edge: %d %d %d %d", img.RGBAAt(44, 100).A, img.RGBAAt(46, 100).A, img.RGBAAt(54, 100).A, img.RGBAAt(56, 100).A)
	}
	if img.RGBAAt(46, 46).A != 255 {
		t.Errorf("the mitred outer corner is open: %v", img.RGBAAt(46, 46))
	}
	square.Stroke.Join = JoinBevel
	if c := rendered(square, r).RGBAAt(46, 46); c.A == 255 {
		t.Errorf("a bevel left the corner square: %v", c)
	}
	square.Stroke.Join = JoinRound
	if c := rendered(square, r).RGBAAt(47, 47); c.A != 255 {
		t.Errorf("a round join missed %v", c)
	}
	// Inside and outside: the whole width on one side.
	square.Stroke.Join = JoinMiter
	square.Stroke.Align = AlignInside
	img = rendered(square, r)
	if img.RGBAAt(58, 100).A != 255 || img.RGBAAt(48, 100).A != 0 || img.RGBAAt(62, 100).A != 0 {
		t.Errorf("inside stroke %d %d %d", img.RGBAAt(48, 100).A, img.RGBAAt(58, 100).A, img.RGBAAt(62, 100).A)
	}
	square.Stroke.Align = AlignOutside
	img = rendered(square, r)
	if img.RGBAAt(42, 100).A != 255 || img.RGBAAt(52, 100).A != 0 || img.RGBAAt(38, 100).A != 0 {
		t.Errorf("outside stroke %d %d %d", img.RGBAAt(38, 100).A, img.RGBAAt(42, 100).A, img.RGBAAt(52, 100).A)
	}
	// An open line: butt ends stop at the points, square and round go half a width on.
	line := Vector{Kind: VectorCurve, Path: []Subpath{polyline(false, Pt{50, 100}, Pt{150, 100})}, M: Identity,
		Stroke: VectorStroke{Paint: solidBlue, Width: 20}}
	if c := rendered(line, r).RGBAAt(45, 100); c.A != 0 {
		t.Errorf("a butt end goes on: %v", c)
	}
	line.Stroke.Cap = CapSquare
	if c := rendered(line, r).RGBAAt(45, 92); c.A != 255 {
		t.Errorf("a square end stops short: %v", c)
	}
	line.Stroke.Cap = CapRound
	if img := rendered(line, r); img.RGBAAt(45, 100).A != 255 || img.RGBAAt(42, 92).A == 255 {
		t.Errorf("a round end: %v %v", img.RGBAAt(45, 100), img.RGBAAt(42, 92))
	}
	// Dashes of a width (20) on and a width off along 100: on over 0–20, 40–60 and 80–100.
	line.Stroke.Cap = CapButt
	whole := coveredArea(rendered(line, r))
	line.Stroke.Dash = []float64{1, 1}
	img = rendered(line, r)
	if part := coveredArea(img); math.Abs(part/whole-0.6) > 0.02 {
		t.Errorf("dashes cover %.2f of the line", part/whole)
	}
	if img.RGBAAt(60, 100).A != 255 || img.RGBAAt(80, 100).A != 0 || img.RGBAAt(140, 100).A != 255 {
		t.Errorf("dashes at 60, 80, 140: %d %d %d", img.RGBAAt(60, 100).A, img.RGBAAt(80, 100).A, img.RGBAAt(140, 100).A)
	}
}

func TestGradientFill(t *testing.T) {
	v := Vector{Kind: VectorRect, Size: Pt{100, 10}, M: Identity, Fill: VectorPaint{Kind: PaintLinear, From: Pt{0, 0}, To: Pt{100, 0},
		Stops: []Stop{{0, color.NRGBA{R: 255, A: 255}}, {1, color.NRGBA{B: 255, A: 255}}}}}
	img := rendered(v, image.Rect(0, 0, 100, 10))
	if l, r := img.RGBAAt(1, 5), img.RGBAAt(98, 5); l.R < 245 || r.B < 245 {
		t.Errorf("gradient ends %v %v", l, r)
	}
	if m := img.RGBAAt(50, 5); !near(m.R, 127, 3) || !near(m.B, 128, 3) {
		t.Errorf("gradient middle %v", m)
	}
}

func TestVectorLayers(t *testing.T) {
	d := New("Layer 1")
	d.Paper = false
	n := d.AddVector(Vector{Kind: VectorRect, Size: Pt{40, 20}, M: Translate(10, 10), Fill: solidRed}, "Rectangle 1")
	if d.Active != n.ID || d.UndoName() != "Add shape" || n.IsGroup() {
		t.Fatalf("the vector layer: %+v (%q)", n, d.UndoName())
	}
	look := func(x, y int) color.RGBA { return d.Composite(image.Rect(x, y, x+1, y+1)).RGBAAt(x, y) }
	if look(20, 20) != (color.RGBA{255, 0, 0, 255}) || look(60, 20).A != 0 {
		t.Fatalf("composite %v %v", look(20, 20), look(60, 20))
	}
	// Pixel tools refuse a vector layer, as Photoshop's do a shape layer's.
	if err := d.Fill(red); !errors.Is(err, ErrVectorLayer) {
		t.Errorf("fill on a vector layer: %v", err)
	}
	if _, _, err := d.BeginStroke(DefaultBrush(), false, 5, 5); !errors.Is(err, ErrVectorLayer) {
		t.Errorf("a stroke on a vector layer: %v", err)
	}
	// Moving and transforming change only the object's map: exact, and undone in one step.
	if err := d.Translate(5, 0); err != nil {
		t.Fatal(err)
	}
	if look(52, 20) != (color.RGBA{255, 0, 0, 255}) || d.UndoName() != "Move" {
		t.Errorf("moved %v (%q)", look(52, 20), d.UndoName())
	}
	tr, err := d.BeginTransform()
	if err != nil {
		t.Fatal(err)
	}
	tr.Set(Scale(2, 2, 15, 10), true)
	tr.Apply()
	if v := d.Node(n.ID).Vector; v.M.A != 2 || look(90, 40) != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("scaled: %+v, %v", v.M, look(90, 40))
	}
	d.Undo()
	if look(90, 40).A != 0 {
		t.Error("undo of the transform")
	}
	// A canvas turn turns the object exactly.
	d.Frame = image.Rect(0, 0, 100, 100)
	d.RotateCanvas(2)
	if look(100-20, 100-20) != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("turned canvas %v", look(80, 80))
	}
	d.Undo()
	// Edited, saved and loaded, it is still a vector.
	v := *d.Node(n.ID).Vector
	v.Stroke = VectorStroke{Paint: solidBlue, Width: 4, Dash: []float64{2, 1}}
	d.SetVector(n.ID, v, "Stroke")
	var buf bytes.Buffer
	if err := d.Snapshot().Save(&buf); err != nil {
		t.Fatal(err)
	}
	e, err := Load(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if lv := e.Node(n.ID).Vector; lv == nil || lv.Stroke.Width != 4 || len(lv.Stroke.Dash) != 2 || lv.M != v.M {
		t.Errorf("loaded %+v", lv)
	}
	// Merging it down makes pixels that look the same.
	before := d.Composite(image.Rect(0, 0, 80, 60))
	if err := d.MergeDown(); err != nil {
		t.Fatal(err)
	}
	if after := d.Composite(image.Rect(0, 0, 80, 60)); !bytes.Equal(before.Pix, after.Pix) {
		t.Error("merging the vector down changed the picture")
	}
	if l := d.Node(d.Active); l.Vector != nil || l.Pixels == nil {
		t.Errorf("the merged layer %+v", l)
	}
	// Rasterize turns one into pixels on request.
	d.AddVector(Vector{Kind: VectorEllipse, Size: Pt{10, 10}, M: Identity, Fill: solidRed}, "Ellipse 1")
	if err := d.Rasterize(); err != nil || d.Node(d.Active).Pixels == nil || d.UndoName() != "Rasterize" {
		t.Errorf("rasterize: %v", err)
	}
}

func TestDamagedVectorsAreRefused(t *testing.T) {
	bad := []Vector{
		{Kind: 99},
		{Kind: VectorRect, Size: Pt{math.NaN(), 1}},
		{Kind: VectorPolygon, Sides: 1 << 20},
		{Kind: VectorCurve, Path: []Subpath{{Nodes: []PathNode{{P: Pt{math.Inf(1), 0}}}}}},
	}
	for i, v := range bad {
		if v.valid() {
			t.Errorf("damaged object %d is taken as valid", i)
		}
	}
}
