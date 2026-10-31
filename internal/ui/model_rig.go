package ui

import (
	"fmt"
	"image/color"
	"math"
	"slices"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	s3 "github.com/btelolabs/btelo-forge/internal/scene3d"
)

// modelRigErrors are the words of what scene3d's rigging refuses, which reach the window as errors.
var modelRigErrors = msgs("there are no bones to bind to", "the object has no mesh", "the vertices are not across a face", "choose two lines of edges of the same length")

const (
	timelineHigh = 58.0 // logical pixels
	clipFrames   = 30.0 // frames in a second, for the arrow keys
	newClipTime  = 2.0  // seconds a new clip shows before it has keys
	boneReach    = 8.0  // pixels around a bone that still choose it
)

// poseWorlds is every node's place as the view shows it.
func (md *modelDoc) poseWorlds() []s3.Mat4 {
	switch {
	case md.clip < 0:
		return md.asset.Worlds()
	case md.wander:
		return md.asset.WorldsAt(md.clip, float32(md.clipTime))
	}
	return md.asset.WorldsInPlace(md.clip, float32(md.clipTime))
}

// updateBonePivot puts the handles on the chosen bone.
func (md *modelDoc) updateBonePivot() {
	if md.bone >= 0 && md.asset != nil && md.bone < len(md.asset.Nodes) {
		md.pivot, md.hasPivot = md.poseWorlds()[md.bone].Point(s3.Vec3{}), true
	}
}

// chooseBone chooses a bone, or none for -1.
func (a *App) chooseBone(md *modelDoc, node int) {
	md.bone = node
	if node >= 0 {
		a.chooseObject(md, -1)
		md.updateBonePivot()
	} else {
		md.hasPivot = false
	}
	md.poseEdits++
	a.requestFrame()
}

// pickBone finds the bone under the point (x, y) of the view r, or -1.
func (a *App) pickBone(md *modelDoc, r gfx.Rect, x, y float32) int {
	shapes, _ := skeletonShapes(md.asset, md.poseWorlds())
	best, found := a.u.px(boneReach), -1
	for _, s := range shapes {
		ax, ay, ok1 := md.cam.Project(s.head, r.W, r.H)
		bx, by, ok2 := md.cam.Project(s.tail, r.W, r.H)
		if ok1 && ok2 {
			if d := distanceToSegment(x, y, ax, ay, bx, by); d < best {
				best, found = d, s.node
			}
		}
	}
	return found
}

// addBone adds a bone after the chosen one, one its own length on, or the start of a skeleton in the middle of the model.
func (a *App) addBone(md *modelDoc) {
	md.remember(md.shot())
	head := md.bounds.Center()
	if md.bounds.Empty() {
		head = s3.Vec3{}
	}
	parent := md.bone
	size := max(md.bounds.Size().Length(), 1)
	if parent >= 0 {
		worlds := md.asset.Worlds()
		head = worlds[parent].Point(s3.Vec3{})
		step := s3.Vec3{0, size * .15, 0}
		if p := md.asset.Nodes[parent].Parent; p >= 0 && slices.ContainsFunc(md.asset.Bones(), func(b s3.Bone) bool { return b.Node == p }) {
			step = head.Sub(worlds[p].Point(s3.Vec3{}))
		}
		head = head.Add(step)
	}
	md.bones = true
	md.dirty = true
	a.playClip(md, -1)
	a.chooseBone(md, md.asset.AddBone(parent, head))
	a.reshape(md, false)
}

// bindMesh ties the chosen object, or the file's first mesh, to the skeleton as it stands.
func (a *App) bindMesh(md *modelDoc) {
	node := -1
	if md.lastObject >= 0 {
		node = firstMesh(md.asset, md.lastObject)
	}
	if node < 0 {
		node = slices.IndexFunc(md.asset.Nodes, func(n s3.Node) bool { return n.Mesh >= 0 })
	}
	if node < 0 {
		a.toast(a.u.t("This file has no mesh to edit"), true)
		return
	}
	md.remember(md.shot())
	a.playClip(md, -1)
	if err := md.asset.Bind(node); err != nil {
		md.undo = md.undo[:len(md.undo)-1]
		a.toast(a.u.t(err.Error()), true)
		return
	}
	md.dirty = true
	a.reshape(md, true)
	a.objectOverlay(md)
	a.toast(a.u.t("The mesh now follows the bones"), false)
}

// newClip starts an empty animation and shows it, ready to be keyed.
func (a *App) newClip(md *modelDoc) {
	md.remember(md.shot())
	md.asset.Clips = append(slices.Clone(md.asset.Clips), s3.Clip{Name: a.u.tf("Animation %d", len(md.asset.Clips)+1)})
	md.dirty = true
	a.playClip(md, len(md.asset.Clips)-1)
	md.playing = false
}

// keyBone records the chosen bone as it stands at the playhead.
func (a *App) keyBone(md *modelDoc) {
	if md.clip < 0 || md.bone < 0 {
		a.toast(a.u.t("Choose a bone and an animation first"), true)
		return
	}
	md.remember(md.shot())
	worlds := md.asset.WorldsAt(md.clip, float32(md.clipTime))
	local := worlds[md.bone]
	if p := md.asset.Nodes[md.bone].Parent; p >= 0 {
		local = worlds[p].Inverse().Mul(local)
	}
	at, turn, _ := s3.Decompose(local)
	md.asset.KeyNode(md.clip, md.bone, float32(md.clipTime), at, turn)
	md.dirty = true
	md.poseEdits++
	a.requestFrame()
}

// unkeyBone takes out the chosen bone's keys at the playhead.
func (a *App) unkeyBone(md *modelDoc) {
	if md.clip < 0 || md.bone < 0 {
		return
	}
	md.remember(md.shot())
	if !md.asset.UnkeyNode(md.clip, md.bone, float32(md.clipTime)) {
		md.undo = md.undo[:len(md.undo)-1]
		return
	}
	md.dirty = true
	md.poseEdits++
	a.requestFrame()
}

// clipLength is how much time the timeline shows.
func (md *modelDoc) clipLength() float64 {
	return max(float64(md.asset.Clips[md.clip].Duration()), newClipTime*boolTo(len(md.asset.Clips[md.clip].Channels) == 0), .1)
}

func boolTo(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// stepPlayhead moves the playhead by frames, round the ends.
func (a *App) stepPlayhead(md *modelDoc, frames float64) {
	length := md.clipLength()
	md.playing = false
	md.clipTime = math.Mod(md.clipTime+frames/clipFrames+length, length)
	md.updateBonePivot()
	a.requestFrame()
}

// startBoneTool starts move, turn or scale on the chosen bone: in an animation it makes keys, else it moves the skeleton.
func (a *App) startBoneTool(md *modelDoc, kind modalKind) {
	worlds := md.poseWorlds()
	n := &md.asset.Nodes[md.bone]
	t := &objectTool{node: md.bone, before: *n, world: worlds[md.bone], outside: s3.Identity(), bone: true}
	if n.Parent >= 0 {
		t.outside = worlds[n.Parent].Inverse()
	}
	t.kind, t.axis, t.startX, t.startY = kind, -1, a.u.mx, a.u.my
	t.pivot = worlds[md.bone].Point(s3.Vec3{})
	md.playing = false
	md.remember(md.shot())
	t.nodes, t.clips = md.asset.Nodes, md.asset.Clips
	if md.ikChain > 1 && kind == modalMove {
		t.posed = md.asset.NodesAt(md.clip, float32(md.clipTime))
	}
	md.objectTool = t
}

// boneToolStep places the bone for the pointer over the view r.
func (a *App) boneToolStep(md *modelDoc, r gfx.Rect) {
	t := md.objectTool
	world, _ := a.modalWorld(md, &t.modelModal, r)
	if t.posed != nil {
		a.solveBone(md, t, world.Point(t.pivot))
		md.updateBonePivot()
		md.poseEdits++
		a.requestFrame()
		return
	}
	at, turn, size := s3.Decompose(t.outside.Mul(world).Mul(t.world))
	if md.clip >= 0 {
		md.asset.Clips = t.clips
		md.asset.KeyNode(md.clip, t.node, float32(md.clipTime), at, turn)
	} else {
		nodes := slices.Clone(t.nodes)
		nodes[t.node].Translation, nodes[t.node].Rotation, nodes[t.node].Scale = at, turn, size
		md.asset.Nodes = nodes
		a.reshape(md, false)
	}
	md.updateBonePivot()
	md.poseEdits++
	a.requestFrame()
}

// endBoneTool finishes the tool, or takes it back.
func (a *App) endBoneTool(md *modelDoc, cancel bool) {
	t := md.objectTool
	md.objectTool = nil
	if cancel {
		md.asset.Nodes, md.asset.Clips = t.nodes, t.clips
		md.undo = md.undo[:len(md.undo)-1]
	} else {
		md.dirty = true
	}
	a.reshape(md, true)
	md.updateBonePivot()
	md.poseEdits++
}

// drawClipBar is the strip over the bottom of the view r while an animation is shown.
func (a *App) drawClipBar(md *modelDoc, r gfx.Rect) {
	u := &a.u
	if md.clip < 0 || md.asset == nil {
		return
	}
	high := u.px(timelineHigh)
	bar := rectAt(r.X, r.Bottom()-high, r.W, high)
	u.c.Fill(bar, colPanel)
	u.c.Fill(rectAt(bar.X, bar.Y, bar.W, max(1, u.px(1))), colBorder)
	pad := u.px(16)
	line := rectAt(bar.X+pad, bar.Y+u.px(24), bar.W-pad*2, u.px(22))
	length := md.clipLength()
	at := func(t float64) float32 { return line.X + float32(t/length)*line.W }
	const id = "model/timeline"
	if u.press(id, bar) || u.dragging(id) {
		md.clipTime = min(max(float64(u.mx-line.X)/float64(line.W)*length, 0), length)
		md.playing = false
		md.updateBonePivot()
		a.requestFrame()
	}
	u.letGo(id)
	u.c.Fill(rectAt(line.X, line.Y+u.px(10), line.W, max(1, u.px(1))), colBorder)
	f := u.face(11)
	steps := max(math.Ceil(length*4), 1)
	for i := 0.0; i <= steps; i++ {
		t := length * i / steps
		x := at(t)
		u.c.Fill(rectAt(x, line.Y+u.px(5), max(1, u.px(1)), u.px(11)), colBorder)
		u.label(f, rectAt(x-u.px(20), bar.Y+u.px(4), u.px(40), u.px(16)), fmt.Sprintf("%.2f", t), colDim, .5)
	}
	keyColor, half := color.RGBA{R: 240, G: 190, B: 70, A: 255}, u.px(4)
	for _, t := range md.asset.KeyTimes(md.clip, md.bone) {
		x, y := at(float64(t)), line.Y+u.px(11)
		u.c.Triangle(x-half, y, x, y-half, x+half, y, keyColor)
		u.c.Triangle(x-half, y, x+half, y, x, y+half, keyColor)
	}
	head := at(md.clipTime)
	u.c.Fill(rectAt(head-u.px(1), bar.Y+u.px(2), u.px(2), bar.H-u.px(4)), modelHue)
}

// drawModelRig is the part of the animation tab that makes skeletons.
func (a *App) drawModelRig(md *modelDoc, x, y, w float32) float32 {
	u := &a.u
	half := (w - u.px(6)) / 2
	if a.modelTool("model/bone/add", rectAt(x, y, w, u.px(28)), u.t("Add bone"), "Shift+A", !md.tooling()) {
		a.addBone(md)
	}
	y += u.px(32)
	if a.modelTool("model/bone/bind", rectAt(x, y, w, u.px(28)), u.t("Bind mesh"), "Ctrl+P", len(md.asset.Skins) > 0) {
		a.bindMesh(md)
	}
	y += u.px(32)
	if a.modelTool("model/clip/new", rectAt(x, y, half, u.px(28)), u.t("New animation"), "", !md.tooling()) {
		a.newClip(md)
	}
	if a.modelTool("model/clip/key", rectAt(x+half+u.px(6), y, half, u.px(28)), u.t("Add key"), "K", md.clip >= 0 && md.bone >= 0) {
		a.keyBone(md)
	}
	y += u.px(36)
	u.label(u.face(12), rectAt(x, y, w, u.px(18)), u.t("IK chain"), colDim, 0)
	y += u.px(20)
	chains := []string{u.t("Off"), "2", "3", "4", "5"}
	pick := max(md.ikChain-1, 0)
	if next := u.chips("model/ik", rectAt(x, y, w, u.px(26)), chains, pick, true); next != pick {
		md.ikChain = 0
		if next > 0 {
			md.ikChain = next + 1
		}
	}
	y += u.px(32)
	if on := u.checkbox("model/weights", rectAt(x, y, w, u.px(26)), u.t("Paint weights"), md.weights); on != md.weights {
		md.weights = on
		if on {
			a.playClip(md, -1)
			md.bones = true
			a.weightOverlay(md)
		} else {
			a.objectOverlay(md)
		}
	}
	return y + u.px(32)
}

// weightColor is a weight as Blender shows it: blue for none, through green, to red for all.
func weightColor(w float32) [4]float32 {
	switch {
	case w < .25:
		return [4]float32{0, w * 4, 1, .92}
	case w < .5:
		return [4]float32{0, 1, 1 - (w-.25)*4, .92}
	case w < .75:
		return [4]float32{(w - .5) * 4, 1, 0, .92}
	}
	return [4]float32{1, 1 - (w-.75)*4, 0, .92}
}

// weightJoint is the chosen bone's place in the first skin, or -1.
func (md *modelDoc) weightJoint() int {
	if md.asset == nil || len(md.asset.Skins) == 0 {
		return -1
	}
	return slices.Index(md.asset.Skins[0].Joints, md.bone)
}

// paintingWeights reports whether the mouse paints weights in the view.
func (md *modelDoc) paintingWeights() bool {
	return md.weights && md.edit == nil && md.clip < 0 && md.weightJoint() >= 0
}

// weightOverlay shows how much the surfaces follow the chosen bone.
func (a *App) weightOverlay(md *modelDoc) {
	joint := uint16(max(md.weightJoint(), 0))
	var tris []float32
	for i := range md.surfaces {
		s := &md.surfaces[i]
		n := &md.asset.Nodes[s.Node]
		if n.Mesh < 0 || s.Part >= len(md.asset.Meshes[n.Mesh].Prims) {
			continue
		}
		p := &md.asset.Meshes[n.Mesh].Prims[s.Part]
		if p.Joints == nil {
			continue
		}
		for _, v := range s.Indices {
			c := weightColor(s3.WeightOf(p, int(v), joint))
			q := s.Pos[v]
			tris = append(tris, q[0], q[1], q[2], c[0], c[1], c[2], c[3], 0, 0)
		}
	}
	md.tris, md.lines = tris, nil
	md.overlay++
	a.requestFrame()
}

// weightDab lays one touch of the brush at the point under the pointer, of the view r.
func (a *App) weightDab(md *modelDoc, r gfx.Rect, x, y float32) {
	u := &a.u
	dir := md.cam.Ray(x, y, r.W, r.H)
	distance, hit, _, ok := md.picker.Hit(md.cam.Pos, dir)
	if !ok {
		return
	}
	center := md.cam.Pos.Add(dir.Scale(distance))
	radius := a.worldBrush(md, distance, r.H)
	strength := float32(a.brush.Flow) * .25
	if u.ctrl {
		strength = -strength
	}
	joint := uint16(md.weightJoint())
	s := &md.surfaces[hit]
	mesh := md.asset.Nodes[s.Node].Mesh
	for i := range md.surfaces {
		q := &md.surfaces[i]
		if md.asset.Nodes[q.Node].Mesh != mesh {
			continue
		}
		p := &md.asset.Meshes[mesh].Prims[q.Part]
		if p.Joints != nil {
			s3.PaintWeight(p, q.Pos, joint, center, radius, strength)
		}
	}
	md.dirty = true
	a.weightOverlay(md)
}

// weightMouse paints weights with the left button in the view r.
func (a *App) weightMouse(md *modelDoc, r gfx.Rect) {
	u := &a.u
	const id = "model/weight"
	if !a.modelHolding(md) && u.press(id, r) {
		md.remember(md.shot())
		// The step of undo holds the lists as they are.
		md.asset.Meshes = slices.Clone(md.asset.Meshes)
		for i := range md.asset.Meshes {
			m := &md.asset.Meshes[i]
			m.Prims = slices.Clone(m.Prims)
			for k := range m.Prims {
				m.Prims[k].Joints, m.Prims[k].Weights = slices.Clone(m.Prims[k].Joints), slices.Clone(m.Prims[k].Weights)
			}
		}
		a.weightDab(md, r, u.pressX-r.X, u.pressY-r.Y)
	}
	if u.dragging(id) && (u.mx != md.lastX || u.my != md.lastY) {
		a.weightDab(md, r, u.mx-r.X, u.my-r.Y)
	}
	u.letGo(id)
}

// solveBone moves the chain above the bone of tool t for its end to reach target, keying what it turned.
func (a *App) solveBone(md *modelDoc, t *objectTool, target s3.Vec3) {
	nodes := t.posed
	if nodes == nil {
		return
	}
	nodes = slices.Clone(nodes)
	turned := md.asset.SolveIK(nodes, t.node, md.ikChain, target)
	if md.clip >= 0 {
		md.asset.Clips = t.clips
		for _, n := range turned {
			md.asset.KeyNode(md.clip, n, float32(md.clipTime), nodes[n].Translation, nodes[n].Rotation)
		}
		return
	}
	next := slices.Clone(t.nodes)
	for _, n := range turned {
		next[n].Rotation = nodes[n].Rotation
	}
	md.asset.Nodes = next
	a.reshape(md, false)
}
