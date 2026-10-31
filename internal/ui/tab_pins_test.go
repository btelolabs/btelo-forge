package ui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// tabAt is the middle of tab i on the strip.
func (x *harness) tabAt(i int) (float32, float32) {
	u := &x.a.u
	l := x.a.layout().tabs
	if x.a.settings.VerticalTabs {
		return l.X + l.W/2, l.Y + u.px(6) + (float32(i)+0.5)*u.px(36)
	}
	return l.X + u.px(8) + (float32(i)+0.5)*u.px(220), l.Y + l.H/2
}

// rightClick clicks the right button at a point, for one frame down and one up.
func (x *harness) rightClick(px, py float32) {
	x.mx, x.my = px, py
	x.frame(Input{})
	x.frame(Input{Pointer: []Pointer{{X: px, Y: py, Right: true, Down: true}}})
	x.frame(Input{Pointer: []Pointer{{X: px, Y: py, Right: true}}})
}

// menuClick clicks the item of the open menu labelled so.
func (x *harness) menuClick(label string) {
	x.t.Helper()
	m := x.a.menu
	if m == nil {
		x.t.Fatal("no menu is open")
	}
	u := &x.a.u
	rows := u.menuRows(m.items, x.a.menuBelow(m.items, m.anchor))
	for i, it := range m.items {
		if it.label == label {
			x.click(rows[i])
			return
		}
	}
	x.t.Fatalf("the menu has no %q", label)
}

// tabTitles are the tabs' names, in the order of the strip.
func (x *harness) tabTitles() []string {
	var out []string
	for _, d := range x.a.docs {
		out = append(out, d.title(x.a))
	}
	return out
}

// threeTabs opens tabs named A, B and C, as the file manager's folders are named.
func threeTabs(t *testing.T, x *harness) []*doc {
	t.Helper()
	x.a.settings.Language = English
	var docs []*doc
	for _, name := range []string{"A", "B", "C"} {
		dir := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		x.a.newTab()
		x.a.newFilesTab(dir, false)
		docs = append(docs, x.a.doc)
	}
	x.a.closeTab(0)
	x.frame(Input{})
	return docs
}

// Dragged along the strip, or down it, a tab goes between the others and comes forward; a click still only brings a
// tab forward.
func TestDraggingATabReordersTheStrip(t *testing.T) {
	for _, vertical := range []bool{false, true} {
		x := newHarness(t)
		x.a.settings.VerticalTabs = vertical
		docs := threeTabs(t, x)
		ax, ay := x.tabAt(0)
		cx, cy := x.tabAt(2)
		x.drag(ax, ay, cx, cy)
		if want := []*doc{docs[1], docs[2], docs[0]}; !slices.Equal(x.a.docs, want) || x.a.doc != docs[0] {
			t.Fatalf("vertical %v: the order is %v, with %q in front", vertical, x.tabTitles(), x.a.doc.title(x.a))
		}
		if !x.a.tabsMoving() {
			t.Fatal("the tab should slide into its place")
		}
		x.pump(2*time.Second, func() bool { return !x.a.tabsMoving() })
		bx, by := x.tabAt(0)
		x.mx, x.my = bx, by
		x.click(gfx.Rect{X: bx, Y: by})
		if x.a.doc != docs[1] || !slices.Equal(x.a.docs, []*doc{docs[1], docs[2], docs[0]}) {
			t.Fatalf("a click moved a tab, or did not bring it forward: %v", x.tabTitles())
		}
	}
}

// A tab closed while it is held lets go of the mouse.
func TestClosingAHeldTab(t *testing.T) {
	x := newHarness(t)
	docs := threeTabs(t, x)
	ax, ay := x.tabAt(0)
	x.mx, x.my = ax, ay
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: ax, Y: ay, Down: true}}})
	x.a.closeTab(0)
	x.frame(Input{Down: true})
	x.frame(Input{Pointer: []Pointer{{X: ax, Y: ay}}})
	x.frame(Input{})
	if x.a.tabDrag != nil || len(x.a.docs) != len(docs)-1 {
		t.Fatalf("the tab is still held: %+v", x.a.tabDrag)
	}
}

// Pinned from its menu a tab goes to the left of the others and stays among the pinned when dragged; it comes back
// with the app, in its place, with what it held and the tab in front in front.
func TestPinnedTabsComeBackWithTheApp(t *testing.T) {
	x := newHarness(t)
	docs := threeTabs(t, x)
	cx, cy := x.tabAt(2)
	x.rightClick(cx, cy)
	x.menuClick("Pin the tab")
	if !docs[2].pinned || x.a.docs[0] != docs[2] {
		t.Fatalf("the pinned tab did not go left: %v", x.tabTitles())
	}
	x.a.switchTab(1) // A
	if err := x.a.newCanvas(); err != nil {
		t.Fatal(err)
	}
	canvas := x.a.doc
	x.a.pinTab(canvas, true)
	if _, err := os.Stat(canvas.art.path); err != nil {
		t.Fatalf("a pinned new canvas should have a file to open again: %v", err)
	}
	// A pinned tab cannot be dragged among the others, nor another among the pinned.
	ax, ay := x.tabAt(0)
	lastX, lastY := x.tabAt(len(x.a.docs) - 1)
	x.drag(ax, ay, lastX, lastY)
	if x.a.docs[0] != canvas || x.a.docs[1] != docs[2] {
		t.Fatalf("the pinned tabs left their group: %v", x.tabTitles())
	}
	x.a.switchTab(0)
	x.frame(Input{})
	dir := docs[2].files.panes[0].dir
	path := canvas.art.path
	state := x.a.stateDir
	cache := x.a.cacheDir
	host, fonts := x.a.host, x.a.u.fonts
	x.a.Close()

	b := New(host, fonts, cache, state)
	defer b.Close()
	x.a = b
	x.pump(10*time.Second, func() bool {
		return !slices.ContainsFunc(b.docs, func(d *doc) bool { return d.restoring != nil })
	})
	if len(b.docs) != 2 || !b.docs[0].pinned || !b.docs[1].pinned {
		t.Fatalf("the tabs came back as %v", x.tabTitles())
	}
	if b.docs[1].files == nil || b.docs[1].files.panes[0].dir != dir {
		t.Fatalf("the file manager came back at the wrong folder: %+v", b.docs[1])
	}
	if b.docs[0].art == nil || b.docs[0].art.path != path || b.doc != b.docs[0] {
		t.Fatalf("the canvas did not come back in front: %+v", b.docs[0])
	}
}

// What can no longer be read closes its tab, the others keeping their places, and it is not tried again.
func TestAPinnedTabWhoseFileIsGoneCloses(t *testing.T) {
	x := newHarness(t)
	x.a.settings.PinnedTabs = []savedTab{{Kind: tabCanvas, Path: filepath.Join(t.TempDir(), "gone.btart")}, {Kind: tabKeyboard, Front: true}}
	x.a.docs, x.a.doc = nil, nil
	x.a.restoreTabs()
	x.pump(10*time.Second, func() bool { return len(x.a.docs) == 1 })
	if !x.a.docs[0].keyboardTab || !x.a.docs[0].pinned || len(x.a.settings.PinnedTabs) != 1 {
		t.Fatalf("left %v, remembering %+v", x.tabTitles(), x.a.settings.PinnedTabs)
	}
}

// A file opened while its pinned tab is still being read brings that tab forward rather than a second one.
func TestOpeningAFileThatIsComingBack(t *testing.T) {
	x := newHarness(t)
	path := filepath.Join(t.TempDir(), "held.btart")
	x.a.docs, x.a.doc = nil, nil
	x.a.docs = []*doc{{}, {restoring: &savedTab{Kind: tabCanvas, Path: path}, opening: path, pinned: true}}
	x.a.doc = x.a.docs[0]
	x.a.openCanvasFile(path)
	if x.a.current() != 1 {
		t.Fatal("the tab coming back should come forward")
	}
}

// The strip can go down the left side and back, and a tab does the same there; the video editor's parts move beside it.
func TestTabsDownTheLeftSide(t *testing.T) {
	x := newHarness(t)
	x.open(x.fixture())
	top := x.a.layout()
	x.a.settings.VerticalTabs = true
	x.frame(Input{})
	side := x.a.layout()
	if side.tabs.H != x.a.screen.H || side.body.X != side.tabs.W || side.preview.X != side.tabs.W || side.timeline.X != side.tabs.W {
		t.Fatalf("the parts did not make room: %+v", side)
	}
	if side.panel.Right() != top.panel.Right() || side.body.W+side.tabs.W != x.a.screen.W {
		t.Fatalf("the panel left the window's edge: %+v", side)
	}
	if side.preview.Right() != side.panel.X || side.timeline.Right() != x.a.screen.W {
		t.Fatalf("the parts do not meet: %+v", side)
	}
	cx, cy := x.tabAt(0)
	x.rightClick(cx, cy)
	x.menuClick("Pin the tab")
	if !x.a.doc.pinned {
		t.Fatal("the menu should work on a tab down the side")
	}
	// New tab, player mode and settings are whole rows: a click near the row's far end counts.
	u := &x.a.u
	farX := side.tabs.Right() - u.px(10)
	opened := len(x.a.docs)
	x.click(gfx.Rect{X: farX, Y: side.tabs.Y + u.px(6) + float32(opened)*u.px(36) + u.px(17)})
	if len(x.a.docs) != opened+1 {
		t.Fatalf("the row for a new tab was not clicked: %d tabs", len(x.a.docs))
	}
	x.click(gfx.Rect{X: farX, Y: side.tabs.Bottom() - u.px(21)})
	if !x.a.settingsOpen {
		t.Error("the row for the settings was not clicked")
	}
}

// With more tabs than fit, the tabs and the row for a new tab share the strip and stay above the settings.
func TestManyTabsDownTheLeftSide(t *testing.T) {
	x := newHarness(t)
	x.a.settings.VerticalTabs = true
	for range 60 {
		x.a.newTab()
	}
	x.frame(Input{})
	u, side := &x.a.u, x.a.layout().tabs
	bottom := side.Bottom() - u.px(36) - u.px(4)
	step := (bottom - side.Y - u.px(6)) / float32(len(x.a.docs)+1)
	opened := len(x.a.docs)
	x.click(gfx.Rect{X: side.Right() - u.px(10), Y: side.Y + u.px(6) + float32(opened)*step + step/3})
	if len(x.a.docs) != opened+1 {
		t.Fatalf("the row for a new tab is not where the strip puts it: %d tabs", len(x.a.docs))
	}
}
