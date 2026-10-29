package ui

import (
	"testing"

	"github.com/btelolabs/btelo-forge/internal/mind"
)

// mindLaunchMap makes a map as a plan is drawn: a central topic with four branches of two topics each, a table
// beside it at the bottom right, where the overview floats, and a free topic at the top.
func mindLaunchMap(t *testing.T, x *harness) (*mindDoc, mind.ID) {
	t.Helper()
	if err := x.a.newMindMap(); err != nil {
		t.Fatal(err)
	}
	md := x.a.board
	m := md.m
	root := md.chosen()[0]
	m.Begin("test")
	for _, name := range []string{"Design", "Video", "Marketing", "Schedule"} {
		b, err := m.Add(mind.Node{Text: name}, root, -1)
		if err != nil {
			t.Fatal(err)
		}
		for _, leaf := range []string{"one", "two"} {
			if _, err := m.Add(mind.Node{Text: name + " " + leaf}, b, -1); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := m.Add(mind.Node{Kind: mind.Table, X: 520, Y: 260, Cells: &mind.Grid{Rows: 4, Cols: 2, Cells: make([]string, 8), ColW: make([]float64, 2)}}, 0, -1); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add(mind.Node{Text: "Notes", X: -200, Y: -380}, 0, -1); err != nil {
		t.Fatal(err)
	}
	m.Commit()
	return md, root
}

// Showing the whole board fits it in what the floating bars leave: no node is under the zoom bar or the overview.
func TestMindFitLeavesTheFloatingBarsClear(t *testing.T) {
	x := newHarness(t)
	md, _ := mindLaunchMap(t, x)
	md.selectOnly(0)
	md.fit = true
	x.settleMind()
	r := x.a.layout().body
	c := x.a.mindChromeRects(md, r)
	v := md.screenView(r)
	for _, n := range md.m.Visible() {
		box := v.ScreenRect(md.m.Box(n))
		for name, bar := range map[string]struct{ x, y, w, h float32 }{
			"zoom bar": {c.zoom.X, c.zoom.Y, c.zoom.W, c.zoom.H},
			"overview": {c.overview.X, c.overview.Y, c.overview.W, c.overview.H},
		} {
			if bar.w > 0 && box.X < bar.x+bar.w && bar.x < box.X+box.W && box.Y < bar.y+bar.h && bar.y < box.Y+box.H {
				t.Errorf("%q at %+v is under the %s", n.Text, box, name)
			}
		}
	}
	x.shot("mind-fit-clear")
}

// The style bar of the chosen topic goes above it or below it, wherever it hides fewer of the other topics: on the
// central topic of a map, whose branches sit beside it, it hides none.
func TestMindStyleBarHidesNoOtherTopic(t *testing.T) {
	x := newHarness(t)
	md, root := mindLaunchMap(t, x)
	md.fit = true
	x.settleMind()
	md.selectOnly(root)
	x.frame(Input{})
	r := x.a.layout().body
	c := x.a.mindChromeRects(md, r)
	if c.style.W == 0 {
		t.Fatal("no style bar for the chosen topic")
	}
	if n := x.a.mindCovered(md, r, c.style); n != 0 {
		t.Fatalf("the style bar at %+v hides %d other topics", c.style, n)
	}
	x.shot("mind-style-bar")
}
