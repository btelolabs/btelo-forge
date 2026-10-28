package paint

import (
	"image"
	"slices"
)

// VectorSession is the pen or the node tool editing a vector layer's object, shown as it goes and recorded as one
// step when it ends.
type VectorSession struct {
	d     *Doc
	n     *Node
	mark  Mark
	name  string
	added bool
}

// BeginVector starts editing the vector layer id, or, with id 0, a new vector layer called layer holding v above the
// chosen node, as the pen's first node makes one. name is the step it will be recorded as.
func (d *Doc) BeginVector(id int, v Vector, layer, name string) (*VectorSession, error) {
	s := &VectorSession{d: d, mark: d.Mark(), name: name}
	if id == 0 {
		s.n = &Node{ID: d.newID(), Name: layer, Visible: true, Opacity: 1, Vector: &v}
		d.insertAbove(s.n)
		d.EditMask = false
		s.added = true
		return s, nil
	}
	n := d.Node(id)
	if n == nil {
		return nil, ErrNoLayer
	}
	if n.Vector == nil {
		return nil, ErrNotVector
	}
	s.n = n
	return s, nil
}

// Layer is the layer being edited.
func (s *VectorSession) Layer() int { return s.n.ID }

// Vector is the object as it is now.
func (s *VectorSession) Vector() Vector { return *s.n.Vector }

// Set shows v as the layer's object and returns the canvas area that changed.
func (s *VectorSession) Set(v Vector) image.Rectangle {
	before := reachOfNode(s.n)
	s.n.Vector = &v
	return before.Union(reachOfNode(s.n))
}

// End records the edit as one step. An object left with no outline goes with its layer, as deleting a curve's last
// nodes does; the picture's only layer becomes an empty pixel layer instead.
func (s *VectorSession) End() {
	if len(s.n.Vector.Path) == 0 && s.n.Vector.Kind == VectorCurve {
		s.drop()
	}
	if s.added && s.d.Node(s.n.ID) == nil {
		s.d.restore(s.mark.s) // a new layer that came to nothing leaves no step
		return
	}
	s.d.Commit(s.name, s.mark)
}

// drop takes the edited layer out of the picture.
func (s *VectorSession) drop() {
	d := s.d
	n, parent, i := d.find(s.n.ID)
	if n == nil {
		return
	}
	if len(d.Layers()) == 1 {
		n.Vector, n.Pixels = nil, NewRaster()
		return
	}
	parent.Children = slices.Delete(parent.Children, i, i+1)
	switch {
	case i < len(parent.Children):
		d.Active = parent.Children[i].ID
	case i > 0:
		d.Active = parent.Children[i-1].ID
	case parent != d.Root:
		d.Active = parent.ID
	}
}

// Cancel puts the picture back as it was before the edit and returns the canvas area that changed.
func (s *VectorSession) Cancel() image.Rectangle {
	r := reachOfNode(s.n)
	s.d.restore(s.mark.s)
	if n := s.d.Node(s.n.ID); n != nil {
		r = r.Union(reachOfNode(n))
	}
	return r
}
