package paint

import (
	"image"
	"image/color"
	"testing"
)

// viewDoc is a large picture of many small things, as a busy drawing is: pixel layers with masks, vector layers
// with gradients and strokes, one of them compound with a vector mask on another.
func viewDoc() *Doc {
	d, _ := NewFramed(3840, 2160, "Background", color.NRGBA{R: 240, G: 240, B: 240, A: 255})
	d.View = d.Frame
	for i := range 24 {
		x, y := 150*(i%8)+100, 600*(i/8)+100
		d.AddLayer("Dots")
		d.Select(image.Rect(x, y, x+200, y+200))
		d.Fill(color.NRGBA{R: uint8(i * 10), G: 100, B: 200, A: 255})
		d.Deselect()
		if i%3 == 0 {
			d.AddMask()
			d.DrawGradient(Gradient{From: [2]float64{float64(x), 0}, To: [2]float64{float64(x + 200), 0}, Start: color.NRGBA{R: 255, G: 255, B: 255, A: 255}, End: color.NRGBA{A: 255}, Opacity: 1})
			d.EditMask = false
		}
	}
	for i := range 12 {
		x, y := float64(300*(i%6)+1500), float64(500*(i/6)+200)
		v := Vector{Kind: VectorEllipse, Size: Pt{250, 180}, M: Translate(x, y),
			Fill:   VectorPaint{Kind: PaintRadial, From: Pt{125, 90}, To: Pt{250, 90}, Aspect: 0.7, Stops: []Stop{{0, color.NRGBA{R: 255, A: 255}}, {1, color.NRGBA{B: 255, A: 255}}}},
			Stroke: VectorStroke{Paint: VectorPaint{Kind: PaintSolid, Color: color.NRGBA{A: 255}}, Width: 6}}
		if i%4 == 0 {
			v = v.WithShape(Vector{Kind: VectorRect, Size: Pt{120, 120}, M: Translate(x+60, y+30)}, OpSubtract)
		}
		n := d.AddVector(v, "Shape")
		if i%5 == 0 {
			d.AddShape(n.ID, Vector{Kind: VectorStar, Size: Pt{240, 240}, Sides: 5, Inner: 0.5, M: Translate(x, y)}, OpAdd)
		}
	}
	return d
}

func BenchmarkRenderView(b *testing.B) {
	d := viewDoc()
	r := image.Rect(1000, 500, 2600, 1500)
	dst := image.NewRGBA(r)
	b.ResetTimer()
	for range b.N {
		d.Render(dst, r, 0)
	}
}

func BenchmarkRenderWholePictureZoomedOut(b *testing.B) {
	d := viewDoc()
	r := image.Rect(0, 0, 3840>>2, 2160>>2)
	dst := image.NewRGBA(r)
	b.ResetTimer()
	for range b.N {
		d.Render(dst, r, 2)
	}
}

func BenchmarkMaskGradientOnALargeCanvas(b *testing.B) {
	d, _ := NewFramed(8000, 8000, "Background", color.NRGBA{})
	d.Select(image.Rect(100, 100, 400, 400))
	d.Fill(color.NRGBA{R: 255, A: 255})
	d.Deselect()
	b.ResetTimer()
	for range b.N {
		d.MaskGradient(Gradient{From: [2]float64{100, 0}, To: [2]float64{400, 0}})
	}
}

// BenchmarkPresetStroke paints a stroke with each of a few presets at their own sizes, three times larger, as a
// stroke across a canvas is.
func BenchmarkPresetStroke(b *testing.B) {
	for _, id := range []string{"flat-bristle", "charcoal", "splatter"} {
		p, _ := PresetByID(id)
		b.Run(id, func(b *testing.B) {
			d, _ := NewFramed(3840, 2160, "Background", color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			br := p.Brush
			br.Size *= 3
			br.Color = color.NRGBA{R: 200, G: 60, B: 60, A: 255}
			b.ResetTimer()
			for range b.N {
				s, _, _ := d.BeginStroke(br, false, 100, 100)
				for x := 100.0; x < 1600; x += 6 {
					s.To(x, 100+x/4)
				}
				s.End()
			}
		})
	}
}

// BenchmarkStyledLayerInAView draws a view where a small layer has a shadow, a glow and a stroke.
func BenchmarkStyledLayerInAView(b *testing.B) {
	d, _ := NewFramed(3840, 2160, "Background", color.NRGBA{R: 240, G: 240, B: 240, A: 255})
	n := d.AddVector(Vector{Kind: VectorEllipse, Size: Pt{200, 150}, M: Translate(1700, 900), Fill: VectorPaint{Kind: PaintSolid, Color: color.NRGBA{R: 200, A: 255}}}, "Badge")
	d.SetStyle(n.ID, &LayerStyle{
		DropShadow: &Shadow{Color: color.NRGBA{A: 255}, Opacity: 0.6, Angle: 120, Distance: 10, Size: 20},
		OuterGlow:  &Glow{Color: color.NRGBA{R: 255, G: 255, A: 255}, Opacity: 0.8, Size: 15},
	})
	r := image.Rect(1000, 500, 2600, 1500)
	dst := image.NewRGBA(r)
	b.ResetTimer()
	for range b.N {
		d.compositeInto(dst, d.Root, r, 0)
	}
}
