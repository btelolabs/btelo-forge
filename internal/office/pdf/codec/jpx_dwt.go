package codec

import (
	"errors"
	"math"
)

// Lifting constants of the 9/7 filter, T.800 table F.4.
const (
	dwtAlpha = -1.586134342059924
	dwtBeta  = -0.052980118572961
	dwtGamma = 0.882911075530934
	dwtDelta = 0.443506852043971
	dwtK     = 1.230174104914001
)

// lift53 is the reversible 1D synthesis of a signal whose first sample has parity cas.
func lift53(b []int32, cas int) {
	n := len(b)
	if n == 1 {
		if cas == 1 {
			b[0] /= 2
		}
		return
	}
	for i := cas; i < n; i += 2 {
		l, r := neighbours(i, n)
		b[i] -= (b[l] + b[r] + 2) >> 2
	}
	for i := 1 - cas; i < n; i += 2 {
		l, r := neighbours(i, n)
		b[i] += (b[l] + b[r]) >> 1
	}
}

// neighbours gives the two samples beside i, mirrored at the ends.
func neighbours(i, n int) (int, int) {
	l, r := i-1, i+1
	if l < 0 {
		l = r
	}
	if r >= n {
		r = l
	}
	return l, r
}

// lift97 is the irreversible 1D synthesis.
func lift97(b []float32, cas int) {
	n := len(b)
	if n == 1 {
		if cas == 1 {
			b[0] /= 2
		}
		return
	}
	for i := cas; i < n; i += 2 {
		b[i] *= dwtK
	}
	for i := 1 - cas; i < n; i += 2 {
		b[i] *= 1 / dwtK
	}
	step := func(start int, f float32) {
		for i := start; i < n; i += 2 {
			l, r := neighbours(i, n)
			b[i] -= f * (b[l] + b[r])
		}
	}
	step(cas, dwtDelta)
	step(1-cas, dwtGamma)
	step(cas, dwtBeta)
	step(1-cas, dwtAlpha)
}

// synth runs the 2D synthesis of one component, resolution by resolution.
// data is the Mallat layout, stride wide; lift is the 1D filter.
func synth[T int32 | float32](data []T, stride int, res []jpxRes, lift func([]T, int)) {
	for r := 1; r < len(res); r++ {
		cur, low := &res[r], &res[r-1]
		w, h := cur.x1-cur.x0, cur.y1-cur.y0
		lw, lh := low.x1-low.x0, low.y1-low.y0
		if w <= 0 || h <= 0 {
			continue
		}
		casX, casY := cur.x0&1, cur.y0&1
		parallelFor(h, 32, func(lo, hi int) {
			buf := make([]T, w)
			for y := lo; y < hi; y++ {
				row := data[y*stride:][:w]
				for i := range lw {
					buf[casX+2*i] = row[i]
				}
				for i := lw; i < w; i++ {
					buf[1-casX+2*(i-lw)] = row[i]
				}
				lift(buf, casX)
				copy(row, buf)
			}
		})
		parallelFor(w, 16, func(lo, hi int) {
			buf := make([]T, h)
			for x := lo; x < hi; x++ {
				for i := range lh {
					buf[casY+2*i] = data[i*stride+x]
				}
				for i := lh; i < h; i++ {
					buf[1-casY+2*(i-lh)] = data[i*stride+x]
				}
				lift(buf, casY)
				for i, v := range buf {
					data[i*stride+x] = v
				}
			}
		})
	}
}

func (t *tile) inverseDWT() {
	for c := range t.comps {
		tc := &t.comps[c]
		stride := tc.x1 - tc.x0
		if tc.idata != nil {
			synth(tc.idata, stride, tc.res, lift53)
		} else {
			synth(tc.fdata, stride, tc.res, lift97)
		}
	}
}

// inverseMCT undoes the multiple component transform, annex G.
func (t *tile) inverseMCT() error {
	if t.p.mct == 0 || len(t.comps) < 3 {
		return nil
	}
	a, b, c := &t.comps[0], &t.comps[1], &t.comps[2]
	same := func(p, q *jpxTileComp) bool {
		return p.x0 == q.x0 && p.y0 == q.y0 && p.x1 == q.x1 && p.y1 == q.y1 &&
			(p.idata != nil) == (q.idata != nil)
	}
	if !same(a, b) || !same(a, c) {
		return errors.New("jpx: component transform over unlike components")
	}
	if a.idata != nil {
		x, y, z := a.idata, b.idata, c.idata
		for i := range x {
			y0, y1, y2 := x[i], y[i], z[i]
			g := y0 - (y2+y1)>>2
			x[i], y[i], z[i] = y2+g, g, y1+g
		}
		return nil
	}
	x, y, z := a.fdata, b.fdata, c.fdata
	for i := range x {
		y0, y1, y2 := x[i], y[i], z[i]
		x[i] = y0 + 1.402*y2
		y[i] = y0 - 0.34413*y1 - 0.71414*y2
		z[i] = y0 + 1.772*y1
	}
	return nil
}

// store writes a tile's samples into the image planes, with the DC level undone.
func (t *tile) store() {
	for c := range t.comps {
		tc := &t.comps[c]
		comp := &t.d.comps[c]
		w, h := tc.x1-tc.x0, tc.y1-tc.y0
		depth := comp.depth
		low, high := int64(0), int64(1)<<depth-1
		// Planes are unsigned: signed samples are moved up by half the range too.
		shift := int64(1) << (depth - 1)
		if comp.signed {
			low, high = -shift, shift-1
		}
		down := max(depth-16, 0)
		for y := range h {
			dst := comp.plane[(tc.y0-comp.y0+y)*comp.w+tc.x0-comp.x0:][:w]
			for x := range w {
				var v int64
				if tc.idata != nil {
					v = int64(tc.idata[y*w+x])
				} else {
					f := tc.fdata[y*w+x]
					if f != f {
						f = 0
					}
					v = int64(math.Round(float64(max(min(f, 1<<40), -1<<40))))
				}
				if !comp.signed {
					v += shift
				}
				v = min(max(v, low), high)
				if comp.signed {
					v += shift
				}
				dst[x] = uint16(v >> down)
			}
		}
		tc.idata, tc.fdata = nil, nil
	}
}
