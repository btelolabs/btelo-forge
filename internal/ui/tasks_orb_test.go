package ui

import (
	"bytes"
	"image"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/proc"
)

// runningTask has the corner show a model in memory: one an earlier run left, as no program needs to start for that.
func runningTask(x *harness) {
	x.a.ai.orphans = []proc.Entry{{Pid: 999999, Label: "model:qwen3-4b", Started: time.Now()}}
	x.a.ai.orphansAt = time.Now()
}

func TestTheOrbTakesLittleRoomAndOpensUnderThePointer(t *testing.T) {
	x := newHarness(t)
	runningTask(x)
	x.frame(Input{})
	l := x.a.layoutTasks()
	if l.orb.W == 0 {
		t.Fatal("no orb")
	}
	if len(l.tasks) != 0 {
		t.Fatalf("%d cards while the pointer is away", len(l.tasks))
	}
	if l.orb.W > x.a.u.px(orbSize) || l.orb.Right() != x.a.screen.W-x.a.u.px(16) || l.orb.Bottom() != x.a.screen.H-x.a.u.px(16) {
		t.Fatalf("the orb is at %+v", l.orb)
	}
	x.shot("orb-closed")

	// On the orb, the list opens above it.
	x.mx, x.my = l.orb.X+l.orb.W/2, l.orb.Y+l.orb.H/2
	x.frame(Input{})
	x.frame(Input{})
	x.shot("orb-open")
	l = x.a.layoutTasks()
	if len(l.tasks) != 1 {
		t.Fatalf("%d cards under the pointer", len(l.tasks))
	}
	card := l.cards[0]
	if card.Bottom() > l.orb.Y || card.Right() != l.orb.Right() || card.H > x.a.u.px(46) {
		t.Fatalf("card %+v, orb %+v", card, l.orb)
	}

	// It stays while the pointer goes up to the list, across the gap.
	x.mx, x.my = l.orb.X+2, card.Bottom()+1
	x.frame(Input{})
	x.mx, x.my = card.X+10, card.Y+10
	x.frame(Input{})
	if !x.a.orb.open {
		t.Fatal("the list closed on the way to it")
	}

	// And its stop button works.
	stop := x.a.layoutTasks().cancels[0]
	x.click(stop)
	if len(x.a.ai.orphans) != 0 {
		t.Fatal("the stop button did nothing")
	}

	// Away from it, it closes after a moment.
	runningTask(x)
	x.a.orb.open, x.a.orb.until = true, time.Now().Add(-time.Second)
	x.mx, x.my = 100, 100
	x.frame(Input{})
	if x.a.orb.open {
		t.Fatal("the list stayed open")
	}
}

func TestTheOrbDoesNotOpenUnderADialog(t *testing.T) {
	x := newHarness(t)
	runningTask(x)
	x.frame(Input{})
	l := x.a.layoutTasks()
	x.mx, x.my = l.orb.X+2, l.orb.Y+2
	x.a.u.blocked = true
	x.a.drawTasks(false)
	x.a.u.blocked = false
	if x.a.orb.open {
		t.Fatal("it opened while a dialog holds the window")
	}
}

func TestWorkThatEndsKeepsItsCardAboveTheOrb(t *testing.T) {
	x := newHarness(t)
	x.frame(Input{})
	tasks := []task{{id: "model", running: true}, {id: "work", fraction: 0.5}}
	l := x.a.placeTasks(tasks)
	if len(l.tasks) != 1 || l.tasks[0].id != "work" || l.cards[0].Bottom() > l.orb.Y {
		t.Fatalf("%+v over %+v", l.cards, l.orb)
	}
	x.a.orb.open, x.a.orb.until = true, time.Now().Add(time.Minute)
	l = x.a.placeTasks(tasks)
	if len(l.tasks) != 2 || l.tasks[0].id != "model" || l.cards[1].Bottom() > l.cards[0].Y {
		t.Fatalf("%+v", l.tasks)
	}
}

func TestTheOrbMoves(t *testing.T) {
	a, b := image.NewRGBA(image.Rect(0, 0, 68, 68)), image.NewRGBA(image.Rect(0, 0, 68, 68))
	paintOrb(a, 1)
	paintOrb(b, 1.5)
	if bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("it stands still")
	}
	if a.RGBAAt(34, 34).A != 255 {
		t.Fatalf("its middle is %+v", a.RGBAAt(34, 34))
	}
	for _, c := range [][2]int{{0, 0}, {67, 0}, {0, 67}, {67, 67}} {
		if got := a.RGBAAt(c[0], c[1]); got.A != 0 {
			t.Fatalf("corner %v is %+v", c, got)
		}
	}
	for i := 0; i < len(a.Pix); i += 4 {
		if p := a.Pix[i : i+4]; p[0] > p[3] || p[1] > p[3] || p[2] > p[3] {
			t.Fatalf("not premultiplied: %v", p)
		}
	}
}

func TestTheOrbOnlyAsksForFramesWhenItCanBeSeen(t *testing.T) {
	x := newHarness(t)
	if x.a.orbFrame() != 0 {
		t.Fatal("frames with no model")
	}
	runningTask(x)
	x.frame(Input{})
	if x.a.orbFrame() != orbIdleFrame {
		t.Fatalf("%v", x.a.orbFrame())
	}
	x.a.u.inactive = true
	if x.a.orbFrame() != 0 {
		t.Fatal("frames in a window in the background")
	}
	x.a.u.inactive = false
	x.a.mail.organised = true
	if x.a.orbFrame() != orbBusyFrame {
		t.Fatalf("%v", x.a.orbFrame())
	}
	x.a.mail.organised = false
}

func BenchmarkPaintOrb(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 68, 68))
	for i := 0; i < b.N; i++ {
		paintOrb(img, float64(i)/24)
	}
}

func BenchmarkFrameWithTheOrb(b *testing.B) {
	x := newHarness(b)
	runningTask(x)
	for b.Loop() {
		x.frame(Input{})
	}
}
