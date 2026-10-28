package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/draw"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// paintSettings are the brush and recent colours, kept between runs and shared by every canvas.
type paintSettings struct {
	Size     float64 `json:"size"`
	Hardness float64 `json:"hardness"`
	Opacity  float64 `json:"opacity"`
	Flow     float64 `json:"flow"`
	Smooth   float64 `json:"smoothing"`
	Angle    float64 `json:"angle"`
	Round    float64 `json:"roundness"`
	Spacing  float64 `json:"spacing"`
	Tip      int     `json:"tip"`
	ShapeTip string  `json:"shapeTip,omitempty"` // the picture tip's file in the brushes folder, when Tip is paint.TipShaped

	// The vector brush's tip: its own, round unless chosen otherwise.
	VBrushSize  float64  `json:"vbrushSize,omitempty"`
	VBrushTip   int      `json:"vbrushTip,omitempty"`
	VBrushAngle float64  `json:"vbrushAngle,omitempty"`
	VBrushRound float64  `json:"vbrushRound,omitempty"`
	VBrushShape string   `json:"vbrushShape,omitempty"`
	Tips        []string `json:"tips,omitempty"`   // the picture tips made, oldest first
	Preset      string   `json:"preset,omitempty"` // the brush preset whose tip the brush has, when Tip is paint.TipShaped
	SizeJitter  float64  `json:"sizeJitter,omitempty"`
	AngleJit    float64  `json:"angleJitter,omitempty"`
	Scatter     float64  `json:"scatter,omitempty"`
	Follow      bool     `json:"followStroke,omitempty"`
	Color       string   `json:"color"`      // the foreground colour
	Background  string   `json:"background"` // the background colour

	// SameSize keeps the brush's size when another preset or picture tip is chosen; without it each brush has its own
	// size, kept in Sizes by brushKey, at first the preset's or the tip's.
	SameSize bool               `json:"sameSize,omitempty"`
	Sizes    map[string]float64 `json:"brushSizes,omitempty"`

	SelMode        int     `json:"selectMode"` // paint.SelectMode
	Feather        float64 `json:"feather"`
	WandTolerance  int     `json:"wandTolerance"`
	WandContiguous bool    `json:"wandContiguous"`
	WandAllLayers  bool    `json:"wandAllLayers"`

	FillOpacity    float64 `json:"fillOpacity"` // the paint bucket's
	FillTolerance  int     `json:"fillTolerance"`
	FillContiguous bool    `json:"fillContiguous"`
	FillAllLayers  bool    `json:"fillAllLayers"`

	GradientKind    int     `json:"gradientKind"`    // paint.GradientKind
	GradientColours int     `json:"gradientColours"` // into gradientPresets
	GradientOpacity float64 `json:"gradientOpacity"`
	GradientReverse bool    `json:"gradientReverse"`
	GradientDither  bool    `json:"gradientDither"`

	SmartOnly    bool `json:"smartOnly,omitempty"`    // the layers panel shows only the smart objects
	NoAutoSelect bool `json:"noAutoSelect,omitempty"` // the move tool moves the chosen layer, not what is clicked

	MaskGradientKind    int  `json:"maskGradientKind"` // paint.GradientKind of the mask gradient tool's
	MaskGradientReverse bool `json:"maskGradientReverse"`

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
	b.SizeJitter, b.AngleJitter, b.Scatter, b.Follow = p.SizeJitter, p.AngleJit, p.Scatter, p.Follow
	if p.Spacing > 0 {
		b.Spacing = p.Spacing
	}
	if c, err := parseNRGBA(p.Color); err == nil {
		b.Color = c
	}
	return b
}

// hexColor writes a colour as #rrggbb, or #rrggbbaa when it is partly transparent.
func hexColor(c color.NRGBA) string {
	if c.A < 255 {
		return fmt.Sprintf("#%02x%02x%02x%02x", c.R, c.G, c.B, c.A)
	}
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// parseNRGBA reads a colour written #rrggbb, opaque, or #rrggbbaa.
func parseNRGBA(s string) (color.NRGBA, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	v, err := strconv.ParseUint(s, 16, 32)
	switch {
	case err == nil && len(s) == 6:
		return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}, nil
	case err == nil && len(s) == 8:
		return color.NRGBA{R: uint8(v >> 24), G: uint8(v >> 16), B: uint8(v >> 8), A: uint8(v)}, nil
	}
	return color.NRGBA{}, fmt.Errorf("color %q is not #rrggbb or #rrggbbaa", s)
}

// colorPicker is the colour panel's state: hue, saturation and value, kept apart from the brush colour so dragging
// the value to black does not lose the hue.
type colorPicker struct {
	h, s, v float64
	a       float64 // the opacity, 0 to 1
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
	p.SizeJitter, p.AngleJit, p.Scatter, p.Follow = b.SizeJitter, b.AngleJitter, b.Scatter, b.Follow
	p.Preset = ""
	if preset, ok := a.chosenPreset(); ok {
		p.Preset, p.ShapeTip = preset.ID, ""
	}
	if b.Tip != paint.TipShaped {
		p.ShapeTip = ""
	}
	if !p.SameSize {
		if p.Sizes == nil {
			p.Sizes = map[string]float64{}
		}
		p.Sizes[a.brushKey()] = b.Size
	}
	a.saveSettings()
}

// setBrushColor changes the foreground colour, the brush's, from outside the picker (a swatch, the eyedropper).
func (a *App) setBrushColor(c color.NRGBA) {
	c.A = 255
	a.brush.Color = c
	if !a.pickBackground {
		a.picker.hex = "" // the panel takes the new colour
	}
	a.saveBrush()
}

// backgroundColor is the background colour: what the eraser leaves on a mask, Ctrl+Backspace fills with, X swaps in.
func (a *App) backgroundColor() color.NRGBA {
	if c, err := parseNRGBA(a.settings.Paint.Background); err == nil {
		return c
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

// pickerColor is the colour the panel edits: the foreground or the background, or the chosen vector layer's fill
// or stroke.
func (a *App) pickerColor() color.NRGBA {
	if ad, n := a.paintedVector(); n != nil {
		return ad.vectorColour(n, a.pickBackground)
	}
	if a.pickBackground {
		return a.backgroundColor()
	}
	return a.brush.Color
}

// setPickerColor sets the colour the panel edits, its opacity with it.
func (a *App) setPickerColor(c color.NRGBA) {
	if ad, n := a.paintedVector(); n != nil {
		a.setVectorColour(ad, n, a.pickBackground, c)
		return
	}
	if a.pickBackground {
		a.settings.Paint.Background = hexColor(c)
		a.saveSettings()
		return
	}
	a.brush.Color = c
	a.saveBrush()
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
	picker, layers := a.sideLayout(r)
	a.drawColorPicker(picker)
	a.drawLayers(ad, layers)
}

// sideLayout splits the side panel: the colour panel at the top, the layers (or history) panel under it to the
// bottom, with a narrow margin all round.
func (a *App) sideLayout(side gfx.Rect) (picker, layers gfx.Rect) {
	u := &a.u
	in := gfx.Rect{X: side.X + u.px(12), Y: side.Y + u.px(12), W: side.W - u.px(24), H: side.H - u.px(20)}
	picker = gfx.Rect{X: in.X, Y: in.Y, W: in.W, H: u.px(224)}
	top := picker.Bottom() + u.px(14)
	return picker, gfx.Rect{X: in.X, Y: top, W: in.W, H: in.Bottom() - top}
}

// colourSwatchArea is where the colour panel puts its foreground and background swatches, under the opacity.
func (a *App) colourSwatchArea(picker gfx.Rect) gfx.Rect {
	u := &a.u
	return gfx.Rect{X: picker.X, Y: picker.Bottom() - u.px(40), W: u.px(48), H: u.px(40)}
}

// drawColorPicker is the colour panel: the square, the hue and the opacity edit the foreground or the background
// colour (a chosen vector layer's fill or stroke), whichever of the two swatches under them is in front.
func (a *App) drawColorPicker(r gfx.Rect) {
	u := &a.u
	p := &a.picker
	cur := a.pickerColor()
	// The panel follows its colour when it changes elsewhere, as undo or another layer chosen does, unless the colour
	// is being typed or dragged here.
	if p.hex == "" || u.focus != "art/hex" && u.active != "art/sv" && u.active != "art/hue" && u.active != "art/alpha" && p.hex != hexColor(cur) {
		p.h, p.s, p.v = rgbToHSV(cur)
		p.a = float64(cur.A) / 255
		p.hex = hexColor(cur)
	}
	sq := gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: u.px(130)}
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
		u.c.Outline(ring, u.px(2), colWhite)
		u.c.Outline(ring.Inset(-u.px(1)), u.px(1), gfx.Alpha(colBlack, 0.6))
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
	// The opacity, over a checkerboard: from clear to the colour.
	alpha := gfx.Rect{X: r.X, Y: strip.Bottom() + u.px(8), W: r.W, H: u.px(12)}
	drawTransparency(u, alpha)
	solid := hsvToRGB(p.h, p.s, p.v)
	steps := max(int(alpha.W/u.px(2)), 1)
	for i := range steps {
		k := (float32(i) + 0.5) / float32(steps)
		u.c.Fill(gfx.Rect{X: alpha.X + float32(i)*alpha.W/float32(steps), Y: alpha.Y, W: alpha.W/float32(steps) + 1, H: alpha.H},
			gfx.Alpha(color.RGBA{solid.R, solid.G, solid.B, 255}, k))
	}
	ax := alpha.X + float32(p.a)*alpha.W
	u.c.Fill(gfx.Rect{X: ax - u.px(2), Y: alpha.Y - u.px(2), W: u.px(4), H: alpha.H + u.px(4)}, colWhite)
	u.hint("art/alpha", u.t("Opacity of the colour"), alpha)
	u.press("art/alpha", alpha)
	if u.dragging("art/alpha") || u.active == "art/alpha" && u.down {
		p.a = min(max(float64((u.mx-alpha.X)/alpha.W), 0), 1)
		changed = true
	}
	done = u.letGo("art/alpha") || done
	if changed || done {
		// The colour follows the drag; the panel keeps its own hue, so dragging to grey or black does not lose it.
		h, s, v := p.h, p.s, p.v
		c := hsvToRGB(h, s, v)
		c.A = uint8(math.Round(p.a * 255))
		_, vec := a.paintedVector()
		if done || vec != nil {
			a.setPickerColor(c)
		} else if a.pickBackground {
			a.settings.Paint.Background = hexColor(c)
		} else {
			a.brush.Color = c
		}
		p.h, p.s, p.v, p.hex = h, s, v, hexColor(c)
	}
	// The foreground and background swatches, and the colour edited written out.
	sw := a.colourSwatchArea(r)
	a.drawColourSwatches(sw)
	hex := gfx.Rect{X: sw.Right() + u.px(10), Y: sw.Y + (sw.H-u.px(30))/2, W: r.Right() - sw.Right() - u.px(10), H: u.px(30)}
	if _, done := u.textBox("art/hex", hex, &p.hex, "#rrggbbaa", false); done {
		if c, err := parseNRGBA(p.hex); err == nil {
			a.setPickerColor(c)
		}
		p.hex = ""
	}
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

// smartRows are the smart objects under g, each at the left, open ones with their layers, as the layers panel
// lists them while it shows only smart objects.
func smartRows(g *paint.Node, out []layerRow) []layerRow {
	for _, c := range g.Children {
		if c.IsSmart() {
			out = append(out, layerRow{c, 0})
			if c.Open {
				out = layerRows(c, 1, out)
			}
			continue
		}
		out = smartRows(c, out)
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
	u.c.Fill(list, colBackground)
	u.c.Push(list)
	rowH := u.px(36)
	rows := layerRows(d.Root, 0, nil)
	ad.sliceRows = map[int]paint.Slice{}
	for _, s := range d.Slices() {
		ad.sliceRows[s.ID] = s
	}
	if a.settings.Paint.SmartOnly {
		rows = smartRows(d.Root, nil)
		if len(rows) == 0 {
			u.label(u.face(12), list.Inset(u.px(12)), u.t("No smart objects here: a group becomes one with Layer › Convert to smart object"), colDim, 0)
		}
	}
	maxScroll := max(float32(len(rows))*rowH-list.H, 0)
	if u.hover("art/layers", list) && u.wheel != 0 {
		a.layersScroll -= u.wheel * rowH * 2
		u.wheel = 0
	}
	a.layersScroll = min(max(a.layersScroll, 0), maxScroll)
	if ad.layerDrag != nil {
		a.dragLayers(ad, list, rows, rowH, maxScroll)
	}
	for i, row := range rows {
		n := row.n
		rr := gfx.Rect{X: list.X, Y: list.Y + float32(i)*rowH - a.layersScroll, W: list.W, H: rowH}
		if rr.Bottom() < list.Y || rr.Y > list.Bottom() {
			continue
		}
		a.drawLayerRow(ad, n, row.depth, rr)
	}
	a.drawLayerDrop(ad, list, rowH)
	u.c.Pop()
	a.drawLayerCommands(ad, cmds)
}

// drawLayerRow is one layer or group in the panel: visibility, thumbnails, name, lock.
func (a *App) drawLayerRow(ad *artDoc, n *paint.Node, depth int, rr gfx.Rect) {
	u := &a.u
	d := ad.d
	id := fmt.Sprintf("art/layer/%d", n.ID)
	// A press on the row or a thumbnail that moves drags the layer to a new place.
	if ad.layerDrag == nil && slices.ContainsFunc([]string{id, id + "/pixels", id + "/mask", id + "/clip"}, u.dragging) && u.drag.moved {
		ad.startLayerDrag(n)
	}
	chosen := n.ID == d.Active
	switch {
	case chosen:
		u.c.Fill(rr, colSelected)
	case slices.Contains(ad.picked, n.ID):
		u.c.Fill(rr, gfx.Mix(colSurfaceHi, colSelected, 0.5))
	case u.hover(id, rr):
		u.c.Fill(rr, colSurface)
	case n.IsSmart():
		u.c.Fill(rr, gfx.Mix(colBackground, colSuccess, 0.08)) // a smart object stands out
	}
	if n.IsSmart() {
		u.c.Fill(gfx.Rect{X: rr.X, Y: rr.Y + u.px(6), W: u.px(3), H: rr.H - u.px(12)}, colSuccess)
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
		if n.IsSmart() {
			ir := gfx.Rect{X: fold.Right(), Y: rr.Y, W: u.px(20), H: rr.H}
			u.icon(icSmart, ir, 15, colSuccess)
			copies := len(d.SmartCopies(n.Smart.ID))
			u.hint(id+"/smart", u.tn(copies, "Smart object, link id %s: its %d copy here, and those in other canvases and videos, change together",
				"Smart object, link id %s: its %d copies here, and those in other canvases and videos, change together", n.Smart.ID, copies), ir)
		} else if n.IsBoard() {
			u.icon(icBoard, gfx.Rect{X: fold.Right(), Y: rr.Y, W: u.px(20), H: rr.H}, 14, colAccent)
			u.hint(id+"/board", u.t("Artboard: what is in it is cut to it and exports with it"), gfx.Rect{X: fold.Right(), Y: rr.Y, W: u.px(20), H: rr.H})
		} else {
			u.icon(icGroup, gfx.Rect{X: fold.Right(), Y: rr.Y, W: u.px(20), H: rr.H}, 14, colWarning)
		}
		x = fold.Right() + u.px(24)
	} else {
		thumb := gfx.Rect{X: x, Y: rr.Y + u.px(5), W: thumbW, H: thumbH}
		a.drawLayerThumb(ad, n, thumb)
		if chosen && !editing {
			u.c.Outline(thumb.Inset(-u.px(2)), max(1, u.px(1.5)), colWhite)
		}
		if n.Adjust != nil {
			u.hint(id+"/pixels", u.t("Adjustment layer: double-click to change its settings"), thumb)
		}
		a.ctrlSelect(ad, id, thumb, func(mode paint.SelectMode) error { return d.SelectContent(n.ID, mode) })
		if u.press(id+"/pixels", thumb) && n.Adjust != nil && u.double {
			u.letGo(id + "/pixels")
			d.Choose(n.ID)
			a.openAdjustmentSettings(ad, n.ID)
		}
		if u.clicked(id+"/pixels", thumb) {
			ad.picked = nil
			d.Choose(n.ID)
			d.EditMask = n.Adjust != nil // an adjustment layer has only its mask to paint
		}
		x = thumb.Right() + u.px(6)
	}
	if n.Mask != nil {
		mt := gfx.Rect{X: x, Y: rr.Y + u.px(5), W: thumbW, H: thumbH}
		a.drawMaskThumb(ad, n, mt)
		if editing {
			u.c.Outline(mt.Inset(-u.px(2)), max(1, u.px(1.5)), colWhite)
		}
		if n.MaskOff {
			u.c.Line(mt.X, mt.Y, mt.Right(), mt.Bottom(), u.px(2), colDanger)
			u.c.Line(mt.X, mt.Bottom(), mt.Right(), mt.Y, u.px(2), colDanger)
		}
		u.hint(id+"/mask", u.t("Layer mask: click to paint on it, Shift+click to turn it off or on, Ctrl+click to select by it"), mt)
		a.ctrlSelect(ad, id+"/mask", mt, func(mode paint.SelectMode) error { return d.SelectMask(n.ID, mode) })
		if u.clicked(id+"/mask", mt) {
			ad.picked = nil
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
	if n.Clip != nil {
		ct := gfx.Rect{X: x, Y: rr.Y + u.px(5), W: thumbW, H: thumbH}
		a.drawClipThumb(ad, n, ct)
		if chosen && a.artTool == toolNode && !d.EditMask {
			u.c.RoundOutline(ct.Inset(-u.px(2)), u.px(4), max(1, u.px(1.5)), colWhite)
		}
		u.hint(id+"/clip", u.t("Vector mask: the layer shows inside its outline; click to edit it with the node tool, Ctrl+click to select inside it"), ct)
		a.ctrlSelect(ad, id+"/clip", ct, func(mode paint.SelectMode) error { return d.SelectVectorMask(n.ID, mode) })
		if u.clicked(id+"/clip", ct) {
			ad.picked = nil
			d.Choose(n.ID)
			d.EditMask = false
			a.setArtTool(toolNode)
		}
		x = ct.Right() + u.px(6)
	}
	right := rr.Right() - u.px(6)
	if s, ok := ad.sliceRows[n.ID]; ok {
		right = a.sliceIcon(ad, s, id, rr, right)
	}
	if n.IsSmart() {
		// The link id, so copies are told by it; a click copies it.
		f := u.bold(11)
		label := "#" + n.Smart.ID
		chip := gfx.Rect{X: right - f.Measure(label) - u.px(12), Y: rr.Y + (rr.H-u.px(20))/2, W: f.Measure(label) + u.px(12), H: u.px(20)}
		u.c.RoundRect(chip, u.px(5), gfx.Mix(colBackground, colSuccess, 0.18))
		u.label(f, chip, label, colSuccess, 0.5)
		u.hint(id+"/link", u.t("The link id: smart objects with the same one share their layers; click to copy it"), chip)
		if u.clicked(id+"/link", chip) {
			d.Choose(n.ID)
			a.copyLinkID(ad)
		}
		right = chip.X - u.px(6)
	}
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
		if _, done := u.inlineBox("art/rename", name, &ad.rename, 12.5, u.px(4)); done || u.focus != "art/rename" {
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
	if a.ctrlSelect(ad, id, rr, func(mode paint.SelectMode) error { return d.SelectContent(n.ID, mode) }) {
		return
	}
	if u.press(id, rr) {
		if u.shift && n.ID != d.Active {
			// Shift+click chooses more layers besides the chosen one, or lets one go, as booleans need.
			ad.togglePicked(n.ID)
			u.letGo(id)
			return
		}
		ad.layerHeld = 0
		if len(ad.picked) > 0 && (n.ID == d.Active || slices.Contains(ad.picked, n.ID)) {
			// One of several chosen layers: they stay chosen, to be dragged together, until the press ends
			// without a drag.
			ad.layerHeld = n.ID
		} else {
			ad.picked = nil
		}
		if u.double {
			ad.renaming, ad.rename = n.ID, n.Name
			u.focusEdit("art/rename", n.Name, false)
		}
		if d.Active != n.ID {
			if i := slices.Index(ad.picked, n.ID); i >= 0 {
				ad.picked[i] = d.Active // the chosen stay chosen, this one leading them
			}
			d.Choose(n.ID)
			d.EditMask = n.Adjust != nil
		}
	}
	if u.letGo(id) && ad.layerHeld == n.ID {
		ad.picked, ad.layerHeld = nil, 0
	}
}

// ctrlSelect makes a selection with sel when a Ctrl+press lands in r, as Ctrl+click on a layer's name or thumbnail
// does in Photoshop: a new selection, with Shift added to it, with Alt taken from it, with both where they meet. It
// holds the press under its own id, so the layer is not chosen as a plain click on r would choose it.
func (a *App) ctrlSelect(ad *artDoc, id string, r gfx.Rect, sel func(paint.SelectMode) error) bool {
	u := &a.u
	if !u.ctrl || !u.press(id+"/select", r) {
		return false
	}
	mode := paint.SelectNew
	switch {
	case u.shift && u.alt:
		mode = paint.SelectIntersect
	case u.shift:
		mode = paint.SelectAdd
	case u.alt:
		mode = paint.SelectSubtract
	}
	a.artRun(ad, func() error { return sel(mode) })
	return true
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
	for _, b := range ad.d.Boards() {
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
	u.c.Fill(r, checkLight)
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

// clipThumbKey keeps vector mask thumbnails apart from the layers' and masks' own.
const clipThumbKey = 1 << 40

// drawClipThumb is a small picture of a vector mask, white inside its outline and black beyond.
func (a *App) drawClipThumb(ad *artDoc, n *paint.Node, r gfx.Rect) {
	u := &a.u
	dst := a.thumbArea(ad, n, r)
	size := dst.Pixels().Size()
	if size.X <= 0 || size.Y <= 0 {
		return
	}
	img := a.thumbFor(ad, clipThumbKey+n.ID, n.Clip, size, func(t *image.RGBA) {
		e := thumbExtent(ad, n)
		shape := *n.Clip
		shape.Fill, shape.Stroke = solidPaint(color.NRGBA{R: 255, G: 255, B: 255, A: 255}), paint.VectorStroke{}
		shape.M = shape.M.Then(paint.Translate(-float64(e.Min.X), -float64(e.Min.Y))).Then(paint.Affine{A: float64(size.X) / float64(e.Dx()), E: float64(size.Y) / float64(e.Dy())})
		draw.Draw(t, t.Rect, image.Black, image.Point{}, draw.Src)
		shape.Render(t, t.Rect, 1)
	})
	u.c.Image(img, dst)
}

func (a *App) drawLayerCommands(ad *artDoc, r gfx.Rect) {
	u := &a.u
	d := ad.d
	x := r.X
	type cmd struct {
		id, glyph, hint string
		run             func() error
	}
	// With several layers chosen, the group button puts them into a group, as Affinity Designer's does.
	group := cmd{"group", icNewGroup, u.t("New group"), func() error { d.AddGroup(a.groupName()); return nil }}
	if len(ad.picked) > 0 {
		group = cmd{"group", icNewGroup, withKey(u.t("Group layers"), a.keyHint("draw.group-layers", keys(key('G', "c")))), func() error { return a.groupChosen(ad, a.groupName()) }}
	}
	cmds := []cmd{
		{"new", icNewLayer, withKey(u.t("New layer"), a.keyHint("draw.new-layer", keys(newLayerKeys...))), func() error { d.AddLayer(a.layerName()); return nil }},
		group,
		{"mask", icMask, u.t("Add layer mask"), d.AddMask},
		{"dup", icDuplicate, withKey(u.t("Duplicate layer"), "Ctrl+J"), func() error { _, err := d.Duplicate(u.t(" copy")); return err }},
		{"up", icUp, u.t("Move up"), func() error { return d.Move(true) }},
		{"down", icDown, u.t("Move down"), func() error { return d.Move(false) }},
		{"style", icEffects, u.t("Layer style…"), func() error { a.openStyle(ad); return nil }},
		{"delete", icDelete, u.t("Delete layer"), d.Delete},
	}
	// Showing only the smart objects, at the start: a toggle rather than a command.
	only := gfx.Rect{X: x, Y: r.Y, W: u.px(30), H: u.px(30)}
	if u.iconButton("art/cmd/smartonly", only, icSmartFilter, u.t("Show only the smart objects"), a.settings.Paint.SmartOnly, true) {
		a.settings.Paint.SmartOnly = !a.settings.Paint.SmartOnly
		a.layersScroll = 0
		a.saveSettings()
	}
	x = only.Right() + u.px(6)
	// The buttons share what room the panel has, the menu of more commands at the end.
	gap, moreW := u.px(2), u.px(30)
	w := min(u.px(32), (r.Right()-x-moreW-gap*float32(len(cmds)))/float32(len(cmds)))
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
