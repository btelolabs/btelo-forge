package printing

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"context"
	"fmt"
	"image/color"
	"image/jpeg"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
)

// A vector PDF from the display list. Paths stay paths; gradients are shadings; pictures are images, a JPEG as it
// came; and characters are real text in Type 3 fonts built from the outlines in the list, with a ToUnicode map, so
// any font's glyphs embed the same way, only those used, and the words can be searched and copied.

// WritePDF writes the document. progress, which may be nil, hears how many pages are written.
func WritePDF(ctx context.Context, w io.Writer, d *Doc, progress func(done int)) error {
	p := &pdfWriter{w: bufio.NewWriterSize(w, 1<<16), images: map[any]int{}, glyphs: map[glyphKey]glyphRef{}}
	p.printf("%%PDF-1.7\n%%\xe2\xe3\xcf\xd3\n")
	catalog, pages := p.reserve(), p.reserve()
	var kids []int
	for i, pg := range d.Pages {
		if err := ctx.Err(); err != nil {
			return err
		}
		kids = append(kids, p.page(pg, pages))
		if progress != nil {
			progress(i + 1)
		}
	}
	p.fonts()
	var list strings.Builder
	for _, k := range kids {
		fmt.Fprintf(&list, "%d 0 R ", k)
	}
	p.object(pages, fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", len(kids), list.String()))
	p.object(catalog, fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pages))
	info := p.reserve()
	title := ""
	if d.Title != "" {
		title = "/Title " + hexText(d.Title) + " "
	}
	p.object(info, fmt.Sprintf("<< %s/Producer (Btelo Forge) >>", title))
	at := p.pos
	p.printf("xref\n0 %d\n0000000000 65535 f \n", len(p.offsets)+1)
	for _, o := range p.offsets {
		p.printf("%010d 00000 n \n", o)
	}
	p.printf("trailer\n<< /Size %d /Root %d 0 R /Info %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(p.offsets)+1, catalog, info, at)
	if p.err != nil {
		return p.err
	}
	return p.w.Flush()
}

type glyphKey struct {
	o    *Outline
	text string
}

type glyphRef struct {
	font int // an index of pdfWriter.subfonts
	code byte
}

// subfont is a Type 3 font of up to 256 glyphs.
type subfont struct {
	num    int
	glyphs []glyphKey
}

type pdfWriter struct {
	w        *bufio.Writer
	pos      int
	err      error
	offsets  []int
	images   map[any]int
	glyphs   map[glyphKey]glyphRef
	subfonts []*subfont
}

func (p *pdfWriter) write(b []byte) {
	if p.err == nil {
		_, p.err = p.w.Write(b)
		p.pos += len(b)
	}
}

func (p *pdfWriter) printf(format string, args ...any) { p.write(fmt.Appendf(nil, format, args...)) }

// reserve numbers an object to be written later.
func (p *pdfWriter) reserve() int {
	p.offsets = append(p.offsets, 0)
	return len(p.offsets)
}

func (p *pdfWriter) begin(n int) {
	p.offsets[n-1] = p.pos
	p.printf("%d 0 obj\n", n)
}

func (p *pdfWriter) object(n int, body string) {
	p.begin(n)
	p.printf("%s\nendobj\n", body)
}

// stream writes an object with a stream of data; dict holds the entries other than Length.
func (p *pdfWriter) stream(n int, dict string, data []byte) {
	p.begin(n)
	p.printf("<< %s /Length %d >>\nstream\n", dict, len(data))
	p.write(data)
	p.printf("\nendstream\nendobj\n")
}

func deflate(data []byte) []byte {
	var b bytes.Buffer
	z, _ := zlib.NewWriterLevel(&b, zlib.BestCompression)
	z.Write(data)
	z.Close()
	return b.Bytes()
}

func hexText(s string) string {
	var b strings.Builder
	b.WriteString("<FEFF")
	for _, u := range utf16.Encode([]rune(s)) {
		fmt.Fprintf(&b, "%04X", u)
	}
	b.WriteByte('>')
	return b.String()
}

// num writes a number shortly.
func num(b *bytes.Buffer, v float64) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		v = 0
	}
	s := strconv.AppendFloat(b.AvailableBuffer(), v, 'f', 4, 64)
	end := len(s)
	for end > 0 && s[end-1] == '0' {
		end--
	}
	if end > 0 && s[end-1] == '.' {
		end--
	}
	if end == 0 || end == 1 && s[0] == '-' {
		s, end = append(s[:0], '0'), 1
	}
	b.Write(s[:end])
	b.WriteByte(' ')
}

func nums(b *bytes.Buffer, vs ...float64) {
	for _, v := range vs {
		num(b, v)
	}
}

func writePath(b *bytes.Buffer, p *Path) {
	i := 0
	for _, op := range p.Ops {
		switch op {
		case 'M':
			nums(b, p.Pts[i].X, p.Pts[i].Y)
			b.WriteString("m\n")
			i++
		case 'L':
			nums(b, p.Pts[i].X, p.Pts[i].Y)
			b.WriteString("l\n")
			i++
		case 'C':
			nums(b, p.Pts[i].X, p.Pts[i].Y, p.Pts[i+1].X, p.Pts[i+1].Y, p.Pts[i+2].X, p.Pts[i+2].Y)
			b.WriteString("c\n")
			i += 3
		case 'Z':
			b.WriteString("h\n")
		}
	}
}

// gstate is what the content stream has set; a clip's q and Q save and bring it back.
type gstate struct {
	fill, stroke [3]uint8
	hasF, hasS   bool
	alpha        int // 0 to 255
	width        float64
	cap          Cap
	join         Join
	miter        float64
	dash         string
}

// pageWriter is one page's content and resources.
type pageWriter struct {
	p      *pdfWriter
	b      bytes.Buffer
	gs     gstate
	stack  []gstate
	alphas map[int]bool
	xobj   map[int]bool
	shades map[int]bool
	fonts  map[int]bool
}

func (p *pdfWriter) page(pg *Page, parent int) int {
	w := &pageWriter{p: p, gs: gstate{alpha: 255, miter: 10, width: 1}, alphas: map[int]bool{}, xobj: map[int]bool{}, shades: map[int]bool{}, fonts: map[int]bool{}}
	// The list has y going down from the top of the page.
	nums(&w.b, 1, 0, 0, -1, 0, pg.H)
	w.b.WriteString("cm\n")
	depth := 0
	for _, op := range pg.Ops {
		switch o := op.(type) {
		case *Fill:
			if o.Path.Empty() || o.Color.A == 0 {
				continue
			}
			w.setFill(o.Color)
			writePath(&w.b, o.Path)
			if o.EvenOdd {
				w.b.WriteString("f*\n")
			} else {
				w.b.WriteString("f\n")
			}
		case *Stroke:
			if o.Path.Empty() || o.Color.A == 0 {
				continue
			}
			w.setStroke(o)
			writePath(&w.b, o.Path)
			w.b.WriteString("S\n")
		case *Clip:
			w.save()
			depth++
			if o.Path.Empty() {
				w.b.WriteString("0 0 0 0 re\n")
			} else {
				writePath(&w.b, o.Path)
			}
			if o.EvenOdd {
				w.b.WriteString("W* n\n")
			} else {
				w.b.WriteString("W n\n")
			}
		case *Unclip:
			if depth > 0 {
				depth--
				w.restore()
			}
		case *Picture:
			w.picture(o)
		case *Text:
			w.text(o)
		case *Shade:
			w.shade(o)
		}
	}
	for ; depth > 0; depth-- {
		w.restore()
	}
	content := p.reserve()
	p.stream(content, "/Filter /FlateDecode", deflate(w.b.Bytes()))
	var res strings.Builder
	res.WriteString("<< ")
	section := func(name, prefix string, set map[int]bool) {
		if len(set) == 0 {
			return
		}
		fmt.Fprintf(&res, "/%s << ", name)
		for n := range set {
			fmt.Fprintf(&res, "/%s%d %d 0 R ", prefix, n, n)
		}
		res.WriteString(">> ")
	}
	if len(w.alphas) > 0 {
		res.WriteString("/ExtGState << ")
		for a := range w.alphas {
			fmt.Fprintf(&res, "/A%d << /ca %.4f /CA %.4f >> ", a, float64(a)/255, float64(a)/255)
		}
		res.WriteString(">> ")
	}
	section("XObject", "I", w.xobj)
	section("Shading", "S", w.shades)
	section("Font", "F", w.fonts)
	res.WriteString(">>")
	n := p.reserve()
	p.object(n, fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %.3f %.3f] /Contents %d 0 R /Resources %s >>", parent, pg.W, pg.H, content, res.String()))
	return n
}

func (w *pageWriter) save() {
	w.stack = append(w.stack, w.gs)
	w.b.WriteString("q\n")
}

func (w *pageWriter) restore() {
	w.gs = w.stack[len(w.stack)-1]
	w.stack = w.stack[:len(w.stack)-1]
	w.b.WriteString("Q\n")
}

func (w *pageWriter) setAlpha(a int) {
	if a != w.gs.alpha {
		w.gs.alpha = a
		w.alphas[a] = true
		fmt.Fprintf(&w.b, "/A%d gs\n", a)
	}
}

func (w *pageWriter) setFill(c color.NRGBA) {
	w.setAlpha(int(c.A))
	rgb := [3]uint8{c.R, c.G, c.B}
	if !w.gs.hasF || w.gs.fill != rgb {
		w.gs.fill, w.gs.hasF = rgb, true
		nums(&w.b, float64(c.R)/255, float64(c.G)/255, float64(c.B)/255)
		w.b.WriteString("rg\n")
	}
}

func (w *pageWriter) setStroke(s *Stroke) {
	c := s.Color
	w.setAlpha(int(c.A))
	rgb := [3]uint8{c.R, c.G, c.B}
	if !w.gs.hasS || w.gs.stroke != rgb {
		w.gs.stroke, w.gs.hasS = rgb, true
		nums(&w.b, float64(c.R)/255, float64(c.G)/255, float64(c.B)/255)
		w.b.WriteString("RG\n")
	}
	if s.Width != w.gs.width {
		w.gs.width = s.Width
		nums(&w.b, s.Width)
		w.b.WriteString("w\n")
	}
	if s.Cap != w.gs.cap {
		w.gs.cap = s.Cap
		fmt.Fprintf(&w.b, "%d J\n", s.Cap)
	}
	if s.Join != w.gs.join {
		w.gs.join = s.Join
		fmt.Fprintf(&w.b, "%d j\n", s.Join)
	}
	miter := s.Miter
	if miter == 0 {
		miter = 10
	}
	if miter != w.gs.miter {
		w.gs.miter = miter
		nums(&w.b, max(miter, 1))
		w.b.WriteString("M\n")
	}
	var d bytes.Buffer
	d.WriteByte('[')
	nums(&d, s.Dash...)
	d.WriteString("] ")
	num(&d, s.Phase)
	d.WriteString("d\n")
	if ds := d.String(); ds != w.gs.dash && !(w.gs.dash == "" && len(s.Dash) == 0) {
		w.gs.dash = ds
		w.b.WriteString(ds)
	}
}

func alphaByte(a float64) int {
	if a == 0 {
		return 255
	}
	return int(min(max(a, 0), 1)*255 + 0.5)
}

func (w *pageWriter) picture(o *Picture) {
	n := w.p.imageObject(o)
	if n == 0 {
		return
	}
	w.xobj[n] = true
	w.save()
	w.setAlpha(alphaByte(o.Alpha))
	// An image is drawn in the unit square with its top at y = 1.
	m := o.M.Mul(Matrix{1, 0, 0, -1, 0, 1})
	nums(&w.b, m[:]...)
	fmt.Fprintf(&w.b, "cm /I%d Do\n", n)
	w.restore()
}

// imageObject writes a picture once, however many pages show it, and gives its object.
func (p *pdfWriter) imageObject(o *Picture) int {
	var key any
	switch {
	case len(o.JPEG) > 0:
		key = &o.JPEG[0]
	case o.Img != nil && !o.Img.Rect.Empty():
		key = o.Img
	default:
		return 0
	}
	if n, ok := p.images[key]; ok {
		return n
	}
	n := p.reserve()
	p.images[key] = n
	if len(o.JPEG) > 0 {
		if cfg, err := jpeg.DecodeConfig(bytes.NewReader(o.JPEG)); err == nil && cfg.ColorModel != color.CMYKModel {
			cs := "/DeviceRGB"
			if cfg.ColorModel == color.GrayModel {
				cs = "/DeviceGray"
			}
			p.stream(n, fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace %s /BitsPerComponent 8 /Filter /DCTDecode", cfg.Width, cfg.Height, cs), o.JPEG)
			return n
		}
	}
	img := pictureImage(o)
	if img == nil {
		p.images[key] = 0
		p.stream(n, "/Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8", []byte{255})
		return n
	}
	w, h := img.Rect.Dx(), img.Rect.Dy()
	rgb := make([]byte, 0, w*h*3)
	alpha := make([]byte, 0, w*h)
	opaque := true
	for y := range h {
		row := img.Pix[y*img.Stride : y*img.Stride+w*4]
		for x := 0; x < w*4; x += 4 {
			a := row[x+3]
			r, g, b := row[x], row[x+1], row[x+2]
			if a != 255 {
				opaque = false
				if a != 0 {
					un := func(v uint8) uint8 { return uint8(min(int(v)*255/int(a), 255)) }
					r, g, b = un(r), un(g), un(b)
				}
			}
			rgb = append(rgb, r, g, b)
			alpha = append(alpha, a)
		}
	}
	mask := ""
	if !opaque {
		m := p.reserve()
		p.stream(m, fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /FlateDecode", w, h), deflate(alpha))
		mask = fmt.Sprintf(" /SMask %d 0 R", m)
	}
	p.stream(n, fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode%s", w, h, mask), deflate(rgb))
	return n
}

func (w *pageWriter) shade(o *Shade) {
	if o.Path.Empty() || len(o.Stops) == 0 {
		return
	}
	n := w.p.shadingObject(o)
	w.shades[n] = true
	w.save()
	w.setAlpha(alphaByte(o.Alpha))
	writePath(&w.b, o.Path)
	if o.EvenOdd {
		w.b.WriteString("W* n\n")
	} else {
		w.b.WriteString("W n\n")
	}
	nums(&w.b, o.M[:]...)
	fmt.Fprintf(&w.b, "cm /S%d sh\n", n)
	w.restore()
}

func rgbOf(c color.NRGBA) string {
	return fmt.Sprintf("%.4f %.4f %.4f", float64(c.R)/255, float64(c.G)/255, float64(c.B)/255)
}

// shadingObject writes a gradient: straight between two stops, or a stitching of such pieces.
func (p *pdfWriter) shadingObject(o *Shade) int {
	stops := make([]Stop, 0, len(o.Stops)+2)
	for _, s := range o.Stops {
		s.At = min(max(s.At, 0), 1)
		if n := len(stops); n > 0 && s.At <= stops[n-1].At {
			s.At = stops[n-1].At + 1e-6
		}
		stops = append(stops, s)
	}
	if stops[0].At > 0 {
		stops = append([]Stop{{0, stops[0].Color}}, stops...)
	}
	if stops[len(stops)-1].At < 1 {
		stops = append(stops, Stop{1, stops[len(stops)-1].Color})
	}
	piece := func(a, b color.NRGBA) string {
		return fmt.Sprintf("<< /FunctionType 2 /Domain [0 1] /N 1 /C0 [%s] /C1 [%s] >>", rgbOf(a), rgbOf(b))
	}
	var fn string
	if len(stops) == 2 {
		fn = piece(stops[0].Color, stops[1].Color)
	} else {
		var fs, bounds, encode strings.Builder
		for i := 0; i+1 < len(stops); i++ {
			fs.WriteString(piece(stops[i].Color, stops[i+1].Color))
			fs.WriteByte(' ')
			encode.WriteString("0 1 ")
			if i > 0 {
				fmt.Fprintf(&bounds, "%.6f ", stops[i].At)
			}
		}
		fn = fmt.Sprintf("<< /FunctionType 3 /Domain [0 1] /Functions [%s] /Bounds [%s] /Encode [%s] >>", fs.String(), bounds.String(), encode.String())
	}
	n := p.reserve()
	coords := fmt.Sprintf("/ShadingType 2 /Coords [%.4f %.4f %.4f %.4f]", o.X0, o.Y0, o.X1, o.Y1)
	if o.Radial {
		coords = fmt.Sprintf("/ShadingType 3 /Coords [%.4f %.4f %.4f %.4f %.4f %.4f]", o.X0, o.Y0, o.R0, o.X1, o.Y1, o.R1)
	}
	p.object(n, fmt.Sprintf("<< %s /ColorSpace /DeviceRGB /Function %s /Extend [%t %t] >>", coords, fn, o.Extend[0], o.Extend[1]))
	return n
}

// glyph finds or gives a place in a font to a character.
func (p *pdfWriter) glyph(g *Glyph) glyphRef {
	k := glyphKey{g.Outline, g.Text}
	if r, ok := p.glyphs[k]; ok {
		return r
	}
	n := len(p.subfonts)
	if n == 0 || len(p.subfonts[n-1].glyphs) == 256 {
		p.subfonts = append(p.subfonts, &subfont{num: p.reserve()})
		n++
	}
	f := p.subfonts[n-1]
	r := glyphRef{n - 1, byte(len(f.glyphs))}
	f.glyphs = append(f.glyphs, k)
	p.glyphs[k] = r
	return r
}

func (w *pageWriter) text(o *Text) {
	if o.Color.A == 0 || len(o.Glyphs) == 0 {
		return
	}
	w.setFill(o.Color)
	w.b.WriteString("BT\n")
	font := -1
	var penX, penY float64
	open := false
	end := func() {
		if open {
			w.b.WriteString("] TJ\n")
			open = false
		}
	}
	for i := range o.Glyphs {
		g := &o.Glyphs[i]
		if g.Outline == nil {
			continue
		}
		r := w.p.glyph(g)
		if r.font != font || !open || g.Y != penY {
			end()
			if r.font != font {
				font = r.font
				num := w.p.subfonts[font].num
				w.fonts[num] = true
				fmt.Fprintf(&w.b, "/F%d 1 Tf\n", num)
			}
			m := o.M.Mul(Translate(g.X, g.Y))
			nums(&w.b, m[:]...)
			w.b.WriteString("Tm [")
			open, penX, penY = true, g.X, g.Y
		} else if d := (g.X - penX) * 1000; math.Abs(d) > 0.05 {
			num(&w.b, -d)
		}
		fmt.Fprintf(&w.b, "<%02X>", r.code)
		penX = g.X + g.Outline.Advance
	}
	end()
	w.b.WriteString("ET\n")
}

// fonts writes the Type 3 fonts: one thousandth of an em to the unit, each glyph its own outline.
func (p *pdfWriter) fonts() {
	for _, f := range p.subfonts {
		var procs, diffs, widths strings.Builder
		for i, k := range f.glyphs {
			var b bytes.Buffer
			adv := k.o.Advance * 1000
			num(&b, adv)
			b.WriteString("0 ")
			if k.o.Path.Empty() {
				b.WriteString("0 0 0 0 d1\n")
			} else {
				box := k.o.Path.Bounds()
				nums(&b, box.X0*1000, box.Y0*1000, box.X1*1000, box.Y1*1000)
				b.WriteString("d1\n")
				writePath(&b, k.o.Path.Transformed(Scale(1000, 1000)))
				b.WriteString("f\n")
			}
			n := p.reserve()
			p.stream(n, "/Filter /FlateDecode", deflate(b.Bytes()))
			fmt.Fprintf(&procs, "/g%d %d 0 R ", i, n)
			fmt.Fprintf(&diffs, "/g%d ", i)
			fmt.Fprintf(&widths, "%.2f ", adv)
		}
		cmap := p.reserve()
		p.stream(cmap, "/Filter /FlateDecode", deflate(toUnicode(f)))
		p.object(f.num, fmt.Sprintf("<< /Type /Font /Subtype /Type3 /Name /F%d /FontBBox [0 0 0 0] /FontMatrix [0.001 0 0 0.001 0 0] "+
			"/CharProcs << %s>> /Encoding << /Type /Encoding /Differences [0 %s] >> /FirstChar 0 /LastChar %d /Widths [%s] "+
			"/Resources << >> /ToUnicode %d 0 R >>", f.num, procs.String(), diffs.String(), len(f.glyphs)-1, widths.String(), cmap))
	}
}

// toUnicode is the map from a font's codes to the words they stand for.
func toUnicode(f *subfont) []byte {
	var b bytes.Buffer
	b.WriteString("/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def\n" +
		"/CMapName /Adobe-Identity-UCS def\n/CMapType 2 def\n1 begincodespacerange\n<00> <FF>\nendcodespacerange\n")
	var lines []string
	for i, k := range f.glyphs {
		if k.text == "" {
			continue
		}
		var u strings.Builder
		for _, c := range utf16.Encode([]rune(k.text)) {
			fmt.Fprintf(&u, "%04X", c)
		}
		lines = append(lines, fmt.Sprintf("<%02X> <%s>\n", i, u.String()))
	}
	for len(lines) > 0 {
		n := min(len(lines), 100)
		fmt.Fprintf(&b, "%d beginbfchar\n", n)
		for _, l := range lines[:n] {
			b.WriteString(l)
		}
		b.WriteString("endbfchar\n")
		lines = lines[n:]
	}
	b.WriteString("endcmap\nCMapName currentdict /CMap defineresource pop\nend\nend\n")
	return b.Bytes()
}

// Images counts the pictures on a page, for tests and for telling the user what stays raster.
func (p *Page) Images() int {
	n := 0
	for _, op := range p.Ops {
		if _, ok := op.(*Picture); ok {
			n++
		}
	}
	return n
}
