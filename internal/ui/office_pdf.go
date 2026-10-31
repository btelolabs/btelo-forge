package ui

import (
	"context"
	"errors"
	"image"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/pdf"
	"github.com/btelolabs/btelo-forge/internal/office/pdf/font"
	"github.com/btelolabs/btelo-forge/internal/office/pdf/render"
)

// The PDF tab: pages in a continuous column, drawn away from the window on background goroutines and kept as
// pictures, with the tools and panels around them in the other office_pdf files. The document model is internal/office/pdf,
// the pictures and the text come from its render package.

func init() {
	registerOffice(&officeFormat{id: "pdf", exts: []string{".pdf"}, glyph: icOfficePDF, name: msgs("PDF %s")[0],
		open: func(a *App, od *officeDoc, data []byte) (officeEditor, error) { return openPDF(a, od, data) }})
}

// Limits of what stays in memory and how a page is drawn.
const (
	pdfBitmapBudget  = 600 << 20 // bytes of page pictures
	pdfWholePage     = 16 << 20  // pixels; a page larger than this at its zoom is drawn only around the view
	pdfJobs          = 2         // pages drawn at once, thumbnails apart
	pdfThumbWidth    = 132
	pdfPointsToPx    = 96.0 / 72 // 100% shows a point as it is on a 96 dpi screen
	pdfMaxZoom       = 16.0
	pdfMinZoom       = 0.1
	pdfPageGap       = 12
	pdfPageTimeout   = 60 * time.Second
	pdfSearchMatches = 5000
)

type pdfFit int

const (
	pdfFitNone pdfFit = iota
	pdfFitWidth
	pdfFitPage
)

type pdfBitmapKey struct {
	num   int
	thumb bool
}

// pdfBitmap is a drawn page, or the part of it around the view.
type pdfBitmap struct {
	img   *image.RGBA
	scale float64
	view  int
	rev   int
	used  int // the frame it was last shown in
}

type pdfJob struct {
	cancel context.CancelFunc
	scale  float64
	view   int
	rev    int
	region image.Rectangle
	index  int
}

type pdfMatch struct {
	page     int
	from, to int
}

// pdfEditor is an open PDF.
type pdfEditor struct {
	a  *App
	od *officeDoc

	data     []byte
	locked   bool
	password string
	pwWrong  bool
	opening  bool

	doc    *pdf.Doc
	cache  *render.Cache
	fonts  *render.BookFonts
	pages  []pdf.Page
	seen   uint64 // the document version pages was read at
	signed bool
	frame  int

	// The view.
	zoom     float64
	fit      pdfFit
	view     int
	scrollX  float32
	scrollY  float32
	area     gfx.Rect
	rects    []gfx.Rect // pages in document pixels: the top left of the column is 0, 0
	contentW float32
	contentH float32
	layoutOf [5]float64
	sizes    [][2]float64 // each page's size in points as shown
	sideTab  int          // 0 none, 1 thumbnails, 2 outline, 3 annotations of the file
	sideW    float32

	// Pictures.
	bitmaps map[pdfBitmapKey]*pdfBitmap
	jobs    map[pdfBitmapKey]*pdfJob
	rev     int
	revs    map[int]int
	texts   map[int]*pdfText

	// Text selection.
	selPage        int
	selFrom, selTo int

	// Thumbnails.
	thumbScroll  float32
	picked       map[int]bool
	anchor       int
	dragThumb    bool
	dropAt       int
	outlineOpen  map[string]bool
	outlineItems []pdf.OutlineItem
	outlineSeen  uint64
	outlineView  float32

	// Search.
	searchOpen bool
	query      string
	searched   string
	matches    []pdfMatch
	current    int
	searching  context.CancelFunc
	searchRun  int

	// Tools.
	tool       pdfTool
	color      [3]float64
	altColor   [3]float64 // the colour of the other kind of tool: markers keep theirs, pens theirs
	width      float64
	textStyle  pdf.TextStyle
	drag       pdfDrag
	ink        [][2]float32
	sel        *pdfSel // the chosen annotation
	pending    *pdfPending
	fields     []pdf.Field
	fieldsSeen uint64
	active     *pdfActiveField
	sign       *pdfSignPad
	saveAs     *pdfSaveAs
	pageBox    string
	lastPage   int
	banner     bool       // the signed document's notice was dismissed
	overs      []gfx.Rect // what floated over the pages last frame, whose presses are not for the pages
	oversNext  []gfx.Rect
}

type pdfText struct {
	t   *render.Text
	rev int
}

// openPDF reads a document; one that needs a password opens as a tab asking for it.
func openPDF(a *App, od *officeDoc, data []byte) (*pdfEditor, error) {
	e := &pdfEditor{a: a, od: od, data: data, zoom: 1, fit: pdfFitWidth, sideTab: 1, selPage: -1,
		bitmaps: map[pdfBitmapKey]*pdfBitmap{}, jobs: map[pdfBitmapKey]*pdfJob{}, revs: map[int]int{}, texts: map[int]*pdfText{},
		picked: map[int]bool{}, outlineOpen: map[string]bool{}, color: [3]float64{1, 0.85, 0.1}, altColor: [3]float64{0.95, 0.35, 0.3}, width: 1.5, dropAt: -1}
	e.textStyle = pdf.TextStyle{Family: "Arial", Size: 12}
	doc, err := pdf.Open(data, "")
	switch {
	case errors.Is(err, pdf.ErrPassword):
		e.locked = true
		return e, nil
	case err != nil:
		return nil, err
	}
	return e, e.ready(doc)
}

func (e *pdfEditor) ready(doc *pdf.Doc) error {
	if len(doc.Pages()) == 0 {
		return errors.New("the document has no pages")
	}
	a := e.a
	e.doc = doc
	e.fonts = &render.BookFonts{Book: a.officeBook()}
	e.cache = render.NewCache(doc, e.fonts)
	doc.SetFonts(e.findFont)
	e.locked, e.data = false, nil
	e.pages = doc.Pages()
	e.seen = doc.Version()
	e.signed = doc.Signed()
	return nil
}

// findFont is the first of the family and the fonts behind it that has every character of text.
func (e *pdfEditor) findFont(family string, bold, italic bool, text string) string {
	book := e.a.officeBook()
	var paths []string
	for _, fam := range []string{family, "arial", "微软雅黑", "宋体", "simhei"} {
		if fam == "" {
			continue
		}
		if p := book.File(fam, bold, italic); p != "" {
			paths = append(paths, p)
		}
	}
	paths = append(paths, book.Fallbacks()...)
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		tt, err := font.ParseTrueType(data, 0)
		if err != nil || tt.IsCFF() {
			continue
		}
		all := true
		for _, r := range text {
			if r == '\n' || r == '\r' || r == ' ' {
				continue
			}
			if id, ok := tt.GIDByRune(r); !ok || id == 0 {
				all = false
				break
			}
		}
		if all {
			return p
		}
	}
	if len(paths) > 0 {
		return paths[0]
	}
	return ""
}

func (e *pdfEditor) snapshot() (func() ([]byte, error), error) {
	if e.locked || e.doc == nil {
		data := e.data
		return func() ([]byte, error) { return data, nil }, nil
	}
	e.commitPending()
	e.commitField()
	s := e.doc.Snapshot()
	return s.Save, nil
}

func (e *pdfEditor) version() uint64 {
	if e.doc == nil {
		return 0
	}
	return e.doc.Version()
}

func (e *pdfEditor) holding() bool {
	return e.drag.kind != pdfDragNone || e.pending != nil || e.active != nil
}

func (e *pdfEditor) undo() {
	if e.doc != nil && e.doc.Undo() {
		e.afterHistory()
	}
}

func (e *pdfEditor) redo() {
	if e.doc != nil && e.doc.Redo() {
		e.afterHistory()
	}
}

func (e *pdfEditor) afterHistory() {
	e.rev++
	e.sel, e.active = nil, nil
	e.clearSelection()
	e.sync()
}

func (e *pdfEditor) wait() time.Duration { return 0 }

func (e *pdfEditor) dropped(paths []string) bool {
	if e.doc == nil {
		return false
	}
	used := false
	for _, p := range paths {
		switch strings.ToLower(filepath.Ext(p)) {
		case ".pdf":
			e.insertPDF(p, e.currentPage()+1)
			used = true
		case ".png", ".jpg", ".jpeg", ".webp", ".tif", ".tiff", ".bmp":
			e.placeImage(p)
			used = true
		}
	}
	return used
}

func (e *pdfEditor) closing() {
	for _, j := range e.jobs {
		j.cancel()
	}
	if e.searching != nil {
		e.searching()
	}
}

// busy reports pictures or a search still being worked on.
func (e *pdfEditor) busy() bool { return len(e.jobs) > 0 || e.searching != nil }

// sync notices that the document changed: pages may have come or gone.
func (e *pdfEditor) sync() {
	if v := e.doc.Version(); v != e.seen {
		e.seen = v
		e.pages = e.doc.Pages()
		e.signed = e.doc.Signed()
		e.layoutOf = [5]float64{}
		e.picked = map[int]bool{}
		if e.selPage >= len(e.pages) {
			e.clearSelection()
		}
	}
}

// touched says a page's picture and text are out of date. What is on the screen stays until the new one is ready.
func (e *pdfEditor) touched(pages ...int) {
	for _, i := range pages {
		if i >= 0 && i < len(e.pages) {
			e.revs[e.pages[i].Ref.Num]++
		}
	}
}

func (e *pdfEditor) pageRev(i int) int { return e.rev + e.revs[e.pages[i].Ref.Num] }

// edited runs after an edit that went wrong or right: it tells of a failure and brings the view up to date.
func (e *pdfEditor) edited(err error, pages ...int) bool {
	if err != nil {
		e.a.toast(e.a.u.tf("Could not change the document: %s", e.a.describe(err)), true)
		return false
	}
	e.touched(pages...)
	e.sync()
	e.touched(pages...)
	e.a.requestFrame()
	return true
}

// pxPerPoint is how many window pixels a point of the page takes.
func (e *pdfEditor) pxPerPoint() float64 {
	return e.zoom * pdfPointsToPx * float64(e.a.u.s)
}

func (e *pdfEditor) layout() {
	u := &e.a.u
	e.applyFit()
	scale := e.pxPerPoint()
	key := [5]float64{scale, float64(e.view), float64(len(e.pages)), float64(e.area.W), float64(e.rev)}
	if key == e.layoutOf {
		return
	}
	e.layoutOf = key
	gap := u.px(pdfPageGap)
	e.rects, e.sizes = e.rects[:0], e.sizes[:0]
	var widest float32
	for _, p := range e.pages {
		w, h := e.doc.ViewSize(p, e.view)
		e.sizes = append(e.sizes, [2]float64{w, h})
		rw, rh := float32(math.Round(w*scale)), float32(math.Round(h*scale))
		widest = max(widest, rw)
		e.rects = append(e.rects, gfx.Rect{W: rw, H: rh})
	}
	y := gap
	e.contentW = max(widest+2*gap, e.area.W)
	for i := range e.rects {
		e.rects[i].X = float32(math.Round(float64(e.contentW-e.rects[i].W) / 2))
		e.rects[i].Y = y
		y += e.rects[i].H + gap
	}
	e.contentH = y
}

func (e *pdfEditor) applyFit() {
	if e.fit == pdfFitNone || len(e.pages) == 0 || e.area.W <= 0 {
		return
	}
	u := &e.a.u
	var mw, mh float64
	for i, p := range e.pages {
		if i > 512 {
			break // one size almost always; a long document need not be walked each frame
		}
		w, h := e.doc.ViewSize(p, e.view)
		mw, mh = max(mw, w), max(mh, h)
	}
	gap := float64(u.px(pdfPageGap))
	k := pdfPointsToPx * float64(u.s)
	z := (float64(e.area.W) - 2*gap - float64(u.px(12))) / (mw * k)
	if e.fit == pdfFitPage {
		z = min(z, (float64(e.area.H)-2*gap)/(mh*k))
	}
	e.zoom = min(max(z, pdfMinZoom), pdfMaxZoom)
}

// setZoom changes the zoom keeping the point at (mx, my) in the window where it is.
func (e *pdfEditor) setZoom(z float64, mx, my float32) {
	z = min(max(z, pdfMinZoom), pdfMaxZoom)
	if z == e.zoom && e.fit == pdfFitNone {
		return
	}
	dx, dy := mx-e.area.X+e.scrollX, my-e.area.Y+e.scrollY
	f := float32(z / e.zoom)
	e.zoom, e.fit = z, pdfFitNone
	e.layout()
	e.scrollX, e.scrollY = dx*f-(mx-e.area.X), dy*f-(my-e.area.Y)
}

func (e *pdfEditor) clampScroll() {
	e.scrollY = min(max(e.scrollY, 0), max(e.contentH-e.area.H, 0))
	e.scrollX = min(max(e.scrollX, 0), max(e.contentW-e.area.W, 0))
}

// visibleRange is the pages in view.
func (e *pdfEditor) visibleRange() (first, last int) {
	if len(e.rects) == 0 {
		return 0, -1
	}
	top, bot := e.scrollY, e.scrollY+e.area.H
	first = sort.Search(len(e.rects), func(i int) bool { return e.rects[i].Bottom() >= top })
	last = sort.Search(len(e.rects), func(i int) bool { return e.rects[i].Y > bot }) - 1
	return min(first, len(e.rects)-1), max(last, first)
}

// currentPage is the page that fills most of the view.
func (e *pdfEditor) currentPage() int {
	first, last := e.visibleRange()
	if last < 0 {
		return 0
	}
	best, most := first, float32(-1)
	for i := first; i <= last && i < len(e.rects); i++ {
		r := e.rects[i]
		seen := min(r.Bottom(), e.scrollY+e.area.H) - max(r.Y, e.scrollY)
		if seen > most {
			best, most = i, seen
		}
	}
	return best
}

// goTo scrolls to a page, top being a height in user space when hasY.
func (e *pdfEditor) goTo(page int, top float64, hasY bool) {
	if page < 0 || page >= len(e.rects) {
		return
	}
	y := e.rects[page].Y - e.a.u.px(pdfPageGap)
	if hasY {
		_, sy := e.toScreen(page, 0, top)
		y = sy - e.area.Y + e.scrollY - e.a.u.px(20)
	}
	e.scrollY = y
	e.clampScroll()
}

// matrix maps a page's user space to the window.
func (e *pdfEditor) matrix(i int) pdf.Matrix {
	m := e.doc.ViewMatrix(e.pages[i], e.pxPerPoint(), e.view)
	r := e.rects[i]
	m[4] += float64(e.area.X + r.X - e.scrollX)
	m[5] += float64(e.area.Y + r.Y - e.scrollY)
	return m
}

func (e *pdfEditor) toScreen(i int, x, y float64) (float32, float32) {
	a, b := e.matrix(i).Apply(x, y)
	return float32(a), float32(b)
}

func (e *pdfEditor) fromScreen(i int, x, y float32) (float64, float64) {
	inv, ok := e.matrix(i).Invert()
	if !ok {
		return 0, 0
	}
	return inv.Apply(float64(x), float64(y))
}

// screenBox is the window rectangle around a user space rectangle.
func (e *pdfEditor) screenBox(i int, r pdf.Rect) gfx.Rect {
	b := e.matrix(i).BoxOf(r)
	return gfx.Rect{X: float32(b.X0), Y: float32(b.Y0), W: float32(b.W()), H: float32(b.H())}
}

// pageRect is where a page is in the window now.
func (e *pdfEditor) pageRect(i int) gfx.Rect {
	r := e.rects[i]
	r.X += e.area.X - e.scrollX
	r.Y += e.area.Y - e.scrollY
	return r
}

// pageAt is the page under a window position, -1 for none.
func (e *pdfEditor) pageAt(x, y float32) int {
	first, last := e.visibleRange()
	for i := first; i <= last && i < len(e.rects) && i >= 0; i++ {
		if e.pageRect(i).Contains(x, y) {
			return i
		}
	}
	return -1
}

// ensure asks for a page's picture and returns the best one there is.
func (e *pdfEditor) ensure(i int, thumb bool, scale float64) *pdfBitmap {
	p := e.pages[i]
	key := pdfBitmapKey{p.Ref.Num, thumb}
	bm := e.bitmaps[key]
	rev := e.pageRev(i)
	var region image.Rectangle
	if !thumb {
		w, h := e.doc.ViewSize(p, e.view)
		if w*h*scale*scale > pdfWholePage {
			// Just what shows, with room to scroll into.
			r := e.pageRect(i)
			margin := e.area.H / 2
			x0, y0 := int(e.area.X-r.X-margin), int(e.area.Y-r.Y-margin)
			x1, y1 := int(e.area.Right()-r.X+margin), int(e.area.Bottom()-r.Y+margin)
			region = image.Rect(x0, y0, x1, y1).Intersect(image.Rect(0, 0, int(r.W), int(r.H)))
		}
	}
	fresh := bm != nil && bm.rev == rev && bm.view == e.view && math.Abs(bm.scale-scale) < 1e-6
	if fresh && !region.Empty() {
		// It must still hold what is in view.
		r := e.pageRect(i)
		view := image.Rect(int(e.area.X-r.X), int(e.area.Y-r.Y), int(e.area.Right()-r.X), int(e.area.Bottom()-r.Y)).Intersect(image.Rect(0, 0, int(r.W), int(r.H)))
		fresh = view.In(bm.img.Rect)
	}
	if bm != nil {
		bm.used = e.frame
	}
	if fresh {
		return bm
	}
	if j := e.jobs[key]; j != nil {
		if j.rev == rev && j.view == e.view && math.Abs(j.scale-scale) < 1e-6 && (region.Empty() || region.In(j.region.Inset(-1)) || region == j.region) {
			return bm
		}
		j.cancel()
		return bm // a new one starts when this has stopped
	}
	running := 0
	for k := range e.jobs {
		if k.thumb == thumb {
			running++
		}
	}
	if running >= pdfJobs {
		return bm
	}
	e.startJob(key, i, scale, region, rev)
	return bm
}

func (e *pdfEditor) startJob(key pdfBitmapKey, i int, scale float64, region image.Rectangle, rev int) {
	a := e.a
	page := e.pages[i]
	cache := e.cache
	view := e.view
	cctx, cancel := context.WithTimeout(a.life, pdfPageTimeout)
	j := &pdfJob{cancel: cancel, scale: scale, view: view, rev: rev, region: region, index: i}
	e.jobs[key] = j
	a.background(func(context.Context) {
		defer cancel()
		res := cache.Page(cctx, page, render.Options{Scale: scale, View: view, Region: region, NoAnnots: false})
		a.post(func() {
			if e.jobs[key] == j {
				delete(e.jobs, key)
			}
			if errors.Is(res.Err, context.Canceled) {
				a.requestFrame()
				return
			}
			e.bitmaps[key] = &pdfBitmap{img: res.Img, scale: scale, view: view, rev: rev, used: e.frame}
			if !key.thumb {
				e.texts[key.num] = &pdfText{t: res.Text, rev: rev}
				e.trimBitmaps()
			}
			a.requestFrame()
		})
	})
}

// trimBitmaps lets go of the pictures seen longest ago.
func (e *pdfEditor) trimBitmaps() {
	total := 0
	type item struct {
		k    pdfBitmapKey
		used int
		size int
	}
	var list []item
	for k, b := range e.bitmaps {
		size := len(b.img.Pix)
		total += size
		list = append(list, item{k, b.used, size})
	}
	if total <= pdfBitmapBudget {
		return
	}
	sort.Slice(list, func(i, j int) bool { return list[i].used < list[j].used })
	for _, it := range list {
		if total <= pdfBitmapBudget || it.used >= e.frame {
			break
		}
		delete(e.bitmaps, it.k)
		total -= it.size
	}
}

// dropFarJobs stops drawing pages that scrolled away.
func (e *pdfEditor) dropFarJobs(first, last int) {
	for k, j := range e.jobs {
		if !k.thumb && (j.index < first-1 || j.index > last+1) {
			j.cancel()
		}
	}
}

// textOf is a page's text once its picture has been drawn, nil before.
func (e *pdfEditor) textOf(i int) *render.Text {
	if i < 0 || i >= len(e.pages) {
		return nil
	}
	if t := e.texts[e.pages[i].Ref.Num]; t != nil {
		return t.t
	}
	return nil
}

func (e *pdfEditor) clearSelection() { e.selPage, e.selFrom, e.selTo = -1, 0, 0 }

func (e *pdfEditor) selectionText() string {
	if t := e.textOf(e.selPage); t != nil {
		return strings.TrimRight(t.String(e.selFrom, e.selTo), "\n ")
	}
	return ""
}

// draw is the whole tab.
func (e *pdfEditor) draw(r gfx.Rect) {
	u := &e.a.u
	e.frame++
	u.c.Fill(r, colPanel)
	if e.locked {
		e.drawLocked(r)
		return
	}
	e.sync()
	bar := gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: u.px(40)}
	y := bar.Bottom()
	if e.signed && !e.banner {
		y += u.px(30)
	}
	body := gfx.Rect{X: r.X, Y: y, W: r.W, H: r.Bottom() - y}
	e.sideW = 0
	if e.sideTab != 0 {
		e.sideW = u.px(186)
	}
	e.area = gfx.Rect{X: body.X + e.sideW, Y: body.Y, W: body.W - e.sideW, H: body.H}
	e.layout()
	e.clampScroll()
	if u.focus == "" {
		u.focus = "office/pdf"
	}
	if u.focus == "office/pdf" {
		u.focusDrawn = true
	}
	e.overs, e.oversNext = e.oversNext, e.overs[:0]
	e.drawPages()
	if e.sideTab != 0 {
		e.drawSide(gfx.Rect{X: body.X, Y: body.Y, W: e.sideW, H: body.H})
	}
	e.drawBar(bar)
	if e.signed && !e.banner {
		e.drawSignedBanner(gfx.Rect{X: r.X, Y: y - u.px(30), W: r.W, H: u.px(30)})
	}
	if e.optionsShown() {
		// Over the pages, so that choosing something does not move what was just clicked.
		e.drawOptions(gfx.Rect{X: e.area.X, Y: e.area.Y, W: e.area.W, H: u.px(34)})
	}
	if e.searchOpen {
		e.drawSearch()
	}
	e.drawOverlays()
	if !u.blocked && u.focus == "office/pdf" {
		e.keys()
	}
}

// drawPages draws the column of pages and takes what is done to them.
func (e *pdfEditor) drawPages() {
	u := &e.a.u
	area := e.area
	u.c.Push(area)
	defer u.c.Pop()
	u.c.Fill(area, colPanel)
	first, last := e.visibleRange()
	scale := e.pxPerPoint()
	e.dropFarJobs(first, last)
	for i := first; i <= last && i < len(e.rects); i++ {
		r := e.pageRect(i)
		u.c.Fill(gfx.Rect{X: r.X - 1, Y: r.Y - 1, W: r.W + 2, H: r.H + 2}, colBorderHi)
		u.c.Fill(r, colWhite)
		bm := e.ensure(i, false, scale)
		if bm == nil {
			if tb := e.bitmaps[pdfBitmapKey{e.pages[i].Ref.Num, true}]; tb != nil {
				u.c.Image(tb.img, r) // something to look at while the real one is drawn
			}
		} else {
			e.blit(bm, r)
		}
		e.drawPageMarks(i, r)
	}
	if first >= 0 && first+1 < len(e.rects) {
		e.ensure(min(last+1, len(e.pages)-1), false, scale) // the next page is ready when it is reached
	}
	e.pointer(area)
	e.drawScrollbars(area)
}

// blit puts a picture on the screen, stretched when it is of another zoom.
func (e *pdfEditor) blit(bm *pdfBitmap, r gfx.Rect) {
	u := &e.a.u
	b := bm.img.Rect
	k := float32(e.pxPerPoint() / bm.scale)
	if math.Abs(float64(k-1)) < 1e-4 {
		u.c.Blit(bm.img, int(r.X)+b.Min.X, int(r.Y)+b.Min.Y, true)
		return
	}
	u.c.Image(bm.img, gfx.Rect{X: r.X + float32(b.Min.X)*k, Y: r.Y + float32(b.Min.Y)*k, W: float32(b.Dx()) * k, H: float32(b.Dy()) * k})
}

func (e *pdfEditor) drawScrollbars(area gfx.Rect) {
	u := &e.a.u
	if e.contentH > area.H {
		t := gfx.Rect{X: area.Right() - u.px(10), Y: area.Y, W: u.px(10), H: area.H}
		h := max(area.H*area.H/e.contentH, u.px(28))
		y := area.Y + (area.H-h)*e.scrollY/(e.contentH-area.H)
		col := gfx.Alpha(colDim, 0.35)
		if u.hover("office/pdf/vbar", t) || u.active == "office/pdf/vbar" {
			col = gfx.Alpha(colDim, 0.7)
		}
		u.c.RoundRect(gfx.Rect{X: t.X + u.px(2), Y: y, W: u.px(6), H: h}, u.px(3), col)
		u.press("office/pdf/vbar", t)
		if u.dragging("office/pdf/vbar") {
			e.scrollY = (u.my - area.Y - h/2) / (area.H - h) * (e.contentH - area.H)
			e.clampScroll()
		}
		u.letGo("office/pdf/vbar")
	}
	if e.contentW > area.W+1 {
		t := gfx.Rect{X: area.X, Y: area.Bottom() - u.px(10), W: area.W, H: u.px(10)}
		w := max(area.W*area.W/e.contentW, u.px(28))
		x := area.X + (area.W-w)*e.scrollX/(e.contentW-area.W)
		col := gfx.Alpha(colDim, 0.35)
		if u.hover("office/pdf/hbar", t) || u.active == "office/pdf/hbar" {
			col = gfx.Alpha(colDim, 0.7)
		}
		u.c.RoundRect(gfx.Rect{X: x, Y: t.Y + u.px(2), W: w, H: u.px(6)}, u.px(3), col)
		u.press("office/pdf/hbar", t)
		if u.dragging("office/pdf/hbar") {
			e.scrollX = (u.mx - area.X - w/2) / (area.W - w) * (e.contentW - area.W)
			e.clampScroll()
		}
		u.letGo("office/pdf/hbar")
	}
}

// keys are the view's own: the shell has taken saving and history already.
func (e *pdfEditor) keys() {
	u := &e.a.u
	line := u.px(48)
	for _, k := range u.keys {
		switch {
		case k.Ctrl && k.Code == 'F':
			e.searchOpen = true
			u.setFocus("office/pdf/search")
		case k.Ctrl && k.Code == 'A':
			e.selectAll()
		case k.Ctrl && k.Code == 'C':
			if s := e.selectionText(); s != "" {
				u.setClipText(s)
			}
		case k.Ctrl && (k.Code == '=' || k.Code == 0xBB):
			e.setZoom(e.zoom*1.25, e.area.X+e.area.W/2, e.area.Y+e.area.H/2)
		case k.Ctrl && (k.Code == '-' || k.Code == 0xBD):
			e.setZoom(e.zoom/1.25, e.area.X+e.area.W/2, e.area.Y+e.area.H/2)
		case k.Ctrl && k.Code == '0':
			e.fit = pdfFitWidth
		case k.Code == keyUp:
			e.scrollY -= line
		case k.Code == keyDown:
			e.scrollY += line
		case k.Code == keyLeft:
			e.scrollX -= line
		case k.Code == keyRight:
			e.scrollX += line
		case k.Code == keyPageUp:
			e.scrollY -= e.area.H * 0.9
		case k.Code == keyPageDn || k.Code == keySpace:
			e.scrollY += e.area.H * 0.9
		case k.Code == keyHome:
			e.scrollY = 0
		case k.Code == keyEnd:
			e.scrollY = e.contentH
		case k.Code == keyDelete || k.Code == keyBack:
			e.deleteChosen()
		case k.Code == keyEscape:
			e.escape()
		}
	}
	e.clampScroll()
}

func (e *pdfEditor) escape() {
	switch {
	case e.searchOpen:
		e.closeSearch()
	case e.sel != nil:
		e.sel = nil
	case e.selPage >= 0:
		e.clearSelection()
	default:
		e.tool = pdfToolSelect
	}
}

func (e *pdfEditor) selectAll() {
	page := e.currentPage()
	if t := e.textOf(page); t != nil {
		e.selPage, e.selFrom, e.selTo = page, 0, len(t.Chars)
	}
}

// drawLocked asks for the password.
func (e *pdfEditor) drawLocked(r gfx.Rect) {
	a := e.a
	u := &a.u
	w := min(u.px(380), r.W-u.px(32))
	x, y := r.X+(r.W-w)/2, r.Y+r.H/2-u.px(90)
	u.icon(icPDFLock, gfx.Rect{X: x, Y: y, W: w, H: u.px(36)}, 30, colDim)
	y += u.px(44)
	u.label(u.bold(15), gfx.Rect{X: x, Y: y, W: w, H: u.px(24)}, u.t("This PDF is protected with a password"), colText, 0.5)
	y += u.px(36)
	box := gfx.Rect{X: x, Y: y, W: w, H: u.px(34)}
	id := "office/pdf/password"
	if u.focus == "" {
		u.setFocus(id)
	}
	changed, _ := u.secretBox(id, box, &e.password, u.t("Password"))
	if changed {
		e.pwWrong = false
	}
	y += u.px(44)
	if e.pwWrong {
		u.label(u.face(12.5), gfx.Rect{X: x, Y: y, W: w, H: u.px(20)}, u.t("That is not the password."), colDangerText, 0.5)
		y += u.px(26)
	}
	enter := false
	if u.focus == id {
		for _, k := range u.keys {
			enter = enter || k.Code == keyEnter
		}
	}
	bw := u.px(120)
	if u.button("office/pdf/unlock", gfx.Rect{X: x + (w-bw)/2, Y: y, W: bw, H: u.px(32)}, "", u.t("Open"), primary, e.password != "") || enter {
		e.unlock()
	}
}

func (e *pdfEditor) unlock() {
	doc, err := pdf.Open(e.data, e.password)
	if errors.Is(err, pdf.ErrPassword) {
		e.pwWrong = true
		return
	}
	if err == nil {
		err = e.ready(doc)
	}
	if err != nil {
		e.a.toast(e.a.officeError(e.od.path, err), true)
	}
}
