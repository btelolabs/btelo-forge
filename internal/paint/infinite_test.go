package paint

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"testing"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/math/f64"
)

func TestTheCanvasHasNoEdge(t *testing.T) {
	d := New("Layer 1")
	if !d.Frame.Empty() || !d.Paper || len(d.Layers()) != 1 {
		t.Fatalf("a new canvas: frame %v, paper %v, %d layers", d.Frame, d.Paper, len(d.Layers()))
	}
	if _, err := d.Picture(); err == nil {
		t.Error("an empty canvas without a frame was exported")
	}
	// Painting far out on both sides keeps every dab, in a few tiles each.
	b := Brush{Size: 10, Hardness: 1, Opacity: 1, Flow: 1, Spacing: 0.1, Round: 1, Color: red}
	for _, p := range [][2]float64{{-50_000, -20_000}, {80_000, 60_000}} {
		s, _, err := d.BeginStroke(b, false, p[0], p[1])
		if err != nil {
			t.Fatal(err)
		}
		s.End()
	}
	l := d.Node(d.Active)
	if l.Pixels.At(-50_000, -20_000) != (color.RGBA{255, 0, 0, 255}) || l.Pixels.At(80_000, 60_000) != (color.RGBA{255, 0, 0, 255}) {
		t.Fatal("a distant dab was lost")
	}
	if n := len(l.Pixels.p.tiles); n > 8 {
		t.Errorf("two dots took %d tiles", n)
	}
	// The export holds everything painted, on white paper.
	if b := d.Bounds(); b.Dx() < 130_000 || b.Dy() < 80_000 {
		t.Errorf("bounds %v", b)
	}
	// Undo keeps only what a step changed: a dot is a tile or so, not a canvas.
	steps, _ := d.History()
	if len(steps) != 2 {
		t.Fatalf("history %v", steps)
	}
	for _, e := range d.history.done {
		if e.bytes > 4*TileSize*TileSize*4 {
			t.Errorf("a dot's step keeps %d bytes", e.bytes)
		}
	}
	d.Undo()
	if l := d.Node(d.Active); l.Pixels.At(80_000, 60_000).A != 0 || l.Pixels.At(-50_000, -20_000).A != 255 {
		t.Error("undo of the second dot")
	}
	// A fill with nothing selected reaches what is on screen as well as what is painted.
	d.View = image.Rect(0, 0, 100, 80)
	d.AddLayer("Fill")
	if err := d.Fill(color.NRGBA{B: 255, A: 255}); err != nil {
		t.Fatal(err)
	}
	if c := d.Node(d.Active).Pixels.At(50, 40); c != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("fill on screen %v", c)
	}
	// Select All on a canvas without a frame selects everything; its inverse nothing.
	d.SelectAll()
	if d.Selection == nil || d.Selection.At(-1_000_000, 5) != 255 {
		t.Error("select all")
	}
	d.InvertSelection()
	if d.Selection != nil {
		t.Error("the inverse of everything selected something")
	}
}

func TestSaveAndLoadAnUnboundedCanvas(t *testing.T) {
	d := New("Layer 1")
	d.Select(image.Rect(-300, -300, -100, -200))
	d.Fill(color.NRGBA{G: 255, A: 255})
	d.InvertSelection() // everything but that strip: a mask that is 255 beyond its tiles
	d.AddLayer("Moved")
	d.Fill(color.NRGBA{R: 255, A: 128})
	d.Deselect()
	if err := d.Translate(1000, 7); err != nil { // moved by an offset that is not a whole tile
		t.Fatal(err)
	}
	d.InvertSelection()
	d.Frame = image.Rect(-10, -10, 10, 10)
	var buf bytes.Buffer
	if err := d.Snapshot().Save(&buf); err != nil {
		t.Fatal(err)
	}
	e, err := Load(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if e.Frame != d.Frame || !e.Paper {
		t.Errorf("frame %v paper %v", e.Frame, e.Paper)
	}
	for _, p := range []image.Point{{-200, -250}, {900, 7}, {5000, 5000}, {-250, -250}} {
		r := image.Rectangle{Min: p, Max: p.Add(image.Pt(1, 1))}
		if !bytes.Equal(e.Composite(r).Pix, d.Composite(r).Pix) {
			t.Errorf("the loaded picture differs at %v", p)
		}
	}
	if e.Selection == nil || e.Selection.Fill() != 255 || e.Selection.At(5, 5) != 255 {
		t.Error("the loaded selection")
	}
	// A damaged tile list is refused, not trusted.
	z, _ := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	var fd map[string]any
	for _, f := range z.File {
		if f.Name == "doc.json" {
			r, _ := f.Open()
			json.NewDecoder(r).Decode(&fd)
			r.Close()
		}
	}
	layers := fd["layers"].([]any)
	layers[0].(map[string]any)["pixels"].(map[string]any)["tiles"] = []any{[]any{0, 0}, []any{0, 0}}
	var bad bytes.Buffer
	w := zip.NewWriter(&bad)
	for _, f := range z.File {
		out, _ := w.Create(f.Name)
		if f.Name == "doc.json" {
			json.NewEncoder(out).Encode(fd)
			continue
		}
		r, _ := f.Open()
		var data bytes.Buffer
		data.ReadFrom(r)
		r.Close()
		out.Write(data.Bytes())
	}
	w.Close()
	if _, err := Load(bytes.NewReader(bad.Bytes()), int64(bad.Len())); err == nil {
		t.Error("a file with a damaged tile list loaded")
	}
}

func TestViewRenderingReadsLevels(t *testing.T) {
	d := New("Layer 1")
	d.Paper = false
	d.Select(image.Rect(0, 0, 1024, 1024))
	d.Fill(color.NRGBA{R: 255, A: 255})
	d.Deselect()
	// At a quarter size the view reads level 2: 256×256 pixels for the square.
	view := image.NewRGBA(image.Rect(0, 0, 256, 256))
	d.Render(view, view.Rect, 2)
	if view.RGBAAt(128, 128) != (color.RGBA{255, 0, 0, 255}) || view.RGBAAt(255, 255) != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("level 2: %v %v", view.RGBAAt(128, 128), view.RGBAAt(255, 255))
	}
	// Paper is white under it.
	d.Paper = true
	empty := image.NewRGBA(image.Rect(300, 300, 310, 310))
	d.Render(empty, empty.Rect, 2)
	if empty.RGBAAt(305, 305) != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("paper %v", empty.RGBAAt(305, 305))
	}
}

// A transform that only moves by whole pixels lands exactly where it should, whatever the source's corner; this is
// where x/image/draw's own shortcut goes wrong (see Resample).
func TestMovingByWholePixelsLandsExactly(t *testing.T) {
	src := image.NewRGBA(image.Rect(-50, -60, 250, 240))
	src.SetRGBA(10, 20, color.RGBA{255, 0, 0, 255})
	for _, k := range []interface {
		Transform(xdraw.Image, f64.Aff3, image.Image, image.Rectangle, xdraw.Op, *xdraw.Options)
	}{xdraw.NearestNeighbor, xdraw.ApproxBiLinear, xdraw.CatmullRom} {
		dst := image.NewRGBA(image.Rect(0, 0, 400, 400))
		Resample(k.(xdraw.Interpolator), dst, f64.Aff3{1, 0, 100, 0, 1, 100}, src, src.Rect, xdraw.Over)
		if dst.RGBAAt(110, 120) != (color.RGBA{255, 0, 0, 255}) {
			t.Errorf("%T: the pixel did not land at 110,120", k)
		}
	}
	// Free Transform that only moves a layer.
	d := newDoc(t, 100, 100, color.NRGBA{})
	d.Select(image.Rect(10, 30, 20, 40))
	d.Fill(red)
	d.Deselect()
	tr, err := d.BeginTransform()
	if err != nil {
		t.Fatal(err)
	}
	tr.Set(Translate(5, 7), true)
	tr.Apply()
	if c := d.Node(d.Active).Pixels.At(15, 37); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("moved by Free Transform: %v", c)
	}
}
