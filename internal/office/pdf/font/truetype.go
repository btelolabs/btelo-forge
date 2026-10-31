package font

import (
	"errors"
	"math"
	"sync"
	"unicode/utf16"
)

// Limits on composite glyphs.
const (
	maxCompositeDepth = 6
	maxComponents     = 512     // In one glyph, over all levels.
	maxGlyphPoints    = 1 << 18 // In one glyph, over all levels.
	maxSfntTables     = 4096
)

// TrueType is a TrueType font, or an OpenType font with either glyf or CFF outlines.
type TrueType struct {
	numGlyphs int
	upem      int
	longLoca  bool
	numHM     int
	glyf      []byte
	loca      []byte
	hmtx      []byte
	tables    map[string][]byte
	cmaps     []cmapTable
	cff       *CFF
	ascent    float64
	descent   float64
	capHeight float64
	bbox      [4]float64
	italic    float64
	psName    string

	once  sync.Once
	names *nameIndex
}

// ParseTrueType reads a font file; index selects a font in a TTC. Subset fonts in
// PDF files lack cmap, name, post and OS/2, so only the outlines are required.
func ParseTrueType(data []byte, index int) (t *TrueType, err error) {
	defer func() {
		if err != nil {
			t = nil
		}
	}()
	defer recovered(&err)
	if len(data) < 12 {
		return nil, errTruncated
	}
	base := 0
	if string(data[:4]) == "ttcf" {
		n := int(u32(data, 8))
		if index < 0 || index >= n || 12+4*index+4 > len(data) {
			return nil, errors.New("font: no such font in the collection")
		}
		base = int(u32(data, 12+4*index))
		if base < 0 || base+12 > len(data) {
			return nil, errTruncated
		}
	}
	tables := make(map[string][]byte)
	n := u16(data, base+4)
	if n > maxSfntTables {
		n = maxSfntTables
	}
	for i := 0; i < n; i++ {
		rec := base + 12 + 16*i
		if rec+16 > len(data) {
			break
		}
		tag := string(data[rec : rec+4])
		off, length := int64(u32(data, rec+8)), int64(u32(data, rec+12))
		if off >= int64(len(data)) {
			continue
		}
		if off+length > int64(len(data)) {
			length = int64(len(data)) - off
		}
		if _, dup := tables[tag]; !dup {
			tables[tag] = data[off : off+length]
		}
	}
	t = &TrueType{tables: tables, glyf: tables["glyf"], loca: tables["loca"], hmtx: tables["hmtx"]}
	t.readHead()
	maxp := tables["maxp"]
	if len(maxp) >= 6 {
		t.numGlyphs = u16(maxp, 4)
	}
	if cff, ok := tables["CFF "]; ok && len(t.glyf) == 0 {
		c, err := ParseCFF(cff)
		if err != nil {
			return nil, err
		}
		t.cff = c
		t.numGlyphs = c.NumGlyphs()
	} else if err := t.checkLoca(); err != nil {
		return nil, err
	}
	t.readMetrics()
	t.cmaps = parseCmaps(tables["cmap"])
	t.psName = parseName(tables["name"])
	if t.psName == "" && t.cff != nil {
		t.psName = t.cff.name
	}
	return t, nil
}

func (t *TrueType) readHead() {
	head := t.tables["head"]
	t.upem = 1000
	if len(head) >= 20 {
		if v := u16(head, 18); v > 0 {
			t.upem = v
		}
	}
	if len(head) >= 44 {
		for i := range t.bbox {
			t.bbox[i] = float64(i16(head, 36+2*i))
		}
	}
	if len(head) >= 52 {
		t.longLoca = u16(head, 50) != 0
	}
}

// checkLoca settles the glyph count and offset size. The head flag and maxp may
// both be wrong in subsets, so the table's own length decides when it can.
func (t *TrueType) checkLoca() error {
	if len(t.loca) < 2 || len(t.glyf) == 0 {
		return errNoGlyphs
	}
	if _, ok := t.tables["maxp"]; ok && t.numGlyphs > 0 {
		switch len(t.loca) {
		case 2 * (t.numGlyphs + 1):
			t.longLoca = false
		case 4 * (t.numGlyphs + 1):
			t.longLoca = true
		}
	}
	entries := t.locaEntries()
	if t.numGlyphs == 0 || t.numGlyphs > entries {
		t.numGlyphs = entries
	}
	if t.numGlyphs == 0 {
		return errNoGlyphs
	}
	return nil
}

func (t *TrueType) locaEntries() int {
	if t.longLoca {
		return len(t.loca) / 4
	}
	return len(t.loca) / 2
}

func (t *TrueType) readMetrics() {
	if hhea := t.tables["hhea"]; len(hhea) >= 36 {
		t.ascent = float64(i16(hhea, 4))
		t.descent = float64(i16(hhea, 6))
		t.numHM = u16(hhea, 34)
	}
	if os2 := t.tables["OS/2"]; len(os2) >= 90 && u16(os2, 0) >= 2 {
		t.capHeight = float64(i16(os2, 88))
	}
	if post := t.tables["post"]; len(post) >= 8 {
		t.italic = float64(int32(u32(post, 4))) / 65536
	}
}

// parseName finds name id 6, Windows or Unicode platform first.
func parseName(b []byte) string {
	if len(b) < 6 {
		return ""
	}
	count, store := u16(b, 2), u16(b, 4)
	mac := ""
	for i := 0; i < count; i++ {
		rec := 6 + 12*i
		if rec+12 > len(b) {
			break
		}
		if u16(b, rec+6) != 6 {
			continue
		}
		length, off := u16(b, rec+8), store+u16(b, rec+10)
		if off+length > len(b) {
			continue
		}
		raw := b[off : off+length]
		switch u16(b, rec) {
		case 0, 3:
			u := make([]uint16, len(raw)/2)
			for j := range u {
				u[j] = uint16(u16(raw, 2*j))
			}
			if len(u) > 0 {
				return string(utf16.Decode(u))
			}
		case 1:
			if mac == "" {
				mac = string(raw)
			}
		}
	}
	return mac
}

// NumGlyphs is the number of glyphs in the font.
func (t *TrueType) NumGlyphs() int { return t.numGlyphs }

// UnitsPerEm is the head table's em size, 1000 when the table is missing.
func (t *TrueType) UnitsPerEm() int { return t.upem }

// Ascent is the hhea ascender in font units.
func (t *TrueType) Ascent() float64 { return t.ascent }

// Descent is the hhea descender in font units.
func (t *TrueType) Descent() float64 { return t.descent }

// CapHeight is the OS/2 capital height in font units, 0 when absent.
func (t *TrueType) CapHeight() float64 { return t.capHeight }

// BBox is the head table's bounding box.
func (t *TrueType) BBox() [4]float64 { return t.bbox }

// ItalicAngle is the post table's angle in degrees.
func (t *TrueType) ItalicAngle() float64 { return t.italic }

// IsCFF reports OpenType outlines in a CFF table instead of glyf.
func (t *TrueType) IsCFF() bool { return t.cff != nil }

// PostScriptName is name table id 6 when present.
func (t *TrueType) PostScriptName() string { return t.psName }

// Matrix maps font units to text space.
func (t *TrueType) Matrix() [6]float64 {
	if t.cff != nil {
		return t.cff.Matrix()
	}
	s := 1 / float64(t.upem)
	return [6]float64{s, 0, 0, s, 0, 0}
}

// Advance is the hmtx advance, in the units of Outline.
func (t *TrueType) Advance(gid int) (float64, bool) {
	if gid < 0 || gid >= t.numGlyphs || t.numHM == 0 {
		return 0, false
	}
	i := gid
	if i >= t.numHM {
		i = t.numHM - 1
	}
	if 4*i+2 > len(t.hmtx) {
		return 0, false
	}
	v := float64(u16(t.hmtx, 4*i))
	if t.cff != nil {
		if m := t.cff.Matrix(); m[0] != 0 {
			v = v / float64(t.upem) / m[0]
		}
	}
	return v, true
}

func (t *TrueType) index() *nameIndex {
	t.once.Do(func() {
		if t.cff != nil {
			t.names = t.cff.index()
			return
		}
		names := parsePost(t.tables["post"], t.numGlyphs)
		t.names = newNameIndex(len(names), func(gid int) string { return names[gid] })
	})
	return t.names
}

// GIDByName finds a glyph through the post table, or the CFF charset.
func (t *TrueType) GIDByName(name string) (int, bool) {
	gid, ok := t.index().byName[name]
	return gid, ok
}

// GIDByRune tries the Unicode subtables, a symbol subtable, then the glyph names.
func (t *TrueType) GIDByRune(r rune) (int, bool) {
	if r < 0 {
		return 0, false
	}
	for _, pe := range [][2]int{{3, 10}, {0, 6}, {0, 4}, {3, 1}, {0, 3}, {0, 2}, {0, 1}, {0, 0}} {
		if gid, ok := t.GIDByCmap(pe[0], pe[1], uint32(r)); ok {
			return gid, true
		}
	}
	if r < 0x100 {
		if gid, ok := t.GIDByCmap(3, 0, uint32(r)); ok {
			return gid, true
		}
		if gid, ok := t.GIDByCmap(3, 0, 0xF000|uint32(r)); ok {
			return gid, true
		}
	}
	gid, ok := t.index().byRune[r]
	return gid, ok
}

// parsePost reads glyph names from formats 1 and 2 of the post table.
func parsePost(b []byte, numGlyphs int) []string {
	if len(b) < 32 {
		return nil
	}
	switch u32(b, 0) {
	case 0x00010000:
		n := min(numGlyphs, len(macGlyphNames))
		return macGlyphNames[:n]
	case 0x00020000:
		n := u16(b, 32)
		if n > numGlyphs {
			n = numGlyphs
		}
		p := 34 + 2*u16(b, 32)
		var extra []string
		for p < len(b) {
			l := int(b[p])
			p++
			if p+l > len(b) {
				break
			}
			extra = append(extra, string(b[p:p+l]))
			p += l
		}
		names := make([]string, n)
		for gid := range names {
			if i := u16(b, 34+2*gid); i < len(macGlyphNames) {
				names[gid] = macGlyphNames[i]
			} else if i -= len(macGlyphNames); i < len(extra) {
				names[gid] = extra[i]
			}
		}
		return names
	}
	return nil
}

// glyphData is the glyph's bytes, nil when empty or out of range.
func (t *TrueType) glyphData(gid int) []byte {
	if gid < 0 || gid >= t.numGlyphs {
		return nil
	}
	var a, b int64
	entries := t.locaEntries()
	if gid >= entries {
		return nil
	}
	if t.longLoca {
		a, b = int64(u32(t.loca, 4*gid)), int64(u32(t.loca, 4*gid+4))
	} else {
		a, b = 2*int64(u16(t.loca, 2*gid)), 2*int64(u16(t.loca, 2*gid+2))
	}
	if gid+1 >= entries {
		b = int64(len(t.glyf))
	}
	if b > int64(len(t.glyf)) {
		b = int64(len(t.glyf))
	}
	if a >= b {
		return nil
	}
	return t.glyf[a:b]
}

type ttPoint struct {
	x, y float64
	on   bool
}

// glyphLoader expands composites with a budget shared by all levels.
type glyphLoader struct {
	t          *TrueType
	points     int
	components int
	shift      float64 // Set by load: how far the last glyph moves on x.
}

// bearingShift is how far a glyph moves so that its left edge lands on the hmtx
// bearing, which is what FreeType and the Windows rasterizer do. A glyph with no
// hmtx entry stays where it is.
func (t *TrueType) bearingShift(gid int, g []byte) float64 {
	lsb, ok := t.leftBearing(gid)
	if !ok {
		return 0
	}
	return float64(lsb - i16(g, 2))
}

// load returns the glyph's points and the index of each contour's last point.
func (l *glyphLoader) load(gid, depth int) ([]ttPoint, []int, bool) {
	l.shift = 0
	g := l.t.glyphData(gid)
	if len(g) == 0 {
		return nil, nil, true
	}
	if len(g) < 10 {
		return nil, nil, false
	}
	shift := l.t.bearingShift(gid, g)
	if n := i16(g, 0); n >= 0 {
		l.shift = shift
		return l.simple(g, n)
	}
	if depth >= maxCompositeDepth {
		return nil, nil, false
	}
	return l.composite(g, depth, shift)
}

func (l *glyphLoader) simple(g []byte, contours int) ([]ttPoint, []int, bool) {
	if contours == 0 {
		return nil, nil, true
	}
	p := 10
	if p+2*contours+2 > len(g) {
		return nil, nil, false
	}
	ends := make([]int, contours)
	last := -1
	for i := range ends {
		e := u16(g, p+2*i)
		if e < last {
			return nil, nil, false
		}
		ends[i], last = e, e
	}
	p += 2 * contours
	n := last + 1
	if l.points += n; l.points > maxGlyphPoints {
		return nil, nil, false
	}
	p += 2 + u16(g, p)
	flags := make([]byte, n)
	for i := 0; i < n; {
		if p >= len(g) {
			return nil, nil, false
		}
		f := g[p]
		p++
		flags[i] = f
		i++
		if f&8 != 0 {
			if p >= len(g) {
				return nil, nil, false
			}
			r := int(g[p])
			p++
			for ; r > 0 && i < n; r-- {
				flags[i] = f
				i++
			}
		}
	}
	pts := make([]ttPoint, n)
	read := func(short, same byte, set func(pt *ttPoint, v int)) bool {
		v := 0
		for i, f := range flags {
			switch {
			case f&short != 0:
				if p >= len(g) {
					return false
				}
				d := int(g[p])
				p++
				if f&same == 0 {
					d = -d
				}
				v += d
			case f&same == 0:
				if p+2 > len(g) {
					return false
				}
				v += i16(g, p)
				p += 2
			}
			set(&pts[i], v)
		}
		return true
	}
	if !read(2, 16, func(pt *ttPoint, v int) { pt.x = float64(v) }) {
		return nil, nil, false
	}
	if !read(4, 32, func(pt *ttPoint, v int) { pt.y = float64(v) }) {
		return nil, nil, false
	}
	for i, f := range flags {
		pts[i].on = f&1 != 0
	}
	return pts, ends, true
}

// Composite glyph flags.
const (
	compWords        = 0x0001
	compXY           = 0x0002
	compScale        = 0x0008
	compMore         = 0x0020
	compXYScale      = 0x0040
	comp2x2          = 0x0080
	compUseMetrics   = 0x0200
	compScaledOffset = 0x0800
	compUnscaled     = 0x1000
)

func (l *glyphLoader) composite(g []byte, depth int, shift float64) ([]ttPoint, []int, bool) {
	var pts []ttPoint
	var ends []int
	p := 10
	for {
		if p+4 > len(g) {
			return nil, nil, false
		}
		if l.components++; l.components > maxComponents {
			return nil, nil, false
		}
		flags, comp := u16(g, p), u16(g, p+2)
		p += 4
		var a1, a2 int
		if flags&compWords != 0 {
			if p+4 > len(g) {
				return nil, nil, false
			}
			if flags&compXY != 0 {
				a1, a2 = i16(g, p), i16(g, p+2)
			} else {
				a1, a2 = u16(g, p), u16(g, p+2)
			}
			p += 4
		} else {
			if p+2 > len(g) {
				return nil, nil, false
			}
			if flags&compXY != 0 {
				a1, a2 = int(int8(g[p])), int(int8(g[p+1]))
			} else {
				a1, a2 = int(g[p]), int(g[p+1])
			}
			p += 2
		}
		a, b, c, d := 1.0, 0.0, 0.0, 1.0
		f2 := func(i int) float64 { return float64(i16(g, i)) / 16384 }
		switch {
		case flags&compScale != 0:
			a, d = f2(p), f2(p)
			p += 2
		case flags&compXYScale != 0:
			a, d = f2(p), f2(p+2)
			p += 4
		case flags&comp2x2 != 0:
			a, b, c, d = f2(p), f2(p+2), f2(p+4), f2(p+6)
			p += 8
		}
		if p > len(g) {
			return nil, nil, false
		}
		sub, subEnds, ok := l.load(comp, depth+1)
		if !ok {
			return nil, nil, false
		}
		if flags&compUseMetrics != 0 {
			shift = l.shift
		}
		for i := range sub {
			x, y := sub[i].x, sub[i].y
			sub[i].x, sub[i].y = a*x+c*y, b*x+d*y
		}
		var dx, dy float64
		if flags&compXY != 0 {
			dx, dy = float64(a1), float64(a2)
			if flags&compScaledOffset != 0 && flags&compUnscaled == 0 {
				dx *= math.Hypot(a, b)
				dy *= math.Hypot(c, d)
			}
		} else if a1 < len(pts) && a2 < len(sub) {
			dx, dy = pts[a1].x-sub[a2].x, pts[a1].y-sub[a2].y
		}
		base := len(pts)
		for _, pt := range sub {
			pt.x += dx
			pt.y += dy
			pts = append(pts, pt)
		}
		for _, e := range subEnds {
			ends = append(ends, base+e)
		}
		if flags&compMore == 0 {
			l.shift = shift
			return pts, ends, true
		}
	}
}

// Outline is the glyph's outline in font units, with quadratic curves for glyf.
func (t *TrueType) Outline(gid int) (path Path) {
	defer func() {
		if recover() != nil {
			path = nil
		}
	}()
	if t.cff != nil {
		return t.cff.Outline(gid)
	}
	l := glyphLoader{t: t}
	pts, ends, ok := l.load(gid, 0)
	if !ok {
		return nil
	}
	for i := range pts {
		pts[i].x += l.shift
	}
	start := 0
	for _, e := range ends {
		if e >= start && e < len(pts) {
			path = appendContour(path, pts[start:e+1])
		}
		start = e + 1
	}
	return path
}

// appendContour turns a TrueType contour into path steps, adding the implied
// on-curve point between neighbouring off-curve points.
func appendContour(path Path, pts []ttPoint) Path {
	n := len(pts)
	if n < 2 {
		return path
	}
	var sx, sy float64
	first, count := 0, n
	switch {
	case pts[0].on:
		sx, sy = pts[0].x, pts[0].y
		first, count = 1, n-1
	case pts[n-1].on:
		sx, sy = pts[n-1].x, pts[n-1].y
		count = n - 1
	default:
		sx, sy = (pts[0].x+pts[n-1].x)/2, (pts[0].y+pts[n-1].y)/2
	}
	path = append(path, Seg{Op: 'M', X: sx, Y: sy})
	var cx, cy float64
	curve := false
	for i := 0; i < count; i++ {
		p := pts[first+i]
		switch {
		case p.on && curve:
			path = append(path, Seg{Op: 'Q', X1: cx, Y1: cy, X: p.x, Y: p.y})
			curve = false
		case p.on:
			path = append(path, Seg{Op: 'L', X: p.x, Y: p.y})
		case curve:
			path = append(path, Seg{Op: 'Q', X1: cx, Y1: cy, X: (cx + p.x) / 2, Y: (cy + p.y) / 2})
			cx, cy = p.x, p.y
		default:
			cx, cy = p.x, p.y
			curve = true
		}
	}
	if curve {
		path = append(path, Seg{Op: 'Q', X1: cx, Y1: cy, X: sx, Y: sy})
	}
	return append(path, Seg{Op: 'Z'})
}

// macGlyphNames is the standard Macintosh glyph order of the post table.
var macGlyphNames = []string{
	".notdef", ".null", "nonmarkingreturn", "space", "exclam", "quotedbl", "numbersign", "dollar", "percent",
	"ampersand", "quotesingle", "parenleft", "parenright", "asterisk", "plus", "comma", "hyphen", "period", "slash",
	"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "colon", "semicolon", "less",
	"equal", "greater", "question", "at", "A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M", "N", "O",
	"P", "Q", "R", "S", "T", "U", "V", "W", "X", "Y", "Z", "bracketleft", "backslash", "bracketright", "asciicircum",
	"underscore", "grave", "a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r",
	"s", "t", "u", "v", "w", "x", "y", "z", "braceleft", "bar", "braceright", "asciitilde", "Adieresis", "Aring",
	"Ccedilla", "Eacute", "Ntilde", "Odieresis", "Udieresis", "aacute", "agrave", "acircumflex", "adieresis", "atilde",
	"aring", "ccedilla", "eacute", "egrave", "ecircumflex", "edieresis", "iacute", "igrave", "icircumflex",
	"idieresis", "ntilde", "oacute", "ograve", "ocircumflex", "odieresis", "otilde", "uacute", "ugrave", "ucircumflex",
	"udieresis", "dagger", "degree", "cent", "sterling", "section", "bullet", "paragraph", "germandbls", "registered",
	"copyright", "trademark", "acute", "dieresis", "notequal", "AE", "Oslash", "infinity", "plusminus", "lessequal",
	"greaterequal", "yen", "mu", "partialdiff", "summation", "product", "pi", "integral", "ordfeminine",
	"ordmasculine", "Omega", "ae", "oslash", "questiondown", "exclamdown", "logicalnot", "radical", "florin",
	"approxequal", "Delta", "guillemotleft", "guillemotright", "ellipsis", "nonbreakingspace", "Agrave", "Atilde",
	"Otilde", "OE", "oe", "endash", "emdash", "quotedblleft", "quotedblright", "quoteleft", "quoteright", "divide",
	"lozenge", "ydieresis", "Ydieresis", "fraction", "currency", "guilsinglleft", "guilsinglright", "fi", "fl",
	"daggerdbl", "periodcentered", "quotesinglbase", "quotedblbase", "perthousand", "Acircumflex", "Ecircumflex",
	"Aacute", "Edieresis", "Egrave", "Iacute", "Icircumflex", "Idieresis", "Igrave", "Oacute", "Ocircumflex", "apple",
	"Ograve", "Uacute", "Ucircumflex", "Ugrave", "dotlessi", "circumflex", "tilde", "macron", "breve", "dotaccent",
	"ring", "cedilla", "hungarumlaut", "ogonek", "caron", "Lslash", "lslash", "Scaron", "scaron", "Zcaron", "zcaron",
	"brokenbar", "Eth", "eth", "Yacute", "yacute", "Thorn", "thorn", "minus", "multiply", "onesuperior", "twosuperior",
	"threesuperior", "onehalf", "onequarter", "threequarters", "franc", "Gbreve", "gbreve", "Idotaccent", "Scedilla",
	"scedilla", "Cacute", "cacute", "Ccaron", "ccaron", "dcroat",
}
