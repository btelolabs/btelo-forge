package ui

import (
	"context"
	"image"
	"strconv"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/pptx"
)

// The presentation tab: the slides down the left, the chosen one in the middle with its notes under it, and two rows
// of tools over them. The file's own XML is the document (package pptx); this file holds the state, the frame and the
// thumbnails, and office_slides_*.go the rest: selection and dragging, typing, the tool rows, the slideshow and the
// tools an AI drives it with.

func init() {
	registerOffice(&officeFormat{id: "slides", exts: []string{".pptx", ".ppsx", ".potx", ".pptm"}, glyph: icOfficeSlides,
		open: openSlides, blank: func(*App) ([]byte, error) { return pptx.Blank() }, name: msgs("Presentation %s")[0]})
}

// Sizes of the tab's parts, in logical pixels.
const (
	slidesThumbsW = 196
	slidesRowH    = 38
	slidesNotesH  = 104
)

// slidesEditor is an open presentation.
type slidesEditor struct {
	a  *App
	od *officeDoc
	d  *pptx.Doc
	r  *pptx.Renderer

	cur      int   // the slide shown
	sel      []int // the chosen shapes, by id
	zoom     float64
	panX     float64
	panY     float64
	view     pptx.View // how the slide was last placed, in window pixels
	area     gfx.Rect
	bmp      *image.RGBA
	bmpKey   slidesBmpKey
	thumbs   map[string]*slideThumb
	busy     int // thumbnails being drawn
	scroll   float32
	act      *slideAct
	placing  string // the shape the next drag on the slide draws
	guides   []slideGuide
	text     *slideText
	typedAt  time.Time
	notes    string
	notesFor string
	popup    *slidesPopup
	icons    map[string]*image.RGBA
	layouts  map[string]*image.RGBA
	show     *slideShow
	dragTo   int  // where a thumbnail being dragged would land, -1 for nowhere
	noNotes  bool // the notes pane is hidden
	closed   bool
}

type slidesBmpKey struct {
	part          string
	rev           uint64
	scale, ox, oy float64
	w, h          int
	lang          Language
}

// slideThumb is a slide's small picture.
type slideThumb struct {
	img  *image.RGBA
	rev  uint64
	busy bool
}

func openSlides(a *App, od *officeDoc, data []byte) (officeEditor, error) {
	d, err := pptx.Open(data)
	if err != nil {
		return nil, err
	}
	e := &slidesEditor{a: a, od: od, d: d, thumbs: map[string]*slideThumb{}, icons: map[string]*image.RGBA{}, layouts: map[string]*image.RGBA{}, dragTo: -1}
	e.r = newSlidesRenderer(a, d)
	return e, nil
}

// newSlidesRenderer is a renderer whose words are in the window's language.
func newSlidesRenderer(a *App, d *pptx.Doc) *pptx.Renderer {
	u := &a.u
	return &pptx.Renderer{D: d, Fonts: a.officeBook(), Prompt: func(ph *pptx.Ph) string {
		switch ph.Type {
		case "title", "ctrTitle":
			return u.t("Click to add title")
		case "subTitle":
			return u.t("Click to add subtitle")
		case "pic":
			return u.t("Click to add a picture")
		case "dt", "ftr", "sldNum", "hdr":
			return ""
		}
		return u.t("Click to add text")
	}, Label: func(kind string) string {
		switch kind {
		case "chart":
			return u.t("Chart, kept as it is")
		case "diagram":
			return u.t("SmartArt diagram, kept as it is")
		case "object":
			return u.t("Embedded object, kept as it is")
		case "picture":
			return u.t("Picture in a format not shown here")
		}
		return u.t("Graphic, kept as it is")
	}}
}

func (e *slidesEditor) slide() *pptx.Slide {
	if e.cur >= 0 && e.cur < len(e.d.Slides) {
		return e.d.Slides[e.cur]
	}
	return nil
}

// goTo shows a slide, leaving what was being done on the last.
func (e *slidesEditor) goTo(i int) {
	if n := len(e.d.Slides); n == 0 {
		e.cur = 0
	} else {
		i = min(max(i, 0), n-1)
		if i != e.cur {
			e.endText()
			e.sel, e.placing = nil, ""
			e.cur = i
		}
	}
	e.a.requestFrame()
}

func (e *slidesEditor) snapshot() (func() ([]byte, error), error) {
	e.d.EndBurst()
	return e.d.Saver(), nil
}

func (e *slidesEditor) version() uint64 { return e.d.Version() }

func (e *slidesEditor) holding() bool {
	return e.act != nil || e.text != nil && time.Since(e.typedAt) < time.Second
}

func (e *slidesEditor) undo() { e.step(e.d.Undo) }
func (e *slidesEditor) redo() { e.step(e.d.Redo) }

func (e *slidesEditor) step(fn func() error) {
	if e.act != nil {
		return
	}
	if fn() != nil {
		return
	}
	e.cur = min(e.cur, max(len(e.d.Slides)-1, 0))
	if e.text != nil && !e.textStillThere() {
		e.text = nil
		e.a.u.focus = ""
	}
	e.pruneSelection()
	e.notesFor = ""
	e.a.requestFrame()
}

// pruneSelection forgets chosen shapes that are gone.
func (e *slidesEditor) pruneSelection() {
	s := e.slide()
	if s == nil {
		e.sel = nil
		return
	}
	out := e.sel[:0]
	for _, id := range e.sel {
		if e.d.Find(s, id) != nil {
			out = append(out, id)
		}
	}
	e.sel = out
}

func (e *slidesEditor) wait() time.Duration {
	if e.show != nil && e.show.fading() {
		return time.Second / 60
	}
	if e.busy > 0 {
		return time.Second / 10
	}
	return 0
}

func (e *slidesEditor) closing() { e.closed = true }

func (e *slidesEditor) dropped(paths []string) bool {
	s := e.slide()
	used := false
	for _, p := range paths {
		if s != nil && slidesPictureFile(p) {
			e.addPictureFile(p)
			used = true
		}
	}
	return used
}

// draw lays the tab out.
func (e *slidesEditor) draw(r gfx.Rect) {
	u := &e.a.u
	if e.cur >= len(e.d.Slides) {
		e.cur = max(len(e.d.Slides)-1, 0)
	}
	held := u.blocked
	if e.popup != nil {
		u.blocked = true
	}
	u.c.Fill(r, colPanel)
	rows := gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: u.px(slidesRowH) * 2}
	left := gfx.Rect{X: r.X, Y: rows.Bottom(), W: u.px(slidesThumbsW), H: r.H - rows.H}
	main := gfx.Rect{X: left.Right(), Y: rows.Bottom(), W: r.W - left.W, H: r.H - rows.H}
	notesH := u.px(slidesNotesH)
	if e.noNotes {
		notesH = 0
	}
	e.area = gfx.Rect{X: main.X, Y: main.Y, W: main.W, H: main.H - notesH}
	e.drawThumbs(left)
	e.drawCanvas(e.area)
	if notesH > 0 {
		e.drawNotes(gfx.Rect{X: main.X, Y: main.Bottom() - notesH, W: main.W, H: notesH})
	}
	e.drawRows(rows)
	u.c.Fill(gfx.Rect{X: left.Right() - max(1, u.px(1)), Y: left.Y, W: max(1, u.px(1)), H: left.H}, colBorder)
	e.editorKeys()
	if e.popup != nil {
		u.blocked = held
		e.drawPopup()
	}
}

// drawNotes draws the speaker notes under the slide.
func (e *slidesEditor) drawNotes(r gfx.Rect) {
	u := &e.a.u
	s := e.slide()
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: max(1, u.px(1))}, colBorder)
	if s == nil {
		return
	}
	if e.notesFor != s.Part {
		e.notes, e.notesFor = e.d.Notes(s), s.Part
		if u.focus == "office/slides/notes" {
			u.focus = ""
		}
	}
	box := r.Inset(u.px(8))
	changed, done := u.textBox("office/slides/notes", box, &e.notes, u.t("Click to add notes"), true)
	if changed {
		if err := e.d.SetNotes(s, e.notes, "notes:"+s.Part); err != nil {
			e.a.toast(e.a.describe(err), true)
		}
	}
	if done {
		e.d.EndBurst()
	}
}

// thumbSize is a thumbnail's size in pixels, the slide's shape in the panel's width.
func (e *slidesEditor) thumbSize(r gfx.Rect) (w, h float32) {
	u := &e.a.u
	w = r.W - u.px(46)
	return w, w * float32(e.d.H/e.d.W)
}

// drawThumbs draws the column of slides, and handles picking, dragging and its menu.
func (e *slidesEditor) drawThumbs(r gfx.Rect) {
	u := &e.a.u
	a := e.a
	w, h := e.thumbSize(r)
	gap := u.px(14)
	rowH := h + gap
	n := len(e.d.Slides)
	content := float32(n)*rowH + gap
	top := r.Y + gap - e.scroll
	if r.Contains(u.mx, u.my) && u.wheel != 0 && !u.ctrl {
		e.scroll -= u.wheel * u.px(60)
	}
	e.scroll = max(min(e.scroll, content-r.H), 0)
	top = r.Y + gap - e.scroll
	u.c.Push(r)
	defer u.c.Pop()
	f := u.face(12)
	for i, s := range e.d.Slides {
		y := top + float32(i)*rowH
		if y+h < r.Y || y > r.Bottom() {
			continue
		}
		card := gfx.Rect{X: r.X + u.px(34), Y: y, W: w, H: h}
		col := colDim
		if i == e.cur {
			col = colText
		}
		num := strconv.Itoa(i + 1)
		u.c.Text(f, card.X-u.px(8)-f.Measure(num), y, num, col)
		id := "office/slides/thumb/" + strconv.Itoa(i)
		if u.press(id, gfx.Rect{X: r.X, Y: y - gap/2, W: r.W, H: rowH}) {
			e.goTo(i)
			e.dragTo = -1
		}
		if u.active == id && u.dragging(id) && u.drag.moved {
			e.dragTo = min(max(int((u.my-r.Y+e.scroll-gap/2+rowH/2)/rowH), 0), n)
		}
		if u.letGo(id) {
			if e.dragTo >= 0 {
				to := e.dragTo
				if to > i {
					to--
				}
				e.dragTo = -1
				if to != i {
					if err := e.d.MoveSlide(i, to); err == nil {
						e.cur = to
					}
				}
			}
		}
		e.drawThumb(s, card)
		if i == e.cur {
			u.c.Outline(card.Inset(-u.px(2)), u.px(2), colAccent)
		} else {
			u.c.Outline(card, max(1, u.px(1)), colBorderHi)
		}
		if s.Hidden() {
			u.c.Fill(card, gfx.Alpha(colBlack, 0.45))
			u.icon(icHidden, card, 20, colWhite)
		}
		if u.rightAt != nil && !u.blocked && (gfx.Rect{X: r.X, Y: y - gap/2, W: r.W, H: rowH}).Contains(u.rightAt.X, u.rightAt.Y) {
			e.goTo(i)
			a.openMenu("office/slides/thumbmenu", gfx.Rect{X: u.rightAt.X, Y: u.rightAt.Y}, e.slideMenu(i))
		}
	}
	if e.dragTo >= 0 {
		y := top + float32(e.dragTo)*rowH - gap/2
		u.c.Fill(gfx.Rect{X: r.X + u.px(12), Y: y - u.px(1), W: r.W - u.px(24), H: u.px(2)}, colAccent)
	}
	if n == 0 {
		u.label(f, gfx.Rect{X: r.X, Y: r.Y + u.px(20), W: r.W, H: u.px(20)}, u.t("No slides"), colFaint, 0.5)
	}
	if u.rightAt != nil && !u.blocked && r.Contains(u.rightAt.X, u.rightAt.Y) && a.menu == nil {
		a.openMenu("office/slides/thumbmenu", gfx.Rect{X: u.rightAt.X, Y: u.rightAt.Y}, e.slideMenu(-1))
	}
}

// slideMenu is the commands of a slide, i < 0 for the list's empty end.
func (e *slidesEditor) slideMenu(i int) []menuItem {
	u := &e.a.u
	items := []menuItem{{label: u.t("New slide"), key: "Ctrl+M", run: func() { e.addSlideLike() }}}
	if i < 0 {
		return items
	}
	s := e.d.Slides[i]
	items = append(items,
		menuItem{label: u.t("Duplicate slide"), run: func() {
			if ns, err := e.d.DuplicateSlide(s); err == nil {
				e.goTo(e.d.Number(ns) - 1)
			}
		}},
		menuItem{label: u.t("Delete slide"), run: func() { e.deleteSlide(s) }},
		menuSep,
		menuItem{label: u.t("Hide slide"), checked: s.Hidden(), run: func() {
			e.d.SetHidden(s, !s.Hidden())
		}},
	)
	return items
}

func (e *slidesEditor) deleteSlide(s *pptx.Slide) {
	e.endText()
	if err := e.d.DeleteSlide(s); err != nil {
		e.a.toast(e.a.describe(err), true)
		return
	}
	e.sel = nil
	e.notesFor = ""
	e.cur = min(e.cur, max(len(e.d.Slides)-1, 0))
}

// addSlideLike adds a slide after the current one, on the layout most slides that follow a title use.
func (e *slidesEditor) addSlideLike() {
	var l *pptx.Layout
	if s := e.slide(); s != nil {
		l = s.Layout
		if l.Type == "title" {
			for _, k := range l.Master.Layouts {
				if k.Type == "obj" {
					l = k
				}
			}
		}
	} else {
		l = e.d.Masters[0].Layouts[0]
	}
	e.addSlide(l)
}

func (e *slidesEditor) addSlide(l *pptx.Layout) {
	e.endText()
	at := e.cur + 1
	if len(e.d.Slides) == 0 {
		at = 0
	}
	if _, err := e.d.AddSlide(l, at); err != nil {
		e.a.toast(e.a.describe(err), true)
		return
	}
	e.goTo(at)
	e.sel = nil
}

// thumbJobs is how many thumbnails are drawn at a time.
const thumbJobs = 2

// drawThumb shows a slide's picture, the old one until a new one is ready; drawing one goes on away from the window.
func (e *slidesEditor) drawThumb(s *pptx.Slide, r gfx.Rect) {
	u := &e.a.u
	t := e.thumbs[s.Part]
	if t == nil {
		t = &slideThumb{}
		e.thumbs[s.Part] = t
	}
	w, h := int(r.W), int(r.H)
	stale := t.img == nil || t.rev != s.Rev || t.img.Rect.Dx() != w || t.img.Rect.Dy() != h
	if stale && !t.busy && e.busy < thumbJobs && w > 0 && h > 0 {
		t.busy = true
		e.busy++
		frozen := e.d.Freeze(s)
		rend := *e.r
		e.a.background(func(ctx context.Context) {
			// Drawn at twice the size and halved: words this small come out better for it.
			img := image.NewRGBA(image.Rect(0, 0, w*2, h*2))
			c := gfx.Wrap(img)
			c.Clear(colWhite)
			rend.Slide(c, frozen, pptx.View{Scale: float64(w*2) / e.d.W}, pptx.Options{})
			img = pptx.Halved(img)
			e.a.post(func() {
				t.busy = false
				e.busy--
				t.img, t.rev = img, frozen.Rev
				e.a.requestFrame()
			})
		})
	}
	if t.img != nil {
		u.c.Image(t.img, r)
	} else {
		u.c.Fill(r, colWhite)
	}
}

// fitScale is the zoom that shows the whole slide in the area.
func (e *slidesEditor) fitScale(r gfx.Rect) float64 {
	m := float64(e.a.u.px(20))
	return max(min((float64(r.W)-2*m)/e.d.W, (float64(r.H)-2*m)/e.d.H), 0.02)
}

func (e *slidesEditor) scale(r gfx.Rect) float64 {
	if e.zoom > 0 {
		return e.zoom * float64(e.a.u.s)
	}
	return e.fitScale(r)
}

// drawCanvas draws the slide and everything over it.
func (e *slidesEditor) drawCanvas(r gfx.Rect) {
	u := &e.a.u
	s := e.slide()
	if s == nil {
		u.label(u.face(14), r, u.t("This presentation has no slides. Press Ctrl+M to add one."), colDim, 0.5)
		if u.press("office/slides/none", r) {
			u.letGo("office/slides/none")
		}
		return
	}
	k := e.scale(r)
	pw, ph := e.d.W*k, e.d.H*k
	// A slide smaller than the area stays in the middle; a larger one pans.
	limit := func(pan *float64, size, room float64) float64 {
		if size <= room {
			*pan = 0
			return (room - size) / 2
		}
		*pan = max(min(*pan, 0), room-size-40)
		return *pan + 20
	}
	x := float64(int(float64(r.X) + limit(&e.panX, pw, float64(r.W))))
	y := float64(int(float64(r.Y) + limit(&e.panY, ph, float64(r.H))))
	e.view = pptx.View{Scale: k, X: x, Y: y}
	ax, ay := int(r.X), int(r.Y)
	w, h := int(r.W), int(r.H)
	key := slidesBmpKey{s.Part, s.Rev, k, x, y, w, h, u.lang}
	if key != e.bmpKey || e.bmp == nil {
		if e.bmp == nil || e.bmp.Rect.Dx() != w || e.bmp.Rect.Dy() != h {
			e.bmp = image.NewRGBA(image.Rect(0, 0, w, h))
		}
		c := gfx.Wrap(e.bmp)
		c.Clear(colPanel)
		c.Fill(gfx.Rect{X: float32(x) - float32(ax) + u.px(3), Y: float32(y) - float32(ay) + u.px(4), W: float32(pw), H: float32(ph)}, gfx.Alpha(colBlack, 0.4))
		e.r.Slide(c, s, pptx.View{Scale: k, X: x - float64(ax), Y: y - float64(ay)}, pptx.Options{Prompts: true})
		e.bmpKey = key
	}
	u.c.Blit(e.bmp, ax, ay, true)
	u.c.Push(r)
	defer u.c.Pop()
	e.pointer(r)
	e.drawOverlay()
}

// box2px is a rectangle in points in window pixels.
func (e *slidesEditor) box2px(x, y, w, h float64) gfx.Rect {
	v := e.view
	return gfx.Rect{X: float32(x*v.Scale + v.X), Y: float32(y*v.Scale + v.Y), W: float32(w * v.Scale), H: float32(h * v.Scale)}
}

func (e *slidesEditor) toSlide(x, y float32) (float64, float64) {
	v := e.view
	return (float64(x) - v.X) / v.Scale, (float64(y) - v.Y) / v.Scale
}

// presetIcon is a small picture of a preset, for the shapes panel.
func (e *slidesEditor) presetIcon(name string, size int) *image.RGBA {
	key := name + "/" + strconv.Itoa(size)
	if img, ok := e.icons[key]; ok {
		return img
	}
	img := pptx.PresetIcon(name, size, colDim)
	e.icons[key] = img
	return img
}

// layoutPicture is a layout's small picture, for the picker.
func (e *slidesEditor) layoutPicture(l *pptx.Layout, w int) *image.RGBA {
	key := l.Part + "/" + strconv.Itoa(w)
	if img, ok := e.layouts[key]; ok {
		return img
	}
	h := int(float64(w) * e.d.H / e.d.W)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	c := gfx.Wrap(img)
	c.Clear(colWhite)
	r := *e.r
	r.Prompt = func(*pptx.Ph) string { return "" }
	r.Layout(c, l, pptx.View{Scale: float64(w) / e.d.W})
	e.layouts[key] = img
	return img
}

// slidesFonts are the typefaces the font menu offers.
var slidesFonts = []string{"Calibri", "Calibri Light", "Arial", "Times New Roman", "Georgia", "Verdana", "Tahoma", "Segoe UI", "Consolas", "Courier New", "微软雅黑", "等线", "宋体", "黑体", "楷体", "仿宋"}

var slidesSizes = []float64{8, 9, 10, 10.5, 11, 12, 14, 16, 18, 20, 24, 28, 32, 36, 40, 44, 48, 54, 60, 66, 72, 80, 88, 96}

// slidesPictureFile reports a file a slide can hold as a picture.
func slidesPictureFile(path string) bool {
	return isImage(path)
}
