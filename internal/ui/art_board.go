package ui

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// The artboard tool, as Affinity Designer's: a drag on the canvas makes an artboard, a group with a fixed-size area
// that cuts what is in it to it and exports as a picture of its own; a press on one chooses it, a drag moves it with
// what is in it, its handles resize it. A board being made or resized takes the width and height of the other
// boards when it comes near them, so boards side by side come out the same. The options bar names it, sizes it from
// presets or typed numbers and sets its background.

const icBoard = "\ue069"

// boardEdit is the artboard tool's drag: a new board, the chosen one moved, or one of its handles.
type boardEdit struct {
	grab  int // grabNew, grabMove or a handle
	start xbox
	box   xbox // the board as dragged
	from  [2]float64
	match [2]int // the boards whose width and height it took, or 0
}

// boardPresets are the sizes the artboard tool offers, common for pictures, posts, videos and print, named by
// boardPresetNames.
var (
	boardPresets     = [][2]int{{1920, 1080}, {3840, 2160}, {1080, 1080}, {1080, 1350}, {1080, 1920}, {1280, 720}, {1500, 500}, {2480, 3508}, {1024, 1024}}
	boardPresetNames = msgs("Full HD", "4K", "Square post", "Portrait post", "Story or vertical video", "Video thumbnail",
		"Banner", "A4 at 300 dpi", "Icon")
)

// chosenBoard is the board the artboard tool has chosen, and whether there is one.
func (ad *artDoc) chosenBoard() (paint.Board, bool) {
	if ad.board == 0 {
		return paint.Board{}, false
	}
	return ad.d.Board(ad.board)
}

// boardLabel is where a board's name is drawn, just above its top left corner, on the screen.
func (a *App) boardLabel(ad *artDoc, vp gfx.Rect, b paint.Board) gfx.Rect {
	u := &a.u
	x, y := ad.toScreen(vp, [2]float64{float64(b.Rect.Min.X), float64(b.Rect.Min.Y)})
	w := min(u.face(11.5).Measure(b.Name)+u.px(4), max(u.px(40), float32(float64(b.Rect.Dx())*ad.zoom)))
	return gfx.Rect{X: x, Y: y - u.px(18), W: w, H: u.px(16)}
}

// boardUnder is the board whose label or area is under the pointer, labels first, or 0.
func (a *App) boardUnder(ad *artDoc, vp gfx.Rect) int {
	u := &a.u
	boards := ad.d.Boards()
	for i := len(boards) - 1; i >= 0; i-- {
		if a.boardLabel(ad, vp, boards[i]).Contains(u.mx, u.my) {
			return boards[i].ID
		}
	}
	return ad.d.BoardAt(image.Pt(int(math.Floor(ad.cursor[0])), int(math.Floor(ad.cursor[1]))))
}

// boardPointer is the artboard tool on the canvas.
func (a *App) boardPointer(ad *artDoc, vp gfx.Rect) {
	u := &a.u
	const id = "art/view"
	chosen, has := ad.chosenBoard()
	grab := func() int {
		if has {
			if g := a.boxGrab(ad, vp, boxOf(chosen.Rect), false); g != grabNone {
				return g
			}
		}
		if a.boardUnder(ad, vp) != 0 {
			return grabMove
		}
		return grabNone
	}
	if u.hover(id, vp) {
		u.cursor = grabCursor(grab())
	}
	if u.press(id, vp) {
		g := grab()
		if g == grabMove {
			if under := a.boardUnder(ad, vp); under != 0 && under != ad.board {
				ad.board = under
				chosen, has = ad.chosenBoard()
			}
			if ad.d.BoardOf(ad.d.Active) != ad.board {
				ad.d.Choose(ad.board) // new layers go into it
			}
		}
		if g == grabNone {
			g = grabNew
		}
		skip := 0
		if g != grabNew {
			skip = ad.board
		}
		ad.snapTo = ad.snapTargetsOf(0, skip, nil)
		from := ad.cursor
		if g == grabNew {
			at, _ := a.snapped(ad, paint.Pt{X: from[0], Y: from[1]}, ad.snapTo)
			from = pt2(at)
		}
		e := &boardEdit{grab: g, from: from}
		if has {
			e.start = boxOf(chosen.Rect)
		}
		e.box = e.start
		ad.boardDrag = e
	}
	e := ad.boardDrag
	if u.dragging(id) && e != nil && u.drag.moved {
		p := ad.cursor
		switch g := e.grab; g {
		case grabMove:
			// The board's corner snaps, or else its centre.
			dx, dy := p[0]-e.from[0], p[1]-e.from[1]
			corner := paint.Pt{X: e.start.cx - e.start.w/2 + dx, Y: e.start.cy - e.start.h/2 + dy}
			if at, ok := a.snapped(ad, corner, ad.snapTo); ok {
				dx, dy = dx+at.X-corner.X, dy+at.Y-corner.Y
			} else if at, ok := a.snapped(ad, paint.Pt{X: e.start.cx + dx, Y: e.start.cy + dy}, ad.snapTo); ok {
				dx, dy = at.X-e.start.cx, at.Y-e.start.cy
			}
			e.box.cx = e.start.cx + math.Round(dx)
			e.box.cy = e.start.cy + math.Round(dy)
		case grabNew:
			at, _ := a.snapped(ad, paint.Pt{X: p[0], Y: p[1]}, ad.snapTo)
			w, h := at.X-e.from[0], at.Y-e.from[1]
			if u.shift {
				h = math.Copysign(math.Abs(w), h)
			}
			e.box = xbox{cx: e.from[0] + w/2, cy: e.from[1] + h/2, w: atLeast(math.Abs(w), 1), h: atLeast(math.Abs(h), 1)}
			sx, sy := math.Copysign(1, w), math.Copysign(1, h)
			if u.shift {
				sy = 0 // kept square
			}
			e.box, e.match = a.snapBoardSize(ad, e.box, sx, sy, false, 0)
			if u.shift {
				e.box.h, e.box.cy = e.box.w, e.from[1]+math.Copysign(e.box.w/2, h)
			}
		default:
			corner := boxHandles[g][0] != 0 && boxHandles[g][1] != 0
			at, _ := a.snapped(ad, paint.Pt{X: p[0], Y: p[1]}, ad.snapTo)
			b := e.start.resize(g, pt2(at), u.alt, corner && u.shift)
			b.w, b.h = math.Abs(b.w), math.Abs(b.h)
			e.box, e.match = b, [2]int{}
			if !(corner && u.shift) {
				e.box, e.match = a.snapBoardSize(ad, b, boxHandles[g][0], boxHandles[g][1], u.alt, ad.board)
			}
		}
		e.box = boxOf(e.box.rect()) // a board lies on whole pixels, its corner and its size, as it is dragged too
	}
	if u.letGo(id) && e != nil {
		ad.boardDrag = nil
		switch {
		case !u.drag.moved:
			if e.grab == grabNew {
				ad.board = 0 // a click on nothing lets the board go
			}
		case e.grab == grabNew:
			a.artRun(ad, func() error {
				b, err := ad.d.AddBoard(e.box.rect(), u.tf("Artboard %d", len(ad.d.Boards())+1))
				ad.board = b.ID
				return err
			})
		case e.grab == grabMove:
			dx, dy := int(e.box.cx-e.start.cx), int(e.box.cy-e.start.cy)
			a.artRun(ad, func() error { return ad.d.MoveBoard(chosen.ID, dx, dy) })
		default:
			b := chosen
			b.Rect = e.box.rect()
			a.artRun(ad, func() error { return ad.d.SetBoard(b, "Artboard") })
		}
	}
}

// snapBoardSize draws the size of a board being dragged to the width and the height of the other boards (but
// skip) when it is within snapReach screen pixels of them. sx and sy say which side moves each way: -1 the left or
// top, 1 the right or bottom, 0 neither; the other stays, or with fromCentre the middle. It returns the box and the
// boards whose width and height it took, or 0.
func (a *App) snapBoardSize(ad *artDoc, b xbox, sx, sy float64, fromCentre bool, skip int) (xbox, [2]int) {
	var match [2]int
	if a.settings.Paint.NoSnap {
		return b, match
	}
	reach := float64(a.u.px(snapReach)) / ad.zoom
	fit := func(size float64, side func(paint.Board) int) (float64, int) {
		best, bestD, id := size, reach, 0
		for _, o := range ad.d.Boards() {
			if o.ID == skip {
				continue
			}
			if d := math.Abs(float64(side(o)) - size); d <= bestD {
				best, bestD, id = float64(side(o)), d, o.ID
			}
		}
		return best, id
	}
	// The side that stays, where it is, or the middle.
	place := func(c, size, next, s float64) float64 {
		if fromCentre {
			return c
		}
		return c - s*size/2 + s*next/2
	}
	if sx != 0 {
		w, id := fit(b.w, func(o paint.Board) int { return o.Rect.Dx() })
		b.cx, b.w, match[0] = place(b.cx, b.w, w, sx), w, id
	}
	if sy != 0 {
		h, id := fit(b.h, func(o paint.Board) int { return o.Rect.Dy() })
		b.cy, b.h, match[1] = place(b.cy, b.h, h, sy), h, id
	}
	return b, match
}

// boardKeys are the artboard tool's keys: Delete removes the chosen board, leaving what is drawn on it; Escape lets
// it go; the arrows move it by a pixel, or ten with Shift.
func (a *App) boardKeys(ad *artDoc, k Key) bool {
	if a.artTool != toolBoard {
		return false
	}
	b, ok := ad.chosenBoard()
	if !ok {
		return false
	}
	step := 1
	if k.Shift {
		step = 10
	}
	dx, dy := 0, 0
	switch k.Code {
	case keyDelete, keyBack:
		a.artRun(ad, func() error { return ad.d.DeleteBoard(b.ID) })
		ad.board = 0
		return true
	case keyEscape:
		ad.board = 0
		return true
	case keyLeft:
		dx = -step
	case keyRight:
		dx = step
	case keyUp:
		dy = -step
	case keyDown:
		dy = step
	default:
		return false
	}
	a.artRun(ad, func() error { return ad.d.MoveBoard(b.ID, dx, dy) })
	return true
}

// drawBoardOverlay names every board above its corner and, with the artboard tool, outlines the chosen one with its
// handles, or the board being dragged.
func (a *App) drawBoardOverlay(ad *artDoc, vp gfx.Rect) {
	u := &a.u
	for _, b := range ad.d.Boards() {
		r := a.boardLabel(ad, vp, b)
		col := colDim
		if a.artTool == toolBoard && b.ID == ad.board || ad.d.Active == b.ID {
			col = colAccent
		}
		u.label(u.face(11.5), r, b.Name, col, 0)
		// Its size, above its top right corner, while there is room beside the name.
		size := fmt.Sprintf("%d × %d", b.Rect.Dx(), b.Rect.Dy())
		f := u.face(11)
		rx, _ := ad.toScreen(vp, [2]float64{float64(b.Rect.Max.X), float64(b.Rect.Min.Y)})
		if sw := f.Measure(size); rx-sw > r.X+f.Measure(b.Name)+u.px(10) {
			u.label(f, gfx.Rect{X: rx - sw, Y: r.Y, W: sw, H: r.H}, size, colDim, 1)
		}
	}
	if a.artTool != toolBoard {
		return
	}
	switch e := ad.boardDrag; {
	case e != nil && (e.grab != grabNew || u.drag.moved):
		// The boards whose width or height it took, outlined.
		for _, id := range e.match {
			if b, ok := ad.d.Board(id); ok && id != 0 {
				x0, y0 := ad.toScreen(vp, [2]float64{float64(b.Rect.Min.X), float64(b.Rect.Min.Y)})
				x1, y1 := ad.toScreen(vp, [2]float64{float64(b.Rect.Max.X), float64(b.Rect.Max.Y)})
				u.c.Outline(gfx.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}, max(1, u.px(1.5)), colWarning)
			}
		}
		a.drawBox(ad, vp, e.box, false)
		if e.grab == grabNew || e.grab >= 0 && e.grab < len(boxHandles) {
			r := e.box.rect()
			x, y := ad.toScreen(vp, [2]float64{float64(r.Max.X), float64(r.Max.Y)})
			col := colWhite
			if e.match != [2]int{} {
				col = colWarning // the same size as a board beside it
			}
			u.label(u.face(11.5), gfx.Rect{X: x + u.px(8), Y: y + u.px(4), W: u.px(120), H: u.px(16)}, fmt.Sprintf("%d × %d", r.Dx(), r.Dy()), col, 0)
		}
	default:
		if b, ok := ad.chosenBoard(); ok {
			a.drawBox(ad, vp, boxOf(b.Rect), false)
		}
	}
}

// drawBoardOptions is the artboard tool's options: a preset size (for the chosen board, or a new one in the middle
// of the view), the chosen board's name, size and background, deleting it, and exporting the boards.
func (a *App) drawBoardOptions(ad *artDoc, vp, r gfx.Rect) {
	u := &a.u
	b, has := ad.chosenBoard()
	x := r.X
	labels := make([]string, len(boardPresets))
	for i, p := range boardPresets {
		labels[i] = fmt.Sprintf("%d × %d  %s", p[0], p[1], u.t(boardPresetNames[i]))
	}
	presetLabel := u.t("New artboard")
	if has {
		presetLabel = u.t("Preset size")
	}
	pw := u.px(150)
	if u.button("art/board/presets", gfx.Rect{X: x, Y: r.Y, W: pw, H: r.H}, "", presetLabel, plain, true) {
		anchor := gfx.Rect{X: x, Y: r.Y, W: pw, H: r.H}
		items := make([]menuItem, len(labels))
		for i := range boardPresets {
			p := boardPresets[i]
			items[i] = menuItem{label: labels[i], run: func() { a.boardPreset(ad, vp, p[0], p[1]) }}
		}
		a.openMenu("art/board/presets", anchor, items)
	}
	x += pw + u.px(10)
	if !has {
		if len(ad.d.Boards()) > 0 && x+u.px(170) < r.Right() {
			bw := u.buttonWidth(icDownload, u.t("Export artboards"))
			if u.button("art/board/exportall", gfx.Rect{X: x, Y: r.Y, W: bw, H: r.H}, icDownload, u.t("Export artboards"), plain, true) {
				a.exportBoards(ad, 0)
			}
			x += bw + u.px(10)
		}
		u.label(u.face(12), gfx.Rect{X: x, Y: r.Y, W: r.Right() - x, H: r.H}, u.t("Drag to make an artboard; click one to choose it"), colDim, 0)
		return
	}
	// The name, kept when the box is left.
	if u.focus != "art/board/name" {
		ad.boardName = b.Name
	}
	if _, done := u.textBox("art/board/name", gfx.Rect{X: x, Y: r.Y, W: u.px(140), H: r.H}, &ad.boardName, u.t("Name"), false); done {
		if name := strings.TrimSpace(ad.boardName); name != "" && name != b.Name {
			b.Name = name
			a.artRun(ad, func() error { return ad.d.SetBoard(b, "Artboard") })
		}
	}
	x += u.px(150)
	for _, f := range []struct {
		id, label string
		value     int
		set       func(n int) image.Rectangle
	}{
		{"art/board/w", "W", b.Rect.Dx(), func(n int) image.Rectangle {
			return image.Rect(b.Rect.Min.X, b.Rect.Min.Y, b.Rect.Min.X+n, b.Rect.Max.Y)
		}},
		{"art/board/h", "H", b.Rect.Dy(), func(n int) image.Rectangle {
			return image.Rect(b.Rect.Min.X, b.Rect.Min.Y, b.Rect.Max.X, b.Rect.Min.Y+n)
		}},
	} {
		u.label(u.face(12), gfx.Rect{X: x, Y: r.Y, W: u.px(16), H: r.H}, f.label, colDim, 0)
		x += u.px(18)
		text, editing := ad.boardSize[f.id]
		if u.focus != f.id || !editing {
			text = strconv.Itoa(f.value)
		}
		_, done := u.textBox(f.id, gfx.Rect{X: x, Y: r.Y, W: u.px(64), H: r.H}, &text, "", false)
		if ad.boardSize == nil {
			ad.boardSize = map[string]string{}
		}
		ad.boardSize[f.id] = text
		if done {
			delete(ad.boardSize, f.id)
			if n, err := strconv.Atoi(strings.TrimSpace(text)); err == nil && n != f.value {
				next := b
				next.Rect = f.set(n)
				a.artRun(ad, func() error { return ad.d.SetBoard(next, "Artboard") })
			}
		}
		x += u.px(72)
	}
	x += u.px(4)
	// The background: on with the swatch's colour, which a click sets to the foreground colour.
	on := b.Fill.A > 0
	cw := checkboxWidth(u, u.t("Background"))
	if now := u.checkbox("art/board/fill", gfx.Rect{X: x, Y: r.Y, W: cw, H: r.H}, u.t("Background"), on); now != on {
		next := b
		next.Fill = color.NRGBA{}
		if now {
			next.Fill = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		}
		a.artRun(ad, func() error { return ad.d.SetBoard(next, "Artboard") })
	}
	x += cw + u.px(4)
	sw := gfx.Rect{X: x, Y: r.Y + u.px(5), W: u.px(20), H: r.H - u.px(10)}
	if on {
		u.c.Fill(sw.Inset(-max(1, u.px(1))), colBorder)
		u.c.Fill(sw, color.RGBA{b.Fill.R, b.Fill.G, b.Fill.B, 255})
	} else {
		u.c.Outline(sw, max(1, u.px(1)), colBorder)
		u.c.Line(sw.X, sw.Bottom(), sw.Right(), sw.Y, max(1, u.px(1)), colDanger)
	}
	u.hint("art/board/swatch", u.tf("%s: click to use the foreground colour", u.t("Background")), sw)
	if u.clicked("art/board/swatch", sw) {
		next := b
		next.Fill = a.brush.Color
		a.artRun(ad, func() error { return ad.d.SetBoard(next, "Artboard") })
	}
	x += u.px(32)
	if u.iconButton("art/board/delete", gfx.Rect{X: x, Y: r.Y, W: u.px(30), H: r.H}, icDelete, withKey(u.t("Delete the artboard, keeping what is drawn on it"), "Delete"), false, true) {
		a.boardKeys(ad, Key{Code: keyDelete})
		return
	}
	x += u.px(36)
	bw := u.buttonWidth(icDownload, u.t("Export"))
	if x+bw <= r.Right() && u.button("art/board/export", gfx.Rect{X: x, Y: r.Y, W: bw, H: r.H}, icDownload, u.t("Export"), plain, true) {
		a.exportBoards(ad, b.ID)
	}
}

// boardPreset gives the chosen board a preset size, keeping its corner, or makes a new board of that size in the
// middle of the view.
func (a *App) boardPreset(ad *artDoc, vp gfx.Rect, w, h int) {
	if b, ok := ad.chosenBoard(); ok {
		b.Rect = image.Rectangle{Min: b.Rect.Min, Max: b.Rect.Min.Add(image.Pt(w, h))}
		a.artRun(ad, func() error { return ad.d.SetBoard(b, "Artboard") })
		return
	}
	cx, cy := ad.toCanvas(vp, vp.X+vp.W/2, vp.Y+vp.H/2)
	min := image.Pt(int(math.Round(cx))-w/2, int(math.Round(cy))-h/2)
	a.artRun(ad, func() error {
		b, err := ad.d.AddBoard(image.Rectangle{Min: min, Max: min.Add(image.Pt(w, h))}, a.u.tf("Artboard %d", len(ad.d.Boards())+1))
		ad.board = b.ID
		if err == nil {
			ad.fitTo(vp, b.Rect) // show the new board whole
			ad.fit = false
		}
		return err
	})
}

// exportBoards writes the board id, or every board when id is 0, as PNGs next to the canvas file.
func (a *App) exportBoards(ad *artDoc, id int) {
	var out []namedPicture
	for _, b := range ad.d.Boards() {
		if id != 0 && b.ID != id {
			continue
		}
		img, err := ad.d.BoardPicture(b.ID)
		if err != nil {
			a.toast(a.u.tf("Export failed: %s", a.describe(err)), true)
			return
		}
		out = append(out, namedPicture{name: b.Name, img: img})
	}
	if len(out) == 0 {
		a.toast(a.describe(paint.ErrNoBoard), true)
		return
	}
	a.exportPictures(ad, out)
}

// shadeOutside darkens the view outside the canvas's pages (its frame and boards, in view pixels) within area, as
// the pasteboard around them.
func shadeOutside(view *image.RGBA, area image.Rectangle, pages []image.Rectangle) {
	shade := image.NewUniform(pasteboard)
	for y := area.Min.Y; y < area.Max.Y; y++ {
		// The pages crossing this row, as spans to leave clear, in order.
		var spans [][2]int
		for _, p := range pages {
			// Only the part of a page within the area: one beside it leaves nothing, never a span backwards that
			// would shade past the area's edge.
			if s := [2]int{max(p.Min.X, area.Min.X), min(p.Max.X, area.Max.X)}; y >= p.Min.Y && y < p.Max.Y && s[0] < s[1] {
				spans = append(spans, s)
			}
		}
		slices.SortFunc(spans, func(a, b [2]int) int { return a[0] - b[0] })
		x := area.Min.X
		for _, s := range spans {
			if s[0] > x {
				draw.Draw(view, image.Rect(x, y, s[0], y+1), shade, image.Point{}, draw.Over)
			}
			x = max(x, s[1])
		}
		if x < area.Max.X {
			draw.Draw(view, image.Rect(x, y, area.Max.X, y+1), shade, image.Point{}, draw.Over)
		}
	}
}

// safeFileName is a name made fit to be part of a file name on every system: the characters Windows and macOS keep
// for paths become underscores, and the trailing dots and spaces Windows drops go.
func safeFileName(name string) string {
	s := strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	s = strings.TrimRight(strings.TrimSpace(s), ". ")
	if s == "" {
		return "_"
	}
	return s
}
