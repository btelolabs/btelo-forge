package mind

import (
	"bytes"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := sampleBoard(t, SimpleMeasurer{})
	pic := filepath.Join(dir, "pics", "a.png")
	far := filepath.Join(os.TempDir(), "elsewhere", "b.png")
	m.Add(Node{Kind: Picture, Ref: pic, X: 10, Y: 20, Aspect: 1.5}, 0, -1)
	m.Add(Node{Kind: Picture, Ref: far, X: 10, Y: 20}, 0, -1)
	m.Add(Node{Kind: Smart, Ref: "ABCDEFGH1", X: 30, Y: 40, Fill: gfx.Hex(0x123456), Bold: true, Size: 18.5}, 0, -1)
	folded, _ := m.Add(Node{Text: "folded", Folded: true, Side: SideLeft}, 1, 0)
	m.Add(Node{Text: "hidden"}, folded, -1)
	m.Update(folded, func(n *Node) { n.Folded = true })
	m.Delete(3) // a gap in the ids is kept
	data, err := Encode(m, dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(bytes.NewReader(data), dir, SimpleMeasurer{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != m.Title || got.Len() != m.Len() || got.EdgeCount() != m.EdgeCount() {
		t.Fatalf("title %q, %d nodes, %d connectors; want %q, %d, %d", got.Title, got.Len(), got.EdgeCount(), m.Title, m.Len(), m.EdgeCount())
	}
	m.Each(func(n *Node) bool {
		g := got.Node(n.ID)
		if g == nil {
			t.Fatalf("node %d is missing", n.ID)
		}
		a, b := *n.saved(), *g.saved()
		if a.Text != b.Text || a.Parent != b.Parent || len(a.Children) != len(b.Children) || a.Ref != b.Ref || a.Kind != b.Kind ||
			a.Shape != b.Shape || a.Fill != b.Fill || a.Bold != b.Bold || a.Size != b.Size || a.Folded != b.Folded ||
			a.Side != b.Side || a.Layout != b.Layout || a.Aspect != b.Aspect || (a.Parent == 0 && (a.X != b.X || a.Y != b.Y)) {
			t.Fatalf("node %d read back as\n%+v\nwant\n%+v", n.ID, b, a)
		}
		for i := range a.Children {
			if a.Children[i] != b.Children[i] {
				t.Fatalf("node %d's children are in another order: %v, want %v", n.ID, b.Children, a.Children)
			}
		}
		if (a.Cells == nil) != (b.Cells == nil) || a.Cells != nil && strings.Join(a.Cells.Cells, "|") != strings.Join(b.Cells.Cells, "|") {
			t.Fatalf("node %d's cells differ", n.ID)
		}
		return true
	})
	m.EachEdge(func(e *Edge) bool {
		g := got.Edge(e.ID)
		if g == nil || g.From != e.From || g.To != e.To || g.Label != e.Label || g.Dashed != e.Dashed || g.Route != e.Route ||
			g.FromSide != e.FromSide || g.ToSide != e.ToSide || g.Arrow != e.Arrow {
			t.Fatalf("connector %d read back as %+v, want %+v", e.ID, g, e)
		}
		return true
	})
	// A file under the map's folder is saved relative to it, so it moves with the folder; one elsewhere stays.
	moved := filepath.Join(t.TempDir(), "moved")
	again, err := Load(bytes.NewReader(data), moved, SimpleMeasurer{})
	if err != nil {
		t.Fatal(err)
	}
	refs := again.Refs(Picture)
	if !slicesContain(refs, filepath.Join(moved, "pics", "a.png")) || !slicesContain(refs, far) {
		t.Fatalf("pictures read from a moved folder: %v", refs)
	}
	if got.Unsaved() {
		t.Fatal("a map just read claims to have changes")
	}
}

func TestLoadRefusesDamage(t *testing.T) {
	if _, err := Load(strings.NewReader("not a map"), "", SimpleMeasurer{}); err == nil {
		t.Fatal("read garbage")
	}
	data, _ := Encode(sampleBoard(t, SimpleMeasurer{}), "")
	for _, cut := range []int{8, len(data) / 2, len(data) - 3} {
		if _, err := Load(bytes.NewReader(data[:cut]), "", SimpleMeasurer{}); err == nil {
			t.Fatalf("a file cut at %d of %d bytes was read", cut, len(data))
		}
	}
}

// TestFilesAreSmall: a map of 100 000 topics saves in a few bytes each.
func TestFilesAreSmall(t *testing.T) {
	m, _ := bigMap(t, 100_000)
	data, err := Encode(m, "")
	if err != nil {
		t.Fatal(err)
	}
	per := float64(len(data)) / 100_000
	t.Logf("100 000 topics: %d bytes, %.1f a topic", len(data), per)
	if per > 12 {
		t.Fatalf("%.1f bytes a topic", per)
	}
	start := time.Now()
	got, err := Load(bytes.NewReader(data), "", SimpleMeasurer{})
	if err != nil || got.Len() != 100_000 {
		t.Fatalf("read back %v nodes: %v", got, err)
	}
	t.Logf("read in %v", time.Since(start))
}

func TestOutline(t *testing.T) {
	m := newMap()
	src := "# Plan\n- one\n  - one a\n  - one b\n- two\n\t- two a\n## Later\nplain line\n"
	tops, err := m.AddOutline(src, 0, -1, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tops) != 1 {
		t.Fatalf("%d top topics, want 1", len(tops))
	}
	got := m.Outline(tops...)
	want := "- Plan\n  - one\n    - one a\n    - one b\n  - two\n    - two a\n  - Later\n    - plain line\n"
	if got != want {
		t.Fatalf("outline\n%s\nwant\n%s", got, want)
	}
	if !IsOutline("- a\n  - b") || IsOutline("just words\nover two lines") {
		t.Fatal("IsOutline")
	}
	m.Undo()
	if m.Len() != 0 {
		t.Fatal("pasting an outline is not one step")
	}
	g, ok := ParseTSV("a\tb\n1\t2\n")
	if !ok || g.Rows != 2 || g.Cols != 2 || g.Cell(1, 1) != "2" || TSV(g) != "a\tb\n1\t2\n" {
		t.Fatalf("TSV: %+v", g)
	}
}

// TestNestedCycles: a map holding a map holding the first draws a warning in place of the cycle, and one holding
// itself directly does too; deep chains stop at MaxNest.
func TestNestedCycles(t *testing.T) {
	fonts := systemFonts(t)
	dir := t.TempDir()
	write := func(name string, refs ...string) string {
		m := New(name, FontMeasurer{fonts})
		m.Add(Node{Text: name}, 0, -1)
		for i, r := range refs {
			m.Add(Node{Kind: Nested, Ref: filepath.Join(dir, r), X: float64(i) * 400, Y: 200}, 0, -1)
		}
		p := filepath.Join(dir, name+Ext)
		data, err := Encode(m, dir)
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(p, data, 0o644)
		return p
	}
	a := write("a", "b"+Ext)
	b := write("b", "a"+Ext, "b"+Ext)
	th := DrawNested(b, []string{a}, 300, fonts, Labels{}, nil)
	if th.State != PicReady || th.Img == nil {
		t.Fatalf("b inside a: %v", th.State)
	}
	if !slicesContain(th.Files, b) {
		t.Fatalf("files read %v", th.Files)
	}
	if got := DrawNested(a, []string{a}, 300, fonts, Labels{}, nil); got.State != PicCycle {
		t.Fatalf("a inside itself: %v", got.State)
	}
	// A chain of maps each holding the next stops drawing past MaxNest without error.
	prev := ""
	for i := 9; i >= 0; i-- {
		name := "chain" + string(rune('0'+i))
		if prev == "" {
			prev = write(name)
		} else {
			prev = write(name, filepath.Base(prev))
		}
	}
	if got := DrawNested(prev, nil, 600, fonts, Labels{}, nil); got.State != PicReady {
		t.Fatalf("the top of a long chain: %v", got.State)
	}
	if got := DrawNested(prev, make([]string, MaxNest), 300, fonts, Labels{}, nil); got.State != PicTooDeep {
		t.Fatalf("past the depth: %v", got.State)
	}
	img, err := DrawFile(a, 256, fonts, Labels{}, nil)
	if err != nil || img.Rect.Dx() != 256 && img.Rect.Dy() != 256 {
		t.Fatalf("DrawFile: %v %v", img.Rect, err)
	}
}

func slicesContain(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestTilesMatchDirectDrawing: the view drawn from tiles is the view drawn whole.
func TestTilesMatchDirectDrawing(t *testing.T) {
	fonts := systemFonts(t)
	m := sampleBoard(t, FontMeasurer{fonts})
	m.Layout()
	o := &Options{Fonts: fonts, Pictures: fakePictures{gradient(64, 48)}, Paper: true}
	for _, z := range []float64{1, 0.6, 0.13} {
		b := m.Bounds()
		v := View{Zoom: z, OX: math_round(700 - b.CX()*z), OY: math_round(450 - b.CY()*z)}
		direct := gfx.NewCanvas(1400, 900)
		Render(direct, m, v, o)
		tiled := gfx.NewCanvas(1400, 900)
		ts := NewTiles(200)
		if !ts.Draw(tiled, image.Rect(0, 0, 1400, 900), m, v, o, time.Minute) {
			t.Fatal("the view is not whole with a minute to draw it")
		}
		diff := 0
		for i := range direct.Img.Pix {
			d := int(direct.Img.Pix[i]) - int(tiled.Img.Pix[i])
			if d > 2 || d < -2 {
				diff++
			}
		}
		// Words cut at a tile's edge are drawn the same; only rounding may differ.
		if frac := float64(diff) / float64(len(direct.Img.Pix)); frac > 0.002 {
			t.Fatalf("zoom %v: %.3f%% of the values differ", z, frac*100)
		}
	}
}

func math_round(v float64) float64 { return float64(int64(v)) }

// TestTilesInvalidate: a change draws again only the tiles over it.
func TestTilesInvalidate(t *testing.T) {
	m := sampleBoard(t, SimpleMeasurer{})
	m.Changed()
	v := View{Zoom: 1, OX: 600, OY: 500}
	ts := NewTiles(500)
	c := gfx.NewCanvas(1200, 1000)
	ts.Draw(c, image.Rect(0, 0, 1200, 1000), m, v, &Options{}, time.Minute)
	before := ts.Len()
	m.Update(1, func(n *Node) { n.Text = "Changed" })
	regions, all := m.Changed()
	ts.Invalidate(regions, all)
	if all || ts.Len() == before || ts.Len() == 0 {
		t.Fatalf("%d tiles of %d kept after a small change (all %v)", ts.Len(), before, all)
	}
}
