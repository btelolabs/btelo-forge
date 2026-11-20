package win

import (
	"image"
	"image/color"
	"os"
	"testing"
)

// TestClipboardImageRoundTrip puts a half-transparent picture on the clipboard and reads it back. It replaces what
// is on the clipboard, so it runs only when FORGE_CLIPBOARD_TEST is set.
func TestClipboardImageRoundTrip(t *testing.T) {
	if os.Getenv("FORGE_CLIPBOARD_TEST") == "" {
		t.Skip("set FORGE_CLIPBOARD_TEST to let the test replace the clipboard")
	}
	w, err := Open("clipboard test", 100, 100, Background)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	src := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	src.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	src.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 128})
	src.SetNRGBA(2, 1, color.NRGBA{B: 255, A: 255})
	if err := w.SetClipboardImage(src); err != nil {
		t.Fatal(err)
	}
	img, _, _ := w.Paste()
	if img == nil || img.Bounds().Dx() != 3 || img.Bounds().Dy() != 2 {
		t.Fatalf("read back %v", img)
	}
	for _, p := range []image.Point{{0, 0}, {1, 0}, {2, 1}, {1, 1}} {
		want := color.NRGBAModel.Convert(src.At(p.X, p.Y)).(color.NRGBA)
		got := color.NRGBAModel.Convert(img.At(p.X, p.Y)).(color.NRGBA)
		if got != want {
			t.Errorf("pixel %v is %v, want %v", p, got, want)
		}
	}
}

func TestFillClipboardDIBOpaqueAndTranslucent(t *testing.T) {
	rgba := image.NewRGBA(image.Rect(0, 0, 2, 2))
	rgba.SetRGBA(0, 0, color.RGBA{R: 200, G: 100, B: 50, A: 255})
	rgba.SetRGBA(1, 0, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	rgba.SetRGBA(0, 1, color.RGBA{R: 40, G: 50, B: 60, A: 255})
	rgba.SetRGBA(1, 1, color.RGBA{R: 70, G: 80, B: 90, A: 255})
	dst := make([]byte, 2*2*4)
	if !fillClipboardDIB(dst, rgba, rgba.Bounds(), 2, 2) {
		t.Fatal("fully opaque RGBA reported as non-opaque")
	}
	// Bottom-up row 0 in DIB is y=1 of image: (0,1) = B=60,G=50,R=40,0xff.
	if dst[0] != 60 || dst[1] != 50 || dst[2] != 40 || dst[3] != 0xff {
		t.Fatalf("bottom-left DIB pixel = %v, want [60 50 40 255]", dst[:4])
	}

	nrgba := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	nrgba.SetNRGBA(0, 0, color.NRGBA{R: 255, G: 0, B: 0, A: 255})
	nrgba.SetNRGBA(1, 0, color.NRGBA{R: 0, G: 255, B: 0, A: 128})
	dst2 := make([]byte, 2*1*4)
	if fillClipboardDIB(dst2, nrgba, nrgba.Bounds(), 2, 1) {
		t.Fatal("translucent NRGBA reported as opaque")
	}
}
