package paint

import "image"

// PickAt is what a click at canvas pixel p lands on, as the move tool's auto-select finds it: the ids of the nodes
// from the top level down to the topmost shown layer that shows something there, outermost first. A layer shows
// something where it is not transparent and its masks show it, within its artboard; an artboard with nothing over
// p but its background is picked on its own. It is empty when nothing is there.
func (d *Doc) PickAt(p image.Point) []int {
	return pickIn(d.Root, p)
}

func pickIn(g *Node, p image.Point) []int {
	px := image.Rect(p.X, p.Y, p.X+1, p.Y+1)
	for _, c := range g.Children { // top first
		if !c.Visible || c.Opacity <= 0 || c.Adjust != nil {
			continue
		}
		if c.Board != nil && !p.In(c.Board.Rect) {
			continue // what is in a board shows only on it
		}
		if _, alpha, fill, hidden := shapeAt(c, px, 0); hidden || alpha == nil && fill == 0 || alpha != nil && alpha.Pix[0] == 0 {
			continue // its masks hide it here
		}
		if c.IsGroup() {
			if chain := pickIn(c, p); chain != nil {
				return append([]int{c.ID}, chain...)
			}
			if c.Board != nil && c.Board.Fill.A > 0 {
				return []int{c.ID}
			}
			continue
		}
		if reach := reachOfNode(c); !p.In(reach) {
			continue
		}
		if readLayer(c, px, 0).Pix[3] > 0 {
			return []int{c.ID}
		}
	}
	return nil
}

// PathTo is the ids of the nodes from the top level down to node id, outermost first; empty when there is none.
func (d *Doc) PathTo(id int) []int {
	var path []int
	var walk func(g *Node) bool
	walk = func(g *Node) bool {
		for _, c := range g.Children {
			path = append(path, c.ID)
			if c.ID == id || walk(c) {
				return true
			}
			path = path[:len(path)-1]
		}
		return false
	}
	walk(d.Root)
	return path
}

// SelectMask selects by node id's layer mask, as Ctrl+click on its thumbnail does in Photoshop: white selected, black
// not, whether or not the mask is turned off; combined with the selection by mode, as one step.
func (d *Doc) SelectMask(id int, mode SelectMode) error {
	n := d.Node(id)
	switch {
	case n == nil:
		return ErrNoLayer
	case n.Mask == nil:
		return ErrNoMask
	}
	d.SelectShape(n.Mask.Snapshot(), mode)
	return nil
}

// SelectVectorMask selects inside node id's vector mask, as Ctrl+click on its thumbnail does in Photoshop; combined
// with the selection by mode, as one step.
func (d *Doc) SelectVectorMask(id int, mode SelectMode) error {
	n := d.Node(id)
	switch {
	case n == nil:
		return ErrNoLayer
	case n.Clip == nil:
		return ErrNoClip
	}
	d.SelectShape(clipMask(n.Clip), mode)
	return nil
}

// SelectContent selects what node id shows, as Ctrl+click on a layer does in Photoshop: how opaque it is at each
// pixel, its masks taken in, its own opacity and visibility left out; combined with the selection by mode, as one
// step.
func (d *Doc) SelectContent(id int, mode SelectMode) error {
	n := d.Node(id)
	if n == nil {
		return ErrNoLayer
	}
	shown := *n
	shown.Visible, shown.Opacity = true, 1
	if shown.Blend == BlendPassThrough {
		shown.Blend = BlendNormal // seen on its own
	}
	alone := &Node{Children: []*Node{&shown}}
	reach, bounded := contentReach(&shown)
	if !bounded {
		reach = d.limit()
	}
	shape := NewMask(0)
	if !reach.Empty() {
		k0, k1 := keysOver(reach)
		for ky := k0.Y; ky <= k1.Y; ky++ {
			for kx := k0.X; kx <= k1.X; kx++ {
				r := tileRect(image.Pt(kx, ky)).Intersect(reach)
				img := image.NewRGBA(r)
				d.compositeInto(img, alone, r, 0)
				a := image.NewAlpha(r)
				for i := range a.Pix {
					a.Pix[i] = img.Pix[i*4+3]
				}
				shape.Write(a)
			}
		}
	}
	d.SelectShape(shape, mode)
	return nil
}
