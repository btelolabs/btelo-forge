package ui

import (
	"image"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/chart"
	"github.com/btelolabs/btelo-forge/internal/office/xlsx"
)

// What floats over the cells. It is drawn by the same painter on the screen and on paper; the screen keeps a picture of
// each thing, so a frame costs a copy, and a recording canvas gets a chart's shapes.

// sheetFloat is what the screen keeps of a chart or picture.
type sheetFloat struct {
	chart  chart.Cache
	scaler gfx.Scaler
	img    *image.RGBA
	read   bool
}

// pointPixels is a sheet's pixels at 100% to a chart's point.
const pointPixels = 96.0 / 72

func (p *sheetPainter) floatBox(d *xlsx.Drawing) gfx.Rect {
	v, k := p.v, p.k
	x := p.x0 + (v.colX[d.From.Col]+d.From.DX)*k
	y := p.y0 + (v.rowTop(d.From.Row)+d.From.DY)*k
	if d.Sized {
		return gfx.Rect{X: float32(x), Y: float32(y), W: float32(d.W * k), H: float32(d.H * k)}
	}
	x1 := p.x0 + (v.colX[d.To.Col]+d.To.DX)*k
	y1 := p.y0 + (v.rowTop(d.To.Row)+d.To.DY)*k
	return gfx.Rect{X: float32(x), Y: float32(y), W: float32(x1 - x), H: float32(y1 - y)}
}

// floats draws the sheet's charts and pictures that reach r.
func (p *sheetPainter) floats(r gfx.Rect) {
	ed := p.ed
	for i := range p.v.drawings {
		d := &p.v.drawings[i]
		box := p.floatBox(d)
		if box.W < 2 || box.H < 2 || !box.Intersects(r) {
			continue
		}
		key := d.Chart + d.Picture
		f := ed.floats[key]
		if f == nil {
			if ed.floats == nil {
				ed.floats = map[string]*sheetFloat{}
			}
			f = &sheetFloat{}
			ed.floats[key] = f
		}
		if d.Chart != "" {
			if ch := ed.wb.Chart(d.Chart); ch != nil {
				f.chart.Draw(p.c, box, float32(p.k*pointPixels), ch, chart.FacesOf(ed.a.officeBook()))
			} else {
				ed.floatMissing(p.c, box)
			}
			continue
		}
		if !f.read {
			f.img, f.read = ed.wb.Picture(d), true
		}
		if f.img == nil {
			ed.floatMissing(p.c, box)
			continue
		}
		if p.c.Recorder() != nil {
			p.c.Image(f.img, box)
		} else if s := f.scaler.Scaled(f.img, int(box.W+0.5), int(box.H+0.5)); s != nil {
			p.c.Blit(s, int(box.X+0.5), int(box.Y+0.5), false)
		}
	}
}

// floatMissing marks the place of what cannot be shown.
func (ed *sheetEd) floatMissing(c *gfx.Canvas, box gfx.Rect) {
	c.Fill(box, gfx.Hex(0xf0f1f4))
	c.Outline(box, 1, gfx.Hex(0xb6bac4))
	f := ed.a.u.fonts.Regular.Face(max(min(box.H/4, 13), 6))
	t := f.Ellipsize(ed.a.u.t("Not shown here"), box.W-6)
	c.Text(f, box.X+(box.W-f.Measure(t))/2, box.Y+(box.H-f.Height())/2, t, gfx.Hex(0x6a6f7c))
}
