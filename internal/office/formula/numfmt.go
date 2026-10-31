package formula

import (
	"math"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// Format is a parsed number format code. It is immutable and safe to share.
type Format struct {
	code     string
	sections []*section
	pos      *section
	neg      *section
	zero     *section
	text     *section // nil when the text shows itself
	bad      bool     // the code did not parse and renders as General
	ifs      bool     // the sections are chosen by conditions
}

// Rendered is a value formatted for display.
type Rendered struct {
	Text     string // what shows, with a '*' fill removed
	Color    [3]uint8
	HasColor bool // a [Red] or [Color12] section colour
	Fill     rune // the character of a '*' repeat, 0 for none
	FillAt   int  // byte offset in Text where the repeat goes
	Numeric  bool // the value showed as a number or date, false for text
}

type secKind uint8

const (
	secEmpty secKind = iota
	secGeneral
	secNumber
	secDate
	secText
)

type ftKind uint8

const (
	ftLit ftKind = iota
	ftDigit
	ftDot
	ftComma
	ftPercent
	ftExp
	ftSlash
	ftAt
	ftFill
	ftGeneral
	ftYear
	ftMonth
	ftDay
	ftHour
	ftMin
	ftSec
	ftSub
	ftAMPM
	ftElapsedH
	ftElapsedM
	ftElapsedS
	ftCnWeek
	ftFracNum
	ftFracDen
)

type ftok struct {
	kind ftKind
	s    string // literal text, AM/PM spelling, or the exponent's sign
	r    rune   // digit placeholder or fill character
	n    int    // how many letters
}

type section struct {
	kind     secKind
	toks     []ftok
	hasCond  bool
	condOp   byte
	condVal  float64
	color    [3]uint8
	hasColor bool

	intDigits  int
	fracDigits int
	percent    int
	scale      int
	thousands  bool
	expDigits  int
	isExp      bool

	isFrac   bool
	numW     int
	denW     int
	numPad   rune
	denPad   rune
	fixedDen int
	hasInt   bool

	hour12  bool
	subsec  int
	elapsed bool
}

// The Excel palette behind [Color1] to [Color56].
var excelPalette = [56]uint32{
	0x000000, 0xFFFFFF, 0xFF0000, 0x00FF00, 0x0000FF, 0xFFFF00, 0xFF00FF, 0x00FFFF,
	0x800000, 0x008000, 0x000080, 0x808000, 0x800080, 0x008080, 0xC0C0C0, 0x808080,
	0x9999FF, 0x993366, 0xFFFFCC, 0xCCFFFF, 0x660066, 0xFF8080, 0x0066CC, 0xCCCCFF,
	0x000080, 0xFF00FF, 0xFFFF00, 0x00FFFF, 0x800080, 0x800000, 0x008080, 0x0000FF,
	0x00CCFF, 0xCCFFFF, 0xCCFFCC, 0xFFFF99, 0x99CCFF, 0xFF99CC, 0xCC99FF, 0xFFCC99,
	0x3366FF, 0x33CCCC, 0x99CC00, 0xFFCC00, 0xFF9900, 0xFF6600, 0x666699, 0x969696,
	0x003366, 0x339966, 0x003300, 0x333300, 0x993300, 0x993366, 0x333399, 0x333333,
}

var namedColors = map[string]uint32{
	"black": 0x000000, "white": 0xFFFFFF, "red": 0xFF0000, "green": 0x00FF00,
	"blue": 0x0000FF, "yellow": 0xFFFF00, "magenta": 0xFF00FF, "cyan": 0x00FFFF,
	"黑色": 0x000000, "白色": 0xFFFFFF, "红色": 0xFF0000, "绿色": 0x00FF00,
	"蓝色": 0x0000FF, "黄色": 0xFFFF00, "洋红": 0xFF00FF, "青色": 0x00FFFF,
}

func rgbOf(c uint32) [3]uint8 { return [3]uint8{uint8(c >> 16), uint8(c >> 8), uint8(c)} }

// ParseFormat parses a format code. It never fails: a broken code renders as General.
func ParseFormat(code string) *Format {
	f := &Format{code: code}
	if strings.TrimSpace(code) == "" {
		f.useGeneral()
		return f
	}
	for _, part := range splitSections(code) {
		s, ok := parseSection(part)
		if !ok {
			f.bad = true
			f.useGeneral()
			return f
		}
		f.sections = append(f.sections, s)
	}
	if len(f.sections) > 4 {
		f.bad = true
		f.useGeneral()
		return f
	}
	f.assign()
	return f
}

var generalSection = &section{kind: secGeneral, toks: []ftok{{kind: ftGeneral}}}

func (f *Format) useGeneral() {
	f.sections = []*section{generalSection}
	f.pos, f.neg, f.zero = generalSection, generalSection, generalSection
	f.text = nil
}

// assign decides which section shows which sign of value.
func (f *Format) assign() {
	s := f.sections
	n := len(s)
	for _, x := range s {
		f.ifs = f.ifs || x.hasCond
	}
	if n == 4 {
		f.text = s[3]
		s = s[:3]
		n = 3
	} else if s[n-1].kind == secText {
		f.text = s[n-1]
		n--
		s = s[:n]
		if n == 0 {
			f.pos, f.neg, f.zero = generalSection, generalSection, generalSection
			return
		}
	}
	switch n {
	case 1:
		f.pos, f.neg, f.zero = s[0], s[0], s[0]
	case 2:
		f.pos, f.neg, f.zero = s[0], s[1], s[0]
	default:
		f.pos, f.neg, f.zero = s[0], s[1], s[2]
	}
}

// IsDate says the format shows numbers as dates or times.
func (f *Format) IsDate() bool { return f.pos.kind == secDate }

// IsText says the code is @.
func (f *Format) IsText() bool {
	return f.text != nil && len(f.sections) == 1 && len(f.text.toks) == 1 && f.text.toks[0].kind == ftAt
}

// IsGeneral says the format is plain General.
func (f *Format) IsGeneral() bool {
	return len(f.sections) == 1 && f.text == nil && f.pos.kind == secGeneral && len(f.pos.toks) == 1 && !f.pos.hasColor
}

func splitSections(code string) []string {
	var out []string
	start := 0
	for i := 0; i < len(code); i++ {
		switch code[i] {
		case '\\', '_', '*':
			i++
		case '"':
			for i++; i < len(code) && code[i] != '"'; i++ {
			}
		case '[':
			for i++; i < len(code) && code[i] != ']'; i++ {
			}
		case ';':
			out = append(out, code[start:i])
			start = i + 1
		}
	}
	return append(out, code[start:])
}

func addLit(toks []ftok, s string) []ftok {
	if n := len(toks); n > 0 && toks[n-1].kind == ftLit {
		toks[n-1].s += s
		return toks
	}
	return append(toks, ftok{kind: ftLit, s: s})
}

func countRun(s string, i int) int {
	c := s[i] | 0x20
	j := i
	for j < len(s) && (s[j]|0x20) == c {
		j++
	}
	return j - i
}

// parseSection tokenises one section. It reports false on a code Excel would refuse.
func parseSection(code string) (*section, bool) {
	s := &section{}
	var toks []ftok
	i := 0
	for i < len(code) {
		c := code[i]
		switch c {
		case '"':
			j := i + 1
			for j < len(code) && code[j] != '"' {
				j++
			}
			toks = addLit(toks, code[i+1:j])
			i = j + 1
		case '\\':
			if i+1 >= len(code) {
				i++
				continue
			}
			_, n := utf8.DecodeRuneInString(code[i+1:])
			toks = addLit(toks, code[i+1:i+1+n])
			i += 1 + n
		case '_':
			if i+1 >= len(code) {
				i++
				continue
			}
			_, n := utf8.DecodeRuneInString(code[i+1:])
			toks = addLit(toks, " ")
			i += 1 + n
		case '*':
			if i+1 >= len(code) {
				i++
				continue
			}
			r, n := utf8.DecodeRuneInString(code[i+1:])
			toks = append(toks, ftok{kind: ftFill, r: r})
			i += 1 + n
		case '[':
			j := strings.IndexByte(code[i:], ']')
			if j < 0 {
				return nil, false
			}
			next, ok := s.bracket(code[i+1:i+j], toks)
			if !ok {
				return nil, false
			}
			toks = next
			i += j + 1
		case '0', '#', '?':
			toks = append(toks, ftok{kind: ftDigit, r: rune(c)})
			i++
		case '.':
			if n := len(toks); n > 0 && (toks[n-1].kind == ftSec || toks[n-1].kind == ftElapsedS) {
				j := i + 1
				for j < len(code) && code[j] == '0' {
					j++
				}
				if j > i+1 {
					toks = append(toks, ftok{kind: ftSub, n: j - i - 1})
					s.subsec = max(s.subsec, j-i-1)
					i = j
					continue
				}
			}
			toks = append(toks, ftok{kind: ftDot})
			i++
		case ',':
			toks = append(toks, ftok{kind: ftComma})
			i++
		case '%':
			toks = append(toks, ftok{kind: ftPercent})
			i++
		case '/':
			toks = append(toks, ftok{kind: ftSlash})
			i++
		case '@':
			toks = append(toks, ftok{kind: ftAt})
			i++
		case 'E', 'e':
			if i+1 < len(code) && (code[i+1] == '+' || code[i+1] == '-') {
				toks = append(toks, ftok{kind: ftExp, s: code[i : i+2]})
				i += 2
				continue
			}
			n := countRun(code, i)
			toks = append(toks, ftok{kind: ftYear, n: 4})
			i += n
		case 'y', 'Y':
			n := countRun(code, i)
			toks = append(toks, ftok{kind: ftYear, n: n})
			i += n
		case 'm', 'M':
			n := countRun(code, i)
			toks = append(toks, ftok{kind: ftMonth, n: n})
			i += n
		case 'd', 'D':
			n := countRun(code, i)
			toks = append(toks, ftok{kind: ftDay, n: n})
			i += n
		case 'h', 'H':
			n := countRun(code, i)
			toks = append(toks, ftok{kind: ftHour, n: n})
			i += n
		case 's', 'S':
			n := countRun(code, i)
			toks = append(toks, ftok{kind: ftSec, n: n})
			i += n
		case 'a', 'A':
			switch {
			case len(code)-i >= 5 && strings.EqualFold(code[i:i+5], "AM/PM"):
				toks = append(toks, ftok{kind: ftAMPM, s: "AM/PM"})
				s.hour12 = true
				i += 5
			case len(code)-i >= 3 && strings.EqualFold(code[i:i+3], "A/P"):
				toks = append(toks, ftok{kind: ftAMPM, s: code[i : i+3]})
				s.hour12 = true
				i += 3
			default:
				n := countRun(code, i)
				if n < 3 {
					return nil, false
				}
				toks = append(toks, ftok{kind: ftCnWeek, n: n})
				i += n
			}
		case 'g', 'G':
			if len(code)-i >= 7 && strings.EqualFold(code[i:i+7], "General") {
				toks = append(toks, ftok{kind: ftGeneral})
				i += 7
				continue
			}
			if strings.HasPrefix(code[i:], "G/通用格式") {
				toks = append(toks, ftok{kind: ftGeneral})
				i += len("G/通用格式")
				continue
			}
			return nil, false
		case '$', '-', '+', '(', ')', ':', ' ', '!', '^', '&', '\'', '~', '{', '}', '<', '>', '=', '|',
			'1', '2', '3', '4', '5', '6', '7', '8', '9':
			toks = addLit(toks, string(c))
			i++
		default:
			if c >= 0x80 {
				if strings.HasPrefix(code[i:], "上午/下午") {
					toks = append(toks, ftok{kind: ftAMPM, s: "上午/下午"})
					s.hour12 = true
					i += len("上午/下午")
					continue
				}
				_, n := utf8.DecodeRuneInString(code[i:])
				toks = addLit(toks, code[i:i+n])
				i += n
				continue
			}
			return nil, false
		}
	}
	s.toks = toks
	s.classify()
	return s, true
}

// bracket handles what is between [ and ]: a colour, a condition, elapsed time, a locale or currency.
func (s *section) bracket(in string, toks []ftok) ([]ftok, bool) {
	low := strings.ToLower(in)
	if c, ok := namedColors[low]; ok {
		s.color, s.hasColor = rgbOf(c), true
		return toks, true
	}
	if strings.HasPrefix(low, "color") {
		n, err := strconv.Atoi(strings.TrimSpace(low[5:]))
		if err == nil && n >= 1 && n <= 56 {
			s.color, s.hasColor = rgbOf(excelPalette[n-1]), true
			return toks, true
		}
		return toks, false
	}
	if in != "" && strings.IndexByte("<>=", in[0]) >= 0 {
		op := in[0]
		rest := in[1:]
		switch {
		case strings.HasPrefix(in, "<="):
			op, rest = opLE, in[2:]
		case strings.HasPrefix(in, ">="):
			op, rest = opGE, in[2:]
		case strings.HasPrefix(in, "<>"):
			op, rest = opNE, in[2:]
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
		if err != nil {
			return toks, false
		}
		s.hasCond, s.condOp, s.condVal = true, op, v
		return toks, true
	}
	if in != "" && in[0] == '$' {
		sym, _, _ := strings.Cut(in[1:], "-")
		if sym != "" {
			toks = addLit(toks, sym)
		}
		return toks, true
	}
	if isElapsed(low, 'h') {
		return append(toks, ftok{kind: ftElapsedH, n: len(low)}), true
	}
	if isElapsed(low, 'm') {
		return append(toks, ftok{kind: ftElapsedM, n: len(low)}), true
	}
	if isElapsed(low, 's') {
		return append(toks, ftok{kind: ftElapsedS, n: len(low)}), true
	}
	// [DBNum1], [ENG], [JpnNum] and the like change nothing here.
	return toks, true
}

func isElapsed(s string, c byte) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != c {
			return false
		}
	}
	return true
}

// classify works out what the section shows and prepares its tokens.
func (s *section) classify() {
	var date, at, gen, digit bool
	for i := range s.toks {
		switch s.toks[i].kind {
		case ftYear, ftMonth, ftDay, ftHour, ftSec, ftAMPM, ftCnWeek, ftSub:
			date = true
		case ftElapsedH, ftElapsedM, ftElapsedS:
			date = true
			s.elapsed = true
		case ftAt:
			at = true
		case ftGeneral:
			gen = true
		case ftDigit:
			digit = true
		}
	}
	switch {
	case date:
		s.kind = secDate
		s.prepareDate()
	case at:
		s.kind = secText
	case gen:
		s.kind = secGeneral
	case len(s.toks) == 0:
		s.kind = secEmpty
	default:
		s.kind = secNumber
		s.prepareNumber(digit)
	}
}

// prepareDate turns the m of minutes into minutes and leftover number tokens into literals.
func (s *section) prepareDate() {
	last := -1
	for i := range s.toks {
		t := &s.toks[i]
		switch t.kind {
		case ftMonth:
			if last >= 0 && t.n <= 2 {
				if k := s.toks[last].kind; k == ftHour || k == ftElapsedH {
					t.kind = ftMin
				}
			}
			last = i
		case ftSec, ftElapsedS:
			if last >= 0 && s.toks[last].kind == ftMonth && s.toks[last].n <= 2 {
				s.toks[last].kind = ftMin
			}
			last = i
		case ftYear, ftDay, ftHour, ftElapsedH, ftElapsedM:
			last = i
		case ftDigit:
			t.kind, t.s = ftLit, string(t.r)
		case ftDot:
			t.kind, t.s = ftLit, "."
		case ftComma:
			t.kind, t.s = ftLit, ","
		case ftPercent:
			t.kind, t.s = ftLit, "%"
		case ftSlash:
			t.kind, t.s = ftLit, "/"
		case ftExp:
			t.kind = ftLit
		}
	}
}

func isDigitTok(t *ftok) bool { return t.kind == ftDigit }

// prepareNumber settles commas, the exponent and fractions, and counts the placeholders.
func (s *section) prepareNumber(digit bool) {
	toks := s.toks
	if digit && s.prepareFraction() {
		return
	}
	// Resolve the commas: one between placeholders asks for thousands, one after the last scales by 1000.
	out := toks[:0:0]
	seenDigit := false
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.kind != ftComma {
			if t.kind == ftDigit {
				seenDigit = true
			}
			out = append(out, t)
			continue
		}
		if !seenDigit {
			out = addLit(out, ",")
			continue
		}
		j := i
		for j < len(toks) && toks[j].kind == ftComma {
			j++
		}
		if j < len(toks) && isDigitTok(&toks[j]) {
			s.thousands = true
		} else {
			s.scale += j - i
		}
		i = j - 1
	}
	// Placeholders after the exponent are its width.
	toks = out
	out = toks[:0:0]
	inExp := false
	dot := false
	for i := range toks {
		t := toks[i]
		switch t.kind {
		case ftExp:
			if digit && !inExp {
				inExp, s.isExp = true, true
				out = append(out, t)
			} else {
				out = addLit(out, t.s)
			}
			continue
		case ftDigit:
			if inExp {
				if t.r == '0' {
					s.expDigits++
				}
				continue
			}
			if dot {
				s.fracDigits++
			} else {
				s.intDigits++
			}
		case ftDot:
			if dot || inExp {
				out = addLit(out, ".")
				continue
			}
			dot = true
		case ftPercent:
			s.percent++
			out = append(out, ftok{kind: ftLit, s: "%"})
			continue
		case ftSlash:
			out = addLit(out, "/")
			continue
		}
		out = append(out, t)
	}
	s.toks = out
}

// prepareFraction recognises # ?/?, ??/16 and their kin.
func (s *section) prepareFraction() bool {
	toks := s.toks
	slash := -1
	for i := range toks {
		if toks[i].kind == ftSlash {
			slash = i
			break
		}
	}
	if slash <= 0 || toks[slash-1].kind != ftDigit {
		return false
	}
	start := slash
	for start > 0 && toks[start-1].kind == ftDigit {
		start--
	}
	end := slash + 1
	var den strings.Builder
	var denPad rune
	for end < len(toks) {
		t := toks[end]
		if t.kind == ftDigit {
			den.WriteRune(t.r)
			if denPad == 0 {
				denPad = t.r
			}
		} else if t.kind == ftLit && allDigits(t.s) {
			den.WriteString(t.s)
		} else {
			break
		}
		end++
	}
	d := den.String()
	if d == "" {
		return false
	}
	s.isFrac = true
	s.numW = slash - start
	s.numPad = toks[start].r
	s.denW = len(d)
	s.denPad = denPad
	if allDigits(d) && d[0] != '0' {
		s.fixedDen, _ = strconv.Atoi(d)
		s.denPad = '0'
	}
	out := toks[:0:0]
	for i := 0; i < start; i++ {
		t := toks[i]
		switch t.kind {
		case ftDigit:
			s.hasInt = true
			s.intDigits++
		case ftComma:
			t = ftok{kind: ftLit, s: ","}
		case ftDot:
			t = ftok{kind: ftLit, s: "."}
		case ftSlash:
			t = ftok{kind: ftLit, s: "/"}
		case ftPercent:
			s.percent++
			t = ftok{kind: ftLit, s: "%"}
		}
		out = append(out, t)
	}
	out = append(out, ftok{kind: ftFracNum}, ftok{kind: ftFracDen})
	for i := end; i < len(toks); i++ {
		t := toks[i]
		switch t.kind {
		case ftDigit:
			t = ftok{kind: ftLit, s: string(t.r)}
		case ftComma:
			t = ftok{kind: ftLit, s: ","}
		case ftDot:
			t = ftok{kind: ftLit, s: "."}
		case ftSlash:
			t = ftok{kind: ftLit, s: "/"}
		case ftPercent:
			s.percent++
			t = ftok{kind: ftLit, s: "%"}
		}
		out = append(out, t)
	}
	s.toks = out
	return true
}

func (s *section) test(v float64) bool {
	switch s.condOp {
	case '<':
		return v < s.condVal
	case '>':
		return v > s.condVal
	case '=':
		return v == s.condVal
	case opLE:
		return v <= s.condVal
	case opGE:
		return v >= s.condVal
	case opNE:
		return v != s.condVal
	}
	return false
}

// negOnly says the section's condition admits only negative numbers, so it does not need a minus sign.
func (s *section) negOnly() bool {
	if !s.hasCond {
		return false
	}
	return (s.condOp == '<' && s.condVal <= 0) || (s.condOp == opLE && s.condVal <= 0)
}

// choose picks the section for a number, and says whether the number shows a minus sign of its own.
func (f *Format) choose(v float64) (*section, bool) {
	if !f.ifs {
		switch {
		case v > 0:
			return f.pos, false
		case v < 0:
			return f.neg, f.neg == f.pos
		}
		return f.zero, false
	}
	secs := f.sections
	if f.text != nil && len(secs) > 0 && secs[len(secs)-1] == f.text {
		secs = secs[:len(secs)-1]
	}
	minus := func(s *section) bool { return v < 0 && !s.negOnly() }
	for i, s := range secs {
		if i == len(secs)-1 {
			break
		}
		if !s.hasCond {
			// Default conditions: the first section takes positives, the second negatives.
			if (i == 0 && v > 0) || (i == 1 && v < 0) {
				return s, minus(s) && i == 0
			}
			continue
		}
		if s.test(v) {
			return s, minus(s)
		}
	}
	if len(secs) == 0 {
		return f.pos, v < 0
	}
	last := secs[len(secs)-1]
	if last.hasCond && !last.test(v) {
		// Nothing fits: Excel supplies a General section at the end.
		return generalSection, v < 0
	}
	if len(secs) >= 2 && secs[0].hasCond && secs[0].condVal == 0 && !last.hasCond &&
		(secs[0].condOp == '>' || secs[0].condOp == opGE) {
		return last, false
	}
	return last, v < 0 && !last.negOnly()
}

// Render formats a value.
func (f *Format) Render(v Value, date1904 bool) Rendered {
	switch v.Kind {
	case Blank, Ref:
		return Rendered{}
	case Array:
		if v.Arr != nil && len(v.Arr.V) > 0 {
			return f.Render(v.Arr.V[0], date1904)
		}
		return Rendered{}
	case Bool:
		if v.Num != 0 {
			return Rendered{Text: "TRUE"}
		}
		return Rendered{Text: "FALSE"}
	case Error:
		return Rendered{Text: v.Str}
	case Text:
		if f.text == nil {
			return Rendered{Text: v.Str}
		}
		return f.text.renderText(v.Str)
	}
	x := v.Num
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return Rendered{Text: ErrNum}
	}
	s, minus := f.choose(x)
	var o output
	if s.hasColor {
		o.r.Color, o.r.HasColor = s.color, true
	}
	o.r.Numeric = true
	abs := math.Abs(x)
	switch s.kind {
	case secEmpty:
		return o.r
	case secText:
		if minus {
			o.b.WriteByte('-')
		}
		o.tokens(s.toks, General(abs, 0))
	case secGeneral:
		if minus {
			o.b.WriteByte('-')
		}
		o.tokens(s.toks, General(abs, 0))
	case secDate:
		if !s.renderDate(x, date1904, &o) {
			r := o.r
			r.Text, r.Fill, r.FillAt = "########", 0, 0
			return r
		}
	case secNumber:
		if minus {
			o.b.WriteByte('-')
		}
		s.renderNumber(abs, &o)
	}
	o.r.Text = o.b.String()
	return o.r
}

// output gathers the text and where the fill goes.
type output struct {
	b strings.Builder
	r Rendered
}

func (o *output) fill(c rune) {
	if o.r.Fill == 0 {
		o.r.Fill, o.r.FillAt = c, o.b.Len()
	}
}

// tokens writes literals and fills, with general standing for the General and @ placeholders.
func (o *output) tokens(toks []ftok, general string) {
	for i := range toks {
		t := &toks[i]
		switch t.kind {
		case ftLit:
			o.b.WriteString(t.s)
		case ftFill:
			o.fill(t.r)
		case ftGeneral, ftAt:
			o.b.WriteString(general)
		}
	}
}

func (s *section) renderText(v string) Rendered {
	var o output
	if s.hasColor {
		o.r.Color, o.r.HasColor = s.color, true
	}
	o.tokens(s.toks, v)
	o.r.Text = o.b.String()
	return o.r
}

// dec is a decimal number: 0.d1d2d3... times 10^e, with no leading or trailing zero digits.
type dec struct {
	d []byte
	e int
}

// newDec holds a non-negative number to 15 significant digits, as Excel does.
func newDec(x float64) dec {
	if x == 0 {
		return dec{}
	}
	var buf [32]byte
	b := strconv.AppendFloat(buf[:0], x, 'e', 14, 64)
	// b is d.ddddddddddddddde±xx
	i := len(b) - 1
	for b[i] != 'e' {
		i--
	}
	exp, _ := strconv.Atoi(string(b[i+1:]))
	d := make([]byte, 0, 15)
	d = append(d, b[0])
	d = append(d, b[2:i]...)
	for len(d) > 0 && d[len(d)-1] == '0' {
		d = d[:len(d)-1]
	}
	return dec{d: d, e: exp + 1}
}

// Rounding modes of dec.round.
const (
	roundHalf = iota
	roundUp
	roundDown
)

// round keeps the given number of places after the decimal point; a negative count rounds left of the point.
func (d dec) round(places, mode int) dec {
	keep := d.e + places
	if keep >= len(d.d) || len(d.d) == 0 {
		return d
	}
	if keep < 0 {
		if mode == roundUp {
			return dec{d: []byte{'1'}, e: 1 - places}
		}
		return dec{}
	}
	up := false
	switch mode {
	case roundHalf:
		up = d.d[keep] >= '5'
	case roundUp:
		up = true
	}
	out := make([]byte, keep, keep+1)
	copy(out, d.d[:keep])
	e := d.e
	if up {
		i := keep - 1
		for ; i >= 0; i-- {
			if out[i] != '9' {
				out[i]++
				break
			}
			out[i] = '0'
		}
		if i < 0 {
			out = append([]byte{'1'}, out...)
			e++
		}
	}
	for len(out) > 0 && out[len(out)-1] == '0' {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return dec{}
	}
	return dec{d: out, e: e}
}

func (d dec) shift(k int) dec { return dec{d: d.d, e: d.e + k} }

// intStr gives the digits before the point, "" for none.
func (d dec) intStr() string {
	if d.e <= 0 || len(d.d) == 0 {
		return ""
	}
	if d.e <= len(d.d) {
		return string(d.d[:d.e])
	}
	return string(d.d) + strings.Repeat("0", d.e-len(d.d))
}

// fracStr gives exactly n digits after the point.
func (d dec) fracStr(n int) string {
	b := make([]byte, n)
	for i := range b {
		p := d.e + i
		if p >= 0 && p < len(d.d) {
			b[i] = d.d[p]
		} else {
			b[i] = '0'
		}
	}
	return string(b)
}

func (d dec) float() float64 {
	if len(d.d) == 0 {
		return 0
	}
	f, _ := strconv.ParseFloat("0."+string(d.d)+"e"+strconv.Itoa(d.e), 64)
	return f
}

func (s *section) renderNumber(abs float64, o *output) {
	d := newDec(abs).shift(2*s.percent - 3*s.scale)
	if s.isFrac {
		s.renderFraction(d, o)
		return
	}
	expo := 0
	if s.isExp {
		k := max(s.intDigits, 1)
		if len(d.d) > 0 {
			expo = floorDiv(d.e-1, k) * k
			m := d.shift(-expo).round(s.fracDigits, roundHalf)
			if m.e > k {
				expo += k
				m = d.shift(-expo).round(s.fracDigits, roundHalf)
			}
			d = m
		}
	} else {
		d = d.round(s.fracDigits, roundHalf)
	}
	ints := d.intStr()
	fracs := d.fracStr(s.fracDigits)
	k, j := 0, 0
	wroteInts := s.intDigits > 0 || s.fracDigits == 0
	flushInts := func() {
		if !wroteInts {
			wroteInts = true
			writeGrouped(&o.b, ints, 0, s.thousands)
		}
	}
	for i := range s.toks {
		t := &s.toks[i]
		switch t.kind {
		case ftLit:
			o.b.WriteString(t.s)
		case ftFill:
			o.fill(t.r)
		case ftDigit:
			if k < s.intDigits {
				s.placeInt(&o.b, t.r, k, ints)
				k++
				continue
			}
			c := fracs[j]
			j++
			if c == '0' && t.r != '0' && strings.Trim(fracs[j-1:], "0") == "" {
				if t.r == '?' {
					o.b.WriteByte(' ')
				}
				continue
			}
			o.b.WriteByte(c)
		case ftDot:
			flushInts()
			o.b.WriteByte('.')
		case ftExp:
			flushInts()
			o.b.WriteByte(t.s[0])
			switch {
			case expo < 0:
				o.b.WriteByte('-')
			case t.s[1] == '+':
				o.b.WriteByte('+')
			}
			e := strconv.Itoa(max(expo, -expo))
			for n := len(e); n < s.expDigits; n++ {
				o.b.WriteByte('0')
			}
			o.b.WriteString(e)
		}
	}
	flushInts()
}

// placeInt writes the k-th integer placeholder: digits fill from the right, the first takes any overflow.
func (s *section) placeInt(b *strings.Builder, ph rune, k int, ints string) {
	pos := s.intDigits - 1 - k
	if k == 0 && len(ints) > s.intDigits {
		writeGrouped(b, ints[:len(ints)-pos], pos, s.thousands)
		return
	}
	sep := s.thousands && pos > 0 && pos%3 == 0
	if pos < len(ints) {
		b.WriteByte(ints[len(ints)-1-pos])
		if sep {
			b.WriteByte(',')
		}
		return
	}
	switch ph {
	case '0':
		b.WriteByte('0')
		if sep {
			b.WriteByte(',')
		}
	case '?':
		b.WriteByte(' ')
		if sep {
			b.WriteByte(' ')
		}
	}
}

// writeGrouped writes digits whose last digit has the given position from the right, with thousands commas.
func writeGrouped(b *strings.Builder, ds string, pos int, group bool) {
	for i := 0; i < len(ds); i++ {
		b.WriteByte(ds[i])
		p := pos + len(ds) - 1 - i
		if group && p > 0 && p%3 == 0 {
			b.WriteByte(',')
		}
	}
}

// bestFraction approximates x in [0,1) by a fraction with a limited denominator, by continued fractions.
func bestFraction(x float64, maxDen int) (int, int) {
	if x <= 0 {
		return 0, 1
	}
	p0, q0, p1, q1 := 0, 1, 1, 0
	v := x
	for range 64 {
		a := int(math.Min(math.Floor(v), 1e12))
		if float64(a)*float64(q1) > float64(maxDen) {
			break
		}
		q2 := q0 + a*q1
		if q2 > maxDen {
			break
		}
		p0, q0, p1, q1 = p1, q1, p0+a*p1, q2
		rem := v - float64(a)
		if rem < 1e-12 {
			break
		}
		v = 1 / rem
	}
	if q1 == 0 {
		return 0, 1
	}
	k := (maxDen - q0) / q1
	p2, q2 := p0+k*p1, q0+k*q1
	if q2 > 0 && math.Abs(float64(p2)/float64(q2)-x) < math.Abs(float64(p1)/float64(q1)-x) {
		return p2, q2
	}
	return p1, q1
}

func pow10i(n int) int {
	r := 1
	for ; n > 0 && r < 1_000_000_000; n-- {
		r *= 10
	}
	return r
}

func (s *section) renderFraction(d dec, o *output) {
	x := d.float()
	whole := 0.0
	frac := x
	if s.hasInt {
		whole = math.Floor(x)
		frac = x - whole
	}
	var num, den int
	if s.fixedDen > 0 {
		den = s.fixedDen
		n := math.Round(frac * float64(den))
		num = int(math.Min(n, 1e15))
		if s.hasInt && num >= den {
			whole++
			num -= den
		}
	} else if s.hasInt {
		num, den = bestFraction(frac, pow10i(s.denW)-1)
		if num >= den {
			whole++
			num = 0
		}
	} else {
		w := math.Floor(frac)
		num, den = bestFraction(frac-w, pow10i(s.denW)-1)
		if w < 1e15 {
			num += int(w) * den
		}
	}
	ints := ""
	if whole > 0 {
		ints = newDec(whole).intStr()
	} else if num == 0 {
		ints = "0"
	}
	k := 0
	for i := range s.toks {
		t := &s.toks[i]
		switch t.kind {
		case ftLit:
			o.b.WriteString(t.s)
		case ftFill:
			o.fill(t.r)
		case ftDigit:
			s.placeInt(&o.b, t.r, k, ints)
			k++
		case ftFracNum:
			if s.hasInt && num == 0 {
				o.b.WriteString(strings.Repeat(" ", s.numW+1+s.denW))
				continue
			}
			text := strconv.Itoa(num)
			pad(&o.b, s.numPad, s.numW-len(text))
			o.b.WriteString(text)
			o.b.WriteByte('/')
		case ftFracDen:
			if s.hasInt && num == 0 {
				continue
			}
			text := strconv.Itoa(den)
			o.b.WriteString(text)
			if s.denPad == '?' {
				pad(&o.b, '?', s.denW-len(text))
			}
		}
	}
}

func pad(b *strings.Builder, ph rune, n int) {
	for ; n > 0; n-- {
		switch ph {
		case '0':
			b.WriteByte('0')
		case '?':
			b.WriteByte(' ')
		}
	}
}

var weekdayNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
var monthTitles = []string{"January", "February", "March", "April", "May", "June", "July", "August", "September",
	"October", "November", "December"}
var cnWeekdays = []string{"日", "一", "二", "三", "四", "五", "六"}

func writeInt(b *strings.Builder, v, width int) {
	t := strconv.Itoa(v)
	for n := len(t); n < width; n++ {
		b.WriteByte('0')
	}
	b.WriteString(t)
}

// roundTo rounds to a number of decimal places.
func roundTo(v float64, places int) float64 {
	p := math.Pow10(places)
	return math.Round(v*p) / p
}

// renderDate writes a date or time. It reports false for a serial Excel cannot show.
func (s *section) renderDate(x float64, date1904 bool, o *output) bool {
	if x < 0 || x >= maxSerial+1 {
		return false
	}
	day := math.Floor(x)
	secs := roundTo((x-day)*86400, min(s.subsec, 9))
	if secs >= 86400 {
		day++
		secs -= 86400
	}
	if day > maxSerial {
		return false
	}
	y, m, d := dateOfDay(int(day), date1904)
	whole := math.Floor(secs)
	sub := secs - whole
	t := int(whole)
	hour, minute, second := t/3600, t/60%60, t%60
	total := roundTo(x*86400, min(s.subsec, 9))
	totalWhole := math.Floor(total)
	if s.elapsed {
		sub = total - totalWhole
		second = int(math.Mod(totalWhole, 60))
		minute = int(math.Mod(math.Floor(totalWhole/60), 60))
	}
	h12 := hour
	if s.hour12 {
		h12 = hour % 12
		if h12 == 0 {
			h12 = 12
		}
	}
	for i := range s.toks {
		tk := &s.toks[i]
		switch tk.kind {
		case ftLit:
			o.b.WriteString(tk.s)
		case ftFill:
			o.fill(tk.r)
		case ftYear:
			if tk.n <= 2 {
				writeInt(&o.b, y%100, 2)
			} else {
				writeInt(&o.b, y, 4)
			}
		case ftMonth:
			switch tk.n {
			case 1:
				writeInt(&o.b, m, 1)
			case 2:
				writeInt(&o.b, m, 2)
			case 3:
				o.b.WriteString(monthTitles[m-1][:3])
			case 5:
				o.b.WriteString(monthTitles[m-1][:1])
			default:
				o.b.WriteString(monthTitles[m-1])
			}
		case ftDay:
			switch tk.n {
			case 1:
				writeInt(&o.b, d, 1)
			case 2:
				writeInt(&o.b, d, 2)
			case 3:
				o.b.WriteString(weekdayNames[weekdayOf(int(day), date1904)][:3])
			default:
				o.b.WriteString(weekdayNames[weekdayOf(int(day), date1904)])
			}
		case ftCnWeek:
			if tk.n > 3 {
				o.b.WriteString("星期")
			}
			o.b.WriteString(cnWeekdays[weekdayOf(int(day), date1904)])
		case ftHour:
			writeInt(&o.b, h12, min(tk.n, 2))
		case ftMin:
			writeInt(&o.b, minute, min(tk.n, 2))
		case ftSec:
			writeInt(&o.b, second, min(tk.n, 2))
		case ftSub:
			digits := strconv.FormatFloat(sub, 'f', tk.n, 64)
			if i := strings.IndexByte(digits, '.'); i >= 0 {
				o.b.WriteString(digits[i:])
			}
		case ftElapsedH:
			o.b.WriteString(padded(math.Floor(totalWhole/3600), tk.n))
		case ftElapsedM:
			o.b.WriteString(padded(math.Floor(totalWhole/60), tk.n))
		case ftElapsedS:
			o.b.WriteString(padded(totalWhole, tk.n))
		case ftAMPM:
			switch tk.s {
			case "AM/PM":
				if hour >= 12 {
					o.b.WriteString("PM")
				} else {
					o.b.WriteString("AM")
				}
			case "上午/下午":
				if hour >= 12 {
					o.b.WriteString("下午")
				} else {
					o.b.WriteString("上午")
				}
			default:
				// A/P keeps the case written.
				if hour >= 12 {
					o.b.WriteString(tk.s[2:3])
				} else {
					o.b.WriteString(tk.s[0:1])
				}
			}
		}
	}
	return true
}

func padded(v float64, width int) string {
	t := strconv.FormatFloat(v, 'f', 0, 64)
	if len(t) < width {
		return strings.Repeat("0", width-len(t)) + t
	}
	return t
}

var builtinFormats = map[int]string{
	0: "General", 1: "0", 2: "0.00", 3: "#,##0", 4: "#,##0.00",
	5: `"$"#,##0_);\("$"#,##0\)`, 6: `"$"#,##0_);[Red]\("$"#,##0\)`,
	7: `"$"#,##0.00_);\("$"#,##0.00\)`, 8: `"$"#,##0.00_);[Red]\("$"#,##0.00\)`,
	9: "0%", 10: "0.00%", 11: "0.00E+00", 12: "# ?/?", 13: "# ??/??",
	14: "m/d/yyyy", 15: "d-mmm-yy", 16: "d-mmm", 17: "mmm-yy",
	18: "h:mm AM/PM", 19: "h:mm:ss AM/PM", 20: "h:mm", 21: "h:mm:ss", 22: "m/d/yyyy h:mm",
	37: "#,##0_);(#,##0)", 38: "#,##0_);[Red](#,##0)", 39: "#,##0.00_);(#,##0.00)", 40: "#,##0.00_);[Red](#,##0.00)",
	41: `_(* #,##0_);_(* \(#,##0\);_(* "-"_);_(@_)`,
	42: `_("$"* #,##0_);_("$"* \(#,##0\);_("$"* "-"_);_(@_)`,
	43: `_(* #,##0.00_);_(* \(#,##0.00\);_(* "-"??_);_(@_)`,
	44: `_("$"* #,##0.00_);_("$"* \(#,##0.00\);_("$"* "-"??_);_(@_)`,
	45: "mm:ss", 46: "[h]:mm:ss", 47: "mm:ss.0", 48: "##0.0E+0", 49: "@",
}

// BuiltinFormat gives the en-US code of a builtin format id, "" if there is none.
func BuiltinFormat(id int) string { return builtinFormats[id] }

// General gives Excel's General display of a number: at most 11 characters by default (maxChars <= 0), or fitted
// into maxChars by rounding, in scientific notation when that shows more. The result is best effort if nothing fits.
func General(n float64, maxChars int) string {
	if n == 0 {
		return "0"
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return ErrNum
	}
	neg := n < 0
	abs := math.Abs(n)
	w := maxChars
	if w <= 0 {
		w = 11
	} else if neg {
		w--
	}
	w = min(max(w, 1), 20)
	s := generalText(abs, w)
	if neg {
		return "-" + s
	}
	return s
}

func generalText(abs float64, w int) string {
	d := newDec(abs)
	e10 := d.e - 1
	if e10 < w && e10 >= -6 {
		places := max(w-max(e10+1, 1)-1, 0)
		r := d.round(places, roundHalf)
		lossless := len(r.d) == len(d.d) && r.e == d.e
		if len(r.d) > 0 && (e10 >= -4 || lossless) {
			if t := fixedText(r); len(t) <= w {
				return t
			}
		}
	}
	return sciText(d, w)
}

func fixedText(d dec) string {
	ints := d.intStr()
	if ints == "" {
		ints = "0"
	}
	n := len(d.d) - d.e
	if n <= 0 {
		return ints
	}
	return ints + "." + d.fracStr(n)
}

func sciText(d dec, w int) string {
	exp := d.e - 1
	el := 4
	if exp >= 100 || exp <= -100 {
		el = 5
	}
	sig := min(w-el-1, 15)
	if w <= 11 {
		sig = min(sig, 6)
	}
	sig = max(sig, 1)
	m := dec{d: d.d, e: 1}.round(sig-1, roundHalf)
	if m.e > 1 {
		m.e = 1
		exp++
	}
	return sciJoin(m, exp)
}

func sciJoin(m dec, exp int) string {
	var b strings.Builder
	b.WriteString(fixedText(m))
	b.WriteByte('E')
	if exp < 0 {
		b.WriteByte('-')
		exp = -exp
	} else {
		b.WriteByte('+')
	}
	if exp < 10 {
		b.WriteByte('0')
	}
	b.WriteString(strconv.Itoa(exp))
	return b.String()
}

// numToText is how a number becomes text in & and text functions: General without a width, 15 significant digits.
func numToText(f float64) string {
	if f == 0 {
		return "0"
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return ErrNum
	}
	neg := f < 0
	d := newDec(math.Abs(f))
	e10 := d.e - 1
	var s string
	if e10 < 15 && e10 >= -9 {
		s = fixedText(d)
		if len(s) > 20 {
			s = sciJoin(dec{d: d.d, e: 1}, e10)
		}
	} else {
		s = sciJoin(dec{d: d.d, e: 1}, e10)
	}
	if neg {
		return "-" + s
	}
	return s
}

var (
	formatCache   = map[string]*Format{}
	formatCacheMu sync.RWMutex
)

// cachedFormat parses a code once, for TEXT in long columns.
func cachedFormat(code string) *Format {
	formatCacheMu.RLock()
	f := formatCache[code]
	formatCacheMu.RUnlock()
	if f != nil {
		return f
	}
	f = ParseFormat(code)
	formatCacheMu.Lock()
	if len(formatCache) >= 4096 {
		clear(formatCache)
	}
	formatCache[code] = f
	formatCacheMu.Unlock()
	return f
}
