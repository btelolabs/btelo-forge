package ui

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"math"
	"math/rand/v2"
	"slices"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// The adjustments and filters with settings, each tried in a dialog with a live preview.
const (
	adjustBrightness = iota
	adjustLevels
	adjustCurves
	adjustHue
	adjustPosterize
	adjustThreshold
	filterBlur
	filterNoise
	filterMosaic
	filterSharpen
	adjustColorBalance
	adjustVibrance
	adjustSelective
	adjustPhotoFilter
	adjustGradientMap
	adjustExposure
	adjustShadowsHighlights
	filterDevelop
)

// adjustParam is a setting of an adjustment: a slider with its range and how its value reads.
type adjustParam struct {
	label    string
	lo, hi   float64
	initial  float64
	whole    bool // whole numbers only
	unit     string
	decimals int
}

// adjustSwatch is a colour among an adjustment's settings: three values after its sliders, set from the foreground
// colour.
type adjustSwatch struct {
	label   string
	initial [3]float64
}

// adjustKind is an adjustment's dialog: its title, its name in the history, its settings and what it does with them.
type adjustKind struct {
	title   string
	step    string // the history's name, which the history panel translates
	params  []adjustParam
	pages   []string       // the same sliders for each of these, chosen in a list, or one set of them
	pagesOf string         // what the list chooses
	colours []adjustSwatch // after the sliders' values
	toggle  string         // a checkbox, or none
	filter  func(v []float64, on bool, curve []paint.CurvePoint, seed uint64) paint.Filter
	curve   bool // a curve to edit
	levels  bool // the input levels over a histogram
	height  float32
	initial bool // the toggle's starting state
}

// startValues are the settings a dialog opens with, every page's and then the colours'.
func (k adjustKind) startValues() []float64 {
	var v []float64
	for range max(len(k.pages), 1) {
		for _, p := range k.params {
			v = append(v, p.initial)
		}
	}
	for _, s := range k.colours {
		v = append(v, s.initial[:]...)
	}
	return v
}

// percentSliders are sliders from -100 to 100 that start at nothing.
func percentSliders(labels ...string) []adjustParam {
	out := make([]adjustParam, len(labels))
	for i, l := range labels {
		out[i] = adjustParam{label: l, lo: -100, hi: 100, whole: true}
	}
	return out
}

func (a *App) adjustKinds() map[int]adjustKind {
	u := &a.u
	return map[int]adjustKind{
		adjustColorBalance: {title: u.t("Color balance"), step: "Color balance", toggle: u.t("Preserve luminosity"), initial: true,
			pages: u.ts(toneRangeNames), pagesOf: u.t("Tones"), params: percentSliders(u.t("Cyan – Red"), u.t("Magenta – Green"), u.t("Yellow – Blue")),
			filter: layerFilter(adjustColorBalance)},
		adjustVibrance: {title: u.t("Vibrance"), step: "Vibrance", params: percentSliders(u.t("Vibrance"), u.t("Saturation")), filter: layerFilter(adjustVibrance)},
		adjustSelective: {title: u.t("Selective color"), step: "Selective color", toggle: u.t("Relative"), initial: true,
			pages: u.ts(selectiveColourNames), pagesOf: u.t("Colors"), params: percentSliders(u.t("Cyan"), u.t("Magenta"), u.t("Yellow"), u.tc("ink", "Black")),
			filter: layerFilter(adjustSelective)},
		adjustPhotoFilter: {title: u.t("Photo filter"), step: "Photo filter", toggle: u.t("Preserve luminosity"), initial: true,
			params:  []adjustParam{{label: u.t("Density"), lo: 0, hi: 100, initial: 25, whole: true, unit: "%"}},
			colours: []adjustSwatch{{label: u.t("Color"), initial: [3]float64{236, 138, 0}}}, filter: layerFilter(adjustPhotoFilter)},
		adjustGradientMap: {title: u.t("Gradient map"), step: "Gradient map",
			params:  []adjustParam{{label: u.t("Midpoint"), lo: 1, hi: 99, initial: 50, whole: true, unit: "%"}},
			colours: []adjustSwatch{{label: u.t("Shadows"), initial: [3]float64{0, 0, 0}}, {label: u.t("Highlights"), initial: [3]float64{255, 255, 255}}},
			filter:  layerFilter(adjustGradientMap)},
		adjustExposure: {title: u.t("Exposure"), step: "Exposure", params: []adjustParam{
			{label: u.t("Exposure"), lo: -10, hi: 10, decimals: 2},
			{label: u.t("Offset"), lo: -0.5, hi: 0.5, decimals: 3},
			{label: u.t("Gamma"), lo: 0.01, hi: 9.99, initial: 1, decimals: 2},
		}, filter: layerFilter(adjustExposure)},
		adjustShadowsHighlights: {title: u.t("Shadows/Highlights"), step: "Shadows/Highlights", params: []adjustParam{
			{label: u.t("Shadows"), lo: 0, hi: 100, initial: 35, whole: true, unit: "%"},
			{label: u.t("Highlights"), lo: 0, hi: 100, whole: true, unit: "%"},
			{label: u.t("Radius"), lo: 1, hi: 250, initial: 30, whole: true, unit: "px"},
		}, filter: func(v []float64, _ bool, _ []paint.CurvePoint, _ uint64) paint.Filter {
			return paint.ShadowsHighlights(v[0], v[1], v[2])
		}},
		filterDevelop: {title: u.t("Develop"), step: "Develop", params: append(append(percentSliders(u.t("Temperature"), u.t("Tint")),
			adjustParam{label: u.t("Exposure"), lo: -5, hi: 5, decimals: 2}),
			percentSliders(u.t("Contrast"), u.t("Highlights"), u.t("Shadows"), u.t("Whites"), u.t("Blacks"), u.t("Clarity"), u.t("Vibrance"), u.t("Saturation"))...),
			filter: func(v []float64, _ bool, _ []paint.CurvePoint, _ uint64) paint.Filter { return developOf(v).Filter() }},
		adjustBrightness: {title: u.t("Brightness/Contrast"), step: "Brightness/Contrast", params: []adjustParam{
			{label: u.t("Brightness"), lo: -150, hi: 150, whole: true},
			{label: u.t("Contrast"), lo: -50, hi: 100, whole: true},
		}, filter: layerFilter(adjustBrightness)},
		adjustLevels: {title: u.t("Levels"), step: "Levels", levels: true, params: []adjustParam{
			{label: u.t("Input black"), lo: 0, hi: 253, whole: true},
			{label: u.t("Midtones"), lo: 0.1, hi: 9.99, initial: 1, decimals: 2},
			{label: u.t("Input white"), lo: 2, hi: 255, initial: 255, whole: true},
			{label: u.t("Output black"), lo: 0, hi: 255, whole: true},
			{label: u.t("Output white"), lo: 0, hi: 255, initial: 255, whole: true},
		}, filter: layerFilter(adjustLevels)},
		adjustCurves: {title: u.t("Curves"), step: "Curves", curve: true, filter: layerFilter(adjustCurves)},
		adjustHue: {title: u.t("Hue/Saturation"), step: "Hue/Saturation", toggle: u.t("Colorize"), params: []adjustParam{
			{label: u.t("Hue"), lo: -180, hi: 180, whole: true, unit: "°"},
			{label: u.t("Saturation"), lo: -100, hi: 100, whole: true},
			{label: u.t("Lightness"), lo: -100, hi: 100, whole: true},
		}, filter: layerFilter(adjustHue)},
		adjustPosterize: {title: u.t("Posterize"), step: "Posterize", params: []adjustParam{
			{label: u.t("Levels"), lo: 2, hi: 255, initial: 4, whole: true},
		}, filter: layerFilter(adjustPosterize)},
		adjustThreshold: {title: u.t("Threshold"), step: "Threshold", levels: true, params: []adjustParam{
			{label: u.t("Threshold level"), lo: 1, hi: 255, initial: 128, whole: true},
		}, filter: layerFilter(adjustThreshold)},
		filterBlur: {title: u.t("Gaussian blur"), step: "Gaussian blur", params: []adjustParam{
			{label: u.t("Radius"), lo: 0.1, hi: 250, initial: 2, unit: "px", decimals: 1},
		}, filter: func(v []float64, _ bool, _ []paint.CurvePoint, _ uint64) paint.Filter {
			return paint.GaussianBlur(v[0])
		}},
		filterNoise: {title: u.t("Add noise"), step: "Add noise", toggle: u.t("Monochromatic"), params: []adjustParam{
			{label: u.t("Amount"), lo: 0.1, hi: 400, initial: 12.5, unit: "%", decimals: 1},
		}, filter: func(v []float64, on bool, _ []paint.CurvePoint, seed uint64) paint.Filter {
			return paint.AddNoise(v[0], on, seed)
		}},
		filterMosaic: {title: u.t("Mosaic"), step: "Mosaic", params: []adjustParam{
			{label: u.t("Cell size"), lo: 2, hi: 200, initial: 8, whole: true, unit: "px"},
		}, filter: func(v []float64, _ bool, _ []paint.CurvePoint, _ uint64) paint.Filter { return paint.Mosaic(int(v[0])) }},
		filterSharpen: {title: u.t("Unsharp mask"), step: "Unsharp mask", params: []adjustParam{
			{label: u.t("Amount"), lo: 1, hi: 500, initial: 50, whole: true, unit: "%"},
			{label: u.t("Radius"), lo: 0.1, hi: 100, initial: 1, unit: "px", decimals: 1},
			{label: u.t("Threshold"), lo: 0, hi: 255, whole: true},
		}, filter: func(v []float64, _ bool, _ []paint.CurvePoint, _ uint64) paint.Filter {
			return paint.UnsharpMask(v[0], v[1], v[2])
		}},
	}
}

// selectiveColourNames name Selective Color's pages, in the order of its values.
var selectiveColourNames = msgs("Reds", "Yellows", "Greens", "Cyans", "Blues", "Magentas", "Whites", "Neutrals", "Blacks")

// developOf is Develop's settings from its sliders, in their order.
func developOf(v []float64) paint.Develop {
	return paint.Develop{Temperature: v[0], Tint: v[1], Exposure: v[2], Contrast: v[3], Highlights: v[4], Shadows: v[5],
		Whites: v[6], Blacks: v[7], Clarity: v[8], Vibrance: v[9], Saturate: v[10]}
}

// adjustGlyph is an adjustment layer's icon in the layers panel.
func adjustGlyph(k paint.AdjustKind) string {
	switch k {
	case paint.AdjustInvert:
		return "\ue891"
	case paint.AdjustBlackWhite:
		return "\ue3db"
	case paint.AdjustHue:
		return "\ue40a"
	case paint.AdjustCurves, paint.AdjustLevels:
		return "\ue427"
	}
	return "\ue3ab"
}

// layerKinds are the adjustments that can also be adjustment layers, with what they are in the picture.
var layerKinds = map[int]paint.AdjustKind{adjustBrightness: paint.AdjustBrightness, adjustLevels: paint.AdjustLevels,
	adjustCurves: paint.AdjustCurves, adjustHue: paint.AdjustHue, adjustPosterize: paint.AdjustPosterize, adjustThreshold: paint.AdjustThreshold,
	adjustColorBalance: paint.AdjustColorBalance, adjustVibrance: paint.AdjustVibrance, adjustSelective: paint.AdjustSelectiveColor,
	adjustPhotoFilter: paint.AdjustPhotoFilter, adjustGradientMap: paint.AdjustGradientMap, adjustExposure: paint.AdjustExposure}

// adjustmentOf is the dialog's settings for an adjustment layer.
func adjustmentOf(kind int, v []float64, on bool, curve []paint.CurvePoint) paint.Adjustment {
	adj := paint.Adjustment{Kind: layerKinds[kind], Values: slices.Clone(v), On: on, Curve: slices.Clone(curve)}
	if kind == adjustCurves {
		adj.Values = nil
	} else {
		adj.Curve = nil
	}
	return adj
}

// layerFilter is an adjustment's change, the same whether applied to pixels or held by an adjustment layer.
func layerFilter(kind int) func(v []float64, on bool, curve []paint.CurvePoint, _ uint64) paint.Filter {
	return func(v []float64, on bool, curve []paint.CurvePoint, _ uint64) paint.Filter {
		return adjustmentOf(kind, v, on, curve).Filter()
	}
}

// adjustDialog is an adjustment or filter being tried: its settings, and its preview, worked out on another
// goroutine and shown when ready so dragging a slider stays smooth on a large picture.
type adjustDialog struct {
	kind         int
	ad           *artDoc
	s            *paint.FilterSession
	layer        *paint.AdjustSession      // or, instead, an adjustment layer's settings
	smart        *paint.SmartFilterSession // or a smart object's filter
	smartInitial paint.SmartFilter         // the filter when the dialog opened, shown without the preview
	initial      paint.Adjustment          // the layer's settings when the dialog opened, shown without the preview
	values       []float64
	on           bool
	curve        []paint.CurvePoint
	grab         int // the curve point being dragged, or -1
	page         int // which of the kind's pages the sliders show
	preview      bool
	seed         uint64
	hist         [256]int // the brightness histogram, for Levels and Threshold
	shown        string   // the settings the picture shows
	pending      string   // the settings being worked out
	busy         bool
}

// openAdjust starts trying an adjustment or filter on the chosen layer.
func (a *App) openAdjust(ad *artDoc, kind int) {
	if ad.stroke != nil || ad.move != nil {
		return
	}
	if n := ad.d.Node(ad.d.Active); n != nil && n.IsSmart() {
		a.openSmartFilter(ad, kind)
		return
	}
	s, err := ad.d.BeginFilter()
	if err != nil {
		a.artFailed(ad, err)
		return
	}
	k := a.adjustKinds()[kind]
	f := &adjustDialog{kind: kind, ad: ad, s: s, preview: true, grab: -1, on: k.initial, seed: rand.Uint64(), values: k.startValues()}
	if k.curve {
		f.curve = []paint.CurvePoint{{In: 0, Out: 0}, {In: 255, Out: 255}}
	}
	if k.levels {
		f.hist = histogram(ad.d.Node(ad.d.Active).Pixels, s.Pieces())
	}
	f.shown = f.key() // nothing is shown yet: the picture is as it was
	a.adjust = f
}

// openAdjustLayer makes an adjustment layer of the kind and opens its settings: OK keeps it, Cancel takes it away.
func (a *App) openAdjustLayer(ad *artDoc, kind int) {
	k := a.adjustKinds()[kind]
	f := &adjustDialog{kind: kind, ad: ad, preview: true, grab: -1, on: k.initial, values: k.startValues()}
	if k.curve {
		f.curve = []paint.CurvePoint{{In: 0, Out: 0}, {In: 255, Out: 255}}
	}
	s, err := ad.d.BeginNewAdjustment(adjustmentOf(kind, f.values, f.on, f.curve), fmt.Sprintf("%s %d", k.title, len(ad.d.Layers())+1))
	if err != nil {
		a.artFailed(ad, err)
		return
	}
	f.layer, f.initial = s, s.Adjustment()
	a.openLayerDialog(f)
}

// openAdjustmentSettings opens an adjustment layer's settings to change them.
func (a *App) openAdjustmentSettings(ad *artDoc, id int) {
	s, err := ad.d.BeginAdjust(id)
	if err != nil {
		a.artFailed(ad, err)
		return
	}
	adj := s.Adjustment()
	kind := -1
	for k, pk := range layerKinds {
		if pk == adj.Kind {
			kind = k
		}
	}
	if kind < 0 {
		s.Cancel() // invert and black & white have no settings
		return
	}
	f := &adjustDialog{kind: kind, ad: ad, preview: true, grab: -1, layer: s, initial: adj,
		values: slices.Clone(adj.Values), on: adj.On, curve: slices.Clone(adj.Curve)}
	a.openLayerDialog(f)
}

// openLayerDialog shows an adjustment layer's dialog, with the histogram of the picture under it.
func (a *App) openLayerDialog(f *adjustDialog) {
	ad := f.ad
	if a.adjustKinds()[f.kind].levels {
		// The picture as it looks, at about 512 pixels across, so a large canvas costs little.
		e := ad.d.Extent()
		if !e.Empty() {
			l := paint.LevelFor(512 / float64(max(e.Dx(), e.Dy())))
			lr := image.Rect(e.Min.X>>l, e.Min.Y>>l, (e.Max.X+(1<<l)-1)>>l, (e.Max.Y+(1<<l)-1)>>l)
			img := image.NewRGBA(lr)
			ad.d.Render(img, lr, l)
			f.hist = addHistogram(f.hist, img)
		}
	}
	f.shown = ""
	a.adjust = f
	ad.changedAll()
}

// histogram counts brightness levels in the pieces of a layer.
func histogram(layer *paint.Raster, pieces []image.Rectangle) [256]int {
	var h [256]int
	for _, r := range pieces {
		h = addHistogram(h, layer.Read(r))
	}
	return h
}

// addHistogram adds the brightness levels of an image's visible pixels to h.
func addHistogram(h [256]int, p *image.RGBA) [256]int {
	r := p.Rect
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			i := p.PixOffset(x, y)
			if p.Pix[i+3] == 0 {
				continue
			}
			luma := (299*int(p.Pix[i]) + 587*int(p.Pix[i+1]) + 114*int(p.Pix[i+2]) + 500) / 1000 // premultiplied
			h[min(luma*255/int(p.Pix[i+3]), 255)]++
		}
	}
	return h
}

// key identifies the settings, to tell when the preview is out of date.
func (f *adjustDialog) key() string {
	return fmt.Sprint(f.values, f.on, f.curve, f.preview)
}

// filter is the adjustment with the dialog's settings.
func (a *App) adjustFilter(f *adjustDialog) paint.Filter {
	return a.adjustKinds()[f.kind].filter(f.values, f.on, f.curve, f.seed)
}

// refreshPreview works the preview out again when the settings changed, on another goroutine, and shows it.
func (a *App) refreshPreview(f *adjustDialog) {
	want := f.key()
	if f.busy || want == f.shown {
		return
	}
	if f.smart != nil {
		sf := f.smartInitial
		if f.preview {
			sf = smartFilterOf(f)
			sf.Off = f.smartInitial.Off
		}
		if err := f.smart.Set(sf); err == nil {
			f.ad.changedAll()
		}
		f.shown = want
		return
	}
	if f.layer != nil {
		// An adjustment layer's settings show at once: the picture is only drawn with them.
		adj := f.initial
		if f.preview {
			adj = adjustmentOf(f.kind, f.values, f.on, f.curve)
		}
		if err := f.layer.Set(adj); err == nil {
			f.ad.changedAll()
		}
		f.shown = want
		return
	}
	if !f.preview {
		f.ad.touched(f.s.Show(nil))
		f.shown = want
		return
	}
	f.busy, f.pending = true, want
	fn, s := a.adjustFilter(f), f.s
	a.background(func(context.Context) {
		out := s.Compute(fn)
		a.post(func() {
			f.busy = false
			if a.adjust != f {
				return // closed meanwhile
			}
			f.ad.touched(f.s.Show(out))
			f.shown = f.pending
		})
	})
}

// closeAdjust applies the adjustment, or puts the pixels back.
func (a *App) closeAdjust(apply bool) {
	f := a.adjust
	a.adjust = nil
	if f == nil {
		return
	}
	switch {
	case f.smart != nil && apply:
		sf := smartFilterOf(f)
		sf.Off = f.smartInitial.Off
		f.smart.Set(sf)
		f.smart.End()
	case f.smart != nil:
		f.smart.Cancel()
	case f.layer != nil && apply:
		f.layer.Set(adjustmentOf(f.kind, f.values, f.on, f.curve))
		f.layer.End()
	case f.layer != nil:
		f.layer.Cancel()
	case apply:
		f.ad.touched(f.s.Apply(a.adjustKinds()[f.kind].step, a.adjustFilter(f)))
	default:
		f.ad.touched(f.s.Cancel())
	}
	f.ad.changedAll()
}

func (a *App) drawAdjustDialog() {
	f := a.adjust
	if f == nil {
		return
	}
	if a.art != f.ad {
		a.closeAdjust(false) // its tab is no longer shown
		return
	}
	u := &a.u
	// The list of pages lies over the dialog and has the pointer and the keys while it is open.
	listed := a.menuOpen("adjust/page")
	u.blocked = listed
	defer func() { u.blocked = false }()
	k := a.adjustKinds()[f.kind]
	h := u.px(130) + float32(len(k.params))*u.px(40)
	h += float32(len(k.colours)) * u.px(36)
	if len(k.pages) > 0 {
		h += u.px(40)
	}
	if k.toggle != "" {
		h += u.px(36)
	}
	if k.levels {
		h += u.px(110)
	}
	if k.curve {
		h += u.px(290)
	}
	box := rectCentered(a.screen, u.px(420), h)
	// The dialog sits to the right, leaving the picture in view as Photoshop's do.
	box.X = min(a.screen.W-box.W-u.px(330), box.X+u.px(200))
	panel(u, box)
	in := box.Inset(u.px(22))
	u.label(u.bold(16), gfx.Rect{X: in.X, Y: in.Y, W: in.W, H: u.px(24)}, k.title, colText, 0)
	y := in.Y + u.px(40)
	if k.levels {
		a.drawHistogram(gfx.Rect{X: in.X, Y: y, W: in.W, H: u.px(96)}, f.hist)
		y += u.px(110)
	}
	if k.curve {
		a.drawCurveEditor(f, gfx.Rect{X: in.X + (in.W-u.px(270))/2, Y: y, W: u.px(270), H: u.px(270)})
		y += u.px(290)
	}
	if len(k.pages) > 0 {
		u.label(u.face(12.5), gfx.Rect{X: in.X, Y: y, W: u.px(110), H: u.px(28)}, k.pagesOf, colDim, 0)
		a.dropdown("adjust/page", gfx.Rect{X: in.X + u.px(110), Y: y, W: in.W - u.px(110), H: u.px(28)}, k.pages, f.page, nil, func(i int) { f.page = i })
		y += u.px(40)
	}
	for i, p := range k.params {
		at := f.page*len(k.params) + i
		lw := u.px(110)
		u.label(u.face(12.5), gfx.Rect{X: in.X, Y: y, W: lw, H: u.px(28)}, p.label, colDim, 0)
		sr := gfx.Rect{X: in.X + lw, Y: y + u.px(11), W: in.W - lw - u.px(90), H: u.px(6)}
		u.slider(fmt.Sprintf("adjust/%d", at), sr, &f.values[at], p.lo, p.hi, true)
		if p.whole {
			f.values[at] = math.Round(f.values[at])
		}
		text := strconvFloat(f.values[at], p.decimals) + p.unit
		u.label(u.face(12.5), gfx.Rect{X: sr.Right() + u.px(12), Y: y, W: u.px(78), H: u.px(28)}, text, colText, 0)
		y += u.px(40)
	}
	for i, s := range k.colours {
		a.drawAdjustSwatch(gfx.Rect{X: in.X, Y: y, W: in.W, H: u.px(28)}, s.label, i, f.values[len(f.values)-3*(len(k.colours)-i):])
		y += u.px(36)
	}
	if k.toggle != "" {
		f.on = u.checkbox("adjust/toggle", gfx.Rect{X: in.X, Y: y, W: in.W, H: u.px(28)}, k.toggle, f.on)
		y += u.px(36)
	}
	foot := in.Bottom() - u.px(34)
	f.preview = u.checkbox("adjust/preview", gfx.Rect{X: in.X, Y: foot, W: u.px(110), H: u.px(34)}, u.t("Preview"), f.preview)
	cancel := u.button("adjust/cancel", gfx.Rect{X: in.Right() - u.px(210), Y: foot, W: u.px(100), H: u.px(34)}, "", u.t("Cancel"), ghost, true)
	ok := u.button("adjust/ok", gfx.Rect{X: in.Right() - u.px(100), Y: foot, W: u.px(100), H: u.px(34)}, "", u.t("OK"), primary, true)
	if !listed {
		for _, key := range u.keys {
			switch key.Code {
			case keyEscape:
				cancel = true
			case keyEnter:
				ok = true
			}
		}
		u.keys = nil
	}
	switch {
	case ok:
		a.closeAdjust(true)
	case cancel:
		a.closeAdjust(false)
	default:
		a.refreshPreview(f)
	}
}

// drawAdjustSwatch is a colour among the settings, c its three values; a click takes the foreground colour, as the
// layer style dialog's do.
func (a *App) drawAdjustSwatch(r gfx.Rect, label string, i int, c []float64) {
	u := &a.u
	id := fmt.Sprintf("adjust/colour/%d", i)
	u.label(u.face(12.5), gfx.Rect{X: r.X, Y: r.Y, W: u.px(110), H: r.H}, label, colDim, 0)
	sw := gfx.Rect{X: r.X + u.px(110), Y: r.Y + u.px(2), W: u.px(44), H: r.H - u.px(4)}
	u.c.Fill(sw.Inset(-max(1, u.px(1))), colBorder)
	u.c.Fill(sw, color.RGBA{uint8(c[0]), uint8(c[1]), uint8(c[2]), 255})
	u.hint(id, u.tf("%s: click to use the foreground colour", label), sw)
	if u.hover(id, sw) {
		u.cursor = CursorHand
	}
	if u.clicked(id, sw) {
		fg := a.brush.Color
		c[0], c[1], c[2] = float64(fg.R), float64(fg.G), float64(fg.B)
	}
}

func strconvFloat(v float64, decimals int) string { return fmt.Sprintf("%.*f", decimals, v) }

// drawHistogram draws how many pixels have each brightness.
func (a *App) drawHistogram(r gfx.Rect, h [256]int) {
	u := &a.u
	u.c.Fill(r, colBackground)
	peak := 1
	for _, n := range h {
		peak = max(peak, n)
	}
	in := r.Inset(u.px(6))
	w := in.W / 256
	for i, n := range h {
		bh := in.H * float32(math.Sqrt(float64(n)/float64(peak))) // a square root keeps small counts visible
		u.c.Fill(gfx.Rect{X: in.X + float32(i)*w, Y: in.Bottom() - bh, W: max(w, 1), H: bh}, colDim)
	}
}

// drawCurveEditor is Curves' graph: click to add a point, drag one to move it, drag it out of the graph to remove
// it (the ends stay).
func (a *App) drawCurveEditor(f *adjustDialog, r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colBackground)
	for i := 1; i < 4; i++ {
		t := float32(i) / 4
		u.c.Fill(gfx.Rect{X: r.X + r.W*t, Y: r.Y, W: max(1, u.px(1)), H: r.H}, colBorder)
		u.c.Fill(gfx.Rect{X: r.X, Y: r.Y + r.H*t, W: r.W, H: max(1, u.px(1))}, colBorder)
	}
	u.c.Line(r.X, r.Bottom(), r.Right(), r.Y, max(1, u.px(1)), colFaint)
	toScreen := func(p paint.CurvePoint) (float32, float32) {
		return r.X + float32(p.In/255)*r.W, r.Bottom() - float32(p.Out/255)*r.H
	}
	fromScreen := func(x, y float32) paint.CurvePoint {
		return paint.CurvePoint{In: math.Round(float64(min(max((x-r.X)/r.W, 0), 1)) * 255),
			Out: math.Round(float64(min(max((r.Bottom()-y)/r.H, 0), 1)) * 255)}
	}
	fn := paint.Curve(f.curve)
	px, py := r.X, r.Bottom()-float32(fn(0))*r.H
	for i := 1; i <= 128; i++ {
		x := float64(i) / 128
		nx, ny := r.X+float32(x)*r.W, r.Bottom()-float32(fn(x))*r.H
		u.c.Line(px, py, nx, ny, u.px(2), colText)
		px, py = nx, ny
	}
	const id = "adjust/curve"
	hit := func() int {
		for i, p := range f.curve {
			x, y := toScreen(p)
			if dx, dy := u.mx-x, u.my-y; dx*dx+dy*dy <= u.px(8)*u.px(8) {
				return i
			}
		}
		return -1
	}
	if u.hover(id, r.Inset(-u.px(6))) {
		u.cursor = CursorCross
	}
	if u.press(id, r.Inset(-u.px(6))) {
		f.grab = hit()
		if f.grab < 0 {
			f.curve = append(f.curve, fromScreen(u.mx, u.my))
			f.grab = len(f.curve) - 1
		}
	}
	if u.dragging(id) && f.grab >= 0 {
		p := fromScreen(u.mx, u.my)
		ends := f.grab < 2 // the first two points are the ends: they stay on their side
		switch {
		case ends && f.grab == 0:
			p.In = min(p.In, 254)
		case ends:
			p.In = max(p.In, 1)
		}
		far := !r.Inset(-u.px(30)).Contains(u.mx, u.my)
		if far && !ends {
			f.curve = append(f.curve[:f.grab], f.curve[f.grab+1:]...)
			f.grab = -1
		} else {
			// Two points cannot share an input level.
			taken := false
			for i, q := range f.curve {
				taken = taken || i != f.grab && q.In == p.In
			}
			if !taken {
				f.curve[f.grab] = p
			}
		}
	}
	if u.letGo(id) {
		f.grab = -1
	}
	for i, p := range f.curve {
		x, y := toScreen(p)
		s := u.px(4)
		col := colWhite
		if i == f.grab {
			col = colAccentHi
		}
		u.c.Fill(gfx.Rect{X: x - s - 1, Y: y - s - 1, W: 2*s + 2, H: 2*s + 2}, colBlack)
		u.c.Fill(gfx.Rect{X: x - s, Y: y - s, W: 2 * s, H: 2 * s}, col)
	}
}
