package paint

import (
	"errors"
	"image"
	"math"
)

// A brush tip can be made from a picture, as Photoshop's Define Brush Preset makes one: what each of its pixels lays
// down is the picture's opacity there, or, for a picture with no transparency, its darkness, as Photoshop reads one;
// either way the selection's edge fades it. The brush is drawn from it at its size, the longer side being the size,
// turned and squashed as any tip, its hardness having no say.

// TipShape is the picture of a tip: how much each pixel lays down, 0 to 255, row by row.
type TipShape struct {
	W, H int
	Pix  []uint8
}

// maxTipSide is the largest side a tip is kept at; a larger picture is scaled down to it.
const maxTipSide = 512

// ErrEmptyTip means a tip was asked of a picture with nothing in it.
var ErrEmptyTip = errors.New("there is nothing there to make a brush tip of")

func (t *TipShape) valid() bool {
	return t != nil && t.W > 0 && t.H > 0 && t.W <= maxTipSide && t.H <= maxTipSide && len(t.Pix) == t.W*t.H
}

// at is how much of the tip covers the point (dx, dy) from its centre, already turned and squashed, for a brush of
// size: sampled between pixels, so the tip is smooth at every size.
func (t *TipShape) at(dx, dy, size float64) float64 {
	return t.atScaled(dx, dy, float64(max(t.W, t.H))/size)
}

// atScaled is at with the tip's pixels to a canvas pixel worked out, as a dab works it out once.
func (t *TipShape) atScaled(dx, dy, scale float64) float64 {
	x, y := dx*scale+float64(t.W)/2-0.5, dy*scale+float64(t.H)/2-0.5
	if x <= -1 || y <= -1 || x >= float64(t.W) || y >= float64(t.H) {
		return 0 // beyond the picture and its edge's blend
	}
	x0, y0 := math.Floor(x), math.Floor(y)
	fx, fy := x-x0, y-y0
	i, j := int(x0), int(y0)
	if i >= 0 && j >= 0 && i+1 < t.W && j+1 < t.H {
		// Well inside: the four pixels, without looking for the edge.
		k := j*t.W + i
		p := t.Pix
		top := float64(p[k])*(1-fx) + float64(p[k+1])*fx
		bot := float64(p[k+t.W])*(1-fx) + float64(p[k+t.W+1])*fx
		return (top*(1-fy) + bot*fy) / 255
	}
	px := func(i, j int) float64 {
		if i < 0 || j < 0 || i >= t.W || j >= t.H {
			return 0
		}
		return float64(t.Pix[j*t.W+i])
	}
	v := (px(i, j)*(1-fx)+px(i+1, j)*fx)*(1-fy) + (px(i, j+1)*(1-fx)+px(i+1, j+1)*fx)*fy
	return v / 255
}

// TipFrom makes a tip of the picture img (premultiplied) where sel covers it (all of it when sel is nil): its
// opacity, or its darkness when it has no transparency, cut to what it covers and scaled down to at most
// maxTipSide.
func TipFrom(img *image.RGBA, sel *image.Alpha) (*TipShape, error) {
	r := img.Rect
	opaque := true
	for y := r.Min.Y; y < r.Max.Y && opaque; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if coverage(sel, x, y) > 0 && img.Pix[img.PixOffset(x, y)+3] < 255 {
				opaque = false
				break
			}
		}
	}
	w, h := r.Dx(), r.Dy()
	full := make([]uint8, w*h)
	used := image.Rectangle{}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			i := img.PixOffset(x, y)
			v := uint32(img.Pix[i+3])
			if opaque {
				// Darkness: the luma of the colour, unpremultiplied, which an opaque pixel already is.
				v = 255 - (299*uint32(img.Pix[i])+587*uint32(img.Pix[i+1])+114*uint32(img.Pix[i+2])+500)/1000
			}
			v = mul8(v, coverage(sel, x, y))
			full[(y-r.Min.Y)*w+x-r.Min.X] = uint8(v)
			if v > 0 {
				used = used.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	if used.Empty() {
		return nil, ErrEmptyTip
	}
	// Cut to what it covers, then scaled down by whole boxes of pixels so no detail is skipped.
	cw, ch := used.Dx(), used.Dy()
	step := (max(cw, ch) + maxTipSide - 1) / maxTipSide
	t := &TipShape{W: (cw + step - 1) / step, H: (ch + step - 1) / step}
	t.Pix = make([]uint8, t.W*t.H)
	for ty := range t.H {
		for tx := range t.W {
			sum, n := 0, 0
			for y := ty * step; y < min((ty+1)*step, ch); y++ {
				for x := tx * step; x < min((tx+1)*step, cw); x++ {
					sum += int(full[(used.Min.Y-r.Min.Y+y)*w+used.Min.X-r.Min.X+x])
					n++
				}
			}
			t.Pix[ty*t.W+tx] = uint8((sum + n/2) / n)
		}
	}
	return t, nil
}

// TipFromLayer makes a tip of the chosen layer as it looks on its own, within the selection when there is one,
// cut to what it covers.
func (d *Doc) TipFromLayer() (*TipShape, error) {
	l, err := d.ActiveLayer()
	if err != nil {
		return nil, err
	}
	if l.Adjust != nil {
		return nil, ErrAdjustmentLayer
	}
	r := boundsOfNode(l)
	if d.Selection != nil {
		r = r.Intersect(d.SelectionBounds())
	}
	if r.Empty() {
		return nil, ErrEmptyTip
	}
	if r.Dx() > maxSide || r.Dy() > maxSide {
		return nil, ErrTooBig
	}
	return TipFrom(readLayer(l, r, 0), d.selectionIn(r))
}

// TipShapeOf is a tip read back from a greyscale picture of it, as a saved tip is: its grey is what it lays down.
func TipShapeOf(img *image.Gray) (*TipShape, error) {
	t := &TipShape{W: img.Rect.Dx(), H: img.Rect.Dy()}
	if !t.validSize() {
		return nil, ErrEmptyTip
	}
	t.Pix = make([]uint8, t.W*t.H)
	for y := range t.H {
		copy(t.Pix[y*t.W:(y+1)*t.W], img.Pix[img.PixOffset(img.Rect.Min.X, img.Rect.Min.Y+y):][:t.W])
	}
	return t, nil
}

func (t *TipShape) validSize() bool {
	return t.W > 0 && t.H > 0 && t.W <= maxTipSide && t.H <= maxTipSide
}

// Gray is the tip as a greyscale picture, to save.
func (t *TipShape) Gray() *image.Gray {
	img := image.NewGray(image.Rect(0, 0, t.W, t.H))
	copy(img.Pix, t.Pix)
	return img
}
