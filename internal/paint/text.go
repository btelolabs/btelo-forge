package paint

import (
	"errors"
	"image"
	"image/color"
	"strings"
)

// TextAlign is how a text layer's lines line up with its anchor.
type TextAlign int

const (
	AlignLeft TextAlign = iota
	AlignCenter
	AlignRight
)

// Text is what a text layer says and how it looks, kept so it can be edited again, as Photoshop's type layers are.
// The layer's pixels are drawn from it by the interface, which has the fonts; painting on the layer turns it into
// plain pixels.
type Text struct {
	Text  string      `json:"text"`
	X     float64     `json:"x"` // the anchor: the first line's top, at its left, middle or right by Align
	Y     float64     `json:"y"`
	Size  float64     `json:"size"` // in picture pixels
	Bold  bool        `json:"bold,omitempty"`
	Color color.NRGBA `json:"color"`
	Align TextAlign   `json:"align,omitempty"`
}

// Lines are the text's lines.
func (t Text) Lines() []string { return strings.Split(t.Text, "\n") }

// LineHeight is the distance from one line to the next, Photoshop's automatic leading.
func (t Text) LineHeight() float64 { return t.Size * 1.2 }

// ErrNotText means the chosen layer is not a text layer.
var ErrNotText = errors.New("the chosen layer is not a text layer")

// dropText turns a text layer whose pixels are about to change into plain pixels, as Photoshop rasterizes a type
// layer before painting on it. It is called after the change's Mark, so undo brings the text back.
func (d *Doc) dropText(l *Node) {
	if l.Text != nil && !(d.EditMask && l.Mask != nil) {
		l.Text = nil
	}
}

// RasterizeText turns the chosen text layer into plain pixels, as Layer › Rasterize › Type does.
func (d *Doc) RasterizeText() error {
	l, err := d.ActiveLayer()
	if err != nil {
		return err
	}
	if l.Text == nil {
		return ErrNotText
	}
	m := d.Mark()
	l.Text = nil
	d.Commit("Rasterize type", m)
	return nil
}

// TextSession is a text layer being typed: a new one, or one being edited again. What is typed shows at once and
// is recorded as one step when the session is committed.
type TextSession struct {
	d       *Doc
	l       *Node
	mark    Mark
	created bool
	named   bool // the layer is named after its text, so the name follows it
	shown   image.Rectangle
}

// textName is the name Photoshop gives a text layer: its first line.
func textName(s string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(first); len(r) > 40 {
		first = string(r[:40])
	}
	return first
}

// BeginText starts a new text layer above the chosen one, or, with a text layer's id, edits that one again.
func (d *Doc) BeginText(id int) (*TextSession, error) {
	m := d.Mark()
	if id != 0 {
		l := d.Node(id)
		if l == nil || l.Text == nil {
			return nil, ErrNotText
		}
		d.Active, d.EditMask = id, false
		return &TextSession{d: d, l: l, mark: m, named: l.Name == textName(l.Text.Text), shown: l.Pixels.TileBounds()}, nil
	}
	l := &Node{ID: d.newID(), Visible: true, Opacity: 1, Pixels: NewRaster(), Text: &Text{}}
	d.insertAbove(l)
	d.EditMask = false
	return &TextSession{d: d, l: l, mark: m, created: true, named: true}, nil
}

// Layer is the text layer being typed.
func (t *TextSession) Layer() *Node { return t.l }

// Set shows t with its pixels, drawn from it; it returns the picture's area that changed.
func (t *TextSession) Set(txt Text, pix *image.RGBA) image.Rectangle {
	t.l.Text, t.l.Pixels = &txt, RasterFrom(pix)
	changed := t.shown.Union(pix.Rect)
	t.shown = pix.Rect
	return changed
}

// Commit records the text as one step, naming the layer after its first line unless it was renamed, as Photoshop
// does; a new layer left empty is removed instead. It returns the picture's area that changed.
func (t *TextSession) Commit() image.Rectangle {
	if t.created && strings.TrimSpace(t.l.Text.Text) == "" {
		return t.Cancel()
	}
	if t.named {
		t.l.Name = textName(t.l.Text.Text)
	}
	name := "Edit text"
	if t.created {
		name = "Text"
	}
	t.d.Commit(name, t.mark)
	return t.shown
}

// Cancel puts everything back as it was before the session.
func (t *TextSession) Cancel() image.Rectangle {
	t.d.restore(t.mark.s)
	return t.shown
}

// TextAt is the topmost visible text layer whose pixels cover (x, y), for the text tool to edit, or nil.
func (d *Doc) TextAt(x, y int) *Node {
	for _, l := range d.Layers() {
		if l.Text == nil || !l.Visible || !(image.Point{x, y}).In(l.Pixels.Bounds()) {
			continue
		}
		return l
	}
	return nil
}

// shiftText moves a text layer's anchor along with its pixels.
func shiftText(l *Node, dx, dy int) {
	if l.Text != nil && (dx != 0 || dy != 0) {
		t := *l.Text
		t.X, t.Y = t.X+float64(dx), t.Y+float64(dy)
		l.Text = &t
	}
}
