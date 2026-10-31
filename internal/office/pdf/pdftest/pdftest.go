// Package pdftest writes small PDF files by hand for tests: exact bytes, so that a parser sees what a test means it to.
package pdftest

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"sort"
)

// Builder collects objects by number and writes a file.
type Builder struct {
	objs    map[int][]byte
	Trailer string // extra trailer entries
	Root    int
}

// New is an empty builder.
func New() *Builder { return &Builder{objs: map[int][]byte{}, Root: 1} }

// Obj sets an object's body.
func (b *Builder) Obj(n int, body string) *Builder {
	b.objs[n] = []byte(body)
	return b
}

// Stream sets a stream object with the extra dictionary entries given.
func (b *Builder) Stream(n int, dict string, data []byte) *Builder {
	var o bytes.Buffer
	fmt.Fprintf(&o, "<< /Length %d %s >>\nstream\n", len(data), dict)
	o.Write(data)
	o.WriteString("\nendstream")
	b.objs[n] = o.Bytes()
	return b
}

// Deflated is a Flate-compressed stream.
func (b *Builder) Deflated(n int, dict string, data []byte) *Builder {
	return b.Stream(n, "/Filter /FlateDecode "+dict, Deflate(data))
}

// Deflate compresses data.
func Deflate(data []byte) []byte {
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	w.Write(data)
	w.Close()
	return z.Bytes()
}

// Bytes writes the file with a classic cross-reference table.
func (b *Builder) Bytes() []byte {
	var out bytes.Buffer
	out.WriteString("%PDF-1.7\n%\xE2\xE3\xCF\xD3\n")
	nums := make([]int, 0, len(b.objs))
	for n := range b.objs {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	offsets := map[int]int{}
	for _, n := range nums {
		offsets[n] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n", n)
		out.Write(b.objs[n])
		out.WriteString("\nendobj\n")
	}
	size := nums[len(nums)-1] + 1
	at := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", size)
	for n := 1; n < size; n++ {
		if off, ok := offsets[n]; ok {
			fmt.Fprintf(&out, "%010d 00000 n \n", off)
		} else {
			out.WriteString("0000000000 00000 f \n")
		}
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root %d 0 R %s >>\nstartxref\n%d\n%%%%EOF\n", size, b.Root, b.Trailer, at)
	return out.Bytes()
}

// Sample is a document of n Letter pages, each saying "Page k of n" and more in Helvetica, with an outline, named
// destinations, an internal link and an address on page 1, and a form on page 1: a text field, a check box, radio
// buttons and a combo box.
func Sample(n int) []byte {
	b := New()
	kids := ""
	firstPage := 10
	for i := range n {
		kids += fmt.Sprintf("%d 0 R ", firstPage+2*i)
	}
	b.Obj(1, fmt.Sprintf("<< /Type /Catalog /Pages 2 0 R /Outlines 3 0 R /Names << /Dests 7 0 R >> /AcroForm << /Fields [40 0 R 41 0 R 42 0 R 45 0 R] /DR << /Font << /Helv 5 0 R >> >> /DA (/Helv 11 Tf 0 g) >> >>"))
	b.Obj(2, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids, n))
	b.Obj(3, "<< /Type /Outlines /First 4 0 R /Last 6 0 R /Count 2 >>")
	last := firstPage + 2*(n-1)
	b.Obj(4, fmt.Sprintf("<< /Title (First page) /Parent 3 0 R /Next 6 0 R /Dest [%d 0 R /XYZ 72 720 0] >>", firstPage))
	b.Obj(6, fmt.Sprintf("<< /Title (Last page) /Parent 3 0 R /Prev 4 0 R /Dest (end) >>"))
	b.Obj(7, fmt.Sprintf("<< /Names [(end) [%d 0 R /Fit]] >>", last))
	b.Obj(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	for i := range n {
		page, content := firstPage+2*i, firstPage+2*i+1
		annots := ""
		if i == 0 {
			annots = "/Annots [30 0 R 31 0 R 40 0 R 41 0 R 43 0 R 44 0 R 45 0 R]"
			b.Obj(30, fmt.Sprintf("<< /Type /Annot /Subtype /Link /Rect [72 640 200 656] /Border [0 0 0] /Dest (end) >>"))
			b.Obj(31, "<< /Type /Annot /Subtype /Link /Rect [72 620 200 636] /Border [0 0 0] /A << /S /URI /URI (https://example.com/) >> >>")
			b.Obj(40, "<< /FT /Tx /T (name) /V (Ada) /Rect [72 560 272 584] /Type /Annot /Subtype /Widget /DA (/Helv 12 Tf 0 g) /MK << /BC [0 0 0] >> /F 4 >>")
			b.Obj(41, "<< /FT /Btn /T (agree) /V /Off /AS /Off /Rect [72 530 90 548] /Type /Annot /Subtype /Widget /F 4 /AP << /N << /Yes 50 0 R /Off 51 0 R >> >> >>")
			b.Obj(42, "<< /FT /Btn /Ff 49152 /T (colour) /V /Off /Kids [43 0 R 44 0 R] >>")
			b.Obj(43, "<< /Parent 42 0 R /Rect [72 500 90 518] /AS /Off /Type /Annot /Subtype /Widget /F 4 /AP << /N << /red 50 0 R /Off 51 0 R >> >> >>")
			b.Obj(44, "<< /Parent 42 0 R /Rect [100 500 118 518] /AS /Off /Type /Annot /Subtype /Widget /F 4 /AP << /N << /blue 50 0 R /Off 51 0 R >> >> >>")
			b.Obj(45, "<< /FT /Ch /Ff 131072 /T (size) /V (m) /Opt [[(s) (Small)] [(m) (Medium)] [(l) (Large)]] /Rect [72 460 272 484] /Type /Annot /Subtype /Widget /DA (/Helv 12 Tf 0 g) /F 4 >>")
			b.Stream(50, "/Type /XObject /Subtype /Form /BBox [0 0 18 18]", []byte("0 0 18 18 re S 3 3 m 15 15 l 3 15 m 15 3 l S"))
			b.Stream(51, "/Type /XObject /Subtype /Form /BBox [0 0 18 18]", []byte("0 0 18 18 re S"))
		}
		b.Obj(page, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents %d 0 R %s >>", content, annots))
		text := fmt.Sprintf("BT /F1 24 Tf 72 700 Td (Page %d of %d) Tj ET\nBT /F1 12 Tf 72 670 Td (The quick brown fox jumps over the lazy dog.) Tj 0 -14 Td (Second line of plain words.) Tj ET\n", i+1, n)
		if i == 0 {
			text += "BT /F1 12 Tf 72 644 Td (Go to the end) Tj 0 -20 Td (Visit an address) Tj ET\n0.9 0.2 0.2 rg 320 560 120 80 re f\n"
		}
		b.Deflated(content, "", []byte(text))
	}
	return b.Bytes()
}
