package anim

import (
	"bytes"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/media"
)

func TestGIFFramesComposeAndLoop(t *testing.T) {
	g := &gif.GIF{Config: image.Config{Width: 4, Height: 2, ColorModel: color.Palette(palette.Plan9)}}
	red := image.NewPaletted(image.Rect(0, 0, 4, 2), palette.Plan9)
	for i := range red.Pix {
		red.Pix[i] = uint8(red.Palette.Index(color.RGBA{255, 0, 0, 255}))
	}
	blue := image.NewPaletted(image.Rect(2, 0, 4, 2), palette.Plan9)
	for i := range blue.Pix {
		blue.Pix[i] = uint8(blue.Palette.Index(color.RGBA{0, 0, 255, 255}))
	}
	g.Image = []*image.Paletted{red, blue}
	g.Delay = []int{5, 0}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	a, err := Decode(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if a.Len() != 2 || a.W != 4 || a.H != 2 || a.Duration() != 50*time.Millisecond+minDelay {
		t.Fatalf("%d frames, %d×%d, %v", a.Len(), a.W, a.H, a.Duration())
	}
	a.Next()
	img, d, err := a.Next()
	if err != nil || d != minDelay {
		t.Fatal(err, d)
	}
	// The second frame covers only the right half; the left half still shows the first.
	if img.NRGBAAt(0, 0).R != 255 || img.NRGBAAt(3, 1).B != 255 {
		t.Fatalf("%v %v", img.NRGBAAt(0, 0), img.NRGBAAt(3, 1))
	}
	if img, _, _ := a.Next(); img.NRGBAAt(3, 1).R != 255 {
		t.Fatal("the loop should start again from the first frame")
	}
}

func TestStillImagesAreNotAnimations(t *testing.T) {
	g := &gif.GIF{Image: []*image.Paletted{image.NewPaletted(image.Rect(0, 0, 2, 2), palette.Plan9)}, Delay: []int{0}}
	var buf bytes.Buffer
	gif.EncodeAll(&buf, g)
	if _, err := Decode(buf.Bytes()); err != ErrNotAnimated {
		t.Fatal(err)
	}
	if img, err := First(buf.Bytes()); err != nil || img.Bounds().Dx() != 2 {
		t.Fatal(err)
	}
	if _, err := Decode([]byte("RIFF\x04\x00\x00\x00WEBPjunk")); err == nil {
		t.Fatal("a damaged WebP should be refused")
	}
}

func TestWebPFromFFmpegPlays(t *testing.T) {
	if err := media.Available(); err != nil {
		t.Skip(err)
	}
	out := filepath.Join(t.TempDir(), "a.webp")
	cmd, err := media.Command(t.Context(), "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=s=160x90:r=10:d=1",
		"-c:v", "libwebp_anim", "-quality", "70", "-loop", "0", "-pix_fmt", "yuv420p", "-f", "webp", out)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("this ffmpeg cannot write animated WebP: %v %s", err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	a, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if a.Len() != 10 || a.W != 160 || a.H != 90 || a.Duration() != time.Second {
		t.Fatalf("%d frames %d×%d %v", a.Len(), a.W, a.H, a.Duration())
	}
	for range a.Len() {
		img, _, err := a.Next()
		if err != nil {
			t.Fatal(err)
		}
		if img.NRGBAAt(80, 45).A != 255 {
			t.Fatal("an opaque animation decoded with holes")
		}
	}
}
