package ui

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-video-cut/internal/paint"
)

func TestCanvasArtboards(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	// The artboard tool shares the move tool's place: Shift+V goes to it.
	x.key('V', false)
	x.frame(Input{Keys: []Key{{Code: 'V', Shift: true}}})
	if a.artTool != toolBoard {
		t.Fatalf("Shift+V chose tool %d", a.artTool)
	}
	// A drag over nothing makes a board, chosen.
	x.dragWith(ad, [2]float64{-150, 20}, [2]float64{-30, 120}, false, false)
	if len(d.Boards) != 1 || d.Boards[0].Rect != image.Rect(-150, 20, -30, 120) || ad.board != d.Boards[0].ID || d.UndoName() != "Add artboard" {
		t.Fatalf("boards %+v, chosen %d, %q", d.Boards, ad.board, d.UndoName())
	}
	id := ad.board
	// Something drawn on it moves with it.
	d.AddVector(paint.Vector{Kind: paint.VectorEllipse, Size: paint.Pt{X: 40, Y: 40}, M: paint.Translate(-120, 50),
		Fill: paint.VectorPaint{Kind: paint.PaintSolid, Color: color.NRGBA{B: 255, A: 255}}}, "Dot")
	ad.changedAll()
	x.dragWith(ad, [2]float64{-90, 70}, [2]float64{-90, 170}, false, false)
	if b, _ := d.Board(id); b.Rect != image.Rect(-150, 120, -30, 220) || d.UndoName() != "Move artboard" {
		t.Fatalf("moved to %v (%q)", b.Rect, d.UndoName())
	}
	if c := ad.look(-100, 170); c.B < 250 {
		t.Errorf("the dot did not move with the board: %v", c)
	}
	// The bottom right handle resizes it.
	x.dragWith(ad, [2]float64{-30, 220}, [2]float64{0, 260}, false, false)
	if b, _ := d.Board(id); b.Rect != image.Rect(-150, 120, 0, 260) || d.UndoName() != "Artboard" {
		t.Fatalf("resized to %v (%q)", b.Rect, d.UndoName())
	}
	x.frame(Input{})
	x.shot("canvas-artboards")
	// A preset with nothing chosen makes a new board of that size in the view.
	x.key(keyEscape, false)
	if ad.board != 0 {
		t.Fatal("Escape kept the board chosen")
	}
	a.boardPreset(ad, a.artLayout(a.layout().body).view, 1080, 1080)
	if len(d.Boards) != 2 || d.Boards[1].Rect.Dx() != 1080 || ad.board != d.Boards[1].ID {
		t.Fatalf("after the preset: %+v", d.Boards)
	}
	x.frame(Input{})
	x.frame(Input{})
	x.shot("canvas-artboards-preset")
	// Each board exports at its own size, named after it.
	a.exportBoards(ad, 0)
	x.pump(10*time.Second, func() bool {
		m, _ := filepath.Glob(strings.TrimSuffix(ad.path, paint.Ext) + " - *.png")
		return len(m) == 2
	})
	img, err := loadImage(strings.TrimSuffix(ad.path, paint.Ext) + " - Artboard 1.png")
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 150 || img.Bounds().Dy() != 140 {
		t.Errorf("the exported board is %v", img.Bounds())
	}
	// Delete takes the chosen board away and keeps the drawing.
	x.key(keyDelete, false)
	if len(d.Boards) != 1 || d.UndoName() != "Delete artboard" || len(d.Layers()) != 2 {
		t.Errorf("after Delete: %d boards, %q, %d layers", len(d.Boards), d.UndoName(), len(d.Layers()))
	}
	// The boards are saved with the canvas.
	if err := a.saveArt(ad, true); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(ad.path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, _ := f.Stat()
	e, err := paint.Load(f, st.Size())
	if err != nil || len(e.Boards) != 1 || e.Boards[0].Name != "Artboard 1" {
		t.Errorf("the saved canvas has %+v (%v)", e, err)
	}
}

func TestSafeFileName(t *testing.T) {
	for in, want := range map[string]string{"Hero": "Hero", `a/b\c:d*?`: "a_b_c_d__", "  dots... ": "dots", "": "_"} {
		if got := safeFileName(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
