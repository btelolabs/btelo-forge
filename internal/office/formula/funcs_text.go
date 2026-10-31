package formula

import (
	"math"
	"strconv"
	"strings"
	"unicode"
)

// textLen is the length Excel gives a text: UTF-16 code units.
func textLen(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}

// cp1252 is the part of Windows-1252 that differs from Latin-1, codes 128 to 159.
var cp1252 = [32]rune{
	0x20AC, 0x81, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021, 0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x8D, 0x017D,
	0x8F, 0x90, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014, 0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x9D,
	0x017E, 0x0178,
}

func text1(name string, fn func(s string) Value) {
	scalar(name, 1, 1, func(c *ctx, a []Value) Value {
		s, bad, ok := c.text(a[0])
		if !ok {
			return bad
		}
		return fn(s)
	})
}

// sideFn makes LEFT and RIGHT.
func sideFn(left bool) func(c *ctx, a []Value) Value {
	return func(c *ctx, a []Value) Value {
		s, bad, ok := c.text(a[0])
		if !ok {
			return bad
		}
		n := 1.0
		if bad, ok := c.nums(a[1:], &n); !ok {
			return bad
		}
		if n < 0 {
			return Err(ErrValue)
		}
		r := []rune(s)
		k := min(floatInt(n), len(r))
		if left {
			return Str(string(r[:k]))
		}
		return Str(string(r[len(r)-k:]))
	}
}

func fnMid(c *ctx, a []Value) Value {
	s, bad, ok := c.text(a[0])
	if !ok {
		return bad
	}
	var start, n float64
	if bad, ok := c.nums(a[1:], &start, &n); !ok {
		return bad
	}
	if start < 1 || n < 0 {
		return Err(ErrValue)
	}
	r := []rune(s)
	i := floatInt(start) - 1
	if i >= len(r) {
		return Str("")
	}
	return Str(string(r[i:min(len(r), i+min(floatInt(n), len(r)))]))
}

func fnProper(s string) Value {
	var b strings.Builder
	prev := false
	for _, r := range s {
		if prev {
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(unicode.ToUpper(r))
		}
		prev = unicode.IsLetter(r)
	}
	return Str(b.String())
}

func fnTrim(s string) Value {
	var b strings.Builder
	for _, w := range strings.Split(s, " ") {
		if w == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(w)
	}
	return Str(b.String())
}

func fnSubstitute(c *ctx, a []Value) Value {
	s, bad, ok := c.text(a[0])
	if !ok {
		return bad
	}
	from, bad, ok := c.text(a[1])
	if !ok {
		return bad
	}
	to, bad, ok := c.text(a[2])
	if !ok {
		return bad
	}
	if len(a) > 3 && a[3].Kind != Blank {
		n, bad, ok := c.num(a[3])
		if !ok {
			return bad
		}
		if n < 1 {
			return Err(ErrValue)
		}
		if from == "" {
			return Str(s)
		}
		at := 0
		for i := 1; ; i++ {
			j := strings.Index(s[at:], from)
			if j < 0 {
				return Str(s)
			}
			if float64(i) == math.Floor(n) {
				return limited(s[:at+j] + to + s[at+j+len(from):])
			}
			at += j + len(from)
		}
	}
	if from == "" {
		return Str(s)
	}
	if to != "" && strings.Count(s, from)*len(to) > maxText*4 {
		return Err(ErrValue)
	}
	return limited(strings.ReplaceAll(s, from, to))
}

// limited refuses text past Excel's limit.
func limited(s string) Value {
	if len(s) > maxText && textLen(s) > maxText {
		return Err(ErrValue)
	}
	return Str(s)
}

func fnReplace(c *ctx, a []Value) Value {
	s, bad, ok := c.text(a[0])
	if !ok {
		return bad
	}
	var start, n float64
	if bad, ok := c.nums(a[1:3], &start, &n); !ok {
		return bad
	}
	to, bad, ok := c.text(a[3])
	if !ok {
		return bad
	}
	if start < 1 || n < 0 {
		return Err(ErrValue)
	}
	r := []rune(s)
	i := min(floatInt(start)-1, len(r))
	j := min(i+min(floatInt(n), len(r)), len(r))
	return limited(string(r[:i]) + to + string(r[j:]))
}

// finder makes FIND and SEARCH; positions count characters.
func finder(fold bool) func(c *ctx, a []Value) Value {
	return func(c *ctx, a []Value) Value {
		pat, bad, ok := c.text(a[0])
		if !ok {
			return bad
		}
		s, bad, ok := c.text(a[1])
		if !ok {
			return bad
		}
		start := 1.0
		if bad, ok := c.nums(a[2:], &start); !ok {
			return bad
		}
		r := []rune(s)
		if start < 1 || start > float64(len(r)+1) {
			return Err(ErrValue)
		}
		i := int(start) - 1
		if pat == "" {
			return Num(float64(i + 1))
		}
		if fold && hasWild(pat) {
			low := strings.ToLower(pat)
			if !strings.HasSuffix(low, "*") {
				low += "*"
			}
			for ; i < len(r); i++ {
				if wildMatch(low, strings.ToLower(string(r[i:]))) {
					return Num(float64(i + 1))
				}
			}
			return Err(ErrValue)
		}
		rest := string(r[i:])
		if fold {
			rest, pat = strings.ToLower(rest), strings.ToLower(pat)
		}
		j := strings.Index(rest, pat)
		if j < 0 {
			return Err(ErrValue)
		}
		return Num(float64(i + len([]rune(rest[:j])) + 1))
	}
}

func fnConcat(c *ctx, a []Value) Value {
	var b strings.Builder
	var bad Value
	failed := false
	for _, v := range a {
		c.eachValue(v, func(x Value, _ bool) bool {
			s, e, ok := c.text(x)
			if !ok {
				bad, failed = e, true
				return false
			}
			if b.Len()+len(s) > maxText*4 {
				bad, failed = Err(ErrValue), true
				return false
			}
			b.WriteString(s)
			return true
		})
		if failed {
			return bad
		}
	}
	return limited(b.String())
}

func fnConcatenate(c *ctx, a []Value) Value {
	var b strings.Builder
	for _, v := range a {
		s, bad, ok := c.text(v)
		if !ok {
			return bad
		}
		b.WriteString(s)
		if b.Len() > maxText*4 {
			return Err(ErrValue)
		}
	}
	return limited(b.String())
}

func fnTextJoin(c *ctx, a []Value) Value {
	var delims []string
	var bad Value
	failed := false
	c.eachValue(a[0], func(x Value, _ bool) bool {
		s, e, ok := c.text(x)
		if !ok {
			bad, failed = e, true
			return false
		}
		delims = append(delims, s)
		return true
	})
	if failed {
		return bad
	}
	if len(delims) == 0 {
		delims = []string{""}
	}
	skip, bad, ok := c.logic(c.operand(a[1]))
	if !ok {
		return bad
	}
	var b strings.Builder
	count := 0
	add := func(s string) bool {
		if skip && s == "" {
			return true
		}
		if count > 0 {
			b.WriteString(delims[(count-1)%len(delims)])
		}
		count++
		b.WriteString(s)
		return b.Len() <= maxText*4
	}
	for _, v := range a[2:] {
		if v.Kind == Ref && !isUnion(v) && !skip {
			// Blank cells count when they are not skipped, so walk every cell of the used part.
			g := c.gridValue(v)
			if g.Kind == Error {
				return g
			}
			if g.Kind != Array {
				s, e, ok := c.text(g)
				if !ok {
					return e
				}
				add(s)
				continue
			}
			for _, x := range g.Arr.V {
				s, e, ok := c.text(x)
				if !ok {
					return e
				}
				if !add(s) {
					return Err(ErrValue)
				}
			}
			continue
		}
		c.eachValue(v, func(x Value, _ bool) bool {
			s, e, ok := c.text(x)
			if !ok {
				bad, failed = e, true
				return false
			}
			if !add(s) {
				bad, failed = Err(ErrValue), true
				return false
			}
			return true
		})
		if failed {
			return bad
		}
	}
	return limited(b.String())
}

func fnText(c *ctx, a []Value) Value {
	v := a[0]
	if v.Kind == Error {
		return v
	}
	code, bad, ok := c.text(a[1])
	if !ok {
		return bad
	}
	if v.Kind == Blank {
		v = Num(0)
	}
	if v.Kind == Text {
		if f, ok := textToNumber(v.Str, c.date1904()); ok {
			v = Num(f)
		}
	}
	if code == "" {
		return Str("")
	}
	f := cachedFormat(code)
	if f.bad {
		return Err(ErrValue)
	}
	return Str(f.Render(v, c.date1904()).Text)
}

// groupDigits puts thousands commas into a string of digits.
func groupDigits(ds string) string {
	var b strings.Builder
	writeGrouped(&b, ds, 0, true)
	return b.String()
}

// fixedDigits prints a number with a set number of decimals, the way FIXED and DOLLAR do; places may be negative.
func fixedDigits(x float64, places int, commas bool) string {
	d := newDec(math.Abs(x)).round(places, roundHalf)
	ints := d.intStr()
	if ints == "" {
		ints = "0"
	}
	if commas {
		ints = groupDigits(ints)
	}
	if places <= 0 {
		return ints
	}
	return ints + "." + d.fracStr(places)
}

func fnFixed(c *ctx, a []Value) Value {
	x, n, noCommas := 0.0, 2.0, 0.0
	if len(a) > 2 && a[2].Kind != Blank {
		b, bad, ok := c.logic(a[2])
		if !ok {
			return bad
		}
		if b {
			noCommas = 1
		}
	}
	if bad, ok := c.nums(a[:min(len(a), 2)], &x, &n); !ok {
		return bad
	}
	if n > 127 {
		n = 127
	}
	s := fixedDigits(x, int(math.Trunc(n)), noCommas == 0)
	if x < 0 && strings.Trim(s, "0.,") != "" {
		s = "-" + s
	}
	return Str(s)
}

func fnDollar(c *ctx, a []Value) Value {
	x, n := 0.0, 2.0
	if bad, ok := c.nums(a, &x, &n); !ok {
		return bad
	}
	if n > 127 {
		n = 127
	}
	s := "$" + fixedDigits(x, int(math.Trunc(n)), true)
	if x < 0 {
		s = "(" + s + ")"
	}
	return Str(s)
}

func fnNumberValue(c *ctx, a []Value) Value {
	s, bad, ok := c.text(a[0])
	if !ok {
		return bad
	}
	dec, grp := ".", ","
	if len(a) > 1 && a[1].Kind != Blank {
		if dec, bad, ok = c.text(a[1]); !ok {
			return bad
		}
		if r := []rune(dec); len(r) > 1 {
			dec = string(r[:1])
		}
	}
	if len(a) > 2 && a[2].Kind != Blank {
		if grp, bad, ok = c.text(a[2]); !ok {
			return bad
		}
		if r := []rune(grp); len(r) > 1 {
			grp = string(r[:1])
		}
	}
	if dec == "" {
		return Err(ErrValue)
	}
	s = strings.Join(strings.Fields(s), "")
	if s == "" {
		return Num(0)
	}
	pct := 0
	for strings.HasSuffix(s, "%") {
		pct++
		s = s[:len(s)-1]
	}
	i := strings.Index(s, dec)
	whole, frac := s, ""
	if i >= 0 {
		whole, frac = s[:i], s[i+len(dec):]
	}
	if grp != "" {
		if strings.Contains(frac, grp) {
			return Err(ErrValue)
		}
		whole = strings.ReplaceAll(whole, grp, "")
	}
	t := whole
	if i >= 0 {
		t += "." + frac
	}
	if !validNumberText(strings.TrimLeft(t, "+-")) {
		return Err(ErrValue)
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return Err(ErrValue)
	}
	for ; pct > 0; pct-- {
		f /= 100
	}
	return numResult(f)
}

func registerText() {
	ranged("CONCAT", 1, -1, fnConcat)
	scalar("CONCATENATE", 1, -1, fnConcatenate)
	ranged("TEXTJOIN", 3, -1, fnTextJoin)
	scalar("LEFT", 1, 2, sideFn(true))
	scalar("RIGHT", 1, 2, sideFn(false))
	scalar("MID", 3, 3, fnMid)
	text1("LEN", func(s string) Value { return Num(float64(textLen(s))) })
	text1("LOWER", func(s string) Value { return Str(strings.ToLower(s)) })
	text1("UPPER", func(s string) Value { return Str(strings.ToUpper(s)) })
	text1("PROPER", fnProper)
	text1("TRIM", fnTrim)
	text1("CLEAN", func(s string) Value {
		return Str(strings.Map(func(r rune) rune {
			if r < 32 {
				return -1
			}
			return r
		}, s))
	})
	scalar("SUBSTITUTE", 3, 4, fnSubstitute)
	scalar("REPLACE", 4, 4, fnReplace)
	scalar("FIND", 2, 3, finder(false))
	scalar("SEARCH", 2, 3, finder(true))
	scalar("TEXT", 2, 2, fnText)
	scalar("VALUE", 1, 1, func(c *ctx, a []Value) Value {
		switch a[0].Kind {
		case Number:
			return a[0]
		case Bool:
			return Err(ErrValue)
		}
		f, bad, ok := c.num(a[0])
		if !ok {
			return bad
		}
		return Num(f)
	})
	scalar("EXACT", 2, 2, func(c *ctx, a []Value) Value {
		x, bad, ok := c.text(a[0])
		if !ok {
			return bad
		}
		y, bad, ok := c.text(a[1])
		if !ok {
			return bad
		}
		return BoolOf(x == y)
	})
	scalar("REPT", 2, 2, func(c *ctx, a []Value) Value {
		s, bad, ok := c.text(a[0])
		if !ok {
			return bad
		}
		n, bad, ok := c.num(a[1])
		if !ok {
			return bad
		}
		if n < 0 {
			return Err(ErrValue)
		}
		k := math.Floor(n)
		if float64(textLen(s))*k > maxText {
			return Err(ErrValue)
		}
		return Str(strings.Repeat(s, int(k)))
	})
	num1("CHAR", func(x float64) Value {
		n := int(math.Trunc(x))
		if n < 1 || n > 255 {
			return Err(ErrValue)
		}
		if n >= 128 && n < 160 {
			return Str(string(cp1252[n-128]))
		}
		return Str(string(rune(n)))
	})
	num1("UNICHAR", func(x float64) Value {
		n := int(math.Trunc(x))
		if n < 1 || n > 0x10FFFF || (n >= 0xD800 && n <= 0xDFFF) {
			return Err(ErrValue)
		}
		return Str(string(rune(n)))
	})
	text1("CODE", func(s string) Value {
		if s == "" {
			return Err(ErrValue)
		}
		r := []rune(s)[0]
		if r < 128 || (r >= 160 && r <= 255) {
			return Num(float64(r))
		}
		for i, m := range cp1252 {
			if m == r {
				return Num(float64(i + 128))
			}
		}
		return Num('?')
	})
	text1("UNICODE", func(s string) Value {
		if s == "" {
			return Err(ErrValue)
		}
		return Num(float64([]rune(s)[0]))
	})
	scalar("NUMBERVALUE", 1, 3, fnNumberValue)
	scalar("FIXED", 1, 3, fnFixed)
	scalar("DOLLAR", 1, 2, fnDollar)
}
