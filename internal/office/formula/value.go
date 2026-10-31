// Package formula is a spreadsheet formula engine with Excel semantics: it parses, prints and rewrites formulas,
// evaluates them over a Book, keeps a dependency graph for incremental recalculation and renders number formats.
package formula

import (
	"strconv"
	"strings"
)

// Sheet limits, the same as Excel's.
const (
	MaxRows = 1 << 20
	MaxCols = 1 << 14
)

// maxCells is the largest array the evaluator will materialise.
const maxCells = 5_000_000

// maxText is Excel's limit on the length of a text value.
const maxText = 32767

// Kind says what a Value holds.
type Kind uint8

// The kinds of value.
const (
	Blank Kind = iota
	Number
	Text
	Bool
	Error
	Array
	Ref
)

// Value is the result of a formula, a cell's content or an intermediate during evaluation.
type Value struct {
	Kind Kind
	Num  float64 // Number; Bool as 0 or 1
	Str  string  // Text; for Error the code, as "#N/A"
	Arr  *Grid   // Array
	Area Area    // Ref: only inside evaluation; never handed to Book.SetResult
}

// Grid is a rectangular array of values in row-major order.
type Grid struct {
	Rows, Cols int
	V          []Value
}

// Num makes a number.
func Num(f float64) Value { return Value{Kind: Number, Num: f} }

// Str makes a text.
func Str(s string) Value { return Value{Kind: Text, Str: s} }

// BoolOf makes a logical value.
func BoolOf(b bool) Value {
	if b {
		return Value{Kind: Bool, Num: 1}
	}
	return Value{Kind: Bool}
}

// Err makes an error value from its code.
func Err(code string) Value { return Value{Kind: Error, Str: code} }

// The error codes.
const (
	ErrNull  = "#NULL!"
	ErrDiv0  = "#DIV/0!"
	ErrValue = "#VALUE!"
	ErrRef   = "#REF!"
	ErrName  = "#NAME?"
	ErrNum   = "#NUM!"
	ErrNA    = "#N/A"
)

// Cell addresses one cell; all indices are zero-based.
type Cell struct{ Sheet, Row, Col int32 }

// Area is an inclusive rectangle of cells on one sheet.
type Area struct{ Sheet, R1, C1, R2, C2 int32 }

// Book is the workbook as the engine sees it.
type Book interface {
	SheetIndex(name string) int // case-insensitive; -1 when unknown
	SheetName(i int) string
	Value(sheet, row, col int) Value // Blank when empty; a formula cell gives its cached result
	// Each calls fn for the non-empty cells of the area in row-major order, until fn is false.
	// It must be sparse so SUM(A:A) is cheap.
	Each(sheet, r1, c1, r2, c2 int, fn func(row, col int, v Value) bool)
	Bounds(sheet int) (rows, cols int)  // used extent: last used row+1, last used col+1
	Name(name string, sheet int) string // the text of a defined name visible from sheet, "" if none
	Date1904() bool
	SetResult(c Cell, v Value) // a formula's new value; v may be Array for an array formula, the Book spreads it
}

// ColName gives the letters of a zero-based column.
func ColName(c int) string {
	if c < 0 {
		return ""
	}
	var b [8]byte
	i := len(b)
	for c >= 0 {
		i--
		b[i] = byte('A' + c%26)
		c = c/26 - 1
	}
	return string(b[i:])
}

// ParseCol reads column letters, case-insensitively, up to XFD.
func ParseCol(s string) (int, bool) {
	if s == "" || len(s) > 3 {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		ch := s[i] | 0x20
		if ch < 'a' || ch > 'z' {
			return 0, false
		}
		n = n*26 + int(ch-'a') + 1
	}
	if n > MaxCols {
		return 0, false
	}
	return n - 1, true
}

// CellName gives the A1 name of a zero-based cell.
func CellName(row, col int) string { return ColName(col) + strconv.Itoa(row+1) }

// ParseCell reads an A1 cell name, with or without $ signs.
func ParseCell(s string) (row, col int, ok bool) {
	p, n, ok := scanPart(s, 0)
	if !ok || n != len(s) || p.kind != partCell {
		return 0, 0, false
	}
	return int(p.row), int(p.col), true
}

// ParseArea reads "A1", "A1:C3", "A:A" or "1:3", with or without $ signs. Sheet is left 0 and the area is normalised.
func ParseArea(s string) (a Area, ok bool) {
	p, n, ok := scanPart(s, 0)
	if !ok {
		return Area{}, false
	}
	if n == len(s) {
		if p.kind != partCell {
			return Area{}, false
		}
		return Area{R1: p.row, C1: p.col, R2: p.row, C2: p.col}, true
	}
	if s[n] != ':' {
		return Area{}, false
	}
	q, m, ok := scanPart(s, n+1)
	if !ok || m != len(s) || q.kind != p.kind {
		return Area{}, false
	}
	switch p.kind {
	case partCell:
		a = Area{R1: p.row, C1: p.col, R2: q.row, C2: q.col}
	case partCol:
		a = Area{R1: 0, C1: p.col, R2: MaxRows - 1, C2: q.col}
	case partRow:
		a = Area{R1: p.row, C1: 0, R2: q.row, C2: MaxCols - 1}
	}
	return normArea(a), true
}

func normArea(a Area) Area {
	if a.R1 > a.R2 {
		a.R1, a.R2 = a.R2, a.R1
	}
	if a.C1 > a.C2 {
		a.C1, a.C2 = a.C2, a.C1
	}
	return a
}

// AreaName gives "A1:C3", "A1" for a single cell, "A:B" for whole columns and "1:3" for whole rows; it ignores Sheet.
func AreaName(a Area) string {
	whole := a.R1 == 0 && a.R2 == MaxRows-1
	wholeRow := a.C1 == 0 && a.C2 == MaxCols-1
	switch {
	case whole && !wholeRow:
		return ColName(int(a.C1)) + ":" + ColName(int(a.C2))
	case wholeRow && !whole:
		return strconv.Itoa(int(a.R1)+1) + ":" + strconv.Itoa(int(a.R2)+1)
	case a.R1 == a.R2 && a.C1 == a.C2:
		return CellName(int(a.R1), int(a.C1))
	}
	return CellName(int(a.R1), int(a.C1)) + ":" + CellName(int(a.R2), int(a.C2))
}

// partKind says what scanPart found.
type partKind uint8

const (
	partCell partKind = iota
	partCol
	partRow
)

// part is one end of a reference.
type part struct {
	kind       partKind
	row, col   int32
	absR, absC bool
}

// scanPart reads one end of a reference at i: $A$1, A, $A, 1 or $1.
func scanPart(s string, i int) (p part, next int, ok bool) {
	j := i
	dollar := j < len(s) && s[j] == '$'
	if dollar {
		j++
	}
	k := j
	for k < len(s) && (s[k]|0x20) >= 'a' && (s[k]|0x20) <= 'z' {
		k++
	}
	if k > j {
		col, good := ParseCol(s[j:k])
		if !good {
			return p, 0, false
		}
		p.col, p.absC = int32(col), dollar
		m := k
		absR := m < len(s) && s[m] == '$'
		if absR {
			m++
		}
		e := m
		for e < len(s) && s[e] >= '0' && s[e] <= '9' {
			e++
		}
		if e == m {
			if absR {
				return p, 0, false
			}
			p.kind = partCol
			return p, k, true
		}
		row, good := scanRow(s[m:e])
		if !good {
			return p, 0, false
		}
		p.kind, p.row, p.absR = partCell, row, absR
		return p, e, true
	}
	for k < len(s) && s[k] >= '0' && s[k] <= '9' {
		k++
	}
	if k == j {
		return p, 0, false
	}
	row, good := scanRow(s[j:k])
	if !good {
		return p, 0, false
	}
	p.kind, p.row, p.absR = partRow, row, dollar
	return p, k, true
}

func scanRow(s string) (int32, bool) {
	if len(s) > 7 {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	if n < 1 || n > MaxRows {
		return 0, false
	}
	return int32(n - 1), true
}

// foldCompare orders two texts the way Excel does, ignoring case.
func foldCompare(a, b string) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		x, y := a[i], b[i]
		if x >= 0x80 || y >= 0x80 {
			return strings.Compare(strings.ToLower(a), strings.ToLower(b))
		}
		if x >= 'A' && x <= 'Z' {
			x += 32
		}
		if y >= 'A' && y <= 'Z' {
			y += 32
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}
