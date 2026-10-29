package paint

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"slices"
)

// Artboards are groups of a kind, as Photoshop's and Affinity Designer's are: a group with an area of the canvas, a
// fixed size and its own background. What is in it is cut to its area and is what it exports as a picture of its
// own; the canvas around the boards is the pasteboard, which the editor dims. Boards lie at the top level of the
// layers, never one in another, and a picture may hold any number of them.

// BoardFrame is what makes a group an artboard: its area of the canvas and its background. It is a value never
// changed in place.
type BoardFrame struct {
	Rect image.Rectangle
	Fill color.NRGBA // transparent leaves the board clear
}

// Board describes an artboard: its group's id and name, and its frame.
type Board struct {
	ID   int
	Name string
	Rect image.Rectangle
	Fill color.NRGBA
}

// Errors the artboard operations can return.
var (
	ErrNoBoard     = errors.New("there is no such artboard")
	ErrBoardInside = errors.New("an artboard stays at the top level of the layers, not inside another group")
	ErrMergeBoard  = errors.New("an artboard cannot be merged; ungroup it first")
)

// minBoard is the smallest side a board can have.
const minBoard = 1

// IsBoard reports whether n is an artboard.
func (n *Node) IsBoard() bool { return n.Board != nil }

func boardOf(n *Node) Board {
	return Board{ID: n.ID, Name: n.Name, Rect: n.Board.Rect, Fill: n.Board.Fill}
}

// Boards lists the artboards, bottom first.
func (d *Doc) Boards() []Board {
	var out []Board
	for i := len(d.Root.Children) - 1; i >= 0; i-- {
		if c := d.Root.Children[i]; c.IsBoard() {
			out = append(out, boardOf(c))
		}
	}
	return out
}

// Board finds a board by id.
func (d *Doc) Board(id int) (Board, bool) {
	n := d.Node(id)
	if n == nil || !n.IsBoard() {
		return Board{}, false
	}
	return boardOf(n), true
}

// BoardOf is the board that n is in, or is, or 0.
func (d *Doc) BoardOf(id int) int {
	for _, c := range d.Root.Children {
		if c.IsBoard() && (c.ID == id || within(c, id)) {
			return c.ID
		}
	}
	return 0
}

// within reports whether node id is somewhere inside group g.
func within(g *Node, id int) bool {
	for _, c := range g.Children {
		if c.ID == id || within(c, id) {
			return true
		}
	}
	return false
}

// validBoard keeps a board's rectangle upright, at least a pixel and at most the largest picture.
func validBoard(r image.Rectangle) (image.Rectangle, error) {
	r = r.Canon()
	switch {
	case r.Dx() < minBoard || r.Dy() < minBoard:
		return r, ErrEmpty
	case r.Dx() > maxSide || r.Dy() > maxSide:
		return r, ErrTooBig
	}
	return r, nil
}

// AddBoard adds a board over r called name, with a white background, and chooses it, as one step. The top-level
// layers and groups lying wholly on r go into it, keeping their order; it takes the place of the topmost of them,
// or else goes above the top-level node the chosen one is in.
func (d *Doc) AddBoard(r image.Rectangle, name string) (Board, error) {
	r, err := validBoard(r)
	if err != nil {
		return Board{}, err
	}
	if d.Root.IsSmart() {
		return Board{}, ErrSmartBoard // its layers go into groups elsewhere, where boards cannot be
	}
	m := d.Mark()
	n := d.newBoard(r, name)
	d.placeBoard(n, false)
	d.Active = n.ID
	d.EditMask = false
	d.Commit("Add artboard", m)
	return boardOf(n), nil
}

func (d *Doc) newBoard(r image.Rectangle, name string) *Node {
	return &Node{ID: d.newID(), Name: name, Visible: true, Opacity: 1, Blend: BlendPassThrough, Open: true, Children: []*Node{},
		Board: &BoardFrame{Rect: r, Fill: color.NRGBA{R: 255, G: 255, B: 255, A: 255}}}
}

// placeBoard puts a new board into the top level, taking in what lies wholly on it: in the place of the topmost of
// that, or else above the top-level node the chosen one is in, or with under at the bottom, under everything.
func (d *Doc) placeBoard(n *Node, under bool) {
	at := -1
	var kept []*Node
	for _, c := range d.Root.Children {
		reach, bounded := contentReach(c)
		if !c.IsBoard() && bounded && !reach.Empty() && boundsOf2(c).In(n.Board.Rect) {
			if at < 0 {
				at = len(kept)
			}
			n.Children = append(n.Children, c)
			continue
		}
		kept = append(kept, c)
	}
	switch {
	case under:
		at = len(kept)
	case at < 0:
		at = 0
		if top := d.topLevel(d.Active); top != nil {
			at = max(slices.Index(kept, top), 0)
		}
	}
	d.Root.Children = slices.Insert(kept, at, n)
}

// boundsOf2 is where a node shows anything, exactly: a layer's bounds, a group's children's.
func boundsOf2(n *Node) image.Rectangle {
	if !n.IsGroup() {
		return boundsOfNode(n)
	}
	var r image.Rectangle
	for _, c := range n.Children {
		r = r.Union(boundsOf2(c))
	}
	return r
}

// topLevel is the top-level node that node id is in, or is.
func (d *Doc) topLevel(id int) *Node {
	for _, c := range d.Root.Children {
		if c.ID == id || within(c, id) {
			return c
		}
	}
	return nil
}

// SetBoard changes a board's name, area and background, as resizing, renaming or recolouring it does, as one step
// called name.
func (d *Doc) SetBoard(b Board, name string) error {
	n := d.Node(b.ID)
	if n == nil || !n.IsBoard() {
		return ErrNoBoard
	}
	r, err := validBoard(b.Rect)
	if err != nil {
		return err
	}
	m := d.Mark()
	n.Name, n.Board = b.Name, &BoardFrame{Rect: r, Fill: b.Fill}
	d.Commit(name, m)
	return nil
}

// MoveBoard moves a board by (dx, dy) with everything in it, as one step.
func (d *Doc) MoveBoard(id, dx, dy int) error {
	n := d.Node(id)
	if n == nil || !n.IsBoard() {
		return ErrNoBoard
	}
	if dx == 0 && dy == 0 {
		return nil
	}
	m := d.Mark()
	moveNode(n, dx, dy)
	d.Commit("Move artboard", m)
	return nil
}

// moveNode moves a node's pixels, objects, text and masks, and its children's, by whole pixels.
func moveNode(n *Node, dx, dy int) {
	switch {
	case n.Vector != nil:
		transformVector(n, Translate(float64(dx), float64(dy)))
	case n.Pixels != nil:
		n.Pixels = n.Pixels.Moved(dx, dy)
		shiftText(n, dx, dy)
	}
	if n.Mask != nil {
		n.Mask = n.Mask.Moved(dx, dy)
	}
	if n.Clip != nil {
		n.Clip = n.Clip.Transformed(Translate(float64(dx), float64(dy)))
	}
	if b := n.Board; b != nil {
		n.Board = &BoardFrame{Rect: b.Rect.Add(image.Pt(dx, dy)), Fill: b.Fill}
	}
	if s := n.Smart; s != nil {
		// A smart object moved whole is a copy in another place, its shared layers the same.
		n.Smart = &SmartLink{ID: s.ID, Offset: s.Offset.Add(image.Pt(dx, dy)), Version: s.Version}
	}
	for _, c := range n.Children {
		if c.IsBoard() {
			continue // never inside another, but a damaged file is not followed into one
		}
		moveNode(c, dx, dy)
	}
}

// DeleteBoard removes a board, leaving what is in it in its place, as one step.
func (d *Doc) DeleteBoard(id int) error {
	n, parent, i := d.find(id)
	if n == nil || !n.IsBoard() {
		return ErrNoBoard
	}
	m := d.Mark()
	parent.Children = slices.Replace(parent.Children, i, i+1, n.Children...)
	if d.Active == id {
		if len(n.Children) > 0 {
			d.Active = n.Children[0].ID
		} else {
			d.Active = d.someNode()
		}
	}
	d.Commit("Delete artboard", m)
	return nil
}

// BoardAt is the topmost board at canvas pixel p, or 0.
func (d *Doc) BoardAt(p image.Point) int {
	for _, c := range d.Root.Children {
		if c.IsBoard() && p.In(c.Board.Rect) {
			return c.ID
		}
	}
	return 0
}

// BoardPicture is a board as it is exported: its background and what is in it, at its size.
func (d *Doc) BoardPicture(id int) (*image.RGBA, error) {
	n := d.Node(id)
	if n == nil || !n.IsBoard() {
		return nil, ErrNoBoard
	}
	img := image.NewRGBA(n.Board.Rect)
	shown := *n
	shown.Visible = true // a hidden board still exports as it looks
	d.compositeInto(img, &Node{Children: []*Node{&shown}}, img.Rect, 0)
	return img, nil
}

// drawBoardFill lays a board's background over dst within r of detail level l.
func drawBoardFill(dst *image.RGBA, b *BoardFrame, r image.Rectangle, l int) {
	if b.Fill.A == 0 {
		return
	}
	if br := levelDown(b.Rect, l).Intersect(r); !br.Empty() {
		draw.Draw(dst, br, image.NewUniform(b.Fill), image.Point{}, draw.Over)
	}
}

// boardsAtTop reports whether every board is a top-level node, as the operations that move nodes keep them.
func (d *Doc) boardsAtTop() bool {
	var deep func(g *Node) bool
	deep = func(g *Node) bool {
		for _, c := range g.Children {
			if c.IsBoard() || deep(c) {
				return true
			}
		}
		return false
	}
	for _, c := range d.Root.Children {
		if deep(c) {
			return false
		}
	}
	return true
}
