package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
	"github.com/btelolabs/btelo-video-cut/internal/paint"
)

const (
	icBrush     = "\ue3ae"
	icMoveTool  = "\ue89f"
	icRectSel   = "\ue835"
	icEllipse   = "\ue836"
	icLasso     = "\ue155"
	icWand      = "\ue663"
	icZoomTool  = "\ue8ff"
	icHand      = "\ue925"
	icPicker    = "\ue3b8"
	icNewLayer  = "\ue146"
	icNewGroup  = "\ue2cc"
	icDuplicate = "\ue3e0"
	icUp        = "\ue5d8"
	icDown      = "\ue5db"
	icDownload  = "\ue2c4"
	icGroup     = "\ue2c7"
	icOpen      = "\ue5cf"
	icClosed    = "\ue5cc"
	icCanvas    = "\ue40a"
	icFill      = "\ue23a"
	icMask      = "\ue435"
	icLock      = "\ue897"
	icSwap      = "\ue8d4"
	icSelNew    = "\ue3c6"
	icSelAdd    = "\ue148"
	icSelSub    = "\ue15d"
	icSelAnd    = "\ue3e0"
)

// imageClipboard is a window that can put a picture on the clipboard.
type imageClipboard interface {
	SetClipboardImage(img image.Image) error
}

// artTools are the tool box, in Photoshop's order, with Photoshop's keys. Tools with the same key share a place,
// as Photoshop's do: the key picks the one last used there and Shift with it goes to the next. The eraser's icon is
// drawn.
var artTools = []struct {
	tool  artTool
	glyph string
	key   uint16
}{
	{toolMove, icMoveTool, 'V'},
	{toolBoard, icBoard, 'V'},
	{toolMarquee, icRectSel, 'M'},
	{toolLasso, icLasso, 'L'},
	{toolWand, icWand, 'W'},
	{toolCrop, icCrop, 'C'},
	{toolPicker, icPicker, 'I'},
	{toolBrush, icBrush, 'B'},
	{toolClone, icClone, 'S'},
	{toolEraser, "", 'E'},
	{toolBlur, icBlurTool, 'R'},
	{toolSharpen, icSharpenTool, 'R'},
	{toolSmudge, icSmudge, 'R'},
	{toolDodge, icDodge, 'O'},
	{toolBurn, icBurn, 'O'},
	{toolSponge, icSponge, 'O'},
	{toolGradient, icGradient, 'G'},
	{toolBucket, icFill, 'G'},
	{toolText, icText, 'T'},
	{toolShape, icShapes, 'U'},
	{toolPen, icPen, 'P'},
	{toolPencil, icPencil, 'N'},
	{toolNode, icNodeTool, 'A'},
	{toolHand, icHand, 'H'},
	{toolZoom, icZoomTool, 'Z'},
}

// artToolNames name the tools, by artTool.
var artToolNames = msgs("Move", "Rectangular marquee", "Lasso", "Magic wand", "Crop", "Eyedropper", "Brush", "Eraser", "Hand", "Zoom",
	"Clone stamp", "Gradient", "Paint bucket", "Shape", "Text", "Pen", "Node", "Artboard", "Pencil", "Dodge", "Burn", "Sponge", "Blur", "Sharpen", "Smudge")

// toolPlace is the place of a tool in the tool box, named by the key it has by default.
func toolPlace(t artTool) uint16 {
	for _, e := range artTools {
		if e.tool == t {
			return e.key
		}
	}
	return 0
}

// toolGlyph is a tool's icon as the tool box shows it now.
func (a *App) toolGlyph(t artTool) string {
	switch {
	case t == toolMarquee && a.marqueeEllipse:
		return icEllipse
	}
	for _, e := range artTools {
		if e.tool == t {
			return e.glyph
		}
	}
	return ""
}

// slotTool is the tool the place with key shows: the chosen one when it is there, else the one last used there.
func (a *App) slotTool(key uint16) artTool {
	if toolPlace(a.artTool) == key {
		return a.artTool
	}
	if t, ok := a.toolSlots[key]; ok {
		return t
	}
	for _, e := range artTools {
		if e.key == key {
			return e.tool
		}
	}
	return a.artTool
}

// nextInSlot is the tool after t in its place, round to the first.
func nextInSlot(t artTool) artTool {
	key := toolPlace(t)
	var in []artTool
	for _, e := range artTools {
		if e.key == key {
			in = append(in, e.tool)
		}
	}
	for i, x := range in {
		if x == t {
			return in[(i+1)%len(in)]
		}
	}
	return t
}

// artLayout is where the canvas editor puts its parts, as Photoshop does: the menus and the tool's options along
// the top, the tools on the left, colour and layers on the right.
type artLayout struct {
	menus, bar, tools, view, side, status gfx.Rect
}

func (a *App) artLayout(r gfx.Rect) artLayout {
	u := &a.u
	var l artLayout
	l.menus = gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: u.px(30)}
	l.bar = gfx.Rect{X: r.X, Y: l.menus.Bottom(), W: r.W, H: u.px(44)}
	body := gfx.Rect{X: r.X, Y: l.bar.Bottom(), W: r.W, H: r.Bottom() - l.bar.Bottom()}
	l.tools = gfx.Rect{X: body.X, Y: body.Y, W: u.px(48), H: body.H}
	l.side = gfx.Rect{X: body.Right() - u.px(300), Y: body.Y, W: u.px(300), H: body.H}
	l.status = gfx.Rect{X: l.tools.Right(), Y: body.Bottom() - u.px(26), W: l.side.X - l.tools.Right(), H: u.px(26)}
	l.view = gfx.Rect{X: l.tools.Right(), Y: body.Y, W: l.status.W, H: l.status.Y - body.Y}
	return l
}

// drawArt is a canvas tab.
func (a *App) drawArt(r gfx.Rect) {
	u := &a.u
	ad := a.art
	l := a.artLayout(r)
	u.c.Fill(r, colPanel)
	// Free Transform, typing and the pen's curve end, kept, when anything but the canvas, its options and the menus
	// is pressed.
	if u.pressed && !u.blocked && !l.view.Contains(u.pressX, u.pressY) && !l.bar.Contains(u.pressX, u.pressY) &&
		!l.menus.Contains(u.pressX, u.pressY) {
		a.applyTransform(ad)
		a.commitText(ad)
		a.finishPen(ad)
	}
	a.textTick(ad)
	if a.artTool == toolCrop && ad.xf == nil {
		a.syncCrop(ad)
	}
	a.drawView(ad, l.view)
	a.artPointer(ad, l.view)
	a.drawArtOverlay(ad, l.view)
	a.drawArtTools(l.tools)
	a.drawArtBar(ad, l.bar, l.view)
	a.drawArtMenus(ad, l.menus, l.view)
	a.drawArtSide(ad, l.side)
	a.drawArtStatus(ad, l.status)
	if u.focus == "" && a.menu == nil && !u.blocked { // a dialog takes the keys
		a.artKeys(ad, l.view)
	}
}

// selectMode is how a selection tool's new shape combines: the option bar's choice, or Shift to add, Alt to
// subtract and both to intersect, held as the drag starts.
func (a *App) selectMode() paint.SelectMode {
	u := &a.u
	switch {
	case u.shift && u.alt:
		return paint.SelectIntersect
	case u.shift:
		return paint.SelectAdd
	case u.alt:
		return paint.SelectSubtract
	}
	return paint.SelectMode(a.settings.Paint.SelMode)
}

// artPointer handles the mouse over the canvas for the chosen tool.
func (a *App) artPointer(ad *artDoc, vp gfx.Rect) {
	u := &a.u
	const id = "art/view"
	p := &a.settings.Paint
	cx, cy := ad.toCanvas(vp, u.mx, u.my)
	ad.cursor, ad.over = [2]float64{cx, cy}, vp.Contains(u.mx, u.my)
	at := image.Pt(int(math.Floor(cx)), int(math.Floor(cy)))
	if u.hover(id, vp) && u.wheel != 0 {
		switch {
		case u.ctrl || u.alt:
			ad.zoomAt(vp, u.mx, u.my, ad.zoom*math.Pow(1.2, float64(u.wheel)))
		case u.shift:
			ad.panX -= float64(u.wheel) * 60 / ad.zoom
			ad.fit = false
		default:
			ad.panY -= float64(u.wheel) * 60 / ad.zoom
			ad.fit = false
		}
		u.wheel = 0
	}
	switch {
	case ad.xf != nil:
		a.transformPointer(ad, vp)
		return
	case a.artTool == toolCrop && ad.crop != nil:
		a.cropPointer(ad, vp)
		return
	case a.artTool == toolText:
		a.textPointer(ad, vp)
		return
	case a.artTool == toolPen:
		a.penPointer(ad, vp)
		return
	case a.artTool == toolPencil:
		a.pencilPointer(ad, vp)
		return
	case a.artTool == toolNode:
		a.nodePointer(ad, vp)
		return
	case a.artTool == toolBoard:
		a.boardPointer(ad, vp)
		return
	}
	if u.hover(id, vp) {
		switch a.artTool {
		case toolHand:
			u.cursor = CursorMove
		case toolMove:
			u.cursor = CursorMove
		default:
			u.cursor = CursorCross
		}
	}
	if u.press(id, vp) {
		ad.mode = a.selectMode()
		switch a.artTool {
		case toolBrush, toolEraser:
			s, changed, err := ad.d.BeginStroke(a.brush, a.artTool == toolEraser, cx, cy)
			if err != nil {
				a.toast(a.describe(err), true)
				u.active = ""
				return
			}
			ad.stroke = s
			ad.touched(changed)
		case toolDodge, toolBurn, toolSponge, toolBlur, toolSharpen, toolSmudge:
			s, changed, err := ad.d.BeginRetouch(a.brush, a.retouch(), cx, cy)
			if err != nil {
				a.toast(a.describe(err), true)
				u.active = ""
				return
			}
			ad.stroke = s
			ad.touched(changed)
		case toolMarquee:
			ad.selFrom, ad.selNow, ad.selDrag = at, at, true
		case toolLasso:
			ad.lasso = [][2]float64{{cx, cy}}
		case toolWand:
			shape, err := ad.d.WandMask(at.X, at.Y, p.WandTolerance, p.WandContiguous, p.WandAllLayers)
			if err != nil {
				a.toast(a.describe(err), true)
			} else {
				ad.d.SelectShape(shape, ad.mode)
				ad.dirty = true
			}
		case toolMove:
			m, err := ad.d.BeginMove()
			if err != nil {
				a.toast(a.describe(err), true)
				u.active = ""
				return
			}
			ad.move, ad.moveFrom = m, [2]float64{cx, cy}
		case toolHand:
			ad.panFrom = [4]float64{float64(u.mx), float64(u.my), ad.panX, ad.panY}
		case toolZoom:
			z := ad.zoom * 2
			if u.alt {
				z = ad.zoom / 2
			}
			ad.zoomAt(vp, u.mx, u.my, z)
		case toolPicker:
			a.pickColor(ad, cx, cy)
		case toolBucket:
			p := &a.settings.Paint
			a.artRun(ad, func() error {
				return ad.d.Bucket(at.X, at.Y, a.brush.Color, p.FillOpacity, p.FillTolerance, p.FillContiguous, p.FillAllLayers)
			})
		case toolGradient, toolShape:
			ad.drawing, ad.drawFrom, ad.drawTo = true, [2]float64{cx, cy}, [2]float64{cx, cy}
			if a.artTool == toolShape {
				ad.snapTo = ad.snapTargetsOf(0, 0, nil)
				from, _ := a.snapped(ad, paint.Pt{X: cx, Y: cy}, ad.snapTo)
				ad.drawFrom, ad.drawTo = pt2(from), pt2(from)
			}
		case toolClone:
			a.beginClone(ad, cx, cy)
		}
	}
	if u.dragging(id) {
		if ad.drawing {
			ad.drawTo = [2]float64{cx, cy}
			if a.artTool == toolShape {
				to, _ := a.snapped(ad, paint.Pt{X: cx, Y: cy}, ad.snapTo)
				ad.drawTo = pt2(to)
				a.showShape(ad)
			}
		}
		switch a.artTool {
		case toolBrush, toolEraser, toolClone, toolDodge, toolBurn, toolSponge, toolBlur, toolSharpen, toolSmudge:
			if ad.stroke != nil {
				ad.touched(ad.stroke.To(cx, cy))
			}
		case toolMarquee:
			ad.selNow = at
			if u.shift && ad.mode == paint.SelectNew {
				// Shift while dragging keeps it square or round, as in Photoshop.
				d := max(abs(ad.selNow.X-ad.selFrom.X), abs(ad.selNow.Y-ad.selFrom.Y))
				ad.selNow = ad.selFrom.Add(image.Pt(d*sign(ad.selNow.X-ad.selFrom.X), d*sign(ad.selNow.Y-ad.selFrom.Y)))
			}
		case toolLasso:
			last := ad.lasso[len(ad.lasso)-1]
			if math.Hypot(cx-last[0], cy-last[1])*ad.zoom >= 2 {
				ad.lasso = append(ad.lasso, [2]float64{cx, cy})
			}
		case toolMove:
			if ad.move != nil {
				dx, dy := int(math.Round(cx-ad.moveFrom[0])), int(math.Round(cy-ad.moveFrom[1]))
				ad.touched(ad.move.To(dx, dy))
			}
		case toolHand:
			ad.panX = ad.panFrom[2] - (float64(u.mx)-ad.panFrom[0])/ad.zoom
			ad.panY = ad.panFrom[3] - (float64(u.my)-ad.panFrom[1])/ad.zoom
			ad.fit = false
		case toolPicker:
			a.pickColor(ad, cx, cy)
		}
	}
	if u.letGo(id) {
		switch {
		case ad.drawing:
			a.finishDrawing(ad)
		case ad.stroke != nil:
			ad.touched(ad.stroke.End())
			ad.stroke = nil
		case ad.selDrag:
			ad.selDrag = false
			r := image.Rectangle{Min: ad.selFrom, Max: ad.selNow}.Canon()
			if r.Dx() < 1 || r.Dy() < 1 {
				// A click without a drag: Photoshop drops the selection.
				if ad.mode == paint.SelectNew {
					ad.d.Deselect()
				}
				break
			}
			shape := ad.d.RectMask(r)
			if a.marqueeEllipse {
				shape = ad.d.EllipseMask(r)
			}
			ad.d.SelectShape(ad.d.Feathered(shape, p.Feather), ad.mode)
			ad.dirty = true
		case len(ad.lasso) > 0:
			if len(ad.lasso) >= 3 {
				ad.d.SelectShape(ad.d.Feathered(ad.d.PolygonMask(ad.lasso), p.Feather), ad.mode)
			} else if ad.mode == paint.SelectNew {
				ad.d.Deselect()
			}
			ad.lasso, ad.dirty = nil, true
		case ad.move != nil:
			ad.move.End()
			ad.move = nil
			ad.changedAll()
		}
	}
}

func sign(v int) int {
	if v < 0 {
		return -1
	}
	return 1
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// pickColor takes the foreground (or, with Alt, the background) colour from the picture.
func (a *App) pickColor(ad *artDoc, x, y float64) {
	px, py := int(math.Floor(x)), int(math.Floor(y))
	if f := ad.d.Frame; !f.Empty() && !(image.Point{px, py}).In(f) {
		return // the pasteboard has no colour to take
	}
	c := unpremultiplied(ad.look(px, py))
	if c.A == 0 {
		return
	}
	c.A = 255
	if a.u.alt {
		a.setBackground(c)
		return
	}
	a.setBrushColor(c)
}

// drawArtOverlay draws the selection, the shape being dragged and the brush outline over the canvas.
func (a *App) drawArtOverlay(ad *artDoc, vp gfx.Rect) {
	u := &a.u
	u.c.Push(vp)
	defer u.c.Pop()
	sx, sy := ad.origin(vp)
	toScreen := func(x, y float64) (float32, float32) {
		return float32(sx + x*ad.zoom), float32(sy + y*ad.zoom)
	}
	switch {
	case ad.xf != nil && ad.xf.mode != xfFree:
		a.drawMeshOverlay(ad, vp)
		return
	case ad.xf != nil:
		a.drawBox(ad, vp, ad.xf.box, true)
		return
	case a.artTool == toolCrop && ad.crop != nil:
		a.drawCropOverlay(ad, vp)
		return
	}
	a.drawSelectionOutline(ad, vp)
	a.drawTextOverlay(ad, vp)
	a.drawVectorOverlay(ad, vp)
	a.drawSnapMark(ad, vp)
	a.drawBoardOverlay(ad, vp)
	if ad.selDrag {
		r := image.Rectangle{Min: ad.selFrom, Max: ad.selNow}.Canon()
		x0, y0 := toScreen(float64(r.Min.X), float64(r.Min.Y))
		x1, y1 := toScreen(float64(r.Max.X), float64(r.Max.Y))
		box := gfx.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
		if a.marqueeEllipse {
			u.c.RoundOutline(box, min(box.W, box.H)/2, max(1, u.px(1)), colWhite)
		}
		drawAnts(u, box)
	}
	for i := 1; i < len(ad.lasso); i++ {
		x0, y0 := toScreen(ad.lasso[i-1][0], ad.lasso[i-1][1])
		x1, y1 := toScreen(ad.lasso[i][0], ad.lasso[i][1])
		col := colBlack
		if i%2 == 0 {
			col = colWhite
		}
		u.c.Line(x0, y0, x1, y1, max(1, u.px(1)), col)
	}
	a.drawFillOverlay(ad, vp)
	if ad.over && (a.artTool == toolBrush || a.artTool == toolEraser || a.artTool == toolClone || a.retouching()) && u.active == "" || ad.stroke != nil {
		d := float32(a.brush.Size * ad.zoom)
		if d >= 4 {
			ring := gfx.Rect{X: u.mx - d/2, Y: u.my - d/2, W: d, H: d}
			u.c.RoundOutline(ring, d/2, max(1, u.px(1)), gfx.Alpha(colBlack, 0.6))
			u.c.RoundOutline(ring.Inset(-1), d/2+1, max(1, u.px(1)), gfx.Alpha(colWhite, 0.8))
		}
	}
}

// drawAnts outlines a rectangle with black and white dashes, visible on any picture.
func drawAnts(u *ctx, r gfx.Rect) {
	dash := max(u.px(4), 2)
	w := max(1, u.px(1))
	for i, x := 0, r.X; x < r.Right(); i, x = i+1, x+dash {
		col := colBlack
		if i%2 == 1 {
			col = colWhite
		}
		seg := min(dash, r.Right()-x)
		u.c.Fill(gfx.Rect{X: x, Y: r.Y, W: seg, H: w}, col)
		u.c.Fill(gfx.Rect{X: x, Y: r.Bottom() - w, W: seg, H: w}, col)
	}
	for i, y := 0, r.Y; y < r.Bottom(); i, y = i+1, y+dash {
		col := colBlack
		if i%2 == 1 {
			col = colWhite
		}
		seg := min(dash, r.Bottom()-y)
		u.c.Fill(gfx.Rect{X: r.X, Y: y, W: w, H: seg}, col)
		u.c.Fill(gfx.Rect{X: r.Right() - w, Y: y, W: w, H: seg}, col)
	}
}

func (a *App) drawArtTools(r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colBackground)
	y := r.Y + u.px(8)
	var places []uint16
	for _, e := range artTools {
		if !slices.Contains(places, e.key) {
			places = append(places, e.key)
		}
	}
	// The places shrink to fit a short window, leaving room for the colours.
	step := min(u.px(36), (r.H-u.px(70))/float32(len(places)))
	for _, place := range places {
		t := a.slotTool(place)
		b := gfx.Rect{X: r.X + u.px(8), Y: y, W: u.px(32), H: min(u.px(32), step-u.px(2))}
		name := u.t(artToolNames[t])
		if t == toolMarquee && a.marqueeEllipse {
			name = u.t("Elliptical marquee")
		}
		label := a.keyHint(toolID(t), keys(key(toolPlace(t), "")))
		if label != "" && nextInSlot(t) != t {
			label += ", Shift+" + label // to the next tool with the same key
		}
		id := fmt.Sprintf("art/tool/%c", place)
		if u.iconButton(id, b, a.toolGlyph(t), withKey(name, label), a.artTool == t, true) {
			switch {
			case t == toolMarquee && a.artTool == toolMarquee:
				a.marqueeEllipse = !a.marqueeEllipse // clicking the chosen marquee switches its shape
			case a.artTool == t:
				t = nextInSlot(t) // clicking the chosen tool of a shared place goes to the next
			}
			a.setArtTool(t)
		}
		if t == toolEraser {
			drawEraserGlyph(u, b, a.artTool == toolEraser)
		}
		y += step
	}
	// The foreground and background colours, overlapping as in Photoshop, with swap and default.
	y += u.px(10)
	bg := gfx.Rect{X: r.X + u.px(18), Y: y + u.px(14), W: u.px(22), H: u.px(22)}
	fg := gfx.Rect{X: r.X + u.px(8), Y: y + u.px(4), W: u.px(22), H: u.px(22)}
	for _, s := range []struct {
		id     string
		r      gfx.Rect
		c      color.NRGBA
		target bool
		hint   string
	}{
		{"art/bg", bg, a.backgroundColor(), true, u.t("Background colour: click to choose it")},
		{"art/fg", fg, a.brush.Color, false, u.t("Foreground colour: click to choose it")},
	} {
		u.c.Fill(s.r.Inset(-max(1, u.px(1))), colWhite)
		u.c.Fill(s.r, color.RGBA{s.c.R, s.c.G, s.c.B, 255})
		if a.pickBackground == s.target {
			u.c.Fill(gfx.Rect{X: s.r.X, Y: s.r.Bottom() + u.px(2), W: s.r.W, H: u.px(2)}, colAccent)
		}
		u.hint(s.id, s.hint, s.r)
		if u.clicked(s.id, s.r) {
			a.pickBackground = s.target
			a.picker.hex = ""
		}
	}
	swap := gfx.Rect{X: r.X + u.px(30), Y: y - u.px(4), W: u.px(16), H: u.px(16)}
	if u.iconButton("art/swap", swap, icSwap, withKey(u.t("Swap the colours"), "X"), false, true) {
		a.swapColors()
	}
}

// drawEraserGlyph draws the eraser's icon, a block eraser lying at an angle.
func drawEraserGlyph(u *ctx, r gfx.Rect, on bool) {
	col := colDim
	if on {
		col = colText
	}
	cx, cy, s := r.X+r.W/2, r.Y+r.H/2, u.px(3.4)
	// A rectangle turned 45°: its corners, then the half nearer the paper filled.
	p := [4][2]float32{{cx - 2*s, cy + 0}, {cx, cy - 2*s}, {cx + 2*s, cy}, {cx, cy + 2*s}}
	q := [4][2]float32{p[0], {(p[0][0] + p[1][0]) / 2, (p[0][1] + p[1][1]) / 2}, {(p[3][0] + p[2][0]) / 2, (p[3][1] + p[2][1]) / 2}, p[3]}
	u.c.Triangle(q[0][0], q[0][1], q[1][0], q[1][1], q[2][0], q[2][1], col)
	u.c.Triangle(q[0][0], q[0][1], q[2][0], q[2][1], q[3][0], q[3][1], col)
	w := max(1, u.px(1.1))
	for i := range 4 {
		a, b := p[i], p[(i+1)%4]
		u.c.Line(a[0], a[1], b[0], b[1], w, col)
	}
}

func (a *App) setArtTool(t artTool) {
	a.artTool = t
	if a.toolSlots == nil {
		a.toolSlots = map[uint16]artTool{}
	}
	a.toolSlots[toolPlace(t)] = t
	if ad := a.art; ad != nil {
		a.applyTransform(ad)
		a.commitText(ad)
		a.finishPen(ad)
		ad.selDrag, ad.lasso, ad.crop = false, nil, nil
	}
}

// drawArtBar is the chosen tool's options.
func (a *App) drawArtBar(ad *artDoc, r, view gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colPanel)
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Bottom() - max(1, u.px(1)), W: r.W, H: max(1, u.px(1))}, colBorder)
	y, h := r.Y+u.px(7), u.px(30)
	x := r.X + u.px(12)
	icon := gfx.Rect{X: x, Y: y, W: u.px(30), H: h}
	glyph := a.toolGlyph(a.artTool)
	if a.artTool == toolEraser {
		drawEraserGlyph(u, icon, true)
	} else {
		u.icon(glyph, icon, 18, colText)
	}
	x = icon.Right() + u.px(10)
	u.c.Fill(gfx.Rect{X: x, Y: y + u.px(4), W: max(1, u.px(1)), H: h - u.px(8)}, colBorder)
	x += u.px(12)
	area := gfx.Rect{X: x, Y: y, W: r.Right() - x - u.px(12), H: h}
	switch {
	case ad.xf != nil:
		a.drawTransformOptions(ad, area)
		return
	case a.artTool == toolCrop && ad.crop != nil:
		a.drawCropOptions(ad, area)
		return
	}
	switch a.artTool {
	case toolBrush, toolEraser:
		a.drawBrushOptions(area)
	case toolDodge, toolBurn, toolSponge, toolBlur, toolSharpen, toolSmudge:
		a.drawRetouchOptions(area)
	case toolMarquee, toolLasso, toolWand:
		a.drawSelectOptions(ad, area)
	case toolBucket:
		a.drawBucketOptions(area)
	case toolGradient:
		a.drawGradientOptions(area)
	case toolClone:
		a.drawCloneOptions(area)
	case toolShape:
		a.drawShapeOptions(area)
	case toolText:
		a.drawTextOptions(ad, area)
	case toolPen:
		a.drawPenOptions(area, u.t("Click for a sharp node, drag for a smooth one; click the first node to close, Enter to finish"))
	case toolPencil:
		a.drawPenOptions(area, u.t("Draw freely: the line becomes a smooth curve; end where it began to close it"))
	case toolNode:
		a.drawNodeOptions(ad, area)
	case toolBoard:
		a.drawBoardOptions(ad, view, area)
	case toolMove:
		u.label(u.face(12), area, u.t("Drag to move the layer, or the selected pixels; the arrow keys nudge by a pixel, with Shift by ten"), colDim, 0)
	case toolZoom:
		u.label(u.face(12), area, u.t("Click to zoom in, Alt+click to zoom out; Ctrl+0 fits the picture to the window"), colDim, 0)
	case toolHand:
		u.label(u.face(12), area, u.t("Drag to move around the picture; the wheel scrolls, Shift+wheel sideways, Ctrl+wheel zooms"), colDim, 0)
	case toolPicker:
		u.label(u.face(12), area, u.t("Click to take the foreground colour from the picture, Alt+click for the background colour"), colDim, 0)
	}
}

// slider draws a labelled slider with its value; it reports when a drag ended, for saving.
func (a *App) labelledSlider(id string, x, y, h float32, label string, value *float64, lo, hi float64, format func(float64) string) (float32, bool) {
	u := &a.u
	lw := u.face(12).Measure(label) + u.px(6)
	u.label(u.face(12), gfx.Rect{X: x, Y: y, W: lw, H: h}, label, colDim, 0)
	x += lw
	sr := gfx.Rect{X: x, Y: y + h/2 - u.px(3), W: u.px(84), H: u.px(6)}
	_, done := u.slider(id, sr, value, lo, hi, true)
	x += sr.W + u.px(6)
	u.label(u.face(11.5), gfx.Rect{X: x, Y: y, W: u.px(44), H: h}, format(*value), colText, 0)
	return x + u.px(48), done
}

// drawBrushOptions is the brush tip and its settings, as Photoshop's options bar has them.
func (a *App) drawBrushOptions(r gfx.Rect) {
	u := &a.u
	b := &a.brush
	x := r.X
	for _, tip := range paint.Tips {
		tr := gfx.Rect{X: x, Y: r.Y, W: u.px(30), H: r.H}
		id := fmt.Sprintf("art/tip/%d", tip)
		hot := u.hover(id, tr)
		bg := colSurface
		switch {
		case b.Tip == tip:
			bg = gfx.Mix(colSurface, colAccent, 0.45)
		case hot:
			bg = colSurfaceHi
		}
		u.c.RoundRect(tr, u.px(6), bg)
		drawTip(u, tip, tr, colText)
		u.hint(id, u.t(tipNames[tip]), tr)
		if u.clicked(id, tr) {
			b.Tip = tip
			a.saveBrush()
		}
		x += tr.W + u.px(4)
	}
	shape := gfx.Rect{X: x, Y: r.Y, W: u.px(30), H: r.H}
	if u.iconButton("art/tipshape", shape, "\ue429", u.t("Brush tip shape: angle, roundness and spacing"), a.menuOpen("art/tipshape"), true) {
		a.openMenu("art/tipshape", shape, nil) // drawn by drawTipShape: a panel rather than a list
	}
	x = shape.Right() + u.px(14)
	pct := func(v float64) string { return fmt.Sprintf("%.0f%%", v*100) }
	saved := false
	for _, s := range []struct {
		id, label string
		value     *float64
		lo, hi    float64
		format    func(float64) string
	}{
		{"size", u.t("Brush size"), &b.Size, 1, 500, func(v float64) string { return fmt.Sprintf("%.0f px", v) }},
		{"hardness", u.t("Hardness"), &b.Hardness, 0, 1, pct},
		{"opacity", u.t("Opacity"), &b.Opacity, 0.01, 1, pct},
		{"flow", u.t("Flow"), &b.Flow, 0.01, 1, pct},
		{"smooth", u.t("Smoothing"), &b.Smooth, 0, 1, pct},
	} {
		if x+u.px(190) > r.Right() {
			break
		}
		var done bool
		x, done = a.labelledSlider("art/"+s.id, x, r.Y, r.H, s.label, s.value, s.lo, s.hi, s.format)
		saved = saved || done
	}
	if saved {
		a.saveBrush()
	}
}

// drawTipShape is the brush tip panel opened from the options bar: a preview of the tip, its angle, roundness and
// spacing.
func (a *App) drawTipShape() {
	m := a.menu
	if m == nil || m.id != "art/tipshape" {
		return
	}
	u := &a.u
	b := &a.brush
	box := gfx.Rect{X: m.anchor.X, Y: m.anchor.Bottom() + u.px(4), W: u.px(300), H: u.px(200)}
	u.c.RoundRect(gfx.Rect{X: box.X, Y: box.Y + u.px(3), W: box.W, H: box.H}, u.px(10), gfx.Alpha(colBlack, 0.35))
	u.c.RoundRect(box, u.px(10), colSurface)
	u.c.RoundOutline(box, u.px(10), max(1, u.px(1)), colBorder)
	in := box.Inset(u.px(14))
	preview := gfx.Rect{X: in.X, Y: in.Y, W: u.px(70), H: u.px(70)}
	u.c.RoundRect(preview, u.px(8), colBackground)
	a.drawTipPreview(preview)
	y := in.Y
	x0 := preview.Right() + u.px(14)
	saved := false
	for _, s := range []struct {
		id, label string
		value     *float64
		lo, hi    float64
		format    func(float64) string
	}{
		{"angle", u.t("Angle"), &b.Angle, -180, 180, func(v float64) string { return fmt.Sprintf("%.0f°", v) }},
		{"round", u.t("Roundness"), &b.Round, 0.05, 1, percent},
		{"spacing", u.t("Spacing"), &b.Spacing, 0.01, 1, percent},
	} {
		_, done := a.labelledSlider("art/tip/"+s.id, x0, y, u.px(28), s.label, s.value, s.lo, s.hi, s.format)
		saved = saved || done
		y += u.px(34)
	}
	if saved {
		a.saveBrush()
	}
	if u.button("art/tip/reset", gfx.Rect{X: in.X, Y: in.Bottom() - u.px(30), W: u.px(150), H: u.px(30)}, "", u.t("Reset the tip"), ghost, true) {
		b.Angle, b.Round, b.Spacing = 0, 1, 0.1
		a.saveBrush()
	}
	// Clicks inside the panel stay with it.
	if u.pressed && !box.Contains(u.pressX, u.pressY) && !m.anchor.Contains(u.pressX, u.pressY) && m.opened {
		a.menu = nil
		u.pressed = false
	}
	m.opened = true
}

// drawTipPreview draws the brush tip at a size that fits, with its angle and roundness.
func (a *App) drawTipPreview(r gfx.Rect) {
	b := a.brush
	b.Size = float64(min(r.W, r.H)) * 0.8
	b.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	cx, cy := float64(r.X+r.W/2), float64(r.Y+r.H/2)
	img := a.u.c.Img
	for y := int(r.Y); y < int(r.Bottom()); y++ {
		for x := int(r.X); x < int(r.Right()); x++ {
			c := paint.TipCoverage(b, float64(x)+0.5, float64(y)+0.5, cx, cy)
			if c <= 0 || !(image.Point{x, y}).In(img.Rect) {
				continue
			}
			v := uint8(c * 230)
			if old := img.RGBAAt(x, y); v > old.R {
				img.SetRGBA(x, y, color.RGBA{v, v, v, 255})
			}
		}
	}
}

// drawSelectOptions is the selection tools' options: how a new shape combines, feather, and the wand's tolerance.
func (a *App) drawSelectOptions(ad *artDoc, r gfx.Rect) {
	u := &a.u
	p := &a.settings.Paint
	x := r.X
	for i, m := range []struct {
		glyph, hint string
	}{
		{icSelNew, u.t("New selection")},
		{icSelAdd, withKey(u.t("Add to the selection"), "Shift")},
		{icSelSub, withKey(u.t("Subtract from the selection"), "Alt")},
		{icSelAnd, withKey(u.t("Intersect with the selection"), "Shift+Alt")},
	} {
		if u.iconButton(fmt.Sprintf("art/selmode/%d", i), gfx.Rect{X: x, Y: r.Y, W: u.px(30), H: r.H}, m.glyph, m.hint, p.SelMode == i, true) {
			p.SelMode = i
			a.saveSettings()
		}
		x += u.px(32)
	}
	x += u.px(12)
	if a.artTool == toolWand {
		tol := float64(p.WandTolerance)
		nx, done := a.labelledSlider("art/tolerance", x, r.Y, r.H, u.t("Tolerance"), &tol, 0, 255, func(v float64) string { return fmt.Sprintf("%.0f", v) })
		p.WandTolerance = int(math.Round(tol))
		x = nx
		if on := u.checkbox("art/contiguous", gfx.Rect{X: x, Y: r.Y, W: u.px(110), H: r.H}, u.t("Contiguous"), p.WandContiguous); on != p.WandContiguous {
			p.WandContiguous, done = on, true
		}
		x += u.px(116)
		if on := u.checkbox("art/alllayers", gfx.Rect{X: x, Y: r.Y, W: u.px(170), H: r.H}, u.t("Sample all layers"), p.WandAllLayers); on != p.WandAllLayers {
			p.WandAllLayers, done = on, true
		}
		x += u.px(176)
		if done {
			a.saveSettings()
		}
	} else {
		nx, done := a.labelledSlider("art/feather", x, r.Y, r.H, u.t("Feather"), &p.Feather, 0, 100, func(v float64) string { return fmt.Sprintf("%.0f px", v) })
		if done {
			p.Feather = math.Round(p.Feather)
			a.saveSettings()
		}
		x = nx
	}
	for _, c := range []struct {
		id, label string
		run       func()
	}{
		{"all", u.t("Select all"), ad.d.SelectAll},
		{"none", u.t("Deselect"), ad.d.Deselect},
		{"inverse", u.t("Inverse"), ad.d.InvertSelection},
	} {
		w := u.buttonWidth("", c.label)
		if x+w > r.Right() {
			break
		}
		if u.button("art/sel/"+c.id, gfx.Rect{X: x, Y: r.Y, W: w, H: r.H}, "", c.label, ghost, true) {
			c.run()
			ad.dirty = true
		}
		x += w + u.px(4)
	}
}

var tipNames = msgs("Round", "Square", "Flat", "Chalk")

func percent(v float64) string { return fmt.Sprintf("%.0f%%", v*100) }

// drawTip draws a small picture of a brush tip.
func drawTip(u *ctx, tip paint.Tip, r gfx.Rect, col color.RGBA) {
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	s := u.px(7)
	switch tip {
	case paint.TipSquare:
		u.c.Fill(gfx.Rect{X: cx - s, Y: cy - s, W: 2 * s, H: 2 * s}, col)
	case paint.TipFlat:
		u.c.Line(cx-s, cy+s, cx+s, cy-s, u.px(3.5), col)
	case paint.TipChalk:
		for i := 0; i < 14; i++ {
			a := float64(i) * 2.4
			d := float32(i%4+1) * s / 4
			u.c.Circle(cx+d*float32(math.Cos(a)), cy+d*float32(math.Sin(a)), u.px(1.2), col)
		}
	default:
		u.c.Circle(cx, cy, s, col)
	}
}

func (a *App) drawArtStatus(ad *artDoc, r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colBackground)
	var parts []string
	if f := ad.d.Frame; !f.Empty() {
		parts = append(parts, fmt.Sprintf("%d × %d", f.Dx(), f.Dy()))
	}
	parts = append(parts, fmt.Sprintf("%.0f%%", ad.zoom*100))
	if ad.over {
		parts = append(parts, fmt.Sprintf("%d, %d", int(math.Floor(ad.cursor[0])), int(math.Floor(ad.cursor[1]))))
	}
	if s := ad.d.SelectionBounds(); !s.Empty() {
		parts = append(parts, u.tf("Selection %d × %d", s.Dx(), s.Dy()))
	}
	if ad.d.EditMask {
		parts = append(parts, u.t("Editing the layer mask"))
	}
	switch {
	case ad.saveErr != nil:
		parts = append(parts, u.t("Not saved"))
	case ad.dirty || ad.saving:
		parts = append(parts, u.t("Saving…"))
	default:
		parts = append(parts, u.tf("Saved in %s", filepath.Base(ad.path)))
	}
	u.label(u.face(11.5), r.Inset(u.px(10)), strings.Join(parts, "   ·   "), colDim, 0)
	fit := gfx.Rect{X: r.Right() - u.px(30), Y: r.Y + u.px(1), W: u.px(26), H: r.H - u.px(2)}
	if u.iconButton("art/fit", fit, icFit, u.t("Fit the canvas to the window (Ctrl+0)"), ad.fit, true) {
		ad.fitView()
	}
}

// artKeys are the canvas shortcuts, Photoshop's; the keys it uses do not reach the editor's other shortcuts.
func (a *App) artKeys(ad *artDoc, vp gfx.Rect) {
	u := &a.u
	var rest []Key
	for _, k := range u.keys {
		if !a.artShortcut(ad, vp, k) {
			rest = append(rest, k)
		}
	}
	u.keys = rest
}

// artShortcut runs the command bound to a key; it reports whether there was one.
func (a *App) artShortcut(ad *artDoc, vp gfx.Rect, k Key) bool {
	d := ad.d
	step := 1
	if k.Shift {
		step = 10
	}
	if !k.Ctrl && !k.Alt {
		switch {
		case ad.xf != nil && a.transformKeys(ad, k), a.artTool == toolCrop && ad.crop != nil && a.cropKeys(ad, k),
			a.nodeKeys(ad, k), a.boardKeys(ad, k):
			return true
		}
	}
	if t, ok := a.toolFor(k); ok {
		if t == toolMarquee && k.Shift {
			a.marqueeEllipse = !a.marqueeEllipse // Shift with the marquee's key switches its shape, as in Photoshop
		}
		a.setArtTool(t)
		return true
	}
	if a.drawingKey(k) {
		return true
	}
	// The arrows nudge the layer with the move tool, by a pixel or with Shift ten, as in Photoshop.
	if !k.Ctrl && !k.Alt && a.artTool == toolMove {
		switch k.Code {
		case keyLeft, keyRight, keyUp, keyDown:
			var dx, dy int
			switch k.Code {
			case keyLeft:
				dx = -step
			case keyRight:
				dx = step
			case keyUp:
				dy = -step
			default:
				dy = step
			}
			a.artRun(ad, func() error { return d.Translate(dx, dy) })
			return true
		}
	}
	for _, c := range a.artCommands(ad, vp) {
		if a.bound(c.id, keys(c.key, c.also), k) {
			if c.enabled == nil || c.enabled() {
				c.run()
			}
			return true
		}
	}
	return false
}

// artRun runs a layer or selection command and shows why it could not be done.
func (a *App) artRun(ad *artDoc, fn func() error) {
	if ad.stroke != nil || ad.move != nil || ad.shape != nil || ad.nodes != nil && ad.nodes.s != nil || ad.props != nil {
		return
	}
	a.applyTransform(ad)
	a.commitText(ad)
	a.finishPen(ad)
	if err := fn(); err != nil {
		a.toast(a.describe(err), true)
		return
	}
	ad.changedAll()
}

// artCopy puts the selected part of the chosen layer, or of the picture as it looks, on the clipboard.
func (a *App) artCopy(ad *artDoc, merged bool) {
	cb, ok := a.host.(imageClipboard)
	if !ok {
		a.toast(a.u.t("Pictures cannot be put on the clipboard on this system"), true)
		return
	}
	img, err := ad.d.Copy(merged)
	if err == nil {
		err = cb.SetClipboardImage(img)
	}
	if err != nil {
		a.toast(a.u.tf("Could not copy: %s", a.describe(err)), true)
		return
	}
	a.toast(a.u.tf("Copied %d × %d", img.Rect.Dx(), img.Rect.Dy()), false)
}

func (a *App) artCut(ad *artDoc) {
	cb, ok := a.host.(imageClipboard)
	if !ok {
		a.toast(a.u.t("Pictures cannot be put on the clipboard on this system"), true)
		return
	}
	img, err := ad.d.Cut()
	if err == nil {
		err = cb.SetClipboardImage(img)
	}
	if err != nil {
		a.toast(a.u.tf("Could not cut: %s", a.describe(err)), true)
	}
	ad.changedAll()
}

// artPaste puts the clipboard's picture on a new layer, at the selection's corner or in the middle.
func (a *App) artPaste(ad *artDoc) {
	img, files, _ := a.host.Paste()
	if img == nil {
		for _, f := range files {
			if isImage(f) {
				img, _ = loadImage(f)
				break
			}
		}
	}
	if img == nil {
		a.toast(a.u.t("There is no picture on the clipboard"), false)
		return
	}
	var at *image.Point
	if s := ad.d.SelectionBounds(); !s.Empty() {
		at = &s.Min
	}
	ad.d.Paste(img, a.u.t("Pasted"), at)
	ad.changedAll()
}

// exportArt writes the picture as it looks, or the selection, as a PNG next to the canvas file.
func (a *App) exportArt(ad *artDoc, selectionOnly bool) {
	var img *image.RGBA
	var err error
	if selectionOnly && ad.d.Selection != nil {
		img, err = ad.d.Copy(true)
	} else {
		img, err = ad.d.Picture()
	}
	if err != nil {
		a.toast(a.u.tf("Export failed: %s", a.describe(err)), true)
		return
	}
	a.exportPictures(ad, []namedPicture{{img: img}})
}

// exportTargets are the files pictures export to, next to the canvas file and named after it and them, each a name
// no file has yet.
func exportTargets(ad *artDoc, pics []namedPicture) []string {
	base := strings.TrimSuffix(ad.path, paint.Ext)
	taken := map[string]bool{}
	outs := make([]string, len(pics))
	for i, p := range pics {
		stem := base
		if p.name != "" {
			stem += " - " + safeFileName(p.name)
		}
		out := stem + ".png"
		for n := 2; ; n++ {
			if _, err := os.Stat(out); errors.Is(err, os.ErrNotExist) && !taken[out] {
				break
			}
			out = fmt.Sprintf("%s (%d).png", stem, n)
		}
		taken[out], outs[i] = true, out
	}
	return outs
}

// namedPicture is a picture to export, with the name its file adds to the canvas's (none for the canvas itself).
type namedPicture struct {
	name string
	img  *image.RGBA
}

// exportPictures writes pictures as PNGs next to the canvas file, each under a name no file has yet, encoding them
// away from the window.
func (a *App) exportPictures(ad *artDoc, pics []namedPicture) {
	outs := exportTargets(ad, pics)
	a.background(func(context.Context) {
		var err error
		for i, p := range pics {
			if err = writePNG(outs[i], p.img); err != nil {
				break
			}
		}
		a.post(func() {
			if err != nil {
				a.toast(a.u.tf("Export failed: %s", a.describe(err)), true)
				return
			}
			actions := []toastAction{{a.u.t("Show in folder"), func() { a.host.Reveal(outs[0]) }}}
			msg := a.u.tf("Exported %s", filepath.Base(outs[0]))
			if len(outs) > 1 {
				msg = a.u.tf("Exported %d pictures", len(outs))
			}
			a.toastActions(msg, false, actions)
		})
	})
}

// layerName and groupName number new layers and groups the way Photoshop does.
func (a *App) layerName() string { return a.u.tf("Layer %d", len(a.art.d.Layers())+1) }

func (a *App) groupName() string {
	n := 0
	var walk func(g *paint.Node)
	walk = func(g *paint.Node) {
		for _, c := range g.Children {
			if c.IsGroup() {
				n++
				walk(c)
			}
		}
	}
	walk(a.art.d.Root)
	return a.u.tf("Group %d", n+1)
}
