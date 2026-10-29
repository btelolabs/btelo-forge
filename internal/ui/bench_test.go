package ui

import (
	"image"
	"image/color"
	"os"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/paint"
)

func BenchmarkFrame(b *testing.B) {
	x := newHarness(b)
	x.c.Resize(1800, 1125)
	x.scale = 1.25
	src := x.fixture()
	x.frame(Input{Dropped: []string{src}})
	x.settle(30 * time.Second)
	x.a.seek(1)
	x.key('T', false)
	x.frame(Input{Keys: []Key{{Code: keyEscape}}})
	x.settle(30 * time.Second)
	if path := os.Getenv("FORGE_PROFILE"); path != "" {
		f, _ := os.Create(path)
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		x.mx, x.my = float32(400+i%800), 900
		x.frame(Input{})
	}
}

// BenchmarkCanvasZoom draws a busy canvas at a new zoom every frame, so the whole view is drawn again each time.
func BenchmarkCanvasZoom(b *testing.B) {
	x, ad := canvasHarness(b)
	x.c.Resize(1800, 1125)
	d := ad.d
	for i := range 40 {
		d.AddVector(solidSquare(float64(i%8)*45, float64(i/8)*55, 40, red), "Shape")
	}
	x.frame(Input{})
	if path := os.Getenv("FORGE_PROFILE"); path != "" {
		f, _ := os.Create(path)
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ad.zoom = 1.5 + float64(i%7)*0.25
		ad.fit = false
		x.frame(Input{})
	}
}

// boardsCanvas is a new canvas with two 1024×1024 artboards, each with a painted layer and shapes, fitted to an
// 1800×1125 window.
func boardsCanvas(b testing.TB) (*harness, *artDoc, paint.Board) {
	x := newHarness(b)
	x.a.settings.ExportDir = x.mediaDir()
	x.c.Resize(1800, 1125)
	if err := x.a.newCanvas(); err != nil {
		b.Fatal(err)
	}
	ad := x.a.art
	d := ad.d
	d.Select(image.Rect(100, 100, 900, 900))
	d.Fill(color.NRGBA{R: 200, G: 120, B: 40, A: 255})
	d.Deselect()
	for i := range 20 {
		d.AddVector(solidSquare(float64(i%5)*180+40, float64(i/5)*200+40, 120, red), "Shape")
	}
	second, err := d.AddBoard(image.Rect(1200, 0, 2224, 1024), "Artboard 2")
	if err != nil {
		b.Fatal(err)
	}
	d.AddLayer("Paint")
	d.Select(image.Rect(1300, 100, 2100, 900))
	d.Fill(color.NRGBA{G: 160, B: 220, A: 255})
	d.Deselect()
	ad.changedAll()
	ad.fitView()
	x.frame(Input{})
	x.frame(Input{})
	return x, ad, second
}

// benchDrag drags with the move tool from canvas point from, a step every frame, the profile kept in FORGE_PROFILE.
func benchDrag(b *testing.B, x *harness, ad *artDoc, from [2]float64) {
	x.key('V', false)
	fx, fy := x.viewPoint(ad, from[0], from[1])
	x.mx, x.my = fx, fy
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: fx, Y: fy, Down: true}}})
	if path := os.Getenv("FORGE_PROFILE"); path != "" {
		f, _ := os.Create(path)
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		x.mx, x.my = fx+float32(i%60)*3, fy+float32(i%40)*2
		x.frame(Input{Down: true})
	}
}

// BenchmarkCanvasMoveLayer drags a painted layer on an artboard with the move tool.
func BenchmarkCanvasMoveLayer(b *testing.B) {
	x, ad, _ := boardsCanvas(b)
	x.a.settings.Paint.NoAutoSelect = true
	benchDrag(b, x, ad, [2]float64{1700, 500})
}

// BenchmarkCanvasMoveBoard drags an artboard, with everything on it, with the move tool.
func BenchmarkCanvasMoveBoard(b *testing.B) {
	x, ad, second := boardsCanvas(b)
	x.a.settings.Paint.NoAutoSelect = true
	ad.d.Choose(second.ID)
	benchDrag(b, x, ad, [2]float64{1700, 500})
}

// BenchmarkCanvasMovePasted drags a large picture just pasted on an artboard, its selection round it, as a paste
// leaves it: the selection holds the whole layer, which so moves whole rather than lifting its pixels each frame.
func BenchmarkCanvasMovePasted(b *testing.B) {
	x, ad, second := boardsCanvas(b)
	x.a.settings.Paint.NoAutoSelect = true
	d := ad.d
	d.Choose(second.ID)
	photo := image.NewRGBA(image.Rect(0, 0, 3000, 2000))
	for i := range photo.Pix {
		photo.Pix[i] = uint8(i * 7)
	}
	d.Paste(photo, "Photo", &image.Point{X: 1200})
	ad.changedAll()
	x.frame(Input{})
	benchDrag(b, x, ad, [2]float64{1700, 500})
}
