package paint

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"slices"
)

// Board is an artboard: a named area of the infinite canvas with a fixed size and its own background, exported as a
// picture of its own, as Affinity Designer's artboards are. Layers are not inside a board; what lies on it is what
// it shows.
type Board struct {
	ID   int
	Name string
	Rect image.Rectangle
	Fill color.NRGBA // the background; transparent leaves the board clear
}

// ErrNoBoard means an artboard asked for is not in the picture.
var ErrNoBoard = errors.New("there is no such artboard")

// minBoard is the smallest side a board can have.
const minBoard = 1

// Board finds a board by id.
func (d *Doc) Board(id int) (Board, bool) {
	for _, b := range d.Boards {
		if b.ID == id {
			return b, true
		}
	}
	return Board{}, false
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

// AddBoard adds a board over r called name, with a white background, as one step.
func (d *Doc) AddBoard(r image.Rectangle, name string) (Board, error) {
	r, err := validBoard(r)
	if err != nil {
		return Board{}, err
	}
	m := d.Mark()
	b := Board{ID: d.newID(), Name: name, Rect: r, Fill: color.NRGBA{R: 255, G: 255, B: 255, A: 255}}
	d.Boards = append(slices.Clone(d.Boards), b)
	d.Commit("Add artboard", m)
	return b, nil
}

// SetBoard replaces a board, as resizing, renaming or recolouring it does, as one step called name.
func (d *Doc) SetBoard(b Board, name string) error {
	i := slices.IndexFunc(d.Boards, func(x Board) bool { return x.ID == b.ID })
	if i < 0 {
		return ErrNoBoard
	}
	r, err := validBoard(b.Rect)
	if err != nil {
		return err
	}
	b.Rect = r
	m := d.Mark()
	d.Boards = slices.Clone(d.Boards)
	d.Boards[i] = b
	d.Commit(name, m)
	return nil
}

// MoveBoard moves a board by (dx, dy) with the layers that lie wholly on it, as dragging an artboard carries what
// is drawn on it, as one step.
func (d *Doc) MoveBoard(id, dx, dy int) error {
	i := slices.IndexFunc(d.Boards, func(x Board) bool { return x.ID == id })
	if i < 0 {
		return ErrNoBoard
	}
	if dx == 0 && dy == 0 {
		return nil
	}
	m := d.Mark()
	on := d.Boards[i].Rect
	for _, l := range d.Layers() {
		if b := boundsOfNode(l); b.Empty() || !b.In(on) {
			continue
		}
		switch {
		case l.Vector != nil:
			transformVector(l, Translate(float64(dx), float64(dy)))
		case l.Pixels != nil:
			l.Pixels = l.Pixels.Snapshot()
			l.Pixels.Move(dx, dy)
			shiftText(l, dx, dy)
		}
		if l.Mask != nil {
			l.Mask = l.Mask.Snapshot()
			l.Mask.Move(dx, dy)
		}
	}
	d.Boards = slices.Clone(d.Boards)
	d.Boards[i].Rect = on.Add(image.Pt(dx, dy))
	d.Commit("Move artboard", m)
	return nil
}

// DeleteBoard removes a board, leaving what is drawn on it, as one step.
func (d *Doc) DeleteBoard(id int) error {
	i := slices.IndexFunc(d.Boards, func(x Board) bool { return x.ID == id })
	if i < 0 {
		return ErrNoBoard
	}
	m := d.Mark()
	d.Boards = slices.Delete(slices.Clone(d.Boards), i, i+1)
	d.Commit("Delete artboard", m)
	return nil
}

// BoardAt is the topmost board at canvas pixel p, or 0.
func (d *Doc) BoardAt(p image.Point) int {
	for i := len(d.Boards) - 1; i >= 0; i-- {
		if p.In(d.Boards[i].Rect) {
			return d.Boards[i].ID
		}
	}
	return 0
}

// BoardPicture is a board as it is exported: its background and what is drawn on it, at its size.
func (d *Doc) BoardPicture(id int) (*image.RGBA, error) {
	b, ok := d.Board(id)
	if !ok {
		return nil, ErrNoBoard
	}
	img := image.NewRGBA(b.Rect)
	d.Render(img, b.Rect, 0)
	return img, nil
}

// drawBoards fills the boards' backgrounds within r of detail level l, under the layers.
func (d *Doc) drawBoards(dst *image.RGBA, r image.Rectangle, l int) {
	for _, b := range d.Boards {
		if b.Fill.A == 0 {
			continue
		}
		br := image.Rect(b.Rect.Min.X>>l, b.Rect.Min.Y>>l, (b.Rect.Max.X+(1<<l)-1)>>l, (b.Rect.Max.Y+(1<<l)-1)>>l).Intersect(r)
		if !br.Empty() {
			draw.Draw(dst, br, image.NewUniform(b.Fill), image.Point{}, draw.Over)
		}
	}
}
