package xlsx

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/office/formula"
	"github.com/btelolabs/btelo-forge/internal/office/opc"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
)

// Edits: all of them run inside a step (Workbook.Begin) and leave the engine told what changed; results follow from
// Workbook.Calc.

// maxStyledCells bounds a format applied to a range: past it, only rows in use are formatted.
const maxStyledCells = 4 << 20

// Calc works through pending formulas for at most budget (0 for all) and reports whether all are done.
func (wb *Workbook) Calc(budget time.Duration) bool { return wb.calc.Recalc(budget) }

// Pending reports formulas not yet worked out.
func (wb *Workbook) Pending() bool { return wb.calc.Pending() }

// Circular lists the cells found in a circular reference.
func (wb *Workbook) Circular() []formula.Cell { return wb.calc.Circular() }

// Refresh recalculates the volatile formulas.
func (wb *Workbook) Refresh() { wb.calc.Refresh() }

func (s *Sheet) at(r, c int) formula.Cell {
	return formula.Cell{Sheet: int32(s.index()), Row: int32(r), Col: int32(c)}
}

// StyleAt is the style index a cell shows: its own, its row's or its column's.
func (s *Sheet) StyleAt(r, c int) int32 {
	row := s.RowAt(r)
	if row != nil {
		if i, ok := row.find(int32(c)); ok {
			return row.Cells[i].Style
		}
		if row.Custom {
			return row.Style
		}
	}
	return s.ColInfo(c).Style
}

// ColInfo is what the sheet says about a column.
func (s *Sheet) ColInfo(c int) Col {
	i, ok := slices.BinarySearchFunc(s.cols, c, func(k Col, c int) int {
		switch {
		case k.Max < c:
			return -1
		case k.Min > c:
			return 1
		}
		return 0
	})
	if ok && s.cols[i].Width >= 0 {
		return s.cols[i]
	}
	if ok {
		k := s.cols[i]
		k.Width = s.defW
		return k
	}
	return Col{Min: c, Max: c, Width: s.defW}
}

// ColPixels is a column's width at 100%, zero when hidden: the file's characters turned into pixels as Excel does with
// a seven-pixel digit.
func (s *Sheet) ColPixels(c int) float64 {
	k := s.ColInfo(c)
	if k.Hidden {
		return 0
	}
	return math.Trunc((256*k.Width + math.Trunc(128.0/7)) / 256 * 7)
}

// RowPoints is a row's height in points, zero when hidden.
func (s *Sheet) RowPoints(r int) float64 {
	row := s.RowAt(r)
	if row == nil {
		return s.defH
	}
	if row.Hidden {
		return 0
	}
	if row.Height > 0 {
		return row.Height
	}
	return s.defH
}

// Hidden reports the sheet's state.
func (s *Sheet) Hidden() bool { return s.hidden }

// Freeze is how many rows and columns are frozen.
func (s *Sheet) Freeze() (rows, cols int) { return s.freezeR, s.freezeC }

// TabColor is the tab's colour as RRGGBB, "" for none.
func (s *Sheet) TabColor() string { return s.tab }

// Merges are the merged areas.
func (s *Sheet) Merges() []formula.Area { return s.merges }

// MergeAt is the merged area holding the cell.
func (s *Sheet) MergeAt(r, c int) (formula.Area, bool) {
	for _, i := range s.mergeIdx[int32(r)>>6] {
		m := s.merges[i]
		if int(m.R1) <= r && r <= int(m.R2) && int(m.C1) <= c && c <= int(m.C2) {
			return m, true
		}
	}
	return formula.Area{}, false
}

// put stores a value and formula in a cell.
func (s *Sheet) put(r, c int, v formula.Value, f *Formula) {
	old := s.Cell(r, c)
	if old != nil && old.F != nil {
		s.dropFormula(old)
	}
	style := s.StyleAt(r, c)
	row := s.writable(r)
	cell := &row.Cells[row.ensure(int32(c))]
	if old == nil {
		cell.Style = style
		s.noteCol(c)
	}
	s.setKind(cell, v)
	cell.F = f
	cell.flags |= flagValue | flagFormula
	at := s.at(r, c)
	s.wb.setEngine(at, cell)
	s.wb.calc.Touch(at)
}

func (s *Sheet) setKind(cell *Cell, v formula.Value) {
	cell.Text, cell.Num = "", 0
	switch v.Kind {
	case formula.Blank:
		cell.Kind = Empty
	case formula.Number:
		cell.Kind, cell.Num = Number, v.Num
	case formula.Text:
		cell.Kind, cell.Num = SharedS, float64(s.wb.strs.add(v.Str))
	case formula.Bool:
		cell.Kind, cell.Num = Bool, v.Num
	case formula.Error:
		cell.Kind, cell.Text = ErrorV, v.Str
	}
}

// dropFormula is told a cell's formula is going: the first cell of a shared group passes its text to the rest, and an
// array formula's other cells go with it.
func (s *Sheet) dropFormula(c *Cell) {
	f := c.F
	if f.Shared >= 0 && f.Text != "" {
		s.expandGroups(map[int32]bool{f.Shared: true})
	}
	if f.Array != "" {
		if a, ok := formula.ParseArea(f.Array); ok {
			for r := int(a.R1); r <= int(a.R2); r++ {
				for k := int(a.C1); k <= int(a.C2); k++ {
					if cell := s.Cell(r, k); cell != nil && cell.F == nil && cell.Kind != Empty {
						row := s.writable(r)
						i, _ := row.find(int32(k))
						row.Cells[i].Kind, row.Cells[i].Num, row.Cells[i].Text = Empty, 0, ""
						row.Cells[i].flags |= flagValue
					}
				}
			}
		}
	}
}

// expandGroups turns shared formulas into plain ones, each cell with its own text. Nil is every group in the sheet.
func (s *Sheet) expandGroups(groups map[int32]bool) {
	for r, row := range s.rows {
		if row == nil {
			continue
		}
		changed := false
		for i := range row.Cells {
			if f := row.Cells[i].F; f != nil && f.Shared >= 0 && (groups == nil || groups[f.Shared]) {
				changed = true
				break
			}
		}
		if !changed {
			continue
		}
		row = s.writable(r)
		for i := range row.Cells {
			c := &row.Cells[i]
			if f := c.F; f != nil && f.Shared >= 0 && (groups == nil || groups[f.Shared]) {
				c.F = &Formula{Expr: f.Expr, Text: c.FormulaText(), DR: f.DR, DC: f.DC, Shared: -1}
				c.flags |= flagFormula
			}
		}
	}
}

// SetValue stores a value, or a text as it is.
func (s *Sheet) SetValue(r, c int, v formula.Value) error {
	if err := inSheet(r, c); err != nil {
		return err
	}
	s.put(r, c, v, nil)
	return nil
}

func inSheet(r, c int) error {
	if r < 0 || c < 0 || r >= MaxRows || c >= MaxCols {
		return errors.New("that cell is outside the sheet")
	}
	return nil
}

// SetFormula stores a formula, text without its =.
func (s *Sheet) SetFormula(r, c int, text string) error {
	if err := inSheet(r, c); err != nil {
		return err
	}
	ex, err := formula.Parse(text)
	if err != nil {
		return err
	}
	s.put(r, c, formula.Value{}, &Formula{Expr: ex, Text: text, Shared: -1})
	return nil
}

// SetInput stores what was typed into a cell: a formula, a number, a date or time, a truth value or a text. A number
// typed with a percent sign, currency sign or as a date gives a General cell the matching format.
func (s *Sheet) SetInput(r, c int, text string) error {
	if err := inSheet(r, c); err != nil {
		return err
	}
	if text == "" {
		s.put(r, c, formula.Value{}, nil)
		return nil
	}
	st := s.wb.Styles
	style := st.Get(s.StyleAt(r, c))
	if len(text) > 1 && text[0] == '=' {
		return s.SetFormula(r, c, strings.TrimSpace(text[1:]))
	}
	if style.Format.IsText() {
		s.put(r, c, formula.Str(text), nil)
		return nil
	}
	if text[0] == '\'' {
		s.put(r, c, formula.Str(text[1:]), nil)
		return nil
	}
	v, code := ParseInput(text, s.wb.date)
	s.put(r, c, v, nil)
	if code != "" && style.NumFmtID == 0 {
		s.styleCell(r, c, func(i int32) int32 { return st.Apply(i, &Change{NumFmt: &code}) })
	}
	return nil
}

var (
	reISO  = regexp.MustCompile(`^(\d{4})[-/.](\d{1,2})[-/.](\d{1,2})(?:[ T](\d{1,2}):(\d{2})(?::(\d{2}))?)?$`)
	reUS   = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})/(\d{4})(?: (\d{1,2}):(\d{2})(?::(\d{2}))?)?$`)
	reTime = regexp.MustCompile(`^(\d{1,2}):(\d{2})(?::(\d{2}))?(?:\s*([AaPp])[Mm]?)?$`)
)

// ParseInput reads typed text as Excel does; code is the format the text implies, "" for none.
func ParseInput(text string, date1904 bool) (v formula.Value, code string) {
	t := strings.TrimSpace(text)
	switch strings.ToUpper(t) {
	case "TRUE":
		return formula.BoolOf(true), ""
	case "FALSE":
		return formula.BoolOf(false), ""
	case "#N/A", "#DIV/0!", "#VALUE!", "#REF!", "#NAME?", "#NUM!", "#NULL!":
		return formula.Err(strings.ToUpper(t)), ""
	}
	if n, ok := parseNumber(t); ok {
		return formula.Num(n), ""
	}
	if strings.HasSuffix(t, "%") {
		if n, ok := parseNumber(strings.TrimSpace(t[:len(t)-1])); ok {
			return formula.Num(n / 100), "0.00%"
		}
	}
	for _, sym := range []string{"$", "¥", "€", "£"} {
		rest, neg := strings.CutPrefix(t, "-")
		if body, ok := strings.CutPrefix(rest, sym); ok {
			if n, ok := parseNumber(body); ok {
				if neg {
					n = -n
				}
				return formula.Num(n), `"` + sym + `"#,##0.00`
			}
		}
	}
	date := func(y, m, d, hh, mm, ss int, hasTime bool) (formula.Value, string, bool) {
		if m < 1 || m > 12 || d < 1 || d > 31 || hh > 23 || mm > 59 || ss > 59 {
			return formula.Value{}, "", false
		}
		serial := formula.SerialFromDate(y, m, d, date1904) + (float64(hh)*3600+float64(mm)*60+float64(ss))/86400
		if serial < 0 {
			return formula.Value{}, "", false
		}
		if hasTime {
			return formula.Num(serial), "m/d/yyyy h:mm", true
		}
		return formula.Num(serial), "m/d/yyyy", true
	}
	if m := reISO.FindStringSubmatch(t); m != nil {
		y, mo, d, hh, mi, ss := num(m[1]), num(m[2]), num(m[3]), num(m[4]), num(m[5]), num(m[6])
		if v, code, ok := date(y, mo, d, hh, mi, ss, m[4] != ""); ok {
			return v, code
		}
	}
	if m := reUS.FindStringSubmatch(t); m != nil {
		mo, d, y, hh, mi, ss := num(m[1]), num(m[2]), num(m[3]), num(m[4]), num(m[5]), num(m[6])
		if v, code, ok := date(y, mo, d, hh, mi, ss, m[4] != ""); ok {
			return v, code
		}
	}
	if m := reTime.FindStringSubmatch(t); m != nil {
		hh, mi, ss := num(m[1]), num(m[2]), num(m[3])
		if m[4] != "" {
			hh %= 12
			if strings.EqualFold(m[4], "p") {
				hh += 12
			}
		}
		if hh < 24 && mi < 60 && ss < 60 {
			code := "h:mm"
			if m[3] != "" {
				code = "h:mm:ss"
			}
			if m[4] != "" {
				code += " AM/PM"
			}
			return formula.Num((float64(hh)*3600 + float64(mi)*60 + float64(ss)) / 86400), code
		}
	}
	return formula.Str(text), ""
}

func num(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// parseNumber reads a number, with thousands commas in the right places.
func parseNumber(t string) (float64, bool) {
	if t == "" {
		return 0, false
	}
	if strings.ContainsRune(t, ',') {
		int1, _, _ := strings.Cut(t, ".")
		int1 = strings.TrimLeft(int1, "+-")
		parts := strings.Split(int1, ",")
		if len(parts[0]) == 0 || len(parts[0]) > 3 {
			return 0, false
		}
		for _, p := range parts[1:] {
			if len(p) != 3 {
				return 0, false
			}
		}
		t = strings.ReplaceAll(t, ",", "")
	}
	c := t[len(t)-1]
	if c < '0' && c != '.' || c > '9' {
		return 0, false // ParseFloat's Inf, NaN and hex forms are not numbers here
	}
	for i := 0; i < len(t); i++ {
		ch := t[i]
		if !(ch >= '0' && ch <= '9' || ch == '.' || ch == '-' || ch == '+' || ch == 'e' || ch == 'E') {
			return 0, false
		}
	}
	n, err := strconv.ParseFloat(t, 64)
	if err != nil || math.IsInf(n, 0) {
		return 0, false
	}
	return n, true
}

// InputText is what the formula bar shows for a cell.
func (s *Sheet) InputText(r, c int) string {
	cell := s.Cell(r, c)
	if cell == nil || (cell.Kind == Empty && cell.F == nil) {
		return ""
	}
	if cell.F != nil {
		return "=" + cell.FormulaText()
	}
	v := s.wb.value(cell)
	switch v.Kind {
	case formula.Number:
		f := s.wb.Styles.Get(cell.Style)
		if f.Format.IsDate() {
			code := "m/d/yyyy"
			_, _, _, h, m, sec, _ := formula.DateFromSerial(v.Num, s.wb.date)
			switch {
			case v.Num < 1:
				code = "h:mm:ss"
			case h != 0 || m != 0 || sec != 0:
				code = "m/d/yyyy h:mm:ss"
			}
			return s.wb.Styles.format(code).Render(v, s.wb.date).Text
		}
		return strconv.FormatFloat(v.Num, 'f', -1, 64)
	case formula.Text, formula.Error:
		return v.Str
	case formula.Bool:
		if v.Num != 0 {
			return "TRUE"
		}
		return "FALSE"
	}
	return ""
}

// Shown is the cell's text as it displays, without regard to the column's width.
func (s *Sheet) Shown(r, c int) formula.Rendered {
	cell := s.Cell(r, c)
	if cell == nil {
		return formula.Rendered{}
	}
	return s.wb.Styles.Get(cell.Style).Format.Render(s.wb.value(cell), s.wb.date)
}

// Clear empties an area of its contents, its formats, or both.
func (s *Sheet) Clear(a formula.Area, contents, formats bool) {
	groups := map[int32]bool{}
	s.eachCell(a, func(c *Cell, r int) {
		if contents && c.F != nil {
			if c.F.Shared >= 0 && c.F.Text != "" {
				groups[c.F.Shared] = true
			}
		}
	})
	if len(groups) > 0 {
		s.expandGroups(groups)
	}
	last := min(int(a.R2), len(s.rows)-1)
	for r := int(a.R1); r <= last; r++ {
		row := s.rows[r]
		if row == nil {
			continue
		}
		i, _ := row.find(a.C1)
		if i >= len(row.Cells) || row.Cells[i].Col > a.C2 {
			continue
		}
		row = s.writable(r)
		for ; i < len(row.Cells) && row.Cells[i].Col <= a.C2; i++ {
			c := &row.Cells[i]
			if contents && (c.Kind != Empty || c.F != nil) {
				if c.F != nil {
					cp := *c
					s.dropFormula(&cp)
				}
				c = &s.writable(r).Cells[i]
				c.Kind, c.Num, c.Text, c.F = Empty, 0, "", nil
				c.flags |= flagValue | flagFormula
				at := s.at(r, int(c.Col))
				s.wb.calc.Set(at, nil, 0, 0, false)
				s.wb.calc.Touch(at)
			}
			if formats && c.Style != 0 {
				c.Style = 0
				c.flags |= flagStyle
			}
		}
	}
}

// eachCell visits the entries in an area.
func (s *Sheet) eachCell(a formula.Area, fn func(c *Cell, r int)) {
	last := min(int(a.R2), len(s.rows)-1)
	for r := int(a.R1); r <= last; r++ {
		row := s.rows[r]
		if row == nil {
			continue
		}
		i, _ := row.find(a.C1)
		for ; i < len(row.Cells) && row.Cells[i].Col <= a.C2; i++ {
			fn(&row.Cells[i], r)
		}
	}
}

// styleCell changes a cell's style through fn, making the cell when it has no entry.
func (s *Sheet) styleCell(r, c int, fn func(int32) int32) {
	base := s.StyleAt(r, c)
	next := fn(base)
	if old := s.Cell(r, c); old != nil && old.Style == next {
		return
	}
	row := s.writable(r)
	i, ok := row.find(int32(c))
	if !ok {
		row.Cells = slices.Insert(row.Cells, i, Cell{Col: int32(c)})
		row.Cells[i].flags = flagValue
		s.noteCol(c)
	}
	row.Cells[i].Style = next
	row.Cells[i].flags |= flagStyle
}

// ApplyStyle makes a change of format over areas. Whole rows and columns set the row's or column's own style as well, so
// their empty cells follow.
func (s *Sheet) ApplyStyle(areas []formula.Area, ch *Change) {
	st := s.wb.Styles
	seen := map[int32]int32{}
	apply := func(i int32) int32 {
		if v, ok := seen[i]; ok {
			return v
		}
		v := st.Apply(i, ch)
		seen[i] = v
		return v
	}
	for _, a := range areas {
		switch {
		case a.C1 == 0 && int(a.C2) == MaxCols-1 && a.R1 == 0 && int(a.R2) == MaxRows-1:
			s.editCols(0, MaxCols-1, func(e *ox.Node) {
				n, _ := strconv.Atoi(e.Attr("style"))
				e.SetAttr("style", strconv.Itoa(int(apply(int32(n)))))
			})
			s.eachRow(0, len(s.rows)-1, func(r int) { s.styleRowCells(r, 0, MaxCols-1, apply) })
		case a.C1 == 0 && int(a.C2) == MaxCols-1:
			for r := int(a.R1); r <= int(a.R2); r++ {
				row := s.writable(r)
				row.Style, row.Custom = apply(row.defaultStyle()), true
				s.styleRowCells(r, 0, MaxCols-1, apply)
			}
		case a.R1 == 0 && int(a.R2) == MaxRows-1:
			s.editCols(int(a.C1), int(a.C2), func(e *ox.Node) {
				n, _ := strconv.Atoi(e.Attr("style"))
				e.SetAttr("style", strconv.Itoa(int(apply(int32(n)))))
			})
			s.eachRow(0, len(s.rows)-1, func(r int) { s.styleRowCells(r, int(a.C1), int(a.C2), apply) })
		default:
			rows, cols := int64(a.R2-a.R1)+1, int64(a.C2-a.C1)+1
			r2 := int(a.R2)
			if rows*cols > maxStyledCells {
				r2 = min(r2, len(s.rows)-1)
			}
			for r := int(a.R1); r <= r2; r++ {
				for c := int(a.C1); c <= int(a.C2); c++ {
					s.styleCell(r, c, apply)
				}
			}
		}
	}
}

func (s *Sheet) eachRow(r1, r2 int, fn func(r int)) {
	for r := r1; r <= r2 && r < len(s.rows); r++ {
		if s.rows[r] != nil {
			fn(r)
		}
	}
}

func (s *Sheet) styleRowCells(r, c1, c2 int, apply func(int32) int32) {
	row := s.writable(r)
	i, _ := row.find(int32(c1))
	for ; i < len(row.Cells) && int(row.Cells[i].Col) <= c2; i++ {
		if n := apply(row.Cells[i].Style); n != row.Cells[i].Style {
			row.Cells[i].Style = n
			row.Cells[i].flags |= flagStyle
		}
	}
}

// Merge merges an area. What was in its other cells goes, as in Excel; merges it overlaps are undone.
func (s *Sheet) Merge(a formula.Area) error {
	if a.R1 == a.R2 && a.C1 == a.C2 {
		return nil
	}
	if int64(a.R2-a.R1+1)*int64(a.C2-a.C1+1) > 1<<26 {
		return errors.New("that area is too large to merge")
	}
	s.Unmerge(a)
	for r := int(a.R1); r <= int(a.R2) && r < len(s.rows); r++ {
		first := a.C1
		if r == int(a.R1) {
			first++
		}
		if first <= a.C2 {
			s.Clear(formula.Area{R1: int32(r), C1: first, R2: int32(r), C2: a.C2}, true, false)
		}
	}
	s.saveTree()
	root := s.doc.Root
	m := root.Ensure("mergeCells", worksheetOrder)
	m.Append(ox.New("mergeCell", "ref", formula.AreaName(a)))
	m.SetAttr("count", strconv.Itoa(len(m.Elements("mergeCell"))))
	s.sync()
	return nil
}

// Unmerge undoes the merges touching an area.
func (s *Sheet) Unmerge(a formula.Area) {
	m := s.doc.Root.Child("mergeCells")
	if m == nil {
		return
	}
	any := false
	for _, e := range m.Elements("mergeCell") {
		if b, ok := formula.ParseArea(e.Attr("ref")); ok && overlap(a, b) {
			any = true
			break
		}
	}
	if !any {
		return
	}
	s.saveTree()
	m = s.doc.Root.Child("mergeCells")
	for _, e := range m.Elements("mergeCell") {
		if b, ok := formula.ParseArea(e.Attr("ref")); ok && overlap(a, b) {
			e.Remove()
		}
	}
	n := len(m.Elements("mergeCell"))
	if n == 0 {
		m.Remove()
	} else {
		m.SetAttr("count", strconv.Itoa(n))
	}
	s.sync()
}

func overlap(a, b formula.Area) bool {
	return a.R1 <= b.R2 && b.R1 <= a.R2 && a.C1 <= b.C2 && b.C1 <= a.C2
}

// editCols runs fn on <col> elements that together are exactly the columns c1 to c2, splitting and adding as needed.
func (s *Sheet) editCols(c1, c2 int, fn func(e *ox.Node)) {
	s.saveTree()
	root := s.doc.Root
	cols := root.Ensure("cols", worksheetOrder)
	var out []*ox.Node
	next := c1
	def := strconv.FormatFloat(s.defW, 'g', -1, 64)
	add := func(lo, hi int) {
		e := ox.New("col", "min", strconv.Itoa(lo+1), "max", strconv.Itoa(hi+1), "width", def)
		fn(e)
		out = append(out, e)
	}
	for _, e := range cols.Elements("col") {
		lo, hi := num(e.Attr("min"))-1, num(e.Attr("max"))-1
		hi = min(hi, MaxCols-1)
		if hi < c1 || lo > c2 {
			if lo > c2 && next <= c2 {
				add(next, c2)
				next = c2 + 1
			}
			out = append(out, e)
			continue
		}
		if next < lo {
			add(next, lo-1)
		}
		next = max(next, hi+1)
		clip := func(a, b int, change bool) {
			if a > b {
				return
			}
			k := e.Clone()
			k.SetAttr("min", strconv.Itoa(a+1))
			k.SetAttr("max", strconv.Itoa(b+1))
			if change {
				fn(k)
			}
			out = append(out, k)
		}
		clip(lo, c1-1, false)
		clip(max(lo, c1), min(hi, c2), true)
		clip(c2+1, hi, false)
	}
	if next <= c2 {
		add(next, c2)
	}
	cols.Children = nil
	cols.Append(out...)
	s.sync()
}

// SetColWidth sets columns' width in characters.
func (s *Sheet) SetColWidth(c1, c2 int, width float64) {
	width = min(max(width, 0), 255)
	s.editCols(c1, c2, func(e *ox.Node) {
		e.SetAttr("width", strconv.FormatFloat(width, 'g', 8, 64))
		e.SetAttr("customWidth", "1")
		e.DelAttr("bestFit")
	})
}

// WidthOfPixels is the width in characters that draws in px pixels at 100%.
func WidthOfPixels(px float64) float64 { return math.Trunc(px/7*256) / 256 }

// SetColsHidden hides or shows columns.
func (s *Sheet) SetColsHidden(c1, c2 int, hide bool) {
	s.editCols(c1, c2, func(e *ox.Node) {
		if hide {
			e.SetAttr("hidden", "1")
		} else {
			e.DelAttr("hidden")
		}
	})
}

// SetRowHeight sets rows' height in points; 0 returns them to automatic.
func (s *Sheet) SetRowHeight(r1, r2 int, h float64) {
	h = min(max(h, 0), 409.5)
	for r := r1; r <= r2; r++ {
		if h == 0 && s.RowAt(r) == nil {
			continue
		}
		row := s.writable(r)
		row.Height, row.Manual = h, h > 0
	}
}

// AutoRowHeight records a measured height on a row that was not sized by hand.
func (s *Sheet) AutoRowHeight(r int, h float64) {
	if row := s.RowAt(r); row != nil && row.Manual || h == s.RowPoints(r) {
		return
	}
	s.writable(r).Height = h
}

// SetRowsHidden hides or shows rows.
func (s *Sheet) SetRowsHidden(r1, r2 int, hide bool) {
	if !hide {
		r2 = min(r2, len(s.rows)-1)
	}
	for r := r1; r <= r2; r++ {
		if row := s.RowAt(r); row == nil && !hide || row != nil && row.Hidden == hide {
			continue
		}
		s.writable(r).Hidden = hide
	}
}

// SetFreeze freezes the rows above and columns left of (rows, cols); zero for both thaws.
func (s *Sheet) SetFreeze(rows, cols int) {
	s.saveTree()
	root := s.doc.Root
	views := root.Ensure("sheetViews", worksheetOrder)
	view := views.Child("sheetView")
	if view == nil {
		view = ox.New("sheetView", "workbookViewId", "0")
		views.Append(view)
	}
	if p := view.Child("pane"); p != nil {
		p.Remove()
	}
	for _, e := range view.Elements("selection") {
		e.DelAttr("pane")
	}
	if rows > 0 || cols > 0 {
		pane := "bottomRight"
		switch {
		case cols == 0:
			pane = "bottomLeft"
		case rows == 0:
			pane = "topRight"
		}
		p := ox.New("pane")
		if cols > 0 {
			p.SetAttr("xSplit", strconv.Itoa(cols))
		}
		if rows > 0 {
			p.SetAttr("ySplit", strconv.Itoa(rows))
		}
		p.SetAttr("topLeftCell", formula.CellName(rows, cols))
		p.SetAttr("activePane", pane)
		p.SetAttr("state", "frozen")
		view.Insert(0, p)
		if sel := view.Elements("selection"); len(sel) > 0 {
			sel[len(sel)-1].SetAttr("pane", pane)
			for _, e := range sel[:len(sel)-1] {
				e.Remove()
			}
		}
	}
	s.sync()
}

// SetTabColor sets the tab's colour, RRGGBB, or removes it.
func (s *Sheet) SetTabColor(hex string) {
	s.saveTree()
	root := s.doc.Root
	if hex == "" {
		if t := root.Path("sheetPr", "tabColor"); t != nil {
			t.Remove()
		}
		s.sync()
		return
	}
	pr := root.Ensure("sheetPr", worksheetOrder)
	t := pr.Ensure("tabColor", sheetPrOrder)
	t.Attrs = nil
	t.SetAttr("rgb", rgbAttr(hex))
	s.sync()
}

// rewrite changes formulas everywhere by fn, which gets a formula's text and own sheet. name is the sheet concerned: a
// formula on another sheet can only be touched if it names it. Shared groups affected become plain.
func (wb *Workbook) rewrite(sheet *Sheet, fn func(text, own string) (string, bool)) {
	name := lower(sheet.Name)
	for _, s := range wb.Sheets {
		own := s == sheet
		if own {
			s.expandGroups(nil)
		} else {
			groups := map[int32]bool{}
			s.eachCell(formula.Area{R2: MaxRows - 1, C2: MaxCols - 1}, func(c *Cell, r int) {
				if c.F != nil && c.F.Shared >= 0 && c.F.Text != "" && strings.Contains(lower(c.F.Text), name) {
					groups[c.F.Shared] = true
				}
			})
			if len(groups) > 0 {
				s.expandGroups(groups)
			}
		}
		for r, row := range s.rows {
			if row == nil {
				continue
			}
			for i := range row.Cells {
				c := &row.Cells[i]
				if c.F == nil {
					continue
				}
				text := c.FormulaText()
				if !own && !strings.Contains(lower(text), name) {
					continue
				}
				next, ok := fn(text, s.Name)
				if !ok {
					continue
				}
				row = s.writable(r)
				c = &row.Cells[i]
				f := &Formula{Text: next, Shared: -1, Array: c.F.Array}
				if ex, err := formula.Parse(next); err == nil {
					f.Expr = ex
				}
				c.F = f
				c.flags |= flagFormula
			}
		}
		// Rules in the sheet's tree: conditional formats and validations.
		s.rewriteRules(fn)
	}
	wb.rewriteNames(fn)
}

func (s *Sheet) rewriteRules(fn func(text, own string) (string, bool)) {
	saved := false
	s.doc.Root.Walk(func(e *ox.Node) bool {
		switch e.Name {
		case "formula", "formula1", "formula2", "xm:f":
			if next, ok := fn(e.InnerText(), s.Name); ok {
				if !saved {
					saved = true
					s.saveTree()
				}
				e.SetText(next)
			}
		}
		return true
	})
}

func (wb *Workbook) rewriteNames(fn func(text, own string) (string, bool)) {
	wb.saveBook()
	for _, e := range wb.doc.Root.Child("definedNames").Elements("definedName") {
		if next, ok := fn(e.InnerText(), ""); ok {
			e.SetText(next)
		}
	}
	wb.syncNames()
}

// Structure edits.

// InsertRows puts n empty rows before at.
func (s *Sheet) InsertRows(at, n int) error {
	return s.restructure(formula.Edit{Rows: true, At: at, N: n})
}

// DeleteRows removes n rows from at.
func (s *Sheet) DeleteRows(at, n int) error {
	return s.restructure(formula.Edit{Rows: true, At: at, N: -n})
}

// InsertCols puts n empty columns before at.
func (s *Sheet) InsertCols(at, n int) error { return s.restructure(formula.Edit{At: at, N: n}) }

// DeleteCols removes n columns from at.
func (s *Sheet) DeleteCols(at, n int) error { return s.restructure(formula.Edit{At: at, N: -n}) }

func (s *Sheet) restructure(e formula.Edit) error {
	e.Sheet = s.Name
	limit := MaxCols
	if e.Rows {
		limit = MaxRows
	}
	count := max(e.N, -e.N)
	if e.At < 0 || e.At >= limit || count == 0 || count > limit {
		return errors.New("that is outside the sheet")
	}
	if e.N < 0 {
		count = min(count, limit-e.At)
		e.N = -count
	} else if s.lastUsed(e.Rows)+count >= limit {
		return errors.New("there is no room: cells would be pushed off the end of the sheet")
	}
	wb := s.wb
	s.saveRows()
	s.boundOK = false
	wb.rewrite(s, func(text, own string) (string, bool) { return formula.Adjust(text, own, e) })
	if e.Rows {
		s.moveRows(e)
	} else {
		s.moveCols(e)
	}
	// Array formulas' ranges live on cells that have just moved.
	for r, row := range s.rows {
		if row == nil {
			continue
		}
		for i := range row.Cells {
			if f := row.Cells[i].F; f != nil && f.Array != "" {
				if ref := formula.AdjustRange(f.Array, e); ref != f.Array && ref != "" {
					row = s.writable(r)
					nf := *f
					nf.Array = ref
					row.Cells[i].F = &nf
					row.Cells[i].flags |= flagFormula
				}
			}
		}
	}
	s.adjustTree(e)
	s.adjustParts(e)
	wb.reindex(true)
	return nil
}

func (s *Sheet) lastUsed(rows bool) int {
	if rows {
		return s.RowCount() - 1
	}
	last := -1
	for _, row := range s.rows {
		if row != nil && len(row.Cells) > 0 {
			last = max(last, int(row.Cells[len(row.Cells)-1].Col))
		}
	}
	return last
}

func (s *Sheet) moveRows(e formula.Edit) {
	if e.N > 0 {
		if e.At >= len(s.rows) {
			return
		}
		var above *Row
		if e.At > 0 {
			above = s.rows[e.At-1]
		}
		s.rows = slices.Insert(s.rows, e.At, make([]*Row, e.N)...)
		if above != nil {
			// New rows take the format of the row above, as in Excel.
			for r := e.At; r < e.At+e.N; r++ {
				row := s.writable(r)
				row.Height, row.Manual, row.Style, row.Custom = above.Height, above.Manual, above.Style, above.Custom
				for k := range above.Cells {
					if above.Cells[k].Style != 0 {
						row.Cells = append(row.Cells, Cell{Col: above.Cells[k].Col, Style: above.Cells[k].Style, flags: flagValue})
					}
				}
			}
		}
		return
	}
	if e.At >= len(s.rows) {
		return
	}
	s.rows = slices.Delete(s.rows, e.At, min(e.At-e.N, len(s.rows)))
}

func (s *Sheet) moveCols(e formula.Edit) {
	end := int32(e.At - e.N) // deleting: the first column that stays
	for r, row := range s.rows {
		if row == nil {
			continue
		}
		i, _ := row.find(int32(e.At))
		if i >= len(row.Cells) {
			continue
		}
		row = s.writable(r)
		cells := row.Cells[:i]
		for k := i; k < len(row.Cells); k++ {
			c := row.Cells[k]
			if e.N < 0 && c.Col < end {
				continue
			}
			c.Col += int32(e.N)
			c.flags |= flagMoved
			cells = append(cells, c)
		}
		row.Cells = cells
	}
	// Column formats move with their columns.
	root := s.doc.Root
	cols := root.Child("cols")
	if cols == nil {
		return
	}
	s.saveTree()
	cols = s.doc.Root.Child("cols")
	at := e.At + 1
	var out []*ox.Node
	for _, c := range cols.Elements("col") {
		lo, hi := num(c.Attr("min")), num(c.Attr("max"))
		set := func(a, b int) {
			k := c.Clone()
			k.SetAttr("min", strconv.Itoa(a))
			k.SetAttr("max", strconv.Itoa(min(b, MaxCols)))
			if a <= MaxCols {
				out = append(out, k)
			}
		}
		switch {
		case e.N > 0 && hi < at:
			out = append(out, c)
		case e.N > 0 && lo >= at:
			set(lo+e.N, hi+e.N)
		case e.N > 0:
			set(lo, hi+e.N)
		case hi < at:
			out = append(out, c)
		case lo >= at-e.N:
			set(lo+e.N, hi+e.N)
		default:
			keep := hi - lo + 1
			keep -= min(hi, at-e.N-1) - max(lo, at) + 1
			if keep > 0 {
				set(min(lo, at), min(lo, at)+keep-1)
			}
		}
	}
	cols.Children = nil
	if len(out) == 0 {
		cols.Remove()
		return
	}
	cols.Append(out...)
}

// adjustTree moves everything in the sheet's tree that names cells.
func (s *Sheet) adjustTree(e formula.Edit) {
	s.saveTree()
	root := s.doc.Root
	var drop []*ox.Node
	// A filter column's number counts from the filter's left edge, which may move too.
	filterOld, filterNew, hasFilter := int32(0), int32(0), false
	if f := root.Child("autoFilter"); f != nil {
		if a, ok := formula.ParseArea(f.Attr("ref")); ok {
			filterOld, hasFilter = a.C1, true
			if b, ok := formula.ParseArea(formula.AdjustRange(f.Attr("ref"), e)); ok {
				filterNew = b.C1
			}
		}
	}
	root.Walk(func(n *ox.Node) bool {
		attr := ""
		switch n.Name {
		case "mergeCell", "autoFilter", "sortState", "sortCondition", "hyperlink":
			attr = "ref"
		case "conditionalFormatting", "dataValidation", "protectedRange", "ignoredError", "selection":
			attr = "sqref"
		case "xm:sqref":
			if t := formula.AdjustRange(n.InnerText(), e); t != "" {
				n.SetText(t)
			}
		case "filterColumn":
			if id, err := strconv.Atoi(n.Attr("colId")); err == nil && !e.Rows && hasFilter {
				abs := int(filterOld) + id
				switch {
				case e.N > 0 && abs >= e.At:
					abs += e.N
				case e.N < 0 && abs >= e.At-e.N:
					abs += e.N
				case e.N < 0 && abs >= e.At:
					drop = append(drop, n)
				}
				n.SetAttr("colId", strconv.Itoa(max(abs-int(filterNew), 0)))
			}
		case "brk":
			if v, err := strconv.Atoi(n.Attr("id")); err == nil && (e.Rows == (n.Parent != nil && n.Parent.Name == "rowBreaks")) && v > e.At {
				n.SetAttr("id", strconv.Itoa(max(v+e.N, e.At)))
			}
		}
		if attr != "" {
			if v, ok := n.Lookup(attr); ok {
				t := formula.AdjustRange(v, e)
				switch {
				case t != "":
					n.SetAttr(attr, t)
					if n.Name == "selection" {
						first, _, _ := strings.Cut(strings.Fields(t)[0], ":")
						n.SetAttr("activeCell", first)
					}
				case n.Name == "selection":
					n.SetAttr("sqref", "A1")
					n.SetAttr("activeCell", "A1")
				default:
					drop = append(drop, n)
				}
			}
		}
		return true
	})
	for _, n := range drop {
		n.Remove()
	}
	if m := root.Child("mergeCells"); m != nil {
		list := m.Elements("mergeCell")
		for _, c := range list {
			if a, ok := formula.ParseArea(c.Attr("ref")); !ok || a.R1 == a.R2 && a.C1 == a.C2 {
				c.Remove()
			}
		}
		if n := len(m.Elements("mergeCell")); n == 0 {
			m.Remove()
		} else {
			m.SetAttr("count", strconv.Itoa(n))
		}
	}
	if d := root.Child("dataValidations"); d != nil {
		d.SetAttr("count", strconv.Itoa(len(d.Elements("dataValidation"))))
	}
	if p := root.Path("sheetViews", "sheetView"); p != nil {
		if v, ok := p.Lookup("topLeftCell"); ok && formula.AdjustRange(v, e) == "" {
			p.DelAttr("topLeftCell")
		}
	}
	s.sync()
}

var (
	reVMLRow = regexp.MustCompile(`<x:Row>(\d+)</x:Row>`)
	reVMLCol = regexp.MustCompile(`<x:Column>(\d+)</x:Column>`)
)

// adjustParts moves what other parts say about this sheet's cells: tables, drawing anchors, comments and charts.
func (s *Sheet) adjustParts(e formula.Edit) {
	wb := s.wb
	if s.part == "" {
		return
	}
	rels, err := wb.pkg.Rels(s.part)
	if err != nil {
		return
	}
	adjustDoc := func(name string, fn func(root *ox.Node) bool) {
		if wb.pkg.Part(name) == nil {
			return
		}
		doc, err := wb.pkg.Doc(name)
		if err != nil {
			return
		}
		if fn(doc.Root) {
			wb.setPart(name, doc.Bytes())
		}
	}
	for _, r := range rels {
		if r.External {
			continue
		}
		name := opc.Resolve(s.part, r.Target)
		switch r.Type {
		case relTable:
			adjustDoc(name, func(root *ox.Node) bool {
				changed := false
				for _, n := range append([]*ox.Node{root}, root.Elements("autoFilter")...) {
					if v, ok := n.Lookup("ref"); ok {
						if t := formula.AdjustRange(v, e); t != "" && t != v {
							n.SetAttr("ref", t)
							changed = true
						}
					}
				}
				return changed
			})
		case relDrawing:
			adjustDoc(name, func(root *ox.Node) bool {
				tag := "xdr:col"
				if e.Rows {
					tag = "xdr:row"
				}
				changed := false
				root.Walk(func(n *ox.Node) bool {
					if n.Name != tag {
						return true
					}
					v, err := strconv.Atoi(n.InnerText())
					if err != nil || v < e.At {
						return true
					}
					n.SetText(strconv.Itoa(max(v+e.N, e.At)))
					changed = true
					return true
				})
				return changed
			})
		case relComments:
			adjustDoc(name, func(root *ox.Node) bool {
				changed := false
				root.Walk(func(n *ox.Node) bool {
					if n.Name == "comment" {
						if v := n.Attr("ref"); v != "" {
							if t := formula.AdjustRange(v, e); t != "" && t != v {
								n.SetAttr("ref", t)
								changed = true
							}
						}
					}
					return true
				})
				return changed
			})
		case relVML:
			if p := wb.pkg.Part(name); p != nil {
				re := reVMLCol
				if e.Rows {
					re = reVMLRow
				}
				data := re.ReplaceAllFunc(p.Data, func(m []byte) []byte {
					sub := re.FindSubmatch(m)
					v, _ := strconv.Atoi(string(sub[1]))
					if v < e.At {
						return m
					}
					return []byte(strings.Replace(string(m), string(sub[1]), strconv.Itoa(max(v+e.N, e.At)), 1))
				})
				if string(data) != string(p.Data) {
					wb.setPart(name, data)
				}
			}
		}
	}
	// A chart can be anywhere and name any sheet.
	for _, name := range wb.pkg.Names() {
		if !strings.HasPrefix(name, "xl/charts/chart") {
			continue
		}
		adjustDoc(name, func(root *ox.Node) bool {
			changed := false
			root.Walk(func(n *ox.Node) bool {
				if n.Name == "c:f" {
					if t, ok := formula.Adjust(n.InnerText(), "", e); ok {
						n.SetText(t)
						changed = true
					}
				}
				return true
			})
			return changed
		})
	}
}

// Sheets.

// AddSheet makes an empty sheet at index, with a name not in use that starts with base.
func (wb *Workbook) AddSheet(base string, at int) (*Sheet, error) {
	if len(wb.Sheets) >= 4096 {
		return nil, errors.New("the workbook has too many sheets")
	}
	wb.saveBook()
	name := base
	for i := 1; ; i++ {
		name = base + strconv.Itoa(i)
		if wb.Sheet(name) == nil {
			break
		}
	}
	root := wb.doc.Root
	list := root.Ensure("sheets", workbookOrder)
	id := 0
	for _, e := range list.Elements("sheet") {
		id = max(id, num(e.Attr("sheetId")))
	}
	for _, s := range wb.Sheets {
		id = max(id, num(s.id))
	}
	s := &Sheet{wb: wb, Name: name, id: strconv.Itoa(id + 1), touched: true}
	doc, err := ox.Parse([]byte(strings.Replace(blankSheet, "<sheetData/>", rowsMark, 1)))
	if err != nil {
		return nil, err
	}
	s.doc = doc
	s.sync()
	at = min(max(at, 0), len(wb.Sheets))
	e := ox.New("sheet", "name", name, "sheetId", s.id)
	els := list.Elements("sheet")
	if at < len(els) {
		list.Insert(list.Index(els[at]), e)
	} else {
		list.Append(e)
	}
	wb.moveNames(func(i int) int {
		if i >= at {
			return i + 1
		}
		return i
	})
	wb.Sheets = slices.Insert(slices.Clone(wb.Sheets), at, s)
	wb.syncNames()
	wb.reindex(false)
	return s, nil
}

// moveNames renumbers what is tied to a sheet's place: local names, the active tab. A sheet going away gets -1.
func (wb *Workbook) moveNames(fn func(int) int) {
	root := wb.doc.Root
	for _, e := range root.Child("definedNames").Elements("definedName") {
		if v, ok := e.Lookup("localSheetId"); ok {
			if n := fn(num(v)); n >= 0 {
				e.SetAttr("localSheetId", strconv.Itoa(n))
			} else {
				e.Remove()
			}
		}
	}
	if v := root.Path("bookViews", "workbookView"); v != nil {
		if t, ok := v.Lookup("activeTab"); ok {
			v.SetAttr("activeTab", strconv.Itoa(max(fn(num(t)), 0)))
		}
	}
}

// RenameSheet renames a sheet and every formula naming it.
func (wb *Workbook) RenameSheet(s *Sheet, name string) error {
	if err := validSheetName(wb, name, s); err != nil {
		return err
	}
	if s.Name == name {
		return nil
	}
	wb.saveBook()
	old := s.Name
	wb.rewrite(s, func(text, own string) (string, bool) { return formula.RenameSheet(text, old, name) })
	s.Name = name
	for _, e := range wb.doc.Root.Child("sheets").Elements("sheet") {
		if e.Attr("sheetId") == s.id {
			e.SetAttr("name", name)
		}
	}
	wb.reindex(false)
	return nil
}

// DeleteSheet removes a sheet; formulas naming it give #REF!.
func (wb *Workbook) DeleteSheet(s *Sheet) error {
	visible := 0
	for _, o := range wb.Sheets {
		if !o.hidden {
			visible++
		}
	}
	if visible <= 1 && !s.hidden || len(wb.Sheets) == 1 {
		return errors.New("a workbook must keep one visible sheet")
	}
	wb.saveBook()
	at := s.index()
	old := s.Name
	wb.rewrite(s, func(text, own string) (string, bool) {
		if own == old {
			return text, false
		}
		return formula.DeleteSheet(text, old)
	})
	for _, e := range wb.doc.Root.Child("sheets").Elements("sheet") {
		if e.Attr("sheetId") == s.id {
			e.Remove()
		}
	}
	wb.moveNames(func(i int) int {
		switch {
		case i == at:
			return -1
		case i > at:
			return i - 1
		}
		return i
	})
	wb.Sheets = slices.Delete(slices.Clone(wb.Sheets), at, at+1)
	wb.syncNames()
	wb.reindex(true)
	return nil
}

// MoveSheet moves a sheet to place to.
func (wb *Workbook) MoveSheet(s *Sheet, to int) {
	from := s.index()
	to = min(max(to, 0), len(wb.Sheets)-1)
	if from == to {
		return
	}
	wb.saveBook()
	list := wb.doc.Root.Child("sheets")
	els := list.Elements("sheet")
	e := els[from]
	e.Remove()
	rest := list.Elements("sheet")
	if to < len(rest) {
		list.Insert(list.Index(rest[to]), e)
	} else {
		list.Append(e)
	}
	sheets := slices.Delete(slices.Clone(wb.Sheets), from, from+1)
	wb.Sheets = slices.Insert(sheets, to, s)
	wb.moveNames(func(i int) int {
		switch {
		case i == from:
			return to
		case from < i && i <= to:
			return i - 1
		case to <= i && i < from:
			return i + 1
		}
		return i
	})
	wb.syncNames()
	wb.reindex(false)
}

// reindex starts the engine over, as it knows cells by place. changed says values moved, so everything is worked out
// again, as it also must be if work was pending, which starting over forgets.
func (wb *Workbook) reindex(changed bool) {
	changed = changed || wb.calc.Pending()
	wb.fillEngine(false)
	if changed {
		wb.calc.TouchAll()
	}
}

// Sort.

// SortKey is a column of a sort.
type SortKey struct {
	Col  int // the sheet's column
	Desc bool
}

// compareCells orders two values as Excel sorts: numbers, then text, then truth values, then errors; blanks come last
// whichever way the sort runs, and are handled by the caller.
func compareCells(a, b formula.Value) int {
	rank := func(v formula.Value) int {
		switch v.Kind {
		case formula.Number:
			return 0
		case formula.Text:
			return 1
		case formula.Bool:
			return 2
		}
		return 3
	}
	if ra, rb := rank(a), rank(b); ra != rb {
		return ra - rb
	}
	switch a.Kind {
	case formula.Number, formula.Bool:
		switch {
		case a.Num < b.Num:
			return -1
		case a.Num > b.Num:
			return 1
		}
		return 0
	}
	return strings.Compare(lower(a.Str), lower(b.Str))
}

// Sort orders the rows of an area by keys. Formulas move as if copied; hidden rows stay out of it, as filtered rows do.
func (s *Sheet) Sort(a formula.Area, keys []SortKey) error {
	if len(keys) == 0 {
		return errors.New("a sort needs a column")
	}
	for _, m := range s.merges {
		if overlap(a, m) {
			return errors.New("a range with merged cells cannot be sorted")
		}
	}
	a.R2 = min(a.R2, int32(max(s.RowCount()-1, 0)))
	if a.R2 < a.R1 {
		return nil
	}
	s.expandGroups(nil)
	type line struct {
		r    int
		keys []formula.Value
	}
	var lines []line
	for r := int(a.R1); r <= int(a.R2); r++ {
		if row := s.RowAt(r); row != nil && row.Hidden {
			continue
		}
		l := line{r: r, keys: make([]formula.Value, len(keys))}
		for i, k := range keys {
			l.keys[i] = s.ValueAt(r, k.Col)
		}
		lines = append(lines, l)
	}
	sorted := slices.Clone(lines)
	slices.SortStableFunc(sorted, func(x, y line) int {
		for i, k := range keys {
			p, q := x.keys[i], y.keys[i]
			switch {
			case p.Kind == formula.Blank && q.Kind == formula.Blank:
				continue
			case p.Kind == formula.Blank:
				return 1
			case q.Kind == formula.Blank:
				return -1
			}
			c := compareCells(p, q)
			if k.Desc {
				c = -c
			}
			if c != 0 {
				return c
			}
		}
		return 0
	})
	// Lift each row's stretch of cells, then lay them down in the new order.
	blocks := make([][]Cell, len(lines))
	for i, l := range sorted {
		if row := s.RowAt(l.r); row != nil {
			first, _ := row.find(a.C1)
			end, _ := row.find(a.C2 + 1)
			blocks[i] = slices.Clone(row.Cells[first:end])
		}
	}
	for i, l := range lines {
		from := sorted[i].r
		if from == l.r {
			continue
		}
		cells := blocks[i]
		dr := int32(l.r - from)
		for k := range cells {
			c := &cells[k]
			c.flags |= flagMoved
			if c.F != nil {
				nf := *c.F
				nf.Text = ""
				if nf.Expr != nil {
					nf.DR += dr
					nf.Text = nf.Expr.Text(int(nf.DR), int(nf.DC))
				} else {
					nf.Text = c.F.Text
				}
				c.F = &nf
				c.flags |= flagFormula
			}
		}
		if len(cells) == 0 && s.RowAt(l.r) == nil {
			continue
		}
		row := s.writable(l.r)
		first, _ := row.find(a.C1)
		end, _ := row.find(a.C2 + 1)
		row.Cells = slices.Replace(row.Cells, first, end, cells...)
	}
	s.wb.reindex(true)
	return nil
}

// Filter.

// AutoFilter is the sheet's filter range; ok is false when it has none.
func (s *Sheet) AutoFilter() (a formula.Area, ok bool) {
	if e := s.doc.Root.Child("autoFilter"); e != nil {
		return formula.ParseArea(e.Attr("ref"))
	}
	return a, false
}

// SetAutoFilter puts a filter on an area, or takes it away for an empty one, showing the rows it hid.
func (s *Sheet) SetAutoFilter(a formula.Area, on bool) {
	s.saveTree()
	root := s.doc.Root
	if old := root.Child("autoFilter"); old != nil {
		if cur, ok := formula.ParseArea(old.Attr("ref")); ok {
			s.SetRowsHidden(int(cur.R1)+1, int(cur.R2), false)
		}
		old.Remove()
	}
	if on {
		root.InsertOrdered(ox.New("autoFilter", "ref", formula.AreaName(a)), worksheetOrder)
	}
}

// FilterValues are the values chosen for a filter column, nil when it does not filter. blanks says blank cells show.
func (s *Sheet) FilterValues(col int) (values []string, blanks, ok bool) {
	e := s.doc.Root.Child("autoFilter")
	a, has := formula.ParseArea(e.Attr("ref"))
	if !has {
		return nil, false, false
	}
	for _, f := range e.Elements("filterColumn") {
		if num(f.Attr("colId")) != col-int(a.C1) {
			continue
		}
		list := f.Child("filters")
		if list == nil {
			return nil, false, false
		}
		for _, v := range list.Elements("filter") {
			values = append(values, v.Attr("val"))
		}
		return values, boolAttr(list, "blank"), true
	}
	return nil, false, false
}

// SetFilter shows only rows whose text in col is one of values; nil values takes the column's filter off. A column's
// other kinds of filter (custom, top ten, colour) are kept in the file until the column is filtered here.
func (s *Sheet) SetFilter(col int, values []string, blanks bool) error {
	s.saveTree()
	e := s.doc.Root.Child("autoFilter")
	a, ok := formula.ParseArea(e.Attr("ref"))
	if !ok || col < int(a.C1) || col > int(a.C2) {
		return errors.New("that column has no filter")
	}
	id := strconv.Itoa(col - int(a.C1))
	for _, f := range e.Elements("filterColumn") {
		if f.Attr("colId") == id {
			f.Remove()
		}
	}
	if values != nil || blanks {
		f := ox.New("filterColumn", "colId", id)
		list := ox.New("filters")
		if blanks {
			list.SetAttr("blank", "1")
		}
		for _, v := range values {
			list.Append(ox.New("filter", "val", v))
		}
		f.Append(list)
		// filterColumn comes before sortState and extLst.
		at := len(e.Children)
		for i, c := range e.Children {
			if c.Kind == ox.Element && c.Name != "filterColumn" {
				at = i
				break
			}
		}
		e.Insert(at, f)
	}
	s.applyFilters(a, e)
	return nil
}

// applyFilters hides rows no column's value list passes. Columns with other criteria leave rows as the file has them.
func (s *Sheet) applyFilters(a formula.Area, e *ox.Node) {
	type test struct {
		col    int
		set    map[string]bool
		blanks bool
	}
	var tests []test
	for _, f := range e.Elements("filterColumn") {
		list := f.Child("filters")
		if list == nil {
			continue
		}
		t := test{col: int(a.C1) + num(f.Attr("colId")), set: map[string]bool{}, blanks: boolAttr(list, "blank")}
		for _, v := range list.Elements("filter") {
			t.set[v.Attr("val")] = true
		}
		tests = append(tests, t)
	}
	for r := int(a.R1) + 1; r <= int(a.R2); r++ {
		show := true
		for _, t := range tests {
			text := s.Shown(r, t.col).Text
			if text == "" && !t.blanks || text != "" && !t.set[text] {
				show = false
				break
			}
		}
		s.SetRowsHidden(r, r, !show)
	}
}

// Find and replace.

// Match is a found cell.
type Match struct{ Row, Col int }

// Find lists the cells of an area whose shown text or formula contains what, in reading order, up to limit.
func (s *Sheet) Find(what string, a formula.Area, matchCase, formulas bool, limit int) []Match {
	if !matchCase {
		what = lower(what)
	}
	var out []Match
	if what == "" {
		return out
	}
	s.eachCell(a, func(c *Cell, r int) {
		if len(out) >= limit || c.Kind == Empty && c.F == nil {
			return
		}
		text := s.Shown(r, int(c.Col)).Text
		if formulas && c.F != nil {
			text = "=" + c.FormulaText()
		}
		if !matchCase {
			text = lower(text)
		}
		if strings.Contains(text, what) {
			out = append(out, Match{r, int(c.Col)})
		}
	})
	return out
}

// ReplaceIn replaces what in a cell's input and reports whether it changed.
func (s *Sheet) ReplaceIn(r, c int, what, with string, matchCase bool) (bool, error) {
	text := s.InputText(r, c)
	var next string
	if matchCase {
		next = strings.ReplaceAll(text, what, with)
	} else {
		re, err := regexp.Compile("(?i)" + regexp.QuoteMeta(what))
		if err != nil {
			return false, err
		}
		next = re.ReplaceAllLiteralString(text, with)
	}
	if next == text {
		return false, nil
	}
	if err := s.SetInput(r, c, next); err != nil {
		return false, fmt.Errorf("%s: %w", formula.CellName(r, c), err)
	}
	return true, nil
}
