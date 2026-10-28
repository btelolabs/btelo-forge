package paint

import (
	"errors"
	"image"
	"math"
)

// ErrVectorLayer means a pixel operation was asked of a vector layer, which has no pixels to paint until it is
// rasterized, as Photoshop asks of a shape layer.
var ErrVectorLayer = errors.New("the chosen layer is a vector layer; rasterize it to paint on it")

// pixelLayer is the chosen node when it has pixels to paint: a pixel layer's own, or the mask being edited of any
// layer or group, which a mask hides alike.
func (d *Doc) pixelLayer() (*Node, error) {
	n := d.Node(d.Active)
	switch {
	case n == nil:
		return nil, ErrNoLayer
	case d.EditMask && n.Mask != nil:
		return n, nil
	case n.IsGroup():
		return nil, ErrGroup
	case n.Vector != nil:
		return nil, ErrVectorLayer
	case n.Adjust != nil:
		return nil, ErrAdjustmentLayer
	}
	return n, nil
}

// reachOfNode is a quick bound of where a layer shows anything: its tiles, or its object, and its effects.
func reachOfNode(n *Node) image.Rectangle {
	var r image.Rectangle
	switch {
	case n.Vector != nil:
		r = n.Vector.PixelBounds()
	case n.Pixels != nil:
		r = n.Pixels.TileBounds()
	}
	if e := n.Style.reach(); e > 0 && !r.Empty() {
		r = r.Inset(-e)
	}
	return r
}

// boundsOfNode is where a layer has anything, exactly for pixels.
func boundsOfNode(n *Node) image.Rectangle {
	var r image.Rectangle
	switch {
	case n.Vector != nil:
		r = n.Vector.PixelBounds()
	case n.Pixels != nil:
		r = n.Pixels.Bounds()
	}
	if e := n.Style.reach(); e > 0 && !r.Empty() {
		r = r.Inset(-e) // the effects show beyond the pixels
	}
	return r
}

// ReadLayer is a layer's pixels over r at detail level l (r in that level's pixels): its raster's, or its object
// drawn there, as a thumbnail shows it.
func ReadLayer(n *Node, r image.Rectangle, l int) *image.RGBA { return readLayer(n, r, l) }

func readLayer(n *Node, r image.Rectangle, l int) *image.RGBA {
	if n.Adjust != nil {
		return image.NewRGBA(r) // it has no pixels of its own
	}
	if n.Vector != nil {
		img := image.NewRGBA(r)
		n.Vector.Render(img, r, 1/float64(int(1)<<l))
		return img
	}
	return n.Pixels.ReadLevel(l, r)
}

// rasterOf is a layer as pixels: its raster, or its object drawn tile by tile.
func rasterOf(n *Node) *Raster {
	if n.Vector == nil {
		return n.Pixels
	}
	out := NewRaster()
	b := n.Vector.PixelBounds()
	if b.Empty() {
		return out
	}
	k0, k1 := keysOver(b)
	for ky := k0.Y; ky <= k1.Y; ky++ {
		for kx := k0.X; kx <= k1.X; kx++ {
			out.Write(readLayer(n, tileRect(image.Pt(kx, ky)), 0))
		}
	}
	return out
}

// AddVector puts an object on a new vector layer called name above the chosen node, and chooses it.
func (d *Doc) AddVector(v Vector, name string) *Node {
	m := d.Mark()
	n := &Node{ID: d.newID(), Name: name, Visible: true, Opacity: 1, Vector: &v}
	d.insertAbove(n)
	d.EditMask = false
	d.Commit("Add shape", m)
	return n
}

// AddVectorLayer adds an empty vector layer above the chosen node and chooses it, for the vector brush, the pen and
// the shapes to draw on; what is drawn on it is filled with fill.
func (d *Doc) AddVectorLayer(name string, fill VectorPaint) *Node {
	m := d.Mark()
	n := &Node{ID: d.newID(), Name: name, Visible: true, Opacity: 1, Vector: &Vector{Kind: VectorCurve, M: Identity, Fill: fill}}
	d.insertAbove(n)
	d.EditMask = false
	d.Commit("New vector layer", m)
	return n
}

// SetVector replaces a vector layer's object, or else the node's vector mask, as one step called name, as editing
// it with the node tool or its properties does.
func (d *Doc) SetVector(id int, v Vector, name string) {
	n := d.Node(id)
	if n == nil || n.vectorOf() == nil {
		return
	}
	m := d.Mark()
	n.setVectorOf(&v)
	d.Commit(name, m)
}

// Rasterize turns the chosen vector or text layer into a plain pixel layer that looks the same, as Layer ›
// Rasterize does.
func (d *Doc) Rasterize() error {
	l, err := d.ActiveLayer()
	if err != nil {
		return err
	}
	if l.Adjust != nil {
		return ErrAdjustmentLayer
	}
	if l.Vector == nil {
		return d.RasterizeText()
	}
	m := d.Mark()
	l.Pixels, l.Vector = rasterOf(l), nil
	d.Commit("Rasterize", m)
	return nil
}

// Transformed is the object mapped through m: exactly, as it is drawn anew at any scale.
func (v *Vector) Transformed(m Affine) *Vector {
	w := *v
	w.M = w.M.Then(m)
	return &w
}

// transformVector maps a vector layer's object through m: exactly, as it is drawn anew at any scale.
func transformVector(n *Node, m Affine) { n.Vector = n.Vector.Transformed(m) }

// TransformVector maps the chosen vector layer's object through m as one step called name, as Free Transform and
// the move tool do for an object.
func (d *Doc) TransformVector(m Affine, name string) error {
	l, err := d.ActiveLayer()
	if err != nil {
		return err
	}
	if l.Vector == nil {
		return ErrNotVector
	}
	mark := d.Mark()
	transformVector(l, m)
	d.Commit(name, mark)
	return nil
}

// ErrNotVector means a vector operation was asked of a layer that is not a vector layer.
var ErrNotVector = errors.New("the chosen layer is not a vector layer")

// valid reports whether an object read from a file makes sense to draw.
func (v *Vector) valid() bool {
	finite := func(fs ...float64) bool {
		for _, f := range fs {
			if math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > 1e9 {
				return false
			}
		}
		return true
	}
	if v.Kind < VectorCurve || v.Kind > VectorStar || v.Rule < NonZero || v.Rule > EvenOdd ||
		!finite(v.Size.X, v.Size.Y, v.Radius, v.Inner, v.M.A, v.M.B, v.M.C, v.M.D, v.M.E, v.M.F, v.Stroke.Width, v.Stroke.Miter) ||
		v.Sides < 0 || v.Sides > 1000 || v.Stroke.Width < 0 || len(v.Stroke.Dash) > 64 ||
		v.Fill.Kind < PaintNone || v.Fill.Kind > PaintConical || v.Stroke.Paint.Kind < PaintNone || v.Stroke.Paint.Kind > PaintConical ||
		!finite(v.Fill.Aspect, v.Stroke.Paint.Aspect) || v.Fill.Aspect < 0 || v.Stroke.Paint.Aspect < 0 {
		return false
	}
	nodes := 0
	for _, s := range v.Path {
		if s.Op < OpJoin || s.Op > OpExclude {
			return false
		}
		for _, n := range s.Nodes {
			if !finite(n.P.X, n.P.Y, n.In.X, n.In.Y, n.Out.X, n.Out.Y) {
				return false
			}
		}
		nodes += len(s.Nodes)
	}
	return nodes <= 1_000_000 && len(v.Fill.Stops) <= 256 && len(v.Stroke.Paint.Stops) <= 256
}

// ReachOf is a quick bound of where a layer has anything: its tiles, or its object; empty for a group.
func ReachOf(n *Node) image.Rectangle { return reachOfNode(n) }
