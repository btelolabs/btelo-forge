package mind

import (
	"bufio"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"image/color"
	"io"
	"math"
	"path/filepath"
	"strings"
)

// The file: "BTMIND" and a version byte, then the map deflated. It holds only what cannot be worked out: a tree's
// node has no place saved, as its parent's layout gives it one, and pictures, videos, nested maps and smart objects
// are named, never copied — by a path relative to the map's folder when they lie under it, so a folder of maps and
// their files can be moved together. Numbers are variable-length, places and sizes in quarters of a unit, and nodes
// come tree by tree, each before its children, so the children's order needs no saving.

const (
	magic       = "BTMIND"
	fileVersion = 1
)

// ErrNotAMap means a file is not a mind map saved by Save.
var ErrNotAMap = errors.New("not a mind map file")

// The flags of a node: which of its optional fields follow.
const (
	fBold = 1 << iota
	fFolded
	fPlace
	fSize
	fFill
	fLine
	fInk
	fFont
	fRef
	fAspect
	fCells
	fText
	fShape
	fLayout
	fSide
)

type writer struct {
	w   *bufio.Writer
	buf [binary.MaxVarintLen64]byte
	err error
}

func (w *writer) uvarint(v uint64) {
	n := binary.PutUvarint(w.buf[:], v)
	w.bytes(w.buf[:n])
}

func (w *writer) varint(v int64) {
	n := binary.PutVarint(w.buf[:], v)
	w.bytes(w.buf[:n])
}

func (w *writer) bytes(b []byte) {
	if w.err == nil {
		_, w.err = w.w.Write(b)
	}
}

func (w *writer) byte1(b byte) { w.bytes([]byte{b}) }

func (w *writer) str(s string) {
	w.uvarint(uint64(len(s)))
	w.bytes([]byte(s))
}

// quarter writes a place or size in quarters of a unit.
func (w *writer) quarter(v float64) { w.varint(int64(math.Round(v * 4))) }

func (w *writer) color(c color.RGBA) { w.bytes([]byte{c.R, c.G, c.B, c.A}) }

// Save writes the map; dir is the folder it is saved in, against which file references are written.
func Save(out io.Writer, m *Map, dir string) error {
	s, err := Snapshot(m, dir)
	if err != nil {
		return err
	}
	return s.Write(out)
}

// Saving is a map read for saving, to be compressed and written on another goroutine while the map changes on.
type Saving struct{ body []byte }

// Snapshot reads the map for saving, quickly: compressing it is left to Write.
func Snapshot(m *Map, dir string) (*Saving, error) {
	var buf bytes.Buffer
	w := &writer{w: bufio.NewWriterSize(&buf, 1<<16)}
	if err := encodeBody(w, m, dir); err != nil {
		return nil, err
	}
	if err := w.w.Flush(); err != nil {
		return nil, err
	}
	return &Saving{body: buf.Bytes()}, nil
}

// Write writes the map as Save does.
func (s *Saving) Write(out io.Writer) error {
	if _, err := io.WriteString(out, magic); err != nil {
		return err
	}
	if _, err := out.Write([]byte{fileVersion}); err != nil {
		return err
	}
	fw, err := flate.NewWriter(out, flate.BestCompression)
	if err != nil {
		return err
	}
	if _, err := fw.Write(s.body); err != nil {
		return err
	}
	return fw.Close()
}

// encodeBody writes what follows the file's head, before it is compressed.
func encodeBody(w *writer, m *Map, dir string) error {
	w.str(m.Title)
	w.uvarint(uint64(m.count))
	prev := ID(0)
	var write func(n *Node)
	write = func(n *Node) {
		w.uvarint(uint64(n.ID - prev))
		prev = n.ID
		w.byte1(byte(n.Kind))
		w.uvarint(uint64(n.Parent))
		flags := uint64(0)
		set := func(f uint64, on bool) {
			if on {
				flags |= f
			}
		}
		set(fBold, n.Bold)
		set(fFolded, n.Folded)
		set(fPlace, n.Parent == 0)
		set(fSize, n.W != 0 || n.H != 0)
		set(fFill, n.Fill.A != 0)
		set(fLine, n.Line.A != 0)
		set(fInk, n.Ink.A != 0)
		set(fFont, n.Size != 0)
		set(fRef, n.Ref != "")
		set(fAspect, n.Aspect != 0)
		set(fCells, n.Cells != nil)
		set(fText, n.Text != "")
		set(fShape, n.Shape != ShapeAuto)
		set(fLayout, n.Layout != LayoutMap)
		set(fSide, n.Side != SideAuto)
		w.uvarint(flags)
		if flags&fPlace != 0 {
			w.quarter(n.X)
			w.quarter(n.Y)
		}
		if flags&fSize != 0 {
			w.quarter(n.W)
			w.quarter(n.H)
		}
		if flags&fFill != 0 {
			w.color(n.Fill)
		}
		if flags&fLine != 0 {
			w.color(n.Line)
		}
		if flags&fInk != 0 {
			w.color(n.Ink)
		}
		if flags&fFont != 0 {
			w.uvarint(uint64(math.Round(float64(n.Size) * 4)))
		}
		if flags&fRef != 0 {
			w.str(relRef(n, dir))
		}
		if flags&fAspect != 0 {
			w.uvarint(uint64(math.Float32bits(float32(n.Aspect))))
		}
		if flags&fCells != 0 {
			g := n.Cells
			w.uvarint(uint64(g.Rows))
			w.uvarint(uint64(g.Cols))
			for _, c := range g.Cells {
				w.str(c)
			}
			for i := range g.Cols {
				v := 0.0
				if i < len(g.ColW) {
					v = g.ColW[i]
				}
				w.quarter(v)
			}
		}
		if flags&fText != 0 {
			w.str(n.Text)
		}
		if flags&fShape != 0 {
			w.byte1(byte(n.Shape))
		}
		if flags&fLayout != 0 {
			w.byte1(byte(n.Layout))
		}
		if flags&fSide != 0 {
			w.byte1(byte(n.Side))
		}
	}
	// Tree by tree, each node before its children, iteratively: trees may be deeper than a stack allows.
	m.Each(func(root *Node) bool {
		if root.Parent == 0 {
			stack := []*Node{root}
			for len(stack) > 0 && w.err == nil {
				n := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				write(n)
				for i := len(n.Children) - 1; i >= 0; i-- {
					stack = append(stack, m.nodes[n.Children[i]])
				}
			}
		}
		return w.err == nil
	})
	w.uvarint(uint64(m.links))
	prevE := EdgeID(0)
	m.EachEdge(func(e *Edge) bool {
		w.uvarint(uint64(e.ID - prevE))
		prevE = e.ID
		w.uvarint(uint64(e.From))
		w.uvarint(uint64(e.To))
		w.byte1(byte(e.FromSide) | byte(e.ToSide)<<4)
		style := byte(e.Route) | byte(e.Arrow)<<2
		if e.Dashed {
			style |= 1 << 4
		}
		if e.Color.A != 0 {
			style |= 1 << 5
		}
		w.byte1(style)
		if e.Color.A != 0 {
			w.color(e.Color)
		}
		w.str(e.Label)
		return w.err == nil
	})
	return w.err
}

// fileRef reports whether a node's reference is a file: a smart object's is a link id.
func fileRef(n *Node) bool { return n.Kind != Smart }

// relRef is a node's reference as saved: relative to dir when the file lies under it, with forward slashes.
func relRef(n *Node, dir string) string {
	if !fileRef(n) || dir == "" || !filepath.IsAbs(n.Ref) {
		return n.Ref
	}
	if rel, err := filepath.Rel(dir, n.Ref); err == nil && !strings.HasPrefix(rel, "..") {
		return "./" + filepath.ToSlash(rel)
	}
	return n.Ref
}

// absRef is a saved reference as used: a relative one made absolute against dir.
func absRef(n *Node, ref, dir string) string {
	if !fileRef(n) || !strings.HasPrefix(ref, "./") {
		return ref
	}
	return filepath.Join(dir, filepath.FromSlash(ref[2:]))
}

type reader struct {
	r   *bufio.Reader
	err error
}

func (r *reader) fail(err error) {
	if r.err == nil {
		r.err = err
	}
}

func (r *reader) uvarint() uint64 {
	if r.err != nil {
		return 0
	}
	v, err := binary.ReadUvarint(r.r)
	r.fail(err)
	return v
}

func (r *reader) varint() int64 {
	if r.err != nil {
		return 0
	}
	v, err := binary.ReadVarint(r.r)
	r.fail(err)
	return v
}

func (r *reader) byte1() byte {
	if r.err != nil {
		return 0
	}
	b, err := r.r.ReadByte()
	r.fail(err)
	return b
}

// maxString bounds a string read, so a damaged length does not ask for all the memory.
const maxString = 64 << 20

func (r *reader) str() string {
	n := r.uvarint()
	if r.err != nil {
		return ""
	}
	if n > maxString {
		r.fail(fmt.Errorf("%w: a string of %d bytes", ErrNotAMap, n))
		return ""
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r.r, b)
	r.fail(err)
	return string(b)
}

func (r *reader) quarter() float64 { return float64(r.varint()) / 4 }

func (r *reader) color() color.RGBA {
	return color.RGBA{r.byte1(), r.byte1(), r.byte1(), r.byte1()}
}

// maxNodes bounds how many nodes a file may claim.
const maxNodes = 1 << 26

// Load reads a map saved by Save from the folder dir, measured with measure.
func Load(in io.Reader, dir string, measure Measurer) (*Map, error) {
	head := make([]byte, len(magic)+1)
	if _, err := io.ReadFull(in, head); err != nil || string(head[:len(magic)]) != magic {
		return nil, ErrNotAMap
	}
	if head[len(magic)] > fileVersion {
		return nil, fmt.Errorf("the mind map was saved by a newer version of the app (file version %d)", head[len(magic)])
	}
	r := &reader{r: bufio.NewReaderSize(flate.NewReader(in), 1<<16)}
	m := New(r.str(), measure)
	count := r.uvarint()
	if count > maxNodes {
		return nil, fmt.Errorf("%w: %d nodes", ErrNotAMap, count)
	}
	id := ID(0)
	for range count {
		id += ID(r.uvarint())
		n := &Node{ID: id, Kind: Kind(r.byte1()), Parent: ID(r.uvarint())}
		flags := r.uvarint()
		if flags&fPlace != 0 {
			n.X, n.Y = r.quarter(), r.quarter()
		}
		if flags&fSize != 0 {
			n.W, n.H = r.quarter(), r.quarter()
		}
		if flags&fFill != 0 {
			n.Fill = r.color()
		}
		if flags&fLine != 0 {
			n.Line = r.color()
		}
		if flags&fInk != 0 {
			n.Ink = r.color()
		}
		if flags&fFont != 0 {
			n.Size = float32(r.uvarint()) / 4
		}
		if flags&fRef != 0 {
			n.Ref = r.str()
		}
		if flags&fAspect != 0 {
			n.Aspect = float64(math.Float32frombits(uint32(r.uvarint())))
		}
		if flags&fCells != 0 {
			g := &Grid{Rows: int(r.uvarint()), Cols: int(r.uvarint())}
			if g.Rows*g.Cols > maxNodes || g.Rows < 0 || g.Cols < 0 {
				return nil, fmt.Errorf("%w: a table of %d × %d", ErrNotAMap, g.Rows, g.Cols)
			}
			g.Cells = make([]string, g.Rows*g.Cols)
			for i := range g.Cells {
				g.Cells[i] = r.str()
			}
			g.ColW = make([]float64, g.Cols)
			for i := range g.ColW {
				g.ColW[i] = r.quarter()
			}
			n.Cells = g
		}
		if flags&fText != 0 {
			n.Text = r.str()
		}
		if flags&fShape != 0 {
			n.Shape = Shape(r.byte1())
		}
		if flags&fLayout != 0 {
			n.Layout = Layout(r.byte1())
		}
		if flags&fSide != 0 {
			n.Side = Side(r.byte1())
		}
		n.Bold, n.Folded = flags&fBold != 0, flags&fFolded != 0
		if r.err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNotAMap, r.err)
		}
		if id == 0 || int(id) < len(m.nodes) && m.nodes[id] != nil || int(id) > maxNodes {
			return nil, fmt.Errorf("%w: node %d twice", ErrNotAMap, id)
		}
		if n.Kind > Nested {
			n.Kind = Topic // a kind from a newer version: its words are kept
		}
		if n.Shape > ShapeNone {
			n.Shape = ShapeAuto
		}
		if n.Layout > LayoutList {
			n.Layout = LayoutMap
		}
		if n.Side > SideLeft {
			n.Side = SideAuto
		}
		n.Ref = absRef(n, n.Ref, dir)
		// A parent comes before its children; one that does not is a damaged file, and the node is set free.
		if n.Parent != 0 {
			if p := m.Node(n.Parent); p != nil {
				p.Children = append(p.Children, id)
			} else {
				n.Parent = 0
			}
		}
		m.grow(id)
		m.nodes[id] = n
	}
	edges := r.uvarint()
	if edges > maxNodes {
		return nil, fmt.Errorf("%w: %d connectors", ErrNotAMap, edges)
	}
	eid := EdgeID(0)
	for range edges {
		eid += EdgeID(r.uvarint())
		e := &Edge{ID: eid, From: ID(r.uvarint()), To: ID(r.uvarint())}
		sides := r.byte1()
		e.FromSide, e.ToSide = Side(sides&15), Side(sides>>4)
		style := r.byte1()
		e.Route, e.Arrow, e.Dashed = Route(style&3), Arrow(style>>2&3), style&(1<<4) != 0
		if style&(1<<5) != 0 {
			e.Color = r.color()
		}
		e.Label = r.str()
		if r.err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNotAMap, r.err)
		}
		if e.Route > RouteStraight {
			e.Route = RouteCurve
		}
		if e.FromSide > SideLeft {
			e.FromSide = SideAuto
		}
		if e.ToSide > SideLeft {
			e.ToSide = SideAuto
		}
		if m.Node(e.From) == nil || m.Node(e.To) == nil || e.From == e.To || eid == 0 || int(eid) < len(m.edges) && m.edges[eid] != nil {
			continue // a damaged connector is dropped, the rest kept
		}
		m.growEdges(eid)
		m.edges[eid] = e
	}
	if r.err != nil && !errors.Is(r.err, io.EOF) {
		return nil, fmt.Errorf("%w: %v", ErrNotAMap, r.err)
	}
	m.reindex()
	m.Saved()
	return m, nil
}

// Encode is the map as Save writes it.
func Encode(m *Map, dir string) ([]byte, error) {
	var buf bytes.Buffer
	err := Save(&buf, m, dir)
	return buf.Bytes(), err
}
