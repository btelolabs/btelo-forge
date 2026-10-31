package ui

import (
	"bytes"
	"errors"
	"fmt"
	"image/png"
	"math"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/formula"
	"github.com/btelolabs/btelo-forge/internal/office/xlsx"
)

// The spreadsheet tools act on a workbook in a tab, the active one or the one named by path, as the window does; each
// call is one step of the workbook's history.

var sheetMCPTools = []string{"sheet_new", "sheet_open", "sheet_state", "sheet_read", "sheet_write", "sheet_format", "sheet_structure", "sheet_sort", "sheet_render", "sheet_save", "sheet_export_pdf"}

const sheetMCPInstructions = `Spreadsheets are .xlsx and .xlsm files open in tabs. Start with sheet_new or sheet_open, or name a file with path in any tool; without path, tools act on the active tab. Addresses are A1 style; a range is "A1:C10", "A:C" or "2:5", and a tool's sheet is a sheet's name (default: the sheet showing). Read with sheet_read; write many cells at once with sheet_write, as values, formulas (start with =) or typed text. Formulas recalculate at once. Format with sheet_format; change rows, columns and sheets with sheet_structure; sort with sheet_sort; look at the result with sheet_render, which returns a PNG; sheet_save writes the file now, though a workbook also saves itself moments after each change. Anything else in the file, charts, conditional formats and the like, is kept as it was.`

var errNoSheet = errors.New("no spreadsheet is open; call sheet_new or sheet_open first, or name one with path")

// sheetTarget is the workbook named by path, opened in a tab when it is not, or the active tab's, ready to use.
func (a *App) sheetTarget(path string) (*sheetEd, error) {
	var ed *sheetEd
	if path == "" {
		if a.office == nil {
			return nil, errNoSheet
		}
		e, ok := a.office.ed.(*sheetEd)
		if !ok {
			return nil, errNoSheet
		}
		ed = e
	} else {
		for i, d := range a.docs {
			if d.office != nil && samePath(d.office.path, path) {
				e, ok := d.office.ed.(*sheetEd)
				if !ok {
					return nil, fmt.Errorf("%s is not a spreadsheet", path)
				}
				a.switchTab(i)
				ed = e
			}
		}
		if ed == nil {
			f := officeFormatOf(path)
			if f == nil || f.id != "sheet" {
				return nil, fmt.Errorf("%s is not a .xlsx or .xlsm file", path)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			od, err := a.openOffice(f, path, data)
			if err != nil {
				return nil, err
			}
			ed = od.ed.(*sheetEd)
		}
	}
	if err := ed.await(); err != nil {
		return nil, err
	}
	ed.finishEdit()
	return ed, nil
}

// sheetNamed is a sheet of the workbook; "" is the one showing, and the sheet named is shown.
func (ed *sheetEd) sheetNamed(name string) (*xlsx.Sheet, error) {
	if name == "" {
		return ed.sheet(), nil
	}
	i := ed.wb.SheetIndex(name)
	if i < 0 {
		return nil, fmt.Errorf("the workbook has no sheet %q", name)
	}
	ed.cur = i
	return ed.wb.Sheets[i], nil
}

// parseRange reads an address; ""  is the whole used range.
func parseRange(s *xlsx.Sheet, ref string) (formula.Area, error) {
	if ref == "" {
		return s.Used(), nil
	}
	a, ok := formula.ParseArea(ref)
	if !ok {
		return a, fmt.Errorf("%q is not an address or range", ref)
	}
	if wholeCols(a) || wholeRows(a) {
		u := s.Used()
		a.R2, a.C2 = min(a.R2, u.R2), min(a.C2, u.C2)
	}
	return a, nil
}

type sheetInfo struct {
	Name    string `json:"name"`
	Used    string `json:"used"`
	Hidden  bool   `json:"hidden,omitempty"`
	FrozenR int    `json:"frozen_rows,omitempty"`
	FrozenC int    `json:"frozen_columns,omitempty"`
	Merges  int    `json:"merges,omitempty"`
	Tab     string `json:"tab_color,omitempty"`
	Filter  string `json:"filter,omitempty"`
}

type sheetStateOut struct {
	Path      string      `json:"path"`
	Sheets    []sheetInfo `json:"sheets"`
	Active    string      `json:"active"`
	Selection []string    `json:"selection"`
	Saved     bool        `json:"saved"`
	Circular  string      `json:"circular_reference,omitempty"`
}

func (a *App) sheetState(ed *sheetEd) sheetStateOut {
	out := sheetStateOut{Path: ed.od.path, Active: ed.sheet().Name, Saved: ed.version() == ed.od.saved, Circular: ed.circular}
	for _, s := range ed.wb.Sheets {
		fr, fc := s.Freeze()
		i := sheetInfo{Name: s.Name, Used: formula.AreaName(s.Used()), Hidden: s.Hidden(), FrozenR: fr, FrozenC: fc, Merges: len(s.Merges()), Tab: s.TabColor()}
		if f, ok := s.AutoFilter(); ok {
			i.Filter = formula.AreaName(f)
		}
		out.Sheets = append(out.Sheets, i)
	}
	for _, s := range ed.view().sel {
		out.Selection = append(out.Selection, formula.AreaName(s))
	}
	return out
}

// sheetJSON is a value as JSON has it.
func sheetJSON(v formula.Value) any {
	switch v.Kind {
	case formula.Number:
		return v.Num
	case formula.Text, formula.Error:
		return v.Str
	case formula.Bool:
		return v.Num != 0
	}
	return nil
}

type sheetCellIn struct {
	Ref   string `json:"ref" jsonschema:"the cell, as B3"`
	Value any    `json:"value,omitempty" jsonschema:"a number, text or true/false, stored as it is"`
	Input string `json:"input,omitempty" jsonschema:"or what a person would type: 12%, 2024-03-05, =SUM(A1:A3)"`
	Clear bool   `json:"clear,omitempty" jsonschema:"empty the cell"`
}

type sheetBorderIn struct {
	Sides string `json:"sides" jsonschema:"all, outside, top, bottom, left, right or none"`
	Style string `json:"style,omitempty" jsonschema:"thin (default), medium, thick, dashed, dotted, double"`
	Color string `json:"color,omitempty" jsonschema:"RRGGBB"`
}

type sheetFormatIn struct {
	Path         string         `json:"path,omitempty"`
	Sheet        string         `json:"sheet,omitempty"`
	Ranges       []string       `json:"ranges" jsonschema:"the ranges to format"`
	Font         string         `json:"font,omitempty"`
	Size         float64        `json:"size,omitempty"`
	Bold         *bool          `json:"bold,omitempty"`
	Italic       *bool          `json:"italic,omitempty"`
	Underline    *bool          `json:"underline,omitempty"`
	Strike       *bool          `json:"strike,omitempty"`
	Color        *string        `json:"color,omitempty" jsonschema:"text colour RRGGBB, empty for automatic"`
	Fill         *string        `json:"fill,omitempty" jsonschema:"fill colour RRGGBB, empty for none"`
	HAlign       *string        `json:"h_align,omitempty" jsonschema:"left, center, right or empty"`
	VAlign       *string        `json:"v_align,omitempty" jsonschema:"top, center, bottom"`
	Wrap         *bool          `json:"wrap,omitempty"`
	Indent       *int           `json:"indent,omitempty"`
	NumberFormat *string        `json:"number_format,omitempty" jsonschema:"an Excel format code, as #,##0.00 or yyyy-mm-dd"`
	Border       *sheetBorderIn `json:"border,omitempty"`
	Merge        bool           `json:"merge,omitempty" jsonschema:"merge each range"`
	Unmerge      bool           `json:"unmerge,omitempty"`
	ColumnWidth  float64        `json:"column_width,omitempty" jsonschema:"in characters"`
	RowHeight    float64        `json:"row_height,omitempty" jsonschema:"in points"`
}

func sheetHex(s string) (string, error) {
	s = strings.ToUpper(strings.TrimPrefix(s, "#"))
	if s != "" && len(s) != 6 {
		return "", fmt.Errorf("%q is not an RRGGBB colour", s)
	}
	return s, nil
}

func (in *sheetFormatIn) change() (*xlsx.Change, error) {
	ch := &xlsx.Change{Bold: in.Bold, Italic: in.Italic, Underline: in.Underline, Strike: in.Strike, HAlign: in.HAlign, VAlign: in.VAlign, Wrap: in.Wrap, Indent: in.Indent, NumFmt: in.NumberFormat}
	if in.Font != "" {
		ch.Font = &in.Font
	}
	if in.Size > 0 {
		ch.Size = &in.Size
	}
	for _, c := range []struct {
		from *string
		to   **string
	}{{in.Color, &ch.Color}, {in.Fill, &ch.Fill}} {
		if c.from != nil {
			hex, err := sheetHex(*c.from)
			if err != nil {
				return nil, err
			}
			*c.to = &hex
		}
	}
	return ch, nil
}

func sheetBorders(s *xlsx.Sheet, a formula.Area, in *sheetBorderIn) error {
	hex, err := sheetHex(in.Color)
	if err != nil {
		return err
	}
	style := in.Style
	if style == "" {
		style = "thin"
	}
	e := xlsx.Edge{Style: style, Color: hex}
	none := xlsx.Edge{}
	switch in.Sides {
	case "all":
		s.ApplyStyle([]formula.Area{a}, &xlsx.Change{Left: &e, Right: &e, Top: &e, Bottom: &e})
	case "outside":
		s.ApplyStyle([]formula.Area{{R1: a.R1, C1: a.C1, R2: a.R1, C2: a.C2}}, &xlsx.Change{Top: &e})
		s.ApplyStyle([]formula.Area{{R1: a.R2, C1: a.C1, R2: a.R2, C2: a.C2}}, &xlsx.Change{Bottom: &e})
		s.ApplyStyle([]formula.Area{{R1: a.R1, C1: a.C1, R2: a.R2, C2: a.C1}}, &xlsx.Change{Left: &e})
		s.ApplyStyle([]formula.Area{{R1: a.R1, C1: a.C2, R2: a.R2, C2: a.C2}}, &xlsx.Change{Right: &e})
	case "top":
		s.ApplyStyle([]formula.Area{a}, &xlsx.Change{Top: &e})
	case "bottom":
		s.ApplyStyle([]formula.Area{a}, &xlsx.Change{Bottom: &e})
	case "left":
		s.ApplyStyle([]formula.Area{a}, &xlsx.Change{Left: &e})
	case "right":
		s.ApplyStyle([]formula.Area{a}, &xlsx.Change{Right: &e})
	case "none":
		s.ApplyStyle([]formula.Area{a}, &xlsx.Change{Left: &none, Right: &none, Top: &none, Bottom: &none})
	default:
		return fmt.Errorf("%q is not a side: all, outside, top, bottom, left, right or none", in.Sides)
	}
	return nil
}

// renderPNG draws a range of a sheet as it shows, with headings, at most maxSize pixels on its longest side.
func (ed *sheetEd) renderPNG(s *xlsx.Sheet, a formula.Area, maxSize int, headings bool) ([]byte, error) {
	if maxSize <= 0 {
		maxSize = 1600
	}
	maxSize = min(maxSize, 4096)
	v := ed.viewOf(s)
	w := v.colX[a.C2+1] - v.colX[a.C1]
	h := v.rowTop(int(a.R2)+1) - v.rowTop(int(a.R1))
	headW, headH := 0.0, 0.0
	if headings {
		headW, headH = 40, 20
	}
	k := math.Min(1, float64(maxSize)/math.Max(w+headW, h+headH))
	if k <= 0 || math.IsInf(k, 0) || math.IsNaN(k) {
		return nil, errors.New("that range has no size")
	}
	cw, ch := int(math.Ceil((w+headW)*k)), int(math.Ceil((h+headH)*k))
	if cw <= 0 || ch <= 0 {
		return nil, errors.New("that range is hidden")
	}
	c := gfx.NewCanvas(cw, ch)
	styles, styleK := ed.styles, ed.styleK
	ed.styles, ed.styleK = map[int32]*sheetStyle{}, k
	defer func() { ed.styles, ed.styleK = styles, styleK }()
	p := sheetPainter{ed: ed, v: v, c: c, k: k, styled: s.HasColumnStyles()}
	p.x0 = (headW - v.colX[a.C1]) * k
	p.y0 = (headH - v.rowTop(int(a.R1))) * k
	c.Clear(gfx.Hex(0xffffff))
	p.pane(gfx.Rect{X: float32(headW * k), Y: float32(headH * k), W: float32(w * k), H: float32(h * k)}, int(a.R1), int(a.C1))
	if headings {
		u := &ed.a.u
		f := u.fonts.Regular.Face(max(float32(11*k), 1))
		ink, line, band := gfx.Hex(0x505050), gfx.Hex(0xc8c8c8), gfx.Hex(0xefefef)
		c.Fill(gfx.Rect{W: float32(cw), H: float32(headH * k)}, band)
		c.Fill(gfx.Rect{W: float32(headW * k), H: float32(ch)}, band)
		for col := int(a.C1); col <= int(a.C2); col++ {
			x, cwid := float32(p.x0+v.colX[col]*k), float32((v.colX[col+1]-v.colX[col])*k)
			if cwid <= 0 {
				continue
			}
			name := formula.ColName(col)
			c.Text(f, x+(cwid-f.Measure(name))/2, float32(headH*k-float64(f.Height()))/2, name, ink)
			c.Fill(gfx.Rect{X: x + cwid - 1, W: 1, H: float32(headH * k)}, line)
		}
		for r := int(a.R1); r <= int(a.R2); r++ {
			y, rh := float32(p.y0+v.rowTop(r)*k), float32((v.rowTop(r+1)-v.rowTop(r))*k)
			if rh <= 0 {
				continue
			}
			name := fmt.Sprint(r + 1)
			c.Text(f, float32(headW*k)-f.Measure(name)-3, y+(rh-f.Height())/2, name, ink)
			c.Fill(gfx.Rect{Y: y + rh - 1, W: float32(headW * k), H: 1}, line)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, c.Img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// addSheetTools adds the spreadsheet tools.
func (a *App) addSheetTools(s *mcp.Server) {
	type pathIn struct {
		Path string `json:"path,omitempty"`
	}
	type newIn struct {
		Path string `json:"path,omitempty" jsonschema:"where to make the file; left out: in the user's documents"`
	}
	tool(s, a, "sheet_new", "Make a new workbook with one empty sheet, in a tab.", func(in newIn) (any, error) {
		f := officeFormatOf("x.xlsx")
		if in.Path == "" {
			od, err := a.newOffice(f)
			if err != nil {
				return nil, err
			}
			return a.sheetState(od.ed.(*sheetEd)), nil
		}
		if officeFormatOf(in.Path) != f {
			return nil, errors.New("the path must end in .xlsx or .xlsm")
		}
		data, err := f.blank(a)
		if err != nil {
			return nil, err
		}
		if err := writeFileWhole(in.Path, data); err != nil {
			return nil, err
		}
		ed, err := a.sheetTarget(in.Path)
		if err != nil {
			return nil, err
		}
		return a.sheetState(ed), nil
	})
	tool(s, a, "sheet_open", "Open a .xlsx or .xlsm file in a tab, or show the tab it is in.", func(in pathIn) (any, error) {
		if in.Path == "" {
			return nil, errors.New("sheet_open needs a path")
		}
		ed, err := a.sheetTarget(in.Path)
		if err != nil {
			return nil, err
		}
		return a.sheetState(ed), nil
	})
	tool(s, a, "sheet_state", "What is open: the file, its sheets with their used ranges, frozen panes and filters, and the selection.", func(in pathIn) (any, error) {
		ed, err := a.sheetTarget(in.Path)
		if err != nil {
			return nil, err
		}
		return a.sheetState(ed), nil
	})
	type readIn struct {
		Path  string `json:"path,omitempty"`
		Sheet string `json:"sheet,omitempty"`
		Range string `json:"range,omitempty" jsonschema:"left out: the whole used range"`
	}
	type readOut struct {
		Range    string     `json:"range"`
		Values   [][]any    `json:"values"`
		Shown    [][]string `json:"shown"`
		Formulas [][]string `json:"formulas,omitempty"`
	}
	tool(s, a, "sheet_read", "Read a range: each cell's value, what it shows with its number format, and its formula.", func(in readIn) (any, error) {
		ed, err := a.sheetTarget(in.Path)
		if err != nil {
			return nil, err
		}
		sh, err := ed.sheetNamed(in.Sheet)
		if err != nil {
			return nil, err
		}
		area, err := parseRange(sh, in.Range)
		if err != nil {
			return nil, err
		}
		ed.wb.Calc(0)
		if int64(area.R2-area.R1+1)*int64(area.C2-area.C1+1) > 200000 {
			return nil, errors.New("that range has more than 200,000 cells; read a smaller one")
		}
		out := readOut{Range: formula.AreaName(area)}
		any2 := false
		for r := int(area.R1); r <= int(area.R2); r++ {
			var vals []any
			var shown, fx []string
			for c := int(area.C1); c <= int(area.C2); c++ {
				vals = append(vals, sheetJSON(sh.ValueAt(r, c)))
				shown = append(shown, sh.Shown(r, c).Text)
				f := ""
				if cell := sh.Cell(r, c); cell != nil && cell.F != nil {
					f = "=" + cell.FormulaText()
					any2 = true
				}
				fx = append(fx, f)
			}
			out.Values, out.Shown, out.Formulas = append(out.Values, vals), append(out.Shown, shown), append(out.Formulas, fx)
		}
		if !any2 {
			out.Formulas = nil
		}
		return out, nil
	})
	type writeIn struct {
		Path  string        `json:"path,omitempty"`
		Sheet string        `json:"sheet,omitempty"`
		Cells []sheetCellIn `json:"cells,omitempty" jsonschema:"cells one by one"`
		Start string        `json:"start,omitempty" jsonschema:"or the top-left cell of rows"`
		Rows  [][]any       `json:"rows,omitempty" jsonschema:"a block of values; strings are read as typed, so =A1*2 is a formula; null leaves a cell alone"`
	}
	tool(s, a, "sheet_write", "Write many cells in one step. Formulas recalculate, and so does everything depending on the cells.", func(in writeIn) (any, error) {
		ed, err := a.sheetTarget(in.Path)
		if err != nil {
			return nil, err
		}
		sh, err := ed.sheetNamed(in.Sheet)
		if err != nil {
			return nil, err
		}
		ed.wb.Begin()
		put := func(r, c int, v any) error {
			switch t := v.(type) {
			case nil:
				return nil
			case string:
				return sh.SetInput(r, c, t)
			case bool:
				return sh.SetValue(r, c, formula.BoolOf(t))
			case float64:
				return sh.SetValue(r, c, formula.Num(t))
			}
			return fmt.Errorf("%s: a cell takes a number, text or true/false", formula.CellName(r, c))
		}
		n := 0
		for _, cell := range in.Cells {
			r, c, ok := formula.ParseCell(cell.Ref)
			if !ok {
				return nil, fmt.Errorf("%q is not a cell", cell.Ref)
			}
			var err error
			switch {
			case cell.Clear:
				sh.Clear(cellArea(r, c), true, false)
			case cell.Input != "":
				err = sh.SetInput(r, c, cell.Input)
			case cell.Value == nil:
				err = fmt.Errorf("%s has no value", cell.Ref)
			default:
				if t, ok := cell.Value.(string); ok {
					err = sh.SetValue(r, c, formula.Str(t))
				} else {
					err = put(r, c, cell.Value)
				}
			}
			if err != nil {
				return nil, fmt.Errorf("%s: %w", cell.Ref, err)
			}
			n++
		}
		if len(in.Rows) > 0 {
			r0, c0, ok := formula.ParseCell(in.Start)
			if !ok {
				return nil, fmt.Errorf("%q is not a cell", in.Start)
			}
			for i, row := range in.Rows {
				for j, v := range row {
					if err := put(r0+i, c0+j, v); err != nil {
						return nil, fmt.Errorf("%s: %w", formula.CellName(r0+i, c0+j), err)
					}
					n++
				}
			}
		}
		ed.wb.Calc(0)
		ed.noteCircular()
		return map[string]any{"written": n, "circular_reference": ed.circular}, nil
	})
	tool(s, a, "sheet_format", "Format ranges: font, size, bold and so on, colours, alignment, wrapping, number format, borders, merging, column width and row height. Say only what is to change.", func(in sheetFormatIn) (any, error) {
		ed, err := a.sheetTarget(in.Path)
		if err != nil {
			return nil, err
		}
		sh, err := ed.sheetNamed(in.Sheet)
		if err != nil {
			return nil, err
		}
		ch, err := in.change()
		if err != nil {
			return nil, err
		}
		var areas []formula.Area
		for _, ref := range in.Ranges {
			area, ok := formula.ParseArea(ref)
			if !ok {
				return nil, fmt.Errorf("%q is not a range", ref)
			}
			areas = append(areas, area)
		}
		if len(areas) == 0 {
			return nil, errors.New("sheet_format needs ranges")
		}
		ed.wb.Begin()
		sh.ApplyStyle(areas, ch)
		for _, area := range areas {
			if in.Border != nil {
				if err := sheetBorders(sh, area, in.Border); err != nil {
					return nil, err
				}
			}
			if in.Unmerge {
				sh.Unmerge(area)
			}
			if in.Merge {
				if err := sh.Merge(area); err != nil {
					return nil, err
				}
			}
			if in.ColumnWidth > 0 {
				sh.SetColWidth(int(area.C1), int(area.C2), in.ColumnWidth)
			}
			if in.RowHeight > 0 {
				sh.SetRowHeight(int(area.R1), min(int(area.R2), xlsx.MaxRows-1), in.RowHeight)
			}
		}
		ed.fitRows(ed.viewOf(sh))
		return map[string]any{"formatted": len(areas)}, nil
	})
	type structIn struct {
		Path   string `json:"path,omitempty"`
		Sheet  string `json:"sheet,omitempty"`
		Action string `json:"action" jsonschema:"insert_rows, delete_rows, insert_columns, delete_columns, hide_rows, show_rows, hide_columns, show_columns, add_sheet, delete_sheet, rename_sheet, move_sheet, tab_color, freeze or filter"`
		At     string `json:"at,omitempty" jsonschema:"the first row (5) or column (C); for freeze, the first cell that scrolls (B2), A1 to thaw; for filter, the range, empty to take it off"`
		Count  int    `json:"count,omitempty" jsonschema:"how many rows or columns (default 1)"`
		Name   string `json:"name,omitempty" jsonschema:"a sheet's new name"`
		To     int    `json:"to,omitempty" jsonschema:"move_sheet: its new place, from 0"`
		Color  string `json:"color,omitempty" jsonschema:"tab_color: RRGGBB, empty for none"`
	}
	tool(s, a, "sheet_structure", "Change structure: insert or delete rows and columns (formulas, merges and everything else follow), hide or show them, add, delete, rename, move or colour sheets, freeze panes, turn a filter on or off.", func(in structIn) (any, error) {
		ed, err := a.sheetTarget(in.Path)
		if err != nil {
			return nil, err
		}
		sh, err := ed.sheetNamed(in.Sheet)
		if err != nil {
			return nil, err
		}
		n := max(in.Count, 1)
		row := func() (int, error) {
			var r int
			if _, err := fmt.Sscanf(in.At, "%d", &r); err != nil || r < 1 {
				return 0, fmt.Errorf("%q is not a row", in.At)
			}
			return r - 1, nil
		}
		col := func() (int, error) {
			c, ok := formula.ParseCol(in.At)
			if !ok {
				return 0, fmt.Errorf("%q is not a column", in.At)
			}
			return c, nil
		}
		ed.wb.Begin()
		switch in.Action {
		case "insert_rows", "delete_rows", "hide_rows", "show_rows":
			r, err := row()
			if err != nil {
				return nil, err
			}
			switch in.Action {
			case "insert_rows":
				err = sh.InsertRows(r, n)
			case "delete_rows":
				err = sh.DeleteRows(r, n)
			default:
				sh.SetRowsHidden(r, r+n-1, in.Action == "hide_rows")
			}
			if err != nil {
				return nil, err
			}
		case "insert_columns", "delete_columns", "hide_columns", "show_columns":
			c, err := col()
			if err != nil {
				return nil, err
			}
			switch in.Action {
			case "insert_columns":
				err = sh.InsertCols(c, n)
			case "delete_columns":
				err = sh.DeleteCols(c, n)
			default:
				sh.SetColsHidden(c, c+n-1, in.Action == "hide_columns")
			}
			if err != nil {
				return nil, err
			}
		case "add_sheet":
			ns, err := ed.wb.AddSheet("Sheet", len(ed.wb.Sheets))
			if err != nil {
				return nil, err
			}
			if in.Name != "" {
				if err := ed.wb.RenameSheet(ns, in.Name); err != nil {
					return nil, err
				}
			}
			ed.cur = len(ed.wb.Sheets) - 1
		case "delete_sheet":
			if err := ed.wb.DeleteSheet(sh); err != nil {
				return nil, err
			}
		case "rename_sheet":
			if err := ed.wb.RenameSheet(sh, in.Name); err != nil {
				return nil, err
			}
		case "move_sheet":
			ed.wb.MoveSheet(sh, in.To)
		case "tab_color":
			hex, err := sheetHex(in.Color)
			if err != nil {
				return nil, err
			}
			sh.SetTabColor(hex)
		case "freeze":
			r, c, ok := formula.ParseCell(in.At)
			if !ok {
				return nil, fmt.Errorf("%q is not a cell", in.At)
			}
			sh.SetFreeze(r, c)
		case "filter":
			if in.At == "" {
				sh.SetAutoFilter(formula.Area{}, false)
				break
			}
			area, ok := formula.ParseArea(in.At)
			if !ok {
				return nil, fmt.Errorf("%q is not a range", in.At)
			}
			sh.SetAutoFilter(area, true)
		default:
			return nil, fmt.Errorf("%q is not an action", in.Action)
		}
		ed.wb.Calc(0)
		ed.noteCircular()
		return a.sheetState(ed), nil
	})
	type sortKeyIn struct {
		Column string `json:"column" jsonschema:"the column's letter"`
		Desc   bool   `json:"desc,omitempty"`
	}
	type sortIn struct {
		Path   string      `json:"path,omitempty"`
		Sheet  string      `json:"sheet,omitempty"`
		Range  string      `json:"range" jsonschema:"the rows to sort, without a heading row unless header is set"`
		Keys   []sortKeyIn `json:"keys" jsonschema:"the columns to sort by, first the most important"`
		Header bool        `json:"header,omitempty" jsonschema:"the first row of the range stays where it is"`
	}
	tool(s, a, "sheet_sort", "Sort the rows of a range by one or more columns. Numbers come before text, blanks last.", func(in sortIn) (any, error) {
		ed, err := a.sheetTarget(in.Path)
		if err != nil {
			return nil, err
		}
		sh, err := ed.sheetNamed(in.Sheet)
		if err != nil {
			return nil, err
		}
		area, err := parseRange(sh, in.Range)
		if err != nil {
			return nil, err
		}
		if in.Header {
			area.R1++
		}
		var keys []xlsx.SortKey
		for _, k := range in.Keys {
			c, ok := formula.ParseCol(k.Column)
			if !ok {
				return nil, fmt.Errorf("%q is not a column", k.Column)
			}
			keys = append(keys, xlsx.SortKey{Col: c, Desc: k.Desc})
		}
		ed.wb.Begin()
		if err := sh.Sort(area, keys); err != nil {
			return nil, err
		}
		ed.wb.Calc(0)
		return map[string]any{"sorted": formula.AreaName(area)}, nil
	})
	type renderIn struct {
		Path     string `json:"path,omitempty"`
		Sheet    string `json:"sheet,omitempty"`
		Range    string `json:"range,omitempty" jsonschema:"left out: the used range"`
		MaxSize  int    `json:"max_size,omitempty" jsonschema:"the picture's longest side in pixels (default 1600, at most 4096)"`
		Headings *bool  `json:"headings,omitempty" jsonschema:"draw row numbers and column letters (default true)"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "sheet_render", Description: "A PNG of a range as it looks, with its formats, to check it."},
		func(ctx contextT, req *mcp.CallToolRequest, in renderIn) (*mcp.CallToolResult, any, error) {
			return imageResult(call(a, func() ([]byte, error) {
				ed, err := a.sheetTarget(in.Path)
				if err != nil {
					return nil, err
				}
				sh, err := ed.sheetNamed(in.Sheet)
				if err != nil {
					return nil, err
				}
				area, err := parseRange(sh, in.Range)
				if err != nil {
					return nil, err
				}
				ed.wb.Calc(0)
				if int64(area.R2-area.R1) > 5000 || area.C2-area.C1 > 500 {
					return nil, errors.New("that range is too large to draw; render a part of it")
				}
				return ed.renderPNG(sh, area, in.MaxSize, in.Headings == nil || *in.Headings)
			}))
		})
	tool(s, a, "sheet_save", "Write the workbook to its file now.", func(in pathIn) (any, error) {
		ed, err := a.sheetTarget(in.Path)
		if err != nil {
			return nil, err
		}
		ed.od.dirty = true
		if err := a.saveOffice(ed.od, true); err != nil {
			return nil, err
		}
		return map[string]any{"path": ed.od.path}, nil
	})
}
