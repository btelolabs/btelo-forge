package paint

import (
	"errors"
	"image"
	"math"
)

// Transforming chosen nodes, as Ctrl+T does in Photoshop with anchor points chosen by the direct selection tool:
// the box goes round those nodes alone, and its handles move them and their handles, the rest of the object
// staying where it is. Every mode of Free Transform works, the warps mapping each point through the mesh.

// ErrNoNodes means a node transform was asked with no nodes of the object chosen.
var ErrNoNodes = errors.New("choose nodes with the node tool to transform them")

// BeginNodeTransform starts Free Transform on the nodes refs of node id's object (its vector mask when it has
// none). The box starts round the nodes, at least a pixel each way.
func (d *Doc) BeginNodeTransform(id int, refs []NodeRef) (*TransformSession, error) {
	n := d.Node(id)
	if n == nil {
		return nil, ErrNoLayer
	}
	v := n.vectorOf()
	if v == nil {
		return nil, ErrNotVector
	}
	cur := v.ToCurve()
	inv, ok := cur.M.Inverse()
	if !ok {
		return nil, ErrEmptyLayer
	}
	var chosen []NodeRef
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, r := range refs {
		nd, ok := cur.node(r)
		if !ok {
			continue
		}
		chosen = append(chosen, r)
		p := cur.M.Map(nd.P)
		minX, minY, maxX, maxY = min(minX, p.X), min(minY, p.Y), max(maxX, p.X), max(maxY, p.Y)
	}
	if len(chosen) == 0 {
		return nil, ErrNoNodes
	}
	b := image.Rect(int(math.Floor(minX)), int(math.Floor(minY)), int(math.Ceil(maxX)), int(math.Ceil(maxY)))
	if b.Dx() < 1 {
		b.Max.X = b.Min.X + 1
	}
	if b.Dy() < 1 {
		b.Max.Y = b.Min.Y + 1
	}
	return &TransformSession{d: d, l: n, mark: d.Mark(), m: Identity, bounds: b, shown: cur.PixelBounds(),
		nodes: &nodeTransform{refs: chosen, start: cur, inv: inv}}, nil
}

// nodeTransform is what a node transform moves: the nodes, and the object as it was.
type nodeTransform struct {
	refs  []NodeRef
	start Vector
	inv   Affine // from the canvas to the object's own space
}

// setNodes shows the chosen nodes moved through to, a map of canvas points; it returns the area that changed.
func (t *TransformSession) setNodes(to func(Pt) Pt) image.Rectangle {
	nt := t.nodes
	next := nt.start.MapNodes(nt.refs, func(p Pt) Pt { return nt.inv.Map(to(nt.start.M.Map(p))) })
	t.l.setVectorOf(&next)
	out := next.PixelBounds()
	changed := t.shown.Union(out)
	t.shown = out
	return changed
}

// MapNodes is the curve with the nodes refs, and their handles, mapped through f, in the object's own space.
func (v Vector) MapNodes(refs []NodeRef, f func(Pt) Pt) Vector {
	out := v.ToCurve()
	out.Path = cloneSubpaths(out.Path)
	done := map[NodeRef]bool{}
	for _, r := range refs {
		n, ok := out.node(r)
		if !ok || done[r] {
			continue
		}
		done[r] = true
		n.P, n.In, n.Out = f(n.P), f(n.In), f(n.Out)
		out.Path[r.Sub].Nodes[r.Node] = n
	}
	return out
}
