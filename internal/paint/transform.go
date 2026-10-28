package paint

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"math"
	"slices"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/math/f64"
)

// Affine maps a picture point (x, y) to (A·x + B·y + C, D·x + E·y + F).
type Affine struct{ A, B, C, D, E, F float64 }

// Identity leaves every point where it is.
var Identity = Affine{A: 1, E: 1}

// Then is m followed by n.
func (m Affine) Then(n Affine) Affine {
	return Affine{
		A: n.A*m.A + n.B*m.D, B: n.A*m.B + n.B*m.E, C: n.A*m.C + n.B*m.F + n.C,
		D: n.D*m.A + n.E*m.D, E: n.D*m.B + n.E*m.E, F: n.D*m.C + n.E*m.F + n.F,
	}
}

// Apply maps a point.
func (m Affine) Apply(x, y float64) (float64, float64) {
	return m.A*x + m.B*y + m.C, m.D*x + m.E*y + m.F
}

// Translate, Scale and Rotate build the simple maps; Rotate turns by degrees clockwise about (cx, cy).
func Translate(dx, dy float64) Affine { return Affine{A: 1, C: dx, E: 1, F: dy} }
func Scale(sx, sy, cx, cy float64) Affine {
	return Translate(-cx, -cy).Then(Affine{A: sx, E: sy}).Then(Translate(cx, cy))
}
func Rotate(deg, cx, cy float64) Affine {
	s, c := math.Sincos(deg * math.Pi / 180)
	if q := deg / 90; q == math.Trunc(q) { // quarter turns are exact
		s, c = math.Round(s), math.Round(c)
	}
	return Translate(-cx, -cy).Then(Affine{A: c, B: -s, D: s, E: c}).Then(Translate(cx, cy))
}

// bounds is where a rectangle goes, rounded out to whole pixels.
func (m Affine) bounds(r image.Rectangle) image.Rectangle {
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range [][2]float64{{float64(r.Min.X), float64(r.Min.Y)}, {float64(r.Max.X), float64(r.Min.Y)},
		{float64(r.Min.X), float64(r.Max.Y)}, {float64(r.Max.X), float64(r.Max.Y)}} {
		x, y := m.Apply(p[0], p[1])
		minX, minY, maxX, maxY = min(minX, x), min(minY, y), max(maxX, x), max(maxY, y)
	}
	return image.Rect(int(math.Floor(minX)), int(math.Floor(minY)), int(math.Ceil(maxX)), int(math.Ceil(maxY)))
}

func (m Affine) aff3() f64.Aff3 { return f64.Aff3{m.A, m.B, m.C, m.D, m.E, m.F} }

// ErrEmptyLayer means the chosen layer has nothing on it to transform.
var ErrEmptyLayer = errors.New("the chosen layer has nothing to transform")

// TransformSession is Free Transform: the chosen layer's content (or the selected part of it) shown under a map
// that the handles change, recorded as one step when it is applied.
type TransformSession struct {
	d      *Doc
	l      *Node
	mark   Mark
	source *image.RGBA // the selected pixels being transformed, where they were
	layer  *Raster     // or the whole layer, as it was
	vector *Vector     // or a vector layer's object, as it was
	base   *Raster     // the layer without the selected pixels, when they were lifted out
	mask   *Mask       // the layer's mask, transformed with a whole layer
	sel    *Mask       // the selection being transformed, or nil
	bounds image.Rectangle
	m      Affine
	mesh   *Mesh  // a mesh the content is mapped by instead of m, set by SetMesh
	name   string // the step a mesh is recorded as
	shown  image.Rectangle
}

// BeginTransform starts transforming the chosen layer's content, or the selected part of it.
func (d *Doc) BeginTransform() (*TransformSession, error) {
	l, err := d.ActiveLayer()
	if err != nil {
		return nil, err
	}
	if l.Adjust != nil {
		return nil, ErrAdjustmentLayer
	}
	t := &TransformSession{d: d, l: l, mark: d.Mark(), m: Identity}
	if l.Vector != nil {
		t.vector, t.mask = l.Vector, l.Mask
		t.bounds = l.Vector.PixelBounds()
		if t.bounds.Empty() {
			d.restore(t.mark.s)
			return nil, ErrEmptyLayer
		}
		t.shown = t.bounds
		return t, nil
	}
	d.dropText(l)
	if d.Selection == nil {
		t.bounds = l.Pixels.Bounds()
		t.layer, t.mask = l.Pixels.Snapshot(), l.Mask
	} else {
		mv, err := d.BeginMove() // lifts the selected pixels
		if err != nil {
			return nil, err
		}
		t.base, t.sel = mv.base, mv.sel
		t.bounds = RasterFrom(mv.lifted).Bounds()
		if t.bounds.Empty() {
			t.bounds = mv.sel.Bounds()
		}
		t.source = image.NewRGBA(t.bounds)
		draw.Draw(t.source, t.bounds, mv.lifted, t.bounds.Min, draw.Src)
	}
	if t.bounds.Empty() {
		d.restore(t.mark.s)
		return nil, ErrEmptyLayer
	}
	t.shown = t.bounds
	return t, nil
}

// Bounds is the content's rectangle before transforming, which the handles start on.
func (t *TransformSession) Bounds() image.Rectangle { return t.bounds }

// Map is the transform as it stands.
func (t *TransformSession) Map() Affine { return t.m }

// Set shows the content under m, quickly while a handle is dragged or at full quality; it returns the canvas area
// that changed.
func (t *TransformSession) Set(m Affine, fast bool) image.Rectangle {
	t.mesh = nil
	var k xdraw.Interpolator = xdraw.CatmullRom
	if fast {
		k = xdraw.ApproxBiLinear
	}
	return t.set(m, k)
}

// set shows the content under m, resampled with k.
func (t *TransformSession) set(m Affine, k xdraw.Interpolator) image.Rectangle {
	t.m = m
	out := m.bounds(t.bounds)
	if t.vector != nil {
		t.l.Vector = t.vector
		transformVector(t.l, m)
		if t.mask != nil {
			t.l.Mask = transformMask(t.mask, m, k)
		}
		changed := t.shown.Union(out)
		t.shown = out
		return changed
	}
	if t.base == nil {
		t.l.Pixels = resampleRaster(t.layer, m, k)
		if t.mask != nil {
			t.l.Mask = transformMask(t.mask, m, k)
		}
	} else {
		pix := t.base.Snapshot()
		under := pix.Read(out)
		Resample(k, under, m.aff3(), t.source, t.bounds, xdraw.Over)
		pix.Write(under)
		t.l.Pixels = pix
		t.d.Selection = transformMask(t.sel, m, k)
	}
	changed := t.shown.Union(out)
	t.shown = out
	return changed
}

// transformMask is a mask under m: what differs from its fill is mapped, and the fill stays everywhere else.
func transformMask(mask *Mask, m Affine, k xdraw.Interpolator) *Mask {
	src := mask.Bounds() // exactly what differs, so its edges are resampled as edges
	out := NewMask(mask.Fill())
	if src.Empty() {
		return out
	}
	dst := m.bounds(src)
	img := image.NewAlpha(dst)
	draw.Draw(img, dst, image.NewUniform(color.Alpha{A: mask.Fill()}), image.Point{}, draw.Src)
	Resample(k, img, m.aff3(), mask.Read(src), src, xdraw.Src)
	out.Write(img)
	return out
}

// Apply records the transform at full quality as one step.
func (t *TransformSession) Apply() image.Rectangle {
	if t.mesh != nil {
		t.d.Commit(t.name, t.mark)
		return t.shown
	}
	changed := t.Set(t.m, false)
	if t.m == Identity {
		return t.Cancel().Union(changed)
	}
	t.d.Commit("Free transform", t.mark)
	return changed
}

// Cancel puts everything back as it was.
func (t *TransformSession) Cancel() image.Rectangle {
	t.d.restore(t.mark.s)
	return t.shown.Union(t.bounds)
}

// FlipLayer mirrors the chosen layer (or the selected part of it) across its content's middle, exactly.
func (d *Doc) FlipLayer(horizontal bool) error {
	t, err := d.BeginTransform()
	if err != nil {
		return err
	}
	b := t.bounds
	cx, cy := float64(b.Min.X+b.Max.X)/2, float64(b.Min.Y+b.Max.Y)/2
	sx, sy := 1.0, -1.0
	if horizontal {
		sx, sy = -1, 1
	}
	t.set(Scale(sx, sy, cx, cy), xdraw.NearestNeighbor) // a mirror lands on whole pixels, so nearest is exact
	d.Commit("Flip", t.mark)
	return nil
}

// RotateLayer turns the chosen layer (or the selected part of it) by quarter turns clockwise (negative:
// anticlockwise), exactly, about its content's middle.
func (d *Doc) RotateLayer(quarters int) error {
	t, err := d.BeginTransform()
	if err != nil {
		return err
	}
	b := t.bounds
	cx, cy := float64(b.Min.X+b.Max.X)/2, float64(b.Min.Y+b.Max.Y)/2
	m := Rotate(float64(quarters)*90, cx, cy)
	// A quarter turn of a rectangle whose sides differ by an odd number would land on half pixels; shift it onto
	// whole ones so nearest stays exact.
	if quarters%2 != 0 && (b.Dx()+b.Dy())%2 != 0 {
		m = m.Then(Translate(0.5, 0.5))
	}
	t.set(m, xdraw.NearestNeighbor)
	d.Commit("Rotate", t.mark)
	return nil
}

// ---- The whole picture

// remapCanvas moves every layer, mask and the selection through m, a map of the canvas, as one step called name;
// the frame goes with them, becoming frame when that is not empty (an exact size a rounded map would miss). A text layer follows a move or a scale that keeps its proportions as text, and
// becomes plain pixels under anything else. Exact maps (mirrors and quarter turns) keep every pixel as it was.
func (d *Doc) remapCanvas(name string, m Affine, exact bool, frame image.Rectangle) {
	mark := d.Mark()
	var k xdraw.Interpolator = xdraw.CatmullRom
	if exact {
		k = xdraw.NearestNeighbor
	}
	var walk func(g *Node)
	walk = func(g *Node) {
		for _, c := range g.Children {
			if c.Mask != nil {
				c.Mask = resampleMask(c.Mask, m, k)
			}
			switch {
			case c.IsGroup():
				walk(c)
				continue
			case c.Vector != nil:
				transformVector(c, m) // exactly, whatever the map
				continue
			case c.Adjust != nil:
				continue // only its mask, done above
			}
			c.Pixels = resampleRaster(c.Pixels, m, k)
			if t := c.Text; t != nil {
				c.Text = nil
				if m.B == 0 && m.D == 0 && m.A > 0 && math.Abs(m.A-m.E) <= 1e-9*m.A {
					moved := *t
					moved.X, moved.Y = m.A*t.X+m.C, m.E*t.Y+m.F
					moved.Size *= m.A
					c.Text = &moved
				}
			}
		}
	}
	walk(d.Root)
	if d.Selection != nil {
		d.Selection = resampleMask(d.Selection, m, k)
	}
	switch {
	case !frame.Empty():
		d.Frame = frame
	case !d.Frame.Empty():
		d.Frame = m.bounds(d.Frame)
	}
	if len(d.Boards) > 0 {
		boards := slices.Clone(d.Boards)
		for i := range boards {
			boards[i].Rect = m.bounds(boards[i].Rect)
		}
		d.Boards = boards
	}
	d.lastSelection = nil // it would no longer fit
	d.Commit(name, mark)
}

// pivot is the middle the whole picture turns and mirrors about: the frame's, or else what is painted's.
func (d *Doc) pivot() (float64, float64) {
	r := d.Extent()
	return float64(r.Min.X+r.Max.X) / 2, float64(r.Min.Y+r.Max.Y) / 2
}

// FlipCanvas mirrors the whole picture.
func (d *Doc) FlipCanvas(horizontal bool) {
	cx, cy := d.pivot()
	sx, sy := 1.0, -1.0
	if horizontal {
		sx, sy = -1, 1
	}
	d.remapCanvas("Flip canvas", Scale(sx, sy, cx, cy), true, image.Rectangle{})
}

// RotateCanvas turns the whole picture by quarter turns clockwise (negative: anticlockwise).
func (d *Doc) RotateCanvas(quarters int) {
	q := ((quarters % 4) + 4) % 4
	if q == 0 {
		return
	}
	r := d.Extent()
	cx, cy := d.pivot()
	m := Rotate(float64(q)*90, cx, cy)
	// An odd quarter turn of a frame whose sides differ by an odd number lands on half pixels; shift it onto
	// whole ones so every pixel stays exact.
	if q%2 != 0 && (r.Dx()+r.Dy())%2 != 0 {
		m = m.Then(Translate(0.5, 0.5))
	}
	d.remapCanvas("Rotate canvas", m, true, image.Rectangle{})
}

// ResizeImage resamples the whole picture so its frame (or what is painted) becomes w×h, as Image › Image Size
// does.
func (d *Doc) ResizeImage(w, h int) error {
	r := d.Extent()
	switch {
	case w <= 0 || h <= 0 || w > maxSide || h > maxSide:
		return ErrTooBig
	case r.Empty():
		return ErrEmpty
	case w == r.Dx() && h == r.Dy():
		return nil
	}
	sx, sy := float64(w)/float64(r.Dx()), float64(h)/float64(r.Dy())
	m := Translate(-float64(r.Min.X), -float64(r.Min.Y)).Then(Affine{A: sx, E: sy}).Then(Translate(float64(r.Min.X), float64(r.Min.Y)))
	var frame image.Rectangle
	if !d.Frame.Empty() {
		frame = image.Rect(r.Min.X, r.Min.Y, r.Min.X+w, r.Min.Y+h)
	}
	d.remapCanvas("Image size", m, false, frame)
	return nil
}

// RotateCanvasBy turns the whole picture by deg degrees clockwise about its middle, as Image › Image Rotation ›
// Arbitrary does; the frame grows to hold every corner.
func (d *Doc) RotateCanvasBy(deg float64) error {
	if q := deg / 90; q == math.Trunc(q) {
		d.RotateCanvas(int(q))
		return nil
	}
	cx, cy := d.pivot()
	d.remapCanvas("Rotate canvas", Rotate(deg, cx, cy), false, image.Rectangle{})
	return nil
}

// ResizeCanvas makes the frame w×h, keeping the picture's place by the anchor (0 to 1 each way: 0, 0 keeps the top
// left, 0.5, 0.5 the middle), as Image › Canvas Size does. Nothing is cut: the canvas goes on past the frame.
func (d *Doc) ResizeCanvas(w, h int, ax, ay float64) error {
	if w <= 0 || h <= 0 || w > maxSide || h > maxSide {
		return ErrTooBig
	}
	r := d.Extent()
	dx, dy := int(math.Round(float64(w-r.Dx())*ax)), int(math.Round(float64(h-r.Dy())*ay))
	frame := image.Rect(r.Min.X-dx, r.Min.Y-dy, r.Min.X-dx+w, r.Min.Y-dy+h)
	if frame == d.Frame {
		return nil
	}
	m := d.Mark()
	d.Frame = frame
	d.Commit("Canvas size", m)
	return nil
}

// Crop makes r the frame, as the crop tool does. With deletePixels what falls outside is thrown away, as
// Photoshop's "Delete cropped pixels" does; without it the canvas keeps it past the frame.
func (d *Doc) Crop(r image.Rectangle, deletePixels bool) error {
	r = r.Canon()
	if r.Empty() || r.Dx() > maxSide || r.Dy() > maxSide {
		return ErrTooBig
	}
	if r == d.Frame && !deletePixels {
		return nil
	}
	m := d.Mark()
	d.Frame = r
	if deletePixels {
		// Kept tile by tile: the frame may be large and a layer's pixels far apart.
		keep := func(rs *Raster) *Raster {
			out := NewRaster()
			keys := map[image.Point]bool{}
			keysOf(&rs.p, keys)
			for k := range keys {
				if part := tileRect(k).Intersect(r); !part.Empty() {
					out.Write(rs.Read(part))
				}
			}
			return out
		}
		var walk func(g *Node)
		walk = func(g *Node) {
			for _, c := range g.Children {
				if c.IsGroup() {
					walk(c)
					continue
				}
				if c.Pixels != nil && !r.Union(c.Pixels.TileBounds()).Eq(r) {
					c.Pixels, c.Text = keep(c.Pixels), nil // cutting may take part of a text away
				}
			}
		}
		walk(d.Root)
	}
	d.Commit("Crop", m)
	return nil
}

// RevealAll makes the frame hold everything painted, as Image › Reveal All does.
func (d *Doc) RevealAll() error {
	if d.Frame.Empty() {
		return nil // without a frame everything is shown already
	}
	r := d.Frame.Union(d.Bounds())
	if r.Dx() > maxSide || r.Dy() > maxSide {
		return ErrTooBig
	}
	if r != d.Frame {
		m := d.Mark()
		d.Frame = r
		d.Commit("Reveal all", m)
	}
	return nil
}
