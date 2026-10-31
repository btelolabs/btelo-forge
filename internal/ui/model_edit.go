package ui

import (
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	s3 "github.com/btelolabs/btelo-forge/internal/scene3d"
)

// selectMode is what clicking chooses.
type selectMode int

const (
	selectVertices selectMode = iota
	selectEdges
	selectFaces
)

// modalKind is what dragging does while a tool is under way.
type modalKind int

const (
	modalMove modalKind = iota
	modalRotate
	modalScale
	modalInset
	modalBevel
)

const (
	maxEditUndo    = 64
	pickRadius     = 24     // pixels around a vertex or an edge that still choose it
	dragStartsAt   = 3      // pixels before a press counts as a drag
	maxOverlayWire = 300000 // edges drawn in edit mode
	vertexDot      = 3.0    // half the size of a vertex on screen
)

// modelEdit is a mesh being modelled in a 3D tab.
type modelEdit struct {
	node, mesh int
	em         *s3.EditMesh
	world      s3.Mat4 // from the mesh's space to the world's
	inverse    s3.Mat4
	mode       selectMode
	verts      map[int32]bool
	edges      map[s3.Edge]bool
	faces      map[int32]bool
	undo, redo []*s3.EditMesh
	picker     *s3.Picker
	parts      [][]int32 // the face of each triangle, part by part
	modal      *modelModal
	boxing     bool // the left button is down and may become a box
	modifiers  s3.Modifiers
	soft       bool                        // a tool moves what is around the choice as well, less the farther
	softRadius float32                     // how far, in the mesh's units
	pictures   map[*s3.EditMesh][]s3.Image // the file's pictures for the states of the mesh that changed its layout
	island     []int32                     // the chart chosen in the UV editor
	uvMoving   bool                        // a step of undo is made for what the editor is doing
	boxX, boxY float32
}

// modelModal is a tool following the mouse until a click or Enter ends it.
type modelModal struct {
	kind         modalKind
	base         *s3.EditMesh // the mesh as it was
	vertices     []int32
	faces        []int32
	edges        []s3.Edge
	pivot        s3.Vec3 // in the world
	startX       float32
	startY       float32
	axis         int     // 0, 1 or 2 when held to an axis, else -1
	along        s3.Vec3 // a direction it is held to, as an extrusion is
	held         bool    // begun on a handle: letting the button go ends it
	typed        string  // a number being typed, which takes the place of the mouse
	undoOnCancel bool
}

func newEditSelection(e *modelEdit) {
	e.verts, e.edges, e.faces = map[int32]bool{}, map[s3.Edge]bool{}, map[int32]bool{}
}

// enterEdit starts modelling the mesh under the point (x, y) of the view r, or the first there is.
func (a *App) enterEdit(md *modelDoc, r gfx.Rect) {
	if md.asset == nil {
		return
	}
	a.playClip(md, -1) // modelling works on the file's own pose
	node := -1
	if md.chosen >= 0 {
		node = firstMesh(md.asset, md.chosen)
	}
	if node < 0 && md.picker != nil && r.Contains(a.u.mx, a.u.my) {
		dir := md.cam.Ray(a.u.mx-r.X, a.u.my-r.Y, r.W, r.H)
		if _, s, _, ok := md.picker.Hit(md.cam.Pos, dir); ok {
			node = md.surfaces[s].Node
		}
	}
	if node < 0 {
		node = slices.IndexFunc(md.asset.Nodes, func(n s3.Node) bool { return n.Mesh >= 0 })
	}
	if node < 0 {
		a.toast(a.u.t("This file has no mesh to edit"), true)
		return
	}
	n := &md.asset.Nodes[node]
	e := &modelEdit{node: node, mesh: n.Mesh, em: s3.EditMeshOf(&md.asset.Meshes[n.Mesh]), mode: selectFaces, world: s3.Identity(), inverse: s3.Identity()}
	e.em.DeriveSeams()
	if n.Skin < 0 {
		e.world = md.asset.Worlds()[node]
		e.inverse = e.world.Inverse()
	}
	newEditSelection(e)
	e.softRadius = max(md.bounds.Size().Length()*.25, 1e-4)
	md.edit, md.panel, md.chosen = e, modelPanelShape, node
	a.editRefresh(md, true)
}

func (a *App) leaveEdit(md *modelDoc) {
	if e := md.edit; e != nil {
		a.endModal(md, true)
		md.edit = nil
		if len(e.undo) > 0 {
			md.remember(&modelShot{edit: e})
		}
		a.objectOverlay(md)
	}
}

// reshape works out what is drawn and picked after the asset changed.
func (a *App) reshape(md *modelDoc, picker bool) {
	md.surfaces, md.bounds = md.asset.Surfaces()
	md.stats = md.asset.Stats()
	md.cam.SetScene(md.bounds)
	md.geometry++
	if picker {
		md.picker = s3.NewPicker(md.surfaces)
		md.cam.Pick = md.picker.Ray
	}
	a.requestFrame()
}

// editRefresh shows the mesh as it now is; a tool under way leaves the search structure for when it ends.
func (a *App) editRefresh(md *modelDoc, final bool) {
	e := md.edit
	var prims []s3.Prim
	if e.modifiers.Any() {
		shown, origin := e.em.Modified(e.modifiers)
		prims, e.parts = shown.Bake()
		for _, part := range e.parts {
			for i, f := range part {
				part[i] = origin[f]
			}
		}
	} else {
		prims, e.parts = e.em.Bake()
	}
	md.asset.Meshes[e.mesh].Prims = prims
	a.reshape(md, final)
	if final {
		var own []s3.Surface
		for _, s := range md.surfaces {
			if s.Node == e.node {
				own = append(own, s)
			}
		}
		e.picker = s3.NewPicker(own)
	}
	a.editOverlay(md)
}

// editOverlay draws the selection, over the surface: the edges, the vertices in vertex mode, and the chosen faces.
func (a *App) editOverlay(md *modelDoc) {
	e := md.edit
	var tris, plainLines, lines []float32
	vertex := func(out []float32, p s3.Vec3, c [4]float32, dx, dy float32) []float32 {
		return append(out, p[0], p[1], p[2], c[0], c[1], c[2], c[3], dx, dy)
	}
	chosen := [4]float32{1, .55, .1, 1}
	plain := [4]float32{.96, .97, 1, .9}
	dark := [4]float32{0, 0, 0, .6}
	at := func(v int32) s3.Vec3 { return e.world.Point(e.em.Pos[v]) }
	around := e.em.EdgeFaces()
	seam := [4]float32{1, .2, .2, 1}
	line := func(out []float32, edge s3.Edge, c [4]float32) []float32 {
		p, q := at(edge.A), at(edge.B)
		return append(out, p[0], p[1], p[2], q[0], q[1], q[2], c[0], c[1], c[2], c[3])
	}
	if len(around) <= maxOverlayWire {
		for edge := range around {
			switch {
			case e.mode != selectFaces && e.edgeChosen(edge):
				lines = line(lines, edge, chosen)
			case e.em.Seams[edge]:
				lines = line(lines, edge, seam)
			default:
				plainLines = line(plainLines, edge, plain)
			}
		}
	}
	if e.mode == selectVertices {
		for v := range e.em.Pos {
			c, size := plain, float32(vertexDot)
			if e.verts[int32(v)] {
				c, size = chosen, vertexDot+1
			}
			p := at(int32(v))
			for _, s := range [][2]float32{{-1, -1}, {1, -1}, {1, 1}, {-1, -1}, {1, 1}, {-1, 1}} {
				tris = vertex(tris, p, dark, s[0]*(size+1), s[1]*(size+1))
			}
			for _, s := range [][2]float32{{-1, -1}, {1, -1}, {1, 1}, {-1, -1}, {1, 1}, {-1, 1}} {
				tris = vertex(tris, p, c, s[0]*size, s[1]*size)
			}
		}
	}
	if e.mode == selectFaces {
		tint := [4]float32{1, .55, .1, .38}
		var split [][3]int32
		for f := range e.faces {
			c := e.em.FaceCorners(int(f))
			split = e.em.Triangles(int(f), split[:0])
			for _, t := range split {
				for _, k := range t {
					tris = vertex(tris, at(c[k].V), tint, 0, 0)
				}
			}
		}
	}
	md.tris, md.lines = tris, append(plainLines, lines...) // what is chosen goes on top
	md.overlay++
	picked := e.chosenVertices()
	md.hasPivot = len(picked) > 0
	if md.hasPivot {
		md.pivot = e.worldPivot(picked)
	}
	a.requestFrame()
}

// edgeChosen reports whether edge counts as chosen in the current mode.
func (e *modelEdit) edgeChosen(edge s3.Edge) bool {
	if e.mode == selectEdges {
		return e.edges[edge]
	}
	return e.verts[edge.A] && e.verts[edge.B]
}

// chosenVertices is the vertices the selection covers.
func (e *modelEdit) chosenVertices() []int32 {
	set := map[int32]bool{}
	switch e.mode {
	case selectVertices:
		for v := range e.verts {
			set[v] = true
		}
	case selectEdges:
		for edge := range e.edges {
			set[edge.A], set[edge.B] = true, true
		}
	case selectFaces:
		for f := range e.faces {
			for _, c := range e.em.FaceCorners(int(f)) {
				set[c.V] = true
			}
		}
	}
	out := make([]int32, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	slices.Sort(out)
	return out
}

// chosenFaces is the faces the selection covers: those with every corner chosen.
func (e *modelEdit) chosenFaces() []int32 {
	var out []int32
	if e.mode == selectFaces {
		for f := range e.faces {
			out = append(out, f)
		}
		slices.Sort(out)
		return out
	}
	set := map[int32]bool{}
	for _, v := range e.chosenVertices() {
		set[v] = true
	}
	for f := range e.em.Faces {
		c := e.em.FaceCorners(f)
		if len(c) > 0 && !slices.ContainsFunc(c, func(k s3.Corner) bool { return !set[k.V] }) {
			out = append(out, int32(f))
		}
	}
	return out
}

// chosenEdges is the edges the selection covers.
func (e *modelEdit) chosenEdges() []s3.Edge {
	var out []s3.Edge
	for edge := range e.em.EdgeFaces() {
		if e.mode == selectEdges && e.edges[edge] || e.mode == selectVertices && e.verts[edge.A] && e.verts[edge.B] {
			out = append(out, edge)
		}
	}
	if e.mode == selectFaces {
		for _, f := range e.chosenFaces() {
			c := e.em.FaceCorners(int(f))
			for i := range c {
				if edge := s3.NewEdge(c[i].V, c[(i+1)%len(c)].V); !slices.Contains(out, edge) {
					out = append(out, edge)
				}
			}
		}
	}
	return out
}

// pickAt chooses what is under (x, y) of the view r.
func (a *App) pickAt(md *modelDoc, r gfx.Rect, x, y float32, add bool) {
	e := md.edit
	if !add {
		newEditSelection(e)
	}
	dir := md.cam.Ray(x, y, r.W, r.H)
	_, s, t, ok := e.picker.Hit(md.cam.Pos, dir)
	if ok {
		a.pickFace(md, e, r, x, y, e.parts[s][t])
	}
	a.editOverlay(md)
}

func toggle[K comparable](set map[K]bool, k K) {
	if set[k] {
		delete(set, k)
	} else {
		set[k] = true
	}
}

// pickFace chooses face f, or what is nearest (x, y) of it.
func (a *App) pickFace(md *modelDoc, e *modelEdit, r gfx.Rect, x, y float32, f int32) {
	c := e.em.FaceCorners(int(f))
	near2 := func(px, py float32) float32 { return (px-x)*(px-x) + (py-y)*(py-y) }
	switch e.mode {
	case selectFaces:
		toggle(e.faces, f)
	case selectVertices:
		best, at := float32(pickRadius*pickRadius), int32(-1)
		for _, k := range c {
			if px, py, ok := md.cam.Project(e.world.Point(e.em.Pos[k.V]), r.W, r.H); ok && near2(px, py) < best {
				best, at = near2(px, py), k.V
			}
		}
		if at >= 0 {
			toggle(e.verts, at)
		}
	case selectEdges:
		best, at := float32(pickRadius*pickRadius), s3.Edge{A: -1}
		for i := range c {
			ax, ay, ok1 := md.cam.Project(e.world.Point(e.em.Pos[c[i].V]), r.W, r.H)
			j := (i + 1) % len(c)
			bx, by, ok2 := md.cam.Project(e.world.Point(e.em.Pos[c[j].V]), r.W, r.H)
			if !ok1 || !ok2 {
				continue
			}
			dx, dy := bx-ax, by-ay
			t := min(max(((x-ax)*dx+(y-ay)*dy)/max(dx*dx+dy*dy, 1e-6), 0), 1)
			if d := near2(ax+dx*t, ay+dy*t); d < best {
				best, at = d, s3.NewEdge(c[i].V, c[j].V)
			}
		}
		if at.A >= 0 {
			toggle(e.edges, at)
		}
	}
}

// pickBox chooses everything inside the box between two points of the view r.
func (a *App) pickBox(md *modelDoc, r gfx.Rect, x0, y0, x1, y1 float32, add bool) {
	e := md.edit
	if !add {
		newEditSelection(e)
	}
	lo := [2]float32{min(x0, x1), min(y0, y1)}
	hi := [2]float32{max(x0, x1), max(y0, y1)}
	inside := make([]bool, len(e.em.Pos))
	for v, p := range e.em.Pos {
		if x, y, ok := md.cam.Project(e.world.Point(p), r.W, r.H); ok {
			inside[v] = x >= lo[0] && x <= hi[0] && y >= lo[1] && y <= hi[1]
		}
	}
	switch e.mode {
	case selectVertices:
		for v, in := range inside {
			if in {
				e.verts[int32(v)] = true
			}
		}
	case selectEdges:
		for edge := range e.em.EdgeFaces() {
			if inside[edge.A] && inside[edge.B] {
				e.edges[edge] = true
			}
		}
	case selectFaces:
		for f := range e.em.Faces {
			c := e.em.FaceCorners(f)
			if len(c) > 0 && !slices.ContainsFunc(c, func(k s3.Corner) bool { return !inside[k.V] }) {
				e.faces[int32(f)] = true
			}
		}
	}
	a.editOverlay(md)
}

// chooseAll chooses everything, or nothing if something is chosen.
func (a *App) chooseAll(md *modelDoc) {
	e := md.edit
	if len(e.verts)+len(e.edges)+len(e.faces) > 0 {
		newEditSelection(e)
	} else {
		switch e.mode {
		case selectVertices:
			for v := range e.em.Pos {
				e.verts[int32(v)] = true
			}
		case selectEdges:
			for edge := range e.em.EdgeFaces() {
				e.edges[edge] = true
			}
		case selectFaces:
			for f := range e.em.Faces {
				e.faces[int32(f)] = true
			}
		}
	}
	a.editOverlay(md)
}

// setSelectMode changes what is chosen, carrying the selection across.
func (a *App) setSelectMode(md *modelDoc, mode selectMode) {
	e := md.edit
	if e.mode == mode {
		return
	}
	verts := e.chosenVertices()
	faces := e.chosenFaces()
	edges := e.chosenEdges()
	newEditSelection(e)
	e.mode = mode
	switch mode {
	case selectVertices:
		for _, v := range verts {
			e.verts[v] = true
		}
	case selectEdges:
		for _, edge := range edges {
			e.edges[edge] = true
		}
	case selectFaces:
		for _, f := range faces {
			e.faces[f] = true
		}
	}
	a.editOverlay(md)
}

// pushUndo keeps the mesh as it is, before a change.
func (e *modelEdit) pushUndo() {
	e.undo = append(e.undo, e.em.Clone())
	if len(e.undo) > maxEditUndo {
		e.undo = e.undo[1:]
	}
	e.redo = nil
}

func (a *App) editUndo(md *modelDoc, redo bool) {
	e := md.edit
	from, to := &e.undo, &e.redo
	if redo {
		from, to = to, from
	}
	if len(*from) == 0 {
		if !redo {
			a.leaveEdit(md) // what came before this run of modelling
			a.modelUndo(md, false)
		}
		return
	}
	*to = append(*to, e.em)
	e.em = (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	a.restorePictures(md)
	newEditSelection(e)
	md.dirty = true
	a.editRefresh(md, true)
}

// editChange does a change that needs no dragging, as one step of undo.
func (a *App) editChange(md *modelDoc, change func(e *modelEdit) error) {
	e := md.edit
	e.pushUndo()
	if err := change(e); err != nil {
		e.undo = e.undo[:len(e.undo)-1]
		a.toast(a.u.t(err.Error()), true)
		return
	}
	newEditSelection(e)
	md.dirty = true
	a.editRefresh(md, true)
}

// What a tool needs chosen.
const (
	hintSomething = iota
	hintFaces
	hintEdges
	hintEdge
	hintTwo
	hintThree
)

var editHints = msgs("Choose something first", "Choose faces first", "Choose edges first", "Choose an edge first", "Choose at least two vertices", "Choose at least three vertices")

// editNeed reports whether what a tool needs is chosen, and says so if not.
func (a *App) editNeed(ok bool, hint int) bool {
	if !ok {
		a.toast(a.u.t(editHints[hint]), true)
	}
	return ok
}

// worldPivot is the middle of the vertices, in the world.
func (e *modelEdit) worldPivot(vertices []int32) s3.Vec3 {
	var p s3.Vec3
	for _, v := range vertices {
		p = p.Add(e.world.Point(e.em.Pos[v]))
	}
	return p.Scale(1 / float32(max(len(vertices), 1)))
}

// startModal begins a tool following the mouse.
func (a *App) startModal(md *modelDoc, m *modelModal) {
	e := md.edit
	m.base, m.startX, m.startY = e.em.Clone(), a.u.mx, a.u.my
	if m.along == (s3.Vec3{}) {
		m.axis = -1
	}
	e.modal = m
	e.boxing = false
}

// tool starts move, rotate or scale on what is chosen.
func (a *App) tool(md *modelDoc, kind modalKind) {
	e := md.edit
	vertices := e.chosenVertices()
	if !a.editNeed(len(vertices) > 0, hintSomething) {
		return
	}
	e.pushUndo()
	a.startModal(md, &modelModal{kind: kind, vertices: vertices, pivot: e.worldPivot(vertices), undoOnCancel: true})
}

func (a *App) extrude(md *modelDoc) {
	e := md.edit
	faces := e.chosenFaces()
	if !a.editNeed(len(faces) > 0, hintFaces) {
		return
	}
	var n s3.Vec3
	for _, f := range faces {
		n = n.Add(e.em.FaceNormal(int(f)))
	}
	e.pushUndo()
	moved, region := e.em.Extrude(faces)
	if len(moved) == 0 {
		e.undo = e.undo[:len(e.undo)-1]
		return
	}
	e.mode = selectFaces
	newEditSelection(e)
	for _, f := range region {
		e.faces[f] = true
	}
	md.dirty = true
	a.editRefresh(md, false)
	along := e.world.Normal(n.Norm())
	a.startModal(md, &modelModal{kind: modalMove, vertices: moved, pivot: e.worldPivot(moved), along: along, undoOnCancel: true})
}

func (a *App) inset(md *modelDoc) {
	e := md.edit
	faces := e.chosenFaces()
	if !a.editNeed(len(faces) > 0, hintFaces) {
		return
	}
	e.pushUndo()
	a.startModal(md, &modelModal{kind: modalInset, faces: faces, pivot: e.worldPivot(e.chosenVertices()), undoOnCancel: true})
}

func (a *App) bevel(md *modelDoc) {
	e := md.edit
	edges := e.chosenEdges()
	if !a.editNeed(len(edges) > 0, hintEdges) {
		return
	}
	e.pushUndo()
	a.startModal(md, &modelModal{kind: modalBevel, edges: edges, pivot: e.worldPivot(e.chosenVertices()), undoOnCancel: true})
}

func (a *App) loopCut(md *modelDoc) {
	e := md.edit
	edges := e.chosenEdges()
	if !a.editNeed(len(edges) > 0 && e.mode == selectEdges, hintEdge) {
		return
	}
	a.editChange(md, func(e *modelEdit) error {
		for _, edge := range edges {
			if len(e.em.EdgeFaces()[edge]) > 0 { // an earlier cut may have split it
				e.em.LoopCut(edge)
			}
		}
		e.em.Compact()
		return nil
	})
}

// typedKey takes a key of a number being typed, and reports whether it was one.
func (m *modelModal) typedKey(k Key) bool {
	switch {
	case k.Code >= '0' && k.Code <= '9':
		m.typed += string(rune(k.Code))
	case k.Code >= keyNum0 && k.Code <= keyNum0+9:
		m.typed += string(rune('0' + k.Code - keyNum0))
	case k.Code == 0xBE || k.Code == 0x6E: // the full stop, on the keys and the number pad
		if !strings.Contains(m.typed, ".") {
			m.typed += "."
		}
	case k.Code == 0xBD || k.Code == 0x6D: // minus
		if strings.HasPrefix(m.typed, "-") {
			m.typed = m.typed[1:]
		} else {
			m.typed = "-" + m.typed
		}
	case k.Code == keyBack && m.typed != "":
		m.typed = m.typed[:len(m.typed)-1]
	default:
		return false
	}
	return true
}

// typedNumber is the number typed, if it is one yet.
func (m *modelModal) typedNumber() (float32, bool) {
	v, err := strconv.ParseFloat(m.typed, 32)
	return float32(v), err == nil
}

// axisKey holds the tool to an axis, or lets it go on a second press.
func (m *modelModal) axisKey(axis int) {
	if m.along != (s3.Vec3{}) || m.kind > modalScale {
		return
	}
	if m.axis == axis {
		m.axis = -1
	} else {
		m.axis = axis
	}
}

// axisOf is the direction the tool is held to, if any.
func (m *modelModal) axisOf() (s3.Vec3, bool) {
	if m.along != (s3.Vec3{}) {
		return m.along, true
	}
	if m.axis >= 0 {
		var v s3.Vec3
		v[m.axis] = 1
		return v, true
	}
	return s3.Vec3{}, false
}

func rotation(axis s3.Vec3, angle float32) s3.Quat {
	s, c := math.Sincos(float64(angle) / 2)
	v := axis.Norm().Scale(float32(s))
	return s3.Quat{v[0], v[1], v[2], float32(c)}
}

// around is a transform done about the point p.
func around(p s3.Vec3, t s3.Mat4) s3.Mat4 {
	id := s3.Quat{0, 0, 0, 1}
	one := s3.Vec3{1, 1, 1}
	return s3.Compose(p, id, one).Mul(t).Mul(s3.Compose(p.Scale(-1), id, one))
}

// modalStep applies the tool for the pointer at (x, y) of the view r to the mesh as it was.
func (a *App) modalStep(md *modelDoc, r gfx.Rect) {
	e := md.edit
	m := e.modal
	cam := md.cam
	dx, dy := a.u.mx-m.startX, a.u.my-m.startY
	depth := max(m.pivot.Sub(cam.Pos).Length(), 1e-4)
	unit := 2 * float32(math.Tan(float64(cam.FovY)/2)) * depth / r.H
	e.em = m.base.Clone()
	if t, ok := a.modalWorld(md, m, r); ok {
		if local := e.inverse.Mul(t).Mul(e.world); e.soft {
			e.em.MoveSoft(m.vertices, local, e.softRadius)
		} else {
			e.em.Move(m.vertices, local)
		}
	}
	switch m.kind {
	case modalInset:
		e.em.Inset(m.faces, min(max(float32(math.Hypot(float64(dx), float64(dy)))/300, .01), .95))
	case modalBevel:
		e.em.Bevel(m.edges, max(float32(math.Hypot(float64(dx), float64(dy)))*unit, 1e-5))
	}
	a.editRefresh(md, false)
}

// modalWorld is the move, turn or scale in the world for the pointer over the view r, if the tool is one of them.
func (a *App) modalWorld(md *modelDoc, m *modelModal, r gfx.Rect) (s3.Mat4, bool) {
	cam := md.cam
	dx, dy := a.u.mx-m.startX, a.u.my-m.startY
	depth := max(m.pivot.Sub(cam.Pos).Length(), 1e-4)
	unit := 2 * float32(math.Tan(float64(cam.FovY)/2)) * depth / r.H
	px, py, ok := cam.Project(m.pivot, r.W, r.H)
	if !ok {
		px, py = r.W/2, r.H/2
	}
	x0, y0 := m.startX-r.X, m.startY-r.Y
	x1, y1 := a.u.mx-r.X, a.u.my-r.Y
	snap := a.u.ctrl
	typed, number := m.typedNumber()
	switch m.kind {
	case modalMove:
		var move s3.Vec3
		if axis, held := m.axisOf(); held {
			// So far along the axis as the pointer went along its picture on the screen.
			ax, ay, ok1 := cam.Project(m.pivot.Add(axis.Scale(depth*.1)), r.W, r.H)
			if ok1 {
				sx, sy := ax-px, ay-py
				length := float32(math.Hypot(float64(sx), float64(sy)))
				if length > 1e-3 {
					move = axis.Scale((dx*sx + dy*sy) / (length * length) * depth * .1)
				}
			}
		} else {
			right, up, _ := cam.Axes()
			move = right.Scale(dx * unit).Add(up.Scale(-dy * unit))
		}
		if number {
			axis, held := m.axisOf()
			if !held {
				axis = s3.Vec3{1, 0, 0}
			}
			move = axis.Scale(typed)
		} else if snap {
			step := float32(math.Pow(10, math.Floor(math.Log10(float64(depth)))-1))
			for i := range move {
				move[i] = float32(math.Round(float64(move[i]/step))) * step
			}
		}
		return s3.Compose(move, s3.Quat{0, 0, 0, 1}, s3.Vec3{1, 1, 1}), true
	case modalRotate:
		angle := float32(math.Atan2(float64(y1-py), float64(x1-px)) - math.Atan2(float64(y0-py), float64(x0-px)))
		_, _, ahead := cam.Axes()
		axis := ahead
		if v, held := m.axisOf(); held {
			axis = v
			if v.Dot(ahead) < 0 {
				angle = -angle
			}
		}
		if number {
			angle = -typed * math.Pi / 180
		} else if snap {
			const step = 15 * math.Pi / 180
			angle = float32(math.Round(float64(angle)/step)) * step
		}
		return around(m.pivot, s3.Compose(s3.Vec3{}, rotation(axis, -angle), s3.Vec3{1, 1, 1})), true
	case modalScale:
		from := math.Hypot(float64(x0-px), float64(y0-py))
		factor := float32(math.Hypot(float64(x1-px), float64(y1-py)) / max(from, 1))
		if number {
			factor = typed
		} else if snap {
			factor = float32(math.Round(float64(factor)*10)) / 10
		}
		scale := s3.Vec3{factor, factor, factor}
		if m.axis >= 0 {
			scale = s3.Vec3{1, 1, 1}
			scale[m.axis] = factor
		}
		return around(m.pivot, s3.Compose(s3.Vec3{}, s3.Quat{0, 0, 0, 1}, scale)), true
	}
	return s3.Mat4{}, false
}

// endModal finishes the tool, or takes it back.
func (a *App) endModal(md *modelDoc, cancel bool) {
	e := md.edit
	m := e.modal
	if m == nil {
		return
	}
	e.modal = nil
	if cancel {
		if m.undoOnCancel && len(e.undo) > 0 {
			e.em = e.undo[len(e.undo)-1]
			e.undo = e.undo[:len(e.undo)-1]
		}
		newEditSelection(e)
	} else {
		md.dirty = true
		if m.kind == modalBevel {
			newEditSelection(e) // the faces have new numbers
		}
	}
	a.editRefresh(md, true)
}

// editMouse handles the left and right buttons in the view r while modelling; it reports whether it took them.
func (a *App) editMouse(md *modelDoc, r gfx.Rect) bool {
	u := &a.u
	e := md.edit
	if m := e.modal; m != nil {
		if e.soft && u.wheel != 0 {
			e.softRadius *= float32(math.Pow(1.15, float64(u.wheel)))
			u.wheel = 0
			a.modalStep(md, r)
		}
		if u.rightAt != nil {
			a.endModal(md, true)
			return true
		}
		if m.held && !u.down {
			a.endModal(md, false)
			return true
		}
		if u.pressed {
			u.pressed = false
			a.endModal(md, false)
			return true
		}
		if u.mx != md.lastX || u.my != md.lastY || m.startX == u.mx {
			a.modalStep(md, r)
		}
		return true
	}
	const id = "model/pick"
	if !a.modelHolding(md) && u.press(id, r) {
		e.boxing, e.boxX, e.boxY = true, u.pressX, u.pressY
	}
	if e.boxing {
		x0, y0, x1, y1 := e.boxX, e.boxY, u.mx, u.my
		far := math.Hypot(float64(x1-x0), float64(y1-y0)) > float64(u.px(dragStartsAt))
		if far && u.dragging(id) {
			box := rectAt(min(x0, x1), min(y0, y1), max(x1-x0, x0-x1), max(y1-y0, y0-y1))
			u.c.Outline(box, max(1, u.px(1)), colWhite)
		}
		if u.letGo(id) {
			e.boxing = false
			if far {
				a.pickBox(md, r, x0-r.X, y0-r.Y, x1-r.X, y1-r.Y, u.shift)
			} else {
				a.pickAt(md, r, x0-r.X, y0-r.Y, u.shift)
				if u.alt && e.mode == selectEdges {
					a.chooseLoop(md)
				}
			}
		}
		return true
	}
	return false
}

// addPrimitive adds a shape to the asset, or starts one, and models it.
func (a *App) addPrimitive(md *modelDoc, p s3.Primitive) {
	if md.asset == nil {
		md.asset = &s3.Asset{}
		md.version++
	}
	a.leaveEdit(md)
	a.playClip(md, -1)
	md.remember(md.shot())
	as := md.asset
	prims, _ := s3.NewPrimitive(p).Bake()
	as.Meshes = append(as.Meshes, s3.Mesh{Name: a.u.t(primitiveNames[p]), Prims: prims})
	as.Nodes = append(as.Nodes, s3.Node{Name: a.u.t(primitiveNames[p]), Parent: -1, Mesh: len(as.Meshes) - 1, Skin: -1, Scale: s3.Vec3{1, 1, 1}, Rotation: s3.Quat{0, 0, 0, 1}})
	as.Roots = append(as.Roots, len(as.Nodes)-1)
	md.dirty = true
	a.reshape(md, true)
	if len(as.Nodes) == 1 {
		md.cam.Frame(md.bounds)
	}
	n := len(as.Nodes) - 1
	e := &modelEdit{node: n, mesh: as.Nodes[n].Mesh, em: s3.NewPrimitive(p), mode: selectFaces, world: s3.Identity(), inverse: s3.Identity()}
	newEditSelection(e)
	e.softRadius = 1
	md.edit, md.panel, md.chosen = e, modelPanelShape, n
	a.editRefresh(md, true)
}

// primitiveNames name the shapes, in the order of scene3d's.
var primitiveNames = msgs("Cube", "Plane", "Sphere", "Cylinder")

// saveModel writes the tab's file: the one it came from if that is a .glb, else a new one beside it, never over anything
// else; work with no file yet goes where the user says, the picker opening among the creative work.
func (a *App) saveModel(md *modelDoc) {
	switch {
	case md.asset == nil:
	case strings.EqualFold(filepath.Ext(md.path), ".glb"):
		a.writeModel(md, md.path)
	case md.path != "":
		a.writeModel(md, freeName(filepath.Dir(md.path), strings.TrimSuffix(filepath.Base(md.path), filepath.Ext(md.path)), ".glb"))
	default:
		dir, err := a.categoryDir(categoryCreative)
		if err == nil {
			err = os.MkdirAll(dir, 0o755)
		}
		if err != nil {
			a.toast(a.u.tf("The file could not be saved: %s", a.describe(err)), true)
			return
		}
		a.pickSaveAs(pickRequest{title: a.u.t("Save the 3D file"), kinds: a.u.t("3D files"), exts: []string{".glb"}, dir: dir, name: a.u.t("Model") + ".glb"}, func(path string) {
			a.writeModel(md, path)
		})
	}
}

// freeName is a file in dir called base with ext, numbered if that is taken.
func freeName(dir, base, ext string) string {
	path := filepath.Join(dir, base+ext)
	for i := 2; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path
		}
		path = filepath.Join(dir, fmt.Sprintf("%s %d%s", base, i, ext))
	}
}

// writeModel writes the tab's work to path, and goes on in that file.
func (a *App) writeModel(md *modelDoc, path string) {
	md.flushPaint()
	if err := s3.Save(md.asset, path); err != nil {
		a.toast(a.u.tf("The file could not be saved: %s", a.describe(err)), true)
		return
	}
	md.path, md.dirty = path, false
	a.toast(a.u.tf("Saved %s", filepath.Base(path)), false)
}

// exportFBX writes the asset as an .fbx beside its file, or in a folder the user chooses, never over anything else.
func (a *App) exportFBX(md *modelDoc) {
	dir, base := filepath.Dir(md.path), strings.TrimSuffix(filepath.Base(md.path), filepath.Ext(md.path))
	if md.path == "" {
		var err error
		if dir, err = a.categoryDir(categoryCreative); err == nil {
			err = os.MkdirAll(dir, 0o755)
		}
		if err != nil {
			a.toast(a.u.tf("The file could not be saved: %s", a.describe(err)), true)
			return
		}
		base = a.u.t("Model")
	}
	md.flushPaint()
	path := freeName(dir, base, ".fbx")
	if err := s3.SaveFBX(md.asset, path); err != nil {
		a.toast(a.u.tf("The file could not be saved: %s", a.describe(err)), true)
		return
	}
	a.toast(a.u.tf("Saved %s", filepath.Base(path)), false)
}

// keepModel saves a tab's unsaved work as a copy among the app's files, as it closes.
func (a *App) keepModel(md *modelDoc) {
	if md == nil || !md.dirty || md.asset == nil {
		return
	}
	md.flushPaint()
	dir := filepath.Join(a.stateDir, "models")
	base := "model"
	if md.path != "" {
		base = strings.TrimSuffix(filepath.Base(md.path), filepath.Ext(md.path))
	}
	path := freeName(dir, base+time.Now().Format(" 2006-01-02 15-04-05"), ".glb")
	err := os.MkdirAll(dir, 0o755)
	if err == nil {
		err = s3.Save(md.asset, path)
	}
	if err != nil {
		a.toast(a.u.tf("The file could not be saved: %s", a.describe(err)), true)
		return
	}
	md.dirty = false
	a.toast(a.u.tf("Your unsaved 3D work is kept in %s", path), false)
}

// modelKey does what a key does in a 3D tab, and reports whether it took it.
func (a *App) modelKey(md *modelDoc, r gfx.Rect, k Key) bool {
	e := md.edit
	switch {
	case k.Code == 'S' && k.Ctrl:
		a.saveModel(md)
	case k.Shift && !k.Ctrl && !md.tooling() && k.Code >= '1' && k.Code <= '4':
		a.addPrimitive(md, s3.Primitive(k.Code-'1'))
	case k.Code == keyTab && !k.Ctrl:
		if e != nil {
			a.leaveEdit(md)
		} else {
			a.enterEdit(md, r)
		}
	case e == nil:
		return false
	case e.modal != nil:
		switch k.Code {
		case keyEscape:
			a.endModal(md, true)
		case keyEnter:
			a.endModal(md, false)
		case 'X':
			e.modal.axisKey(0)
		case 'Y':
			e.modal.axisKey(1)
		case 'Z':
			e.modal.axisKey(2)
		default:
			if !e.modal.typedKey(k) {
				return false
			}
		}
		if e.modal != nil {
			a.modalStep(md, r)
		}
	case k.Ctrl && k.Code == 'Z' && !k.Shift:
		a.editUndo(md, false)
	case k.Ctrl && (k.Code == 'Y' || k.Code == 'Z' && k.Shift):
		a.editUndo(md, true)
	case k.Ctrl && k.Code == 'A':
		a.chooseAll(md)
	case k.Ctrl && k.Code == 'I':
		a.chooseOthers(md)
	case k.Ctrl && k.Code == 'B':
		a.bevel(md)
	case k.Ctrl && k.Code == 'R':
		a.loopCut(md)
	case k.Ctrl && k.Code == 'E':
		a.markSeam(md, true)
	case k.Alt && k.Code == 'E':
		a.markSeam(md, false)
	case k.Alt && k.Code == 'J':
		a.toQuads(md)
	case k.Alt && k.Code == 'B':
		a.bridge(md)
	case k.Shift && k.Code == 'U':
		a.unwrapSeams(md)
	case k.Code == 'V':
		md.uvOpen = !md.uvOpen
	case k.Code == 'R' && md.uvOpen && len(e.island) > 0:
		a.uvEdit(md, true, func(em *s3.EditMesh, faces []int32) {
			em.TransformUV(faces, uvCenter(em, faces), math.Pi/2, 1, s3.Vec2{})
		})
	case k.Ctrl && k.Code == 'D':
		a.subdivide(md)
	case k.Shift && k.Code == 'F':
		a.fill(md)
	case k.Ctrl || k.Alt:
		return false
	case k.Code == '1':
		a.setSelectMode(md, selectVertices)
	case k.Code == '2':
		a.setSelectMode(md, selectEdges)
	case k.Code == '3':
		a.setSelectMode(md, selectFaces)
	case k.Code == 'G':
		a.tool(md, modalMove)
	case k.Code == 'R':
		a.tool(md, modalRotate)
	case k.Code == 'T':
		a.tool(md, modalScale)
	case k.Code == 'E':
		a.extrude(md)
	case k.Code == 'I':
		a.inset(md)
	case k.Code == 'M':
		a.merge(md)
	case k.Code == 'U':
		a.unwrap(md)
	case k.Code == 'L':
		a.chooseLinked(md)
	case k.Code == 'J':
		a.connect(md)
	case k.Code == 'O':
		e.soft = !e.soft
		a.toast(a.u.t(softWords[boolInt(e.soft)]), false)
	case k.Code == 'N':
		a.flip(md)
	case k.Code == 'H':
		a.shade(md, true)
	case k.Code == 'J':
		a.shade(md, false)
	case k.Code == 'X' || k.Code == keyDelete:
		a.deleteChosen(md)
	case k.Code == keyEscape:
		a.leaveEdit(md)
	default:
		return false
	}
	return true
}

func (a *App) merge(md *modelDoc) {
	vertices := md.edit.chosenVertices()
	if a.editNeed(len(vertices) > 1, hintTwo) {
		a.editChange(md, func(e *modelEdit) error { return e.em.Merge(vertices) })
	}
}

func (a *App) fill(md *modelDoc) {
	e := md.edit
	loop := e.em.BoundaryLoop(e.chosenVertices())
	if a.editNeed(len(loop) > 2, hintThree) {
		a.editChange(md, func(e *modelEdit) error {
			_, err := e.em.Fill(loop)
			return err
		})
	}
}

func (a *App) deleteChosen(md *modelDoc) {
	e := md.edit
	switch e.mode {
	case selectFaces:
		if faces := e.chosenFaces(); a.editNeed(len(faces) > 0, hintSomething) {
			a.editChange(md, func(e *modelEdit) error { e.em.DeleteFaces(faces); return nil })
		}
	default:
		if vertices := e.chosenVertices(); a.editNeed(len(vertices) > 0, hintSomething) {
			a.editChange(md, func(e *modelEdit) error { e.em.DeleteVertices(vertices); return nil })
		}
	}
}

// drawEditPanel is the tools of the panel while modelling; it returns where it ends.
func (a *App) drawEditPanel(md *modelDoc, x, y, w float32) float32 {
	u := &a.u
	e := md.edit
	modes := []string{u.t("Vertices") + " 1", u.t("Edges") + " 2", u.t("Faces") + " 3"}
	if m := selectMode(u.chips("model/select", rectAt(x, y, w, u.px(28)), modes, int(e.mode), e.modal == nil)); m != e.mode {
		a.setSelectMode(md, m)
	}
	y += u.px(36)
	half := (w - u.px(6)) / 2
	button := func(column int, id, text, key string, run func()) {
		r := rectAt(x+float32(column)*(half+u.px(6)), y, half, u.px(28))
		if a.modelTool("model/"+id, r, text, key, e.modal == nil) {
			run()
		}
	}
	rows := [][2]struct {
		id, text, key string
		run           func()
	}{
		{{"move", u.t("Move"), "G", func() { a.tool(md, modalMove) }}, {"rotate", u.t("Rotate"), "R", func() { a.tool(md, modalRotate) }}},
		{{"scale", u.t("Scale"), "T", func() { a.tool(md, modalScale) }}, {"extrude", u.t("Extrude"), "E", func() { a.extrude(md) }}},
		{{"inset", u.t("Inset"), "I", func() { a.inset(md) }}, {"bevel", u.t("Bevel"), "Ctrl+B", func() { a.bevel(md) }}},
		{{"loop", u.t("Loop cut"), "Ctrl+R", func() { a.loopCut(md) }}, {"subdivide", u.t("Subdivide"), "Ctrl+D", func() { a.subdivide(md) }}},
		{{"merge", u.t("Merge"), "M", func() { a.merge(md) }}, {"fill", u.t("Fill"), "Shift+F", func() { a.fill(md) }}},
		{{"delete", u.t("Delete"), "X", func() { a.deleteChosen(md) }}, {"flip", u.t("Flip normals"), "N", func() { a.flip(md) }}},
		{{"flat", u.t("Shade flat"), "H", func() { a.shade(md, true) }}, {"smooth", u.t("Shade smooth"), "J", func() { a.shade(md, false) }}},
		{{"seam", u.t("Seam"), "Ctrl+E", func() { a.markSeam(md, true) }}, {"unseam", u.t("No seam"), "Alt+E", func() { a.markSeam(md, false) }}},
		{{"connect", u.t("Connect"), "J", func() { a.connect(md) }}, {"bridge", u.t("Bridge"), "Alt+B", func() { a.bridge(md) }}},
		{{"quads", u.t("To quads"), "Alt+J", func() { a.toQuads(md) }}, {"linked", u.t("Choose linked"), "L", func() { a.chooseLinked(md) }}},
		{{"unwrap", u.t("Unwrap"), "Shift+U", func() { a.unwrapSeams(md) }}, {"uv", u.t("UV editor"), "V", func() { md.uvOpen = !md.uvOpen }}},
		{{"undo", u.t("Undo"), "Ctrl+Z", func() { a.editUndo(md, false) }}, {"redo", u.t("Redo"), "Ctrl+Shift+Z", func() { a.editUndo(md, true) }}},
	}
	for _, row := range rows {
		for c, b := range row {
			button(c, b.id, b.text, b.key, b.run)
		}
		y += u.px(32)
	}
	return a.drawModifiers(md, x, y+u.px(4), w)
}

// drawModifiers is the mirror and subdivision of the mesh being modelled, which come into it on leaving.
func (a *App) drawModifiers(md *modelDoc, x, y, w float32) float32 {
	u := &a.u
	e := md.edit
	u.label(u.bold(12), rectAt(x, y, w, u.px(20)), u.t("Modifiers"), colDim, 0)
	y += u.px(22)
	before := e.modifiers
	u.label(u.face(12), rectAt(x, y, w, u.px(18)), u.t("Mirror"), colDim, 0)
	y += u.px(20)
	third := (w - u.px(12)) / 3
	for i, name := range []string{"X", "Y", "Z"} {
		e.modifiers.Mirror[i] = u.checkbox(fmt.Sprintf("model/mirror%d", i), rectAt(x+float32(i)*(third+u.px(6)), y, third, u.px(26)), name, e.modifiers.Mirror[i])
	}
	y += u.px(30)
	u.label(u.face(12), rectAt(x, y, w, u.px(18)), u.t("Subdivision"), colDim, 0)
	y += u.px(20)
	e.modifiers.Subdivide = u.chips("model/subdivide", rectAt(x, y, w, u.px(26)), []string{"0", "1", "2", "3"}, e.modifiers.Subdivide, e.modal == nil)
	y += u.px(32)
	if e.modifiers != before {
		md.dirty = true
		a.editRefresh(md, true)
	}
	return y + u.px(4)
}

// toQuads joins the mesh's triangles into quads where they make good ones.
func (a *App) toQuads(md *modelDoc) {
	joined := 0
	a.editChange(md, func(e *modelEdit) error { joined = e.em.TrisToQuads(40); return nil })
	a.toast(a.u.tf("%d quads made", joined), false)
}

// softWords say whether a tool moves what is around.
var softWords = msgs("Tools move the choice alone", "Tools move what is around too: the wheel sets how far")

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// chooseLoop goes on from the edges chosen along their lines.
func (a *App) chooseLoop(md *modelDoc) {
	e := md.edit
	for edge := range maps.Clone(e.edges) {
		for _, o := range e.em.EdgeLoop(edge) {
			e.edges[o] = true
		}
	}
	a.editOverlay(md)
}

// connect joins the two vertices chosen with a new edge across their face.
func (a *App) connect(md *modelDoc) {
	vertices := md.edit.chosenVertices()
	if a.editNeed(len(vertices) == 2, hintTwo) {
		a.editChange(md, func(e *modelEdit) error {
			if err := e.em.ConnectVertices(vertices[0], vertices[1]); err != nil {
				return err
			}
			e.em.Compact()
			return nil
		})
	}
}

// bridge joins the two lines of edges chosen.
func (a *App) bridge(md *modelDoc) {
	edges := md.edit.chosenEdges()
	if a.editNeed(len(edges) > 1, hintEdges) {
		a.editChange(md, func(e *modelEdit) error {
			_, err := e.em.Bridge(edges)
			return err
		})
	}
}

// chooseLinked adds all that is joined to what is chosen.
func (a *App) chooseLinked(md *modelDoc) {
	e := md.edit
	around := e.em.EdgeFaces()
	seen := map[int32]bool{}
	queue := e.chosenFaces()
	for _, v := range e.chosenVertices() {
		for f := range e.em.Faces {
			if slices.ContainsFunc(e.em.FaceCorners(f), func(c s3.Corner) bool { return c.V == v }) {
				queue = append(queue, int32(f))
				break
			}
		}
	}
	for len(queue) > 0 {
		f := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if seen[f] {
			continue
		}
		seen[f] = true
		c := e.em.FaceCorners(int(f))
		for i := range c {
			queue = append(queue, around[s3.NewEdge(c[i].V, c[(i+1)%len(c)].V)]...)
		}
	}
	for f := range seen {
		switch e.mode {
		case selectFaces:
			e.faces[f] = true
		case selectVertices:
			for _, c := range e.em.FaceCorners(int(f)) {
				e.verts[c.V] = true
			}
		case selectEdges:
			c := e.em.FaceCorners(int(f))
			for i := range c {
				e.edges[s3.NewEdge(c[i].V, c[(i+1)%len(c)].V)] = true
			}
		}
	}
	a.editOverlay(md)
}

// chooseOthers turns the choice around.
func (a *App) chooseOthers(md *modelDoc) {
	e := md.edit
	switch e.mode {
	case selectFaces:
		next := map[int32]bool{}
		for f := range e.em.Faces {
			if e.em.Faces[f].Count > 0 && !e.faces[int32(f)] {
				next[int32(f)] = true
			}
		}
		e.faces = next
	case selectVertices:
		next := map[int32]bool{}
		for v := range e.em.Pos {
			if !e.verts[int32(v)] {
				next[int32(v)] = true
			}
		}
		e.verts = next
	case selectEdges:
		next := map[s3.Edge]bool{}
		for edge := range e.em.EdgeFaces() {
			if !e.edges[edge] {
				next[edge] = true
			}
		}
		e.edges = next
	}
	a.editOverlay(md)
}

func (a *App) subdivide(md *modelDoc) {
	faces := md.edit.chosenFaces()
	if a.editNeed(len(faces) > 0, hintFaces) {
		a.editChange(md, func(e *modelEdit) error { e.em.Subdivide(faces); e.em.Compact(); return nil })
	}
}

func (a *App) flip(md *modelDoc) {
	faces := md.edit.chosenFaces()
	if a.editNeed(len(faces) > 0, hintFaces) {
		a.editChange(md, func(e *modelEdit) error { e.em.FlipFaces(faces); return nil })
	}
}

func (a *App) shade(md *modelDoc, flat bool) {
	faces := md.edit.chosenFaces()
	if a.editNeed(len(faces) > 0, hintFaces) {
		a.editChange(md, func(e *modelEdit) error { e.em.SetFlat(faces, flat); return nil })
	}
}
