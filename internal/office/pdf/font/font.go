// Package font reads the font programs and CMaps embedded in PDF files.
//
// Every input is treated as hostile: nothing here panics on bad data, and
// recursion, sizes and charstring work are all bounded.
package font

import (
	"errors"
	"fmt"
	"math"
)

// Seg is one path step: Op 'M' (X,Y), 'L' (X,Y), 'Q' (X1,Y1,X,Y), 'C' (X1,Y1,X2,Y2,X,Y), 'Z'.
type Seg struct {
	Op                   byte
	X1, Y1, X2, Y2, X, Y float64
}

// Path is a glyph outline.
type Path []Seg

// Program is a parsed font file. Glyph ids are the program's own.
type Program interface {
	NumGlyphs() int
	// Matrix maps glyph space to text space (1/1000 or 1/unitsPerEm scale, or the FontMatrix of Type1/CFF).
	Matrix() [6]float64
	// Outline is the glyph's outline in glyph space, y up, nil for an empty or bad glyph. It never panics.
	Outline(gid int) Path
	// Advance is the glyph's own advance in glyph space; ok false when unknown.
	Advance(gid int) (float64, bool)
	// GIDByName finds a glyph by its PostScript name.
	GIDByName(name string) (int, bool)
	// GIDByRune finds a glyph through the program's Unicode map, or its glyph names through the Adobe glyph list.
	GIDByRune(r rune) (int, bool)
}

// Limits on charstring work. Real glyphs stay far below them.
const (
	maxCallDepth = 16      // Nested subroutine calls.
	maxOps       = 1 << 18 // Operators and operands in one glyph.
	maxSegs      = 1 << 17 // Path steps in one glyph.
)

var (
	errTruncated = errors.New("font: truncated data")
	errBudget    = errors.New("font: glyph too complex")
	errStack     = errors.New("font: charstring stack error")
	errNoGlyphs  = errors.New("font: no glyph data")
)

// recovered turns a panic into an error for the public entry points.
func recovered(err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("font: bad data: %v", r)
	}
}

func u16(b []byte, i int) int {
	if i < 0 || i+2 > len(b) {
		return 0
	}
	return int(b[i])<<8 | int(b[i+1])
}

func i16(b []byte, i int) int {
	return int(int16(u16(b, i)))
}

func u32(b []byte, i int) uint32 {
	if i < 0 || i+4 > len(b) {
		return 0
	}
	return uint32(b[i])<<24 | uint32(b[i+1])<<16 | uint32(b[i+2])<<8 | uint32(b[i+3])
}

// pathBuilder collects a contour at a time. A moveto stays pending until something
// is drawn, so empty contours leave no trace.
type pathBuilder struct {
	p    Path
	x, y float64
	open bool
	over bool
}

func (b *pathBuilder) add(s Seg) {
	if len(b.p) >= maxSegs {
		b.over = true
		return
	}
	b.p = append(b.p, s)
}

func (b *pathBuilder) begin() {
	if !b.open {
		b.add(Seg{Op: 'M', X: b.x, Y: b.y})
		b.open = true
	}
}

func (b *pathBuilder) moveTo(x, y float64) {
	b.closePath()
	b.x, b.y = x, y
}

func (b *pathBuilder) lineTo(x, y float64) {
	b.begin()
	b.add(Seg{Op: 'L', X: x, Y: y})
	b.x, b.y = x, y
}

func (b *pathBuilder) curveTo(x1, y1, x2, y2, x, y float64) {
	b.begin()
	b.add(Seg{Op: 'C', X1: x1, Y1: y1, X2: x2, Y2: y2, X: x, Y: y})
	b.x, b.y = x, y
}

func (b *pathBuilder) closePath() {
	if b.open {
		b.add(Seg{Op: 'Z'})
		b.open = false
	}
}

// finish closes the last contour and drops a path holding bad numbers.
func (b *pathBuilder) finish() Path {
	b.closePath()
	if b.over || !finitePath(b.p) {
		return nil
	}
	return b.p
}

func finitePath(p Path) bool {
	bad := func(v float64) bool { return math.IsNaN(v) || math.IsInf(v, 0) }
	for i := range p {
		s := &p[i]
		if bad(s.X) || bad(s.Y) || bad(s.X1) || bad(s.Y1) || bad(s.X2) || bad(s.Y2) {
			return false
		}
	}
	return true
}

// transform applies a font matrix to every point of p, in place.
func transform(p Path, m *[6]float64) {
	at := func(x, y float64) (float64, float64) {
		return m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]
	}
	for i := range p {
		s := &p[i]
		s.X1, s.Y1 = at(s.X1, s.Y1)
		s.X2, s.Y2 = at(s.X2, s.Y2)
		s.X, s.Y = at(s.X, s.Y)
	}
}

// usableMatrix reports whether m can map glyphs at all.
func usableMatrix(m [6]float64) bool {
	for _, v := range m {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return m[0]*m[3]-m[1]*m[2] != 0
}

// nameIndex holds the glyph name lookups of a program, built when first used.
type nameIndex struct {
	byName map[string]int
	byRune map[rune]int
}

func newNameIndex(n int, name func(gid int) string) *nameIndex {
	x := &nameIndex{byName: make(map[string]int, n), byRune: make(map[rune]int, n)}
	for gid := 0; gid < n; gid++ {
		s := name(gid)
		if s == "" {
			continue
		}
		if _, dup := x.byName[s]; !dup {
			x.byName[s] = gid
		}
		if r, ok := NameToRune(s); ok {
			if _, dup := x.byRune[r]; !dup {
				x.byRune[r] = gid
			}
		}
	}
	return x
}
