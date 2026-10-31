package font

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"sync"
)

const (
	maxType1Glyphs = 1 << 16
	maxType1Subrs  = 1 << 16
)

// Type1 is a Type 1 font program.
type Type1 struct {
	matrix   [6]float64
	names    []string // Glyph names; .notdef comes first when it exists.
	charstrs [][]byte
	subrs    [][]byte
	byName   map[string]int
	encoding [256]string

	once  sync.Once
	index *nameIndex
}

// type1Parse holds what the two parts of the file yield.
type type1Parse struct {
	t       *Type1
	lenIV   int
	hasMat  bool
	hasEnc  bool
	hasSubs bool
	names   []string
	raw     [][]byte
}

// ParseType1 reads a PFA or PFB program. Length1 is not trusted: the eexec
// operator is found in the data itself.
func ParseType1(data []byte) (t *Type1, err error) {
	defer func() {
		if err != nil {
			t = nil
		}
	}()
	defer recovered(&err)
	data = stripPFB(data)
	clear, private := splitEexec(data)
	t = &Type1{matrix: [6]float64{0.001, 0, 0, 0.001, 0, 0}, byName: make(map[string]int)}
	p := &type1Parse{t: t, lenIV: 4}
	p.section(clear)
	if private != nil {
		p.section(private)
	}
	if len(p.raw) == 0 {
		return nil, errNoGlyphs
	}
	p.finish()
	return t, nil
}

// stripPFB joins the segments of a PFB file. Lengths past the end are clipped.
func stripPFB(data []byte) []byte {
	if len(data) < 6 || data[0] != 0x80 {
		return data
	}
	var out []byte
	p := 0
	for p+6 <= len(data) && data[p] == 0x80 {
		kind := data[p+1]
		if kind != 1 && kind != 2 {
			break
		}
		n := int64(data[p+2]) | int64(data[p+3])<<8 | int64(data[p+4])<<16 | int64(data[p+5])<<24
		p += 6
		if n > int64(len(data)-p) {
			n = int64(len(data) - p)
		}
		out = append(out, data[p:p+int(n)]...)
		p += int(n)
	}
	return out
}

func decrypt(b []byte, r uint16, skip int) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = c ^ byte(r>>8)
		r = (uint16(c)+r)*52845 + 22719
	}
	if skip > len(out) {
		return nil
	}
	return out[skip:]
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func hexValue(c byte) byte {
	switch {
	case c <= '9':
		return c - '0'
	case c >= 'a':
		return c - 'a' + 10
	}
	return c - 'A' + 10
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' || c == 0
}

// splitEexec returns the clear text and the decrypted private part, which is nil
// for the odd font left unencrypted.
func splitEexec(data []byte) (clear, private []byte) {
	i := bytes.Index(data, []byte("currentfile eexec"))
	if i >= 0 {
		i += len("currentfile ")
	} else if i = bytes.Index(data, []byte("eexec")); i < 0 {
		return data, nil
	}
	clear = data[:i]
	p := i + len("eexec")
	for p < len(data) && (data[p] == ' ' || data[p] == '\t' || data[p] == '\r' || data[p] == '\n') {
		p++
	}
	rest := data[p:]
	hex := len(rest) >= 4
	for j := 0; j < 4 && j < len(rest); j++ {
		if !isHexDigit(rest[j]) {
			hex = false
		}
	}
	if hex {
		bin := make([]byte, 0, len(rest)/2)
		var hi byte
		half := false
		for _, c := range rest {
			if isSpace(c) {
				continue
			}
			if !isHexDigit(c) {
				break
			}
			if half {
				bin = append(bin, hi<<4|hexValue(c))
			} else {
				hi = hexValue(c)
			}
			half = !half
		}
		rest = bin
	}
	return clear, decrypt(rest, 55665, 4)
}

// psLex splits PostScript text into tokens. Names keep their slash.
type psLex struct {
	b []byte
	p int
}

func isPSDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func (l *psLex) next() string {
	for l.p < len(l.b) {
		c := l.b[l.p]
		if isSpace(c) {
			l.p++
		} else if c == '%' {
			for l.p < len(l.b) && l.b[l.p] != '\n' && l.b[l.p] != '\r' {
				l.p++
			}
		} else {
			break
		}
	}
	if l.p >= len(l.b) {
		return ""
	}
	start := l.p
	c := l.b[l.p]
	l.p++
	switch c {
	case '(':
		depth := 1
		for l.p < len(l.b) && depth > 0 {
			switch l.b[l.p] {
			case '\\':
				l.p++
			case '(':
				depth++
			case ')':
				depth--
			}
			l.p++
		}
		return "()"
	case ')', '<', '>', '[', ']', '{', '}':
		return string(c)
	case '/':
	}
	for l.p < len(l.b) && !isSpace(l.b[l.p]) && !isPSDelim(l.b[l.p]) {
		l.p++
	}
	return string(l.b[start:min(l.p, len(l.b))])
}

// psInt reads an integer, radix numbers included.
func psInt(s string) (int, bool) {
	if base, digits, ok := strings.Cut(s, "#"); ok {
		b, err := strconv.Atoi(base)
		if err != nil || b < 2 || b > 36 {
			return 0, false
		}
		v, err := strconv.ParseInt(digits, b, 32)
		return int(v), err == nil
	}
	v, err := strconv.Atoi(s)
	return v, err == nil
}

func (p *type1Parse) section(b []byte) {
	l := &psLex{b: b}
	for {
		tok := l.next()
		switch tok {
		case "":
			return
		case "/FontMatrix":
			p.fontMatrix(l)
		case "/Encoding":
			p.fontEncoding(l)
		case "/lenIV":
			if v, ok := psInt(l.next()); ok {
				p.lenIV = v
			}
		case "/Subrs":
			p.readSubrs(l)
		case "/CharStrings":
			p.readCharStrings(l)
		}
	}
}

func (p *type1Parse) fontMatrix(l *psLex) {
	if tok := l.next(); tok != "[" && tok != "{" {
		return
	}
	var m [6]float64
	for i := range m {
		v, err := strconv.ParseFloat(l.next(), 64)
		if err != nil {
			return
		}
		m[i] = v
	}
	if !p.hasMat && usableMatrix(m) {
		p.t.matrix = m
		p.hasMat = true
	}
}

func (p *type1Parse) fontEncoding(l *psLex) {
	first := l.next()
	if p.hasEnc {
		return
	}
	if first == "StandardEncoding" {
		p.t.encoding = StandardEncoding
		p.hasEnc = true
		return
	}
	if first == "[" {
		for code := 0; ; code++ {
			tok := l.next()
			if tok == "" || tok == "]" {
				break
			}
			if code < 256 && strings.HasPrefix(tok, "/") && tok != "/.notdef" {
				p.t.encoding[code] = tok[1:]
			}
		}
		p.hasEnc = true
		return
	}
	if _, ok := psInt(first); !ok {
		return
	}
	p.hasEnc = true
	for {
		tok := l.next()
		switch tok {
		case "", "def", "readonly":
			return
		case "dup":
			code, ok := psInt(l.next())
			name := l.next()
			if ok && code >= 0 && code < 256 && strings.HasPrefix(name, "/") && name != "/.notdef" {
				p.t.encoding[code] = name[1:]
			}
		}
	}
}

// binary reads a length and the RD token, then the bytes after one space.
func (l *psLex) binary(n int) []byte {
	l.next() // RD, -| or anything else a font calls it.
	l.p++
	if n < 0 || l.p > len(l.b) || n > len(l.b)-l.p {
		return nil
	}
	out := l.b[l.p : l.p+n]
	l.p += n
	return out
}

func (p *type1Parse) readSubrs(l *psLex) {
	count, ok := psInt(l.next())
	if !ok || p.hasSubs {
		return // Synthetic fonts define Subrs twice; the first set is the one to use.
	}
	p.hasSubs = true
	count = min(max(count, 0), maxType1Subrs)
	subrs := make([][]byte, 0, min(count, 1024))
	for {
		save := l.p
		tok := l.next()
		switch tok {
		case "array", "put", "noaccess", "NP", "|", "readonly":
			continue
		case "dup":
			i, ok1 := psInt(l.next())
			n, ok2 := psInt(l.next())
			if !ok1 || !ok2 {
				p.t.subrs = subrs
				return
			}
			b := l.binary(n)
			if b == nil {
				p.t.subrs = subrs
				return
			}
			if i >= 0 && i < maxType1Subrs {
				for len(subrs) <= i {
					subrs = append(subrs, nil)
				}
				subrs[i] = b
			}
			continue
		}
		if strings.HasPrefix(tok, "/") {
			l.p = save
		}
		p.t.subrs = subrs
		return
	}
}

func (p *type1Parse) readCharStrings(l *psLex) {
	if len(p.raw) > 0 {
		return
	}
	for {
		tok := l.next()
		switch {
		case tok == "":
			return
		case tok == "end":
			if len(p.raw) > 0 {
				return
			}
		case tok[0] == '/':
			save := l.p
			n, ok := psInt(l.next())
			if !ok {
				l.p = save
				continue
			}
			b := l.binary(n)
			if b == nil {
				return
			}
			if name := tok[1:]; len(p.raw) < maxType1Glyphs {
				p.names = append(p.names, name)
				p.raw = append(p.raw, b)
			}
		}
	}
}

func (p *type1Parse) decode(b []byte) []byte {
	if b == nil || p.lenIV < 0 {
		return b
	}
	return decrypt(b, 4330, p.lenIV)
}

// finish decrypts the charstrings, lenIV being known only now, and moves .notdef
// to glyph 0.
func (p *type1Parse) finish() {
	t := p.t
	t.names = p.names
	t.charstrs = make([][]byte, len(p.raw))
	for i, b := range p.raw {
		t.charstrs[i] = p.decode(b)
	}
	for i := range t.subrs {
		t.subrs[i] = p.decode(t.subrs[i])
	}
	for i, name := range t.names {
		if name == ".notdef" {
			t.names[0], t.names[i] = t.names[i], t.names[0]
			t.charstrs[0], t.charstrs[i] = t.charstrs[i], t.charstrs[0]
			break
		}
	}
	for i, name := range t.names {
		if _, dup := t.byName[name]; !dup {
			t.byName[name] = i
		}
	}
}

// NumGlyphs is the number of charstrings.
func (t *Type1) NumGlyphs() int { return len(t.charstrs) }

// Matrix is the /FontMatrix.
func (t *Type1) Matrix() [6]float64 { return t.matrix }

// GIDByCode maps a code through the built-in encoding.
func (t *Type1) GIDByCode(code byte) (int, bool) {
	if name := t.encoding[code]; name != "" {
		return t.GIDByName(name)
	}
	return 0, false
}

// GlyphName is the glyph's name, "" for a bad id.
func (t *Type1) GlyphName(gid int) string {
	if gid < 0 || gid >= len(t.names) {
		return ""
	}
	return t.names[gid]
}

// GIDByName finds a glyph by name.
func (t *Type1) GIDByName(name string) (int, bool) {
	g, ok := t.byName[name]
	return g, ok
}

// GIDByRune finds a glyph through its name and the Adobe glyph list.
func (t *Type1) GIDByRune(r rune) (int, bool) {
	t.once.Do(func() { t.index = newNameIndex(len(t.names), t.GlyphName) })
	g, ok := t.index.byRune[r]
	return g, ok
}

// Outline runs the glyph's charstring.
func (t *Type1) Outline(gid int) (path Path) {
	defer func() {
		if recover() != nil {
			path = nil
		}
	}()
	if gid < 0 || gid >= len(t.charstrs) {
		return nil
	}
	s := t1Run{f: t}
	if err := s.run(t.charstrs[gid], 0); err != nil {
		return nil
	}
	return s.pb.finish()
}

// Advance runs the charstring as far as hsbw or sbw.
func (t *Type1) Advance(gid int) (w float64, ok bool) {
	defer func() {
		if recover() != nil {
			w, ok = 0, false
		}
	}()
	if gid < 0 || gid >= len(t.charstrs) {
		return 0, false
	}
	s := t1Run{f: t, widthOnly: true}
	if err := s.run(t.charstrs[gid], 0); err != nil || !s.haveWidth {
		return 0, false
	}
	return s.wx, true
}

// t1Run interprets one Type 1 charstring.
type t1Run struct {
	f         *Type1
	st        [64]float64
	sp        int
	ps        [64]float64
	psn       int
	x, y      float64
	ox, oy    float64 // Origin of a seac accent.
	sbx       float64
	wx        float64
	haveWidth bool
	widthOnly bool
	done      bool
	inSeac    bool
	flexing   bool
	flexEnded bool
	flexN     int
	flex      [7][2]float64
	ops       int
	pb        pathBuilder
}

var errType1 = errors.New("font: bad Type 1 charstring")

func (s *t1Run) push(v float64) error {
	if s.sp >= len(s.st) {
		return errStack
	}
	s.st[s.sp] = v
	s.sp++
	return nil
}

func (s *t1Run) rel(dx, dy float64) {
	s.x += dx
	s.y += dy
}

func (s *t1Run) moveBy(dx, dy float64) {
	s.rel(dx, dy)
	if !s.flexing {
		s.pb.moveTo(s.x, s.y)
	}
}

func (s *t1Run) lineBy(dx, dy float64) {
	s.rel(dx, dy)
	s.pb.lineTo(s.x, s.y)
}

func (s *t1Run) curveBy(dx1, dy1, dx2, dy2, dx3, dy3 float64) {
	x1, y1 := s.x+dx1, s.y+dy1
	x2, y2 := x1+dx2, y1+dy2
	s.x, s.y = x2+dx3, y2+dy3
	s.pb.curveTo(x1, y1, x2, y2, s.x, s.y)
}

func (s *t1Run) sideBearing(sbx, sby, wx float64) {
	s.sbx = sbx
	s.wx = wx
	s.haveWidth = true
	s.x, s.y = s.ox+sbx, s.oy+sby
	s.pb.moveTo(s.x, s.y)
	if s.widthOnly {
		s.done = true
	}
}

func (s *t1Run) run(cs []byte, depth int) error {
	if depth > maxCallDepth {
		return errBudget
	}
	for p := 0; p < len(cs) && !s.done; {
		if s.ops++; s.ops > maxOps || s.pb.over {
			return errBudget
		}
		b := cs[p]
		p++
		switch {
		case b >= 32 && b <= 246:
			if err := s.push(float64(int(b) - 139)); err != nil {
				return err
			}
			continue
		case b >= 247 && b <= 250:
			if p >= len(cs) {
				return errTruncated
			}
			if err := s.push(float64((int(b)-247)*256 + int(cs[p]) + 108)); err != nil {
				return err
			}
			p++
			continue
		case b >= 251 && b <= 254:
			if p >= len(cs) {
				return errTruncated
			}
			if err := s.push(float64(-(int(b)-251)*256 - int(cs[p]) - 108)); err != nil {
				return err
			}
			p++
			continue
		case b == 255:
			if p+4 > len(cs) {
				return errTruncated
			}
			if err := s.push(float64(int32(u32(cs, p)))); err != nil {
				return err
			}
			p += 4
			continue
		}
		a := s.st[:s.sp]
		need := func(n int) bool { return len(a) >= n }
		switch b {
		case 1, 3: // hstem, vstem
			s.sp = 0
		case 4: // vmoveto
			if !need(1) {
				return errStack
			}
			s.moveBy(0, a[len(a)-1])
			s.sp = 0
		case 5: // rlineto
			if !need(2) {
				return errStack
			}
			s.lineBy(a[len(a)-2], a[len(a)-1])
			s.sp = 0
		case 6: // hlineto
			if !need(1) {
				return errStack
			}
			s.lineBy(a[len(a)-1], 0)
			s.sp = 0
		case 7: // vlineto
			if !need(1) {
				return errStack
			}
			s.lineBy(0, a[len(a)-1])
			s.sp = 0
		case 8: // rrcurveto
			if !need(6) {
				return errStack
			}
			a = a[len(a)-6:]
			s.curveBy(a[0], a[1], a[2], a[3], a[4], a[5])
			s.sp = 0
		case 9: // closepath
			s.pb.closePath()
			s.sp = 0
		case 10: // callsubr
			if !need(1) {
				return errStack
			}
			s.sp--
			i := int(s.st[s.sp])
			if i < 0 || i >= len(s.f.subrs) || s.f.subrs[i] == nil {
				return errType1
			}
			if err := s.run(s.f.subrs[i], depth+1); err != nil {
				return err
			}
		case 11: // return
			return nil
		case 13: // hsbw
			if !need(2) {
				return errStack
			}
			s.sideBearing(a[0], 0, a[1])
			s.sp = 0
		case 14: // endchar
			s.pb.closePath()
			s.done = true
		case 21: // rmoveto
			if !need(2) {
				return errStack
			}
			s.moveBy(a[len(a)-2], a[len(a)-1])
			s.sp = 0
		case 22: // hmoveto
			if !need(1) {
				return errStack
			}
			s.moveBy(a[len(a)-1], 0)
			s.sp = 0
		case 30: // vhcurveto
			if !need(4) {
				return errStack
			}
			a = a[len(a)-4:]
			s.curveBy(0, a[0], a[1], a[2], a[3], 0)
			s.sp = 0
		case 31: // hvcurveto
			if !need(4) {
				return errStack
			}
			a = a[len(a)-4:]
			s.curveBy(a[0], 0, a[1], a[2], 0, a[3])
			s.sp = 0
		case 12:
			if p >= len(cs) {
				return errTruncated
			}
			op := cs[p]
			p++
			if err := s.escape(op); err != nil {
				return err
			}
		default:
			s.sp = 0
		}
	}
	return nil
}

func (s *t1Run) escape(op byte) error {
	a := s.st[:s.sp]
	switch op {
	case 0, 1, 2: // dotsection, vstem3, hstem3
		s.sp = 0
	case 6: // seac
		if len(a) < 5 {
			return errStack
		}
		return s.seac(a[0], a[1], a[2], int(a[3])&0xFF, int(a[4])&0xFF)
	case 7: // sbw
		if len(a) < 4 {
			return errStack
		}
		s.sideBearing(a[0], a[1], a[2])
		s.sp = 0
	case 12: // div
		if len(a) < 2 {
			return errStack
		}
		v := 0.0
		if a[len(a)-1] != 0 {
			v = a[len(a)-2] / a[len(a)-1]
		}
		s.sp -= 2
		return s.push(v)
	case 16:
		return s.callOther()
	case 17: // pop
		if s.psn == 0 {
			return s.push(0)
		}
		s.psn--
		return s.push(s.ps[s.psn])
	case 33: // setcurrentpoint
		if len(a) < 2 {
			return errStack
		}
		// Flex has already put the pen on these absolute coordinates.
		if !s.flexEnded {
			s.x, s.y = a[len(a)-2]+s.ox, a[len(a)-1]+s.oy
			s.pb.x, s.pb.y = s.x, s.y
		}
		s.flexEnded = false
		s.sp = 0
	default:
		s.sp = 0
	}
	return nil
}

// callOther handles the standard OtherSubrs; others just move their arguments to
// the PostScript stack for pop to fetch.
func (s *t1Run) callOther() error {
	if s.sp < 2 {
		return errStack
	}
	which := int(s.st[s.sp-1])
	n := int(s.st[s.sp-2])
	s.sp -= 2
	if n < 0 || n > s.sp {
		return errStack
	}
	args := s.st[s.sp-n : s.sp]
	s.sp -= n
	switch which {
	case 0: // End flex.
		if !s.flexing || s.flexN != 7 || n < 3 {
			return errType1
		}
		s.flexing = false
		f := &s.flex
		s.pb.curveTo(f[1][0], f[1][1], f[2][0], f[2][1], f[3][0], f[3][1])
		s.pb.curveTo(f[4][0], f[4][1], f[5][0], f[5][1], f[6][0], f[6][1])
		s.x, s.y = f[6][0], f[6][1]
		s.flexEnded = true
		s.psn = 0
		s.ps[0], s.ps[1] = args[2], args[1]
		s.psn = 2
	case 1: // Start flex.
		s.flexing = true
		s.flexN = 0
	case 2: // Add a flex point.
		if !s.flexing || s.flexN >= len(s.flex) {
			return errType1
		}
		s.flex[s.flexN] = [2]float64{s.x, s.y}
		s.flexN++
	default:
		if n > len(s.ps)-s.psn {
			return errStack
		}
		for i := n - 1; i >= 0; i-- {
			s.ps[s.psn] = args[i]
			s.psn++
		}
	}
	return nil
}

// seac draws base and accent, both standard encoding codes. The accent's own
// side bearing is measured from sbx + adx - asb.
func (s *t1Run) seac(asb, adx, ady float64, bchar, achar int) error {
	if s.inSeac {
		return errType1
	}
	s.pb.closePath()
	for i, code := range [2]int{bchar, achar} {
		gid, ok := s.f.GIDByName(StandardEncoding[code])
		if !ok {
			continue
		}
		sub := t1Run{f: s.f, inSeac: true, ops: s.ops, pb: s.pb}
		if i == 1 {
			sub.ox, sub.oy = s.sbx+adx-asb, ady
		}
		if err := sub.run(s.f.charstrs[gid], 0); err != nil {
			return err
		}
		sub.pb.closePath()
		s.pb = sub.pb
		s.ops = sub.ops
	}
	s.done = true
	return nil
}
