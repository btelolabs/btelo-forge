package paint

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"sync"
)

// Brush presets are ready-made brushes, as Photoshop's: a tip made from a scanned mark of a real tool, with the
// spacing, flow and shape dynamics that make it paint like that tool. Each tip is a greyscale picture in tips/, its
// grey what it lays down, cut from drawn sheets of marks by packaging/cut_brush_tips.py.

//go:embed tips/*.png
var presetTips embed.FS

// Preset is a ready-made brush.
type Preset struct {
	ID    string // its tip's file in tips/, and its name to the MCP tools
	Name  string // in English; the interface translates it
	Brush Brush  // its colour is left to the painter
}

// presetSettings are the presets, in the order they are offered: drawing tools, then paint brushes, then textures.
// Tips that follow the stroke are drawn across it at 90°, so a flat brush paints its full width.
var presetSettings = []Preset{
	{"pencil", "Pencil", Brush{Size: 8, Opacity: 0.9, Flow: 0.3, Spacing: 0.2, Smooth: 0.15, Round: 1, AngleJitter: 1, SizeJitter: 0.1}},
	{"charcoal", "Charcoal", Brush{Size: 40, Opacity: 1, Flow: 0.45, Spacing: 0.12, Smooth: 0.1, Round: 1, AngleJitter: 1, SizeJitter: 0.2}},
	{"pastel", "Soft pastel", Brush{Size: 36, Opacity: 1, Flow: 0.35, Spacing: 0.15, Smooth: 0.1, Round: 1, AngleJitter: 1, SizeJitter: 0.15}},
	{"ink-pen", "Ink pen", Brush{Size: 5, Opacity: 1, Flow: 1, Spacing: 0.05, Smooth: 0.3, Round: 1}},
	{"ink-brush", "Ink brush", Brush{Size: 30, Opacity: 1, Flow: 1, Spacing: 0.04, Smooth: 0.25, Round: 1, Angle: 90, Follow: true}},
	{"flat-bristle", "Flat bristle brush", Brush{Size: 50, Opacity: 1, Flow: 0.6, Spacing: 0.03, Smooth: 0.15, Round: 1, Angle: 90, Follow: true}},
	{"round-bristle", "Round bristle brush", Brush{Size: 40, Opacity: 1, Flow: 0.3, Spacing: 0.08, Smooth: 0.15, Round: 1, AngleJitter: 0.15}},
	{"fan", "Dry fan brush", Brush{Size: 70, Opacity: 1, Flow: 1, Spacing: 0.12, Smooth: 0.15, Round: 1, Angle: 90, Follow: true}},
	{"sponge", "Sponge", Brush{Size: 60, Opacity: 1, Flow: 0.7, Spacing: 0.35, Smooth: 0.1, Round: 1, AngleJitter: 1, SizeJitter: 0.3, Scatter: 0.3}},
	{"splatter", "Spatter", Brush{Size: 80, Opacity: 1, Flow: 1, Spacing: 0.25, Smooth: 0.1, Round: 1, AngleJitter: 1, SizeJitter: 0.4, Scatter: 0.5}},
	{"grunge", "Grunge", Brush{Size: 90, Opacity: 1, Flow: 0.6, Spacing: 0.4, Smooth: 0.1, Round: 1, AngleJitter: 1, SizeJitter: 0.3, Scatter: 0.2}},
	{"ellipse", "Oval pen", Brush{Size: 24, Opacity: 1, Flow: 1, Spacing: 0.04, Smooth: 0.3, Round: 1, Angle: -30}},
}

// Presets are the ready-made brushes, their tips read once; they are shared, so a brush taken from one is a copy
// whose tip must not be changed in place, as no tip is.
var Presets = sync.OnceValue(func() []Preset {
	out := make([]Preset, len(presetSettings))
	for i, p := range presetSettings {
		tip, err := presetTip(p.ID)
		if err != nil {
			panic(err) // the tips are built in, and tested
		}
		p.Brush.Tip, p.Brush.Shape = TipShaped, tip
		p.Brush.Hardness = 1
		out[i] = p
	}
	return out
})

func presetTip(id string) (*TipShape, error) {
	data, err := presetTips.ReadFile("tips/" + id + ".png")
	if err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("brush tip %s: %w", id, err)
	}
	g, ok := img.(*image.Gray)
	if !ok {
		return nil, fmt.Errorf("brush tip %s is not greyscale", id)
	}
	return TipShapeOf(g)
}

// PresetByID is the preset called id.
func PresetByID(id string) (Preset, bool) {
	for _, p := range Presets() {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// PresetOf is the preset whose tip is shape, the one a brush taken from it has.
func PresetOf(shape *TipShape) (Preset, bool) {
	if shape == nil {
		return Preset{}, false
	}
	for _, p := range Presets() {
		if p.Brush.Shape == shape {
			return p, true
		}
	}
	return Preset{}, false
}

// Sample is how much of each pixel of a w×h picture a stroke of the brush covers along a gentle S across it, as a
// brush picker shows a brush; its colour does not matter.
func Sample(b Brush, w, h int) *image.Alpha {
	out := image.NewAlpha(image.Rect(0, 0, w, h))
	if w <= 0 || h <= 0 {
		return out
	}
	b.Color, b.Opacity, b.Smooth = color.NRGBA{A: 255}, 1, 0
	b = b.normalized()
	d := New("sample")
	d.Paper = false
	margin := min(b.Size*(1+b.Scatter)/2+1, float64(w)/4)
	amp := max(float64(h)/2-b.Size*(1+b.Scatter)/2-1, 0)
	at := func(t float64) (float64, float64) {
		return margin + t*(float64(w)-2*margin), float64(h)/2 - amp*math.Sin(t*2*math.Pi)
	}
	x, y := at(0)
	st, _, err := d.BeginStroke(b, false, x, y)
	if err != nil {
		return out // an empty layer of a new canvas is always paintable
	}
	steps := max(w, 2)
	for i := 1; i <= steps; i++ {
		st.To(at(float64(i) / float64(steps)))
	}
	st.End()
	img := d.Composite(out.Rect)
	for i := range out.Pix {
		out.Pix[i] = img.Pix[i*4+3]
	}
	return out
}
