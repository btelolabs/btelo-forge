package ui

import (
	"image/color"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/formula"
	"github.com/btelolabs/btelo-forge/internal/office/xlsx"
)

// The grid: paper-white cells with Office's own look, under dark headings that belong to the app.

var (
	sheetGridLine = gfx.Hex(0xd4d4d4)
	sheetPaper    = colWhite
	sheetInk      = colBlack
)

// sheetView is a sheet's place in the window, and where its rows and columns are.
type sheetView struct {
	s                *xlsx.Sheet
	scrollX, scrollY float64 // in pixels at 100%, past the frozen part
	active           [2]int  // the cell typed into
	anchor           [2]int  // the fixed corner of the range being stretched
	tip              [2]int  // and its free corner
	sel              []formula.Area
	ver              uint64
	built            bool
	colX             []float64 // left edges at 100%; one more than the sheet has columns
	rowY             []float64 // top edges at 100% of the rows in the table
	defH             float64   // the height of the rows past the table
	frozenR, frozenC int
	auto             map[int]float64 // heights in points of rows measured for drawing, the file having none
	used             formula.Area
	drawings         []xlsx.Drawing // what floats over the cells
}

// layout works out where rows and columns are, when the sheet changed since the last time.
func (v *sheetView) layout(ver uint64) {
	if v.built && v.ver == ver {
		return
	}
	v.ver, v.built = ver, true
	s := v.s
	v.drawings = s.Drawings()
	if v.colX == nil {
		v.colX = make([]float64, xlsx.MaxCols+1)
	}
	x := 0.0
	for c := 0; c < xlsx.MaxCols; c++ {
		v.colX[c] = x
		x += s.ColPixels(c)
	}
	v.colX[xlsx.MaxCols] = x
	n := s.RowCount()
	v.rowY = v.rowY[:0]
	if cap(v.rowY) < n+1 {
		v.rowY = make([]float64, 0, n+1+n/2)
	}
	y := 0.0
	for r := 0; r < n; r++ {
		v.rowY = append(v.rowY, y)
		h := sheetRowPixels(s, r)
		if v.auto != nil {
			if pt, ok := v.auto[r]; ok && h == math.Round(s.DefaultHeight()*96/72) {
				h = math.Round(pt * 96 / 72)
			}
		}
		y += h
	}
	v.rowY = append(v.rowY, y)
	v.defH = sheetRowPixels(s, n)
	v.frozenR, v.frozenC = s.Freeze()
	v.used = s.Used()
}

func sheetRowPixels(s *xlsx.Sheet, r int) float64 { return math.Round(s.RowPoints(r) * 96 / 72) }

// rowTop is the top of a row at 100%.
func (v *sheetView) rowTop(r int) float64 {
	if last := len(v.rowY) - 1; r > last {
		return v.rowY[last] + float64(r-last)*v.defH
	}
	return v.rowY[max(r, 0)]
}

// rowAtY is the row at a height at 100%.
func (v *sheetView) rowAtY(y float64) int {
	last := len(v.rowY) - 1
	if y >= v.rowY[last] {
		if v.defH <= 0 {
			return min(last, xlsx.MaxRows-1)
		}
		return min(last+int((y-v.rowY[last])/v.defH), xlsx.MaxRows-1)
	}
	lo, hi := 0, last
	for lo+1 < hi {
		m := (lo + hi) / 2
		if v.rowY[m] <= y {
			lo = m
		} else {
			hi = m
		}
	}
	return lo
}

// colAtX is the column at a distance at 100%.
func (v *sheetView) colAtX(x float64) int {
	lo, hi := 0, xlsx.MaxCols
	for lo+1 < hi {
		m := (lo + hi) / 2
		if v.colX[m] <= x {
			lo = m
		} else {
			hi = m
		}
	}
	return lo
}

// cellBox is a range's box on screen.
func (ed *sheetEd) cellBox(v *sheetView, a formula.Area) gfx.Rect {
	k := ed.scale()
	x, _ := ed.screenX(v, int(a.C1))
	y, _ := ed.screenY(v, int(a.R1))
	return gfx.Rect{X: float32(x), Y: float32(y), W: float32((v.colX[a.C2+1] - v.colX[a.C1]) * k), H: float32((v.rowTop(int(a.R2)+1) - v.rowTop(int(a.R1))) * k)}
}

// screenX is a column's left edge on screen, and whether it is in the frozen part.
func (ed *sheetEd) screenX(v *sheetView, c int) (float64, bool) {
	k := ed.scale()
	p := float64(ed.body.panes.X)
	if c < v.frozenC {
		return p + v.colX[c]*k, true
	}
	return p + (v.colX[c]-v.scrollX)*k, false
}

// screenY is a row's top edge on screen, and whether it is in the frozen part.
func (ed *sheetEd) screenY(v *sheetView, r int) (float64, bool) {
	k := ed.scale()
	p := float64(ed.body.panes.Y)
	if r < v.frozenR {
		return p + v.rowTop(r)*k, true
	}
	return p + (v.rowTop(r)-v.scrollY)*k, false
}

// cellAt is the cell under a point of the panes.
func (ed *sheetEd) cellAt(v *sheetView, x, y float32) (r, c int) {
	k := ed.scale()
	p := ed.body.panes
	dx, dy := float64(x-p.X)/k, float64(y-p.Y)/k
	if dx >= v.colX[v.frozenC] {
		dx += v.scrollX
	}
	if dy >= v.rowTop(v.frozenR) {
		dy += v.scrollY
	}
	return v.rowAtY(max(dy, 0)), v.colAtX(max(dx, 0))
}

// sheetStyle is a format ready to draw at the current zoom.
type sheetStyle struct {
	*xlsx.Style
	fg      color.RGBA
	fill    color.RGBA
	hasFill bool
	face    *gfx.Face
	lineH   float32
}

func (ed *sheetEd) styleOf(i int32) *sheetStyle {
	if s := ed.styles[i]; s != nil {
		return s
	}
	st := ed.wb.Styles.Get(i)
	s := &sheetStyle{Style: st, fg: hexRGBA(st.Color, sheetInk)}
	s.fill, s.hasFill = hexRGBA(st.Fill, sheetPaper), st.Fill != ""
	size := float32(st.Size * 96 / 72 * ed.styleK)
	s.face = ed.a.officeBook().Font(st.Font, st.Bold, st.Italic).Face(max(size, 1))
	s.lineH = size * sheetLineHeight * 72 / 96
	ed.styles[i] = s
	return s
}

// sheetPainter draws cells onto a canvas: the window's, or a picture for sheet_render.
type sheetPainter struct {
	ed     *sheetEd
	v      *sheetView
	c      *gfx.Canvas
	k      float64
	x0, y0 float64 // where the sheet's corner is in the canvas's pixels
	styled bool    // the sheet has column formats, so empty cells may have fills
	// On paper: lines are this many canvas pixels a screen's pixel, and the file's gridline setting is not the
	// screen's. Zero is the screen.
	lineW float32
	grid  bool
}

// unit is how wide one of the screen's pixels is drawn.
func (p *sheetPainter) unit() float32 {
	if p.lineW > 0 {
		return p.lineW
	}
	return 1
}

// gridded reports that gridlines show.
func (p *sheetPainter) gridded() bool {
	if p.lineW > 0 {
		return p.grid
	}
	return p.v.s.ShowGrid()
}

// drawGrid draws the headings and the panes.
func (ed *sheetEd) drawGrid(v *sheetView) {
	a := ed.a
	u := &a.u
	b := &ed.body
	k := ed.scale()
	p := sheetPainter{ed: ed, v: v, c: u.c, k: k, styled: v.s.HasColumnStyles()}
	fw, fh := float32(v.colX[v.frozenC]*k), float32(v.rowTop(v.frozenR)*k)
	fw, fh = min(fw, b.panes.W), min(fh, b.panes.H)
	px, py := float64(b.panes.X), float64(b.panes.Y)
	sx, sy := px-v.scrollX*k, py-v.scrollY*k
	main := gfx.Rect{X: b.panes.X + fw, Y: b.panes.Y + fh, W: b.panes.W - fw, H: b.panes.H - fh}
	pane := func(r gfx.Rect, x0, y0 float64, r0, c0 int) {
		if r.W <= 0 || r.H <= 0 {
			return
		}
		p.x0, p.y0 = x0, y0
		p.pane(r, r0, c0)
		ed.drawSelection(&p, r)
	}
	pane(main, sx, sy, v.frozenR, v.frozenC)
	if fw > 0 {
		pane(gfx.Rect{X: b.panes.X, Y: main.Y, W: fw, H: main.H}, px, sy, v.frozenR, 0)
	}
	if fh > 0 {
		pane(gfx.Rect{X: main.X, Y: b.panes.Y, W: main.W, H: fh}, sx, py, 0, v.frozenC)
	}
	if fw > 0 && fh > 0 {
		pane(gfx.Rect{X: b.panes.X, Y: b.panes.Y, W: fw, H: fh}, px, py, 0, 0)
	}
	if fw > 0 {
		u.c.Fill(gfx.Rect{X: main.X - 1, Y: b.panes.Y, W: 1, H: b.panes.H}, gfx.Hex(0x808080))
	}
	if fh > 0 {
		u.c.Fill(gfx.Rect{X: b.panes.X, Y: main.Y - 1, W: b.panes.W, H: 1}, gfx.Hex(0x808080))
	}
	ed.drawHeadings(v)
	ed.drawScrollbars(v)
}

// pane draws the cells in r, one row at a time, from row r0 and column c0 at the most.
func (p *sheetPainter) pane(r gfx.Rect, r0, c0 int) {
	v, c := p.v, p.c
	c.Push(r)
	defer c.Pop()
	c.Fill(r, sheetPaper)
	k := p.k
	// The rows and columns on show.
	first := max(v.rowAtY((float64(r.Y)-p.y0)/k), r0)
	firstC := max(v.colAtX((float64(r.X)-p.x0)/k), c0)
	last, lastC := first, firstC
	for p.y0+v.rowTop(last+1)*k < float64(r.Bottom()) && last < xlsx.MaxRows-1 {
		last++
		if last > first+4000 {
			break
		}
	}
	for p.x0+v.colX[lastC+1]*k < float64(r.Right()) && lastC < xlsx.MaxCols-1 {
		lastC++
	}
	if p.gridded() {
		u := p.unit()
		for i := first; i <= last+1; i++ {
			c.Fill(gfx.Rect{X: r.X, Y: float32(p.y0+v.rowTop(i)*k) - u, W: r.W, H: u}, sheetGridLine)
		}
		for i := firstC; i <= lastC+1; i++ {
			c.Fill(gfx.Rect{X: float32(p.x0+v.colX[i]*k) - u, Y: r.Y, W: u, H: r.H}, sheetGridLine)
		}
	}
	merged := p.visibleMerges(first, firstC, last, lastC)
	for i := first; i <= last; i++ {
		p.rowFills(i, firstC, lastC)
	}
	for _, m := range merged {
		p.mergeBack(m)
	}
	for i := first; i <= last; i++ {
		p.rowText(i, firstC, lastC, merged)
	}
	for _, m := range merged {
		p.mergeText(m)
	}
	for i := first; i <= last; i++ {
		p.rowBorders(i, firstC, lastC, merged)
	}
	p.floats(r)
}

func (p *sheetPainter) box(r, c int) gfx.Rect {
	v := p.v
	x1, x2 := p.x0+v.colX[c]*p.k, p.x0+v.colX[c+1]*p.k
	y1, y2 := p.y0+v.rowTop(r)*p.k, p.y0+v.rowTop(r+1)*p.k
	return gfx.Rect{X: float32(x1), Y: float32(y1), W: float32(x2 - x1), H: float32(y2 - y1)}
}

func (p *sheetPainter) area(a formula.Area) gfx.Rect {
	v := p.v
	x1, x2 := p.x0+v.colX[a.C1]*p.k, p.x0+v.colX[a.C2+1]*p.k
	y1, y2 := p.y0+v.rowTop(int(a.R1))*p.k, p.y0+v.rowTop(int(a.R2)+1)*p.k
	return gfx.Rect{X: float32(x1), Y: float32(y1), W: float32(x2 - x1), H: float32(y2 - y1)}
}

func (p *sheetPainter) visibleMerges(r1, c1, r2, c2 int) []formula.Area {
	var out []formula.Area
	for _, m := range p.v.s.Merges() {
		if int(m.R1) <= r2 && r1 <= int(m.R2) && int(m.C1) <= c2 && c1 <= int(m.C2) {
			out = append(out, m)
		}
	}
	return out
}

func inMerge(ms []formula.Area, r, c int) bool {
	for i := range ms {
		m := &ms[i]
		if int(m.R1) <= r && r <= int(m.R2) && int(m.C1) <= c && c <= int(m.C2) {
			return true
		}
	}
	return false
}

// rowFills paints the backgrounds of a row.
func (p *sheetPainter) rowFills(r, c1, c2 int) {
	ed, v := p.ed, p.v
	row := v.s.RowAt(r)
	if row != nil && row.Hidden {
		return
	}
	if p.styled || row != nil && row.Custom {
		for c := c1; c <= c2; c++ {
			if st := ed.styleOf(v.s.StyleAt(r, c)); st.hasFill {
				p.c.Fill(p.box(r, c), st.fill)
			}
		}
		return
	}
	if row == nil {
		return
	}
	i := row.Find(int32(c1))
	for ; i < len(row.Cells) && int(row.Cells[i].Col) <= c2; i++ {
		if st := ed.styleOf(row.Cells[i].Style); st.hasFill {
			p.c.Fill(p.box(r, int(row.Cells[i].Col)), st.fill)
		}
	}
}

// mergeBack clears a merged cell's inside.
func (p *sheetPainter) mergeBack(m formula.Area) {
	st := p.ed.styleOf(p.v.s.StyleAt(int(m.R1), int(m.C1)))
	col := sheetPaper
	if st.hasFill {
		col = st.fill
	}
	box := p.area(m)
	u := p.unit()
	p.c.Fill(gfx.Rect{X: box.X, Y: box.Y, W: box.W - u, H: box.H - u}, col)
}

// rowText draws the words of a row. A text runs on over empty cells beside it, on the side its alignment points to.
func (p *sheetPainter) rowText(r, c1, c2 int, merged []formula.Area) {
	v := p.v
	row := v.s.RowAt(r)
	if row == nil || row.Hidden {
		return
	}
	cells := row.Cells
	i := row.Find(int32(c1))
	// A text to the left of the view can reach into it, and one to the right too.
	for i > 0 && cells[i-1].Kind == xlsx.Empty {
		i--
	}
	if i > 0 {
		i--
	}
	for ; i < len(cells); i++ {
		c := &cells[i]
		if c.Kind == xlsx.Empty {
			continue
		}
		col := int(c.Col)
		if len(merged) > 0 && inMerge(merged, r, col) {
			continue
		}
		left, right := 0, xlsx.MaxCols-1
		for j := i - 1; j >= 0; j-- {
			if cells[j].Kind != xlsx.Empty {
				left = int(cells[j].Col) + 1
				break
			}
		}
		for j := i + 1; j < len(cells); j++ {
			if cells[j].Kind != xlsx.Empty {
				right = int(cells[j].Col) - 1
				break
			}
		}
		if col > c2 && left > c2 {
			break
		}
		p.cellText(r, col, c, cellArea(r, col), left, right, c1, c2)
		if col > c2 {
			break
		}
	}
}

func (p *sheetPainter) mergeText(m formula.Area) {
	cell := p.v.s.Cell(int(m.R1), int(m.C1))
	if cell == nil || cell.Kind == xlsx.Empty {
		return
	}
	p.cellText(int(m.R1), int(m.C1), cell, m, int(m.C1), int(m.C2), int(m.C1), int(m.C2))
}

// cellText draws what a cell shows in box.
func (p *sheetPainter) cellText(r, col int, cell *xlsx.Cell, a formula.Area, left, right, c1, c2 int) {
	ed, v, canvas := p.ed, p.v, p.c
	st := ed.styleOf(cell.Style)
	box := p.area(a)
	if box.W <= 0 || box.H <= 0 {
		return
	}
	f := st.face
	val := ed.wb.ValueOf(cell)
	shown := st.Format.Render(val, ed.wb.Date1904())
	text := shown.Text
	if text == "" {
		return
	}
	fg := st.fg
	if shown.HasColor {
		fg = gfx.RGBA(shown.Color)
	}
	pad := float32(max(2*p.k, 1))
	h := st.HAlign
	indent := float32(st.Indent) * float32(9*p.k)
	numeric := val.Kind == formula.Number
	if h == "" {
		switch {
		case numeric:
			h = "right"
		case val.Kind == formula.Bool, val.Kind == formula.Error:
			h = "center"
		default:
			h = "left"
		}
	}
	w := f.Measure(text)
	room := box.W - 2*pad - indent
	if st.Shrink && !st.Wrap && w > room && room > 0 {
		// Shrink to fit: a size of its own for this cell.
		f = ed.a.officeBook().Font(st.Font, st.Bold, st.Italic).Face(max(f.Size*room/w, 1))
		w = f.Measure(text)
		scaled := *st
		scaled.face = f
		st = &scaled
	}
	if numeric && w > room && shown.Numeric {
		if st.Format.IsGeneral() {
			// General rounds to what fits; other formats cannot.
			chars := int(room / max(f.Measure("0"), 1))
			text = formula.General(val.Num, max(chars, 1))
			w = f.Measure(text)
		}
		if w > room {
			n := max(int(room/max(f.Measure("#"), 1)), 1)
			text = strings.Repeat("#", n)
			w = f.Measure(text)
		}
	}
	if shown.Fill != 0 && !st.Wrap {
		text = fillOut(text, shown.Fill, shown.FillAt, f, room)
		w = f.Measure(text)
		h = "left"
	}
	lineH := st.lineH
	if st.Wrap || strings.ContainsRune(text, '\n') {
		var lines []string
		for _, part := range strings.Split(text, "\n") {
			if st.Wrap {
				wrapped := f.Wrap(part, max(room, 1))
				if len(wrapped) == 0 {
					wrapped = []string{""}
				}
				lines = append(lines, wrapped...)
			} else {
				lines = append(lines, part)
			}
		}
		total := float32(len(lines)) * lineH
		y := sheetVPos(st.VAlign, box, total, pad)
		canvas.Push(box)
		for _, line := range lines {
			lw := f.Measure(line)
			p.line(st, fg, line, sheetHPos(h, box, lw, pad, indent), y, lw)
			y += lineH
		}
		canvas.Pop()
		return
	}
	y := sheetVPos(st.VAlign, box, f.Height(), 1)
	x := sheetHPos(h, box, w, pad, indent)
	if w <= box.W-pad {
		if runs := ed.richRuns(cell); runs != nil {
			p.runs(st, fg, runs, x, y)
			return
		}
		p.line(st, fg, text, x, y, w)
		return
	}
	// Too wide: it runs into empty neighbours, or is cut at the cell.
	clip := box
	if !numeric {
		if h != "right" {
			end := right
			for c := col + 1; c <= right; c++ {
				e := p.x0 + v.colX[c+1]*p.k
				p.eraseLine(r, c-1)
				if float32(e) >= x+w+pad {
					end = c
					break
				}
			}
			clip.W = float32(p.x0+v.colX[end+1]*p.k) - clip.X
		}
		if h != "left" {
			start := left
			for c := col - 1; c >= left; c-- {
				s := p.x0 + v.colX[c]*p.k
				p.eraseLine(r, c)
				if float32(s) <= x-pad {
					start = c
					break
				}
			}
			e := clip.Right()
			clip.X = float32(p.x0 + v.colX[start]*p.k)
			clip.W = e - clip.X
		}
	}
	canvas.Push(clip)
	p.line(st, fg, text, x, y, w)
	canvas.Pop()
}

// eraseLine takes out the gridline right of column c in row r, under a text running over it.
func (p *sheetPainter) eraseLine(r, c int) {
	if st := p.ed.styleOf(p.v.s.StyleAt(r, c)); st.hasFill || !p.gridded() {
		return
	}
	box := p.box(r, c)
	u := p.unit()
	p.c.Fill(gfx.Rect{X: box.Right() - u, Y: box.Y, W: u, H: box.H - u}, sheetPaper)
}

func fillOut(text string, fill rune, at int, f *gfx.Face, room float32) string {
	at = min(max(at, 0), len(text))
	w := f.Measure(text)
	one := max(f.Measure(string(fill)), 1)
	n := int((room - w) / one)
	if n <= 0 {
		return text
	}
	return text[:at] + strings.Repeat(string(fill), n) + text[at:]
}

func sheetVPos(align string, box gfx.Rect, h, pad float32) float32 {
	switch align {
	case "top":
		return box.Y + pad
	case "center", "justify", "distributed":
		return box.Y + (box.H-h)/2
	}
	return box.Bottom() - h - pad
}

func sheetHPos(align string, box gfx.Rect, w, pad, indent float32) float32 {
	switch align {
	case "right":
		return box.Right() - w - pad - indent
	case "center", "centerContinuous", "distributed":
		return box.X + (box.W-w)/2
	}
	return box.X + pad + indent
}

// line draws one line with its underline or strike.
func (p *sheetPainter) line(st *sheetStyle, fg color.RGBA, text string, x, y, w float32) {
	p.c.Text(st.face, x, y, text, fg)
	p.decorate(st.face, st.Underline, st.Double, st.Strike, fg, x, y, w)
}

func (p *sheetPainter) decorate(f *gfx.Face, under, double, strike bool, fg color.RGBA, x, y, w float32) {
	u := p.unit()
	if under {
		yy := y + f.Ascent + max(u, f.Descent/3)
		p.c.Fill(gfx.Rect{X: x, Y: yy, W: w, H: u}, fg)
		if double {
			p.c.Fill(gfx.Rect{X: x, Y: yy + 2*u, W: w, H: u}, fg)
		}
	}
	if strike {
		p.c.Fill(gfx.Rect{X: x, Y: y + f.Ascent*0.62, W: w, H: u}, fg)
	}
}

// richRuns are a rich string's parts, read once.
func (ed *sheetEd) richRuns(cell *xlsx.Cell) []xlsx.Run {
	if !ed.wb.IsRich(cell) {
		return nil
	}
	i := int(cell.Num)
	if r, ok := ed.runs[i]; ok {
		return r
	}
	r := ed.wb.Runs(cell)
	if len(ed.runs) > 4096 {
		clear(ed.runs)
	}
	ed.runs[i] = r
	return r
}

// runs draws a rich string on one line.
func (p *sheetPainter) runs(st *sheetStyle, fg color.RGBA, runs []xlsx.Run, x, y float32) {
	ed := p.ed
	base := y + st.face.Ascent
	for _, run := range runs {
		f, col := st.face, fg
		under, strike := st.Underline, st.Strike
		if run.HasProp {
			family, size := st.Font, st.Size
			if run.Font != "" {
				family = run.Font
			}
			if run.Size > 0 {
				size = run.Size
			}
			f = ed.a.officeBook().Font(family, run.Bold, run.Italic).Face(max(float32(size*96/72*p.k), 1))
			col = hexRGBA(run.Color, fg)
			under, strike = run.Under, run.Strike
		}
		w := f.Measure(run.Text)
		p.c.Text(f, x, base-f.Ascent, run.Text, col)
		p.decorate(f, under, false, strike, col, x, base-f.Ascent, w)
		x += w
	}
}

// rowBorders draws a row's borders over everything.
func (p *sheetPainter) rowBorders(r, c1, c2 int, merged []formula.Area) {
	ed, v := p.ed, p.v
	row := v.s.RowAt(r)
	if row == nil || row.Hidden {
		return
	}
	i := row.Find(int32(c1))
	for ; i < len(row.Cells) && int(row.Cells[i].Col) <= c2; i++ {
		c := &row.Cells[i]
		st := ed.styleOf(c.Style)
		if st.Left.Style == "" && st.Right.Style == "" && st.Top.Style == "" && st.Bottom.Style == "" {
			continue
		}
		box := p.box(r, int(c.Col))
		u := p.unit()
		p.edge(st.Left, box.X-u, box.Y-u, box.X-u, box.Bottom())
		p.edge(st.Right, box.Right()-u, box.Y-u, box.Right()-u, box.Bottom())
		p.edge(st.Top, box.X-u, box.Y-u, box.Right(), box.Y-u)
		p.edge(st.Bottom, box.X-u, box.Bottom()-u, box.Right(), box.Bottom()-u)
	}
}

// edge draws one side, from (x1, y1) to (x2, y2), which run along an axis.
func (p *sheetPainter) edge(e xlsx.Edge, x1, y1, x2, y2 float32) {
	if e.Style == "" || e.Style == "none" {
		return
	}
	col := hexRGBA(e.Color, sheetInk)
	u := p.unit()
	across := float32(1)
	on, off := float32(0), float32(0)
	switch e.Style {
	case "medium", "mediumDashed", "mediumDashDot", "mediumDashDotDot", "slantDashDot":
		across = 2
	case "thick":
		across = 3
	}
	switch e.Style {
	case "dashed", "mediumDashed", "dashDot", "mediumDashDot":
		on, off = 4, 3
	case "dotted", "dashDotDot", "mediumDashDotDot":
		on, off = 1, 2
	case "hair":
		on, off = 1, 1
	}
	vertical := x1 == x2
	seg := func(a, b, offset float32) {
		if vertical {
			p.c.Fill(gfx.Rect{X: x1 - float32(math.Floor(float64(across)/2))*u + offset*u, Y: a, W: across * u, H: b - a}, col)
		} else {
			p.c.Fill(gfx.Rect{X: a, Y: y1 - float32(math.Floor(float64(across)/2))*u + offset*u, W: b - a, H: across * u}, col)
		}
	}
	a, b := x1, x2
	if vertical {
		a, b = y1, y2
	}
	if e.Style == "double" {
		across = 1
		seg(a, b, -1)
		seg(a, b, 1)
		return
	}
	if on == 0 {
		seg(a, b, 0)
		return
	}
	for t := a; t < b; t += (on + off) * u {
		seg(t, min(t+on*u, b), 0)
	}
}

// drawSelection marks the chosen ranges in a pane.
func (ed *sheetEd) drawSelection(p *sheetPainter, r gfx.Rect) {
	v := p.v
	c := p.c
	c.Push(r)
	defer c.Pop()
	// One cell is shown by its outline; a range is tinted, but for the cell typed into.
	if ar := v.mergedArea(cellArea(v.active[0], v.active[1])); len(v.sel) > 1 || ar != v.area() {
		tint := gfx.Alpha(colAccent, 0.16)
		for _, a := range v.sel {
			c.Fill(p.area(ed.clipArea(v, a, r, p)), tint)
		}
		box := p.area(ar)
		c.Fill(box, sheetPaper)
		p.repaintActive(ar, box)
	}
	last := p.area(ed.clipArea(v, v.area(), r, p))
	edge := gfx.Hex(0x1a73e8)
	c.Outline(gfx.Rect{X: last.X - 2, Y: last.Y - 2, W: last.W + 2, H: last.H + 2}, 2, edge)
	if len(v.sel) == 1 && !ed.editing {
		h := float32(6)
		hb := gfx.Rect{X: last.Right() - h/2 - 1, Y: last.Bottom() - h/2 - 1, W: h, H: h}
		c.Fill(hb.Inset(-1), sheetPaper)
		c.Fill(hb, edge)
	}
	if ed.drag.kind == sheetDragFill {
		box := p.area(ed.drag.fillTo)
		c.Outline(box, 1, gfx.Hex(0x606060))
	}
	if ed.clip != nil && ed.clip.Sheet == v.s {
		a := formula.Area{R1: int32(ed.clip.FromRow), C1: int32(ed.clip.FromCol), R2: int32(ed.clip.FromRow + ed.clip.Rows - 1), C2: int32(ed.clip.FromCol + ed.clip.Cols - 1)}
		box := p.area(a)
		for _, e := range [][4]float32{{box.X, box.Y, box.X, box.Bottom()}, {box.Right() - 1, box.Y, box.Right() - 1, box.Bottom()}, {box.X, box.Y, box.Right(), box.Y}, {box.X, box.Bottom() - 1, box.Right(), box.Bottom() - 1}} {
			p.edge(xlsx.Edge{Style: "mediumDashed", Color: "1A73E8"}, e[0], e[1], e[2], e[3])
		}
	}
}

// clipArea keeps a huge selection's box in numbers a canvas can take: whole rows and columns reach far off the pane.
func (ed *sheetEd) clipArea(v *sheetView, a formula.Area, r gfx.Rect, p *sheetPainter) formula.Area {
	last := v.rowAtY((float64(r.Bottom())-p.y0)/p.k) + 1
	lastC := v.colAtX((float64(r.Right())-p.x0)/p.k) + 1
	firstR := v.rowAtY(max((float64(r.Y)-p.y0)/p.k, 0))
	firstC := v.colAtX(max((float64(r.X)-p.x0)/p.k, 0))
	if int(a.R2) > last {
		a.R2 = int32(last)
	}
	if int(a.C2) > lastC {
		a.C2 = int32(lastC)
	}
	if int(a.R1) < firstR-1 {
		a.R1 = int32(firstR - 1)
	}
	if int(a.C1) < firstC-1 {
		a.C1 = int32(firstC - 1)
	}
	return a
}

// repaintActive draws the active cell again over the tint.
func (p *sheetPainter) repaintActive(a formula.Area, box gfx.Rect) {
	r, c := int(a.R1), int(a.C1)
	st := p.ed.styleOf(p.v.s.StyleAt(r, c))
	if st.hasFill {
		p.c.Fill(box, st.fill)
	}
	if cell := p.v.s.Cell(r, c); cell != nil && cell.Kind != xlsx.Empty {
		p.c.Push(box)
		p.cellText(r, c, cell, a, c, c, c, c)
		p.c.Pop()
	}
	p.rowBorders(r, c, c, nil)
}

// drawHeadings draws the row numbers and column letters.
func (ed *sheetEd) drawHeadings(v *sheetView) {
	a := ed.a
	u := &a.u
	b := &ed.body
	k := ed.scale()
	u.c.Fill(gfx.Rect{X: b.grid.X, Y: b.grid.Y, W: b.rows.W, H: b.cols.H}, colSurface)
	u.c.Fill(b.cols, colSurface)
	u.c.Fill(b.rows, colSurface)
	// The corner's triangle, which chooses everything.
	cw, ch := b.rows.W, b.cols.H
	u.c.Triangle(b.grid.X+cw-u.px(4), b.grid.Y+ch-u.px(4), b.grid.X+cw-u.px(4), b.grid.Y+ch-u.px(13), b.grid.X+cw-u.px(13), b.grid.Y+ch-u.px(4), colFaint)
	f := u.face(11.5)
	on := func(lo, hi int32, at int) bool { return int(lo) <= at && at <= int(hi) }
	u.c.Push(b.cols)
	drawCol := func(c int) float32 {
		x, _ := ed.screenX(v, c)
		w := v.colX[c+1] - v.colX[c]
		r := gfx.Rect{X: float32(x), Y: b.cols.Y, W: float32(w * k), H: b.cols.H}
		if r.W <= 0 {
			return r.X
		}
		sel, full := false, false
		for _, a := range v.sel {
			sel = sel || on(a.C1, a.C2, c)
			full = full || wholeCols(a) && on(a.C1, a.C2, c)
		}
		col := colDim
		if sel {
			fill := colSelected
			if full {
				fill = gfx.Mix(colSurface, colAccent, 0.45)
			}
			u.c.Fill(r, fill)
			col = colText
		}
		u.c.Fill(gfx.Rect{X: r.Right() - 1, Y: r.Y, W: 1, H: r.H}, colBorderHi)
		if r.W > u.px(14) {
			u.label(f, r, formula.ColName(c), col, 0.5)
		}
		return r.Right()
	}
	for c := 0; c < v.frozenC; c++ {
		drawCol(c)
	}
	mainX := b.panes.X + float32(v.colX[v.frozenC]*k)
	u.c.Push(gfx.Rect{X: mainX, Y: b.cols.Y, W: b.cols.Right() - mainX, H: b.cols.H})
	for c := max(v.colAtX(v.scrollX), v.frozenC); c < xlsx.MaxCols; c++ {
		if drawCol(c) > b.cols.Right() {
			break
		}
	}
	u.c.Pop()
	u.c.Pop()
	u.c.Push(b.rows)
	drawRow := func(r int) float32 {
		y, _ := ed.screenY(v, r)
		h := v.rowTop(r+1) - v.rowTop(r)
		box := gfx.Rect{X: b.rows.X, Y: float32(y), W: b.rows.W, H: float32(h * k)}
		if box.H <= 0 {
			return box.Y
		}
		sel, full := false, false
		for _, a := range v.sel {
			sel = sel || on(a.R1, a.R2, r)
			full = full || wholeRows(a) && on(a.R1, a.R2, r)
		}
		col := colDim
		if sel {
			fill := colSelected
			if full {
				fill = gfx.Mix(colSurface, colAccent, 0.45)
			}
			u.c.Fill(box, fill)
			col = colText
		}
		u.c.Fill(gfx.Rect{X: box.X, Y: box.Bottom() - 1, W: box.W, H: 1}, colBorderHi)
		if box.H > u.px(10) {
			u.label(f, box, strconv.Itoa(r+1), col, 0.5)
		}
		return box.Bottom()
	}
	for r := 0; r < v.frozenR; r++ {
		drawRow(r)
	}
	mainY := b.panes.Y + float32(v.rowTop(v.frozenR)*k)
	u.c.Push(gfx.Rect{X: b.rows.X, Y: mainY, W: b.rows.W, H: b.rows.Bottom() - mainY})
	for r := max(v.rowAtY(v.scrollY), v.frozenR); r < xlsx.MaxRows; r++ {
		if drawRow(r) > b.rows.Bottom() {
			break
		}
	}
	u.c.Pop()
	u.c.Pop()
	// Filter buttons sit on the header row's cells.
	if af, ok := v.s.AutoFilter(); ok {
		u.c.Push(b.panes)
		for c := int(af.C1); c <= int(af.C2); c++ {
			box := ed.filterButton(v, int(af.R1), c)
			if box.Right() < b.panes.X || box.X > b.panes.Right() {
				continue
			}
			on := false
			if _, _, has := v.s.FilterValues(c); has {
				on = true
			}
			u.c.Fill(box, gfx.Hex(0xf2f2f2))
			u.c.Outline(box, 1, gfx.Hex(0xa6a6a6))
			col := gfx.Hex(0x404040)
			if on {
				col = gfx.Hex(0x1a73e8)
			}
			u.icon(icSheetDown, box, 13, col)
		}
		u.c.Pop()
	}
}

// filterButton is the button in the corner of a filter's header cell.
func (ed *sheetEd) filterButton(v *sheetView, r, c int) gfx.Rect {
	u := &ed.a.u
	s := u.px(15)
	box := ed.cellBox(v, cellArea(r, c))
	return gfx.Rect{X: box.Right() - s - 2, Y: box.Bottom() - s - 2, W: s, H: s}
}

// sheetExtent is how far the sheet may scroll: past what is used and past where the view is.
func (ed *sheetEd) sheetExtent(v *sheetView) (w, h float64) {
	return max(v.colX[min(int(v.used.C2)+20, xlsx.MaxCols)], v.scrollX*1.2), max(v.rowTop(int(v.used.R2)+50), v.scrollY*1.2)
}

func (ed *sheetEd) drawScrollbars(v *sheetView) {
	u := &ed.a.u
	b := &ed.body
	k := ed.scale()
	u.c.Fill(b.vbar, colPanel)
	u.c.Fill(b.hbar, colPanel)
	u.c.Fill(gfx.Rect{X: b.vbar.X, Y: b.hbar.Y, W: b.vbar.W, H: b.hbar.H}, colPanel)
	w, h := ed.sheetExtent(v)
	track := func(r gfx.Rect, pos, view, total float64, vertical bool) {
		length := r.H
		if !vertical {
			length = r.W
		}
		size := max(float32(view/(total+view))*length, u.px(24))
		at := float32(pos/total) * (length - size)
		box := gfx.Rect{X: r.X + 2, Y: r.Y + at, W: r.W - 4, H: size}
		if !vertical {
			box = gfx.Rect{X: r.X + at, Y: r.Y + 2, W: size, H: r.H - 4}
		}
		col := colBorderHi
		if ed.drag.kind == sheetDragVBar && vertical || ed.drag.kind == sheetDragHBar && !vertical {
			col = colDim
		}
		u.c.RoundRect(box, u.px(3), col)
	}
	track(b.vbar, v.scrollY, float64(b.panes.H)/k, h, true)
	track(b.hbar, v.scrollX, float64(b.panes.W)/k, w, false)
}

// drawEditor draws the box over the cell being typed into.
func (ed *sheetEd) drawEditor(v *sheetView) {
	u := &ed.a.u
	if ed.editSheet != v.s {
		return // pointing at another sheet for a reference
	}
	area := v.mergedArea(cellArea(ed.editR, ed.editC))
	box := ed.cellBox(v, area)
	st := ed.styleOf(v.s.StyleAt(ed.editR, ed.editC))
	f := st.face
	lines := strings.Split(string(u.edit.text), "\n")
	tw := float32(0)
	for _, l := range lines {
		tw = max(tw, f.Measure(l))
	}
	pad := float32(max(2*ed.scale(), 1))
	w := min(max(box.W, tw+2*pad+f.Measure("00")), ed.body.panes.Right()-box.X)
	h := max(box.H, float32(len(lines))*st.lineH+2)
	r := gfx.Rect{X: box.X, Y: box.Y, W: w, H: min(h, ed.body.panes.Bottom()-box.Y)}
	u.c.Push(ed.body.panes)
	defer u.c.Pop()
	bg := sheetPaper
	if st.hasFill {
		bg = st.fill
	}
	u.c.Fill(r, bg)
	u.c.Outline(gfx.Rect{X: r.X - 2, Y: r.Y - 2, W: r.W + 3, H: r.H + 3}, 2, gfx.Hex(0x1a73e8))
	x0, y0 := r.X+pad, r.Y+1
	sa, sb := u.edit.selection()
	offset := 0
	for i, l := range lines {
		rs := []rune(l)
		y := y0 + float32(i)*st.lineH
		if a, b := min(max(sa-offset, 0), len(rs)), min(max(sb-offset, 0), len(rs)); a < b {
			x1, x2 := x0+f.Measure(string(rs[:a])), x0+f.Measure(string(rs[:b]))
			u.c.Fill(gfx.Rect{X: x1, Y: y, W: x2 - x1, H: st.lineH}, gfx.Alpha(colAccent, 0.35))
		}
		u.c.Text(f, x0, y, l, st.fg)
		offset += len(rs) + 1
	}
	if !ed.inBar {
		line, col := position(u.edit.text, u.edit.caret)
		cx := f.Measure(string([]rune(lines[line])[:col]))
		ed.caretOn(x0+cx, y0+float32(line)*st.lineH, f.Height())
		u.imeWords(u.imeAt[0], u.imeAt[1], u.imeAt[2], f.Size, st.Font)
	}
}

// caretOn draws the caret in the paper's ink rather than the window's light one.
func (ed *sheetEd) caretOn(x, y, h float32) {
	u := &ed.a.u
	seen := caretSeen{u.editOwner, u.edit.caret, u.edit.anchor, u.edit.rev}
	if seen != u.caretSeen {
		u.caretSeen, u.caretBlink = seen, time.Now()
	}
	if u.inactive || time.Since(u.caretBlink)%caretBlinkPeriod < caretBlinkPeriod/2 {
		u.c.Fill(gfx.Rect{X: x, Y: y, W: max(1, u.px(1.5)), H: h}, sheetInk)
	}
	u.imeAt = [3]float32{x, y, h}
	u.blinking = true
}

// placeIME keeps the input method at the active cell, where typing starts.
func (ed *sheetEd) placeIME(v *sheetView) {
	box := ed.cellBox(v, cellArea(v.active[0], v.active[1]))
	st := ed.styleOf(v.s.StyleAt(v.active[0], v.active[1]))
	ed.a.u.imeWords(box.X+2, box.Y, box.H, st.face.Size, st.Font)
}
