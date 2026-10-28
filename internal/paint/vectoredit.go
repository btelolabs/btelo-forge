package paint

import (
	"image"
	"slices"
)

// VectorSession is the pen or the node tool editing a vector layer's object, or a layer's vector mask, shown as it
// goes and recorded as one step when it ends.
type VectorSession struct {
	d     *Doc
	n     *Node
	mark  Mark
	name  string
	added bool
	empty bool // the object had no outline when the edit began, as a new vector layer has none
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
	if n.vectorOf() == nil {
		return nil, ErrNotVector
	}
	s.n = n
	s.empty = outlineless(n.vectorOf())
	return s, nil
}

// outlineless reports whether v is a curve with no outline, which shows nothing.
func outlineless(v *Vector) bool { return len(v.Path) == 0 && v.Kind == VectorCurve }

// Layer is the layer being edited.
func (s *VectorSession) Layer() int { return s.n.ID }

// Vector is the object as it is now.
func (s *VectorSession) Vector() Vector { return *s.n.vectorOf() }

// Set shows v as the layer's object (or vector mask) and returns the canvas area that changed.
func (s *VectorSession) Set(v Vector) image.Rectangle {
	before := s.reach()
	s.n.setVectorOf(&v)
	return before.Union(s.reach())
}

// reach is where the edited object shows: a vector mask changes what its layer shows, within the layer's reach.
func (s *VectorSession) reach() image.Rectangle {
	if s.n.Vector != nil {
		return reachOfNode(s.n)
	}
	if r, bounded := contentReach(s.n); bounded {
		return r
	}
	return everywhere
}

// End records the edit as one step. An object the edit left with no outline goes with its layer, as deleting a
// curve's last nodes does (the picture's only layer becomes an empty pixel layer instead); a vector mask left with
// none goes. An empty vector layer the edit added nothing to stays, as it was made to be drawn on.
func (s *VectorSession) End() {
	if !s.empty && outlineless(s.n.vectorOf()) {
		if s.n.Vector == nil {
			s.n.Clip = nil
		} else {
			s.drop()
		}
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
	r := s.reach()
	s.d.restore(s.mark.s)
	if n := s.d.Node(s.n.ID); n != nil {
		r = r.Union(reachOfNode(n))
	}
	return r
}
