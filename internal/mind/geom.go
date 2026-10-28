package mind

import "math"

// Rect is a rectangle on the board, in board units: at 100% zoom one unit is one logical pixel.
type Rect struct{ X0, Y0, X1, Y1 float64 }

// R is the rectangle at x, y of size w × h.
func R(x, y, w, h float64) Rect { return Rect{x, y, x + w, y + h} }

// emptyRect is a rectangle that any union replaces.
var emptyRect = Rect{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}

func (r Rect) W() float64     { return r.X1 - r.X0 }
func (r Rect) H() float64     { return r.Y1 - r.Y0 }
func (r Rect) Empty() bool    { return !(r.X1 > r.X0 && r.Y1 > r.Y0) }
func (r Rect) CX() float64    { return (r.X0 + r.X1) / 2 }
func (r Rect) CY() float64    { return (r.Y0 + r.Y1) / 2 }
func (r Rect) Add(v Vec) Rect { return Rect{r.X0 + v.X, r.Y0 + v.Y, r.X1 + v.X, r.Y1 + v.Y} }

// Union is the smallest rectangle holding both; an empty one holds nothing.
func (r Rect) Union(o Rect) Rect {
	if o.Empty() && !(o.X1 == o.X0 || o.Y1 == o.Y0) {
		return r
	}
	return Rect{math.Min(r.X0, o.X0), math.Min(r.Y0, o.Y0), math.Max(r.X1, o.X1), math.Max(r.Y1, o.Y1)}
}

// Overlaps reports whether the two share any area or touch.
func (r Rect) Overlaps(o Rect) bool {
	return r.X0 <= o.X1 && o.X0 <= r.X1 && r.Y0 <= o.Y1 && o.Y0 <= r.Y1
}

// Contains reports whether the point is inside.
func (r Rect) Contains(x, y float64) bool { return x >= r.X0 && x <= r.X1 && y >= r.Y0 && y <= r.Y1 }

// Inside reports whether o lies wholly within r.
func (r Rect) Inside(o Rect) bool {
	return o.X0 >= r.X0 && o.X1 <= r.X1 && o.Y0 >= r.Y0 && o.Y1 <= r.Y1
}

// Grow is r widened by d on every side.
func (r Rect) Grow(d float64) Rect { return Rect{r.X0 - d, r.Y0 - d, r.X1 + d, r.Y1 + d} }

// Vec is a point or an offset on the board.
type Vec struct{ X, Y float64 }

func (v Vec) Add(o Vec) Vec { return Vec{v.X + o.X, v.Y + o.Y} }
func (v Vec) Sub(o Vec) Vec { return Vec{v.X - o.X, v.Y - o.Y} }

// span is the bounding box of points.
func span(pts ...Vec) Rect {
	r := emptyRect
	for _, p := range pts {
		r = Rect{math.Min(r.X0, p.X), math.Min(r.Y0, p.Y), math.Max(r.X1, p.X), math.Max(r.Y1, p.Y)}
	}
	return r
}
