package paint

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"
)

var (
	red   = color.NRGBA{R: 255, A: 255}
	white = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
)

func newDoc(t *testing.T, w, h int, bg color.NRGBA) *Doc {
	t.Helper()
	d, err := NewFramed(w, h, "Background", bg)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// at is a pixel of an image or a raster.
func at(p any, x, y int) color.RGBA {
	switch v := p.(type) {
	case *Raster:
		return v.At(x, y)
	case *image.RGBA:
		return v.RGBAAt(x, y)
	}
	panic("at: not a picture")
}

// framePix are a layer's pixels over the frame, row after row.
func framePix(d *Doc, l *Node) []uint8 { return l.Pixels.Read(d.Frame).Pix }

// selectedAt is how selected (x, y) is, 255 when nothing is selected.
func selectedAt(d *Doc, x, y int) uint32 {
	if d.Selection == nil {
		return 255
	}
	return uint32(d.Selection.At(x, y))
}

func near(a, b uint8, tol int) bool { return math.Abs(float64(a)-float64(b)) <= float64(tol) }

func stroke(t *testing.T, d *Doc, b Brush, erase bool, pts ...[2]float64) {
	t.Helper()
	s, _, err := d.BeginStroke(b, erase, pts[0][0], pts[0][1])
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pts[1:] {
		s.To(p[0], p[1])
	}
	s.End()
}

func TestHardBrushIsSolidInsideAndSmoothAtTheEdge(t *testing.T) {
	d := newDoc(t, 64, 64, color.NRGBA{})
	b := Brush{Size: 20, Hardness: 1, Opacity: 1, Spacing: 0.1, Color: red}
	stroke(t, d, b, false, [2]float64{32, 32})
	l := d.Layers()[0].Pixels
	if c := at(l, 32, 32); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("centre %v", c)
	}
	if c := at(l, 32, 32+12); c.A != 0 {
		t.Errorf("outside the dab %v", c)
	}
	// The edge pixel is partly covered: anti-aliased, not stepped.
	edge := at(l, 32+9, 32).A
	if edge == 0 || edge == 255 {
		t.Errorf("edge alpha %d", edge)
	}
}

func TestSoftBrushFadesFromTheCentre(t *testing.T) {
	d := newDoc(t, 64, 64, color.NRGBA{})
	stroke(t, d, Brush{Size: 40, Hardness: 0, Opacity: 1, Spacing: 0.1, Color: red}, false, [2]float64{32, 32})
	l := d.Layers()[0].Pixels
	prev := uint8(255)
	for dx := 0; dx <= 20; dx += 4 {
		a := at(l, 32+dx, 32).A
		if a > prev {
			t.Errorf("alpha rises at %d: %d after %d", dx, a, prev)
		}
		prev = a
	}
	if mid := at(l, 32+10, 32).A; mid < 60 || mid > 200 {
		t.Errorf("halfway out the alpha is %d", mid)
	}
}

func TestOpacityCapsAStrokeThatCrossesItself(t *testing.T) {
	d := newDoc(t, 80, 80, white)
	b := Brush{Size: 10, Hardness: 1, Opacity: 0.5, Spacing: 0.05, Color: color.NRGBA{A: 255}}
	// Back and forth over the same line many times in one stroke.
	stroke(t, d, b, false, [2]float64{10, 40}, [2]float64{70, 40}, [2]float64{10, 40}, [2]float64{70, 40})
	c := at(d.Layers()[0].Pixels, 40, 40)
	if !near(c.R, 128, 2) {
		t.Errorf("one stroke at 50%% gives grey %d, want 128", c.R)
	}
	// A second stroke adds to the first.
	stroke(t, d, b, false, [2]float64{10, 40}, [2]float64{70, 40})
	if c := at(d.Layers()[0].Pixels, 40, 40); !near(c.R, 64, 2) {
		t.Errorf("two strokes give grey %d, want 64", c.R)
	}
}

func TestSpacingLeavesNoGapsInALine(t *testing.T) {
	d := newDoc(t, 200, 40, color.NRGBA{})
	b := DefaultBrush()
	b.Size, b.Hardness = 6, 1
	s, _, _ := d.BeginStroke(b, false, 10, 20)
	// Many short moves, as a mouse makes.
	for x := 10.0; x <= 190; x += 0.7 {
		s.To(x, 20)
	}
	s.End()
	for x := 12; x <= 188; x++ {
		if a := at(d.Layers()[0].Pixels, x, 20).A; a != 255 {
			t.Fatalf("gap at x=%d: alpha %d", x, a)
		}
	}
}

func TestTipsDifferInShape(t *testing.T) {
	cover := func(tip Tip) int {
		d := newDoc(t, 64, 64, color.NRGBA{})
		stroke(t, d, Brush{Size: 30, Hardness: 1, Opacity: 1, Spacing: 0.1, Tip: tip, Color: red}, false, [2]float64{32, 32})
		n := 0
		pix := framePix(d, d.Layers()[0])
		for i := 3; i < len(pix); i += 4 {
			if pix[i] > 127 {
				n++
			}
		}
		return n
	}
	round, square, flat, chalk := cover(TipRound), cover(TipSquare), cover(TipFlat), cover(TipChalk)
	// The half-covered edge is half a pixel in, so a 30-pixel circle is about π·14.5² = 660 pixels and the square
	// 30² = 900; the flat ellipse is a third of the circle.
	if math.Abs(float64(round)-660) > 25 || math.Abs(float64(square)-900) > 25 {
		t.Errorf("round %d, square %d", round, square)
	}
	if flat > round/2 || flat < round/5 {
		t.Errorf("flat %d against round %d", flat, round)
	}
	if chalk >= round || chalk < round/3 {
		t.Errorf("chalk %d against round %d", chalk, round)
	}
}

func TestEraserUsesTheBrushAndCapsLikeIt(t *testing.T) {
	d := newDoc(t, 60, 60, red)
	b := Brush{Size: 10, Hardness: 1, Opacity: 0.5, Spacing: 0.1, Color: red}
	stroke(t, d, b, true, [2]float64{10, 30}, [2]float64{50, 30}, [2]float64{10, 30})
	c := at(d.Layers()[0].Pixels, 30, 30)
	if !near(c.A, 128, 2) || !near(c.R, 128, 2) {
		t.Errorf("half-erased pixel %v", c)
	}
	b.Opacity = 1
	stroke(t, d, b, true, [2]float64{10, 30}, [2]float64{50, 30})
	if c := at(d.Layers()[0].Pixels, 30, 30); c.A != 0 {
		t.Errorf("erased pixel %v", c)
	}
	if c := at(d.Layers()[0].Pixels, 30, 50); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("untouched pixel %v", c)
	}
}

func TestSelectionLimitsPainting(t *testing.T) {
	d := newDoc(t, 60, 60, color.NRGBA{})
	d.Select(image.Rect(20, 0, 40, 60))
	stroke(t, d, Brush{Size: 8, Hardness: 1, Opacity: 1, Spacing: 0.1, Color: red}, false, [2]float64{0, 30}, [2]float64{60, 30})
	l := d.Layers()[0].Pixels
	if at(l, 10, 30).A != 0 || at(l, 50, 30).A != 0 || at(l, 30, 30).A != 255 {
		t.Errorf("outside %d %d, inside %d", at(l, 10, 30).A, at(l, 50, 30).A, at(l, 30, 30).A)
	}
}

func TestUndoAndRedoRestoreEveryPixel(t *testing.T) {
	d := newDoc(t, 50, 50, white)
	original := append([]byte(nil), framePix(d, d.Layers()[0])...)
	stroke(t, d, DefaultBrush(), false, [2]float64{5, 5}, [2]float64{45, 45})
	painted := append([]byte(nil), framePix(d, d.Layers()[0])...)
	d.AddLayer("Layer 2")
	stroke(t, d, DefaultBrush(), false, [2]float64{5, 45}, [2]float64{45, 5})
	if d.UndoName() != "Brush" {
		t.Errorf("last step %q", d.UndoName())
	}
	d.Undo() // the erasing
	d.Undo() // the new layer
	if len(d.Layers()) != 1 || !bytes.Equal(framePix(d, d.Layers()[0]), painted) {
		t.Fatalf("after two undos: %d layers, pixels restored %v", len(d.Layers()), bytes.Equal(framePix(d, d.Layers()[0]), painted))
	}
	d.Undo()
	if !bytes.Equal(framePix(d, d.Layers()[0]), original) {
		t.Fatal("undoing the stroke did not restore the pixels")
	}
	d.Redo()
	if !bytes.Equal(framePix(d, d.Layers()[0]), painted) {
		t.Fatal("redoing the stroke did not repaint it")
	}
	d.Redo()
	if len(d.Layers()) != 2 || d.RedoName() != "Brush" {
		t.Errorf("redo: %d layers, next %q", len(d.Layers()), d.RedoName())
	}
	// A new change drops what could be redone.
	d.AddGroup("Group 1")
	if d.CanRedo() {
		t.Error("redo survives a new change")
	}
}

func TestLayerOperations(t *testing.T) {
	d := newDoc(t, 20, 20, white)
	bg := d.Layers()[0]
	top := d.AddLayer("Top")
	if d.Active != top.ID || d.Root.Children[0] != top {
		t.Fatal("a new layer goes above the chosen one and is chosen")
	}
	d.Fill(color.NRGBA{B: 255, A: 255})
	d.SetOpacity(top.ID, 0.5)
	look := d.Composite(image.Rect(0, 0, 20, 20))

	dup, _ := d.Duplicate(" copy")
	if dup.Name != "Top copy" || dup.Pixels == top.Pixels || !bytes.Equal(framePix(d, dup), framePix(d, top)) {
		t.Errorf("duplicate %q shares pixels %v", dup.Name, dup.Pixels == top.Pixels)
	}
	if err := d.Delete(); err != nil || len(d.Layers()) != 2 || d.Active != top.ID {
		t.Fatalf("delete: %v, %d layers, chosen %d", err, len(d.Layers()), d.Active)
	}

	// Merging down keeps the look.
	if err := d.MergeDown(); err != nil {
		t.Fatal(err)
	}
	if len(d.Layers()) != 1 || d.Active != bg.ID || !bytes.Equal(d.Composite(image.Rect(0, 0, 20, 20)).Pix, look.Pix) {
		t.Error("merge down changed the picture or kept the layer")
	}
	if err := d.MergeDown(); !errors.Is(err, ErrNothingBelow) {
		t.Errorf("merging the bottom layer: %v", err)
	}
	if err := d.Delete(); !errors.Is(err, ErrLastLayer) {
		t.Errorf("deleting the last layer: %v", err)
	}

	// Groups: put a layer in one, fade the group, merge it.
	l2 := d.AddLayer("Ink")
	d.Fill(red)
	g, _ := d.GroupChosen("Group 1")
	if g.Children[0] != d.Node(l2.ID) || d.Active != g.ID {
		t.Fatal("grouping")
	}
	d.SetOpacity(g.ID, 0.25)
	look = d.Composite(image.Rect(0, 0, 20, 20))
	// A quarter of red over the background, which is white with half of blue merged into it: (127, 127, 255).
	if c := look.RGBAAt(5, 5); !near(c.R, 159, 1) || !near(c.G, 95, 1) || !near(c.B, 191, 1) {
		t.Errorf("faded group looks %v", c)
	}
	if err := d.MergeGroup(); err != nil {
		t.Fatal(err)
	}
	if n := d.Node(d.Active); n.IsGroup() || n.Name != "Group 1" || !bytes.Equal(d.Composite(image.Rect(0, 0, 20, 20)).Pix, look.Pix) {
		t.Error("merging the group changed the picture")
	}
	d.Undo()
	if err := d.Ungroup(); err != nil || d.Active != l2.ID || d.Root.Children[0].ID != l2.ID {
		t.Errorf("ungroup: %v, chosen %d", err, d.Active)
	}

	// Moving: down past the background, then back up.
	d.Move(false)
	if d.Root.Children[len(d.Root.Children)-1].ID != l2.ID {
		t.Error("moving down")
	}
	d.Move(true)
	if d.Root.Children[0].ID != l2.ID {
		t.Error("moving up")
	}

	// Hidden layers do not show, and are not painted on.
	d.SetVisible(l2.ID, false)
	if c := d.Composite(image.Rect(0, 0, 20, 20)).RGBAAt(5, 5); c != (color.RGBA{127, 127, 255, 255}) {
		t.Errorf("hidden layer shows: %v", c)
	}
	if _, _, err := d.BeginStroke(DefaultBrush(), false, 5, 5); !errors.Is(err, ErrHidden) {
		t.Errorf("painting a hidden layer: %v", err)
	}
	d.Flatten("Background")
	if len(d.Layers()) != 1 {
		t.Error("flatten")
	}
}

func TestMovingIntoAndOutOfGroups(t *testing.T) {
	d := newDoc(t, 10, 10, white)
	g := d.AddGroup("Group")
	d.Choose(d.Layers()[0].ID)
	bg := d.Active
	// Up from below an open group enters it at the bottom.
	d.Move(true)
	if _, parent, _ := d.find(bg); parent.ID != g.ID {
		t.Fatal("the layer did not go into the group")
	}
	// Up again leaves it on top.
	d.Move(true)
	if _, parent, i := d.find(bg); parent != d.Root || i != 0 {
		t.Fatalf("the layer did not leave the group above it: parent %d at %d", parent.ID, i)
	}
}

func TestCopyCutPasteAndExport(t *testing.T) {
	d := newDoc(t, 40, 30, white)
	l := d.AddLayer("Ink")
	d.Select(image.Rect(10, 10, 20, 15))
	d.Fill(red)
	merged, _ := d.Copy(true)
	if merged.Rect != image.Rect(0, 0, 10, 5) || merged.RGBAAt(0, 0) != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("copy %v %v", merged.Rect, merged.RGBAAt(0, 0))
	}
	cut, _ := d.Cut()
	if cut.RGBAAt(9, 4) != (color.RGBA{255, 0, 0, 255}) || l.Pixels.At(12, 12).A != 0 {
		t.Error("cut")
	}
	p := d.Paste(cut, "Pasted", &image.Point{X: 0, Y: 0})
	if d.Active != p.ID || d.SelectionBounds() != image.Rect(0, 0, 10, 5) || p.Pixels.At(9, 4).R != 255 {
		t.Errorf("paste: selection %v", d.SelectionBounds())
	}
	var buf bytes.Buffer
	if err := d.WritePNG(&buf, true); err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(&buf)
	if err != nil || img.Bounds().Dx() != 10 {
		t.Fatalf("selection export %v %v", img.Bounds(), err)
	}
	d.Select(image.Rectangle{})
	buf.Reset()
	d.WritePNG(&buf, true)
	img, _ = png.Decode(&buf)
	if img.Bounds() != image.Rect(0, 0, 40, 30) {
		t.Errorf("with nothing selected the export is %v", img.Bounds())
	}
}

func TestFromImageAndLimits(t *testing.T) {
	src := image.NewRGBA(image.Rect(5, 5, 25, 15))
	src.Set(5, 5, red)
	d, err := FromImage(src, "Background")
	if err != nil || d.Frame.Dx() != 20 || d.Frame.Dy() != 10 || d.Layers()[0].Pixels.At(0, 0) != (color.RGBA{255, 0, 0, 255}) {
		t.Fatalf("from image: %v %d×%d", err, d.Frame.Dx(), d.Frame.Dy())
	}
	if _, err := NewFramed(0, 10, "", white); !errors.Is(err, ErrTooBig) {
		t.Error("an empty picture was made")
	}
	if _, err := NewFramed(maxSide+1, 10, "", white); !errors.Is(err, ErrTooBig) {
		t.Error("an oversized picture was made")
	}
}

func TestCancelledStrokeLeavesNothing(t *testing.T) {
	d := newDoc(t, 30, 30, white)
	before := append([]byte(nil), framePix(d, d.Layers()[0])...)
	s, _, _ := d.BeginStroke(DefaultBrush(), false, 5, 5)
	s.To(25, 25)
	s.Cancel()
	if !bytes.Equal(framePix(d, d.Layers()[0]), before) || d.CanUndo() {
		t.Error("a cancelled stroke left pixels or a step")
	}
}

func BenchmarkStrokeOnA4KCanvas(b *testing.B) {
	d, _ := NewFramed(3840, 2160, "Background", white)
	br := DefaultBrush()
	br.Size = 40
	b.ResetTimer()
	for range b.N {
		s, _, _ := d.BeginStroke(br, false, 100, 100)
		for x := 100.0; x < 3700; x += 4 {
			s.To(x, 100+x/4)
		}
		s.End()
	}
}

func TestSaveAndLoadKeepEveryLayer(t *testing.T) {
	d := newDoc(t, 64, 48, white)
	stroke(t, d, Brush{Size: 9, Hardness: 0.3, Opacity: 0.7, Spacing: 0.1, Color: red}, false, [2]float64{4, 4}, [2]float64{60, 40})
	g := d.AddGroup("Group")
	ink := d.AddLayer("Ink")
	stroke(t, d, DefaultBrush(), false, [2]float64{60, 4}, [2]float64{4, 40})
	d.SetOpacity(ink.ID, 0.4)
	d.SetVisible(g.ID, true)
	d.SetOpenGroup(g.ID, false)
	d.Select(image.Rect(3, 4, 30, 20))
	look := d.Composite(image.Rect(0, 0, 64, 48))

	var buf bytes.Buffer
	if err := d.Snapshot().Save(&buf); err != nil {
		t.Fatal(err)
	}
	e, err := Load(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if e.Frame.Dx() != 64 || e.Frame.Dy() != 48 || e.Active != ink.ID || e.SelectionBounds() != d.SelectionBounds() || len(e.Layers()) != 2 {
		t.Fatalf("loaded %d×%d, chosen %d, selection %v, %d layers", e.Frame.Dx(), e.Frame.Dy(), e.Active, e.SelectionBounds(), len(e.Layers()))
	}
	if !bytes.Equal(e.Composite(image.Rect(0, 0, 64, 48)).Pix, look.Pix) {
		t.Error("the loaded picture looks different")
	}
	if lg := e.Node(g.ID); !lg.IsGroup() || lg.Open || lg.Children[0].Opacity != 0.4 {
		t.Errorf("group %+v", lg)
	}
	// New nodes get fresh ids after the loaded ones.
	if n := e.AddLayer("More"); n.ID <= ink.ID {
		t.Errorf("new id %d", n.ID)
	}
	if _, err := Load(bytes.NewReader([]byte("not a zip")), 9); !errors.Is(err, ErrNotACanvas) {
		t.Errorf("garbage: %v", err)
	}
}
