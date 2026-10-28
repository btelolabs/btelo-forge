package paint

import (
	"errors"
	"image"
	"image/color"
	"math"
)

// Layer styles, as Photoshop's: effects drawn from a layer's shape (its pixels' transparency, after its mask) every
// time it is shown, so they follow any change to the layer. Drop shadow and outer glow lie under the layer, each with
// its own blend mode; inner shadow, inner glow and colour overlay lie on it, within its shape; the stroke runs round
// it. The layer's opacity and blend mode apply to them all. Growing and shrinking the shape use an exact distance
// transform, so strokes and spreads are round as Photoshop's; softening is three box blurs, close to a Gaussian.

// Shadow is a drop shadow or an inner shadow: a copy of the shape moved by Distance at Angle (degrees, the light's
// direction as in Photoshop: 120 lights from the top left), grown by Spread (a share of Size) and softened by Size.
type Shadow struct {
	Color    color.NRGBA `json:"color"`
	Blend    Blend       `json:"blend"`
	Opacity  float64     `json:"opacity"`
	Angle    float64     `json:"angle"`
	Distance float64     `json:"distance"`
	Spread   float64     `json:"spread"` // 0 to 1
	Size     float64     `json:"size"`
}

// Glow is an outer or inner glow: the shape's edge grown by Spread and softened by Size.
type Glow struct {
	Color   color.NRGBA `json:"color"`
	Blend   Blend       `json:"blend"`
	Opacity float64     `json:"opacity"`
	Spread  float64     `json:"spread"`
	Size    float64     `json:"size"`
}

// StrokePosition is where a layer style's stroke runs: outside the shape, inside it, or centred on its edge.
type StrokePosition int

const (
	StrokeOutside StrokePosition = iota
	StrokeInside
	StrokeCenter
)

// Outline is a layer style's stroke.
type Outline struct {
	Color    color.NRGBA    `json:"color"`
	Opacity  float64        `json:"opacity"`
	Size     float64        `json:"size"`
	Position StrokePosition `json:"position"`
}

// Overlay is a colour laid over the layer within its shape.
type Overlay struct {
	Color   color.NRGBA `json:"color"`
	Blend   Blend       `json:"blend"`
	Opacity float64     `json:"opacity"`
}

// LayerStyle is a layer's effects; nil ones are off. It is a value never changed in place.
type LayerStyle struct {
	DropShadow   *Shadow  `json:"dropShadow,omitempty"`
	InnerShadow  *Shadow  `json:"innerShadow,omitempty"`
	OuterGlow    *Glow    `json:"outerGlow,omitempty"`
	InnerGlow    *Glow    `json:"innerGlow,omitempty"`
	Stroke       *Outline `json:"stroke,omitempty"`
	ColorOverlay *Overlay `json:"colorOverlay,omitempty"`
}

// maxEffect is the largest size, distance or stroke an effect may have, in canvas pixels.
const maxEffect = 250

// Defaults, as Photoshop's.
func DefaultDropShadow() Shadow {
	return Shadow{Color: color.NRGBA{A: 255}, Blend: BlendMultiply, Opacity: 0.75, Angle: 120, Distance: 5, Size: 5}
}
func DefaultInnerShadow() Shadow {
	return Shadow{Color: color.NRGBA{A: 255}, Blend: BlendMultiply, Opacity: 0.75, Angle: 120, Distance: 5, Size: 5}
}
func DefaultOuterGlow() Glow {
	return Glow{Color: color.NRGBA{R: 255, G: 255, B: 190, A: 255}, Blend: BlendScreen, Opacity: 0.75, Size: 5}
}
func DefaultInnerGlow() Glow {
	return Glow{Color: color.NRGBA{R: 255, G: 255, B: 190, A: 255}, Blend: BlendScreen, Opacity: 0.75, Size: 5}
}
func DefaultOutline() Outline {
	return Outline{Color: color.NRGBA{R: 255, A: 255}, Opacity: 1, Size: 3, Position: StrokeOutside}
}
func DefaultOverlay() Overlay {
	return Overlay{Color: color.NRGBA{R: 255, A: 255}, Blend: BlendNormal, Opacity: 1}
}

// empty reports whether no effect is on.
func (s *LayerStyle) empty() bool {
	return s == nil || s.DropShadow == nil && s.InnerShadow == nil && s.OuterGlow == nil && s.InnerGlow == nil &&
		s.Stroke == nil && s.ColorOverlay == nil
}

// valid reports whether the effects' settings make sense.
func (s *LayerStyle) valid() bool {
	ok := func(v, lo, hi float64) bool { return !math.IsNaN(v) && v >= lo && v <= hi }
	blend := func(b Blend) bool { return b >= 0 && b < BlendPassThrough }
	for _, sh := range []*Shadow{s.DropShadow, s.InnerShadow} {
		if sh != nil && !(blend(sh.Blend) && ok(sh.Opacity, 0, 1) && ok(sh.Angle, -360, 360) && ok(sh.Distance, 0, maxEffect) &&
			ok(sh.Spread, 0, 1) && ok(sh.Size, 0, maxEffect)) {
			return false
		}
	}
	for _, g := range []*Glow{s.OuterGlow, s.InnerGlow} {
		if g != nil && !(blend(g.Blend) && ok(g.Opacity, 0, 1) && ok(g.Spread, 0, 1) && ok(g.Size, 0, maxEffect)) {
			return false
		}
	}
	if o := s.Stroke; o != nil && !(ok(o.Opacity, 0, 1) && ok(o.Size, 0, maxEffect) && o.Position >= StrokeOutside && o.Position <= StrokeCenter) {
		return false
	}
	if o := s.ColorOverlay; o != nil && !(blend(o.Blend) && ok(o.Opacity, 0, 1)) {
		return false
	}
	return true
}

// reach is how far, in canvas pixels, the effects reach beyond the layer's content.
func (s *LayerStyle) reach() int {
	if s.empty() {
		return 0
	}
	r := 0.0
	if sh := s.DropShadow; sh != nil {
		r = max(r, sh.Distance+sh.Size)
	}
	if g := s.OuterGlow; g != nil {
		r = max(r, g.Size)
	}
	if o := s.Stroke; o != nil {
		r = max(r, o.Size)
	}
	return int(math.Ceil(r)) + 2
}

// ErrStyleGroup means a layer style was asked of a group or an adjustment layer, which have no shape of their own.
var ErrStyleGroup = errors.New("layer styles go on pixel, text and vector layers")

// SetStyle gives the layer id the effects of st (nil or empty takes them away) as one step.
func (d *Doc) SetStyle(id int, st *LayerStyle) error {
	s, err := d.BeginStyle(id)
	if err != nil {
		return err
	}
	if err := s.Set(st); err != nil {
		return err
	}
	s.End()
	return nil
}

// StyleSession is a layer's effects being tried in the Layer Style dialog, shown as they change and recorded as one
// step.
type StyleSession struct {
	d    *Doc
	n    *Node
	mark Mark
}

// BeginStyle starts changing the effects of the layer id.
func (d *Doc) BeginStyle(id int) (*StyleSession, error) {
	n := d.Node(id)
	switch {
	case n == nil:
		return nil, ErrNoLayer
	case n.IsGroup() || n.Adjust != nil:
		return nil, ErrStyleGroup
	}
	return &StyleSession{d: d, n: n, mark: d.Mark()}, nil
}

// Style is the effects as they are now, never nil.
func (s *StyleSession) Style() LayerStyle {
	if s.n.Style == nil {
		return LayerStyle{}
	}
	return *s.n.Style
}

// Set shows the effects st.
func (s *StyleSession) Set(st *LayerStyle) error {
	if st == nil || st.empty() {
		s.n.Style = nil
		return nil
	}
	if !st.valid() {
		return errors.New("a layer effect's settings are out of range")
	}
	c := *st
	s.n.Style = &c
	return nil
}

// End records the change as one step.
func (s *StyleSession) End() { s.d.Commit("Layer style", s.mark) }

// Cancel puts the effects back.
func (s *StyleSession) Cancel() { s.d.restore(s.mark.s) }

// EffectReach is how far, in canvas pixels, any shown layer's effects reach beyond what it holds, for redrawing
// what a change to a layer touches.
func (d *Doc) EffectReach() int {
	r := 0
	for _, l := range d.Layers() {
		if l.Visible && l.Style != nil {
			r = max(r, l.Style.reach())
		}
	}
	return r
}

// ---- Drawing the effects

// plane of floats over a rectangle, for the effects' working.
type field struct {
	r image.Rectangle
	v []float64
}

func newField(r image.Rectangle) field { return field{r: r, v: make([]float64, r.Dx()*r.Dy())} }

func (f field) at(x, y int) float64 {
	if !(image.Point{x, y}).In(f.r) {
		return 0
	}
	return f.v[(y-f.r.Min.Y)*f.r.Dx()+x-f.r.Min.X]
}

// shifted is f moved by (dx, dy), in whole pixels.
func (f field) shifted(dx, dy int) field {
	out := newField(f.r)
	w := f.r.Dx()
	for y := f.r.Min.Y; y < f.r.Max.Y; y++ {
		for x := f.r.Min.X; x < f.r.Max.X; x++ {
			out.v[(y-f.r.Min.Y)*w+x-f.r.Min.X] = f.at(x-dx, y-dy)
		}
	}
	return out
}

// inverted is 1 - f.
func (f field) inverted() field {
	out := newField(f.r)
	for i, v := range f.v {
		out.v[i] = 1 - v
	}
	return out
}

// distanceOut is, for each pixel, how far its centre is from the nearest pixel at least half covered (0 there).
func (f field) distanceOut() []float64 {
	w, h := f.r.Dx(), f.r.Dy()
	d := make([]float64, w*h)
	for i, v := range f.v {
		if v < 0.5 {
			d[i] = math.Inf(1)
		}
	}
	edt2(d, w, h)
	for i := range d {
		d[i] = math.Sqrt(d[i])
	}
	return d
}

// grown is f grown by k pixels, anti-aliased at the new edge.
func (f field) grown(k float64) field {
	if k <= 0 {
		return f
	}
	dist := f.distanceOut()
	out := newField(f.r)
	for i, v := range f.v {
		out.v[i] = max(v, min(max(k+0.5-dist[i], 0), 1))
	}
	return out
}

// blurred is f softened over size pixels: three box blurs, each a third of it.
func (f field) blurred(size float64) field {
	rad := int(math.Round(size / 3))
	if rad <= 0 {
		return f
	}
	w, h := f.r.Dx(), f.r.Dy()
	a := append([]float64(nil), f.v...)
	b := make([]float64, len(a))
	pass := func(from, to []float64, n, lines, step, stride int) {
		inv := 1 / float64(2*rad+1)
		for line := range lines {
			base := line * stride
			at := func(i int) float64 {
				if i < 0 || i >= n {
					return 0 // beyond the working area nothing is covered
				}
				return from[base+i*step]
			}
			sum := 0.0
			for i := -rad; i <= rad; i++ {
				sum += at(i)
			}
			for i := range n {
				to[base+i*step] = sum * inv
				sum += at(i+rad+1) - at(i-rad)
			}
		}
	}
	for range 3 {
		pass(a, b, w, h, 1, w)
		pass(b, a, h, w, w, 1)
	}
	return field{r: f.r, v: a}
}

// edt2 turns d (0 at seeds, +Inf elsewhere) into squared distances to the nearest seed: Felzenszwalb and
// Huttenlocher's exact transform, a pass along rows then along columns.
func edt2(d []float64, w, h int) {
	n := max(w, h)
	f, z, out := make([]float64, n), make([]float64, n+1), make([]float64, n)
	v := make([]int, n)
	line := func(get func(int) float64, set func(int, float64), n int) {
		for i := range n {
			f[i] = get(i)
		}
		k := 0
		v[0], z[0], z[1] = 0, math.Inf(-1), math.Inf(1)
		for q := 1; q < n; q++ {
			if math.IsInf(f[q], 1) {
				continue
			}
			for {
				p := v[k]
				var s float64
				if math.IsInf(f[p], 1) {
					s = math.Inf(-1)
				} else {
					s = ((f[q] + float64(q*q)) - (f[p] + float64(p*p))) / float64(2*q-2*p)
				}
				if s <= z[k] && k > 0 {
					k--
					continue
				}
				if s <= z[k] {
					// The first parabola is at infinity: this one replaces it.
					v[k], z[k], z[k+1] = q, math.Inf(-1), math.Inf(1)
					break
				}
				k++
				v[k], z[k], z[k+1] = q, s, math.Inf(1)
				break
			}
		}
		k = 0
		for q := range n {
			for z[k+1] < float64(q) {
				k++
			}
			p := v[k]
			out[q] = float64((q-p)*(q-p)) + f[p]
		}
		for i := range n {
			set(i, out[i])
		}
	}
	for y := range h {
		line(func(i int) float64 { return d[y*w+i] }, func(i int, x float64) { d[y*w+i] = x }, w)
	}
	for x := range w {
		line(func(i int) float64 { return d[i*w+x] }, func(i int, v float64) { d[i*w+x] = v }, h)
	}
}

// solidOf is the colour c laid where a covers, over r, premultiplied.
func solidOf(c color.NRGBA, a field, r image.Rectangle) *image.RGBA {
	img := image.NewRGBA(r)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			k := min(max(a.at(x, y), 0), 1)
			if k == 0 {
				continue
			}
			i := img.PixOffset(x, y)
			al := float64(c.A) / 255 * k
			img.Pix[i] = uint8(math.Round(float64(c.R) * al))
			img.Pix[i+1] = uint8(math.Round(float64(c.G) * al))
			img.Pix[i+2] = uint8(math.Round(float64(c.B) * al))
			img.Pix[i+3] = uint8(math.Round(255 * al))
		}
	}
	return img
}

// times multiplies two fields.
func (f field) times(g field) field {
	out := newField(f.r)
	for i := range f.v {
		out.v[i] = f.v[i] * g.v[i]
	}
	return out
}

// shadowOffset is a shadow's move in pixels of detail level l: away from the light, as Photoshop's angle says.
func shadowOffset(sh *Shadow, l int) (int, int) {
	s := 1 / float64(int(1)<<l)
	rad := sh.Angle * math.Pi / 180
	return int(math.Round(-math.Cos(rad) * sh.Distance * s)), int(math.Round(math.Sin(rad) * sh.Distance * s))
}

// drawStyled composites layer n, which has effects, into dst within r at detail level l: the effects under it, the
// layer with the effects on it, and its stroke, through the layer's opacity and blend mode.
func (d *Doc) drawStyled(dst *image.RGBA, n *Node, r image.Rectangle, l int) {
	st := n.Style
	scale := 1 / float64(int(1)<<l)
	pad := int(math.Ceil(float64(st.reach())*scale)) + 2
	wide := r.Inset(-pad)
	content := readLayer(n, wide, l)
	if n.activeMask() != nil {
		// The mask shapes the layer, and so its effects, as Photoshop's does by default.
		m := n.Mask.ReadLevel(l, wide)
		for i := 0; i < len(content.Pix); i += 4 {
			k := uint32(m.Pix[i/4])
			for c := range 4 {
				content.Pix[i+c] = uint8(mul8(uint32(content.Pix[i+c]), k))
			}
		}
	}
	alpha := newField(wide)
	for i := range alpha.v {
		alpha.v[i] = float64(content.Pix[i*4+3]) / 255
	}
	under := func(c color.NRGBA, blend Blend, opacity float64, a field) {
		blendPixels(dst, solidOf(c, a, r), r, opacity*n.Opacity, blend, nil, 255)
	}
	if sh := st.DropShadow; sh != nil {
		dx, dy := shadowOffset(sh, l)
		size := sh.Size * scale
		under(sh.Color, sh.Blend, sh.Opacity, alpha.shifted(dx, dy).grown(size*sh.Spread).blurred(size*(1-sh.Spread)))
	}
	if g := st.OuterGlow; g != nil {
		size := g.Size * scale
		under(g.Color, g.Blend, g.Opacity, alpha.grown(size*g.Spread).blurred(size*(1-g.Spread)))
	}
	// The layer and what lies on it, kept within its shape.
	top := image.NewRGBA(r)
	copyRect(top, content, r)
	on := func(c color.NRGBA, blend Blend, opacity float64, a field) {
		blendPixels(top, solidOf(c, a.times(alpha), r), r, opacity, blend, nil, 255)
		// The effect stays within the shape: the alpha is the layer's.
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				i, j := top.PixOffset(x, y), content.PixOffset(x, y)
				if a0 := content.Pix[j+3]; top.Pix[i+3] != a0 {
					keepAlpha(top.Pix[i:i+4], uint32(a0))
				}
			}
		}
	}
	if o := st.ColorOverlay; o != nil {
		on(o.Color, o.Blend, o.Opacity, constField(wide, 1))
	}
	if g := st.InnerGlow; g != nil {
		size := g.Size * scale
		on(g.Color, g.Blend, g.Opacity, alpha.inverted().grown(size*g.Spread).blurred(size*(1-g.Spread)))
	}
	if sh := st.InnerShadow; sh != nil {
		dx, dy := shadowOffset(sh, l)
		size := sh.Size * scale
		on(sh.Color, sh.Blend, sh.Opacity, alpha.inverted().shifted(dx, dy).grown(size*sh.Spread).blurred(size*(1-sh.Spread)))
	}
	if o := st.Stroke; o != nil {
		size := o.Size * scale
		var a field
		switch o.Position {
		case StrokeInside:
			a = alpha.inverted().grown(size).times(alpha)
		case StrokeCenter:
			out := alpha.grown(size / 2)
			in := alpha.inverted().grown(size / 2)
			a = newField(wide)
			for i := range a.v {
				a.v[i] = min(out.v[i], in.v[i]) // the band either side of the edge
			}
		default:
			a = alpha.grown(size)
			for i := range a.v {
				a.v[i] *= 1 - alpha.v[i]
			}
		}
		blendPixels(top, solidOf(o.Color, a, r), r, o.Opacity, BlendNormal, nil, 255)
	}
	blendPixels(dst, top, r, n.Opacity, n.Blend, nil, 255)
}

func constField(r image.Rectangle, v float64) field {
	f := newField(r)
	for i := range f.v {
		f.v[i] = v
	}
	return f
}
