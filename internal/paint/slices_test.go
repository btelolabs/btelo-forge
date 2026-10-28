package paint

import (
	"errors"
	"image"
	"image/color"
	"testing"
)

// The names of the Krita named-assets plugin, marked by .png: folders, a size after the name, folder groups whose
// size and folders what is directly in them takes.
func TestSliceNames(t *testing.T) {
	d := New("Background")
	add := func(name string) *Node { return d.AddLayer(name) }
	group := func(name string, kids ...string) *Node {
		g := d.AddGroup(name)
		for _, k := range kids {
			d.Choose(g.ID)
			n := d.newLayer(k)
			g.Children = append([]*Node{n}, g.Children...)
		}
		return g
	}
	plain := add("Layer 2")
	fish := add("assets/fish.png")
	button := add("ui/button_100x40.png")
	sized := add("200x300/assets/cat.png")
	folder := group("200x300/assets", "dog", "eel_50x50", "Fin.PNG")
	d.Choose(fish.ID) // what is added next goes beside it, not into the folder group
	bad := add("../up.png")

	byName := map[string]Slice{}
	for _, s := range d.Slices() {
		byName[s.Name] = s
	}
	if _, ok := byName[plain.Name]; ok {
		t.Error("an unmarked layer outside a folder group is not a slice")
	}
	for _, c := range []struct {
		name string
		path string
		size image.Point
		from SizeFrom
	}{
		{fish.Name, "assets/fish.png", image.Point{}, SizeOwn},
		{button.Name, "ui/button.png", image.Pt(100, 40), SizeInName},
		{sized.Name, "assets/cat.png", image.Pt(200, 300), SizeInPath},
		{"dog", "assets/dog.png", image.Pt(200, 300), SizeInFolder},
		{"eel_50x50", "assets/eel.png", image.Pt(50, 50), SizeInName},
		{"Fin.PNG", "assets/Fin.png", image.Pt(200, 300), SizeInFolder},
	} {
		s, ok := byName[c.name]
		if !ok || s.Role != SliceFile || s.Err != nil || s.Path != c.path || s.Size != c.size || s.From != c.from {
			t.Errorf("%s: %+v, want %s %v from %d", c.name, s, c.path, c.size, c.from)
		}
	}
	if s := byName[folder.Name]; s.Role != SliceFolder || s.Path != "assets/" || s.Size != image.Pt(200, 300) {
		t.Errorf("the folder group: %+v", s)
	}
	var se *SliceError
	if s := byName[bad.Name]; !errors.As(s.Err, &se) || se.Problem != SliceDots {
		t.Errorf("../up.png: %v", s.Err)
	}
	// Two files on one path: the second one listed is refused.
	d.Choose(fish.ID)
	add("Assets/FISH.png")
	dup := 0
	for _, s := range d.Slices() {
		if errors.As(s.Err, &se) && se.Problem == SliceDuplicate {
			dup++
		}
	}
	if dup != 1 {
		t.Errorf("%d duplicates found, want 1", dup)
	}
}

func TestSliceNameProblems(t *testing.T) {
	for name, want := range map[string]SliceProblem{
		"/abs.png":       SliceAbsolute,
		`a\b.png`:        SliceAbsolute,
		"a//b.png":       SliceEmptyPart,
		"a /b.png":       SliceSpaces,
		"a?.png":         SliceBadChar,
		"con.png":        SliceReserved,
		"300x200.png":    SliceSizeOnly,
		"x_0x10.png":     SliceTooBig,
		"x_20000x10.png": SliceTooBig,
	} {
		d := New("Background")
		d.AddLayer(name)
		s, _ := d.SliceOf(d.Active)
		var se *SliceError
		if !errors.As(s.Err, &se) || se.Problem != want {
			t.Errorf("%q: %v, want problem %d", name, s.Err, want)
		}
	}
}

// K marks and unmarks the chosen nodes, as one step.
func TestMarkSlices(t *testing.T) {
	d := New("Background")
	a := d.AddLayer("Fish")
	b := d.AddLayer("cat.png")
	d.MarkSlices([]int{a.ID, b.ID}, true)
	if a.Name != "Fish.png" || b.Name != "cat.png" || d.UndoName() != "Mark as slice" {
		t.Fatalf("marked %q %q (%q)", a.Name, b.Name, d.UndoName())
	}
	d.MarkSlices([]int{a.ID, b.ID}, false)
	if d.Node(a.ID).Name != "Fish" || d.Node(b.ID).Name != "cat" || d.UndoName() != "Unmark as slice" {
		t.Fatalf("unmarked %q %q", d.Node(a.ID).Name, d.Node(b.ID).Name)
	}
	d.Undo()
	if d.Node(a.ID).Name != "Fish.png" {
		t.Errorf("undone to %q", d.Node(a.ID).Name)
	}
}

// A slice is the node alone cut to what it shows; a size pads or crops about its centre, never scales; an artboard
// is its frame; hidden slices are left out.
func TestSlicePictures(t *testing.T) {
	d := New("Background")
	red := color.NRGBA{R: 255, A: 255}
	n := d.AddLayer("dot.png")
	d.Select(image.Rect(10, 20, 13, 24)) // 3×4
	if err := d.Fill(red); err != nil {
		t.Fatal(err)
	}
	d.Deselect()
	s, _ := d.SliceOf(n.ID)
	img, err := d.SlicePicture(s)
	if err != nil || img.Rect != image.Rect(0, 0, 3, 4) || img.RGBAAt(1, 1).R != 255 {
		t.Fatalf("cut to what it shows: %v %v", img.Rect, err)
	}
	d.Rename(n.ID, "dot_8x2.png")
	s, _ = d.SliceOf(n.ID)
	img, err = d.SlicePicture(s)
	if err != nil || img.Rect != image.Rect(0, 0, 8, 2) {
		t.Fatalf("sized: %v %v", img.Rect, err)
	}
	// 3 wide in 8: 2 transparent on the left; 4 high in 2: one row cropped off the top.
	if img.RGBAAt(1, 0).A != 0 || img.RGBAAt(2, 0).R != 255 || img.RGBAAt(4, 1).R != 255 || img.RGBAAt(5, 0).A != 0 {
		t.Errorf("not centred: row 0 %v %v %v %v", img.RGBAAt(1, 0), img.RGBAAt(2, 0), img.RGBAAt(4, 1), img.RGBAAt(5, 0))
	}
	empty := d.AddLayer("empty.png")
	if s, _ := d.SliceOf(empty.ID); s.Err == nil {
		if _, err := d.SlicePicture(s); !errors.Is(err, ErrSliceEmpty) {
			t.Errorf("an empty slice: %v", err)
		}
	}
	d.SetVisible(n.ID, false)
	if s, _ := d.SliceOf(n.ID); !s.Hidden {
		t.Error("a hidden slice should say so")
	}

	board, err := d.AddBoard(image.Rect(100, 100, 401, 350), "card.png")
	if err != nil {
		t.Fatal(err)
	}
	s, _ = d.SliceOf(board.ID)
	img, err = d.SlicePicture(s)
	if err != nil || !s.Board || img.Rect.Size() != image.Pt(301, 250) {
		t.Errorf("the artboard: %+v %v %v", s, img.Rect, err)
	}
}
