package ui

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/formula"
	"github.com/btelolabs/btelo-forge/internal/office/xlsx"
	"github.com/btelolabs/btelo-forge/internal/printing"
)

// A spreadsheet on paper: its print area or what it uses, cut into pages by the sheet's own setup. Cells are drawn by
// the same painter as on the screen, on a recording canvas.

// sheetCanvasPixels is how many canvas pixels a pixel of the sheet at 100% is on paper.
const sheetCanvasPixels = 8

// sheetPrintLine is how wide a thin border is, as a share of a pixel at 100%.
const sheetPrintLine = 0.7

const (
	sheetActive = iota
	sheetWorkbook
	sheetSelection
)

// The headings on paper, in pixels at 100%.
const (
	sheetHeadW = 34.0
	sheetHeadH = 18.0
)

type sheetPrint struct {
	ed    *sheetEd
	what  int
	fit   bool
	grid  bool
	heads bool
	paper int
	// What the boxes started as: until one is changed, each sheet goes by its own setting.
	grid0, heads0 bool
	pages         []sheetPage
	set           string // the choices the pages were cut for
	ver           uint64
}

// sheetPage is one sheet of paper.
type sheetPage struct {
	s            *xlsx.Sheet
	setup        *xlsx.PageSetup
	w, h         float64 // the paper
	scale        float64
	r1, r2       int
	c1, c2       int
	titleR       [2]int // rows repeated at the top, -1 for none
	titleC       [2]int
	x, y         float64 // where the cells start
	number       int     // of the sheet's pages, from one
	sheetPages   int
	workbookName string
}

func newSheetPrint(ed *sheetEd) *sheetPrint {
	st := ed.sheet().PrintSetup()
	return &sheetPrint{ed: ed, grid: st.Grid, heads: st.Headings, grid0: st.Grid, heads0: st.Headings}
}

func (s *sheetPrint) name() string { return docName(s.ed.od.path) }

func (s *sheetPrint) options(p *printDialog, x, y, w float32) float32 {
	u := &p.a.u
	p.choice("sheet/what", u.t("What to print"), []string{u.t("The active sheet"), u.t("The whole workbook"), u.t("The selection")}, s.what, x, &y, w, func(i int) { s.what = i })
	p.choice("sheet/paper", u.t("Paper, when the file names none"), printPaperNames(), s.paper, x, &y, w, func(i int) { s.paper = i })
	p.check("sheet/fit", u.t("Fit all columns on one page wide"), &s.fit, x, &y, w)
	p.check("sheet/grid", u.t("Print the gridlines"), &s.grid, x, &y, w)
	p.check("sheet/heads", u.t("Print the row and column headings"), &s.heads, x, &y, w)
	return y
}

// gridOf and headsOf say whether a sheet prints its gridlines and headings.
func (s *sheetPrint) gridOf(st *xlsx.PageSetup) bool {
	if s.grid != s.grid0 {
		return s.grid
	}
	return st.Grid
}

func (s *sheetPrint) headsOf(st *xlsx.PageSetup) bool {
	if s.heads != s.heads0 {
		return s.heads
	}
	return st.Headings
}

func (s *sheetPrint) sheets() []*xlsx.Sheet {
	if s.what != sheetWorkbook {
		return []*xlsx.Sheet{s.ed.sheet()}
	}
	var out []*xlsx.Sheet
	for _, k := range s.ed.wb.Sheets {
		if !k.Hidden() {
			out = append(out, k)
		}
	}
	return out
}

// cut works the pages out, when anything they depend on changed.
func (s *sheetPrint) cut() {
	key := fmt.Sprint(s.what, s.fit, s.heads, s.heads0, s.paper, s.ed.cur)
	ver := s.ed.version()
	if s.pages != nil && key == s.set && ver == s.ver {
		return
	}
	s.set, s.ver = key, ver
	s.pages = s.pages[:0]
	for _, k := range s.sheets() {
		setup := k.PrintSetup()
		v := s.ed.viewOf(k)
		areas := setup.Areas
		if s.what == sheetSelection {
			areas = []formula.Area{s.selection(v)}
		} else if len(areas) == 0 {
			areas = []formula.Area{printedArea(k)}
		}
		used := printedArea(k)
		_, floats := k.DrawingArea()
		if s.what == sheetWorkbook && len(setup.Areas) == 0 && !floats && k.Cell(int(used.R1), int(used.C1)) == nil {
			continue // a sheet with nothing on it takes no paper
		}
		first := len(s.pages)
		for _, a := range areas {
			// A whole row or column in a print area stops where the sheet does.
			a.R2 = min(a.R2, max(used.R2, a.R1))
			a.C2 = min(a.C2, max(used.C2, a.C1))
			s.cutArea(k, &setup, v, a)
		}
		for i := first; i < len(s.pages); i++ {
			s.pages[i].number, s.pages[i].sheetPages = i-first+1, len(s.pages)-first
		}
	}
	if len(s.pages) == 0 {
		s.pages = append(s.pages, sheetPage{s: s.ed.sheet(), w: 595, h: 842, scale: 1})
	}
}

// printedArea is what a sheet with no print area puts on paper: its cells and what floats over them.
func printedArea(k *xlsx.Sheet) formula.Area {
	a := k.Used()
	if b, ok := k.DrawingArea(); ok {
		if k.Cell(int(a.R1), int(a.C1)) == nil && a == (formula.Area{}) {
			return b
		}
		a.R1, a.C1, a.R2, a.C2 = min(a.R1, b.R1), min(a.C1, b.C1), max(a.R2, b.R2), max(a.C2, b.C2)
	}
	return a
}

// selection is what is chosen on the sheet.
func (s *sheetPrint) selection(v *sheetView) formula.Area {
	if len(v.sel) > 0 {
		return v.sel[len(v.sel)-1]
	}
	r1, c1, r2, c2 := v.anchor[0], v.anchor[1], v.tip[0], v.tip[1]
	if r1 > r2 {
		r1, r2 = r2, r1
	}
	if c1 > c2 {
		c1, c2 = c2, c1
	}
	return formula.Area{R1: int32(r1), C1: int32(c1), R2: int32(r2), C2: int32(c2)}
}

func (s *sheetPrint) cutArea(k *xlsx.Sheet, st *xlsx.PageSetup, v *sheetView, a formula.Area) {
	w, h := st.PaperW, st.PaperH
	if w == 0 {
		paper := printPapers[s.paper]
		w, h = min(paper.w, paper.h), max(paper.w, paper.h)
	}
	if st.Landscape {
		w, h = max(w, h), min(w, h)
	}
	availW, availH := w-st.Margins[0]-st.Margins[2], h-st.Margins[1]-st.Margins[3]
	r1, r2, c1, c2 := int(a.R1), int(a.R2), int(a.C1), int(a.C2)
	wide := v.colX[c2+1] - v.colX[c1]
	high := v.rowTop(r2+1) - v.rowTop(r1)
	headW, headH := 0.0, 0.0
	if s.headsOf(st) {
		headW, headH = sheetHeadW, sheetHeadH
	}
	titleR, titleC := st.TitleRows, st.TitleCols
	band := func(t [2]int, top func(int) float64) float64 {
		if t[0] < 0 {
			return 0
		}
		return top(t[1]+1) - top(t[0])
	}
	bandH := band(titleR, v.rowTop)
	bandW := band(titleC, func(c int) float64 { return v.colX[c] })
	// The scale: the file's, or what makes it fit as many pages as asked.
	scale := 1.0
	if st.Scale > 0 {
		scale = float64(st.Scale) / 100
	}
	fitW, fitH := 0, 0
	if st.FitToPage {
		fitW, fitH = st.FitWidth, st.FitHeight
	}
	if s.fit {
		fitW, fitH = 1, 0
	}
	if fitW > 0 && wide > 0 {
		scale = min(1, (availW/0.75*float64(fitW)-(headW+bandW)*float64(fitW))/wide)
	}
	if fitH > 0 && high > 0 {
		scale = min(scale, 1, (availH/0.75*float64(fitH)-(headH+bandH)*float64(fitH))/high)
	}
	scale = max(scale, 0.05)
	roomW := availW/0.75/scale - headW - bandW
	roomH := availH/0.75/scale - headH - bandH
	manual := fitW == 0 && fitH == 0
	cuts := func(from, to int, size func(int) float64, breaks []int, room float64) [][2]int {
		var out [][2]int
		start := from
		used := 0.0
		for i := from; i <= to; i++ {
			d := size(i)
			if i > start && (used+d > room || manual && slices.Contains(breaks, i)) {
				out = append(out, [2]int{start, i - 1})
				start, used = i, 0
			}
			used += d
		}
		return append(out, [2]int{start, to})
	}
	cols := cuts(c1, c2, func(c int) float64 { return v.colX[c+1] - v.colX[c] }, st.ColBreaks, roomW)
	rows := cuts(r1, r2, func(r int) float64 { return v.rowTop(r+1) - v.rowTop(r) }, st.RowBreaks, roomH)
	x, y := st.Margins[0], st.Margins[1]
	if st.CenterH && len(cols) == 1 {
		x += max(availW-(wide+headW+bandW)*0.75*scale, 0) / 2
	}
	if st.CenterV && len(rows) == 1 {
		y += max(availH-(high+headH+bandH)*0.75*scale, 0) / 2
	}
	add := func(col, row [2]int) {
		s.pages = append(s.pages, sheetPage{s: k, setup: st, w: w, h: h, scale: scale, r1: row[0], r2: row[1], c1: col[0], c2: col[1], titleR: titleR, titleC: titleC, x: x, y: y, workbookName: s.name()})
	}
	if st.OverThenDown {
		for _, row := range rows {
			for _, col := range cols {
				add(col, row)
			}
		}
		return
	}
	for _, col := range cols {
		for _, row := range rows {
			add(col, row)
		}
	}
}

func (s *sheetPrint) count() int {
	s.cut()
	return len(s.pages)
}

func (s *sheetPrint) page(i int) (*printing.Page, error) {
	s.cut()
	if i < 0 || i >= len(s.pages) {
		return nil, fmt.Errorf("there is no page %d", i+1)
	}
	pg := &s.pages[i]
	out := &printing.Page{W: pg.w, H: pg.h}
	if pg.setup == nil {
		return out, nil
	}
	ed := s.ed
	v := ed.viewOf(pg.s)
	styles, styleK := ed.styles, ed.styleK
	const k = sheetCanvasPixels
	ed.styles, ed.styleK = map[int32]*sheetStyle{}, k
	defer func() { ed.styles, ed.styleK = styles, styleK }()
	m := printing.Translate(pg.x, pg.y).Mul(printing.Scale(0.75*pg.scale/k, 0.75*pg.scale/k))
	big := max(pg.w, pg.h) / (0.75 * pg.scale) * k * 2
	c := printing.NewCanvas(out, m, big, big)
	p := sheetPainter{ed: ed, v: v, c: c, k: k, styled: pg.s.HasColumnStyles(), lineW: k * sheetPrintLine, grid: s.gridOf(pg.setup)}
	heads := s.headsOf(pg.setup)
	headW, headH := 0.0, 0.0
	if heads {
		headW, headH = sheetHeadW*k, sheetHeadH*k
	}
	// The bands: repeated titles over the cells of this page, unless the page holds them already.
	rowsOf := func(a, b int) sheetSpan { return sheetSpan{a, b, (v.rowTop(b+1) - v.rowTop(a)) * k} }
	colsOf := func(a, b int) sheetSpan { return sheetSpan{a, b, (v.colX[b+1] - v.colX[a]) * k} }
	rows := []sheetSpan{rowsOf(pg.r1, pg.r2)}
	if pg.titleR[0] >= 0 && pg.titleR[1] < pg.r1 {
		rows = append([]sheetSpan{rowsOf(pg.titleR[0], pg.titleR[1])}, rows...)
	}
	cols := []sheetSpan{colsOf(pg.c1, pg.c2)}
	if pg.titleC[0] >= 0 && pg.titleC[1] < pg.c1 {
		cols = append([]sheetSpan{colsOf(pg.titleC[0], pg.titleC[1])}, cols...)
	}
	y := headH
	for _, row := range rows {
		x := headW
		for _, col := range cols {
			r := gfx.Rect{X: float32(x), Y: float32(y), W: float32(col.size), H: float32(row.size)}
			p.x0, p.y0 = x-v.colX[col.from]*k, y-v.rowTop(row.from)*k
			p.pane(r, row.from, col.from)
			x += col.size
		}
		y += row.size
	}
	if heads {
		s.headings(c, v, rows, cols)
	}
	s.margins(out, pg)
	return out, nil
}

// sheetSpan is a run of rows or columns, and how much room it takes.
type sheetSpan struct {
	from, to int
	size     float64
}

// headings draws the letters and numbers around the cells.
func (s *sheetPrint) headings(c *gfx.Canvas, v *sheetView, rows, cols []sheetSpan) {
	k := float64(sheetCanvasPixels)
	f := s.ed.a.u.fonts.Regular.Face(float32(11 * k))
	ink, line := gfx.Hex(0x505050), gfx.Hex(0xb0b0b0)
	thin := float32(k * sheetPrintLine)
	x := sheetHeadW * k
	for _, sp := range cols {
		for col := sp.from; col <= sp.to; col++ {
			w := (v.colX[col+1] - v.colX[col]) * k
			if w <= 0 {
				continue
			}
			name := formula.ColName(col)
			c.Text(f, float32(x)+(float32(w)-f.Measure(name))/2, (float32(sheetHeadH*k)-f.Height())/2, name, ink)
			c.Fill(gfx.Rect{X: float32(x + w), W: thin, H: float32(sheetHeadH * k)}, line)
			x += w
		}
	}
	y := sheetHeadH * k
	for _, sp := range rows {
		for r := sp.from; r <= sp.to; r++ {
			h := (v.rowTop(r+1) - v.rowTop(r)) * k
			if h <= 0 {
				continue
			}
			name := fmt.Sprint(r + 1)
			c.Text(f, float32(sheetHeadW*k)-f.Measure(name)-float32(3*k), float32(y)+(float32(h)-f.Height())/2, name, ink)
			c.Fill(gfx.Rect{Y: float32(y + h), W: float32(sheetHeadW * k), H: thin}, line)
			y += h
		}
	}
}

// margins draws the header and the footer.
func (s *sheetPrint) margins(out *printing.Page, pg *sheetPage) {
	st := pg.setup
	const px = printing.Pixels
	c := printing.Canvas(out)
	f := s.ed.a.u.fonts.Regular.Face(10 * px)
	draw := func(text string, top float64) {
		parts := headerParts(text)
		now := time.Now()
		expand := func(t string) string {
			return strings.NewReplacer("&P", fmt.Sprint(pg.number), "&N", fmt.Sprint(pg.sheetPages), "&A", pg.s.Name,
				"&F", pg.workbookName, "&D", now.Format("2006-01-02"), "&T", now.Format("15:04"), "&&", "&").Replace(t)
		}
		left, mid, right := expand(parts[0]), expand(parts[1]), expand(parts[2])
		y := float32(top * px)
		ink := gfx.Hex(0x303030)
		c.Text(f, float32(st.Margins[0]*px), y, left, ink)
		c.Text(f, float32(out.W*px/2)-f.Measure(mid)/2, y, mid, ink)
		c.Text(f, float32((out.W-st.Margins[2])*px)-f.Measure(right), y, right, ink)
	}
	if st.Header != "" {
		draw(st.Header, st.HeaderAt)
	}
	if st.Footer != "" {
		draw(st.Footer, pg.h-st.FooterAt-math.Round(float64(f.Height())/px))
	}
}

// headerParts splits a header's text into its left, middle and right, dropping codes of font and size.
func headerParts(s string) [3]string {
	var out [3]string
	at := 1
	for i := 0; i < len(s); {
		if s[i] != '&' || i+1 >= len(s) {
			out[at] += s[i : i+1]
			i++
			continue
		}
		switch c := s[i+1]; c {
		case 'L':
			at, i = 0, i+2
		case 'C':
			at, i = 1, i+2
		case 'R':
			at, i = 2, i+2
		case '"':
			end := strings.IndexByte(s[i+2:], '"')
			if end < 0 {
				return out
			}
			i += 3 + end
		case 'B', 'I', 'U', 'S', 'E', 'X', 'Y', 'K', 'G':
			i += 2
		default:
			if c >= '0' && c <= '9' {
				for i += 1; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
				}
				continue
			}
			out[at] += s[i : i+2]
			i += 2
		}
	}
	return out
}
