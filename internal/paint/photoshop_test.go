package paint

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"testing"
)

// over2 composites one solid colour over another with a blend mode, as a two-layer picture.
func over2(t *testing.T, bottom, top color.NRGBA, mode Blend) color.RGBA {
	t.Helper()
	d := newDoc(t, 4, 4, bottom)
	l := d.AddLayer("Top")
	d.Fill(top)
	d.SetBlend(l.ID, mode)
	return d.Composite(image.Rect(0, 0, 4, 4)).RGBAAt(1, 1)
}

func TestBlendModesMatchPhotoshop(t *testing.T) {
	grey := color.NRGBA{R: 128, G: 128, B: 128, A: 255}
	orange := color.NRGBA{R: 255, G: 128, B: 0, A: 255}
	for _, c := range []struct {
		mode Blend
		want color.RGBA
	}{
		{BlendNormal, color.RGBA{255, 128, 0, 255}},
		{BlendMultiply, color.RGBA{128, 64, 0, 255}},       // b·s
		{BlendScreen, color.RGBA{255, 192, 128, 255}},      // b+s−b·s
		{BlendDarken, color.RGBA{128, 128, 0, 255}},        // min
		{BlendLighten, color.RGBA{255, 128, 128, 255}},     // max
		{BlendDifference, color.RGBA{127, 0, 128, 255}},    // |b−s|
		{BlendLinearDodge, color.RGBA{255, 255, 128, 255}}, // b+s
		{BlendSubtract, color.RGBA{0, 0, 128, 255}},        // b−s
	} {
		got := over2(t, grey, orange, c.mode)
		for i, v := range []uint8{got.R, got.G, got.B} {
			w := []uint8{c.want.R, c.want.G, c.want.B}[i]
			if !near(v, w, 1) {
				t.Errorf("mode %d: %v, want %v", c.mode, got, c.want)
				break
			}
		}
	}
	// Luminosity takes the top's brightness and keeps the bottom's colour.
	got := over2(t, color.NRGBA{R: 200, G: 40, B: 40, A: 255}, color.NRGBA{R: 20, G: 20, B: 20, A: 255}, BlendLuminosity)
	if got.R <= got.G || got.R > 100 {
		t.Errorf("luminosity gives %v", got)
	}
	// Over transparency every mode shows the layer as it is.
	d := newDoc(t, 4, 4, color.NRGBA{})
	l := d.AddLayer("Top")
	d.Fill(orange)
	d.SetBlend(l.ID, BlendMultiply)
	if c := d.Composite(image.Rect(0, 0, 4, 4)).RGBAAt(1, 1); c != (color.RGBA{255, 128, 0, 255}) {
		t.Errorf("multiply over nothing gives %v", c)
	}
	// Only groups pass through.
	d.SetBlend(l.ID, BlendPassThrough)
	if d.Node(l.ID).Blend != BlendMultiply {
		t.Error("a layer took pass-through")
	}
}

func TestPassThroughGroupsLetLayersBlendBelow(t *testing.T) {
	d := newDoc(t, 4, 4, color.NRGBA{R: 128, G: 128, B: 128, A: 255})
	l := d.AddLayer("Ink")
	d.Fill(color.NRGBA{R: 255, G: 128, B: 0, A: 255})
	d.SetBlend(l.ID, BlendMultiply)
	g, _ := d.GroupChosen("Group")
	if c := d.Composite(image.Rect(0, 0, 4, 4)).RGBAAt(1, 1); !near(c.R, 128, 1) || !near(c.G, 64, 1) {
		t.Errorf("multiply inside a pass-through group gives %v", c)
	}
	// A normal group is composed on its own: the multiply has nothing under it inside the group.
	d.SetBlend(g.ID, BlendNormal)
	if c := d.Composite(image.Rect(0, 0, 4, 4)).RGBAAt(1, 1); c != (color.RGBA{255, 128, 0, 255}) {
		t.Errorf("multiply inside a normal group gives %v", c)
	}
}

func TestLayerMasks(t *testing.T) {
	d := newDoc(t, 40, 20, white)
	l := d.AddLayer("Ink")
	d.Fill(red)
	// A mask from the selection hides the rest.
	d.Select(image.Rect(0, 0, 20, 20))
	if err := d.AddMask(); err != nil {
		t.Fatal(err)
	}
	if !d.EditMask {
		t.Error("adding a mask did not start editing it")
	}
	look := d.Composite(image.Rect(0, 0, 40, 20))
	if look.RGBAAt(5, 5) != (color.RGBA{255, 0, 0, 255}) || look.RGBAAt(30, 5) != (color.RGBA{255, 255, 255, 255}) {
		t.Fatalf("masked layer shows %v and %v", look.RGBAAt(5, 5), look.RGBAAt(30, 5))
	}
	// Painting black on the mask hides more; the layer's own pixels are untouched.
	d.Deselect()
	stroke(t, d, Brush{Size: 6, Hardness: 1, Opacity: 1, Spacing: 0.1, Color: color.NRGBA{A: 255}}, false, [2]float64{2, 10}, [2]float64{18, 10})
	if c := d.Composite(image.Rect(0, 0, 40, 20)).RGBAAt(10, 10); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("painting black on the mask shows %v", c)
	}
	if l.Pixels.At(10, 10) != (color.RGBA{255, 0, 0, 255}) {
		t.Error("painting on the mask changed the layer")
	}
	// Turned off, the mask hides nothing.
	d.SetMaskOff(l.ID, true)
	if c := d.Composite(image.Rect(0, 0, 40, 20)).RGBAAt(30, 5); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("with the mask off %v", c)
	}
	d.SetMaskOff(l.ID, false)
	look = d.Composite(image.Rect(0, 0, 40, 20))
	// Applying it keeps the look and removes the mask.
	if err := d.ApplyMask(); err != nil {
		t.Fatal(err)
	}
	if d.Node(l.ID).Mask != nil || d.EditMask || !bytes.Equal(d.Composite(image.Rect(0, 0, 40, 20)).Pix, look.Pix) {
		t.Error("applying the mask changed the picture or kept the mask")
	}
	if err := d.DeleteMask(); !errors.Is(err, ErrNoMask) {
		t.Errorf("deleting a missing mask: %v", err)
	}
	d.Undo()
	if d.Node(l.ID).Mask == nil {
		t.Error("undo did not bring the mask back")
	}
}

func TestLockedTransparencyKeepsTheShape(t *testing.T) {
	d := newDoc(t, 30, 30, color.NRGBA{})
	l := d.Layers()[0]
	stroke(t, d, Brush{Size: 10, Hardness: 1, Opacity: 1, Spacing: 0.1, Color: red}, false, [2]float64{15, 15})
	d.SetLockAlpha(l.ID, true)
	// A big blue stroke over everything recolours the dot and nothing else.
	stroke(t, d, Brush{Size: 40, Hardness: 1, Opacity: 1, Spacing: 0.1, Color: color.NRGBA{B: 255, A: 255}}, false, [2]float64{15, 15})
	if c := l.Pixels.At(15, 15); c != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("the dot is %v", c)
	}
	if c := l.Pixels.At(2, 2); c.A != 0 {
		t.Errorf("transparency was painted: %v", c)
	}
	stroke(t, d, DefaultBrush(), true, [2]float64{15, 15})
	if c := l.Pixels.At(15, 15); c.A != 255 {
		t.Error("locked pixels were erased")
	}
}

func TestMovingALayerKeepsWhatLeavesThePicture(t *testing.T) {
	d := newDoc(t, 20, 20, color.NRGBA{})
	l := d.Layers()[0]
	d.Select(image.Rect(15, 0, 20, 20))
	d.Fill(red)
	d.Deselect()
	if err := d.Translate(10, 0); err != nil {
		t.Fatal(err)
	}
	if c := d.Composite(image.Rect(0, 0, 20, 20)).RGBAAt(17, 5); c.A != 0 {
		t.Errorf("the strip is still shown: %v", c)
	}
	d.Translate(-10, 0)
	if c := d.Composite(image.Rect(0, 0, 20, 20)).RGBAAt(17, 5); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("after moving back the strip is %v", c)
	}
	if l := d.Node(l.ID); l.Pixels.Bounds() != image.Rect(15, 0, 20, 20) {
		t.Errorf("the layer is %v", l.Pixels.Bounds())
	}
	// With a selection only the selected part moves, and the selection with it.
	d.Select(image.Rect(15, 0, 20, 5))
	d.Translate(-15, 0)
	look := d.Composite(image.Rect(0, 0, 20, 20))
	if look.RGBAAt(2, 2) != (color.RGBA{255, 0, 0, 255}) || look.RGBAAt(17, 2).A != 0 || look.RGBAAt(17, 10).A != 255 {
		t.Errorf("moving the selection: %v %v %v", look.RGBAAt(2, 2), look.RGBAAt(17, 2), look.RGBAAt(17, 10))
	}
	if b := d.SelectionBounds(); b != image.Rect(0, 0, 5, 5) {
		t.Errorf("the selection moved to %v", b)
	}
	// Painting on a layer that was moved away still lands on the picture.
	d.Deselect()
	d.Translate(100, 100)
	stroke(t, d, Brush{Size: 4, Hardness: 1, Opacity: 1, Spacing: 0.1, Color: red}, false, [2]float64{3, 15})
	if c := d.Composite(image.Rect(0, 0, 20, 20)).RGBAAt(3, 15); c.A != 255 {
		t.Errorf("paint on a moved layer: %v", c)
	}
}

func TestSelectionShapesAndModes(t *testing.T) {
	d := newDoc(t, 100, 100, white)
	d.SelectShape(d.EllipseMask(image.Rect(10, 10, 90, 90)), SelectNew)
	if selectedAt(d, 50, 50) != 255 || selectedAt(d, 12, 12) != 0 {
		t.Errorf("ellipse: centre %d, corner %d", selectedAt(d, 50, 50), selectedAt(d, 12, 12))
	}
	// The edge is anti-aliased: somewhere along it a pixel is partly selected.
	partial := false
	for x := 5; x < 15; x++ {
		if v := selectedAt(d, x, 50); v > 0 && v < 255 {
			partial = true
		}
	}
	if !partial {
		t.Error("the ellipse's edge is not anti-aliased")
	}
	d.SelectShape(d.RectMask(image.Rect(40, 0, 60, 100)), SelectSubtract)
	if selectedAt(d, 50, 50) != 0 || selectedAt(d, 30, 50) != 255 {
		t.Error("subtract")
	}
	d.SelectShape(d.RectMask(image.Rect(0, 45, 100, 55)), SelectAdd)
	if selectedAt(d, 50, 50) != 255 || selectedAt(d, 2, 50) != 255 {
		t.Error("add")
	}
	d.SelectShape(d.RectMask(image.Rect(0, 0, 50, 100)), SelectIntersect)
	if selectedAt(d, 70, 50) != 0 || selectedAt(d, 30, 50) != 255 {
		t.Error("intersect")
	}
	// A lasso triangle: inside and outside, and its bounds.
	d.SelectShape(d.PolygonMask([][2]float64{{10, 10}, {90, 10}, {50, 90}}), SelectNew)
	if selectedAt(d, 50, 30) != 255 || selectedAt(d, 15, 80) != 0 {
		t.Error("lasso")
	}
	if b := d.SelectionBounds(); b.Min.Y != 10 || b.Max.Y < 89 || b.Max.Y > 91 {
		t.Errorf("lasso bounds %v", b)
	}
	d.InvertSelection()
	if selectedAt(d, 50, 30) != 0 || selectedAt(d, 15, 80) != 255 {
		t.Error("inverse")
	}
	d.Deselect()
	d.Reselect()
	if selectedAt(d, 15, 80) != 255 {
		t.Error("reselect")
	}
	d.Select(image.Rect(30, 30, 70, 70))
	d.FeatherSelection(8)
	if v := selectedAt(d, 30, 50); v < 60 || v > 200 {
		t.Errorf("a feathered edge is %d", v)
	}
	if selectedAt(d, 50, 50) != 255 || selectedAt(d, 5, 5) != 0 {
		t.Error("feathering changed the middle or the far outside")
	}
}

func TestMagicWand(t *testing.T) {
	d := newDoc(t, 60, 30, white)
	// Two red squares, apart, and a pink one near red.
	for _, r := range []image.Rectangle{image.Rect(5, 5, 15, 15), image.Rect(40, 5, 50, 15)} {
		d.Select(r)
		d.Fill(red)
	}
	d.Select(image.Rect(20, 20, 25, 25))
	d.Fill(color.NRGBA{R: 255, G: 20, B: 20, A: 255})
	d.Deselect()
	m, _ := d.WandMask(10, 10, 10, true, false)
	if m.At(45, 10) != 0 || m.At(10, 10) != 255 {
		t.Error("contiguous took the other square or missed its own")
	}
	m, _ = d.WandMask(10, 10, 10, false, false)
	if m.At(45, 10) != 255 || m.At(22, 22) != 0 {
		t.Error("not contiguous: the other square should join, pink should not at tolerance 10")
	}
	m, _ = d.WandMask(10, 10, 32, false, false)
	if m.At(22, 22) != 255 {
		t.Error("pink stays out at tolerance 32")
	}
	// On the white: everything but the squares.
	m, _ = d.WandMask(0, 0, 0, true, false)
	if m.At(0, 0) != 255 || m.At(10, 10) != 0 || m.Bounds() != image.Rect(0, 0, 60, 30) {
		t.Error("wand on the background")
	}
}

func TestFlowBuildsUpWithinAStroke(t *testing.T) {
	d := newDoc(t, 60, 20, white)
	b := Brush{Size: 10, Hardness: 1, Opacity: 1, Spacing: 0.1, Flow: 0.2, Round: 1, Color: color.NRGBA{A: 255}}
	stroke(t, d, b, false, [2]float64{10, 10})
	dot := at(d.Layers()[0].Pixels, 10, 10).R
	stroke(t, d, b, false, [2]float64{30, 10}, [2]float64{40, 10})
	line := at(d.Layers()[0].Pixels, 35, 10).R
	// One dab at 20% flow is light grey; many overlapping dabs build towards black.
	if !near(dot, 204, 2) || line > 40 {
		t.Errorf("a dab gives %d, a line %d", dot, line)
	}
	// Opacity still caps it.
	b.Opacity = 0.5
	stroke(t, d, b, false, [2]float64{10, 5}, [2]float64{50, 5}, [2]float64{10, 5})
	if c := at(d.Layers()[0].Pixels, 30, 4).R; c < 125 {
		t.Errorf("flow went past the opacity: %d", c)
	}
}

func TestSmoothingTrailsThePointerAndCatchesUp(t *testing.T) {
	d := newDoc(t, 200, 40, color.NRGBA{})
	b := Brush{Size: 6, Hardness: 1, Opacity: 1, Spacing: 0.1, Smooth: 1, Round: 1, Color: red}
	s, _, _ := d.BeginStroke(b, false, 10, 20)
	s.To(40, 20) // within the string: nothing yet beyond the first dab
	if c := at(d.Layers()[0].Pixels, 30, 20); c.A != 0 {
		t.Errorf("the brush did not trail: %v", c)
	}
	s.To(150, 20)
	if c := at(d.Layers()[0].Pixels, 140, 20); c.A != 0 {
		t.Errorf("the brush reached the pointer: %v", c)
	}
	s.End() // catches up
	if c := at(d.Layers()[0].Pixels, 148, 20); c.A != 255 {
		t.Errorf("the stroke did not catch up: %v", c)
	}
}

func TestTipAngleAndRoundness(t *testing.T) {
	d := newDoc(t, 60, 60, color.NRGBA{})
	b := Brush{Size: 40, Hardness: 1, Opacity: 1, Spacing: 0.1, Round: 0.25, Angle: 90, Color: red}
	stroke(t, d, b, false, [2]float64{30, 30})
	l := d.Layers()[0].Pixels
	// Turned upright and squashed: tall and narrow.
	if l.At(30, 14).A == 0 || l.At(14, 30).A != 0 {
		t.Errorf("top %v, left %v", l.At(30, 14), l.At(14, 30))
	}
}

func TestSaveAndLoadVersion2AndReadVersion1(t *testing.T) {
	d := newDoc(t, 32, 24, white)
	l := d.AddLayer("Ink")
	d.Fill(red)
	d.SetBlend(l.ID, BlendScreen)
	d.Select(image.Rect(0, 0, 16, 24))
	d.AddMask()
	d.Deselect()
	d.Translate(40, 0) // partly off the picture
	d.Translate(-30, 0)
	d.SelectShape(d.EllipseMask(image.Rect(2, 2, 20, 20)), SelectNew)
	look := d.Composite(image.Rect(0, 0, 32, 24))
	var buf bytes.Buffer
	if err := d.Snapshot().Save(&buf); err != nil {
		t.Fatal(err)
	}
	e, err := Load(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	el := e.Node(l.ID)
	if el.Blend != BlendScreen || el.Mask == nil || !el.Pixels.Bounds().Eq(d.Node(l.ID).Pixels.Bounds()) || !e.EditMask {
		t.Fatalf("loaded layer %+v", el)
	}
	if !bytes.Equal(e.Composite(image.Rect(0, 0, 32, 24)).Pix, look.Pix) || !bytes.Equal(e.Selection.Read(d.Frame).Pix, d.Selection.Read(d.Frame).Pix) {
		t.Error("the loaded picture or selection differs")
	}
	// A version 1 file: layers the picture's size and a rectangular selection.
	var v1 bytes.Buffer
	z := zip.NewWriter(&v1)
	doc, _ := z.Create("doc.json")
	json.NewEncoder(doc).Encode(map[string]any{"version": 1, "width": 2, "height": 1, "active": 2, "nextId": 2,
		"selection": [4]int{0, 0, 1, 1}, "layers": []map[string]any{{"id": 2, "name": "Background", "visible": true, "opacity": 1}}})
	pix, _ := z.Create("layer-2.rgba")
	pix.Write([]byte{255, 0, 0, 255, 0, 0, 255, 255})
	z.Close()
	old, err := Load(bytes.NewReader(v1.Bytes()), int64(v1.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if old.Layers()[0].Pixels.At(1, 0) != (color.RGBA{0, 0, 255, 255}) || old.SelectionBounds() != image.Rect(0, 0, 1, 1) {
		t.Errorf("version 1: %v, selection %v", old.Layers()[0].Pixels.At(1, 0), old.SelectionBounds())
	}
}

func TestMoveSessionIsOneStepAndKeepsHistoryApart(t *testing.T) {
	d := newDoc(t, 30, 30, color.NRGBA{})
	d.Select(image.Rect(0, 0, 10, 10))
	d.Fill(red)
	d.Deselect()
	steps := len(d.history.done)
	m, err := d.BeginMove()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 10; i++ {
		m.To(i, i)
	}
	m.End()
	if len(d.history.done) != steps+1 {
		t.Fatalf("a drag made %d steps", len(d.history.done)-steps)
	}
	look := d.Composite(image.Rect(0, 0, 30, 30))
	if look.RGBAAt(15, 15) != (color.RGBA{255, 0, 0, 255}) || look.RGBAAt(5, 5).A != 0 {
		t.Fatalf("after the move %v %v", look.RGBAAt(15, 15), look.RGBAAt(5, 5))
	}
	// Painting after the move must not show up when the move is undone.
	stroke(t, d, Brush{Size: 4, Hardness: 1, Opacity: 1, Spacing: 0.1, Color: color.NRGBA{B: 255, A: 255}}, false, [2]float64{5, 25})
	d.Undo() // the stroke
	d.Undo() // the move
	back := d.Composite(image.Rect(0, 0, 30, 30))
	if back.RGBAAt(5, 5) != (color.RGBA{255, 0, 0, 255}) || back.RGBAAt(15, 15).A != 0 || back.RGBAAt(5, 25).A != 0 {
		t.Errorf("after undoing: %v %v %v", back.RGBAAt(5, 5), back.RGBAAt(15, 15), back.RGBAAt(5, 25))
	}
	// A press without a drag records nothing.
	m, _ = d.BeginMove()
	m.End()
	if d.CanRedo() == false {
		t.Error("an empty move dropped the redo")
	}
}
