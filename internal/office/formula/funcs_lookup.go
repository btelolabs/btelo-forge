package formula

import (
	"math"
	"strconv"
	"strings"
)

// minIndexed is the length from which a column gets a hash index rather than a scan.
const minIndexed = 32

// vec is a one-dimensional view of a range or array for the lookup functions.
type vec struct {
	c    *ctx
	area Area
	grid *Grid
	base int // offset in a grid
	step int // between items in a grid
	n    int
	down bool // a reference that runs down one column
}

// vecOf makes a vector from a row or column of a range or array.
func (c *ctx) vecOf(v Value) (vec, Value, bool) {
	switch v.Kind {
	case Ref:
		if isUnion(v) {
			return vec{}, Err(ErrValue), false
		}
		c.settle(v)
		a := c.clip(v.Area)
		if a.R1 != a.R2 && a.C1 != a.C2 {
			return vec{}, Err(ErrNA), false
		}
		if a.C1 == a.C2 {
			return vec{c: c, area: a, n: int(a.R2-a.R1) + 1, down: true}, Value{}, true
		}
		return vec{c: c, area: a, n: int(a.C2-a.C1) + 1}, Value{}, true
	case Array:
		g := v.Arr
		if g.Rows != 1 && g.Cols != 1 {
			return vec{}, Err(ErrNA), false
		}
		if g.Cols == 1 {
			return vec{c: c, grid: g, step: 1, n: g.Rows}, Value{}, true
		}
		return vec{c: c, grid: g, step: 1, n: g.Cols}, Value{}, true
	case Error:
		return vec{}, v, false
	}
	return vec{c: c, grid: &Grid{Rows: 1, Cols: 1, V: []Value{v}}, step: 1, n: 1}, Value{}, true
}

func (v *vec) at(i int) Value {
	if v.grid != nil {
		return v.grid.V[v.base+i*v.step]
	}
	if v.down {
		return v.c.b.Value(int(v.area.Sheet), int(v.area.R1)+i, int(v.area.C1))
	}
	return v.c.b.Value(int(v.area.Sheet), int(v.area.R1), int(v.area.C1)+i)
}

func sameKind(a, b Value) bool {
	if a.Kind == Blank || b.Kind == Blank {
		return false
	}
	return a.Kind == b.Kind
}

// lookupKey normalises the value looked up.
func lookupKey(v Value) Value {
	if v.Kind == Blank {
		return Num(0)
	}
	return v
}

// approx is Excel's binary search: it lands on the last item not past the value in sorted data, skipping items of
// another type. desc searches data sorted high to low.
func (v *vec) approx(want Value, desc bool) int {
	lo, hi, best := 0, v.n-1, -1
	for lo <= hi {
		mid := int(uint(lo+hi) >> 1)
		m := mid
		for m >= lo && !sameKind(v.at(m), want) {
			m--
		}
		if m < lo {
			lo = mid + 1
			continue
		}
		r := compareValues(v.at(m), want)
		if desc {
			r = -r
		}
		if r <= 0 {
			best = m
			lo = mid + 1
		} else {
			hi = m - 1
		}
	}
	return best
}

// exact finds the first item equal to want, ignoring case in text; wild lets * ? ~ in text match.
func (v *vec) exact(want Value, wild bool) int {
	if want.Kind == Text && wild && hasWild(want.Str) {
		pat := strings.ToLower(want.Str)
		return v.scan(func(x Value) bool { return x.Kind == Text && wildMatch(pat, strings.ToLower(x.Str)) })
	}
	if e := v.c.e; e != nil && v.grid == nil && v.down && v.n >= minIndexed {
		return e.findExact(v.c, v.area, want)
	}
	if want.Kind == Text {
		return v.scan(func(x Value) bool { return x.Kind == Text && foldCompare(x.Str, want.Str) == 0 })
	}
	return v.scan(func(x Value) bool { return x.Kind == want.Kind && x.Num == want.Num })
}

// scan finds the first item that passes, through the sparse walk when it can.
func (v *vec) scan(test func(Value) bool) int {
	if v.grid != nil {
		for i := range v.n {
			if test(v.at(i)) {
				return i
			}
		}
		return -1
	}
	found := -1
	a := v.area
	v.c.b.Each(int(a.Sheet), int(a.R1), int(a.C1), int(a.R2), int(a.C2), func(r, col int, x Value) bool {
		if test(x) {
			found = r - int(a.R1) + col - int(a.C1)
			return false
		}
		return true
	})
	return found
}

// tableView is a rectangle of a range or array with one way to read a cell.
type tableView struct {
	area Area
	grid *Grid
	rows int
	cols int
}

func (c *ctx) tableOf(v Value) (tableView, Value, bool) {
	switch v.Kind {
	case Ref:
		if isUnion(v) {
			return tableView{}, Err(ErrValue), false
		}
		c.settle(v)
		a := c.clip(v.Area)
		return tableView{area: a, rows: int(a.R2-a.R1) + 1, cols: int(a.C2-a.C1) + 1}, Value{}, true
	case Array:
		return tableView{grid: v.Arr, rows: v.Arr.Rows, cols: v.Arr.Cols}, Value{}, true
	case Error:
		return tableView{}, v, false
	}
	return tableView{grid: &Grid{Rows: 1, Cols: 1, V: []Value{v}}, rows: 1, cols: 1}, Value{}, true
}

func (t *tableView) at(c *ctx, row, col int) Value {
	if t.grid != nil {
		return t.grid.V[row*t.grid.Cols+col]
	}
	return c.b.Value(int(t.area.Sheet), int(t.area.R1)+row, int(t.area.C1)+col)
}

// slice gives one column (byCol) or row of the table as a vector.
func (t *tableView) slice(c *ctx, i int, byCol bool) vec {
	if t.grid != nil {
		if byCol {
			return vec{c: c, grid: t.grid, base: i, step: t.grid.Cols, n: t.rows}
		}
		return vec{c: c, grid: t.grid, base: i * t.grid.Cols, step: 1, n: t.cols}
	}
	a := t.area
	if byCol {
		col := a.C1 + int32(i)
		return vec{c: c, area: Area{Sheet: a.Sheet, R1: a.R1, R2: a.R2, C1: col, C2: col}, n: t.rows, down: true}
	}
	row := a.R1 + int32(i)
	return vec{c: c, area: Area{Sheet: a.Sheet, R1: row, R2: row, C1: a.C1, C2: a.C2}, n: t.cols}
}

// fnVHLookup makes VLOOKUP and HLOOKUP.
func fnVHLookup(vertical bool) func(c *ctx, a []Value) Value {
	return func(c *ctx, a []Value) Value {
		return c.mapValue(c.operand(a[0]), func(key Value) Value {
			if key.Kind == Error {
				return key
			}
			key = lookupKey(key)
			idx, bad, ok := c.num(c.operand(a[2]))
			if !ok {
				return bad
			}
			if idx < 1 {
				return Err(ErrValue)
			}
			approx := true
			if len(a) > 3 {
				if a[3].Kind != Blank {
					b, bad, ok := c.logic(c.operand(a[3]))
					if !ok {
						return bad
					}
					approx = b
				} else {
					approx = false
				}
			}
			t, bad, ok := c.tableOf(a[1])
			if !ok {
				return bad
			}
			// Before clipping the width: asking past the table is #REF!, past the used part just reads blanks.
			width := t.cols
			if vertical && a[1].Kind == Ref {
				width = int(a[1].Area.C2-a[1].Area.C1) + 1
			} else if !vertical {
				width = t.rows
				if a[1].Kind == Ref {
					width = int(a[1].Area.R2-a[1].Area.R1) + 1
				}
			}
			if idx >= float64(width)+1 {
				return Err(ErrRef)
			}
			n := int(idx) - 1
			head := t.slice(c, 0, vertical)
			var pos int
			if approx {
				pos = head.approx(key, false)
			} else {
				pos = head.exact(key, true)
			}
			if pos < 0 {
				return Err(ErrNA)
			}
			if vertical {
				if n >= t.cols {
					return Value{}
				}
				return t.at(c, pos, n)
			}
			if n >= t.rows {
				return Value{}
			}
			return t.at(c, n, pos)
		})
	}
}

func fnLookup(c *ctx, a []Value) Value {
	return c.mapValue(c.operand(a[0]), func(key Value) Value {
		if key.Kind == Error {
			return key
		}
		key = lookupKey(key)
		t, bad, ok := c.tableOf(a[1])
		if !ok {
			return bad
		}
		byCol := t.rows >= t.cols
		head := t.slice(c, 0, byCol)
		pos := head.approx(key, false)
		if pos < 0 {
			return Err(ErrNA)
		}
		if len(a) > 2 {
			res, bad, ok := c.vecOf(a[2])
			if !ok {
				return bad
			}
			if pos >= res.n {
				return Err(ErrNA)
			}
			return res.at(pos)
		}
		if byCol {
			return t.at(c, pos, t.cols-1)
		}
		return t.at(c, t.rows-1, pos)
	})
}

func fnMatch(c *ctx, a []Value) Value {
	mode := 1.0
	if len(a) > 2 && a[2].Kind != Blank {
		var bad Value
		var ok bool
		if mode, bad, ok = c.num(c.operand(a[2])); !ok {
			return bad
		}
	} else if len(a) > 2 {
		mode = 0
	}
	return c.mapValue(c.operand(a[0]), func(key Value) Value {
		if key.Kind == Error {
			return key
		}
		key = lookupKey(key)
		v, bad, ok := c.vecOf(a[1])
		if !ok {
			return bad
		}
		var pos int
		switch {
		case mode == 0:
			pos = v.exact(key, true)
		case mode > 0:
			pos = v.approx(key, false)
		default:
			pos = v.approx(key, true)
		}
		if pos < 0 {
			return Err(ErrNA)
		}
		return Num(float64(pos + 1))
	})
}

// nearest finds the exact match, or the closest item below (dir < 0) or above (dir > 0), in search order.
func (v *vec) nearest(want Value, dir int, reverse bool) int {
	best := -1
	for i := range v.n {
		j := i
		if reverse {
			j = v.n - 1 - i
		}
		x := v.at(j)
		if !sameKind(x, want) {
			continue
		}
		r := compareValues(x, want)
		if r == 0 {
			return j
		}
		if (dir < 0 && r < 0) || (dir > 0 && r > 0) {
			if best < 0 || compareValues(x, v.at(best))*dir < 0 {
				best = j
			}
		}
	}
	return best
}

// reverseExact finds the last match.
func (v *vec) reverseExact(want Value, wild bool) int {
	pat := ""
	if wild && want.Kind == Text {
		pat = strings.ToLower(want.Str)
	}
	for i := v.n - 1; i >= 0; i-- {
		x := v.at(i)
		if pat != "" {
			if x.Kind == Text && wildMatch(pat, strings.ToLower(x.Str)) {
				return i
			}
			continue
		}
		if sameKind(x, want) && compareValues(x, want) == 0 {
			return i
		}
	}
	return -1
}

// binaryExact is the binary search of XLOOKUP: an exact hit, or the neighbour the match mode asks for.
func (v *vec) binaryExact(want Value, dir int, desc bool) int {
	pos := v.approx(want, desc)
	if pos >= 0 && sameKind(v.at(pos), want) && compareValues(v.at(pos), want) == 0 {
		return pos
	}
	switch {
	case dir == 0:
		return -1
	case (dir < 0) != desc:
		return pos
	}
	if pos+1 < v.n {
		return pos + 1
	}
	return -1
}

func fnXLookup(c *ctx, a []Value) Value {
	key := c.operand(a[0])
	matchMode, searchMode := 0.0, 1.0
	if len(a) > 4 {
		if bad, ok := c.nums([]Value{c.operand(a[4])}, &matchMode); !ok {
			return bad
		}
	}
	if len(a) > 5 {
		if bad, ok := c.nums([]Value{c.operand(a[5])}, &searchMode); !ok {
			return bad
		}
	}
	mm, sm := int(matchMode), int(searchMode)
	if mm < -1 || mm > 2 || (sm != 1 && sm != -1 && sm != 2 && sm != -2) {
		return Err(ErrValue)
	}
	return c.mapValue(key, func(k Value) Value {
		if k.Kind == Error {
			return k
		}
		k = lookupKey(k)
		v, bad, ok := c.vecOf(a[1])
		if !ok {
			return bad
		}
		var pos int
		switch {
		case sm == 2 || sm == -2:
			dir := mm
			if mm == 2 {
				dir = 0
			}
			pos = v.binaryExact(k, dir, sm == -2)
		case mm == 2:
			if sm == 1 {
				pos = v.exact(k, true)
			} else {
				pos = v.reverseExact(k, true)
			}
		case mm == 0:
			if sm == 1 {
				pos = v.exact(k, false)
			} else {
				pos = v.reverseExact(k, false)
			}
		default:
			pos = v.nearest(k, mm, sm == -1)
		}
		if pos < 0 {
			if len(a) > 3 && a[3].Kind != Blank {
				x := c.operand(a[3])
				if x.Kind == Array {
					return x.Arr.V[0]
				}
				return x
			}
			return Err(ErrNA)
		}
		return c.takeFrom(a[2], pos, v.grid == nil && v.down || (v.grid != nil && v.grid.Cols == 1))
	})
}

// takeFrom picks item pos along the lookup direction; a table gives the whole row or column, as an array.
func (c *ctx) takeFrom(r Value, pos int, byRow bool) Value {
	t, bad, ok := c.tableOf(r)
	if !ok {
		return bad
	}
	if byRow {
		if pos >= t.rows {
			if r.Kind == Ref {
				return Value{}
			}
			return Err(ErrNA)
		}
		if t.cols == 1 {
			return t.at(c, pos, 0)
		}
		out := make([]Value, t.cols)
		for j := range t.cols {
			out[j] = t.at(c, pos, j)
		}
		return Value{Kind: Array, Arr: &Grid{Rows: 1, Cols: t.cols, V: out}}
	}
	if pos >= t.cols {
		if r.Kind == Ref {
			return Value{}
		}
		return Err(ErrNA)
	}
	if t.rows == 1 {
		return t.at(c, 0, pos)
	}
	out := make([]Value, t.rows)
	for i := range t.rows {
		out[i] = t.at(c, i, pos)
	}
	return Value{Kind: Array, Arr: &Grid{Rows: t.rows, Cols: 1, V: out}}
}

func fnIndex(c *ctx, a []Value) Value {
	v := a[0]
	if v.Kind == Error {
		return v
	}
	if isUnion(v) {
		n := 1.0
		if len(a) > 3 {
			if bad, ok := c.nums([]Value{c.operand(a[3])}, &n); !ok {
				return bad
			}
		}
		if n < 1 || int(n) > len(v.Arr.V) {
			return Err(ErrRef)
		}
		v = v.Arr.V[int(n)-1]
	}
	if v.Kind != Ref && v.Kind != Array {
		v = Value{Kind: Array, Arr: &Grid{Rows: 1, Cols: 1, V: []Value{v}}}
	}
	rowArg := c.operand(a[1])
	colArg := Value{}
	if len(a) > 2 {
		colArg = c.operand(a[2])
	}
	if rowArg.Kind == Array || colArg.Kind == Array {
		return broadcast([]Value{rowArg, colArg}, func(x []Value) Value {
			r := c.indexAt(v, x[0], x[1], len(a) > 2)
			switch r.Kind {
			case Ref:
				return c.intersect(r)
			case Array:
				return r.Arr.V[0]
			}
			return r
		})
	}
	return c.indexAt(v, rowArg, colArg, len(a) > 2)
}

// indexAt resolves one INDEX. A reference stays a reference, so INDEX can stand on either side of a colon.
func (c *ctx) indexAt(v, rowArg, colArg Value, hasCol bool) Value {
	row, bad, ok := c.num(rowArg)
	if !ok {
		return bad
	}
	col, bad, ok := c.num(colArg)
	if !ok {
		return bad
	}
	r, k := int(row), int(col)
	if r < 0 || k < 0 {
		return Err(ErrValue)
	}
	rows, cols := 0, 0
	if v.Kind == Ref {
		rows, cols = int(v.Area.R2-v.Area.R1)+1, int(v.Area.C2-v.Area.C1)+1
	} else {
		rows, cols = v.Arr.Rows, v.Arr.Cols
	}
	if !hasCol && rows == 1 && cols > 1 {
		r, k = 1, r
		if k == 0 {
			r = 0
		}
	} else if !hasCol && cols == 1 {
		k = 1
	}
	if r > rows || k > cols {
		return Err(ErrRef)
	}
	if v.Kind == Ref {
		a := v.Area
		if r > 0 {
			a.R1 += int32(r - 1)
			a.R2 = a.R1
		}
		if k > 0 {
			a.C1 += int32(k - 1)
			a.C2 = a.C1
		}
		return Value{Kind: Ref, Area: a, Num: v.Num}
	}
	g := v.Arr
	if r > 0 && k > 0 {
		return g.V[(r-1)*g.Cols+k-1]
	}
	if r == 0 && k == 0 {
		return v
	}
	if k == 0 {
		out := make([]Value, g.Cols)
		copy(out, g.V[(r-1)*g.Cols:r*g.Cols])
		return Value{Kind: Array, Arr: &Grid{Rows: 1, Cols: g.Cols, V: out}}
	}
	out := make([]Value, g.Rows)
	for i := range g.Rows {
		out[i] = g.V[i*g.Cols+k-1]
	}
	return Value{Kind: Array, Arr: &Grid{Rows: g.Rows, Cols: 1, V: out}}
}

func fnOffset(c *ctx, a []Value) Value {
	v := a[0]
	if v.Kind == Error {
		return v
	}
	if v.Kind != Ref || isUnion(v) {
		return Err(ErrValue)
	}
	dr, bad, ok := c.num(c.operand(a[1]))
	if !ok {
		return bad
	}
	dc, bad, ok := c.num(c.operand(a[2]))
	if !ok {
		return bad
	}
	area := v.Area
	h, w := float64(area.R2-area.R1)+1, float64(area.C2-area.C1)+1
	if len(a) > 3 && a[3].Kind != Blank {
		if h, bad, ok = c.num(c.operand(a[3])); !ok {
			return bad
		}
	}
	if len(a) > 4 && a[4].Kind != Blank {
		if w, bad, ok = c.num(c.operand(a[4])); !ok {
			return bad
		}
	}
	h, w = math.Trunc(h), math.Trunc(w)
	if h == 0 || w == 0 {
		return Err(ErrRef)
	}
	r1 := float64(area.R1) + math.Trunc(dr)
	c1 := float64(area.C1) + math.Trunc(dc)
	r2, c2 := r1+h-1, c1+w-1
	if h < 0 {
		r1, r2 = r1+h+1, r1
	}
	if w < 0 {
		c1, c2 = c1+w+1, c1
	}
	if r1 < 0 || c1 < 0 || r2 >= MaxRows || c2 >= MaxCols {
		return Err(ErrRef)
	}
	return Value{Kind: Ref, Num: dynamicRef,
		Area: Area{Sheet: area.Sheet, R1: int32(r1), C1: int32(c1), R2: int32(r2), C2: int32(c2)}}
}

// r1c1Ref reads a reference in R1C1 style, as INDIRECT's second argument asks.
func (c *ctx) r1c1Ref(s string) (Area, bool) {
	sheet := c.sheet
	if i := strings.LastIndexByte(s, '!'); i >= 0 {
		name := s[:i]
		if len(name) >= 2 && name[0] == '\'' && name[len(name)-1] == '\'' {
			name = strings.ReplaceAll(name[1:len(name)-1], "''", "'")
		}
		if sheet = c.b.SheetIndex(name); sheet < 0 {
			return Area{}, false
		}
		s = s[i+1:]
	}
	one := func(t string) (row, col int32, ok bool) {
		axis := func(letter byte, base, size int) (int32, bool) {
			if t == "" || (t[0]|0x20) != letter {
				return 0, false
			}
			t = t[1:]
			v := base
			if strings.HasPrefix(t, "[") {
				end := strings.IndexByte(t, ']')
				if end < 0 {
					return 0, false
				}
				d, err := strconv.Atoi(t[1:end])
				if err != nil {
					return 0, false
				}
				v += d
				t = t[end+1:]
			} else {
				j := 0
				for j < len(t) && t[j] >= '0' && t[j] <= '9' {
					j++
				}
				if j > 0 {
					d, err := strconv.Atoi(t[:j])
					if err != nil {
						return 0, false
					}
					v = d - 1
					t = t[j:]
				}
			}
			if v < 0 || v >= size {
				return 0, false
			}
			return int32(v), true
		}
		r, ok := axis('r', c.row, MaxRows)
		if !ok {
			return 0, 0, false
		}
		k, ok := axis('c', c.col, MaxCols)
		if !ok || t != "" {
			return 0, 0, false
		}
		return r, k, true
	}
	first, second, pair := strings.Cut(s, ":")
	r1, c1, ok := one(first)
	if !ok {
		return Area{}, false
	}
	r2, c2 := r1, c1
	if pair {
		if r2, c2, ok = one(second); !ok {
			return Area{}, false
		}
	}
	return normArea(Area{Sheet: int32(sheet), R1: r1, C1: c1, R2: r2, C2: c2}), true
}

func fnIndirect(c *ctx, a []Value) Value {
	text, bad, ok := c.text(c.operand(a[0]))
	if !ok {
		return bad
	}
	a1 := true
	if len(a) > 1 && a[1].Kind != Blank {
		if a1, bad, ok = c.logic(c.operand(a[1])); !ok {
			return bad
		}
	}
	text = strings.TrimSpace(text)
	if !a1 {
		ar, good := c.r1c1Ref(text)
		if !good {
			return Err(ErrRef)
		}
		return Value{Kind: Ref, Area: ar, Num: dynamicRef}
	}
	x := c.parse(text)
	if x == nil || (x.root.kind != nRef && x.root.kind != nName && x.root.kind != nRange) {
		return Err(ErrRef)
	}
	cc := *c
	cc.dr, cc.dc, cc.wrap = 0, 0, false
	v := cc.eval(x.root)
	if v.Kind != Ref {
		return Err(ErrRef)
	}
	if isUnion(v) {
		for i := range v.Arr.V {
			v.Arr.V[i].Num = dynamicRef
		}
		return v
	}
	v.Num = dynamicRef
	return v
}

// rowOrCol makes ROW and COLUMN.
func rowOrCol(isRow bool) func(c *ctx, a []Value) Value {
	return func(c *ctx, a []Value) Value {
		if len(a) == 0 {
			if isRow {
				return Num(float64(c.row + 1))
			}
			return Num(float64(c.col + 1))
		}
		v := a[0]
		if v.Kind == Error {
			return v
		}
		if v.Kind != Ref || isUnion(v) {
			return Err(ErrValue)
		}
		ar := v.Area
		lo, hi := ar.R1, ar.R2
		if !isRow {
			lo, hi = ar.C1, ar.C2
		}
		if lo == hi || !c.array {
			return Num(float64(lo + 1))
		}
		ar = c.clip(ar)
		if isRow {
			hi = ar.R2
		} else {
			hi = ar.C2
		}
		out := make([]Value, 0, hi-lo+1)
		for i := lo; i <= hi; i++ {
			out = append(out, Num(float64(i+1)))
		}
		if isRow {
			return Value{Kind: Array, Arr: &Grid{Rows: len(out), Cols: 1, V: out}}
		}
		return Value{Kind: Array, Arr: &Grid{Rows: 1, Cols: len(out), V: out}}
	}
}

// sizeOf makes ROWS and COLUMNS.
func sizeOf(rows bool) func(c *ctx, a []Value) Value {
	return func(_ *ctx, a []Value) Value {
		v := a[0]
		switch v.Kind {
		case Error:
			return v
		case Ref:
			if isUnion(v) {
				return Err(ErrRef)
			}
			if rows {
				return Num(float64(v.Area.R2-v.Area.R1) + 1)
			}
			return Num(float64(v.Area.C2-v.Area.C1) + 1)
		case Array:
			if rows {
				return Num(float64(v.Arr.Rows))
			}
			return Num(float64(v.Arr.Cols))
		}
		return Num(1)
	}
}

func registerLookup() {
	ranged("VLOOKUP", 3, 4, fnVHLookup(true))
	ranged("HLOOKUP", 3, 4, fnVHLookup(false))
	ranged("LOOKUP", 2, 3, fnLookup)
	ranged("XLOOKUP", 3, 6, fnXLookup)
	ranged("MATCH", 2, 3, fnMatch)
	ranged("INDEX", 2, 4, fnIndex)
	ranged("OFFSET", 3, 5, fnOffset).volatile = true
	ranged("INDIRECT", 1, 2, fnIndirect).volatile = true
	ranged("ROW", 0, 1, rowOrCol(true))
	ranged("COLUMN", 0, 1, rowOrCol(false))
	ranged("ROWS", 1, 1, sizeOf(true))
	ranged("COLUMNS", 1, 1, sizeOf(false))
}
