package ui

import (
	"testing"
	"time"
)

// TestStartPageOffersThreeKinds checks the new tab's cards: equal and side by side in a wide window, one above another
// in a narrow one, the drawing card opening a canvas and the recording card the recorder.
func TestStartPageOffersThreeKinds(t *testing.T) {
	for _, lang := range []Language{English, Chinese} {
		x := newHarness(t)
		x.a.u.lang = lang
		x.frame(Input{})
		p := x.a.startLayout(x.a.layout().body, x.a.startCards())
		if p.stacked || len(p.cards) != 3 {
			t.Fatalf("%s: wide window: stacked %v with %d cards", lang, p.stacked, len(p.cards))
		}
		for i := 1; i < 3; i++ {
			if p.cards[i].W != p.cards[0].W || p.cards[i].H != p.cards[0].H || p.cards[i].Y != p.cards[0].Y {
				t.Errorf("%s: card %d is %+v, card 0 %+v", lang, i, p.cards[i], p.cards[0])
			}
		}
		x.shot(string(lang) + "-start")

		x.click(p.cards[1])
		if x.a.art == nil {
			t.Fatalf("%s: the drawing card opened no canvas", lang)
		}

		x.c.Resize(560, 900)
		x.a.newTab()
		x.frame(Input{})
		p = x.a.startLayout(x.a.layout().body, x.a.startCards())
		if !p.stacked {
			t.Errorf("%s: narrow window: the cards are not stacked", lang)
		}
		for i := 1; i < 3; i++ {
			if p.cards[i].Y < p.cards[i-1].Bottom() {
				t.Errorf("%s: stacked card %d overlaps the one above", lang, i)
			}
		}
		x.shot(string(lang) + "-start-narrow")
	}

	x := recordingHarness(t)
	x.frame(Input{})
	p := x.a.startLayout(x.a.layout().body, x.a.startCards())
	x.shot("en-start-record")
	x.click(p.cards[2])
	x.pump(time.Minute, func() bool { return x.a.rec.open })
	if !x.a.rec.open {
		t.Error("the recording card did not open the recorder")
	}
}
