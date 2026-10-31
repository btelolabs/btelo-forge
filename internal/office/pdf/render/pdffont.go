package render

import (
	"reflect"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"

	"github.com/btelolabs/btelo-forge/internal/office/pdf"
	"github.com/btelolabs/btelo-forge/internal/office/pdf/font"
)

// FontSource finds fonts on this computer for what a document does not embed.
type FontSource interface {
	// Substitute is a font of that family, or close to it, nil when there is none.
	Substitute(family string, bold, italic bool) font.Program
	// ForRune is a font that has the character, and the glyph.
	ForRune(r rune) (font.Program, int)
}

// glyph is one character to show.
type glyph struct {
	code   uint32
	n      int // bytes the code took
	prog   font.Program
	gid    int     // -1: nothing to draw
	width  float64 // advance in text space units of 1 em
	text   string
	xscale float64 // squeezes a substitute's glyph into the width the document expects
	name   string  // the Type 3 glyph
}

// pdfFont is a font dictionary made ready to use.
type pdfFont struct {
	doc          *pdf.Doc
	name         string
	bold, italic bool
	serif, fixed bool
	ascent       float64 // in ems
	descent      float64
	embedded     bool

	// A simple font's codes.
	names   [256]string
	widths  [256]float64
	hasW    [256]bool
	missing float64
	simple  [256]glyph
	done    [256]bool

	// A composite font's codes.
	composite bool
	cmap      *font.CMap
	charset   encoding.Encoding // a predefined legacy CMap, by character set
	unicode16 bool              // a predefined UTF-16 CMap
	dw        float64
	w         map[int]float64
	cidToGID  []byte
	identity  bool
	vertical  bool

	toUnicode *font.CMap
	prog      font.Program
	subst     bool
	src       FontSource

	type3      bool
	charProcs  pdf.Dict
	fontMatrix pdf.Matrix
	resources  pdf.Dict

	mu       sync.Mutex
	outlines map[outlineKey]font.Path
	cidCache map[uint32]glyph
	reverse  map[int]rune
}

type outlineKey struct {
	prog font.Program
	gid  int
}

func (f *pdfFont) outline(p font.Program, gid int) font.Path {
	key := outlineKey{p, gid}
	f.mu.Lock()
	o, ok := f.outlines[key]
	f.mu.Unlock()
	if ok {
		return o
	}
	o = p.Outline(gid)
	f.mu.Lock()
	if f.outlines == nil {
		f.outlines = map[outlineKey]font.Path{}
	}
	if len(f.outlines) < 1<<16 {
		f.outlines[key] = o
	}
	f.mu.Unlock()
	return o
}

// font reads a font dictionary once.
func (c *Cache) font(o pdf.Object, r *ctx) *pdfFont {
	if o == nil {
		return nil
	}
	var key uintptr
	if ref, ok := o.(pdf.Ref); ok {
		key = uintptr(ref.Num)
	} else if dict, ok := o.(pdf.Dict); ok {
		key = reflect.ValueOf(dict).Pointer()
	}
	c.mu.Lock()
	f, ok := c.fonts[key]
	c.mu.Unlock()
	if ok {
		return f
	}
	f = c.parseFont(c.doc.Dict(o))
	c.mu.Lock()
	c.fonts[key] = f
	c.mu.Unlock()
	return f
}

func (c *Cache) parseFont(dict pdf.Dict) (out *pdfFont) {
	d := c.doc
	f := &pdfFont{doc: d, src: c.src, ascent: 0.8, descent: -0.2, fontMatrix: pdf.Matrix{0.001, 0, 0, 0.001, 0, 0}}
	if dict == nil {
		return f
	}
	defer func() {
		// A broken font draws nothing rather than taking the page with it.
		if p := recover(); p != nil {
			out = f
		}
	}()
	f.name = stripSubset(string(d.Name(dict["BaseFont"])))
	if s, ok := d.Resolve(dict["ToUnicode"]).(*pdf.Stream); ok {
		if body, err := d.Decode(s); err == nil || len(body) > 0 {
			f.toUnicode, _ = font.ParseCMap(body, nil)
		}
	}
	switch d.Name(dict["Subtype"]) {
	case "Type0":
		c.parseType0(f, dict)
	case "Type3":
		f.type3 = true
		f.charProcs = d.Dict(dict["CharProcs"])
		f.fontMatrix = d.MatrixOf(dict["FontMatrix"])
		f.resources = d.Dict(dict["Resources"])
		c.parseEncoding(f, dict, nil)
		c.parseWidths(f, dict)
		// Type 3 widths are in glyph space.
		for i := range f.widths {
			f.widths[i] *= f.fontMatrix[0] * 1000
		}
	default:
		desc := d.Dict(dict["FontDescriptor"])
		c.readDescriptor(f, desc)
		c.loadProgram(f, desc)
		c.parseEncoding(f, dict, desc)
		c.parseWidths(f, dict)
		if f.prog == nil {
			f.subst = true
			f.prog = c.substitute(f)
		}
	}
	return f
}

func stripSubset(s string) string {
	if len(s) > 7 && s[6] == '+' && strings.ToUpper(s[:6]) == s[:6] {
		return s[7:]
	}
	return s
}

func (c *Cache) readDescriptor(f *pdfFont, desc pdf.Dict) {
	d := c.doc
	low := strings.ToLower(f.name)
	f.bold = strings.Contains(low, "bold") || strings.Contains(low, "black") || strings.Contains(low, "heavy") || strings.Contains(low, "demi")
	f.italic = strings.Contains(low, "italic") || strings.Contains(low, "oblique")
	f.fixed = strings.Contains(low, "courier") || strings.Contains(low, "mono") || strings.Contains(low, "consol")
	f.serif = strings.Contains(low, "times") || strings.Contains(low, "serif") && !strings.Contains(low, "sans") || strings.Contains(low, "georgia") || strings.Contains(low, "garamond") || strings.Contains(low, "song") || strings.Contains(low, "sun") || strings.Contains(low, "ming") || strings.Contains(low, "palatino") || strings.Contains(low, "book")
	if desc == nil {
		return
	}
	flags := d.Int(desc["Flags"], 0)
	f.fixed = f.fixed || flags&1 != 0
	f.serif = f.serif || flags&2 != 0
	f.italic = f.italic || flags&64 != 0 || d.Float(desc["ItalicAngle"], 0) != 0
	f.bold = f.bold || flags&(1<<18) != 0 || d.Float(desc["FontWeight"], 0) >= 700 || d.Float(desc["StemV"], 0) >= 140
	if a := d.Float(desc["Ascent"], 0); a > 0 && a < 3000 {
		f.ascent = a / 1000
	}
	if v := d.Float(desc["Descent"], 0); v < 0 && v > -3000 {
		f.descent = v / 1000
	} else if v > 0 && v < 3000 {
		f.descent = -v / 1000
	}
	f.missing = d.Float(desc["MissingWidth"], 0)
}

func (c *Cache) loadProgram(f *pdfFont, desc pdf.Dict) {
	d := c.doc
	if desc == nil {
		return
	}
	for _, k := range []pdf.Name{"FontFile2", "FontFile3", "FontFile"} {
		s, _ := d.Resolve(desc[k]).(*pdf.Stream)
		if s == nil {
			continue
		}
		body, err := d.Decode(s)
		if err != nil && len(body) == 0 {
			continue
		}
		if p := parseProgram(body); p != nil {
			f.prog = p
			f.embedded = true
			return
		}
	}
}

// parseProgram goes by the data: writers mislabel font files.
func parseProgram(b []byte) font.Program {
	if len(b) < 4 {
		return nil
	}
	head := string(b[:4])
	switch {
	case head == "\x00\x01\x00\x00" || head == "true" || head == "ttcf" || head == "OTTO":
		if t, err := font.ParseTrueType(b, 0); err == nil {
			return t
		}
	case b[0] == 1 && b[1] == 0:
		if p, err := font.ParseCFF(b); err == nil {
			return p
		}
	}
	if t, err := font.ParseType1(b); err == nil {
		return t
	}
	if p, err := font.ParseCFF(b); err == nil {
		return p
	}
	if t, err := font.ParseTrueType(b, 0); err == nil {
		return t
	}
	return nil
}

func (c *Cache) substitute(f *pdfFont) font.Program {
	if c.src == nil {
		return nil
	}
	family := f.name
	if canon, ok := font.Standard14(f.name); ok {
		switch {
		case strings.HasPrefix(canon, "Helvetica"):
			family = "Arial"
		case strings.HasPrefix(canon, "Times"):
			family = "Times New Roman"
		case strings.HasPrefix(canon, "Courier"):
			family = "Courier New"
		default:
			return nil // Symbol and ZapfDingbats go character by character
		}
	} else {
		if i := strings.IndexAny(family, ",-"); i > 0 {
			family = family[:i]
		}
	}
	if p := c.src.Substitute(family, f.bold, f.italic); p != nil {
		return p
	}
	switch {
	case f.fixed:
		family = "Courier New"
	case f.serif:
		family = "Times New Roman"
	default:
		family = "Arial"
	}
	return c.src.Substitute(family, f.bold, f.italic)
}

func (c *Cache) parseEncoding(f *pdfFont, dict, desc pdf.Dict) {
	d := c.doc
	var base *[256]string
	enc := d.Resolve(dict["Encoding"])
	setBase := func(n pdf.Name) {
		switch n {
		case "WinAnsiEncoding":
			base = &font.WinAnsiEncoding
		case "MacRomanEncoding":
			base = &font.MacRomanEncoding
		case "MacExpertEncoding":
			base = &font.MacExpertEncoding
		case "StandardEncoding":
			base = &font.StandardEncoding
		}
	}
	var diffs pdf.Array
	switch v := enc.(type) {
	case pdf.Name:
		setBase(v)
	case pdf.Dict:
		setBase(d.Name(v["BaseEncoding"]))
		diffs = d.Array(v["Differences"])
	}
	canon, std := font.Standard14(f.name)
	if base == nil {
		switch {
		case canon == "Symbol":
			base = &font.SymbolEncoding
		case canon == "ZapfDingbats":
			base = &font.ZapfDingbatsEncoding
		case f.prog == nil && std:
			base = &font.StandardEncoding
		case f.prog == nil:
			base = &font.WinAnsiEncoding
		}
	}
	if base != nil {
		f.names = *base
	}
	hasEncoding := base != nil
	code := 0
	for _, e := range diffs {
		switch v := d.Resolve(e).(type) {
		case int64:
			code = int(v)
		case float64:
			code = int(v)
		case pdf.Name:
			if code >= 0 && code < 256 {
				f.names[code] = string(v)
				hasEncoding = true
			}
			code++
		}
	}
	if !f.type3 && f.prog != nil {
		c.mapSimple(f, desc, hasEncoding)
	}
}

// mapSimple finds each code's glyph in an embedded program.
func (c *Cache) mapSimple(f *pdfFont, desc pdf.Dict, hasEncoding bool) {
	symbolic := false
	if desc != nil {
		flags := c.doc.Int(desc["Flags"], 0)
		symbolic = flags&4 != 0 && flags&32 == 0
	}
	byName := func(name string) (int, bool) {
		if name == "" {
			return 0, false
		}
		if g, ok := f.prog.GIDByName(name); ok {
			return g, true
		}
		if r, ok := font.NameToRune(name); ok {
			if g, ok := f.prog.GIDByRune(r); ok {
				return g, true
			}
		}
		return 0, false
	}
	var builtin func(code int) (int, bool)
	var nameOf func(gid int) string
	switch p := f.prog.(type) {
	case *font.Type1:
		builtin = func(code int) (int, bool) { return p.GIDByCode(byte(code)) }
		nameOf = p.GlyphName
	case *font.CFF:
		builtin = func(code int) (int, bool) { return p.GIDByCode(byte(code)) }
		nameOf = p.GlyphName
	case *font.TrueType:
		if p.IsCFF() {
			cff := p.CFF()
			builtin = func(code int) (int, bool) { return cff.GIDByCode(byte(code)) }
			nameOf = cff.GlyphName
			break
		}
		builtin = func(code int) (int, bool) {
			for _, try := range []struct {
				pl, en int
				base   uint32
			}{{3, 0, 0xF000}, {3, 0, 0}, {3, 0, 0xF100}, {3, 0, 0xF200}, {1, 0, 0}, {0, 3, 0}, {0, 0, 0}} {
				if g, ok := p.GIDByCmap(try.pl, try.en, try.base+uint32(code)); ok {
					return g, true
				}
			}
			return 0, false
		}
		macCode := func(name string) (int, bool) {
			for i, n := range font.MacRomanEncoding {
				if n == name {
					return i, true
				}
			}
			return 0, false
		}
		byNameTT := func(name string) (int, bool) {
			if name == "" {
				return 0, false
			}
			if r, ok := font.NameToRune(name); ok {
				if g, ok := p.GIDByCmap(3, 1, uint32(r)); ok {
					return g, true
				}
				if g, ok := p.GIDByCmap(0, 3, uint32(r)); ok {
					return g, true
				}
			}
			if i, ok := macCode(name); ok {
				if g, ok := p.GIDByCmap(1, 0, uint32(i)); ok {
					return g, true
				}
			}
			return p.GIDByName(name)
		}
		for code := range 256 {
			gid, ok := 0, false
			if hasEncoding && !symbolic || f.names[code] != "" && !p.HasCmap(3, 0) {
				gid, ok = byNameTT(f.names[code])
			}
			if !ok {
				gid, ok = builtin(code)
			}
			if !ok && f.names[code] != "" {
				gid, ok = byNameTT(f.names[code])
			}
			if ok {
				c.setSimple(f, code, gid)
			}
		}
		return
	}
	for code := range 256 {
		gid, ok := byName(f.names[code])
		if !ok && f.names[code] == "" {
			if gid, ok = builtin(code); ok && nameOf != nil {
				f.names[code] = nameOf(gid)
			}
		}
		if ok {
			c.setSimple(f, code, gid)
		}
	}
}

func (c *Cache) setSimple(f *pdfFont, code, gid int) {
	f.simple[code] = glyph{gid: gid, prog: f.prog}
	f.done[code] = true
}

func (c *Cache) parseWidths(f *pdfFont, dict pdf.Dict) {
	d := c.doc
	first := d.Int(dict["FirstChar"], 0)
	w := d.Array(dict["Widths"])
	for i, e := range w {
		code := first + i
		if code >= 0 && code < 256 {
			if v, ok := pdf.Num(d.Resolve(e)); ok {
				f.widths[code] = v
				f.hasW[code] = true
			}
		}
	}
	if len(w) > 0 {
		return
	}
	if canon, ok := font.Standard14(f.name); ok {
		for code := range 256 {
			if v, ok := font.Standard14Width(canon, f.names[code]); ok {
				f.widths[code] = v
				f.hasW[code] = true
			}
		}
	}
}

// cidString decodes a predefined legacy code.
func decodeCharset(enc encoding.Encoding, b []byte) rune {
	out, err := enc.NewDecoder().Bytes(b)
	if err != nil || len(out) == 0 {
		return 0
	}
	r, _ := utf8.DecodeRune(out)
	if r == utf8.RuneError {
		return 0
	}
	return r
}

func charsetOf(name string) (encoding.Encoding, bool) {
	switch {
	case strings.Contains(name, "UTF16") || strings.Contains(name, "UCS2"):
		return nil, true
	case strings.HasPrefix(name, "GB"):
		return simplifiedchinese.GB18030, false
	case strings.HasPrefix(name, "B5") || strings.HasPrefix(name, "ETen") || strings.HasPrefix(name, "HKscs") || strings.HasPrefix(name, "CNS"):
		return traditionalchinese.Big5, false
	case strings.Contains(name, "RKSJ"):
		return japanese.ShiftJIS, false
	case strings.HasPrefix(name, "EUC") || strings.HasPrefix(name, "Ext"):
		return japanese.EUCJP, false
	case strings.HasPrefix(name, "KSC"):
		return korean.EUCKR, false
	}
	return nil, false
}

func (c *Cache) parseType0(f *pdfFont, dict pdf.Dict) {
	d := c.doc
	f.composite = true
	switch v := d.Resolve(dict["Encoding"]).(type) {
	case pdf.Name:
		switch {
		case v == "Identity-H" || v == "Identity-V":
			f.cmap = font.IdentityCMap(v == "Identity-V")
		default:
			f.vertical = strings.HasSuffix(string(v), "-V") || v == "V"
			f.charset, f.unicode16 = charsetOf(string(v))
		}
	case *pdf.Stream:
		body, err := d.Decode(v)
		if err == nil || len(body) > 0 {
			f.cmap, _ = font.ParseCMap(body, func(name string) *font.CMap {
				if name == "Identity-H" || name == "Identity-V" {
					return font.IdentityCMap(name == "Identity-V")
				}
				return nil
			})
		}
	}
	if f.cmap == nil && f.charset == nil && !f.unicode16 {
		f.cmap = font.IdentityCMap(false)
	}
	if f.cmap != nil {
		f.vertical = f.cmap.Vertical()
	}
	descendants := d.Array(dict["DescendantFonts"])
	var cid pdf.Dict
	if len(descendants) > 0 {
		cid = d.Dict(descendants[0])
	}
	f.dw = d.Float(cid["DW"], 1000)
	if f.name == "" {
		f.name = stripSubset(string(d.Name(cid["BaseFont"])))
	}
	desc := d.Dict(cid["FontDescriptor"])
	c.readDescriptor(f, desc)
	c.loadProgram(f, desc)
	f.w = parseCIDWidths(d, cid["W"])
	switch v := d.Resolve(cid["CIDToGIDMap"]).(type) {
	case *pdf.Stream:
		f.cidToGID, _ = d.Decode(v)
	default:
		f.identity = true
	}
	if f.prog == nil {
		f.subst = true
		ordering := ""
		if s, ok := d.Resolve(d.Dict(cid["CIDSystemInfo"])["Ordering"]).(pdf.String); ok {
			ordering = string(s)
		}
		f.prog = c.substituteCJK(f, ordering)
	}
}

func (c *Cache) substituteCJK(f *pdfFont, ordering string) font.Program {
	if c.src == nil {
		return nil
	}
	family := "微软雅黑"
	if f.serif {
		family = "宋体"
	}
	if ordering == "Identity" || ordering == "" {
		if p := c.substitute(f); p != nil {
			return p
		}
	}
	return c.src.Substitute(family, f.bold, f.italic)
}

func parseCIDWidths(d *pdf.Doc, o pdf.Object) map[int]float64 {
	a := d.Array(o)
	out := map[int]float64{}
	for i := 0; i < len(a) && len(out) < 1<<20; {
		first, ok := pdf.Num(d.Resolve(a[i]))
		if !ok || i+1 >= len(a) {
			break
		}
		switch v := d.Resolve(a[i+1]).(type) {
		case pdf.Array:
			for j, e := range v {
				if w, ok := pdf.Num(d.Resolve(e)); ok {
					out[int(first)+j] = w
				}
			}
			i += 2
		default:
			last, ok := pdf.Num(v)
			if !ok || i+2 >= len(a) {
				return out
			}
			w, _ := pdf.Num(d.Resolve(a[i+2]))
			for c := int(first); c <= int(last) && c-int(first) < 1<<16; c++ {
				out[c] = w
			}
			i += 3
		}
	}
	return out
}

// next reads one character from a shown string.
func (f *pdfFont) next(s []byte) glyph {
	if !f.composite {
		return f.simpleGlyph(s[0])
	}
	return f.cidGlyph(s)
}

func (f *pdfFont) simpleGlyph(c byte) glyph {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.simple[c]
	if g.n == 1 {
		return g // finished before
	}
	g.code, g.n = uint32(c), 1
	if !f.done[c] {
		g.gid = -1
	}
	name := f.names[c]
	g.name = name
	if f.toUnicode != nil {
		g.text, _ = f.toUnicode.Unicode(uint32(c), 1)
	}
	var r rune
	if name != "" {
		r, _ = font.NameToRune(name)
	}
	if g.text == "" && r != 0 {
		g.text = string(r)
	}
	if g.text == "" && !f.embedded && c >= 32 {
		g.text = string(rune(c))
	}
	if f.hasW[c] {
		g.width = f.widths[c] / 1000
	} else if f.missing != 0 {
		g.width = f.missing / 1000
	} else if g.prog != nil && g.gid >= 0 {
		if adv, ok := g.prog.Advance(g.gid); ok {
			g.width = adv * g.prog.Matrix()[0]
		}
	}
	g.xscale = 1
	if !f.embedded && !f.type3 {
		f.pickSubstitute(&g, r)
	}
	f.simple[c] = g
	return g
}

// pickSubstitute finds a substitute's glyph for a character and fits it to the width the document gives it.
func (f *pdfFont) pickSubstitute(g *glyph, r rune) {
	if r == 0 {
		for _, c := range g.text {
			r = c
			break
		}
	}
	g.gid, g.prog = -1, nil
	if r == 0 {
		return
	}
	if f.prog != nil {
		if id, ok := f.prog.GIDByRune(r); ok {
			g.gid, g.prog = id, f.prog
		}
	}
	if g.gid < 0 && f.src != nil && r > 32 {
		if p, id := f.src.ForRune(r); p != nil {
			g.gid, g.prog = id, p
		}
	}
	if g.gid >= 0 {
		if adv, ok := g.prog.Advance(g.gid); ok {
			have := adv * g.prog.Matrix()[0]
			if g.width <= 0 {
				g.width = have
			} else if have > g.width*1.01 {
				g.xscale = g.width / have // too wide: squeeze it; too narrow stays, with its spacing as the document says
			}
		}
	}
}

func (f *pdfFont) cidGlyph(s []byte) glyph {
	if f.cmap == nil {
		return f.legacyGlyph(s)
	}
	code, n := f.cmap.Next(s)
	key := code | uint32(n)<<28
	f.mu.Lock()
	g, ok := f.cidCache[key]
	f.mu.Unlock()
	if ok {
		return g
	}
	cid, _ := f.cmap.CID(code, n)
	g = glyph{code: code, n: n, gid: -1, xscale: 1, prog: f.prog}
	if w, ok := f.w[cid]; ok {
		g.width = w / 1000
	} else {
		g.width = f.dw / 1000
	}
	if f.toUnicode != nil {
		g.text, _ = f.toUnicode.Unicode(code, n)
	}
	if f.subst {
		var r rune
		for _, c := range g.text {
			r = c
			break
		}
		f.pickSubstitute(&g, r)
	} else if f.prog != nil {
		g.gid = f.gidOfCID(cid)
		if g.text == "" {
			g.text = f.textOfGID(g.gid)
		}
	}
	f.cacheCID(key, g)
	return g
}

func (f *pdfFont) cacheCID(key uint32, g glyph) {
	f.mu.Lock()
	if f.cidCache == nil {
		f.cidCache = map[uint32]glyph{}
	}
	if len(f.cidCache) < 1<<17 {
		f.cidCache[key] = g
	}
	f.mu.Unlock()
}

func (f *pdfFont) gidOfCID(cid int) int {
	switch p := f.prog.(type) {
	case *font.CFF:
		if id, ok := p.GIDByCID(cid); ok {
			return id
		}
		return -1
	case *font.TrueType:
		if p.IsCFF() {
			if id, ok := p.CFF().GIDByCID(cid); ok {
				return id
			}
			return -1
		}
	}
	if f.cidToGID != nil {
		if 2*cid+1 < len(f.cidToGID) {
			return int(f.cidToGID[2*cid])<<8 | int(f.cidToGID[2*cid+1])
		}
		return -1
	}
	return cid
}

// textOfGID recovers a character from the font's own map, for documents with no ToUnicode.
func (f *pdfFont) textOfGID(gid int) string {
	if gid <= 0 {
		return ""
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reverse == nil {
		f.reverse = map[int]rune{}
		for r := rune(0xFFFF); r >= 0x20; r-- {
			if id, ok := f.prog.GIDByRune(r); ok && id != 0 {
				f.reverse[id] = r
			}
		}
	}
	if r, ok := f.reverse[gid]; ok {
		return string(r)
	}
	return ""
}

// legacyGlyph reads a character of a predefined CMap by its character set: the character is known, its CID is not,
// so an embedded font is reached through its own Unicode map.
func (f *pdfFont) legacyGlyph(s []byte) glyph {
	n := 1
	var r rune
	if f.unicode16 {
		n = 2
		if len(s) < 2 {
			return glyph{n: 1, gid: -1, xscale: 1, width: f.dw / 1000}
		}
		r = rune(s[0])<<8 | rune(s[1])
		if r >= 0xD800 && r < 0xDC00 && len(s) >= 4 {
			r = 0x10000 + (r-0xD800)<<10 + (rune(s[2])<<8 | rune(s[3])) - 0xDC00
			n = 4
		}
	} else {
		if s[0] >= 0x81 && len(s) >= 2 {
			n = 2
		}
		r = decodeCharset(f.charset, s[:n])
	}
	code := uint32(0)
	for _, b := range s[:n] {
		code = code<<8 | uint32(b)
	}
	key := code&0x0FFFFFFF | uint32(n)<<28
	f.mu.Lock()
	g, ok := f.cidCache[key]
	f.mu.Unlock()
	if ok {
		return g
	}
	g = glyph{code: code, n: n, gid: -1, xscale: 1, width: f.dw / 1000}
	if r >= 0x20 && r < 0x7F {
		g.width = 0.5 // half width Latin in a CJK font, where the writer left W out
	}
	if r != 0 {
		g.text = string(r)
		if f.toUnicode != nil {
			if t, ok := f.toUnicode.Unicode(code, n); ok && t != "" {
				g.text = t
			}
		}
		f.pickSubstitute(&g, r)
	}
	f.cacheCID(key, g)
	return g
}
