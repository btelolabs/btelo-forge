package paint

import (
	"errors"
	"image"
	"slices"
	"sync"
)

// A vector layer's object may be made of several shapes, as a Photoshop shape layer's path components are: each
// shape is a run of subpaths and combines with all the shapes before it by an operation, adding to them, taking
// itself away, keeping only where they meet, or keeping where exactly one of them is. The shapes stay editable node
// by node; the fill is worked out pixel by pixel from each shape's coverage, and the stroke follows the outline they
// make together. Expanding the shapes bakes that outline into one.

// ShapeOp is how a shape combines with the shapes before it.
type ShapeOp int

const (
	OpJoin      ShapeOp = iota // the subpath is part of the shape before it; the first one starts the first shape
	OpAdd                      // a shape added to those before it
	OpSubtract                 // a shape taken away from those before it
	OpIntersect                // only where the shape and those before it meet
	OpExclude                  // where either the shape or those before it is, but not both
)

// ErrNoShape means an operation was asked of a shape a vector layer does not have.
var ErrNoShape = errors.New("there is no such shape")

// boolOf is the boolean that a shape operation is.
func (op ShapeOp) boolOf() BoolOp {
	switch op {
	case OpSubtract:
		return BoolSubtract
	case OpIntersect:
		return BoolIntersect
	case OpExclude:
		return BoolExclude
	}
	return BoolAdd
}

// shapeRuns are a path's shapes, as the [start, end) runs of its subpaths.
func shapeRuns(path []Subpath) [][2]int {
	var runs [][2]int
	for i, s := range path {
		if i == 0 || s.Op != OpJoin {
			runs = append(runs, [2]int{i, i})
		}
		runs[len(runs)-1][1] = i + 1
	}
	return runs
}

// Compound reports whether the object is made of more than one shape.
func (v *Vector) Compound() bool { return v.Kind == VectorCurve && len(shapeRuns(v.Path)) > 1 }

// Shapes are the object's shapes as the [start, end) runs of its subpaths: one for a smart shape.
func (v *Vector) Shapes() [][2]int {
	if v.Kind != VectorCurve {
		return [][2]int{{0, 1}}
	}
	return shapeRuns(v.Path)
}

// ShapeOf is the shape that subpath sub is part of, as the index into Shapes, or -1.
func (v *Vector) ShapeOf(sub int) int {
	for i, r := range v.Shapes() {
		if sub >= r[0] && sub < r[1] {
			return i
		}
	}
	return -1
}

// ShapeOpOf is how shape i combines with the shapes before it; the first shape's is OpJoin.
func (v *Vector) ShapeOpOf(i int) ShapeOp {
	runs := v.Shapes()
	if i <= 0 || i >= len(runs) {
		return OpJoin
	}
	return v.Path[runs[i][0]].Op
}

// part is shape i as an object of its own, filled, as its coverage is worked out.
func (v *Vector) part(r [2]int) Vector {
	return Vector{Kind: VectorCurve, Path: v.Path[r[0]:r[1]], M: v.M, Rule: v.Rule}
}

// fillCoverage is how much of each pixel of r, at scale, the object's shapes fill together, 0 to 255; the outline
// of an open subpath is filled as if closed, as in Affinity.
func (v *Vector) fillCoverage(r image.Rectangle, scale float64) []uint8 {
	m := v.M.Then(Affine{A: scale, E: scale})
	polysOf := func(paths []Subpath) [][]Pt {
		polys, closed := flatten(paths, m, vectorTolerance)
		var shapes [][]Pt
		for i, p := range polys {
			if closed[i] || len(p) > 2 {
				shapes = append(shapes, p)
			}
		}
		return shapes
	}
	if !v.Compound() {
		return coverageOf(polysOf(v.Outline()), v.Rule, r, vectorSub)
	}
	runs := shapeRuns(v.Path)
	var out []uint8
	// Shapes added one after another, as the vector brush's strokes are, are covered in one pass: each wound the
	// same way, their outlines together wind round non-zero just where any of them is. pending holds them until a
	// shape combining another way comes, or the end.
	var pending [][]Pt
	flush := func() {
		if pending == nil {
			return
		}
		c := coverageOf(pending, v.Rule, r, vectorSub)
		if out == nil {
			out = c
		} else {
			combineCover(out, c, OpAdd)
		}
		pending = nil
	}
	for i, run := range runs {
		op := v.Path[run[0]].Op
		polys := polysOf(v.Path[run[0]:run[1]])
		if v.Rule == NonZero && (i == 0 || op == OpAdd) {
			pending = append(pending, wound(polys)...)
			continue
		}
		if i == 0 {
			out = coverageOf(polys, v.Rule, r, vectorSub)
			continue
		}
		flush()
		combineCover(out, coverageOf(polys, v.Rule, r, vectorSub), op)
	}
	flush()
	return out
}

// wound is a shape's polygons all turned the way that winds round positively, its holes the other way, so shapes
// covered together add up.
func wound(polys [][]Pt) [][]Pt {
	area := 0.0
	for _, p := range polys {
		for i := range p {
			area += p[i].Cross(p[(i+1)%len(p)])
		}
	}
	if area < 0 {
		for _, p := range polys {
			slices.Reverse(p)
		}
	}
	return polys
}

// combineCover combines coverage b into a by a shape operation, pixel by pixel.
func combineCover(a, b []uint8, op ShapeOp) {
	for i, av := range a {
		x, y := uint32(av), uint32(b[i])
		var c uint32
		switch op {
		case OpSubtract:
			c = mul8(x, 255-y)
		case OpIntersect:
			c = mul8(x, y)
		case OpExclude:
			c = x + y - 2*mul8(x, y)
		default:
			c = x + y - mul8(x, y)
		}
		a[i] = uint8(c)
	}
}

// combinedCache keeps the outlines that compound objects' strokes follow, by object: objects are never changed in
// place, so one worked out stays right for as long as it is kept, which keeps the object from being reused.
var combinedCache = struct {
	sync.Mutex
	order []*Vector
	of    map[*Vector][]Subpath
}{of: map[*Vector][]Subpath{}}

const combinedCacheSize = 64

// Combined is the outline the object's shapes make together, in its own space: its outline, for an object of one
// shape.
func (v *Vector) Combined() []Subpath {
	if !v.Compound() {
		return v.Outline()
	}
	c := &combinedCache
	c.Lock()
	if out, ok := c.of[v]; ok {
		c.Unlock()
		return out
	}
	c.Unlock()
	out := v.combine()
	c.Lock()
	if _, ok := c.of[v]; !ok {
		if len(c.order) >= combinedCacheSize {
			delete(c.of, c.order[0])
			c.order = slices.Delete(c.order, 0, 1)
		}
		c.order = append(c.order, v)
		c.of[v] = out
	}
	c.Unlock()
	return out
}

// combine works the shapes' outline out on the canvas, where the boolean's precision is set, and maps it back.
func (v *Vector) combine() []Subpath {
	inv, ok := v.M.Inverse()
	if !ok {
		return nil
	}
	runs := shapeRuns(v.Path)
	acc := Vector{Kind: VectorCurve, Path: v.Path[runs[0][0]:runs[0][1]], M: v.M, Rule: v.Rule}
	for _, run := range runs[1:] {
		next := v.part(run)
		acc = Vector{Kind: VectorCurve, Path: Combine(v.Path[run[0]].Op.boolOf(), []Vector{acc, next}), M: Identity, Rule: NonZero}
	}
	return mapSubpaths(acc.Path, acc.M.Then(inv))
}

// mapSubpaths is a path with every point and handle mapped through m.
func mapSubpaths(path []Subpath, m Affine) []Subpath {
	out := make([]Subpath, len(path))
	for i, s := range path {
		nodes := make([]PathNode, len(s.Nodes))
		for j, n := range s.Nodes {
			nodes[j] = PathNode{P: m.Map(n.P), In: m.Map(n.In), Out: m.Map(n.Out), Smooth: n.Smooth}
		}
		out[i] = Subpath{Nodes: nodes, Closed: s.Closed, Op: s.Op}
	}
	return out
}

// WithShape is the object with the outline of other added as a shape of its own that combines by op (OpAdd when
// op is OpJoin): other's shapes keep how they combine among themselves. The object becomes a curve if it was a
// smart shape; its fill and stroke stay.
func (v Vector) WithShape(other Vector, op ShapeOp) Vector {
	if op == OpJoin {
		op = OpAdd
	}
	res := v.ToCurve()
	inv, ok := res.M.Inverse()
	if !ok {
		return res
	}
	added := mapSubpaths(other.ToCurve().Path, other.M.Then(inv))
	if len(added) == 0 {
		return res
	}
	added[0].Op = op
	res.Path = append(cloneSubpaths(res.Path), added...)
	if len(res.Path) == len(added) {
		res.Path[0].Op = OpJoin // it was empty: the added shape is the first
	}
	return res
}

// WithShapeOp is the object with shape i combining by op instead; the first shape has none.
func (v Vector) WithShapeOp(i int, op ShapeOp) Vector {
	res := v.ToCurve()
	runs := shapeRuns(res.Path)
	if i <= 0 || i >= len(runs) || op == OpJoin {
		return res
	}
	res.Path = cloneSubpaths(res.Path)
	res.Path[runs[i][0]].Op = op
	return res
}

// Shape is shape i as an object of its own, with the object's fill, stroke and place, as copying it takes it.
func (v Vector) Shape(i int) (Vector, bool) {
	res := v.ToCurve()
	runs := shapeRuns(res.Path)
	if i < 0 || i >= len(runs) {
		return Vector{}, false
	}
	res.Path = cloneSubpaths(res.Path[runs[i][0]:runs[i][1]])
	res.Path[0].Op = OpJoin
	return res, true
}

// WithoutShape is the object without shape i, as cutting it leaves it; the shape after a first one taken away
// becomes the first.
func (v Vector) WithoutShape(i int) Vector {
	res := v.ToCurve()
	runs := shapeRuns(res.Path)
	if i < 0 || i >= len(runs) {
		return res
	}
	res.Path = slices.Delete(cloneSubpaths(res.Path), runs[i][0], runs[i][1])
	if len(res.Path) > 0 {
		res.Path[0].Op = OpJoin
	}
	return res
}

// Expanded is the object with its shapes baked into the one outline they make together, as Expand Shapes does.
func (v Vector) Expanded() Vector {
	if !v.Compound() {
		return v
	}
	res := v
	res.Path, res.Rule = cloneSubpaths(v.Combined()), NonZero
	return res
}

// ---- Shapes on the picture

// AddShape combines v into the node id as a shape: into a vector layer's object by op, or, on any other layer or
// group, into its vector mask, which it becomes when there is none. It is one step.
func (d *Doc) AddShape(id int, v Vector, op ShapeOp) error {
	n := d.Node(id)
	switch {
	case n == nil:
		return ErrNoLayer
	case n.Adjust != nil && n.Vector == nil:
		return ErrAdjustmentLayer
	}
	m := d.Mark()
	name := "Add vector mask"
	switch {
	case n.Vector != nil:
		next := n.Vector.WithShape(v, op)
		n.Vector, name = &next, shapeOpNames[max(op, OpAdd)]
	case n.Clip != nil:
		next := n.Clip.WithShape(v, op)
		n.Clip, name = &next, shapeOpNames[max(op, OpAdd)]
	default:
		clip := v.ToCurve()
		n.Clip = &clip
	}
	d.EditMask = false
	d.Commit(name, m)
	return nil
}

// shapeOpNames name the steps that combine a shape, by ShapeOp.
var shapeOpNames = []string{"Add shape", "Add shape", "Subtract shape", "Intersect shape", "Exclude shape"}

// SetShapeOp makes shape i of vector layer id combine by op, as one step.
func (d *Doc) SetShapeOp(id, i int, op ShapeOp) error {
	n := d.Node(id)
	if n == nil {
		return ErrNoLayer
	}
	v := n.vectorOf()
	if v == nil {
		return ErrNotVector
	}
	if i <= 0 || i >= len(v.Shapes()) || op < OpAdd || op > OpExclude {
		return ErrNoShape
	}
	m := d.Mark()
	next := v.WithShapeOp(i, op)
	n.setVectorOf(&next)
	d.Commit("Shape operation", m)
	return nil
}

// ExpandShapes bakes the shapes of vector layer id (or of its vector mask) into one outline, as one step.
func (d *Doc) ExpandShapes(id int) error {
	n := d.Node(id)
	if n == nil {
		return ErrNoLayer
	}
	v := n.vectorOf()
	if v == nil {
		return ErrNotVector
	}
	if !v.Compound() {
		return nil
	}
	m := d.Mark()
	next := v.Expanded()
	n.setVectorOf(&next)
	d.Commit("Expand shapes", m)
	return nil
}

// CombineShapes merges the chosen vector layers (ids, any order) into one, as Photoshop's Combine Shapes does: the
// bottom one keeps its place, fill and stroke, and each layer above it becomes a shape of its object that combines
// with those under it by op (OpAdd unites them, OpSubtract takes the front ones away, OpIntersect keeps where they
// all meet, OpExclude where an odd number of them are). The shapes stay editable; nothing is worked out until the
// picture is drawn, so combining costs the same however large the layers are. It is one step.
func (d *Doc) CombineShapes(op ShapeOp, ids []int) (*Node, error) {
	var nodes []*Node
	for _, l := range d.Layers() { // top first
		if slices.Contains(ids, l.ID) && l.Vector != nil {
			nodes = append(nodes, l)
		}
	}
	if len(nodes) < 2 {
		return nil, ErrBoolean
	}
	if op == OpJoin {
		op = OpAdd
	}
	slices.Reverse(nodes) // bottom first
	bottom := nodes[0]
	res := bottom.Vector.ToCurve()
	for _, n := range nodes[1:] {
		res = res.WithShape(operandFor(*n.Vector, op, res.Rule), op)
	}
	m := d.Mark()
	for _, n := range nodes[1:] {
		_, parent, i := d.find(n.ID)
		parent.Children = slices.Delete(parent.Children, i, i+1)
	}
	n := d.Node(bottom.ID)
	n.Vector = &res
	d.Active = n.ID
	d.EditMask = false
	d.Commit("Combine shapes", m)
	return n, nil
}

// operandFor is v as it can join an object filled by rule as one shape combining by op and still cover what it
// covers on its own: v itself, unless the way its own shapes combine, or its fill rule, would come out differently
// among the object's shapes, when its outline is baked into one first. Shapes only added together, added or taken
// away as one, stay as they are.
func operandFor(v Vector, op ShapeOp, rule FillRule) Vector {
	keep := v.Rule == rule || len(v.Outline()) <= 1
	if keep && v.Compound() {
		if op == OpIntersect || op == OpExclude {
			keep = false
		}
		runs := shapeRuns(v.Path)
		for _, run := range runs[1:] {
			if v.Path[run[0]].Op != OpAdd {
				keep = false
			}
		}
	}
	if keep {
		return v
	}
	res := v.ToCurve()
	res.Path, res.M, res.Rule = Combine(BoolAdd, []Vector{v}), Identity, NonZero
	return res
}

// vectorOf is what the node tool edits on n: a vector layer's object, or else a vector mask; nil for neither.
func (n *Node) vectorOf() *Vector {
	if n.Vector != nil {
		return n.Vector
	}
	return n.Clip
}

func (n *Node) setVectorOf(v *Vector) {
	if n.Vector != nil {
		n.Vector = v
		return
	}
	n.Clip = v
}

// EditedVector is what the node tool edits on n: a vector layer's object, or else its vector mask; nil for neither.
func (n *Node) EditedVector() *Vector { return n.vectorOf() }

// ---- Vector masks

// ErrNoClip means a vector mask operation was asked of a layer without one.
var ErrNoClip = errors.New("the chosen layer has no vector mask")

// DeleteClip removes node id's vector mask, as one step.
func (d *Doc) DeleteClip(id int) error {
	n := d.Node(id)
	switch {
	case n == nil:
		return ErrNoLayer
	case n.Clip == nil:
		return ErrNoClip
	}
	m := d.Mark()
	n.Clip = nil
	d.Commit("Delete vector mask", m)
	return nil
}

// RasterizeClip turns node id's vector mask into its layer mask, multiplied into the one it has, as one step.
func (d *Doc) RasterizeClip(id int) error {
	n := d.Node(id)
	switch {
	case n == nil:
		return ErrNoLayer
	case n.Clip == nil:
		return ErrNoClip
	}
	m := d.Mark()
	n.Mask, n.Clip, n.MaskOff = n.shownMask(), nil, false
	d.Commit("Rasterize vector mask", m)
	return nil
}

// clipMask is a vector mask as a layer mask: its fill's coverage over its tiles, hiding everything beyond.
func clipMask(v *Vector) *Mask {
	out := NewMask(0)
	b := clipBounds(v)
	if b.Empty() {
		return out
	}
	k0, k1 := keysOver(b)
	for ky := k0.Y; ky <= k1.Y; ky++ {
		for kx := k0.X; kx <= k1.X; kx++ {
			r := tileRect(image.Pt(kx, ky))
			out.Write(&image.Alpha{Pix: v.fillCoverage(r, 1), Stride: r.Dx(), Rect: r})
		}
	}
	return out
}

// clipBounds is where a vector mask shows anything: its fill's area, the stroke left out.
func clipBounds(v *Vector) image.Rectangle {
	w := *v
	w.Stroke = VectorStroke{}
	return w.PixelBounds()
}

// shownMask is what shows a node, as a layer mask: its mask (when on) times its vector mask; nil for everything.
func (n *Node) shownMask() *Mask {
	mask := n.activeMask()
	if n.Clip == nil {
		return mask
	}
	clip := clipMask(n.Clip)
	if mask == nil {
		return clip
	}
	out := NewMask(0)
	for k := range clip.p.tiles {
		r := tileRect(k)
		a, b := clip.Read(r), mask.Read(r)
		for i := range a.Pix {
			a.Pix[i] = uint8(mul8(uint32(a.Pix[i]), uint32(b.Pix[i])))
		}
		out.Write(a)
	}
	return out
}

// shapeAt is what shows node n over r of detail level l: its mask times its vector mask, as an alpha over r and
// the value beyond it, or nil when it shows everywhere. It narrows r to where anything shows; hidden reports that
// nothing does.
func shapeAt(n *Node, r image.Rectangle, l int) (at image.Rectangle, alpha *image.Alpha, fill uint8, hidden bool) {
	at, fill = r, 255
	mask := n.activeMask()
	if mask != nil && mask.Fill() == 0 {
		at = at.Intersect(levelDown(mask.TileBounds(), l)) // only the mask's tiles show anything
	}
	if n.Clip != nil {
		at = at.Intersect(levelDown(clipBounds(n.Clip), l))
	}
	if at.Empty() {
		return at, nil, 0, true
	}
	if mask != nil {
		alpha, fill = mask.ReadLevel(l, at), mask.Fill()
	}
	if n.Clip != nil {
		cover := n.Clip.fillCoverage(at, 1/float64(int(1)<<l))
		if alpha == nil {
			alpha = &image.Alpha{Pix: cover, Stride: at.Dx(), Rect: at}
		} else {
			for i := range alpha.Pix {
				alpha.Pix[i] = uint8(mul8(uint32(alpha.Pix[i]), uint32(cover[i])))
			}
		}
		fill = 0
	}
	return at, alpha, fill, false
}
