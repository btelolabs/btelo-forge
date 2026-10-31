package printing

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"math"
	"strings"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

func red() color.NRGBA { return color.NRGBA{255, 0, 0, 255} }

func at(img *image.RGBA, x, y int) color.RGBA { return img.RGBAAt(x, y) }

func TestMatrixOrderAndInverse(t *testing.T) {
	m := Translate(10, 20).Mul(Scale(2, 3))
	if x, y := m.Apply(1, 1); x != 12 || y != 23 {
		t.Fatalf("(1,1) went to %v,%v", x, y)
	}
	inv, ok := m.Inverse()
	if !ok {
		t.Fatal("no inverse")
	}
	if x, y := inv.Apply(12, 23); math.Abs(x-1) > 1e-9 || math.Abs(y-1) > 1e-9 {
		t.Fatalf("back to %v,%v", x, y)
	}
	if _, ok := (Matrix{}).Inverse(); ok {
		t.Fatal("a flat matrix has an inverse")
	}
}

func TestPageHairlines(t *testing.T) {
	p := &Page{W: 100, H: 100}
	p.Add(&Stroke{Path: RectPath(1, 1, 10, 10), Width: 0.01})
	if w := p.Ops[0].(*Stroke).Width; w != MinLine {
		t.Fatalf("a hairline is %v wide", w)
	}
	p.Add(&Fill{Path: RectPath(10, 10, 80, 0.02), Color: red()})
	b := p.Ops[1].(*Fill).Path.Bounds()
	if h := b.Y1 - b.Y0; math.Abs(h-MinLine) > 1e-9 {
		t.Fatalf("a thin bar is %v high", h)
	}
	if c := (b.Y0 + b.Y1) / 2; math.Abs(c-10.01) > 1e-9 {
		t.Fatalf("the bar moved to %v", c)
	}
}

func TestPlaceScalesLines(t *testing.T) {
	in := &Page{W: 100, H: 100}
	in.Add(&Stroke{Path: RectPath(0, 0, 10, 10), Width: 4, Dash: []float64{2, 2}})
	in.Add(&Unclip{})
	out := &Page{W: 200, H: 200}
	out.Place(in, Translate(50, 50).Mul(Scale(0.5, 0.5)))
	s := out.Ops[0].(*Stroke)
	if s.Width != 2 || s.Dash[0] != 1 {
		t.Fatalf("width %v, dash %v", s.Width, s.Dash)
	}
	if b := s.Path.Bounds(); b.X0 != 50 || b.X1 != 55 {
		t.Fatalf("box %+v", b)
	}
}

func TestRenderFillClipAndRules(t *testing.T) {
	p := &Page{W: 40, H: 40}
	ring := RectPath(5, 5, 30, 30)
	ring.Append(RectPath(15, 15, 10, 10))
	p.Add(&Fill{Path: ring, EvenOdd: true, Color: red()})
	img := Render(p, 1, image.Rectangle{})
	if c := at(img, 8, 8); c.G != 0 {
		t.Fatalf("the ring is %v", c)
	}
	if c := at(img, 20, 20); c.G != 255 {
		t.Fatalf("the even-odd hole is %v", c)
	}
	q := &Page{W: 40, H: 40}
	q.Add(&Clip{Path: EllipsePath(10, 10, 20, 20)})
	q.Add(&Fill{Path: RectPath(0, 0, 40, 40), Color: red()})
	q.Add(&Unclip{})
	q.Add(&Fill{Path: RectPath(0, 0, 4, 4), Color: color.NRGBA{0, 0, 255, 255}})
	img = Render(q, 1, image.Rectangle{})
	if c := at(img, 20, 20); c.G != 0 {
		t.Fatalf("inside the clip: %v", c)
	}
	if c := at(img, 11, 11); c.G != 255 {
		t.Fatalf("outside the clip: %v", c)
	}
	if c := at(img, 2, 2); c.B != 255 || c.R != 0 {
		t.Fatalf("after the clip: %v", c)
	}
}

func TestRenderStrokeDashAndShade(t *testing.T) {
	line := &Path{}
	line.MoveTo(0, 10)
	line.LineTo(100, 10)
	p := &Page{W: 100, H: 60}
	p.Add(&Stroke{Path: line, Width: 4, Dash: []float64{10, 10}, Color: color.NRGBA{A: 255}})
	p.Add(&Shade{Path: RectPath(0, 30, 100, 20), X1: 100, Stops: []Stop{{0, color.NRGBA{0, 0, 0, 255}}, {1, color.NRGBA{255, 255, 255, 255}}}, Extend: [2]bool{true, true}, M: Identity})
	img := Render(p, 1, image.Rectangle{})
	if c := at(img, 5, 10); c.R != 0 {
		t.Fatalf("a dash is %v", c)
	}
	if c := at(img, 15, 10); c.R != 255 {
		t.Fatalf("a gap is %v", c)
	}
	if c := at(img, 50, 40); c.R < 120 || c.R > 135 {
		t.Fatalf("the middle of the gradient is %v", c)
	}
}

func TestPicturePlacement(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	src.SetRGBA(0, 0, color.RGBA{255, 0, 0, 255})
	src.SetRGBA(1, 0, color.RGBA{0, 255, 0, 255})
	src.SetRGBA(0, 1, color.RGBA{0, 0, 255, 255})
	src.SetRGBA(1, 1, color.RGBA{255, 255, 0, 255})
	p := &Page{W: 20, H: 20}
	p.Add(&Picture{Img: src, M: Matrix{20, 0, 0, 20, 0, 0}})
	img := Render(p, 1, image.Rectangle{})
	if c := at(img, 2, 2); c.R < 240 || c.G > 15 {
		t.Fatalf("top left is %v", c)
	}
	if c := at(img, 2, 17); c.B < 240 {
		t.Fatalf("bottom left is %v", c)
	}
}

func TestFlattenKeepsVectorsAndMixesOverPlainColour(t *testing.T) {
	p := &Page{W: 100, H: 100}
	p.Add(&Fill{Path: RectPath(0, 0, 100, 100), Color: color.NRGBA{200, 200, 0, 255}})
	p.Add(&Fill{Path: EllipsePath(10, 10, 30, 30), Color: color.NRGBA{0, 0, 255, 128}})
	f := Flatten(p, 100)
	if f.Images() != 0 {
		t.Fatalf("%d pictures over one plain colour", f.Images())
	}
	c := f.Ops[1].(*Fill).Color
	if c.A != 255 || c.R < 95 || c.R > 105 || c.B < 124 || c.B > 132 {
		t.Fatalf("mixed to %v", c)
	}
}

func TestFlattenTakesPixelsOnlyWhereBlendedOverDetail(t *testing.T) {
	p := &Page{W: 100, H: 100}
	p.Add(&Fill{Path: EllipsePath(5, 5, 60, 60), Color: color.NRGBA{255, 0, 0, 255}})
	p.Add(&Fill{Path: RectPath(70, 70, 20, 20), Color: color.NRGBA{0, 255, 0, 255}})
	p.Add(&Fill{Path: RectPath(20, 20, 40, 40), Color: color.NRGBA{0, 0, 255, 128}})
	f := Flatten(p, 144)
	if f.Images() != 1 {
		t.Fatalf("%d pictures", f.Images())
	}
	n := 0
	for _, op := range f.Ops {
		if _, ok := op.(*Fill); ok {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("%d fills stayed vector, not 2", n)
	}
	a, b := Render(p, 2, image.Rectangle{}), Render(f, 2, image.Rectangle{})
	for _, q := range [][2]int{{80, 80}, {40, 40}, {160, 160}, {100, 100}} {
		ca, cb := at(a, q[0], q[1]), at(b, q[0], q[1])
		if d := int(ca.B) - int(cb.B); d < -3 || d > 3 || int(ca.R)-int(cb.R) > 3 {
			t.Fatalf("at %v: %v against %v", q, ca, cb)
		}
	}
}

func TestFlattenGradientIsBandsOfPlainColour(t *testing.T) {
	p := &Page{W: 100, H: 100}
	p.Add(&Shade{Path: EllipsePath(0, 0, 100, 100), X1: 100, Stops: []Stop{{0, color.NRGBA{0, 0, 0, 255}}, {1, color.NRGBA{255, 0, 0, 255}}}, Extend: [2]bool{true, true}, M: Identity})
	f := Flatten(p, 144)
	fills := 0
	for _, op := range f.Ops {
		switch op.(type) {
		case *Fill:
			fills++
		case *Shade, *Picture:
			t.Fatalf("left a %T", op)
		}
	}
	if fills < 100 {
		t.Fatalf("%d bands", fills)
	}
	a, b := Render(p, 1, image.Rectangle{}), Render(f, 1, image.Rectangle{})
	if ca, cb := at(a, 50, 50), at(b, 50, 50); int(ca.R)-int(cb.R) > 4 || int(cb.R)-int(ca.R) > 4 {
		t.Fatalf("%v against %v", ca, cb)
	}
}

func TestPlacement(t *testing.T) {
	a4 := Paper{W: 595, H: 842, Left: 18, Top: 18, Right: 18, Bottom: 18}
	// A page of the paper's size goes where it is, whatever the printer can reach.
	if x, y := Placement(595, 842, false, a4).Apply(0, 0); math.Abs(x) > 1e-6 || math.Abs(y) > 1e-6 {
		t.Fatalf("corner at %v,%v", x, y)
	}
	// A landscape page turns on portrait paper, and every corner stays on the sheet.
	m := Placement(842, 595, false, a4)
	for _, c := range [4][2]float64{{0, 0}, {842, 0}, {842, 595}, {0, 595}} {
		x, y := m.Apply(c[0], c[1])
		if x < -1e-6 || y < -1e-6 || x > 595+1e-6 || y > 842+1e-6 {
			t.Fatalf("corner %v at %v,%v", c, x, y)
		}
	}
	// A slide fills what the printer can reach, kept in proportion.
	m = Placement(960, 540, true, a4)
	if k := m.Size(); math.Abs(k-(842-36)/960.0) > 1e-6 {
		t.Fatalf("scale %v", k)
	}
	// Too large is scaled down, into what can be marked.
	m = Placement(2000, 3000, false, a4)
	x0, y0 := m.Apply(0, 0)
	x1, y1 := m.Apply(2000, 3000)
	if min(x0, x1) < 18-1e-6 || max(x0, x1) > 577+1e-6 || min(y0, y1) < 18-1e-6 || max(y0, y1) > 824+1e-6 {
		t.Fatalf("at %v,%v to %v,%v", x0, y0, x1, y1)
	}
}

func font(t *testing.T) *gfx.Font {
	t.Helper()
	f, err := gfx.LoadSystemFonts()
	if err != nil {
		t.Fatal(err)
	}
	return f.Regular
}

func TestRecordingIsShapes(t *testing.T) {
	pg := &Page{W: 200, H: 100}
	c := Canvas(pg)
	if c.Recorder() == nil {
		t.Fatal("not recording")
	}
	c.Fill(gfx.Rect{X: 10, Y: 20, W: 100, H: 50}, gfx.Hex(0xff0000))
	c.Push(gfx.Rect{X: 0, Y: 0, W: 500, H: 500})
	c.RoundRect(gfx.Rect{X: 100, Y: 100, W: 200, H: 200}, 30, gfx.Hex(0x00ff00))
	c.Outline(gfx.Rect{X: 100, Y: 100, W: 200, H: 200}, 10, gfx.Hex(0x0000ff))
	c.Line(0, 0, 100, 100, 10, gfx.Hex(0))
	c.Triangle(0, 0, 100, 0, 50, 100, gfx.Hex(0))
	c.Pie(500, 500, []gfx.Wedge{{From: 0, To: 1, Radius: 200, Col: gfx.Hex(0)}}, 4)
	c.Pop()
	end := c.Text(font(t).Face(120), 100, 300, "Ab 中", gfx.Hex(0x101010))
	if len(c.Img.Pix) != 0 {
		t.Fatal("pixels were made")
	}
	var fills, strokes, texts, clips int
	for _, op := range pg.Ops {
		switch o := op.(type) {
		case *Fill:
			fills++
		case *Stroke:
			strokes++
		case *Clip:
			clips++
		case *Text:
			texts++
			if len(o.Glyphs) != 4 || o.Glyphs[3].Text != "中" {
				t.Fatalf("glyphs %+v", o.Glyphs)
			}
			if o.Glyphs[0].Outline.Path.Empty() || !o.Glyphs[2].Outline.Path.Empty() {
				t.Fatal("an A has a shape, and a space has none")
			}
			if x, y := o.M.Apply(0, 0); math.Abs(x-10) > 0.01 || y < 30 || y > 45 {
				t.Fatalf("baseline at %v,%v", x, y)
			}
		}
	}
	if fills != 4 || strokes != 2 || clips != 1 || texts != 1 {
		t.Fatalf("%d fills, %d strokes, %d clips, %d texts", fills, strokes, clips, texts)
	}
	if b := pg.Ops[0].(*Fill).Path.Bounds(); b.X0 != 1 || b.Y1 != 7 {
		t.Fatalf("mapped to %+v", b)
	}
	if want := font(t).Face(120).Measure("Ab 中") + 100; math.Abs(float64(end-want)) > 0.5 {
		t.Fatalf("the pen ended at %v, not %v", end, want)
	}
}

// A real page through the recorder and the writer, read back with this repository's own PDF reader's tests kept apart:
// only the file's structure is checked here. The independent readers are in tools/officecheck.
func TestPDFWriter(t *testing.T) {
	pg := &Page{W: 300, H: 200}
	pg.Add(&Fill{Path: RectPath(10, 10, 100, 50), Color: color.NRGBA{255, 0, 0, 128}})
	pg.Add(&Stroke{Path: EllipsePath(10, 10, 100, 50), Width: 2, Dash: []float64{3, 2}, Color: color.NRGBA{A: 255}})
	pg.Add(&Clip{Path: RectPath(0, 0, 300, 100)})
	pg.Add(&Shade{Path: RectPath(120, 10, 100, 50), X1: 100, Stops: []Stop{{0, red()}, {0.5, color.NRGBA{0, 255, 0, 255}}, {1, color.NRGBA{0, 0, 255, 255}}}, Extend: [2]bool{true, true}, M: Translate(120, 0)})
	pg.Add(&Shade{Path: RectPath(120, 70, 50, 50), Radial: true, X0: 145, Y0: 95, R1: 25, X1: 145, Y1: 95, Stops: []Stop{{0, red()}, {1, color.NRGBA{A: 255}}}, Extend: [2]bool{true, true}, M: Identity})
	pg.Add(&Unclip{})
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.SetRGBA(0, 0, color.RGBA{128, 0, 0, 128})
	pg.Add(&Picture{Img: img, M: Matrix{40, 0, 0, 40, 230, 10}})
	pg.Add(&Picture{Img: img, M: Matrix{40, 0, 0, 40, 230, 60}})
	c := Canvas(pg)
	c.Text(font(t).Face(120), 100, 1300, "Hello 世界", gfx.Hex(0))
	var b bytes.Buffer
	if err := WritePDF(context.Background(), &b, &Doc{Title: "测试", Pages: []*Page{pg, {W: 595, H: 842}}}, nil); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	for _, want := range []string{"%PDF-1.7", "/Type /Catalog", "/Count 2", "/Subtype /Type3", "/ToUnicode", "/ShadingType 2", "/ShadingType 3", "/SMask", "/FunctionType 3", "/A128", "startxref", "/Title <FEFF"} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in the file", want)
		}
	}
	// One picture, however many times it is shown, and its mask.
	if n := strings.Count(s, "/Subtype /Image"); n != 2 {
		t.Errorf("%d images", n)
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if err := WritePDF(cancelled, &bytes.Buffer{}, &Doc{Pages: []*Page{pg}}, nil); err == nil {
		t.Error("a cancelled export went on")
	}
}
