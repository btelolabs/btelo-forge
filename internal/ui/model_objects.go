package ui

import (
	"fmt"
	"math"
	"slices"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/render3d"
	s3 "github.com/btelolabs/btelo-forge/internal/scene3d"
)

const (
	maxObjectsHigh = 200 // logical pixels the list of objects grows to before it scrolls
	maxModelUndo   = 64
)

// modelShot is a step of a tab's undo outside modelling: the asset as it was, or a run of modelling, which going back
// into brings back its own steps. Meshes' parts are replaced and never changed in place, so a shot shares them.
type modelShot struct {
	nodes     []s3.Node
	meshes    []s3.Mesh
	roots     []int
	materials []s3.Material
	images    []s3.Image
	clips     []s3.Clip
	posed     []s3.Node // the pose an IK solve starts from
	skins     []s3.Skin
	chosen    int
	edit      *modelEdit
	paint     *paintPatch
}

// objectTool is an object following the mouse until a click or Enter ends it.
type objectTool struct {
	modelModal
	node     int
	before   s3.Node
	world    s3.Mat4 // the object's when the tool began
	outside  s3.Mat4 // from the world to the object's parent
	bone     bool    // it is a bone: see startBoneTool
	stepless bool    // the step of undo was made before the tool began
	nodes    []s3.Node
	clips    []s3.Clip
	posed    []s3.Node // the pose an IK solve starts from
}

// tooling reports whether a tool is following the mouse.
func (md *modelDoc) tooling() bool {
	return md.objectTool != nil || md.edit != nil && md.edit.modal != nil
}

func (md *modelDoc) shot() *modelShot {
	md.flushPaint()
	nodes := slices.Clone(md.asset.Nodes)
	for i := range nodes {
		nodes[i].Children = slices.Clone(nodes[i].Children)
	}
	return &modelShot{nodes: nodes, meshes: slices.Clone(md.asset.Meshes), roots: slices.Clone(md.asset.Roots), materials: md.asset.Materials, images: md.asset.Images, clips: md.asset.Clips, skins: md.asset.Skins, chosen: md.chosen}
}

// remember adds a step of undo.
func (md *modelDoc) remember(s *modelShot) {
	md.undo = append(md.undo, s)
	if len(md.undo) > maxModelUndo {
		md.undo = md.undo[1:]
	}
	md.redo = nil
}

// modelUndo goes a step back or, with redo, forward. A run of modelling is gone back into, as its steps are its own.
func (a *App) modelUndo(md *modelDoc, redo bool) {
	from, to := &md.undo, &md.redo
	if redo {
		from, to = to, from
	}
	if len(*from) == 0 || md.asset == nil {
		return
	}
	s := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	if s.paint != nil {
		*to = append(*to, &modelShot{paint: a.undoPaint(md, s.paint)})
		return
	}
	if s.edit != nil {
		md.edit, md.panel, md.chosen = s.edit, modelPanelShape, s.edit.node
		a.editRefresh(md, true)
		a.editUndo(md, redo)
		return
	}
	*to = append(*to, md.shot())
	if len(s.images) != len(md.asset.Images) || !sameTextures(s.materials, md.asset.Materials) {
		md.version++
	}
	md.asset.Nodes, md.asset.Meshes, md.asset.Roots, md.chosen = s.nodes, s.meshes, s.roots, s.chosen
	md.asset.Materials, md.asset.Images, md.asset.Clips, md.asset.Skins = s.materials, s.images, s.clips, s.skins
	if md.clip >= len(s.clips) {
		a.playClip(md, -1)
	}
	if md.bone >= len(s.nodes) {
		md.bone = -1
		md.hasPivot = false
	}
	md.poseEdits++
	md.material = min(md.material, len(s.materials)-1)
	md.materials++
	md.dirty = true
	a.reshape(md, true)
	a.objectOverlay(md)
	md.updateBonePivot()
}

// holdsMesh reports whether node or anything under it has a mesh.
func holdsMesh(as *s3.Asset, node int) bool {
	return as.Nodes[node].Mesh >= 0 || slices.ContainsFunc(as.Nodes[node].Children, func(c int) bool { return holdsMesh(as, c) })
}

// firstMesh is the node with a mesh at node or under it, or -1.
func firstMesh(as *s3.Asset, node int) int {
	if as.Nodes[node].Mesh >= 0 {
		return node
	}
	for _, c := range as.Nodes[node].Children {
		if n := firstMesh(as, c); n >= 0 {
			return n
		}
	}
	return -1
}

// underChosen marks the chosen object and what hangs from it.
func (md *modelDoc) underChosen() map[int]bool {
	marked := map[int]bool{}
	var mark func(n int)
	mark = func(n int) {
		marked[n] = true
		for _, c := range md.asset.Nodes[n].Children {
			mark(c)
		}
	}
	if md.asset != nil && md.chosen >= 0 && md.chosen < len(md.asset.Nodes) {
		mark(md.chosen)
	}
	return marked
}

// objectOverlay marks the chosen object in the view, unless a mesh is being modelled.
func (a *App) objectOverlay(md *modelDoc) {
	if md.edit != nil {
		a.editOverlay(md)
		return
	}
	var tris []float32
	var own []s3.Surface
	marked := md.underChosen()
	tint := [4]float32{1, .55, .1, .16}
	box := s3.EmptyBox()
	for _, s := range md.surfaces {
		if !marked[s.Node] {
			continue
		}
		own = append(own, s)
		for _, i := range s.Indices {
			p := s.Pos[i]
			box.Grow(p)
			tris = append(tris, p[0], p[1], p[2], tint[0], tint[1], tint[2], tint[3], 0, 0)
		}
	}
	md.hasPivot = !box.Empty()
	if md.hasPivot {
		md.pivot = box.Center()
	}
	md.tris, md.lines = tris, render3d.MeshLines(own, [4]float32{1, .55, .1, .5})
	md.overlay++
	a.requestFrame()
}

// chooseObject makes node the chosen object, or none for -1; while modelling it models that one instead.
func (a *App) chooseObject(md *modelDoc, node int) {
	if node >= 0 {
		md.lastObject, md.bone = node, -1
	}
	if md.edit != nil {
		if node < 0 || firstMesh(md.asset, node) == md.edit.node {
			return
		}
		a.leaveEdit(md)
		md.chosen = node
		a.enterEdit(md, gfx.Rect{})
		return
	}
	md.chosen = node
	a.objectOverlay(md)
}

// pickObject chooses what is under the point (x, y) of the view r.
func (a *App) pickObject(md *modelDoc, r gfx.Rect, x, y float32) {
	if md.picker == nil {
		return
	}
	if _, s, _, ok := md.picker.Hit(md.cam.Pos, md.cam.Ray(x, y, r.W, r.H)); ok {
		a.chooseObject(md, md.surfaces[s].Node)
		return
	}
	a.chooseObject(md, -1)
}

// deleteObject takes the chosen object out of what is shown. Its nodes stay, empty, as skins and animations count them.
func (a *App) deleteObject(md *modelDoc) {
	if md.chosen < 0 {
		a.toast(a.u.t(editHints[hintSomething]), true)
		return
	}
	a.playClip(md, -1)
	md.remember(md.shot())
	for n := range md.underChosen() {
		md.asset.Nodes[n].Mesh = -1
	}
	md.chosen, md.dirty = -1, true
	a.reshape(md, true)
	a.objectOverlay(md)
}

// duplicateObject copies the chosen object and picks it up.
func (a *App) duplicateObject(md *modelDoc) {
	node := firstMesh(md.asset, md.chosen)
	if node < 0 {
		return
	}
	md.remember(md.shot())
	copied := md.asset.DuplicateNode(node)
	md.dirty = true
	a.reshape(md, true)
	a.chooseObject(md, copied)
	a.startObjectTool(md, modalMove)
	if t := md.objectTool; t != nil {
		md.undo = md.undo[:len(md.undo)-1] // the copy and its move are one step
		t.stepless = true
	}
}

// startObjectTool starts move, rotate or scale on the chosen object.
func (a *App) startObjectTool(md *modelDoc, kind modalKind) {
	if md.bone >= 0 {
		a.startBoneTool(md, kind)
		return
	}
	if md.chosen < 0 {
		a.toast(a.u.t(editHints[hintSomething]), true)
		return
	}
	a.playClip(md, -1) // a tool works on the file's own pose
	box := s3.EmptyBox()
	skinned := false
	marked := md.underChosen()
	for _, s := range md.surfaces {
		if marked[s.Node] {
			skinned = skinned || md.asset.Nodes[s.Node].Skin >= 0
			for _, p := range s.Pos {
				box.Grow(p)
			}
		}
	}
	if skinned {
		a.toast(a.u.t("A skinned object goes where its bones go"), true)
		return
	}
	n := &md.asset.Nodes[md.chosen]
	worlds := md.asset.Worlds()
	t := &objectTool{node: md.chosen, before: *n, world: worlds[md.chosen], outside: s3.Identity()}
	if n.Parent >= 0 {
		t.outside = worlds[n.Parent].Inverse()
	}
	t.kind, t.axis, t.startX, t.startY = kind, -1, a.u.mx, a.u.my
	t.pivot = worlds[md.chosen].Point(s3.Vec3{})
	if !box.Empty() {
		t.pivot = box.Center()
	}
	md.objectTool = t
	md.remember(md.shot())
}

// objectToolStep places the object for the pointer over the view r.
func (a *App) objectToolStep(md *modelDoc, r gfx.Rect) {
	t := md.objectTool
	if t.bone {
		a.boneToolStep(md, r)
		return
	}
	md.asset.Nodes = slices.Clone(md.asset.Nodes)
	n := &md.asset.Nodes[t.node]
	world, _ := a.modalWorld(md, &t.modelModal, r)
	n.Translation, n.Rotation, n.Scale = s3.Decompose(t.outside.Mul(world).Mul(t.world))
	a.reshape(md, false)
	a.objectOverlay(md)
}

// endObjectTool finishes the tool, or takes it back.
func (a *App) endObjectTool(md *modelDoc, cancel bool) {
	t := md.objectTool
	if t == nil {
		return
	}
	if t.bone {
		a.endBoneTool(md, cancel)
		return
	}
	md.objectTool = nil
	if cancel {
		md.asset.Nodes = slices.Clone(md.asset.Nodes)
		md.asset.Nodes[t.node] = t.before
		if !t.stepless {
			md.undo = md.undo[:len(md.undo)-1]
		}
	} else {
		md.dirty = true
	}
	a.reshape(md, true)
	a.objectOverlay(md)
}

// objectMouse handles the left and right buttons in the view r outside modelling.
func (a *App) objectMouse(md *modelDoc, r gfx.Rect) {
	u := &a.u
	if t := md.objectTool; t != nil {
		switch {
		case u.rightAt != nil:
			a.endObjectTool(md, true)
		case t.held && !u.down:
			a.endObjectTool(md, false)
		case u.pressed:
			u.pressed = false
			a.endObjectTool(md, false)
		case u.mx != md.lastX || u.my != md.lastY:
			a.objectToolStep(md, r)
		}
		return
	}
	const id = "model/object"
	if !a.modelHolding(md) {
		u.press(id, r)
	}
	if u.letGo(id) && !u.drag.moved {
		if md.bones {
			if b := a.pickBone(md, r, u.mx-r.X, u.my-r.Y); b >= 0 {
				a.chooseBone(md, b)
				return
			}
			a.chooseBone(md, -1)
		}
		a.pickObject(md, r, u.mx-r.X, u.my-r.Y)
	}
}

// drawShapeKeys is a slider for each shape key of the chosen object; it returns where it ends.
func (a *App) drawShapeKeys(md *modelDoc, x, y, w float32) float32 {
	u := &a.u
	if md.asset == nil || md.chosen < 0 || md.edit != nil {
		return y
	}
	node := firstMesh(md.asset, md.chosen)
	if node < 0 {
		return y
	}
	mesh := &md.asset.Meshes[md.asset.Nodes[node].Mesh]
	count := mesh.KeyCount()
	if count == 0 {
		return y
	}
	u.label(u.bold(12), rectAt(x, y, w, u.px(20)), u.t("Shape keys"), colDim, 0)
	y += u.px(22)
	strengths := make([]float32, count)
	copy(strengths, md.asset.StrengthsOf(node))
	for k := range count {
		name := u.tf("Key %d", k+1)
		if k < len(mesh.Keys) && mesh.Keys[k] != "" {
			name = mesh.Keys[k]
		}
		f := u.face(12)
		u.label(f, rectAt(x, y, w, u.px(18)), f.Ellipsize(name, w), colDim, 0)
		y += u.px(18)
		value := float64(strengths[k])
		id := fmt.Sprintf("model/key/%d", k)
		if changed, done := u.slider(id, rectAt(x, y, w, u.px(22)), &value, 0, 1, true); changed || done {
			if !md.keyDrag {
				md.keyDrag = true
				md.remember(md.shot())
			}
			strengths[k] = float32(value)
			mesh.Weights = slices.Clone(strengths) // the undo step holds the slice before
			md.asset.Nodes[node].Weights = nil
			md.dirty = true
			a.reshape(md, false)
			if done {
				md.keyDrag = false
				a.objectOverlay(md)
			}
		}
		y += u.px(24)
	}
	return y + u.px(6)
}

// modelSpeeds are the choices of how fast a clip plays.
var modelSpeeds = []float64{.25, .5, 1, 2}

// stepClip moves the clip on by dt seconds, round to its start at the end.
func (a *App) stepClip(md *modelDoc, dt float32) {
	if !md.playing || md.clip < 0 || a.u.active == "model/clip/time" {
		return
	}
	d := float64(md.asset.Clips[md.clip].Duration())
	if d <= 0 {
		return
	}
	md.clipTime = math.Mod(md.clipTime+float64(dt)*modelSpeeds[md.speed], d)
	md.updateBonePivot()
}

// playClip shows clip from its start, or stops showing it for -1.
func (a *App) playClip(md *modelDoc, clip int) {
	md.clip, md.clipTime, md.playing = clip, 0, clip >= 0
	a.requestFrame()
}

// drawModelClips is the panel's tab for the file's animations and its skeleton.
func (a *App) drawModelClips(md *modelDoc, x, y, w float32) float32 {
	u := &a.u
	if md.asset == nil {
		return y
	}
	y = a.drawModelRig(md, x, y, w)
	bones := md.asset.Bones()
	if len(bones) > 0 {
		md.bones = u.checkbox("model/bones", rectAt(x, y, w, u.px(26)), u.tf("Show the bones (%d)", len(bones)), md.bones)
		y += u.px(34)
	}
	clips := md.asset.Clips
	if len(clips) == 0 {
		u.label(u.face(12.5), rectAt(x, y, w, u.px(20)), u.t("This file has no animations"), colDim, 0)
		return y + u.px(24)
	}
	half := (w - u.px(6)) / 2
	label := u.t("Play")
	if md.playing {
		label = u.t("Pause")
	}
	if a.modelTool("model/clip/play", rectAt(x, y, half, u.px(28)), label, "P", md.clip >= 0) {
		md.playing = !md.playing
	}
	if a.modelTool("model/clip/stop", rectAt(x+half+u.px(6), y, half, u.px(28)), u.t("Stop"), "Esc", md.clip >= 0) {
		a.playClip(md, -1)
	}
	y += u.px(34)
	if md.clip >= 0 {
		c := &clips[md.clip]
		if changed, _ := u.slider("model/clip/time", rectAt(x, y, w, u.px(22)), &md.clipTime, 0, md.clipLength(), true); changed {
			md.updateBonePivot()
		}
		y += u.px(22)
		row := rectAt(x, y, w, u.px(18))
		u.label(u.face(11.5), row, fmt.Sprintf("%.2f / %.2f s", md.clipTime, max(float64(c.Duration()), md.clipLength())), colDim, 1)
		y += u.px(24)
	}
	md.wander = !u.checkbox("model/clip/place", rectAt(x, y, w, u.px(26)), u.t("Stay in place"), !md.wander)
	y += u.px(30)
	speeds := make([]string, len(modelSpeeds))
	for i, s := range modelSpeeds {
		speeds[i] = fmt.Sprintf("%g×", s)
	}
	md.speed = u.chips("model/clip/speed", rectAt(x, y, w, u.px(26)), speeds, md.speed, true)
	y += u.px(34)
	f := u.face(12.5)
	high := u.px(26)
	for i := range clips {
		r := rectAt(x, y, w, high)
		y += high
		id := fmt.Sprintf("model/clip/%d", i)
		if u.hover(id, r) {
			u.c.Fill(r, colBorder)
		}
		colour := colText
		if md.clip == i {
			u.c.Fill(r, modelHue)
			colour = colBackground
		}
		if u.press(id, r) {
			a.playClip(md, i)
		}
		name := clips[i].Name
		if name == "" {
			name = u.tf("Animation %d", i+1)
		}
		u.label(f, rectAt(x+u.px(8), r.Y, w-u.px(16), high), f.Ellipsize(name, w-u.px(16)), colour, 0)
	}
	return y
}

// objectKey does what a key does outside modelling, and reports whether it took it.
func (a *App) objectKey(md *modelDoc, r gfx.Rect, k Key) bool {
	if t := md.objectTool; t != nil {
		switch k.Code {
		case keyEscape:
			a.endObjectTool(md, true)
		case keyEnter:
			a.endObjectTool(md, false)
		case 'X':
			t.axisKey(0)
		case 'Y':
			t.axisKey(1)
		case 'Z':
			t.axisKey(2)
		default:
			if !t.typedKey(k) {
				return false
			}
		}
		if md.objectTool != nil {
			a.objectToolStep(md, r)
		}
		return true
	}
	switch {
	case k.Ctrl && k.Code == 'Z':
		a.modelUndo(md, k.Shift)
	case k.Ctrl && k.Code == 'Y':
		a.modelUndo(md, true)
	case k.Ctrl && k.Code == 'P':
		a.bindMesh(md)
	case k.Ctrl || k.Alt:
		return false
	case k.Code == 'D' && k.Shift && md.chosen >= 0:
		a.duplicateObject(md)
	case k.Code == 'A' && k.Shift:
		a.addBone(md)
	case k.Code == 'K' && k.Shift:
		a.unkeyBone(md)
	case k.Code == 'K':
		a.keyBone(md)
	case k.Code == 'E' && md.bone >= 0:
		a.addBone(md)
		a.startBoneTool(md, modalMove)
	case k.Code == keyLeft && md.clip >= 0:
		a.stepPlayhead(md, -1)
	case k.Code == keyRight && md.clip >= 0:
		a.stepPlayhead(md, 1)
	case k.Code == 'P' && md.clip >= 0:
		md.playing = !md.playing
	case k.Code == keyEscape && md.clip >= 0:
		a.playClip(md, -1)
	case k.Code == 'U':
		a.unwrap(md)
	case k.Code == 'G':
		a.startObjectTool(md, modalMove)
	case k.Code == 'R':
		a.startObjectTool(md, modalRotate)
	case k.Code == 'T':
		a.startObjectTool(md, modalScale)
	case k.Code == 'X' || k.Code == keyDelete:
		a.deleteObject(md)
	case k.Code == keyEscape && md.bone >= 0:
		a.chooseBone(md, -1)
	case k.Code == keyEscape && md.chosen >= 0:
		a.chooseObject(md, -1)
	default:
		return false
	}
	return true
}

// modelTool is a button of the panel with its key at the right.
func (a *App) modelTool(id string, r gfx.Rect, text, key string, enabled bool) bool {
	u := &a.u
	clicked := u.button(id, r, "", "", ghost, enabled)
	pad := u.px(8)
	keys := u.face(10.5)
	width := keys.Measure(key)
	colour := colText
	if !enabled {
		colour = colFaint
	}
	u.label(keys, rectAt(r.X, r.Y, r.W-pad, r.H), key, colFaint, 1)
	f := u.face(12.5)
	room := r.W - pad*2 - width - u.px(4)
	u.label(f, rectAt(r.X+pad, r.Y, room, r.H), f.Ellipsize(text, room), colour, 0)
	return clicked
}

// drawObjectList is the panel's list of the file's objects; it returns where it ends.
func (a *App) drawObjectList(md *modelDoc, x, y, w float32) float32 {
	u := &a.u
	as := md.asset
	if as == nil {
		return y
	}
	type row struct{ node, depth int }
	var rows []row
	var walk func(n, depth int)
	walk = func(n, depth int) {
		if !holdsMesh(as, n) {
			return
		}
		rows = append(rows, row{n, depth})
		for _, c := range as.Nodes[n].Children {
			walk(c, depth+1)
		}
	}
	for _, n := range as.Roots {
		walk(n, 0)
	}
	if len(rows) == 0 {
		return y
	}
	u.label(u.bold(12), rectAt(x, y, w, u.px(20)), u.t("Objects"), colDim, 0)
	y += u.px(22)
	high := u.px(26)
	box := rectAt(x, y, w, min(float32(len(rows))*high, u.px(maxObjectsHigh)))
	a.scrolledBy("model/objects", box, &md.listScroll, &md.listContent, func(view gfx.Rect) float32 {
		at := view.Y
		for _, q := range rows {
			r := rectAt(x, at, w, high)
			at += high
			id := fmt.Sprintf("model/object/%d", q.node)
			if u.hover(id, r) {
				u.c.Fill(r, colBorder)
			}
			if md.chosen == q.node {
				u.c.Fill(r, modelHue)
			}
			if u.press(id, r) {
				a.chooseObject(md, q.node)
			}
			n := &as.Nodes[q.node]
			name := n.Name
			if name == "" && n.Mesh >= 0 {
				name = as.Meshes[n.Mesh].Name
			}
			if name == "" {
				name = u.tf("Object %d", q.node+1)
			}
			f := u.face(12.5)
			left := u.px(8) + float32(q.depth)*u.px(12)
			colour := colText
			if md.chosen == q.node {
				colour = colBackground
			}
			u.label(f, rectAt(x+left, r.Y, w-left-u.px(8), high), f.Ellipsize(name, w-left-u.px(8)), colour, 0)
		}
		return at - u.px(12) // scrolledBy adds its margin, which a list has no use for
	})
	return box.Bottom() + u.px(12)
}
