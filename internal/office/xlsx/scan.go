package xlsx

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
	"unsafe"

	"github.com/btelolabs/btelo-forge/internal/office/formula"
)

// A scanner over a sheet's XML, by hand: encoding/xml would take ten times as long on a large sheet, and offsets into
// the source are what keep untouched cells exact. It reads only well-formed markup and fails on anything else.

// cellArena is how many cells are allocated at a time while reading.
const cellArena = 1 << 15

type scanner struct {
	b    []byte
	i    int
	kids [8]attr // scratch for tags inside a cell, as a local array would escape once per cell
}

// attr is an attribute as offsets into the source.
type attr struct {
	name []byte
	val  []byte
}

var errBadXML = errors.New("the worksheet's XML is broken")

// next moves to the next '<' and reports whether there was one.
func (z *scanner) next() bool {
	j := bytes.IndexByte(z.b[z.i:], '<')
	if j < 0 {
		z.i = len(z.b)
		return false
	}
	z.i += j
	return true
}

// skipMarkup steps over a comment or processing instruction at z.i; ok is false when there is none.
func (z *scanner) skipMarkup() (bool, error) {
	if z.i+1 >= len(z.b) {
		return false, errBadXML
	}
	switch z.b[z.i+1] {
	case '!':
		if bytes.HasPrefix(z.b[z.i:], []byte("<!--")) {
			j := bytes.Index(z.b[z.i:], []byte("-->"))
			if j < 0 {
				return false, errBadXML
			}
			z.i += j + 3
			return true, nil
		}
		return false, errBadXML
	case '?':
		j := bytes.Index(z.b[z.i:], []byte("?>"))
		if j < 0 {
			return false, errBadXML
		}
		z.i += j + 2
		return true, nil
	}
	return false, nil
}

// tag reads the start tag at z.i into attrs, and returns its name and whether it closes itself.
func (z *scanner) tag(attrs []attr) (name []byte, out []attr, closed bool, err error) {
	b, i := z.b, z.i+1
	start := i
	for i < len(b) && !isTagEnd(b[i]) {
		i++
	}
	name = b[start:i]
	out = attrs[:0]
	for {
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		if i >= len(b) {
			return nil, nil, false, errBadXML
		}
		if b[i] == '>' {
			z.i = i + 1
			return name, out, false, nil
		}
		if b[i] == '/' {
			if i+1 >= len(b) || b[i+1] != '>' {
				return nil, nil, false, errBadXML
			}
			z.i = i + 2
			return name, out, true, nil
		}
		ns := i
		for i < len(b) && b[i] != '=' && !isSpace(b[i]) && b[i] != '>' {
			i++
		}
		ne := i
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		if i >= len(b) || b[i] != '=' {
			return nil, nil, false, errBadXML
		}
		i++
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		if i >= len(b) || b[i] != '"' && b[i] != '\'' {
			return nil, nil, false, errBadXML
		}
		q := b[i]
		i++
		j := bytes.IndexByte(b[i:], q)
		if j < 0 {
			return nil, nil, false, errBadXML
		}
		out = append(out, attr{b[ns:ne], b[i : i+j]})
		i += j + 1
	}
}

func isSpace(c byte) bool { return c == ' ' || c == '\n' || c == '\t' || c == '\r' }

func isTagEnd(c byte) bool { return isSpace(c) || c == '>' || c == '/' }

// text reads character data up to the next tag, CDATA and entities resolved. The result may alias the source.
func (z *scanner) text() (string, error) {
	j := bytes.IndexByte(z.b[z.i:], '<')
	if j < 0 {
		return "", errBadXML
	}
	raw := z.b[z.i : z.i+j]
	z.i += j
	if !bytes.HasPrefix(z.b[z.i:], []byte("<![CDATA[")) {
		return unescape(raw)
	}
	var sb strings.Builder
	s, err := unescape(raw)
	if err != nil {
		return "", err
	}
	sb.WriteString(s)
	for {
		if bytes.HasPrefix(z.b[z.i:], []byte("<![CDATA[")) {
			z.i += 9
			e := bytes.Index(z.b[z.i:], []byte("]]>"))
			if e < 0 {
				return "", errBadXML
			}
			sb.Write(z.b[z.i : z.i+e])
			z.i += e + 3
			continue
		}
		j := bytes.IndexByte(z.b[z.i:], '<')
		if j < 0 {
			return "", errBadXML
		}
		if j == 0 {
			return sb.String(), nil
		}
		s, err := unescape(z.b[z.i : z.i+j])
		if err != nil {
			return "", err
		}
		sb.WriteString(s)
		z.i += j
	}
}

// unescape resolves XML's entities; text without any shares the source's bytes.
func unescape(b []byte) (string, error) {
	if bytes.IndexByte(b, '&') < 0 {
		return unsafe.String(unsafe.SliceData(b), len(b)), nil
	}
	var sb strings.Builder
	sb.Grow(len(b))
	for i := 0; i < len(b); {
		c := b[i]
		if c != '&' {
			sb.WriteByte(c)
			i++
			continue
		}
		e := bytes.IndexByte(b[i:], ';')
		if e < 0 || e > 12 {
			return "", errBadXML
		}
		ent := b[i+1 : i+e]
		i += e + 1
		switch string(ent) {
		case "amp":
			sb.WriteByte('&')
		case "lt":
			sb.WriteByte('<')
		case "gt":
			sb.WriteByte('>')
		case "quot":
			sb.WriteByte('"')
		case "apos":
			sb.WriteByte('\'')
		default:
			if len(ent) < 2 || ent[0] != '#' {
				return "", errBadXML
			}
			var n uint64
			var err error
			if ent[1] == 'x' || ent[1] == 'X' {
				n, err = strconv.ParseUint(string(ent[2:]), 16, 32)
			} else {
				n, err = strconv.ParseUint(string(ent[1:]), 10, 32)
			}
			if err != nil || n > utf8.MaxRune {
				return "", errBadXML
			}
			sb.WriteRune(rune(n))
		}
	}
	return sb.String(), nil
}

// decodeXString resolves the _xHHHH_ escapes OOXML strings use for characters XML cannot hold.
func decodeXString(s string) string {
	if !strings.Contains(s, "_x") {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '_' && i+6 < len(s) && s[i+1] == 'x' && s[i+6] == '_' {
			if n, err := strconv.ParseUint(s[i+2:i+6], 16, 16); err == nil {
				sb.WriteRune(rune(n))
				i += 7
				continue
			}
		}
		sb.WriteByte(s[i])
		i++
	}
	return sb.String()
}

// parseCellRef reads "AB12" fast.
func parseCellRef(b []byte) (row, col int32, ok bool) {
	i, c := 0, int32(0)
	for i < len(b) {
		ch := b[i]
		if ch >= 'a' && ch <= 'z' {
			ch -= 'a' - 'A'
		}
		if ch < 'A' || ch > 'Z' {
			break
		}
		c = c*26 + int32(ch-'A') + 1
		i++
		if i > 3 {
			return 0, 0, false
		}
	}
	if i == 0 || i == len(b) {
		return 0, 0, false
	}
	r := int32(0)
	for ; i < len(b); i++ {
		if b[i] < '0' || b[i] > '9' || r > MaxRows {
			return 0, 0, false
		}
		r = r*10 + int32(b[i]-'0')
	}
	if r < 1 || r > MaxRows || c > MaxCols {
		return 0, 0, false
	}
	return r - 1, c - 1, true
}

func atoi(b []byte) (int, bool) {
	n := 0
	if len(b) == 0 || len(b) > 10 {
		return 0, false
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// isTrue reads an xsd:boolean.
func isTrue(b []byte) bool { return len(b) == 1 && b[0] == '1' || string(b) == "true" }

func parseFloat(b []byte) (float64, error) {
	return strconv.ParseFloat(unsafe.String(unsafe.SliceData(b), len(b)), 64)
}

// scanRows reads the sheetData's contents (s.src) into s.rows. A shared formula's other cells are joined to their
// first when all are read, as a file need not list that one first.
func (s *Sheet) scanRows(limit *int64) error {
	z := &scanner{b: s.src}
	var attrs, cattrs []attr
	attrs, cattrs = make([]attr, 0, 16), make([]attr, 0, 16)
	arena := make([]Cell, cellArena)
	nextRow := int32(0)
	var buf []Cell
	s.shared = map[int32]*Formula{}
	type orphan struct {
		row  int32
		cell int32
		si   int32
	}
	var orphans []orphan
	for z.next() {
		if ok, err := z.skipMarkup(); err != nil {
			return err
		} else if ok {
			continue
		}
		start := z.i
		name, as, closed, err := z.tag(attrs)
		if err != nil {
			return err
		}
		attrs = as
		if string(name) != "row" {
			return fmt.Errorf("%w: <%s> in sheetData", errBadXML, name)
		}
		row := &Row{orig: -1, raw: uint32(start)}
		rn := nextRow
		for _, a := range attrs {
			switch string(a.name) {
			case "r":
				n, ok := atoi(a.val)
				if !ok || n < 1 || n > MaxRows {
					return fmt.Errorf("%w: row number %q", errBadXML, a.val)
				}
				rn = int32(n - 1)
			case "ht":
				if h, err := parseFloat(a.val); err == nil && h >= 0 && h <= 8192 {
					row.Height = h
				}
			case "customHeight":
				row.Manual = isTrue(a.val)
			case "hidden":
				row.Hidden = isTrue(a.val)
			case "s":
				if n, ok := atoi(a.val); ok {
					row.Style = int32(n)
				}
			case "customFormat":
				row.Custom = isTrue(a.val)
			}
		}
		row.orig, row.headLen = rn, uint32(z.i-start)
		buf = buf[:0]
		col := int32(0)
		for !closed {
			if !z.next() {
				return errBadXML
			}
			if ok, err := z.skipMarkup(); err != nil {
				return err
			} else if ok {
				continue
			}
			if z.b[z.i+1] == '/' {
				e := bytes.IndexByte(z.b[z.i:], '>')
				if e < 0 {
					return errBadXML
				}
				z.i += e + 1
				break
			}
			var c Cell
			var si int32 = -1
			cstart := z.i
			name, as, cclosed, err := z.tag(cattrs)
			if err != nil {
				return err
			}
			cattrs = as
			if string(name) != "c" {
				// Something else in the row, kept by the row's own text: step over it.
				if !cclosed {
					if err := z.skipTo(name); err != nil {
						return err
					}
				}
				continue
			}
			var t []byte
			c.Col = col
			for _, a := range cattrs {
				switch string(a.name) {
				case "r":
					_, cn, ok := parseCellRef(a.val)
					if !ok {
						return fmt.Errorf("%w: cell %q", errBadXML, a.val)
					}
					c.Col = cn
				case "s":
					if n, ok := atoi(a.val); ok {
						c.Style = int32(n)
					}
				case "t":
					t = a.val
				}
			}
			col = c.Col + 1
			c.raw = uint32(cstart)
			if !cclosed {
				if si, err = s.scanCell(z, &c, t, rn); err != nil {
					return err
				}
			}
			c.rawLen = uint32(z.i - cstart)
			if si >= 0 {
				orphans = append(orphans, orphan{rn, int32(len(buf)), si})
			}
			buf = append(buf, c)
		}
		row.rawLen = uint32(z.i - start)
		if len(buf) > 0 {
			// Office refuses cells out of order, so nothing good would come of guessing.
			sorted := true
			for i := 1; i < len(buf); i++ {
				if buf[i].Col <= buf[i-1].Col {
					sorted = false
					break
				}
			}
			if !sorted {
				return fmt.Errorf("%w: the cells of row %d are out of order", errBadXML, rn+1)
			}
			if len(arena) < len(buf) {
				arena = make([]Cell, max(cellArena, len(buf)))
			}
			n := len(buf)
			copy(arena, buf)
			row.Cells = arena[:n:n]
			arena = arena[n:]
		}
		if int(rn) >= len(s.rows) {
			if int64(rn)+1-int64(len(s.rows)) > *limit {
				return errors.New("the workbook claims too many rows")
			}
			*limit -= int64(rn) + 1 - int64(len(s.rows))
			n := max(int(rn)+1, min(2*len(s.rows), MaxRows))
			if int(rn) >= cap(s.rows) {
				grown := make([]*Row, len(s.rows), n)
				copy(grown, s.rows)
				s.rows = grown
			}
			s.rows = s.rows[:int(rn)+1]
		}
		if s.rows[rn] != nil {
			return fmt.Errorf("%w: row %d is there twice", errBadXML, rn+1)
		}
		s.rows[rn] = row
		nextRow = rn + 1
	}
	for _, o := range orphans {
		row := s.rows[o.row]
		c := &row.Cells[o.cell]
		f := s.shared[o.si]
		if f == nil {
			continue // no first cell: it keeps its cached value
		}
		m := f
		c.F = &Formula{Expr: m.Expr, DR: o.row - m.DR, DC: c.Col - m.DC, Shared: o.si}
	}
	return nil
}

// skipTo moves past the end tag of name, with nesting.
func (z *scanner) skipTo(name []byte) error {
	depth := 1

	for z.next() {
		if ok, err := z.skipMarkup(); err != nil {
			return err
		} else if ok {
			continue
		}
		if z.b[z.i+1] == '/' {
			e := bytes.IndexByte(z.b[z.i:], '>')
			if e < 0 {
				return errBadXML
			}
			if bytes.Equal(z.b[z.i+2:z.i+e], name) {
				depth--
			}
			z.i += e + 1
			if depth == 0 {
				return nil
			}
			continue
		}
		if bytes.HasPrefix(z.b[z.i:], []byte("<![CDATA[")) {
			e := bytes.Index(z.b[z.i:], []byte("]]>"))
			if e < 0 {
				return errBadXML
			}
			z.i += e + 3
			continue
		}
		n, _, closed, err := z.tag(z.kids[:0])
		if err != nil {
			return err
		}
		if !closed && bytes.Equal(n, name) {
			depth++
		}
	}
	return errBadXML
}

// scanCell reads a cell's children into c. A shared formula's cell without text returns its group to be joined later.
func (s *Sheet) scanCell(z *scanner, c *Cell, t []byte, row int32) (si int32, err error) {
	si = -1

	var v string
	hasV := false
	for {
		if !z.next() {
			return si, errBadXML
		}
		if ok, err := z.skipMarkup(); err != nil {
			return si, err
		} else if ok {
			continue
		}
		if z.b[z.i+1] == '/' {
			e := bytes.IndexByte(z.b[z.i:], '>')
			if e < 0 {
				return si, errBadXML
			}
			z.i += e + 1
			break
		}
		name, as, closed, err := z.tag(z.kids[:0])
		if err != nil {
			return si, err
		}
		switch string(name) {
		case "v":
			if closed {
				continue
			}
			if v, err = z.text(); err != nil {
				return si, err
			}
			hasV = true
			if err := z.endTag(name); err != nil {
				return si, err
			}
		case "f":
			var kind, ref []byte
			shared := int32(-1)
			for _, a := range as {
				switch string(a.name) {
				case "t":
					kind = a.val
				case "ref":
					ref = a.val
				case "si":
					if n, ok := atoi(a.val); ok {
						shared = int32(n)
					}
				}
			}
			text := ""
			if !closed {
				if text, err = z.text(); err != nil {
					return si, err
				}
				if err := z.endTag(name); err != nil {
					return si, err
				}
				text = strings.Clone(text)
			}
			switch {
			case string(kind) == "dataTable":
				// What-if tables are the file's business: the value stays as it is.
			case string(kind) == "shared" && text == "":
				si = shared
			default:
				f := &Formula{Text: text, Shared: -1}
				if ex, err := formula.Parse(text); err == nil {
					f.Expr = ex
				}
				if string(kind) == "shared" && shared >= 0 {
					f.Shared = shared
					f.DR, f.DC = 0, 0
					s.shared[shared] = &Formula{Expr: f.Expr, DR: row, DC: c.Col}
					s.nextSI = max(s.nextSI, shared+1)
				}
				if string(kind) == "array" {
					f.Array = string(ref)
				}
				c.F = f
			}
		case "is":
			if closed {
				continue
			}
			text, err := z.inlineText()
			if err != nil {
				return si, err
			}
			c.Text, c.Kind = decodeXString(text), InlineS
		default:
			if !closed {
				if err := z.skipTo(name); err != nil {
					return si, err
				}
			}
		}
	}
	if c.Kind == InlineS {
		return si, nil
	}
	switch string(t) {
	case "", "n":
		if hasV && v != "" {
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return si, fmt.Errorf("%w: the number %q", errBadXML, v)
			}
			c.Num, c.Kind = n, Number
		}
	case "s":
		if hasV {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return si, fmt.Errorf("%w: the shared string %q", errBadXML, v)
			}
			c.Num, c.Kind = float64(n), SharedS
		}
	case "str":
		if hasV {
			c.Text, c.Kind = decodeXString(strings.Clone(v)), FormulaS
		}
	case "b":
		c.Num, c.Kind = 0, Bool
		if v == "1" || v == "true" {
			c.Num = 1
		}
	case "e":
		if hasV {
			c.Text, c.Kind = strings.Clone(v), ErrorV
		}
	case "d":
		// An ISO date: strict files use these. Shown as text, kept as written.
		if hasV {
			c.Text, c.Kind = strings.Clone(v), InlineS
		}
	}
	return si, nil
}

// endTag steps over the end tag at z.i, which must be name's.
func (z *scanner) endTag(name []byte) error {
	e := bytes.IndexByte(z.b[z.i:], '>')
	if e < 0 || z.i+2+len(name) > len(z.b) || z.b[z.i+1] != '/' || !bytes.Equal(z.b[z.i+2:z.i+2+len(name)], name) {
		return errBadXML
	}
	z.i += e + 1
	return nil
}

// inlineText reads an <is> or <si> element's text up to its end: the <t> runs, without phonetic hints.
func (z *scanner) inlineText() (string, error) {
	var out string
	var sb *strings.Builder

	depthPh := 0
	for {
		if !z.next() {
			return "", errBadXML
		}
		if ok, err := z.skipMarkup(); err != nil {
			return "", err
		} else if ok {
			continue
		}
		if z.b[z.i+1] == '/' {
			e := bytes.IndexByte(z.b[z.i:], '>')
			if e < 0 {
				return "", errBadXML
			}
			n := z.b[z.i+2 : z.i+e]
			z.i += e + 1
			switch string(n) {
			case "is", "si":
				if sb != nil {
					return sb.String(), nil
				}
				return out, nil
			case "rPh":
				depthPh--
			}
			continue
		}
		name, _, closed, err := z.tag(z.kids[:0])
		if err != nil {
			return "", err
		}
		if closed {
			continue
		}
		switch string(name) {
		case "t":
			text, err := z.text()
			if err != nil {
				return "", err
			}
			if err := z.endTag(name); err != nil {
				return "", err
			}
			if depthPh > 0 {
				continue
			}
			switch {
			case sb != nil:
				sb.WriteString(text)
			case out == "":
				out = text
			default:
				sb = &strings.Builder{}
				sb.WriteString(out)
				sb.WriteString(text)
			}
		case "rPh":
			depthPh++
		}
	}
}
