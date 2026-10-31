package font

import (
	"errors"
	"math"
	"strconv"
	"sync"
)

// CFF is a bare Compact Font Format program, name-keyed or CID-keyed. Only the
// first font of a FontSet is read.
type CFF struct {
	name        string
	data        []byte
	isCID       bool
	matrix      [6]float64
	charStrings cffIndex
	globalSubrs cffIndex
	strings     cffIndex
	charset     []uint16 // SID or CID of each glyph; nil means identity.
	cidToGID    map[uint16]uint16
	encoding    [256]uint16 // Glyph of each code, 0 for none.
	privates    []cffPrivate
	fdSelect    []uint8

	once  sync.Once
	names *nameIndex
}

// cffPrivate is what charstrings need from a Private DICT.
type cffPrivate struct {
	subrs    cffIndex
	defaultW float64
	nominalW float64
	adjust   *[6]float64 // Maps this FD's space to the font's when they differ.
}

// cffIndex is an INDEX structure read in place.
type cffIndex struct {
	data     []byte
	count    int
	offSize  int
	offBase  int
	dataBase int
}

func readIndex(data []byte, pos int) (cffIndex, int, error) {
	if pos < 0 || pos+2 > len(data) {
		return cffIndex{}, 0, errTruncated
	}
	x := cffIndex{data: data, count: u16(data, pos)}
	if x.count == 0 {
		return x, pos + 2, nil
	}
	if pos+3 > len(data) {
		return cffIndex{}, 0, errTruncated
	}
	x.offSize = int(data[pos+2])
	if x.offSize < 1 || x.offSize > 4 {
		return cffIndex{}, 0, errors.New("font: bad CFF offset size")
	}
	x.offBase = pos + 3
	x.dataBase = x.offBase + (x.count+1)*x.offSize - 1
	if x.dataBase > len(data) {
		return cffIndex{}, 0, errTruncated
	}
	next := int64(x.dataBase) + x.offset(x.count)
	if next > int64(len(data)) {
		next = int64(len(data))
	}
	return x, int(next), nil
}

func (x *cffIndex) offset(i int) int64 {
	var v int64
	p := x.offBase + i*x.offSize
	for _, b := range x.data[p : p+x.offSize] {
		v = v<<8 | int64(b)
	}
	return v
}

// get returns entry i, nil when it is out of range or malformed.
func (x *cffIndex) get(i int) []byte {
	if i < 0 || i >= x.count {
		return nil
	}
	a, b := x.offset(i), x.offset(i+1)
	if a < 1 || b < a {
		return nil
	}
	end := int64(x.dataBase) + b
	if end > int64(len(x.data)) {
		end = int64(len(x.data))
	}
	start := int64(x.dataBase) + a
	if start > end {
		return nil
	}
	return x.data[start:end]
}

// CFF DICT operators; two-byte ones are 1200 plus the second byte.
const (
	opCharset     = 15
	opEncoding    = 16
	opCharStrings = 17
	opPrivate     = 18
	opSubrs       = 19
	opDefaultW    = 20
	opNominalW    = 21
	opCharType    = 1206
	opFontMatrix  = 1207
	opROS         = 1230
	opFDArray     = 1236
	opFDSelect    = 1237
)

// parseDict reads operators and their operands. Bad input ends the dict early.
func parseDict(b []byte) map[int][]float64 {
	d := make(map[int][]float64)
	var args []float64
	for p := 0; p < len(b); {
		c := b[p]
		p++
		switch {
		case c <= 21:
			op := int(c)
			if c == 12 {
				if p >= len(b) {
					return d
				}
				op = 1200 + int(b[p])
				p++
			}
			if _, dup := d[op]; !dup {
				d[op] = args
			}
			args = nil
			continue
		case c == 28:
			if p+2 > len(b) {
				return d
			}
			args = append(args, float64(i16(b, p)))
			p += 2
		case c == 29:
			if p+4 > len(b) {
				return d
			}
			args = append(args, float64(int32(u32(b, p))))
			p += 4
		case c == 30:
			v, n := parseReal(b[p:])
			args = append(args, v)
			p += n
		case c >= 32 && c <= 246:
			args = append(args, float64(int(c)-139))
		case c >= 247 && c <= 250:
			if p >= len(b) {
				return d
			}
			args = append(args, float64((int(c)-247)*256+int(b[p])+108))
			p++
		case c >= 251 && c <= 254:
			if p >= len(b) {
				return d
			}
			args = append(args, float64(-(int(c)-251)*256-int(b[p])-108))
			p++
		}
		if len(args) > 513 {
			return d
		}
	}
	return d
}

// parseReal reads a nibble-coded real and returns the bytes it used.
func parseReal(b []byte) (float64, int) {
	var s []byte
	used := 0
loop:
	for _, c := range b {
		used++
		for _, n := range [2]byte{c >> 4, c & 15} {
			switch {
			case n <= 9:
				s = append(s, '0'+n)
			case n == 10:
				s = append(s, '.')
			case n == 11:
				s = append(s, 'E')
			case n == 12:
				s = append(s, 'E', '-')
			case n == 14:
				s = append(s, '-')
			case n == 15:
				break loop
			}
		}
		if len(s) > 64 {
			break
		}
	}
	v, err := strconv.ParseFloat(string(s), 64)
	if err != nil || math.IsInf(v, 0) {
		return 0, used
	}
	return v, used
}

func dictInt(d map[int][]float64, op int, def int) int {
	if a := d[op]; len(a) > 0 && math.Abs(a[len(a)-1]) < 1<<31 {
		return int(a[len(a)-1])
	}
	return def
}

func dictFloat(d map[int][]float64, op int, def float64) float64 {
	if a := d[op]; len(a) > 0 {
		return a[len(a)-1]
	}
	return def
}

func dictMatrix(d map[int][]float64) ([6]float64, bool) {
	var m [6]float64
	a := d[opFontMatrix]
	if len(a) < 6 {
		return m, false
	}
	copy(m[:], a)
	return m, usableMatrix(m)
}

// ParseCFF reads a bare CFF program.
func ParseCFF(data []byte) (c *CFF, err error) {
	defer func() {
		if err != nil {
			c = nil
		}
	}()
	defer recovered(&err)
	// Some files carry junk before the header.
	for len(data) > 0 && data[0] != 1 {
		data = data[1:]
	}
	if len(data) < 4 {
		return nil, errTruncated
	}
	nameIdx, p, err := readIndex(data, int(data[2]))
	if err != nil {
		return nil, err
	}
	topIdx, p, err := readIndex(data, p)
	if err != nil {
		return nil, err
	}
	c = &CFF{data: data, name: string(nameIdx.get(0))}
	if c.strings, p, err = readIndex(data, p); err != nil {
		return nil, err
	}
	if c.globalSubrs, _, err = readIndex(data, p); err != nil {
		return nil, err
	}
	top := parseDict(topIdx.get(0))
	if dictInt(top, opCharType, 2) != 2 {
		return nil, errors.New("font: CFF with Type 1 charstrings")
	}
	if c.charStrings, _, err = readIndex(data, dictInt(top, opCharStrings, -1)); err != nil {
		return nil, err
	}
	if c.charStrings.count == 0 {
		return nil, errNoGlyphs
	}
	_, c.isCID = top[opROS]
	var hasTop bool
	if c.matrix, hasTop = dictMatrix(top); !hasTop {
		c.matrix = [6]float64{0.001, 0, 0, 0.001, 0, 0}
	}
	if c.isCID {
		if err := c.readFDs(top, hasTop); err != nil {
			return nil, err
		}
	} else {
		c.privates = []cffPrivate{c.readPrivate(top)}
	}
	c.readCharset(dictInt(top, opCharset, 0))
	if !c.isCID {
		c.readEncoding(dictInt(top, opEncoding, 0))
	}
	return c, nil
}

func (c *CFF) readPrivate(d map[int][]float64) cffPrivate {
	var pr cffPrivate
	a := d[opPrivate]
	if len(a) < 2 {
		return pr
	}
	size, off := int64(a[0]), int64(a[1])
	if size < 0 || off < 0 || off >= int64(len(c.data)) {
		return pr
	}
	if off+size > int64(len(c.data)) {
		size = int64(len(c.data)) - off
	}
	dict := parseDict(c.data[off : off+size])
	pr.defaultW = dictFloat(dict, opDefaultW, 0)
	pr.nominalW = dictFloat(dict, opNominalW, 0)
	if rel := dictInt(dict, opSubrs, 0); rel > 0 {
		if x, _, err := readIndex(c.data, int(off)+rel); err == nil {
			pr.subrs = x
		}
	}
	return pr
}

// matrixUnits is the em size a matrix stands for.
func matrixUnits(m [6]float64) float64 {
	v := math.Abs(m[0])
	if v == 0 {
		v = math.Abs(m[1])
	}
	if v == 0 {
		return 1
	}
	return 1 / v
}

// combineMatrix joins a Top DICT matrix with a Font DICT one. When both carry a
// scale the product is scaled back as FreeType does, or glyphs would vanish.
func combineMatrix(sub, top [6]float64) [6]float64 {
	s := 1.0
	if a, b := matrixUnits(top), matrixUnits(sub); a > 1.5 && b > 1.5 {
		s = min(a, b)
	}
	return [6]float64{
		s * (sub[0]*top[0] + sub[1]*top[2]), s * (sub[0]*top[1] + sub[1]*top[3]),
		s * (sub[2]*top[0] + sub[3]*top[2]), s * (sub[2]*top[1] + sub[3]*top[3]),
		s * (sub[4]*top[0] + sub[5]*top[2] + top[4]), s * (sub[4]*top[1] + sub[5]*top[3] + top[5]),
	}
}

func (c *CFF) readFDs(top map[int][]float64, hasTop bool) error {
	fdIdx, _, err := readIndex(c.data, dictInt(top, opFDArray, -1))
	if err != nil {
		return err
	}
	if fdIdx.count == 0 || fdIdx.count > 256 {
		return errors.New("font: bad CFF FDArray")
	}
	eff := make([][6]float64, fdIdx.count)
	c.privates = make([]cffPrivate, fdIdx.count)
	for i := range c.privates {
		d := parseDict(fdIdx.get(i))
		c.privates[i] = c.readPrivate(d)
		m, has := dictMatrix(d)
		switch {
		case has && hasTop:
			eff[i] = combineMatrix(m, c.matrix)
		case has:
			eff[i] = m
		default:
			eff[i] = c.matrix
		}
		if !usableMatrix(eff[i]) {
			eff[i] = [6]float64{0.001, 0, 0, 0.001, 0, 0}
		}
	}
	c.matrix = eff[0]
	inv := invertMatrix(c.matrix)
	for i := range c.privates {
		if eff[i] != c.matrix {
			m := matrixProduct(eff[i], inv)
			c.privates[i].adjust = &m
		}
	}
	c.readFDSelect(dictInt(top, opFDSelect, -1))
	return nil
}

func invertMatrix(m [6]float64) [6]float64 {
	det := m[0]*m[3] - m[1]*m[2]
	return [6]float64{
		m[3] / det, -m[1] / det, -m[2] / det, m[0] / det,
		(m[2]*m[5] - m[3]*m[4]) / det, (m[1]*m[4] - m[0]*m[5]) / det,
	}
}

// matrixProduct applies a first, then b.
func matrixProduct(a, b [6]float64) [6]float64 {
	return [6]float64{
		a[0]*b[0] + a[1]*b[2], a[0]*b[1] + a[1]*b[3],
		a[2]*b[0] + a[3]*b[2], a[2]*b[1] + a[3]*b[3],
		a[4]*b[0] + a[5]*b[2] + b[4], a[4]*b[1] + a[5]*b[3] + b[5],
	}
}

func (c *CFF) readFDSelect(off int) {
	n := c.charStrings.count
	c.fdSelect = make([]uint8, n)
	if off <= 0 || off >= len(c.data) {
		return
	}
	switch c.data[off] {
	case 0:
		copy(c.fdSelect, c.data[off+1:])
	case 3:
		ranges := u16(c.data, off+1)
		p := off + 3
		for i := 0; i < ranges && p+5 <= len(c.data); i++ {
			first, fd, next := u16(c.data, p), c.data[p+2], u16(c.data, p+3)
			for g := first; g < next && g < n; g++ {
				c.fdSelect[g] = fd
			}
			p += 3
		}
	}
}

func (c *CFF) readCharset(off int) {
	n := c.charStrings.count
	if c.isCID {
		if off <= 2 {
			return // CID equals glyph id.
		}
	} else {
		switch off {
		case 0:
			c.setCharset(isoAdobeCharset)
			return
		case 1:
			c.setCharset(expertCharset)
			return
		case 2:
			c.setCharset(expertSubsetCharset)
			return
		}
	}
	if off < 0 || off >= len(c.data) {
		return
	}
	set := make([]uint16, n)
	p := off + 1
	g := 1
	switch f := c.data[off]; f {
	case 0:
		for ; g < n && p+2 <= len(c.data); g++ {
			set[g] = uint16(u16(c.data, p))
			p += 2
		}
	case 1, 2:
		for g < n && p+2 < len(c.data) {
			first := u16(c.data, p)
			p += 2
			left := int(c.data[p])
			if f == 2 {
				left = u16(c.data, p)
				p++
			}
			p++
			for i := 0; i <= left && g < n; i++ {
				set[g] = uint16(first + i)
				g++
			}
		}
	}
	c.setCharset(set)
}

func (c *CFF) setCharset(set []uint16) {
	n := c.charStrings.count
	if len(set) > n {
		set = set[:n]
	}
	c.charset = set
	if c.isCID {
		c.cidToGID = make(map[uint16]uint16, len(set))
		for g := len(set) - 1; g >= 0; g-- {
			c.cidToGID[set[g]] = uint16(g)
		}
	}
}

func (c *CFF) readEncoding(off int) {
	sidGID := make(map[uint16]uint16, len(c.charset))
	for g := len(c.charset) - 1; g > 0; g-- {
		sidGID[c.charset[g]] = uint16(g)
	}
	switch off {
	case 0:
		byName := make(map[string]uint16, len(c.charset))
		for g := len(c.charset) - 1; g > 0; g-- {
			byName[c.sidString(int(c.charset[g]))] = uint16(g)
		}
		for code, name := range StandardEncoding {
			if name != "" {
				c.encoding[code] = byName[name]
			}
		}
		return
	case 1:
		for code := range expertEncoding {
			if sid := expertEncoding[code]; sid != 0 {
				c.encoding[code] = sidGID[sid]
			}
		}
		return
	}
	if off < 0 || off >= len(c.data) {
		return
	}
	f := c.data[off]
	p := off + 1
	n := c.charStrings.count
	if p >= len(c.data) {
		return
	}
	count := int(c.data[p])
	p++
	g := 1
	switch f & 0x7F {
	case 0:
		for i := 0; i < count && p < len(c.data); i++ {
			if g < n {
				c.encoding[c.data[p]] = uint16(g)
			}
			g++
			p++
		}
	case 1:
		for i := 0; i < count && p+2 <= len(c.data); i++ {
			first, left := int(c.data[p]), int(c.data[p+1])
			p += 2
			for j := 0; j <= left; j++ {
				if first+j < 256 && g < n {
					c.encoding[first+j] = uint16(g)
				}
				g++
			}
		}
	}
	if f&0x80 != 0 && p < len(c.data) {
		sups := int(c.data[p])
		p++
		for i := 0; i < sups && p+3 <= len(c.data); i++ {
			c.encoding[c.data[p]] = sidGID[uint16(u16(c.data, p+1))]
			p += 3
		}
	}
}

func (c *CFF) sidString(sid int) string {
	if sid < len(cffStandardStrings) {
		return cffStandardStrings[sid]
	}
	return string(c.strings.get(sid - len(cffStandardStrings)))
}

// NumGlyphs is the number of charstrings.
func (c *CFF) NumGlyphs() int { return c.charStrings.count }

// Matrix is the font's FontMatrix; for a CID font, that of its first FD.
func (c *CFF) Matrix() [6]float64 { return c.matrix }

// IsCID reports a CID-keyed font.
func (c *CFF) IsCID() bool { return c.isCID }

// GIDByCID maps a CID through the charset; for a name-keyed font, cid == gid.
func (c *CFF) GIDByCID(cid int) (int, bool) {
	if c.cidToGID != nil {
		if cid < 0 || cid > 0xFFFF {
			return 0, false
		}
		g, ok := c.cidToGID[uint16(cid)]
		return int(g), ok
	}
	return cid, cid >= 0 && cid < c.charStrings.count
}

// GIDByCode maps a code through the built-in encoding.
func (c *CFF) GIDByCode(code byte) (int, bool) {
	g := c.encoding[code]
	return int(g), g != 0
}

// GlyphName is the glyph's charset name, "" for a CID font or a bad id.
func (c *CFF) GlyphName(gid int) string {
	if c.isCID || gid < 0 || gid >= c.charStrings.count {
		return ""
	}
	if gid == 0 {
		return ".notdef"
	}
	if gid >= len(c.charset) {
		return ""
	}
	return c.sidString(int(c.charset[gid]))
}

func (c *CFF) index() *nameIndex {
	c.once.Do(func() {
		n := c.charStrings.count
		if c.isCID {
			n = 0
		}
		c.names = newNameIndex(n, c.GlyphName)
	})
	return c.names
}

// GIDByName finds a glyph by its charset name.
func (c *CFF) GIDByName(name string) (int, bool) {
	g, ok := c.index().byName[name]
	return g, ok
}

// GIDByRune finds a glyph through its name and the Adobe glyph list.
func (c *CFF) GIDByRune(r rune) (int, bool) {
	g, ok := c.index().byRune[r]
	return g, ok
}

func (c *CFF) private(gid int) *cffPrivate {
	i := 0
	if gid < len(c.fdSelect) {
		i = int(c.fdSelect[gid])
	}
	if i >= len(c.privates) {
		i = 0
	}
	return &c.privates[i]
}

// Outline runs the glyph's charstring.
func (c *CFF) Outline(gid int) (path Path) {
	defer func() {
		if recover() != nil {
			path = nil
		}
	}()
	cs := c.charStrings.get(gid)
	if cs == nil {
		return nil
	}
	pr := c.private(gid)
	s := t2Run{c: c, pr: pr}
	if err := s.run(cs, 0); err != nil {
		return nil
	}
	path = s.pb.finish()
	if pr.adjust != nil {
		transform(path, pr.adjust)
	}
	return path
}

// Advance runs the charstring as far as its width.
func (c *CFF) Advance(gid int) (w float64, ok bool) {
	defer func() {
		if recover() != nil {
			w, ok = 0, false
		}
	}()
	cs := c.charStrings.get(gid)
	if cs == nil {
		return 0, false
	}
	pr := c.private(gid)
	s := t2Run{c: c, pr: pr, widthOnly: true}
	if err := s.run(cs, 0); err != nil && !s.haveWidth {
		return 0, false
	}
	if !s.widthSet {
		return pr.defaultW, true
	}
	return s.width, true
}
