package capture

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWritePNGRoundTripAndExclusive(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	img.SetRGBA(10, 20, color.RGBA{R: 210, G: 95, B: 40, A: 255})
	path := filepath.Join(t.TempDir(), "shot.png")
	if err := WritePNG(path, img, true); err != nil {
		t.Fatalf("first exclusive WritePNG: %v", err)
	}
	if err := WritePNG(path, img, true); err == nil {
		t.Fatal("second exclusive WritePNG overwrote an existing file")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := png.Decode(f)
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	if got.Bounds().Dx() != 64 || got.Bounds().Dy() != 48 {
		t.Fatalf("decoded bounds %v", got.Bounds())
	}
	r, g, b, a := got.At(10, 20).RGBA()
	if uint8(r>>8) != 210 || uint8(g>>8) != 95 || uint8(b>>8) != 40 || uint8(a>>8) != 255 {
		t.Fatalf("pixel (10,20) = (%d,%d,%d,%d)", r>>8, g>>8, b>>8, a>>8)
	}
}

func TestBGRAToRGBAConversion(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific pixel swizzle")
	}
	// Test > 256K pixels so the parallel band path is exercised too.
	const n = (1<<18 + 37)
	src := make([]byte, n*4)
	dst := make([]byte, n*4)
	for i := 0; i < n; i++ {
		src[i*4+0] = byte(i)      // B
		src[i*4+1] = byte(i >> 3) // G
		src[i*4+2] = byte(i >> 6) // R
		src[i*4+3] = 0            // undefined GDI alpha
	}
	bgraToRGBA(dst, src)
	for i := range [4]int{0, 1, 1 << 18, n - 1} {
		idx := [4]int{0, 1, 1 << 18, n - 1}[i]
		if dst[idx*4+0] != src[idx*4+2] || dst[idx*4+1] != src[idx*4+1] || dst[idx*4+2] != src[idx*4+0] || dst[idx*4+3] != 255 {
			t.Fatalf("at %d: got RGBA=%v from BGRA=%v", idx, dst[idx*4:idx*4+4], src[idx*4:idx*4+4])
		}
	}
}
