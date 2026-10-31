package formula

import (
	"math"
	"math/rand/v2"
)

// roundDecimal rounds on the decimal form of the number at 15 significant digits, so ROUND(1.005,2) is 1.01.
func roundDecimal(x float64, places, mode int) float64 {
	if x == 0 || math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	if places > 400 {
		return x
	}
	if places < -400 {
		return 0
	}
	neg := x < 0
	r := newDec(math.Abs(x)).round(places, mode).float()
	if neg {
		return -r
	}
	return r
}

// fix15 removes binary noise past Excel's 15 significant digits.
func fix15(x float64) float64 {
	if x == 0 || math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	if x < 0 {
		return -newDec(-x).float()
	}
	return newDec(x).float()
}

func floatInt(f float64) int {
	switch {
	case f > 1e15:
		return 1_000_000_000_000_000
	case f < -1e15:
		return -1_000_000_000_000_000
	case math.IsNaN(f):
		return 0
	}
	return int(f)
}

func roundFn(name string, mode int) {
	scalar(name, 2, 2, func(c *ctx, a []Value) Value {
		var x, n float64
		if bad, ok := c.nums(a, &x, &n); !ok {
			return bad
		}
		return numResult(roundDecimal(x, floatInt(math.Trunc(n)), mode))
	})
}

func multipleUp(x, s float64) float64   { return fix15(math.Ceil(fix15(x/s)) * s) }
func multipleDown(x, s float64) float64 { return fix15(math.Floor(fix15(x/s)) * s) }

func fnCeiling(c *ctx, a []Value) Value {
	var x, s float64
	s = 1
	if bad, ok := c.nums(a, &x, &s); !ok {
		return bad
	}
	if x == 0 || s == 0 {
		return Num(0)
	}
	if x > 0 && s < 0 {
		return Err(ErrNum)
	}
	return numResult(multipleUp(x, s))
}

func fnFloor(c *ctx, a []Value) Value {
	var x, s float64
	s = 1
	if bad, ok := c.nums(a, &x, &s); !ok {
		return bad
	}
	if s == 0 {
		if x == 0 {
			return Num(0)
		}
		return Err(ErrDiv0)
	}
	if x == 0 {
		return Num(0)
	}
	if x > 0 && s < 0 {
		return Err(ErrNum)
	}
	return numResult(multipleDown(x, s))
}

func fnCeilingMath(c *ctx, a []Value) Value {
	x, s, mode := 0.0, 1.0, 0.0
	if bad, ok := c.nums(a, &x, &s, &mode); !ok {
		return bad
	}
	if s == 0 || x == 0 {
		return Num(0)
	}
	s = math.Abs(s)
	if x < 0 && mode != 0 {
		return numResult(multipleDown(x, s))
	}
	return numResult(multipleUp(x, s))
}

func fnFloorMath(c *ctx, a []Value) Value {
	x, s, mode := 0.0, 1.0, 0.0
	if bad, ok := c.nums(a, &x, &s, &mode); !ok {
		return bad
	}
	if s == 0 || x == 0 {
		return Num(0)
	}
	s = math.Abs(s)
	if x < 0 && mode != 0 {
		return numResult(multipleUp(x, s))
	}
	return numResult(multipleDown(x, s))
}

func fnMod(n, d float64) Value {
	if d == 0 {
		return Err(ErrDiv0)
	}
	if math.Abs(n/d) >= 1125900000000 {
		return Err(ErrNum)
	}
	m := math.Mod(n, d)
	if m != 0 && (m < 0) != (d < 0) {
		m += d
	}
	return numResult(m)
}

func gcd(a, b float64) float64 {
	for b != 0 {
		a, b = b, math.Mod(a, b)
	}
	return a
}

func registerMath() {
	ranged("SUM", 0, -1, func(c *ctx, a []Value) Value {
		s := 0.0
		if bad, ok := c.eachNum(a, func(f float64) { s += f }); !ok {
			return bad
		}
		return numResult(s)
	})
	ranged("PRODUCT", 0, -1, func(c *ctx, a []Value) Value {
		p, n := 1.0, 0
		if bad, ok := c.eachNum(a, func(f float64) { p *= f; n++ }); !ok {
			return bad
		}
		if n == 0 {
			return Num(0)
		}
		return numResult(p)
	})
	d := ranged("SUMPRODUCT", 1, -1, func(c *ctx, a []Value) Value {
		var first *Grid
		sum := make([]float64, 0)
		for i, v := range a {
			g, bad, ok := c.gridOf(v)
			if !ok {
				return bad
			}
			if i == 0 {
				first = g
				sum = make([]float64, len(g.V))
				for j := range sum {
					sum[j] = 1
				}
			} else if g.Rows != first.Rows || g.Cols != first.Cols {
				return Err(ErrValue)
			}
			for j, x := range g.V {
				switch x.Kind {
				case Number:
					sum[j] *= x.Num
				case Error:
					return x
				default:
					sum[j] = 0
				}
			}
		}
		t := 0.0
		for _, x := range sum {
			t += x
		}
		return numResult(t)
	})
	d.arrayArgs = true

	roundFn("ROUND", roundHalf)
	roundFn("ROUNDUP", roundUp)
	roundFn("ROUNDDOWN", roundDown)
	num1("INT", func(x float64) Value { return Num(math.Floor(x)) })
	scalar("TRUNC", 1, 2, func(c *ctx, a []Value) Value {
		var x, n float64
		if bad, ok := c.nums(a, &x, &n); !ok {
			return bad
		}
		return numResult(roundDecimal(x, floatInt(math.Trunc(n)), roundDown))
	})
	num2("MOD", fnMod)
	num1("ABS", func(x float64) Value { return Num(math.Abs(x)) })
	num1("SQRT", func(x float64) Value {
		if x < 0 {
			return Err(ErrNum)
		}
		return Num(math.Sqrt(x))
	})
	num2("POWER", power)
	num1("EXP", func(x float64) Value { return numResult(math.Exp(x)) })
	num1("LN", func(x float64) Value {
		if x <= 0 {
			return Err(ErrNum)
		}
		return Num(math.Log(x))
	})
	num1("LOG10", func(x float64) Value {
		if x <= 0 {
			return Err(ErrNum)
		}
		return Num(math.Log10(x))
	})
	scalar("LOG", 1, 2, func(c *ctx, a []Value) Value {
		x, b := 0.0, 10.0
		if bad, ok := c.nums(a, &x, &b); !ok {
			return bad
		}
		if x <= 0 || b <= 0 {
			return Err(ErrNum)
		}
		if b == 1 {
			return Err(ErrDiv0)
		}
		if b == 10 {
			return Num(math.Log10(x))
		}
		return numResult(fix15(math.Log(x) / math.Log(b)))
	})
	ranged("PI", 0, 0, func(*ctx, []Value) Value { return Num(math.Pi) })
	ranged("RAND", 0, 0, func(*ctx, []Value) Value { return Num(rand.Float64()) }).volatile = true
	scalar("RANDBETWEEN", 2, 2, func(c *ctx, a []Value) Value {
		var lo, hi float64
		if bad, ok := c.nums(a, &lo, &hi); !ok {
			return bad
		}
		lo, hi = math.Ceil(lo), math.Floor(hi)
		if lo > hi {
			return Err(ErrNum)
		}
		return Num(lo + math.Floor(rand.Float64()*(hi-lo+1)))
	}).volatile = true
	scalar("CEILING", 1, 2, fnCeiling)
	scalar("FLOOR", 1, 2, fnFloor)
	scalar("CEILING.MATH", 1, 3, fnCeilingMath)
	scalar("FLOOR.MATH", 1, 3, fnFloorMath)
	alias("CEILING.PRECISE", "CEILING.MATH")
	alias("FLOOR.PRECISE", "FLOOR.MATH")
	num1("SIGN", func(x float64) Value {
		switch {
		case x > 0:
			return Num(1)
		case x < 0:
			return Num(-1)
		}
		return Num(0)
	})
	num2("MROUND", func(x, m float64) Value {
		if m == 0 {
			return Num(0)
		}
		if (x < 0) != (m < 0) && x != 0 {
			return Err(ErrNum)
		}
		return numResult(fix15(roundDecimal(fix15(x/m), 0, roundHalf) * m))
	})
	num2("QUOTIENT", func(x, y float64) Value {
		if y == 0 {
			return Err(ErrDiv0)
		}
		return numResult(math.Trunc(x / y))
	})
	ranged("GCD", 1, -1, func(c *ctx, a []Value) Value {
		g, fail := 0.0, false
		bad, ok := c.eachNum(a, func(f float64) {
			if f < 0 {
				fail = true
			}
			g = gcd(g, math.Trunc(f))
		})
		if !ok {
			return bad
		}
		if fail {
			return Err(ErrNum)
		}
		return Num(g)
	})
	ranged("LCM", 1, -1, func(c *ctx, a []Value) Value {
		l, fail := 1.0, false
		bad, ok := c.eachNum(a, func(f float64) {
			f = math.Trunc(f)
			if f < 0 {
				fail = true
				return
			}
			if f == 0 || l == 0 {
				l = 0
				return
			}
			l = l / gcd(l, f) * f
		})
		if !ok {
			return bad
		}
		if fail {
			return Err(ErrNum)
		}
		return numResult(l)
	})
	num1("FACT", func(x float64) Value {
		if x < 0 {
			return Err(ErrNum)
		}
		n := math.Floor(x)
		if n > 170 {
			return Err(ErrNum)
		}
		r := 1.0
		for i := 2.0; i <= n; i++ {
			r *= i
		}
		return Num(r)
	})
	num1("DEGREES", func(x float64) Value { return Num(x * 180 / math.Pi) })
	num1("RADIANS", func(x float64) Value { return Num(x * math.Pi / 180) })
	num1("SIN", func(x float64) Value { return numResult(math.Sin(x)) })
	num1("COS", func(x float64) Value { return numResult(math.Cos(x)) })
	num1("TAN", func(x float64) Value { return numResult(math.Tan(x)) })
	num1("ASIN", func(x float64) Value { return numResult(math.Asin(x)) })
	num1("ACOS", func(x float64) Value { return numResult(math.Acos(x)) })
	num1("ATAN", func(x float64) Value { return numResult(math.Atan(x)) })
	num2("ATAN2", func(x, y float64) Value {
		if x == 0 && y == 0 {
			return Err(ErrDiv0)
		}
		return numResult(math.Atan2(y, x))
	})
}
