package formula

type funcKind uint8

const (
	kScalar funcKind = iota // arguments arrive as scalars; arrays broadcast
	kRange                  // arguments arrive as evaluated, references lazy
	kLazy                   // arguments arrive unevaluated
)

// funcDef is one worksheet function.
type funcDef struct {
	name      string
	min, max  int // max < 0 for no limit
	kind      funcKind
	arrayArgs bool // arguments are evaluated as an array formula would
	volatile  bool
	fn        func(c *ctx, a []Value) Value
	lazy      func(c *ctx, k []*node) Value
}

var funcs = map[string]*funcDef{}

func scalar(name string, lo, hi int, fn func(c *ctx, a []Value) Value) *funcDef {
	d := &funcDef{name: name, min: lo, max: hi, kind: kScalar, fn: fn}
	funcs[name] = d
	return d
}

func ranged(name string, lo, hi int, fn func(c *ctx, a []Value) Value) *funcDef {
	d := &funcDef{name: name, min: lo, max: hi, kind: kRange, fn: fn}
	funcs[name] = d
	return d
}

func lazy(name string, lo, hi int, fn func(c *ctx, k []*node) Value) *funcDef {
	d := &funcDef{name: name, min: lo, max: hi, kind: kLazy, lazy: fn}
	funcs[name] = d
	return d
}

func alias(name, of string) { funcs[name] = funcs[of] }

// num1 makes a scalar function of one number.
func num1(name string, fn func(x float64) Value) {
	scalar(name, 1, 1, func(c *ctx, a []Value) Value {
		x, bad, ok := c.num(a[0])
		if !ok {
			return bad
		}
		return fn(x)
	})
}

// num2 makes a scalar function of two numbers.
func num2(name string, fn func(x, y float64) Value) {
	scalar(name, 2, 2, func(c *ctx, a []Value) Value {
		x, bad, ok := c.num(a[0])
		if !ok {
			return bad
		}
		y, bad, ok := c.num(a[1])
		if !ok {
			return bad
		}
		return fn(x, y)
	})
}

// nums coerces the leading arguments into out; arguments past the end, or missing, keep what out holds.
func (c *ctx) nums(a []Value, out ...*float64) (Value, bool) {
	for i, p := range out {
		if i >= len(a) {
			break
		}
		if a[i].Kind == Blank {
			continue
		}
		f, bad, ok := c.num(a[i])
		if !ok {
			return bad, false
		}
		*p = f
	}
	return Value{}, true
}

func init() {
	registerMath()
	registerStats()
	registerCriteria()
	registerLogic()
	registerLookup()
	registerText()
	registerDates()
	registerFinance()
}
