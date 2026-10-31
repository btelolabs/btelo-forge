package pdf

import (
	"bytes"
	"errors"
	"strconv"
)

// Nesting limits for hostile files.
const (
	maxDepth     = 64
	maxContainer = 1 << 20 // the most entries an array or dictionary may have
)

var errSyntax = errors.New("pdf: bad syntax")

// Lexer reads PDF syntax from a byte slice, also content streams.
type Lexer struct {
	b   []byte
	Pos int
	// noRefs turns off "n g R", as in content streams.
	noRefs bool
	// decrypt, when set, decrypts each string as it is read.
	decrypt func([]byte) []byte
}

// NewLexer reads objects and keywords from b, as a content stream.
func NewLexer(b []byte) *Lexer { return &Lexer{b: b, noRefs: true} }

func isSpace(c byte) bool {
	return c == ' ' || c == '\n' || c == '\r' || c == '\t' || c == '\f' || c == 0
}

func isDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func (l *Lexer) skip() {
	for l.Pos < len(l.b) {
		c := l.b[l.Pos]
		switch {
		case isSpace(c):
			l.Pos++
		case c == '%':
			for l.Pos < len(l.b) && l.b[l.Pos] != '\n' && l.b[l.Pos] != '\r' {
				l.Pos++
			}
		default:
			return
		}
	}
}

// Keyword is an operator or a word that is no object.
type Keyword string

// Next reads the next object or Keyword; at the end it returns nil and false.
func (l *Lexer) Next() (Object, bool) {
	l.skip()
	if l.Pos >= len(l.b) {
		return nil, false
	}
	o, err := l.object(0)
	if err != nil {
		l.Pos++ // get past whatever stopped it
		return Keyword(""), true
	}
	return o, true
}

// Bytes is what remains unread.
func (l *Lexer) Bytes() []byte { return l.b[min(l.Pos, len(l.b)):] }

func (l *Lexer) object(depth int) (Object, error) {
	if depth > maxDepth {
		return nil, errSyntax
	}
	l.skip()
	if l.Pos >= len(l.b) {
		return nil, errSyntax
	}
	c := l.b[l.Pos]
	switch {
	case c == '/':
		return l.name(), nil
	case c == '(':
		return l.literal(), nil
	case c == '<':
		if l.Pos+1 < len(l.b) && l.b[l.Pos+1] == '<' {
			l.Pos += 2
			return l.dict(depth)
		}
		return l.hexString(), nil
	case c == '[':
		l.Pos++
		return l.array(depth)
	case c == '+' || c == '-' || c == '.' || c >= '0' && c <= '9':
		return l.number(), nil
	case c == ']' || c == '}' || c == '{' || c == ')':
		l.Pos++
		return Keyword(string(c)), nil
	case c == '>':
		l.Pos++
		if l.Pos < len(l.b) && l.b[l.Pos] == '>' {
			l.Pos++
			return Keyword(">>"), nil
		}
		return Keyword(">"), nil
	}
	start := l.Pos
	for l.Pos < len(l.b) && !isSpace(l.b[l.Pos]) && !isDelim(l.b[l.Pos]) {
		l.Pos++
	}
	w := string(l.b[start:l.Pos])
	switch w {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	}
	return Keyword(w), nil
}

func (l *Lexer) name() Name {
	l.Pos++
	var out []byte
	for l.Pos < len(l.b) {
		c := l.b[l.Pos]
		if isSpace(c) || isDelim(c) {
			break
		}
		l.Pos++
		if c == '#' && l.Pos+1 < len(l.b) {
			h, ok1 := hexVal(l.b[l.Pos])
			lo, ok2 := hexVal(l.b[l.Pos+1])
			if ok1 && ok2 {
				out = append(out, h<<4|lo)
				l.Pos += 2
				continue
			}
		}
		out = append(out, c)
	}
	return Name(out)
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

func (l *Lexer) str(b []byte) String {
	if l.decrypt != nil {
		b = l.decrypt(b)
	}
	return String(b)
}

func (l *Lexer) literal() String {
	l.Pos++
	depth := 1
	var out []byte
	for l.Pos < len(l.b) {
		c := l.b[l.Pos]
		l.Pos++
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return l.str(out)
			}
		case '\\':
			if l.Pos >= len(l.b) {
				return l.str(out)
			}
			e := l.b[l.Pos]
			l.Pos++
			switch e {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case '\r':
				if l.Pos < len(l.b) && l.b[l.Pos] == '\n' {
					l.Pos++
				}
			case '\n':
			case '0', '1', '2', '3', '4', '5', '6', '7':
				v := int(e - '0')
				for i := 0; i < 2 && l.Pos < len(l.b) && l.b[l.Pos] >= '0' && l.b[l.Pos] <= '7'; i++ {
					v = v*8 + int(l.b[l.Pos]-'0')
					l.Pos++
				}
				out = append(out, byte(v))
			default:
				out = append(out, e)
			}
			continue
		case '\r':
			if l.Pos < len(l.b) && l.b[l.Pos] == '\n' {
				l.Pos++
			}
			c = '\n'
		}
		out = append(out, c)
	}
	return l.str(out)
}

func (l *Lexer) hexString() String {
	l.Pos++
	var out []byte
	var hi byte
	half := false
	for l.Pos < len(l.b) {
		c := l.b[l.Pos]
		l.Pos++
		if c == '>' {
			break
		}
		v, ok := hexVal(c)
		if !ok {
			continue
		}
		if half {
			out = append(out, hi<<4|v)
		} else {
			hi = v
		}
		half = !half
	}
	if half {
		out = append(out, hi<<4)
	}
	return l.str(out)
}

func (l *Lexer) array(depth int) (Object, error) {
	out := Array{}
	for len(out) < maxContainer {
		l.skip()
		if l.Pos >= len(l.b) {
			return out, nil // a truncated array keeps what it had
		}
		if l.b[l.Pos] == ']' {
			l.Pos++
			return out, nil
		}
		o, err := l.object(depth + 1)
		if err != nil {
			return nil, err
		}
		if k, ok := o.(Keyword); ok {
			switch k {
			case "R":
				if n := len(out); n >= 2 {
					a, ok1 := out[n-2].(int64)
					b, ok2 := out[n-1].(int64)
					if ok1 && ok2 {
						out = append(out[:n-2], Ref{int(a), int(b)})
					}
				}
			case "endobj", "endstream", "stream", "obj":
				l.Pos -= len(k)
				return out, nil
			}
			continue
		}
		out = append(out, o)
	}
	return nil, errSyntax
}

func (l *Lexer) dict(depth int) (Object, error) {
	out := Dict{}
	for len(out) < maxContainer {
		l.skip()
		if l.Pos >= len(l.b) {
			return out, nil
		}
		if l.b[l.Pos] == '>' {
			l.Pos++
			if l.Pos < len(l.b) && l.b[l.Pos] == '>' {
				l.Pos++
			}
			return out, nil
		}
		if l.b[l.Pos] != '/' {
			// A value without a key, or the end of a broken dictionary.
			o, err := l.object(depth + 1)
			if err != nil {
				return nil, err
			}
			if k, ok := o.(Keyword); ok && (k == "endobj" || k == "stream" || k == "endstream") {
				l.Pos -= len(k)
				return out, nil
			}
			continue
		}
		key := l.name()
		l.skip()
		if l.Pos < len(l.b) && l.b[l.Pos] == '>' {
			continue
		}
		v, err := l.object(depth + 1)
		if err != nil {
			return nil, err
		}
		if k, ok := v.(Keyword); ok {
			if k == "endobj" || k == "stream" {
				l.Pos -= len(k)
				return out, nil
			}
			continue
		}
		// "n g R": read on a number, undo when no R follows.
		if a, ok := v.(int64); ok && !l.noRefs {
			save := l.Pos
			l.skip()
			if l.Pos < len(l.b) && l.b[l.Pos] >= '0' && l.b[l.Pos] <= '9' {
				if g, ok := l.number().(int64); ok {
					l.skip()
					if l.Pos < len(l.b) && l.b[l.Pos] == 'R' && (l.Pos+1 == len(l.b) || isSpace(l.b[l.Pos+1]) || isDelim(l.b[l.Pos+1])) {
						l.Pos++
						out[key] = Ref{int(a), int(g)}
						continue
					}
				}
			}
			l.Pos = save
		}
		if v != nil {
			out[key] = v
		}
	}
	return nil, errSyntax
}

func (l *Lexer) number() Object {
	start := l.Pos
	if c := l.b[l.Pos]; c == '+' || c == '-' {
		l.Pos++
	}
	dot := false
	for l.Pos < len(l.b) {
		c := l.b[l.Pos]
		if c == '.' && !dot {
			dot = true
		} else if c < '0' || c > '9' {
			break
		}
		l.Pos++
	}
	// Some writers leave a minus inside a number; skip the rest of the token.
	end := l.Pos
	for l.Pos < len(l.b) && !isSpace(l.b[l.Pos]) && !isDelim(l.b[l.Pos]) {
		if c := l.b[l.Pos]; c != '-' && c != '.' && (c < '0' || c > '9') {
			break
		}
		l.Pos++
	}
	s := string(l.b[start:end])
	if end == start+1 && (s == "+" || s == "-" || s == ".") {
		return int64(0)
	}
	if !dot {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return int64(0)
	}
	return v
}

// keyword reports the next word, without moving past it unless it matches.
func (l *Lexer) accept(w string) bool {
	l.skip()
	if bytes.HasPrefix(l.b[min(l.Pos, len(l.b)):], []byte(w)) {
		l.Pos += len(w)
		return true
	}
	return false
}

// intToken reads an unsigned integer token.
func (l *Lexer) intToken() (int, bool) {
	l.skip()
	start := l.Pos
	v := 0
	for l.Pos < len(l.b) && l.b[l.Pos] >= '0' && l.b[l.Pos] <= '9' && l.Pos-start < 12 {
		v = v*10 + int(l.b[l.Pos]-'0')
		l.Pos++
	}
	if l.Pos == start {
		return 0, false
	}
	return v, true
}
