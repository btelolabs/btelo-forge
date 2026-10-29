package paint

import "image"

// MoveSession is the move tool dragging the chosen layer, or the selected pixels of it, with the result shown as it
// goes and recorded as one step when it ends.
type MoveSession struct {
	d    *Doc
	l    *Node
	mark Mark
	off  image.Point

	// Moving a selection lifts its pixels off the layer once; each move lays them over the layer without them.
	base   *Raster     // the layer with the selected pixels taken out
	lifted *image.RGBA // the selected pixels, weighted by the selection, where they were
	sel    *Mask       // the selection when the move began
	// carried is the selection when it holds all of the layer's pixels wholly, as after a paste: the layer then
	// moves whole, as with nothing selected, and the selection with it.
	carried *Mask

	vector *Vector // a vector layer's object as it was, moved whole whatever is selected
	clip   *Vector // the layer's vector mask as it was, moved with the whole layer
	group  bool    // a group, or smart object, moved whole with everything in it
}

// BeginMove starts moving the chosen layer, or the selected part of it when something is selected.
func (d *Doc) BeginMove() (*MoveSession, error) {
	if g := d.Node(d.Active); g != nil && g.IsGroup() {
		// A group moves whole, whatever is selected: a smart object so goes to another place, its layers the same,
		// and an artboard with what is in it.
		return &MoveSession{d: d, l: g, mark: d.Mark(), group: true}, nil
	}
	l, err := d.ActiveLayer()
	if err != nil {
		return nil, err
	}
	if l.Adjust != nil {
		return nil, ErrAdjustmentLayer
	}
	m := &MoveSession{d: d, l: l, mark: d.Mark(), vector: l.Vector, clip: l.Clip}
	if d.Selection == nil || l.Vector != nil {
		return m, nil // a whole layer, a text layer as text, moves by shifting it
	}
	if l.Mask == nil && l.Clip == nil && d.selectsWhole(l) {
		m.carried = d.Selection
		return m, nil
	}
	d.dropText(l)
	m.sel = d.Selection
	r := d.SelectionBounds().Intersect(l.Pixels.TileBounds())
	m.lifted = l.Pixels.Read(r)
	sel := d.selectionIn(r)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			k := coverage(sel, x, y)
			i := m.lifted.PixOffset(x, y)
			for c := range 4 {
				m.lifted.Pix[i+c] = uint8(mul8(uint32(m.lifted.Pix[i+c]), k))
			}
		}
	}
	m.base = l.Pixels.Snapshot()
	m.base.Tiles(r, func(pix []uint8, part image.Rectangle, corner image.Point) {
		for y := part.Min.Y; y < part.Max.Y; y++ {
			for x := part.Min.X; x < part.Max.X; x++ {
				k := coverage(sel, x, y)
				i := ((y-corner.Y)*TileSize + x - corner.X) * 4
				for c := range 4 {
					pix[i+c] = uint8(mul8(uint32(pix[i+c]), 255-k))
				}
			}
		}
	})
	m.show()
	return m, nil
}

// selectsWhole reports whether the selection holds every pixel of layer l wholly, so lifting them leaves nothing.
func (d *Doc) selectsWhole(l *Node) bool {
	b := l.Pixels.Bounds()
	if b.Empty() || d.Selection.Fill() != 0 || !b.In(d.SelectionBounds()) {
		return false
	}
	sel := d.Selection.Read(b)
	p := &l.Pixels.p
	for k, t := range p.tiles {
		part := tileRect(k).Add(p.off).Intersect(b)
		corner := tileRect(k).Min.Add(p.off)
		for y := part.Min.Y; y < part.Max.Y; y++ {
			row := t.pix[((y-corner.Y)*TileSize)*4:]
			for x := part.Min.X; x < part.Max.X; x++ {
				if row[(x-corner.X)*4+3] != 0 && sel.Pix[sel.PixOffset(x, y)] != 255 {
					return false
				}
			}
		}
	}
	return true
}

// show lays the lifted pixels at the move's place over the layer without them, and moves the selection with them.
func (m *MoveSession) show() {
	pix := m.base.Snapshot()
	at := m.lifted.Rect.Add(m.off)
	moved := &image.RGBA{Pix: m.lifted.Pix, Stride: m.lifted.Stride, Rect: at}
	under := pix.Read(at)
	over(under, moved, at, 1)
	pix.Write(under)
	m.l.Pixels = pix
	m.d.Selection = m.sel.Moved(m.off.X, m.off.Y) // its levels kept, for the outline drawn each frame
}

// To moves by (dx, dy) from where the move began and returns the canvas area that changed.
func (m *MoveSession) To(dx, dy int) image.Rectangle {
	off := image.Pt(dx, dy)
	if off == m.off {
		return image.Rectangle{}
	}
	prev := m.off
	m.off = off
	l := m.l
	if m.group {
		// Moved on from where the last frame left it: moveNode gives every layer new pixels, its halved levels
		// kept, so a big picture dragged about is not halved again for each frame drawn small.
		before, bounded := contentReach(l)
		moveNode(l, off.X-prev.X, off.Y-prev.Y)
		after, _ := contentReach(l)
		if !bounded {
			return everywhere // an adjustment in it changes all under it
		}
		return before.Union(after)
	}
	if m.clip != nil && (m.vector != nil || m.sel == nil) {
		// It goes with the whole layer; what it shows lies within the layer's reach, which is redrawn.
		l.Clip = m.clip.Transformed(Translate(float64(dx), float64(dy)))
	}
	if m.vector != nil {
		before := reachOfNode(l)
		l.Vector = m.vector
		transformVector(l, Translate(float64(dx), float64(dy)))
		if l.Mask != nil {
			l.Mask.Move(off.X-prev.X, off.Y-prev.Y)
		}
		return before.Union(reachOfNode(l))
	}
	if m.sel == nil {
		if m.carried != nil {
			m.d.Selection = m.carried.Moved(off.X, off.Y)
		}
		before := l.Pixels.TileBounds()
		l.Pixels.Move(off.X-prev.X, off.Y-prev.Y)
		changed := before.Union(l.Pixels.TileBounds())
		if l.Mask != nil {
			before := l.Mask.TileBounds()
			l.Mask.Move(off.X-prev.X, off.Y-prev.Y)
			changed = changed.Union(before).Union(l.Mask.TileBounds())
		}
		return changed
	}
	m.show()
	return m.lifted.Rect.Add(prev).Union(m.lifted.Rect.Add(off))
}

// End records the move as one step.
func (m *MoveSession) End() {
	d, l := m.d, m.l
	if m.off == (image.Point{}) {
		d.restore(m.mark.s) // nothing moved: not even the text became pixels
		return
	}
	if m.sel == nil && !m.group {
		shiftText(l, m.off.X, m.off.Y)
	}
	name := "Move"
	if l.IsBoard() {
		name = "Move artboard"
	}
	d.Commit(name, m.mark)
}
