package render

import (
	"math"

	"github.com/btelolabs/btelo-forge/internal/office/pdf"
)

// function is a PDF function; eval returns a new slice or an internal one good until the next call.
type function interface {
	eval(in []float64) []float64
}

func parseFunction(d *pdf.Doc, o pdf.Object, depth int) function {
	if depth > 8 {
		return nil
	}
	if a := d.Array(o); a != nil {
		// An array of one-output functions.
		out := make(multiFunc, 0, len(a))
		for _, e := range a[:min(len(a), 32)] {
			f := parseFunction(d, e, depth+1)
			if f == nil {
				return nil
			}
			out = append(out, f)
		}
		return out
	}
	dict := d.Dict(o)
	if dict == nil {
		if d.Name(o) == "Identity" {
			return identityFunc{}
		}
		return nil
	}
	domain := d.Floats(dict["Domain"])
	rng := d.Floats(dict["Range"])
	var f function
	switch d.Int(dict["FunctionType"], -1) {
	case 0:
		s, _ := d.Resolve(o).(*pdf.Stream)
		if s != nil {
			f = parseSampled(d, s, dict, domain, rng)
		}
	case 2:
		e := &exponential{n: d.Float(dict["N"], 1), c0: d.Floats(dict["C0"]), c1: d.Floats(dict["C1"])}
		if e.c0 == nil {
			e.c0 = []float64{0}
		}
		if e.c1 == nil {
			e.c1 = []float64{1}
		}
		if len(e.c1) < len(e.c0) {
			e.c0 = e.c0[:len(e.c1)]
		}
		f = e
	case 3:
		f = parseStitching(d, dict, domain, depth)
	case 4:
		s, _ := d.Resolve(o).(*pdf.Stream)
		if s != nil {
			body, err := d.Decode(s)
			if err == nil || len(body) > 0 {
				f = parsePostScript(body)
			}
		}
	}
	if f == nil {
		return nil
	}
	return &clamped{f: f, domain: domain, rng: rng}
}

type identityFunc struct{}

func (identityFunc) eval(in []float64) []float64 { return in }

type multiFunc []function

func (m multiFunc) eval(in []float64) []float64 {
	out := make([]float64, 0, len(m))
	for _, f := range m {
		v := f.eval(in)
		if len(v) == 0 {
			out = append(out, 0)
		} else {
			out = append(out, v[0])
		}
	}
	return out
}

type clamped struct {
	f           function
	domain, rng []float64
}

func (c *clamped) eval(in []float64) []float64 {
	x := make([]float64, len(in))
	for i, v := range in {
		if 2*i+1 < len(c.domain) {
			v = min(max(v, c.domain[2*i]), c.domain[2*i+1])
		}
		x[i] = v
	}
	out := c.f.eval(x)
	for i := range out {
		if 2*i+1 < len(c.rng) {
			out[i] = min(max(out[i], c.rng[2*i]), c.rng[2*i+1])
		}
	}
	return out
}

type exponential struct {
	n      float64
	c0, c1 []float64
}

func (e *exponential) eval(in []float64) []float64 {
	if len(in) == 0 {
		return nil
	}
	t := in[0]
	if e.n != 1 {
		t = math.Pow(t, e.n)
	}
	out := make([]float64, len(e.c0))
	for i := range out {
		out[i] = e.c0[i] + t*(e.c1[i]-e.c0[i])
	}
	return out
}

type stitching struct {
	fns            []function
	bounds, encode []float64
	lo, hi         float64
}

func parseStitching(d *pdf.Doc, dict pdf.Dict, domain []float64, depth int) function {
	if len(domain) < 2 {
		return nil
	}
	a := d.Array(dict["Functions"])
	s := &stitching{bounds: d.Floats(dict["Bounds"]), encode: d.Floats(dict["Encode"]), lo: domain[0], hi: domain[1]}
	for _, e := range a[:min(len(a), 4096)] {
		f := parseFunction(d, e, depth+1)
		if f == nil {
			return nil
		}
		s.fns = append(s.fns, f)
	}
	if len(s.fns) == 0 || len(s.bounds) < len(s.fns)-1 || len(s.encode) < 2*len(s.fns) {
		return nil
	}
	return s
}

func (s *stitching) eval(in []float64) []float64 {
	if len(in) == 0 {
		return nil
	}
	x := in[0]
	k := 0
	for k < len(s.fns)-1 && x >= s.bounds[k] {
		k++
	}
	lo, hi := s.lo, s.hi
	if k > 0 {
		lo = s.bounds[k-1]
	}
	if k < len(s.fns)-1 {
		hi = s.bounds[k]
	}
	e0, e1 := s.encode[2*k], s.encode[2*k+1]
	t := e0
	if hi != lo {
		t = e0 + (x-lo)*(e1-e0)/(hi-lo)
	}
	return s.fns[k].eval([]float64{t})
}

type sampled struct {
	size           []int
	domain, encode []float64
	decode         []float64
	outs           int
	samples        []float32 // 0..1
}

func parseSampled(d *pdf.Doc, s *pdf.Stream, dict pdf.Dict, domain, rng []float64) function {
	sizes := d.Floats(dict["Size"])
	m := len(domain) / 2
	n := len(rng) / 2
	bps := d.Int(dict["BitsPerSample"], 8)
	if m == 0 || n == 0 || len(sizes) < m || m > 8 || bps < 1 || bps > 32 {
		return nil
	}
	f := &sampled{domain: domain, outs: n}
	total := n
	for i := range m {
		sz := int(sizes[i])
		if sz < 1 {
			return nil
		}
		f.size = append(f.size, sz)
		total *= sz
		if total > 1<<24 {
			return nil
		}
	}
	f.encode = d.Floats(dict["Encode"])
	if len(f.encode) < 2*m {
		f.encode = nil
		for i := range m {
			f.encode = append(f.encode, 0, float64(f.size[i]-1))
		}
	}
	f.decode = d.Floats(dict["Decode"])
	if len(f.decode) < 2*n {
		f.decode = rng
	}
	data, err := d.Decode(s)
	if err != nil && len(data) == 0 {
		return nil
	}
	f.samples = make([]float32, total)
	top := math.Pow(2, float64(bps)) - 1
	bit := 0
	for i := range total {
		var v uint64
		for b := 0; b < bps; b++ {
			at := bit >> 3
			if at >= len(data) {
				break
			}
			v = v<<1 | uint64(data[at]>>(7-bit&7))&1
			bit++
		}
		f.samples[i] = float32(float64(v) / top)
	}
	return f
}

func (f *sampled) eval(in []float64) []float64 {
	m := len(f.size)
	if len(in) < m {
		return make([]float64, f.outs)
	}
	var i0 [8]int
	var frac [8]float64
	for i := range m {
		lo, hi := f.domain[2*i], f.domain[2*i+1]
		e := f.encode[2*i]
		if hi != lo {
			e += (in[i] - lo) * (f.encode[2*i+1] - f.encode[2*i]) / (hi - lo)
		}
		e = min(max(e, 0), float64(f.size[i]-1))
		i0[i] = min(int(e), f.size[i]-1)
		frac[i] = e - float64(i0[i])
	}
	out := make([]float64, f.outs)
	for corner := 0; corner < 1<<m; corner++ {
		w := 1.0
		at, stride := 0, 1
		for i := range m {
			idx := i0[i]
			if corner>>i&1 != 0 {
				w *= frac[i]
				idx = min(idx+1, f.size[i]-1)
			} else {
				w *= 1 - frac[i]
			}
			at += idx * stride
			stride *= f.size[i]
		}
		if w == 0 {
			continue
		}
		for j := range f.outs {
			out[j] += w * float64(f.samples[at*f.outs+j])
		}
	}
	for j := range f.outs {
		out[j] = f.decode[2*j] + out[j]*(f.decode[2*j+1]-f.decode[2*j])
	}
	return out
}

// psOp is a step of a calculator function: a number, an operator, or a jump for if and ifelse.
type psOp struct {
	kind byte // 'n' number, 'o' operator, 'j' jump if false, 'g' go
	num  float64
	op   string
	to   int
}

type postScript struct{ ops []psOp }

func parsePostScript(b []byte) function {
	l := pdf.NewLexer(b)
	depth := 0
	var parse func() ([]psOp, bool)
	parse = func() ([]psOp, bool) {
		depth++
		defer func() { depth-- }()
		if depth > 64 {
			return nil, false
		}
		var out []psOp
		var blocks [][]psOp
		for {
			o, ok := l.Next()
			if !ok {
				return out, true
			}
			switch v := o.(type) {
			case int64:
				out = append(out, psOp{kind: 'n', num: float64(v)})
			case float64:
				out = append(out, psOp{kind: 'n', num: v})
			case bool:
				n := 0.0
				if v {
					n = 1
				}
				out = append(out, psOp{kind: 'n', num: n})
			case pdf.Keyword:
				switch v {
				case "{":
					blk, ok := parse()
					if !ok {
						return nil, false
					}
					blocks = append(blocks, blk)
				case "}":
					return out, true
				case "if":
					if len(blocks) < 1 {
						return nil, false
					}
					blk := blocks[len(blocks)-1]
					blocks = blocks[:len(blocks)-1]
					out = append(out, psOp{kind: 'j', to: len(blk) + 1})
					out = append(out, blk...)
				case "ifelse":
					if len(blocks) < 2 {
						return nil, false
					}
					a, b := blocks[len(blocks)-2], blocks[len(blocks)-1]
					blocks = blocks[:len(blocks)-2]
					out = append(out, psOp{kind: 'j', to: len(a) + 2})
					out = append(out, a...)
					out = append(out, psOp{kind: 'g', to: len(b) + 1})
					out = append(out, b...)
				default:
					out = append(out, psOp{kind: 'o', op: string(v)})
				}
			}
		}
	}
	// The whole program is one block in braces.
	if o, ok := l.Next(); !ok || o != pdf.Keyword("{") {
		return nil
	}
	ops, ok := parse()
	if !ok {
		return nil
	}
	return &postScript{ops: ops}
}

func (p *postScript) eval(in []float64) []float64 {
	st := make([]float64, 0, 32)
	st = append(st, in...)
	pop := func() float64 {
		if len(st) == 0 {
			return 0
		}
		v := st[len(st)-1]
		st = st[:len(st)-1]
		return v
	}
	push := func(v float64) {
		if len(st) < 256 {
			st = append(st, v)
		}
	}
	b2f := func(b bool) float64 {
		if b {
			return 1
		}
		return 0
	}
	for i := 0; i < len(p.ops); i++ {
		o := &p.ops[i]
		switch o.kind {
		case 'n':
			push(o.num)
		case 'j':
			if pop() == 0 {
				i += o.to - 1
			}
		case 'g':
			i += o.to - 1
		case 'o':
			switch o.op {
			case "add":
				b, a := pop(), pop()
				push(a + b)
			case "sub":
				b, a := pop(), pop()
				push(a - b)
			case "mul":
				b, a := pop(), pop()
				push(a * b)
			case "div":
				b, a := pop(), pop()
				if b == 0 {
					push(0)
				} else {
					push(a / b)
				}
			case "idiv":
				b, a := int64(pop()), int64(pop())
				if b == 0 {
					push(0)
				} else {
					push(float64(a / b))
				}
			case "mod":
				b, a := int64(pop()), int64(pop())
				if b == 0 {
					push(0)
				} else {
					push(float64(a % b))
				}
			case "neg":
				push(-pop())
			case "abs":
				push(math.Abs(pop()))
			case "ceiling":
				push(math.Ceil(pop()))
			case "floor":
				push(math.Floor(pop()))
			case "round":
				push(math.Floor(pop() + 0.5))
			case "truncate", "cvi":
				push(math.Trunc(pop()))
			case "cvr":
			case "sqrt":
				push(math.Sqrt(math.Max(pop(), 0)))
			case "sin":
				push(math.Sin(pop() * math.Pi / 180))
			case "cos":
				push(math.Cos(pop() * math.Pi / 180))
			case "atan":
				b, a := pop(), pop()
				v := math.Atan2(a, b) * 180 / math.Pi
				if v < 0 {
					v += 360
				}
				push(v)
			case "exp":
				b, a := pop(), pop()
				push(math.Pow(a, b))
			case "ln":
				push(math.Log(math.Max(pop(), 1e-300)))
			case "log":
				push(math.Log10(math.Max(pop(), 1e-300)))
			case "eq":
				push(b2f(pop() == pop()))
			case "ne":
				push(b2f(pop() != pop()))
			case "gt":
				b, a := pop(), pop()
				push(b2f(a > b))
			case "ge":
				b, a := pop(), pop()
				push(b2f(a >= b))
			case "lt":
				b, a := pop(), pop()
				push(b2f(a < b))
			case "le":
				b, a := pop(), pop()
				push(b2f(a <= b))
			case "and":
				b, a := int64(pop()), int64(pop())
				push(float64(a & b))
			case "or":
				b, a := int64(pop()), int64(pop())
				push(float64(a | b))
			case "xor":
				b, a := int64(pop()), int64(pop())
				push(float64(a ^ b))
			case "not":
				push(b2f(pop() == 0))
			case "bitshift":
				b, a := int64(pop()), int64(pop())
				if b >= 0 {
					push(float64(a << min(b, 62)))
				} else {
					push(float64(a >> min(-b, 62)))
				}
			case "pop":
				pop()
			case "exch":
				b, a := pop(), pop()
				push(b)
				push(a)
			case "dup":
				a := pop()
				push(a)
				push(a)
			case "copy":
				n := int(pop())
				if n > 0 && n <= len(st) && len(st)+n <= 256 {
					st = append(st, st[len(st)-n:]...)
				}
			case "index":
				n := int(pop())
				if n >= 0 && n < len(st) {
					push(st[len(st)-1-n])
				}
			case "roll":
				j, n := int(pop()), int(pop())
				if n > 0 && n <= len(st) {
					part := st[len(st)-n:]
					j = (j%n + n) % n
					tmp := make([]float64, n)
					for k := range n {
						tmp[(k+j)%n] = part[k]
					}
					copy(part, tmp)
				}
			case "true":
				push(1)
			case "false":
				push(0)
			}
		}
	}
	return st
}
