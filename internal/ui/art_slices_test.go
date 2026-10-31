package ui

import (
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// K marks the chosen layers as slices and unmarks them; the ⓘ by a slice explains it; Ctrl+Shift+P exports the slices
// beside the canvas file, keeping a copy of each file it replaces.
func TestSlices(t *testing.T) {
	x, ad := canvasHarness(t)
	a, d := x.a, ad.d
	u := &a.u
	fish := d.AddLayer("Fish")
	d.Select(image.Rect(20, 30, 321, 280)) // 301×250
	if err := d.Fill(color.NRGBA{R: 255, A: 255}); err != nil {
		t.Fatal(err)
	}
	d.Deselect()
	cat := d.AddLayer("ui/cat_40x20")
	d.Select(image.Rect(0, 0, 10, 10))
	if err := d.Fill(color.NRGBA{B: 255, A: 255}); err != nil {
		t.Fatal(err)
	}
	d.Deselect()
	x.frame(Input{})

	// K marks the chosen layer and the one picked besides it, as one step; again, unmarks them.
	ad.picked = []int{fish.ID}
	x.key('K', false)
	if d.Node(fish.ID).Name != "Fish.png" || d.Node(cat.ID).Name != "ui/cat_40x20.png" || d.UndoName() != "Mark as slice" {
		t.Fatalf("K marked %q and %q (%q)", d.Node(fish.ID).Name, d.Node(cat.ID).Name, d.UndoName())
	}
	x.key('K', false)
	if d.Node(fish.ID).Name != "Fish" || d.Node(cat.ID).Name != "ui/cat_40x20" {
		t.Fatalf("K again left %q and %q", d.Node(fish.ID).Name, d.Node(cat.ID).Name)
	}
	x.key('K', false)
	ad.picked = nil
	x.frame(Input{})

	// The ⓘ on Fish's row (under ui/cat) opens what it exports.
	row := x.layerRowAt(1)
	x.click(gfx.Rect{X: row.Right() - u.px(6) - u.px(16), Y: row.Y + row.H/2 - 2, W: 4, H: 4})
	if !a.menuOpen("art/slice-info") || ad.sliceInfo != fish.ID {
		t.Fatalf("the ⓘ opened %v for %d", a.menu, ad.sliceInfo)
	}
	said := strings.Join(a.sliceExplanation(ad, ad.sliceRows[fish.ID]), " ")
	if !strings.Contains(said, "Fish.png") || !strings.Contains(said, "301 × 250") {
		t.Errorf("the explanation: %s", said)
	}
	x.frame(Input{})
	x.shot("canvas-slice-info")
	x.key(keyEscape, false)
	if a.menu != nil {
		t.Error("Esc should close the explanation")
	}

	// Ctrl+Shift+P writes them beside the canvas file.
	dir := filepath.Dir(ad.path)
	x.frame(Input{Keys: []Key{{Code: 'P', Ctrl: true, Shift: true}}})
	x.pump(10*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(dir, "ui", "cat.png"))
		return err == nil && len(a.toasts) > 0
	})
	for name, size := range map[string]image.Point{"Fish.png": {301, 250}, "ui/cat.png": {40, 20}} {
		if got := pngSize(t, filepath.Join(dir, filepath.FromSlash(name))); got != size {
			t.Errorf("%s is %v, want %v", name, got, size)
		}
	}
	// Again: the files are replaced, the old ones kept.
	toasts := len(a.toasts)
	x.frame(Input{Keys: []Key{{Code: 'P', Ctrl: true, Shift: true}}})
	x.pump(10*time.Second, func() bool { return len(a.toasts) > toasts })
	kept, _ := filepath.Glob(filepath.Join(dir, sliceBackups, "*", "ui", "cat.png"))
	if len(kept) != 1 {
		t.Errorf("the replaced files were not kept: %v (%s)", kept, a.toasts[len(a.toasts)-1].text)
	}

	// A name that cannot export stops the export before anything is written.
	d.Rename(cat.ID, "ui/c?t.png")
	os.Remove(filepath.Join(dir, "Fish.png"))
	toasts = len(a.toasts)
	x.frame(Input{Keys: []Key{{Code: 'P', Ctrl: true, Shift: true}}})
	x.settle(5 * time.Second)
	if len(a.toasts) <= toasts || !a.toasts[len(a.toasts)-1].err {
		t.Error("a bad name should say why the slices cannot export")
	}
	if _, err := os.Stat(filepath.Join(dir, "Fish.png")); err == nil {
		t.Error("nothing should be written while a name cannot export")
	}
	var se *paint.SliceError
	if s, _ := d.SliceOf(cat.ID); !errors.As(s.Err, &se) || se.Problem != paint.SliceBadChar || !strings.Contains(a.describeSlice(s.Err), "c?t") {
		t.Errorf("the problem: %v, described as %q", s.Err, a.describeSlice(s.Err))
	}
}

func pngSize(t *testing.T, path string) image.Point {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c, err := png.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	return image.Pt(c.Width, c.Height)
}
