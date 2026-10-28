package paint

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"reflect"
	"testing"
)

// square is a 100-pixel square at (x, y) as a smart shape.
func square(x, y float64) Vector {
	return Vector{Kind: VectorRect, Size: Pt{100, 100}, M: Translate(x, y), Fill: solidRed}
}

// The shapes of one object combine pixel by pixel: two squares overlapping by half, each way.
func TestShapesCombineInOneLayer(t *testing.T) {
	r := image.Rect(0, 0, 300, 200)
	for _, c := range []struct {
		op   ShapeOp
		area float64
		at   map[image.Point]bool // filled or not
	}{
		{OpAdd, 15000, map[image.Point]bool{{25, 50}: true, {75, 50}: true, {125, 50}: true}},
		{OpSubtract, 5000, map[image.Point]bool{{25, 50}: true, {75, 50}: false, {125, 50}: false}},
		{OpIntersect, 5000, map[image.Point]bool{{25, 50}: false, {75, 50}: true, {125, 50}: false}},
		{OpExclude, 10000, map[image.Point]bool{{25, 50}: true, {75, 50}: false, {125, 50}: true}},
	} {
		v := square(0, 0).WithShape(square(50, 0), c.op)
		if !v.Compound() || len(v.Shapes()) != 2 || v.ShapeOpOf(1) != c.op {
			t.Fatalf("%v: shapes %v", c.op, v.Shapes())
		}
		img := rendered(v, r)
		if a := coveredArea(img); math.Abs(a-c.area) > 2 {
			t.Errorf("%v covers %.1f, want %.0f", c.op, a, c.area)
		}
		for p, filled := range c.at {
			if got := img.RGBAAt(p.X, p.Y).A == 255; got != filled {
				t.Errorf("%v at %v: filled %v", c.op, p, got)
			}
		}
	}
}

// The stroke follows the outline the shapes make together, not each shape's own.
func TestCompoundStrokeFollowsTheCombinedOutline(t *testing.T) {
	v := square(0, 0).WithShape(square(50, 0), OpAdd)
	v.Fill = VectorPaint{}
	v.Stroke = VectorStroke{Paint: solidBlue, Width: 4}
	img := rendered(v, image.Rect(-10, -10, 170, 120))
	if c := img.RGBAAt(75, 0); c.A < 200 {
		t.Errorf("the combined outline's top edge %v", c)
	}
	if c := img.RGBAAt(100, 50); c.A != 0 {
		t.Errorf("the second square's left edge is inside the union and has no stroke: %v", c)
	}
	if c := img.RGBAAt(50, 50); c.A != 0 {
		t.Errorf("the first square's right edge is inside the union: %v", c)
	}
	// Expanding bakes the outline: it looks the same.
	e := v.Expanded()
	if diff := coveredDiff(rendered(e, img.Rect), img); e.Compound() || diff > 40 {
		t.Errorf("the expanded object looks different (%.1f pixels' worth)", diff)
	}
}

func coveredDiff(a, b *image.RGBA) float64 {
	sum := 0.0
	for i := 3; i < len(a.Pix); i += 4 {
		sum += math.Abs(float64(a.Pix[i])-float64(b.Pix[i])) / 255
	}
	return sum
}

// A shape added keeps its place on the canvas whatever the object's own space is; it can be taken out again, and
// the operation of a shape whose first subpath is deleted passes to its next.
func TestShapesEditing(t *testing.T) {
	base := Vector{Kind: VectorRect, Size: Pt{50, 50}, M: Affine{A: 2, E: 2, C: 10, F: 10}, Fill: solidRed}
	v := base.WithShape(square(150, 0), OpSubtract)
	img := rendered(v, image.Rect(0, 0, 300, 200))
	if img.RGBAAt(60, 60).A != 255 || img.RGBAAt(200, 50).A != 0 {
		t.Error("the subtracted square is not where it was put")
	}
	if got := v.ShapeOf(1); got != 1 {
		t.Errorf("subpath 1 is in shape %d", got)
	}
	shape, ok := v.Shape(1)
	if !ok || shape.Compound() || shape.Path[0].Op != OpJoin {
		t.Fatalf("shape 1 as an object %+v", shape)
	}
	if c := rendered(shape, image.Rect(0, 0, 300, 200)); c.RGBAAt(200, 50).A != 255 {
		t.Error("the copied shape is not where it was")
	}
	if w := v.WithoutShape(0); w.Compound() || w.Path[0].Op != OpJoin || rendered(w, img.Rect).RGBAAt(200, 50).A != 255 {
		t.Error("taking the first shape out should leave the second, as a shape of its own")
	}
	// Three shapes; deleting the second's nodes leaves its operation to nothing and the third keeps its own.
	three := v.WithShape(square(0, 120), OpAdd)
	gone := []NodeRef{{Sub: 1, Node: 0}, {Sub: 1, Node: 1}, {Sub: 1, Node: 2}}
	after := three.DeleteNodes(gone)
	if len(after.Shapes()) != 2 || after.ShapeOpOf(len(after.Shapes())-1) != OpAdd {
		t.Errorf("the last shape's operation %v", after.ShapeOpOf(len(after.Shapes())-1))
	}
	if w := three.WithShapeOp(1, OpIntersect); w.ShapeOpOf(1) != OpIntersect || w.ShapeOpOf(2) != OpAdd {
		t.Error("changing one shape's operation")
	}
}

// A vector mask shows its layer inside its outline and hides it beyond, moves with the layer, and comes back from
// the file.
func TestVectorMasks(t *testing.T) {
	d := New("Pixels")
	d.Paper = false
	d.View = image.Rect(0, 0, 450, 450)
	d.Fill(color.NRGBA{G: 255, A: 255})
	l := d.Root.Children[0]
	circle := Vector{Kind: VectorEllipse, Size: Pt{100, 100}, M: Translate(100, 100), Fill: solidRed}
	if err := d.AddShape(l.ID, circle, OpAdd); err != nil || l.Clip == nil || d.UndoName() != "Add vector mask" {
		t.Fatalf("adding a vector mask: %v, %q", err, d.UndoName())
	}
	at := func(x, y int) color.RGBA { return d.Composite(image.Rect(x, y, x+1, y+1)).RGBAAt(x, y) }
	if c := at(150, 150); c != (color.RGBA{0, 255, 0, 255}) {
		t.Errorf("inside the vector mask %v", c)
	}
	if c := at(105, 105); c.A != 0 {
		t.Errorf("outside the circle %v", c)
	}
	// Another shape pasted on combines with the mask.
	if err := d.AddShape(l.ID, square(300, 300), OpAdd); err != nil || !l.Clip.Compound() {
		t.Fatalf("combining the vector mask: %v", err)
	}
	if c := at(350, 350); c.A != 255 {
		t.Errorf("in the added square %v", c)
	}
	// It goes with the layer.
	if err := d.Translate(10, 0); err != nil {
		t.Fatal(err)
	}
	if at(105, 150).A != 0 || at(205, 150).A != 255 {
		t.Error("the vector mask did not move with the layer")
	}
	// Merging keeps how it looks.
	before := d.Composite(image.Rect(0, 0, 400, 400))
	d.AddLayer("Top")
	d.Choose(l.ID)
	d.AddLayer("Under")
	d.Move(false)
	d.Choose(l.ID)
	saved := d.Snapshot()
	if err := d.MergeDown(); err != nil {
		t.Fatal(err)
	}
	if after := d.Composite(image.Rect(0, 0, 400, 400)); coveredDiff(after, before) > 1 {
		t.Errorf("merging down changed the picture by %.1f pixels", coveredDiff(after, before))
	}
	// Saved and loaded, it is the same object.
	var buf bytes.Buffer
	if err := saved.Save(&buf); err != nil {
		t.Fatal(err)
	}
	e, err := Load(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if n := e.Node(l.ID); n == nil || !reflect.DeepEqual(n.Clip, l.Clip) {
		t.Error("the vector mask did not come back from the file")
	}
	// Rasterized, it becomes the layer mask; deleted, the layer shows whole.
	d.Undo()
	if err := d.RasterizeClip(l.ID); err != nil {
		t.Fatal(err)
	}
	if n := d.Node(l.ID); n.Clip != nil || n.Mask == nil || n.Mask.At(105, 105) != 0 || n.Mask.At(160, 150) != 255 {
		t.Error("rasterizing should make the vector mask the layer mask")
	}
	d.Undo()
	if err := d.DeleteClip(l.ID); err != nil || at(105, 105).A != 255 {
		t.Errorf("deleting the vector mask: %v", err)
	}
}

// Shapes of a vector layer added by AddShape, then changed and expanded, each as a step.
func TestShapeSteps(t *testing.T) {
	d := New("Background")
	n := d.AddVector(square(0, 0), "Shape")
	if err := d.AddShape(n.ID, square(50, 50), OpSubtract); err != nil || d.UndoName() != "Subtract shape" {
		t.Fatalf("%v, %q", err, d.UndoName())
	}
	if err := d.SetShapeOp(n.ID, 1, OpExclude); err != nil || n.Vector.ShapeOpOf(1) != OpExclude {
		t.Fatalf("set the operation: %v", err)
	}
	if err := d.SetShapeOp(n.ID, 5, OpExclude); err != ErrNoShape {
		t.Errorf("a shape it does not have: %v", err)
	}
	if err := d.ExpandShapes(n.ID); err != nil || n.Vector.Compound() {
		t.Fatalf("expand: %v", err)
	}
	d.Undo()
	if !d.Node(n.ID).Vector.Compound() {
		t.Error("undo of expanding")
	}
	// A boolean of two layers, one of them compound, uses the shapes' outline together.
	m := d.AddVector(square(25, 25), "Other")
	if _, err := d.Boolean(BoolIntersect, []int{n.ID, m.ID}); err != nil {
		t.Fatal(err)
	}
	res := d.Node(n.ID).Vector
	img := rendered(*res, image.Rect(0, 0, 200, 200))
	if img.RGBAAt(30, 30).A != 255 || img.RGBAAt(75, 75).A != 0 {
		t.Error("the boolean should follow the compound object's shapes")
	}
}
