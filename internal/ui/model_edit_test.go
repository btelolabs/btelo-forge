package ui

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/render3d"
	s3 "github.com/btelolabs/btelo-forge/internal/scene3d"
)

// modelling opens a 3D tab with a cube being modelled, and gives its view.
func modelling(t *testing.T) (*harness, *modelDoc, viewRect) {
	t.Helper()
	x := newHarness(t)
	modelCardAt(t, x)
	md := x.a.model
	x.a.addPrimitive(md, s3.Cube)
	if md.edit == nil || md.asset == nil {
		t.Fatal("the cube is not being modelled")
	}
	x.frame(Input{})
	r := x.a.layout().body
	r.W -= x.a.u.px(264)
	return x, md, viewRect{r.X, r.Y, r.W, r.H}
}

type viewRect struct{ x, y, w, h float32 }

// clickAt presses and lets go at the point of the world p.
func clickAt(t *testing.T, x *harness, v viewRect, md *modelDoc, p s3.Vec3, extra Input) {
	t.Helper()
	px, py, ok := md.cam.Project(p, v.w, v.h)
	if !ok {
		t.Fatalf("%v is behind the camera", p)
	}
	x.mx, x.my = v.x+px, v.y+py
	in := extra
	in.Pointer, in.Down = []Pointer{{X: x.mx, Y: x.my, Down: true}}, true
	x.frame(in)
	up := extra
	up.Pointer = []Pointer{{X: x.mx, Y: x.my}}
	x.frame(up)
}

func press(x *harness, code uint16, ctrl, shift bool) {
	x.frame(Input{Keys: []Key{{Code: code, Ctrl: ctrl, Shift: shift}}})
}

// looking turns the camera to see the cube's top.
func looking(md *modelDoc) {
	md.cam.Yaw, md.cam.Pitch = .4, -.8
	md.cam.Frame(md.bounds)
}

func TestClickChoosesAFaceAndShiftAdds(t *testing.T) {
	x, md, v := modelling(t)
	looking(md)
	clickAt(t, x, v, md, s3.Vec3{0, 1, 0}, Input{})
	e := md.edit
	if len(e.faces) != 1 {
		t.Fatalf("%d faces chosen", len(e.faces))
	}
	for f := range e.faces {
		if e.em.FaceNormal(int(f)).Dot(s3.Vec3{0, 1, 0}) < .99 {
			t.Fatalf("the face chosen looks %v", e.em.FaceNormal(int(f)))
		}
	}
	if len(md.tris) == 0 {
		t.Fatal("the chosen face is not drawn")
	}
	clickAt(t, x, v, md, s3.Vec3{0, 0, 1}, Input{Shift: true})
	if len(e.faces) != 2 {
		t.Fatalf("Shift left %d faces chosen", len(e.faces))
	}
	clickAt(t, x, v, md, md.cam.Pos.Add(md.cam.Ray(8, 8, v.w, v.h).Scale(3)), Input{}) // a corner of the view, with nothing there
	if len(e.faces) != 0 {
		t.Fatalf("a click on nothing left %d chosen", len(e.faces))
	}
}

func TestChoosingVerticesAndEdges(t *testing.T) {
	x, md, v := modelling(t)
	looking(md)
	press(x, '1', false, false)
	e := md.edit
	if e.mode != selectVertices {
		t.Fatal("1 did not choose vertices")
	}
	clickAt(t, x, v, md, s3.Vec3{.98, 1, .98}, Input{})
	if len(e.verts) != 1 {
		t.Fatalf("%d vertices chosen", len(e.verts))
	}
	for at := range e.verts {
		if p := e.em.Pos[at]; p != (s3.Vec3{1, 1, 1}) {
			t.Fatalf("vertex at %v", p)
		}
	}
	press(x, '2', false, false)
	clickAt(t, x, v, md, s3.Vec3{0, 1, .98}, Input{})
	if e.mode != selectEdges || len(e.edges) != 1 {
		t.Fatalf("mode %v, %d edges", e.mode, len(e.edges))
	}
}

func TestBoxChoosesWhatIsInside(t *testing.T) {
	x, md, v := modelling(t)
	looking(md)
	press(x, '1', false, false)
	x.mx, x.my = v.x+2, v.y+2
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my, Down: true}}, Down: true})
	x.mx, x.my = v.x+v.w-2, v.y+v.h-2
	x.frame(Input{Down: true})
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}})
	if len(md.edit.verts) != 8 {
		t.Fatalf("%d vertices in the box", len(md.edit.verts))
	}
}

// Extruding follows the mouse along the face's normal, and a click keeps it; undo brings the cube back.
func TestExtrudeFollowsTheMouseAndUndoes(t *testing.T) {
	x, md, v := modelling(t)
	looking(md)
	clickAt(t, x, v, md, s3.Vec3{0, 1, 0}, Input{})
	press(x, 'E', false, false)
	e := md.edit
	if e.modal == nil {
		t.Fatal("E started nothing")
	}
	x.my -= 60
	x.frame(Input{})
	x.frame(Input{})
	top := float32(-9)
	for _, p := range e.em.Pos {
		top = max(top, p[1])
	}
	if top <= 1.05 {
		t.Fatalf("the top is at %v", top)
	}
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my, Down: true}}, Down: true})
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}})
	if e.modal != nil || !md.dirty {
		t.Fatalf("modal %v, dirty %v", e.modal, md.dirty)
	}
	if len(e.em.Faces) != 10 {
		t.Fatalf("%d faces", len(e.em.Faces))
	}
	if md.stats.Triangles != 20 {
		t.Fatalf("%d triangles", md.stats.Triangles)
	}
	press(x, 'Z', true, false)
	if len(e.em.Faces) != 6 {
		t.Fatalf("undo left %d faces", len(e.em.Faces))
	}
	press(x, 'Y', true, false)
	if len(e.em.Faces) != 10 {
		t.Fatalf("redo left %d faces", len(e.em.Faces))
	}
}

// The inner face stays chosen after an inset, so that it can be extruded at once.
func TestInsetThenExtrude(t *testing.T) {
	x, md, v := modelling(t)
	looking(md)
	clickAt(t, x, v, md, s3.Vec3{0, 1, 0}, Input{})
	press(x, 'I', false, false)
	x.mx += 70
	x.frame(Input{})
	x.frame(Input{})
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my, Down: true}}, Down: true})
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}})
	if len(md.edit.faces) != 1 {
		t.Fatalf("%d faces chosen after the inset", len(md.edit.faces))
	}
	press(x, 'E', false, false)
	if md.edit.modal == nil {
		t.Fatal("the inner face did not extrude")
	}
	press(x, keyEnter, false, false)
	if len(md.edit.em.Faces) != 14 {
		t.Fatalf("%d faces", len(md.edit.em.Faces))
	}
}

// Starting to edit by the key brings the tools forward, whatever tab of the panel was showing.
func TestEditingShowsTheToolsTab(t *testing.T) {
	x, md, _ := modelling(t)
	a := x.a
	a.leaveEdit(md)
	md.panel = modelPanelSky
	a.enterEdit(md, gfx.Rect{})
	if md.panel != modelPanelShape {
		t.Fatalf("tab %d", md.panel)
	}
}

// Each held key moves the view by the mouse alone.
func TestHeldKeysMoveTheViewWithoutAButton(t *testing.T) {
	x, md, _ := modelling(t)
	x.a.leaveEdit(md)
	x.frame(Input{})
	for _, k := range []uint16{keySpace, 'Z', 'C'} {
		pos, yaw := md.cam.Pos, md.cam.Yaw
		x.mx, x.my = 300, 300
		x.frame(Input{})
		x.frame(Input{Keys: []Key{{Code: k}}, Up: []uint16{}})
		x.frame(Input{Up: []uint16{}})
		x.mx += 60
		x.frame(Input{Up: []uint16{}})
		x.frame(Input{Up: []uint16{k}})
		settleFrames(t, x, "the view", func() bool { return !md.cam.Moving() })
		if md.cam.Pos == pos && md.cam.Yaw == yaw {
			t.Fatalf("key %d moved nothing", k)
		}
		if md.turning || md.panning || md.zooming {
			t.Fatalf("key %d is still at work", k)
		}
	}
}

func objects(t *testing.T) (*harness, *modelDoc, viewRect) {
	t.Helper()
	x, md, v := modelling(t)
	x.a.leaveEdit(md)
	looking(md)
	x.frame(Input{})
	return x, md, v
}

func TestClickChoosesAnObject(t *testing.T) {
	x, md, v := objects(t)
	md.chosen = -1
	clickAt(t, x, v, md, s3.Vec3{0, 1, 0}, Input{})
	if md.chosen != 0 {
		t.Fatalf("chosen %d", md.chosen)
	}
	if len(md.lines) == 0 || len(md.tris) == 0 {
		t.Fatal("the chosen object is not marked")
	}
	press(x, keyEscape, false, false)
	if md.chosen != -1 || len(md.lines) != 0 {
		t.Fatalf("still chosen: %d", md.chosen)
	}
}

func TestObjectMovesAndUndoBringsItBack(t *testing.T) {
	x, md, _ := objects(t)
	md.chosen = 0
	press(x, 'G', false, false)
	if md.objectTool == nil {
		t.Fatal("no tool")
	}
	press(x, 'X', false, false)
	x.mx += 80
	x.frame(Input{})
	press(x, keyEnter, false, false)
	moved := md.asset.Nodes[0].Translation
	if moved == (s3.Vec3{}) || moved[1] != 0 || moved[2] != 0 {
		t.Fatalf("at %v", moved)
	}
	press(x, 'Z', true, false)
	if md.asset.Nodes[0].Translation != (s3.Vec3{}) {
		t.Fatalf("undo left it at %v", md.asset.Nodes[0].Translation)
	}
	press(x, 'Z', true, true)
	if md.asset.Nodes[0].Translation != moved {
		t.Fatalf("redo put it at %v", md.asset.Nodes[0].Translation)
	}
}

// Undo outside modelling goes back into it, a step at a time, and then to before the shape was added.
func TestUndoGoesBackThroughModelling(t *testing.T) {
	x, md, v := modelling(t)
	looking(md)
	clickAt(t, x, v, md, s3.Vec3{0, 1, 0}, Input{})
	press(x, 'N', false, false)
	press(x, keyTab, false, false)
	if md.edit != nil {
		t.Fatal("still modelling")
	}
	press(x, 'Z', true, false)
	if md.edit == nil || len(md.edit.undo) != 0 {
		t.Fatalf("undo did not go back into modelling")
	}
	press(x, 'Z', true, true)
	if len(md.edit.undo) != 1 {
		t.Fatalf("%d steps after redo", len(md.edit.undo))
	}
}

func TestDeleteAnObjectThenUndo(t *testing.T) {
	x, md, _ := objects(t)
	md.chosen = 0
	press(x, 'X', false, false)
	if len(md.surfaces) != 0 || md.chosen != -1 {
		t.Fatalf("%d surfaces left", len(md.surfaces))
	}
	press(x, 'Z', true, false)
	if len(md.surfaces) == 0 || md.chosen != 0 {
		t.Fatalf("undo left %d surfaces, chosen %d", len(md.surfaces), md.chosen)
	}
}

func TestEveryToolHasAKey(t *testing.T) {
	x, md, v := modelling(t)
	looking(md)
	clickAt(t, x, v, md, s3.Vec3{0, 1, 0}, Input{})
	before := len(md.edit.em.Faces)
	press(x, 'D', true, false)
	if len(md.edit.em.Faces) == before {
		t.Fatal("Ctrl+D did not subdivide")
	}
	press(x, '4', false, true)
	if md.edit == nil || md.edit.node != len(md.asset.Nodes)-1 {
		t.Fatal("Shift+4 added no cylinder")
	}
}

func TestEscTakesAToolBack(t *testing.T) {
	x, md, v := modelling(t)
	looking(md)
	clickAt(t, x, v, md, s3.Vec3{0, 1, 0}, Input{})
	e := md.edit
	before := e.em.Clone()
	undo := len(e.undo)
	press(x, 'G', false, false)
	x.mx += 80
	x.frame(Input{})
	x.frame(Input{})
	if e.em.Pos[0] == before.Pos[0] && e.em.Pos[1] == before.Pos[1] && e.em.Pos[2] == before.Pos[2] && e.em.Pos[3] == before.Pos[3] &&
		e.em.Pos[4] == before.Pos[4] && e.em.Pos[5] == before.Pos[5] && e.em.Pos[6] == before.Pos[6] && e.em.Pos[7] == before.Pos[7] {
		t.Fatal("G moved nothing")
	}
	press(x, keyEscape, false, false)
	if e.modal != nil || len(e.undo) != undo {
		t.Fatalf("modal %v, %d undo steps, was %d", e.modal, len(e.undo), undo)
	}
	for i := range before.Pos {
		if e.em.Pos[i] != before.Pos[i] {
			t.Fatalf("vertex %d is at %v, was %v", i, e.em.Pos[i], before.Pos[i])
		}
	}
	if md.edit == nil {
		t.Fatal("Esc left editing while a tool was under way")
	}
}

func TestAxisHoldsTheMove(t *testing.T) {
	x, md, v := modelling(t)
	looking(md)
	clickAt(t, x, v, md, s3.Vec3{0, 1, 0}, Input{})
	e := md.edit
	y := make([]float32, len(e.em.Pos))
	for i, p := range e.em.Pos {
		y[i] = p[1]
	}
	press(x, 'G', false, false)
	press(x, 'X', false, false)
	x.mx += 90
	x.my -= 30
	x.frame(Input{})
	x.frame(Input{})
	moved := false
	for i, p := range e.em.Pos {
		if p[1] != y[i] {
			t.Fatalf("vertex %d left its height: %v", i, p)
		}
		moved = moved || p[0] > 1.01
	}
	if !moved {
		t.Fatal("nothing moved along x")
	}
	press(x, keyEnter, false, false)
}

func TestToolsNeedSomethingChosen(t *testing.T) {
	x, md, _ := modelling(t)
	for _, k := range []uint16{'E', 'I', 'G', 'R', 'T', 'X', 'M'} {
		press(x, k, false, false)
		if md.edit == nil || md.edit.modal != nil || len(md.edit.em.Faces) != 6 {
			t.Fatalf("%c with nothing chosen: %+v", k, md.edit)
		}
	}
	if len(x.a.toasts) == 0 {
		t.Fatal("no word of why nothing happened")
	}
}

func TestOtherOperationsFromTheKeys(t *testing.T) {
	x, md, v := modelling(t)
	looking(md)
	clickAt(t, x, v, md, s3.Vec3{0, 1, 0}, Input{})
	press(x, 'X', false, false)
	e := md.edit
	if len(e.em.Faces) != 5 {
		t.Fatalf("X left %d faces", len(e.em.Faces))
	}
	press(x, 'A', true, false) // everything
	x.a.setSelectMode(md, selectVertices)
	if len(e.verts) != 8 {
		t.Fatalf("%d vertices chosen", len(e.verts))
	}
	// The hole's edge, chosen by its four vertices, is filled.
	for v := range e.verts {
		if e.em.Pos[v][1] < 0 {
			delete(e.verts, v)
		}
	}
	press(x, 'F', false, true)
	if len(e.em.Faces) != 6 {
		t.Fatalf("Shift+F left %d faces", len(e.em.Faces))
	}
	press(x, keyTab, false, false)
	if md.edit != nil {
		t.Fatal("Tab did not leave editing")
	}
	x.mx, x.my = v.x+v.w/2, v.y+v.h/2
	press(x, keyTab, false, false)
	if md.edit == nil {
		t.Fatal("Tab did not go back to editing")
	}
}

func TestSaveWritesTheFileAndCloseKeepsUnsavedWork(t *testing.T) {
	x, md, _ := modelling(t)
	path := filepath.Join(t.TempDir(), "thing.glb")
	md.path = path
	if got := md.title(x.a); got != "• thing.glb" {
		t.Fatalf("title %q", got)
	}
	press(x, 'S', true, false)
	if md.dirty {
		t.Fatal("still unsaved")
	}
	a, err := s3.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if a.Stats().Triangles != 12 {
		t.Fatalf("%d triangles saved", a.Stats().Triangles)
	}

	md.dirty = true
	x.a.keepModel(md)
	if md.dirty {
		t.Fatal("the copy did not count")
	}
	kept, err := filepath.Glob(filepath.Join(x.a.stateDir, "models", "thing*.glb"))
	if err != nil || len(kept) != 1 {
		t.Fatalf("%v, %v", kept, err)
	}
}

func TestSavingAGLTFGoesToANewFile(t *testing.T) {
	x, md, _ := modelling(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "thing.glb"), []byte("taken"), 0o644); err != nil {
		t.Fatal(err)
	}
	md.path = filepath.Join(dir, "thing.gltf")
	press(x, 'S', true, false)
	if got := filepath.Base(md.path); got != "thing 2.glb" {
		t.Fatalf("saved as %q", got)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "thing.glb"))
	if string(data) != "taken" {
		t.Fatal("a file was overwritten")
	}
}

// The model drawn on a real card, with its selection: FORGE_SHOTS is where the pictures go.
func TestRealCardShowsModelling(t *testing.T) {
	dir := os.Getenv("FORGE_SHOTS")
	if dir == "" {
		t.Skip("set FORGE_SHOTS to see modelling")
	}
	realCard(t)
	x, md, v := modelling(t)
	looking(md)
	settleFrames(t, x, "a frame", func() bool { return md.picture != nil && !x.a.models.busy })
	clickAt(t, x, v, md, s3.Vec3{0, 1, 0}, Input{})
	press(x, 'I', false, false)
	x.mx += 70
	x.frame(Input{})
	x.frame(Input{})
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my, Down: true}}, Down: true})
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}})
	press(x, 'E', false, false)
	x.my -= 80
	x.frame(Input{})
	x.frame(Input{})
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my, Down: true}}, Down: true})
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}})
	md.options.View = render3d.ViewMaterial
	shot := func(name string) {
		f := md.drawn
		md.drawn = modelFrame{}
		md.picture = nil
		settleFrames(t, x, name, func() bool { return md.picture != nil && !x.a.models.busy })
		x.frame(Input{})
		_ = f
		x.shot(name)
	}
	shot("model-edit-faces")
	x.a.setSelectMode(md, selectVertices)
	shot("model-edit-vertices")
	press(x, keyTab, false, false)
	shot("model-edit-done")
}

// The top joint only links to its children, and a last bone goes on the way the one before went.
func TestSkeletonShapes(t *testing.T) {
	node := func(parent int, x, y float32, children ...int) s3.Node {
		return s3.Node{Parent: parent, Children: children, Translation: s3.Vec3{x, y, 0}, Mesh: -1, Skin: -1, Scale: s3.Vec3{1, 1, 1}, Rotation: s3.Quat{0, 0, 0, 1}}
	}
	as := &s3.Asset{Roots: []int{0}, Nodes: []s3.Node{node(-1, 0, 0, 1, 3), node(0, 0, 1, 2), node(1, 0, 1), node(0, 1, 0)},
		Skins: []s3.Skin{{Joints: []int{0, 1, 2, 3}}}}
	shapes, links := skeletonShapes(as, as.Worlds())
	if len(shapes) != 3 || len(links) != 2 {
		t.Fatalf("%d bones, %d links", len(shapes), len(links))
	}
	if shapes[0].head != (s3.Vec3{0, 1, 0}) || shapes[0].tail != (s3.Vec3{0, 2, 0}) {
		t.Fatalf("the first bone goes from %v to %v", shapes[0].head, shapes[0].tail)
	}
	if links[0][1] != (s3.Vec3{0, 1, 0}) || links[1][1] != (s3.Vec3{1, 0, 0}) {
		t.Fatalf("the top joint links to %v", links)
	}
	if shapes[1].tail != (s3.Vec3{0, 2.5, 0}) {
		t.Fatalf("the last bone ends at %v", shapes[1].tail)
	}
}

// A slider's change, a new material and its assignment each come back with undo.
func TestMaterialChangesUndo(t *testing.T) {
	x, md, _ := objects(t)
	if len(md.asset.Materials) == 0 {
		x.a.addMaterial(md)
	}
	n := len(md.asset.Materials)
	x.a.addMaterial(md)
	if len(md.asset.Materials) != n+1 || md.material != n {
		t.Fatalf("%d materials, showing %d", len(md.asset.Materials), md.material)
	}
	md.chosen = 0
	x.a.assignMaterial(md)
	if md.asset.Meshes[md.asset.Nodes[0].Mesh].Prims[0].Material != n {
		t.Fatal("the object did not take the material")
	}
	press(x, 'Z', true, false)
	if md.asset.Meshes[md.asset.Nodes[0].Mesh].Prims[0].Material == n {
		t.Fatal("undo left the material")
	}
	press(x, 'Z', true, false)
	if len(md.asset.Materials) != n {
		t.Fatalf("%d materials after undo", len(md.asset.Materials))
	}
}

func TestColourConversions(t *testing.T) {
	for _, c := range [][3]float32{{1, 0, 0}, {.2, .6, .4}, {.3, .3, .3}, {.1, .2, .9}} {
		h, s, v := lightToHSV(c)
		got := hsvToLight(h, s, v)
		for i := range c {
			if d := got[i] - c[i]; d > 1e-5 || d < -1e-5 {
				t.Fatalf("%v came back as %v", c, got)
			}
		}
	}
}

// Dragging the arrow of an axis moves along that axis alone, and letting go ends it as one step of undo.
func TestGizmoArrowMovesAlongItsAxis(t *testing.T) {
	x, md, v := objects(t)
	x.a.chooseObject(md, 0)
	x.frame(Input{})
	r := gfx.Rect{W: v.w, H: v.h}
	x0, y0, x1, y1, _, ok := x.a.gizmoPoints(md, r, 0)
	if !ok {
		t.Fatal("the handles are out of sight")
	}
	h, found := x.a.gizmoAt(md, r, (x0+x1)/2-(x1-x0)*.05, (y0+y1)/2-(y1-y0)*.05)
	if !found || h.axis != 0 || h.kind != modalMove {
		t.Fatalf("found %v %+v", found, h)
	}
	steps := len(md.undo)
	ox, oy := v.x+x0+(x1-x0)*.45, v.y+y0+(y1-y0)*.45
	x.mx, x.my = ox, oy
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: ox, Y: oy, Down: true}}})
	if md.objectTool == nil {
		t.Fatal("the press did not start the tool")
	}
	x.mx, x.my = ox+(x1-x0)/2, oy+(y1-y0)/2
	x.frame(Input{Down: true})
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}})
	got := md.asset.Nodes[0].Translation
	if md.objectTool != nil || got[0] <= 0 || got[1] != 0 || got[2] != 0 {
		t.Fatalf("tool %v, at %v", md.objectTool != nil, got)
	}
	if len(md.undo) != steps+1 {
		t.Fatalf("%d steps of undo from %d", len(md.undo), steps)
	}
}

func TestExportFBXMakesAFile(t *testing.T) {
	x, md, _ := objects(t)
	md.path = filepath.Join(t.TempDir(), "cube.glb")
	x.a.exportFBX(md)
	back, err := s3.Load(strings.TrimSuffix(md.path, ".glb") + ".fbx")
	if err != nil {
		t.Fatal(err)
	}
	if back.Stats().Triangles != md.stats.Triangles {
		t.Fatalf("%d triangles from %d", back.Stats().Triangles, md.stats.Triangles)
	}
}

// A stroke changes the picture where the object is, and undo and redo bring it back and forth.
func TestPaintingAndUndo(t *testing.T) {
	x, md, v := objects(t)
	press(x, 'U', false, false)
	md.chosen = -1
	x.a.makePicture(md, 256)
	if len(md.asset.Images) != 1 {
		t.Fatalf("%d pictures", len(md.asset.Images))
	}
	md.panel = modelPanelPaint
	x.a.brush.Color, x.a.brush.Size, x.a.brush.Opacity, x.a.brush.Flow, x.a.brush.Hardness = color.NRGBA{R: 200, A: 255}, 60, 1, 1, 1
	x.frame(Input{})
	px, py, ok := md.cam.Project(s3.Vec3{0, 1, 0}, v.w, v.h)
	if !ok {
		t.Fatal("the top is out of sight")
	}
	ox, oy := v.x+px, v.y+py
	x.mx, x.my = ox, oy
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: ox, Y: oy, Down: true}}})
	x.mx += 30
	x.frame(Input{Down: true})
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: oy}}})
	p := md.pictures[0]
	if p == nil {
		t.Fatal("no picture was opened")
	}
	red := func() int {
		n := 0
		for i := 0; i < len(p.im.Pix); i += 4 {
			if p.im.Pix[i+1] < 10 {
				n++
			}
		}
		return n
	}
	painted := red()
	if painted < 50 {
		t.Fatalf("%d texels painted", painted)
	}
	press(x, 'Z', true, false)
	if n := red(); n != 0 {
		t.Fatalf("%d texels left after undo", n)
	}
	press(x, 'Z', true, true)
	if n := red(); n != painted {
		t.Fatalf("%d texels after redo, from %d", n, painted)
	}
	md.flushPaint()
	back, _, err := image.Decode(bytes.NewReader(md.asset.Images[0].Data))
	if err != nil || back.Bounds().Dx() != 256 {
		t.Fatalf("the saved picture: %v", err)
	}
}

// A skeleton made by hand, bound, posed with a key, and all of it undone.
func TestMakeASkeletonAndKeyIt(t *testing.T) {
	x, md, _ := objects(t)
	md.chosen = 0
	x.a.objectOverlay(md)
	press(x, 'A', false, true)
	if md.bone < 0 || !md.bones {
		t.Fatal("no bone was added")
	}
	first := md.bone
	press(x, 'E', false, false)
	if md.objectTool == nil || md.bone == first {
		t.Fatal("E did not start a new bone")
	}
	x.my -= 60
	x.frame(Input{})
	press(x, keyEnter, false, false)
	if md.objectTool != nil {
		t.Fatal("the tool did not end")
	}
	head := md.asset.Worlds()[md.bone].Point(s3.Vec3{})
	if head == md.asset.Worlds()[first].Point(s3.Vec3{}) {
		t.Fatal("the new bone sits on the first")
	}
	x.a.bindMesh(md)
	if md.asset.Nodes[0].Skin != 0 {
		t.Fatal("the mesh is not bound")
	}
	x.a.newClip(md)
	if md.clip != 0 || md.playing {
		t.Fatalf("clip %d, playing %v", md.clip, md.playing)
	}
	press(x, 'R', false, false)
	x.mx += 50
	x.my += 40
	x.frame(Input{})
	press(x, keyEnter, false, false)
	if n := len(md.asset.KeyTimes(0, md.bone)); n != 1 {
		t.Fatalf("%d keys after a turn", n)
	}
	md.clipTime = 1
	press(x, 'K', false, false)
	if n := len(md.asset.KeyTimes(0, md.bone)); n != 2 {
		t.Fatalf("%d keys after K", n)
	}
	press(x, 'K', false, true)
	if n := len(md.asset.KeyTimes(0, md.bone)); n != 1 {
		t.Fatalf("%d keys after Shift+K", n)
	}
	press(x, 'Z', true, false)
	if n := len(md.asset.KeyTimes(0, md.bone)); n != 2 {
		t.Fatalf("%d keys after undo", n)
	}
}

// What is drawn on the canvas reaches the model, and what is painted on the model lands on a layer of the canvas.
func TestPictureGoesBothWaysWithTheDrawingTool(t *testing.T) {
	x, md, v := objects(t)
	press(x, 'U', false, false)
	md.chosen = -1
	x.a.makePicture(md, 128)
	md.panel = modelPanelPaint
	x.frame(Input{})
	p := x.a.pictureOf(md, md.material)
	if p == nil {
		t.Fatal("no picture")
	}
	modelTab := slices.IndexFunc(x.a.docs, func(d *doc) bool { return d.model == md })
	x.a.editInDrawing(md, p)
	ad := x.a.art
	if ad == nil || len(md.links) != 1 {
		t.Fatalf("canvas %v, %d links", ad != nil, len(md.links))
	}
	x.frame(Input{})
	green := image.NewRGBA(image.Rect(10, 10, 30, 30))
	for i := 0; i < len(green.Pix); i += 4 {
		green.Pix[i+1], green.Pix[i+3] = 255, 255
	}
	ad.d.Root.Children[0].Pixels.Write(green)
	ad.changedAll()
	x.a.switchTab(modelTab)
	x.frame(Input{})
	at := p.im.PixOffset(15, 15)
	if p.im.Pix[at] != 0 || p.im.Pix[at+1] != 255 {
		t.Fatalf("the model's picture has %v", p.im.Pix[at:at+4])
	}
	if !md.dirty || len(md.undo) == 0 {
		t.Fatal("the change is no step of undo")
	}
	x.a.brush.Color, x.a.brush.Size, x.a.brush.Opacity, x.a.brush.Flow, x.a.brush.Hardness = color.NRGBA{R: 200, A: 255}, 60, 1, 1, 1
	px, py, _ := md.cam.Project(s3.Vec3{0, 1, 0}, v.w, v.h)
	x.a.beginStroke(md, gfx.Rect{W: v.w, H: v.h}, px, py)
	x.a.endStroke(md)
	if l := md.links[0].layer; l == nil || l.Pixels.Empty() {
		t.Fatal("the stroke did not reach the canvas")
	}
	if len(ad.d.Layers()) != 2 {
		t.Fatalf("%d layers", len(ad.d.Layers()))
	}
}

func TestSeamsAndTheUVEditor(t *testing.T) {
	x, md, v := modelling(t)
	e := md.edit
	looking(md)
	press(x, '2', false, false)
	clickAt(t, x, v, md, s3.Vec3{1, 1, 0}, Input{})
	if len(e.edges) != 1 {
		t.Fatalf("%d edges chosen", len(e.edges))
	}
	press(x, 'E', true, false)
	if len(e.em.Seams) != 1 {
		t.Fatalf("%d seams", len(e.em.Seams))
	}
	press(x, 'Z', true, false)
	if len(e.em.Seams) != 0 {
		t.Fatalf("%d seams after undo", len(e.em.Seams))
	}
	press(x, 'U', false, true)
	press(x, 'V', false, false)
	if !md.uvOpen {
		t.Fatal("the editor did not open")
	}
	x.frame(Input{})
	r := x.a.layout().body
	r.W -= x.a.u.px(264)
	half := r.W / 2
	square := x.a.uvRect(rectAt(r.X+half, r.Y, half, r.H))
	f := e.em.FaceCorners(0)
	var cx, cy float32
	for _, c := range f {
		cx += c.UV[0] / float32(len(f))
		cy += c.UV[1] / float32(len(f))
	}
	before := f[0].UV
	x.mx, x.my = square.X+cx*square.W, square.Y+cy*square.H
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: x.mx, Y: x.my, Down: true}}})
	if len(e.island) == 0 {
		t.Fatal("no chart was chosen")
	}
	x.mx += 20
	x.frame(Input{Down: true})
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}})
	if got := e.em.FaceCorners(0)[0].UV; got == before {
		t.Fatal("the drag moved nothing")
	}
	steps := len(e.undo)
	press(x, 'Z', true, false)
	if got := e.em.FaceCorners(0)[0].UV; got != before || len(e.undo) != steps-1 {
		t.Fatalf("undo left the corner at %v, from %v", got, before)
	}
}

// The modifiers show while the cage stays, picking still finds the cage's faces, and leaving keeps what shows.
func TestModifiersWhileModelling(t *testing.T) {
	x, md, v := modelling(t)
	e := md.edit
	looking(md)
	e.modifiers.Subdivide = 2
	x.a.editRefresh(md, true)
	if len(e.em.Faces) != 6 {
		t.Fatalf("the cage has %d faces", len(e.em.Faces))
	}
	if md.stats.Triangles <= 12 {
		t.Fatalf("%d triangles show", md.stats.Triangles)
	}
	clickAt(t, x, v, md, s3.Vec3{0, .8, 0}, Input{})
	if len(e.faces) != 1 {
		t.Fatalf("%d faces chosen", len(e.faces))
	}
	for f := range e.faces {
		if f >= 6 {
			t.Fatalf("face %d is not of the cage", f)
		}
	}
	x.a.leaveEdit(md)
	if md.stats.Triangles <= 12 {
		t.Fatal("leaving lost the result")
	}
}

func TestDuplicateAndChooseLinked(t *testing.T) {
	x, md, _ := objects(t)
	md.chosen = 0
	x.a.objectOverlay(md)
	press(x, 'D', false, true)
	if len(md.asset.Nodes) != 2 || md.objectTool == nil {
		t.Fatalf("%d nodes", len(md.asset.Nodes))
	}
	press(x, keyEscape, false, false)
	press(x, 'Z', true, false)
	if len(md.asset.Nodes) != 1 {
		t.Fatalf("%d nodes after undo", len(md.asset.Nodes))
	}
	x.a.chooseObject(md, 0)
	press(x, keyTab, false, false)
	e := md.edit
	e.faces[0] = true
	press(x, 'L', false, false)
	if len(e.faces) != len(e.em.Faces) {
		t.Fatalf("%d faces linked of %d", len(e.faces), len(e.em.Faces))
	}
	press(x, 'I', true, false)
	if len(e.faces) != 0 {
		t.Fatalf("%d faces after inverting", len(e.faces))
	}
}

// Unwrapping keeps a picture on the model, undo brings the old picture back, and roughness goes in its own channel.
func TestPicturesFollowTheLayout(t *testing.T) {
	x, md, _ := objects(t)
	press(x, 'U', false, false)
	x.a.makePicture(md, 64)
	first := md.asset.Images[0].Data
	press(x, 'U', false, false)
	if bytes.Equal(first, md.asset.Images[0].Data) && md.asset.Images[0].Mime == "" {
		t.Fatal("the picture was not carried over")
	}
	press(x, 'Z', true, false)
	if !bytes.Equal(first, md.asset.Images[0].Data) {
		t.Fatal("undo did not bring the picture back")
	}
	md.paintChannel = 1
	x.a.makePicture(md, 32)
	m := md.asset.Materials[md.material]
	if m.MetalRoughTex.Image < 0 || m.Roughness != 1 {
		t.Fatalf("slot %d, roughness %v", m.MetalRoughTex.Image, m.Roughness)
	}
	p := x.a.pictureOf(md, md.material)
	if p == nil || p.slot != slotMetalRough {
		t.Fatal("the channel's picture did not open")
	}
}

// Weights painted with the mouse change, and undo brings them back; moving a bone with IK turns the bones above it.
func TestWeightsAndIK(t *testing.T) {
	x, md, v := objects(t)
	md.chosen = 0
	x.a.objectOverlay(md)
	press(x, 'A', false, true)
	bone := md.bone
	x.a.bindMesh(md)
	second := md.asset.AddBone(bone, md.bounds.Max)
	x.a.reshape(md, false)
	x.a.chooseBone(md, second)
	x.a.bindMesh(md)
	if md.weightJoint() < 0 {
		t.Fatal("the bone has no place in the skin")
	}
	md.weights = true
	x.a.weightOverlay(md)
	if len(md.tris) == 0 {
		t.Fatal("no weights show")
	}
	x.frame(Input{})
	x.a.brush.Size, x.a.brush.Flow = 1200, 1 // a cube has its vertices far from the middle of a face
	p := &md.asset.Meshes[0].Prims[0]
	sum := func(q *s3.Prim) (n float32) {
		for i := range q.Pos {
			n += s3.WeightOf(q, i, uint16(md.weightJoint()))
		}
		return n
	}
	before := sum(p)
	px, py, _ := md.cam.Project(md.bounds.Center(), v.w, v.h)
	x.mx, x.my = v.x+px, v.y+py
	x.frame(Input{})
	x.frame(Input{Down: true, Pointer: []Pointer{{X: x.mx, Y: x.my, Down: true}}})
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}})
	if after := sum(&md.asset.Meshes[0].Prims[0]); after <= before {
		t.Fatalf("weights %v from %v", after, before)
	}
	press(x, 'Z', true, false)
	if got := sum(&md.asset.Meshes[0].Prims[0]); got != before {
		t.Fatalf("undo left %v of %v", got, before)
	}
	md.weights = false
	x.a.objectOverlay(md)
	md.ikChain = 2
	x.a.chooseBone(md, second)
	turn := md.asset.Nodes[bone].Rotation
	press(x, 'G', false, false)
	x.mx += 60
	x.frame(Input{})
	press(x, keyEnter, false, false)
	if md.asset.Nodes[bone].Rotation == turn {
		t.Fatal("the bone above did not turn")
	}
}

// G, X, 2, Enter moves by two along x exactly, and Ctrl snaps a turn to fifteen degrees.
func TestTypedNumbersAndSnapping(t *testing.T) {
	x, md, _ := objects(t)
	x.a.chooseObject(md, 0)
	press(x, 'G', false, false)
	press(x, 'X', false, false)
	press(x, '2', false, false)
	press(x, keyEnter, false, false)
	if got := md.asset.Nodes[0].Translation; got != (s3.Vec3{2, 0, 0}) {
		t.Fatalf("at %v", got)
	}
	press(x, 'R', false, false)
	press(x, 'Z', false, false)
	press(x, '9', false, false)
	press(x, '0', false, false)
	press(x, keyEnter, false, false)
	if r := md.asset.Nodes[0].Rotation; r[3] < .70 || r[3] > .71 {
		t.Fatalf("turned %v", r)
	}
}

// With Ctrl held a move goes by whole steps of the grid.
func TestCtrlSnapsAMove(t *testing.T) {
	x, md, _ := objects(t)
	x.a.chooseObject(md, 0)
	press(x, 'G', false, false)
	x.mx += 37
	x.frame(Input{Ctrl: true})
	x.frame(Input{Ctrl: true, Keys: []Key{{Code: keyEnter}}})
	at := md.asset.Nodes[0].Translation
	if at == (s3.Vec3{}) {
		t.Fatal("it did not move")
	}
	for _, v := range at {
		if steps := float64(v) * 10; math.Abs(steps-math.Round(steps)) > 1e-3 && math.Abs(steps*10-math.Round(steps*10)) > 1e-3 {
			t.Fatalf("at %v, off the grid", at)
		}
	}
}
