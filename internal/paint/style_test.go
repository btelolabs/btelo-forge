package paint

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"reflect"
	"testing"
)

// styledDoc is a white canvas with a red 40×40 square at (30, 30) on its own layer.
func styledDoc(t *testing.T) (*Doc, *Node) {
	t.Helper()
	d, err := NewFramed(100, 100, "Background", color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	if err != nil {
		t.Fatal(err)
	}
	n := d.AddLayer("Square")
	d.Select(image.Rect(30, 30, 70, 70))
	d.Fill(color.NRGBA{R: 255, A: 255})
	d.Deselect()
	return d, n
}

func TestEdtIsExact(t *testing.T) {
	const w, h = 9, 7
	d := make([]float64, w*h)
	for i := range d {
		d[i] = math.Inf(1)
	}
	d[3*w+4] = 0
	edt2(d, w, h)
	for y := range h {
		for x := range w {
			want := float64((x-4)*(x-4) + (y-3)*(y-3))
			if d[y*w+x] != want {
				t.Fatalf("(%d, %d): %v, want %v", x, y, d[y*w+x], want)
			}
		}
	}
}

func TestLayerStyles(t *testing.T) {
	d, n := styledDoc(t)
	at := func(x, y int) color.RGBA { return d.Composite(image.Rect(x, y, x+1, y+1)).RGBAAt(x, y) }
	// A hard drop shadow 5 pixels down and right: dark where it falls past the square, untouched elsewhere.
	sh := DefaultDropShadow()
	sh.Angle, sh.Distance, sh.Size, sh.Opacity = 135, 7, 0, 1
	if err := d.SetStyle(n.ID, &LayerStyle{DropShadow: &sh}); err != nil {
		t.Fatal(err)
	}
	if c := at(73, 73); c.R > 10 {
		t.Errorf("under the shadow %v", c)
	}
	if c := at(27, 27); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("on the light's side %v", c)
	}
	if c := at(50, 50); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("the square itself %v", c)
	}
	if steps, _ := d.History(); steps[len(steps)-1] != "Layer style" {
		t.Errorf("steps %v", steps)
	}
	// The effects follow the layer: moved, the shadow moves too.
	if err := d.Translate(10, 0); err != nil {
		t.Fatal(err)
	}
	if c := at(83, 73); c.R > 10 {
		t.Errorf("the moved shadow %v", c)
	}
	d.Undo()
	// A 4-pixel outside stroke, round at the corners as Photoshop's.
	st := DefaultOutline()
	st.Color, st.Size = color.NRGBA{B: 255, A: 255}, 4
	d.SetStyle(n.ID, &LayerStyle{Stroke: &st})
	if c := at(28, 50); c != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("the stroke beside the square %v", c)
	}
	if c := at(25, 50); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("past the stroke %v", c)
	}
	if c := at(26, 26); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("a square corner where a round one belongs: %v", c)
	}
	if c := at(50, 50); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("an outside stroke covered the square: %v", c)
	}
	// Inside and centred.
	st.Position = StrokeInside
	d.SetStyle(n.ID, &LayerStyle{Stroke: &st})
	if c := at(31, 50); c != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("the inside stroke %v", c)
	}
	if c := at(28, 50); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("outside the inside stroke %v", c)
	}
	st.Position = StrokeCenter
	d.SetStyle(n.ID, &LayerStyle{Stroke: &st})
	if a, b := at(29, 50), at(30, 50); a.B != 255 || b.B != 255 {
		t.Errorf("the centred stroke %v %v", a, b)
	}
	// A colour overlay changes the square's colour, not its shape.
	ov := DefaultOverlay()
	ov.Color = color.NRGBA{G: 255, A: 255}
	d.SetStyle(n.ID, &LayerStyle{ColorOverlay: &ov})
	if c, out := at(50, 50), at(20, 20); c != (color.RGBA{0, 255, 0, 255}) || out != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("overlay %v, outside %v", c, out)
	}
	// An outer glow brightens nothing on white with screen; on a dark background it shows round the square.
	d.Choose(d.Layers()[1].ID)
	d.SelectAll()
	d.Fill(color.NRGBA{A: 255})
	d.Deselect()
	g := DefaultOuterGlow()
	g.Size, g.Opacity = 10, 1
	d.SetStyle(n.ID, &LayerStyle{OuterGlow: &g})
	if edge, near, mid, far := at(29, 50), at(27, 50), at(24, 50), at(5, 50); !(edge.R > near.R && near.R > mid.R && mid.R > 0 && far.R == 0) {
		t.Errorf("the glow fades %v, %v, %v, %v", edge, near, mid, far)
	}
	// Inner shadow and glow stay within the shape.
	is := DefaultInnerShadow()
	is.Distance, is.Size, is.Opacity, is.Angle = 5, 0, 1, 135
	d.SetStyle(n.ID, &LayerStyle{InnerShadow: &is})
	if c := at(32, 32); c.R > 10 {
		t.Errorf("inside the shadow's edge %v", c)
	}
	if c := at(65, 65); c.R != 255 {
		t.Errorf("away from it %v", c)
	}
	if c := at(28, 28); c.R != 0 || c.A != 255 {
		t.Errorf("outside the square %v", c)
	}
	// Taking the effects away is a step too, and settings out of range are refused.
	if err := d.SetStyle(n.ID, &LayerStyle{OuterGlow: &Glow{Opacity: 2}}); err == nil {
		t.Error("an opacity of 2 was taken")
	}
	d.SetStyle(n.ID, nil)
	if n := d.Node(n.ID); n.Style != nil {
		t.Error("the style stayed")
	}
	if _, err := d.BeginStyle(d.Root.Children[1].ID); err != nil {
		t.Errorf("a pixel layer refused a style: %v", err)
	}
}

func TestStyledLayersMergeAndSave(t *testing.T) {
	d, n := styledDoc(t)
	st := DefaultOutline()
	st.Color, st.Size = color.NRGBA{B: 255, A: 255}, 3
	sh := DefaultDropShadow()
	d.SetStyle(n.ID, &LayerStyle{Stroke: &st, DropShadow: &sh})
	look := d.Composite(image.Rect(0, 0, 100, 100))
	var buf bytes.Buffer
	if err := d.Snapshot().Save(&buf); err != nil {
		t.Fatal(err)
	}
	e, err := Load(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e.Node(n.ID).Style, d.Node(n.ID).Style) || !bytes.Equal(e.Composite(look.Rect).Pix, look.Pix) {
		t.Error("the styled layer came back different")
	}
	// Merged down, the effects become pixels and the picture looks the same.
	d.Choose(n.ID)
	if err := d.MergeDown(); err != nil {
		t.Fatal(err)
	}
	if got := d.Composite(look.Rect); !bytes.Equal(got.Pix, look.Pix) {
		t.Error("merging the styled layer changed the picture")
	}
	if d.Node(d.Active).Style != nil {
		t.Error("the layer under took the style")
	}
}
