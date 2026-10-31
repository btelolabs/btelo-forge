package ui

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/docx"
)

// The Word tab: pages as they will print, white on the dark work area, under a toolbar, beside an outline when it is
// shown. The file's XML is the document (internal/office/docx); this file draws its pages and turns the mouse and the
// keys into edits. Pages are painted once into images of their own and only painted again when they change, so that
// scrolling a long document is copying images.

func init() {
	registerOffice(&officeFormat{
		id:    "word",
		exts:  []string{".docx", ".docm", ".dotx", ".dotm"},
		glyph: icOfficeWord,
		name:  msgs("Document %s")[0],
		open:  openWord,
		blank: func(*App) ([]byte, error) { return docx.Blank() },
	})
}

// wordFocus is the keyboard focus of the document itself.
const wordFocus = "office/word"

// Zoom limits, as a share of actual size.
const (
	wordMinZoom = 0.25
	wordMaxZoom = 4
)

// wordCountEvery is how often a long document's words are counted while it changes.
const wordCountEvery = 500 * time.Millisecond

// wordLayoutBudget is how long a frame may spend laying out what the eye is not waiting for.
const wordLayoutBudget = 6 * time.Millisecond

// wordPointPx is how many pixels a point is at 100%: a page shown at the size of paper on a 96 dpi screen.
const wordPointPx = 96.0 / 72.0

// wordGap is the space around and between pages, in logical pixels.
const wordGap = 16

// wordTile is a page painted into an image.
type wordTile struct {
	page   *docx.Page
	rev    uint64
	scale  float32
	number int
	total  int
	stamp  uint64
	img    *image.RGBA
	used   int
}

// wordEditor is an open Word document.
type wordEditor struct {
	a  *App
	od *officeDoc
	d  *docx.Doc

	zoom   float32
	fit    bool // the width of the page fills the view
	scroll float32
	sideX  float32 // pages wider than the view scroll sideways
	view   gfx.Rect
	scale  float32 // pixels a point, this frame
	tops   []float32
	left   []float32
	height float32 // of all the pages, in pixels

	tiles   map[*docx.Page]*wordTile
	frame   int
	picRev  uint64
	follow  bool // the caret must be brought into view
	goalX   float32
	lastAt  docx.Pos
	lastN   uint64
	blink   time.Time
	drag    bool
	dragBy  int // 1 by words, 2 by paragraphs
	pressAt time.Time
	pressN  int
	pressXY [2]float32
	bodyAt  docx.Pos // where the caret was before going into a header

	outline  bool
	pop      string // the open popup: "color", "highlight", "table", "page"
	page     wordPagePanel
	popAt    gfx.Rect
	tableR   int
	tableC   int
	find     *wordFind
	link     *wordLink
	bar      gfx.Rect
	headings []docx.Heading
	headRev  uint64
	counts   docx.Counts
	countRev uint64
	countAt  time.Time

	dragFrom   docx.Selection // what the press chose, a drag by words or paragraphs stretches from it
	outlineTop float32
}

// openWord reads a document into a tab.
func openWord(a *App, od *officeDoc, data []byte) (officeEditor, error) {
	d, err := docx.Open(data, a.officeBook())
	if err != nil {
		return nil, err
	}
	e := &wordEditor{a: a, od: od, d: d, zoom: 1, fit: false, goalX: -1, tiles: map[*docx.Page]*wordTile{}}
	e.zoom = 1
	d.Pics.Ready = func() {
		a.post(func() { e.picRev++ })
		a.requestFrame()
	}
	return e, nil
}

func (e *wordEditor) snapshot() (func() ([]byte, error), error) {
	return e.d.Snapshot(), nil
}

func (e *wordEditor) version() uint64 { return e.d.Version() }

func (e *wordEditor) holding() bool { return e.drag }

func (e *wordEditor) undo() {
	if e.d.Undo() {
		e.follow, e.goalX = true, -1
	}
}

func (e *wordEditor) redo() {
	if e.d.Redo() {
		e.follow, e.goalX = true, -1
	}
}

func (e *wordEditor) wait() time.Duration {
	if !e.d.Done() {
		return time.Millisecond
	}
	if e.d.Version() != e.countRev {
		return time.Until(e.countAt.Add(wordCountEvery)) + time.Millisecond
	}
	return 0
}

func (e *wordEditor) closing() {
	e.d.Pics.Ready = nil
	e.tiles = nil
}

// dropped puts dropped pictures at the caret.
func (e *wordEditor) dropped(paths []string) bool {
	used := false
	for _, p := range paths {
		if !isPictureFile(p) {
			continue
		}
		used = true
		e.insertPictureFile(p)
	}
	return used
}

func isPictureFile(p string) bool {
	return slices.Contains([]string{".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp", ".tif", ".tiff"}, strings.ToLower(filepath.Ext(p)))
}

// insertPictureFile reads a picture away from the window and puts it at the caret.
func (e *wordEditor) insertPictureFile(path string) {
	a := e.a
	a.background(func(ctx context.Context) {
		data, err := os.ReadFile(path)
		a.post(func() {
			if err == nil {
				err = e.d.InsertPicture(data, filepath.Base(path), 0, 0)
				e.follow = true
			}
			if err != nil {
				a.toast(a.u.tf("The picture could not be added: %s", a.describe(err)), true)
			}
		})
	})
}

// choosePicture asks for a picture and inserts it.
func (e *wordEditor) choosePicture() {
	u := &e.a.u
	e.a.pickFiles(pickRequest{key: "picture", title: u.t("Insert a picture"), kinds: u.t("Pictures"), exts: pickPictures}, func(paths []string) {
		e.insertPictureFile(paths[0])
	})
}

// pasteImage puts an image from the clipboard at the caret.
func (e *wordEditor) pasteImage(img image.Image) {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return
	}
	if err := e.d.InsertPicture(b.Bytes(), "", 0, 0); err != nil {
		e.a.toast(e.a.u.tf("The picture could not be added: %s", e.a.describe(err)), true)
	}
	e.follow = true
}

// draw draws the tab.
func (e *wordEditor) draw(r gfx.Rect) {
	a := e.a
	u := &a.u
	d := e.d
	d.Layout(wordLayoutBudget)
	held := u.blocked
	if e.pop != "" {
		u.blocked = true
	}
	if !u.blocked && u.focus == "" {
		u.setFocus(wordFocus)
	}
	bar := e.drawBar(gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H})
	top := bar.Bottom()
	if e.find != nil {
		h := e.drawFind(gfx.Rect{X: r.X, Y: top, W: r.W, H: r.H})
		top += h
	}
	if e.link != nil {
		h := e.drawLink(gfx.Rect{X: r.X, Y: top, W: r.W, H: r.H})
		top += h
	}
	bottom := r.Bottom() - u.px(26)
	view := gfx.Rect{X: r.X, Y: top, W: r.W, H: bottom - top}
	if e.outline {
		side := gfx.Rect{X: r.X, Y: top, W: u.px(240), H: bottom - top}
		e.drawOutline(side)
		view.X += side.W
		view.W -= side.W
	}
	e.view = view
	e.drawPages(view)
	e.drawCountBar(gfx.Rect{X: r.X, Y: bottom, W: r.W, H: r.Bottom() - bottom})
	u.blocked = held
	if e.pop != "" {
		e.drawPop()
	}
}

// pageRects works out where the pages go at this zoom.
func (e *wordEditor) pageRects(view gfx.Rect) {
	u := &e.a.u
	pages := e.d.Pages()
	gap := u.px(wordGap)
	widest := float32(0)
	for _, pg := range pages {
		widest = max(widest, pg.W)
	}
	if e.fit && widest > 0 {
		e.zoom = min(max((view.W-2*gap)/widest/u.s/wordPointPx, wordMinZoom), wordMaxZoom)
	}
	e.scale = e.zoom * u.s * wordPointPx
	e.tops = e.tops[:0]
	e.left = e.left[:0]
	y := gap
	for _, pg := range pages {
		e.tops = append(e.tops, y)
		w := pg.W * e.scale
		x := max((view.W-w)/2, gap)
		e.left = append(e.left, x)
		y += pg.H*e.scale + gap
	}
	e.height = y
}

// pageXY is the top-left of a page in the window.
func (e *wordEditor) pageXY(i int) (x, y float32) {
	return e.view.X + e.left[i] - e.sideX, e.view.Y + e.tops[i] - e.scroll
}

// pageAtY is the page at a height in the view, the nearest when it is between two.
func (e *wordEditor) pageAt(wy float32) int {
	y := wy - e.view.Y + e.scroll
	n := len(e.tops)
	if n == 0 {
		return -1
	}
	lo, hi := 0, n-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if e.tops[mid] <= y+e.a.u.px(wordGap)/2 {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// tile is the page's image, painted when it has changed.
func (e *wordEditor) tile(pg *docx.Page) *wordTile {
	t := e.tiles[pg]
	stamp := e.d.PaintStamp() + e.picRev
	if t != nil && t.rev == pg.Rev && t.scale == e.scale && t.number == pg.Number && t.total == pg.Total && t.stamp == stamp {
		t.used = e.frame
		return t
	}
	w, h := int(pg.W*e.scale+0.5), int(pg.H*e.scale+0.5)
	if t == nil {
		t = &wordTile{page: pg}
		e.tiles[pg] = t
	}
	if t.img == nil || t.img.Rect.Dx() != w || t.img.Rect.Dy() != h {
		t.img = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	c := gfx.Wrap(t.img)
	c.Clear(colWhite)
	e.d.DrawPage(c, pg, 0, 0, e.scale)
	t.rev, t.scale, t.number, t.total, t.stamp, t.used = pg.Rev, e.scale, pg.Number, pg.Total, stamp, e.frame
	if len(e.tiles) > 12 {
		e.dropTiles()
	}
	return t
}

// dropTiles forgets the images of pages not shown lately.
func (e *wordEditor) dropTiles() {
	for pg, t := range e.tiles {
		if e.frame-t.used > 2 {
			delete(e.tiles, pg)
		}
	}
}

// drawPages draws the visible pages and handles the view's mouse and keys.
func (e *wordEditor) drawPages(view gfx.Rect) {
	a := e.a
	u := &a.u
	d := e.d
	e.frame++
	u.c.Fill(view, colPanel)
	e.pageRects(view)
	e.keys()
	d.Layout(wordLayoutBudget) // what was just typed shows in this frame
	e.pageRects(view)
	e.wheel(view)
	if e.follow {
		e.bringCaret(view)
	}
	e.clampScroll(view)
	pages := d.Pages()
	u.c.Push(view)
	defer u.c.Pop()
	first := max(e.pageAt(view.Y)-1, 0)
	for i := first; i < len(pages); i++ {
		x, y := e.pageXY(i)
		pg := pages[i]
		if y > view.Bottom() {
			break
		}
		w, h := pg.W*e.scale, pg.H*e.scale
		if y+h < view.Y {
			continue
		}
		box := gfx.Rect{X: x, Y: y, W: w, H: h}
		u.c.Fill(gfx.Rect{X: box.X + u.px(1), Y: box.Y + u.px(2), W: w + u.px(2), H: h + u.px(2)}, gfx.Alpha(colBlack, 0.35))
		t := e.tile(pg)
		u.c.Blit(t.img, int(x+0.5), int(y+0.5), true)
		e.drawFound(pg, x, y)
		e.drawSelection(pg, x, y)
		e.drawEditing(pg, i, x, y)
	}
	e.mouse(view)
	e.drawCaret()
	e.drawScrollbar(view)
}

// drawFound marks what the find bar found on a page.
func (e *wordEditor) drawFound(pg *docx.Page, x, y float32) {
	if e.find == nil || len(e.find.hits) == 0 {
		return
	}
	for i, m := range e.find.hits {
		col := gfx.Alpha(gfx.Hex(0xffd54a), 0.45)
		if i == e.find.at {
			col = gfx.Alpha(gfx.Hex(0xff9d2e), 0.6)
		}
		e.d.RangeRects(pg, docx.Pos{B: m.B, Off: m.A}, docx.Pos{B: m.B, Off: m.C}, func(r gfx.Rect) {
			e.a.u.c.Fill(gfx.Rect{X: x + r.X*e.scale, Y: y + r.Y*e.scale, W: r.W * e.scale, H: r.H * e.scale}, col)
		})
	}
}

// drawSelection shades what is chosen.
func (e *wordEditor) drawSelection(pg *docx.Page, x, y float32) {
	u := &e.a.u
	e.d.SelectionRects(pg, func(r gfx.Rect) {
		u.c.Fill(gfx.Rect{X: x + r.X*e.scale, Y: y + r.Y*e.scale, W: r.W * e.scale, H: r.H * e.scale}, gfx.Alpha(colAccent, 0.3))
	})
}

// drawEditing marks the header or footer being typed in, with a line and a word, as Word does.
func (e *wordEditor) drawEditing(pg *docx.Page, i int, x, y float32) {
	if !e.inHeader() || i != e.d.EditPage {
		return
	}
	u := &e.a.u
	st := e.d.Sel.Caret.B.In.Story
	line, name := e.d.HeaderLine(pg, st)
	ly := y + line*e.scale
	u.c.Fill(gfx.Rect{X: x, Y: ly, W: pg.W * e.scale, H: max(1, u.px(1))}, colAccent)
	label := u.t("Header")
	if name == "footer" {
		label = u.t("Footer")
	}
	f := u.face(11)
	w := f.Measure(label) + u.px(12)
	tag := gfx.Rect{X: x, Y: ly, W: w, H: u.px(18)}
	if name == "footer" {
		tag.Y -= tag.H
	}
	u.c.Fill(tag, colAccent)
	u.label(f, tag, label, colOnAccent, 0.5)
}

func (e *wordEditor) inHeader() bool {
	b := e.d.Sel.Caret.B
	return b != nil && b.In.Story != e.d.Body
}

// leaveHeader goes back to the body.
func (e *wordEditor) leaveHeader() {
	if !e.inHeader() {
		return
	}
	at := e.bodyAt
	if at.B == nil || at.B.In == nil {
		at = docx.Pos{B: e.d.Body.Paragraphs()[0]}
	}
	e.d.Sel = docx.Selection{Anchor: at, Caret: at}
	e.d.TouchHeaders()
}

// enterHeader puts the caret in a page's header or footer, making it when the page has none.
func (e *wordEditor) enterHeader(i int, kind string) {
	pages := e.d.Pages()
	if i < 0 || i >= len(pages) {
		return
	}
	if !e.inHeader() {
		e.bodyAt = e.d.Sel.Caret
	}
	st := e.d.HeaderStory(pages[i], kind)
	if st == nil {
		return
	}
	e.d.EditPage = i
	e.d.Finish()
	at := docx.Pos{B: st.Paragraphs()[0]}
	e.d.Sel = docx.Selection{Anchor: at, Caret: at}
	e.d.TouchHeaders()
}

// wheel scrolls, and with Ctrl zooms about the pointer.
func (e *wordEditor) wheel(view gfx.Rect) {
	u := &e.a.u
	if u.wheel == 0 || u.blocked || !view.Contains(u.mx, u.my) {
		return
	}
	if u.ctrl {
		old := e.scale
		z := e.zoom * pow(1.1, u.wheel)
		e.setZoom(z)
		e.fit = false
		e.pageRects(view)
		// The point under the pointer stays there.
		e.scroll = (e.scroll+u.my-view.Y)*e.scale/old - (u.my - view.Y)
		u.wheel = 0
		return
	}
	if u.shift {
		e.sideX -= u.wheel * u.px(70)
	} else {
		e.scroll -= u.wheel * u.px(70)
	}
	u.wheel = 0
}

func pow(b, x float32) float32 {
	v := float32(1)
	for x >= 1 {
		v *= b
		x--
	}
	for x <= -1 {
		v /= b
		x++
	}
	return v * (1 + (b-1)*x)
}

func (e *wordEditor) setZoom(z float32) {
	e.zoom = min(max(z, wordMinZoom), wordMaxZoom)
	e.fit = false
}

func (e *wordEditor) clampScroll(view gfx.Rect) {
	e.scroll = max(min(e.scroll, e.height-view.H), 0)
	widest := float32(0)
	for i, pg := range e.d.Pages() {
		widest = max(widest, e.left[i]+pg.W*e.scale)
	}
	e.sideX = max(min(e.sideX, widest+e.a.u.px(wordGap)-view.W), 0)
}

// bringCaret scrolls until the caret shows.
func (e *wordEditor) bringCaret(view gfx.Rect) {
	e.follow = false
	s, ok := e.d.Reach(e.d.Sel.Caret)
	if !ok {
		return
	}
	e.pageRects(view)
	if s.Page >= len(e.tops) {
		return
	}
	y := e.tops[s.Page] + s.Y*e.scale
	h := s.H * e.scale
	pad := e.a.u.px(24)
	if y-pad < e.scroll {
		e.scroll = y - pad
	} else if y+h+pad > e.scroll+view.H {
		e.scroll = y + h + pad - view.H
	}
	x := e.left[s.Page] + s.X*e.scale
	if x-pad < e.sideX {
		e.sideX = x - pad
	} else if x+pad > e.sideX+view.W {
		e.sideX = x + pad - view.W
	}
}

// caretRect is the caret in window pixels.
func (e *wordEditor) caretRect() (gfx.Rect, bool) {
	s, ok := e.d.Locate(e.d.Sel.Caret)
	if !ok || s.Page >= len(e.tops) {
		return gfx.Rect{}, false
	}
	x, y := e.pageXY(s.Page)
	return gfx.Rect{X: x + s.X*e.scale, Y: y + s.Y*e.scale, W: max(1, e.a.u.px(1.25)), H: s.H * e.scale}, true
}

// drawCaret draws the blinking caret and puts the input method there.
func (e *wordEditor) drawCaret() {
	u := &e.a.u
	if u.focus != wordFocus {
		return
	}
	r, ok := e.caretRect()
	if !ok {
		return
	}
	c := e.d.Sel.Caret
	if c != e.lastAt || e.d.Version() != e.lastN {
		e.lastAt, e.lastN = c, e.d.Version()
		u.caretBlink = time.Now()
	}
	if u.inactive || time.Since(u.caretBlink)%caretBlinkPeriod < caretBlinkPeriod/2 {
		if e.d.Sel.Empty() {
			u.c.Fill(r, colBlack)
		}
	}
	rp := e.d.CaretRun()
	family := rp.East
	if family == "" {
		family = rp.Ascii
	}
	u.imeWords(r.X, r.Y, r.H, rp.Size*e.scale, family)
	u.blinking = true
}

// drawScrollbar shows how far down the document the view is; it can be dragged.
func (e *wordEditor) drawScrollbar(view gfx.Rect) {
	u := &e.a.u
	if e.height <= view.H {
		return
	}
	track := gfx.Rect{X: view.Right() - u.px(10), Y: view.Y, W: u.px(10), H: view.H}
	h := max(view.H*view.H/e.height, u.px(30))
	y := view.Y + (view.H-h)*e.scroll/max(e.height-view.H, 1)
	knob := gfx.Rect{X: track.X + u.px(2), Y: y, W: u.px(6), H: h}
	col := colBorderHi
	id := "office/word/scroll"
	if u.hover(id, track) {
		col = colDim
	}
	u.c.Fill(knob, col)
	if u.press(id, track) {
		u.drag.values[0] = float64(e.scroll)
	}
	if u.dragging(id) {
		e.scroll = float32(u.drag.values[0]) + (u.my-u.drag.y)*(e.height-view.H)/max(view.H-h, 1)
	}
	u.letGo(id)
}

// countNow keeps the word count; a long document's is counted at most twice a second, as it walks all the text.
func (e *wordEditor) countNow() docx.Counts {
	if v := e.d.Version(); v != e.countRev && (time.Since(e.countAt) > wordCountEvery || len(e.d.Body.Paragraphs()) < 2000) || e.counts.Pages != len(e.d.Pages()) {
		e.counts, e.countRev, e.countAt = e.d.Count(), v, time.Now()
	}
	return e.counts
}

// drawCountBar is the line under the pages: the page, the count, the zoom.
func (e *wordEditor) drawCountBar(r gfx.Rect) {
	a := e.a
	u := &a.u
	u.c.Fill(r, colSurface)
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: max(1, u.px(1))}, colBorder)
	c := e.countNow()
	page := 1
	if s, ok := e.d.Locate(e.d.Sel.Caret); ok {
		page = s.Page + 1
	}
	total := u.tf("Page %d of %d", page, max(c.Pages, 1))
	if !e.d.Done() {
		total = u.tf("Page %d of %d…", page, max(c.Pages, 1))
	}
	text := total + "    " + u.tf("%d words", c.Words) + "    " + u.tf("%d characters", c.NoSpaces)
	f := u.face(12)
	u.label(f, gfx.Rect{X: r.X + u.px(12), Y: r.Y, W: r.W / 2, H: r.H}, text, colDim, 0)
	side := r.H
	x := r.Right() - u.px(8) - side
	if u.iconButton("office/word/zoomin", gfx.Rect{X: x, Y: r.Y, W: side, H: side}, "", u.t("Zoom in"), false, true) {
		e.setZoom(e.zoom * 1.1)
	}
	x -= u.px(52)
	u.label(f, gfx.Rect{X: x, Y: r.Y, W: u.px(52), H: r.H}, u.tf("%d%%", int(e.zoom*100+0.5)), colDim, 0.5)
	x -= side
	if u.iconButton("office/word/zoomout", gfx.Rect{X: x, Y: r.Y, W: side, H: side}, "", u.t("Zoom out"), false, true) {
		e.setZoom(e.zoom / 1.1)
	}
	x -= side + u.px(6)
	if u.iconButton("office/word/fit", gfx.Rect{X: x, Y: r.Y, W: side, H: side}, "", u.t("Fit the width"), e.fit, true) {
		e.fit = !e.fit
	}
	x -= side
	if u.iconButton("office/word/full", gfx.Rect{X: x, Y: r.Y, W: side, H: side}, "", u.t("Actual size"), !e.fit && e.zoom == 1, true) {
		e.fit = false
		e.zoom = 1
	}
}

// drawOutline is the headings beside the pages.
func (e *wordEditor) drawOutline(r gfx.Rect) {
	a := e.a
	u := &a.u
	u.c.Fill(r, colSurface)
	u.c.Fill(gfx.Rect{X: r.Right() - max(1, u.px(1)), Y: r.Y, W: max(1, u.px(1)), H: r.H}, colBorder)
	if v := e.d.Version(); v != e.headRev || e.headings == nil {
		e.headings, e.headRev = e.d.Outline(), v
		if e.headings == nil {
			e.headings = []docx.Heading{}
		}
	}
	u.label(u.bold(12.5), gfx.Rect{X: r.X + u.px(12), Y: r.Y, W: r.W - u.px(24), H: u.px(32)}, u.t("Headings"), colDim, 0)
	list := gfx.Rect{X: r.X, Y: r.Y + u.px(32), W: r.W, H: r.H - u.px(32)}
	if len(e.headings) == 0 {
		lines := u.face(12).Wrap(u.t("Paragraphs in a heading style are listed here."), r.W-u.px(24))
		for i, line := range lines {
			u.label(u.face(12), gfx.Rect{X: r.X + u.px(12), Y: list.Y + float32(i)*u.px(18), W: r.W - u.px(24), H: u.px(18)}, line, colFaint, 0)
		}
		return
	}
	u.c.Push(list)
	defer u.c.Pop()
	rowH := u.px(28)
	if list.Contains(u.mx, u.my) && u.wheel != 0 {
		e.outlineScroll(u.wheel * rowH * 2)
		u.wheel = 0
	}
	maxScroll := max(float32(len(e.headings))*rowH-list.H, 0)
	e.outlineTop = min(max(e.outlineTop, 0), maxScroll)
	here := -1
	for i, h := range e.headings {
		if h.B.Flat <= e.d.Sel.Caret.B.Flat && e.d.Sel.Caret.B.In.Story == e.d.Body {
			here = i
		}
	}
	y := list.Y - e.outlineTop
	for i, h := range e.headings {
		row := gfx.Rect{X: list.X, Y: y, W: list.W, H: rowH}
		y += rowH
		if row.Bottom() < list.Y || row.Y > list.Bottom() {
			continue
		}
		id := "office/word/head/" + strconv.Itoa(i)
		hot := u.hover(id, row)
		if i == here {
			u.c.Fill(row, colSelected)
		} else if hot {
			u.c.Fill(row, colSurfaceHi)
		}
		if hot {
			u.cursor = CursorHand
		}
		pad := u.px(12) + float32(min(h.Level, 6))*u.px(14)
		col := colDim
		if h.Level == 0 || i == here {
			col = colText
		}
		u.label(u.face(12.5), gfx.Rect{X: row.X + pad, Y: row.Y, W: row.W - pad - u.px(8), H: row.H}, h.Text, col, 0)
		if u.clicked(id, row) {
			at := docx.Pos{B: h.B}
			e.d.SetCaret(at, false)
			e.scrollToCaretTop()
			u.setFocus(wordFocus)
		}
	}
}

// scrollToCaretTop puts the caret's line near the top of the view.
func (e *wordEditor) scrollToCaretTop() {
	s, ok := e.d.Reach(e.d.Sel.Caret)
	if !ok {
		return
	}
	e.pageRects(e.view)
	if s.Page < len(e.tops) {
		e.scroll = e.tops[s.Page] + s.Y*e.scale - e.a.u.px(24)
	}
}

func (e *wordEditor) outlineScroll(by float32) { e.outlineTop -= by }
