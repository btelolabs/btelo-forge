package formula

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxDepth bounds the nesting of a formula, in the parser and so in the evaluator.
const maxDepth = 512

// Expr is a parsed formula. It is immutable after Parse and safe to share between cells and goroutines.
type Expr struct {
	src      string
	root     *node
	refs     []ref
	known    bool
	volatile bool
	subtotal bool
	hull     bool // holds a ':' between separate references
}

type nodeKind uint8

const (
	nConst nodeKind = iota
	nMissing
	nRef
	nName
	nFunc
	nUnary
	nPercent
	nBinary
	nRange
	nIntersect
	nUnion
	nOpaque // parsed, but not something the evaluator handles
)

// node is one piece of the syntax tree.
type node struct {
	kind nodeKind
	op   byte
	val  Value
	r    *ref
	kids []*node
	fn   *funcDef
}

// Forms of reference.
const (
	formCell uint8 = iota
	formRange
	formCols
	formRows
)

// Bits of ref.abs.
const (
	absR1 uint8 = 1 << iota
	absC1
	absR2
	absC2
)

// ref is one reference or defined name as written in the source, with its span there.
type ref struct {
	pos, cell, end int32 // source span: start, after the sheet prefix, end
	sheet1, sheet2 string
	hasSheet       bool
	external       bool
	bad            bool // the #REF! of Sheet1!#REF!
	isName         bool
	name           string
	form           uint8
	abs            uint8
	r1, c1, r2, c2 int32
}

type tokKind uint8

const (
	tEOF tokKind = iota
	tNum
	tStr
	tBool
	tErr
	tRef
	tName
	tFunc
	tOpaque
	tOp
	tLP
	tRP
	tLB
	tRB
	tComma
	tSemi
)

// The two-character operators, as one byte.
const (
	opLE byte = 'l'
	opGE byte = 'g'
	opNE byte = 'n'
)

type token struct {
	kind  tokKind
	space bool // whitespace came before it
	op    byte
	num   float64
	str   string
	ref   int32
}

type lexer struct {
	src   string
	i     int
	toks  []token
	refs  []ref
	space bool
}

func isNameStart(r rune) bool { return r == '_' || r == '\\' || unicode.IsLetter(r) }

func isNameChar(r rune) bool {
	return isNameStart(r) || r == '.' || r == '?' || (r >= '0' && r <= '9') || unicode.IsDigit(r) || unicode.IsMark(r)
}

// scanName returns where a run of name characters that starts at i ends.
func scanName(s string, i int) int {
	for i < len(s) {
		r, n := utf8.DecodeRuneInString(s[i:])
		if !isNameChar(r) {
			break
		}
		i += n
	}
	return i
}

func (l *lexer) push(t token) {
	t.space = l.space
	l.space = false
	l.toks = append(l.toks, t)
}

func (l *lexer) pushRef(r ref) {
	l.refs = append(l.refs, r)
	l.push(token{kind: tRef, ref: int32(len(l.refs) - 1)})
}

// scanQuoted reads a '...' sheet name at i, with ” for a quote.
func scanQuoted(s string, i int) (string, int, bool) {
	var b strings.Builder
	j := i + 1
	for j < len(s) {
		if s[j] == '\'' {
			if j+1 < len(s) && s[j+1] == '\'' {
				b.WriteByte('\'')
				j += 2
				continue
			}
			return b.String(), j + 1, true
		}
		b.WriteByte(s[j])
		j++
	}
	return "", 0, false
}

// closeBracket finds the end of the balanced [...] at i, honouring the ' escape of structured references.
func closeBracket(s string, i int) int {
	depth := 0
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '\'':
			j++
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return j + 1
			}
		}
	}
	return -1
}

// sheetPrefix reads Sheet1!, 'My Sheet'!, Sheet1:Sheet3!, [1]Sheet1! and the like at i.
func (l *lexer) sheetPrefix(i int) (r ref, next int, ok bool) {
	s := l.src
	switch s[i] {
	case '\'':
		name, j, good := scanQuoted(s, i)
		if !good || j >= len(s) || s[j] != '!' {
			return r, 0, false
		}
		r.hasSheet = true
		if strings.ContainsRune(name, '[') {
			r.external, r.sheet1 = true, name
		} else if a, b, cut := strings.Cut(name, ":"); cut {
			r.sheet1, r.sheet2 = a, b
		} else {
			r.sheet1 = name
		}
		return r, j + 1, true
	case '[':
		j := closeBracket(s, i)
		if j < 0 {
			return r, 0, false
		}
		k := scanName(s, j)
		if k >= len(s) || s[k] != '!' {
			return r, 0, false
		}
		r.hasSheet, r.external, r.sheet1 = true, true, s[i:k]
		return r, k + 1, true
	}
	j := scanName(s, i)
	if j == i || j >= len(s) {
		return r, 0, false
	}
	if s[j] == '!' {
		r.hasSheet, r.sheet1 = true, s[i:j]
		return r, j + 1, true
	}
	if s[j] == ':' {
		k := scanName(s, j+1)
		if k > j+1 && k < len(s) && s[k] == '!' {
			r.hasSheet, r.sheet1, r.sheet2 = true, s[i:j], s[j+1:k]
			return r, k + 1, true
		}
	}
	return r, 0, false
}

// refTail reads the cell part of a reference at i into r.
func (l *lexer) refTail(i int, r *ref) (int, bool) {
	s := l.src
	p, j, ok := scanPart(s, i)
	if !ok {
		return 0, false
	}
	r.cell = int32(i)
	setEnd := func(q part, second bool) {
		var f uint8
		if q.absR {
			f |= absR1
		}
		if q.absC {
			f |= absC1
		}
		if second {
			r.r2, r.c2 = q.row, q.col
			r.abs |= f << 2
		} else {
			r.r1, r.c1 = q.row, q.col
			r.abs |= f
		}
	}
	if j < len(s) && s[j] == ':' {
		if q, k, ok := scanPart(s, j+1); ok && q.kind == p.kind && !nameContinues(s, k) {
			setEnd(p, false)
			setEnd(q, true)
			switch p.kind {
			case partCell:
				r.form = formRange
			case partCol:
				r.form, r.r1, r.r2 = formCols, 0, MaxRows-1
			case partRow:
				r.form, r.c1, r.c2 = formRows, 0, MaxCols-1
			}
			return k, true
		}
	}
	if p.kind != partCell || nameContinues(s, j) {
		return 0, false
	}
	setEnd(p, false)
	setEnd(p, true)
	r.form = formCell
	return j, true
}

// nameContinues says a reference-like run does not end at i, as in A1B, A1.x, LOG10( or Sheet1!.
func nameContinues(s string, i int) bool {
	if i >= len(s) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return isNameChar(r) || r == '(' || r == '!' || r == '['
}

var errorCodes = []string{ErrNull, ErrDiv0, ErrValue, ErrRef, ErrName, ErrNum, ErrNA,
	"#SPILL!", "#CALC!", "#FIELD!", "#BLOCKED!", "#CONNECT!", "#UNKNOWN!", "#GETTING_DATA", "#PYTHON!", "#TIMEOUT!", "#BUSY!"}

func scanError(s string, i int) string {
	for _, c := range errorCodes {
		if len(s)-i >= len(c) && strings.EqualFold(s[i:i+len(c)], c) {
			return c
		}
	}
	return ""
}

// run tokenises the source.
func (l *lexer) run() error {
	s := l.src
	for l.i < len(s) {
		c := s[l.i]
		switch c {
		case ' ', '\t', '\r', '\n':
			l.i++
			l.space = true
			continue
		case '"':
			var b strings.Builder
			j := l.i + 1
			closed := false
			for j < len(s) {
				if s[j] == '"' {
					if j+1 < len(s) && s[j+1] == '"' {
						b.WriteByte('"')
						j += 2
						continue
					}
					closed = true
					break
				}
				b.WriteByte(s[j])
				j++
			}
			if !closed {
				return errors.New("unterminated text")
			}
			l.push(token{kind: tStr, str: b.String()})
			l.i = j + 1
			continue
		case '(':
			l.push(token{kind: tLP})
			l.i++
			continue
		case ')':
			l.push(token{kind: tRP})
			l.i++
			continue
		case '{':
			l.push(token{kind: tLB})
			l.i++
			continue
		case '}':
			l.push(token{kind: tRB})
			l.i++
			continue
		case ',':
			l.push(token{kind: tComma})
			l.i++
			continue
		case ';':
			l.push(token{kind: tSemi})
			l.i++
			continue
		case '+', '-', '*', '/', '^', '&', '=', '%', ':', '@':
			l.push(token{kind: tOp, op: c})
			l.i++
			continue
		case '<':
			if strings.HasPrefix(s[l.i:], "<=") {
				l.push(token{kind: tOp, op: opLE})
				l.i += 2
			} else if strings.HasPrefix(s[l.i:], "<>") {
				l.push(token{kind: tOp, op: opNE})
				l.i += 2
			} else {
				l.push(token{kind: tOp, op: '<'})
				l.i++
			}
			continue
		case '>':
			if strings.HasPrefix(s[l.i:], ">=") {
				l.push(token{kind: tOp, op: opGE})
				l.i += 2
			} else {
				l.push(token{kind: tOp, op: '>'})
				l.i++
			}
			continue
		case '#':
			if err := l.hash(); err != nil {
				return err
			}
			continue
		}
		if err := l.word(); err != nil {
			return err
		}
	}
	l.push(token{kind: tEOF})
	return nil
}

// hash handles an error literal, the #REF!A1 Excel writes for a lost sheet, and the spill operator.
func (l *lexer) hash() error {
	s := l.src
	code := scanError(s, l.i)
	if code == "" {
		if n := len(l.toks); n > 0 {
			switch l.toks[n-1].kind {
			case tRef, tName, tRP:
				l.push(token{kind: tOp, op: '#'})
				l.i++
				return nil
			}
		}
		return fmt.Errorf("unexpected # at %d", l.i)
	}
	j := l.i + len(code)
	if code == ErrRef && j < len(s) {
		var r ref
		if k, ok := l.refTail(j, &r); ok {
			j = k
		}
	}
	l.push(token{kind: tErr, str: code})
	l.i = j
	return nil
}

// word handles everything that starts with a name character, a digit, a dollar, a quote or a bracket.
func (l *lexer) word() error {
	s := l.src
	start := l.i
	c := s[start]
	if c == '.' && !(start+1 < len(s) && s[start+1] >= '0' && s[start+1] <= '9') {
		return fmt.Errorf("unexpected . at %d", start)
	}
	if c != '.' && c != '$' {
		if r, _, ok := l.sheetPrefix(start); ok {
			return l.prefixed(r, start)
		}
	}
	if c == '\'' {
		return fmt.Errorf("unexpected quote at %d", start)
	}
	if c == '[' {
		// A structured reference without its table, as in [@Col].
		j := closeBracket(s, start)
		if j < 0 {
			return fmt.Errorf("unclosed [ at %d", start)
		}
		l.push(token{kind: tOpaque})
		l.i = j
		return nil
	}
	r := ref{pos: int32(start)}
	if j, ok := l.refTail(start, &r); ok {
		r.end = int32(j)
		l.pushRef(r)
		l.i = j
		return nil
	}
	if c == '.' || (c >= '0' && c <= '9') {
		return l.number()
	}
	first, _ := utf8.DecodeRuneInString(s[start:])
	if !isNameStart(first) {
		return fmt.Errorf("unexpected %q at %d", first, start)
	}
	j := scanName(s, start)
	word := s[start:j]
	if j < len(s) && s[j] == '(' {
		l.push(token{kind: tFunc, str: word})
		l.i = j
		return nil
	}
	if j < len(s) && s[j] == '[' {
		k := closeBracket(s, j)
		if k < 0 {
			return fmt.Errorf("unclosed [ at %d", j)
		}
		l.push(token{kind: tOpaque})
		l.i = k
		return nil
	}
	if lookupFunc(word) != nil {
		k := j
		for k < len(s) && (s[k] == ' ' || s[k] == '\t') {
			k++
		}
		if k < len(s) && s[k] == '(' {
			l.push(token{kind: tFunc, str: word})
			l.i = k
			return nil
		}
	}
	if strings.EqualFold(word, "TRUE") {
		l.push(token{kind: tBool, num: 1})
		l.i = j
		return nil
	}
	if strings.EqualFold(word, "FALSE") {
		l.push(token{kind: tBool})
		l.i = j
		return nil
	}
	l.pushRef(ref{pos: int32(start), cell: int32(start), end: int32(j), isName: true, name: word})
	l.i = j
	return nil
}

// prefixed finishes a reference whose sheet prefix was found at start.
func (l *lexer) prefixed(r ref, start int) error {
	s := l.src
	_, next, _ := l.sheetPrefix(start)
	r.pos, r.cell = int32(start), int32(next)
	if len(s)-next >= len(ErrRef) && strings.EqualFold(s[next:next+len(ErrRef)], ErrRef) {
		r.bad = true
		r.end = int32(next + len(ErrRef))
		l.pushRef(r)
		l.i = next + len(ErrRef)
		return nil
	}
	if j, ok := l.refTail(next, &r); ok {
		r.end = int32(j)
		l.pushRef(r)
		l.i = j
		return nil
	}
	j := scanName(s, next)
	if j == next {
		return fmt.Errorf("bad reference at %d", start)
	}
	if j < len(s) && s[j] == '(' {
		// A function in an add-in or external book.
		l.push(token{kind: tFunc, str: s[start:j]})
		l.i = j
		return nil
	}
	if j < len(s) && s[j] == '[' {
		k := closeBracket(s, j)
		if k < 0 {
			return fmt.Errorf("unclosed [ at %d", j)
		}
		l.push(token{kind: tOpaque})
		l.i = k
		return nil
	}
	r.isName, r.name, r.end = true, s[next:j], int32(j)
	l.pushRef(r)
	l.i = j
	return nil
}

func (l *lexer) number() error {
	s := l.src
	j := l.i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j < len(s) && s[j] == '.' {
		j++
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
	}
	if j < len(s) && (s[j] == 'e' || s[j] == 'E') {
		k := j + 1
		if k < len(s) && (s[k] == '+' || s[k] == '-') {
			k++
		}
		if k < len(s) && s[k] >= '0' && s[k] <= '9' {
			for k < len(s) && s[k] >= '0' && s[k] <= '9' {
				k++
			}
			j = k
		}
	}
	f, err := strconv.ParseFloat(s[l.i:j], 64)
	if err != nil {
		return fmt.Errorf("bad number at %d", l.i)
	}
	l.push(token{kind: tNum, num: f})
	l.i = j
	return nil
}

type parser struct {
	toks     []token
	i        int
	depth    int
	refs     []ref
	known    bool
	volatile bool
	subtotal bool
	hull     bool
}

var errTooDeep = errors.New("formula is nested too deeply")

// Parse reads a formula without its leading '='. It fails only on real syntax errors: unknown functions, external
// references, structured references and the like parse, and show up in Known.
func Parse(src string) (*Expr, error) {
	l := lexer{src: src}
	if err := l.run(); err != nil {
		return nil, err
	}
	p := parser{toks: l.toks, refs: l.refs, known: true}
	if p.peek().kind == tEOF {
		return nil, errors.New("empty formula")
	}
	root, err := p.union()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tEOF {
		return nil, errors.New("unexpected text after the formula")
	}
	for i := range p.refs {
		if p.refs[i].external {
			p.known = false
		}
	}
	return &Expr{src: src, root: root, refs: p.refs, known: p.known, volatile: p.volatile, subtotal: p.subtotal,
		hull: p.hull}, nil
}

func (p *parser) peek() *token { return &p.toks[p.i] }

func (p *parser) isOp(ops string) bool {
	t := p.peek()
	return t.kind == tOp && strings.IndexByte(ops, t.op) >= 0
}

func (p *parser) enter() error {
	p.depth++
	if p.depth > maxDepth {
		return errTooDeep
	}
	return nil
}

// union reads expressions joined by commas: a union at the top of a formula, or inside parentheses.
func (p *parser) union() (*node, error) {
	first, err := p.expr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tComma {
		return first, nil
	}
	n := &node{kind: nUnion, kids: []*node{first}}
	for p.peek().kind == tComma {
		p.i++
		k, err := p.expr()
		if err != nil {
			return nil, err
		}
		n.kids = append(n.kids, k)
	}
	return n, nil
}

func (p *parser) expr() (*node, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer func() { p.depth-- }()
	return p.compare()
}

func binary(op byte, a, b *node) *node { return &node{kind: nBinary, op: op, kids: []*node{a, b}} }

func (p *parser) compare() (*node, error) {
	left, err := p.concat()
	if err != nil {
		return nil, err
	}
	for p.isOp("=<>lgn") {
		op := p.peek().op
		p.i++
		right, err := p.concat()
		if err != nil {
			return nil, err
		}
		left = binary(op, left, right)
	}
	return left, nil
}

func (p *parser) concat() (*node, error) {
	left, err := p.add()
	if err != nil {
		return nil, err
	}
	for p.isOp("&") {
		p.i++
		right, err := p.add()
		if err != nil {
			return nil, err
		}
		left = binary('&', left, right)
	}
	return left, nil
}

func (p *parser) add() (*node, error) {
	left, err := p.mul()
	if err != nil {
		return nil, err
	}
	for p.isOp("+-") {
		op := p.peek().op
		p.i++
		right, err := p.mul()
		if err != nil {
			return nil, err
		}
		left = binary(op, left, right)
	}
	return left, nil
}

func (p *parser) mul() (*node, error) {
	left, err := p.pow()
	if err != nil {
		return nil, err
	}
	for p.isOp("*/") {
		op := p.peek().op
		p.i++
		right, err := p.pow()
		if err != nil {
			return nil, err
		}
		left = binary(op, left, right)
	}
	return left, nil
}

func (p *parser) pow() (*node, error) {
	left, err := p.unary()
	if err != nil {
		return nil, err
	}
	for p.isOp("^") {
		p.i++
		right, err := p.unary()
		if err != nil {
			return nil, err
		}
		left = binary('^', left, right)
	}
	return left, nil
}

func (p *parser) unary() (*node, error) {
	if p.isOp("+-@") {
		op := p.peek().op
		if err := p.enter(); err != nil {
			return nil, err
		}
		defer func() { p.depth-- }()
		p.i++
		k, err := p.unary()
		if err != nil {
			return nil, err
		}
		return &node{kind: nUnary, op: op, kids: []*node{k}}, nil
	}
	return p.percent()
}

func (p *parser) percent() (*node, error) {
	n, err := p.rangeLevel()
	if err != nil {
		return nil, err
	}
	for count := 0; p.isOp("%"); count++ {
		if count == maxDepth {
			return nil, errTooDeep
		}
		p.i++
		n = &node{kind: nPercent, kids: []*node{n}}
	}
	return n, nil
}

// startsOperand says a token can begin the right side of an intersection.
func startsOperand(t *token) bool {
	switch t.kind {
	case tRef, tFunc, tLP, tOpaque:
		return true
	}
	return false
}

// rangeLevel reads the reference operators, which bind tightest: ':', and the space that means intersection.
func (p *parser) rangeLevel() (*node, error) {
	left, err := p.primary()
	if err != nil {
		return nil, err
	}
	for count := 0; ; count++ {
		if count == maxDepth {
			return nil, errTooDeep
		}
		t := p.peek()
		switch {
		case t.kind == tOp && t.op == ':':
			p.i++
			right, err := p.primary()
			if err != nil {
				return nil, err
			}
			p.hull = p.hull || (left.kind == nRef && right.kind == nRef)
			left = &node{kind: nRange, kids: []*node{left, right}}
		case t.kind == tOp && t.op == '#':
			p.i++
			p.known = false
			left = &node{kind: nOpaque}
		case t.kind == tLP && !t.space && (left.kind == nFunc || left.kind == nOpaque):
			// A call of what a call returned, as LAMBDA(...)(1).
			p.i++
			if _, err := p.args(); err != nil {
				return nil, err
			}
			p.known = false
			left = &node{kind: nOpaque}
		case t.space && startsOperand(t):
			right, err := p.primary()
			if err != nil {
				return nil, err
			}
			left = &node{kind: nIntersect, kids: []*node{left, right}}
		default:
			return left, nil
		}
	}
}

// args reads call arguments after the '(' up to and including the ')'.
func (p *parser) args() ([]*node, error) {
	var kids []*node
	if p.peek().kind == tRP {
		p.i++
		return nil, nil
	}
	for {
		t := p.peek()
		if t.kind == tComma {
			kids = append(kids, &node{kind: nMissing})
			p.i++
			if p.peek().kind == tRP {
				kids = append(kids, &node{kind: nMissing})
				p.i++
				return kids, nil
			}
			continue
		}
		if t.kind == tRP {
			kids = append(kids, &node{kind: nMissing})
			p.i++
			return kids, nil
		}
		k, err := p.expr()
		if err != nil {
			return nil, err
		}
		kids = append(kids, k)
		switch p.peek().kind {
		case tComma:
			p.i++
			if p.peek().kind == tRP {
				kids = append(kids, &node{kind: nMissing})
				p.i++
				return kids, nil
			}
		case tRP:
			p.i++
			return kids, nil
		default:
			return nil, errors.New("expected , or ) in a call")
		}
	}
}

func (p *parser) primary() (*node, error) {
	t := p.peek()
	switch t.kind {
	case tNum:
		p.i++
		return &node{kind: nConst, val: Num(t.num)}, nil
	case tStr:
		p.i++
		return &node{kind: nConst, val: Str(t.str)}, nil
	case tBool:
		p.i++
		return &node{kind: nConst, val: BoolOf(t.num != 0)}, nil
	case tErr:
		p.i++
		return &node{kind: nConst, val: Err(t.str)}, nil
	case tRef:
		p.i++
		r := &p.refs[t.ref]
		if r.isName {
			if strings.HasPrefix(strings.ToLower(r.name), "_xlpm.") {
				p.known = false
			}
			return &node{kind: nName, r: r}, nil
		}
		return &node{kind: nRef, r: r}, nil
	case tOpaque:
		p.i++
		p.known = false
		return &node{kind: nOpaque}, nil
	case tFunc:
		return p.call(t)
	case tLP:
		if err := p.enter(); err != nil {
			return nil, err
		}
		defer func() { p.depth-- }()
		p.i++
		n, err := p.union()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tRP {
			return nil, errors.New("expected )")
		}
		p.i++
		return n, nil
	case tLB:
		return p.arrayLit()
	}
	return nil, errors.New("unexpected token")
}

func (p *parser) call(t *token) (*node, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer func() { p.depth-- }()
	p.i += 2 // the name and its '('
	kids, err := p.args()
	if err != nil {
		return nil, err
	}
	def := lookupFunc(t.str)
	if def == nil {
		p.known = false
		return &node{kind: nOpaque}, nil
	}
	if def.volatile {
		p.volatile = true
	}
	if def.name == "SUBTOTAL" {
		p.subtotal = true
	}
	return &node{kind: nFunc, fn: def, kids: kids}, nil
}

// funcKey strips the prefixes files put on newer functions and folds the case.
func funcKey(name string) string {
	s := strings.ToUpper(name)
	for {
		switch {
		case strings.HasPrefix(s, "_XLFN."):
			s = s[6:]
		case strings.HasPrefix(s, "_XLWS."):
			s = s[6:]
		case strings.HasPrefix(s, "_XLUDF."):
			s = s[7:]
		default:
			return s
		}
	}
}

func lookupFunc(name string) *funcDef { return funcs[funcKey(name)] }

func (p *parser) arrayLit() (*node, error) {
	p.i++
	var vals []Value
	rows, cols, col := 1, 0, 0
	for {
		v, err := p.arrayItem()
		if err != nil {
			return nil, err
		}
		vals = append(vals, v)
		if len(vals) > maxCells {
			return nil, errors.New("array is too large")
		}
		col++
		t := p.peek()
		switch t.kind {
		case tComma:
			p.i++
			continue
		case tSemi:
			p.i++
			if cols != 0 && col != cols {
				return nil, errors.New("array rows differ in length")
			}
			cols, col = col, 0
			rows++
			continue
		case tRB:
			p.i++
			if cols != 0 && col != cols {
				return nil, errors.New("array rows differ in length")
			}
			return &node{kind: nConst, val: Value{Kind: Array, Arr: &Grid{Rows: rows, Cols: col, V: vals}}}, nil
		}
		return nil, errors.New("expected , ; or } in an array")
	}
}

func (p *parser) arrayItem() (Value, error) {
	t := p.peek()
	sign := 1.0
	if t.kind == tOp && (t.op == '-' || t.op == '+') {
		if t.op == '-' {
			sign = -1
		}
		p.i++
		t = p.peek()
		if t.kind != tNum {
			return Value{}, errors.New("expected a number in an array")
		}
	}
	p.i++
	switch t.kind {
	case tNum:
		return Num(sign * t.num), nil
	case tStr:
		return Str(t.str), nil
	case tBool:
		return BoolOf(t.num != 0), nil
	case tErr:
		return Err(t.str), nil
	}
	return Value{}, errors.New("bad item in an array")
}

// Volatile says the formula calls RAND, RANDBETWEEN, NOW, TODAY, OFFSET or INDIRECT.
func (x *Expr) Volatile() bool { return x.volatile }

// Known is false when a function called is not implemented, or the formula holds an external or structured
// reference: the caller keeps the cached value and never evaluates it.
func (x *Expr) Known() bool { return x.known }
