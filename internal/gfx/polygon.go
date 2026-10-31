package gfx

import (
	"image/color"
	"math"
	"slices"
)

// polygonLines is how many scan lines a pixel's height is sampled with; across, coverage is exact.
const polygonLines = 4

// polygonEdge is one side of a contour going down the canvas.
type polygonEdge struct {
	x0, y0, x1, y1 float32
	wind           int
}

type polygonHit struct {
	x    float32
	wind int
}

// Polygon fills closed contours by the non-zero rule, anti-aliased.
func (c *Canvas) Polygon(contours [][][2]float32, col color.RGBA) {
	if c.rec != nil {
		c.rec.Polygon(contours, col)
		return
	}
	var edges []polygonEdge
	top, bottom := float32(math.Inf(1)), float32(math.Inf(-1))
	for _, ct := range contours {
		if len(ct) < 3 {
			continue
		}
		for i, p := range ct {
			q := ct[(i+1)%len(ct)]
			top, bottom = min(top, p[1]), max(bottom, p[1])
			switch {
			case p[1] < q[1]:
				edges = append(edges, polygonEdge{p[0], p[1], q[0], q[1], 1})
			case p[1] > q[1]:
				edges = append(edges, polygonEdge{q[0], q[1], p[0], p[1], -1})
			}
		}
	}
	y0, y1 := max(int(math.Floor(float64(top))), c.clip.Min.Y), min(int(math.Ceil(float64(bottom))), c.clip.Max.Y)
	x0, x1 := c.clip.Min.X, c.clip.Max.X
	if len(edges) == 0 || y0 >= y1 || x0 >= x1 {
		return
	}
	cover := make([]float32, x1-x0)
	var hits []polygonHit
	for y := y0; y < y1; y++ {
		clear(cover)
		used := false
		for s := range polygonLines {
			sy := float32(y) + (float32(s)+0.5)/polygonLines
			hits = hits[:0]
			for i := range edges {
				e := &edges[i]
				if sy >= e.y0 && sy < e.y1 {
					hits = append(hits, polygonHit{e.x0 + (sy-e.y0)*(e.x1-e.x0)/(e.y1-e.y0), e.wind})
				}
			}
			slices.SortFunc(hits, func(a, b polygonHit) int {
				switch {
				case a.x < b.x:
					return -1
				case a.x > b.x:
					return 1
				}
				return 0
			})
			wind := 0
			for i := 0; i+1 < len(hits); i++ {
				wind += hits[i].wind
				if wind != 0 {
					addSpan(cover, hits[i].x-float32(x0), hits[i+1].x-float32(x0))
					used = true
				}
			}
		}
		if !used {
			continue
		}
		i := c.Img.PixOffset(x0, y)
		for _, v := range cover {
			if v > 0 {
				c.blend(i, col, min(v/polygonLines, 1))
			}
			i += 4
		}
	}
}

// addSpan adds the span from a to b to the coverage of a pixel row, partial at its ends.
func addSpan(cover []float32, a, b float32) {
	a, b = max(a, 0), min(b, float32(len(cover)))
	if a >= b {
		return
	}
	first, last := int(a), int(b)
	if first == last {
		cover[first] += b - a
		return
	}
	cover[first] += float32(first+1) - a
	for i := first + 1; i < last; i++ {
		cover[i]++
	}
	if last < len(cover) {
		cover[last] += b - float32(last)
	}
}

// TextTurned draws s turned clockwise by angle radians about its top-left corner at (x, y), and gives how long it is.
func (c *Canvas) TextTurned(fc *Face, x, y float32, s string, col color.RGBA, angle float64) float32 {
	if c.rec != nil {
		return c.rec.TextTurned(fc, x, y, s, col, angle)
	}
	size := float64(fc.Size)
	sin, cos := math.Sincos(angle)
	at := func(ex, ey float64) [2]float32 {
		// In ems from the pen, y going up, to the canvas.
		px, py := ex*size, float64(fc.Ascent)-ey*size
		return [2]float32{x + float32(px*cos-py*sin), y + float32(px*sin+py*cos)}
	}
	pen := 0.0
	for _, r := range s {
		o := fc.Outline(r)
		if o == nil {
			pen += float64(fc.advance(r)) / size
			continue
		}
		var contours [][][2]float32
		var ct [][2]float32
		k := 0
		for _, op := range o.Ops {
			switch op {
			case 'M':
				if len(ct) > 0 {
					contours = append(contours, ct)
				}
				ct = [][2]float32{at(pen+o.Pts[k][0], o.Pts[k][1])}
				k++
			case 'L':
				ct = append(ct, at(pen+o.Pts[k][0], o.Pts[k][1]))
				k++
			case 'C':
				var p0 [2]float64
				if k > 0 {
					p0 = o.Pts[k-1]
				}
				p1, p2, p3 := o.Pts[k], o.Pts[k+1], o.Pts[k+2]
				const steps = 6
				for i := 1; i <= steps; i++ {
					t := float64(i) / steps
					u := 1 - t
					a, b, cc, d := u*u*u, 3*u*u*t, 3*u*t*t, t*t*t
					ct = append(ct, at(pen+a*p0[0]+b*p1[0]+cc*p2[0]+d*p3[0], a*p0[1]+b*p1[1]+cc*p2[1]+d*p3[1]))
				}
				k += 3
			}
		}
		if len(ct) > 0 {
			contours = append(contours, ct)
		}
		c.Polygon(contours, col)
		pen += o.Advance
	}
	return float32(pen * size)
}
