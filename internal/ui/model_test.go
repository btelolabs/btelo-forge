package ui

import (
	"errors"
	"fmt"
	"github.com/btelolabs/btelo-forge/internal/scene3d"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/gpu"
	"github.com/btelolabs/btelo-forge/internal/gpu/wgpu"
	"github.com/btelolabs/btelo-forge/internal/render3d"
)

// Tests reach no graphics card, unless they ask for one with realCard.
func init() {
	newModelDevice = func() (gpu.Device, error) { return nil, fmt.Errorf("%w: none in tests", gpu.ErrUnavailable) }
}

func realCard(t *testing.T) {
	t.Helper()
	_, err := wgpu.Shared()
	if errors.Is(err, gpu.ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	before := newModelDevice
	newModelDevice = wgpu.Shared
	t.Cleanup(func() { newModelDevice = before })
}

// settleFrames draws frames until done says so.
func settleFrames(t *testing.T, x *harness, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s", what)
		}
		x.frame(Input{})
		time.Sleep(5 * time.Millisecond)
	}
}

func modelCardAt(t *testing.T, x *harness) {
	t.Helper()
	x.frame(Input{})
	card := x.startCard("empty/model")
	x.click(card)
	if x.a.model == nil {
		t.Fatal("the card opened no 3D tab")
	}
}

func TestTheCardOpensA3DTabThatSaysWhatIsMissing(t *testing.T) {
	x := newHarness(t)
	modelCardAt(t, x)
	if got := x.a.model.title(x.a); got != "3D assets" {
		t.Fatalf("title %q", got)
	}
	settleFrames(t, x, "the card's answer", func() bool { return x.a.models.failed != "" })
	if x.a.modelMoving() {
		t.Fatal("an idle view keeps drawing")
	}
	x.shot("model-no-card")
	x.a.closeTab(0)
	if x.a.models.requests != nil {
		t.Fatal("the graphics card is kept with no 3D tab open")
	}
}

func TestDroppedBrokenFileSaysSo(t *testing.T) {
	x := newHarness(t)
	path := filepath.Join(t.TempDir(), "broken.glb")
	if err := os.WriteFile(path, []byte("glTF but no more"), 0o644); err != nil {
		t.Fatal(err)
	}
	x.frame(Input{Dropped: []string{path}})
	if x.a.model == nil || x.a.model.path != path {
		t.Fatal("a .glb did not open a 3D tab")
	}
	settleFrames(t, x, "the file to fail", func() bool { return !x.a.model.loading })
	if x.a.model.asset != nil || len(x.a.toasts) == 0 {
		t.Fatalf("asset %v, toasts %v", x.a.model.asset, x.a.toasts)
	}
}

// A 3D file opened from outside the app, or from a file list, goes to the 3D tool.
func TestOpenedFromOutsideGoesToThe3DTool(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thing.GLB")
	if err := os.WriteFile(path, []byte("not yet"), 0o644); err != nil {
		t.Fatal(err)
	}
	x := newHarness(t)
	x.a.openFromSystem([]string{path})
	if x.a.model == nil || x.a.model.path != path {
		t.Fatal("from the system: no 3D tab")
	}
	x.a.openFromSystem([]string{path})
	if len(x.a.docs) != 1 {
		t.Fatalf("%d tabs for one file", len(x.a.docs))
	}
	settleFrames(t, x, "the file to fail", func() bool { return !x.a.model.loading })
	y := newHarness(t)
	y.a.openAsset(path)
	if y.a.model == nil {
		t.Fatal("from a file list: no 3D tab")
	}
	settleFrames(t, y, "the file to fail", func() bool { return !y.a.model.loading })
}

// The mouse and keys move the camera as the tool says they do.
func TestNavigationInTheView(t *testing.T) {
	x := newHarness(t)
	modelCardAt(t, x)
	md := x.a.model
	view := x.a.layout().body
	view.W -= x.a.u.px(264)
	x.mx, x.my = view.X+view.W/2, view.Y+view.H/2

	yaw := md.cam.Yaw
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my, Right: true, Down: true}}})
	x.mx += 80
	x.frame(Input{})
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my, Right: true}}})
	if !x.a.modelMoving() {
		t.Fatal("the turn does not ease out")
	}
	settleFrames(t, x, "the turn", func() bool { return !md.cam.Moving() })
	if md.cam.Yaw == yaw {
		t.Fatal("a right drag did not turn the camera")
	}

	pos := md.cam.Pos
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my, Middle: true, Down: true}}})
	if !md.panning {
		t.Fatal("the middle button did not start moving")
	}
	x.mx -= 60
	x.frame(Input{})
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my, Middle: true}}})
	settleFrames(t, x, "the move", func() bool { return !md.cam.Moving() })
	if md.panning || md.cam.Pos == pos {
		t.Fatalf("panning %v, %v to %v", md.panning, pos, md.cam.Pos)
	}

	// Space held makes a left drag move too, and a left drag alone does nothing.
	pos = md.cam.Pos
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my, Down: true}}, Down: true})
	x.mx += 40
	x.frame(Input{Down: true})
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}})
	settleFrames(t, x, "nothing", func() bool { return !md.cam.Moving() })
	if md.cam.Pos != pos {
		t.Fatal("a left drag moved the camera")
	}
	x.frame(Input{Keys: []Key{{Code: keySpace}}, Up: []uint16{}})
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my, Down: true}}, Down: true})
	x.mx += 40
	x.frame(Input{Down: true})
	x.frame(Input{Pointer: []Pointer{{X: x.mx, Y: x.my}}, Up: []uint16{keySpace}})
	settleFrames(t, x, "the move", func() bool { return !md.cam.Moving() })
	if md.cam.Pos == pos {
		t.Fatal("Space and a left drag did not move the camera")
	}

	pos = md.cam.Pos
	x.frame(Input{Keys: []Key{{Code: 'W'}}, Up: []uint16{}})
	if !x.a.modelMoving() {
		t.Fatal("a key held down gets no frames")
	}
	time.Sleep(20 * time.Millisecond)
	x.frame(Input{Up: []uint16{'W'}})
	settleFrames(t, x, "the keys", func() bool { return !md.cam.Moving() })
	if md.cam.Pos == pos {
		t.Fatal("W did not move the camera")
	}

	pos = md.cam.Pos
	x.frame(Input{Wheel: 2})
	settleFrames(t, x, "the zoom", func() bool { return !md.cam.Moving() })
	if md.cam.Pos == pos {
		t.Fatal("the wheel did not zoom")
	}
}

func TestSkiesAreAConfiguration(t *testing.T) {
	x := newHarness(t)
	k, err := findConfig("sky_presets")
	if err != nil {
		t.Fatal(err)
	}
	version := x.a.models.skyVersion
	if err := x.a.saveConfig(k, `{"skies":[{"id":"mine","name":"Mine","sun":{"elevation":30,"intensity":1}}]}`); err != nil {
		t.Fatal(err)
	}
	if len(x.a.models.skies.Skies) != 1 || x.a.models.skyVersion == version {
		t.Fatalf("skies %+v", x.a.models.skies)
	}
	if err := x.a.saveConfig(k, `{"skies":[]}`); err == nil {
		t.Fatal("an empty list was taken")
	}
	if err := x.a.resetConfig(k); err != nil {
		t.Fatal(err)
	}
	if len(x.a.models.skies.Skies) != 5 {
		t.Fatalf("%d skies after the reset", len(x.a.models.skies.Skies))
	}
}

// FORGE_MODEL names a model to look at; FORGE_SHOTS is where its pictures go, one for each way of showing it.
func TestRealCardShowsAModel(t *testing.T) {
	path, dir := os.Getenv("FORGE_MODEL"), os.Getenv("FORGE_SHOTS")
	if path == "" || dir == "" {
		t.Skip("set FORGE_MODEL and FORGE_SHOTS to look at a model")
	}
	realCard(t)
	x := newHarness(t)
	x.frame(Input{Dropped: []string{path}})
	md := x.a.model
	settleFrames(t, x, "the file", func() bool { return md.asset != nil })
	shoot := func(name string) {
		md.drawn = modelFrame{}
		md.picture = nil
		settleFrames(t, x, name, func() bool { return md.picture != nil && !x.a.models.busy })
		x.frame(Input{})
		f, err := os.Create(filepath.Join(dir, "model-"+name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, image.Image(md.picture)); err != nil {
			t.Fatal(err)
		}
		x.shot("model-window-" + name)
	}
	shoot("material")
	for _, k := range x.a.models.skies.Skies[1:] {
		md.sky = k.ID
		shoot("sky-" + k.ID)
	}
	md.sky = x.a.models.skies.Skies[0].ID
	for i, name := range []string{"material2", "solid", "normals", "uv"} {
		md.options.View = render3d.ViewMode(i)
		md.options.Wireframe = i == 1
		shoot(name)
	}
	md.options.View = render3d.ViewMaterial
	md.options.Wireframe = true
	for i, name := range []string{"panel-model", "panel-material", "panel-paint", "panel-clips", "panel-view", "panel-sky", "panel-file"} {
		md.panel = i
		shoot(name)
	}
	md.panel = modelPanelShape
	x.a.chooseObject(md, md.surfaces[0].Node)
	shoot("chosen")
	x.a.enterEdit(md, gfx.Rect{})
	shoot("editing")
	start := time.Now()
	x.a.unwrapSeams(md)
	t.Logf("unwrapped along %d seams in %v", len(md.edit.em.Seams), time.Since(start))
	md.uvOpen = true
	md.options.Wireframe = false
	shoot("uv-editor")
	md.uvOpen = false
	x.a.leaveEdit(md)
	if os.Getenv("FORGE_TRACE") != "" {
		md.panel, md.options.Wireframe, md.options.View = modelPanelView, false, render3d.ViewTraced
		x.a.chooseObject(md, -1)
		start := time.Now()
		settleFrames(t, x, "the samples", func() bool { return md.traceSamples >= 300 })
		t.Logf("%d samples in %v", md.traceSamples, time.Since(start))
		md.spare = nil
		x.frame(Input{})
		f, err := os.Create(filepath.Join(dir, "model-traced.png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		png.Encode(f, image.Image(md.picture))
		x.shot("model-window-traced")
		md.options.View = render3d.ViewMaterial
		shoot("raster")
	}
	if name := os.Getenv("FORGE_BONE"); name != "" {
		for n := range md.asset.Nodes {
			if md.asset.Nodes[n].Name == name {
				x.a.playClip(md, -1)
				md.panel, md.options.Wireframe = modelPanelClips, false
				x.a.chooseBone(md, n)
				md.bones, md.weights = true, true
				x.a.weightOverlay(md)
				shoot("weights")
				md.weights = false
				x.a.objectOverlay(md)
			}
		}
	}
	if os.Getenv("FORGE_PAINT") != "" {
		md.panel, md.chosen = modelPanelPaint, -1
		x.a.objectOverlay(md)
		md.options.Wireframe = false
		x.a.brush.Color, x.a.brush.Size, x.a.brush.Opacity, x.a.brush.Flow, x.a.brush.Hardness = color.NRGBA{R: 30, G: 120, B: 255, A: 255}, 44, 1, 1, .7
		x.frame(Input{})
		view := x.a.layout().body
		view.W -= x.a.u.px(264)
		r := gfx.Rect{W: view.W, H: view.H}
		x0, y0, _ := md.cam.Project(md.bounds.Center(), r.W, r.H)
		x.a.beginStroke(md, r, x0-90, y0)
		if md.stroke == nil {
			t.Fatal("the stroke found no surface")
		}
		for k := range 30 {
			x.a.strokeTo(md, r, x0-90+float32(k)*6, y0+float32(k%7))
		}
		x.a.endStroke(md)
		shoot("painted")
		md.cam.Pos = md.cam.Pos.Sub(md.bounds.Center()).Scale(2.5).Add(md.bounds.Center())
		shoot("painted-far")
	}
	x.a.leaveEdit(md)
	md.panel = modelPanelClips
	for i, c := range md.asset.Clips {
		if c.Name == os.Getenv("FORGE_CLIP") {
			md.bones, md.options.Wireframe = true, false
			x.a.chooseObject(md, -1)
			x.a.playClip(md, i)
			md.playing = false
			d := float64(c.Duration())
			for f := range 12 {
				md.clipTime = d * float64(f) / 12
				shoot(fmt.Sprintf("clip-%02d", f))
			}
			worlds := md.asset.WorldsInPlace(i, 0)
			start := time.Now()
			for range 20 {
				md.asset.SurfacesIn(worlds, nil)
			}
			t.Logf("%d triangles skinned in %v", md.stats.Triangles, time.Since(start)/20)
		}
	}
	if x.a.models.failed != "" {
		t.Fatal(x.a.models.failed)
	}
}

// FORGE_RIG is a folder for a tube made into a bending animation from nothing, by the steps a user takes, and the file it
// makes, to look at elsewhere.
func TestRealCardMakesARig(t *testing.T) {
	dir := os.Getenv("FORGE_RIG")
	if dir == "" {
		t.Skip("set FORGE_RIG to make a rig")
	}
	realCard(t)
	x := newHarness(t)
	x.a.openModelTab()
	x.frame(Input{})
	md := x.a.model
	x.a.addPrimitive(md, scene3d.Cylinder)
	e := md.edit
	x.a.editChange(md, func(*modelEdit) error {
		for range 2 {
			e.em.Subdivide(e.chosenFaces())
		}
		return nil
	})
	x.a.leaveEdit(md)
	md.asset.Nodes[0].Scale = scene3d.Vec3{1, 4, 1}
	x.a.reshape(md, true)
	md.cam.Frame(md.bounds)
	md.options.Wireframe = false
	low, high := md.bounds.Min[1], md.bounds.Max[1]
	root := md.asset.AddBone(-1, scene3d.Vec3{0, low, 0})
	mid := md.asset.AddBone(root, scene3d.Vec3{0, (low + high) / 2, 0})
	md.asset.AddBone(mid, scene3d.Vec3{0, high, 0})
	x.a.reshape(md, false)
	md.lastObject = 0
	x.a.bindMesh(md)
	md.bones, md.panel = true, modelPanelClips
	x.a.newClip(md)
	x.a.chooseBone(md, mid)
	turn := render3dTurn()
	at := md.asset.Nodes[mid].Translation
	md.asset.KeyNode(0, mid, 0, at, scene3d.Quat{0, 0, 0, 1})
	md.asset.KeyNode(0, mid, 1, at, turn)
	md.clipTime = .75
	md.poseEdits++
	settleFrames(t, x, "the frame", func() bool { return md.picture != nil })
	for range 6 {
		md.drawn = modelFrame{}
		md.picture = nil
		settleFrames(t, x, "the pose", func() bool { return md.picture != nil && !x.a.models.busy })
	}
	x.frame(Input{})
	x.shot("model-rig")
	cube := x.a.newModelDoc("")
	x.a.model = cube
	x.a.addPrimitive(cube, scene3d.Cube)
	for f := range cube.edit.em.Pos {
		if p := &cube.edit.em.Pos[f]; p[0] < 0 {
			p[0] *= .3
		}
	}
	cube.edit.modifiers = scene3d.Modifiers{Mirror: [3]bool{false, true, false}, Subdivide: 3}
	x.a.editRefresh(cube, true)
	cube.cam.Frame(cube.bounds)
	cube.options.Wireframe = true
	settleFrames(t, x, "the smooth shape", func() bool { return cube.picture != nil && !x.a.models.busy })
	for range 5 {
		cube.drawn = modelFrame{}
		cube.picture = nil
		settleFrames(t, x, "the frame", func() bool { return cube.picture != nil && !x.a.models.busy })
	}
	x.frame(Input{})
	x.shot("model-subdivided")
	x.a.model = md
	if err := scene3d.Save(md.asset, filepath.Join(dir, "rig.glb")); err != nil {
		t.Fatal(err)
	}
	if x.a.models.failed != "" {
		t.Fatal(x.a.models.failed)
	}
}

func render3dTurn() scene3d.Quat {
	const half = .7071068
	return scene3d.Quat{0, 0, half, half}
}
