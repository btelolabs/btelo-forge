package ui

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func TestSpringSettlesOnItsTargetAndKeepsItsSpeedForANewOne(t *testing.T) {
	var s spring
	frames := 0
	for s.step(1, 1.0/60, pieTurnFreq, pieTurnRatio) {
		if frames++; frames > 120 {
			t.Fatalf("still moving after two seconds: %+v", s)
		}
	}
	if s.pos != 1 || s.vel != 0 {
		t.Fatalf("settled at %+v", s)
	}
	// Midway to another target, the spring is moving; a new target turns it without stopping it dead.
	s.step(0, 0.1, pieTurnFreq, pieTurnRatio)
	before := s.vel
	s.step(2, 1.0/60, pieTurnFreq, pieTurnRatio)
	if before >= 0 || s.vel >= 0 {
		t.Fatalf("the speed went from %v to %v in one frame", before, s.vel)
	}
}

// pieHarness is a file manager tab at a folder of nine folders of falling sizes and a small file, analysed.
func pieHarness(t *testing.T) (*harness, *time.Time) {
	layout := map[string]string{"small.txt": "x"}
	for i := 0; i < 11; i++ {
		layout[fmt.Sprintf("f%02d/data.bin", i)] = strings.Repeat("d", 1000*(11-i)*(11-i))
	}
	x, _ := filesHarness(t, layout)
	now := time.Unix(1_000_000, 0)
	x.a.fm.clock = func() time.Time { return now }
	x.press('I', "")
	x.waitFiles()
	if x.a.paneAnalysis(x.a.files.pane()) == nil {
		t.Fatal("the folder was not analysed")
	}
	return x, &now
}

// pieFrame draws a frame a sixtieth of a second after the last.
func pieFrame(x *harness, now *time.Time, in Input) {
	*now = now.Add(time.Second / 60)
	x.frame(in)
}

func TestPieWedgesAreTheLargestItemsThenTheRest(t *testing.T) {
	x, _ := pieHarness(t)
	p := x.a.files.pane()
	node := x.a.paneAnalysis(p)
	w := x.a.pieWedges(p, node)
	if len(w) != pieSlots+1 || w[len(w)-1].key != pieOther {
		t.Fatalf("%d wedges, the last %q", len(w), w[len(w)-1].key)
	}
	var sum int64
	for i, wd := range w {
		if i < pieSlots && wd.key != fmt.Sprintf("f%02d", i) {
			t.Errorf("wedge %d is %q", i, wd.key)
		}
		if i > 0 && w[i-1].to != wd.from {
			t.Errorf("wedge %d starts at %v, the one before ends at %v", i, wd.from, w[i-1].to)
		}
		sum += wd.size
	}
	if sum != node.Size || w[0].from != 0 || w[len(w)-1].to != 2*math.Pi {
		t.Errorf("the wedges hold %d of %d and span %v to %v", sum, node.Size, w[0].from, w[len(w)-1].to)
	}
	// f08, f09, f10 and the small file share the last wedge.
	if rest := w[len(w)-1]; rest.count != 3 && rest.count != 4 {
		t.Errorf("the rest holds %d items", rest.count)
	}
	p.cursorTo("f10")
	if k := x.a.pieKeyOf(w, node, p.current()); k != pieOther {
		t.Errorf("f10 is in wedge %q", k)
	}
}

func TestPieTurnsTheItemUnderTheCursorToTwelveSmoothly(t *testing.T) {
	x, now := pieHarness(t)
	fd := x.a.files
	p := fd.pane()
	pieFrame(x, now, Input{})
	if !p.pie.shown {
		t.Fatal("no pie under the analysed list")
	}
	// The pie starts turned to the item under the cursor, which grows out from the rest.
	if x.a.nextFrame(0, false) == 0 {
		t.Fatal("the window does not draw again while the wedge grows")
	}
	for i := 0; i < 3; i++ {
		x.press('J', "")
	}
	frames, last := 0, p.pie.turn.pos
	for x.a.pieMoving() {
		pieFrame(x, now, Input{})
		if step := math.Abs(p.pie.turn.pos - last); step > 0.35 {
			t.Fatalf("the pie leapt %.2f radians in a frame", step)
		}
		last = p.pie.turn.pos
		if frames++; frames > 180 {
			t.Fatal("still moving after three seconds")
		}
	}
	if frames < 12 {
		t.Errorf("the turn took only %d frames: it jumps rather than turns", frames)
	}
	w := x.a.pieWedges(p, x.a.paneAnalysis(p))
	focus := w[3]
	if p.current().Name != "f03" || p.pie.focus != focus.key {
		t.Fatalf("the cursor is on %q and the pie turned to %q", p.current().Name, p.pie.focus)
	}
	mid := math.Mod(p.pie.turn.pos+(focus.from+focus.to)/2, 2*math.Pi)
	if mid > 1e-3 && mid < 2*math.Pi-1e-3 {
		t.Errorf("the middle of %s is at %.4f radians, not twelve o'clock", focus.key, mid)
	}
	if got := p.pie.pop(focus.key); got != 1 {
		t.Errorf("%s stands out by %v", focus.key, got)
	}
	if got := p.pie.pop(w[0].key); got != 0 {
		t.Errorf("%s, no longer under the cursor, still stands out by %v", w[0].key, got)
	}
	if x.a.nextFrame(0, false) != 0 {
		t.Error("the window keeps drawing once the pie is still")
	}
	// Going back to the first item turns the shorter way, not round the whole pie.
	before := p.pie.turn.pos
	for p.cursor > 0 {
		x.press('K', "")
	}
	for x.a.pieMoving() {
		pieFrame(x, now, Input{})
	}
	if d := math.Abs(p.pie.turn.pos - before); d > math.Pi {
		t.Errorf("going back turned the pie %.2f radians", d)
	}
}

func TestPieClickPutsTheCursorOnTheWedgesItem(t *testing.T) {
	x, now := pieHarness(t)
	p := x.a.files.pane()
	for pieFrame(x, now, Input{}); x.a.pieMoving(); {
		pieFrame(x, now, Input{})
	}
	w := x.a.pieWedges(p, x.a.paneAnalysis(p))
	m := &p.pie
	// Halfway out along the middle of f02's wedge, where it is drawn now.
	a := (w[2].from+w[2].to)/2 + m.turn.pos
	r := float64(m.radius) * 0.6
	x.mx, x.my = m.cx+float32(r*math.Sin(a)), m.cy-float32(r*math.Cos(a))
	pieFrame(x, now, Input{})
	pieFrame(x, now, Input{Down: true, Pointer: []Pointer{{X: x.mx, Y: x.my, Down: true}}})
	pieFrame(x, now, Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}})
	if e := p.current(); e == nil || e.Name != "f02" {
		t.Fatalf("the click put the cursor on %v", e)
	}
	if !x.a.pieMoving() {
		t.Error("the pie does not turn to the wedge clicked")
	}
}
