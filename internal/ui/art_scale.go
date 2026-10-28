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
	n := min(runtime.GOMAXPROCS(0), area.Dy()/32)
	if n <= 1 {
		scaleRows(dst, area, src, cols, rows, 0, area.Dy())
		return
	}
	var wg sync.WaitGroup
	for i := range n {
		y0, y1 := area.Dy()*i/n, area.Dy()*(i+1)/n
		wg.Go(func() { scaleRows(dst, area, src, cols, rows, y0, y1) })
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

// scaleRows does scaleOver's rows y0 to y1 of area (counted from its top).
func scaleRows(dst *image.RGBA, area image.Rectangle, src *image.RGBA, cols, rows []tap, y0, y1 int) {
	var px [4]uint32
	for j := y0; j < y1; j++ {
		ry := rows[j]
		top := src.Pix[src.PixOffset(src.Rect.Min.X, ry.a):]
		bot := src.Pix[src.PixOffset(src.Rect.Min.X, ry.b):]
		di := dst.PixOffset(area.Min.X, area.Min.Y+j)
		for i, cx := range cols {
			a, b := (cx.a-src.Rect.Min.X)*4, (cx.b-src.Rect.Min.X)*4
			if cx.w == 0 && ry.w == 0 {
				px = [4]uint32{uint32(top[a]), uint32(top[a+1]), uint32(top[a+2]), uint32(top[a+3])}
			} else {
				for c := range 4 {
					t := uint32(top[a+c])*(256-cx.w) + uint32(top[b+c])*cx.w
					u := uint32(bot[a+c])*(256-cx.w) + uint32(bot[b+c])*cx.w
					px[c] = (t*(256-ry.w) + u*ry.w + 1<<15) >> 16
				}
			}
			d := dst.Pix[di+i*4 : di+i*4+4 : di+i*4+4]
			switch sa := px[3]; sa {
			case 0:
			case 255:
				d[0], d[1], d[2], d[3] = uint8(px[0]), uint8(px[1]), uint8(px[2]), 255
			default:
				inv := 255 - sa
				for c := range 4 {
					v := uint32(d[c])*inv + 127
					d[c] = uint8(min(px[c]+(v+v>>8)>>8, 255))
				}
			}
		}
	}
}
