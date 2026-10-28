package paint

import (
	"errors"
	"image"
	"math"
)

// ErrVectorLayer means a pixel operation was asked of a vector layer, which has no pixels to paint until it is
// rasterized, as Photoshop asks of a shape layer.
var ErrVectorLayer = errors.New("the chosen layer is a vector layer; rasterize it to paint on it")

// pixelLayer is the chosen layer when it has pixels.
func (d *Doc) pixelLayer() (*Node, error) {
	l, err := d.ActiveLayer()
	if err != nil {
		return nil, err
	}
	switch {
	case l.Vector != nil:
		return nil, ErrVectorLayer
	case l.Adjust != nil && !(d.EditMask && l.Mask != nil):
		return nil, ErrAdjustmentLayer
	}
	return l, nil
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

// SetVector replaces a vector layer's object as one step called name, as editing it with the node tool or its
// properties does.
func (d *Doc) SetVector(id int, v Vector, name string) {
	n := d.Node(id)
	if n == nil || n.Vector == nil {
		return
	}
	m := d.Mark()
	n.Vector = &v
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

// transformVector maps a vector layer's object through m: exactly, as it is drawn anew at any scale.
func transformVector(n *Node, m Affine) {
	v := *n.Vector
	v.M = v.M.Then(m)
	n.Vector = &v
}

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
		v.Fill.Kind < PaintNone || v.Fill.Kind > PaintRadial || v.Stroke.Paint.Kind < PaintNone || v.Stroke.Paint.Kind > PaintRadial {
		return false
	}
	nodes := 0
	for _, s := range v.Path {
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
