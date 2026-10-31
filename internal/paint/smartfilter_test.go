package paint

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

// filteredDoc is a white picture with a black square in a smart object.
func filteredDoc(t *testing.T) (*Doc, *Node) {
	t.Helper()
	d := newDoc(t, 300, 200, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	d.AddLayer("Square")
	d.Select(image.Rect(100, 60, 200, 140))
	if err := d.Fill(color.NRGBA{A: 255}); err != nil {
		t.Fatal(err)
	}
	d.Deselect()
	if _, err := d.ConvertToSmart("Smart"); err != nil {
		t.Fatal(err)
	}
	return d, d.Node(d.Active)
}

func addFilter(t *testing.T, d *Doc, id int, f SmartFilter) {
	t.Helper()
	s, err := d.BeginSmartFilter(id, f)
	if err != nil {
		t.Fatal(err)
	}
	s.End()
}

func shown(d *Doc, x, y int) color.RGBA { return d.Composite(d.Frame).RGBAAt(x, y) }

func TestASmartFilterLeavesTheLayersAlone(t *testing.T) {
	d, n := filteredDoc(t)
	before := shown(d, 100, 100)
	addFilter(t, d, n.ID, SmartFilter{Kind: SmartBlur, Values: []float64{12}})
	if c := shown(d, 100, 100); c.R < 90 || c.R > 170 {
		t.Errorf("the edge shows %v, was %v", c, before)
	}
	if c := n.Children[0].Pixels.At(100, 100); c.A != 255 || c.R != 0 {
		t.Errorf("the layer changed: %v", c)
	}
	// Past the blur's reach nothing moved, and the picture's edge stays as it was.
	if c := shown(d, 150, 100); c.R != 0 {
		t.Errorf("the middle shows %v", c)
	}
	if d.UndoName() != "Smart filter" {
		t.Errorf("step %q", d.UndoName())
	}
	d.Undo()
	if c := shown(d, 100, 100); c != before {
		t.Errorf("undo shows %v", c)
	}
	d.Redo()
	if c := d.Node(n.ID).Filters; c == nil {
		t.Error("redo lost the filter")
	}
}

func TestSmartFiltersGoInOrderAndCanBeChanged(t *testing.T) {
	d, n := filteredDoc(t)
	addFilter(t, d, n.ID, SmartFilter{Kind: SmartMosaic, Values: []float64{50}})
	addFilter(t, d, n.ID, SmartFilter{Kind: SmartAdjust, Adjust: &Adjustment{Kind: AdjustInvert}})
	if c := shown(d, 150, 100); c.R != 255 {
		t.Errorf("after invert %v", c)
	}
	if err := d.ToggleSmartFilter(n.ID, 1); err != nil {
		t.Fatal(err)
	}
	// The mosaic's cell is four fifths black, over white.
	if c := shown(d, 150, 100); !near(c.R, 51, 1) {
		t.Errorf("turned off %v", c)
	}
	if err := d.ToggleSmartFilter(n.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := d.MoveSmartFilter(n.ID, 1, -1); err != nil {
		t.Fatal(err)
	}
	if got := d.Node(n.ID).Filters.List[0].Kind; got != SmartAdjust {
		t.Errorf("first is %q", got)
	}
	if err := d.RemoveSmartFilter(n.ID, 0); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveSmartFilter(n.ID, 0); err != nil {
		t.Fatal(err)
	}
	if d.Node(n.ID).Filters != nil {
		t.Error("filters are left")
	}
	if err := d.RemoveSmartFilter(n.ID, 0); err != ErrNoSmartFilter {
		t.Errorf("no filter: %v", err)
	}
}

func TestTheFilterFollowsWhatTheSmartObjectHolds(t *testing.T) {
	d, n := filteredDoc(t)
	addFilter(t, d, n.ID, SmartFilter{Kind: SmartBlur, Values: []float64{4}})
	if c := shown(d, 50, 50); c.R != 255 {
		t.Fatalf("before %v", c)
	}
	d.Choose(n.Children[0].ID)
	d.Select(image.Rect(40, 40, 60, 60))
	if err := d.Fill(color.NRGBA{A: 255}); err != nil {
		t.Fatal(err)
	}
	d.Deselect()
	if c := shown(d, 50, 50); c.R != 0 {
		t.Errorf("the new pixels do not show: %v", c)
	}
	d.Node(n.Children[0].ID).Visible = false
	if c := shown(d, 50, 50); c.R != 255 {
		t.Errorf("hiding the layer does not show: %v", c)
	}
}

func TestSmartFiltersReachBeyondWhatIsDrawn(t *testing.T) {
	d, n := filteredDoc(t)
	addFilter(t, d, n.ID, SmartFilter{Kind: SmartBlur, Values: []float64{20}})
	// Beyond the square by much more than a tile's rounding.
	if c := shown(d, 90, 100); c.R == 255 {
		t.Errorf("the blur stops at the square: %v", c)
	}
}

func TestTheBlurAtAReducedLevelLooksTheSame(t *testing.T) {
	d, n := filteredDoc(t)
	addFilter(t, d, n.ID, SmartFilter{Kind: SmartBlur, Values: []float64{16}})
	full := d.Composite(d.Frame)
	half := image.Rect(0, 0, 150, 100)
	img := image.NewRGBA(half)
	d.Render(img, half, 1)
	for _, p := range []image.Point{{46, 50}, {50, 40}, {30, 50}, {75, 50}} {
		a, b := full.RGBAAt(p.X*2, p.Y*2), img.RGBAAt(p.X, p.Y)
		if !near(a.R, b.R, 24) {
			t.Errorf("at %v: %v at full size, %v at half", p, a, b)
		}
	}
}

func TestSmartFiltersAreSavedAndShared(t *testing.T) {
	d, n := filteredDoc(t)
	addFilter(t, d, n.ID, SmartFilter{Kind: SmartNoise, Values: []float64{20}, On: true, Seed: 7})
	addFilter(t, d, n.ID, SmartFilter{Kind: SmartDevelop, Values: make([]float64, 11), Off: true})
	var b bytes.Buffer
	if err := d.Snapshot().Save(&b); err != nil {
		t.Fatal(err)
	}
	back, err := Load(bytes.NewReader(b.Bytes()), int64(b.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got := back.Node(n.ID).Filters
	if got == nil || len(got.List) != 2 || got.List[0].Seed != 7 || !got.List[1].Off || got.List[0].Kind != SmartNoise {
		t.Fatalf("filters %+v", got)
	}
	a, c := shown(d, 150, 100), shown(back, 150, 100)
	if a != c {
		t.Errorf("shown %v, came back %v", a, c)
	}
}

func TestOnlyASmartObjectHoldsFilters(t *testing.T) {
	d := newDoc(t, 20, 20, color.NRGBA{A: 255})
	if _, err := d.BeginSmartFilter(d.Active, SmartFilter{Kind: SmartBlur, Values: []float64{3}}); err != ErrNotSmart {
		t.Errorf("%v", err)
	}
	dd, n := filteredDoc(t)
	if _, err := dd.BeginSmartFilter(n.ID, SmartFilter{Kind: SmartBlur}); err == nil {
		t.Error("a blur without a radius was taken")
	}
	if _, err := dd.BeginSmartFilter(n.ID, SmartFilter{Kind: "melt", Values: []float64{1}}); err == nil {
		t.Error("an unknown filter was taken")
	}
}

func TestAConvertedGroupDropsItsFilters(t *testing.T) {
	d, n := filteredDoc(t)
	addFilter(t, d, n.ID, SmartFilter{Kind: SmartBlur, Values: []float64{12}})
	d.Choose(n.ID)
	if err := d.ReleaseSmart(); err != nil {
		t.Fatal(err)
	}
	if d.Node(n.ID).Filters != nil {
		t.Error("the plain group kept its filters")
	}
	if c := shown(d, 150, 100); c.R != 0 {
		t.Errorf("shown %v", c)
	}
}

func TestEffectLossIsCountedForPSD(t *testing.T) {
	d, n := filteredDoc(t)
	addFilter(t, d, n.ID, SmartFilter{Kind: SmartBlur, Values: []float64{12}})
	_, loss, err := d.ToPSD()
	if err != nil {
		t.Fatal(err)
	}
	if loss.Effects != 1 {
		t.Errorf("loss %+v", loss)
	}
}
