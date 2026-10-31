// Package codec decodes the two image formats a PDF can embed that neither the
// standard library nor golang.org/x/image covers: JBIG2 and JPEG 2000.
//
// Every input is treated as hostile. The public functions recover from panics,
// bound every allocation by the declared image size and count their work, so a
// crafted file ends in an error instead of a crash or a hang.
package codec

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
)

// maxImagePixels is the hard ceiling on any decoded image, whatever the caller asks for.
const maxImagePixels = 100_000_000

// pixelLimit turns the caller's limit into the one actually enforced.
func pixelLimit(maxPixels int) int {
	if maxPixels <= 0 || maxPixels > maxImagePixels {
		return maxImagePixels
	}
	return maxPixels
}

// fits reports whether w by h is a positive size within limit, without overflowing.
func fits(w, h, limit int) bool {
	if w <= 0 || h <= 0 {
		return false
	}
	return w <= limit && h <= limit/w
}

var errWork = errors.New("the stream needs too much decoding work")

// budget counts work. It stops a small file that claims an enormous amount of it.
type budget struct {
	left int64
}

// spend takes n units and reports when the budget has run out.
func (b *budget) spend(n int) error {
	b.left -= int64(n)
	if b.left < 0 {
		return errWork
	}
	return nil
}

// parallelFor runs fn over [0,n) in chunks on all processors.
// A panic in a worker comes back here as a panic, where the entry point recovers it.
func parallelFor(n, grain int, fn func(lo, hi int)) {
	workers := min(runtime.GOMAXPROCS(0), n/max(grain, 1))
	if workers <= 1 {
		fn(0, n)
		return
	}
	var (
		next   atomic.Int64
		wg     sync.WaitGroup
		failed atomic.Value
	)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					failed.Store(&r)
				}
			}()
			for {
				lo := int(next.Add(int64(grain))) - grain
				if lo >= n {
					return
				}
				fn(lo, min(lo+grain, n))
			}
		}()
	}
	wg.Wait()
	if r := failed.Load(); r != nil {
		panic(*r.(*any))
	}
}
