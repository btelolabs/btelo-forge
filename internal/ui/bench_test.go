package ui

import (
	"os"
	"runtime/pprof"
	"testing"
	"time"
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
