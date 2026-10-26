package caption

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
	"github.com/btelolabs/btelo-video-cut/internal/project"
)

func fonts(t *testing.T) gfx.Fonts {
	f, err := gfx.LoadSystemFonts()
	if err != nil {
		t.Skip(err)
	}
	return f
}

func TestWrapBreaksCJKAndWordsWithinTheWidth(t *testing.T) {
	face := fonts(t).Regular.Face(20)
	width := face.Measure("中文中文")
	lines := face.Wrap("中文中文中文中文中", width+0.5)
	if len(lines) != 3 || lines[0] != "中文中文" || lines[2] != "中" {
		t.Fatalf("%q", lines)
	}
	words := face.Wrap("hello world again", face.Measure("hello world")+1)
	if len(words) != 2 || words[0] != "hello world" || words[1] != "again" {
		t.Fatalf("%q", words)
	}
	if got := face.Wrap("a\n\nb", 1000); len(got) != 3 || got[1] != "" {
		t.Fatalf("%q", got)
	}
	for _, l := range face.Wrap(strings.Repeat("长句子", 30), 200) {
		if face.Measure(l) > 200 {
			t.Fatalf("line %q is %v wide", l, face.Measure(l))
		}
	}
}

func TestCaptionStaysInsideTheFrameAndScalesWithIt(t *testing.T) {
	r := New(fonts(t).Bold)
	text := project.Overlay{Body: "第一行\nSecond line", X: 0.98, Y: 0.98, Size: 0.08, Color: [3]uint8{255, 220, 0}, Outline: true, Box: true, Opacity: 1}
	small := r.Draw(text, 640, 360)
	big := r.Draw(text, 1920, 1080)
	for _, c := range []struct {
		img  Image
		w, h int
	}{{small, 640, 360}, {big, 1920, 1080}} {
		b := c.img.Bounds().Add(c.img.At)
		if b.Min.X < 0 || b.Min.Y < 0 || b.Max.X > c.w || b.Max.Y > c.h {
			t.Fatalf("caption %v leaves the %d×%d frame", b, c.w, c.h)
		}
	}
	ratio := float64(big.Bounds().Dx()) / float64(small.Bounds().Dx())
	if ratio < 2.8 || ratio > 3.2 {
		t.Fatalf("caption width ratio %v, want about 3", ratio)
	}
	path := filepath.Join(t.TempDir(), "c.png")
	if err := big.WritePNG(path); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(path)
	defer f.Close()
	decoded, err := png.Decode(f)
	if err != nil || decoded.Bounds() != big.Bounds() {
		t.Fatalf("png %v %v", decoded.Bounds(), err)
	}
	if _, _, _, a := decoded.At(big.Bounds().Dx()/2, big.Bounds().Dy()/2).RGBA(); a == 0 {
		t.Fatal("caption centre is transparent")
	}
}

func TestPictureKeepsItsShapeAndStaysInFrame(t *testing.T) {
	r := New(fonts(t).Bold)
	src := image.NewRGBA(image.Rect(0, 0, 400, 200))
	img := r.Picture(project.Overlay{X: 1, Y: 0, Size: 0.5, Opacity: 1}, src, 640, 360)
	if img.Bounds().Dx() != 360 || img.Bounds().Dy() != 180 {
		t.Fatalf("picture %v", img.Bounds())
	}
	if img.At.X != 640-360 || img.At.Y != 0 {
		t.Fatalf("picture at %v", img.At)
	}
	if again := r.Picture(project.Overlay{X: 1, Y: 0, Size: 0.5, Opacity: 1}, src, 640, 360); again.RGBA != img.RGBA {
		t.Fatal("picture was not cached")
	}
}

func TestMissingCharactersComeFromFallbackFonts(t *testing.T) {
	f := fonts(t)
	face := f.Regular.Face(24)
	box := face.Measure("□")
	for _, s := range []string{"한국어", "Tiếng Việt"} {
		img := gfx.NewCanvas(200, 40)
		img.Text(face, 0, 0, s, color.RGBA{255, 255, 255, 255})
		ink := 0
		for i := 3; i < len(img.Img.Pix); i += 4 {
			if img.Img.Pix[i] > 0 {
				ink++
			}
		}
		if ink == 0 || face.Measure(s) == float32(len([]rune(s)))*box {
			t.Fatalf("%q drew %d pixels, width %v (box %v)", s, ink, face.Measure(s), box)
		}
	}
}

func TestOpacityFadesTextAndPictures(t *testing.T) {
	fonts, err := gfx.LoadSystemFonts()
	if err != nil {
		t.Skip(err)
	}
	r := New(fonts.Bold)
	text := project.Overlay{Body: "Hello", X: 0.5, Y: 0.5, Size: 0.1, Color: [3]uint8{255, 255, 255}, Box: true, Opacity: 1}
	solid := r.Draw(text, 640, 360)
	text.Opacity = 0.5
	half := r.Draw(text, 640, 360)
	if solid.RGBA == half.RGBA || maxAlpha(half.RGBA)*2 > maxAlpha(solid.RGBA)+2 || maxAlpha(half.RGBA) == 0 {
		t.Fatalf("alpha %d at full opacity, %d at half", maxAlpha(solid.RGBA), maxAlpha(half.RGBA))
	}
	src := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for i := range src.Pix {
		src.Pix[i] = 255
	}
	if a := maxAlpha(r.Picture(project.Overlay{X: 0.5, Y: 0.5, Size: 0.2, Opacity: 0.25}, src, 100, 100).RGBA); a < 60 || a > 68 {
		t.Fatalf("picture alpha %d at a quarter", a)
	}
}

func maxAlpha(img *image.RGBA) uint8 {
	m := uint8(0)
	for i := 3; i < len(img.Pix); i += 4 {
		m = max(m, img.Pix[i])
	}
	return m
}
