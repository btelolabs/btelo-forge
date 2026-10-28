package paint

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/math/f64"
)

// Whole-layer work goes tile by tile, so its cost follows what is painted and never the span between pixels far
// apart.

// keysOf are the canvas tiles that a plane's own tiles touch, in canvas tile keys (a plane moved by part of a tile
// touches up to four for each of its own).
func keysOf(p *plane, into map[image.Point]bool) {
	for k := range p.tiles {
		k0, k1 := keysOver(tileRect(k).Add(p.off))
		for ky := k0.Y; ky <= k1.Y; ky++ {
			for kx := k0.X; kx <= k1.X; kx++ {
				into[image.Pt(kx, ky)] = true
			}
		}
	}
}

// keysUnder are the canvas tiles that the layers of n (and their masks, where those hide or show, and their effects)
// touch.
func keysUnder(n *Node, into map[image.Point]bool) {
	if n.Pixels != nil {
		keysOf(&n.Pixels.p, into)
	}
	if n.Vector != nil {
		if b := n.Vector.PixelBounds(); !b.Empty() {
			k0, k1 := keysOver(b)
			for ky := k0.Y; ky <= k1.Y; ky++ {
				for kx := k0.X; kx <= k1.X; kx++ {
					into[image.Pt(kx, ky)] = true
				}
			}
		}
	}
	if !n.Style.empty() {
		// The effects reach beyond what the layer holds.
		if b := reachOfNode(n); !b.Empty() {
			k0, k1 := keysOver(b)
			for ky := k0.Y; ky <= k1.Y; ky++ {
				for kx := k0.X; kx <= k1.X; kx++ {
					into[image.Pt(kx, ky)] = true
				}
			}
		}
	}
	for _, c := range n.Children {
		keysUnder(c, into)
	}
}

// compositeOf makes a raster of how the layers of group g look together, tile by tile.
func (d *Doc) compositeOf(g *Node) *Raster {
	keys := map[image.Point]bool{}
	keysUnder(g, keys)
	out := NewRaster()
	for k := range keys {
		r := tileRect(k)
		img := image.NewRGBA(r)
		d.compositeInto(img, g, r, 0)
		out.Write(img)
	}
	return out
}

// blendOnto paints src over dst tile by tile with an opacity, a mode and a mask, as merging a layer down does.
func blendOnto(dst, src *Raster, opacity float64, mode Blend, mask *Mask) *Raster {
	keys := map[image.Point]bool{}
	keysOf(&dst.p, keys)
	keysOf(&src.p, keys)
	out := NewRaster()
	for k := range keys {
		r := tileRect(k)
		img := dst.Read(r)
		var m *image.Alpha
		var fill uint8 = 255
		if mask != nil {
			m, fill = mask.Read(r), mask.Fill()
		}
		blendPixels(img, src.Read(r), r, opacity, mode, m, fill)
		out.Write(img)
	}
	return out
}

// resampleRaster maps a raster through m, one tile of the result at a time; each reads the source around where it
// comes from, so tiles meet without seams.
func resampleRaster(src *Raster, m Affine, k xdraw.Interpolator) *Raster {
	out := NewRaster()
	inv, ok := m.Inverse()
	if !ok {
		return out
	}
	for key := range destKeys(&src.p, m) {
		dst := tileRect(key)
		from := inv.bounds(dst).Inset(-3) // the kernel's reach
		img := image.NewRGBA(dst)
		Resample(k, img, m.aff3(), src.Read(from), from, xdraw.Src)
		out.Write(img)
	}
	return out
}

// resampleMask maps a mask through m like resampleRaster; beyond what it maps the mask keeps its fill.
func resampleMask(src *Mask, m Affine, k xdraw.Interpolator) *Mask {
	out := NewMask(src.Fill())
	inv, ok := m.Inverse()
	if !ok {
		return out
	}
	for key := range destKeys(&src.p, m) {
		dst := tileRect(key)
		from := inv.bounds(dst).Inset(-3)
		img := image.NewAlpha(dst)
		draw.Draw(img, dst, image.NewUniform(color.Alpha{A: src.Fill()}), image.Point{}, draw.Src)
		Resample(k, img, m.aff3(), src.Read(from), from, xdraw.Src)
		out.Write(img)
	}
	return out
}

// destKeys are the tiles a plane's tiles land on under m.
func destKeys(p *plane, m Affine) map[image.Point]bool {
	keys := map[image.Point]bool{}
	for k := range p.tiles {
		k0, k1 := keysOver(m.bounds(tileRect(k).Add(p.off)).Inset(-1))
		for ky := k0.Y; ky <= k1.Y; ky++ {
			for kx := k0.X; kx <= k1.X; kx++ {
				keys[image.Pt(kx, ky)] = true
			}
		}
	}
	return keys
}

// inverse is the map that undoes m, when there is one.
// Inverse is the map that undoes m, and whether there is one.
func (m Affine) Inverse() (Affine, bool) {
	det := m.A*m.E - m.B*m.D
	if det == 0 {
		return Affine{}, false
	}
	a, b, d, e := m.E/det, -m.B/det, -m.D/det, m.A/det
	return Affine{A: a, B: b, C: -(a*m.C + b*m.F), D: d, E: e, F: -(d*m.C + e*m.F)}, true
}

// Resample draws src's area sr onto dst through s2d with the interpolator k, as k.Transform does. A map that only
// moves by whole pixels is drawn as a copy here: x/image/draw takes the same shortcut but places the copy with the
// source's left edge as its top (sr.Min.X for sr.Min.Y), which misplaces any source not at a corner where x is y.
func Resample(k xdraw.Interpolator, dst xdraw.Image, s2d f64.Aff3, src image.Image, sr image.Rectangle, op xdraw.Op) {
	if s2d[0] == 1 && s2d[1] == 0 && s2d[3] == 0 && s2d[4] == 1 && s2d[2] == math.Trunc(s2d[2]) && s2d[5] == math.Trunc(s2d[5]) {
		xdraw.Copy(dst, sr.Min.Add(image.Pt(int(s2d[2]), int(s2d[5]))), src, sr, op, nil)
		return
	}
	k.Transform(dst, s2d, src, sr, op, nil)
}
