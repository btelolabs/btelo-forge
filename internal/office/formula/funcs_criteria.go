package formula

import (
	"strings"
	"unicode/utf8"
)

// criterion is a parsed condition of SUMIF and its kin.
type criterion struct {
	op      byte // '=', opNE, '<', '>', opLE, opGE
	num     float64
	isNum   bool
	isBool  bool
	isErr   bool
	empty   bool   // no operand: "=", "<>" or ""
	plain   bool   // given as "", which also takes empty text
	text    string // lower case
	wild    bool
	errCode string
}

func hasWild(s string) bool { return strings.ContainsAny(s, "*?~") }

func (c *ctx) parseCriterion(v Value) criterion {
	cr := criterion{op: '='}
	switch v.Kind {
	case Number:
		cr.isNum, cr.num = true, v.Num
		return cr
	case Blank:
		cr.isNum = true
		return cr
	case Bool:
		cr.isBool, cr.num = true, v.Num
		return cr
	case Error:
		cr.isErr, cr.errCode = true, v.Str
		return cr
	}
	s := v.Str
	plain := true
	for _, p := range []struct {
		s  string
		op byte
	}{{">=", opGE}, {"<=", opLE}, {"<>", opNE}, {">", '>'}, {"<", '<'}, {"=", '='}} {
		if strings.HasPrefix(s, p.s) {
			cr.op = p.op
			s = s[len(p.s):]
			plain = false
			break
		}
	}
	if s == "" {
		cr.empty, cr.plain = true, plain
		return cr
	}
	if f, ok := textToNumber(s, c.date1904()); ok {
		cr.isNum, cr.num = true, f
		return cr
	}
	if strings.EqualFold(s, "TRUE") || strings.EqualFold(s, "FALSE") {
		cr.isBool = true
		if strings.EqualFold(s, "TRUE") {
			cr.num = 1
		}
		return cr
	}
	if code := scanError(s, 0); code != "" && len(code) == len(s) {
		cr.isErr, cr.errCode = true, code
		return cr
	}
	cr.text = strings.ToLower(s)
	cr.wild = (cr.op == '=' || cr.op == opNE) && hasWild(s)
	return cr
}

// blank says the criterion is met by an empty cell.
func (cr *criterion) blank() bool {
	switch cr.op {
	case '=':
		return cr.empty
	case opNE:
		return !cr.empty
	}
	return false
}

// equal tests the '=' sense of the criterion.
func (cr *criterion) equal(c *ctx, v Value) bool {
	switch {
	case cr.empty:
		if cr.plain {
			return v.Kind == Blank || (v.Kind == Text && v.Str == "")
		}
		return v.Kind == Blank
	case cr.isNum:
		if v.Kind == Number {
			return v.Num == cr.num
		}
		if v.Kind == Text && v.Str != "" {
			f, ok := textToNumber(v.Str, c.date1904())
			return ok && f == cr.num
		}
		return false
	case cr.isBool:
		return v.Kind == Bool && v.Num == cr.num
	case cr.isErr:
		return v.Kind == Error && v.Str == cr.errCode
	}
	if v.Kind != Text {
		return false
	}
	if cr.wild {
		return wildMatch(cr.text, strings.ToLower(v.Str))
	}
	return strings.ToLower(v.Str) == cr.text
}

func (cr *criterion) match(c *ctx, v Value) bool {
	switch cr.op {
	case '=':
		return cr.equal(c, v)
	case opNE:
		return !cr.equal(c, v)
	}
	if cr.empty {
		return false
	}
	var r int
	switch {
	case cr.isNum:
		if v.Kind != Number {
			return false
		}
		r = compareValues(v, Num(cr.num))
	case cr.isBool:
		if v.Kind != Bool {
			return false
		}
		r = compareValues(v, BoolOf(cr.num != 0))
	case cr.isErr:
		return false
	default:
		if v.Kind != Text {
			return false
		}
		r = foldCompare(v.Str, cr.text)
	}
	switch cr.op {
	case '<':
		return r < 0
	case '>':
		return r > 0
	case opLE:
		return r <= 0
	}
	return r >= 0
}

// wildMatch matches lower-cased text against a lower-cased pattern with * ? and the ~ escape.
func wildMatch(pat, s string) bool {
	p, i := 0, 0
	star, mark := -1, 0
	for i < len(s) {
		if p < len(pat) {
			pc, pn := utf8.DecodeRuneInString(pat[p:])
			if pc == '*' {
				star, mark = p+pn, i
				p += pn
				continue
			}
			literal := true
			if pc == '~' && p+pn < len(pat) {
				if next := pat[p+pn]; next == '*' || next == '?' || next == '~' {
					p += pn
					pc, pn = utf8.DecodeRuneInString(pat[p:])
				}
			} else if pc == '?' {
				literal = false
			}
			sc, sn := utf8.DecodeRuneInString(s[i:])
			if !literal || pc == sc {
				p += pn
				i += sn
				continue
			}
		}
		if star < 0 {
			return false
		}
		_, sn := utf8.DecodeRuneInString(s[mark:])
		mark += sn
		i = mark
		p = star
	}
	for p < len(pat) && pat[p] == '*' {
		p++
	}
	return p == len(pat)
}

// condition is a criteria range with its criterion.
type condition struct {
	area Area
	cr   criterion
}

// scanIfs calls fn with the offset from the top-left of the ranges of every position that meets all the conditions.
// It returns the count of positions past the used part of the sheet that meet them, which only a blank-matching
// criterion can do; no value is there to report for those.
func (c *ctx) scanIfs(conds []condition, fn func(dr, dc int) bool) float64 {
	first := conds[0].area
	rows, cols := int(first.R2-first.R1)+1, int(first.C2-first.C1)+1
	rest := func(dr, dc int) bool {
		for i := 1; i < len(conds); i++ {
			a := conds[i].area
			if !conds[i].cr.match(c, c.b.Value(int(a.Sheet), int(a.R1)+dr, int(a.C1)+dc)) {
				return false
			}
		}
		return true
	}
	blank := false
	all := true
	for i := range conds {
		if conds[i].cr.blank() {
			blank = true
		} else {
			all = false
		}
	}
	if !blank {
		c.b.Each(int(first.Sheet), int(first.R1), int(first.C1), int(first.R2), int(first.C2),
			func(r, col int, v Value) bool {
				if conds[0].cr.match(c, v) {
					dr, dc := r-int(first.R1), col-int(first.C1)
					if rest(dr, dc) {
						return fn(dr, dc)
					}
				}
				return true
			})
		return 0
	}
	// Blank cells count, so walk densely, but only the part of the ranges that holds anything.
	inRows, inCols := 0, 0
	for i := range conds {
		a := conds[i].area
		usedRows, usedCols := c.b.Bounds(int(a.Sheet))
		inRows = max(inRows, min(rows, usedRows-int(a.R1)))
		inCols = max(inCols, min(cols, usedCols-int(a.C1)))
	}
	stop := false
	for dr := 0; dr < inRows && !stop; dr++ {
		for dc := 0; dc < inCols; dc++ {
			v := c.b.Value(int(first.Sheet), int(first.R1)+dr, int(first.C1)+dc)
			if conds[0].cr.match(c, v) && rest(dr, dc) && !fn(dr, dc) {
				stop = true
				break
			}
		}
	}
	if !all {
		return 0
	}
	return float64(rows)*float64(cols) - float64(inRows)*float64(inCols)
}

// conditions builds the criteria from (range, criterion) argument pairs; crit gives each criterion's value.
func (c *ctx) conditions(a []Value, crit []Value) ([]condition, Value, bool) {
	conds := make([]condition, 0, len(a)/2)
	for i := 0; i+1 < len(a); i += 2 {
		v := a[i]
		if v.Kind == Error {
			return nil, v, false
		}
		if v.Kind != Ref || isUnion(v) {
			return nil, Err(ErrValue), false
		}
		c.settle(v)
		if len(conds) > 0 {
			f := conds[0].area
			if f.R2-f.R1 != v.Area.R2-v.Area.R1 || f.C2-f.C1 != v.Area.C2-v.Area.C1 {
				return nil, Err(ErrValue), false
			}
		}
		conds = append(conds, condition{area: v.Area, cr: c.parseCriterion(crit[i/2])})
	}
	return conds, Value{}, true
}

// perCriterion runs fn for the criteria, once when they are single values and across the array when any is an array.
func (c *ctx) perCriterion(a []Value, fn func(crit []Value) Value) Value {
	crit := make([]Value, 0, len(a)/2)
	broad := false
	for i := 1; i < len(a); i += 2 {
		v := c.operand(a[i])
		broad = broad || v.Kind == Array
		crit = append(crit, v)
	}
	if !broad {
		return fn(crit)
	}
	return broadcast(crit, fn)
}

// sumRange gives the area of the values matching a criteria range: same shape, from sum's top left.
func sumRange(crit, sum Area) Area {
	return Area{Sheet: sum.Sheet, R1: sum.R1, C1: sum.C1, R2: sum.R1 + crit.R2 - crit.R1, C2: sum.C1 + crit.C2 - crit.C1}
}

// aggIfs is the tally behind SUMIFS, AVERAGEIFS, MINIFS and MAXIFS.
type aggIfs struct {
	sum, low, high float64
	n              int
	bad            Value
	failed         bool
}

func (c *ctx) tally(conds []condition, s Area) aggIfs {
	var t aggIfs
	c.scanIfs(conds, func(dr, dc int) bool {
		v := c.b.Value(int(s.Sheet), int(s.R1)+dr, int(s.C1)+dc)
		switch v.Kind {
		case Number:
			if t.n == 0 || v.Num < t.low {
				t.low = v.Num
			}
			if t.n == 0 || v.Num > t.high {
				t.high = v.Num
			}
			t.sum += v.Num
			t.n++
		case Error:
			t.bad, t.failed = v, true
			return false
		}
		return true
	})
	return t
}

// ifsFn makes SUMIFS, AVERAGEIFS, MINIFS and MAXIFS, whose arguments are (values, range1, crit1, ...).
func ifsFn(out func(t aggIfs) Value) func(c *ctx, a []Value) Value {
	return func(c *ctx, a []Value) Value {
		if a[0].Kind == Error {
			return a[0]
		}
		if a[0].Kind != Ref || isUnion(a[0]) || len(a)%2 == 0 {
			return Err(ErrValue)
		}
		c.settle(a[0])
		pairs := a[1:]
		return c.perCriterion(pairs, func(crit []Value) Value {
			conds, bad, ok := c.conditions(pairs, crit)
			if !ok {
				return bad
			}
			s := a[0].Area
			f := conds[0].area
			if s.R2-s.R1 != f.R2-f.R1 || s.C2-s.C1 != f.C2-f.C1 {
				return Err(ErrValue)
			}
			t := c.tally(conds, s)
			if t.failed {
				return t.bad
			}
			return out(t)
		})
	}
}

// ifFn makes SUMIF and AVERAGEIF, whose arguments are (range, crit[, values]).
func ifFn(out func(t aggIfs) Value) func(c *ctx, a []Value) Value {
	return func(c *ctx, a []Value) Value {
		pairs := a[:2]
		return c.perCriterion(pairs, func(crit []Value) Value {
			conds, bad, ok := c.conditions(pairs, crit)
			if !ok {
				return bad
			}
			s := conds[0].area
			if len(a) > 2 && a[2].Kind != Blank {
				if a[2].Kind == Error {
					return a[2]
				}
				if a[2].Kind != Ref || isUnion(a[2]) {
					return Err(ErrValue)
				}
				c.settle(a[2])
				s = sumRange(conds[0].area, a[2].Area)
				if s.R2 >= MaxRows || s.C2 >= MaxCols {
					return Err(ErrRef)
				}
			}
			t := c.tally(conds, s)
			if t.failed {
				return t.bad
			}
			return out(t)
		})
	}
}

func fnCountIfs(c *ctx, a []Value) Value {
	if len(a)%2 != 0 {
		return Err(ErrValue)
	}
	return c.perCriterion(a, func(crit []Value) Value {
		conds, bad, ok := c.conditions(a, crit)
		if !ok {
			return bad
		}
		n := 0.0
		outside := c.scanIfs(conds, func(_, _ int) bool { n++; return true })
		return Num(n + outside)
	})
}

func registerCriteria() {
	sum := func(t aggIfs) Value { return numResult(t.sum) }
	avg := func(t aggIfs) Value {
		if t.n == 0 {
			return Err(ErrDiv0)
		}
		return numResult(t.sum / float64(t.n))
	}
	ranged("SUMIF", 2, 3, ifFn(sum))
	ranged("AVERAGEIF", 2, 3, ifFn(avg))
	ranged("SUMIFS", 3, -1, ifsFn(sum))
	ranged("AVERAGEIFS", 3, -1, ifsFn(avg))
	ranged("MINIFS", 3, -1, ifsFn(func(t aggIfs) Value { return Num(t.low) }))
	ranged("MAXIFS", 3, -1, ifsFn(func(t aggIfs) Value { return Num(t.high) }))
	ranged("COUNTIF", 2, 2, fnCountIfs)
	ranged("COUNTIFS", 2, -1, fnCountIfs)
}
