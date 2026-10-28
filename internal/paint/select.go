package paint

import (
	"image"
	"math"
	"slices"
)

// SelectMode is how a new shape combines with the selection, as the selection tools' options in Photoshop.
type SelectMode int

const (
	SelectNew SelectMode = iota
	SelectAdd
	SelectSubtract
	SelectIntersect
)

// The selection is an 8-bit mask on the unbounded canvas: 255 is selected, 0 is not, values between are partly
// selected (an anti-aliased or feathered edge). A selection is never changed once made, so the history and Reselect
// can keep it: changes make a new one.

// RectMask is a rectangle as a selection shape.
func (d *Doc) RectMask(r image.Rectangle) *Mask {
	img := image.NewAlpha(r.Canon())
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	return MaskFrom(img, 0)
}

// EllipseMask is the ellipse inscribed in r, its edge anti-aliased.
func (d *Doc) EllipseMask(r image.Rectangle) *Mask {
	r = r.Canon()
	cx, cy := float64(r.Min.X+r.Max.X)/2, float64(r.Min.Y+r.Max.Y)/2
	rx, ry := float64(r.Dx())/2, float64(r.Dy())/2
	const n = 24 // points a quarter, plenty for a smooth outline at any size
	pts := make([][2]float64, 0, 4*n)
	steps := max(4*n, int(math.Max(rx, ry)))
	for i := range steps {
		a := 2 * math.Pi * float64(i) / float64(steps)
		pts = append(pts, [2]float64{cx + rx*math.Cos(a), cy + ry*math.Sin(a)})
	}
	return d.PolygonMask(pts)
}

// PolygonMask is a closed outline as a selection shape, as the lasso draws it, with its edge anti-aliased.
func (d *Doc) PolygonMask(pts [][2]float64) *Mask {
	if len(pts) < 3 {
		return NewMask(0)
	}
	return MaskFrom(polygonCoverage(pts), 0)
}

// polygonCoverage covers a closed outline exactly: each pixel row is sampled on four lines and each line's spans
// are covered across the pixels they cross.
func polygonCoverage(pts [][2]float64) *image.Alpha {
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range pts {
		minX, minY, maxX, maxY = min(minX, p[0]), min(minY, p[1]), max(maxX, p[0]), max(maxY, p[1])
	}
	b := image.Rect(int(math.Floor(minX)), int(math.Floor(minY)), int(math.Ceil(maxX))+1, int(math.Ceil(maxY)))
	img := image.NewAlpha(b)
	const sub = 4
	w := b.Dx()
	cover := make([]float64, w+1)
	var xs []float64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		clear(cover)
		touched := false
		for s := range sub {
			sy := float64(y) + (float64(s)+0.5)/sub
			xs = xs[:0]
			for i := range pts {
				a, c := pts[i], pts[(i+1)%len(pts)]
				if (a[1] <= sy) != (c[1] <= sy) {
					xs = append(xs, a[0]+(sy-a[1])*(c[0]-a[0])/(c[1]-a[1]))
				}
			}
			slices.Sort(xs)
			for i := 0; i+1 < len(xs); i += 2 {
				l, r := max(xs[i]-float64(b.Min.X), 0), min(xs[i+1]-float64(b.Min.X), float64(w))
				if r <= l {
					continue
				}
				touched = true
				li, ri := int(l), int(r)
				if li == ri {
					cover[li] += (r - l) / sub
					continue
				}
				cover[li] += (float64(li+1) - l) / sub
				for x := li + 1; x < ri; x++ {
					cover[x] += 1.0 / sub
				}
				if ri < w {
					cover[ri] += (r - float64(ri)) / sub
				}
			}
		}
		if !touched {
			continue
		}
		row := img.Pix[img.PixOffset(b.Min.X, y):][:w]
		for x := range row {
			row[x] = uint8(min(cover[x], 1)*255 + 0.5)
		}
	}
	return img
}

// limit bounds what would otherwise reach without end, such as a flood fill on an empty canvas: the frame, or else
// what is on screen.
func (d *Doc) limit() image.Rectangle {
	switch {
	case !d.Frame.Empty():
		return d.Frame
	case !d.View.Empty():
		return d.View
	}
	return image.Rect(0, 0, TileSize, TileSize)
}

// WandMask is the magic wand's selection: pixels whose colour is within tolerance (0 to 255) of the colour at
// (x, y), only those connected to it when contiguous, looking at the chosen layer or at the picture as it looks.
// On the unbounded canvas it looks as far as the frame, or else what is on screen, and a step past.
func (d *Doc) WandMask(x, y int, tolerance int, contiguous, merged bool) (*Mask, error) {
	area := d.limit().Inset(-1)
	if !(image.Point{x, y}).In(area) {
		return NewMask(0), nil
	}
	var src *image.RGBA
	if merged {
		src = d.Composite(area)
	} else {
		l, err := d.ActiveLayer()
		if err != nil {
			return nil, err
		}
		src = readLayer(l, area, 0)
	}
	out := image.NewAlpha(area)
	seed := src.Pix[src.PixOffset(x, y) : src.PixOffset(x, y)+4]
	r0, g0, b0, a0 := int(seed[0]), int(seed[1]), int(seed[2]), int(seed[3])
	near := func(px, py int) bool {
		p := src.Pix[src.PixOffset(px, py):][:4]
		return max(abs(int(p[0])-r0), abs(int(p[1])-g0), abs(int(p[2])-b0), abs(int(p[3])-a0)) <= tolerance
	}
	set := func(px, py int) bool { return out.Pix[out.PixOffset(px, py)] != 0 }
	if !contiguous {
		for py := area.Min.Y; py < area.Max.Y; py++ {
			for px := area.Min.X; px < area.Max.X; px++ {
				if near(px, py) {
					out.Pix[out.PixOffset(px, py)] = 255
				}
			}
		}
		return MaskFrom(out, 0), nil
	}
	// A scanline flood fill: each run of matching pixels in a row is filled at once, and the rows above and below
	// are searched for runs to continue into.
	stack := []image.Point{{x, y}}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if set(p.X, p.Y) || !near(p.X, p.Y) {
			continue
		}
		l, r := p.X, p.X
		for l > area.Min.X && !set(l-1, p.Y) && near(l-1, p.Y) {
			l--
		}
		for r < area.Max.X-1 && !set(r+1, p.Y) && near(r+1, p.Y) {
			r++
		}
		for i := l; i <= r; i++ {
			out.Pix[out.PixOffset(i, p.Y)] = 255
		}
		for _, ny := range []int{p.Y - 1, p.Y + 1} {
			if ny < area.Min.Y || ny >= area.Max.Y {
				continue
			}
			for i := l; i <= r; i++ {
				if !set(i, ny) && near(i, ny) {
					stack = append(stack, image.Pt(i, ny))
					for i < r && near(i+1, ny) {
						i++ // one seed per run is enough
					}
				}
			}
		}
	}
	return MaskFrom(out, 0), nil
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// combine makes a new mask from two, value by value, everywhere: their tiles and their fills alike.
func combine(a, b *Mask, op func(x, y uint32) uint32) *Mask {
	out := NewMask(uint8(op(uint32(a.Fill()), uint32(b.Fill()))))
	area := a.TileBounds().Union(b.TileBounds())
	if area.Empty() {
		return out
	}
	ia, ib := a.Read(area), b.Read(area)
	for i := range ia.Pix {
		ia.Pix[i] = uint8(op(uint32(ia.Pix[i]), uint32(ib.Pix[i])))
	}
	out.Write(ia)
	return out
}

// SelectShape combines a shape with the selection as one step of the history.
func (d *Doc) SelectShape(shape *Mask, mode SelectMode) {
	m := d.Mark()
	cur := d.Selection
	var out *Mask
	switch {
	case mode == SelectNew || cur == nil && mode == SelectAdd:
		out = shape
	case cur == nil: // intersecting with or subtracting from nothing
		out = nil
	case mode == SelectAdd:
		out = combine(cur, shape, func(a, b uint32) uint32 { return a + b - mul8(a, b) })
	case mode == SelectSubtract:
		out = combine(cur, shape, func(a, b uint32) uint32 { return mul8(a, 255-b) })
	default:
		out = combine(cur, shape, func(a, b uint32) uint32 { return mul8(a, b) })
	}
	d.setSelection(out)
	d.Commit("Select", m)
}

// setSelection keeps a selection that selects something, remembering the last one for Reselect.
func (d *Doc) setSelection(s *Mask) {
	if s != nil && s.Fill() == 0 && s.Bounds().Empty() {
		s = nil
	}
	if d.Selection != nil {
		d.lastSelection = d.Selection
	}
	d.Selection = s
}

// Select replaces the selection with a rectangle; an empty one selects nothing.
func (d *Doc) Select(r image.Rectangle) {
	if r.Canon().Empty() {
		d.Deselect()
		return
	}
	d.SelectShape(d.RectMask(r), SelectNew)
}

// SelectAll selects the frame, or everything on a canvas without one.
func (d *Doc) SelectAll() {
	if !d.Frame.Empty() {
		d.Select(d.Frame)
		return
	}
	d.SelectShape(NewMask(255), SelectNew)
}

// Deselect drops the selection.
func (d *Doc) Deselect() {
	if d.Selection == nil {
		return
	}
	m := d.Mark()
	d.setSelection(nil)
	d.Commit("Deselect", m)
}

// Reselect brings back the selection before the last Deselect or new selection.
func (d *Doc) Reselect() {
	if d.lastSelection == nil {
		return
	}
	m := d.Mark()
	d.Selection, d.lastSelection = d.lastSelection, d.Selection
	d.Commit("Reselect", m)
}

// InvertSelection selects what was not selected.
func (d *Doc) InvertSelection() {
	m := d.Mark()
	out := NewMask(255)
	if d.Selection != nil {
		out = d.Selection.Snapshot()
		out.Invert()
	}
	d.setSelection(out)
	d.Commit("Inverse", m)
}

// FeatherSelection softens the selection's edge over about radius pixels, as Photoshop's Feather.
func (d *Doc) FeatherSelection(radius float64) {
	if d.Selection == nil || radius <= 0 {
		return
	}
	m := d.Mark()
	d.setSelection(featherMask(d.Selection, radius))
	d.Commit("Feather", m)
}

// Feathered is a selection shape with its edge softened over radius pixels, as the selection tools' Feather
// option does before the shape is combined; with no radius it is the shape itself.
func (d *Doc) Feathered(shape *Mask, radius float64) *Mask {
	if radius <= 0 {
		return shape
	}
	return featherMask(shape, radius)
}

// featherMask is a blurred copy of a mask; beyond its tiles, where it is all its fill, a blur changes nothing.
func featherMask(m *Mask, radius float64) *Mask {
	reach := int(math.Ceil(radius*3)) + 2
	area := m.TileBounds()
	out := NewMask(m.Fill())
	if area.Empty() {
		return out
	}
	out.Write(blurMask(m.Read(area.Inset(-reach)), radius))
	return out
}

// blurMask returns a blurred copy: three box blurs in each direction, close to a Gaussian.
func blurMask(src *image.Alpha, radius float64) *image.Alpha {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	buf := make([]float32, w*h)
	for y := range h {
		for x := range w {
			buf[y*w+x] = float32(src.Pix[y*src.Stride+x])
		}
	}
	tmp := make([]float32, max(w, h))
	r := max(int(math.Round(radius/math.Sqrt(3))), 1)
	for range 3 {
		for y := range h {
			boxLine(buf[y*w:(y+1)*w], 1, w, r, tmp)
		}
		for x := range w {
			boxLine(buf[x:], w, h, r, tmp)
		}
	}
	out := image.NewAlpha(src.Rect)
	for i, v := range buf {
		out.Pix[i] = uint8(min(max(v+0.5, 0), 255))
	}
	return out
}

// boxLine averages n values spaced stride apart over a window of 2r+1, edges extended.
func boxLine(v []float32, stride, n, r int, tmp []float32) {
	at := func(i int) float32 { return v[min(max(i, 0), n-1)*stride] }
	var sum float32
	for i := -r; i <= r; i++ {
		sum += at(i)
	}
	k := float32(2*r + 1)
	for i := range n {
		tmp[i] = sum / k
		sum += at(i+r+1) - at(i-r)
	}
	for i := range n {
		v[i*stride] = tmp[i]
	}
}

// maskBounds is the smallest rectangle holding an alpha image's non-zero pixels.
func maskBounds(m *image.Alpha) image.Rectangle {
	if m == nil {
		return image.Rectangle{}
	}
	w := m.Rect.Dx()
	b := image.Rectangle{Min: image.Pt(w, m.Rect.Dy())}
	for y := range m.Rect.Dy() {
		row := m.Pix[y*m.Stride : y*m.Stride+w]
		first := slices.IndexFunc(row, func(v uint8) bool { return v != 0 })
		if first < 0 {
			continue
		}
		last := len(row) - 1
		for row[last] == 0 {
			last--
		}
		b.Min.X, b.Max.X = min(b.Min.X, first), max(b.Max.X, last+1)
		b.Min.Y, b.Max.Y = min(b.Min.Y, y), max(b.Max.Y, y+1)
	}
	if b.Max.X <= b.Min.X {
		return image.Rectangle{}
	}
	return b.Add(m.Rect.Min)
}

// SelectionBounds is the area the selection covers, empty when nothing is selected. A selection that reaches
// without end (Select All on a canvas without a frame, or an inverted one) is bounded as operations bound it.
func (d *Doc) SelectionBounds() image.Rectangle {
	switch {
	case d.Selection == nil:
		return image.Rectangle{}
	case d.Selection.Fill() != 0:
		return d.limit()
	}
	return d.Selection.Bounds()
}

// selectionIn is how selected each pixel of r is, 0 to 255; nil when nothing is selected, which means all is.
func (d *Doc) selectionIn(r image.Rectangle) *image.Alpha {
	if d.Selection == nil {
		return nil
	}
	return d.Selection.Read(r)
}

// coverage is the value of an alpha image at (x, y), 255 for a nil image.
func coverage(a *image.Alpha, x, y int) uint32 {
	if a == nil {
		return 255
	}
	return uint32(a.Pix[a.PixOffset(x, y)])
}
