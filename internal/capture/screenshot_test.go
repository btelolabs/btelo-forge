package capture

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
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
