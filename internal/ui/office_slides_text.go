package ui

import (
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
	"github.com/btelolabs/btelo-forge/internal/office/pptx"
)

// Typing in a shape or a table cell. The XML changes with each key, so that the slide shown is always the file; the
// caret is a paragraph and a place in it, and the frame the words were laid out in says where that is.

var errSlidesNoWords = errors.New("that shape cannot hold words")

const slidesTextFocus = "office/slides/text"

// slideText is the caret in a body of text.
type slideText struct {
	t             pptx.Target
	caret, anchor pptx.Pos
	trailing      bool
	frame         *pptx.TextFrame
	frameRev      uint64
	pending       []func(*ox.Node) // what the toolbar set with no words chosen, for the next words typed
	dragging      bool
}

func (e *slidesEditor) textStillThere() bool {
	s := e.slide()
	return s != nil && e.text != nil && e.d.HasBody(s, e.text.t)
}

// beginText puts the caret in a body.
func (e *slidesEditor) beginText(t pptx.Target, at pptx.Pos) {
	if e.text != nil {
		e.endText()
	}
	e.text = &slideText{t: t, caret: at, anchor: at}
	e.a.u.setFocus(slidesTextFocus)
	e.typedAt = time.Now()
}

// endText takes the caret out. A box left with no words is removed, as PowerPoint does with a text box.
func (e *slidesEditor) endText() {
	x := e.text
	if x == nil {
		return
	}
	e.text = nil
	e.d.EndBurst()
	if e.a.u.focus == slidesTextFocus {
		e.a.u.focus = ""
	}
	s := e.slide()
	if s == nil || x.t.Cell {
		return
	}
	if k := e.d.Find(s, x.t.ID); k != nil && k.Ph == nil && !k.HasText() && isTextBox(k) {
		e.d.DeleteShapes(s, []int{k.ID})
		e.pruneSelection()
	}
}

func isTextBox(k *pptx.Shape) bool {
	return k.Node.Path("p:nvSpPr", "p:cNvSpPr").Attr("txBox") == "1"
}

// startTextAt puts the caret where a point on the slide falls in the shape's words.
func (e *slidesEditor) startTextAt(k *pptx.Shape, x, y float64) {
	s := e.slide()
	if k.Kind == pptx.KindGroup {
		// The double click went on a shape in the group: that one takes the words.
		for i := len(k.Kids) - 1; i >= 0; i-- {
			if k.Kids[i].Box.Contains(x, y, 0) {
				e.sel = []int{k.Kids[i].ID}
				e.startTextAt(k.Kids[i], x, y)
				return
			}
		}
		return
	}
	if k.Kind == pptx.KindTable {
		lx, ly := k.Box.Upright().Inverse().Apply(x, y)
		if t := e.r.Table(s, k); t != nil {
			for _, c := range t.Cells {
				if lx >= c.X && ly >= c.Y && lx < c.X+c.W && ly < c.Y+c.H {
					target := pptx.Target{ID: k.ID, Cell: true, Row: c.Row, Col: c.Col}
					e.beginText(target, pptx.Pos{})
					f := e.frame()
					if f != nil {
						fx, fy := f.Matrix.Inverse().Apply(x, y)
						e.text.caret, e.text.trailing = f.Hit(fx, fy)
						e.text.anchor = e.text.caret
					}
					return
				}
			}
		}
		return
	}
	if k.Kind != pptx.KindShape {
		return
	}
	if k.Ph != nil && k.EmptyPlaceholder() {
		switch k.Ph.Type {
		case "pic":
			e.pictureInto(k)
			return
		case "chart", "tbl", "dgm", "media", "clipArt":
			return // these hold what they are named for, not words
		}
	}
	e.ensureBody(k)
	t := pptx.Target{ID: k.ID}
	e.beginText(t, pptx.Pos{})
	if f := e.frame(); f != nil && len(f.Paras) > 0 {
		fx, fy := f.Matrix.Inverse().Apply(x, y)
		e.text.caret, e.text.trailing = f.Hit(fx, fy)
		e.text.anchor = e.text.caret
	}
}

// startTextEnd puts the caret after a shape's words.
func (e *slidesEditor) startTextEnd(k *pptx.Shape) {
	if k.Kind != pptx.KindShape {
		return
	}
	e.ensureBody(k)
	s := e.slide()
	body, _ := e.d.Body(s, pptx.Target{ID: k.ID})
	if body == nil {
		return
	}
	_, end := pptx.Whole(body)
	e.beginText(pptx.Target{ID: k.ID}, end)
}

// ensureBody gives a shape with no text body one.
func (e *slidesEditor) ensureBody(k *pptx.Shape) {
	s := e.slide()
	if b, _ := e.d.Body(s, pptx.Target{ID: k.ID}); b == nil {
		e.d.Edit("", []*pptx.Slide{s}, false, func() error {
			pptx.EnsureBody(k)
			return nil
		})
	}
}

// frame is the layout of the words being edited, worked out again after a change.
func (e *slidesEditor) frame() *pptx.TextFrame {
	x := e.text
	s := e.slide()
	if x == nil || s == nil {
		return nil
	}
	if x.frame != nil && x.frameRev == s.Rev {
		return x.frame
	}
	k := e.d.Find(s, x.t.ID)
	if k == nil {
		return nil
	}
	if x.t.Cell {
		x.frame, _ = e.r.CellFrame(s, k, x.t.Row, x.t.Col)
	} else {
		x.frame = e.r.Frame(s, k)
	}
	x.frameRev = s.Rev
	return x.frame
}

// textPointer handles the mouse while the caret is in a shape; it reports that the press was its own.
func (e *slidesEditor) textPointer(id string, r gfx.Rect) bool {
	u := &e.a.u
	x := e.text
	f := e.frame()
	if f == nil {
		return false
	}
	s := e.slide()
	k := e.d.Find(s, x.t.ID)
	if k == nil {
		return false
	}
	inFrame := func(px, py float32) bool {
		sx, sy := e.toSlide(px, py)
		if x.t.Cell {
			return k.Box.Contains(sx, sy, 0)
		}
		return k.Box.Contains(sx, sy, 4/e.view.Scale)
	}
	place := func(px, py float32) (pptx.Pos, bool) {
		sx, sy := e.toSlide(px, py)
		fx, fy := f.Matrix.Inverse().Apply(sx, sy)
		return f.Hit(fx, fy)
	}
	if u.hover(id, r) && inFrame(u.mx, u.my) {
		u.cursor = CursorText
	}
	if u.pressed && r.Contains(u.pressX, u.pressY) && !u.blocked && u.active == "" {
		if !inFrame(u.pressX, u.pressY) {
			e.endText()
			return false
		}
		u.press(id, r)
		pos, tr := place(u.pressX, u.pressY)
		x.caret, x.trailing = pos, tr
		if !u.shift {
			x.anchor = pos
		}
		if u.double {
			x.anchor, x.caret = e.wordAt(pos)
		}
		x.dragging = !u.double
		u.setFocus(slidesTextFocus)
		return true
	}
	if x.dragging && u.dragging(id) {
		x.caret, x.trailing = place(u.mx, u.my)
		e.a.requestFrame()
	}
	if u.letGo(id) {
		x.dragging = false
	}
	return true
}

// wordAt is the bounds of the word at a place.
func (e *slidesEditor) wordAt(p pptx.Pos) (pptx.Pos, pptx.Pos) {
	body := e.body()
	if body == nil {
		return p, p
	}
	paras := body.Elements("a:p")
	if p.Para >= len(paras) {
		return p, p
	}
	text := []rune(pptx.RangeText(body, pptx.Pos{Para: p.Para}, pptx.Pos{Para: p.Para, Off: 1 << 30}))
	a, b := wordAround(text, min(p.Off, len(text)))
	return pptx.Pos{Para: p.Para, Off: a}, pptx.Pos{Para: p.Para, Off: b}
}

func (e *slidesEditor) body() *ox.Node {
	s := e.slide()
	if s == nil || e.text == nil {
		return nil
	}
	b, _ := e.d.Body(s, e.text.t)
	return b
}

func posLess(a, b pptx.Pos) bool { return a.Para < b.Para || a.Para == b.Para && a.Off < b.Off }

func (x *slideText) range_() (pptx.Pos, pptx.Pos) {
	if posLess(x.anchor, x.caret) {
		return x.anchor, x.caret
	}
	return x.caret, x.anchor
}

// edit changes the words as a step: typing joins into one, anything else stands alone.
func (e *slidesEditor) editWords(key string, fn func(body *ox.Node)) {
	s := e.slide()
	e.d.Edit(key, []*pptx.Slide{s}, false, func() error {
		body := e.body()
		if body == nil {
			return errSlidesNoWords
		}
		fn(body)
		e.fit(e.text.t)
		return nil
	})
	e.typedAt = time.Now()
	e.a.requestFrame()
}

// insertTyped puts words at the caret, in the look the toolbar set for them.
func (e *slidesEditor) insertTyped(text string) {
	x := e.text
	e.editWords("typing:"+strconv.Itoa(x.t.ID), func(body *ox.Node) {
		a, b := x.range_()
		pptx.DeleteRange(body, a, b)
		start := a
		end := pptx.TypeText(body, a, text)
		for _, fn := range x.pending {
			pptx.EachRun(body, start, end, fn)
		}
		x.caret, x.anchor = end, end
		if len(x.pending) > 0 {
			e.d.EndBurst()
		}
		x.pending = nil
		x.trailing = false
	})
}

// textKeys handles the keyboard while the caret is in a shape.
func (e *slidesEditor) textKeys() {
	u := &e.a.u
	if u.focus != slidesTextFocus && u.focus != "" {
		// The notes or another box took the keyboard.
		e.endText()
		return
	}
	u.focus, u.focusDrawn = slidesTextFocus, true
	keys := u.keys
	u.keys = nil
	for _, k := range keys {
		if e.text == nil {
			break
		}
		e.textKey(k)
	}
	if e.text != nil && len(u.typed) > 0 {
		e.insertTyped(string(u.typed))
		u.typed = nil
	}
}

func (e *slidesEditor) textKey(k Key) {
	u := &e.a.u
	x := e.text
	body := e.body()
	if body == nil {
		e.endText()
		return
	}
	paras := body.Elements("a:p")
	length := func(i int) int {
		if i < 0 || i >= len(paras) {
			return 0
		}
		return pptx.ParaLength(paras[i])
	}
	move := func(p pptx.Pos) {
		x.caret = p
		if !k.Shift {
			x.anchor = p
		}
		e.d.EndBurst()
		x.pending = nil
	}
	a, b := x.range_()
	switch {
	case k.Code == keyEscape:
		e.endText()
		return
	case k.Code == keyEnter:
		e.editWords("", func(body *ox.Node) {
			pptx.DeleteRange(body, a, b)
			if k.Shift {
				x.caret = pptx.TypeText(body, a, "\v")
			} else {
				x.caret = pptx.SplitPara(body, a)
			}
			x.anchor = x.caret
		})
	case k.Code == keyBack || k.Code == keyDelete:
		from, to := a, b
		if a == b {
			switch {
			case k.Code == keyBack && k.Ctrl:
				from = e.wordEdge(a, true)
			case k.Code == keyDelete && k.Ctrl:
				to = e.wordEdge(a, false)
			case k.Code == keyBack && a.Off > 0:
				from.Off--
			case k.Code == keyBack && a.Para > 0:
				from = pptx.Pos{Para: a.Para - 1, Off: length(a.Para - 1)}
			case k.Code == keyDelete && a.Off < length(a.Para):
				to.Off++
			case k.Code == keyDelete && a.Para+1 < len(paras):
				to = pptx.Pos{Para: a.Para + 1}
			default:
				return
			}
		}
		e.editWords("", func(body *ox.Node) {
			pptx.DeleteRange(body, from, to)
			x.caret, x.anchor = from, from
		})
	case k.Code == keyLeft:
		switch {
		case k.Ctrl:
			move(e.wordEdge(x.caret, true))
		case a != b && !k.Shift:
			move(a)
		case x.caret.Off > 0:
			move(pptx.Pos{Para: x.caret.Para, Off: x.caret.Off - 1})
		case x.caret.Para > 0:
			move(pptx.Pos{Para: x.caret.Para - 1, Off: length(x.caret.Para - 1)})
		}
	case k.Code == keyRight:
		switch {
		case k.Ctrl:
			move(e.wordEdge(x.caret, false))
		case a != b && !k.Shift:
			move(b)
		case x.caret.Off < length(x.caret.Para):
			move(pptx.Pos{Para: x.caret.Para, Off: x.caret.Off + 1})
		case x.caret.Para+1 < len(paras):
			move(pptx.Pos{Para: x.caret.Para + 1})
		}
	case k.Code == keyUp || k.Code == keyDown:
		if f := e.frame(); f != nil {
			dir := 1
			if k.Code == keyUp {
				dir = -1
			}
			move(f.VerticalMove(x.caret, dir, x.trailing))
		}
	case k.Code == keyHome || k.Code == keyEnd:
		if k.Ctrl {
			if k.Code == keyHome {
				move(pptx.Pos{})
			} else {
				_, end := pptx.Whole(body)
				move(end)
			}
			break
		}
		if f := e.frame(); f != nil {
			start, end := f.LineEnds(x.caret, x.trailing)
			if k.Code == keyHome {
				move(start)
				x.trailing = false
			} else {
				move(end)
				x.trailing = true
			}
		}
	case k.Ctrl && k.Code == 'A':
		x.anchor, x.caret = pptx.Whole(body)
	case k.Ctrl && (k.Code == 'C' || k.Code == 'X'):
		if a != b {
			e.a.host.SetClipboard(pptx.RangeText(body, a, b))
			if k.Code == 'X' {
				e.editWords("", func(body *ox.Node) {
					pptx.DeleteRange(body, a, b)
					x.caret, x.anchor = a, a
				})
			}
		}
	case k.Ctrl && k.Code == 'V':
		if s := u.clipText(); s != "" {
			e.d.EndBurst()
			e.insertTyped(s)
			e.d.EndBurst()
		}
	case k.Ctrl && k.Code == 'B':
		e.toggleRun("b")
	case k.Ctrl && k.Code == 'I':
		e.toggleRun("i")
	case k.Ctrl && k.Code == 'U':
		e.toggleRun("u")
	case k.Code == keyTab:
		if x.t.Cell {
			e.nextCell(k.Shift)
			return
		}
		if x.caret.Off > 0 && a == b && !e.inBullets() {
			e.insertTyped("\t")
			return
		}
		e.editWords("", func(body *ox.Node) {
			pptx.EachPara(body, a.Para, b.Para, func(pPr *ox.Node) {
				level := 0
				if v, err := strconv.Atoi(pPr.Attr("lvl")); err == nil {
					level = v
				}
				if k.Shift {
					level--
				} else {
					level++
				}
				pptx.SetLevel(min(max(level, 0), 8))(pPr)
			})
		})
	case k.Code == 0x74: // F5
		e.endText()
		e.startShow(k.Shift)
	}
	u.caretBlink = time.Now()
	e.a.requestFrame()
}

func (e *slidesEditor) inBullets() bool {
	p := e.r.ParaAt(e.slide(), e.text.t, e.text.caret)
	return p.BuKind == "char" || p.BuKind == "auto" || p.BuKind == "blip"
}

// nextCell goes to the next cell of the table, or the one before.
func (e *slidesEditor) nextCell(back bool) {
	s := e.slide()
	x := e.text
	k := e.d.Find(s, x.t.ID)
	t := e.r.Table(s, k)
	if t == nil {
		return
	}
	for i, c := range t.Cells {
		if c.Row == x.t.Row && c.Col == x.t.Col {
			n := i + 1
			if back {
				n = i - 1
			}
			if n >= 0 && n < len(t.Cells) {
				c := t.Cells[n]
				e.beginText(pptx.Target{ID: k.ID, Cell: true, Row: c.Row, Col: c.Col}, pptx.Pos{})
				if body := e.body(); body != nil {
					e.text.anchor, e.text.caret = pptx.Whole(body)
				}
			}
			return
		}
	}
}

// wordEdge is where Ctrl with an arrow goes from a place.
func (e *slidesEditor) wordEdge(p pptx.Pos, back bool) pptx.Pos {
	body := e.body()
	text := []rune(pptx.RangeText(body, pptx.Pos{Para: p.Para}, pptx.Pos{Para: p.Para, Off: 1 << 30}))
	off := min(p.Off, len(text))
	if back {
		if off == 0 && p.Para > 0 {
			paras := body.Elements("a:p")
			return pptx.Pos{Para: p.Para - 1, Off: pptx.ParaLength(paras[p.Para-1])}
		}
		return pptx.Pos{Para: p.Para, Off: wordStart(text, off)}
	}
	if off == len(text) {
		if p.Para+1 < len(body.Elements("a:p")) {
			return pptx.Pos{Para: p.Para + 1}
		}
		return p
	}
	return pptx.Pos{Para: p.Para, Off: wordEnd(text, off)}
}

// toggleRun turns a run property on or off for what is chosen; "u" underlines.
func (e *slidesEditor) toggleRun(name string) {
	run := e.currentRun()
	switch name {
	case "b":
		e.setRun(pptx.SetBool("b", !run.Bold))
	case "i":
		e.setRun(pptx.SetBool("i", !run.Italic))
	case "u":
		e.setRun(pptx.SetUnderline(!run.Under))
	}
}

// currentRun is how the words at the caret, or the chosen shape's first words, are set.
func (e *slidesEditor) currentRun() pptx.RunProps {
	s := e.slide()
	if s == nil {
		return pptx.RunProps{Size: 18}
	}
	if e.text != nil {
		return e.r.RunAt(s, e.text.t, e.text.caret)
	}
	for _, k := range e.chosen() {
		if t := (pptx.Target{ID: k.ID}); e.d.HasBody(s, t) {
			return e.r.RunAt(s, t, pptx.Pos{})
		}
	}
	return pptx.RunProps{Size: 18}
}

func (e *slidesEditor) currentPara() pptx.ParaProps {
	s := e.slide()
	if s == nil {
		return pptx.ParaProps{}
	}
	if e.text != nil {
		return e.r.ParaAt(s, e.text.t, e.text.caret)
	}
	for _, k := range e.chosen() {
		if t := (pptx.Target{ID: k.ID}); e.d.HasBody(s, t) {
			return e.r.ParaAt(s, t, pptx.Pos{})
		}
	}
	return pptx.ParaProps{}
}

// setRun applies a run change to what is chosen: the words chosen, the words to come at a caret with none chosen, or
// the whole of each chosen shape.
func (e *slidesEditor) setRun(fn func(*ox.Node)) {
	s := e.slide()
	if s == nil {
		return
	}
	if x := e.text; x != nil {
		a, b := x.range_()
		if a == b {
			if pptx.ParaLength(e.body().Elements("a:p")[a.Para]) > 0 {
				x.pending = append(x.pending, fn)
				return
			}
		}
		e.editWords("", func(body *ox.Node) { pptx.EachRun(body, a, b, fn) })
		return
	}
	e.eachBody(func(body *ox.Node) {
		a, b := pptx.Whole(body)
		pptx.EachRun(body, a, b, fn)
	})
}

// setPara applies a paragraph change to the paragraphs at the caret, or to each chosen shape's.
func (e *slidesEditor) setPara(fn func(*ox.Node)) {
	if x := e.text; x != nil {
		a, b := x.range_()
		e.editWords("", func(body *ox.Node) { pptx.EachPara(body, a.Para, b.Para, fn) })
		return
	}
	e.eachBody(func(body *ox.Node) { pptx.EachPara(body, 0, 1<<30, fn) })
}

// eachBody edits the text bodies of the chosen shapes as one step, tables' cells among them.
func (e *slidesEditor) eachBody(fn func(*ox.Node)) {
	s := e.slide()
	if s == nil || len(e.sel) == 0 {
		return
	}
	e.d.Edit("", []*pptx.Slide{s}, false, func() error {
		var visit func(k *pptx.Shape)
		visit = func(k *pptx.Shape) {
			for _, c := range k.Kids {
				visit(c)
			}
			if k.Kind == pptx.KindTable {
				for _, tc := range k.Node.All("a:tc") {
					if b := tc.Child("a:txBody"); b != nil {
						fn(b)
					}
				}
				return
			}
			if b, _ := e.d.Body(s, pptx.Target{ID: k.ID}); b != nil {
				fn(b)
			}
		}
		for _, k := range e.chosen() {
			visit(k)
		}
		for _, k := range e.chosen() {
			e.r.FitText(s, k)
		}
		return nil
	})
}

// drawText draws the caret and the chosen words over the slide.
func (e *slidesEditor) drawText() {
	x := e.text
	if x == nil {
		return
	}
	u := &e.a.u
	f := e.frame()
	if f == nil {
		return
	}
	u.focus, u.focusDrawn = slidesTextFocus, true
	m := e.view.Matrix().Mul(f.Matrix)
	a, b := x.range_()
	if a != b {
		for _, r := range f.Rects(a, b) {
			p := [4][2]float64{{r[0], r[1]}, {r[0] + r[2], r[1]}, {r[0] + r[2], r[1] + r[3]}, {r[0], r[1] + r[3]}}
			var q [4][2]float32
			for i := range p {
				px, py := m.Apply(p[i][0], p[i][1])
				q[i] = [2]float32{float32(px), float32(py)}
			}
			col := gfx.Alpha(colAccent, 0.35)
			u.c.Triangle(q[0][0], q[0][1], q[1][0], q[1][1], q[2][0], q[2][1], col)
			u.c.Triangle(q[0][0], q[0][1], q[2][0], q[2][1], q[3][0], q[3][1], col)
		}
	}
	cx, cy, h := f.Caret(x.caret, x.trailing)
	x0, y0 := m.Apply(cx, cy)
	x1, y1 := m.Apply(cx, cy+h)
	// The shared caret keeps the blink and the input method's place; the line itself is drawn here, as it may lean.
	u.edit.caret, u.edit.anchor = x.caret.Para<<20|x.caret.Off, x.anchor.Para<<20|x.anchor.Off
	u.editOwner = slidesTextFocus
	u.c.Push(gfx.Rect{})
	onScreen := float32(math.Hypot(x1-x0, y1-y0))
	u.drawCaret(float32(x0), float32(y0), onScreen, true)
	run := e.currentRun()
	family := run.EA
	if family == "" {
		family = run.Latin
	}
	u.imeWords(float32(x0), float32(y0), onScreen, float32(run.Size)*onScreen/float32(h), family)
	u.c.Pop()
	if u.inactive || time.Since(u.caretBlink)%caretBlinkPeriod < caretBlinkPeriod/2 {
		u.c.Line(float32(x0), float32(y0), float32(x1), float32(y1), max(1, u.px(3)), gfx.Alpha(colWhite, 0.6))
		u.c.Line(float32(x0), float32(y0), float32(x1), float32(y1), max(1, u.px(1.5)), colBlack)
	}
}
