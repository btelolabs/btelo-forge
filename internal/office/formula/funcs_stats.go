package formula

import (
	"math"
	"sort"
)

// numbers gathers the numbers of the arguments.
func (c *ctx) numbers(a []Value) ([]float64, Value, bool) {
	var xs []float64
	bad, ok := c.eachNum(a, func(f float64) { xs = append(xs, f) })
	return xs, bad, ok
}

// sqDev gives the sum of squared deviations and the count, by Welford's method.
func sqDev(c *ctx, a []Value, all bool) (float64, int, Value, bool) {
	mean, m2 := 0.0, 0.0
	n := 0
	step := func(f float64) {
		n++
		d := f - mean
		mean += d / float64(n)
		m2 += d * (f - mean)
	}
	var bad Value
	var ok bool
	if all {
		bad, ok = c.eachNumA(a, step)
	} else {
		bad, ok = c.eachNum(a, step)
	}
	return m2, n, bad, ok
}

// spread makes STDEV, VAR and their population forms.
func spread(population, root bool) func(c *ctx, a []Value) Value {
	return func(c *ctx, a []Value) Value {
		m2, n, bad, ok := sqDev(c, a, false)
		if !ok {
			return bad
		}
		d := n - 1
		if population {
			d = n
		}
		if d <= 0 {
			return Err(ErrDiv0)
		}
		v := m2 / float64(d)
		if root {
			v = math.Sqrt(v)
		}
		return numResult(v)
	}
}

func fnMedian(c *ctx, a []Value) Value {
	xs, bad, ok := c.numbers(a)
	if !ok {
		return bad
	}
	if len(xs) == 0 {
		return Err(ErrNum)
	}
	sort.Float64s(xs)
	m := len(xs) / 2
	if len(xs)%2 == 1 {
		return Num(xs[m])
	}
	return numResult((xs[m-1] + xs[m]) / 2)
}

func fnMode(c *ctx, a []Value) Value {
	xs, bad, ok := c.numbers(a)
	if !ok {
		return bad
	}
	count := make(map[float64]int, len(xs))
	top := 1
	for _, x := range xs {
		count[x]++
		top = max(top, count[x])
	}
	if top < 2 {
		return Err(ErrNA)
	}
	// Ties go to the value that appears first in the data.
	for _, x := range xs {
		if count[x] == top {
			return Num(x)
		}
	}
	return Err(ErrNA)
}

// kth makes LARGE and SMALL.
func kth(large bool) func(c *ctx, a []Value) Value {
	return func(c *ctx, a []Value) Value {
		xs, bad, ok := c.numbers(a[:1])
		if !ok {
			return bad
		}
		sort.Float64s(xs)
		return c.mapValue(c.operand(a[1]), func(k Value) Value {
			f, bad, ok := c.num(k)
			if !ok {
				return bad
			}
			n := int(math.Ceil(f))
			if n < 1 || n > len(xs) || f > float64(len(xs)) {
				return Err(ErrNum)
			}
			if large {
				return Num(xs[len(xs)-n])
			}
			return Num(xs[n-1])
		})
	}
}

func fnRank(c *ctx, a []Value) Value {
	if a[1].Kind != Ref {
		return Err(ErrValue)
	}
	order := 0.0
	if len(a) > 2 {
		v := c.operand(a[2])
		if bad, ok := c.nums([]Value{v}, &order); !ok {
			return bad
		}
	}
	xs, bad, ok := c.numbers(a[1:2])
	if !ok {
		return bad
	}
	return c.mapValue(c.operand(a[0]), func(v Value) Value {
		x, bad, ok := c.num(v)
		if !ok {
			return bad
		}
		r, found := 1, false
		for _, f := range xs {
			if f == x {
				found = true
			}
			if (order == 0 && f > x) || (order != 0 && f < x) {
				r++
			}
		}
		if !found {
			return Err(ErrNA)
		}
		return Num(float64(r))
	})
}

func fnMaxMin(want float64) func(c *ctx, a []Value) Value {
	return func(c *ctx, a []Value) Value {
		r, seen := 0.0, false
		bad, ok := c.eachNum(a, func(f float64) {
			if !seen || f*want > r*want {
				r, seen = f, true
			}
		})
		if !ok {
			return bad
		}
		return Num(r)
	}
}

func fnMaxMinA(want float64) func(c *ctx, a []Value) Value {
	return func(c *ctx, a []Value) Value {
		r, seen := 0.0, false
		bad, ok := c.eachNumA(a, func(f float64) {
			if !seen || f*want > r*want {
				r, seen = f, true
			}
		})
		if !ok {
			return bad
		}
		return Num(r)
	}
}

func fnAverage(c *ctx, a []Value) Value {
	s, n := 0.0, 0
	if bad, ok := c.eachNum(a, func(f float64) { s += f; n++ }); !ok {
		return bad
	}
	if n == 0 {
		return Err(ErrDiv0)
	}
	return numResult(s / float64(n))
}

func fnAverageA(c *ctx, a []Value) Value {
	s, n := 0.0, 0
	if bad, ok := c.eachNumA(a, func(f float64) { s += f; n++ }); !ok {
		return bad
	}
	if n == 0 {
		return Err(ErrDiv0)
	}
	return numResult(s / float64(n))
}

func fnCount(c *ctx, a []Value) Value {
	n := 0
	for _, v := range a {
		c.eachValue(v, func(x Value, lit bool) bool {
			switch x.Kind {
			case Number:
				n++
			case Bool, Blank:
				if lit {
					n++
				}
			case Text:
				if lit {
					if _, ok := textToNumber(x.Str, c.date1904()); ok {
						n++
					}
				}
			}
			return true
		})
	}
	return Num(float64(n))
}

func fnCountA(c *ctx, a []Value) Value {
	n := 0
	for _, v := range a {
		c.eachValue(v, func(Value, bool) bool { n++; return true })
	}
	return Num(float64(n))
}

func areaCells(a Area) float64 { return float64(a.R2-a.R1+1) * float64(a.C2-a.C1+1) }

func fnCountBlank(c *ctx, a []Value) Value {
	v := a[0]
	if v.Kind != Ref {
		return Err(ErrValue)
	}
	total := 0.0
	full := 0
	parts := []Value{v}
	if isUnion(v) {
		parts = v.Arr.V
	}
	for _, p := range parts {
		total += areaCells(p.Area)
		c.eachCell(p, func(_, _, _ int, x Value) bool {
			if !(x.Kind == Text && x.Str == "") && x.Kind != Blank {
				full++
			}
			return true
		})
	}
	return Num(total - float64(full))
}

// fnSubtotal skips cells that are themselves SUBTOTAL formulas when it can tell, through the engine.
func fnSubtotal(c *ctx, a []Value) Value {
	code, bad, ok := c.num(c.operand(a[0]))
	if !ok {
		return bad
	}
	n := int(code)
	if n > 100 {
		n -= 100
	}
	if n < 1 || n > 11 {
		return Err(ErrValue)
	}
	args := a[1:]
	if c.e != nil {
		filtered := make([]Value, 0, len(args))
		for _, v := range args {
			filtered = append(filtered, c.withoutSubtotals(v))
		}
		args = filtered
	}
	switch n {
	case 1:
		return fnAverage(c, args)
	case 2:
		return fnCount(c, args)
	case 3:
		return fnCountA(c, args)
	case 4:
		return fnMaxMin(1)(c, args)
	case 5:
		return fnMaxMin(-1)(c, args)
	case 6:
		return funcs["PRODUCT"].fn(c, args)
	case 7:
		return spread(false, true)(c, args)
	case 8:
		return spread(true, true)(c, args)
	case 9:
		return funcs["SUM"].fn(c, args)
	case 10:
		return spread(false, false)(c, args)
	}
	return spread(true, false)(c, args)
}

// withoutSubtotals gives the argument as an array without SUBTOTAL formula cells, when it holds any.
func (c *ctx) withoutSubtotals(v Value) Value {
	if v.Kind != Ref || !c.e.hasSubtotals {
		return v
	}
	var keep []Value
	found := false
	c.eachCell(v, func(s, r, col int, x Value) bool {
		if c.e.isSubtotal(s, r, col) {
			found = true
			return true
		}
		keep = append(keep, x)
		return true
	})
	if !found {
		return v
	}
	if len(keep) == 0 {
		return Value{Kind: Array, Arr: &Grid{Rows: 1, Cols: 1, V: []Value{{}}}}
	}
	return Value{Kind: Array, Arr: &Grid{Rows: len(keep), Cols: 1, V: keep}}
}

func registerStats() {
	ranged("AVERAGE", 1, -1, fnAverage)
	ranged("AVERAGEA", 1, -1, fnAverageA)
	ranged("COUNT", 0, -1, fnCount)
	ranged("COUNTA", 0, -1, fnCountA)
	ranged("COUNTBLANK", 1, 1, fnCountBlank)
	ranged("MAX", 0, -1, fnMaxMin(1))
	ranged("MIN", 0, -1, fnMaxMin(-1))
	ranged("MAXA", 0, -1, fnMaxMinA(1))
	ranged("MINA", 0, -1, fnMaxMinA(-1))
	ranged("MEDIAN", 1, -1, fnMedian)
	ranged("MODE", 1, -1, fnMode)
	alias("MODE.SNGL", "MODE")
	ranged("STDEV", 1, -1, spread(false, true))
	alias("STDEV.S", "STDEV")
	ranged("STDEVP", 1, -1, spread(true, true))
	alias("STDEV.P", "STDEVP")
	ranged("VAR", 1, -1, spread(false, false))
	alias("VAR.S", "VAR")
	ranged("VARP", 1, -1, spread(true, false))
	alias("VAR.P", "VARP")
	ranged("LARGE", 2, 2, kth(true))
	ranged("SMALL", 2, 2, kth(false))
	ranged("RANK", 2, 3, fnRank)
	alias("RANK.EQ", "RANK")
	ranged("SUBTOTAL", 2, -1, fnSubtotal)
}
