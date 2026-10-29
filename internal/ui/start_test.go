package ui

import (
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestStartPageOffersEveryKind checks the new tab's cards: equal and side by side in a wide window, one above another
// in a narrow one, the drawing card opening a canvas and the recording card the recorder.
func TestStartPageOffersEveryKind(t *testing.T) {
	for _, lang := range []Language{English, Chinese} {
		x := newHarness(t)
		x.a.u.lang = lang
		x.frame(Input{})
		p := x.a.startLayout(x.a.layout().body, x.a.startCards())
		if p.stacked || len(p.cards) != len(x.a.startCards()) {
			t.Fatalf("%s: wide window: stacked %v with %d cards", lang, p.stacked, len(p.cards))
		}
		// Equal cards in rows of as many as fit, as even as can be, none overlapping.
		rows := map[float32]int{}
		for i, c := range p.cards {
			if c.W != p.cards[0].W || c.H != p.cards[0].H {
				t.Errorf("%s: card %d is %+v, card 0 %+v", lang, i, c, p.cards[0])
			}
			rows[c.Y]++
			for j := range i {
				if c.Intersects(p.cards[j]) {
					t.Errorf("%s: cards %d and %d overlap", lang, j, i)
				}
			}
		}
		for y, n := range rows {
			if n < rows[p.cards[0].Y]-1 {
				t.Errorf("%s: the row at %v has %d cards, the first %d", lang, y, n, rows[p.cards[0].Y])
			}
		}
		x.shot(string(lang) + "-start")
		canvas := x.startCard("empty/canvas")
		x.mx, x.my = canvas.X+canvas.W/2, canvas.Y+canvas.H/2
		x.frame(Input{})
		x.shot(string(lang) + "-start-hover")

		x.click(canvas)
		if x.a.art == nil {
			t.Fatalf("%s: the drawing card opened no canvas", lang)
		}
		// Nothing is drawn on it: closing it leaves no file behind.
		path := x.a.art.path
		x.a.closeTab(len(x.a.docs) - 1)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s: an empty canvas was saved as %s", lang, path)
		}

		x.c.Resize(560, 900)
		x.a.newTab()
		x.frame(Input{})
		p = x.a.startLayout(x.a.layout().body, x.a.startCards())
		if !p.stacked {
			t.Errorf("%s: narrow window: the cards are not stacked", lang)
		}
		for i := 1; i < len(p.cards); i++ {
			if p.cards[i].Y < p.cards[i-1].Bottom() {
				t.Errorf("%s: stacked card %d overlaps the one above", lang, i)
			}
		}
		x.shot(string(lang) + "-start-narrow")
	}

	x := recordingHarness(t)
	x.frame(Input{})
	// The card is the way to record: the tab strip has no recording or screenshot button.
	x.shot("en-start-record")
	x.click(x.startCard("empty/record"))
	x.pump(time.Minute, func() bool { return x.a.rec.open })
	if !x.a.rec.open {
		t.Error("the recording card did not open the recorder")
	}
}

// TestEditCardOpensTheEditorFirst checks that Edit a video opens the editor at once, waiting for videos, and that
// several videos added there go one after another on the timeline.
func TestEditCardOpensTheEditorFirst(t *testing.T) {
	x := newHarness(t)
	a := x.a
	x.frame(Input{})
	x.click(x.startCard("empty/video"))
	if !a.editing || a.p != nil || len(a.docs) != 1 {
		t.Fatalf("the card did not make this tab the editor: editing %v, %d tabs", a.editing, len(a.docs))
	}
	x.frame(Input{})
	x.shot(string(a.u.lang) + "-editor-empty")
	if got := a.doc.title(a); got != a.u.t("Edit a video") {
		t.Errorf("the tab is called %q", got)
	}
	// Choose from files… opens the files chosen, in order, in this tab.
	first, second := x.fixture(), clipFile(t, x.mediaDir(), "second.mp4", "testsrc2=size=640x360", "3")
	x.h.chosen = []string{first, second}
	x.click(a.emptyEditorLayout(a.layout()).choose)
	x.pump(time.Minute, func() bool { return a.p != nil && a.opening == "" && len(a.doc.queued) == 0 && len(a.p.Sources) == 2 })
	if a.p == nil || len(a.p.Sources) != 2 || len(a.docs) != 1 {
		t.Fatalf("the videos did not open here: %d tabs, project %v", len(a.docs), a.p)
	}
	if filepath.Base(a.p.Sources[0].Path) != "clip.mp4" || filepath.Base(a.p.Sources[1].Path) != "second.mp4" {
		t.Fatalf("out of order: %s, %s", a.p.Sources[0].Path, a.p.Sources[1].Path)
	}
}

// TestCanvasIsSavedOnlyOnceSomethingIsOnIt checks that a new canvas is written with its first change, not before.
func TestCanvasIsSavedOnlyOnceSomethingIsOnIt(t *testing.T) {
	x := newHarness(t)
	a := x.a
	a.newCanvasNow()
	path := a.art.path
	x.frame(Input{})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the empty canvas was written")
	}
	a.art.touched(image.Rect(0, 0, 8, 8))
	a.art.changed = time.Now().Add(-artSaveDelay)
	x.frame(Input{})
	x.pump(10*time.Second, func() bool { return !a.art.saving })
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the canvas with a change was not written: %v", err)
	}
}

// TestResourceCardOpensTheDesktop checks that the one card for files comes first and, the first time, opens the
// desktop as a list on the left and as pictures on the right.
func TestResourceCardOpensTheDesktop(t *testing.T) {
	x := newHarness(t)
	a := x.a
	if cards := a.startCards(); cards[0].id != "empty/resources" {
		t.Fatalf("the first card is %s", cards[0].id)
	}
	x.frame(Input{})
	x.click(x.startCard("empty/resources"))
	x.waitFiles()
	fd := a.files
	if fd == nil || len(fd.panes) != 2 {
		t.Fatal("the card did not open the resource manager with two lists")
	}
	desktop := defaultFolder()
	if fd.panes[0].dir != desktop || fd.panes[0].grid || fd.panes[1].dir != desktop || !fd.panes[1].grid {
		t.Fatalf("lists %s (pictures %v) and %s (pictures %v), want %s as a list and as pictures",
			fd.panes[0].dir, fd.panes[0].grid, fd.panes[1].dir, fd.panes[1].grid, desktop)
	}
	x.frame(Input{})
	x.shot("resources-first")
}

// TestTabsShowTheirCardsSymbol checks that a tab opened from a start card carries that card's symbol, and a tab not
// yet put to any work a plus.
func TestTabsShowTheirCardsSymbol(t *testing.T) {
	x := newHarness(t)
	a := x.a
	x.frame(Input{})
	if got := a.doc.glyph(); got != icAdd {
		t.Errorf("a new tab shows %q", got)
	}
	glyphs := map[string]string{}
	for _, c := range a.startCards() {
		glyphs[c.id] = c.glyph
	}
	for i, id := range []string{"empty/resources", "empty/video", "empty/canvas", "empty/mind"} {
		if i > 0 {
			a.newTab()
			x.frame(Input{})
		}
		x.click(x.startCard(id))
		x.frame(Input{})
		if got := a.doc.glyph(); got != glyphs[id] {
			t.Errorf("%s opened a tab showing %q, the card shows %q", id, got, glyphs[id])
		}
	}
	x.shot(string(a.u.lang) + "-tab-symbols")
}
