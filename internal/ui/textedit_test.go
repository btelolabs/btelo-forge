package ui

import (
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// boxBench draws one text box on the harness's canvas, frame by frame, as a screen with only that box would.
type boxBench struct {
	x     *harness
	r     gfx.Rect
	value string
}

func newBoxBench(t *testing.T, value string) *boxBench {
	x := newHarness(t)
	x.frame(Input{})
	u := &x.a.u
	return &boxBench{x: x, r: gfx.Rect{X: u.px(40), Y: u.px(40), W: u.px(240), H: u.px(32)}, value: value}
}

// frame draws the box with input in and reports what it said.
func (b *boxBench) frame(in Input, secret bool) (changed, done bool) {
	u := &b.x.a.u
	u.begin(in)
	if secret {
		changed, done = u.secretBox("bench", b.r, &b.value, "")
	} else {
		changed, done = u.textBox("bench", b.r, &b.value, "", false)
	}
	u.end()
	return changed, done
}

// at is the screen point over character i of the box's words, unscrolled.
func (b *boxBench) at(i int) (float32, float32) {
	u := &b.x.a.u
	f := u.face(13.5)
	rs := []rune(b.value)
	return b.r.X + u.px(10) + f.Measure(string(rs[:i])) + 1, b.r.Y + b.r.H/2
}

// clickAt clicks character i of the box's words.
func (b *boxBench) clickAt(i int) {
	x, y := b.at(i)
	b.click(x, y, false, false)
}

func (b *boxBench) click(x, y float32, double, shift bool) {
	b.frame(Input{X: x, Y: y, Down: true, Shift: shift, Pointer: []Pointer{{X: x, Y: y, Down: true, Double: double}}}, false)
	b.frame(Input{X: x, Y: y, Shift: shift, Pointer: []Pointer{{X: x, Y: y}}}, false)
}

func (b *boxBench) keys(ks ...Key) {
	for _, k := range ks {
		b.frame(Input{Keys: []Key{k}}, false)
	}
}

// A box given the keyboard from outside, or found holding another box's words, starts from its own words, all
// chosen: the editor is shared, and no box ever shows what another left in it.
func TestTextBoxNeverShowsAnotherBoxsWords(t *testing.T) {
	b := newBoxBench(t, "Layer 1")
	u := &b.x.a.u
	u.beginEdit("elsewhere", "left over", false, 2, 2)
	u.setFocus("bench") // the keyboard handed over without the words, as a shortcut does
	b.frame(Input{}, false)
	if string(u.edit.text) != "Layer 1" || u.edit.anchor != 0 || u.edit.caret != len("Layer 1") {
		t.Fatalf("the box starts with %q chosen %d-%d", string(u.edit.text), u.edit.anchor, u.edit.caret)
	}
	b.frame(Input{Typed: []rune("Ink")}, false)
	if b.value != "Ink" {
		t.Errorf("typing over the chosen words gave %q", b.value)
	}
}

// Every box has the same keys: words, the whole text, copy, cut, paste, and undo with typing as one step.
func TestTextBoxKeys(t *testing.T) {
	b := newBoxBench(t, "alpha beta gamma")
	u := &b.x.a.u
	host := b.x.h
	b.clickAt(len("alpha beta gamma"))
	b.keys(Key{Code: keyLeft, Ctrl: true}, Key{Code: keyLeft, Ctrl: true, Shift: true})
	if a, c := u.edit.selection(); string(u.edit.text[a:c]) != "beta " {
		t.Fatalf("Ctrl+Shift+Left chose %q", string(u.edit.text[a:c]))
	}
	b.keys(Key{Code: 'C', Ctrl: true})
	if host.clip != "beta " {
		t.Errorf("Ctrl+C copied %q", host.clip)
	}
	b.keys(Key{Code: 'X', Ctrl: true})
	if b.value != "alpha gamma" || host.clip != "beta " {
		t.Errorf("Ctrl+X left %q, copied %q", b.value, host.clip)
	}
	b.keys(Key{Code: keyEnd}, Key{Code: 'V', Ctrl: true})
	if b.value != "alpha gammabeta " {
		t.Errorf("Ctrl+V gave %q", b.value)
	}
	b.keys(Key{Code: keyBack, Ctrl: true})
	if b.value != "alpha " {
		t.Errorf("Ctrl+Backspace gave %q", b.value)
	}
	// Typing is one step to undo; each other change one of its own.
	b.frame(Input{Typed: []rune("de")}, false)
	b.frame(Input{Typed: []rune("lta")}, false)
	if b.value != "alpha delta" {
		t.Fatalf("typed %q", b.value)
	}
	b.keys(Key{Code: 'Z', Ctrl: true})
	if b.value != "alpha " {
		t.Errorf("undoing the typing gave %q", b.value)
	}
	b.keys(Key{Code: 'Z', Ctrl: true})
	if b.value != "alpha gammabeta " {
		t.Errorf("undoing Ctrl+Backspace gave %q", b.value)
	}
	b.keys(Key{Code: 'Y', Ctrl: true}, Key{Code: 'Z', Ctrl: true, Shift: true})
	if b.value != "alpha delta" {
		t.Errorf("redoing gave %q", b.value)
	}
	b.keys(Key{Code: keyHome, Ctrl: true}, Key{Code: keyDelete, Ctrl: true})
	if b.value != " delta" {
		t.Errorf("Ctrl+Delete gave %q", b.value)
	}
}

// A secret can be typed and pasted in, never copied out.
func TestSecretBoxCannotBeCopied(t *testing.T) {
	b := newBoxBench(t, "stream-key")
	b.x.h.clip = "unchanged"
	u := &b.x.a.u
	u.focusEdit("bench", b.value, false)
	b.frame(Input{Keys: []Key{{Code: 'C', Ctrl: true}}}, true)
	b.frame(Input{Keys: []Key{{Code: 'X', Ctrl: true}}}, true)
	if b.x.h.clip != "unchanged" || b.value != "stream-key" {
		t.Errorf("the secret was copied (%q) or cut (%q)", b.x.h.clip, b.value)
	}
}

// Every box has the same mouse: a click puts the caret, Shift+click stretches the choice, a double-click chooses
// a word and a drag chooses what it passes over.
func TestTextBoxMouse(t *testing.T) {
	b := newBoxBench(t, "one two three")
	u := &b.x.a.u
	b.clickAt(5)
	if u.edit.caret != 5 || u.edit.anchor != 5 {
		t.Fatalf("the click put the caret at %d-%d", u.edit.anchor, u.edit.caret)
	}
	x, y := b.at(9)
	b.click(x, y, false, true)
	if u.edit.anchor != 5 || u.edit.caret != 9 {
		t.Errorf("Shift+click chose %d-%d", u.edit.anchor, u.edit.caret)
	}
	x, y = b.at(10)
	b.click(x, y, true, false)
	if a, c := u.edit.selection(); string(u.edit.text[a:c]) != "three" {
		t.Errorf("the double-click chose %q", string(u.edit.text[a:c]))
	}
	fx, fy := b.at(1)
	tx, ty := b.at(6)
	b.frame(Input{X: fx, Y: fy, Down: true, Pointer: []Pointer{{X: fx, Y: fy, Down: true}}}, false)
	b.frame(Input{X: tx, Y: ty, Down: true}, false)
	b.frame(Input{X: tx, Y: ty, Pointer: []Pointer{{X: tx, Y: ty}}}, false)
	if a, c := u.edit.selection(); a != 1 || c != 6 {
		t.Errorf("the drag chose %d-%d", a, c)
	}
}

// The caret blinks while the box is typed into, and shows again at once whenever it moves or the words change.
func TestCaretBlinksAndShowsWhenMoved(t *testing.T) {
	b := newBoxBench(t, "abc")
	u := &b.x.a.u
	b.clickAt(3)
	if !u.blinking {
		t.Fatal("a box being typed into should ask for the caret's blink")
	}
	u.caretBlink = time.Now().Add(-caretBlinkPeriod * 3 / 4) // in the hidden half
	b.frame(Input{}, false)
	if time.Since(u.caretBlink) < caretBlinkPeriod/2 {
		t.Fatal("a frame without a change started the blink again")
	}
	b.keys(Key{Code: keyLeft})
	if time.Since(u.caretBlink) > caretBlinkPeriod/4 {
		t.Error("moving the caret should show it at once")
	}
	u.caretBlink = time.Now().Add(-caretBlinkPeriod * 3 / 4)
	b.frame(Input{Typed: []rune("x")}, false)
	if time.Since(u.caretBlink) > caretBlinkPeriod/4 {
		t.Error("typing should show the caret at once")
	}
}

// A long line scrolls to keep the caret in the box, and a click there finds the character under it.
func TestTextBoxScrollsToTheCaret(t *testing.T) {
	long := "The quick brown fox jumps over the lazy dog, then naps in the warm afternoon sun"
	b := newBoxBench(t, long)
	u := &b.x.a.u
	u.beginEdit("bench", long, false, len(long), len(long))
	b.frame(Input{}, false)
	if u.editScroll[0] <= 0 {
		t.Fatal("the caret at the end of a long line should scroll it")
	}
	// The caret's own place: a click just right of it, at the box's right edge, finds the end.
	x := b.r.Right() - u.px(8)
	b.click(x, b.r.Y+b.r.H/2, false, false)
	if u.edit.caret < len(long)-3 {
		t.Errorf("a click at the right of the scrolled line put the caret at %d of %d", u.edit.caret, len(long))
	}
}
