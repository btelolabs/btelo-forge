package paint

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
)

// Node is a layer, which has pixels, or a group, which has children; the first child is the top one.
type Node struct {
	ID        int
	Name      string
	Visible   bool
	Opacity   float64 // 0 to 1
	Blend     Blend
	LockAlpha bool // painting keeps the layer's transparency, as Photoshop's lock on transparent pixels
	// A pixel layer's pixels, premultiplied, on the unbounded canvas; nil for a group, a vector or an adjustment layer.
	Pixels *Raster
	// A vector layer's object; nil for other nodes. Never changed in place.
	Vector *Vector
	// An adjustment layer's adjustment, applied to what is under it; nil for other nodes. Never changed in place.
	Adjust *Adjustment
	// The layer's effects (shadows, glows, stroke, overlay); nil for none. Never changed in place.
	Style *LayerStyle
	// The layer mask: white shows the layer, black hides it; beyond what was painted on it, it is its fill. Off
	// turns it off without removing it.
	Mask     *Mask
	MaskOff  bool
	Text     *Text   // a text layer's words, which its pixels are drawn from; nil for other layers. Never changed in place.
	Children []*Node // a group's layers and groups, top first
	Open     bool    // a group shows its children in the layers panel
}

// IsGroup reports whether n is a group.
func (n *Node) IsGroup() bool { return n.Pixels == nil && n.Vector == nil && n.Adjust == nil }

// Doc is a picture made of layers on an infinite canvas.
type Doc struct {
	// Frame is the picture's frame, which export, crop and Fit use; it is empty for a canvas without one, which
	// exports what is on it.
	Frame image.Rectangle
	// Paper is white under the layers, as a new canvas has; without it the picture is transparent where nothing is.
	Paper bool
	// Boards are the artboards, fixed-size areas of the canvas each exported as a picture of its own, bottom first.
	Boards    []Board
	Root      *Node // the top-level group; it is not shown as a layer
	Active    int   // the node being painted on or changed
	Selection *Mask // 255 where selected; nil when nothing is
	EditMask  bool  // painting and filling go to the chosen layer's mask instead of its pixels
	// View is the part of the canvas on screen. It bounds what would otherwise reach without end, such as a fill
	// with nothing selected on a canvas without a frame.
	View image.Rectangle

	nextID        int
	history       history
	stroke        *Stroke
	lastSelection *Mask // for Reselect
}

// Errors the layer operations can return.
var (
	ErrNoLayer      = errors.New("no layer is chosen")
	ErrGroup        = errors.New("this works on a layer, not a group")
	ErrNothingBelow = errors.New("there is no layer below to merge into")
	ErrLastLayer    = errors.New("a picture keeps at least one layer")
	ErrNoSelection  = errors.New("nothing is selected")
	ErrTooBig       = errors.New("the picture is too large")
)

// maxSide bounds the sides of a picture made whole at once, as an export or a resample is, which keeps it within a
// few hundred megabytes. The canvas itself has no bounds.
const maxSide = 16384

// New is an empty canvas without a frame: one empty layer called name, on white paper.
func New(name string) *Doc {
	d := &Doc{Paper: true}
	d.Root = &Node{ID: d.newID(), Visible: true, Opacity: 1, Blend: BlendPassThrough, Open: true}
	l := d.newLayer(name)
	d.Root.Children = []*Node{l}
	d.Active = l.ID
	return d
}

// NewFramed is a w×h canvas framed at the origin, with one layer called name filled with bg (left empty when bg's
// alpha is 0) and no paper, as a canvas drawn over a video frame or a screen is.
func NewFramed(w, h int, name string, bg color.NRGBA) (*Doc, error) {
	if w <= 0 || h <= 0 || w > maxSide || h > maxSide {
		return nil, fmt.Errorf("%w: %d×%d", ErrTooBig, w, h)
	}
	d := New(name)
	d.Paper, d.Frame = false, image.Rect(0, 0, w, h)
	if bg.A > 0 {
		img := image.NewRGBA(d.Frame)
		draw.Draw(img, img.Rect, image.NewUniform(bg), image.Point{}, draw.Src)
		d.Root.Children[0].Pixels.Write(img)
	}
	return d, nil
}

// FromImage is a canvas framed to img, such as a screenshot, whose one layer, called name, is img.
func FromImage(img image.Image, name string) (*Doc, error) {
	b := img.Bounds()
	d, err := NewFramed(b.Dx(), b.Dy(), name, color.NRGBA{})
	if err != nil {
		return nil, err
	}
	pix := image.NewRGBA(d.Frame)
	draw.Draw(pix, pix.Rect, img, b.Min, draw.Src)
	d.Root.Children[0].Pixels.Write(pix)
	return d, nil
}

func (d *Doc) newID() int {
	d.nextID++
	return d.nextID
}

func (d *Doc) newLayer(name string) *Node {
	return &Node{ID: d.newID(), Name: name, Visible: true, Opacity: 1, Pixels: NewRaster()}
}

// Node finds a node by id, or nil.
func (d *Doc) Node(id int) *Node {
	n, _, _ := d.find(id)
	return n
}

// find returns the node with id, its parent and its index in the parent.
func (d *Doc) find(id int) (n, parent *Node, index int) {
	var walk func(g *Node) bool
	walk = func(g *Node) bool {
		for i, c := range g.Children {
			if c.ID == id {
				n, parent, index = c, g, i
				return true
			}
			if c.IsGroup() && walk(c) {
				return true
			}
		}
		return false
	}
	walk(d.Root)
	return n, parent, index
}

// Layers lists every layer, top first, for counting and painting checks.
func (d *Doc) Layers() []*Node {
	var out []*Node
	var walk func(g *Node)
	walk = func(g *Node) {
		for _, c := range g.Children {
			if c.IsGroup() {
				walk(c)
			} else {
				out = append(out, c)
			}
		}
	}
	walk(d.Root)
	return out
}

// ActiveLayer is the chosen node when it is a layer.
func (d *Doc) ActiveLayer() (*Node, error) {
	n := d.Node(d.Active)
	switch {
	case n == nil:
		return nil, ErrNoLayer
	case n.IsGroup():
		return nil, ErrGroup
	}
	return n, nil
}

// Composite is the visible picture within r, over transparency.
func (d *Doc) Composite(r image.Rectangle) *image.RGBA {
	out := image.NewRGBA(r)
	d.compositeInto(out, d.Root, r, 0)
	return out
}

// Bounds is where anything is painted on the canvas, hidden layers included.
func (d *Doc) Bounds() image.Rectangle {
	var b image.Rectangle
	for _, l := range d.Layers() {
		b = b.Union(boundsOfNode(l))
	}
	return b
}

// Reach is a quick, generous bound of what is painted: where the layers have tiles.
func (d *Doc) Reach() image.Rectangle {
	var b image.Rectangle
	for _, l := range d.Layers() {
		b = b.Union(reachOfNode(l))
	}
	return b
}

// Extent is the area the picture is: its frame, or else what is painted on it.
func (d *Doc) Extent() image.Rectangle {
	if !d.Frame.Empty() {
		return d.Frame
	}
	return d.Bounds()
}

// compositeInto draws group g's visible children, bottom first, onto dst within r, reading the layers at detail
// level l (r is then in that level's pixels). A pass-through group at full opacity lets its layers blend with what
// is under it directly; any other group is composed on its own first and then blended as one.
func (d *Doc) compositeInto(dst *image.RGBA, g *Node, r image.Rectangle, l int) {
	for i := len(g.Children) - 1; i >= 0; i-- {
		c := g.Children[i]
		if !c.Visible || c.Opacity <= 0 {
			continue
		}
		mask := c.activeMask()
		var maskPix *image.Alpha
		var maskFill uint8 = 255
		if mask != nil {
			maskFill = mask.Fill()
			if maskFill == 0 && mask.TileBounds().Intersect(levelUp(r, l)).Empty() {
				continue // hidden here
			}
			maskPix = mask.ReadLevel(l, r)
		}
		if c.Adjust != nil {
			// What is under it so far, adjusted, laid over it through the layer's opacity, blend mode and mask.
			blendPixels(dst, c.Adjust.Filter().apply(dst, r), r, c.Opacity, c.Blend, maskPix, maskFill)
			continue
		}
		if !c.IsGroup() {
			if reachOfNode(c).Intersect(levelUp(r, l)).Empty() {
				continue // nothing here
			}
			if !c.Style.empty() {
				d.drawStyled(dst, c, r, l)
				continue
			}
			blendPixels(dst, readLayer(c, r, l), r, c.Opacity, c.Blend, maskPix, maskFill)
			continue
		}
		if c.Blend == BlendPassThrough && c.Opacity >= 1 && mask == nil {
			d.compositeInto(dst, c, r, l)
			continue
		}
		tmp := image.NewRGBA(r)
		d.compositeInto(tmp, c, r, l)
		mode := c.Blend
		if mode == BlendPassThrough {
			mode = BlendNormal
		}
		blendPixels(dst, tmp, r, c.Opacity, mode, maskPix, maskFill)
	}
}

// levelUp is the canvas area of the area r of detail level l.
func levelUp(r image.Rectangle, l int) image.Rectangle {
	return image.Rect(r.Min.X<<l, r.Min.Y<<l, r.Max.X<<l, r.Max.Y<<l)
}

// Render composites the area r of detail level l (the canvas halved l times) into dst, on white when the canvas
// has paper: what the editor shows, and only what it shows.
func (d *Doc) Render(dst *image.RGBA, r image.Rectangle, l int) {
	if d.Paper {
		draw.Draw(dst, r, image.White, image.Point{}, draw.Src)
	}
	d.drawBoards(dst, r, l)
	d.compositeInto(dst, d.Root, r, l)
}

// activeMask is the mask that applies, nil when there is none or it is turned off.
func (n *Node) activeMask() *Mask {
	if n.Mask == nil || n.MaskOff {
		return nil
	}
	return n.Mask
}

// over composites src onto dst within r at an opacity; both are premultiplied.
func over(dst, src *image.RGBA, r image.Rectangle, opacity float64) {
	blendPixels(dst, src, r, opacity, BlendNormal, nil, 255)
}

// mul8 is a·b/255, rounded.
func mul8(a, b uint32) uint32 {
	t := a*b + 128
	return (t + t>>8) >> 8
}
