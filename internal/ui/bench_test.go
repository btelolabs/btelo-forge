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
	if path := os.Getenv("BVC_PROFILE"); path != "" {
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
