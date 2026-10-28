package paint

import (
	"bytes"
	"image"
	"image/color"
	"reflect"
	"testing"
)

func TestArtboards(t *testing.T) {
	d := New("Layer 1")
	d.Paper = false
	d.Select(image.Rect(150, 120, 170, 140))
	d.Fill(color.NRGBA{R: 255, A: 255})
	d.Deselect()
	outside := d.AddVector(Vector{Kind: VectorRect, Size: Pt{20, 20}, M: Translate(0, 0), Fill: solidBlue}, "Outside")
	b, err := d.AddBoard(image.Rect(100, 100, 300, 200), "Banner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddBoard(image.Rect(0, 0, 0, 10), "Flat"); err != ErrEmpty {
		t.Errorf("a board with no width: %v", err)
	}
	// It takes in what lies wholly on it, and nothing else; it is chosen, so new layers go into it.
	board := d.Node(b.ID)
	if len(board.Children) != 1 || board.Children[0].Name != "Layer 1" || d.BoardOf(outside.ID) != 0 || d.Active != b.ID {
		t.Fatalf("the new board holds %d, active %d", len(board.Children), d.Active)
	}
	across := d.AddVector(Vector{Kind: VectorRect, Size: Pt{200, 20}, M: Translate(250, 150), Fill: solidRed}, "Across")
	if d.BoardOf(across.ID) != b.ID {
		t.Fatal("a layer added in a board goes into it")
	}
	at := func(x, y int) color.RGBA { return d.Composite(image.Rect(x, y, x+1, y+1)).RGBAAt(x, y) }
	// Its background is under what is in it, and what is in it is cut to it.
	if c := at(101, 101); c != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("the background %v", c)
	}
	if c := at(290, 155); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("the shape in the board %v", c)
	}
	if c := at(310, 155); c.A != 0 {
		t.Errorf("the shape past the board's edge shows %v", c)
	}
	if c := at(5, 5); c != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("a layer outside every board %v", c)
	}
	// It exports with its background and only what is in it.
	img, err := d.BoardPicture(b.ID)
	if err != nil || img.Rect != b.Rect {
		t.Fatalf("picture %v, %v", img.Rect, err)
	}
	if c := img.RGBAAt(160, 130); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("what is on the board %v", c)
	}
	// Moving it carries what is in it, and only that.
	if err := d.MoveBoard(b.ID, 1000, 10); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.Board(b.ID); got.Rect != image.Rect(1100, 110, 1300, 210) {
		t.Errorf("moved board %v", got.Rect)
	}
	if m := d.Node(across.ID).Vector.M; m.C != 1250 || m.F != 160 {
		t.Errorf("the shape in it is at %v, %v", m.C, m.F)
	}
	if m := d.Node(outside.ID).Vector.M; m.C != 0 {
		t.Errorf("the shape outside moved to %v", m.C)
	}
	if c := at(1160, 140); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("the pixels in it did not move with it: %v", c)
	}
	if steps, _ := d.History(); steps[len(steps)-1] != "Move artboard" {
		t.Errorf("steps %v", steps)
	}
	d.Undo()
	if got, _ := d.Board(b.ID); got.Rect != b.Rect || d.Node(across.ID).Vector.M.C != 250 {
		t.Errorf("undo left the board at %v", got.Rect)
	}
	// It stays at the top level: it is neither grouped nor merged, and steps past an open group.
	d.Choose(b.ID)
	if _, err := d.GroupChosen("G"); err != ErrBoardInside {
		t.Errorf("grouping a board: %v", err)
	}
	if err := d.MergeGroup(); err != ErrMergeBoard {
		t.Errorf("merging a board: %v", err)
	}
	d.Choose(outside.ID)
	g, _ := d.GroupChosen("Open group")
	d.Choose(b.ID)
	for range 3 {
		d.Move(false)
	}
	if !d.boardsAtTop() || d.Node(g.ID).Children[0].ID != outside.ID {
		t.Error("moving a board took it into a group")
	}
	// Renaming, a transparent background, then deleting, which leaves what was in it.
	b.Name, b.Fill = "Hero", color.NRGBA{}
	if err := d.SetBoard(b, "Artboard"); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.Board(b.ID); got.Name != "Hero" || d.BoardAt(image.Pt(110, 110)) != b.ID || d.BoardAt(image.Pt(10, 10)) != 0 {
		t.Errorf("board %+v", got)
	}
	if c := at(101, 101); c.A != 0 {
		t.Errorf("a clear board's background %v", c)
	}
	if err := d.DeleteBoard(b.ID); err != nil || len(d.Boards()) != 0 {
		t.Fatalf("delete: %v, %d left", err, len(d.Boards()))
	}
	if n := d.Node(across.ID); n == nil || d.BoardOf(across.ID) != 0 {
		t.Error("deleting a board should keep what was in it")
	}
	if c := at(310, 155); c != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("out of the board the shape shows whole: %v", c)
	}
	d.Undo()
	if bs := d.Boards(); len(bs) != 1 || bs[0].Name != "Hero" {
		t.Errorf("undo of the delete: %+v", bs)
	}
	// Resizing the image scales the boards with it.
	d.Frame = image.Rect(0, 0, 400, 300)
	if err := d.ResizeImage(800, 600); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.Board(b.ID); got.Rect != image.Rect(200, 200, 600, 400) {
		t.Errorf("the board after doubling %v", got.Rect)
	}
}

// Artboards saved by versions 4 and 5, apart from the layers, become groups of the top-level layers lying on them,
// under everything else.
func TestOldArtboardsBecomeGroups(t *testing.T) {
	d := New("Background")
	d.Select(image.Rect(10, 10, 20, 20))
	d.Fill(color.NRGBA{R: 255, A: 255})
	d.Deselect()
	d.AddVector(Vector{Kind: VectorRect, Size: Pt{500, 20}, M: Translate(0, 50), Fill: solidBlue}, "Wide")
	s := d.Snapshot()
	s.doc.Version = 5
	s.doc.NextID = 100
	s.doc.Boards = []fileBoard{{ID: 50, Name: "Lower", Rect: [4]int{0, 0, 100, 100}, Fill: [4]uint8{255, 255, 255, 255}},
		{ID: 51, Name: "Upper", Rect: [4]int{200, 0, 300, 100}}}
	var buf bytes.Buffer
	if err := s.Save(&buf); err != nil {
		t.Fatal(err)
	}
	e, err := Load(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	bs := e.Boards()
	if len(bs) != 2 || bs[0].ID != 50 || bs[1].ID != 51 || bs[0].Rect != image.Rect(0, 0, 100, 100) {
		t.Fatalf("boards %+v", bs)
	}
	top := e.Root.Children
	if !top[len(top)-1].IsBoard() || top[len(top)-1].ID != 50 || !top[len(top)-2].IsBoard() {
		t.Error("the boards should be at the bottom, the lowest last")
	}
	if lower := e.Node(50); len(lower.Children) != 1 || lower.Children[0].Name != "Background" {
		t.Errorf("the lower board holds %+v", lower.Children)
	}
	if e.BoardOf(e.Layers()[0].ID) != 0 {
		t.Error("the wide shape reaches past the boards and stays out of them")
	}
	if n := e.AddLayer("New"); n.ID <= 100 {
		t.Errorf("a new layer took id %d", n.ID)
	}
}

// TestTheFileKeepsEveryDrawing saves a picture with each kind of thing a drawing holds and checks it comes back
// whole: the history is all a file leaves out.
func TestTheFileKeepsEveryDrawing(t *testing.T) {
	d := New("Pixels")
	d.Select(image.Rect(-40, 10, 60, 30))
	d.Fill(color.NRGBA{G: 200, A: 255})
	d.Deselect()
	d.AddMask()
	d.EditMask = false
	curve := Vector{Kind: VectorCurve, M: Affine{A: 1.5, B: 0.2, C: 30, D: -0.1, E: 0.8, F: 40}, Rule: EvenOdd,
		Path: []Subpath{{Closed: true, Nodes: []PathNode{
			{P: Pt{0, 0}, In: Pt{-5, 2}, Out: Pt{5, -2}, Smooth: true}, {P: Pt{40, 0}, In: Pt{40, 0}, Out: Pt{40, 0}},
			{P: Pt{20, 30}, In: Pt{30, 30}, Out: Pt{10, 30}},
		}}},
		Fill: VectorPaint{Kind: PaintLinear, From: Pt{0, 0}, To: Pt{40, 0},
			Stops: []Stop{{At: 0, Color: color.NRGBA{R: 255, A: 255}}, {At: 1, Color: color.NRGBA{B: 255, A: 128}}}},
		Stroke: VectorStroke{Paint: solidRed, Width: 3.5, Align: AlignOutside, Cap: CapSquare, Join: JoinBevel, Miter: 6, Dash: []float64{3, 2}}}
	d.AddVector(curve, "Curve")
	star := d.AddVector(Vector{Kind: VectorStar, Size: Pt{50, 50}, Sides: 7, Inner: 0.4, M: Translate(-100, -100), Fill: solidRed}, "Star")
	d.Node(star.ID).Opacity, d.Node(star.ID).Blend, d.Node(star.ID).Visible = 0.5, BlendMultiply, false
	d.AddGroup("Group")
	if _, err := d.AddBoard(image.Rect(-50, -50, 150, 100), "Square"); err != nil {
		t.Fatal(err)
	}
	b, _ := d.AddBoard(image.Rect(500, 0, 600, 80), "Clear")
	b.Fill = color.NRGBA{}
	d.SetBoard(b, "Artboard")
	d.Frame = image.Rect(0, 0, 64, 48)
	d.Select(image.Rect(5, 5, 9, 9))

	var buf bytes.Buffer
	if err := d.Snapshot().Save(&buf); err != nil {
		t.Fatal(err)
	}
	e, err := Load(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e.Boards(), d.Boards()) || e.Frame != d.Frame || e.Paper != d.Paper || e.Active != d.Active {
		t.Errorf("boards %+v, frame %v, paper %v, active %d", e.Boards(), e.Frame, e.Paper, e.Active)
	}
	if len(e.Layers()) != len(d.Layers()) {
		t.Fatalf("%d layers, want %d", len(e.Layers()), len(d.Layers()))
	}
	for i, l := range d.Layers() {
		m := e.Layers()[i]
		if m.ID != l.ID || m.Name != l.Name || m.Opacity != l.Opacity || m.Blend != l.Blend || m.Visible != l.Visible ||
			(m.Mask == nil) != (l.Mask == nil) || !reflect.DeepEqual(m.Vector, l.Vector) {
			t.Errorf("layer %q came back as %+v", l.Name, m)
		}
	}
	if g := e.Node(d.Active); g == nil || !g.IsGroup() {
		t.Error("the group did not come back")
	}
	all := image.Rect(-150, -150, 700, 200)
	if !bytes.Equal(e.Composite(all).Pix, d.Composite(all).Pix) {
		t.Error("the loaded picture looks different")
	}
	if e.Selection == nil || e.Selection.At(6, 6) != 255 || e.Selection.At(20, 20) != 0 {
		t.Error("the selection did not come back")
	}
	if steps, _ := e.History(); len(steps) != 0 {
		t.Errorf("a loaded picture has history: %v", steps)
	}
	// New layers and boards after loading do not take an id already used.
	if n := e.AddLayer("New"); n.ID <= b.ID {
		t.Errorf("a new layer took id %d", n.ID)
	}
}
