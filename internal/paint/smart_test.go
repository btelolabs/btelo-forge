package paint

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

var green = color.NRGBA{G: 255, A: 255}

// smartPicture is a picture with a group of a red square layer made a smart object, and a copy of it moved to the
// right; it returns the two copies.
func smartPicture(t *testing.T) (*Doc, *Node, *Node) {
	t.Helper()
	d := New("Background")
	d.Paper = false
	d.View = image.Rect(0, 0, 600, 300)
	d.AddLayer("Square")
	d.Select(image.Rect(10, 10, 60, 60))
	d.Fill(color.NRGBA{R: 255, A: 255})
	d.Deselect()
	if _, err := d.GroupChosen("Badge"); err != nil {
		t.Fatal(err)
	}
	id, err := d.ConvertToSmart("Group")
	if err != nil || !validLinkID(id) || d.UndoName() != "Convert to smart object" {
		t.Fatalf("convert: %q, %v, %q", id, err, d.UndoName())
	}
	a := d.Node(d.Active)
	if changes := d.SmartChanges(); len(changes) != 1 || changes[0].ID != id {
		t.Fatalf("the new link is offered to be kept: %+v", changes)
	}
	if _, err := d.Duplicate(" copy"); err != nil {
		t.Fatal(err)
	}
	b := d.Node(d.Active)
	if err := d.Translate(200, 0); err != nil {
		t.Fatal(err)
	}
	b = d.Node(b.ID)
	if b.Smart == nil || b.Smart.ID != id || b.Smart.Offset != image.Pt(200, 0) {
		t.Fatalf("the moved copy %+v", b.Smart)
	}
	if len(d.SmartChanges()) != 0 {
		t.Error("copying and moving a smart object does not change what it shares")
	}
	return d, a, b
}

func TestSmartCopiesChangeTogether(t *testing.T) {
	d, a, b := smartPicture(t)
	at := func(x, y int) color.RGBA { return d.Composite(image.Rect(x, y, x+1, y+1)).RGBAAt(x, y) }
	if c := at(230, 30); c != (color.RGBA{255, 0, 0, 255}) {
		t.Fatalf("the copy shows %v", c)
	}
	// Painting in the first copy's layer paints in the second, in the same step.
	d.Choose(a.Children[0].ID)
	steps, _ := d.History()
	d.Select(image.Rect(10, 70, 60, 90))
	d.Fill(green)
	if c := at(230, 80); c != (color.RGBA{0, 255, 0, 255}) {
		t.Fatalf("the change did not reach the copy: %v", c)
	}
	if now, _ := d.History(); len(now) != len(steps)+2 { // the selection, then the fill
		t.Errorf("steps %v", now)
	}
	changes := d.SmartChanges()
	if len(changes) != 1 || changes[0].ID != a.Smart.ID {
		t.Fatalf("changes %+v", changes)
	}
	// Changing the copy's own place or settings does not change what it shares.
	d.Deselect()
	d.SetOpacity(b.ID, 0.5)
	if len(d.SmartChanges()) != 0 || d.Node(a.ID).Opacity != 1 {
		t.Error("a copy's opacity is its own")
	}
	// Undo takes the change back from both, and offers that as a change in turn.
	d.Undo()
	d.Undo()
	d.Undo()
	if c := at(230, 80); c.A != 0 {
		t.Errorf("after undo the copy shows %v", c)
	}
	if c := at(30, 80); c.A != 0 {
		t.Errorf("after undo the first shows %v", c)
	}
	if len(d.SmartChanges()) != 1 {
		t.Error("undoing a change to shared layers is a change to offer too")
	}
	// Deleting a layer in the second copy deletes it in the first.
	d.Choose(d.Node(b.ID).Children[0].ID)
	d.AddLayer("More")
	if n := d.Node(a.ID); len(n.Children) != 2 || n.Children[0].Name != "More" {
		t.Fatalf("a layer added in one copy: the other has %d", len(n.Children))
	}
}

func TestBreakingTheLink(t *testing.T) {
	d, a, b := smartPicture(t)
	d.Choose(b.ID)
	old := b.Smart.ID
	id, err := d.UnlinkSmart()
	if err != nil || id == old || d.Node(b.ID).Smart.ID != id {
		t.Fatalf("unlink: %q, %v", id, err)
	}
	d.SmartChanges()
	d.Choose(d.Node(a.ID).Children[0].ID)
	d.Fill(green)
	at := func(x, y int) color.RGBA { return d.Composite(image.Rect(x, y, x+1, y+1)).RGBAAt(x, y) }
	if c := at(230, 30); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("the unlinked copy changed: %v", c)
	}
	d.Choose(b.ID)
	if err := d.ReleaseSmart(); err != nil || d.Node(b.ID).Smart != nil || !d.Node(b.ID).IsGroup() {
		t.Errorf("release: %v", err)
	}
}

// Shared layers go between pictures: kept on their own, taken into another picture's copies, which then show them.
func TestSmartAcrossPictures(t *testing.T) {
	d, a, _ := smartPicture(t)
	// A copy pasted into another picture keeps its link.
	n, from, _ := d.CopyNode(a.ID)
	e := New("Other")
	e.Paper = false
	pasted := e.PasteNode(n, from)
	if pasted.Smart == nil || pasted.Smart.ID != a.Smart.ID || pasted.ID == a.ID && e.Node(pasted.ID) == nil {
		t.Fatalf("pasted %+v", pasted.Smart)
	}
	// A change in the first picture, kept and read back, reaches the second.
	d.Choose(d.Node(a.ID).Children[0].ID)
	d.Fill(green)
	changes := d.SmartChanges()
	var buf bytes.Buffer
	if err := SaveSmart(&buf, changes[0]); err != nil {
		t.Fatal(err)
	}
	kept, err := LoadSmart(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil || kept.ID != changes[0].ID || kept.Version != changes[0].Version {
		t.Fatalf("kept %+v, %v", kept, err)
	}
	if !e.ApplySmart(kept) || e.UndoName() != "Update smart object" {
		t.Fatal("the other picture did not take the change")
	}
	if c := e.Composite(image.Rect(30, 30, 31, 31)).RGBAAt(30, 30); c != (color.RGBA{0, 255, 0, 255}) {
		t.Errorf("the other picture shows %v", c)
	}
	if e.ApplySmart(kept) {
		t.Error("taking the same change twice changes nothing")
	}
	if len(e.SmartChanges()) != 0 {
		t.Error("a change taken from elsewhere is not offered back")
	}
	// Its picture on its own is cut to what it shows.
	if img := SmartPicture(kept); img == nil || img.Rect != image.Rect(0, 0, 768, 512) { // the fill covered the view's tiles
		t.Errorf("its picture is not cut to what it shows: %v", img.Rect)
	}
	// The file keeps the link and the offset.
	var file bytes.Buffer
	if err := d.Snapshot().Save(&file); err != nil {
		t.Fatal(err)
	}
	f, err := Load(bytes.NewReader(file.Bytes()), int64(file.Len()))
	if err != nil {
		t.Fatal(err)
	}
	copies := f.SmartCopies(a.Smart.ID)
	if len(copies) != 2 || copies[1].Smart.Offset != image.Pt(200, 0) && copies[0].Smart.Offset != image.Pt(200, 0) {
		t.Fatalf("copies after loading %d", len(copies))
	}
	if c, ok := f.SmartContentOf(a.Smart.ID); !ok || c.Version != changes[0].Version {
		t.Errorf("the loaded picture's shared layers %+v, want version %d; copies %+v %+v", c, changes[0].Version, *copies[0].Smart, *copies[1].Smart)
	}
}

// A copy moved with its artboard keeps what it shares.
func TestSmartMovesWithItsBoard(t *testing.T) {
	d, _, b := smartPicture(t)
	board, err := d.AddBoard(image.Rect(190, 0, 300, 100), "Board")
	if err != nil || d.BoardOf(b.ID) != board.ID {
		t.Fatalf("the copy should go into the board: %v", err)
	}
	d.SmartChanges()
	if err := d.MoveBoard(board.ID, 0, 100); err != nil {
		t.Fatal(err)
	}
	if len(d.SmartChanges()) != 0 || d.Node(b.ID).Smart.Offset != image.Pt(200, 100) {
		t.Errorf("moving the board: offset %v", d.Node(b.ID).Smart.Offset)
	}
}

// A link id is nine letters and digits, its first a letter, read in either case.
func TestLinkIDs(t *testing.T) {
	seen := map[string]bool{}
	for range 2000 {
		id := newLinkID()
		if !validLinkID(id) || seen[id] {
			t.Fatalf("id %q", id)
		}
		seen[id] = true
	}
	for in, want := range map[string]bool{"a1b2c3d4e": true, " K00000000 ": true, "1ABCDEFGH": false, "ABCDEFGH": false, "ABCDEFGHIJ": false, "ABCD-FGHI": false} {
		if id, ok := NormalLinkID(in); ok != want || ok && !validLinkID(id) {
			t.Errorf("%q: %q, %v", in, id, ok)
		}
	}
}

// A smart object's own file holds its layers at the top level; a change made in it is offered as any copy's.
func TestSmartObjectFile(t *testing.T) {
	d, a, _ := smartPicture(t)
	c, _ := d.SmartContentOf(a.Smart.ID)
	f := SmartFile(c)
	if f.SmartSource() != a.Smart.ID || len(f.Root.Children) != 1 || f.Root.Children[0].Name != "Square" || f.Node(f.Active).IsGroup() {
		t.Fatalf("the file's layers %+v", f.Root.Children)
	}
	var buf bytes.Buffer
	if err := f.Snapshot().Save(&buf); err != nil {
		t.Fatal(err)
	}
	g, err := Load(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil || g.SmartSource() != a.Smart.ID || g.SmartName() != "Badge" {
		t.Fatalf("loaded %q %q, %v", g.SmartSource(), g.SmartName(), err)
	}
	// An adjustment layer added in the file is a change to the shared layers.
	if _, err := g.AddAdjustment(Adjustment{Kind: AdjustInvert}, "Invert"); err != nil {
		t.Fatal(err)
	}
	changes := g.SmartChanges()
	if len(changes) != 1 || len(changes[0].Layers) != 2 || changes[0].Version <= c.Version {
		t.Fatalf("changes %+v", changes)
	}
	if !d.ApplySmart(changes[0]) {
		t.Fatal("the picture did not take the file's change")
	}
	for _, n := range d.SmartCopies(a.Smart.ID) {
		if len(n.Children) != 2 || n.Children[0].Adjust == nil {
			t.Errorf("copy %d holds %d layers", n.ID, len(n.Children))
		}
	}
	if _, err := g.AddBoard(image.Rect(0, 0, 10, 10), "B"); err != ErrSmartBoard {
		t.Errorf("a board in a smart object's file: %v", err)
	}
}

// A picture whose only layers are in a smart object, emptied elsewhere, keeps working: nothing is chosen that is
// not there.
func TestSmartObjectEmptiedElsewhere(t *testing.T) {
	d := New("Only")
	id, _ := d.ConvertToSmart("Group")
	c, _ := d.SmartContentOf(id)
	c.Layers, c.Version = nil, c.Version+1
	if !d.ApplySmart(c) {
		t.Fatal("the emptied layers were not taken")
	}
	if n := d.Node(d.Active); n == nil {
		t.Fatal("nothing is chosen")
	}
	d.Undo()
	d.Redo()
	if err := d.Delete(); err == nil && len(d.Root.Children) == 0 {
		t.Error("the picture lost everything")
	}
	d.Composite(image.Rect(0, 0, 10, 10))
}
