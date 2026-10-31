package paint

import (
	"crypto/rand"
	"errors"
	"image"
	"image/draw"
	"io"
	"reflect"
	"slices"
	"strings"
	"time"
)

// Smart objects are groups whose layers are shared, as Photoshop's linked smart objects are: every copy of one has
// the same link id, and a change made in any copy's layers is made in all of them, in the same step. Each copy has
// a place of its own, its offset from where the shared layers lie, and its own name, opacity, blend mode, mask and
// visibility. The shared layers also go beyond one picture: a change is offered, through SmartChanges, to be kept
// and taken into the other pictures that hold copies (ApplySmart), and into anything else made from them. Breaking
// the link gives a copy an id of its own.

// SmartLink is what makes a group a smart object; it is a value never changed in place.
type SmartLink struct {
	ID      string      // the link id: copies with the same one share their layers
	Offset  image.Point // where this copy is: the shared layers moved by this much
	Version int64       // when the shared layers this copy shows were made, in Unix nanoseconds
}

// SmartContent is the shared layers of the smart objects with one link: in the place they have in a copy with no
// offset, frozen so they can be saved or drawn on another goroutine, and when they were made.
type SmartContent struct {
	ID      string
	Name    string
	Version int64
	Layers  []*Node // top first
}

// Errors the smart object operations can return.
var (
	ErrNotSmart   = errors.New("the chosen layer is not a smart object")
	ErrSmartBoard = errors.New("an artboard cannot be a smart object, nor be in one")
)

// IsSmart reports whether n is a smart object.
func (n *Node) IsSmart() bool { return n.Smart != nil }

// A link id is nine letters and digits, its first a letter, written in capitals; letter case does not matter in
// one read or typed: 36^8 × 26, about 7×10^13 ids, so two smart objects made apart never share one.
const (
	linkIDLength = 9
	linkLetters  = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	linkSymbols  = linkLetters + "0123456789"
)

// newLinkID is a fresh link id, each character drawn evenly from what it may be.
func newLinkID() string {
	out := make([]byte, linkIDLength)
	for i := range out {
		set := linkSymbols
		if i == 0 {
			set = linkLetters
		}
		out[i] = set[randBelow(len(set))]
	}
	return string(out)
}

// randBelow is an even random number from 0 to n−1, from the system's random source.
func randBelow(n int) int {
	limit := 256 - 256%n // bytes at or past it would favour the low numbers
	var b [1]byte
	for {
		rand.Read(b[:])
		if int(b[0]) < limit {
			return int(b[0]) % n
		}
	}
}

// NormalLinkID is a link id as it is written, in capitals, and whether it is one: nine letters and digits, its first
// a letter, in either case.
func NormalLinkID(id string) (string, bool) {
	id = strings.ToUpper(strings.TrimSpace(id))
	return id, validLinkID(id)
}

// validLinkID reports whether a link id is written as one is: nine capitals and digits, the first a capital. It is
// safe as a file name.
func validLinkID(id string) bool {
	if len(id) != linkIDLength {
		return false
	}
	for i := range len(id) {
		c := id[i]
		if !(c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// smartShared is what a picture knows of a link's shared layers: what its copies show, and since when.
type smartShared struct {
	layers  []*Node // canonical: as a copy with no offset holds them
	version int64
	name    string
}

// nextVersion is a version later than any before it, now.
func (d *Doc) nextVersion(after int64) int64 {
	return max(time.Now().UnixNano(), after+1)
}

// ConvertToSmart makes the chosen group a smart object with a link id of its own, as one step; a layer is put into a
// group of its own first. It returns the link id.
func (d *Doc) ConvertToSmart(groupName string) (string, error) {
	n, parent, i := d.find(d.Active)
	switch {
	case n == nil:
		return "", ErrNoLayer
	case n.IsBoard():
		return "", ErrSmartBoard
	case n.IsSmart():
		return n.Smart.ID, nil
	}
	m := d.Mark()
	if !n.IsGroup() {
		g := &Node{ID: d.newID(), Name: groupName, Visible: true, Opacity: 1, Blend: BlendPassThrough, Children: []*Node{n}, Open: false}
		parent.Children[i] = g
		n = g
	}
	id := newLinkID()
	n.Smart = &SmartLink{ID: id, Version: d.nextVersion(0)}
	d.Active, d.EditMask = n.ID, false
	d.noteShared(n, true)
	d.Commit("Convert to smart object", m)
	return id, nil
}

// UnlinkSmart gives the chosen smart object a link id of its own, so it no longer changes with its copies, as one
// step; it returns the new id.
func (d *Doc) UnlinkSmart() (string, error) {
	n := d.Node(d.Active)
	if n == nil || !n.IsSmart() {
		return "", ErrNotSmart
	}
	m := d.Mark()
	id := newLinkID()
	n.Smart = &SmartLink{ID: id, Offset: n.Smart.Offset, Version: d.nextVersion(n.Smart.Version)}
	d.noteShared(n, true)
	d.Commit("Break the link", m)
	return id, nil
}

// ReleaseSmart makes the chosen smart object a plain group again, keeping its layers, as one step.
func (d *Doc) ReleaseSmart() error {
	n := d.Node(d.Active)
	if n == nil || !n.IsSmart() {
		return ErrNotSmart
	}
	m := d.Mark()
	n.Smart, n.Filters = nil, nil
	d.Commit("Convert to group", m)
	return nil
}

// SmartCopies are the smart objects with link id in the picture, in the order of the layers; a smart object's own
// file is its first.
func (d *Doc) SmartCopies(id string) []*Node {
	var out []*Node
	if d.Root.IsSmart() && d.Root.Smart.ID == id {
		out = append(out, d.Root)
	}
	var walk func(g *Node)
	walk = func(g *Node) {
		for _, c := range g.Children {
			if c.IsSmart() && c.Smart.ID == id {
				out = append(out, c)
			}
			walk(c)
		}
	}
	walk(d.Root)
	return out
}

// smartNodes are every smart object in the picture, in the order of the layers: a smart object's own file first,
// whose layers are the shared ones.
func (d *Doc) smartNodes() []*Node {
	var out []*Node
	if d.Root.IsSmart() {
		out = append(out, d.Root)
	}
	var walk func(g *Node)
	walk = func(g *Node) {
		for _, c := range g.Children {
			if c.IsSmart() {
				out = append(out, c)
			}
			walk(c)
		}
	}
	walk(d.Root)
	return out
}

// canonical is what a smart object's layers would be with no offset: a frozen copy of them moved back.
func canonical(n *Node) []*Node {
	out := make([]*Node, len(n.Children))
	for i, c := range n.Children {
		cc := copyTree(c)
		moveNode(cc, -n.Smart.Offset.X, -n.Smart.Offset.Y)
		out[i] = cc
	}
	return out
}

// noteShared takes n's layers as what its link shares now; with changed, the change is offered to the rest.
func (d *Doc) noteShared(n *Node, changed bool) {
	if d.smart == nil {
		d.smart = map[string]*smartShared{}
	}
	s := &smartShared{layers: canonical(n), version: n.Smart.Version, name: n.Name}
	d.smart[n.Smart.ID] = s
	if changed {
		d.smartOut = slices.DeleteFunc(d.smartOut, func(c SmartContent) bool { return c.ID == n.Smart.ID })
		d.smartOut = append(d.smartOut, SmartContent{ID: n.Smart.ID, Name: s.name, Version: s.version, Layers: s.layers})
	}
}

// syncSmart makes every copy of a smart object show the same layers: a copy whose layers changed since they were
// shared gives them to the others, the one chosen (or holding the chosen layer) first when several changed. It
// runs as each step is recorded, so the copies change in the same step, and after undo and redo.
func (d *Doc) syncSmart() {
	// A copy inside another's layers changes those too, so it goes round until nothing more changes.
	for range 8 {
		if !d.syncSmartOnce() {
			return
		}
	}
}

func (d *Doc) syncSmartOnce() bool {
	nodes := d.smartNodes()
	if len(nodes) == 0 {
		return false
	}
	if d.smart == nil {
		d.smart = map[string]*smartShared{}
	}
	active := d.topSmartOf(d.Active)
	byID := map[string][]*Node{}
	var order []string
	for _, n := range nodes {
		if _, ok := byID[n.Smart.ID]; !ok {
			order = append(order, n.Smart.ID)
		}
		byID[n.Smart.ID] = append(byID[n.Smart.ID], n)
	}
	moved := false
	for _, id := range order {
		copies := byID[id]
		s := d.smart[id]
		if s == nil {
			// First seen, as a loaded picture's are: the newest copy's layers are what is shared.
			newest := copies[0]
			for _, c := range copies[1:] {
				if c.Smart.Version > newest.Smart.Version {
					newest = c
				}
			}
			d.noteShared(newest, false)
			s = d.smart[id]
		}
		var source *Node
		for _, c := range copies {
			if !sameLayers(s.layers, c.Children, c.Smart.Offset) {
				if source == nil || c == active {
					source = c
				}
			}
		}
		if source == nil {
			continue
		}
		version := d.nextVersion(s.version)
		source.Smart = &SmartLink{ID: id, Offset: source.Smart.Offset, Version: version}
		d.noteShared(source, true)
		for _, c := range copies {
			switch {
			case c == source:
			case sameLayers(d.smart[id].layers, c.Children, c.Smart.Offset):
				c.Smart = &SmartLink{ID: id, Offset: c.Smart.Offset, Version: version} // as after undo: only newer
			default:
				d.showShared(c, d.smart[id])
			}
		}
		moved = true
	}
	return moved
}

// settleSmart makes a loaded picture's smart objects share their layers again: the file holds each copy's own, read
// apart, so every copy shows the newest copy's, sharing its pixels.
func (d *Doc) settleSmart() {
	d.smart = nil
	for _, id := range d.SmartLinks() {
		copies := d.SmartCopies(id)
		newest := copies[0]
		for _, c := range copies[1:] {
			if c.Smart.Version > newest.Smart.Version {
				newest = c
			}
		}
		d.noteShared(newest, false)
		for _, c := range d.SmartCopies(id) {
			if c != newest {
				d.showShared(c, d.smart[id])
			}
		}
	}
}

// topSmartOf is the outermost smart object that node id is in, or is, or nil.
func (d *Doc) topSmartOf(id int) *Node {
	var found *Node
	var top *Node
	if d.Root.IsSmart() {
		top = d.Root // in a smart object's own file every layer is in it
	}
	var walk func(g *Node, outer *Node) bool
	walk = func(g *Node, outer *Node) bool {
		for _, c := range g.Children {
			o := outer
			if o == nil && c.IsSmart() {
				o = c
			}
			if c.ID == id {
				found = o
				return true
			}
			if walk(c, o) {
				return true
			}
		}
		return false
	}
	if !walk(d.Root, top) {
		return top
	}
	return found
}

// showShared gives a copy the shared layers, at its offset, with ids of its own.
func (d *Doc) showShared(c *Node, s *smartShared) {
	c.Children = d.placed(s.layers, c.Smart.Offset)
	c.Smart = &SmartLink{ID: c.Smart.ID, Offset: c.Smart.Offset, Version: s.version}
}

// placed is a fresh copy of layers, with new ids, moved by off.
func (d *Doc) placed(layers []*Node, off image.Point) []*Node {
	out := make([]*Node, len(layers))
	for i, l := range layers {
		c := d.renumbered(l)
		moveNode(c, off.X, off.Y)
		out[i] = c
	}
	return out
}

// renumbered is a copy of a tree sharing its pixels and their levels, with ids new to the picture.
func (d *Doc) renumbered(n *Node) *Node {
	c := copyTree(n)
	var walk func(x *Node)
	walk = func(x *Node) {
		x.ID = d.newID()
		for _, ch := range x.Children {
			walk(ch)
		}
	}
	walk(c)
	return c
}

// sameLayers reports whether layers b are layers a moved by off, whatever their ids: what a copy shows is what is
// shared.
func sameLayers(a, b []*Node, off image.Point) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameShifted(a[i], b[i], off) {
			return false
		}
	}
	return true
}

// sameShifted reports whether node b is node a moved by off: the same settings, pixels shared tile for tile at the
// moved place, objects and text moved, and children the same in turn.
func sameShifted(a, b *Node, off image.Point) bool {
	if a.Name != b.Name || a.Visible != b.Visible || a.Opacity != b.Opacity || a.Blend != b.Blend || a.LockAlpha != b.LockAlpha ||
		a.MaskOff != b.MaskOff || a.Open != b.Open || a.Adjust != b.Adjust && !reflect.DeepEqual(a.Adjust, b.Adjust) ||
		a.Style != b.Style && !reflect.DeepEqual(a.Style, b.Style) ||
		!samePlaneShifted(rasterPlane(a.Pixels), rasterPlane(b.Pixels), off) || !samePlaneShifted(maskPlane(a.Mask), maskPlane(b.Mask), off) ||
		!sameVectorShifted(a.Vector, b.Vector, off) || !sameVectorShifted(a.Clip, b.Clip, off) ||
		(a.Text == nil) != (b.Text == nil) || (a.Smart == nil) != (b.Smart == nil) || len(a.Children) != len(b.Children) {
		return false
	}
	if a.Text != nil {
		t := *a.Text
		t.X, t.Y = t.X+float64(off.X), t.Y+float64(off.Y)
		if t != *b.Text {
			return false
		}
	}
	if a.Smart != nil && (a.Smart.ID != b.Smart.ID || a.Smart.Offset.Add(off) != b.Smart.Offset) {
		return false
	}
	for i := range a.Children {
		if !sameShifted(a.Children[i], b.Children[i], off) {
			return false
		}
	}
	return true
}

func rasterPlane(r *Raster) *plane {
	if r == nil {
		return nil
	}
	return &r.p
}

func maskPlane(m *Mask) *plane {
	if m == nil {
		return nil
	}
	return &m.p
}

// samePlaneShifted reports whether plane b is plane a moved by off, sharing its tiles.
func samePlaneShifted(a, b *plane, off image.Point) bool {
	if a == nil || b == nil {
		return a == b
	}
	moved := *a
	moved.off = a.off.Add(off)
	return moved.same(b)
}

// sameVectorShifted reports whether object b is object a moved by off.
func sameVectorShifted(a, b *Vector, off image.Point) bool {
	if a == nil || b == nil {
		return a == b
	}
	if off == (image.Point{}) && a == b {
		return true
	}
	moved := a.Transformed(Translate(float64(off.X), float64(off.Y)))
	return reflect.DeepEqual(*moved, *b)
}

// SmartChanges are the shared layers changed in this picture since they were last asked for, to keep and to take
// into the other pictures; asking clears them.
func (d *Doc) SmartChanges() []SmartContent {
	out := d.smartOut
	d.smartOut = nil
	return out
}

// SmartContentOf is the shared layers of link id as this picture holds them, when it holds a copy.
func (d *Doc) SmartContentOf(id string) (SmartContent, bool) {
	d.syncSmart()
	s := d.smart[id]
	if s == nil {
		return SmartContent{}, false
	}
	return SmartContent{ID: id, Name: s.name, Version: s.version, Layers: s.layers}, true
}

// SourceOrCopy reports whether the picture is link id's own file or holds a copy of it.
func (d *Doc) SourceOrCopy(id string) bool { return len(d.SmartCopies(id)) > 0 }

// SmartLinks are the link ids of the smart objects in the picture.
func (d *Doc) SmartLinks() []string {
	var out []string
	for _, n := range d.smartNodes() {
		if !slices.Contains(out, n.Smart.ID) {
			out = append(out, n.Smart.ID)
		}
	}
	return out
}

// ApplySmart takes shared layers kept elsewhere into every copy of their link here that shows other ones, as one
// step; it reports whether any copy changed. What is kept is preferred over what the picture holds, which is its
// copy of them. Taking them is not itself offered as a change.
func (d *Doc) ApplySmart(c SmartContent) bool {
	copies := d.SmartCopies(c.ID)
	stale := false
	for _, n := range copies {
		stale = stale || n.Smart.Version != c.Version || !sameLayers(c.Layers, n.Children, n.Smart.Offset)
	}
	if !stale {
		return false
	}
	m := d.Mark()
	if d.smart == nil {
		d.smart = map[string]*smartShared{}
	}
	// What is taken supersedes a change here not yet asked for, as a copy pasted from an older clipboard is.
	d.smartOut = slices.DeleteFunc(d.smartOut, func(o SmartContent) bool { return o.ID == c.ID })
	s := &smartShared{layers: c.Layers, version: c.Version, name: c.Name}
	d.smart[c.ID] = s
	for _, n := range copies {
		d.showShared(n, s)
	}
	if len(d.Root.Children) == 0 {
		d.Root.Children = []*Node{d.newLayer("Layer 1")} // a smart object's file keeps a layer
	}
	if n := d.Node(d.Active); n == nil {
		d.Active = d.someNode() // the chosen layer was in a copy, whose layers are new
	}
	d.Commit("Update smart object", m)
	return true
}

// ---- Copying layers between pictures

// CopyNode is a frozen copy of the node id with its layers, for pasting into this picture or another, and the area
// of the artboard it is in, empty when it is in none or is a board itself.
func (d *Doc) CopyNode(id int) (*Node, image.Rectangle, error) {
	n := d.Node(id)
	if n == nil {
		return nil, image.Rectangle{}, ErrNoLayer
	}
	var from image.Rectangle
	if b, ok := d.Board(d.BoardOf(id)); ok && !n.IsBoard() {
		from = b.Rect
	}
	return snapshotTree(n), from, nil
}

// PasteNode puts a copy of n, from CopyNode, above the chosen node with ids new to the picture, and chooses it, as
// one step; an artboard goes to the top level. from is the area of the artboard n was copied from, empty when none:
// pasted while another artboard, or something in one, is chosen, n goes into that board at the place it had in its
// own, as Photoshop pastes between artboards; else where it was on the canvas. A smart object keeps its link, so it
// changes with its copies here and elsewhere.
func (d *Doc) PasteNode(n *Node, from image.Rectangle) *Node {
	m := d.Mark()
	c := d.renumbered(n)
	if d.Root.IsSmart() {
		unboard(c) // a smart object's layers hold no artboards: they become groups
	}
	switch {
	case c.IsBoard() || d.boardsInside(c):
		d.Root.Children = slices.Insert(d.Root.Children, 0, c)
		d.Active = c.ID
	default:
		if to, ok := d.Board(d.BoardOf(d.Active)); ok && !from.Empty() {
			moveNode(c, to.Rect.Min.X-from.Min.X, to.Rect.Min.Y-from.Min.Y)
		}
		if b := d.Node(d.Active); b != nil && b.IsBoard() {
			b.Children = slices.Insert(b.Children, 0, c) // into the chosen board, open or not
			d.Active = c.ID
		} else {
			d.insertAbove(c)
		}
	}
	d.EditMask = false
	d.Commit("Paste", m)
	return c
}

// unboard makes every artboard in a tree a plain group.
func unboard(n *Node) {
	n.Board = nil
	for _, c := range n.Children {
		unboard(c)
	}
}

// boardsInside reports whether a tree holds an artboard below its top.
func (d *Doc) boardsInside(n *Node) bool {
	for _, c := range n.Children {
		if c.IsBoard() || d.boardsInside(c) {
			return true
		}
	}
	return false
}

// NodePicture is how a node looks on its own, cut to what it shows; nil when it shows nothing.
func NodePicture(n *Node) *image.RGBA {
	d := &Doc{Root: &Node{Visible: true, Opacity: 1, Blend: BlendPassThrough, Children: []*Node{n}}}
	b := d.Bounds()
	if b.Empty() {
		return nil
	}
	b = image.Rectangle{Min: b.Min, Max: b.Min.Add(image.Pt(min(b.Dx(), maxSide), min(b.Dy(), maxSide)))}
	img := image.NewRGBA(b)
	d.compositeBands(img, d.Root, b, 0)
	return img
}

// ---- Shared layers on their own

// A smart object's own file is a picture whose layers are its shared layers, as Photoshop opens a smart object's
// contents: no group around them, its top level being the smart object itself. Opened in a canvas, it is edited as
// any picture, and every change in it is a change to the shared layers, offered as any copy's is.

// smartDoc is the file of a smart object: its shared layers at the top level, with no offset.
func smartDoc(c SmartContent) *Doc {
	d := &Doc{}
	d.Root = &Node{Name: c.Name, Visible: true, Opacity: 1, Blend: BlendPassThrough, Open: true, Smart: &SmartLink{ID: c.ID, Version: c.Version}}
	d.Root.ID = d.newID()
	d.Root.Children = d.placed(c.Layers, image.Point{})
	if len(d.Root.Children) == 0 {
		d.Root.Children = []*Node{d.newLayer("Layer 1")} // a picture keeps a layer; an empty one shows nothing
	}
	d.Active = d.Root.Children[0].ID
	if d.Node(d.Active).IsGroup() {
		d.Active = firstLayerOf(d.Root)
	}
	d.noteShared(d.Root, false) // what it shows is what is shared, before any change
	return d
}

// firstLayerOf is the id of the first layer under g, or of g's first child.
func firstLayerOf(g *Node) int {
	for _, c := range g.Children {
		if !c.IsGroup() {
			return c.ID
		}
		if id := firstLayerOf(c); id != 0 {
			return id
		}
	}
	return 0
}

// SmartFile is the file of the shared layers c, to save and open as a canvas.
func SmartFile(c SmartContent) *Doc { return smartDoc(c) }

// SaveSmart writes shared layers as their smart object's file.
func SaveSmart(w io.Writer, c SmartContent) error { return smartDoc(c).Snapshot().Save(w) }

// LoadSmart reads a smart object's file.
func LoadSmart(r io.ReaderAt, size int64) (SmartContent, error) {
	d, err := Load(r, size)
	if err != nil {
		return SmartContent{}, err
	}
	if !d.Root.IsSmart() {
		return SmartContent{}, ErrNotACanvas
	}
	return SmartContent{ID: d.Root.Smart.ID, Name: d.Root.Name, Version: d.Root.Smart.Version, Layers: canonical(d.Root)}, nil
}

// SmartSource is the link id of the smart object whose own file the picture is, or "".
func (d *Doc) SmartSource() string {
	if d.Root.IsSmart() {
		return d.Root.Smart.ID
	}
	return ""
}

// SmartName is the name of the smart object whose own file the picture is.
func (d *Doc) SmartName() string { return d.Root.Name }

// RenameSmart names the smart object whose own file the picture is, as one step; the name goes with its shared
// layers to its copies' files.
func (d *Doc) RenameSmart(name string) {
	if !d.Root.IsSmart() {
		return
	}
	m := d.Mark()
	d.Root.Name = name
	d.noteShared(d.Root, true)
	d.Commit("Rename layer", m)
}

// SmartPicture is how shared layers look on their own, cut to what they show; nil when they show nothing.
func SmartPicture(c SmartContent) *image.RGBA {
	d := smartDoc(c)
	d.Root.Smart = nil // drawn as layers
	b := d.Bounds()
	if b.Empty() {
		return nil
	}
	if b.Dx() > maxSide || b.Dy() > maxSide {
		b = image.Rectangle{Min: b.Min, Max: b.Min.Add(image.Pt(min(b.Dx(), maxSide), min(b.Dy(), maxSide)))}
	}
	img := image.NewRGBA(b)
	draw.Draw(img, b, image.Transparent, image.Point{}, draw.Src)
	d.compositeBands(img, d.Root, b, 0)
	return img
}
