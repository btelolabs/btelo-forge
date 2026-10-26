package ui

import (
	"slices"
	"testing"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
)

// overlayRect is where overlay id is drawn on the timeline now.
func (x *harness) overlayRect(id int) gfx.Rect {
	x.t.Helper()
	t := x.a.tracks(x.a.layout().timeline)
	k, i, ok := x.a.p.FindOverlay(id)
	if !ok {
		x.t.Fatalf("no overlay %d", id)
	}
	o := x.a.p.Overlays[k].Items[i]
	l, _ := t.lane(groupOverlay, k)
	return x.a.clipRect(t, l, o.Start, o.End)
}

// clickWith clicks the middle of r holding Ctrl (or ⌘) or Shift.
func (x *harness) clickWith(r gfx.Rect, ctrl, shift bool) {
	x.mx, x.my = r.X+r.W/2, r.Y+r.H/2
	x.frame(Input{Ctrl: ctrl, Shift: shift})
	x.frame(Input{Ctrl: ctrl, Shift: shift, Down: true, Pointer: []Pointer{{X: x.mx, Y: x.my, Down: true}}})
	x.frame(Input{Ctrl: ctrl, Shift: shift, Pointer: []Pointer{{X: x.mx, Y: x.my}}})
}

func TestTimelineSelectsSeveralItemsAndEditsThemTogether(t *testing.T) {
	x := newHarness(t)
	x.open(x.fixture())
	x.a.seek(1)
	x.a.addText()
	first := x.a.sel.id
	x.a.seek(5)
	x.a.addText()
	second := x.a.sel.id

	x.click(x.overlayRect(first))
	if ids := x.a.selectedIDs(); !slices.Equal(ids, []int{first}) {
		t.Fatalf("a click selects %v", ids)
	}
	x.clickWith(x.overlayRect(second), true, false)
	if ids := x.a.selectedIDs(); len(ids) != 2 || !x.a.selectionCard() {
		t.Fatalf("Ctrl-click left %v", ids)
	}
	x.clickWith(x.overlayRect(second), true, false)
	if ids := x.a.selectedIDs(); !slices.Equal(ids, []int{first}) {
		t.Fatalf("a second Ctrl-click should take the item out again, left %v", ids)
	}
	x.clickWith(x.overlayRect(second), false, true)
	if len(x.a.selectedIDs()) != 2 {
		t.Fatal("Shift-click did not add")
	}
	x.click(x.overlayRect(second))
	if ids := x.a.selectedIDs(); !slices.Equal(ids, []int{second}) {
		t.Fatalf("a plain click should select just the item, left %v", ids)
	}

	r1, r2 := x.overlayRect(first), x.overlayRect(second)
	x.drag(r1.X-x.a.u.px(20), r1.Y+r1.H/2, r2.Right()+x.a.u.px(6), r1.Y+r1.H-x.a.u.px(2))
	if ids := x.a.selectedIDs(); len(ids) != 2 || !x.a.isPicked(first) || !x.a.isPicked(second) {
		t.Fatalf("the box selected %v", ids)
	}
	before := len(x.a.allItems())
	x.key(keyDelete, false)
	if x.a.hasOverlay(first) || x.a.hasOverlay(second) || len(x.a.allItems()) != before-2 {
		t.Fatalf("Delete did not remove both: selected %v, focus %q, overlays %v %v", x.a.selectedIDs(), x.a.u.focus, x.a.hasOverlay(first), x.a.hasOverlay(second))
	}
	x.key('Z', true)
	if !x.a.hasOverlay(first) || !x.a.hasOverlay(second) {
		t.Fatal("one undo should bring both back")
	}

	x.key('A', true)
	if got := len(x.a.selectedIDs()); got != len(x.a.allItems()) {
		t.Fatalf("Ctrl+A selected %d of %d", got, len(x.a.allItems()))
	}
	x.frame(Input{})
	ids := x.a.selectedIDs()
	x.a.change(func() { x.a.p.SetOpacity(ids, 0.4) })
	if o, _ := x.a.p.Overlay(first); o.Opacity != 0.4 {
		t.Fatal(o.Opacity)
	}
	if c := x.a.p.Video[0].Clips[0]; c.Opacity != 0.4 {
		t.Fatal(c.Opacity)
	}
	x.key(keyDelete, false)
	if len(x.a.p.Video[0].Clips) == 0 || !x.a.hasOverlay(first) {
		t.Fatal("deleting everything must keep the main track, and then delete nothing")
	}
	x.key(keyEscape, false)
	if len(x.a.selectedIDs()) != 0 {
		t.Fatal("Esc did not clear the selection")
	}
}

func TestAnAIEditsSeveralItemsThroughMCP(t *testing.T) {
	x := newHarness(t)
	s := connectMCP(t, x)
	x.open(x.fixture())
	var added map[string]int
	s.ok("add_text", map[string]any{"text": "a", "start": 1, "end": 2}, &added)
	a := added["id"]
	s.ok("add_text", map[string]any{"text": "b", "start": 3, "end": 4}, &added)
	b := added["id"]
	main := x.a.p.Video[0].Clips[0].ID
	var res struct {
		Changed map[string]int `json:"changed"`
	}
	s.ok("set_items", map[string]any{"ids": []int{a, b, main}, "opacity": 0.3, "volume": 0.5}, &res)
	if res.Changed["opacity"] != 3 || res.Changed["volume"] != 1 {
		t.Fatalf("%+v", res.Changed)
	}
	var st stateView
	s.ok("get_state", nil, &st)
	if st.Project.Overlays[0].Items[0].Opacity != 0.3 || st.Project.Video[0].Clips[0].Opacity != 0.3 || st.Project.Video[0].Clips[0].Volume != 0.5 {
		t.Fatalf("%+v", st.Project)
	}
	if r := s.call("delete_items", map[string]any{"ids": []int{a, 999}}); !r.IsError {
		t.Fatal("an unknown id was accepted")
	}
	if r := s.call("delete_items", map[string]any{"ids": []int{main}}); !r.IsError {
		t.Fatal("emptying the main track was accepted")
	}
	var del struct {
		Deleted int `json:"deleted"`
	}
	s.ok("delete_items", map[string]any{"ids": []int{a, b}}, &del)
	if del.Deleted != 2 || x.a.hasOverlay(a) || x.a.hasOverlay(b) {
		t.Fatalf("deleted %d", del.Deleted)
	}
	s.ok("undo", nil, nil)
	if !x.a.hasOverlay(a) || !x.a.hasOverlay(b) {
		t.Fatal("one undo should bring both back")
	}
}
