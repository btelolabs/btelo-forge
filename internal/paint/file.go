package paint

import (
	"archive/zip"
	"compress/flate"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"slices"
	"strings"
)

// Ext is the extension of a saved picture with its layers.
const Ext = ".btcanvas"

// ErrNotACanvas means a file is not a picture saved by Save.
var ErrNotACanvas = errors.New("not a canvas file")

// The file is a zip with the layer tree in doc.json and, for each layer, mask and the selection, its tiles one
// after another in <name>.tiles (premultiplied RGBA, or 8-bit for masks), in the order doc.json lists their keys;
// all losslessly compressed. Versions 1 and 2 had a picture of a fixed size, whose layers were rectangles in
// layer-<id>.rgba, masks in mask-<id>.a and the selection, the picture's size, in selection.a (version 1 had no
// masks and a rectangular selection); they are still read, their size becoming the frame. The zip's comment, which
// ends the file, is the small picture (internal/thumb). Version 4 added the
// artboards, version 5 adjustment layers, version 6 vector masks and made the artboards groups (versions 4 and 5
// listed them apart from the layers; each becomes a group of the top-level layers lying wholly on it); older files
// are read the same way, without what they lack.
const fileVersion = 6

// fileBoard is an artboard of versions 4 and 5: its rectangle and its background as R, G, B, A.
type fileBoard struct {
	ID   int      `json:"id"`
	Name string   `json:"name"`
	Rect [4]int   `json:"rect"`
	Fill [4]uint8 `json:"fill"`
}

// fileSmart is what makes a group a smart object: its link id, its offset and its shared layers' version. Its
// layers are saved as any group's, so a file always holds what its smart objects show.
type fileSmart struct {
	ID      string `json:"id"`
	Offset  [2]int `json:"offset,omitempty"`
	Version int64  `json:"version"`
}

// fileFrame is what makes a group an artboard: its rectangle and its background as R, G, B, A.
type fileFrame struct {
	Rect [4]int   `json:"rect"`
	Fill [4]uint8 `json:"fill"`
}

// filePlane is where a raster or mask is: its offset, its fill and the keys of its tiles.
type filePlane struct {
	Offset [2]int   `json:"offset,omitempty"`
	Fill   uint8    `json:"fill,omitempty"`
	Tiles  [][2]int `json:"tiles"`
}

type fileNode struct {
	ID        int          `json:"id"`
	Name      string       `json:"name"`
	Visible   bool         `json:"visible"`
	Opacity   float64      `json:"opacity"`
	Blend     Blend        `json:"blend,omitempty"`
	LockAlpha bool         `json:"lockAlpha,omitempty"`
	Pixels    *filePlane   `json:"pixels,omitempty"`
	Vector    *Vector      `json:"vector,omitempty"`
	Adjust    *Adjustment  `json:"adjust,omitempty"`
	Style     *LayerStyle  `json:"style,omitempty"`
	MaskTiles *filePlane   `json:"maskTiles,omitempty"`
	MaskOff   bool         `json:"maskOff,omitempty"`
	Text      *Text        `json:"text,omitempty"` // a text layer's words; its pixels are saved as for any layer
	Clip      *Vector      `json:"clip,omitempty"`
	Board     *fileFrame   `json:"board,omitempty"`
	Smart     *fileSmart   `json:"smart,omitempty"`
	Filters   *FilterStack `json:"filters,omitempty"`
	Group     bool         `json:"group,omitempty"`
	Open      bool         `json:"open,omitempty"`
	Children  []fileNode   `json:"children,omitempty"`

	// Versions 1 and 2.
	Rect     *[4]int `json:"rect,omitempty"` // a layer's pixels; version 1 had none: the whole picture
	Mask     *[4]int `json:"mask,omitempty"`
	MaskFill uint8   `json:"maskFill,omitempty"`
}

type fileDoc struct {
	Version  int         `json:"version"`
	Frame    *[4]int     `json:"frame,omitempty"`
	Paper    bool        `json:"paper,omitempty"`
	Boards   []fileBoard `json:"boards,omitempty"`
	Active   int         `json:"active"`
	NextID   int         `json:"nextId"`
	EditMask bool        `json:"editMask,omitempty"`
	// A smart object's own file: its link, its top level being the smart object, and its name.
	Smart     *fileSmart `json:"smart,omitempty"`
	SmartName string     `json:"smartName,omitempty"`
	Sel       *filePlane `json:"selectionTiles,omitempty"`
	Layers    []fileNode `json:"layers"`

	// Versions 1 and 2.
	W         int     `json:"width,omitempty"`
	H         int     `json:"height,omitempty"`
	Selection *[4]int `json:"selection,omitempty"` // version 1: a rectangle; version 2: selection.a holds it
	HasSel    bool    `json:"hasSelection,omitempty"`
}

func rectOf(r image.Rectangle) *[4]int { return &[4]int{r.Min.X, r.Min.Y, r.Max.X, r.Max.Y} }

func rectFrom(a *[4]int) image.Rectangle { return image.Rect(a[0], a[1], a[2], a[3]) }

// Snapshot is a copy of a picture that Save can write on another goroutine while painting goes on: it shares the
// picture's tiles, frozen, so taking it costs no copying of pixels.
type Snapshot struct {
	doc    fileDoc
	planes map[string]plane

	Thumb []byte // the end of the file, from thumb.Encode, if it has one
}

// planeOf notes a plane for saving under name and describes it, its tile keys in a fixed order.
func (s *Snapshot) planeOf(name string, p plane) *filePlane {
	keys := make([][2]int, 0, len(p.tiles))
	for k := range p.tiles {
		keys = append(keys, [2]int{k.X, k.Y})
	}
	slices.SortFunc(keys, func(a, b [2]int) int {
		if a[1] != b[1] {
			return a[1] - b[1]
		}
		return a[0] - b[0]
	})
	s.planes[name] = p
	return &filePlane{Offset: [2]int{p.off.X, p.off.Y}, Fill: p.fill, Tiles: keys}
}

// Snapshot copies the picture for saving.
func (d *Doc) Snapshot() Snapshot {
	s := Snapshot{planes: map[string]plane{}}
	var conv func(n *Node) fileNode
	conv = func(n *Node) fileNode {
		f := fileNode{ID: n.ID, Name: n.Name, Visible: n.Visible, Opacity: n.Opacity, Blend: n.Blend, LockAlpha: n.LockAlpha,
			Group: n.IsGroup(), Open: n.Open, Text: n.Text, MaskOff: n.MaskOff, Style: n.Style, Clip: n.Clip, Filters: n.Filters}
		if s := n.Smart; s != nil {
			f.Smart = &fileSmart{ID: s.ID, Offset: [2]int{s.Offset.X, s.Offset.Y}, Version: s.Version}
		}
		if b := n.Board; b != nil {
			f.Board = &fileFrame{Rect: *rectOf(b.Rect), Fill: [4]uint8{b.Fill.R, b.Fill.G, b.Fill.B, b.Fill.A}}
		}
		if n.Mask != nil {
			f.MaskTiles = s.planeOf(fmt.Sprintf("mask-%d", n.ID), n.Mask.Snapshot().p)
		}
		switch {
		case n.IsGroup():
			for _, c := range n.Children {
				f.Children = append(f.Children, conv(c))
			}
		case n.Vector != nil:
			f.Vector = n.Vector
		case n.Adjust != nil:
			f.Adjust = n.Adjust
		default:
			f.Pixels = s.planeOf(fmt.Sprintf("layer-%d", n.ID), n.Pixels.Snapshot().p)
		}
		return f
	}
	root := conv(d.Root)
	s.doc = fileDoc{Version: fileVersion, Paper: d.Paper, Active: d.Active, NextID: d.nextID, EditMask: d.EditMask,
		Layers: root.Children}
	if !d.Frame.Empty() {
		s.doc.Frame = rectOf(d.Frame)
	}
	if sm := d.Root.Smart; sm != nil {
		s.doc.Smart, s.doc.SmartName = &fileSmart{ID: sm.ID, Version: sm.Version}, d.Root.Name
	}
	if d.Selection != nil {
		s.doc.Sel = s.planeOf("selection", d.Selection.Snapshot().p)
	}
	return s
}

// Save writes the snapshot.
func (s Snapshot) Save(w io.Writer) error {
	z := zip.NewWriter(w)
	z.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, flate.BestSpeed)
	})
	f, err := z.Create("doc.json")
	if err != nil {
		return err
	}
	if err := json.NewEncoder(f).Encode(s.doc); err != nil {
		return err
	}
	names := make([]string, 0, len(s.planes))
	for name := range s.planes {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		p := s.planes[name]
		f, err := z.Create(name + ".tiles")
		if err != nil {
			return err
		}
		for _, k := range s.planeOf(name, p).Tiles {
			if _, err := f.Write(p.tiles[image.Pt(k[0], k[1])].pix); err != nil {
				return err
			}
		}
	}
	if err := z.SetComment(string(s.Thumb)); err != nil {
		return err
	}
	return z.Close()
}

// Limits against damaged files: how many tiles a file may hold, and how far from the origin, in tiles, they and
// an old layer's rectangle may be.
const (
	maxTiles     = 1 << 20
	maxTileReach = 1 << 20
	maxBoards    = 10000
	maxLayerSide = 3 * maxSide
)

// Load reads a picture written by Save; its history starts empty.
func Load(r io.ReaderAt, size int64) (*Doc, error) {
	z, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotACanvas, err)
	}
	files := map[string]*zip.File{}
	for _, f := range z.File {
		files[f.Name] = f
	}
	read := func(name string, want int64) ([]byte, error) {
		f, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("%w: %s is missing", ErrNotACanvas, name)
		}
		if f.UncompressedSize64 != uint64(want) && want >= 0 {
			return nil, fmt.Errorf("%w: %s has %d bytes, want %d", ErrNotACanvas, name, f.UncompressedSize64, want)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		limit := want
		if limit < 0 {
			limit = 16 << 20
		}
		data, err := io.ReadAll(io.LimitReader(rc, limit+1))
		if err == nil && want >= 0 && int64(len(data)) != want {
			err = fmt.Errorf("%w: %s is damaged", ErrNotACanvas, name)
		}
		return data, err
	}
	data, err := read("doc.json", -1)
	if err != nil {
		return nil, err
	}
	var fd fileDoc
	if err := json.Unmarshal(data, &fd); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotACanvas, err)
	}
	if fd.Version < 1 || fd.Version > fileVersion {
		return nil, fmt.Errorf("%w: version %d", ErrNotACanvas, fd.Version)
	}
	if fd.Version < 3 {
		return loadOld(fd, read)
	}
	d := &Doc{nextID: fd.NextID, EditMask: fd.EditMask, Paper: fd.Paper}
	if fd.Frame != nil {
		d.Frame = rectFrom(fd.Frame).Canon()
		if d.Frame.Dx() > maxSide || d.Frame.Dy() > maxSide {
			return nil, fmt.Errorf("%w: a frame of %v", ErrTooBig, d.Frame)
		}
	}
	tiles := 0
	loadPlane := func(name string, fp *filePlane, bpp int) (plane, error) {
		p := newPlane(bpp, fp.Fill)
		p.off = image.Pt(fp.Offset[0], fp.Offset[1])
		tiles += len(fp.Tiles)
		if tiles > maxTiles || abs(p.off.X) > maxTileReach*TileSize || abs(p.off.Y) > maxTileReach*TileSize {
			return p, fmt.Errorf("%w: %s is too large", ErrNotACanvas, name)
		}
		size := TileSize * TileSize * bpp
		data, err := read(name+".tiles", int64(len(fp.Tiles))*int64(size))
		if err != nil {
			return p, err
		}
		for i, k := range fp.Tiles {
			key := image.Pt(k[0], k[1])
			if abs(key.X) > maxTileReach || abs(key.Y) > maxTileReach || p.tiles[key] != nil {
				return p, fmt.Errorf("%w: %s has a bad tile", ErrNotACanvas, name)
			}
			p.tiles[key] = &tile{pix: data[i*size : (i+1)*size : (i+1)*size]}
		}
		return p, nil
	}
	seen := map[int]bool{}
	var conv func(f fileNode) (*Node, error)
	conv = func(f fileNode) (*Node, error) {
		if seen[f.ID] || f.ID <= 0 || f.Blend < 0 || f.Blend >= blendCount || !f.Group && f.Pixels == nil && f.Vector == nil && f.Adjust == nil ||
			f.Vector != nil && !f.Vector.valid() || f.Adjust != nil && !f.Adjust.valid() || f.Style != nil && !f.Style.valid() || !f.Filters.valid() ||
			f.Clip != nil && !f.Clip.valid() || f.Board != nil && !f.Group ||
			f.Smart != nil && (!f.Group || f.Board != nil || !validLinkID(strings.ToUpper(f.Smart.ID)) || abs(f.Smart.Offset[0]) > maxTileReach*TileSize || abs(f.Smart.Offset[1]) > maxTileReach*TileSize) {
			return nil, fmt.Errorf("%w: layer %d", ErrNotACanvas, f.ID)
		}
		seen[f.ID] = true
		d.nextID = max(d.nextID, f.ID)
		n := &Node{ID: f.ID, Name: f.Name, Visible: f.Visible, Opacity: min(max(f.Opacity, 0), 1), Blend: f.Blend,
			LockAlpha: f.LockAlpha, Open: f.Open, MaskOff: f.MaskOff}
		if f.Smart != nil {
			n.Filters = f.Filters
		}
		if !f.Group && f.Adjust == nil && !f.Style.empty() {
			n.Style = f.Style
		}
		n.Text, n.Clip = checkedText(f), f.Clip
		if s := f.Smart; s != nil {
			n.Smart = &SmartLink{ID: strings.ToUpper(s.ID), Offset: image.Pt(s.Offset[0], s.Offset[1]), Version: s.Version}
		}
		if fb := f.Board; fb != nil {
			r, err := boardRect(fb.Rect)
			if err != nil {
				return nil, fmt.Errorf("%w: artboard %d", ErrNotACanvas, f.ID)
			}
			n.Board = &BoardFrame{Rect: r, Fill: color.NRGBA{fb.Fill[0], fb.Fill[1], fb.Fill[2], fb.Fill[3]}}
		}
		if f.MaskTiles != nil {
			p, err := loadPlane(fmt.Sprintf("mask-%d", f.ID), f.MaskTiles, 1)
			if err != nil {
				return nil, err
			}
			n.Mask = &Mask{p: p}
		}
		if f.Group {
			n.Children = []*Node{}
			for _, c := range f.Children {
				cn, err := conv(c)
				if err != nil {
					return nil, err
				}
				n.Children = append(n.Children, cn)
			}
			return n, nil
		}
		if f.Adjust != nil {
			n.Adjust, n.Text = f.Adjust, nil
			return n, nil
		}
		if f.Vector != nil {
			n.Vector, n.Text = f.Vector, nil
			return n, nil
		}
		p, err := loadPlane(fmt.Sprintf("layer-%d", f.ID), f.Pixels, 4)
		if err != nil {
			return nil, err
		}
		n.Pixels = &Raster{p: p}
		return n, nil
	}
	if err := d.loadTree(fd, conv); err != nil {
		return nil, err
	}
	if !d.boardsAtTop() {
		return nil, fmt.Errorf("%w: an artboard inside a group", ErrNotACanvas)
	}
	if s := fd.Smart; s != nil {
		id := strings.ToUpper(s.ID)
		if !validLinkID(id) || len(d.Boards()) > 0 || len(fd.Boards) > 0 {
			return nil, fmt.Errorf("%w: a smart object's file", ErrNotACanvas)
		}
		d.Root.Smart, d.Root.Name = &SmartLink{ID: id, Version: s.Version}, fd.SmartName
	}
	if len(fd.Boards) > maxBoards {
		return nil, fmt.Errorf("%w: %d artboards", ErrNotACanvas, len(fd.Boards))
	}
	for i := len(fd.Boards) - 1; i >= 0; i-- { // versions 4 and 5 list them bottom first; each goes under the rest
		fb := fd.Boards[i]
		r, err := boardRect(fb.Rect)
		if seen[fb.ID] || fb.ID <= 0 || err != nil {
			return nil, fmt.Errorf("%w: artboard %d", ErrNotACanvas, fb.ID)
		}
		seen[fb.ID] = true
		d.nextID = max(d.nextID, fb.ID)
		n := &Node{ID: fb.ID, Name: fb.Name, Visible: true, Opacity: 1, Blend: BlendPassThrough, Open: true, Children: []*Node{},
			Board: &BoardFrame{Rect: r, Fill: color.NRGBA{fb.Fill[0], fb.Fill[1], fb.Fill[2], fb.Fill[3]}}}
		d.placeBoard(n, true) // its background was drawn under every layer
	}
	if fd.Sel != nil {
		p, err := loadPlane("selection", fd.Sel, 1)
		if err != nil {
			return nil, err
		}
		d.Selection = &Mask{p: p}
	}
	d.settleSmart()
	d.settleLoaded()
	return d, nil
}

// boardRect is an artboard's rectangle read from a file, when it makes sense.
func boardRect(a [4]int) (image.Rectangle, error) {
	r, err := validBoard(rectFrom(&a))
	if err == nil && (abs(r.Min.X) > maxTileReach*TileSize || abs(r.Min.Y) > maxTileReach*TileSize) {
		err = ErrTooBig
	}
	return r, err
}

// checkedText is a layer's text when it makes sense; otherwise the layer is left as the pixels it was drawn as.
func checkedText(f fileNode) *Text {
	if t := f.Text; t != nil && !f.Group && t.Size > 0 && t.Size <= maxSide && t.Align >= AlignLeft && t.Align <= AlignRight {
		return t
	}
	return nil
}

// loadTree builds the layer tree from a file's nodes.
func (d *Doc) loadTree(fd fileDoc, conv func(fileNode) (*Node, error)) error {
	d.Root = &Node{Visible: true, Opacity: 1, Blend: BlendPassThrough, Open: true, Children: []*Node{}}
	for _, f := range fd.Layers {
		n, err := conv(f)
		if err != nil {
			return err
		}
		d.Root.Children = append(d.Root.Children, n)
	}
	if len(d.Layers()) == 0 {
		return fmt.Errorf("%w: no layers", ErrNotACanvas)
	}
	d.Root.ID = d.newID()
	d.Active = fd.Active
	return nil
}

// settleLoaded chooses a layer that exists and leaves mask editing only for a layer with a mask.
func (d *Doc) settleLoaded() {
	if d.Node(d.Active) == nil {
		d.Active = d.someNode()
	}
	if n := d.Node(d.Active); n == nil || n.Mask == nil {
		d.EditMask = false
	}
}

// loadOld reads a version 1 or 2 file, whose picture had a fixed size: that size becomes the frame.
func loadOld(fd fileDoc, read func(string, int64) ([]byte, error)) (*Doc, error) {
	if fd.W <= 0 || fd.H <= 0 || fd.W > maxSide || fd.H > maxSide {
		return nil, fmt.Errorf("%w: %d×%d", ErrTooBig, fd.W, fd.H)
	}
	full := image.Rect(0, 0, fd.W, fd.H)
	d := &Doc{Frame: full, nextID: fd.NextID, EditMask: fd.EditMask}
	sane := func(r image.Rectangle) bool {
		return !r.Empty() && r.Dx() <= maxLayerSide && r.Dy() <= maxLayerSide &&
			r.Min.X > -maxLayerSide && r.Min.Y > -maxLayerSide && r.Max.X < 2*maxLayerSide && r.Max.Y < 2*maxLayerSide
	}
	seen := map[int]bool{}
	var conv func(f fileNode) (*Node, error)
	conv = func(f fileNode) (*Node, error) {
		if seen[f.ID] || f.ID <= 0 || f.Blend < 0 || f.Blend >= blendCount {
			return nil, fmt.Errorf("%w: layer %d", ErrNotACanvas, f.ID)
		}
		seen[f.ID] = true
		d.nextID = max(d.nextID, f.ID)
		n := &Node{ID: f.ID, Name: f.Name, Visible: f.Visible, Opacity: min(max(f.Opacity, 0), 1), Blend: f.Blend,
			LockAlpha: f.LockAlpha, Open: f.Open, MaskOff: f.MaskOff}
		n.Text = checkedText(f)
		if f.Mask != nil {
			mr := rectFrom(f.Mask)
			if !sane(mr) {
				return nil, fmt.Errorf("%w: mask of layer %d", ErrNotACanvas, f.ID)
			}
			pix, err := read(fmt.Sprintf("mask-%d.a", f.ID), int64(mr.Dx())*int64(mr.Dy()))
			if err != nil {
				return nil, err
			}
			n.Mask = MaskFrom(&image.Alpha{Pix: pix, Stride: mr.Dx(), Rect: mr}, f.MaskFill)
		}
		if f.Group {
			if fd.Version == 1 {
				n.Blend = BlendPassThrough
			}
			n.Children = []*Node{}
			for _, c := range f.Children {
				cn, err := conv(c)
				if err != nil {
					return nil, err
				}
				n.Children = append(n.Children, cn)
			}
			return n, nil
		}
		lr := full
		if f.Rect != nil {
			lr = rectFrom(f.Rect)
		}
		if !sane(lr) {
			return nil, fmt.Errorf("%w: layer %d's area", ErrNotACanvas, f.ID)
		}
		pix, err := read(fmt.Sprintf("layer-%d.rgba", f.ID), int64(lr.Dx())*int64(lr.Dy())*4)
		if err != nil {
			return nil, err
		}
		n.Pixels = RasterFrom(&image.RGBA{Pix: pix, Stride: lr.Dx() * 4, Rect: lr})
		return n, nil
	}
	if err := d.loadTree(fd, conv); err != nil {
		return nil, err
	}
	switch {
	case fd.HasSel:
		pix, err := read("selection.a", int64(fd.W)*int64(fd.H))
		if err != nil {
			return nil, err
		}
		d.Selection = MaskFrom(&image.Alpha{Pix: pix, Stride: fd.W, Rect: full}, 0)
	case fd.Selection != nil && !rectFrom(fd.Selection).Intersect(full).Empty():
		d.Selection = d.RectMask(rectFrom(fd.Selection).Intersect(full))
	}
	d.settleLoaded()
	return d, nil
}
