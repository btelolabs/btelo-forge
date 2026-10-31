package xlsx

import (
	"bytes"
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/btelolabs/btelo-forge/internal/office/formula"
	"github.com/btelolabs/btelo-forge/internal/office/opc"
)

// Comma- and tab-separated files, held as a one-sheet workbook. A plain text file has no types, so what is read must
// come back out the same: only what is plainly a number becomes one, and a saved file keeps the encoding, separator
// and line ends it came with. Codes with leading zeros, long ids, dates and anything starting with = stay words, so
// that opening and saving a file changes nothing that was not edited, and nothing in a file is run as a formula.

// Text encodings of a delimited file.
const (
	encUTF8 = iota
	encUTF8BOM
	encUTF16LE
	encUTF16BE
	encLegacy
)

// maxDelimited is the most a delimited file may decode to.
const maxDelimited = 1 << 30

// Delimited is how a delimited file is written, and what its numbers looked like.
type Delimited struct {
	Comma    rune
	enc      int
	legacy   encoding.Encoding
	eol      string
	lastEOL  bool   // the file ends with a line end
	sep      string // a sep= line Excel reads, kept as it was
	width    int    // every row of the file had this many fields; 0 when they differed
	tail     int    // rows of nothing at the file's end
	numerals map[numeralKey]string
}

// numeralKey finds how a number was written: "1.50" stays that, though it is 1.5.
type numeralKey struct {
	col  int32
	bits uint64
}

// decodeText turns a file's bytes into text, and says how to turn it back.
func decodeText(data []byte, d *Delimited) (string, error) {
	switch {
	case bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}):
		d.enc = encUTF8BOM
		return string(data[3:]), nil
	case bytes.HasPrefix(data, []byte{0xff, 0xfe}), bytes.HasPrefix(data, []byte{0xfe, 0xff}):
		little := data[0] == 0xff
		d.enc = encUTF16BE
		if little {
			d.enc = encUTF16LE
		}
		data = data[2:]
		units := make([]uint16, len(data)/2)
		for i := range units {
			if little {
				units[i] = uint16(data[2*i]) | uint16(data[2*i+1])<<8
			} else {
				units[i] = uint16(data[2*i])<<8 | uint16(data[2*i+1])
			}
		}
		return string(utf16.Decode(units)), nil
	case utf8.Valid(data):
		d.enc = encUTF8
		return string(data), nil
	}
	// What is not UTF-8 is most often the system's legacy code page, here Chinese; a file that does not survive being
	// read and written that way falls to Latin, where every byte is a character, so no byte is ever lost.
	d.enc = encLegacy
	for _, e := range []encoding.Encoding{simplifiedchinese.GB18030, charmap.Windows1252, charmap.ISO8859_1} {
		text, err := e.NewDecoder().Bytes(data)
		if err != nil {
			continue
		}
		if back, err := e.NewEncoder().Bytes(text); err == nil && bytes.Equal(back, data) {
			d.legacy = e
			return string(text), nil
		}
	}
	return "", errors.New("the file's text cannot be read")
}

func (d *Delimited) encode(text string) ([]byte, error) {
	switch d.enc {
	case encUTF8:
		return []byte(text), nil
	case encUTF8BOM:
		return append([]byte{0xef, 0xbb, 0xbf}, text...), nil
	case encUTF16LE, encUTF16BE:
		units := utf16.Encode([]rune(text))
		out := make([]byte, 2, 2+len(units)*2)
		put := func(hi, lo byte) { out = append(out, hi, lo) }
		if d.enc == encUTF16LE {
			out[0], out[1] = 0xff, 0xfe
			for _, u := range units {
				put(byte(u), byte(u>>8))
			}
		} else {
			out[0], out[1] = 0xfe, 0xff
			for _, u := range units {
				put(byte(u>>8), byte(u))
			}
		}
		return out, nil
	}
	return encoding.ReplaceUnsupported(d.legacy.NewEncoder()).Bytes([]byte(text))
}

// separator finds what a file's fields are split by: the character that splits its first lines most evenly.
func separator(text string) rune {
	lines := splitRecords(text, '\x00', 32)
	best, bestScore := ',', 0
	for _, c := range []rune{',', '\t', ';', '|'} {
		count, even := 0, true
		for i, rec := range lines {
			n := strings.Count(quotesOut(rec[0]), string(c))
			if i == 0 {
				count = n
			} else if n != count {
				even = false
			}
		}
		score := count * 2
		if even {
			score = count*2 + 1
		}
		if count > 0 && score > bestScore {
			best, bestScore = c, score
		}
	}
	return best
}

// quotesOut is a line without what is between quotes.
func quotesOut(s string) string {
	if !strings.Contains(s, `"`) {
		return s
	}
	var b strings.Builder
	in := false
	for _, r := range s {
		if r == '"' {
			in = !in
		} else if !in {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// splitRecords cuts text into records of fields, at most limit records when limit is above zero. A quoted field may
// hold separators, line ends and doubled quotes; a stray quote inside a plain field is kept as it is.
func splitRecords(text string, comma rune, limit int) [][]string {
	var out [][]string
	var rec []string
	var f strings.Builder
	quoted, began := false, false
	end := func() {
		rec = append(rec, f.String())
		f.Reset()
		began = false
	}
	flush := func() {
		end()
		out = append(out, rec)
		rec = nil
	}
	for i := 0; i < len(text); {
		r, n := utf8.DecodeRuneInString(text[i:])
		switch {
		case quoted && r == '"':
			if i+1 < len(text) && text[i+1] == '"' {
				f.WriteByte('"')
				i += 2
				continue
			}
			quoted = false
		case quoted:
			f.WriteRune(r)
		case r == '"' && !began:
			quoted, began = true, true
		case r == comma:
			end()
		case r == '\r' && i+1 < len(text) && text[i+1] == '\n':
			i++
			flush()
		case r == '\n' || r == '\r':
			flush()
		default:
			began = true
			f.WriteRune(r)
		}
		i += n
		if limit > 0 && len(out) >= limit {
			return out
		}
	}
	if f.Len() > 0 || len(rec) > 0 || began {
		flush()
	}
	return out
}

// plainNumber reports a field that is a number and nothing else: "007", "+1", "1,5" and ids too long to be exact are not.
func plainNumber(s string) (float64, bool) {
	if s == "" || len(s) > 24 {
		return 0, false
	}
	t := strings.TrimPrefix(s, "-")
	if t == "" || t[0] < '0' || t[0] > '9' {
		return 0, false
	}
	if len(t) > 1 && t[0] == '0' && t[1] != '.' && t[1] != 'e' && t[1] != 'E' {
		return 0, false
	}
	digits := 0
	for _, c := range t {
		switch {
		case c >= '0' && c <= '9':
			digits++
		case c == '.' || c == 'e' || c == 'E' || c == '-' || c == '+':
		default:
			return 0, false
		}
	}
	if digits > 15 {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

func escapeXML(b *strings.Builder, s string) {
	for _, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '\t' || r == '\n' || r == '\r' || r >= 0x20 && r != 0xfffe && r != 0xffff:
			b.WriteRune(r)
		}
	}
}

// OpenDelimited reads a delimited file into a workbook of one sheet. tab says that a file with nothing else to go by is
// split by tabs.
func OpenDelimited(data []byte, name string, tab bool) (*Workbook, *Delimited, error) {
	d := &Delimited{numerals: map[numeralKey]string{}, eol: "\n"}
	text, err := decodeText(data, d)
	if err != nil {
		return nil, nil, err
	}
	if len(text) > maxDelimited {
		return nil, nil, errors.New("the file is too large")
	}
	if i := strings.IndexAny(text, "\r\n"); i >= 0 && text[i] == '\r' {
		d.eol = "\r\n"
		if i+1 >= len(text) || text[i+1] != '\n' {
			d.eol = "\r"
		}
	}
	d.lastEOL = strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\r")
	body := text
	if line, rest, ok := cutLine(text); ok && strings.HasPrefix(strings.ToLower(line), "sep=") {
		if r, n := utf8.DecodeRuneInString(line[4:]); n > 0 && n == len(line)-4 {
			d.Comma, d.sep, body = r, line, rest
		}
	}
	if d.Comma == 0 {
		d.Comma = separator(body)
		if tab && !strings.ContainsRune(body, d.Comma) {
			d.Comma = '\t'
		}
		if tab && strings.ContainsRune(body, '\t') {
			d.Comma = '\t'
		}
	}
	recs := splitRecords(body, d.Comma, 0)
	if len(recs) > MaxRows {
		return nil, nil, errors.New("the file has more rows than a sheet can hold")
	}
	var sheet strings.Builder
	sheet.Grow(len(body) * 2)
	sheet.WriteString(xmlDecl + `<worksheet xmlns="` + mainNS + `" xmlns:r="` + nsR + `"><sheetViews><sheetView workbookViewId="0"/></sheetViews><sheetFormatPr defaultRowHeight="15"/><sheetData>`)
	d.width = 0
	for r, rec := range recs {
		if len(rec) > MaxCols {
			return nil, nil, errors.New("the file has more columns than a sheet can hold")
		}
		if r == 0 {
			d.width = len(rec)
		} else if d.width != len(rec) {
			d.width = -1
		}
		row := strconv.Itoa(r + 1)
		sheet.WriteString(`<row r="` + row + `">`)
		for c, field := range rec {
			if field == "" {
				continue
			}
			ref := formula.ColName(c) + row
			if f, ok := plainNumber(field); ok {
				sheet.WriteString(`<c r="` + ref + `"><v>` + strconv.FormatFloat(f, 'g', -1, 64) + `</v></c>`)
				if field != strconv.FormatFloat(f, 'f', -1, 64) {
					key := numeralKey{int32(c), math.Float64bits(f)}
					if _, seen := d.numerals[key]; !seen {
						d.numerals[key] = field
					}
				}
				continue
			}
			sheet.WriteString(`<c r="` + ref + `" t="inlineStr"><is><t xml:space="preserve">`)
			escapeXML(&sheet, field)
			sheet.WriteString(`</t></is></c>`)
		}
		sheet.WriteString(`</row>`)
	}
	sheet.WriteString(`</sheetData><pageMargins left="0.7" right="0.7" top="0.75" bottom="0.75" header="0.3" footer="0.3"/></worksheet>`)
	if d.width < 0 {
		d.width = 0
	}
	for r := len(recs) - 1; r >= 0 && strings.Join(recs[r], "") == ""; r-- {
		d.tail++
	}
	blank, err := Blank(name)
	if err != nil {
		return nil, nil, err
	}
	p, err := opc.Open(blank)
	if err != nil {
		return nil, nil, err
	}
	p.SetData("xl/worksheets/sheet1.xml", []byte(sheet.String()))
	out, err := p.Bytes()
	if err != nil {
		return nil, nil, err
	}
	wb, err := Open(out)
	if err != nil {
		return nil, nil, err
	}
	return wb, d, nil
}

func cutLine(s string) (line, rest string, ok bool) {
	i := strings.IndexAny(s, "\r\n")
	if i < 0 {
		return s, "", false
	}
	if s[i] == '\r' && i+1 < len(s) && s[i+1] == '\n' {
		return s[:i], s[i+2:], true
	}
	return s[:i], s[i+1:], true
}

// fieldOf is what a cell puts in the file: what a number was written as, and what anything else shows.
func (d *Delimited) fieldOf(s *Sheet, r int, c *Cell) string {
	col := int(c.Col)
	v := s.ValueAt(r, col)
	switch v.Kind {
	case formula.Blank:
		return ""
	case formula.Number:
		if c.F == nil && s.wb.Styles != nil && s.StyleAt(r, col) == 0 {
			if t, ok := d.numerals[numeralKey{c.Col, math.Float64bits(v.Num)}]; ok {
				return t
			}
			return strconv.FormatFloat(v.Num, 'f', -1, 64)
		}
	case formula.Text:
		return v.Str
	}
	return s.Shown(r, col).Text
}

// Bytes writes the workbook's first sheet back in the file's own form.
func (d *Delimited) Bytes(wb *Workbook) ([]byte, error) {
	s := wb.Sheets[0]
	var b strings.Builder
	if d.sep != "" {
		b.WriteString(d.sep + d.eol)
	}
	rows := s.RowCount()
	// Trailing rows with nothing in them are not the file's.
	for rows > 0 && (s.RowAt(rows-1) == nil || len(s.RowAt(rows-1).Cells) == 0) {
		rows--
	}
	widest := d.width
	for r := range rows {
		if row := s.RowAt(r); row != nil && len(row.Cells) > 0 {
			widest = max(widest, int(row.Cells[len(row.Cells)-1].Col)+1)
		}
	}
	for r := range rows + d.tail {
		if r > 0 {
			b.WriteString(d.eol)
		}
		var cells []Cell
		if row := s.RowAt(r); r < rows && row != nil {
			cells = row.Cells
		}
		width := 0
		if len(cells) > 0 {
			width = int(cells[len(cells)-1].Col) + 1
		}
		// A file whose rows were all as wide stays so; otherwise a row ends where it does.
		if d.width > 0 {
			width = widest
		}
		k := 0
		for c := range width {
			if c > 0 {
				b.WriteRune(d.Comma)
			}
			if k < len(cells) && int(cells[k].Col) == c {
				d.writeField(&b, d.fieldOf(s, r, &cells[k]))
				k++
			}
		}
	}
	if d.lastEOL && rows+d.tail > 0 {
		b.WriteString(d.eol)
	}
	return d.encode(b.String())
}

func (d *Delimited) writeField(b *strings.Builder, s string) {
	if !strings.ContainsAny(s, "\"\r\n") && !strings.ContainsRune(s, d.Comma) {
		b.WriteString(s)
		return
	}
	b.WriteByte('"')
	b.WriteString(strings.ReplaceAll(s, `"`, `""`))
	b.WriteByte('"')
}
