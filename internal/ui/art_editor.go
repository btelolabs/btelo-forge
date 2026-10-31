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

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
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
// as Photoshop's do: the key picks the one last used there and Shift with it goes to the next. A place left unbound
// keeps its tools together without a key, which then does something else (R names the layer). The eraser's icon is
// drawn.
var artTools = []struct {
	tool    artTool
	glyph   string
	key     uint16
	unbound bool
}{
	{toolMove, icMoveTool, 'V', false},
	{toolMarquee, icRectSel, 'M', false},
	{toolLasso, icLasso, 'L', false},
	{toolWand, icWand, 'W', false},
	{toolPen, icPen, 'C', false},
	{toolBoard, icBoard, 'J', false},
	{toolPicker, icPicker, 'I', false},
	{toolBrush, icBrush, 'B', false},
	{toolClone, icClone, 'S', false},
	{toolSpot, icSpot, 'S', false},
	{toolHeal, icHeal, 'S', false},
	{toolPatch, icPatch, 'S', false},
	{toolEraser, "", 'E', false},
	{toolBlur, icBlurTool, 'R', true},
	{toolSharpen, icSharpenTool, 'R', true},
	{toolSmudge, icSmudge, 'R', true},
	{toolLiquify, icLiquify, 'R', true},
	{toolDodge, icDodge, 'O', false},
	{toolBurn, icBurn, 'O', false},
	{toolSponge, icSponge, 'O', false},
	{toolGradient, icGradient, 'G', false},
	{toolBucket, icFill, 'G', false},
	{toolMaskGradient, icMaskGradient, 'Y', false},
	{toolText, icText, 'T', false},
	{toolShape, icShapes, 'U', false},
	{toolPencil, icVectorBrush, 'P', false},
	{toolNode, icNodeTool, 'A', false},
	{toolHand, icHand, 'H', false},
	{toolZoom, icZoomTool, 'Z', false},
}

// artToolNames name the tools, by artTool.
var artToolNames = msgs("Move", "Rectangular marquee", "Lasso", "Magic wand", "Eyedropper", "Brush", "Eraser", "Hand", "Zoom",
	"Clone stamp", "Gradient", "Paint bucket", "Shape", "Text", "Pen", "Node", "Artboard", "Vector brush", "Dodge", "Burn", "Sponge", "Blur", "Sharpen", "Smudge",
	"Mask gradient", "Spot healing brush", "Healing brush", "Patch", "Liquify")

// toolDefaultKeys are the keys that pick tool t before others are chosen in Settings: its place's, unless the place
// is unbound.
func toolDefaultKeys(t artTool) []Key {
	for _, e := range artTools {
		if e.tool == t && !e.unbound {
			return keys(key(e.key, ""))
		}
	}
	return nil
}

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
	if ad == nil {
		return // the tab strip above closed this tab, or switched from it, in this frame
	}
	// Free Transform, typing and the pen's curve end, kept, when anything but the canvas, its options and the menus
	// is pressed.
	if u.pressed && !u.blocked && !l.view.Contains(u.pressX, u.pressY) && !l.bar.Contains(u.pressX, u.pressY) &&
		!l.menus.Contains(u.pressX, u.pressY) {
		a.applyTransform(ad)
		a.commitText(ad)
		a.finishPen(ad)
	}
	a.textTick(ad)
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
	a.releaseNavigation()
	a.endLiveVector(ad)
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
	if a.navigate(ad, vp) {
		return
	}
	ad.noteBoardLayer()
	if u.pressedIn(vp) && !u.isHeld(keySpace) && a.zoomHeld == nil {
		a.targetBoardAt(ad, at)
	}
	switch {
	case ad.xf != nil:
		a.transformPointer(ad, vp)
		return
	case a.artTool == toolText:
		a.textPointer(ad, vp)
		return
	case a.artTool == toolPen:
		a.penPointer(ad, vp)
		return
	case a.artTool == toolPencil:
		a.vectorBrushPointer(ad, vp)
		return
	case a.artTool == toolNode:
		a.nodePointer(ad, vp)
		return
	case a.artTool == toolBoard:
		a.boardPointer(ad, vp)
		return
	}
	if n, p, ok := a.gradientEdited(ad); ok {
		a.vectorGradientPointer(ad, vp, n, *p)
		return
	}
	if u.hover(id, vp) {
		switch a.artTool {
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
			b := a.brush
			if a.artTool == toolBrush {
				b.Opacity *= float64(b.Color.A) / 255 // a colour's transparency is the paint's
			}
			s, changed, err := ad.d.BeginStroke(b, a.artTool == toolEraser, cx, cy)
			if err != nil {
				a.artFailed(ad, err)
				u.active = ""
				return
			}
			ad.stroke = s
			ad.touched(changed)
		case toolDodge, toolBurn, toolSponge, toolBlur, toolSharpen, toolSmudge:
			s, changed, err := ad.d.BeginRetouch(a.brush, a.retouch(), cx, cy)
			if err != nil {
				a.artFailed(ad, err)
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
				a.artFailed(ad, err)
			} else {
				ad.d.SelectShape(shape, ad.mode)
				ad.dirty = true
			}
		case toolMove:
			if u.shift && !a.settings.Paint.NoAutoSelect && a.shiftPick(ad, vp, at) {
				u.active = "" // choosing, not moving
				return
			}
			if !a.settings.Paint.NoAutoSelect {
				a.autoSelect(ad, vp, at)
			}
			m, err := ad.d.BeginMove()
			if err != nil {
				a.artFailed(ad, err)
				u.active = ""
				return
			}
			ad.move, ad.moveFrom = m, [2]float64{cx, cy}
		case toolPicker:
			a.pickColor(ad, cx, cy)
		case toolBucket:
			p := &a.settings.Paint
			a.artRun(ad, func() error {
				return ad.d.Bucket(at.X, at.Y, a.brush.Color, p.FillOpacity, p.FillTolerance, p.FillContiguous, p.FillAllLayers)
			})
		case toolGradient, toolShape, toolMaskGradient:
			ad.drawing, ad.drawFrom, ad.drawTo = true, [2]float64{cx, cy}, [2]float64{cx, cy}
			if a.artTool == toolShape {
				ad.snapTo = ad.snapTargetsOf(0, 0, nil)
				from, _ := a.snapped(ad, paint.Pt{X: cx, Y: cy}, ad.snapTo)
				ad.drawFrom, ad.drawTo = pt2(from), pt2(from)
			}
		case toolClone, toolHeal:
			a.beginClone(ad, cx, cy)
		case toolSpot:
			a.beginSpot(ad, cx, cy)
		case toolPatch:
			if ad.d.Selection != nil && ad.d.Selection.At(at.X, at.Y) != 0 {
				ad.patching, ad.patchFrom = true, [2]float64{cx, cy}
			} else {
				ad.lasso = [][2]float64{{cx, cy}}
			}
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
		case toolBrush, toolEraser, toolClone, toolSpot, toolHeal, toolDodge, toolBurn, toolSponge, toolBlur, toolSharpen, toolSmudge, toolLiquify:
			if ad.stroke != nil {
				ad.touched(ad.stroke.To(cx, cy))
				ad.touched(ad.stroke.Hold())
			}
		case toolMarquee:
			ad.selNow = at
			if u.shift && ad.mode == paint.SelectNew {
				// Shift while dragging keeps it square or round, as in Photoshop.
				d := max(abs(ad.selNow.X-ad.selFrom.X), abs(ad.selNow.Y-ad.selFrom.Y))
				ad.selNow = ad.selFrom.Add(image.Pt(d*sign(ad.selNow.X-ad.selFrom.X), d*sign(ad.selNow.Y-ad.selFrom.Y)))
			}
		case toolLasso, toolPatch:
			if len(ad.lasso) == 0 {
				break // the Patch tool dragging the selection
			}
			last := ad.lasso[len(ad.lasso)-1]
			if math.Hypot(cx-last[0], cy-last[1])*ad.zoom >= 2 {
				ad.lasso = append(ad.lasso, [2]float64{cx, cy})
			}
		case toolMove:
			if ad.move != nil {
				dx, dy := int(math.Round(cx-ad.moveFrom[0])), int(math.Round(cy-ad.moveFrom[1]))
				ad.touched(ad.move.To(dx, dy))
			}
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
		case ad.patching:
			a.finishPatch(ad, cx, cy)
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
			if !u.drag.moved && ad.descend != 0 && ad.d.Node(ad.descend) != nil {
				ad.d.Choose(ad.descend) // a click on the chosen group goes into it
			}
			ad.descend = 0
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
	if n := ad.paintedVector(); n != nil {
		// Straight onto the fill or the stroke being coloured, or with Alt the other.
		a.setVectorColour(ad, n, a.pickBackground != a.u.alt, c)
		return
	}
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
			u.c.Outline(box, max(1, u.px(1)), colWhite)
		}
		drawAnts(u, box)
	}
	if len(ad.lasso) > 1 {
		pts := make([][2]float32, len(ad.lasso))
		for i, p := range ad.lasso {
			pts[i][0], pts[i][1] = toScreen(p[0], p[1])
		}
		drawAntsPath(u, pts)
	}
	a.drawFillOverlay(ad, vp)
	a.drawVectorGradientOverlay(ad, vp)
	if ad.over && (a.artTool == toolBrush || a.artTool == toolEraser || a.artTool == toolClone || a.artTool == toolSpot || a.artTool == toolHeal || a.artTool == toolPencil || a.retouching()) && u.active == "" ||
		ad.stroke != nil || ad.vstroke != nil {
		a.drawBrushOutline(ad)
	}
}

// drawAntsPath draws a line through pts as marching ants: black, with white dashes over it measured along its
// length, so it shows whole on any picture.
func drawAntsPath(u *ctx, pts [][2]float32) {
	w := max(1, u.px(1))
	dash := max(u.px(4), 2)
	for i := 1; i < len(pts); i++ {
		u.c.Line(pts[i-1][0], pts[i-1][1], pts[i][0], pts[i][1], w, colBlack)
	}
	var along float32 // the length before this segment
	for i := 1; i < len(pts); i++ {
		x0, y0, x1, y1 := pts[i-1][0], pts[i-1][1], pts[i][0], pts[i][1]
		l := float32(math.Hypot(float64(x1-x0), float64(y1-y0)))
		if l == 0 {
			continue
		}
		// Each white dash, or the part of one, on this segment.
		for p := float32(math.Floor(float64(along/dash))) * dash; p < along+l; p += dash {
			if int(math.Floor(float64(p/dash)))%2 == 0 {
				continue
			}
			a0, a1 := (max(p, along)-along)/l, (min(p+dash, along+l)-along)/l
			u.c.Line(x0+(x1-x0)*a0, y0+(y1-y0)*a0, x0+(x1-x0)*a1, y0+(y1-y0)*a1, w, colWhite)
		}
		along += l
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
	// The places shrink to fit a short window.
	step := min(u.px(36), (r.H-u.px(16))/float32(len(places)))
	for _, place := range places {
		b := gfx.Rect{X: r.X + u.px(8), Y: y, W: u.px(32), H: min(u.px(32), step-u.px(2))}
		a.artToolButton(fmt.Sprintf("art/tool/%c", place), a.slotTool(place), b, true)
		y += step
	}
}

// artToolButton is a tool's button: its symbol, its name and keys as the hint; a click chooses it. Clicking the
// chosen marquee switches its shape; with shared, clicking the chosen tool of a place shared with others goes to the
// next of them. It reports a click.
func (a *App) artToolButton(id string, t artTool, b gfx.Rect, shared bool) bool {
	u := &a.u
	name := u.t(artToolNames[t])
	if t == toolMarquee && a.marqueeEllipse {
		name = u.t("Elliptical marquee")
	}
	label := a.keyHint(toolID(t), toolDefaultKeys(t))
	if label != "" && shared && nextInSlot(t) != t {
		label += ", Shift+" + label // to the next tool with the same key
	}
	clicked := u.iconButton(id, b, a.toolGlyph(t), withKey(name, label), a.artTool == t, true)
	if clicked {
		switch {
		case t == toolMarquee && a.artTool == toolMarquee:
			a.marqueeEllipse = !a.marqueeEllipse // clicking the chosen marquee switches its shape
		case shared && a.artTool == t:
			t = nextInSlot(t) // clicking the chosen tool of a shared place goes to the next
		}
		a.setArtTool(t)
	}
	if t == toolEraser {
		drawEraserGlyph(u, b, a.artTool == toolEraser)
	}
	return clicked
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
		ad.selDrag, ad.lasso = false, nil
	}
}

// drawArtBar is the chosen tool's options.
func (a *App) drawArtBar(ad *artDoc, r, view gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colPanel)
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Bottom() - max(1, u.px(1)), W: r.W, H: max(1, u.px(1))}, colBorder)
	y, h := r.Y+u.px(7), u.px(30)
	x := r.X + u.px(12)
	if !a.over.flow.on { // the narrow panel on the screen shows the chosen tool in its tool box, right above
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
	}
	if a.over.flow.on { // its name and keys, there being little to tell its small symbols apart by
		name := withKey(u.t(artToolNames[a.artTool]), a.keyHint(toolID(a.artTool), toolDefaultKeys(a.artTool)))
		u.label(u.face(12), gfx.Rect{X: x, Y: y, W: r.W - u.px(24), H: u.px(screenToolNameH - 2)}, name, colText, 0)
		y += u.px(screenToolNameH)
	}
	area := gfx.Rect{X: x, Y: y, W: r.Right() - x - u.px(12), H: h}
	a.over.flow.left = area.X
	switch {
	case ad.xf != nil:
		a.drawTransformOptions(ad, area)
		return
	}
	switch a.artTool {
	case toolBrush, toolEraser:
		a.drawBrushOptions(area)
	case toolDodge, toolBurn, toolSponge, toolBlur, toolSharpen, toolSmudge, toolLiquify:
		a.drawRetouchOptions(area)
	case toolMarquee, toolLasso, toolWand, toolPatch:
		a.drawSelectOptions(ad, area)
	case toolBucket:
		a.drawBucketOptions(area)
	case toolMaskGradient:
		a.drawMaskGradientOptions(area)
	case toolGradient:
		if n, p, ok := a.gradientEdited(ad); ok {
			a.drawVectorGradientOptions(ad, n, *p, area)
			break
		}
		a.drawGradientOptions(area)
	case toolClone, toolSpot, toolHeal:
		a.drawCloneOptions(area)
	case toolShape:
		a.drawShapeOptions(ad, area)
	case toolText:
		a.drawTextOptions(ad, area)
	case toolPen:
		a.drawPenOptions(ad, area, u.t("Click for a sharp node, drag for a smooth one; click the first node to close, Enter to finish"))
	case toolPencil:
		a.drawVectorBrushOptions(area)
	case toolNode:
		a.drawNodeOptions(ad, area)
	case toolBoard:
		a.drawBoardOptions(ad, view, area)
	case toolMove:
		a.drawMoveOptions(ad, area)
	case toolZoom:
		a.optHint(area.X, area, u.tf("Click to zoom in, Alt+click to zoom out, drag sideways to zoom; hold Z with any tool to zoom for a moment; %s fits the picture to the window", a.keyHint("draw.fit-on-screen", keys(fitKey))))
	case toolHand:
		a.optHint(area.X, area, u.t("Drag to move around the picture, or hold Space with any tool; the wheel zooms, Shift+wheel scrolls sideways, Ctrl+wheel up and down"))
	case toolPicker:
		a.optHint(area.X, area, u.t("Click to take the foreground colour from the picture, Alt+click for the background colour"))
	}
}

// optRow goes on to a new row of the options for a control w wide that does not fit in what is left of this one, in
// the narrow panel on the screen; the canvas's bar stays one row, each control leaving out what does not fit.
func (a *App) optRow(x *float32, r *gfx.Rect, w float32) {
	f := &a.over.flow
	if f.on && *x > f.left && *x+w > r.Right() {
		*x = f.left
		r.Y += r.H + a.u.px(optRowGap)
		f.rows++
	}
}

// optHint is a line of advice at the end of the options, from x; the narrow panel on the screen has no room for it.
func (a *App) optHint(x float32, r gfx.Rect, text string) {
	if !a.over.flow.on {
		a.u.label(a.u.face(12), gfx.Rect{X: x, Y: r.Y, W: r.Right() - x, H: r.H}, text, colDim, 0)
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
	pr := gfx.Rect{X: r.X, Y: r.Y, W: u.px(46), H: r.H}
	a.drawPresetButton(pr)
	x := pr.Right() + u.px(10)
	for _, tip := range paint.Tips {
		tr := gfx.Rect{X: x, Y: r.Y, W: u.px(30), H: r.H}
		id := fmt.Sprintf("art/tip/%d", tip)
		hot := u.hover(id, tr)
		bg := colSurface
		switch {
		case b.Tip == tip:
			bg = colSelected
		case hot:
			bg = colSurfaceHi
		}
		u.c.Fill(tr, bg)
		drawTip(u, tip, tr, colText)
		u.hint(id, u.t(tipNames[tip]), tr)
		if u.clicked(id, tr) {
			size := b.Size
			b.Tip = tip
			a.chosenBrush(size)
			a.saveBrush()
		}
		x += tr.W + u.px(4)
	}
	x = a.drawTipButtons(x, &r)
	a.optRow(&x, &r, u.px(30))
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
		{"size", u.t("Brush size"), &b.Size, 1, maxBrushSize, func(v float64) string { return fmt.Sprintf("%.0f px", v) }},
		{"hardness", u.t("Hardness"), &b.Hardness, 0, 1, pct},
		{"opacity", u.t("Opacity"), &b.Opacity, 0.01, 1, pct},
		{"flow", u.t("Flow"), &b.Flow, 0.01, 1, pct},
		{"smooth", u.t("Smoothing"), &b.Smooth, 0, 1, pct},
	} {
		a.optRow(&x, &r, u.px(190))
		if x+u.px(190) > r.Right() {
			break
		}
		var done bool
		x, done = a.labelledSlider("art/"+s.id, x, r.Y, r.H, s.label, s.value, s.lo, s.hi, s.format)
		saved = saved || done
		if s.id == "size" {
			x = a.drawSameSize(x, &r) + u.px(16)
		}
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
	box := gfx.Rect{X: m.anchor.X, Y: m.anchor.Bottom() + u.px(4), W: u.px(300), H: u.px(376)}
	u.c.Fill(gfx.Rect{X: box.X, Y: box.Y + u.px(3), W: box.W, H: box.H}, gfx.Alpha(colBlack, 0.35))
	u.c.Fill(box, colSurface)
	u.c.Outline(box, max(1, u.px(1)), colBorder)
	in := box.Inset(u.px(14))
	preview := gfx.Rect{X: in.X, Y: in.Y, W: u.px(70), H: u.px(70)}
	u.c.Fill(preview, colBackground)
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
	// Shape dynamics, under the preview.
	y = max(y, preview.Bottom()) + u.px(10)
	u.heading(gfx.Rect{X: in.X, Y: y, W: in.W, H: u.px(20)}, u.t("Shape dynamics"))
	y += u.px(26)
	for _, s := range []struct {
		id, label string
		value     *float64
		hi        float64
	}{
		{"sizejitter", u.t("Size jitter"), &b.SizeJitter, 1},
		{"anglejitter", u.t("Angle jitter"), &b.AngleJitter, 1},
		{"scatter", u.t("Scatter"), &b.Scatter, 3},
	} {
		_, done := a.labelledSlider("art/tip/"+s.id, in.X, y, u.px(28), s.label, s.value, 0, s.hi, percent)
		saved = saved || done
		y += u.px(34)
	}
	follow := gfx.Rect{X: in.X, Y: y, W: in.W, H: u.px(28)}
	if on := u.checkbox("art/tip/follow", follow, u.t("Turn the tip with the stroke"), b.Follow); on != b.Follow {
		b.Follow = on
		saved = true
	}
	if saved {
		a.saveBrush()
	}
	if u.button("art/tip/reset", gfx.Rect{X: in.X, Y: in.Bottom() - u.px(30), W: u.px(150), H: u.px(30)}, "", u.t("Reset the tip"), ghost, true) {
		b.Angle, b.Round, b.Spacing = 0, 1, 0.1
		b.SizeJitter, b.AngleJitter, b.Scatter, b.Follow = 0, 0, 0, false
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
	a.optRow(&x, &r, u.px(190))
	if a.artTool == toolWand {
		tol := float64(p.WandTolerance)
		nx, done := a.labelledSlider("art/tolerance", x, r.Y, r.H, u.t("Tolerance"), &tol, 0, 255, func(v float64) string { return fmt.Sprintf("%.0f", v) })
		p.WandTolerance = int(math.Round(tol))
		x = nx
		a.optRow(&x, &r, u.px(116))
		if on := u.checkbox("art/contiguous", gfx.Rect{X: x, Y: r.Y, W: u.px(110), H: r.H}, u.t("Contiguous"), p.WandContiguous); on != p.WandContiguous {
			p.WandContiguous, done = on, true
		}
		x += u.px(116)
		a.optRow(&x, &r, u.px(176))
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
		a.optRow(&x, &r, w)
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
	if u.iconButton("art/fit", fit, icFit, u.tf("Fit the canvas to the window (%s)", a.keyHint("draw.fit-on-screen", keys(fitKey))), ad.fit, true) {
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
	if a.navigationKey(k) {
		return true
	}
	if !k.Ctrl && !k.Alt {
		switch {
		case ad.xf != nil && a.transformKeys(ad, k), a.nodeKeys(ad, k), a.boardKeys(ad, k), a.gradientKeys(ad, k):
			return true
		}
	}
	// A command on Shift with a tool's key, as Shift+M adds a layer mask, comes before going to the next tool.
	if k.Shift && !k.Ctrl && !k.Alt && a.runArtCommand(ad, vp, k) {
		return true
	}
	if t, ok := a.toolFor(k); ok {
		if t == toolMarquee && k.Shift {
			a.marqueeEllipse = !a.marqueeEllipse // Shift with the marquee's key switches its shape, as in Photoshop
		}
		a.setArtTool(t)
		return true
	}
	if a.drawingKey(k) || a.opacityKey(ad, k) {
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
	if a.runArtCommand(ad, vp, k) {
		return true
	}
	if k == (Key{Code: keyEnter}) {
		a.startRename(ad) // as R and a double-click on it do
		return true
	}
	return false
}

// startRename puts the chosen layer's or group's name in the layers panel in a box to change it.
func (a *App) startRename(ad *artDoc) {
	if n := ad.d.Node(ad.d.Active); n != nil {
		ad.renaming, ad.rename = n.ID, n.Name
		a.u.focusEdit("art/rename", n.Name, false)
		a.sidePage = sideLayers
	}
}

// runArtCommand runs the canvas command bound to k, when it is enabled; it reports whether one is bound.
func (a *App) runArtCommand(ad *artDoc, vp gfx.Rect, k Key) bool {
	for _, c := range a.artCommands(ad, vp) {
		if a.bound(c.id, c.defaults(), k) {
			if c.enabled == nil || c.enabled() {
				c.run()
			}
			return true
		}
	}
	return false
}

// artFailed says why a canvas command could not be done; a pixel tool or command refused on a vector layer offers
// to rasterize the layer there and then.
func (a *App) artFailed(ad *artDoc, err error) {
	if errors.Is(err, paint.ErrVectorLayer) {
		a.toastActions(a.describe(err), true, []toastAction{{a.u.t("Rasterize"), func() { a.artRun(ad, ad.d.Rasterize) }}})
		return
	}
	a.toast(a.describe(err), true)
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
		a.artFailed(ad, err)
		return
	}
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

// vectorLayerName names a new vector layer as the vector brush names the one it makes.
func (a *App) vectorLayerName() string { return a.u.tf("Drawing %d", len(a.art.d.Layers())+1) }

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
