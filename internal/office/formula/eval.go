package formula

import (
	"math"
	"strings"
)

// maxNames bounds the depth of defined names that refer to each other.
const maxNames = 32

// dynamicRef in Value.Num marks a reference made while evaluating (OFFSET, INDIRECT), which the dependency graph
// could not see: reading it has to settle any dirty cell inside it first.
const dynamicRef = 1

// ctx is the place a formula is being evaluated at.
type ctx struct {
	b        Book
	e        *Engine // nil outside the engine
	sheet    int
	row, col int
	dr, dc   int
	array    bool
	wrap     bool // relative references wrap round the sheet: this is a defined name
	names    int
	depth    int // on-demand evaluations in progress
}

// Eval computes the formula placed at (sheet,row,col); dr, dc are how far that cell is from where the formula was
// written. array true evaluates as an array formula: ranges and arrays broadcast through operators and scalar
// functions, and the result may be Array. array false applies implicit intersection to a multi-cell range result and
// takes the top-left of an array.
func (x *Expr) Eval(b Book, sheet, row, col, dr, dc int, array bool) Value {
	c := ctx{b: b, sheet: sheet, row: row, col: col, dr: dr, dc: dc, array: array}
	return c.top(x)
}

func (c *ctx) top(x *Expr) Value {
	if !x.known {
		return Err(ErrName)
	}
	return c.finish(c.eval(x.root))
}

// finish turns what evaluation produced into what a cell holds.
func (c *ctx) finish(v Value) Value {
	if v.Kind == Ref {
		if c.array {
			v = c.gridValue(v)
		} else {
			v = c.intersect(v)
		}
	}
	if v.Kind == Array {
		g := v.Arr
		if g == nil || len(g.V) == 0 {
			return Num(0)
		}
		if !c.array || len(g.V) == 1 {
			v = g.V[0]
		} else {
			out := make([]Value, len(g.V))
			for i, x := range g.V {
				if x.Kind == Blank {
					x = Num(0)
				}
				out[i] = x
			}
			return Value{Kind: Array, Arr: &Grid{Rows: g.Rows, Cols: g.Cols, V: out}}
		}
	}
	if v.Kind == Blank {
		return Num(0)
	}
	return v
}

func isUnion(v Value) bool { return v.Kind == Ref && v.Arr != nil }

// settle evaluates any dirty formula inside a dynamic reference.
func (c *ctx) settle(v Value) {
	if v.Num == dynamicRef && c.e != nil {
		c.e.settle(v.Area, c.depth)
	}
}

// eval computes a node. The result may be a Ref, which stays lazy until something needs its cells.
func (c *ctx) eval(n *node) Value {
	switch n.kind {
	case nConst:
		return n.val
	case nMissing:
		return Value{}
	case nRef:
		return c.refValue(n.r)
	case nName:
		return c.nameValue(n.r)
	case nFunc:
		return c.call(n)
	case nUnary:
		v := c.eval(n.kids[0])
		switch n.op {
		case '+':
			return v
		case '@':
			if v.Kind == Ref {
				return c.intersect(v)
			}
			if v.Kind == Array {
				return v.Arr.V[0]
			}
			return v
		}
		return c.mapValue(c.operand(v), func(x Value) Value {
			f, bad, ok := c.num(x)
			if !ok {
				return bad
			}
			return Num(-f)
		})
	case nPercent:
		return c.mapValue(c.operand(c.eval(n.kids[0])), func(x Value) Value {
			f, bad, ok := c.num(x)
			if !ok {
				return bad
			}
			return Num(f / 100)
		})
	case nBinary:
		return c.binary(n)
	case nRange:
		return c.rangeOp(c.eval(n.kids[0]), c.eval(n.kids[1]))
	case nIntersect:
		return c.intersectOp(c.eval(n.kids[0]), c.eval(n.kids[1]))
	case nUnion:
		return c.unionOp(n)
	}
	return Err(ErrName)
}

// binary folds a left-leaning chain of operators in a loop, so a long sum does not recurse.
func (c *ctx) binary(n *node) Value {
	var buf [16]*node
	spine := buf[:0]
	for n.kind == nBinary {
		spine = append(spine, n)
		n = n.kids[0]
	}
	v := c.eval(n)
	for i := len(spine) - 1; i >= 0; i-- {
		right := c.eval(spine[i].kids[1])
		v = c.apply(spine[i].op, c.operand(v), c.operand(right))
	}
	return v
}

// operand makes a value ready for an operator or a scalar function.
func (c *ctx) operand(v Value) Value {
	if v.Kind != Ref {
		return v
	}
	if c.array {
		return c.gridValue(v)
	}
	return c.intersect(v)
}

func (c *ctx) apply(op byte, a, b Value) Value {
	if a.Kind != Array && b.Kind != Array {
		return c.scalarOp(op, a, b)
	}
	if a.Kind == Error && b.Kind != Array {
		return a
	}
	return broadcast([]Value{a, b}, func(x []Value) Value { return c.scalarOp(op, x[0], x[1]) })
}

// broadcast applies fn over arrays: a one-row or one-column array stretches, and past the end of a shorter one
// the result is #N/A.
func broadcast(args []Value, fn func(x []Value) Value) Value {
	rows, cols := 1, 1
	for _, a := range args {
		if a.Kind == Array {
			rows = max(rows, a.Arr.Rows)
			cols = max(cols, a.Arr.Cols)
		}
	}
	if rows*cols > maxCells {
		return Err(ErrNum)
	}
	out := make([]Value, 0, rows*cols)
	tmp := make([]Value, len(args))
	for i := range rows {
		for j := range cols {
			missing := false
			for k, a := range args {
				if a.Kind != Array {
					tmp[k] = a
					continue
				}
				g := a.Arr
				r, c := i, j
				if g.Rows == 1 {
					r = 0
				}
				if g.Cols == 1 {
					c = 0
				}
				if r >= g.Rows || c >= g.Cols {
					missing = true
					break
				}
				tmp[k] = g.V[r*g.Cols+c]
			}
			if missing {
				out = append(out, Err(ErrNA))
			} else {
				out = append(out, fn(tmp))
			}
		}
	}
	return Value{Kind: Array, Arr: &Grid{Rows: rows, Cols: cols, V: out}}
}

// mapValue applies fn to a scalar, or to each item of an array.
func (c *ctx) mapValue(v Value, fn func(Value) Value) Value {
	if v.Kind != Array {
		return fn(v)
	}
	out := make([]Value, len(v.Arr.V))
	for i, x := range v.Arr.V {
		out[i] = fn(x)
	}
	return Value{Kind: Array, Arr: &Grid{Rows: v.Arr.Rows, Cols: v.Arr.Cols, V: out}}
}

func (c *ctx) date1904() bool { return c.b.Date1904() }

// num coerces a scalar to a number the way an operator does. ok is false with the error to return in bad.
func (c *ctx) num(v Value) (f float64, bad Value, ok bool) {
	switch v.Kind {
	case Number, Bool:
		return v.Num, Value{}, true
	case Blank:
		return 0, Value{}, true
	case Text:
		if n, good := textToNumber(v.Str, c.date1904()); good {
			return n, Value{}, true
		}
		return 0, Err(ErrValue), false
	case Error:
		return 0, v, false
	}
	return 0, Err(ErrValue), false
}

// text coerces a scalar to text.
func (c *ctx) text(v Value) (string, Value, bool) {
	switch v.Kind {
	case Text:
		return v.Str, Value{}, true
	case Number:
		return numToText(v.Num), Value{}, true
	case Bool:
		if v.Num != 0 {
			return "TRUE", Value{}, true
		}
		return "FALSE", Value{}, true
	case Blank:
		return "", Value{}, true
	case Error:
		return "", v, false
	}
	return "", Err(ErrValue), false
}

// logic coerces a scalar to a truth value.
func (c *ctx) logic(v Value) (bool, Value, bool) {
	switch v.Kind {
	case Number, Bool:
		return v.Num != 0, Value{}, true
	case Blank:
		return false, Value{}, true
	case Text:
		if strings.EqualFold(v.Str, "TRUE") {
			return true, Value{}, true
		}
		if strings.EqualFold(v.Str, "FALSE") {
			return false, Value{}, true
		}
		return false, Err(ErrValue), false
	case Error:
		return false, v, false
	}
	return false, Err(ErrValue), false
}

func numResult(f float64) Value {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return Err(ErrNum)
	}
	return Num(f)
}

func (c *ctx) scalarOp(op byte, a, b Value) Value {
	switch op {
	case '&':
		x, bad, ok := c.text(a)
		if !ok {
			return bad
		}
		y, bad, ok := c.text(b)
		if !ok {
			return bad
		}
		if len(x)+len(y) > maxText*4 || textLen(x)+textLen(y) > maxText {
			return Err(ErrValue)
		}
		return Str(x + y)
	case '=', '<', '>', opLE, opGE, opNE:
		if a.Kind == Error {
			return a
		}
		if b.Kind == Error {
			return b
		}
		r := compareValues(a, b)
		switch op {
		case '=':
			return BoolOf(r == 0)
		case '<':
			return BoolOf(r < 0)
		case '>':
			return BoolOf(r > 0)
		case opLE:
			return BoolOf(r <= 0)
		case opGE:
			return BoolOf(r >= 0)
		}
		return BoolOf(r != 0)
	}
	x, bad, ok := c.num(a)
	if !ok {
		return bad
	}
	y, bad, ok := c.num(b)
	if !ok {
		return bad
	}
	switch op {
	case '+':
		return numResult(fixZero(x+y, x, y))
	case '-':
		return numResult(fixZero(x-y, x, -y))
	case '*':
		return numResult(x * y)
	case '/':
		if y == 0 {
			return Err(ErrDiv0)
		}
		return numResult(x / y)
	case '^':
		return power(x, y)
	}
	return Err(ErrValue)
}

// fixZero does what Excel does to a sum that cancels to within rounding noise of its operands: it gives exactly 0.
func fixZero(r, a, b float64) float64 {
	if r != 0 && (a < 0) != (b < 0) && math.Abs(r) < 1e-15*math.Abs(a) {
		return 0
	}
	return r
}

func power(x, y float64) Value {
	if x == 0 {
		switch {
		case y == 0:
			return Err(ErrNum)
		case y < 0:
			return Err(ErrDiv0)
		}
		return Num(0)
	}
	return numResult(math.Pow(x, y))
}

// rank orders kinds in comparisons: numbers, then text, then logical values.
func rank(k Kind) int {
	switch k {
	case Text:
		return 1
	case Bool:
		return 2
	}
	return 0
}

// compareValues orders two scalars, neither an error. A blank takes the type of the other side.
func compareValues(a, b Value) int {
	if a.Kind == Blank {
		switch b.Kind {
		case Text:
			a = Str("")
		case Bool:
			a = BoolOf(false)
		default:
			a = Num(0)
		}
	}
	if b.Kind == Blank {
		switch a.Kind {
		case Text:
			b = Str("")
		case Bool:
			b = BoolOf(false)
		default:
			b = Num(0)
		}
	}
	ra, rb := rank(a.Kind), rank(b.Kind)
	if ra != rb {
		if ra < rb {
			return -1
		}
		return 1
	}
	if a.Kind == Text {
		return foldCompare(a.Str, b.Str)
	}
	switch {
	case a.Num < b.Num:
		return -1
	case a.Num > b.Num:
		return 1
	}
	return 0
}

// refValue resolves a reference at this evaluation point. 3-D references give a union.
func (c *ctx) refValue(r *ref) Value {
	if r.bad || r.external {
		return Err(ErrRef)
	}
	r1, c1, r2, c2, ok := r.shift(c.dr, c.dc, c.wrap)
	if !ok {
		return Err(ErrRef)
	}
	a := normArea(Area{R1: r1, C1: c1, R2: r2, C2: c2})
	s := c.sheet
	if r.hasSheet {
		if s = c.b.SheetIndex(r.sheet1); s < 0 {
			return Err(ErrRef)
		}
		if r.sheet2 != "" {
			t := c.b.SheetIndex(r.sheet2)
			if t < 0 {
				return Err(ErrRef)
			}
			if s > t {
				s, t = t, s
			}
			if s != t {
				refs := make([]Value, 0, t-s+1)
				for i := s; i <= t; i++ {
					a.Sheet = int32(i)
					refs = append(refs, Value{Kind: Ref, Area: a})
				}
				return Value{Kind: Ref, Arr: &Grid{Rows: 1, Cols: len(refs), V: refs}}
			}
		}
	}
	a.Sheet = int32(s)
	return Value{Kind: Ref, Area: a}
}

func (c *ctx) parse(text string) *Expr {
	if c.e != nil {
		return c.e.parsed(text)
	}
	x, err := Parse(text)
	if err != nil {
		return nil
	}
	return x
}

func (c *ctx) nameValue(r *ref) Value {
	sheet := c.sheet
	if r.external {
		return Err(ErrRef)
	}
	if r.hasSheet {
		if sheet = c.b.SheetIndex(r.sheet1); sheet < 0 {
			return Err(ErrRef)
		}
	}
	text := strings.TrimPrefix(strings.TrimSpace(c.b.Name(r.name, sheet)), "=")
	if text == "" || c.names >= maxNames {
		return Err(ErrName)
	}
	x := c.parse(text)
	if x == nil || !x.known {
		return Err(ErrName)
	}
	cc := *c
	cc.names++
	cc.sheet = sheet
	cc.dr, cc.dc, cc.wrap = c.row, c.col, true
	return cc.eval(x.root)
}

func (c *ctx) rangeOp(a, b Value) Value {
	if a.Kind == Error {
		return a
	}
	if b.Kind == Error {
		return b
	}
	if a.Kind != Ref || b.Kind != Ref || isUnion(a) || isUnion(b) {
		return Err(ErrValue)
	}
	if a.Area.Sheet != b.Area.Sheet {
		return Err(ErrRef)
	}
	x, y := a.Area, b.Area
	h := Area{Sheet: x.Sheet, R1: min(x.R1, y.R1), C1: min(x.C1, y.C1), R2: max(x.R2, y.R2), C2: max(x.C2, y.C2)}
	return Value{Kind: Ref, Area: h, Num: max(a.Num, b.Num)}
}

func (c *ctx) intersectOp(a, b Value) Value {
	if a.Kind == Error {
		return a
	}
	if b.Kind == Error {
		return b
	}
	if a.Kind != Ref || b.Kind != Ref {
		return Err(ErrValue)
	}
	var hits []Value
	flat := func(v Value) []Value {
		if isUnion(v) {
			return v.Arr.V
		}
		return []Value{v}
	}
	for _, x := range flat(a) {
		for _, y := range flat(b) {
			p, q := x.Area, y.Area
			if p.Sheet != q.Sheet {
				continue
			}
			h := Area{Sheet: p.Sheet, R1: max(p.R1, q.R1), C1: max(p.C1, q.C1), R2: min(p.R2, q.R2), C2: min(p.C2, q.C2)}
			if h.R1 <= h.R2 && h.C1 <= h.C2 {
				hits = append(hits, Value{Kind: Ref, Area: h, Num: max(x.Num, y.Num)})
			}
		}
	}
	switch len(hits) {
	case 0:
		return Err(ErrNull)
	case 1:
		return hits[0]
	}
	return Value{Kind: Ref, Arr: &Grid{Rows: 1, Cols: len(hits), V: hits}}
}

func (c *ctx) unionOp(n *node) Value {
	var parts []Value
	for _, k := range n.kids {
		v := c.eval(k)
		switch {
		case v.Kind == Error:
			return v
		case v.Kind != Ref:
			return Err(ErrValue)
		case isUnion(v):
			parts = append(parts, v.Arr.V...)
		default:
			parts = append(parts, v)
		}
	}
	return Value{Kind: Ref, Arr: &Grid{Rows: 1, Cols: len(parts), V: parts}}
}

// read gives one cell.
func (c *ctx) read(a Area, row, col int) Value {
	return c.b.Value(int(a.Sheet), row, col)
}

// intersect reduces a reference to one value, by implicit intersection with the formula's row and column.
func (c *ctx) intersect(v Value) Value {
	if isUnion(v) {
		return Err(ErrValue)
	}
	c.settle(v)
	a := v.Area
	row, col := int(a.R1), int(a.C1)
	if a.R1 != a.R2 {
		if c.row < int(a.R1) || c.row > int(a.R2) {
			return Err(ErrValue)
		}
		row = c.row
	}
	if a.C1 != a.C2 {
		if c.col < int(a.C1) || c.col > int(a.C2) {
			return Err(ErrValue)
		}
		col = c.col
	}
	return c.read(a, row, col)
}

// clip cuts a range that runs to the edge of the sheet down to the used extent, keeping at least one cell.
func (c *ctx) clip(a Area) Area {
	if a.R2 != MaxRows-1 && a.C2 != MaxCols-1 {
		return a
	}
	rows, cols := c.b.Bounds(int(a.Sheet))
	if a.R2 == MaxRows-1 {
		a.R2 = max(a.R1, min(a.R2, int32(rows)-1))
	}
	if a.C2 == MaxCols-1 {
		a.C2 = max(a.C1, min(a.C2, int32(cols)-1))
	}
	return a
}

// gridValue turns a reference into an array, or a scalar for a single cell.
func (c *ctx) gridValue(v Value) Value {
	if isUnion(v) {
		return Err(ErrValue)
	}
	c.settle(v)
	a := c.clip(v.Area)
	if a.R1 == a.R2 && a.C1 == a.C2 {
		return c.read(a, int(a.R1), int(a.C1))
	}
	rows, cols := int(a.R2-a.R1)+1, int(a.C2-a.C1)+1
	if rows > maxCells/cols {
		return Err(ErrNum)
	}
	vals := make([]Value, rows*cols)
	c.b.Each(int(a.Sheet), int(a.R1), int(a.C1), int(a.R2), int(a.C2), func(r, col int, x Value) bool {
		vals[(r-int(a.R1))*cols+col-int(a.C1)] = x
		return true
	})
	return Value{Kind: Array, Arr: &Grid{Rows: rows, Cols: cols, V: vals}}
}

// gridOf gives any value as a grid.
func (c *ctx) gridOf(v Value) (*Grid, Value, bool) {
	switch v.Kind {
	case Ref:
		g := c.gridValue(v)
		switch g.Kind {
		case Array:
			return g.Arr, Value{}, true
		case Error:
			if v.Area.R1 != v.Area.R2 || v.Area.C1 != v.Area.C2 || isUnion(v) {
				if isUnion(v) || g.Str == ErrNum {
					return nil, g, false
				}
			}
		}
		return &Grid{Rows: 1, Cols: 1, V: []Value{g}}, Value{}, true
	case Array:
		return v.Arr, Value{}, true
	}
	return &Grid{Rows: 1, Cols: 1, V: []Value{v}}, Value{}, true
}

// eachCell visits the non-empty cells of a reference, or of each part of a union.
func (c *ctx) eachCell(v Value, fn func(sheet, row, col int, x Value) bool) bool {
	if isUnion(v) {
		for _, p := range v.Arr.V {
			if !c.eachCell(p, fn) {
				return false
			}
		}
		return true
	}
	c.settle(v)
	a := v.Area
	s := int(a.Sheet)
	done := true
	c.b.Each(s, int(a.R1), int(a.C1), int(a.R2), int(a.C2), func(r, col int, x Value) bool {
		if !fn(s, r, col, x) {
			done = false
			return false
		}
		return true
	})
	return done
}

// eachValue visits an argument. lit is true for a value given directly rather than through a reference or array.
func (c *ctx) eachValue(v Value, fn func(x Value, lit bool) bool) bool {
	switch v.Kind {
	case Ref:
		return c.eachCell(v, func(_, _, _ int, x Value) bool { return fn(x, false) })
	case Array:
		for _, x := range v.Arr.V {
			if x.Kind != Blank && !fn(x, false) {
				return false
			}
		}
		return true
	}
	return fn(v, true)
}

// eachNum visits the numbers of the arguments by the rules of SUM: in ranges and arrays only numbers count, a value
// given directly is coerced, and an error stops the walk and is returned.
func (c *ctx) eachNum(args []Value, fn func(f float64)) (Value, bool) {
	var bad Value
	ok := true
	for _, a := range args {
		c.eachValue(a, func(x Value, lit bool) bool {
			switch x.Kind {
			case Number:
				fn(x.Num)
			case Error:
				bad, ok = x, false
				return false
			default:
				if lit {
					f, e, good := c.num(x)
					if !good {
						bad, ok = e, false
						return false
					}
					fn(f)
				}
			}
			return true
		})
		if !ok {
			return bad, false
		}
	}
	return Value{}, true
}

// eachNumA is eachNum by the rules of AVERAGEA: in ranges text counts as 0 and logical values as 0 or 1.
func (c *ctx) eachNumA(args []Value, fn func(f float64)) (Value, bool) {
	var bad Value
	ok := true
	for _, a := range args {
		c.eachValue(a, func(x Value, lit bool) bool {
			switch x.Kind {
			case Number, Bool:
				fn(x.Num)
			case Error:
				bad, ok = x, false
				return false
			case Text:
				if !lit {
					fn(0)
					return true
				}
				f, e, good := c.num(x)
				if !good {
					bad, ok = e, false
					return false
				}
				fn(f)
			case Blank:
				if lit {
					fn(0)
				}
			}
			return true
		})
		if !ok {
			return bad, false
		}
	}
	return Value{}, true
}

// call runs a function.
func (c *ctx) call(n *node) Value {
	f := n.fn
	if len(n.kids) < f.min || (f.max >= 0 && len(n.kids) > f.max) {
		return Err(ErrValue)
	}
	if f.kind == kLazy {
		return f.lazy(c, n.kids)
	}
	cc := c
	if f.arrayArgs && !c.array {
		copied := *c
		copied.array = true
		cc = &copied
	}
	args := make([]Value, len(n.kids))
	for i, k := range n.kids {
		args[i] = cc.eval(k)
	}
	if f.kind == kRange {
		return f.fn(cc, args)
	}
	broad := false
	for i := range args {
		args[i] = c.operand(args[i])
		broad = broad || args[i].Kind == Array
	}
	if !broad {
		return f.fn(c, args)
	}
	return broadcast(args, func(x []Value) Value { return f.fn(c, x) })
}
