package xlsx

import (
	"bytes"
	"errors"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/btelolabs/btelo-forge/internal/office/formula"
	"github.com/btelolabs/btelo-forge/internal/office/opc"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// sheetOut is what the writer needs of a sheet, unchanged by later edits.
type sheetOut struct {
	part string
	head []byte
	tail []byte
	rows []*Row
	src  []byte
}

// Snapshot takes what is to be saved, on the editing goroutine, and returns the work of writing it, which may run on
// another. Formulas are brought up to date first, so the file's cached values are fresh; fullCalcOnLoad tells Office
// to work them out again anyway, as it knows functions this does not. Rows are frozen by moving to a new generation:
// the writer reads them while edits go on to copies.
func (wb *Workbook) Snapshot() (func() ([]byte, error), error) {
	wb.calc.Recalc(0)
	wb.gen++
	strs := wb.strs
	if len(strs.items) > strs.base && strs.part == "" {
		strs.part = "xl/sharedStrings.xml"
		if wb.pkg.Part(strs.part) != nil {
			strs.part = wb.pkg.UniqueName("xl/sharedStrings%d.xml")
		}
		strs.data = []byte(xmlDecl + `<sst xmlns="` + mainNS + `" count="0" uniqueCount="0"></sst>`)
		wb.pkg.Put(strs.part, ctStrings, strs.data)
		wb.pkg.AddRel(wb.part, relStrings, opc.RelativeTarget(wb.part, strs.part), false)
	}
	pkg := wb.pkg.Clone()
	doc := wb.doc.Clone()
	root := doc.Root
	root.Ensure("calcPr", workbookOrder).SetAttr("fullCalcOnLoad", "1")
	if _, ok := root.Lookup("xmlns:r"); !ok {
		root.SetAttr("xmlns:r", nsR)
	}
	rels, err := pkg.Rels(wb.part)
	if err != nil {
		return nil, err
	}
	// Sheets deleted since the file was read leave it; new ones get a part. All of it on the copy, so an undo finds the
	// live package as it was.
	els := root.Child("sheets").Elements("sheet")
	byID := make(map[string]*ox.Node, len(els))
	for _, e := range els {
		byID[e.Attr("sheetId")] = e
	}
	live := map[string]bool{}
	var out []*sheetOut
	var sheets []*Sheet
	for _, s := range wb.Sheets {
		e := byID[s.id]
		if e == nil {
			return nil, errors.New("a sheet is missing from the workbook")
		}
		if s.part != "" {
			live[s.rid] = true
			if s.touched && !s.chart {
				out = append(out, &sheetOut{part: s.part})
				sheets = append(sheets, s)
			}
			continue
		}
		name := pkg.UniqueName("xl/worksheets/sheet%d.xml")
		pkg.Put(name, ctWorksheet, nil)
		rid := pkg.AddRel(wb.part, relWorksheet, opc.RelativeTarget(wb.part, name), false)
		e.SetAttr("r:id", rid)
		live[rid] = true
		out = append(out, &sheetOut{part: name})
		sheets = append(sheets, s)
	}
	for _, r := range rels {
		if (r.Type == relWorksheet || strings.HasSuffix(r.Type, "/worksheet")) && !live[r.ID] {
			pkg.DeleteRel(wb.part, r.ID)
			pkg.Delete(opc.Resolve(wb.part, r.Target))
		}
	}
	if len(out) > 0 {
		// The calculation chain lists formula cells; a stale one makes Office call the file corrupt. It rebuilds it.
		for _, r := range rels {
			if r.Type == relCalcChain {
				pkg.DeleteRel(wb.part, r.ID)
				pkg.Delete(opc.Resolve(wb.part, r.Target))
			}
		}
	}
	pkg.PutDoc(wb.part, doc)
	if wb.Styles.changed {
		pkg.PutDoc(wb.Styles.part, wb.Styles.doc)
	}
	for i, o := range out {
		s := sheets[i]
		s.updateDimension()
		data := s.doc.Bytes()
		at := bytes.Index(data, []byte(rowsMark))
		if at < 0 {
			return nil, errors.New("a worksheet lost its rows")
		}
		o.head, o.tail = data[:at], data[at+len(rowsMark):]
		o.rows, o.src = slices.Clone(s.rows), s.src
	}
	newStrs := strs.items[strs.base:len(strs.items):len(strs.items)]
	part, base := strs.part, strs.data
	return func() ([]byte, error) {
		for _, o := range out {
			pkg.SetData(o.part, o.render())
		}
		if len(newStrs) > 0 {
			data, err := appendStrings(base, newStrs)
			if err != nil {
				return nil, err
			}
			pkg.SetData(part, data)
		}
		return pkg.Bytes()
	}, nil
}

// updateDimension keeps <dimension> honest; readers use it to size their tables.
func (s *Sheet) updateDimension() {
	ref := formula.AreaName(s.Used())
	root := s.doc.Root
	if d := root.Child("dimension"); d != nil {
		d.SetAttr("ref", ref)
		return
	}
	root.InsertOrdered(ox.New("dimension", "ref", ref), worksheetOrder)
}

// appendStrings adds <si> elements before the end of a shared strings part and corrects its counts.
func appendStrings(base []byte, items []strItem) ([]byte, error) {
	z := &scanner{b: base}
	var attrs [8]attr
	for z.next() {
		ok, err := z.skipMarkup()
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
	}
	if z.i >= len(base) {
		return nil, errors.New("the shared strings part is broken")
	}
	start := z.i
	name, as, closed, err := z.tag(attrs[:0])
	if err != nil {
		return nil, err
	}
	prefix := ""
	if k := bytes.IndexByte(name, ':'); k >= 0 {
		prefix = string(name[:k+1])
	}
	body := base[z.i:]
	if !closed {
		e := bytes.LastIndex(body, []byte("</"))
		if e < 0 {
			return nil, errors.New("the shared strings part is broken")
		}
		body = body[:e]
	}
	var b bytes.Buffer
	b.Grow(len(base) + len(items)*32)
	b.Write(base[:start+1])
	b.Write(name)
	total := 0
	for _, a := range as {
		if string(a.name) == "uniqueCount" {
			total, _ = atoi(a.val)
		}
	}
	for _, a := range as {
		v := a.val
		switch string(a.name) {
		case "count":
			n, _ := atoi(v)
			v = strconv.AppendInt(nil, int64(n+len(items)), 10)
		case "uniqueCount":
			v = strconv.AppendInt(nil, int64(total+len(items)), 10)
		}
		b.WriteByte(' ')
		b.Write(a.name)
		b.WriteString(`="`)
		b.Write(v)
		b.WriteByte('"')
	}
	b.WriteByte('>')
	b.Write(body)
	for i := range items {
		b.WriteString("<" + prefix + "si><" + prefix + "t")
		if needsPreserve(items[i].text) {
			b.WriteString(` xml:space="preserve"`)
		}
		b.WriteByte('>')
		writeXString(&b, items[i].text)
		b.WriteString("</" + prefix + "t></" + prefix + "si>")
	}
	b.WriteString("</")
	b.Write(name)
	b.WriteByte('>')
	return b.Bytes(), nil
}

func needsPreserve(s string) bool {
	return s != "" && (s[0] == ' ' || s[len(s)-1] == ' ' || strings.ContainsAny(s, "\n\t\r"))
}

// writeXString writes a string the way OOXML holds them: XML escapes, and _xHHHH_ for what XML cannot hold.
func writeXString(b *bytes.Buffer, s string) {
	const hex = "0123456789ABCDEF"
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '_' && i+6 < len(s) && s[i+1] == 'x' && s[i+6] == '_' && isHex(s[i+2:i+6]):
			b.WriteString("_x005F_")
		case r < 0x20 && r != '\n' && r != '\t' || r == 0xfffe || r == 0xffff:
			b.WriteString("_x")
			b.WriteByte(hex[r>>12&15])
			b.WriteByte(hex[r>>8&15])
			b.WriteByte(hex[r>>4&15])
			b.WriteByte(hex[r&15])
			b.WriteByte('_')
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == utf8.RuneError && n == 1:
		default:
			b.WriteString(s[i : i+n])
		}
		i += n
	}
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// escXML writes text for a formula or attribute.
func escXML(b *bytes.Buffer, s string, attribute bool) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '&':
			b.WriteString("&amp;")
		case c == '<':
			b.WriteString("&lt;")
		case c == '>':
			b.WriteString("&gt;")
		case c == '"' && attribute:
			b.WriteString("&quot;")
		case c < 0x20 && c != '\n' && c != '\t':
		default:
			b.WriteByte(c)
		}
	}
}

// render writes the worksheet.
func (o *sheetOut) render() []byte {
	var b bytes.Buffer
	b.Grow(len(o.head) + len(o.tail) + len(o.src) + len(o.src)/8 + 1024)
	b.Write(o.head)
	b.WriteString("<sheetData>")
	var ref [16]byte
	for i, row := range o.rows {
		if row == nil {
			continue
		}
		if !row.dirty && int(row.orig) == i && row.rawLen > 0 {
			b.Write(o.src[row.raw : row.raw+row.rawLen])
			continue
		}
		if row.rawLen == 0 && len(row.Cells) == 0 && !row.Hidden && !row.Custom && row.Height == 0 {
			continue
		}
		o.writeRow(&b, i, row, ref[:0])
	}
	b.WriteString("</sheetData>")
	b.Write(o.tail)
	return b.Bytes()
}

func (o *sheetOut) writeRow(b *bytes.Buffer, i int, row *Row, ref []byte) {
	b.WriteString(`<row r="`)
	b.WriteString(strconv.Itoa(i + 1))
	b.WriteByte('"')
	writeAttr := func(name, v string) {
		b.WriteByte(' ')
		b.WriteString(name)
		b.WriteString(`="`)
		b.WriteString(v)
		b.WriteByte('"')
	}
	if row.rawLen > 0 {
		z := &scanner{b: o.src[row.raw : row.raw+row.headLen]}
		z.next()
		var buf [24]attr
		_, as, _, _ := z.tag(buf[:0])
		for _, a := range as {
			switch string(a.name) {
			case "r", "spans", "ht", "customHeight", "hidden", "customFormat", "s":
			default:
				b.WriteByte(' ')
				b.Write(a.name)
				b.WriteString(`="`)
				b.Write(a.val)
				b.WriteByte('"')
			}
		}
	}
	if row.Custom {
		writeAttr("s", strconv.Itoa(int(row.Style)))
		writeAttr("customFormat", "1")
	}
	if row.Height > 0 {
		writeAttr("ht", strconv.FormatFloat(row.Height, 'g', -1, 64))
	}
	if row.Manual {
		writeAttr("customHeight", "1")
	}
	if row.Hidden {
		writeAttr("hidden", "1")
	}
	b.WriteByte('>')
	moved := int(row.orig) != i
	for k := range row.Cells {
		c := &row.Cells[k]
		if c.flags == 0 && !moved && c.rawLen > 0 {
			b.Write(o.src[c.raw : c.raw+c.rawLen])
			continue
		}
		ref = strconv.AppendInt(appendCol(ref[:0], int(c.Col)), int64(i+1), 10)
		o.writeCell(b, c, ref)
	}
	o.rowExtras(b, row)
	b.WriteString("</row>")
}

// rowExtras writes what a row held besides cells, as it stood.
func (o *sheetOut) rowExtras(b *bytes.Buffer, row *Row) {
	if row.rawLen == 0 {
		return
	}
	z := &scanner{b: o.src[row.raw : row.raw+row.rawLen]}
	z.i = int(row.headLen)
	for z.next() {
		if z.b[z.i+1] == '/' {
			return
		}
		if ok, err := z.skipMarkup(); err != nil {
			return
		} else if ok {
			continue
		}
		start := z.i
		name, _, closed, err := z.tag(z.kids[:0])
		if err != nil {
			return
		}
		if !closed && z.skipTo(name) != nil {
			return
		}
		if string(name) != "c" {
			b.Write(z.b[start:z.i])
		}
	}
}

func appendCol(b []byte, c int) []byte {
	var tmp [4]byte
	n := len(tmp)
	for c++; c > 0; c = (c - 1) / 26 {
		n--
		tmp[n] = byte('A' + (c-1)%26)
	}
	return append(b, tmp[n:]...)
}

// typeAttr is the cell's t.
func typeAttr(c *Cell) string {
	switch c.Kind {
	case SharedS:
		return "s"
	case InlineS:
		return "inlineStr"
	case FormulaS:
		return "str"
	case Bool:
		return "b"
	case ErrorV:
		return "e"
	}
	return ""
}

// writeCell writes a cell: its old XML with what changed replaced, every other attribute and child as it was.
func (o *sheetOut) writeCell(b *bytes.Buffer, c *Cell, ref []byte) {
	b.WriteString(`<c r="`)
	b.Write(ref)
	b.WriteByte('"')
	var rest [][]byte // children to keep after the value, as they stand
	var f, is []byte  // the formula and inline string elements, as they stand
	keptT := false
	if c.rawLen > 0 {
		z := &scanner{b: o.src[c.raw : c.raw+c.rawLen]}
		z.next()
		var buf [16]attr
		_, as, closed, _ := z.tag(buf[:0])
		for _, a := range as {
			switch string(a.name) {
			case "r", "s":
			case "t":
				if c.flags&flagValue == 0 {
					keptT = true
					b.WriteString(` t="`)
					b.Write(a.val)
					b.WriteByte('"')
				}
			default:
				b.WriteByte(' ')
				b.Write(a.name)
				b.WriteString(`="`)
				b.Write(a.val)
				b.WriteByte('"')
			}
		}
		for !closed && z.next() {
			if z.b[z.i+1] == '/' {
				break
			}
			if ok, err := z.skipMarkup(); err != nil {
				break
			} else if ok {
				continue
			}
			start := z.i
			var inner [8]attr
			name, _, childClosed, err := z.tag(inner[:0])
			if err != nil {
				break
			}
			if !childClosed && z.skipTo(name) != nil {
				break
			}
			switch string(name) {
			case "v":
			case "f":
				f = z.b[start:z.i]
			case "is":
				is = z.b[start:z.i]
			default:
				rest = append(rest, z.b[start:z.i])
			}
		}
	}
	if c.Style != 0 {
		b.WriteString(` s="`)
		b.WriteString(strconv.Itoa(int(c.Style)))
		b.WriteByte('"')
	}
	if t := typeAttr(c); t != "" && !keptT {
		b.WriteString(` t="`)
		b.WriteString(t)
		b.WriteByte('"')
	}
	var fx []byte
	var fb bytes.Buffer
	switch {
	case c.F == nil:
	case c.flags&flagFormula == 0 && f != nil:
		fx = f
	default:
		if c.F.Array != "" {
			fb.WriteString(`<f t="array" ref="`)
			escXML(&fb, c.F.Array, true)
			fb.WriteString(`">`)
		} else {
			fb.WriteString("<f>")
		}
		escXML(&fb, c.FormulaText(), false)
		fb.WriteString("</f>")
		fx = fb.Bytes()
	}
	val := cellValueXML(c, is)
	if len(fx) == 0 && len(val) == 0 && len(rest) == 0 {
		b.WriteString("/>")
		return
	}
	b.WriteByte('>')
	b.Write(fx)
	b.Write(val)
	for _, r := range rest {
		b.Write(r)
	}
	b.WriteString("</c>")
}

// cellValueXML is the <v> or <is> element.
func cellValueXML(c *Cell, is []byte) []byte {
	if is != nil && c.flags&flagValue == 0 {
		return is
	}
	var b bytes.Buffer
	switch c.Kind {
	case Number, Bool:
		b.WriteString("<v>")
		b.WriteString(strconv.FormatFloat(c.Num, 'g', -1, 64))
		b.WriteString("</v>")
	case SharedS:
		b.WriteString("<v>")
		b.WriteString(strconv.Itoa(int(c.Num)))
		b.WriteString("</v>")
	case FormulaS, ErrorV:
		b.WriteString("<v>")
		writeXString(&b, c.Text)
		b.WriteString("</v>")
	case InlineS:
		b.WriteString("<is><t")
		if needsPreserve(c.Text) {
			b.WriteString(` xml:space="preserve"`)
		}
		b.WriteByte('>')
		writeXString(&b, c.Text)
		b.WriteString("</t></is>")
	}
	return b.Bytes()
}
