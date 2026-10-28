package ui

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/paint"
)

func TestCanvasArtboards(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	d := ad.d
	// The artboard tool is on J.
	x.key('J', false)
	if a.artTool != toolBoard {
		t.Fatalf("J chose tool %d", a.artTool)
	}
	// A drag over nothing makes a board, chosen, and chooses its group so new layers go into it.
	x.dragWith(ad, [2]float64{-150, 20}, [2]float64{-30, 120}, false, false)
	if bs := d.Boards(); len(bs) != 1 || bs[0].Rect != image.Rect(-150, 20, -30, 120) || ad.board != bs[0].ID || d.UndoName() != "Add artboard" ||
		d.Active != bs[0].ID {
		t.Fatalf("boards %+v, chosen %d, %q", bs, ad.board, d.UndoName())
	}
	id := ad.board
	// Something drawn in it is cut to it, and moves with it.
	dot := d.AddVector(paint.Vector{Kind: paint.VectorEllipse, Size: paint.Pt{X: 40, Y: 40}, M: paint.Translate(-120, 50),
		Fill: paint.VectorPaint{Kind: paint.PaintSolid, Color: color.NRGBA{B: 255, A: 255}}}, "Dot")
	if d.BoardOf(dot.ID) != id {
		t.Fatal("a layer added with the board chosen should go into it")
	}
	d.AddVector(paint.Vector{Kind: paint.VectorRect, Size: paint.Pt{X: 100, Y: 10}, M: paint.Translate(-60, 30),
		Fill: paint.VectorPaint{Kind: paint.PaintSolid, Color: color.NRGBA{G: 255, A: 255}}}, "Bar")
	ad.changedAll()
	if c := ad.look(-40, 35); c.G < 250 {
		t.Errorf("the bar inside the board: %v", c)
	}
	if c := ad.look(-20, 35); c.G > 0 && c.R < 250 {
		t.Errorf("the bar past the board's edge should be cut off: %v", c)
	}
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
	if b, ok := d.Board(ad.board); len(d.Boards()) != 2 || !ok || b.ID == id || b.Rect.Dx() != 1080 {
		t.Fatalf("after the preset: %+v", d.Boards())
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
	// Delete takes the chosen board away and keeps what was in it.
	x.key(keyDelete, false)
	if len(d.Boards()) != 1 || d.UndoName() != "Delete artboard" || len(d.Layers()) != 3 {
		t.Errorf("after Delete: %d boards, %q, %d layers", len(d.Boards()), d.UndoName(), len(d.Layers()))
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
	if err != nil || len(e.Boards()) != 1 || e.Boards()[0].Name != "Artboard 1" {
		t.Errorf("the saved canvas has %+v (%v)", e, err)
	}
}

// A board being made or resized takes the size of a board beside it when it comes near.
func TestArtboardsSnapToEachOthersSize(t *testing.T) {
	x, ad := canvasHarness(t)
	d := ad.d
	ad.zoom, ad.panX, ad.panY, ad.fit = 0.4, 450, 75, false // all of the boards in view
	x.key('J', false)
	x.dragWith(ad, [2]float64{-300, 0}, [2]float64{-100, 150}, false, false)
	first := ad.board
	// Two pixels short each way, well within the snapping distance at this zoom.
	x.dragWith(ad, [2]float64{500, 0}, [2]float64{698, 148}, false, false)
	if b, _ := d.Board(ad.board); b.Rect != image.Rect(500, 0, 700, 150) {
		t.Errorf("the new board is %v, want the first one's 200 × 150", b.Rect)
	}
	// Its right handle, dragged near the first's width again after a change, takes it.
	second := ad.board
	x.dragWith(ad, [2]float64{700, 75}, [2]float64{760, 75}, false, false)
	if b, _ := d.Board(second); b.Rect.Dx() != 260 {
		t.Fatalf("resized to %v", b.Rect)
	}
	x.dragWith(ad, [2]float64{760, 75}, [2]float64{701, 75}, false, false)
	if b, _ := d.Board(second); b.Rect != image.Rect(500, 0, 700, 150) {
		t.Errorf("the handle did not take the first board's width: %v", b.Rect)
	}
	// Turned off with View › Snapping, it keeps the size dragged.
	x.a.settings.Paint.NoSnap = true
	x.dragWith(ad, [2]float64{1000, 0}, [2]float64{1198, 148}, false, false)
	if b, _ := d.Board(ad.board); b.Rect != image.Rect(1000, 0, 1198, 148) || ad.board == first {
		t.Errorf("without snapping the board is %v", b.Rect)
	}
}

func TestSafeFileName(t *testing.T) {
	for in, want := range map[string]string{"Hero": "Hero", `a/b\c:d*?`: "a_b_c_d__", "  dots... ": "dots", "": "_"} {
		if got := safeFileName(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
