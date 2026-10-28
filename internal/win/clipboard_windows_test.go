package win

import (
	"image"
	"image/color"
	"os"
	"testing"
)

// TestClipboardImageRoundTrip puts a half-transparent picture on the clipboard and reads it back. It replaces what
// is on the clipboard, so it runs only when BVC_CLIPBOARD_TEST is set.
func TestClipboardImageRoundTrip(t *testing.T) {
	if os.Getenv("BVC_CLIPBOARD_TEST") == "" {
		t.Skip("set BVC_CLIPBOARD_TEST to let the test replace the clipboard")
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
