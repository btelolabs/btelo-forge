package ui

import (
	"fmt"
	"image/color"
	"math"
	"strconv"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/ox"
	"github.com/btelolabs/btelo-forge/internal/office/pptx"
)

// Symbols of the tool rows.
const (
	icItalic     = "" // format_italic
	icUnderline  = "" // format_underlined
	icTextColor  = "" // format_color_text
	icLineColor  = "" // border_color
	icBullets    = "" // format_list_bulleted
	icNumbers    = "" // format_list_numbered
	icIndentMore = "" // format_indent_increase
	icIndentLess = "" // format_indent_decrease
	icTable      = "" // table_chart
	icNotes      = "" // notes
	icSlideshow  = "" // slideshow
	icNewSlide   = "" // library_add
	icWallpaper  = "" // wallpaper
)

// drawRows draws the two rows of tools: making things and showing on top, the look of what is chosen under them.
func (e *slidesEditor) drawRows(r gfx.Rect) {
	u := &e.a.u
	h := u.px(slidesRowH)
	u.c.Fill(r, colSurface)
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Bottom() - max(1, u.px(1)), W: r.W, H: max(1, u.px(1))}, colBorder)
	e.drawInsertRow(gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: h})
	e.drawFormatRow(gfx.Rect{X: r.X, Y: r.Y + h, W: r.W, H: h})
}

// rowPen lays controls out along a row.
type rowPen struct {
	x, y, h float32
	u       *ctx
}

func (p *rowPen) next(w float32) gfx.Rect {
	r := gfx.Rect{X: p.x, Y: p.y + (p.h-p.u.px(30))/2, W: w, H: p.u.px(30)}
	p.x += w + p.u.px(3)
	return r
}

func (p *rowPen) icon() gfx.Rect { return p.next(p.u.px(32)) }

func (p *rowPen) rule() {
	u := p.u
	p.x += u.px(5)
	u.c.Fill(gfx.Rect{X: p.x, Y: p.y + u.px(8), W: max(1, u.px(1)), H: p.h - u.px(16)}, colBorderHi)
	p.x += u.px(8)
}

func (e *slidesEditor) drawInsertRow(r gfx.Rect) {
	u := &e.a.u
	p := &rowPen{x: r.X + u.px(8), y: r.Y, h: r.H, u: u}
	has := e.slide() != nil
	label := u.t("New slide")
	nr := p.next(u.buttonWidth(icNewSlide, label) + u.px(6))
	if u.button("office/slides/new", nr, icNewSlide, label, plain, true) {
		e.openPopup(&slidesPopup{kind: popupLayouts, anchor: nr})
	}
	if u.iconButton("office/slides/dup", p.icon(), icCopy, u.t("Duplicate slide"), false, has) {
		if ns, err := e.d.DuplicateSlide(e.slide()); err == nil {
			e.goTo(e.d.Number(ns) - 1)
		}
	}
	if u.iconButton("office/slides/del", p.icon(), icDelete, u.t("Delete slide"), false, has) {
		e.deleteSlide(e.slide())
	}
	if has {
		hide := e.slide().Hidden()
		if u.iconButton("office/slides/hide", p.icon(), icHidden, u.t("Hide slide"), hide, true) {
			e.d.SetHidden(e.slide(), !hide)
		}
	}
	p.rule()
	if u.iconButton("office/slides/print", p.icon(), icPrint, u.t("Print (Ctrl+P)"), false, has) {
		e.a.printOffice(e.od)
	}
	if u.iconButton("office/slides/undo", p.icon(), icUndo, u.t("Undo"), false, e.d.CanUndo()) {
		e.undo()
	}
	if u.iconButton("office/slides/redo", p.icon(), icRedo, u.t("Redo"), false, e.d.CanRedo()) {
		e.redo()
	}
	p.rule()
	if u.iconButton("office/slides/textbox", p.icon(), icTextTool, u.t("Text box: drag on the slide"), e.placing == pptx.ShapeTextBox, has) {
		e.endText()
		e.placing = pptx.ShapeTextBox
	}
	sr := p.icon()
	if u.iconButton("office/slides/shapes", sr, icShapeTool, u.t("Shapes"), e.placing != "" && e.placing != pptx.ShapeTextBox, has) {
		e.openPopup(&slidesPopup{kind: popupShapes, anchor: sr})
	}
	if u.iconButton("office/slides/picture", p.icon(), icImage, u.t("Picture…"), false, has) {
		e.a.pickFiles(pickRequest{key: "picture", title: u.t("Insert a picture"), kinds: u.t("Pictures"), exts: pickPictures, multiple: true}, func(paths []string) {
			for _, f := range paths {
				e.addPictureFile(f)
			}
		})
	}
	tr := p.icon()
	if u.iconButton("office/slides/table", tr, icTable, u.t("Table"), false, has) {
		e.openPopup(&slidesPopup{kind: popupTable, anchor: tr})
	}
	p.rule()
	label = u.t("Arrange")
	ar := p.next(u.buttonWidth("", label) + u.px(16))
	if u.button("office/slides/arrange", ar, "", label, plain, len(e.sel) > 0) {
		e.a.openMenu("office/slides/arrange", ar, e.arrangeMenu())
	}
	br := p.icon()
	if u.iconButton("office/slides/bg", br, icWallpaper, u.t("Slide background"), false, has) {
		bg, _ := e.d.BackgroundColor(e.slide())
		e.openPopup(&slidesPopup{kind: popupColor, anchor: br, current: bg, pick: func(c color.RGBA) { e.d.SetBackground(e.slide(), c) }})
	}
	// The right end: the view and the show.
	end := r.Right() - u.px(8)
	right := func(w float32) gfx.Rect {
		end -= w
		out := gfx.Rect{X: end, Y: r.Y + (r.H-u.px(30))/2, W: w, H: u.px(30)}
		end -= u.px(3)
		return out
	}
	label = u.t("Slideshow")
	if u.button("office/slides/show", right(u.buttonWidth(icSlideshow, label)+u.px(6)), icSlideshow, label, primary, len(e.d.Slides) > 0) {
		e.startShow(false)
	}
	end -= u.px(6)
	if u.iconButton("office/slides/notes-toggle", right(u.px(32)), icNotes, u.t("Show or hide the notes"), !e.noNotes, true) {
		e.noNotes = !e.noNotes
	}
	if u.iconButton("office/slides/zoomin", right(u.px(32)), icZoomIn, u.t("Zoom in"), false, true) {
		e.setZoom(e.view.Scale / float64(u.s) * 1.25)
	}
	zoomR := right(u.px(58))
	text := fmt.Sprintf("%d%%", int(e.view.Scale/float64(u.s)/(96.0/72.0)*100+0.5))
	if u.hover("office/slides/zoomfit", zoomR) {
		u.c.Fill(zoomR, colSurfaceHi)
		u.cursor = CursorHand
	}
	u.label(u.face(12.5), zoomR, text, colText, 0.5)
	u.hint("office/slides/zoomfit", u.t("Fit the slide to the window"), zoomR)
	if u.clicked("office/slides/zoomfit", zoomR) {
		e.zoom, e.panX, e.panY = 0, 0, 0
	}
	if u.iconButton("office/slides/zoomout", right(u.px(32)), icZoomOut, u.t("Zoom out"), false, true) {
		e.setZoom(e.view.Scale / float64(u.s) / 1.25)
	}
}

func (e *slidesEditor) setZoom(z float64) {
	e.zoom = min(max(z, 0.05), 16)
	e.a.requestFrame()
}

func (e *slidesEditor) arrangeMenu() []menuItem {
	u := &e.a.u
	two, three := len(e.sel) >= 2, len(e.sel) >= 3
	groups := false
	for _, k := range e.chosen() {
		groups = groups || k.Kind == pptx.KindGroup
	}
	return []menuItem{
		{label: u.t("Bring to front"), run: func() { e.reorder(pptx.ToFront) }},
		{label: u.t("Bring forward"), run: func() { e.reorder(pptx.Forward) }},
		{label: u.t("Send backward"), run: func() { e.reorder(pptx.Backward) }},
		{label: u.t("Send to back"), run: func() { e.reorder(pptx.ToBack) }},
		menuSep,
		{label: u.t("Group"), key: "Ctrl+G", disabled: !two, run: e.group},
		{label: u.t("Ungroup"), key: "Ctrl+Shift+G", disabled: !groups, run: e.ungroup},
		menuSep,
		{label: u.t("Align left"), run: func() { e.align("l") }},
		{label: u.t("Align centre"), run: func() { e.align("c") }},
		{label: u.t("Align right"), run: func() { e.align("r") }},
		{label: u.t("Align top"), run: func() { e.align("t") }},
		{label: u.t("Align middle"), run: func() { e.align("m") }},
		{label: u.t("Align bottom"), run: func() { e.align("b") }},
		menuSep,
		{label: u.t("Distribute across"), disabled: !three, run: func() { e.distribute(true) }},
		{label: u.t("Distribute down"), disabled: !three, run: func() { e.distribute(false) }},
	}
}

func (e *slidesEditor) drawFormatRow(r gfx.Rect) {
	u := &e.a.u
	a := e.a
	p := &rowPen{x: r.X + u.px(8), y: r.Y, h: r.H, u: u}
	s := e.slide()
	words := e.text != nil
	shapes := e.chosen()
	enabled := words || len(shapes) > 0
	run := e.currentRun()
	para := e.currentPara()
	// The font and its size.
	fonts := slidesFonts
	sel := -1
	for i, f := range fonts {
		if f == run.Latin {
			sel = i
		}
	}
	if sel < 0 && run.Latin != "" {
		fonts = append(append([]string(nil), fonts...), run.Latin)
		sel = len(fonts) - 1
	}
	fr := p.next(u.px(150))
	if enabled {
		a.dropdown("office/slides/font", fr, fonts, sel, nil, func(i int) { e.setRun(pptx.SetFont(fonts[i])) })
	} else {
		e.idleField(fr, u.t("Font"))
	}
	sizes := make([]string, len(slidesSizes))
	selSize := -1
	for i, v := range slidesSizes {
		sizes[i] = strconv.FormatFloat(v, 'f', -1, 64)
		if v == run.Size {
			selSize = i
		}
	}
	if selSize < 0 {
		sizes = append(sizes, strconv.FormatFloat(run.Size, 'f', -1, 64))
		selSize = len(sizes) - 1
	}
	sr := p.next(u.px(64))
	if enabled {
		a.dropdown("office/slides/size", sr, sizes, selSize, nil, func(i int) {
			if v, err := strconv.ParseFloat(sizes[i], 64); err == nil {
				e.setRun(pptx.SetSize(v))
			}
		})
	} else {
		e.idleField(sr, "")
	}
	if u.iconButton("office/slides/smaller", p.icon(), icTextSmaller, u.t("Smaller"), false, enabled) {
		e.setRun(pptx.SetSize(max(run.Size-2, 1)))
	}
	if u.iconButton("office/slides/bigger", p.icon(), icTextBigger, u.t("Larger"), false, enabled) {
		e.setRun(pptx.SetSize(run.Size + 2))
	}
	p.rule()
	if u.iconButton("office/slides/bold", p.icon(), icBold, u.t("Bold (Ctrl+B)"), enabled && run.Bold, enabled) {
		e.toggleRun("b")
	}
	if u.iconButton("office/slides/italic", p.icon(), icItalic, u.t("Italic (Ctrl+I)"), enabled && run.Italic, enabled) {
		e.toggleRun("i")
	}
	if u.iconButton("office/slides/under", p.icon(), icUnderline, u.t("Underline (Ctrl+U)"), enabled && run.Under, enabled) {
		e.toggleRun("u")
	}
	cr := p.icon()
	if e.swatchButton("office/slides/ink", cr, icTextColor, u.t("Text colour"), run.Color, enabled) {
		e.openPopup(&slidesPopup{kind: popupColor, anchor: cr, current: run.Color, pick: func(c color.RGBA) { e.setRun(pptx.SetColor(c)) }})
	}
	p.rule()
	aligns := []struct{ id, glyph, hint, val string }{{"left", icAlignLeft, msgs("Align text left")[0], "l"}, {"mid", icAlignMid, msgs("Centre text")[0], "ctr"}, {"right", icAlignRight, msgs("Align text right")[0], "r"}}
	for _, al := range aligns {
		on := enabled && (para.Align == al.val || para.Align == "" && al.val == "l")
		if u.iconButton("office/slides/align/"+al.id, p.icon(), al.glyph, u.t(al.hint), on, enabled) {
			e.setPara(pptx.SetAlign(al.val))
		}
	}
	p.rule()
	bullet := para.BuKind == "char" || para.BuKind == "blip"
	if u.iconButton("office/slides/bullets", p.icon(), icBullets, u.t("Bullets"), enabled && bullet, enabled) {
		if bullet {
			e.setPara(pptx.SetBullet("none"))
		} else {
			e.setPara(pptx.SetBullet("bullet"))
		}
	}
	number := para.BuKind == "auto"
	if u.iconButton("office/slides/numbers", p.icon(), icNumbers, u.t("Numbering"), enabled && number, enabled) {
		if number {
			e.setPara(pptx.SetBullet("none"))
		} else {
			e.setPara(pptx.SetBullet("number"))
		}
	}
	if u.iconButton("office/slides/less", p.icon(), icIndentLess, u.t("Less indent"), false, enabled) {
		e.changeLevel(-1)
	}
	if u.iconButton("office/slides/more", p.icon(), icIndentMore, u.t("More indent"), false, enabled) {
		e.changeLevel(1)
	}
	p.rule()
	// The shape's own look.
	var fill, line color.RGBA
	width := 0.0
	if len(shapes) > 0 {
		fill, line, width = e.d.Look(s, shapes[0])
	}
	ids := e.sel
	has := len(shapes) > 0
	fr2 := p.icon()
	if e.swatchButton("office/slides/fill", fr2, icFill, u.t("Shape fill"), fill, has) {
		e.openPopup(&slidesPopup{kind: popupColor, anchor: fr2, current: fill, none: true, pick: func(c color.RGBA) { e.d.SetFill(s, ids, c) }})
	}
	lr := p.icon()
	if e.swatchButton("office/slides/line", lr, icLineColor, u.t("Shape outline"), line, has) {
		e.openPopup(&slidesPopup{kind: popupColor, anchor: lr, current: line, none: true, pick: func(c color.RGBA) { e.d.SetLine(s, ids, c, true, -1) }})
	}
	wr := p.next(u.px(74))
	if has {
		opts := []string{"0.25", "0.5", "0.75", "1", "1.5", "2.25", "3", "4.5", "6"}
		at := -1
		for i, o := range opts {
			if v, _ := strconv.ParseFloat(o, 64); math.Abs(v-width) < 0.01 {
				at = i
			}
		}
		labels := make([]string, len(opts))
		for i, o := range opts {
			labels[i] = u.tf("%s pt", o)
		}
		a.dropdown("office/slides/width", wr, labels, at, nil, func(i int) {
			v, _ := strconv.ParseFloat(opts[i], 64)
			e.d.SetLine(s, ids, color.RGBA{}, false, v)
		})
	} else {
		e.idleField(wr, "")
	}
	u.c.Text(u.face(12), p.x+u.px(2), r.Y+(r.H-u.face(12).Height())/2, u.t("Opacity"), colDim)
	p.x += u.face(12).Measure(u.t("Opacity")) + u.px(6)
	op := e.opacityOf(shapes)
	or := p.next(u.px(90))
	if has {
		if changed, done := u.slider("office/slides/opacity", or, &op, 0, 1, true); changed {
			e.d.EndBurst()
			e.d.SetOpacity(s, ids, op)
		} else if done {
			e.d.EndBurst()
		}
	} else {
		v := 1.0
		u.slider("office/slides/opacity", or, &v, 0, 1, false)
	}
}

// opacityOf reads the first shape's opacity.
func (e *slidesEditor) opacityOf(list []*pptx.Shape) float64 {
	if len(list) == 0 {
		return 1
	}
	if list[0].Kind == pptx.KindPicture {
		if am := list[0].Node.Path("p:blipFill", "a:blip", "a:alphaModFix"); am != nil {
			if v, err := strconv.ParseFloat(am.Attr("amt"), 64); err == nil {
				return v / 100000
			}
		}
		return 1
	}
	fill, _, _ := e.d.Look(e.slide(), list[0])
	if fill.A == 0 {
		return 1
	}
	return float64(fill.A) / 255
}

func (e *slidesEditor) changeLevel(by int) {
	e.setPara(func(pPr *ox.Node) {
		level := 0
		if v, err := strconv.Atoi(pPr.Attr("lvl")); err == nil {
			level = v
		}
		pptx.SetLevel(min(max(level+by, 0), 8))(pPr)
	})
}

// idleField is a dropdown with nothing to act on.
func (e *slidesEditor) idleField(r gfx.Rect, text string) {
	u := &e.a.u
	u.c.Fill(r, colSurface)
	u.label(u.face(12.5), gfx.Rect{X: r.X + u.px(10), Y: r.Y, W: r.W - u.px(14), H: r.H}, text, colFaint, 0)
}

// swatchButton is a symbol with the colour under it.
func (e *slidesEditor) swatchButton(id string, r gfx.Rect, glyph, hint string, col color.RGBA, enabled bool) bool {
	u := &e.a.u
	hit := u.iconButton(id, gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H - u.px(3)}, glyph, hint, false, enabled)
	bar := gfx.Rect{X: r.X + u.px(7), Y: r.Bottom() - u.px(5), W: r.W - u.px(14), H: u.px(3)}
	switch {
	case !enabled:
		u.c.Fill(bar, colBorderHi)
	case col.A == 0:
		u.c.Outline(bar, max(1, u.px(1)), colDim)
	default:
		c := col
		c.A = 255
		u.c.Fill(bar, c)
	}
	return hit
}

// Kinds of popup panel.
const (
	popupColor = iota
	popupLayouts
	popupShapes
	popupTable
)

// slidesPopup is a panel open over the tab.
type slidesPopup struct {
	kind    int
	anchor  gfx.Rect
	opened  bool
	current color.RGBA
	none    bool // offers no colour
	pick    func(color.RGBA)
	hex     string
}

func (e *slidesEditor) openPopup(p *slidesPopup) {
	if e.popup != nil && e.popup.kind == p.kind && e.popup.anchor == p.anchor {
		e.popup = nil
		return
	}
	e.popup = p
}

// slidesPalette is the standard colours under the theme's.
var slidesPalette = []uint32{0xc00000, 0xff0000, 0xffc000, 0xffff00, 0x92d050, 0x00b050, 0x00b0f0, 0x0070c0, 0x002060, 0x7030a0}

var slidesShapeList = []string{"rect", "roundRect", "ellipse", "triangle", "rtTriangle", "diamond", "parallelogram", "trapezoid", "pentagon", "hexagon", "octagon",
	"star5", "star4", "star8", "rightArrow", "leftArrow", "upArrow", "downArrow", "leftRightArrow", "upDownArrow", "chevron", "homePlate",
	"wedgeRectCallout", "wedgeRoundRectCallout", "wedgeEllipseCallout", "cloudCallout", "line", "straightConnector1", "bentConnector3", "curvedConnector3",
	"plus", "donut", "noSmoking", "blockArc", "pie", "can", "cube", "heart", "lightningBolt", "cloud", "sun", "moon", "smileyFace", "flowChartProcess", "flowChartDecision",
	"flowChartTerminator", "flowChartDocument", "flowChartInputOutput", "bracketPair", "bracePair", "frame", "foldedCorner"}

// popupRect places a panel of a size under its anchor, inside the window.
func (e *slidesEditor) popupRect(p *slidesPopup, w, h float32) gfx.Rect {
	u := &e.a.u
	x := min(p.anchor.X, e.a.screen.W-w-u.px(8))
	y := p.anchor.Bottom() + u.px(4)
	if y+h > e.a.screen.H {
		y = max(p.anchor.Y-h-u.px(4), u.px(4))
	}
	return gfx.Rect{X: max(x, u.px(4)), Y: y, W: w, H: h}
}

func (e *slidesEditor) drawPopup() {
	u := &e.a.u
	p := e.popup
	var box gfx.Rect
	switch p.kind {
	case popupColor:
		box = e.popupColors(p)
	case popupLayouts:
		box = e.popupLayouts(p)
	case popupShapes:
		box = e.popupShapes(p)
	case popupTable:
		box = e.popupTable(p)
	}
	for _, k := range u.keys {
		if k.Code == keyEscape {
			e.popup = nil
		}
	}
	if p.opened && u.pressed && !box.Contains(u.pressX, u.pressY) {
		e.popup = nil
		u.pressed = false
	}
	p.opened = true
}

func (e *slidesEditor) panel(box gfx.Rect) {
	u := &e.a.u
	u.c.Fill(gfx.Rect{X: box.X, Y: box.Y + u.px(3), W: box.W, H: box.H}, gfx.Alpha(colBlack, 0.35))
	u.c.Fill(box, colSurface)
	u.c.Outline(box, max(1, u.px(1)), colBorder)
}

func (e *slidesEditor) popupColors(p *slidesPopup) gfx.Rect {
	u := &e.a.u
	cell, gap := u.px(22), u.px(4)
	pad := u.px(10)
	s := e.slide()
	theme := []color.RGBA{}
	if s != nil {
		theme = s.Layout.Master.Theme.Palette()
	}
	cols := 10
	rows := 7
	w := float32(cols)*(cell+gap) - gap + 2*pad
	h := float32(rows)*(cell+gap) + u.px(42) + 2*pad
	if p.none {
		h += u.px(30)
	}
	box := e.popupRect(p, w, h)
	e.panel(box)
	y := box.Y + pad
	choose := func(c color.RGBA) {
		e.popup = nil
		if p.pick != nil {
			p.pick(c)
		}
	}
	swatch := func(id string, r gfx.Rect, c color.RGBA) {
		u.c.Fill(r, c)
		hot := u.hover(id, r)
		edge := colBorderHi
		if hot {
			edge = colText
			u.cursor = CursorHand
		}
		u.c.Outline(r, max(1, u.px(1)), edge)
		if u.clicked(id, r) {
			choose(c)
		}
	}
	u.label(u.face(12), gfx.Rect{X: box.X + pad, Y: y, W: w, H: u.px(18)}, u.t("Theme colours"), colDim, 0)
	y += u.px(22)
	for i, c := range theme {
		if i >= cols {
			break
		}
		x := box.X + pad + float32(i)*(cell+gap)
		swatch(fmt.Sprintf("office/slides/theme/%d", i), gfx.Rect{X: x, Y: y, W: cell, H: cell}, c)
		for k, mix := range []float32{0.8, 0.6, 0.4, -0.25, -0.5} {
			var t color.RGBA
			if mix > 0 {
				t = gfx.Mix(c, colWhite, mix)
			} else {
				t = gfx.Mix(c, colBlack, -mix)
			}
			swatch(fmt.Sprintf("office/slides/theme/%d/%d", i, k), gfx.Rect{X: x, Y: y + float32(k+1)*(cell+gap/2) + gap, W: cell, H: cell}, t)
		}
	}
	y += 6*(cell+gap/2) + gap*2
	u.label(u.face(12), gfx.Rect{X: box.X + pad, Y: y, W: w, H: u.px(18)}, u.t("Standard colours"), colDim, 0)
	y += u.px(20)
	for i, v := range slidesPalette {
		swatch(fmt.Sprintf("office/slides/std/%d", i), gfx.Rect{X: box.X + pad + float32(i)*(cell+gap), Y: y, W: cell, H: cell}, gfx.Hex(v))
	}
	y += cell + gap*2
	if p.none {
		if u.button("office/slides/nocolor", gfx.Rect{X: box.X + pad, Y: y, W: w - 2*pad, H: u.px(28)}, "", u.t("No colour"), plain, true) {
			choose(color.RGBA{})
		}
	}
	return box
}

func (e *slidesEditor) popupLayouts(p *slidesPopup) gfx.Rect {
	u := &e.a.u
	var list []*pptx.Layout
	for _, m := range e.d.Masters {
		list = append(list, m.Layouts...)
	}
	const cols = 3
	cw := u.px(150)
	ratio := float32(e.d.H / e.d.W)
	ch := cw*ratio + u.px(24)
	pad := u.px(10)
	rows := (len(list) + cols - 1) / cols
	rows = min(rows, int((e.a.screen.H-u.px(120))/(ch+pad)))
	w := cols*(cw+pad) + pad
	h := float32(rows)*(ch+pad) + pad
	box := e.popupRect(p, w, h)
	e.panel(box)
	for i, l := range list {
		if i/cols >= rows {
			break
		}
		x := box.X + pad + float32(i%cols)*(cw+pad)
		y := box.Y + pad + float32(i/cols)*(ch+pad)
		r := gfx.Rect{X: x, Y: y, W: cw, H: ch}
		id := "office/slides/layout/" + l.Part
		pic := gfx.Rect{X: x, Y: y, W: cw, H: cw * ratio}
		u.c.Image(e.layoutPicture(l, int(cw)), pic)
		edge := colBorderHi
		if u.hover(id, r) {
			edge = colAccent
			u.cursor = CursorHand
		}
		u.c.Outline(pic, max(1, u.px(1)), edge)
		name := l.Name
		if name == "" {
			name = l.Type
		}
		u.label(u.face(11.5), gfx.Rect{X: x, Y: pic.Bottom(), W: cw, H: u.px(24)}, name, colDim, 0.5)
		if u.clicked(id, r) {
			e.popup = nil
			e.addSlide(l)
		}
	}
	return box
}

func (e *slidesEditor) popupShapes(p *slidesPopup) gfx.Rect {
	u := &e.a.u
	const cols = 10
	cell := u.px(34)
	pad := u.px(8)
	rows := (len(slidesShapeList) + cols - 1) / cols
	w := cols*cell + 2*pad
	h := float32(rows)*cell + 2*pad
	box := e.popupRect(p, w, h)
	e.panel(box)
	size := int(u.px(24))
	for i, name := range slidesShapeList {
		r := gfx.Rect{X: box.X + pad + float32(i%cols)*cell, Y: box.Y + pad + float32(i/cols)*cell, W: cell, H: cell}
		id := "office/slides/shape/" + name
		if u.hover(id, r) {
			u.c.Fill(r, colSelected)
			u.cursor = CursorHand
		}
		img := e.presetIcon(name, size)
		u.c.Blit(img, int(r.X+(r.W-float32(size))/2), int(r.Y+(r.H-float32(size))/2), false)
		if u.clicked(id, r) {
			e.popup = nil
			e.endText()
			e.placing = name
		}
	}
	return box
}

func (e *slidesEditor) popupTable(p *slidesPopup) gfx.Rect {
	u := &e.a.u
	const cols, rows = 10, 8
	cell := u.px(22)
	pad := u.px(10)
	w := cols*cell + 2*pad
	h := rows*cell + 2*pad + u.px(24)
	box := e.popupRect(p, w, h)
	e.panel(box)
	gx, gy := box.X+pad, box.Y+pad+u.px(24)
	hc, hr := 0, 0
	if u.mx >= gx && u.my >= gy && u.mx < gx+cols*cell && u.my < gy+rows*cell {
		hc, hr = int((u.mx-gx)/cell)+1, int((u.my-gy)/cell)+1
	}
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			cr := gfx.Rect{X: gx + float32(c)*cell + u.px(1), Y: gy + float32(r)*cell + u.px(1), W: cell - u.px(2), H: cell - u.px(2)}
			if c < hc && r < hr {
				u.c.Fill(cr, colAccent)
			} else {
				u.c.Outline(cr, max(1, u.px(1)), colBorderHi)
			}
		}
	}
	text := u.t("Choose a size")
	if hc > 0 {
		text = u.tf("%d by %d", hr, hc)
		u.cursor = CursorHand
	}
	u.label(u.face(12.5), gfx.Rect{X: box.X + pad, Y: box.Y + pad, W: w, H: u.px(20)}, text, colDim, 0)
	grid := gfx.Rect{X: gx, Y: gy, W: cols * cell, H: rows * cell}
	if u.clicked("office/slides/tablegrid", grid) && hc > 0 {
		e.popup = nil
		s := e.slide()
		b := pptx.Box{X: e.d.W * 0.1, Y: e.d.H * 0.25, W: e.d.W * 0.8, H: float64(hr) * 34}
		if id, err := e.d.AddTable(s, hr, hc, b, nil); err == nil {
			e.sel = []int{id}
		}
	}
	return box
}
