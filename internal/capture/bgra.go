package capture

import (
	"sync"
	"unsafe"
)

// bgraToRGBA swaps blue and red and sets every pixel opaque, four bytes at a time, in parallel bands.
func bgraToRGBA(dst, src []byte) {
	if len(dst) < 4 || len(src) < len(dst) {
		return
	}
	d := unsafe.Slice((*uint32)(unsafe.Pointer(unsafe.SliceData(dst))), len(dst)/4)
	s := unsafe.Slice((*uint32)(unsafe.Pointer(unsafe.SliceData(src))), len(dst)/4)
	swap := func(d, s []uint32) {
		for i, v := range s {
			d[i] = v&0x0000ff00 | v>>16&0xff | v&0xff<<16 | 0xff000000
		}
	}
	const band = 1 << 18
	if len(s) <= band {
		swap(d, s)
		return
	}
	var wg sync.WaitGroup
	for from := 0; from < len(s); from += band {
		to := min(from+band, len(s))
		wg.Add(1)
		go func() {
			defer wg.Done()
			swap(d[from:to], s[from:to])
		}()
	}
	wg.Wait()
}
