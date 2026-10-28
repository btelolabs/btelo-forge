package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"slices"
	"strings"
	"time"

	"golang.org/x/image/draw"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// paintSettings are the brush and recent colours, kept between runs and shared by every canvas.
type paintSettings struct {
	Size       float64  `json:"size"`
	Hardness   float64  `json:"hardness"`
	Opacity    float64  `json:"opacity"`
	Flow       float64  `json:"flow"`
	Smooth     float64  `json:"smoothing"`
	Angle      float64  `json:"angle"`
	Round      float64  `json:"roundness"`
	Spacing    float64  `json:"spacing"`
	Tip        int      `json:"tip"`
	Color      string   `json:"color"`      // the foreground colour
	Background string   `json:"background"` // the background colour
	Recent     []string `json:"recent,omitempty"`

	SelMode        int     `json:"selectMode"` // paint.SelectMode
	Feather        float64 `json:"feather"`
	WandTolerance  int     `json:"wandTolerance"`
	WandContiguous bool    `json:"wandContiguous"`
	WandAllLayers  bool    `json:"wandAllLayers"`

	CropRatio int  `json:"cropRatio"` // into cropRatios
	CropKeep  bool `json:"cropKeep"`  // crop without deleting the cropped pixels

	FillOpacity    float64 `json:"fillOpacity"` // the paint bucket's
	FillTolerance  int     `json:"fillTolerance"`
	FillContiguous bool    `json:"fillContiguous"`
	FillAllLayers  bool    `json:"fillAllLayers"`

	GradientKind    int     `json:"gradientKind"`    // paint.GradientKind
	GradientColours int     `json:"gradientColours"` // into gradientPresets
	GradientOpacity float64 `json:"gradientOpacity"`
	GradientReverse bool    `json:"gradientReverse"`
	GradientDither  bool    `json:"gradientDither"`

	CloneAligned   bool `json:"cloneAligned"`
	CloneAllLayers bool `json:"cloneAllLayers"`

	ShapeKind   int     `json:"shapeKind"` // shapeRect, shapeEllipse…
	ShapeFill   bool    `json:"shapeFill"`
	ShapeStroke bool    `json:"shapeStroke"`
	ShapeWidth  float64 `json:"shapeWidth"` // the stroke's, or a line's weight
	NoSnap      bool    `json:"noSnap"`     // View › Snapping is off
	PenFill     bool    `json:"penFill"`    // the pen fills its curves with the background colour
	ShapeRadius float64 `json:"shapeRadius"`
	ShapeSides  int     `json:"shapeSides"` // a polygon's or star's points
	ShapeInner  float64 `json:"shapeInner"` // a star's inner radius, as a share of its outer

	ToneRange      int     `json:"toneRange"` // dodge's and burn's, paint.ToneRange
	Exposure       float64 `json:"exposure"`  // dodge's and burn's strength
	SpongeSaturate bool    `json:"spongeSaturate"`
	SpongeFlow     float64 `json:"spongeFlow"`
	FocusStrength  float64 `json:"focusStrength"` // blur's and sharpen's
	SmudgeStrength float64 `json:"smudgeStrength"`

	TextSize  float64 `json:"textSize"`
	TextBold  bool    `json:"textBold"`
	TextAlign int     `json:"textAlign"` // paint.TextAlign
}

func defaultPaintSettings() paintSettings {
	b := paint.DefaultBrush()
	return paintSettings{Size: b.Size, Hardness: b.Hardness, Opacity: b.Opacity, Flow: b.Flow, Smooth: b.Smooth, Angle: b.Angle,
		Round: b.Round, Spacing: b.Spacing, Tip: int(b.Tip), Color: hexColor(b.Color), Background: "#ffffff",
		WandTolerance: 32, WandContiguous: true, FillOpacity: 1, FillTolerance: 32, FillContiguous: true,
		GradientOpacity: 1, GradientDither: true, CloneAligned: true, ShapeFill: true, ShapeWidth: 3, ShapeSides: 5, ShapeInner: 0.5, TextSize: 48,
		ToneRange: int(paint.RangeMidtones), Exposure: 0.5, SpongeFlow: 0.5, FocusStrength: 0.5, SmudgeStrength: 0.5}
}

func (p paintSettings) brush() paint.Brush {
	b := paint.DefaultBrush()
	b.Size, b.Hardness, b.Opacity, b.Tip = p.Size, p.Hardness, p.Opacity, paint.Tip(p.Tip)
	b.Flow, b.Smooth, b.Angle, b.Round = p.Flow, p.Smooth, p.Angle, p.Round
	if p.Spacing > 0 {
		b.Spacing = p.Spacing
	}
	if rgb, err := parseColor(p.Color); err == nil {
		b.Color = color.NRGBA{R: rgb[0], G: rgb[1], B: rgb[2], A: 255}
	}
	return b
}

func hexColor(c color.NRGBA) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// colorPicker is the colour panel's state: hue, saturation and value, kept apart from the brush colour so dragging
// the value to black does not lose the hue.
type colorPicker struct {
	h, s, v float64
	hex     string
	square  *image.RGBA // the saturation and value square for hue sq
	sqHue   float64
	sqSize  image.Point
	strip   *image.RGBA
}

// saveBrush keeps the brush settings.
func (a *App) saveBrush() {
	b := a.brush
	p := &a.settings.Paint
	p.Size, p.Hardness, p.Opacity, p.Flow, p.Smooth = b.Size, b.Hardness, b.Opacity, b.Flow, b.Smooth
	p.Angle, p.Round, p.Spacing, p.Tip, p.Color = b.Angle, b.Round, b.Spacing, int(b.Tip), hexColor(b.Color)
	a.saveSettings()
}

// setBrushColor changes the foreground colour, the brush's, from outside the picker (a swatch, the eyedropper).
func (a *App) setBrushColor(c color.NRGBA) {
	c.A = 255
	a.brush.Color = c
	if !a.pickBackground {
		a.picker.hex = "" // the panel takes the new colour
	}
	a.rememberColor(c)
	a.saveBrush()
}

// backgroundColor is the background colour: what the eraser leaves on a mask, Ctrl+Backspace fills with, X swaps in.
func (a *App) backgroundColor() color.NRGBA {
	if rgb, err := parseColor(a.settings.Paint.Background); err == nil {
		return color.NRGBA{R: rgb[0], G: rgb[1], B: rgb[2], A: 255}
	}
	return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
}

func (a *App) setBackground(c color.NRGBA) {
	a.settings.Paint.Background = hexColor(c)
	if a.pickBackground {
		a.picker.hex = ""
	}
	a.saveSettings()
}

// swapColors exchanges the foreground and background colours (X); resetColors makes them black and white (D).
func (a *App) swapColors() {
	fg, bg := a.brush.Color, a.backgroundColor()
	a.brush.Color = bg
	a.settings.Paint.Background = hexColor(fg)
	a.picker.hex = ""
	a.saveBrush()
}

func (a *App) resetColors() {
	a.brush.Color = color.NRGBA{A: 255}
	a.settings.Paint.Background = "#ffffff"
	a.picker.hex = ""
	a.saveBrush()
}

// pickerColor is the colour the panel edits: the foreground or the background.
func (a *App) pickerColor() color.NRGBA {
	if a.pickBackground {
		return a.backgroundColor()
	}
	return a.brush.Color
}

// setPickerColor sets the colour the panel edits.
func (a *App) setPickerColor(c color.NRGBA) {
	c.A = 255
	if a.pickBackground {
		a.settings.Paint.Background = hexColor(c)
		a.rememberColor(c)
		a.saveSettings()
		return
	}
	a.brush.Color = c
	a.rememberColor(c)
	a.saveBrush()
}

// rememberColor puts a colour at the front of the recent ones.
func (a *App) rememberColor(c color.NRGBA) {
	hex := hexColor(c)
	r := slices.DeleteFunc(slices.Clone(a.settings.Paint.Recent), func(s string) bool { return s == hex })
	r = append([]string{hex}, r...)
	if len(r) > 12 {
		r = r[:12]
	}
	a.settings.Paint.Recent = r
}

func rgbToHSV(c color.NRGBA) (h, s, v float64) {
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	mx, mn := max(r, g, b), min(r, g, b)
	v = mx
	if mx > 0 {
		s = (mx - mn) / mx
	}
	d := mx - mn
	switch {
	case d == 0:
		h = 0
	case mx == r:
		h = math.Mod((g-b)/d, 6) / 6
	case mx == g:
		h = ((b-r)/d + 2) / 6
	default:
		h = ((r-g)/d + 4) / 6
	}
	if h < 0 {
		h++
	}
	return h, s, v
}

func hsvToRGB(h, s, v float64) color.NRGBA {
	h = math.Mod(h, 1) * 6
	i := math.Floor(h)
	f := h - i
	p, q, t := v*(1-s), v*(1-s*f), v*(1-s*(1-f))
	var r, g, b float64
	switch int(i) {
	case 0:
		r, g, b = v, t, p
	case 1:
		r, g, b = q, v, p
	case 2:
		r, g, b = p, v, t
	case 3:
		r, g, b = p, q, v
	case 4:
		r, g, b = t, p, v
	default:
		r, g, b = v, p, q
	}
	return color.NRGBA{R: uint8(r*255 + 0.5), G: uint8(g*255 + 0.5), B: uint8(b*255 + 0.5), A: 255}
}

func (a *App) drawArtSide(ad *artDoc, r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colPanel)
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Y, W: max(1, u.px(1)), H: r.H}, colBorder)
	in := r.Inset(u.px(14))
	y := a.drawColorPicker(gfx.Rect{X: in.X, Y: in.Y, W: in.W, H: u.px(250)})
	a.drawLayers(ad, gfx.Rect{X: in.X, Y: y + u.px(14), W: in.W, H: in.Bottom() - y - u.px(14)})
}

// drawColorPicker is the colour panel, editing the foreground or the background colour; it returns where it ends.
func (a *App) drawColorPicker(r gfx.Rect) float32 {
	u := &a.u
	p := &a.picker
	cur := a.pickerColor()
	if p.hex == "" {
		p.h, p.s, p.v = rgbToHSV(cur)
		p.hex = hexColor(cur)
	}
	title := u.t("Foreground colour")
	if a.pickBackground {
		title = u.t("Background colour")
	}
	u.heading(gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: u.px(20)}, title)
	sq := gfx.Rect{X: r.X, Y: r.Y + u.px(26), W: r.W, H: u.px(130)}
	size := sq.Pixels().Size()
	if p.square == nil || p.sqHue != p.h || p.sqSize != size {
		p.square, p.sqHue, p.sqSize = image.NewRGBA(image.Rectangle{Max: size}), p.h, size
		for y := 0; y < size.Y; y++ {
			for x := 0; x < size.X; x++ {
				c := hsvToRGB(p.h, float64(x)/float64(max(size.X-1, 1)), 1-float64(y)/float64(max(size.Y-1, 1)))
				p.square.SetRGBA(x, y, color.RGBA{c.R, c.G, c.B, 255})
			}
		}
	}
	u.c.Blit(p.square, sq.Pixels().Min.X, sq.Pixels().Min.Y, true)
	mark := func(x, y float32) {
		ring := gfx.Rect{X: x - u.px(6), Y: y - u.px(6), W: u.px(12), H: u.px(12)}
		u.c.RoundOutline(ring, u.px(6), u.px(2), colWhite)
		u.c.RoundOutline(ring.Inset(-u.px(1)), u.px(7), u.px(1), gfx.Alpha(colBlack, 0.6))
	}
	mark(sq.X+float32(p.s)*sq.W, sq.Y+float32(1-p.v)*sq.H)
	changed := false
	u.press("art/sv", sq)
	if u.dragging("art/sv") || u.active == "art/sv" && u.down {
		p.s = min(max(float64((u.mx-sq.X)/sq.W), 0), 1)
		p.v = 1 - min(max(float64((u.my-sq.Y)/sq.H), 0), 1)
		changed = true
	}
	done := u.letGo("art/sv")
	strip := gfx.Rect{X: r.X, Y: sq.Bottom() + u.px(10), W: r.W, H: u.px(14)}
	ss := strip.Pixels().Size()
	if p.strip == nil || p.strip.Rect.Size() != ss {
		p.strip = image.NewRGBA(image.Rectangle{Max: ss})
		for x := 0; x < ss.X; x++ {
			c := hsvToRGB(float64(x)/float64(max(ss.X-1, 1)), 1, 1)
			for y := 0; y < ss.Y; y++ {
				p.strip.SetRGBA(x, y, color.RGBA{c.R, c.G, c.B, 255})
			}
		}
	}
	u.c.Blit(p.strip, strip.Pixels().Min.X, strip.Pixels().Min.Y, true)
	hx := strip.X + float32(p.h)*strip.W
	u.c.Fill(gfx.Rect{X: hx - u.px(2), Y: strip.Y - u.px(2), W: u.px(4), H: strip.H + u.px(4)}, colWhite)
	u.press("art/hue", strip)
	if u.dragging("art/hue") || u.active == "art/hue" && u.down {
		p.h = min(max(float64((u.mx-strip.X)/strip.W), 0), 0.9999)
		changed = true
	}
	done = u.letGo("art/hue") || done
	if changed || done {
		// The colour follows the drag; the panel keeps its own hue, so dragging to grey or black does not lose it.
		h, s, v := p.h, p.s, p.v
		c := hsvToRGB(h, s, v)
		if done {
			a.setPickerColor(c)
		} else if a.pickBackground {
			a.settings.Paint.Background = hexColor(c)
		} else {
			a.brush.Color = c
		}
		p.h, p.s, p.v, p.hex = h, s, v, hexColor(c)
	}
	y := strip.Bottom() + u.px(12)
	cur = a.pickerColor()
	sw := gfx.Rect{X: r.X, Y: y, W: u.px(30), H: u.px(30)}
	u.c.RoundRect(sw, u.px(6), color.RGBA{cur.R, cur.G, cur.B, 255})
	u.c.RoundOutline(sw, u.px(6), max(1, u.px(1)), colBorder)
	if _, done := u.textBox("art/hex", gfx.Rect{X: sw.Right() + u.px(8), Y: y, W: u.px(96), H: u.px(30)}, &p.hex, "#rrggbb", false); done {
		if rgb, err := parseColor(p.hex); err == nil {
			a.setPickerColor(color.NRGBA{R: rgb[0], G: rgb[1], B: rgb[2], A: 255})
		}
		p.hex = ""
	}
	y += u.px(38)
	x := r.X
	for i, hex := range a.settings.Paint.Recent {
		rgb, err := parseColor(hex)
		if err != nil {
			continue
		}
		cell := gfx.Rect{X: x, Y: y, W: u.px(20), H: u.px(20)}
		if cell.Right() > r.Right() {
			break
		}
		u.c.RoundRect(cell, u.px(4), color.RGBA{rgb[0], rgb[1], rgb[2], 255})
		id := fmt.Sprintf("art/recent/%d", i)
		if u.hover(id, cell) {
			u.cursor = CursorHand
			u.c.RoundOutline(cell, u.px(4), max(1, u.px(1)), colText)
		}
		if u.clicked(id, cell) {
			a.setPickerColor(color.NRGBA{R: rgb[0], G: rgb[1], B: rgb[2], A: 255})
			p.hex = ""
		}
		x += cell.W + u.px(4)
	}
	return y + u.px(20)
}

// layerRow is one line of the layers panel.
type layerRow struct {
	n     *paint.Node
	depth int
}

func layerRows(g *paint.Node, depth int, out []layerRow) []layerRow {
	for _, c := range g.Children {
		out = append(out, layerRow{c, depth})
		if c.IsGroup() && c.Open {
			out = layerRows(c, depth+1, out)
		}
	}
	return out
}

// blendNames name paint.Blends, in its order; groupBlendNames add pass-through first.
var blendNames = msgsIn("blend", "Normal",
	"Darken", "Multiply", "Color burn", "Linear burn", "Darker color",
	"Lighten", "Screen", "Color dodge", "Linear dodge (add)", "Lighter color",
	"Overlay", "Soft light", "Hard light", "Vivid light", "Linear light", "Pin light", "Hard mix",
	"Difference", "Exclusion", "Subtract", "Divide",
	"Hue", "Saturation", "Color", "Luminosity")

// blendFamilies are where the blend mode menu has separators, as Photoshop's does.
var blendFamilies = map[int]bool{1: true, 6: true, 11: true, 18: true, 22: true}

// drawLayers is the layers panel: the chosen node's blend mode, opacity and lock, the tree, and the commands.
func (a *App) drawLayers(ad *artDoc, r gfx.Rect) {
	u := &a.u
	d := ad.d
	y := a.drawSideTabs(r)
	if a.sidePage == sideHistory {
		a.drawHistory(ad, gfx.Rect{X: r.X, Y: y, W: r.W, H: r.Bottom() - y})
		return
	}
	if n := d.Node(d.Active); n != nil {
		// The blend mode: a group also offers pass-through, first.
		modes, names := paint.Blends, u.tcs("blend", blendNames)
		seps := blendFamilies
		if n.IsGroup() {
			modes = append([]paint.Blend{paint.BlendPassThrough}, modes...)
			names = append([]string{u.tc("blend", "Pass through")}, names...)
			seps = map[int]bool{}
			for k := range blendFamilies {
				seps[k+1] = true
			}
			seps[1] = true
		}
		current := slices.Index(modes, n.Blend)
		id := n.ID
		a.dropdown("art/layer/blend", gfx.Rect{X: r.X, Y: y, W: r.W - u.px(40), H: u.px(28)}, names, current, seps, func(i int) {
			d.SetBlend(id, modes[i])
			ad.changedAll()
		})
		lock := gfx.Rect{X: r.Right() - u.px(32), Y: y, W: u.px(30), H: u.px(28)}
		if u.iconButton("art/layer/lock", lock, icLock, withKey(u.t("Lock transparent pixels"), "/"), n.LockAlpha, !n.IsGroup()) {
			d.SetLockAlpha(n.ID, !n.LockAlpha)
			ad.changedAll()
		}
		y += u.px(34)
		u.label(u.face(12), gfx.Rect{X: r.X, Y: y, W: u.px(70), H: u.px(24)}, u.t("Opacity"), colDim, 0)
		sr := gfx.Rect{X: r.X + u.px(72), Y: y + u.px(9), W: r.W - u.px(124), H: u.px(6)}
		if u.active != "art/layer/opacity" {
			a.layerOpacity = n.Opacity
		}
		if u.pressedIn(sr) {
			a.opacityMark = d.Mark()
		}
		changed, done := u.slider("art/layer/opacity", sr, &a.layerOpacity, 0, 1, true)
		if changed {
			n.Opacity = a.layerOpacity
			ad.refresh()
		}
		if done {
			d.Commit("Layer opacity", a.opacityMark)
			ad.changedAll()
		}
		u.label(u.face(11.5), gfx.Rect{X: sr.Right() + u.px(8), Y: y, W: u.px(44), H: u.px(24)}, percent(n.Opacity), colText, 0)
	}
	y += u.px(32)
	cmds := gfx.Rect{X: r.X, Y: r.Bottom() - u.px(36), W: r.W, H: u.px(36)}
	list := gfx.Rect{X: r.X, Y: y, W: r.W, H: cmds.Y - y - u.px(8)}
	u.c.RoundRect(list, u.px(8), colBackground)
	u.c.Push(list)
	rowH := u.px(36)
	rows := layerRows(d.Root, 0, nil)
	maxScroll := max(float32(len(rows))*rowH-list.H, 0)
	if u.hover("art/layers", list) && u.wheel != 0 {
		a.layersScroll -= u.wheel * rowH * 2
		u.wheel = 0
	}
	a.layersScroll = min(max(a.layersScroll, 0), maxScroll)
	for i, row := range rows {
		n := row.n
		rr := gfx.Rect{X: list.X, Y: list.Y + float32(i)*rowH - a.layersScroll, W: list.W, H: rowH}
		if rr.Bottom() < list.Y || rr.Y > list.Bottom() {
			continue
		}
		a.drawLayerRow(ad, n, row.depth, rr)
	}
	u.c.Pop()
	a.drawLayerCommands(ad, cmds)
}

// drawLayerRow is one layer or group in the panel: visibility, thumbnails, name, lock.
func (a *App) drawLayerRow(ad *artDoc, n *paint.Node, depth int, rr gfx.Rect) {
	u := &a.u
	d := ad.d
	id := fmt.Sprintf("art/layer/%d", n.ID)
	chosen := n.ID == d.Active
	switch {
	case chosen:
		u.c.Fill(rr, gfx.Mix(colSurface, colAccent, 0.3))
	case slices.Contains(ad.picked, n.ID):
		u.c.Fill(rr, gfx.Mix(colSurface, colAccent, 0.16))
	case u.hover(id, rr):
		u.c.Fill(rr, colSurface)
	}
	x := rr.X + u.px(4) + float32(depth)*u.px(16)
	eye := gfx.Rect{X: x, Y: rr.Y, W: u.px(24), H: rr.H}
	glyph, col := icEye, colDim
	if !n.Visible {
		glyph, col = icHide, colFaint
	}
	u.icon(glyph, eye, 14, col)
	if u.clicked(id+"/eye", eye) {
		d.SetVisible(n.ID, !n.Visible)
		ad.changedAll()
	}
	x = eye.Right() + u.px(2)
	thumbW, thumbH := u.px(34), rr.H-u.px(10)
	editing := chosen && d.EditMask && n.Mask != nil
	if n.IsGroup() {
		fold := gfx.Rect{X: x, Y: rr.Y, W: u.px(18), H: rr.H}
		g := icClosed
		if n.Open {
			g = icOpen
		}
		u.icon(g, fold, 15, colDim)
		if u.clicked(id+"/fold", fold) {
			d.SetOpenGroup(n.ID, !n.Open)
			ad.dirty = true
		}
		u.icon(icGroup, gfx.Rect{X: fold.Right(), Y: rr.Y, W: u.px(20), H: rr.H}, 14, colWarning)
		x = fold.Right() + u.px(24)
	} else {
		thumb := gfx.Rect{X: x, Y: rr.Y + u.px(5), W: thumbW, H: thumbH}
		a.drawLayerThumb(ad, n, thumb)
		if chosen && !editing {
			u.c.RoundOutline(thumb.Inset(-u.px(2)), u.px(4), max(1, u.px(1.5)), colWhite)
		}
		if n.Adjust != nil {
			u.hint(id+"/pixels", u.t("Adjustment layer: double-click to change its settings"), thumb)
		}
		if u.press(id+"/pixels", thumb) && n.Adjust != nil && u.double {
			u.letGo(id + "/pixels")
			d.Choose(n.ID)
			a.openAdjustmentSettings(ad, n.ID)
		}
		if u.clicked(id+"/pixels", thumb) {
			d.Choose(n.ID)
			d.EditMask = n.Adjust != nil // an adjustment layer has only its mask to paint
		}
		x = thumb.Right() + u.px(6)
	}
	if n.Mask != nil {
		mt := gfx.Rect{X: x, Y: rr.Y + u.px(5), W: thumbW, H: thumbH}
		a.drawMaskThumb(ad, n, mt)
		if editing {
			u.c.RoundOutline(mt.Inset(-u.px(2)), u.px(4), max(1, u.px(1.5)), colWhite)
		}
		if n.MaskOff {
			u.c.Line(mt.X, mt.Y, mt.Right(), mt.Bottom(), u.px(2), colDanger)
			u.c.Line(mt.X, mt.Bottom(), mt.Right(), mt.Y, u.px(2), colDanger)
		}
		u.hint(id+"/mask", u.t("Layer mask: click to paint on it, Shift+click to turn it off or on"), mt)
		if u.clicked(id+"/mask", mt) {
			d.Choose(n.ID)
			if u.shift {
				d.SetMaskOff(n.ID, !n.MaskOff)
				ad.changedAll()
			} else {
				d.EditMask = true
			}
		}
		x = mt.Right() + u.px(6)
	}
	right := rr.Right() - u.px(6)
	if n.Style != nil {
		// Effects show as fx, as in Photoshop; a click opens them.
		fx := gfx.Rect{X: right - u.px(20), Y: rr.Y, W: u.px(20), H: rr.H}
		u.label(u.bold(11), fx, "fx", colDim, 0.5)
		u.hint(id+"/fx", u.t("Layer style…"), fx)
		if u.clicked(id+"/fx", fx) {
			d.Choose(n.ID)
			a.openStyle(ad)
		}
		right -= u.px(24)
	}
	if n.LockAlpha {
		u.icon(icLock, gfx.Rect{X: right - u.px(18), Y: rr.Y, W: u.px(18), H: rr.H}, 12, colDim)
		right -= u.px(22)
	}
	name := gfx.Rect{X: x, Y: rr.Y + u.px(4), W: right - x, H: rr.H - u.px(8)}
	if ad.renaming == n.ID {
		if _, done := u.textBox("art/rename", name, &ad.rename, "", false); done || u.focus != "art/rename" {
			if strings.TrimSpace(ad.rename) != "" && ad.rename != n.Name {
				d.Rename(n.ID, strings.TrimSpace(ad.rename))
				ad.dirty = true
			}
			ad.renaming = -1
		}
	} else {
		fg := colText
		if !n.Visible {
			fg = colFaint
		}
		u.label(u.face(12.5), name, layerLabel(n), fg, 0)
	}
	if u.rightAt != nil && rr.Contains(u.rightAt.X, u.rightAt.Y) && !u.blocked {
		d.Choose(n.ID)
		a.openMenu("art/layer/context", gfx.Rect{X: u.rightAt.X, Y: u.rightAt.Y, W: 1, H: 1}, a.layerContextMenu(ad))
		u.rightAt = nil
	}
	if u.press(id, rr) {
		if u.ctrl {
			// Ctrl+click chooses more layers besides the chosen one, or lets one go, as in Photoshop.
			ad.togglePicked(n.ID)
			u.letGo(id)
			return
		}
		ad.picked = nil
		if u.double {
			ad.renaming, ad.rename = n.ID, n.Name
			u.setFocus("art/rename")
		}
		if d.Active != n.ID {
			d.Choose(n.ID)
			d.EditMask = n.Adjust != nil
		}
	}
	u.letGo(id)
}

// layerContextMenu is the right-click menu of the layers panel: the Layer menu's commands.
func (a *App) layerContextMenu(ad *artDoc) []menuItem {
	return a.artMenuItems(ad, gfx.Rect{}, menuLayer)
}

// pressedIn reports whether a press starts in r this frame, without claiming it.
func (u *ctx) pressedIn(r gfx.Rect) bool {
	return !u.blocked && u.pressed && u.active == "" && r.Contains(u.pressX, u.pressY)
}

// layerThumb is a small copy of a layer (or its mask) for the layers panel, and when it was made.
type layerThumb struct {
	img *image.RGBA
	of  any // the pixels or mask it was made from
	at  time.Time
}

// thumbFor keeps a thumbnail up to date: made again after the picture changed, at most every 300 ms while a stroke
// is painted.
func (a *App) thumbFor(ad *artDoc, key int, of any, size image.Point, make func(dst *image.RGBA)) *image.RGBA {
	t := a.thumbs[key]
	stale := t == nil || t.of != of || t.img.Rect.Size() != size ||
		ad.changed.After(t.at) && (ad.stroke == nil || time.Since(t.at) > 300*time.Millisecond)
	if stale {
		if t == nil || t.img.Rect.Size() != size {
			t = &layerThumb{img: image.NewRGBA(image.Rectangle{Max: size})}
			a.thumbs[key] = t
		}
		make(t.img)
		t.of, t.at = of, time.Now()
	}
	return t.img
}

// thumbExtent is the canvas area a layer's thumbnail shows: the pages (the frame and the artboards) when the layer
// reaches onto them, else the layer's own content, as Affinity Designer shows a layer off its artboards; with no
// pages, what is painted, or else a tile.
func thumbExtent(ad *artDoc, n *paint.Node) image.Rectangle {
	pages := ad.d.Frame
	for _, b := range ad.d.Boards {
		pages = pages.Union(b.Rect)
	}
	own := paint.ReachOf(n)
	switch {
	case !pages.Empty() && (own.Empty() || own.Overlaps(pages)):
		return pages
	case !own.Empty() && !pages.Empty():
		return own
	}
	if r := ad.d.Reach(); !r.Empty() {
		return r
	}
	return image.Rect(0, 0, paint.TileSize, paint.TileSize)
}

// thumbArea is where a layer's thumbnail shows its area, kept in proportion.
func (a *App) thumbArea(ad *artDoc, n *paint.Node, r gfx.Rect) gfx.Rect {
	e := thumbExtent(ad, n)
	scale := min(r.W/float32(e.Dx()), r.H/float32(e.Dy()))
	w, h := float32(e.Dx())*scale, float32(e.Dy())*scale
	return gfx.Rect{X: r.X + (r.W-w)/2, Y: r.Y + (r.H-h)/2, W: w, H: h}
}

// thumbLevel is the level of detail to read a thumbnail size wide of the extent e from, and e at that level.
func thumbLevel(e image.Rectangle, size image.Point) (int, image.Rectangle) {
	l := paint.LevelFor(float64(size.X) / float64(e.Dx()) * 2) // twice the pixels, for a smooth picture
	return l, image.Rect(e.Min.X>>l, e.Min.Y>>l, (e.Max.X+(1<<l)-1)>>l, (e.Max.Y+(1<<l)-1)>>l)
}

// drawLayerThumb is a small picture of a layer's pixels over the picture's area.
func (a *App) drawLayerThumb(ad *artDoc, n *paint.Node, r gfx.Rect) {
	u := &a.u
	u.c.RoundRect(r, u.px(3), checkLight)
	dst := a.thumbArea(ad, n, r)
	u.c.Fill(dst, checkDark)
	size := dst.Pixels().Size()
	if size.X <= 0 || size.Y <= 0 {
		return
	}
	if n.Text != nil { // a text layer shows a T, as in Photoshop
		u.c.Fill(dst, colSurfaceHi)
		u.icon(icText, dst, 16, colText)
		return
	}
	if n.Adjust != nil { // an adjustment layer shows what it does
		u.c.Fill(dst, colSurfaceHi)
		u.icon(adjustGlyph(n.Adjust.Kind), dst, 16, colText)
		return
	}
	var of any = n.Pixels // what the thumbnail is of: it is made again when that changes
	if n.Vector != nil {
		of = n.Vector
	}
	img := a.thumbFor(ad, n.ID, of, size, func(t *image.RGBA) {
		l, area := thumbLevel(thumbExtent(ad, n), size)
		draw.ApproxBiLinear.Scale(t, t.Rect, paint.ReadLayer(n, area, l), area, draw.Src, nil)
	})
	u.c.Image(img, dst)
}

// drawMaskThumb is a small picture of a layer mask, white showing and black hiding.
func (a *App) drawMaskThumb(ad *artDoc, n *paint.Node, r gfx.Rect) {
	u := &a.u
	dst := a.thumbArea(ad, n, r)
	size := dst.Pixels().Size()
	if size.X <= 0 || size.Y <= 0 {
		return
	}
	img := a.thumbFor(ad, -n.ID, n.Mask, size, func(t *image.RGBA) {
		l, area := thumbLevel(thumbExtent(ad, n), size)
		m := n.Mask.ReadLevel(l, area)
		for y := 0; y < size.Y; y++ {
			for x := 0; x < size.X; x++ {
				v := m.Pix[m.PixOffset(area.Min.X+x*area.Dx()/size.X, area.Min.Y+y*area.Dy()/size.Y)]
				t.SetRGBA(x, y, color.RGBA{v, v, v, 255})
			}
		}
	})
	u.c.Image(img, dst)
}

func (a *App) drawLayerCommands(ad *artDoc, r gfx.Rect) {
	u := &a.u
	d := ad.d
	x := r.X
	cmds := []struct {
		id, glyph, hint string
		run             func() error
	}{
		{"new", icNewLayer, withKey(u.t("New layer"), "Ctrl+Shift+N"), func() error { d.AddLayer(a.layerName()); return nil }},
		{"group", icNewGroup, u.t("New group"), func() error { d.AddGroup(a.groupName()); return nil }},
		{"mask", icMask, u.t("Add layer mask"), d.AddMask},
		{"dup", icDuplicate, withKey(u.t("Duplicate layer"), "Ctrl+J"), func() error { _, err := d.Duplicate(u.t(" copy")); return err }},
		{"up", icUp, u.t("Move up"), func() error { return d.Move(true) }},
		{"down", icDown, u.t("Move down"), func() error { return d.Move(false) }},
		{"style", icEffects, u.t("Layer style…"), func() error { a.openStyle(ad); return nil }},
		{"delete", icDelete, u.t("Delete layer"), d.Delete},
	}
	// The buttons share what room the panel has, the menu of more commands at the end.
	gap, moreW := u.px(2), u.px(30)
	w := min(u.px(32), (r.W-moreW-gap*float32(len(cmds)))/float32(len(cmds)))
	for _, b := range cmds {
		br := gfx.Rect{X: x, Y: r.Y, W: w, H: u.px(30)}
		if u.iconButton("art/cmd/"+b.id, br, b.glyph, b.hint, false, ad.stroke == nil) {
			a.artRun(ad, b.run)
		}
		x += br.W + gap
	}
	more := gfx.Rect{X: r.Right() - moreW, Y: r.Y, W: moreW, H: u.px(30)}
	if u.iconButton("art/cmd/more", more, "\ue5d4", u.t("More layer commands"), a.menuOpen("art/cmd/more"), true) {
		a.openMenu("art/cmd/more", more, a.layerContextMenu(ad))
	}
}

// newCanvasNow opens a new, empty canvas: it has no size until one is wanted (Image › Canvas Size gives it a
// frame), as an infinite canvas in Affinity Designer has none.
func (a *App) newCanvasNow() {
	if err := a.newCanvas(); err != nil {
		a.toast(a.u.tf("The canvas could not be made: %s", a.describe(err)), true)
	}
}
