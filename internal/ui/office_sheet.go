package ui

import (
	"context"
	"errors"
	"image/color"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/formula"
	"github.com/btelolabs/btelo-forge/internal/office/xlsx"
)

// The spreadsheet tab: a tool bar and a formula bar over the grid, sheet tabs and the selection's sum under it. The
// grid is drawn from the rows on screen only, so a sheet's size does not matter to a frame; what depends on the whole
// sheet (where rows and columns are) is worked out when the sheet changes. The file is the model: see internal/office/xlsx.

const (
	sheetFocus = "office/sheet"
	// sheetSyncSize is the largest file opened on the window's goroutine; a larger one opens behind a message.
	sheetSyncSize = 4 << 20
	// sheetCalcBudget is what a frame may spend on formulas; the rest waits for the next frames.
	sheetCalcBudget = 8 * time.Millisecond
	sheetMinZoom    = 0.25
	// sheetLineHeight is a line's height against its font's size, what Office leaves for Calibri.
	sheetLineHeight = 1.34
	sheetMaxZoom    = 4
)

func init() {
	registerOffice(&officeFormat{
		id:    "sheet",
		exts:  []string{".xlsx", ".xlsm", ".csv", ".tsv"},
		glyph: icOfficeSheet,
		name:  msgs("Spreadsheet %s")[0],
		open:  openSheet,
		blank: func(a *App) ([]byte, error) { return xlsx.Blank("Sheet1") },
	})
}

// sheetEd is an open workbook.
type sheetEd struct {
	a  *App
	od *officeDoc
	wb *xlsx.Workbook

	floats  map[string]*sheetFloat
	plain   *xlsx.Delimited // a comma- or tab-separated file, saved back as one
	done    chan struct{}   // closed when a background load has finished
	loaded  *xlsx.Workbook
	loadErr error
	failed  string

	cur   int // the sheet shown
	views map[*xlsx.Sheet]*sheetView
	zoom  float64

	// The cell being typed into; the words are in the window's shared editor.
	editing    bool
	inBar      bool // the caret is in the formula bar
	typing     bool // started by typing over the cell: arrows leave it
	editSheet  *xlsx.Sheet
	editR      int
	editC      int
	pointFrom  int // a reference being pointed at in a formula: where it is in the text, -1 for none
	pointTo    int
	pointStart [2]int

	drag     sheetDrag
	clip     *xlsx.Clip
	clipText string
	find     sheetFind
	pop      *sheetPop
	rename   int // the sheet tab being renamed, or -1
	renameTo string
	nameBox  string
	stats    sheetStats
	grid     gfx.Rect // where the cells are, for the frame
	body     sheetBody
	styles   map[int32]*sheetStyle
	styleK   float64
	runs     map[int][]xlsx.Run
	circular string
	tabDrag  int
}

// sheetStats is the status bar's numbers, kept until the selection or the sheet changes.
type sheetStats struct {
	ver              uint64
	key              string
	count, numbers   int
	sum, lowest, top float64
}

// openSheet is the format's open.
func openSheet(a *App, od *officeDoc, data []byte) (officeEditor, error) {
	ed := &sheetEd{a: a, od: od, views: map[*xlsx.Sheet]*sheetView{}, zoom: 1, rename: -1, styles: map[int32]*sheetStyle{}, runs: map[int][]xlsx.Run{}}
	ext := strings.ToLower(filepath.Ext(od.path))
	read := func() (*xlsx.Workbook, error) {
		if ext != ".csv" && ext != ".tsv" {
			return xlsx.Open(data)
		}
		wb, d, err := xlsx.OpenDelimited(data, strings.TrimSuffix(filepath.Base(od.path), filepath.Ext(od.path)), ext == ".tsv")
		ed.plain = d
		return wb, err
	}
	if len(data) <= sheetSyncSize {
		wb, err := read()
		if err != nil {
			return nil, err
		}
		ed.wb = wb
		ed.install()
		return ed, nil
	}
	ed.done = make(chan struct{})
	a.background(func(context.Context) {
		ed.loaded, ed.loadErr = read()
		close(ed.done)
		a.post(ed.settle)
	})
	return ed, nil
}

// settle takes the result of a background load, on the window's goroutine.
func (ed *sheetEd) settle() {
	if ed.wb != nil || ed.failed != "" {
		return
	}
	if ed.loadErr != nil {
		ed.failed = ed.a.officeError(ed.od.path, ed.loadErr)
		ed.a.toast(ed.failed, true)
		return
	}
	ed.wb = ed.loaded
	ed.install()
	ed.od.saved = ed.wb.Version()
	ed.a.requestFrame()
}

// await waits for a load in progress.
func (ed *sheetEd) await() error {
	if ed.done != nil {
		<-ed.done
		ed.settle()
	}
	if ed.wb == nil {
		return errors.New(ed.failed)
	}
	return nil
}

// install starts on the first visible sheet, or the one the file was left on.
func (ed *sheetEd) install() {
	for i, s := range ed.wb.Sheets {
		if !s.Hidden() {
			ed.cur = i
			break
		}
	}
	if t := ed.wb.ActiveTab(); t >= 0 && t < len(ed.wb.Sheets) && !ed.wb.Sheets[t].Hidden() {
		ed.cur = t
	}
}

func (ed *sheetEd) sheet() *xlsx.Sheet {
	ed.cur = min(max(ed.cur, 0), len(ed.wb.Sheets)-1)
	return ed.wb.Sheets[ed.cur]
}

func (ed *sheetEd) view() *sheetView { return ed.viewOf(ed.sheet()) }

func (ed *sheetEd) viewOf(s *xlsx.Sheet) *sheetView {
	v := ed.views[s]
	if v == nil {
		v = &sheetView{s: s, sel: []formula.Area{{}}, auto: ed.measureRows(s)}
		ed.views[s] = v
		fr, fc := s.Freeze()
		v.moveTo(fr, fc)
	}
	v.layout(ed.wb.Version())
	return v
}

func (ed *sheetEd) version() uint64 {
	if ed.wb == nil {
		return 0
	}
	return ed.wb.Version()
}

func (ed *sheetEd) holding() bool {
	return ed.editing || ed.drag.kind != sheetDragNone || ed.wb != nil && ed.wb.Pending()
}

func (ed *sheetEd) snapshot() (func() ([]byte, error), error) {
	if ed.wb == nil {
		return nil, errors.New("the workbook is not open")
	}
	ed.finishEdit()
	if ed.plain != nil {
		// Text is made here, where the cells can be read; only the bytes go to the other goroutine.
		data, err := ed.plain.Bytes(ed.wb)
		return func() ([]byte, error) { return data, err }, nil
	}
	return ed.wb.Snapshot()
}

// finishEdit keeps what is being typed, whatever it is, for a save or a tab closing.
func (ed *sheetEd) finishEdit() {
	if !ed.editing {
		return
	}
	if err := ed.store(string(ed.a.u.edit.text)); err != nil {
		ed.cancelEdit()
		return
	}
	ed.endEdit()
}

func (ed *sheetEd) undo() {
	if ed.wb == nil {
		return
	}
	if ed.editing {
		ed.cancelEdit()
		return
	}
	if ed.wb.Undo() {
		ed.afterHistory()
	}
}

func (ed *sheetEd) redo() {
	if ed.wb != nil && !ed.editing && ed.wb.Redo() {
		ed.afterHistory()
	}
}

// afterHistory keeps the view on what exists after undo or redo.
func (ed *sheetEd) afterHistory() {
	ed.cur = min(ed.cur, len(ed.wb.Sheets)-1)
	ed.styles = map[int32]*sheetStyle{}
	ed.a.requestFrame()
}

func (ed *sheetEd) wait() time.Duration {
	switch {
	case ed.wb == nil && ed.failed == "":
		return 100 * time.Millisecond
	case ed.wb == nil:
		return 0
	case ed.wb.Pending(), ed.drag.kind != sheetDragNone:
		return 16 * time.Millisecond
	}
	return 0
}

func (ed *sheetEd) dropped(paths []string) bool { return false }

func (ed *sheetEd) closing() {}

// sheetBody is where the parts of the tab are this frame.
type sheetBody struct {
	tools, bar, grid, vbar, hbar, tabs, status gfx.Rect
	cols, rows                                 gfx.Rect // the column and row headings
	panes                                      gfx.Rect // the cells
}

// draw draws the tab.
func (ed *sheetEd) draw(r gfx.Rect) {
	a := ed.a
	u := &a.u
	u.c.Fill(r, colPanel)
	if ed.wb == nil {
		if ed.done != nil && ed.failed == "" {
			select {
			case <-ed.done:
				ed.settle()
			default:
			}
		}
		text := ed.failed
		if text == "" {
			text = u.t("Opening…")
		}
		if ed.wb == nil {
			u.label(u.face(14), r, text, colDim, 0.5)
			return
		}
	}
	wb := ed.wb
	if wb.Pending() {
		wb.Calc(sheetCalcBudget)
		ed.noteCircular()
	}
	ed.cur = min(ed.cur, len(wb.Sheets)-1)
	if k := ed.scale(); k != ed.styleK {
		ed.styleK = k
		ed.styles = map[int32]*sheetStyle{}
	}
	ed.body = ed.layout(r)
	// A pop-up floats over everything, so what lies under it sees no mouse.
	mx, my, pressed, wheel := u.mx, u.my, u.pressed, u.wheel
	over := ed.pop != nil && ed.popRect().Contains(mx, my)
	if over {
		u.mx, u.my, u.pressed, u.wheel = -1e6, -1e6, false, 0
	}
	v := ed.view()
	ed.takeFocus()
	if !u.blocked && u.focus == sheetFocus {
		ed.keys(v)
	}
	v = ed.view()
	ed.drawGrid(v)
	ed.gridMouse(v)
	ed.drawBars(v)
	ed.drawTabs()
	ed.drawStatus(v)
	if ed.editing {
		ed.drawEditor(v)
	} else if u.focus == sheetFocus {
		ed.placeIME(v)
	}
	u.mx, u.my, u.pressed, u.wheel = mx, my, pressed, wheel
	if ed.pop != nil {
		ed.drawPop()
	}
}

// takeFocus gives the keyboard to the grid when nothing else has it.
func (ed *sheetEd) takeFocus() {
	u := &ed.a.u
	if u.blocked {
		return
	}
	if u.focus == "" {
		u.focus = sheetFocus
	}
	if u.focus == sheetFocus {
		u.focusDrawn = true
	}
}

func (ed *sheetEd) noteCircular() {
	if ed.wb.Pending() {
		return
	}
	ed.circular = ""
	if c := ed.wb.Circular(); len(c) > 0 && int(c[0].Sheet) < len(ed.wb.Sheets) {
		ed.circular = ed.wb.Sheets[c[0].Sheet].Name + "!" + formula.CellName(int(c[0].Row), int(c[0].Col))
	}
}

// scale is pixels on screen per pixel of the sheet at 100%.
func (ed *sheetEd) scale() float64 { return ed.zoom * float64(ed.a.u.s) }

func (ed *sheetEd) layout(r gfx.Rect) sheetBody {
	u := &ed.a.u
	var b sheetBody
	b.tools = gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: u.px(38)}
	y := b.tools.Bottom()
	if ed.find.open {
		ed.find.bar = gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(36)}
		y += ed.find.bar.H
	}
	b.bar = gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(30)}
	y = b.bar.Bottom()
	foot := u.px(28)
	bottom := r.Bottom() - foot
	bar := u.px(12)
	b.tabs = gfx.Rect{X: r.X, Y: bottom, W: r.W, H: foot}
	b.grid = gfx.Rect{X: r.X, Y: y, W: r.W, H: bottom - y}
	b.vbar = gfx.Rect{X: r.Right() - bar, Y: y, W: bar, H: bottom - y - bar}
	b.hbar = gfx.Rect{X: r.X, Y: bottom - bar, W: r.W - bar, H: bar}
	hw, hh := u.px(sheetHeadWidth(ed.view())), u.px(22)
	b.cols = gfx.Rect{X: r.X + hw, Y: y, W: r.W - hw - bar, H: hh}
	b.rows = gfx.Rect{X: r.X, Y: y + hh, W: hw, H: b.hbar.Y - y - hh}
	b.panes = gfx.Rect{X: r.X + hw, Y: y + hh, W: r.W - hw - bar, H: b.hbar.Y - y - hh}
	return b
}

// sheetHeadWidth is how wide the row headings are, in units, by the digits they need.
func sheetHeadWidth(v *sheetView) float32 {
	n := v.rowAtY(v.scrollY) + 100
	return 22 + 8*float32(len(strconv.Itoa(n)))
}

func hexRGBA(s string, or color.RGBA) color.RGBA {
	if len(s) != 6 {
		return or
	}
	n, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return or
	}
	return gfx.Hex(uint32(n))
}

// activeArea is the selection's last range.
func (v *sheetView) area() formula.Area { return v.sel[len(v.sel)-1] }

// moveTo makes one cell the selection.
func (v *sheetView) moveTo(r, c int) {
	r, c = min(max(r, 0), xlsx.MaxRows-1), min(max(c, 0), xlsx.MaxCols-1)
	v.active, v.anchor, v.tip = [2]int{r, c}, [2]int{r, c}, [2]int{r, c}
	v.sel = []formula.Area{v.mergedArea(cellArea(r, c))}
}

func cellArea(r, c int) formula.Area {
	return formula.Area{R1: int32(r), C1: int32(c), R2: int32(r), C2: int32(c)}
}

// spanArea is the range between two cells.
func spanArea(a, b [2]int) formula.Area {
	return formula.Area{R1: int32(min(a[0], b[0])), C1: int32(min(a[1], b[1])), R2: int32(max(a[0], b[0])), C2: int32(max(a[1], b[1]))}
}

// mergedArea grows a range until it cuts no merged cell.
func (v *sheetView) mergedArea(a formula.Area) formula.Area {
	merges := v.s.Merges()
	if len(merges) == 0 {
		return a
	}
	for again := true; again; {
		again = false
		for _, m := range merges {
			if m.R1 <= a.R2 && a.R1 <= m.R2 && m.C1 <= a.C2 && a.C1 <= m.C2 &&
				(m.R1 < a.R1 || m.C1 < a.C1 || m.R2 > a.R2 || m.C2 > a.C2) {
				a.R1, a.C1 = min(a.R1, m.R1), min(a.C1, m.C1)
				a.R2, a.C2 = max(a.R2, m.R2), max(a.C2, m.C2)
				again = true
			}
		}
	}
	return a
}

// inSelection reports whether the cell is chosen.
func (v *sheetView) inSelection(r, c int) bool {
	return slices.ContainsFunc(v.sel, func(a formula.Area) bool {
		return int(a.R1) <= r && r <= int(a.R2) && int(a.C1) <= c && c <= int(a.C2)
	})
}

// wholeRows and wholeCols report what a range covers.
func wholeRows(a formula.Area) bool { return a.C1 == 0 && int(a.C2) == xlsx.MaxCols-1 }
func wholeCols(a formula.Area) bool { return a.R1 == 0 && int(a.R2) == xlsx.MaxRows-1 }

// statsFor brings the status numbers up to date.
func (ed *sheetEd) statsFor(v *sheetView) *sheetStats {
	st := &ed.stats
	var b strings.Builder
	b.WriteString(strconv.Itoa(ed.cur))
	for _, a := range v.sel {
		b.WriteString(formula.AreaName(a))
		b.WriteByte(',')
	}
	key := b.String()
	if st.ver == ed.wb.Version() && st.key == key && !ed.wb.Pending() {
		return st
	}
	*st = sheetStats{ver: ed.wb.Version(), key: key, lowest: math.Inf(1), top: math.Inf(-1)}
	for _, a := range v.sel {
		ed.wb.Each(ed.cur, int(a.R1), int(a.C1), int(a.R2), int(a.C2), func(_, _ int, c formula.Value) bool {
			st.count++
			if c.Kind == formula.Number {
				st.numbers++
				st.sum += c.Num
				st.lowest, st.top = math.Min(st.lowest, c.Num), math.Max(st.top, c.Num)
			}
			return true
		})
	}
	return st
}

// format applies a change of format to the selection.
func (ed *sheetEd) format(ch *xlsx.Change) {
	ed.finishEdit()
	v := ed.view()
	ed.wb.Begin()
	v.s.ApplyStyle(v.sel, ch)
	ed.fitRows(v)
}

// fitRows gives wrapped rows of the selection the height their words need, for rows not sized by hand.
func (ed *sheetEd) fitRows(v *sheetView) {
	for _, a := range v.sel {
		if int64(a.R2-a.R1) > 2000 {
			continue
		}
		for r := int(a.R1); r <= int(a.R2); r++ {
			ed.fitRow(v, r)
		}
	}
}

func (ed *sheetEd) fitRow(v *sheetView, r int) {
	row := v.s.RowAt(r)
	if row == nil || row.Manual {
		return
	}
	v.s.AutoRowHeight(r, max(ed.rowNeeds(v.s, r, row), v.s.DefaultHeight()))
}

// rowNeeds is the height in points a row's fonts and wrapped words want.
func (ed *sheetEd) rowNeeds(s *xlsx.Sheet, r int, row *xlsx.Row) float64 {
	need := 0.0
	for i := range row.Cells {
		c := &row.Cells[i]
		if c.Kind == xlsx.Empty {
			continue
		}
		st := ed.wb.Styles.Get(c.Style)
		h := st.Size * sheetLineHeight
		text := s.Shown(r, int(c.Col)).Text
		if st.Wrap || strings.ContainsRune(text, '\n') {
			if _, merged := s.MergeAt(r, int(c.Col)); !merged {
				f := ed.a.officeBook().Font(st.Font, st.Bold, st.Italic).Face(float32(st.Size * 96 / 72))
				w := float32(s.ColPixels(int(c.Col))) - 4
				lines := 0
				for part := range strings.Lines(text) {
					part = strings.TrimRight(part, "\r\n")
					if st.Wrap {
						lines += max(len(f.Wrap(part, w)), 1)
					} else {
						lines++
					}
				}
				h *= float64(lines)
			}
		}
		need = math.Max(need, h)
	}
	return math.Ceil(need*4) / 4
}

// sheetFitLimit is how many rows of a sheet are measured when it first shows.
const sheetFitLimit = 20000

// measureRows finds rows the file gives no height whose fonts or wrapped words need more than the default, as Office
// works out on opening. The result only draws; the file is not changed by being looked at.
func (ed *sheetEd) measureRows(s *xlsx.Sheet) map[int]float64 {
	var out map[int]float64
	big := map[int32]bool{}
	seen := 0
	for r, n := 0, s.RowCount(); r < n; r++ {
		row := s.RowAt(r)
		if row == nil || row.Height > 0 || row.Hidden {
			continue
		}
		wants := false
		for i := range row.Cells {
			c := &row.Cells[i]
			b, ok := big[c.Style]
			if !ok {
				st := ed.wb.Styles.Get(c.Style)
				b = st.Wrap || st.Size*sheetLineHeight > s.DefaultHeight()
				big[c.Style] = b
			}
			if b && c.Kind != xlsx.Empty {
				wants = true
				break
			}
		}
		if !wants {
			continue
		}
		if seen++; seen > sheetFitLimit {
			break
		}
		if h := ed.rowNeeds(s, r, row); h > s.DefaultHeight() {
			if out == nil {
				out = map[int]float64{}
			}
			out[r] = h
		}
	}
	return out
}
