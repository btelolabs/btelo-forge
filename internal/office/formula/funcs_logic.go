package formula

// eachLogic visits the truth values of the arguments of AND, OR and XOR: text in ranges is ignored.
func (c *ctx) eachLogic(a []Value, fn func(b bool)) (bool, Value) {
	seen := false
	var bad Value
	failed := false
	for _, v := range a {
		c.eachValue(v, func(x Value, lit bool) bool {
			switch x.Kind {
			case Error:
				bad, failed = x, true
				return false
			case Number, Bool:
				seen = true
				fn(x.Num != 0)
			case Blank:
				if lit {
					seen = true
					fn(false)
				}
			case Text:
				if lit {
					b, e, ok := c.logic(x)
					if !ok {
						bad, failed = e, true
						return false
					}
					seen = true
					fn(b)
				}
			}
			return true
		})
		if failed {
			return false, bad
		}
	}
	if !seen {
		return false, Err(ErrValue)
	}
	return true, Value{}
}

func fnIf(c *ctx, k []*node) Value {
	cond := c.operand(c.eval(k[0]))
	if cond.Kind == Array {
		yes := BoolOf(true)
		no := BoolOf(false)
		if len(k) > 1 {
			yes = c.operand(c.eval(k[1]))
		}
		if len(k) > 2 {
			no = c.operand(c.eval(k[2]))
		}
		return broadcast([]Value{cond, yes, no}, func(x []Value) Value {
			b, bad, ok := c.logic(x[0])
			if !ok {
				return bad
			}
			if b {
				return x[1]
			}
			return x[2]
		})
	}
	b, bad, ok := c.logic(cond)
	if !ok {
		return bad
	}
	if b {
		if len(k) > 1 {
			return c.eval(k[1])
		}
		return BoolOf(true)
	}
	if len(k) > 2 {
		return c.eval(k[2])
	}
	return BoolOf(false)
}

// fallback makes IFERROR and IFNA.
func fallback(catches func(v Value) bool) func(c *ctx, k []*node) Value {
	return func(c *ctx, k []*node) Value {
		v := c.operand(c.eval(k[0]))
		alt := func() Value {
			if len(k) < 2 {
				return Value{}
			}
			return c.operand(c.eval(k[1]))
		}
		if v.Kind == Array {
			other := alt()
			return broadcast([]Value{v, other}, func(x []Value) Value {
				if catches(x[0]) {
					return x[1]
				}
				return x[0]
			})
		}
		if catches(v) {
			return alt()
		}
		return v
	}
}

func fnIfs(c *ctx, k []*node) Value {
	if len(k)%2 != 0 {
		return Err(ErrValue)
	}
	for i := 0; i+1 < len(k); i += 2 {
		b, bad, ok := c.logic(c.operand(c.eval(k[i])))
		if !ok {
			return bad
		}
		if b {
			return c.eval(k[i+1])
		}
	}
	return Err(ErrNA)
}

func fnSwitch(c *ctx, k []*node) Value {
	v := c.operand(c.eval(k[0]))
	if v.Kind == Error {
		return v
	}
	i := 1
	for ; i+1 < len(k); i += 2 {
		w := c.operand(c.eval(k[i]))
		if w.Kind == Error {
			return w
		}
		if w.Kind == Array {
			w = w.Arr.V[0]
		}
		if compareValues(v, w) == 0 {
			return c.eval(k[i+1])
		}
	}
	if i < len(k) {
		return c.eval(k[i])
	}
	return Err(ErrNA)
}

func fnChoose(c *ctx, k []*node) Value {
	idx := c.operand(c.eval(k[0]))
	pick := func(v Value) Value {
		f, bad, ok := c.num(v)
		if !ok {
			return bad
		}
		i := int(f)
		if i < 1 || i >= len(k) {
			return Err(ErrValue)
		}
		return c.eval(k[i])
	}
	if idx.Kind == Array {
		return c.mapValue(idx, func(v Value) Value {
			r := c.operand(pick(v))
			if r.Kind == Array {
				return r.Arr.V[0]
			}
			return r
		})
	}
	return pick(idx)
}

func registerLogic() {
	lazy("IF", 1, 3, fnIf)
	lazy("IFS", 2, -1, fnIfs)
	lazy("IFERROR", 2, 2, fallback(func(v Value) bool { return v.Kind == Error }))
	lazy("IFNA", 2, 2, fallback(func(v Value) bool { return v.Kind == Error && v.Str == ErrNA }))
	lazy("SWITCH", 3, -1, fnSwitch)
	lazy("CHOOSE", 2, -1, fnChoose)
	ranged("AND", 1, -1, func(c *ctx, a []Value) Value {
		all := true
		ok, bad := c.eachLogic(a, func(b bool) { all = all && b })
		if !ok {
			return bad
		}
		return BoolOf(all)
	})
	ranged("OR", 1, -1, func(c *ctx, a []Value) Value {
		any := false
		ok, bad := c.eachLogic(a, func(b bool) { any = any || b })
		if !ok {
			return bad
		}
		return BoolOf(any)
	})
	ranged("XOR", 1, -1, func(c *ctx, a []Value) Value {
		odd := false
		ok, bad := c.eachLogic(a, func(b bool) { odd = odd != b })
		if !ok {
			return bad
		}
		return BoolOf(odd)
	})
	scalar("NOT", 1, 1, func(c *ctx, a []Value) Value {
		b, bad, ok := c.logic(a[0])
		if !ok {
			return bad
		}
		return BoolOf(!b)
	})
	ranged("TRUE", 0, 0, func(*ctx, []Value) Value { return BoolOf(true) })
	ranged("FALSE", 0, 0, func(*ctx, []Value) Value { return BoolOf(false) })
	ranged("NA", 0, 0, func(*ctx, []Value) Value { return Err(ErrNA) })
	is := func(name string, fn func(v Value) bool) {
		scalar(name, 1, 1, func(_ *ctx, a []Value) Value { return BoolOf(fn(a[0])) })
	}
	is("ISBLANK", func(v Value) bool { return v.Kind == Blank })
	is("ISERROR", func(v Value) bool { return v.Kind == Error })
	is("ISERR", func(v Value) bool { return v.Kind == Error && v.Str != ErrNA })
	is("ISNA", func(v Value) bool { return v.Kind == Error && v.Str == ErrNA })
	is("ISNUMBER", func(v Value) bool { return v.Kind == Number })
	is("ISTEXT", func(v Value) bool { return v.Kind == Text })
	is("ISNONTEXT", func(v Value) bool { return v.Kind != Text })
	is("ISLOGICAL", func(v Value) bool { return v.Kind == Bool })
	parity := func(name string, want int) {
		scalar(name, 1, 1, func(c *ctx, a []Value) Value {
			f, bad, ok := c.num(a[0])
			if !ok {
				return bad
			}
			if f < 0 {
				f = -f
			}
			n := int64(0)
			if f < 9e15 {
				n = int64(f)
			}
			return BoolOf(int(n&1) == want)
		})
	}
	parity("ISEVEN", 0)
	parity("ISODD", 1)
	scalar("N", 1, 1, func(_ *ctx, a []Value) Value {
		switch a[0].Kind {
		case Number, Bool:
			return Num(a[0].Num)
		case Error:
			return a[0]
		}
		return Num(0)
	})
	scalar("T", 1, 1, func(_ *ctx, a []Value) Value {
		switch a[0].Kind {
		case Text, Error:
			return a[0]
		}
		return Str("")
	})
}
