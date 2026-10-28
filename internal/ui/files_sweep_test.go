package ui

import (
	"slices"
	"sort"
	"testing"
	"time"
)

// rowY is the middle of a list's row, below the tab strip and the folder's bar.
func (x *harness) rowY(row int) float32 {
	return x.a.u.px(40) + x.a.u.px(38) + x.a.u.fileRowH()*(float32(row)+0.5)
}

// rightDown presses the right button where the pointer is; rightUp lets it go.
func (x *harness) rightDown() {
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my, Right: true, Down: true}}})
}

func (x *harness) rightUp() { x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my, Right: true}}}) }

// markedNames are a list's marked items, sorted.
func markedNames(p *filesPane) []string {
	var out []string
	for n := range p.marked {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func TestFilesRightSweepMarksARun(t *testing.T) {
	x, _ := filesHarness(t, map[string]string{"1.txt": "", "2.txt": "", "3.txt": "", "4.txt": "", "5.txt": ""})
	p := x.a.files.pane()
	x.mx = 300

	// Pressed on the second row and swept down to the fourth, the three are marked.
	x.my = x.rowY(1)
	x.frame(Input{})
	x.rightDown()
	for _, row := range []int{2, 3} {
		x.my = x.rowY(row)
		x.frame(Input{})
	}
	if got := markedNames(p); !slices.Equal(got, []string{"2.txt", "3.txt", "4.txt"}) {
		t.Fatalf("sweeping rows 2 to 4 marked %v", got)
	}
	// Coming back a row gives the row left its mark from before.
	x.my = x.rowY(2)
	x.frame(Input{})
	if got := markedNames(p); !slices.Equal(got, []string{"2.txt", "3.txt"}) {
		t.Fatalf("coming back to row 3 left %v marked", got)
	}
	x.rightUp()
	if x.a.files.sweep != nil || x.a.menu != nil {
		t.Fatalf("letting go left the sweep %v and the menu %v", x.a.files.sweep, x.a.menu)
	}
	// Moving with the button up marks nothing more.
	x.my = x.rowY(4)
	x.frame(Input{})
	if got := markedNames(p); !slices.Equal(got, []string{"2.txt", "3.txt"}) {
		t.Fatalf("moving after letting go marked %v", got)
	}

	// A sweep begun on a marked item unmarks, up past the first row.
	x.my = x.rowY(2)
	x.frame(Input{})
	x.rightDown()
	x.my = x.rowY(0) - x.a.u.fileRowH()/2 - 2 // onto the folder's bar above the rows
	x.frame(Input{})
	x.rightUp()
	if got := markedNames(p); len(got) != 0 {
		t.Fatalf("sweeping up from a marked row left %v marked", got)
	}
	if p.cursor != 0 {
		t.Errorf("the sweep left the cursor on %d", p.cursor)
	}
}

func TestFilesRightClickFlipsAMark(t *testing.T) {
	x, _ := filesHarness(t, map[string]string{"1.txt": "", "2.txt": ""})
	p := x.a.files.pane()
	x.mx, x.my = 300, x.rowY(1)
	x.frame(Input{})
	x.rightDown()
	x.rightUp()
	if got := markedNames(p); !slices.Equal(got, []string{"2.txt"}) || x.a.menu != nil {
		t.Fatalf("a right click marked %v, menu %v", got, x.a.menu)
	}
	x.rightDown()
	x.rightUp()
	if len(p.marked) != 0 {
		t.Fatalf("a second right click left %v marked", markedNames(p))
	}
}

func TestFilesRightButtonHeldOpensTheMenu(t *testing.T) {
	x, _ := filesHarness(t, map[string]string{"1.txt": "", "2.txt": "", "3.txt": ""})
	p := x.a.files.pane()
	p.marked["3.txt"] = true
	x.mx, x.my = 300, x.rowY(1)
	x.frame(Input{})
	x.rightDown()
	if x.a.menu != nil {
		t.Fatal("the menu opened as the button went down")
	}
	x.a.files.sweep.began = x.a.files.sweep.began.Add(-sweepHold)
	x.frame(Input{})
	if x.a.menu == nil {
		t.Fatal("holding the button still opened no menu")
	}
	if got := markedNames(p); !slices.Equal(got, []string{"3.txt"}) || p.cursor != 1 {
		t.Fatalf("the menu's press left %v marked and the cursor on %d", got, p.cursor)
	}
	x.rightUp()
	if x.a.menu == nil {
		t.Error("letting go closed the menu")
	}
}

func TestFilesRightSweepScrollsPastTheEdge(t *testing.T) {
	layout := map[string]string{}
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t",
		"u", "v", "w", "x", "y", "z", "za", "zb", "zc", "zd", "ze", "zf", "zg", "zh", "zi", "zj", "zk", "zl", "zm", "zn"} {
		layout[n+".txt"] = ""
	}
	x, _ := filesHarness(t, layout)
	p := x.a.files.pane()
	x.mx, x.my = 300, x.rowY(0)
	x.frame(Input{})
	x.rightDown()
	x.my = float32(x.c.Img.Rect.Dy()) + 40 // below the window
	for p.cursor < len(p.rows)-1 {
		x.a.files.sweep.ticked = x.a.files.sweep.ticked.Add(-100 * time.Millisecond)
		x.frame(Input{})
		if x.a.files.sweep == nil {
			t.Fatal("the sweep ended below the list")
		}
	}
	x.rightUp()
	if p.top == 0 || len(p.marked) != len(p.rows) {
		t.Fatalf("sweeping below the list scrolled to %v and marked %d of %d", p.top, len(p.marked), len(p.rows))
	}
}

func TestFilesRightSweepInTheGrid(t *testing.T) {
	x, _ := filesHarness(t, map[string]string{"1.txt": "", "2.txt": "", "3.txt": "", "4.txt": ""})
	fd := x.a.files
	p := fd.pane()
	x.press('Q', "")
	x.waitFiles()
	if !p.grid {
		t.Fatal("q showed no grid")
	}
	// The first card is where pointing first puts the hover on an item, going down the left of the grid.
	x.mx = x.a.u.px(60)
	found := false
	for y := x.a.u.px(80); y < float32(x.c.Img.Rect.Dy()); y += 6 {
		x.my = y
		x.frame(Input{})
		if fd.hovered != "" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no card under the left of the grid")
	}
	first := p.cursor
	x.rightDown()
	if fd.sweep == nil || len(p.marked) != 1 {
		t.Fatalf("a right press on a card began no sweep, marked %v", markedNames(p))
	}
	for p.cursor == first && x.mx < float32(x.c.Img.Rect.Dx()) {
		x.mx += 10
		x.frame(Input{})
	}
	x.rightUp()
	if len(p.marked) != 2 {
		t.Fatalf("sweeping to the next card marked %v", markedNames(p))
	}
}
