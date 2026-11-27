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

func TestCropDisplayAndFrameStoreCopyIfChanged(t *testing.T) {
	d := Display{Width: 100, Height: 80}
	snap := image.NewRGBA(image.Rect(0, 0, 100, 80))
	snap.SetRGBA(15, 25, color.RGBA{R: 12, G: 34, B: 56, A: 255})
	// Odd region 31x21 rounds down to even 30x20.
	cropped := CropDisplay(d, snap, image.Rect(10, 20, 41, 41))
	if cropped == nil || cropped.Rect != image.Rect(0, 0, 30, 20) {
		t.Fatalf("cropped rect = %v, want 30x20", cropped)
	}
	if got := cropped.RGBAAt(5, 5); got != (color.RGBA{R: 12, G: 34, B: 56, A: 255}) {
		t.Fatalf("cropped pixel (5,5) = %v, want {12 34 56 255}", got)
	}
	// Mutating snap does not affect cropped (standalone buffer for async save/clipboard).
	snap.SetRGBA(15, 25, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	if got := cropped.RGBAAt(5, 5); got != (color.RGBA{R: 12, G: 34, B: 56, A: 255}) {
		t.Fatalf("cropped aliased source snapshot: %v", got)
	}
	// Empty region returns full display (even-rounded).
	full := CropDisplay(d, snap, image.Rectangle{})
	if full == nil || full.Rect != image.Rect(0, 0, 100, 80) {
		t.Fatalf("full crop rect = %v, want 100x80", full)
	}
	if CropDisplay(d, nil, image.Rect(0, 0, 10, 10)) != nil {
		t.Fatal("CropDisplay(nil) returned non-nil")
	}

	// Verify frameStore.copyIfChanged skips redundant copies when generation is unchanged.
	fs := newFrameStore(image.Pt(4, 2))
	buf := make([]byte, fs.size)
	g1 := fs.copyIfChanged(buf, 0)
	if g1 == 0 || buf[0] != 16 {
		t.Fatalf("initial copyIfChanged: gen=%d y0=%d", g1, buf[0])
	}
	// Sentinel byte stays intact when generation has not changed (proves copy was skipped).
	buf[0] = 99
	if g2 := fs.copyIfChanged(buf, g1); g2 != g1 || buf[0] != 99 {
		t.Fatalf("unchanged generation copied redundantly: g1=%d g2=%d buf[0]=%d", g1, g2, buf[0])
	}
	feed := &screenFeed{}
	fs.own(feed)
	next := make([]byte, fs.size)
	next[0] = 200
	fs.put(feed, next)
	if g3 := fs.copyIfChanged(buf, g1); g3 == g1 || buf[0] != 200 {
		t.Fatalf("changed generation did not copy: g1=%d g3=%d buf[0]=%d", g1, g3, buf[0])
	}
}
