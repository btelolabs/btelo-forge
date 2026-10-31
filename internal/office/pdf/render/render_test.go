package render

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/flow"
	"github.com/btelolabs/btelo-forge/internal/office/pdf"
	"github.com/btelolabs/btelo-forge/internal/office/pdf/pdftest"
)

// onePage builds a 100×100 page with a content stream and resources.
func onePage(content, resources string, extra func(b *pdftest.Builder)) []byte {
	b := pdftest.New()
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Contents 4 0 R /Resources << "+resources+" >> >>")
	b.Stream(4, "", []byte(content))
	if extra != nil {
		extra(b)
	}
	return b.Bytes()
}

func paintOf(t *testing.T, data []byte) *image.RGBA {
	t.Helper()
	d, err := pdf.Open(data, "")
	if err != nil {
		t.Fatal(err)
	}
	cx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res := NewCache(d, testFonts(t)).Page(cx, d.Pages()[0], Options{Scale: 1})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	return res.Img
}

var bookFonts *BookFonts

func testFonts(t *testing.T) *BookFonts {
	if bookFonts == nil {
		f, err := gfx.LoadSystemFonts()
		if err != nil {
			t.Skip(err)
		}
		bookFonts = &BookFonts{Book: flow.NewBook(f)}
	}
	return bookFonts
}

// at reads a pixel by PDF coordinates.
func at(img *image.RGBA, x, y int) color.RGBA { return img.RGBAAt(x, 99-y) }

func near(c color.RGBA, r, g, b uint8) bool {
	d := func(a, b uint8) int {
		if a > b {
			return int(a - b)
		}
		return int(b - a)
	}
	return d(c.R, r) <= 3 && d(c.G, g) <= 3 && d(c.B, b) <= 3
}

func want(t *testing.T, img *image.RGBA, x, y int, r, g, b uint8) {
	t.Helper()
	if c := at(img, x, y); !near(c, r, g, b) {
		t.Errorf("at %d,%d: %v, want %d %d %d", x, y, c, r, g, b)
	}
}

func TestFillRulesAndClip(t *testing.T) {
	// Two nested squares wound the same way: non-zero fills the middle, even-odd does not.
	sq := "10 10 80 80 re 30 30 40 40 re "
	want(t, paintOf(t, onePage("1 0 0 rg "+sq+"f", "", nil)), 50, 50, 255, 0, 0)
	want(t, paintOf(t, onePage("1 0 0 rg "+sq+"f*", "", nil)), 50, 50, 255, 255, 255)
	img := paintOf(t, onePage("q 0 0 50 100 re W n 0 0 1 rg 0 0 100 100 re f Q", "", nil))
	want(t, img, 25, 50, 0, 0, 255)
	want(t, img, 75, 50, 255, 255, 255)
	// A round clip.
	img = paintOf(t, onePage("q 50 90 m 90 90 90 50 90 50 c 10 10 l h W n 0 1 0 rg 0 0 100 100 re f Q", "", nil))
	want(t, img, 5, 95, 255, 255, 255)
}

func TestStrokes(t *testing.T) {
	img := paintOf(t, onePage("10 w 0 J 20 50 m 80 50 l S", "", nil))
	want(t, img, 50, 50, 0, 0, 0)
	want(t, img, 50, 53, 0, 0, 0)
	want(t, img, 50, 60, 255, 255, 255)
	want(t, img, 15, 50, 255, 255, 255) // a butt cap stops at the end
	img = paintOf(t, onePage("10 w 2 J 20 50 m 80 50 l S", "", nil))
	want(t, img, 17, 50, 0, 0, 0)
	img = paintOf(t, onePage("10 w 1 J 20 50 m 80 50 l S", "", nil))
	want(t, img, 17, 50, 0, 0, 0)
	want(t, img, 15, 56, 255, 255, 255)
	// A dash: on, off.
	img = paintOf(t, onePage("4 w [10 10] 0 d 0 50 m 100 50 l S", "", nil))
	want(t, img, 5, 50, 0, 0, 0)
	want(t, img, 15, 50, 255, 255, 255)
	// A miter join reaches out; a bevel does not.
	img = paintOf(t, onePage("10 w 0 j 20 20 m 50 80 l 80 20 l S", "", nil))
	want(t, img, 50, 88, 0, 0, 0)
	img = paintOf(t, onePage("10 w 2 j 20 20 m 50 80 l 80 20 l S", "", nil))
	want(t, img, 50, 88, 255, 255, 255)
}

func TestTransparencyAndBlend(t *testing.T) {
	res := "/ExtGState << /A << /ca 0.5 >> /M << /BM /Multiply >> /S << /BM /Screen >> >>"
	img := paintOf(t, onePage("/A gs 1 0 0 rg 0 0 100 100 re f", res, nil))
	want(t, img, 50, 50, 255, 128, 128)
	img = paintOf(t, onePage("0.5 0.5 1 rg 0 0 100 100 re f /M gs 1 1 0 rg 0 0 50 100 re f", res, nil))
	want(t, img, 25, 50, 128, 128, 0)
	want(t, img, 75, 50, 128, 128, 255)
	img = paintOf(t, onePage("0.5 g 0 0 100 100 re f /S gs 0.5 g 0 0 100 100 re f", res, nil))
	want(t, img, 25, 50, 191, 191, 191)
}

func TestGroupAlphaIsOneObject(t *testing.T) {
	// Two overlapping squares in a group at half alpha: their overlap is no darker than the rest.
	data := onePage("/A gs /G Do", "/ExtGState << /A << /ca 0.5 >> >> /XObject << /G 5 0 R >>", func(b *pdftest.Builder) {
		b.Stream(5, "/Type /XObject /Subtype /Form /BBox [0 0 100 100] /Group << /S /Transparency >>", []byte("0 g 0 0 60 100 re f 40 0 60 100 re f"))
	})
	img := paintOf(t, data)
	want(t, img, 20, 50, 128, 128, 128)
	want(t, img, 50, 50, 128, 128, 128)
}

func TestSoftMask(t *testing.T) {
	data := onePage("/M gs 1 0 0 rg 0 0 100 100 re f", "/ExtGState << /M << /SMask << /S /Luminosity /G 5 0 R >> >> >>", func(b *pdftest.Builder) {
		b.Stream(5, "/Type /XObject /Subtype /Form /BBox [0 0 100 100] /Group << /S /Transparency /CS /DeviceGray >>", []byte("1 g 0 0 50 100 re f"))
	})
	img := paintOf(t, data)
	want(t, img, 25, 50, 255, 0, 0)
	want(t, img, 75, 50, 255, 255, 255)
}

func TestShadings(t *testing.T) {
	fn := "<< /FunctionType 2 /Domain [0 1] /N 1 /C0 [1 0 0] /C1 [0 0 1] >>"
	axial := "/Shading << /S << /ShadingType 2 /ColorSpace /DeviceRGB /Coords [0 0 100 0] /Function " + fn + " >> >>"
	img := paintOf(t, onePage("/S sh", axial, nil))
	want(t, img, 1, 50, 252, 0, 3)
	want(t, img, 98, 50, 3, 0, 252)
	want(t, img, 50, 50, 126, 0, 128)
	radial := "/Shading << /S << /ShadingType 3 /ColorSpace /DeviceRGB /Coords [50 50 0 50 50 50] /Function " + fn + " >> >>"
	img = paintOf(t, onePage("/S sh", radial, nil))
	want(t, img, 50, 50, 252, 0, 3)
	want(t, img, 2, 2, 255, 255, 255) // past the circle, with no extend
	// Function based, type 1, through a PostScript calculator.
	data := onePage("/S sh", "/Shading << /S << /ShadingType 1 /ColorSpace /DeviceRGB /Matrix [100 0 0 100 0 0] /Function 5 0 R >> >>", func(b *pdftest.Builder) {
		b.Stream(5, "/FunctionType 4 /Domain [0 1 0 1] /Range [0 1 0 1 0 1]", []byte("{ pop dup 0.5 gt { pop 0 1 0 } { pop 1 0 0 } ifelse }"))
	})
	img = paintOf(t, data)
	want(t, img, 25, 50, 255, 0, 0)
	want(t, img, 75, 50, 0, 255, 0)
	// A free-form Gouraud triangle, type 4, flags and coordinates 8 bits.
	data = onePage("/S sh", "/Shading << /S 5 0 R >>", func(b *pdftest.Builder) {
		b.Stream(5, "/ShadingType 4 /ColorSpace /DeviceRGB /BitsPerCoordinate 8 /BitsPerComponent 8 /BitsPerFlag 8 /Decode [0 100 0 100 0 1 0 1 0 1]",
			[]byte{0, 0, 0, 255, 0, 0, 0, 255, 0, 0, 255, 0, 0, 0, 255, 0, 255, 0})
	})
	img = paintOf(t, data)
	want(t, img, 95, 95, 255, 255, 255)
	if c := at(img, 30, 30); c.R == 255 && c.G == 255 {
		t.Errorf("the triangle did not paint: %v", c)
	}
	// A Coons patch, type 6, the unit square.
	data = onePage("/S sh", "/Shading << /S 5 0 R >>", func(b *pdftest.Builder) {
		pts := []byte{0, 0, 0, 0, 85, 0, 170, 0, 255, 85, 255, 170, 255, 255, 255, 255, 170, 255, 85, 255, 0, 170, 0, 85, 0}
		pts = append(pts, 255, 0, 0, 255, 0, 0, 255, 0, 0, 255, 0, 0)
		b.Stream(5, "/ShadingType 6 /ColorSpace /DeviceRGB /BitsPerCoordinate 8 /BitsPerComponent 8 /BitsPerFlag 8 /Decode [0 100 0 100 0 1 0 1 0 1]", pts)
	})
	want(t, paintOf(t, data), 50, 50, 255, 0, 0)
}

func TestTiling(t *testing.T) {
	data := onePage("/Pattern cs /P scn 0 0 100 100 re f", "/Pattern << /P 5 0 R >>", func(b *pdftest.Builder) {
		b.Stream(5, "/PatternType 1 /PaintType 1 /TilingType 1 /BBox [0 0 20 20] /XStep 20 /YStep 20 /Resources << >>", []byte("1 0 0 rg 0 0 10 20 re f 0 0 1 rg 10 0 10 20 re f"))
	})
	img := paintOf(t, data)
	want(t, img, 5, 50, 255, 0, 0)
	want(t, img, 15, 50, 0, 0, 255)
	want(t, img, 25, 50, 255, 0, 0)
	// An uncoloured one takes the colour given.
	data = onePage("/C cs 0 1 0 /P scn 0 0 100 100 re f", "/ColorSpace << /C [/Pattern /DeviceRGB] >> /Pattern << /P 5 0 R >>", func(b *pdftest.Builder) {
		b.Stream(5, "/PatternType 1 /PaintType 2 /TilingType 1 /BBox [0 0 20 20] /XStep 20 /YStep 20 /Resources << >>", []byte("0 0 10 20 re f"))
	})
	img = paintOf(t, data)
	want(t, img, 5, 50, 0, 255, 0)
	want(t, img, 15, 50, 255, 255, 255)
}

func TestColourSpaces(t *testing.T) {
	sep := "/ColorSpace << /S [/Separation /Spot /DeviceRGB << /FunctionType 2 /Domain [0 1] /N 1 /C0 [1 1 1] /C1 [1 0 0] >>] " +
		"/I [/Indexed /DeviceRGB 1 <FF0000 00FF00>] /L [/Lab << /WhitePoint [0.95 1 1.09] /Range [-128 127 -128 127] >>] " +
		"/C [/CalRGB << /WhitePoint [0.95 1 1.09] >>] >>"
	want(t, paintOf(t, onePage("/S cs 1 scn 0 0 100 100 re f", sep, nil)), 50, 50, 255, 0, 0)
	want(t, paintOf(t, onePage("/S cs 0.5 scn 0 0 100 100 re f", sep, nil)), 50, 50, 255, 128, 128)
	want(t, paintOf(t, onePage("/I cs 1 scn 0 0 100 100 re f", sep, nil)), 50, 50, 0, 255, 0)
	want(t, paintOf(t, onePage("/L cs 100 0 0 scn 0 0 100 100 re f", sep, nil)), 50, 50, 255, 255, 255)
	want(t, paintOf(t, onePage("/L cs 0 0 0 scn 0 0 100 100 re f", sep, nil)), 50, 50, 0, 0, 0)
	// A K-only black is the near black of ink.
	c := at(paintOf(t, onePage("0 0 0 1 k 0 0 100 100 re f", "", nil)), 50, 50)
	if c.R > 45 || c.R < 25 {
		t.Errorf("CMYK black %v", c)
	}
}

func imagePage(dict string, data []byte, res string) []byte {
	return onePage("q 100 0 0 100 0 0 cm /I Do Q", "/XObject << /I 5 0 R >> "+res, func(b *pdftest.Builder) {
		b.Stream(5, "/Type /XObject /Subtype /Image "+dict, data)
	})
}

func TestImages(t *testing.T) {
	// 2×1 RGB.
	img := paintOf(t, imagePage("/Width 2 /Height 1 /BitsPerComponent 8 /ColorSpace /DeviceRGB", []byte{255, 0, 0, 0, 0, 255}, ""))
	want(t, img, 10, 50, 255, 0, 0)
	want(t, img, 90, 50, 0, 0, 255)
	// 1 bit grey, inverted by Decode.
	img = paintOf(t, imagePage("/Width 2 /Height 1 /BitsPerComponent 1 /ColorSpace /DeviceGray /Decode [1 0]", []byte{0x40}, ""))
	want(t, img, 10, 50, 255, 255, 255)
	want(t, img, 90, 50, 0, 0, 0)
	// A stencil paints where the bit is 0.
	data := onePage("0 1 0 rg q 100 0 0 100 0 0 cm /I Do Q", "/XObject << /I 5 0 R >>", func(b *pdftest.Builder) {
		b.Stream(5, "/Type /XObject /Subtype /Image /ImageMask true /Width 2 /Height 1 /BitsPerComponent 1", []byte{0x40})
	})
	img = paintOf(t, data)
	want(t, img, 10, 50, 0, 255, 0)
	want(t, img, 90, 50, 255, 255, 255)
	// A soft mask on an image.
	data = onePage("q 100 0 0 100 0 0 cm /I Do Q", "/XObject << /I 5 0 R >>", func(b *pdftest.Builder) {
		b.Stream(5, "/Type /XObject /Subtype /Image /Width 1 /Height 1 /BitsPerComponent 8 /ColorSpace /DeviceRGB /SMask 6 0 R", []byte{255, 0, 0})
		b.Stream(6, "/Type /XObject /Subtype /Image /Width 2 /Height 1 /BitsPerComponent 8 /ColorSpace /DeviceGray", []byte{255, 0})
	})
	img = paintOf(t, data)
	want(t, img, 5, 50, 255, 0, 0)
	want(t, img, 95, 50, 255, 255, 255)
	// A stencil /Mask.
	data = onePage("q 100 0 0 100 0 0 cm /I Do Q", "/XObject << /I 5 0 R >>", func(b *pdftest.Builder) {
		b.Stream(5, "/Type /XObject /Subtype /Image /Width 1 /Height 1 /BitsPerComponent 8 /ColorSpace /DeviceRGB /Mask 6 0 R", []byte{255, 0, 0})
		b.Stream(6, "/Type /XObject /Subtype /Image /ImageMask true /Width 2 /Height 1 /BitsPerComponent 1", []byte{0x40})
	})
	img = paintOf(t, data)
	want(t, img, 10, 50, 255, 0, 0)
	want(t, img, 90, 50, 255, 255, 255)
	// A colour key.
	img = paintOf(t, imagePage("/Width 2 /Height 1 /BitsPerComponent 8 /ColorSpace /DeviceRGB /Mask [250 255 0 5 0 5]", []byte{255, 0, 0, 0, 0, 255}, ""))
	want(t, img, 10, 50, 255, 255, 255)
	want(t, img, 90, 50, 0, 0, 255)
	// A JPEG.
	src := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for i := range src.Pix {
		src.Pix[i] = 255
		if i%4 == 1 || i%4 == 2 {
			src.Pix[i] = 0
		}
	}
	var j bytes.Buffer
	jpeg.Encode(&j, src, &jpeg.Options{Quality: 95})
	img = paintOf(t, imagePage("/Width 16 /Height 16 /BitsPerComponent 8 /ColorSpace /DeviceRGB /Filter /DCTDecode", j.Bytes(), ""))
	if c := at(img, 50, 50); c.R < 240 || c.G > 20 {
		t.Errorf("JPEG %v", c)
	}
	// Undecodable data draws a grey placeholder, not nothing and not a failure.
	img = paintOf(t, imagePage("/Width 16 /Height 16 /BitsPerComponent 8 /ColorSpace /DeviceRGB /Filter /JPXDecode", []byte("not a codestream"), ""))
	if c := at(img, 50, 50); c.R != c.G || c.R < 170 || c.R > 210 {
		t.Errorf("placeholder %v", c)
	}
	img = paintOf(t, imagePage("/Width 16 /Height 16 /BitsPerComponent 1 /ColorSpace /DeviceGray /Filter /JBIG2Decode", []byte{1, 2, 3}, ""))
	if c := at(img, 50, 50); c.R != c.G || c.R < 170 || c.R > 210 {
		t.Errorf("placeholder %v", c)
	}
}

func TestInlineImage(t *testing.T) {
	img := paintOf(t, onePage("q 100 0 0 100 0 0 cm BI /W 1 /H 1 /CS /RGB /BPC 8 ID \xff\x00\x00 EI Q 0 0 1 rg 0 0 10 10 re f", "", nil))
	want(t, img, 50, 50, 255, 0, 0)
	want(t, img, 5, 5, 0, 0, 255) // what follows the image still runs
}

func TestOptionalContent(t *testing.T) {
	b := pdftest.New()
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R /OCProperties << /OCGs [5 0 R 6 0 R] /D << /OFF [6 0 R] >> >> >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Contents 4 0 R /Resources << /Properties << /On 5 0 R /Off 6 0 R >> >> >>")
	b.Stream(4, "", []byte("/OC /Off BDC 1 0 0 rg 0 0 50 100 re f EMC /OC /On BDC 0 1 0 rg 50 0 50 100 re f EMC"))
	b.Obj(5, "<< /Type /OCG /Name (on) >>")
	b.Obj(6, "<< /Type /OCG /Name (off) >>")
	img := paintOf(t, b.Bytes())
	want(t, img, 25, 50, 255, 255, 255)
	want(t, img, 75, 50, 0, 255, 0)
}

func TestRotationAndCrop(t *testing.T) {
	b := pdftest.New()
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 /Rotate 90 /MediaBox [0 0 200 100] >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /CropBox [0 0 100 100] /Contents 4 0 R >>")
	b.Stream(4, "", []byte("1 0 0 rg 0 0 50 50 re f"))
	d, err := pdf.Open(b.Bytes(), "")
	if err != nil {
		t.Fatal(err)
	}
	img := NewCache(d, nil).Page(context.Background(), d.Pages()[0], Options{Scale: 1}).Img
	if img.Rect.Dx() != 100 || img.Rect.Dy() != 100 {
		t.Fatalf("size %v", img.Rect)
	}
	// Turned clockwise, the lower left corner of the page is at the top left.
	if c := img.RGBAAt(25, 25); !near(c, 255, 0, 0) {
		t.Errorf("%v", c)
	}
	if c := img.RGBAAt(75, 75); !near(c, 255, 255, 255) {
		t.Errorf("%v", c)
	}
}

func TestRegionMatchesWholePage(t *testing.T) {
	d, err := pdf.Open(pdftest.Sample(1), "")
	if err != nil {
		t.Fatal(err)
	}
	c := NewCache(d, testFonts(t))
	whole := c.Page(context.Background(), d.Pages()[0], Options{Scale: 2})
	box := image.Rect(100, 150, 500, 400)
	part := c.Page(context.Background(), d.Pages()[0], Options{Scale: 2, Region: box})
	if part.Img.Rect != box {
		t.Fatalf("rect %v", part.Img.Rect)
	}
	for y := box.Min.Y; y < box.Max.Y; y += 7 {
		for x := box.Min.X; x < box.Max.X; x += 7 {
			if a, b := whole.Img.RGBAAt(x, y), part.Img.RGBAAt(x, y); a != b {
				t.Fatalf("at %d,%d: %v and %v", x, y, a, b)
			}
		}
	}
}

func TestTextPositions(t *testing.T) {
	d, err := pdf.Open(pdftest.Sample(1), "")
	if err != nil {
		t.Fatal(err)
	}
	c := NewCache(d, testFonts(t))
	text, err := c.Text(context.Background(), d.Pages()[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.Full(), "Page 1 of 1\nThe quick brown fox") {
		t.Fatalf("%q", text.Full())
	}
	hits := text.Search("LAZY  dog")
	if len(hits) != 1 {
		t.Fatalf("%v", hits)
	}
	q := text.Quads(hits[0].From, hits[0].To)
	if len(q) != 1 || q[0][1] < 670 || q[0][1] > 690 {
		t.Fatalf("%v", q)
	}
	// Hit finds the character under a point.
	i, ok := text.Hit(75, 675)
	if !ok || text.Chars[i].Text != "T" && text.Chars[i].Text != "h" {
		t.Fatalf("%d %v", i, ok)
	}
}

// New Chinese text goes in with an embedded subset and a ToUnicode map, and comes back out as what was written.
func TestAddedTextRoundTrip(t *testing.T) {
	src := testFonts(t)
	d, err := pdf.Open(pdftest.Sample(1), "")
	if err != nil {
		t.Fatal(err)
	}
	book := src.Book
	d.SetFonts(func(family string, bold, italic bool, text string) string {
		return book.File("微软雅黑", false, false)
	})
	if book.File("微软雅黑", false, false) == "" {
		t.Skip("no Chinese font here")
	}
	if err := d.AddText(0, "你好，世界 Hello", 72, 300, 0, pdf.TextStyle{Size: 20, Color: [3]float64{0, 0, 1}}); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	a, err := pdf.Open(out, "")
	if err != nil {
		t.Fatal(err)
	}
	c := NewCache(a, nil) // no system fonts: only what is embedded can draw it
	res := c.Page(context.Background(), a.Pages()[0], Options{Scale: 1})
	if !strings.Contains(res.Text.Full(), "你好，世界 Hello") {
		t.Fatalf("%q", res.Text.Full())
	}
	hits := res.Text.Search("世界")
	if len(hits) != 1 {
		t.Fatalf("%v", hits)
	}
	dark := 0
	for y := 792 - 325; y < 792-290; y++ {
		for x := 70; x < 300; x++ {
			if c := res.Img.RGBAAt(x, y); c.B > c.R+60 {
				dark++
			}
		}
	}
	if dark < 200 {
		t.Fatalf("%d blue pixels: the embedded font did not draw", dark)
	}
}

func TestAnnotationsShow(t *testing.T) {
	d, err := pdf.Open(pdftest.Sample(1), "")
	if err != nil {
		t.Fatal(err)
	}
	specs := []pdf.AnnotSpec{
		{Kind: "Square", Rect: pdf.Rect{X0: 300, Y0: 300, X1: 400, Y1: 400}, Color: [3]float64{1, 0, 0}, Width: 4},
		{Kind: "Circle", Rect: pdf.Rect{X0: 420, Y0: 300, X1: 520, Y1: 400}, Color: [3]float64{0, 1, 0}, Width: 4},
		{Kind: "Line", Line: [4]float64{300, 280, 500, 280}, Arrow: true, Color: [3]float64{0, 0, 1}, Width: 3},
		{Kind: "Ink", Ink: [][]float64{{300, 200, 350, 250, 400, 200}}, Color: [3]float64{1, 0, 1}, Width: 3},
		{Kind: "Highlight", Quads: [][8]float64{{72, 684, 250, 684, 72, 668, 250, 668}}, Color: [3]float64{1, 1, 0}},
		{Kind: "Text", Rect: pdf.Rect{X0: 500, Y0: 700, X1: 520, Y1: 720}, Contents: "Note", Color: [3]float64{1, 0.9, 0.3}},
		{Kind: "FreeText", Rect: pdf.Rect{X0: 100, Y0: 100, X1: 300, Y1: 160}, Contents: "Free text", Color: [3]float64{0, 0, 0}, Size: 14},
	}
	for _, s := range specs {
		if _, err := d.AddAnnot(0, s); err != nil {
			t.Fatal(err)
		}
	}
	res := NewCache(d, testFonts(t)).Page(context.Background(), d.Pages()[0], Options{Scale: 1})
	img := res.Img
	px := func(x, y int) color.RGBA { return img.RGBAAt(x, 792-y) }
	if c := px(350, 398); !near(c, 255, 0, 0) {
		t.Errorf("square %v", c)
	}
	if c := px(470, 398); !near(c, 0, 255, 0) {
		t.Errorf("circle %v", c)
	}
	if c := px(400, 280); !near(c, 0, 0, 255) {
		t.Errorf("line %v", c)
	}
	if c := px(350, 250); c.G > 40 {
		t.Errorf("ink %v", c)
	}
	// The highlight multiplies: white goes yellow, and the words stay black.
	if c := px(245, 683); !near(c, 255, 255, 0) {
		t.Errorf("highlight %v", c)
	}
	dark := 0
	for x := 72; x < 240; x++ {
		if c := px(x, 674); c.R < 80 {
			dark++
		}
	}
	if dark < 10 {
		t.Errorf("the highlight hid the words: %d", dark)
	}
}

func TestAnnotationFlagsAndForeignAppearance(t *testing.T) {
	b := pdftest.New()
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Annots [5 0 R 6 0 R] >>")
	b.Obj(5, "<< /Type /Annot /Subtype /Stamp /Rect [0 0 50 50] /AP << /N 7 0 R >> >>")
	b.Obj(6, "<< /Type /Annot /Subtype /Stamp /Rect [50 50 100 100] /F 2 /AP << /N 7 0 R >> >>")
	b.Stream(7, "/Type /XObject /Subtype /Form /BBox [0 0 10 10]", []byte("1 0 0 rg 0 0 10 10 re f"))
	img := paintOf(t, b.Bytes())
	want(t, img, 25, 25, 255, 0, 0) // stretched from its 10 unit box to the annotation's Rect
	want(t, img, 75, 75, 255, 255, 255)
}

// Files that try to hang or exhaust the renderer. Each must finish in its time, whatever it draws.
func TestHostileContent(t *testing.T) {
	cases := map[string][]byte{
		"self form": onePage("/F Do", "/XObject << /F 5 0 R >>", func(b *pdftest.Builder) {
			b.Stream(5, "/Type /XObject /Subtype /Form /BBox [0 0 100 100] /Resources << /XObject << /F 5 0 R >> >>", []byte("/F Do /F Do"))
		}),
		"self pattern": onePage("/Pattern cs /P scn 0 0 100 100 re f", "/Pattern << /P 5 0 R >>", func(b *pdftest.Builder) {
			b.Stream(5, "/PatternType 1 /PaintType 1 /TilingType 1 /BBox [0 0 20 20] /XStep 1e-9 /YStep 20 /Resources << /Pattern << /P 5 0 R >> >>", []byte("/Pattern cs /P scn 0 0 20 20 re f"))
		}),
		"self soft mask": onePage("/M gs 0 0 100 100 re f", "/ExtGState << /M << /SMask << /S /Alpha /G 5 0 R >> >> >>", func(b *pdftest.Builder) {
			b.Stream(5, "/Type /XObject /Subtype /Form /BBox [0 0 100 100] /Group << /S /Transparency >> /Resources << /ExtGState << /M << /SMask << /S /Alpha /G 5 0 R >> >> >> >>", []byte("/M gs 0 0 100 100 re f"))
		}),
		"self type3": onePage("BT /T 12 Tf (a) Tj ET", "/Font << /T 5 0 R >>", func(b *pdftest.Builder) {
			b.Obj(5, "<< /Type /Font /Subtype /Type3 /FontBBox [0 0 1 1] /FontMatrix [1 0 0 1 0 0] /CharProcs << /a 6 0 R >> /Encoding << /Differences [97 /a] >> /FirstChar 97 /LastChar 97 /Widths [1] /Resources << /Font << /T 5 0 R >> >> >>")
			b.Stream(6, "", []byte("BT /T 1 Tf (aaaa) Tj ET"))
		}),
		"long dash":   onePage("[0.0001] 0 d 1 w 0 0 m 100000 100000 l S", "", nil),
		"huge width":  onePage("1e9 w 0 0 m 100 100 l S 1e30 0 0 1e30 0 0 cm 0 0 1 1 re f", "", nil),
		"nan matrix":  onePage("0 0 0 0 0 0 cm 0 0 m 1 1 l S /X Do BT /F 0 Tf (a) Tj ET", "", nil),
		"q flood":     onePage(strings.Repeat("q ", 200000), "", nil),
		"huge image":  imagePage("/Width 100000000 /Height 100000000 /BitsPerComponent 8 /ColorSpace /DeviceRGB", []byte{1}, ""),
		"bad indexed": onePage("/I cs 300 scn 0 0 10 10 re f", "/ColorSpace << /I [/Indexed /DeviceRGB 1000 <00>] >>", nil),
		"mesh bombs": onePage("/S sh", "/Shading << /S 5 0 R >>", func(b *pdftest.Builder) {
			b.Stream(5, "/ShadingType 6 /ColorSpace /DeviceRGB /BitsPerCoordinate 1 /BitsPerComponent 1 /BitsPerFlag 2 /Decode [0 100000 0 100000 0 1 0 1 0 1]", bytes.Repeat([]byte{0xAA}, 40000))
		}),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			d, err := pdf.Open(data, "")
			if err != nil {
				t.Fatal(err)
			}
			cx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			start := time.Now()
			done := make(chan *Result, 1)
			go func() { done <- NewCache(d, nil).Page(cx, d.Pages()[0], Options{Scale: 1}) }()
			select {
			case res := <-done:
				if res.Img == nil {
					t.Fatal("no picture")
				}
			case <-time.After(15 * time.Second):
				t.Fatalf("hung after %v", time.Since(start))
			}
		})
	}
}

func TestCancelStops(t *testing.T) {
	d, err := pdf.Open(onePage(strings.Repeat("0 0 100 100 re f\n", 3000000), "", nil), "")
	if err != nil {
		t.Fatal(err)
	}
	cx, cancel := context.WithCancel(context.Background())
	done := make(chan *Result, 1)
	go func() { done <- NewCache(d, nil).Page(cx, d.Pages()[0], Options{Scale: 4}) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case res := <-done:
		if res.Err == nil {
			t.Fatal("no error after the cancel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancel did not stop the page")
	}
}

func TestFuzzRender(t *testing.T) {
	seeds := [][]byte{pdftest.Sample(2), onePage("/S sh q 100 0 0 100 0 0 cm /I Do Q", "/Shading << /S << /ShadingType 2 /ColorSpace /DeviceRGB /Coords [0 0 1 1] /Function << /FunctionType 2 /Domain [0 1] /N 1 >> >> >> /XObject << /I 5 0 R >>", func(b *pdftest.Builder) {
		b.Stream(5, "/Type /XObject /Subtype /Image /Width 2 /Height 2 /BitsPerComponent 8 /ColorSpace /DeviceGray", []byte{1, 2, 3, 4})
	})}
	rng := rand.New(rand.NewSource(7))
	deadline := time.Now().Add(4 * time.Second)
	for n := 0; time.Now().Before(deadline); n++ {
		b := bytes.Clone(seeds[n%2])
		for range 1 + rng.Intn(30) {
			b[rng.Intn(len(b))] = byte(rng.Intn(256))
		}
		d, err := pdf.Open(b, "")
		if err != nil {
			continue
		}
		cx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		pages := d.Pages()
		if len(pages) > 0 {
			NewCache(d, nil).Page(cx, pages[0], Options{Scale: 0.5})
		}
		cancel()
	}
}

func TestJPXInPDF(t *testing.T) {
	files := []string{"../codec/testdata"}
	entries, err := os.ReadDir(files[0])
	if err != nil {
		t.Skip(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jp2") {
			continue
		}
		data, _ := os.ReadFile(files[0] + "/" + e.Name())
		img := paintOf(t, imagePage("/Width 8 /Height 8 /Filter /JPXDecode", data, ""))
		if c := at(img, 50, 50); c.R == 0xB4 && c.G == 0xB4 || c.R == 0xC8 && c.G == 0xC8 {
			t.Errorf("%s drew the placeholder", e.Name())
		}
		return
	}
}
