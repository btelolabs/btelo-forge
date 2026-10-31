package ui

import (
	"bytes"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"path/filepath"
	"strings"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/pdf"
	"github.com/btelolabs/btelo-forge/internal/office/pdf/render"
)

// What the pointer does to a page, by tool.

type pdfTool int

const (
	pdfToolSelect pdfTool = iota
	pdfToolHand
	pdfToolHighlight
	pdfToolUnderline
	pdfToolStrike
	pdfToolInk
	pdfToolRect
	pdfToolEllipse
	pdfToolLine
	pdfToolArrow
	pdfToolNote
	pdfToolFreeText
	pdfToolText
	pdfToolCover
	pdfToolEditText
)

type pdfDragKind int

const (
	pdfDragNone pdfDragKind = iota
	pdfDragText
	pdfDragPan
	pdfDragInk
	pdfDragShape
	pdfDragMove
	pdfDragResize
	pdfDragPendingMove
	pdfDragPendingSize
)

// pdfDrag is a drag on a page.
type pdfDrag struct {
	kind         pdfDragKind
	page         int
	x0, y0       float32 // where it began, in the window
	sx, sy       float32 // the scroll then
	handle       int
	link         *pdf.Link
	moved        bool
	startedOnSel bool
}

// pdfSel is the chosen annotation.
type pdfSel struct {
	ref  pdf.Ref
	page int
	kind pdf.Name
	rect pdf.Rect
}

// pdfPendingKind is what is being placed before it is written.
type pdfPendingKind int

const (
	pdfPendText     pdfPendingKind = iota // text added to the page's content
	pdfPendFreeText                       // a free text annotation
	pdfPendNote                           // a sticky note's words
	pdfPendReplace                        // new words for a line of text
	pdfPendImage                          // a picture
	pdfPendSignText                       // a typed signature
)

type pdfPending struct {
	kind  pdfPendingKind
	page  int
	rect  pdf.Rect // in user space
	text  string
	img   image.Image
	jpeg  []byte
	style pdf.TextStyle
	cover pdf.Rect // what a replacement covers
	bg    [3]float64
	base  [2]float64 // a replacement's baseline
	ratio float64    // keeps a picture's shape
	ref   pdf.Ref    // a note being written
}

// pointer takes what the mouse does in the pages' area.
func (e *pdfEditor) pointer(area gfx.Rect) {
	u := &e.a.u
	id := "office/pdf/page"
	if u.wheel != 0 && u.hover(id, area) {
		if u.ctrl {
			e.setZoom(e.zoom*math.Pow(1.0015, float64(u.wheel)), u.mx, u.my)
		} else if u.shift {
			e.scrollX -= u.wheel * u.px(1)
		} else {
			e.scrollY -= u.wheel * u.px(1)
		}
		e.clampScroll()
	}
	over := e.pageAt(u.mx, u.my)
	if u.rightAt != nil && area.Contains(u.rightAt.X, u.rightAt.Y) && !u.blocked {
		e.contextMenu(over)
	}
	if !u.hover(id, area) && u.active != id {
		return
	}
	if e.sign != nil || e.saveAs != nil {
		return // these hold the window until they are closed
	}
	for _, b := range e.overs {
		if b.Contains(u.mx, u.my) && u.active != id {
			return
		}
	}
	e.hoverCursor(over, id, area)
	if u.press(id, area) {
		u.focus = "office/pdf"
		e.commitPending()
		e.commitField()
		e.begin(over)
	}
	if u.dragging(id) {
		e.dragOn()
	}
	if u.letGo(id) {
		e.release()
	}
}

func (e *pdfEditor) hoverCursor(over int, id string, area gfx.Rect) {
	u := &e.a.u
	if e.drag.kind == pdfDragPan {
		u.cursor = CursorHand
		return
	}
	if over < 0 {
		return
	}
	x, y := e.fromScreen(over, u.mx, u.my)
	switch e.tool {
	case pdfToolHand:
		u.cursor = CursorHand
	case pdfToolSelect:
		if l := e.linkAt(over, x, y); l != nil {
			u.cursor = CursorHand
			hint := u.tf("Go to page %d", l.Page+1)
			if l.URI != "" {
				hint = u.tf("Open %s", l.URI)
			}
			u.hint(id+"/link", hint, gfx.Rect{X: u.mx - u.px(1), Y: u.my, W: u.px(2), H: u.px(2)})
			return
		}
		if a := e.annotAt(over, x, y); a != nil {
			u.cursor = CursorMove
			if a.Contents != "" {
				f := u.face(12)
				u.hint(id+"/annot", f.Ellipsize(a.Contents, u.px(420)), gfx.Rect{X: u.mx - u.px(1), Y: u.my, W: u.px(2), H: u.px(2)})
			}
			return
		}
		if t := e.textOf(over); t != nil && t.InText(x, y) {
			u.cursor = CursorText
		}
	case pdfToolHighlight, pdfToolUnderline, pdfToolStrike:
		u.cursor = CursorText
	case pdfToolEditText:
		if t := e.textOf(over); t != nil {
			if l := t.LineAt(x, y); l >= 0 {
				u.cursor = CursorText
				u.c.Outline(e.screenBox(over, t.Lines[l].Box), max(1, u.px(1)), colAccent)
			}
		}
	default:
		u.cursor = CursorCross
	}
}

func (e *pdfEditor) linkAt(page int, x, y float64) *pdf.Link {
	for _, l := range e.doc.Links(page) {
		if l.Rect.Contains(x, y) {
			return &l
		}
	}
	return nil
}

func (e *pdfEditor) begin(page int) {
	u := &e.a.u
	e.drag = pdfDrag{kind: pdfDragNone, page: page, x0: u.mx, y0: u.my, sx: e.scrollX, sy: e.scrollY}
	if e.tool == pdfToolHand || page < 0 {
		e.drag.kind = pdfDragPan
		return
	}
	x, y := e.fromScreen(page, u.mx, u.my)
	switch e.tool {
	case pdfToolSelect:
		if e.sel != nil && e.sel.page == page {
			if h := e.handleAt(u.mx, u.my); h >= 0 {
				e.drag.kind, e.drag.handle = pdfDragResize, h
				return
			}
		}
		if f, w := e.fieldAt(page, x, y); f != nil {
			e.pressField(f, w)
			return
		}
		if l := e.linkAt(page, x, y); l != nil {
			e.drag.link = l
			return
		}
		if a := e.annotAt(page, x, y); a != nil {
			e.sel = &pdfSel{ref: a.Ref, page: page, kind: a.Kind, rect: a.Rect}
			e.clearSelection()
			if u.double && (a.Kind == "Text" || a.Kind == "FreeText") {
				e.openWords(a)
				return
			}
			e.drag.kind = pdfDragMove
			return
		}
		e.sel = nil
		e.startText(page, x, y)
	case pdfToolHighlight, pdfToolUnderline, pdfToolStrike:
		e.startText(page, x, y)
	case pdfToolInk:
		e.drag.kind = pdfDragInk
		e.ink = append(e.ink[:0], [2]float32{u.mx, u.my})
	case pdfToolRect, pdfToolEllipse, pdfToolLine, pdfToolArrow, pdfToolCover, pdfToolFreeText:
		e.drag.kind = pdfDragShape
	case pdfToolNote:
		e.placeNote(page, x, y)
	case pdfToolText:
		e.startPendingText(page, x, y, pdfPendText)
	case pdfToolEditText:
		e.startReplace(page, x, y)
	}
}

func (e *pdfEditor) startText(page int, x, y float64) {
	t := e.textOf(page)
	if t == nil {
		return
	}
	i, ok := t.Hit(x, y)
	if !ok {
		e.clearSelection()
		return
	}
	if e.a.u.double {
		// A double click takes the word.
		e.selPage = page
		e.selFrom, e.selTo = pdfWordAround(t, i)
		return
	}
	e.selPage, e.selFrom, e.selTo = page, i, i
	e.drag.kind = pdfDragText
}

func pdfWordAround(t *render.Text, i int) (int, int) {
	i = min(i, len(t.Chars)-1)
	space := func(k int) bool { return strings.TrimSpace(t.Chars[k].Text) == "" }
	a, b := i, i
	for a > 0 && !space(a-1) {
		a--
	}
	for b < len(t.Chars) && !space(b) {
		b++
	}
	return a, b
}

func (e *pdfEditor) dragOn() {
	u := &e.a.u
	d := &e.drag
	switch d.kind {
	case pdfDragPan:
		e.scrollX, e.scrollY = d.sx-(u.mx-d.x0), d.sy-(u.my-d.y0)
		e.clampScroll()
	case pdfDragText:
		page := d.page
		x, y := e.fromScreen(page, u.mx, u.my)
		if t := e.textOf(page); t != nil {
			if i, ok := t.Hit(x, y); ok {
				e.selTo = i
			}
		}
		e.autoScroll()
	case pdfDragInk:
		if p := e.ink[len(e.ink)-1]; math.Hypot(float64(p[0]-u.mx), float64(p[1]-u.my)) >= 1.5 {
			e.ink = append(e.ink, [2]float32{u.mx, u.my})
		}
	}
	if u.drag.moved {
		d.moved = true
	}
}

// autoScroll moves the view while a drag presses against its edge.
func (e *pdfEditor) autoScroll() {
	u := &e.a.u
	switch {
	case u.my < e.area.Y+u.px(16):
		e.scrollY -= u.px(14)
	case u.my > e.area.Bottom()-u.px(16):
		e.scrollY += u.px(14)
	}
	e.clampScroll()
}

func (e *pdfEditor) release() {
	u := &e.a.u
	d := e.drag
	e.drag.kind = pdfDragNone
	switch d.kind {
	case pdfDragNone:
		if d.link != nil && !d.moved {
			if d.link.URI != "" {
				if err := u.host.LaunchURL(d.link.URI); err != nil {
					e.a.toast(e.a.describe(err), true)
				}
			} else {
				e.goTo(d.link.Page, d.link.Top, d.link.HasY)
			}
		}
	case pdfDragText:
		if e.selFrom != e.selTo {
			switch e.tool {
			case pdfToolHighlight:
				e.markSelection("Highlight")
			case pdfToolUnderline:
				e.markSelection("Underline")
			case pdfToolStrike:
				e.markSelection("StrikeOut")
			}
		}
	case pdfDragInk:
		e.finishInk(d.page)
	case pdfDragShape:
		e.finishShape(d)
	case pdfDragMove:
		if d.moved && e.sel != nil {
			k := 1 / e.pxPerPoint()
			dx, dy := e.deviceToUser(d.page, float64(u.mx-d.x0), float64(u.my-d.y0), k)
			if e.edited(e.doc.MoveAnnot(e.sel.ref, dx, dy), d.page) {
				e.reselect()
			}
		}
	case pdfDragResize:
		if d.moved && e.sel != nil {
			to := e.resized(d)
			if to.W() >= 2 && to.H() >= 2 && e.edited(e.doc.ResizeAnnot(e.sel.ref, to), d.page) {
				e.reselect()
			}
		}
	}
}

// deviceToUser turns a movement on the screen into one in user space.
func (e *pdfEditor) deviceToUser(page int, dx, dy, k float64) (float64, float64) {
	m := e.doc.ViewMatrix(e.pages[page], 1, e.view)
	m[4], m[5] = 0, 0
	inv, ok := m.Invert()
	if !ok {
		return 0, 0
	}
	return inv.Apply(dx*k, dy*k)
}

func (e *pdfEditor) reselect() {
	if e.sel == nil {
		return
	}
	for _, a := range e.doc.Annots(e.sel.page) {
		if a.Ref == e.sel.ref {
			e.sel.rect = a.Rect
			return
		}
	}
	e.sel = nil
}

// resized is the rectangle a handle drag leads to.
func (e *pdfEditor) resized(d pdfDrag) pdf.Rect {
	u := &e.a.u
	x, y := e.fromScreen(d.page, u.mx, u.my)
	r := e.sel.rect
	switch d.handle {
	case 0:
		r.X0, r.Y1 = x, y
	case 1:
		r.X1, r.Y1 = x, y
	case 2:
		r.X1, r.Y0 = x, y
	case 3:
		r.X0, r.Y0 = x, y
	}
	return r.Normal()
}

// handles are the corners of the chosen annotation in the window.
func (e *pdfEditor) handles() [4]gfx.Rect {
	u := &e.a.u
	s := u.px(9)
	var out [4]gfx.Rect
	r := e.sel.rect
	for i, c := range [][2]float64{{r.X0, r.Y1}, {r.X1, r.Y1}, {r.X1, r.Y0}, {r.X0, r.Y0}} {
		x, y := e.toScreen(e.sel.page, c[0], c[1])
		out[i] = gfx.Rect{X: x - s/2, Y: y - s/2, W: s, H: s}
	}
	return out
}

func (e *pdfEditor) handleAt(x, y float32) int {
	for i, h := range e.handles() {
		if h.Inset(-e.a.u.px(3)).Contains(x, y) {
			return i
		}
	}
	return -1
}

func (e *pdfEditor) annotAt(page int, x, y float64) *pdf.Annot {
	list := e.doc.Annots(page)
	for i := len(list) - 1; i >= 0; i-- {
		a := &list[i]
		if a.Kind == "Popup" || a.Kind == "Link" || a.Kind == "Widget" || a.Ref.Num == 0 {
			continue
		}
		if !a.Rect.Contains(x, y) {
			continue
		}
		if a.Kind == "Ink" || a.Kind == "Line" {
			// A stroke's box is mostly empty; take it only near the line.
			if !e.nearStroke(a, x, y) {
				continue
			}
		}
		return a
	}
	return nil
}

func (e *pdfEditor) nearStroke(a *pdf.Annot, x, y float64) bool {
	near := 6 / e.pxPerPoint() * 2
	near = max(near, 4)
	seg := func(p []float64) bool {
		for i := 0; i+3 < len(p); i += 2 {
			if distToSegment(x, y, p[i], p[i+1], p[i+2], p[i+3]) <= near {
				return true
			}
		}
		return false
	}
	d := e.doc
	if a.Kind == "Line" {
		return seg(d.Floats(a.Dict["L"]))
	}
	for _, s := range d.Array(a.Dict["InkList"]) {
		if seg(d.Floats(s)) {
			return true
		}
	}
	return false
}

func distToSegment(x, y, x0, y0, x1, y1 float64) float64 {
	dx, dy := x1-x0, y1-y0
	t := 0.0
	if l := dx*dx + dy*dy; l > 0 {
		t = min(max(((x-x0)*dx+(y-y0)*dy)/l, 0), 1)
	}
	return math.Hypot(x-x0-t*dx, y-y0-t*dy)
}

func (e *pdfEditor) markSelection(kind pdf.Name) {
	t := e.textOf(e.selPage)
	if t == nil {
		return
	}
	quads := t.Quads(e.selFrom, e.selTo)
	if len(quads) == 0 {
		return
	}
	spec := pdf.AnnotSpec{Kind: kind, Quads: quads, Color: e.color}
	page := e.selPage
	if _, err := e.doc.AddAnnot(page, spec); e.edited(err, page) {
		e.clearSelection()
	}
}

func (e *pdfEditor) finishInk(page int) {
	if len(e.ink) < 2 {
		return
	}
	pts := make([]float64, 0, len(e.ink)*2)
	for _, p := range e.ink {
		x, y := e.fromScreen(page, p[0], p[1])
		pts = append(pts, x, y)
	}
	e.ink = e.ink[:0]
	_, err := e.doc.AddAnnot(page, pdf.AnnotSpec{Kind: "Ink", Ink: [][]float64{pts}, Color: e.color, Width: e.width})
	e.edited(err, page)
}

func (e *pdfEditor) finishShape(d pdfDrag) {
	u := &e.a.u
	x0, y0 := e.fromScreen(d.page, d.x0, d.y0)
	x1, y1 := e.fromScreen(d.page, u.mx, u.my)
	r := pdf.Rect{X0: x0, Y0: y0, X1: x1, Y1: y1}.Normal()
	big := r.W() >= 3 || r.H() >= 3
	switch e.tool {
	case pdfToolRect, pdfToolEllipse:
		if !big {
			return
		}
		kind := pdf.Name("Square")
		if e.tool == pdfToolEllipse {
			kind = "Circle"
		}
		_, err := e.doc.AddAnnot(d.page, pdf.AnnotSpec{Kind: kind, Rect: r, Color: e.color, Width: e.width})
		e.edited(err, d.page)
	case pdfToolLine, pdfToolArrow:
		if !big {
			return
		}
		_, err := e.doc.AddAnnot(d.page, pdf.AnnotSpec{Kind: "Line", Line: [4]float64{x0, y0, x1, y1}, Arrow: e.tool == pdfToolArrow, Color: e.color, Width: e.width})
		e.edited(err, d.page)
	case pdfToolCover:
		if !big {
			return
		}
		e.edited(e.doc.CoverRect(d.page, r, [3]float64{1, 1, 1}), d.page)
	case pdfToolFreeText:
		if !big {
			r = pdf.Rect{X0: x0, Y0: y0 - 40, X1: x0 + 200, Y1: y0}
		}
		e.pending = &pdfPending{kind: pdfPendFreeText, page: d.page, rect: r, style: e.textStyle}
		e.focusPending()
	}
}

func (e *pdfEditor) placeNote(page int, x, y float64) {
	r := pdf.Rect{X0: x, Y0: y - 18, X1: x + 18, Y1: y}
	ref, err := e.doc.AddAnnot(page, pdf.AnnotSpec{Kind: "Text", Rect: r, Color: e.color})
	if !e.edited(err, page) {
		return
	}
	e.sel = &pdfSel{ref: ref, page: page, kind: "Text", rect: r}
	e.pending = &pdfPending{kind: pdfPendNote, page: page, rect: pdf.Rect{X0: x + 22, Y0: y - 80, X1: x + 200, Y1: y}, ref: ref}
	e.focusPending()
	e.tool = pdfToolSelect
}

// openWords opens a note or text box for its words to be changed.
func (e *pdfEditor) openWords(a *pdf.Annot) {
	box := a.Rect
	if a.Kind == "Text" {
		box = pdf.Rect{X0: a.Rect.X1 + 4, Y0: a.Rect.Y1 - 80, X1: a.Rect.X1 + 200, Y1: a.Rect.Y1}
	}
	e.pending = &pdfPending{kind: pdfPendNote, page: a.Page, rect: box, ref: a.Ref, text: a.Contents}
	e.focusPending()
}

func (e *pdfEditor) startPendingText(page int, x, y float64, kind pdfPendingKind) {
	s := e.textStyle
	e.pending = &pdfPending{kind: kind, page: page, style: s, rect: pdf.Rect{X0: x, Y0: y - s.Size*4, X1: x + 260, Y1: y}}
	e.focusPending()
}

// startReplace opens a line of text for new words.
func (e *pdfEditor) startReplace(page int, x, y float64) {
	t := e.textOf(page)
	if t == nil {
		return
	}
	i := t.LineAt(x, y)
	if i < 0 {
		return
	}
	l := t.Lines[i]
	c := t.Chars[l.First]
	family := pdfClosestFamily(e, c.Font)
	st := pdf.TextStyle{Family: family, Bold: c.Bold, Italic: c.Italic, Size: max(c.Size, 4),
		Color: [3]float64{float64(c.RGB[0]) / 255, float64(c.RGB[1]) / 255, float64(c.RGB[2]) / 255}}
	box := l.Box
	e.pending = &pdfPending{kind: pdfPendReplace, page: page, rect: pdf.Rect{X0: box.X0, Y0: box.Y0 - st.Size*0.4, X1: max(box.X1+st.Size*2, box.X0+120), Y1: box.Y1 + st.Size*0.4},
		text: t.LineText(i), style: st, cover: box, bg: e.backgroundAround(page, box), base: c.Base}
	e.focusPending()
}

// backgroundAround is the colour just outside a box, from the page's picture.
func (e *pdfEditor) backgroundAround(page int, box pdf.Rect) [3]float64 {
	bm := e.bitmaps[pdfBitmapKey{e.pages[page].Ref.Num, false}]
	if bm == nil {
		return [3]float64{1, 1, 1}
	}
	m := e.doc.ViewMatrix(e.pages[page], bm.scale, bm.view)
	x, y := m.Apply(box.X0-1, (box.Y0+box.Y1)/2)
	c := bm.img.RGBAAt(int(x), int(y))
	if !(image.Point{int(x), int(y)}).In(bm.img.Rect) {
		return [3]float64{1, 1, 1}
	}
	return [3]float64{float64(c.R) / 255, float64(c.G) / 255, float64(c.B) / 255}
}

// pdfClosestFamily finds the family on this computer nearest a font's name.
func pdfClosestFamily(e *pdfEditor, name string) string {
	low := strings.ToLower(name)
	for _, t := range [][2]string{{"times", "times new roman"}, {"georgia", "georgia"}, {"courier", "courier new"}, {"consol", "consolas"},
		{"cambria", "cambria"}, {"calibri", "calibri"}, {"verdana", "verdana"}, {"tahoma", "tahoma"}, {"arial", "arial"}, {"helvetica", "arial"},
		{"simsun", "宋体"}, {"song", "宋体"}, {"simhei", "黑体"}, {"hei", "黑体"}, {"yahei", "微软雅黑"}, {"kai", "楷体"}, {"fang", "仿宋"}, {"deng", "等线"}} {
		if strings.Contains(low, t[0]) {
			return t[1]
		}
	}
	return "arial"
}

// focusPending gives the keyboard to a new box. The box's id is shared, so the words of the last one must go.
func (e *pdfEditor) focusPending() {
	u := &e.a.u
	u.editOwner = ""
	u.setFocus("office/pdf/pending")
}

// commitPending writes what is being placed.
func (e *pdfEditor) commitPending() {
	p := e.pending
	if p == nil {
		return
	}
	e.pending = nil
	u := &e.a.u
	if u.focus == "office/pdf/pending" {
		u.focus = "office/pdf"
	}
	text := strings.TrimRight(p.text, " \n")
	switch p.kind {
	case pdfPendText, pdfPendSignText:
		if text != "" {
			e.edited(e.doc.AddText(p.page, text, p.rect.X0, p.rect.Y1-p.style.Size, p.rect.W(), p.style), p.page)
		}
	case pdfPendFreeText:
		if text != "" {
			_, err := e.doc.AddAnnot(p.page, pdf.AnnotSpec{Kind: "FreeText", Rect: p.rect, Contents: text, Color: p.style.Color, Size: p.style.Size})
			e.edited(err, p.page)
		}
	case pdfPendNote:
		e.edited(e.doc.UpdateAnnot(p.ref, func(d pdf.Dict) { d["Contents"] = pdf.EncodeText(text) }), p.page)
	case pdfPendReplace:
		err := e.doc.Do(func() error {
			if err := e.doc.CoverRect(p.page, p.cover.Inset(-0.5), p.bg); err != nil {
				return err
			}
			if text == "" {
				return nil
			}
			return e.doc.AddText(p.page, text, p.base[0], p.base[1], 0, p.style)
		})
		e.edited(err, p.page)
	case pdfPendImage:
		e.edited(e.doc.AddImage(p.page, p.img, p.jpeg, p.rect), p.page)
	}
}

func (e *pdfEditor) cancelPending() {
	if e.pending != nil && e.pending.kind == pdfPendNote {
		e.commitPending()
		return
	}
	e.pending = nil
	if e.a.u.focus == "office/pdf/pending" {
		e.a.u.focus = "office/pdf"
	}
}

// loadPicture reads a picture file.
func pdfLoadPicture(path string) (image.Image, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, nil, err
	}
	if ext := strings.ToLower(filepath.Ext(path)); ext == ".jpg" || ext == ".jpeg" {
		return img, data, nil
	}
	return img, nil, nil
}

// placeImage puts a picture on the current page to be moved and sized.
func (e *pdfEditor) placeImage(path string) {
	img, data, err := pdfLoadPicture(path)
	if err != nil {
		e.a.toast(e.a.u.tf("Could not read the picture: %s", e.a.describe(err)), true)
		return
	}
	e.placePicture(img, data)
}

func (e *pdfEditor) placePicture(img image.Image, data []byte) {
	page := e.currentPage()
	box := e.doc.CropBox(e.pages[page])
	b := img.Bounds()
	ratio := float64(b.Dx()) / float64(max(b.Dy(), 1))
	w := min(box.W()*0.5, float64(b.Dx())*0.75)
	h := w / ratio
	if h > box.H()*0.5 {
		h = box.H() * 0.5
		w = h * ratio
	}
	cx, cy := (box.X0+box.X1)/2, (box.Y0+box.Y1)/2
	e.pending = &pdfPending{kind: pdfPendImage, page: page, img: img, jpeg: data, ratio: ratio,
		rect: pdf.Rect{X0: cx - w/2, Y0: cy - h/2, X1: cx + w/2, Y1: cy + h/2}}
}

func (e *pdfEditor) deleteChosen() {
	if e.sel == nil {
		return
	}
	s := e.sel
	e.sel = nil
	e.edited(e.doc.DeleteAnnot(s.page, s.ref), s.page)
}

// recolour gives the chosen annotation a colour.
func (e *pdfEditor) recolour(c [3]float64) {
	if e.sel == nil {
		return
	}
	e.edited(e.doc.UpdateAnnot(e.sel.ref, func(d pdf.Dict) { d["C"] = pdf.Reals(c[0], c[1], c[2]) }), e.sel.page)
}

// drawPageMarks draws what lies over a page: search hits, the selection, form fields, the annotation chosen, and the
// shape being drawn.
func (e *pdfEditor) drawPageMarks(i int, r gfx.Rect) {
	u := &e.a.u
	t := e.textOf(i)
	if t != nil {
		for k, m := range e.matches {
			if m.page != i {
				continue
			}
			col := gfx.Alpha(colWarning, 0.35)
			if k == e.current {
				col = gfx.Alpha(gfx.Hex(0xf08a24), 0.5)
			}
			e.shadeQuads(i, t.Quads(m.from, m.to), col)
		}
		if e.selPage == i && e.selFrom != e.selTo {
			e.shadeQuads(i, t.Quads(e.selFrom, e.selTo), gfx.Alpha(colAccent, 0.3))
		}
	}
	e.drawFields(i)
	if e.sel != nil && e.sel.page == i {
		box := e.screenBox(i, e.sel.rect)
		if e.drag.kind == pdfDragMove {
			box.X += u.mx - e.drag.x0
			box.Y += u.my - e.drag.y0
		} else if e.drag.kind == pdfDragResize {
			box = e.screenBox(i, e.resized(e.drag))
		}
		u.c.Outline(box, max(1, u.px(1)), colAccent)
		if e.drag.kind != pdfDragMove && e.drag.kind != pdfDragResize {
			for _, h := range e.handles() {
				u.c.Fill(h, colWhite)
				u.c.Outline(h, max(1, u.px(1)), colAccent)
			}
		}
	}
	if e.drag.page == i {
		switch e.drag.kind {
		case pdfDragInk:
			col := pdfRGBA(e.color)
			w := float32(e.width * e.pxPerPoint())
			for k := 1; k < len(e.ink); k++ {
				a, b := e.ink[k-1], e.ink[k]
				u.c.Line(a[0], a[1], b[0], b[1], max(w, 1), col)
			}
		case pdfDragShape:
			x0, y0, x1, y1 := e.drag.x0, e.drag.y0, u.mx, u.my
			if e.tool == pdfToolLine || e.tool == pdfToolArrow {
				u.c.Line(x0, y0, x1, y1, max(float32(e.width*e.pxPerPoint()), 1), pdfRGBA(e.color))
				break
			}
			box := gfx.Rect{X: min(x0, x1), Y: min(y0, y1), W: max(x0, x1) - min(x0, x1), H: max(y0, y1) - min(y0, y1)}
			if e.tool == pdfToolCover {
				u.c.Fill(box, colWhite)
			}
			u.c.Outline(box, max(1, u.px(1)), colAccent)
		}
	}
	if p := e.pending; p != nil && p.page == i && p.kind == pdfPendImage {
		box := e.screenBox(i, p.rect)
		u.c.Image(p.img, box)
	}
}

func (e *pdfEditor) shadeQuads(page int, quads [][8]float64, col color.RGBA) {
	u := &e.a.u
	for _, q := range quads {
		box := pdf.Rect{X0: q[0], Y0: q[1], X1: q[0], Y1: q[1]}
		for k := 2; k < 8; k += 2 {
			box = box.Union(pdf.Rect{X0: q[k], Y0: q[k+1], X1: q[k], Y1: q[k+1]})
		}
		u.c.Fill(e.screenBox(page, box), col)
	}
}

func pdfRGBA(c [3]float64) color.RGBA {
	return color.RGBA{uint8(c[0]*255 + 0.5), uint8(c[1]*255 + 0.5), uint8(c[2]*255 + 0.5), 255}
}

// drawOverlays draws what floats over the pages.
func (e *pdfEditor) drawOverlays() {
	if e.pending != nil {
		e.drawPending()
	}
	if e.active != nil {
		e.drawActiveField()
	}
	if e.sign != nil {
		e.drawSignPad()
	}
	if e.saveAs != nil {
		e.drawSaveAs()
	}
}

// drawPending draws the box being written in, or the picture being placed, with its buttons.
func (e *pdfEditor) drawPending() {
	a := e.a
	u := &a.u
	p := e.pending
	if p.page >= len(e.pages) {
		e.pending = nil
		return
	}
	box := e.screenBox(p.page, p.rect)
	if p.kind == pdfPendImage {
		e.pendingImage(box)
		return
	}
	id := "office/pdf/pending"
	r := gfx.Rect{X: box.X, Y: box.Y, W: max(box.W, u.px(180)), H: max(box.H, u.px(44))}
	r.X = min(max(r.X, e.area.X+u.px(4)), e.area.Right()-r.W-u.px(4))
	r.Y = min(max(r.Y, e.area.Y+u.px(4)), e.area.Bottom()-r.H-u.px(40))
	e.oversNext = append(e.oversNext, gfx.Rect{X: r.X - u.px(3), Y: r.Y - u.px(3), W: r.W + u.px(170), H: r.H + u.px(70)})
	u.c.Fill(r.Inset(-u.px(3)), colPanel)
	u.textBox(id, r, &p.text, u.t(pdfPendingPrompt(p.kind)), true)
	bw := u.px(76)
	by := r.Bottom() + u.px(6)
	hint := pdfPendingHint(p.kind)
	if hint != "" {
		f := u.face(11.5)
		u.c.Fill(gfx.Rect{X: r.X, Y: by, W: r.W + bw*2, H: u.px(22)}, colPanel)
		u.label(f, gfx.Rect{X: r.X + u.px(4), Y: by, W: r.W + bw*2, H: u.px(22)}, f.Ellipsize(u.t(hint), r.W+bw*2-u.px(8)), colDim, 0)
		by += u.px(24)
	}
	for _, k := range u.keys {
		if u.focus == id && k.Code == keyEscape {
			e.cancelPending()
			return
		}
	}
	if u.button("office/pdf/pending/ok", gfx.Rect{X: r.X, Y: by, W: bw, H: u.px(28)}, "", u.t("Done"), primary, true) {
		e.commitPending()
		return
	}
	if u.button("office/pdf/pending/no", gfx.Rect{X: r.X + bw + u.px(6), Y: by, W: bw, H: u.px(28)}, "", u.t("Cancel"), plain, true) {
		e.cancelPending()
	}
}

// pdfPendingPrompt says what a box wants.
func pdfPendingPrompt(k pdfPendingKind) string {
	switch k {
	case pdfPendNote:
		return msgs("Write a note")[0]
	case pdfPendSignText:
		return msgs("Type your name")[0]
	}
	return msgs("Write here")[0]
}

// pdfPendingHint says what a box will really do.
func pdfPendingHint(k pdfPendingKind) string {
	switch k {
	case pdfPendReplace:
		return msgs("The old line is covered, not removed, and the new words use the closest font and size.")[0]
	case pdfPendSignText:
		return msgs("A typed signature is text on the page, not a cryptographic signature.")[0]
	}
	return ""
}

func (e *pdfEditor) pendingImage(box gfx.Rect) {
	a := e.a
	u := &a.u
	p := e.pending
	id := "office/pdf/pending/image"
	e.oversNext = append(e.oversNext, box.Inset(-u.px(8)), gfx.Rect{X: box.X, Y: box.Bottom(), W: u.px(170), H: u.px(40)})
	u.c.Outline(box, max(1, u.px(1)), colAccent)
	s := u.px(10)
	corner := gfx.Rect{X: box.Right() - s/2, Y: box.Bottom() - s/2, W: s, H: s}
	u.c.Fill(corner, colWhite)
	u.c.Outline(corner, max(1, u.px(1)), colAccent)
	if u.press(id+"/size", corner.Inset(-u.px(3))) {
		e.drag.kind = pdfDragPendingSize
	}
	if u.press(id, box) {
		e.drag = pdfDrag{kind: pdfDragPendingMove, x0: u.mx, y0: u.my}
	}
	k := 1 / e.pxPerPoint()
	if u.dragging(id) {
		dx, dy := e.deviceToUser(p.page, float64(u.mx-e.drag.x0), float64(u.my-e.drag.y0), k)
		p.rect = pdf.Rect{X0: p.rect.X0 + dx, Y0: p.rect.Y0 + dy, X1: p.rect.X1 + dx, Y1: p.rect.Y1 + dy}
		e.drag.x0, e.drag.y0 = u.mx, u.my
	}
	if u.dragging(id + "/size") {
		x, y := e.fromScreen(p.page, u.mx, u.my)
		w := max(x-p.rect.X0, 8)
		h := w / max(p.ratio, 0.001)
		if y < p.rect.Y1 {
			h = max(h, 1)
		}
		p.rect.X1 = p.rect.X0 + w
		p.rect.Y0 = p.rect.Y1 - h
	}
	if u.letGo(id) || u.letGo(id+"/size") {
		e.drag.kind = pdfDragNone
	}
	by := box.Bottom() + u.px(8)
	by = min(by, e.area.Bottom()-u.px(34))
	bw := u.px(76)
	if u.button("office/pdf/pending/ok", gfx.Rect{X: box.X, Y: by, W: bw, H: u.px(28)}, "", u.t("Place"), primary, true) {
		e.commitPending()
		return
	}
	if u.button("office/pdf/pending/no", gfx.Rect{X: box.X + bw + u.px(6), Y: by, W: bw, H: u.px(28)}, "", u.t("Cancel"), plain, true) {
		e.cancelPending()
	}
}

// contextMenu opens on a right click over a page.
func (e *pdfEditor) contextMenu(page int) {
	a := e.a
	u := &a.u
	anchor := gfx.Rect{X: u.rightAt.X, Y: u.rightAt.Y}
	if page >= 0 {
		x, y := e.fromScreen(page, u.rightAt.X, u.rightAt.Y)
		if an := e.annotAt(page, x, y); an != nil {
			e.sel = &pdfSel{ref: an.Ref, page: page, kind: an.Kind, rect: an.Rect}
		}
	}
	items := []menuItem{
		{label: u.t("Copy"), key: "Ctrl+C", disabled: e.selectionText() == "", run: func() { u.setClipText(e.selectionText()) }},
		{label: u.t("Select all on this page"), key: "Ctrl+A", run: e.selectAll},
	}
	if e.selectionText() != "" {
		items = append(items, menuSep,
			menuItem{label: u.t("Highlight"), run: func() { e.markSelection("Highlight") }},
			menuItem{label: u.t("Underline"), run: func() { e.markSelection("Underline") }},
			menuItem{label: u.t("Strike out"), run: func() { e.markSelection("StrikeOut") }})
	}
	if e.sel != nil {
		items = append(items, menuSep, menuItem{label: u.t("Delete annotation"), key: "Del", run: e.deleteChosen})
	}
	a.openMenu("office/pdf/page", anchor, items)
}

// pickImage asks for a picture to place.
func (e *pdfEditor) pickImage() {
	a := e.a
	u := &a.u
	a.pickFiles(pickRequest{key: "picture", title: u.t("Add a picture"), kinds: u.t("Pictures"), exts: pickPictures}, func(paths []string) {
		e.placeImage(paths[0])
	})
}
