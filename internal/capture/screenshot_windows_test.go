package capture

import "testing"

// bgraToRGBA swaps blue and red and makes every pixel opaque, also past one band, where it works in parallel.
func TestBGRAToRGBAConversion(t *testing.T) {
	const n = 1<<18 + 37
	src := make([]byte, n*4)
	dst := make([]byte, n*4)
	for i := 0; i < n; i++ {
		src[i*4+0] = byte(i)      // B
		src[i*4+1] = byte(i >> 3) // G
		src[i*4+2] = byte(i >> 6) // R
		src[i*4+3] = 0            // GDI leaves alpha undefined
	}
	bgraToRGBA(dst, src)
	for _, i := range []int{0, 1, 1<<18 - 1, 1 << 18, n - 1} {
		d, s := dst[i*4:i*4+4], src[i*4:i*4+4]
		if d[0] != s[2] || d[1] != s[1] || d[2] != s[0] || d[3] != 255 {
			t.Fatalf("at %d: RGBA %v from BGRA %v", i, d, s)
		}
	}
}
