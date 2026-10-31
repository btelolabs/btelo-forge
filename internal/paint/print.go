package paint

import (
	"image"
	"image/color"
	"image/draw"
)

// Printing a canvas: the layers handed over one by one, bottom first, each as what it is. Vector layers and type stay
// shapes and words. Pixel layers, and anything whose look depends on what is under it (blend modes, adjustments,
// effects, masks, a group's own opacity), come as pictures at the canvas's own resolution, and only as many layers
// as that needs are merged into one.

// Printer takes what is on the canvas, in canvas pixels.
type Printer interface {
	// Picture is premultiplied pixels placed where img.Rect says.
	Picture(img *image.RGBA)
	Vector(v *Vector, opacity float64)
	Words(t Text, opacity float64)
	// Rect is a board's background.
	Rect(r image.Rectangle, c color.NRGBA)
	// PushRect and PushShape clip what follows, until Pop.
	PushRect(r image.Rectangle)
	PushShape(v *Vector)
	Pop()
}

// Print hands over what shows within r: all of the canvas, or the one artboard board when it is not 0.
func (d *Doc) Print(r image.Rectangle, board int, p Printer) {
	p.PushRect(r)
	defer p.Pop()
	if board != 0 {
		if n := d.Node(board); n != nil && n.IsBoard() {
			shown := *n
			shown.Visible = true
			d.printNodes([]*Node{&shown}, r, false, p)
		}
		return
	}
	d.printNodes(d.Root.Children, r, d.Paper, p)
}

// readsBelow reports that a node's look depends on what is under it.
func readsBelow(n *Node) bool {
	if !n.Visible || n.Opacity <= 0 {
		return false
	}
	return n.Adjust != nil || n.Blend != BlendNormal && n.Blend != BlendPassThrough
}

// holdsBlend reports that something inside a group blends with what is under it.
func holdsBlend(n *Node) bool {
	for _, c := range n.Children {
		if readsBelow(c) || c.IsGroup() && holdsBlend(c) {
			return true
		}
	}
	return false
}

// printNodes hands over a list of siblings, top first. Everything up to the topmost that blends with what is under
// it is merged into a picture, as it must be; what lies above that keeps its own form.
func (d *Doc) printNodes(list []*Node, r image.Rectangle, paper bool, p Printer) {
	from := len(list) // the list from here down goes straight
	for i := range list {
		if readsBelow(list[i]) {
			from = i
			break
		}
	}
	if from < len(list) {
		d.printPicture(list[from:], r, paper, p)
	}
	for i := from - 1; i >= 0; i-- {
		d.printNode(list[i], r, p)
	}
}

// printPicture merges nodes, top first, into a picture of where they show within r.
func (d *Doc) printPicture(nodes []*Node, r image.Rectangle, paper bool, p Printer) {
	at := image.Rectangle{}
	whole := paper
	for _, n := range nodes {
		if reach, bounded := contentReach(n); bounded {
			at = at.Union(reach)
		} else {
			whole = true
		}
	}
	if whole {
		at = r
	}
	if at = at.Intersect(r); at.Empty() {
		return
	}
	img := image.NewRGBA(at)
	if paper {
		draw.Draw(img, at, image.White, image.Point{}, draw.Src)
	}
	d.compositeBands(img, &Node{Children: nodes}, at, 0)
	p.Picture(img)
}

func (d *Doc) printNode(n *Node, r image.Rectangle, p Printer) {
	if !n.Visible || n.Opacity <= 0 {
		return
	}
	if reach, bounded := contentReach(n); bounded && reach.Intersect(r).Empty() {
		return
	}
	plain := n.activeMask() == nil && n.Style.empty() && n.Filters.shown() == nil
	if n.IsGroup() {
		if !plain || n.Opacity < 1 || n.Blend == BlendNormal && holdsBlend(n) {
			d.printPicture([]*Node{n}, r, false, p)
			return
		}
		if n.Clip != nil {
			p.PushShape(n.Clip)
			defer p.Pop()
		}
		if n.Board != nil {
			if n.Board.Fill.A != 0 {
				p.Rect(n.Board.Rect, n.Board.Fill)
			}
			p.PushRect(n.Board.Rect)
			defer p.Pop()
			r = r.Intersect(n.Board.Rect)
		}
		d.printNodes(n.Children, r, false, p)
		return
	}
	if plain && n.Vector != nil {
		if n.Clip != nil {
			p.PushShape(n.Clip)
			defer p.Pop()
		}
		p.Vector(n.Vector, n.Opacity)
		return
	}
	if plain && n.Text != nil {
		if n.Clip != nil {
			p.PushShape(n.Clip)
			defer p.Pop()
		}
		p.Words(*n.Text, n.Opacity)
		return
	}
	d.printPicture([]*Node{n}, r, false, p)
}
