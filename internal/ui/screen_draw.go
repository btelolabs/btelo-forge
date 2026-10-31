package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"slices"
	"time"

	"golang.org/x/image/draw"

	"github.com/btelolabs/btelo-forge/internal/capture"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// ---- Drawing on the screen

// Drawing on the screen is a tool of its own, apart from recording: it can be used while recording, for a
// screenshot, or for showing something to the people watching. It is the canvas editor itself, at work on a canvas
// the size of a display and shown at its own size over it, saved as canvases are; only its tool bars are gathered
// into one tall panel. Whatever the canvas editor gains, it gains here too.

// screenView are the tools that only look around the canvas, which on the screen stays where it is drawn.
var screenView = []artTool{toolHand, toolZoom}

// screenLeavesOut are the canvas commands, by id, with no place on the screen: those that move the view, and a new
// canvas, which would open in the main window. Everything else, and every command to come, works as on the canvas.
var screenLeavesOut = map[string]bool{
	"draw.new-canvas": true, "draw.zoom-in": true, "draw.zoom-out": true, "draw.fit-on-screen": true, "draw.100pct": true,
	"draw.show-the-video-behind-the-drawing": true,
}

// toolOffered reports whether the canvas being worked on offers tool t: every tool, but on the screen not those that
// move the view.
func (a *App) toolOffered(t artTool) bool {
	return a.art == nil || !a.art.onScreen || !slices.Contains(screenView, t)
}

// screenDraw is the drawing on a display, shown by three windows: the picture, which is in recordings and
// screenshots and takes the mouse while drawing; the marks the tools make over it (the selection, handles, the
// brush's outline), left out of them and letting the mouse through; and the panel, left out too, in front of both
// and with the keyboard while drawing. The picture and the panel are one surface to the editor: their input is put
// together, so a press over the panel works the panel whichever of the two windows Windows gave it to, and never
// reaches the picture.
type screenDraw struct {
	d       capture.Display
	tab     *doc // stands in for a tab while the canvas editor works for the screen
	ad      *artDoc
	pic     *surface
	marks   *surface
	panel   *surface
	drawing bool // the picture takes the mouse; otherwise it goes through to the programs below
	// The editor's state of its own on the screen, apart from the canvas tab's.
	tool   artTool
	slots  map[uint16]artTool
	menu   *popupMenu
	dialog artDialog
	adjust *adjustDialog
	style  *styleDialog
	scroll float32 // the layers list
	// The panel: where its top left is on the display, once placed; where in it a drag of its grip took hold; where
	// it is, and the parts of it a press on which keeps the transform or the text under way.
	at     image.Point
	placed bool
	grab   gfx.Rect
	area   gfx.Rect
	keeps  [2]gfx.Rect
	rows   int // the rows the chosen tool's options take
	again  bool
	// The mouse: which window it is over, and where each last said it was.
	fromPic      bool
	picAt, panAt [2]float32
	overPanel    bool
	look         brushLook // the brush as the panel last showed it
	wait         time.Duration
	marksShown   diffShow
	panelShown   diffShow
}

// brushLook is what the panel shows of the brush, so it is drawn again when the brush changes from elsewhere: the
// canvas tab, an MCP tool.
type brushLook struct {
	size   float64
	tip    paint.Tip
	preset string
	color  color.NRGBA
	tool   artTool
}

// barFlow is the options bar laid out in rows, for the narrow panel on the screen: the left edge of its rows and how
// many it took.
type barFlow struct {
	on   bool
	left float32
	rows int
}

// screenPanelScale is the size of everything on the panel, its dialogs and menus too, against the main window's: the
// point of drawing on the screen is what is left of it.
const screenPanelScale = 0.8

// The panel's measures, in logical pixels at that scale: its size, the height of its rows of menus and commands, the room a tool's
// button takes, and the gap between rows of options.
const (
	screenPanelW    = 384
	screenPanelH    = 960
	screenMenusH    = 30
	screenCommandH  = 42
	screenToolStep  = 36
	screenToolNameH = 22
	optRowGap       = 4
)

// pointerDisplay is the display the mouse pointer is on, or the chosen one.
func (a *App) pointerDisplay() (capture.Display, bool) {
	if host, ok := a.host.(overlayHost); ok {
		at := host.Pointer()
		for _, d := range a.rec.displays {
			if at.In(displayBounds(d)) {
				return d, true
			}
		}
	}
	return a.chosenDisplay()
}

// toggleScreenDraw starts drawing on the display the pointer is on, or switches between drawing and giving the
// mouse back while keeping what was drawn.
func (a *App) toggleScreenDraw() {
	if sd := a.over.draw; sd != nil {
		a.setScreenDrawing(!sd.drawing)
		return
	}
	a.startScreenDraw()
}

func (a *App) startScreenDraw() {
	sc := &a.over
	d, ok := a.pointerDisplay()
	if !ok {
		a.toast(a.u.t("No screen to draw on was found"), true)
		return
	}
	fail := func(err error, opened ...*surface) {
		for _, s := range opened {
			s.close()
		}
		a.toast(a.u.tf("Cannot draw on the screen: %s", a.describe(err)), true)
	}
	pd, err := paint.NewFramed(d.Width, d.Height, a.u.tf("Layer %d", 1), color.NRGBA{})
	if err != nil {
		fail(err)
		return
	}
	pd.View = image.Rect(0, 0, d.Width, d.Height)
	// A canvas's file: named now and written with the first change, so a drawing with nothing on it leaves none.
	path, err := a.capturePath(categoryCreative, a.u.t("Canvas %s"), paint.Ext, time.Now())
	if err != nil {
		fail(err)
		return
	}
	b := displayBounds(d)
	pic, err := a.openSurface(OverlayOptions{Bounds: b})
	if err != nil {
		fail(err)
		return
	}
	marks, err := a.openSurface(OverlayOptions{Bounds: b, ClickThrough: true, Excluded: true})
	if err != nil {
		fail(err, pic)
		return
	}
	panel, err := a.openSurface(OverlayOptions{Bounds: b, Excluded: true})
	if err != nil {
		fail(err, marks, pic)
		return
	}
	// At its own size about the display's middle, the canvas's pixels are the display's.
	ad := &artDoc{d: pd, path: path, zoom: 1, panX: float64(d.Width) / 2, panY: float64(d.Height) / 2, renaming: -1, layerHeld: -1, onScreen: true}
	sd := &screenDraw{d: d, tab: &doc{art: ad}, ad: ad, pic: pic, marks: marks, panel: panel, tool: toolBrush, rows: 1}
	sc.draw = sd
	// The recording's bar opens again, in front of the new windows.
	if sc.bar != nil {
		sc.bar.s.close()
		sc.bar = nil
	}
	sd.drawing = false
	a.setScreenDrawing(true)
}

// setScreenDrawing makes the picture take the mouse, or gives it back to the programs below; the drawing stays
// either way, and a drag under way ends. The keyboard goes with it, which drawTick sees to.
func (a *App) setScreenDrawing(on bool) {
	sd := a.over.draw
	if sd == nil || sd.drawing == on {
		return
	}
	if !on {
		a.onScreen(sd, func() { a.dropScreenDrag(sd.ad) })
		sd.ad.over = false // no brush outline while the mouse is elsewhere
	}
	sd.drawing = on
	sd.pic.o.SetClickThrough(!on)
	sd.pic.shown = false // shown again: the near-transparent floor that catches the mouse comes or goes
	sd.marks.shown, sd.panel.shown = false, false
}

// dropScreenDrag ends what a drag on the drawing was doing, keeping it, as letting go would.
func (a *App) dropScreenDrag(ad *artDoc) {
	u := &a.u
	switch {
	case ad.drawing:
		a.finishDrawing(ad)
	case ad.stroke != nil:
		ad.touched(ad.stroke.End())
		ad.stroke = nil
	case ad.move != nil:
		ad.move.End()
		ad.move = nil
		ad.changedAll()
	}
	ad.selDrag, ad.lasso, ad.drawing = false, nil, false
	u.active = ""
}

// closeScreenDraw removes the drawing from the screen; it is saved as a canvas is, so it can be opened again.
func (a *App) closeScreenDraw() {
	sc := &a.over
	sd := sc.draw
	if sd == nil {
		return
	}
	a.onScreen(sd, func() {
		a.dropScreenDrag(sd.ad)
		a.applyTransform(sd.ad)
		a.commitText(sd.ad)
		a.finishPen(sd.ad)
		if sd.ad.dirty {
			if err := a.saveArt(sd.ad, true); err != nil {
				a.toast(a.u.tf("The canvas could not be saved: %s", a.describe(err)), true)
			} else {
				a.toast(a.u.tf("Saved %s", sd.ad.path), false)
			}
		}
	})
	sd.panel.o.TakeKeyboard(false)
	sd.panel.close()
	sd.marks.close()
	sd.pic.close()
	sc.draw = nil
}

// onScreen runs fn with the canvas editor working for the drawing on the screen, as an immediate-mode interface sets
// its current context: the panel's state stands in for the main window's, the drawing for the tab's canvas, and the
// screen's tool, menu and dialogs for the canvas tab's. All is put back after.
func (a *App) onScreen(sd *screenDraw, fn func()) {
	s := sd.panel
	u, tab, menu, screen, tool, slots, zoom := a.u, a.doc, a.menu, a.screen, a.artTool, a.toolSlots, a.zoomHeld
	dialog, adjust, style, scroll := a.dialog, a.adjust, a.style, a.layersScroll
	a.u, a.doc, a.menu, a.artTool, a.toolSlots, a.zoomHeld = s.u, sd.tab, sd.menu, sd.tool, sd.slots, nil
	a.dialog, a.adjust, a.style, a.layersScroll = sd.dialog, sd.adjust, sd.style, sd.scroll
	a.screen = gfx.Rect{W: float32(s.c.Img.Rect.Dx()), H: float32(s.c.Img.Rect.Dy())}
	defer func() {
		s.u, sd.menu, sd.tool, sd.slots = a.u, a.menu, a.artTool, a.toolSlots
		sd.dialog, sd.adjust, sd.style, sd.scroll = a.dialog, a.adjust, a.style, a.layersScroll
		a.u, a.doc, a.menu, a.screen, a.artTool, a.toolSlots, a.zoomHeld = u, tab, menu, screen, tool, slots, zoom
		a.dialog, a.adjust, a.style, a.layersScroll = dialog, adjust, style, scroll
	}()
	fn()
}

// screenView is the canvas's viewport on the screen: the whole display, where canvas pixels are the display's.
func (sd *screenDraw) view() gfx.Rect {
	return gfx.Rect{W: float32(sd.d.Width), H: float32(sd.d.Height)}
}

// pointerGone is below any place on a display: where a window says the pointer is once it has left.
const pointerGone = -1 << 19

// input puts together what the panel's window and the picture's say: the pointer is where the window it is over
// or pressed in says, and the keys and events of both are one stream.
func (sd *screenDraw) input(p, q Input) Input {
	moved := func(in Input, at [2]float32) bool {
		return in.X != at[0] || in.Y != at[1] || len(in.Pointer) > 0
	}
	picMoved, panelMoved := moved(q, sd.picAt), moved(p, sd.panAt)
	sd.picAt, sd.panAt = [2]float32{q.X, q.Y}, [2]float32{p.X, p.Y}
	switch {
	case p.Down:
		sd.fromPic = false
	case q.Down:
		sd.fromPic = true
	case q.X < pointerGone:
		sd.fromPic = false
	case p.X < pointerGone:
		sd.fromPic = true
	case picMoved != panelMoved:
		sd.fromPic = picMoved
	}
	in := p
	if sd.fromPic {
		in.X, in.Y = q.X, q.Y
	}
	in.Down = p.Down || q.Down
	in.Pointer = append(slices.Clone(q.Pointer), p.Pointer...)
	in.Keys = append(slices.Clone(q.Keys), p.Keys...)
	in.Up = append(slices.Clone(q.Up), p.Up...)
	in.Typed = append(slices.Clone(q.Typed), p.Typed...)
	in.Wheel += q.Wheel
	in.Inactive = p.Inactive && q.Inactive
	return in
}

func (a *App) drawTick(sd *screenDraw) {
	if sd.pic.o.Closed() || sd.panel.o.Closed() {
		a.closeScreenDraw() // Alt+F4 on the panel, which has the keyboard
		return
	}
	panelIn, picIn := sd.panel.o.Input(), sd.pic.o.Input()
	// Letting go on the drawing puts the panel and the recording's bar back in front, and the keyboard with the panel.
	// Never during the press: taking the foreground from another program then would end the picture's hold on the
	// mouse, and the stroke with it.
	if !picIn.Down && slices.ContainsFunc(picIn.Pointer, func(p Pointer) bool { return !p.Down }) {
		sd.panel.o.Raise()
		if a.over.bar != nil {
			a.over.bar.s.o.Raise()
		}
	}
	in := sd.input(panelIn, picIn)
	_, changed := a.beginSurfaceWith(sd.panel, in)
	act := screenNothing
	a.onScreen(sd, func() { act = a.screenFrame(sd, in) })
	switch act {
	case screenDrawAgain:
		a.setScreenDrawing(true)
	case screenLetGo:
		a.setScreenDrawing(false)
	case screenShot:
		a.screenshotRegion(shotFile)
	case screenClose:
		a.closeScreenDraw()
		return
	}
	u := &sd.panel.u
	sd.panel.o.SetCursor(u.cursor)
	sd.pic.o.SetCursor(u.cursor)
	typing := u.focus != ""
	sd.panel.o.TextInput(typing, u.imeAt[0], u.imeAt[1], u.imeAt[2])
	sd.panel.o.TakeKeyboard(sd.drawing || typing) // a box in the panel takes it while the mouse is elsewhere too
	a.showScreenPicture(sd)
	// The marks follow the pointer and whatever changed.
	if changed || typing || !sd.marks.shown {
		a.onScreen(sd, func() {
			u := &a.u
			own := u.c
			u.c = sd.marks.c
			u.c.ClearTo(color.RGBA{})
			if sd.drawing || sd.ad.xf != nil || sd.ad.d.Selection != nil || sd.ad.pen != nil || sd.ad.nodes != nil {
				a.drawArtOverlay(sd.ad, sd.view())
			}
			u.c = own
		})
		sd.marksShown.present(sd.marks)
	}
}

// screenFrame is one frame of the editor on the screen, in the order drawArt keeps on a canvas tab: what ends with a
// press elsewhere, the typing, the tool under the pointer, then the panel, its keys, dialogs and menu. It returns
// what a click on the panel asks for.
func (a *App) screenFrame(sd *screenDraw, in Input) screenAction {
	u := &a.u
	ad, vp := sd.ad, sd.view()
	area := a.placePanel(sd)
	modal := a.menu != nil || a.dialog.kind != dialogNone || a.adjust != nil || a.style != nil
	over := area.Contains(u.mx, u.my)
	if u.pressed && !modal && area.Contains(u.pressX, u.pressY) && !sd.keeps[0].Contains(u.pressX, u.pressY) && !sd.keeps[1].Contains(u.pressX, u.pressY) {
		a.applyTransform(ad)
		a.commitText(ad)
		a.finishPen(ad)
	}
	a.textTick(ad)
	stop := false
	if sd.drawing {
		// Over the panel, or under a dialog, the tool does nothing; a stroke under way goes on across the panel.
		u.blocked = modal || over && u.active == ""
		if u.rightAt != nil && !u.blocked && u.active == "" {
			stop = true // the right button gives the mouse back; the drawing stays
		} else {
			a.artPointer(ad, vp)
			ad.over = ad.over && !u.blocked
		}
		a.endLiveVector(ad)
		u.blocked = false
	}
	if due, ok := a.artSaveDue(ad); ok && !time.Now().Before(due) {
		a.saveArt(ad, false)
	}
	act := screenNothing
	look := brushLook{size: a.brush.Size, tip: a.brush.Tip, preset: a.brushKey(), color: a.brush.Color, tool: a.artTool}
	held := u.down && area.Contains(u.pressX, u.pressY) // a slider or the grip dragged beyond the panel
	if in.acted() || over || sd.overPanel || held || modal || u.focus != "" || sd.again || look != sd.look || !sd.panel.shown || len(a.toasts) > 0 {
		sd.again = false
		act = a.screenPanel(sd)
		sd.look = look
		u.end()
		sd.panelShown.present(sd.panel)
	} else {
		u.end()
	}
	sd.overPanel = over
	if stop {
		return screenLetGo
	}
	return act
}

// placePanel is where the panel is on its display: in its top right corner until it is dragged.
func (a *App) placePanel(sd *screenDraw) gfx.Rect {
	u := &a.u
	dw, dh := float32(sd.d.Width), float32(sd.d.Height)
	margin := u.px(12)
	r := gfx.Rect{W: min(u.px(screenPanelW*screenPanelScale), dw), H: min(u.px(screenPanelH*screenPanelScale), dh-2*margin)}
	if !sd.placed {
		sd.at = image.Pt(int(dw-r.W-margin), int(margin))
	}
	r.X = min(max(float32(sd.at.X), 0), dw-r.W)
	r.Y = min(max(float32(sd.at.Y), 0), dh-r.H)
	sd.area = r
	return r
}

// screenAction is what a click on the panel asks for, done once the panel is drawn.
type screenAction int

const (
	screenNothing   screenAction = iota
	screenDrawAgain              // take the mouse again, as choosing a tool while the mouse is elsewhere does
	screenLetGo                  // give the mouse back to the programs below
	screenShot                   // take a screenshot, the drawing in it
	screenClose                  // stop drawing and remove the drawing
)

// screenPanel draws the panel, the canvas editor's bars one under the other: the menus; the grip, undo, redo and
// wiping, the mouse, a screenshot and closing; the tool box; the chosen tool's options, in as many rows as they
// take; and the colours and layers to the bottom. Then come the canvas's keys, its dialogs and open menu and the
// hint under the pointer. It returns what a click on the panel asks for.
func (a *App) screenPanel(sd *screenDraw) (act screenAction) {
	u := &a.u
	full := u.s
	u.s *= screenPanelScale
	defer func() { a.u.s = full }() // the tools on the drawing keep their full size
	c := u.c
	ad := sd.ad
	c.ClearTo(color.RGBA{})
	p := sd.area
	modal := a.menu != nil || a.dialog.kind != dialogNone || a.adjust != nil || a.style != nil
	u.blocked = modal
	c.Fill(p, colPanel)
	menus := gfx.Rect{X: p.X, Y: p.Y, W: p.W, H: u.px(screenMenusH)}
	a.drawArtMenus(ad, menus, sd.view())
	// The commands: the grip drags the panel about its display.
	row := gfx.Rect{X: p.X, Y: menus.Bottom(), W: p.W, H: u.px(screenCommandH)}
	h := u.px(32)
	y := row.Y + (row.H-h)/2
	x := row.X + u.px(8)
	grip := gfx.Rect{X: x, Y: y, W: u.px(14), H: h}
	drawGrip(u, grip)
	u.hint("screen/grip", u.t("Drag to move the panel"), grip)
	if u.hover("screen/grip", grip) {
		u.cursor = CursorMove
	}
	if u.press("screen/grip", grip) {
		sd.grab = gfx.Rect{X: u.mx - p.X, Y: u.my - p.Y}
	}
	if u.dragging("screen/grip") {
		sd.at, sd.placed = image.Pt(int(u.mx-sd.grab.X), int(u.my-sd.grab.Y)), true
	}
	x = grip.Right() + u.px(6)
	step := u.px(34)
	button := func(id, glyph, hint string, on, enabled bool) bool {
		b := gfx.Rect{X: x, Y: y, W: h, H: h}
		x += step
		return u.iconButton("screen/"+id, b, glyph, hint, on, enabled)
	}
	for _, cmd := range []struct{ id, glyph string }{{"draw.undo", icUndo}, {"draw.redo", icRedo}} {
		if ac, ok := a.artCommand(ad, cmd.id); ok {
			if button(cmd.id, cmd.glyph, withKey(ac.label, a.keyHint(ac.id, ac.defaults())), false, ac.enabled()) {
				ac.run()
			}
		}
	}
	if button("wipe", icDelete, u.t("Wipe the drawing"), false, true) {
		a.artRun(ad, func() error { ad.d.Wipe(u.tf("Layer %d", 1)); return nil })
	}
	// The right end: the mouse, a screenshot, closing.
	x = row.Right() - u.px(8) - 3*step
	mouse := u.t("Give the mouse back to the programs below")
	if !sd.drawing {
		mouse = u.t("Draw on the screen")
	}
	if button("mouse", icMouse, withKey(mouse, a.hotkeyLabel(hotkeyDraw)), !sd.drawing, true) {
		act = screenLetGo
		if !sd.drawing {
			act = screenDrawAgain
		}
	}
	if button("shot", icCamera, withKey(u.t("Take a screenshot with the drawing"), a.hotkeyLabel(hotkeyScreenshotFile)), false, !a.rec.shooting && a.over.pick == nil) {
		act = screenShot
	}
	if button("close", icClose, u.t("Stop drawing and remove the drawing"), false, true) {
		act = screenClose
	}
	// The tool box, in as many rows as its places take, as on the canvas where a place holds the tools sharing a key.
	var places []uint16
	for _, e := range artTools {
		if a.toolOffered(e.tool) && !slices.Contains(places, e.key) {
			places = append(places, e.key)
		}
	}
	cols := max(int((p.W-u.px(16))/u.px(screenToolStep)), 1)
	top := row.Bottom() + u.px(4)
	for i, place := range places {
		b := gfx.Rect{X: p.X + u.px(8) + float32(i%cols)*u.px(screenToolStep), Y: top + float32(i/cols)*u.px(screenToolStep), W: u.px(32), H: u.px(32)}
		if a.artToolButton(fmt.Sprintf("art/tool/%c", place), a.slotTool(place), b, true) && !sd.drawing {
			act = screenDrawAgain
		}
	}
	tools := float32((len(places)+cols-1)/cols) * u.px(screenToolStep)
	// The chosen tool's options, as on the canvas, in rows.
	opts := gfx.Rect{X: p.X, Y: top + tools + u.px(2), W: p.W, H: float32(sd.rows)*u.px(30+optRowGap) + u.px(10+screenToolNameH)}
	sd.keeps = [2]gfx.Rect{menus, opts}
	f := &a.over.flow
	f.on, f.rows = true, 1
	a.drawArtBar(ad, opts, sd.view())
	f.on = false
	if f.rows != sd.rows {
		sd.rows, sd.again = f.rows, true
	}
	// The colours and layers, as on the canvas.
	side := gfx.Rect{X: p.X, Y: opts.Bottom(), W: p.W, H: p.Bottom() - opts.Bottom()}
	a.drawArtSide(ad, side)
	c.Outline(p, max(1, u.px(1)), colBorder)
	u.blocked = false
	if u.focus == "" && !modal {
		a.artKeys(ad, sd.view())
	}
	a.drawArtDialog()
	a.drawAdjustDialog()
	a.drawStyleDialog()
	if a.dialog.kind == dialogNone && (a.adjust == nil || a.menuOpen("adjust/page")) && a.style == nil {
		a.drawOpenMenu()
	}
	a.drawToasts()
	sd.wait = u.drawTip(a.screen)
	return act
}

// artCommand is the canvas command with id, when the canvas offers it.
func (a *App) artCommand(ad *artDoc, id string) (artCommand, bool) {
	for _, c := range a.artCommands(ad, gfx.Rect{}) {
		if c.id == id {
			if c.enabled == nil {
				c.enabled = func() bool { return true }
			}
			return c, true
		}
	}
	return artCommand{}, false
}

// showScreenPicture brings the picture window up to date with what changed in the drawing.
func (a *App) showScreenPicture(sd *screenDraw) {
	ad, s := sd.ad, sd.pic
	dirty := ad.stale
	ad.stale = image.Rectangle{}
	if ad.view == nil || !s.shown {
		dirty = s.c.Img.Rect
		ad.view = s.c.Img // the view of the canvas on the screen is the picture window's own pixels
	}
	dirty = dirty.Intersect(s.c.Img.Rect)
	if dirty.Empty() {
		return
	}
	img := s.c.Img
	draw.Draw(img, dirty, ad.d.Composite(dirty), dirty.Min, draw.Src)
	if sd.drawing {
		// A floor of one in 255 is invisible but makes the whole window take the mouse.
		for y := dirty.Min.Y; y < dirty.Max.Y; y++ {
			i := img.PixOffset(dirty.Min.X, y)
			for x := dirty.Min.X; x < dirty.Max.X; x, i = x+1, i+4 {
				if img.Pix[i+3] == 0 {
					img.Pix[i+3] = 1
				}
			}
		}
	}
	s.o.Present(img, dirty)
	s.shown = true
}

// diffShow presents a window the size of a display that holds little, a panel or a few marks, sending only the
// pixels that changed since it was last shown: the frame is drawn whole, then compared with the last.
type diffShow struct {
	last *image.RGBA
}

func (ds *diffShow) present(s *surface) {
	img := s.c.Img
	if ds.last == nil || ds.last.Rect != img.Rect || !s.shown {
		ds.last = image.NewRGBA(img.Rect)
		copy(ds.last.Pix, img.Pix)
		s.o.Present(img, image.Rectangle{})
		s.shown = true
		return
	}
	changed := changedArea(ds.last, img)
	if changed.Empty() {
		return
	}
	for y := changed.Min.Y; y < changed.Max.Y; y++ {
		i, j := img.PixOffset(changed.Min.X, y), img.PixOffset(changed.Max.X, y)
		copy(ds.last.Pix[i:j], img.Pix[i:j])
	}
	s.o.Present(img, changed)
}

// changedArea is the smallest rectangle holding every pixel where two pictures the same size differ.
func changedArea(was, now *image.RGBA) image.Rectangle {
	var r image.Rectangle
	b := now.Rect
	stride := now.Stride
	const chunk = 64 // bytes compared at once, to find a row's changed span quickly
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := (y - b.Min.Y) * stride
		p, q := was.Pix[row:row+b.Dx()*4], now.Pix[row:row+b.Dx()*4]
		if bytes.Equal(p, q) {
			continue
		}
		lo := 0
		for lo+chunk <= len(p) && bytes.Equal(p[lo:lo+chunk], q[lo:lo+chunk]) {
			lo += chunk
		}
		hi := len(p)
		for hi-chunk >= lo && bytes.Equal(p[hi-chunk:hi], q[hi-chunk:hi]) {
			hi -= chunk
		}
		span := image.Rect(b.Min.X+lo/4, y, b.Min.X+(hi+3)/4, y+1)
		r = r.Union(span)
	}
	return r
}
