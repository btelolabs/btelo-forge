package render

import (
	"bytes"
	"context"
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/office/pdf"
	"github.com/btelolabs/btelo-forge/internal/office/pdf/pdftest"
	"github.com/btelolabs/btelo-forge/internal/printing"
)

var emptyRect image.Rectangle

// letterPage builds a page of Letter paper.
func letterPage(content, resources string, extra func(b *pdftest.Builder)) []byte {
	b := pdftest.New()
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << "+resources+" >> >>")
	b.Stream(4, "", []byte(content))
	if extra != nil {
		extra(b)
	}
	return b.Bytes()
}

func printOnePage(t *testing.T, data []byte) *printing.Page {
	t.Helper()
	d, err := pdf.Open(data, "")
	if err != nil {
		t.Fatal(err)
	}
	pg, err := NewCache(d, testFonts(t)).PrintPage(context.Background(), d.Pages()[0], PrintOptions{Annots: true, DPI: 144})
	if err != nil {
		t.Fatal(err)
	}
	return pg
}

func count(p *printing.Page) (fills, strokes, texts, pictures, shades, clips int) {
	for _, op := range p.Ops {
		switch op.(type) {
		case *printing.Fill:
			fills++
		case *printing.Stroke:
			strokes++
		case *printing.Text:
			texts++
		case *printing.Picture:
			pictures++
		case *printing.Shade:
			shades++
		case *printing.Clip:
			clips++
		}
	}
	return
}

func TestPrintPagePlainIsAllVector(t *testing.T) {
	content := `q 1 0 0 RG 3 w 100 100 200 100 re S Q
q 0 0 1 rg 300 300 m 400 300 400 400 300 400 c h f Q
q 50 500 100 100 re W n 0 1 0 rg 0 0 612 792 re f Q
BT /F1 24 Tf 100 700 Td (Vector words) Tj ET
q /Sh sh Q`
	pg := printOnePage(t, letterPage(content, `/Font << /F1 << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >> >> /Shading << /Sh << /ShadingType 2 /ColorSpace /DeviceRGB /Coords [0 0 612 0] /Function << /FunctionType 2 /Domain [0 1] /N 1 /C0 [1 0 0] /C1 [0 0 1] >> /Extend [true true] >> >>`, nil))
	fills, strokes, texts, pictures, shades, clips := count(pg)
	if pictures != 0 {
		t.Fatalf("%d pictures on a page of shapes", pictures)
	}
	if fills < 2 || strokes != 1 || texts != 1 || shades != 1 || clips != 1 {
		t.Fatalf("%d fills, %d strokes, %d texts, %d shades, %d clips", fills, strokes, texts, shades, clips)
	}
	for _, op := range pg.Ops {
		if s, ok := op.(*printing.Stroke); ok {
			b := s.Path.Bounds()
			if s.Width != 3 || b.X0 != 100 || b.Y1 != 692 {
				t.Fatalf("stroke %v over %+v", s.Width, b)
			}
		}
	}
}

func TestPrintPageTakesPixelsOnlyForBlending(t *testing.T) {
	content := `q 1 0 0 rg 100 100 300 300 re f Q
q /Mult gs 0 1 1 rg 200 200 300 300 re f Q
q 0 0 1 rg 50 600 100 100 re f Q`
	pg := printOnePage(t, letterPage(content, `/ExtGState << /Mult << /BM /Multiply >> >>`, nil))
	fills, _, _, pictures, _, _ := count(pg)
	if pictures != 1 {
		t.Fatalf("%d pictures for one blended shape", pictures)
	}
	if fills != 2 {
		t.Fatalf("%d fills stayed vector, not 2", fills)
	}
	// Under the multiply, red and cyan make black; the blue square is untouched.
	img := printing.Render(pg, 2, emptyRect)
	if c := img.RGBAAt(600, 1100); c.R > 8 || c.G > 8 || c.B > 8 {
		t.Fatalf("multiplied to %v", c)
	}
	if c := img.RGBAAt(200, 300); c.B != 255 || c.R != 0 {
		t.Fatalf("the blue square is %v", c)
	}
}

func TestPrintPageSoftMaskAndGroupOpacity(t *testing.T) {
	content := `q 0 1 0 rg 0 0 612 792 re f Q
q /Fade gs /G Do Q`
	pg := printOnePage(t, letterPage(content, `/ExtGState << /Fade << /ca 0.5 >> >> /XObject << /G 20 0 R >>`, func(b *pdftest.Builder) {
		b.Stream(20, "/Type /XObject /Subtype /Form /BBox [0 0 612 792] /Group << /S /Transparency >>", []byte(`1 0 0 rg 100 100 200 200 re f`))
	}))
	_, _, _, pictures, _, _ := count(pg)
	if pictures != 1 {
		t.Fatalf("%d pictures", pictures)
	}
	img := printing.Render(pg, 1, emptyRect)
	if c := img.RGBAAt(200, 592); c.R < 118 || c.R > 138 || c.G < 118 || c.G > 138 {
		t.Fatalf("half red over green is %v", c)
	}
}

func TestPrintPageImageKeepsItsPixels(t *testing.T) {
	data := bytes.Repeat([]byte{200, 0, 0}, 400*300)
	pg := printOnePage(t, letterPage(`q 100 0 0 75 50 50 cm /Im Do Q`, `/XObject << /Im 20 0 R >>`, func(b *pdftest.Builder) {
		b.Stream(20, "/Type /XObject /Subtype /Image /Width 400 /Height 300 /ColorSpace /DeviceRGB /BitsPerComponent 8", data)
	}))
	for _, op := range pg.Ops {
		if p, ok := op.(*printing.Picture); ok {
			if p.Img.Rect.Dx() != 400 || p.Img.Rect.Dy() != 300 {
				t.Fatalf("%v pixels", p.Img.Rect)
			}
			return
		}
	}
	t.Fatal("no picture")
}

func TestPrintPageAnnotationsFollowTheirPrintFlag(t *testing.T) {
	data := pdftest.Sample(1)
	d, err := pdf.Open(data, "")
	if err != nil {
		t.Fatal(err)
	}
	c := NewCache(d, nil)
	on, err := c.PrintPage(context.Background(), d.Pages()[0], PrintOptions{Annots: true})
	if err != nil {
		t.Fatal(err)
	}
	off, err := c.PrintPage(context.Background(), d.Pages()[0], PrintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(on.Ops) <= len(off.Ops) {
		t.Fatalf("%d ops with annotations, %d without", len(on.Ops), len(off.Ops))
	}
	if dir := os.Getenv("FORGE_PRINT_OUT"); dir != "" {
		var b bytes.Buffer
		if err := printing.WritePDF(context.Background(), &b, &printing.Doc{Pages: []*printing.Page{on}}, nil); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, "pdf-sample.pdf"), b.Bytes(), 0o644)
	}
}
