package formula

import "math"

func fvOf(r, n, pmt, pv, t float64) float64 {
	if r == 0 {
		return -(pv + pmt*n)
	}
	g := math.Pow(1+r, n)
	return -(pv*g + pmt*(1+r*t)*(g-1)/r)
}

func pvOf(r, n, pmt, fv, t float64) float64 {
	if r == 0 {
		return -(fv + pmt*n)
	}
	g := math.Pow(1+r, n)
	return -(fv + pmt*(1+r*t)*(g-1)/r) / g
}

func pmtOf(r, n, pv, fv, t float64) float64 {
	if r == 0 {
		return -(pv + fv) / n
	}
	g := math.Pow(1+r, n)
	return -(pv*g + fv) * r / ((1 + r*t) * (g - 1))
}

func ipmtOf(r, per, n, pv, fv, t float64) float64 {
	pmt := pmtOf(r, n, pv, fv, t)
	if t != 0 && per == 1 {
		return 0
	}
	i := fvOf(r, per-1, pmt, pv, t) * r
	if t != 0 {
		i /= 1 + r
	}
	return i
}

func fnPeriodArgs(c *ctx, a []Value) (r, per, n, pv, fv, t float64, bad Value, ok bool) {
	if bad, ok = c.nums(a, &r, &per, &n, &pv, &fv, &t); !ok {
		return
	}
	if per < 1 || per > n || n <= 0 {
		return r, per, n, pv, fv, t, Err(ErrNum), false
	}
	return r, per, n, pv, fv, t, Value{}, true
}

// rateOf solves for the rate with Newton's method, then falls back on bisection over a bracket.
func rateOf(n, pmt, pv, fv, t, guess float64) (float64, bool) {
	f := func(r float64) float64 {
		if math.Abs(r) < 1e-12 {
			return pv + pmt*n + fv
		}
		g := math.Pow(1+r, n)
		return pv*g + pmt*(1+r*t)*(g-1)/r + fv
	}
	r := guess
	for range 100 {
		y := f(r)
		if math.Abs(y) < 1e-9 {
			return r, true
		}
		h := math.Max(math.Abs(r)*1e-7, 1e-9)
		d := (f(r+h) - f(r-h)) / (2 * h)
		if d == 0 || math.IsNaN(d) {
			break
		}
		next := r - y/d
		if next <= -1 {
			next = (r - 1) / 2
		}
		if math.Abs(next-r) < 1e-12 {
			return next, !math.IsNaN(next)
		}
		r = next
		if math.IsNaN(r) || math.IsInf(r, 0) {
			break
		}
	}
	return bisect(f, -0.999999, 10)
}

// bisect looks for a sign change across a grid of the interval, then closes in on it.
func bisect(f func(float64) float64, lo, hi float64) (float64, bool) {
	const steps = 2000
	prev := lo
	fp := f(prev)
	for i := 1; i <= steps; i++ {
		x := lo + (hi-lo)*float64(i)/steps
		fx := f(x)
		if !math.IsNaN(fp) && !math.IsNaN(fx) && !math.IsInf(fp, 0) && !math.IsInf(fx, 0) && (fp < 0) != (fx < 0) {
			a, b := prev, x
			fa := fp
			for range 200 {
				m := (a + b) / 2
				fm := f(m)
				if (fa < 0) != (fm < 0) {
					b = m
				} else {
					a, fa = m, fm
				}
			}
			return (a + b) / 2, true
		}
		prev, fp = x, fx
	}
	return 0, false
}

func npvOf(r float64, xs []float64) float64 {
	s := 0.0
	for i, x := range xs {
		s += x / math.Pow(1+r, float64(i+1))
	}
	return s
}

func fnIrr(c *ctx, a []Value) Value {
	xs, bad, ok := c.numbers(a[:1])
	if !ok {
		return bad
	}
	guess := 0.1
	if len(a) > 1 && a[1].Kind != Blank {
		if bad, ok := c.nums([]Value{c.operand(a[1])}, &guess); !ok {
			return bad
		}
	}
	pos, neg := false, false
	for _, x := range xs {
		pos = pos || x > 0
		neg = neg || x < 0
	}
	if !pos || !neg {
		return Err(ErrNum)
	}
	f := func(r float64) float64 {
		s := 0.0
		for i, x := range xs {
			s += x / math.Pow(1+r, float64(i))
		}
		return s
	}
	r := guess
	for range 100 {
		y := f(r)
		if math.Abs(y) < 1e-10 {
			return Num(r)
		}
		d := 0.0
		for i, x := range xs {
			if i > 0 {
				d -= float64(i) * x / math.Pow(1+r, float64(i+1))
			}
		}
		if d == 0 || math.IsNaN(d) {
			break
		}
		next := r - y/d
		if next <= -1 {
			next = (r - 1) / 2
		}
		if math.Abs(next-r) < 1e-13 {
			return Num(next)
		}
		r = next
		if math.IsNaN(r) || math.IsInf(r, 0) {
			break
		}
	}
	if v, ok := bisect(f, -0.999999, 100); ok {
		return Num(v)
	}
	return Err(ErrNum)
}

func registerFinance() {
	scalar("PMT", 3, 5, func(c *ctx, a []Value) Value {
		var r, n, pv, fv, t float64
		if bad, ok := c.nums(a, &r, &n, &pv, &fv, &t); !ok {
			return bad
		}
		if n == 0 {
			return Err(ErrNum)
		}
		return numResult(pmtOf(r, n, pv, fv, t))
	})
	scalar("FV", 3, 5, func(c *ctx, a []Value) Value {
		var r, n, pmt, pv, t float64
		if bad, ok := c.nums(a, &r, &n, &pmt, &pv, &t); !ok {
			return bad
		}
		return numResult(fvOf(r, n, pmt, pv, t))
	})
	scalar("PV", 3, 5, func(c *ctx, a []Value) Value {
		var r, n, pmt, fv, t float64
		if bad, ok := c.nums(a, &r, &n, &pmt, &fv, &t); !ok {
			return bad
		}
		return numResult(pvOf(r, n, pmt, fv, t))
	})
	ranged("NPV", 2, -1, func(c *ctx, a []Value) Value {
		var r float64
		if bad, ok := c.nums([]Value{c.operand(a[0])}, &r); !ok {
			return bad
		}
		if r == -1 {
			return Err(ErrDiv0)
		}
		xs, bad, ok := c.numbers(a[1:])
		if !ok {
			return bad
		}
		return numResult(npvOf(r, xs))
	})
	ranged("IRR", 1, 2, fnIrr)
	scalar("NPER", 3, 5, func(c *ctx, a []Value) Value {
		var r, pmt, pv, fv, t float64
		if bad, ok := c.nums(a, &r, &pmt, &pv, &fv, &t); !ok {
			return bad
		}
		if r == 0 {
			if pmt == 0 {
				return Err(ErrNum)
			}
			return numResult(-(pv + fv) / pmt)
		}
		w := pmt * (1 + r*t)
		x := (w - fv*r) / (pv*r + w)
		if x <= 0 || r <= -1 {
			return Err(ErrNum)
		}
		return numResult(math.Log(x) / math.Log(1+r))
	})
	scalar("RATE", 3, 6, func(c *ctx, a []Value) Value {
		n, pmt, pv, fv, t, guess := 0.0, 0.0, 0.0, 0.0, 0.0, 0.1
		if bad, ok := c.nums(a, &n, &pmt, &pv, &fv, &t, &guess); !ok {
			return bad
		}
		if n <= 0 {
			return Err(ErrNum)
		}
		r, ok := rateOf(n, pmt, pv, fv, t, guess)
		if !ok {
			return Err(ErrNum)
		}
		return numResult(r)
	})
	scalar("IPMT", 4, 6, func(c *ctx, a []Value) Value {
		r, per, n, pv, fv, t, bad, ok := fnPeriodArgs(c, a)
		if !ok {
			return bad
		}
		return numResult(ipmtOf(r, per, n, pv, fv, t))
	})
	scalar("PPMT", 4, 6, func(c *ctx, a []Value) Value {
		r, per, n, pv, fv, t, bad, ok := fnPeriodArgs(c, a)
		if !ok {
			return bad
		}
		return numResult(pmtOf(r, n, pv, fv, t) - ipmtOf(r, per, n, pv, fv, t))
	})
	num3 := func(name string, fn func(x, y, z float64) Value) {
		scalar(name, 3, 3, func(c *ctx, a []Value) Value {
			var x, y, z float64
			if bad, ok := c.nums(a, &x, &y, &z); !ok {
				return bad
			}
			return fn(x, y, z)
		})
	}
	num3("SLN", func(cost, salvage, life float64) Value {
		if life == 0 {
			return Err(ErrDiv0)
		}
		return numResult((cost - salvage) / life)
	})
}
