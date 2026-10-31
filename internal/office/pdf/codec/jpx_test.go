package codec

import (
	"bytes"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type jpxFixture struct {
	file        string
	w, h, comps int
	lossless    bool
	floor       float64
}

func loadFixtures(t *testing.T) []jpxFixture {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("testdata", "manifest.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var out []jpxFixture
	for line := range strings.Lines(string(text)) {
		var f jpxFixture
		var lossless int
		if _, err := fmt.Sscan(line, &f.file, &f.w, &f.h, &f.comps, &lossless, &f.floor); err != nil {
			t.Fatal(err)
		}
		f.lossless = lossless != 0
		out = append(out, f)
	}
	return out
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func psnr(a, b []byte) float64 {
	var sum float64
	for i := range a {
		d := float64(a[i]) - float64(b[i])
		sum += d * d
	}
	if sum == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(255*255*float64(len(a))/sum)
}

func TestJPXFixtures(t *testing.T) {
	for _, f := range loadFixtures(t) {
		t.Run(f.file, func(t *testing.T) {
			base := strings.TrimSuffix(f.file, filepath.Ext(f.file))
			img, err := JPX(readTestdata(t, f.file), 0)
			if err != nil {
				t.Fatal(err)
			}
			if img.Width != f.w || img.Height != f.h || img.Components != f.comps {
				t.Fatalf("got %dx%d %d components, want %dx%d %d", img.Width, img.Height, img.Components, f.w, f.h, f.comps)
			}
			want := readTestdata(t, base+".raw")
			if len(img.Pix) != len(want) {
				t.Fatalf("got %d bytes, want %d", len(img.Pix), len(want))
			}
			if f.lossless {
				if !bytes.Equal(img.Pix, want) {
					first := 0
					for img.Pix[first] == want[first] {
						first++
					}
					t.Fatalf("pixels differ from byte %d, PSNR %.1f", first, psnr(img.Pix, want))
				}
				return
			}
			// Lossy: close to OpenJPEG, whose rounding differs a little, and to the source.
			biggest := 0
			for i := range want {
				biggest = max(biggest, int(img.Pix[i])-int(want[i]), int(want[i])-int(img.Pix[i]))
			}
			if biggest > 2 {
				t.Errorf("up to %d away from OpenJPEG", biggest)
			}
			if p := psnr(img.Pix, readTestdata(t, base+".src")); p < f.floor {
				t.Errorf("PSNR %.1f is below %.1f", p, f.floor)
			}
		})
	}
}

func TestJPXBoxes(t *testing.T) {
	cases := []struct {
		file  string
		space string
		alpha bool
	}{
		{"rgb_lrcp.jp2", "sRGB", false},
		{"gray.jp2", "sGray", false},
		{"rgba.jp2", "sRGB", true},
		{"la.jp2", "sGray", true},
		{"rgb_rlcp.j2k", "", false},
	}
	for _, c := range cases {
		img, err := JPX(readTestdata(t, c.file), 0)
		if err != nil {
			t.Fatal(c.file, err)
		}
		if img.ColorSpace != c.space || img.HasAlpha != c.alpha {
			t.Errorf("%s: colour space %q alpha %v", c.file, img.ColorSpace, img.HasAlpha)
		}
	}
	img, err := JPX(readTestdata(t, "gray16.jp2"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if img.BitDepth != 16 {
		t.Errorf("bit depth %d", img.BitDepth)
	}
}

func TestJPXLimit(t *testing.T) {
	data := readTestdata(t, "gray.jp2")
	if _, err := JPX(data, 100); err == nil {
		t.Error("an image over the pixel limit was decoded")
	}
	if _, err := JPX(data, 48*40); err != nil {
		t.Error(err)
	}
}

// fuzzJPX feeds damaged copies of a stream and only asks that each one ends.
func fuzzJPX(t *testing.T, seed []byte, rounds int, rng *rand.Rand) {
	t.Helper()
	check := func(data []byte) {
		start := time.Now()
		img, err := JPX(data, 1<<16)
		if err == nil && len(img.Pix) != img.Width*img.Height*img.Components {
			t.Fatalf("wrong buffer size for %dx%d", img.Width, img.Height)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("one decode took %v", d)
		}
	}
	for i := 0; i < len(seed); i += 1 + len(seed)/64 {
		check(seed[:i])
	}
	for range rounds {
		data := bytes.Clone(seed)
		for range 1 + rng.IntN(8) {
			pos := rng.IntN(len(data))
			// Headers matter most: favour the first bytes half of the time.
			if rng.IntN(2) == 0 {
				pos = rng.IntN(min(len(data), 200))
			}
			data[pos] = byte(rng.Uint32())
		}
		check(data)
	}
}

func TestJPXFuzz(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, f := range loadFixtures(t) {
		fuzzJPX(t, readTestdata(t, f.file), 60, rng)
	}
}
