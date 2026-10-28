package paint

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"slices"
)

// insertAbove puts n above the chosen node, in the same group; into a chosen open group, it goes on top.
func (d *Doc) insertAbove(n *Node) {
	cur, parent, i := d.find(d.Active)
	switch {
	case cur == nil:
		d.Root.Children = slices.Insert(d.Root.Children, 0, n)
	case cur.IsGroup() && cur.Open:
		cur.Children = slices.Insert(cur.Children, 0, n)
	default:
		parent.Children = slices.Insert(parent.Children, i, n)
	}
	d.Active = n.ID
}

// AddLayer adds an empty layer above the chosen one and chooses it.
func (d *Doc) AddLayer(name string) *Node {
	m := d.Mark()
	n := d.newLayer(name)
	d.insertAbove(n)
	d.Commit("New layer", m)
	return n
}

// AddGroup adds an empty, open group above the chosen node and chooses it.
func (d *Doc) AddGroup(name string) *Node {
	m := d.Mark()
	n := &Node{ID: d.newID(), Name: name, Visible: true, Opacity: 1, Children: []*Node{}, Open: true}
	d.insertAbove(n)
	d.Commit("New group", m)
	return n
}

// Duplicate copies the chosen layer or group, with its pixels, above it; copySuffix is added to the names.
func (d *Doc) Duplicate(copySuffix string) (*Node, error) {
	cur, parent, i := d.find(d.Active)
	if cur == nil {
		return nil, ErrNoLayer
	}
	m := d.Mark()
	var dup func(n *Node) *Node
	dup = func(n *Node) *Node {
		c := *n
		c.ID = d.newID()
		// The copy shares the tiles until either is painted on.
		if n.Pixels != nil {
			c.Pixels = n.Pixels.Snapshot()
		}
		if n.Mask != nil {
			c.Mask = n.Mask.Snapshot()
		}
		if n.Children != nil {
			c.Children = make([]*Node, len(n.Children))
			for j, ch := range n.Children {
				c.Children[j] = dup(ch)
			}
		}
		return &c
	}
	c := dup(cur)
	c.Name = cur.Name + copySuffix
	parent.Children = slices.Insert(parent.Children, i, c)
	d.Active = c.ID
	d.Commit("Duplicate layer", m)
	return c, nil
}

// Delete removes the chosen node and chooses the one that took its place; the last layer cannot go.
func (d *Doc) Delete() error {
	cur, parent, i := d.find(d.Active)
	if cur == nil {
		return ErrNoLayer
	}
	remaining := len(d.Layers())
	if cur.IsGroup() {
		remaining -= len(layersIn(cur))
	} else {
		remaining--
	}
	if remaining == 0 {
		return ErrLastLayer
	}
	m := d.Mark()
	parent.Children = slices.Delete(parent.Children, i, i+1)
	switch {
	case i < len(parent.Children):
		d.Active = parent.Children[i].ID
	case i > 0:
		d.Active = parent.Children[i-1].ID
	case parent != d.Root:
		d.Active = parent.ID
	default:
		d.Active = d.someNode()
	}
	d.Commit("Delete layer", m)
	return nil
}

func layersIn(g *Node) []*Node {
	var out []*Node
	for _, c := range g.Children {
		if c.IsGroup() {
			out = append(out, layersIn(c)...)
		} else {
			out = append(out, c)
		}
	}
	return out
}

// MergeDown paints the chosen layer onto the layer under it, which keeps its name and opacity; a hidden layer is
// dropped, as nothing of it shows.
func (d *Doc) MergeDown() error {
	cur, parent, i := d.find(d.Active)
	switch {
	case cur == nil:
		return ErrNoLayer
	case cur.IsGroup():
		return ErrGroup
	case i+1 >= len(parent.Children) || parent.Children[i+1].IsGroup() || parent.Children[i+1].Adjust != nil:
		return ErrNothingBelow
	}
	m := d.Mark()
	below := parent.Children[i+1]
	if cur.Adjust != nil {
		// An adjustment merged down changes the layer under it, as Photoshop does.
		under := rasterOf(below)
		if cur.Visible {
			under = blendOnto(under, adjustRaster(under, cur.Adjust.Filter()), cur.Opacity, cur.Blend, cur.shownMask())
		}
		below.Pixels, below.Vector, below.Text = under, nil, nil
		parent.Children = slices.Delete(parent.Children, i, i+1)
		d.Active, d.EditMask = below.ID, false
		d.Commit("Merge down", m)
		return nil
	}
	// The upper layer is painted onto the lower one with its own blend mode, opacity and mask; the lower keeps its
	// own.
	// A vector layer on either side becomes pixels, as merging shape layers does in Photoshop.
	under := rasterOf(below)
	switch {
	case !cur.Visible:
	case !cur.Style.empty():
		// The upper layer's effects become pixels too: the two drawn as they look, the lower one plainly.
		plain := &Node{ID: -1, Visible: true, Opacity: 1, Pixels: under}
		under = d.compositeOf(&Node{Children: []*Node{cur, plain}, Open: true})
	default:
		under = blendOnto(under, rasterOf(cur), cur.Opacity, cur.Blend, cur.shownMask())
	}
	below.Pixels, below.Vector, below.Text = under, nil, nil
	parent.Children = slices.Delete(parent.Children, i, i+1)
	d.Active = below.ID
	d.Commit("Merge down", m)
	return nil
}

// MergeGroup turns the chosen group into one layer that looks the same.
func (d *Doc) MergeGroup() error {
	cur, parent, i := d.find(d.Active)
	switch {
	case cur == nil:
		return ErrNoLayer
	case !cur.IsGroup():
		return ErrGroup
	case cur.IsBoard():
		return ErrMergeBoard
	}
	m := d.Mark()
	mode := cur.Blend
	if mode == BlendPassThrough {
		mode = BlendNormal
	}
	n := &Node{ID: d.newID(), Name: cur.Name, Visible: cur.Visible, Opacity: cur.Opacity, Blend: mode, Pixels: d.compositeOf(cur),
		Mask: cur.Mask, MaskOff: cur.MaskOff, Clip: cur.Clip}
	parent.Children[i] = n
	d.Active = n.ID
	d.Commit("Merge group", m)
	return nil
}

// Flatten merges every visible layer into one, as the picture looks; hidden layers are dropped.
func (d *Doc) Flatten(name string) {
	m := d.Mark()
	n := &Node{ID: d.newID(), Name: name, Visible: true, Opacity: 1, Pixels: d.compositeOf(d.Root)}
	d.Root.Children = []*Node{n}
	d.Active = n.ID
	d.Commit("Flatten", m)
}

// GroupChosen puts the chosen node into a new group in its place.
func (d *Doc) GroupChosen(name string) (*Node, error) { return d.Group(name, []int{d.Active}) }

// Group puts the nodes ids into a new group and chooses it, as Ctrl+G does in Photoshop with several layers chosen:
// the group takes the place of the topmost of them, and they keep their order in the panel. A node inside another
// of them goes along with it; ids no longer there are passed over.
func (d *Doc) Group(name string, ids []int) (*Node, error) {
	nodes := d.outermost(ids)
	if len(nodes) == 0 {
		return nil, ErrNoLayer
	}
	if slices.ContainsFunc(nodes, (*Node).IsBoard) {
		return nil, ErrBoardInside
	}
	m := d.Mark()
	g := &Node{ID: d.newID(), Name: name, Visible: true, Opacity: 1, Blend: BlendPassThrough, Open: true}
	_, parent, i := d.find(nodes[0].ID)
	parent.Children[i] = g
	for _, n := range nodes[1:] {
		_, p, j := d.find(n.ID)
		p.Children = slices.Delete(p.Children, j, j+1)
	}
	g.Children = nodes
	d.Active = g.ID
	d.Commit("Group", m)
	return g, nil
}

// outermost are the nodes ids in panel order, top first, leaving out those inside another of them and ids no
// longer there.
func (d *Doc) outermost(ids []int) []*Node {
	var out []*Node
	var walk func(g *Node)
	walk = func(g *Node) {
		for _, c := range g.Children {
			switch {
			case slices.Contains(ids, c.ID):
				out = append(out, c)
			case c.IsGroup():
				walk(c)
			}
		}
	}
	walk(d.Root)
	return out
}

// Place is where nodes dropped on another one go.
type Place int

const (
	PlaceAbove Place = iota // just above it, in its group
	PlaceBelow              // just below it, in its group
	PlaceInto               // at the top of it, a group
)

// Errors moving nodes can return.
var (
	ErrNotGroup    = errors.New("only a group can hold layers")
	ErrMoveInside  = errors.New("a layer cannot go next to or inside itself")
	ErrSmartInside = errors.New("a smart object cannot go inside a copy of itself")
)

// MoveNodes moves the nodes ids next to the node target or into it, keeping their order in the panel, as dragging
// them in Photoshop's layers panel does; a node inside another of them goes along with it. An artboard stays at the
// top level. It is one step of the history, and none when nothing moves.
func (d *Doc) MoveNodes(ids []int, target int, where Place) error {
	nodes := d.outermost(ids)
	dest, err := d.moveTo(nodes, target, where)
	if err != nil {
		return err
	}
	m := d.Mark()
	for _, n := range nodes {
		_, p, j := d.find(n.ID)
		p.Children = slices.Delete(p.Children, j, j+1)
	}
	at := 0
	if where != PlaceInto {
		_, _, at = d.find(target)
		if where == PlaceBelow {
			at++
		}
	}
	dest.Children = slices.Insert(dest.Children, at, nodes...)
	d.Commit("Move layer", m)
	return nil
}

// CanMoveNodes reports why MoveNodes would refuse to move the nodes ids, or nil when it would move them.
func (d *Doc) CanMoveNodes(ids []int, target int, where Place) error {
	_, err := d.moveTo(d.outermost(ids), target, where)
	return err
}

// moveTo is the group that nodes moved next to target or into it would go into, or why they cannot go there.
func (d *Doc) moveTo(nodes []*Node, target int, where Place) (*Node, error) {
	chain := d.path(target)
	if len(nodes) == 0 || len(chain) < 2 {
		return nil, ErrNoLayer
	}
	groups := chain[:len(chain)-1] // the target's group, and those it is in
	if where == PlaceInto {
		if !chain[len(chain)-1].IsGroup() {
			return nil, ErrNotGroup
		}
		groups = chain
	}
	dest := groups[len(groups)-1]
	// Where they go must be outside them, and not beside one of them.
	for _, n := range chain[1:] {
		if slices.Contains(nodes, n) {
			return nil, ErrMoveInside
		}
	}
	for _, n := range nodes {
		if n.IsBoard() && dest != d.Root {
			return nil, ErrBoardInside
		}
	}
	// A smart object cannot hold a copy of itself, or it would hold itself without end.
	for _, g := range groups {
		if g.IsSmart() && slices.ContainsFunc(nodes, func(n *Node) bool { return holdsLink(n, g.Smart.ID) }) {
			return nil, ErrSmartInside
		}
	}
	return dest, nil
}

// path is the chain of nodes from the root down to the node id, both included, or nil when it is not there.
func (d *Doc) path(id int) []*Node {
	if id == d.Root.ID {
		return []*Node{d.Root}
	}
	var walk func(g *Node, chain []*Node) []*Node
	walk = func(g *Node, chain []*Node) []*Node {
		chain = append(chain, g)
		for _, c := range g.Children {
			if c.ID == id {
				return append(slices.Clone(chain), c)
			}
			if c.IsGroup() {
				if out := walk(c, chain); out != nil {
					return out
				}
			}
		}
		return nil
	}
	return walk(d.Root, nil)
}

// holdsLink reports whether n is, or holds, a smart object with the link id.
func holdsLink(n *Node, id string) bool {
	if n.IsSmart() && n.Smart.ID == id {
		return true
	}
	return slices.ContainsFunc(n.Children, func(c *Node) bool { return holdsLink(c, id) })
}

// Ungroup moves the chosen group's children into its place.
func (d *Doc) Ungroup() error {
	cur, parent, i := d.find(d.Active)
	switch {
	case cur == nil:
		return ErrNoLayer
	case !cur.IsGroup():
		return ErrGroup
	}
	m := d.Mark()
	parent.Children = slices.Replace(parent.Children, i, i+1, cur.Children...)
	if len(cur.Children) > 0 {
		d.Active = cur.Children[0].ID
	} else {
		d.Active = d.someNode()
	}
	d.Commit("Ungroup", m)
	return nil
}

// Move steps the chosen node up (toward the top) or down among its siblings; at the end of a group it leaves the
// group, and next to an open group it goes into it, as in Photoshop's layers panel.
func (d *Doc) Move(up bool) error {
	cur, parent, i := d.find(d.Active)
	if cur == nil {
		return ErrNoLayer
	}
	m := d.Mark()
	if cur.IsBoard() {
		// A board stays at the top level: it steps past its neighbour, open group or not.
		j := i + 1
		if up {
			j = i - 1
		}
		if j >= 0 && j < len(parent.Children) {
			parent.Children[i], parent.Children[j] = parent.Children[j], parent.Children[i]
		}
		d.Commit("Move layer", m)
		return nil
	}
	parent.Children = slices.Delete(parent.Children, i, i+1)
	switch {
	case up && i > 0 && parent.Children[i-1].IsGroup() && parent.Children[i-1].Open:
		g := parent.Children[i-1]
		g.Children = append(g.Children, cur)
	case up && i > 0:
		parent.Children = slices.Insert(parent.Children, i-1, cur)
	case !up && i < len(parent.Children) && parent.Children[i].IsGroup() && parent.Children[i].Open:
		g := parent.Children[i]
		g.Children = slices.Insert(g.Children, 0, cur)
	case !up && i < len(parent.Children):
		parent.Children = slices.Insert(parent.Children, i+1, cur)
	case parent != d.Root:
		_, grand, gi := d.find(parent.ID)
		if up {
			grand.Children = slices.Insert(grand.Children, gi, cur)
		} else {
			grand.Children = slices.Insert(grand.Children, gi+1, cur)
		}
	default:
		parent.Children = slices.Insert(parent.Children, i, cur) // already at the very top or bottom
	}
	d.Commit("Move layer", m)
	return nil
}

// Choose makes id the node that painting and the layer operations act on.
func (d *Doc) Choose(id int) {
	if d.Node(id) != nil {
		d.Active = id
	}
}

// Rename, SetVisible, SetOpenGroup and SetOpacity change one node as a step of the history; to drag the opacity
// without filling the history, change Opacity directly between Mark and Commit.
func (d *Doc) Rename(id int, name string) { d.set(id, "Rename layer", func(n *Node) { n.Name = name }) }
func (d *Doc) SetVisible(id int, on bool) {
	d.set(id, "Show or hide layer", func(n *Node) { n.Visible = on })
}
func (d *Doc) SetOpenGroup(id int, open bool) {
	if n := d.Node(id); n != nil {
		n.Open = open // only how the panel shows it, not a change to the picture
	}
}
func (d *Doc) SetOpacity(id int, v float64) {
	d.set(id, "Layer opacity", func(n *Node) { n.Opacity = min(max(v, 0), 1) })
}

func (d *Doc) set(id int, name string, fn func(*Node)) {
	n := d.Node(id)
	if n == nil {
		return
	}
	m := d.Mark()
	fn(n)
	d.Commit(name, m)
}

// SetBlend sets a layer's or group's blend mode as a step of the history; only groups pass through.
func (d *Doc) SetBlend(id int, mode Blend) {
	d.set(id, "Blend mode", func(n *Node) {
		if mode >= 0 && mode < blendCount && (mode != BlendPassThrough || n.IsGroup()) {
			n.Blend = mode
		}
	})
}

// SetLockAlpha locks or unlocks a layer's transparent pixels.
func (d *Doc) SetLockAlpha(id int, on bool) {
	d.set(id, "Lock transparent pixels", func(n *Node) { n.LockAlpha = on })
}

// AddMask gives the chosen layer or group a mask: from the selection when there is one (hiding the rest), or
// showing everything; painting then goes to the mask.
func (d *Doc) AddMask() error {
	n := d.Node(d.Active)
	if n == nil {
		return ErrNoLayer
	}
	if n.Mask != nil {
		return ErrHasMask
	}
	m := d.Mark()
	mask := NewMask(255)
	if d.Selection != nil {
		mask = d.Selection.Snapshot()
	}
	n.Mask, n.MaskOff = mask, false
	d.EditMask = true
	d.Commit("Add layer mask", m)
	return nil
}

// ErrNoMask and ErrHasMask are what the mask commands say when there is no mask, or one already.
var (
	ErrNoMask  = errors.New("the chosen layer has no mask")
	ErrHasMask = errors.New("the chosen layer already has a mask")
)

// DeleteMask removes the chosen layer's mask, keeping its pixels as they were; ApplyMask makes the mask part of
// them first.
func (d *Doc) DeleteMask() error { return d.dropMask(false) }

// ApplyMask makes the chosen layer's mask part of its pixels and removes the mask.
func (d *Doc) ApplyMask() error { return d.dropMask(true) }

func (d *Doc) dropMask(apply bool) error {
	n := d.Node(d.Active)
	switch {
	case n == nil:
		return ErrNoLayer
	case n.Mask == nil:
		return ErrNoMask
	case apply && n.IsGroup():
		return ErrGroup
	case apply && n.Adjust != nil:
		return ErrAdjustmentLayer
	}
	m := d.Mark()
	if apply {
		n.Pixels, n.Vector, n.Text = blendOnto(NewRaster(), rasterOf(n), 1, BlendNormal, n.Mask), nil, nil
	}
	n.Mask, d.EditMask = nil, false
	name := "Delete layer mask"
	if apply {
		name = "Apply layer mask"
	}
	d.Commit(name, m)
	return nil
}

// SetMaskOff turns the chosen layer's mask off or on.
func (d *Doc) SetMaskOff(id int, off bool) {
	d.set(id, "Disable layer mask", func(n *Node) {
		if n.Mask != nil {
			n.MaskOff = off
		}
	})
}

// Translate moves the chosen layer's pixels (and its mask) by (dx, dy) as one step, as the arrow keys do with the
// move tool; nothing is cut off at the picture's edge. Inside a selection only the selected pixels move, leaving
// transparency behind, and the selection moves with them.
func (d *Doc) Translate(dx, dy int) error {
	m, err := d.BeginMove()
	if err != nil {
		return err
	}
	m.To(dx, dy)
	m.End()
	return nil
}

// Copy is the selected part of the chosen layer, or of the picture as it looks when merged is true, cut to the
// selection's bounds and faded where the selection is partial; with nothing selected it is all of it.
func (d *Doc) Copy(merged bool) (*image.RGBA, error) {
	r := d.SelectionBounds()
	if r.Empty() {
		r = d.Extent()
	}
	var src *image.RGBA
	if merged {
		src = d.Composite(r)
	} else {
		l, err := d.ActiveLayer()
		if err != nil {
			return nil, err
		}
		if d.Selection == nil {
			r = boundsOfNode(l)
		}
		src = readLayer(l, r, 0)
	}
	sel := d.selectionIn(r)
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			k := coverage(sel, x, y)
			i, j := src.PixOffset(x, y), out.PixOffset(x-r.Min.X, y-r.Min.Y)
			for c := range 4 {
				out.Pix[j+c] = uint8(mul8(uint32(src.Pix[i+c]), k))
			}
		}
	}
	return out, nil
}

// Clear makes the selected part of the chosen layer transparent (or hides it, on a mask being edited).
func (d *Doc) Clear() error { return d.clear("Clear") }

// Cut is Copy of the chosen layer followed by Clear.
func (d *Doc) Cut() (*image.RGBA, error) {
	img, err := d.Copy(false)
	if err != nil {
		return nil, err
	}
	return img, d.clear("Cut")
}

// Fill paints the selected part of the chosen layer (all of it when nothing is selected) with c, softly at a
// partial selection's edge; on a mask being edited it fills with c's brightness.
func (d *Doc) Fill(c color.NRGBA) error {
	pc := premul(c)
	return d.paintArea("Fill", nil, func(int, int) ([4]uint32, uint32) { return pc, 255 })
}

// clear deletes the selected pixels, or turns the selected part of the mask black; with nothing selected, all of
// them.
func (d *Doc) clear(name string) error {
	l, err := d.ActiveLayer()
	if err != nil {
		return err
	}
	toMask := d.EditMask && l.Mask != nil
	switch {
	case l.Vector != nil && !toMask:
		return ErrVectorLayer
	case l.Adjust != nil && !toMask:
		return ErrAdjustmentLayer
	}
	m := d.Mark()
	switch {
	case !toMask && l.LockAlpha:
		return nil // clearing would change the transparency the lock keeps
	case d.Selection == nil && toMask:
		l.Mask = NewMask(0)
	case d.Selection == nil:
		d.dropText(l)
		l.Pixels = NewRaster()
	default:
		var own *plane
		if toMask {
			own = &l.Mask.p
		} else {
			d.dropText(l)
			own = &l.Pixels.p
		}
		bpp := own.bpp
		for _, r := range d.areaOf(l, toMask, 0) {
			sel := d.selectionIn(r)
			own.tilesOver(r, func(pix []uint8, part image.Rectangle, corner image.Point) {
				for y := part.Min.Y; y < part.Max.Y; y++ {
					for x := part.Min.X; x < part.Max.X; x++ {
						k := coverage(sel, x, y)
						if k == 0 {
							continue
						}
						i := ((y-corner.Y)*TileSize + x - corner.X) * bpp
						for c := range bpp {
							pix[i+c] = uint8(mul8(uint32(pix[i+c]), 255-k))
						}
					}
				}
			})
		}
	}
	d.Commit(name, m)
	return nil
}

// keepAlpha sets a premultiplied pixel's alpha back to a, keeping its colour.
func keepAlpha(p []byte, a uint32) {
	na := uint32(p[3])
	for c := range 3 {
		v := uint32(0)
		if na > 0 {
			v = min(uint32(p[c])*255/na, 255)
		}
		p[c] = uint8(mul8(v, a))
	}
	p[3] = uint8(a)
}

// luma is a colour's brightness, 0 to 255, for painting on masks.
func luma(c color.NRGBA) uint8 {
	return uint8((299*uint32(c.R) + 587*uint32(c.G) + 114*uint32(c.B) + 500) / 1000)
}

// Paste puts img on a new layer above the chosen one with its top-left at at (centred on the screen, or the frame,
// when at is nil), and selects what was pasted.
func (d *Doc) Paste(img image.Image, name string, at *image.Point) *Node {
	m := d.Mark()
	b := img.Bounds()
	centre := d.View
	if centre.Empty() {
		centre = d.Frame
	}
	pos := image.Pt((centre.Min.X+centre.Max.X-b.Dx())/2, (centre.Min.Y+centre.Max.Y-b.Dy())/2)
	if at != nil {
		pos = *at
	}
	n := d.newLayer(name)
	r := image.Rectangle{Min: pos, Max: pos.Add(b.Size())}
	pix := image.NewRGBA(r)
	draw.Draw(pix, r, img, b.Min, draw.Src)
	n.Pixels.Write(pix)
	d.insertAbove(n)
	d.EditMask = false
	d.setSelection(d.RectMask(r))
	d.Commit("Paste", m)
	return n
}

// ErrEmpty means there is nothing to export: the canvas has no frame and nothing painted on it.
var ErrEmpty = errors.New("the canvas is empty")

// Picture is the picture as it is exported: the frame, or else everything painted, on the paper when it has
// paper.
func (d *Doc) Picture() (*image.RGBA, error) {
	r := d.Extent()
	switch {
	case r.Empty():
		return nil, ErrEmpty
	case r.Dx() > maxSide || r.Dy() > maxSide:
		return nil, ErrTooBig
	}
	img := image.NewRGBA(r)
	d.Render(img, r, 0)
	return img, nil
}

// WritePNG writes the picture as it is exported, or only the selected part when selectionOnly is true.
func (d *Doc) WritePNG(w io.Writer, selectionOnly bool) error {
	var img *image.RGBA
	var err error
	if selectionOnly && d.Selection != nil {
		img, err = d.Copy(true)
	} else {
		img, err = d.Picture()
	}
	if err != nil {
		return err
	}
	return (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(w, img)
}
