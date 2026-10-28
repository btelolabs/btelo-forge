// Package gfx is a small software renderer: blended rectangles, anti-aliased rounded shapes, scaled images and text.
package gfx

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/draw"
)

// Rect is a rectangle in pixels with float coordinates.
type Rect struct{ X, Y, W, H float32 }

func (r Rect) Right() float32  { return r.X + r.W }
func (r Rect) Bottom() float32 { return r.Y + r.H }

// Contains reports whether (x, y) is inside r.
func (r Rect) Contains(x, y float32) bool { return x >= r.X && y >= r.Y && x < r.X+r.W && y < r.Y+r.H }

// Intersects reports whether r and o overlap.
func (r Rect) Intersects(o Rect) bool {
	return r.X < o.Right() && o.X < r.Right() && r.Y < o.Bottom() && o.Y < r.Bottom()
}

// Inset shrinks r by d on every side.
func (r Rect) Inset(d float32) Rect { return Rect{r.X + d, r.Y + d, max(r.W-2*d, 0), max(r.H-2*d, 0)} }

// Pixels rounds r to whole pixels.
func (r Rect) Pixels() image.Rectangle {
	return image.Rect(int(math.Round(float64(r.X))), int(math.Round(float64(r.Y))), int(math.Round(float64(r.X+r.W))), int(math.Round(float64(r.Y+r.H))))
}

// Canvas draws into an RGBA image within a clip rectangle.
type Canvas struct {
	Img   *image.RGBA
	clip  image.Rectangle
	clips []image.Rectangle
}

// NewCanvas allocates a w×h canvas.
func NewCanvas(w, h int) *Canvas {
	c := &Canvas{}
	c.Resize(w, h)
	return c
}

// Resize reallocates the canvas when its size changes.
func (c *Canvas) Resize(w, h int) {
	if c.Img != nil && c.Img.Rect.Dx() == w && c.Img.Rect.Dy() == h {
		return
	}
	c.Img = image.NewRGBA(image.Rect(0, 0, w, h))
	c.clip = c.Img.Rect
	c.clips = c.clips[:0]
}

// Wrap makes a canvas that draws into img.
func Wrap(img *image.RGBA) *Canvas { return &Canvas{Img: img, clip: img.Rect} }

// Push narrows the clip to r until the matching Pop.
func (c *Canvas) Push(r Rect) {
	c.clips = append(c.clips, c.clip)
	c.clip = c.clip.Intersect(r.Pixels())
}

// Pop restores the clip from before the last Push.
func (c *Canvas) Pop() {
	c.clip = c.clips[len(c.clips)-1]
	c.clips = c.clips[:len(c.clips)-1]
}

// Clear fills the whole canvas, ignoring the clip.
// ClearTo fills the whole canvas with col exactly, transparency included (col is premultiplied), for windows whose
// pixels may be see-through.
func (c *Canvas) ClearTo(col color.RGBA) {
	p := c.Img.Pix
	if len(p) == 0 {
		return
	}
	p[0], p[1], p[2], p[3] = col.R, col.G, col.B, col.A
	for k := 4; k < len(p); k *= 2 {
		copy(p[k:], p[:k])
	}
}

// Clear fills the whole canvas with col, opaque.
func (c *Canvas) Clear(col color.RGBA) {
	p := c.Img.Pix
	if len(p) == 0 {
		return
	}
	p[0], p[1], p[2], p[3] = col.R, col.G, col.B, 255
	for k := 4; k < len(p); k *= 2 {
		copy(p[k:], p[:k])
	}
}

// blend mixes col into pixel offset i with coverage cov in [0, 1].
func (c *Canvas) blend(i int, col color.RGBA, cov float32) {
	a := float32(col.A) / 255 * cov
	if a <= 0 {
		return
	}
	p := c.Img.Pix[i : i+4 : i+4]
	if a >= 1 {
		p[0], p[1], p[2], p[3] = col.R, col.G, col.B, 255
		return
	}
	inv := 1 - a
	p[0] = uint8(float32(col.R)*a + float32(p[0])*inv + 0.5)
	p[1] = uint8(float32(col.G)*a + float32(p[1])*inv + 0.5)
	p[2] = uint8(float32(col.B)*a + float32(p[2])*inv + 0.5)
	p[3] = uint8(min(255, float32(p[3])+a*float32(255-p[3])+0.5))
}

// Fill blends col over r.
func (c *Canvas) Fill(r Rect, col color.RGBA) {
	b := r.Pixels().Intersect(c.clip)
	if b.Empty() || col.A == 0 {
		return
	}
	stride := c.Img.Stride
	if col.A == 255 {
		row := c.Img.PixOffset(b.Min.X, b.Min.Y)
		for x := 0; x < b.Dx(); x++ {
			c.Img.Pix[row+x*4], c.Img.Pix[row+x*4+1], c.Img.Pix[row+x*4+2], c.Img.Pix[row+x*4+3] = col.R, col.G, col.B, 255
		}
		line := c.Img.Pix[row : row+b.Dx()*4]
		for y := 1; y < b.Dy(); y++ {
			copy(c.Img.Pix[row+y*stride:], line)
		}
		return
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		c.span(c.Img.PixOffset(b.Min.X, y), b.Dx(), col, 1)
	}
}

// RoundRect fills r with corners of the given radius, anti-aliased.
func (c *Canvas) RoundRect(r Rect, radius float32, col color.RGBA) {
	c.roundShape(r, radius, -1, col)
}

// RoundOutline strokes the inside edge of r with a line of width w.
func (c *Canvas) RoundOutline(r Rect, radius, w float32, col color.RGBA) {
	c.roundShape(r, radius, w, col)
}

// roundShape fills (width < 0) or strokes a rounded rectangle from its signed distance. Only the corners and the
// left and right edges are evaluated per pixel: across the middle of each row the distance depends on y alone.
func (c *Canvas) roundShape(r Rect, radius, width float32, col color.RGBA) {
	radius = min(radius, r.W/2, r.H/2)
	b := image.Rect(int(math.Floor(float64(r.X))), int(math.Floor(float64(r.Y))), int(math.Ceil(float64(r.X+r.W))), int(math.Ceil(float64(r.Y+r.H)))).Intersect(c.clip)
	if b.Empty() || col.A == 0 {
		return
	}
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	hx, hy := r.W/2-radius, r.H/2-radius
	coverage := func(d float32) float32 {
		cov := clamp01(0.5 - d)
		if width >= 0 {
			cov = min(cov, clamp01(d+width+0.5))
		}
		return cov
	}
	margin := 1.5 + max(width, 0)
	deep := float32(1)
	if width >= 0 {
		deep = 0
	}
	// Pixels whose centre lies within spanHalf of cx form the row's span.
	spanHalf := hx - radius - margin
	x0, x1 := b.Max.X, b.Max.X
	if spanHalf > 0 {
		x0 = max(b.Min.X, int(math.Ceil(float64(cx-spanHalf-0.5))))
		x1 = min(b.Max.X, int(math.Floor(float64(cx+spanHalf-0.5)))+1)
		x0 = min(x0, x1)
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		qy := float32(math.Abs(float64(float32(y)+0.5-cy))) - hy
		edges := func(from, to int) {
			i := c.Img.PixOffset(from, y)
			for x := from; x < to; x, i = x+1, i+4 {
				qx := float32(math.Abs(float64(float32(x)+0.5-cx))) - hx
				ox, oy := max(qx, 0), max(qy, 0)
				d := float32(math.Sqrt(float64(ox*ox+oy*oy))) + min(max(qx, qy), 0) - radius
				if cov := coverage(d); cov > 0 {
					c.blend(i, col, cov)
				}
			}
		}
		edges(b.Min.X, x0)
		if x0 < x1 {
			cov := deep
			if qy > -(radius + margin) {
				cov = coverage(qy - radius)
			}
			c.span(c.Img.PixOffset(x0, y), x1-x0, col, cov)
		}
		edges(max(x1, x0), b.Max.X)
	}
}

// span blends col with coverage cov over n pixels starting at offset i.
func (c *Canvas) span(i, n int, col color.RGBA, cov float32) {
	a := float32(col.A) / 255 * cov
	if a <= 0 || n <= 0 {
		return
	}
	p := c.Img.Pix[i : i+n*4]
	if a >= 1 {
		p[0], p[1], p[2], p[3] = col.R, col.G, col.B, 255
		for k := 4; k < len(p); k *= 2 {
			copy(p[k:], p[:k])
		}
		return
	}
	for k := 0; k < n; k++ {
		c.blend(i+k*4, col, cov)
	}
}

func clamp01(v float32) float32 { return min(max(v, 0), 1) }

// Circle fills a disc.
func (c *Canvas) Circle(x, y, radius float32, col color.RGBA) {
	c.RoundRect(Rect{x - radius, y - radius, 2 * radius, 2 * radius}, radius, col)
}

// Line draws a straight anti-aliased line of width w.
func (c *Canvas) Line(x0, y0, x1, y1, w float32, col color.RGBA) {
	minX, maxX := min(x0, x1)-w, max(x0, x1)+w
	minY, maxY := min(y0, y1)-w, max(y0, y1)+w
	b := image.Rect(int(minX), int(minY), int(maxX)+1, int(maxY)+1).Intersect(c.clip)
	dx, dy := x1-x0, y1-y0
	l2 := dx*dx + dy*dy
	for y := b.Min.Y; y < b.Max.Y; y++ {
		i := c.Img.PixOffset(b.Min.X, y)
		for x := b.Min.X; x < b.Max.X; x, i = x+1, i+4 {
			px, py := float32(x)+0.5-x0, float32(y)+0.5-y0
			t := float32(0)
			if l2 > 0 {
				t = clamp01((px*dx + py*dy) / l2)
			}
			ex, ey := px-t*dx, py-t*dy
			d := float32(math.Sqrt(float64(ex*ex+ey*ey))) - w/2
			if cov := clamp01(0.5 - d); cov > 0 {
				c.blend(i, col, cov)
			}
		}
	}
}

// Triangle fills the triangle (a, b, c) with 4×4 supersampled edges.
func (c *Canvas) Triangle(ax, ay, bx, by, cx, cy float32, col color.RGBA) {
	box := image.Rect(int(min(ax, bx, cx)), int(min(ay, by, cy)), int(max(ax, bx, cx))+1, int(max(ay, by, cy))+1).Intersect(c.clip)
	edge := func(x0, y0, x1, y1, px, py float32) float32 { return (x1-x0)*(py-y0) - (y1-y0)*(px-x0) }
	area := edge(ax, ay, bx, by, cx, cy)
	if area == 0 {
		return
	}
	for y := box.Min.Y; y < box.Max.Y; y++ {
		i := c.Img.PixOffset(box.Min.X, y)
		for x := box.Min.X; x < box.Max.X; x, i = x+1, i+4 {
			hits := 0
			for sy := 0; sy < 4; sy++ {
				for sx := 0; sx < 4; sx++ {
					px, py := float32(x)+(float32(sx)+0.5)/4, float32(y)+(float32(sy)+0.5)/4
					w0, w1, w2 := edge(bx, by, cx, cy, px, py), edge(cx, cy, ax, ay, px, py), edge(ax, ay, bx, by, px, py)
					if (area > 0 && w0 >= 0 && w1 >= 0 && w2 >= 0) || (area < 0 && w0 <= 0 && w1 <= 0 && w2 <= 0) {
						hits++
					}
				}
			}
			if hits > 0 {
				c.blend(i, col, float32(hits)/16)
			}
		}
	}
}

// Image draws src scaled into dst with bilinear filtering.
func (c *Canvas) Image(src image.Image, dst Rect) {
	r := dst.Pixels()
	clipped := r.Intersect(c.clip)
	if clipped.Empty() {
		return
	}
	if r == clipped && r.Size() == src.Bounds().Size() {
		draw.Draw(c.Img, r, src, src.Bounds().Min, draw.Over)
		return
	}
	sub := c.Img.SubImage(clipped).(*image.RGBA)
	draw.ApproxBiLinear.Scale(sub, r, src, src.Bounds(), draw.Over, nil)
}

// Blit copies src with its top-left at (x, y) without scaling; opaque sources replace what is underneath.
func (c *Canvas) Blit(src *image.RGBA, x, y int, opaque bool) {
	r := image.Rect(x, y, x+src.Rect.Dx(), y+src.Rect.Dy())
	clipped := r.Intersect(c.clip)
	if clipped.Empty() {
		return
	}
	op := draw.Over
	if opaque {
		op = draw.Src
	}
	draw.Draw(c.Img, clipped, src, src.Rect.Min.Add(clipped.Min.Sub(r.Min)), op)
}

// Scaler keeps one resized copy of an image so it is not resampled again on every frame.
type Scaler struct {
	src  *image.RGBA
	w, h int
	out  *image.RGBA
}

// Scaled returns src resized to w×h, reusing the last result while src and the size stay the same.
func (s *Scaler) Scaled(src *image.RGBA, w, h int) *image.RGBA {
	if w <= 0 || h <= 0 {
		return nil
	}
	if s.src == src && s.w == w && s.h == h && s.out != nil {
		return s.out
	}
	if src.Rect.Dx() == w && src.Rect.Dy() == h {
		s.out = src
	} else {
		if s.out == nil || s.out == s.src || s.out.Rect.Dx() != w || s.out.Rect.Dy() != h {
			s.out = image.NewRGBA(image.Rect(0, 0, w, h))
		}
		draw.ApproxBiLinear.Scale(s.out, s.out.Rect, src, src.Rect, draw.Src, nil)
	}
	s.src, s.w, s.h = src, w, h
	return s.out
}

// Mix interpolates two colours.
func Mix(a, b color.RGBA, t float32) color.RGBA {
	l := func(x, y uint8) uint8 { return uint8(float32(x)*(1-t) + float32(y)*t + 0.5) }
	return color.RGBA{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B), l(a.A, b.A)}
}

// Alpha returns col with its opacity scaled by a.
func Alpha(col color.RGBA, a float32) color.RGBA {
	col.A = uint8(float32(col.A)*clamp01(a) + 0.5)
	return col
}

// Hex builds an opaque colour from 0xRRGGBB.
func Hex(v uint32) color.RGBA { return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255} }

// RGBA makes an opaque colour from red, green and blue.
func RGBA(c [3]uint8) color.RGBA { return color.RGBA{c[0], c[1], c[2], 255} }

// RoundCorners clips img to a rounded rectangle of the given radius in place, with anti-aliased edges.
func RoundCorners(img *image.RGBA, radius float32) {
	iw, ih := img.Rect.Dx(), img.Rect.Dy()
	w, h := float32(iw), float32(ih)
	radius = min(radius, w/2, h/2)
	n := min(int(math.Ceil(float64(radius))), iw)
	into := func(v, size float32) float32 { return max(radius-v, v-(size-radius), 0) }
	for y := 0; y < ih; y++ {
		dy := into(float32(y)+0.5, h)
		if dy <= 0 {
			continue
		}
		for x := 0; x < iw; x++ {
			if x == n && x < iw-n {
				x = iw - n
			}
			dx := into(float32(x)+0.5, w)
			if dx <= 0 {
				continue
			}
			cov := clamp01(radius + 0.5 - float32(math.Hypot(float64(dx), float64(dy))))
			p := img.Pix[img.PixOffset(img.Rect.Min.X+x, img.Rect.Min.Y+y):]
			for k := 0; k < 4; k++ {
				p[k] = uint8(float32(p[k])*cov + 0.5)
			}
		}
	}
}

// Fade returns a copy of img with its opacity scaled by a (0 to 1); img itself is returned when a is 1.
func Fade(img *image.RGBA, a float64) *image.RGBA {
	if a >= 1 {
		return img
	}
	out := image.NewRGBA(img.Rect)
	k := float32(max(a, 0))
	for i, v := range img.Pix {
		out.Pix[i] = uint8(float32(v)*k + 0.5)
	}
	return out
}
