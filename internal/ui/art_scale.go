package ui

import (
	"image"
	"math"
	"runtime"
	"sync"
)

// scaleOver lays src (premultiplied) over dst within area, scaled by scale with src's point (0, 0) at (ox, oy) of
// dst: the view of the canvas at its zoom. Zoomed in it is nearest, so pixels stay sharp; otherwise each pixel is
// mixed from the four nearest, in fixed point. Where each column and row of dst reads src is worked out once, and
// the rows are shared among the processor's cores.
func scaleOver(dst *image.RGBA, area image.Rectangle, src *image.RGBA, scale, ox, oy float64, nearest bool) {
	area = area.Intersect(dst.Rect)
	if area.Empty() || src.Rect.Empty() || scale <= 0 {
		return
	}
	cols := axisTaps(area.Min.X, area.Max.X, src.Rect.Min.X, src.Rect.Max.X, scale, ox, nearest)
	rows := axisTaps(area.Min.Y, area.Max.Y, src.Rect.Min.Y, src.Rect.Max.Y, scale, oy, nearest)
	// Convert column taps to byte offsets in src rows once.
	for i := range cols {
		cols[i].a = (cols[i].a - src.Rect.Min.X) * 4
		cols[i].b = (cols[i].b - src.Rect.Min.X) * 4
	}
	run := func(y0, y1 int) {
		if nearest {
			scaleRowsNearest(dst, area, src, cols, rows, y0, y1)
		} else {
			scaleRows(dst, area, src, cols, rows, y0, y1)
		}
	}
	n := min(runtime.GOMAXPROCS(0), area.Dy()/64)
	if n <= 1 {
		run(0, area.Dy())
		return
	}
	var wg sync.WaitGroup
	for i := range n {
		y0, y1 := area.Dy()*i/n, area.Dy()*(i+1)/n
		wg.Go(func() { run(y0, y1) })
	}
	wg.Wait()
}

// tap is where a column or row of the result reads its source: two neighbours, a and b (absolute positions), and
// how much of b to take, 0 to 256.
type tap struct {
	a, b int
	w    uint32
}

// axisTaps are the taps of the result's positions from lo to hi, reading the source between slo and shi.
func axisTaps(lo, hi, slo, shi int, scale, origin float64, nearest bool) []tap {
	out := make([]tap, hi-lo)
	for i := range out {
		s := (float64(lo+i)+0.5-origin)/scale - 0.5 // the source position whose centre this one's falls on
		if nearest {
			p := min(max(int(math.Floor(s+0.5)), slo), shi-1)
			out[i] = tap{a: p, b: p}
			continue
		}
		f := math.Floor(s)
		a := int(f)
		w := uint32((s - f) * 256)
		b := a + 1
		a, b = min(max(a, slo), shi-1), min(max(b, slo), shi-1)
		out[i] = tap{a: a, b: b, w: w}
	}
	return out
}

// scaleRowsNearest does scaleOver's rows y0 to y1 for nearest-neighbor sampling (cols[i].a are byte offsets).
func scaleRowsNearest(dst *image.RGBA, area image.Rectangle, src *image.RGBA, cols, rows []tap, y0, y1 int) {
	rowBytes := src.Rect.Dx() * 4
	dstBytes := len(cols) * 4
	for j := y0; j < y1; j++ {
		top := src.Pix[src.PixOffset(src.Rect.Min.X, rows[j].a):][:rowBytes]
		drow := dst.Pix[dst.PixOffset(area.Min.X, area.Min.Y+j):][:dstBytes]
		for i, cx := range cols {
			a := cx.a
			sp := top[a : a+4 : a+4]
			sa := uint32(sp[3])
			if sa == 0 {
				continue
			}
			d := drow[i*4 : i*4+4 : i*4+4]
			if sa == 255 {
				d[0], d[1], d[2], d[3] = sp[0], sp[1], sp[2], 255
				continue
			}
			inv := 255 - sa
			v0 := uint32(d[0])*inv + 127
			v1 := uint32(d[1])*inv + 127
			v2 := uint32(d[2])*inv + 127
			v3 := uint32(d[3])*inv + 127
			d[0] = uint8(min(uint32(sp[0])+(v0+v0>>8)>>8, 255))
			d[1] = uint8(min(uint32(sp[1])+(v1+v1>>8)>>8, 255))
			d[2] = uint8(min(uint32(sp[2])+(v2+v2>>8)>>8, 255))
			d[3] = uint8(min(sa+(v3+v3>>8)>>8, 255))
		}
	}
}

// scaleRows does scaleOver's rows y0 to y1 of area (counted from its top) with bilinear sampling.
func scaleRows(dst *image.RGBA, area image.Rectangle, src *image.RGBA, cols, rows []tap, y0, y1 int) {
	rowBytes := src.Rect.Dx() * 4
	dstBytes := len(cols) * 4
	for j := y0; j < y1; j++ {
		ry := rows[j]
		wy := ry.w
		iwy := 256 - wy
		top := src.Pix[src.PixOffset(src.Rect.Min.X, ry.a):][:rowBytes]
		bot := src.Pix[src.PixOffset(src.Rect.Min.X, ry.b):][:rowBytes]
		drow := dst.Pix[dst.PixOffset(area.Min.X, area.Min.Y+j):][:dstBytes]
		for i, cx := range cols {
			a, b := cx.a, cx.b
			var p0, p1, p2, p3 uint32
			if cx.w == 0 && wy == 0 {
				sp := top[a : a+4 : a+4]
				p0, p1, p2, p3 = uint32(sp[0]), uint32(sp[1]), uint32(sp[2]), uint32(sp[3])
			} else {
				wx := cx.w
				iwx := 256 - wx
				ta, tb := top[a:a+4:a+4], top[b:b+4:b+4]
				ba, bb := bot[a:a+4:a+4], bot[b:b+4:b+4]
				p0 = ((uint32(ta[0])*iwx+uint32(tb[0])*wx)*iwy + (uint32(ba[0])*iwx+uint32(bb[0])*wx)*wy + 1<<15) >> 16
				p1 = ((uint32(ta[1])*iwx+uint32(tb[1])*wx)*iwy + (uint32(ba[1])*iwx+uint32(bb[1])*wx)*wy + 1<<15) >> 16
				p2 = ((uint32(ta[2])*iwx+uint32(tb[2])*wx)*iwy + (uint32(ba[2])*iwx+uint32(bb[2])*wx)*wy + 1<<15) >> 16
				p3 = ((uint32(ta[3])*iwx+uint32(tb[3])*wx)*iwy + (uint32(ba[3])*iwx+uint32(bb[3])*wx)*wy + 1<<15) >> 16
			}
			if p3 == 0 {
				continue
			}
			d := drow[i*4 : i*4+4 : i*4+4]
			if p3 == 255 {
				d[0], d[1], d[2], d[3] = uint8(p0), uint8(p1), uint8(p2), 255
				continue
			}
			inv := 255 - p3
			v0 := uint32(d[0])*inv + 127
			v1 := uint32(d[1])*inv + 127
			v2 := uint32(d[2])*inv + 127
			v3 := uint32(d[3])*inv + 127
			d[0] = uint8(min(p0+(v0+v0>>8)>>8, 255))
			d[1] = uint8(min(p1+(v1+v1>>8)>>8, 255))
			d[2] = uint8(min(p2+(v2+v2>>8)>>8, 255))
			d[3] = uint8(min(p3+(v3+v3>>8)>>8, 255))
		}
	}
}
