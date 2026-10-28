package paint

import (
	"bytes"
	"image"
	"image/color"
	"reflect"
	"testing"
)

func TestAdjustmentLayers(t *testing.T) {
	d, err := NewFramed(40, 20, "Background", color.NRGBA{R: 200, G: 100, B: 50, A: 255})
	if err != nil {
		t.Fatal(err)
	}
	grey := func(x, y int) color.RGBA { return d.Composite(image.Rect(x, y, x+1, y+1)).RGBAAt(x, y) }
	bg := d.Active
	n, err := d.AddAdjustment(Adjustment{Kind: AdjustInvert}, "Invert 1")
	if err != nil {
		t.Fatal(err)
	}
	if c := grey(5, 5); c != (color.RGBA{55, 155, 205, 255}) {
		t.Fatalf("inverted %v", c)
	}
	// The pixels under it are not changed.
	if c := d.Node(bg).Pixels.At(5, 5); c != (color.RGBA{200, 100, 50, 255}) {
		t.Errorf("the background became %v", c)
	}
	if !d.EditMask || n.Mask == nil || n.Mask.Fill() != 255 {
		t.Error("a new adjustment layer has a white mask, chosen")
	}
	// Painting black into its mask on the left takes the adjustment away there.
	d.Select(image.Rect(0, 0, 20, 20))
	if err := d.Fill(color.NRGBA{A: 255}); err != nil {
		t.Fatal(err)
	}
	d.Deselect()
	if l, r := grey(5, 5), grey(30, 5); l.R != 200 || r.R != 55 {
		t.Errorf("masked: left %v, right %v", l, r)
	}
	// Half opacity is half the change.
	d.SetOpacity(n.ID, 0.5)
	if c := grey(30, 5); c.R < 126 || c.R > 129 {
		t.Errorf("at half opacity %v", c)
	}
	d.SetOpacity(n.ID, 1)
	// Its settings change as one step, and only to settings of its kind.
	lv, _ := d.AddAdjustment(Adjustment{Kind: AdjustLevels, Values: []float64{0, 1, 255, 0, 255}}, "Levels 1")
	if err := d.SetAdjustment(lv.ID, Adjustment{Kind: AdjustLevels, Values: []float64{0, 1, 255, 0, 128}}); err != nil {
		t.Fatal(err)
	}
	if c := grey(30, 5); c.R > 30 {
		t.Errorf("levels to half white over the invert: %v", c)
	}
	if err := d.SetAdjustment(lv.ID, Adjustment{Kind: AdjustInvert}); err == nil {
		t.Error("a levels layer took an invert")
	}
	if err := d.SetAdjustment(lv.ID, Adjustment{Kind: AdjustLevels, Values: []float64{1}}); err == nil {
		t.Error("levels took one value")
	}
	d.Undo()
	if c := grey(30, 5); c.R != 55 {
		t.Errorf("undo of the settings: %v", c)
	}
	// Pixel work refuses it, but its mask can be painted.
	d.EditMask = false
	if err := d.Fill(color.NRGBA{A: 255}); err != ErrAdjustmentLayer {
		t.Errorf("filling an adjustment layer: %v", err)
	}
	if _, err := d.BeginTransform(); err != ErrAdjustmentLayer {
		t.Errorf("transforming it: %v", err)
	}
	// Inside a group it changes only what is under it in the group; a pass-through group lets it reach further.
	d.Choose(lv.ID)
	d.Delete()
	d.Choose(n.ID)
	g, _ := d.GroupChosen("Group")
	d.SetBlend(g.ID, BlendNormal)
	if c := grey(30, 5); c.R != 200 {
		t.Errorf("an isolated group's adjustment reached outside: %v", c)
	}
	d.SetBlend(g.ID, BlendPassThrough)
	if c := grey(30, 5); c.R != 55 {
		t.Errorf("a pass-through group's adjustment did not reach outside: %v", c)
	}
	// Merging it down applies it to the layer under it.
	if err := d.Ungroup(); err != nil {
		t.Fatal(err)
	}
	d.Choose(n.ID)
	if err := d.MergeDown(); err != nil {
		t.Fatal(err)
	}
	if len(d.Layers()) != 1 || d.Node(d.Active).Pixels.At(30, 5) != (color.RGBA{55, 155, 205, 255}) || d.Node(d.Active).Pixels.At(5, 5).R != 200 {
		t.Errorf("merged: %d layers, %v and %v", len(d.Layers()), d.Node(d.Active).Pixels.At(30, 5), d.Node(d.Active).Pixels.At(5, 5))
	}
}

func TestAdjustmentLayersAreSaved(t *testing.T) {
	d := New("Layer 1")
	d.Select(image.Rect(0, 0, 10, 10))
	d.Fill(color.NRGBA{R: 255, A: 255})
	d.Deselect()
	curve := Adjustment{Kind: AdjustCurves, Curve: []CurvePoint{{0, 0}, {128, 200}, {255, 255}}}
	n, _ := d.AddAdjustment(curve, "Curves 1")
	d.AddAdjustment(Adjustment{Kind: AdjustHue, Values: []float64{120, 0, 0}, On: false}, "Hue")
	var buf bytes.Buffer
	if err := d.Snapshot().Save(&buf); err != nil {
		t.Fatal(err)
	}
	e, err := Load(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if m := e.Node(n.ID); m == nil || !reflect.DeepEqual(m.Adjust, n.Adjust) || m.Mask == nil {
		t.Fatalf("the curves layer came back as %+v", m)
	}
	r := image.Rect(0, 0, 12, 12)
	if !bytes.Equal(e.Composite(r).Pix, d.Composite(r).Pix) {
		t.Error("the loaded picture looks different")
	}
}
