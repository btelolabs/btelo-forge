package ui

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// In pictures, h and l go to the picture before and after, as the hint under the grid says and the arrows do; Enter
// opens one. A picture opened as a canvas leaves no file beside it until it is drawn on.
func TestPictureGridLettersMoveAndOpeningWritesNothing(t *testing.T) {
	x := newHarness(t)
	dir := filepath.Join(x.dir, "pictures")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.png", "b.png", "c.png"} {
		writeCheckPNG(t, filepath.Join(dir, name), 64, 48, color.RGBA{200, 40, 40, 255}, color.RGBA{250, 250, 250, 255})
	}
	x.a.newFilesTab(dir, true)
	x.waitFiles()
	p := x.a.files.panes[0]
	if !p.grid {
		t.Fatal("the list is not shown as pictures")
	}
	first, second := p.entries[p.rows[0]].Name, p.entries[p.rows[1]].Name
	p.cursorTo(first)
	x.mx, x.my = 2, float32(x.c.Img.Rect.Dy()-2) // off the cards, which take the cursor when pointed at
	x.press('L', "")
	if x.a.files == nil || p.current().Name != second {
		t.Fatalf("l in pictures: files tab %v, on %v, want %s", x.a.files != nil, p.current(), second)
	}
	x.press('H', "")
	if p.current().Name != first {
		t.Fatalf("h in pictures: on %s, want %s", p.current().Name, first)
	}

	x.press(keyEnter, "")
	x.pump(10*time.Second, func() bool { return x.a.art != nil })
	ad := x.a.art
	want := filepath.Join(dir, strings.TrimSuffix(first, ".png")+".btcanvas")
	if ad.path != want {
		t.Fatalf("the canvas is to be saved as %s, want %s", ad.path, want)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Fatalf("opening the picture wrote %s already (%v)", want, err)
	}
	ad.changedAll()
	if err := x.a.saveArt(ad, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("drawn on, the canvas was not saved: %v", err)
	}
}

// Canvases made in the same second, whose files are written only with their first change, never share a file.
func TestCanvasesMadeTogetherGetTheirOwnFiles(t *testing.T) {
	x := newHarness(t)
	x.a.settings.ExportDir = x.mediaDir()
	seen := map[string]bool{}
	for range 3 {
		if err := x.a.newCanvas(); err != nil {
			t.Fatal(err)
		}
		if seen[x.a.art.path] {
			t.Fatalf("two canvases are to be saved as %s", x.a.art.path)
		}
		seen[x.a.art.path] = true
	}
}

// Chips that do not fit beside the search box go on to more lines, as words wrap, rather than being cut under it;
// past three lines the wheel scrolls them.
func TestFilterChipsWrapInsteadOfBeingCut(t *testing.T) {
	x := newHarness(t)
	x.c.Resize(1100, 800)
	dir := filepath.Join(x.dir, "pictures")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, name := range []string{"a.png", "b.png"} {
		path := filepath.Join(dir, name)
		writeCheckPNG(t, path, 64, 48, color.RGBA{40, 120, 200, 255}, color.RGBA{250, 250, 250, 255})
		paths = append(paths, path)
	}
	for _, tag := range []string{"holiday", "family", "beach", "sunset", "portrait", "landscape", "cover", "draft"} {
		x.a.addTag(paths, tag)
	}
	x.a.newFilesTab(dir, true)
	x.waitFiles()
	x.frame(Input{})
	u := &x.a.u
	p := x.a.files.panes[0]
	chips := x.a.filterChips(x.a.files, p, "probe")
	widths := make([]float32, len(chips))
	for k, c := range chips {
		widths[k] = u.chipWidth(c)
	}
	strip := u.filterStrip(x.a.layout().body.W)
	xs, _, n := chipPlaces(widths, strip, u.px(filterChipGap))
	if n < 2 {
		t.Fatalf("%d chips on one line %v wide: the test wants them to wrap", len(chips), strip)
	}
	for k := range chips {
		if xs[k]+widths[k] > strip+0.5 {
			t.Errorf("%q ends at %v, past the strip's %v", chips[k].name, xs[k]+widths[k], strip)
		}
	}
	if p.g.barH != u.filterBarHeight(n) || p.g.barH <= u.filterBarHeight(1) {
		t.Fatalf("the bar is %v high for %d lines of chips", p.g.barH, n)
	}
	x.shot("files-chips-wrap")
}
