package thumb

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	return img
}

func TestAFileEndsWithItsPicture(t *testing.T) {
	end, err := Encode(solid(2000, 1000, color.RGBA{10, 200, 30, 255}), Meta{W: 2000, H: 1000, Duration: 12.5})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "a.bin")
	body := []byte("what the file holds")
	if err := os.WriteFile(path, append(bytes.Clone(body), end...), 0o644); err != nil {
		t.Fatal(err)
	}
	img, m, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if m != (Meta{W: 2000, H: 1000, Duration: 12.5}) {
		t.Errorf("meta %+v", m)
	}
	if img.Rect.Dx() != Box || img.Rect.Dy() != Box/2 {
		t.Errorf("picture of %v", img.Rect)
	}
	if c := img.RGBAAt(100, 100); c.G < 180 || c.R > 40 {
		t.Errorf("colour %v", c)
	}
	data, _ := os.ReadFile(path)
	if !bytes.Equal(Strip(data), body) {
		t.Errorf("stripped to %q", Strip(data))
	}
	raw, err := Raw(path)
	if err != nil || !bytes.Equal(raw, end) {
		t.Errorf("raw: %v", err)
	}
}

func TestAPictureLeavesTransparencyOnACheckerboard(t *testing.T) {
	end, err := Encode(solid(64, 64, color.RGBA{}), Meta{W: 64, H: 64})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "a.bin")
	if err := os.WriteFile(path, end, 0o644); err != nil {
		t.Fatal(err)
	}
	img, _, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if a, b := img.RGBAAt(6, 6), img.RGBAAt(18, 6); a.R < 240 || b.R > 240 {
		t.Errorf("cells %v and %v", a, b)
	}
}

func TestFilesWithoutAPicture(t *testing.T) {
	dir := t.TempDir()
	cases := map[string][]byte{
		"short":  []byte("x"),
		"plain":  bytes.Repeat([]byte("plain text "), 20),
		"length": append(make([]byte, 40), append([]byte{0xff, 0xff, 0xff, 0x7f}, append(make([]byte, 12), mark...)...)...),
		"empty":  append(make([]byte, 40), append(make([]byte, 16), mark...)...),
		"long":   append([]byte{1}, append([]byte{200, 0, 0, 0}, append(make([]byte, 12), mark...)...)...),
	}
	for name, data := range cases {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Read(path); !errors.Is(err, ErrNone) {
			t.Errorf("%s: %v", name, err)
		}
		if !bytes.Equal(Strip(data), data) {
			t.Errorf("%s was cut", name)
		}
	}
}

func TestANoisyPictureStillFits(t *testing.T) {
	img := solid(Box, Box, color.RGBA{A: 255})
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2] = uint8(rng.IntN(256)), uint8(rng.IntN(256)), uint8(rng.IntN(256))
	}
	end, err := Encode(img, Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if len(end) > MaxSize || bytes.Contains(end, zipEnd) {
		t.Errorf("%d bytes", len(end))
	}
}
