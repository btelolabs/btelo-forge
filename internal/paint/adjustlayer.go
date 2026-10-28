package paint

import (
	"errors"
	"image"
	"math"
	"slices"
)

// Adjustment layers, as Photoshop's: a layer that holds an adjustment rather than pixels and changes everything
// under it in its group (in a pass-through group, everything under it in the picture), through its own opacity,
// blend mode and mask. The pixels under it are never changed, so the adjustment can be changed or taken away at any
// time. An adjustment layer comes with a white mask, chosen for painting, so where it applies is painted in.

// AdjustKind is what an adjustment layer does.
type AdjustKind int

const (
	AdjustBrightness AdjustKind = iota // Values: brightness, contrast
	AdjustLevels                       // Values: input black, midtones (gamma), input white, output black, output white
	AdjustCurves                       // Curve
	AdjustHue                          // Values: hue, saturation, lightness; On: colorize
	AdjustPosterize                    // Values: levels
	AdjustThreshold                    // Values: level (1 to 255)
	AdjustInvert
	AdjustBlackWhite
	adjustKindCount
)

// adjustValues is how many values each kind takes.
var adjustValues = [adjustKindCount]int{2, 5, 0, 3, 1, 1, 0, 0}

// Adjustment is an adjustment layer's adjustment and its settings. It is a value never changed in place.
type Adjustment struct {
	Kind   AdjustKind   `json:"kind"`
	Values []float64    `json:"values,omitempty"`
	On     bool         `json:"on,omitempty"`
	Curve  []CurvePoint `json:"curve,omitempty"`
}

// ErrAdjustmentLayer means a pixel operation was asked of an adjustment layer, which has none; its mask can be
// painted.
var ErrAdjustmentLayer = errors.New("an adjustment layer has no pixels; paint or fill its mask, or change its settings")

// valid reports whether the settings make sense for the kind.
func (a *Adjustment) valid() bool {
	if a.Kind < 0 || a.Kind >= adjustKindCount || len(a.Values) != adjustValues[a.Kind] || len(a.Curve) > 64 {
		return false
	}
	for _, v := range a.Values {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1e6 {
			return false
		}
	}
	if a.Kind == AdjustCurves && len(a.Curve) < 2 {
		return false
	}
	for _, p := range a.Curve {
		if p.In < 0 || p.In > 255 || p.Out < 0 || p.Out > 255 {
			return false
		}
	}
	return true
}

// Filter is the change the adjustment makes, pixel by pixel.
func (a Adjustment) Filter() Filter {
	v := func(i int) float64 {
		if i < len(a.Values) {
			return a.Values[i]
		}
		return 0
	}
	switch a.Kind {
	case AdjustBrightness:
		return BrightnessContrast(v(0), v(1))
	case AdjustLevels:
		return Levels(v(0), v(2), v(1), v(3), v(4))
	case AdjustCurves:
		return Curves(a.Curve)
	case AdjustHue:
		return HueSaturation(v(0), v(1), v(2), a.On)
	case AdjustPosterize:
		return Posterize(int(v(0)))
	case AdjustThreshold:
		return Threshold(v(0))
	case AdjustInvert:
		return Invert()
	case AdjustBlackWhite:
		return Desaturate()
	}
	return Filter{apply: func(src *image.RGBA, r image.Rectangle) *image.RGBA {
		out := image.NewRGBA(r)
		copyRect(out, src, r)
		return out
	}}
}

func copyRect(dst, src *image.RGBA, r image.Rectangle) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		copy(dst.Pix[dst.PixOffset(r.Min.X, y):dst.PixOffset(r.Max.X, y)], src.Pix[src.PixOffset(r.Min.X, y):])
	}
}

// AddAdjustment adds an adjustment layer called name above the chosen node, with a white mask chosen for painting,
// as Photoshop's Layer › New Adjustment Layer does, and chooses it.
func (d *Doc) AddAdjustment(adj Adjustment, name string) (*Node, error) {
	s, err := d.BeginNewAdjustment(adj, name)
	if err != nil {
		return nil, err
	}
	s.End()
	return s.n, nil
}

// BeginNewAdjustment adds an adjustment layer whose settings can then be tried, as its dialog does: End keeps it as
// one step, Cancel takes it away again.
func (d *Doc) BeginNewAdjustment(adj Adjustment, name string) (*AdjustSession, error) {
	if !adj.valid() {
		return nil, errors.New("the adjustment's settings do not fit its kind")
	}
	s := &AdjustSession{d: d, mark: d.Mark(), name: "New adjustment layer"}
	a := adj
	s.n = &Node{ID: d.newID(), Name: name, Visible: true, Opacity: 1, Adjust: &a, Mask: NewMask(255)}
	d.insertAbove(s.n)
	d.EditMask = true
	return s, nil
}

// AdjustSession is an adjustment layer's settings being changed, shown as they change and recorded as one step.
type AdjustSession struct {
	d    *Doc
	n    *Node
	mark Mark
	name string
}

// Layer is the adjustment layer.
func (s *AdjustSession) Layer() *Node { return s.n }

// BeginAdjust starts changing the adjustment layer id.
func (d *Doc) BeginAdjust(id int) (*AdjustSession, error) {
	n := d.Node(id)
	if n == nil {
		return nil, ErrNoLayer
	}
	if n.Adjust == nil {
		return nil, errors.New("the layer is not an adjustment layer")
	}
	return &AdjustSession{d: d, n: n, mark: d.Mark(), name: "Adjustment"}, nil
}

// Adjustment is the settings as they are now.
func (s *AdjustSession) Adjustment() Adjustment { return *s.n.Adjust }

// Set shows the settings adj; they must be of the layer's kind.
func (s *AdjustSession) Set(adj Adjustment) error {
	if adj.Kind != s.n.Adjust.Kind || !adj.valid() {
		return errors.New("the adjustment's settings do not fit its kind")
	}
	a := adj
	a.Values, a.Curve = slices.Clone(adj.Values), slices.Clone(adj.Curve)
	s.n.Adjust = &a
	return nil
}

// End records the change as one step.
func (s *AdjustSession) End() { s.d.Commit(s.name, s.mark) }

// Cancel puts the settings back.
func (s *AdjustSession) Cancel() { s.d.restore(s.mark.s) }

// SetAdjustment changes an adjustment layer's settings as one step.
func (d *Doc) SetAdjustment(id int, adj Adjustment) error {
	s, err := d.BeginAdjust(id)
	if err != nil {
		return err
	}
	if err := s.Set(adj); err != nil {
		return err
	}
	s.End()
	return nil
}

// adjustRaster is a raster with an adjustment applied, tile by tile.
func adjustRaster(src *Raster, f Filter) *Raster {
	keys := map[image.Point]bool{}
	keysOf(&src.p, keys)
	out := NewRaster()
	for k := range keys {
		r := tileRect(k)
		out.Write(f.apply(src.Read(r), r))
	}
	return out
}
