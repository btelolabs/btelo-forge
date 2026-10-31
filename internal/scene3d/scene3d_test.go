package scene3d

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// glbOf wraps a description and a binary chunk as a .glb.
func glbOf(t *testing.T, doc any, bin []byte) []byte {
	t.Helper()
	js, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	for len(js)%4 != 0 {
		js = append(js, ' ')
	}
	for len(bin)%4 != 0 {
		bin = append(bin, 0)
	}
	var out bytes.Buffer
	put := func(v ...uint32) {
		for _, x := range v {
			binary.Write(&out, binary.LittleEndian, x)
		}
	}
	put(glbMagic, 2, uint32(12+8+len(js)+8+len(bin)), uint32(len(js)), chunkJSON)
	out.Write(js)
	put(uint32(len(bin)), chunkBIN)
	out.Write(bin)
	return out.Bytes()
}

func floatBytes(v ...float32) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, v)
	return b.Bytes()
}

type m = map[string]any

// triangleDoc is one triangle with 16-bit indices, in a node moved by 2 along x.
func triangleDoc(t *testing.T) (m, []byte) {
	t.Helper()
	bin := floatBytes(0, 0, 0, 1, 0, 0, 0, 1, 0)
	indices := new(bytes.Buffer)
	binary.Write(indices, binary.LittleEndian, []uint16{0, 1, 2, 0})
	vertices := len(bin)
	bin = append(bin, indices.Bytes()...)
	return m{
		"asset":       m{"version": "2.0"},
		"scene":       0,
		"scenes":      []m{{"nodes": []int{0}}},
		"nodes":       []m{{"name": "part", "mesh": 0, "translation": []float32{2, 0, 0}}},
		"meshes":      []m{{"name": "tri", "primitives": []m{{"attributes": m{"POSITION": 0}, "indices": 1}}}},
		"buffers":     []m{{"byteLength": len(bin)}},
		"bufferViews": []m{{"buffer": 0, "byteLength": vertices}, {"buffer": 0, "byteOffset": vertices, "byteLength": 6}},
		"accessors":   []m{{"bufferView": 0, "componentType": gltfFloat, "count": 3, "type": "VEC3"}, {"bufferView": 1, "componentType": gltfUshort, "count": 3, "type": "SCALAR"}},
	}, bin
}

func noFiles(string) ([]byte, error) { return nil, errors.New("no files here") }

func TestDecodeGLB(t *testing.T) {
	doc, bin := triangleDoc(t)
	a, err := Decode(glbOf(t, doc, bin), noFiles)
	if err != nil {
		t.Fatal(err)
	}
	s := a.Stats()
	if s.Triangles != 1 || s.Vertices != 3 || s.Meshes != 1 {
		t.Fatalf("stats %+v", s)
	}
	p := a.Meshes[0].Prims[0]
	if p.Normal[0] != (Vec3{0, 0, 1}) {
		t.Fatalf("worked-out normal %v", p.Normal[0])
	}
	surfaces, box := a.Surfaces()
	if len(surfaces) != 1 || surfaces[0].Pos[1] != (Vec3{3, 0, 0}) {
		t.Fatalf("surfaces %+v", surfaces)
	}
	if box.Min != (Vec3{2, 0, 0}) || box.Max != (Vec3{3, 1, 0}) {
		t.Fatalf("bounds %+v", box)
	}
}

func TestDecodeGLTFWithDataAndFiles(t *testing.T) {
	doc, bin := triangleDoc(t)
	doc["buffers"] = []m{{"byteLength": len(bin), "uri": "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(bin)}}
	js, _ := json.Marshal(doc)
	if a, err := Decode(js, noFiles); err != nil || a.Stats().Triangles != 1 {
		t.Fatalf("embedded: %v", err)
	}

	dir := t.TempDir()
	doc["buffers"] = []m{{"byteLength": len(bin), "uri": "my%20mesh.bin"}}
	js, _ = json.Marshal(doc)
	os.WriteFile(filepath.Join(dir, "my mesh.bin"), bin, 0o644)
	os.WriteFile(filepath.Join(dir, "a.gltf"), js, 0o644)
	if a, err := Load(filepath.Join(dir, "a.gltf")); err != nil || a.Stats().Triangles != 1 {
		t.Fatalf("a file beside it: %v", err)
	}

	doc["buffers"] = []m{{"byteLength": len(bin), "uri": "../secret.bin"}}
	js, _ = json.Marshal(doc)
	os.WriteFile(filepath.Join(dir, "b.gltf"), js, 0o644)
	if _, err := Load(filepath.Join(dir, "b.gltf")); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("a file outside its folder: %v", err)
	}
}

func TestBrokenFilesFail(t *testing.T) {
	cases := map[string]func(d m){
		"version":     func(d m) { d["asset"] = m{"version": "1.0"} },
		"draco":       func(d m) { d["extensionsRequired"] = []string{"KHR_draco_mesh_compression"} },
		"mesh":        func(d m) { d["nodes"] = []m{{"mesh": 7}} },
		"accessor":    func(d m) { d["accessors"].([]m)[0]["count"] = 99 },
		"view":        func(d m) { d["bufferViews"].([]m)[0]["byteLength"] = 9999 },
		"index":       func(d m) { d["bufferViews"].([]m)[1]["byteOffset"] = 42; d["bufferViews"].([]m)[1]["byteLength"] = 0 },
		"no position": func(d m) { d["meshes"].([]m)[0]["primitives"].([]m)[0]["attributes"] = m{} },
		"child loop": func(d m) {
			d["nodes"] = []m{{"children": []int{1}}, {"children": []int{0}}}
			d["scenes"] = []m{{"nodes": []int{0}}}
		},
	}
	for name, change := range cases {
		doc, bin := triangleDoc(t)
		change(doc)
		if _, err := Decode(glbOf(t, doc, bin), noFiles); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := Decode([]byte{0x67, 0x6c, 0x54, 0x46, 2}, noFiles); err == nil {
		t.Error("a cut-off file gave no error")
	}
}

func TestStripsAndFans(t *testing.T) {
	if got := triangles([]uint32{0, 1, 2, 3}, modeStrip); len(got) != 6 || got[3] != 2 || got[4] != 1 || got[5] != 3 {
		t.Fatalf("strip %v", got)
	}
	if got := triangles([]uint32{0, 1, 2, 3}, modeFan); len(got) != 6 || got[3] != 0 || got[4] != 2 || got[5] != 3 {
		t.Fatalf("fan %v", got)
	}
}

func TestComposeDecompose(t *testing.T) {
	c, s := float32(math.Cos(.4)), float32(math.Sin(.4))
	for _, q := range []Quat{{0, 0, 0, 1}, {s, 0, 0, c}, {0, s, 0, c}, {0, 0, s, c}, {1, 0, 0, 0}, {0, 1, 0, 0}, {0, 0, 1, 0}} {
		m := Compose(Vec3{1, 2, 3}, q, Vec3{2, 3, 4})
		tr, r, sc := Decompose(m)
		back := Compose(tr, r, sc)
		for i := range m {
			if d := m[i] - back[i]; d > 1e-5 || d < -1e-5 {
				t.Fatalf("%v: %v became %v", q, m, back)
			}
		}
	}
}

func TestNormalUnderUnevenScale(t *testing.T) {
	m := Compose(Vec3{}, Quat{0, 0, 0, 1}, Vec3{1, 4, 1})
	n := m.Normal(Vec3{1, 1, 0}.Norm())
	if want := (Vec3{4, 1, 0}).Norm(); n.Sub(want).Length() > 1e-5 {
		t.Fatalf("normal %v, want %v", n, want)
	}
	if n := Compose(Vec3{}, Quat{0, 0, 0, 1}, Vec3{-1, 1, 1}).Normal(Vec3{1, 0, 0}); n.Sub(Vec3{-1, 0, 0}).Length() > 1e-5 {
		t.Fatalf("mirrored normal %v", n)
	}
}

// A bound pose equals the mesh as modelled; a joint moved carries its vertices along by their weights.
func TestSkinnedSurfaces(t *testing.T) {
	a := &Asset{
		Nodes: []Node{
			{Parent: -1, Mesh: 0, Skin: 0, Scale: Vec3{1, 1, 1}, Rotation: Quat{0, 0, 0, 1}, Translation: Vec3{9, 9, 9}}, // ignored: it is skinned
			{Parent: -1, Mesh: -1, Skin: -1, Scale: Vec3{1, 1, 1}, Rotation: Quat{0, 0, 0, 1}},
			{Parent: -1, Mesh: -1, Skin: -1, Scale: Vec3{1, 1, 1}, Rotation: Quat{0, 0, 0, 1}, Translation: Vec3{0, 2, 0}},
		},
		Roots: []int{0, 1, 2},
		Meshes: []Mesh{{Prims: []Prim{{
			Pos: []Vec3{{0, 0, 0}, {0, 2, 0}}, Normal: []Vec3{{1, 0, 0}, {1, 0, 0}}, Material: -1,
			Joints: [][4]uint16{{0, 0, 0, 0}, {0, 1, 0, 0}}, Weights: []Vec4{{1, 0, 0, 0}, {.5, .5, 0, 0}},
		}}}},
		Skins: []Skin{{Joints: []int{1, 2}, InverseBind: []Mat4{Identity(), Compose(Vec3{0, -2, 0}, Quat{0, 0, 0, 1}, Vec3{1, 1, 1})}}},
	}
	s, _ := a.Surfaces()
	if s[0].Pos[1] != (Vec3{0, 2, 0}) {
		t.Fatalf("bind pose %v", s[0].Pos[1])
	}
	a.Nodes[2].Translation = Vec3{0, 4, 0}
	s, _ = a.Surfaces()
	if s[0].Pos[0] != (Vec3{0, 0, 0}) || s[0].Pos[1] != (Vec3{0, 3, 0}) {
		t.Fatalf("moved joint %v", s[0].Pos)
	}
}

// The tree finds what checking every triangle finds, from all sides.
func TestPickerMatchesBruteForce(t *testing.T) {
	var s Surface
	const side = 40
	for y := range side {
		for x := range side {
			base := uint32(len(s.Pos))
			fx, fy := float32(x), float32(y)
			z := float32(math.Sin(float64(fx)*.3) * math.Cos(float64(fy)*.2))
			s.Pos = append(s.Pos, Vec3{fx, fy, z}, Vec3{fx + 1, fy, z}, Vec3{fx, fy + 1, z})
			s.Indices = append(s.Indices, base, base+1, base+2)
		}
	}
	p := NewPicker([]Surface{s})
	for i := range 2000 {
		f := float64(i) * .37
		o := Vec3{20 + 30*float32(math.Sin(f)), 20 + 30*float32(math.Cos(f*1.3)), 25 * float32(math.Cos(f*.7))}
		target := Vec3{float32(i % side), float32(i * 7 % side), 0}
		d := target.Sub(o).Norm()
		best, found := float32(1e30), false
		for k := 0; k < len(s.Indices); k += 3 {
			if t, ok := hitTriangle(o, d, s.Pos[s.Indices[k]], s.Pos[s.Indices[k+1]], s.Pos[s.Indices[k+2]]); ok && t < best {
				best, found = t, true
			}
		}
		got, ok := p.Ray(o, d)
		if ok != found || (ok && got != best) {
			t.Fatalf("ray %d: %v %v, by hand %v %v", i, got, ok, best, found)
		}
	}
	if _, ok := NewPicker(nil).Ray(Vec3{}, Vec3{0, 0, 1}); ok {
		t.Fatal("nothing was hit")
	}
}

func TestClipPosesWithoutTouchingTheAsset(t *testing.T) {
	a := &Asset{Roots: []int{0}, Nodes: []Node{
		{Parent: -1, Children: []int{1}, Mesh: -1, Skin: -1, Scale: Vec3{1, 1, 1}, Rotation: Quat{0, 0, 0, 1}},
		{Parent: 0, Translation: Vec3{0, 1, 0}, Mesh: -1, Skin: -1, Scale: Vec3{1, 1, 1}, Rotation: Quat{0, 0, 0, 1}}},
		Skins: []Skin{{Joints: []int{0, 1}}},
		Clips: []Clip{{Channels: []Channel{
			{Node: 0, Path: PathTranslation, Times: []float32{0, 2}, Values: []float32{0, 0, 0, 4, 0, 0}, Width: 3},
			{Node: 0, Path: PathRotation, Times: []float32{0, 2}, Values: []float32{0, 0, 0, 1, 0, 0, 1, 0}, Width: 4},
			{Node: 1, Path: PathScale, Interp: Step, Times: []float32{0, 1}, Values: []float32{1, 1, 1, 2, 2, 2}, Width: 3},
		}}},
	}
	w := a.WorldsAt(0, 1)
	if got := w[0].Point(Vec3{}); got != (Vec3{2, 0, 0}) {
		t.Fatalf("halfway the root is at %v", got)
	}
	// Half of a half turn around z takes the child, one up, to the left of the root.
	if got := w[1].Point(Vec3{}); math.Abs(float64(got[0]-1)) > 1e-5 || math.Abs(float64(got[1])) > 1e-5 {
		t.Fatalf("the child is at %v", got)
	}
	if a.Nodes[0].Translation != (Vec3{}) {
		t.Fatal("sampling moved the asset")
	}
	if b := a.Bones(); len(b) != 2 || b[1] != (Bone{1, 0}) || b[0].Parent != -1 {
		t.Fatalf("bones %v", b)
	}
}

func TestCubicSplineFollowsItsKeys(t *testing.T) {
	c := Channel{Interp: CubicSpline, Times: []float32{0, 1}, Width: 3, Values: []float32{0, 0, 0, 1, 2, 3, 0, 0, 0, 0, 0, 0, 3, 4, 5, 0, 0, 0}}
	if got := c.sample(0); got != [3]float32{1, 2, 3} {
		t.Fatalf("at the start %v", got)
	}
	if got := c.sample(1); got != [3]float32{3, 4, 5} {
		t.Fatalf("at the end %v", got)
	}
	if got := c.sample(.5); got != [3]float32{2, 3, 4} {
		t.Fatalf("halfway %v", got)
	}
}

// A walk that carries its root forward stays on the spot, with the joints' places against each other as they were.
func TestInPlaceTakesOutTheStride(t *testing.T) {
	a := &Asset{Roots: []int{0}, Nodes: []Node{
		{Parent: -1, Children: []int{1}, Mesh: -1, Skin: -1, Scale: Vec3{1, 1, 1}, Rotation: Quat{0, 0, 0, 1}},
		{Parent: 0, Translation: Vec3{0, 1, 0}, Mesh: -1, Skin: -1, Scale: Vec3{1, 1, 1}, Rotation: Quat{0, 0, 0, 1}}},
		Skins: []Skin{{Joints: []int{0, 1}}},
		Clips: []Clip{{Channels: []Channel{
			{Node: 0, Path: PathTranslation, Times: []float32{0, 2}, Values: []float32{0, 0, 0, 0, .5, 8}, Width: 3},
		}}},
	}
	got := a.WorldsInPlace(0, 1)
	if p := got[0].Point(Vec3{}); p != (Vec3{0, .25, 0}) {
		t.Fatalf("the root is at %v, up and down staying", p)
	}
	if p := got[1].Point(Vec3{}); p != (Vec3{0, 1.25, 0}) {
		t.Fatalf("the child is at %v", p)
	}
}

func TestFBXGoesThereAndBack(t *testing.T) {
	cube, _ := NewPrimitive(Cube).Bake()
	for i := range cube {
		cube[i].Material = 0
		cube[i].Joints = make([][4]uint16, len(cube[i].Pos))
		cube[i].Weights = make([]Vec4, len(cube[i].Pos))
		for v := range cube[i].Weights {
			cube[i].Weights[v] = Vec4{.75, .25, 0, 0}
			cube[i].Joints[v] = [4]uint16{0, 1, 0, 0}
		}
		cube[i].Morphs = []Morph{{Pos: make([]Vec3, len(cube[i].Pos))}}
		cube[i].Morphs[0].Pos[0] = Vec3{0, 1, 0}
	}
	m := DefaultMaterial()
	m.Name, m.BaseColor, m.Metallic = "red", Vec4{.8, .1, .1, 1}, .5
	identity := Quat{0, 0, 0, 1}
	one := Vec3{1, 1, 1}
	a := &Asset{Roots: []int{0}, Materials: []Material{m}, Meshes: []Mesh{{Name: "cube", Prims: cube, Keys: []string{"lift"}, Weights: []float32{.5}}},
		Nodes: []Node{{Name: "body", Parent: -1, Children: []int{1, 2}, Mesh: 0, Skin: 0, Rotation: identity, Scale: one},
			{Name: "hip", Parent: 0, Children: []int{3}, Mesh: -1, Skin: -1, Translation: Vec3{0, 1, 0}, Rotation: identity, Scale: one},
			{Name: "other", Parent: 0, Mesh: -1, Skin: -1, Rotation: identity, Scale: one},
			{Name: "knee", Parent: 1, Mesh: -1, Skin: -1, Translation: Vec3{0, 1, 0}, Rotation: identity, Scale: one}},
		Skins: []Skin{{Name: "skin", Joints: []int{1, 3}, InverseBind: []Mat4{Compose(Vec3{0, -1, 0}, identity, one), Compose(Vec3{0, -2, 0}, identity, one)}}},
		Clips: []Clip{{Name: "wave", Channels: []Channel{{Node: 3, Path: PathRotation, Times: []float32{0, 1}, Values: []float32{0, 0, 0, 1, 0, 0, .70710678, .70710678}, Width: 4}}}}}
	data, err := EncodeFBX(a)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeFBX(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Meshes) != 1 || len(back.Skins) != 1 || len(back.Clips) != 1 || len(back.Materials) != 1 {
		t.Fatalf("back with %d meshes, %d skins, %d clips, %d materials", len(back.Meshes), len(back.Skins), len(back.Clips), len(back.Materials))
	}
	if back.Materials[0].Name != "red" || back.Materials[0].BaseColor[0] != .8 {
		t.Fatalf("material %+v", back.Materials[0])
	}
	k := &back.Meshes[0]
	if k.Keys[0] != "lift" || k.Weights[0] != .5 || k.KeyCount() != 1 {
		t.Fatalf("shape keys %v %v", k.Keys, k.Weights)
	}
	want, _ := a.Surfaces()
	got, _ := back.Surfaces()
	near := func(p, q Vec3) bool { return p.Sub(q).Length() < 1e-3 }
	var box [2]Box
	box[0], box[1] = EmptyBox(), EmptyBox()
	for _, s := range want {
		for _, p := range s.Pos {
			box[0].Grow(p)
		}
	}
	for _, s := range got {
		for _, p := range s.Pos {
			box[1].Grow(p)
		}
	}
	if !near(box[0].Min, box[1].Min) || !near(box[0].Max, box[1].Max) {
		t.Fatalf("bounds %v became %v", box[0], box[1])
	}
	w := back.WorldsAt(0, 1)
	knee := w[back.Skins[0].Joints[1]]
	if up := knee.Dir(Vec3{0, 1, 0}); !near(up, Vec3{-1, 0, 0}) {
		t.Fatalf("after the clip the knee points %v", up)
	}
}

func TestFBXTextForm(t *testing.T) {
	text := `; FBX 7.3.0 project file
FBXHeaderExtension:  {
	FBXVersion: 7300
}
Objects:  {
	Geometry: 10, "Geometry::tri", "Mesh" {
		Vertices: *9 {
			a: 0,0,0,1,0,0,0,1,0
		}
		PolygonVertexIndex: *3 {
			a: 0,1,-3
		}
	}
	Model: 20, "Model::tri", "Mesh" {
	}
}
Connections:  {
	C: "OO",10,20
	C: "OO",20,0
}
`
	a, err := DecodeFBX([]byte(text), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Meshes) != 1 || a.Meshes[0].Prims[0].Triangles() != 1 {
		t.Fatalf("%+v", a.Meshes)
	}
}

func TestBrokenFBXFails(t *testing.T) {
	if _, err := DecodeFBX([]byte(fbxMagic+"\xe8\x1c\x00\x00\xff\xff\xff\xff\x01\x00"), nil); err == nil {
		t.Fatal("a cut file was read")
	}
	if _, err := DecodeFBX([]byte("hello"), nil); err == nil {
		t.Fatal("words were read as a file")
	}
}

// FORGE_FBX names a real file to read.
func TestRealFBX(t *testing.T) {
	path := os.Getenv("FORGE_FBX")
	if path == "" {
		t.Skip("set FORGE_FBX")
	}
	a, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s, box := a.Surfaces()
	t.Logf("%+v; %d surfaces, bounds %v, %d images, notes %v", a.Stats(), len(s), box, len(a.Images), a.Notes)
	if len(a.Clips) > 0 {
		t.Logf("first clip %q, %v s, %d channels", a.Clips[0].Name, a.Clips[0].Duration(), len(a.Clips[0].Channels))
	}
	if os.Getenv("FORGE_FBX_OUT") != "" {
		if err := SaveFBX(a, os.Getenv("FORGE_FBX_OUT")); err != nil {
			t.Fatal(err)
		}
	}
}

// A cylinder bound to two bones bends at its middle when the second bone turns; the first half stays.
func TestBindAndKeyABone(t *testing.T) {
	prims, _ := NewPrimitive(Cylinder).Bake()
	for i := range prims {
		prims[i].Material = -1
		for v := range prims[i].Pos {
			prims[i].Pos[v][1] *= 4
		}
	}
	a := &Asset{Roots: []int{0}, Meshes: []Mesh{{Prims: prims}}, Nodes: []Node{{Parent: -1, Mesh: 0, Skin: -1, Rotation: Quat{0, 0, 0, 1}, Scale: Vec3{1, 1, 1}}}}
	box := EmptyBox()
	for _, p := range prims[0].Pos {
		box.Grow(p)
	}
	low := box.Min[1]
	hip := a.AddBone(-1, Vec3{0, low, 0})
	knee := a.AddBone(hip, Vec3{0, 0, 0})
	foot := a.AddBone(knee, Vec3{0, box.Max[1], 0})
	if err := a.Bind(0); err != nil {
		t.Fatal(err)
	}
	s, _ := a.Surfaces()
	for v, p := range s[0].Pos {
		if p.Sub(prims[0].Pos[v]).Length() > 1e-3 {
			t.Fatalf("binding moved vertex %d from %v to %v", v, prims[0].Pos[v], p)
		}
	}
	a.Clips = []Clip{{Name: "bend"}}
	turn := fbxEuler([3]float64{0, 0, 90}, 0)
	a.KeyNode(0, knee, 0, a.Nodes[knee].Translation, Quat{0, 0, 0, 1})
	a.KeyNode(0, knee, 1, a.Nodes[knee].Translation, turn)
	if got := a.KeyTimes(0, knee); len(got) != 2 {
		t.Fatalf("keys at %v", got)
	}
	s, _ = a.SurfacesIn(a.WorldsAt(0, 1), nil)
	top, bottom := -1, -1
	for v, p := range prims[0].Pos {
		if p[1] == box.Max[1] && top < 0 {
			top = v
		}
		if p[1] == box.Min[1] && bottom < 0 {
			bottom = v
		}
	}
	if d := s[0].Pos[bottom].Sub(prims[0].Pos[bottom]).Length(); d > .05 {
		t.Fatalf("the far end moved by %v", d)
	}
	if p := s[0].Pos[top]; p[1] > box.Max[1]*.4 || p[0] > -box.Max[1]*.5 {
		t.Fatalf("the top went to %v, turning from %v", p, prims[0].Pos[top])
	}
	if !a.UnkeyNode(0, knee, 1) || len(a.KeyTimes(0, knee)) != 1 {
		t.Fatal("the key did not go")
	}
	_ = foot
}

// A key put between a spline's keys leaves the others where they were.
func TestKeyingASplineKeepsItsKeys(t *testing.T) {
	a := &Asset{Nodes: []Node{{Parent: -1, Mesh: -1, Skin: -1, Rotation: Quat{0, 0, 0, 1}, Scale: Vec3{1, 1, 1}}}, Roots: []int{0}, Clips: []Clip{{Channels: []Channel{
		{Node: 0, Path: PathTranslation, Interp: CubicSpline, Width: 3, Times: []float32{0, 1}, Values: []float32{0, 0, 0, 1, 2, 3, 0, 0, 0, 0, 0, 0, 4, 5, 6, 0, 0, 0}}}}}}
	a.KeyNode(0, 0, .5, Vec3{9, 9, 9}, Quat{0, 0, 0, 1})
	if got := a.WorldsAt(0, 1)[0].Point(Vec3{}); got != (Vec3{4, 5, 6}) {
		t.Fatalf("the last key is at %v", got)
	}
	if got := a.WorldsAt(0, .5)[0].Point(Vec3{}); got != (Vec3{9, 9, 9}) {
		t.Fatalf("the new key is at %v", got)
	}
}

// Three bones in a line bend to bring the last to a point within reach.
func TestIKReachesItsTarget(t *testing.T) {
	a := &Asset{}
	root := a.AddBone(-1, Vec3{})
	mid := a.AddBone(root, Vec3{0, 1, 0})
	tip := a.AddBone(mid, Vec3{0, 2, 0})
	nodes := slices.Clone(a.Nodes)
	target := Vec3{1, 1, 0}
	turned := a.SolveIK(nodes, tip, 3, target)
	if len(turned) != 2 {
		t.Fatalf("turned %v", turned)
	}
	got := worldsOf(nodes, a.Roots)[tip].Point(Vec3{})
	if got.Sub(target).Length() > 1e-2 {
		t.Fatalf("the tip reached %v", got)
	}
	if a.Nodes[root].Rotation != (Quat{0, 0, 0, 1}) {
		t.Fatal("solving changed the asset")
	}
}

func TestPaintWeightKeepsTheSumAtOne(t *testing.T) {
	p := Prim{Joints: [][4]uint16{{0, 1, 2, 3}, {0, 0, 0, 0}}, Weights: []Vec4{{.4, .3, .2, .1}, {1, 0, 0, 0}}}
	places := []Vec3{{}, {100, 0, 0}}
	PaintWeight(&p, places, 9, Vec3{}, 1, .5)
	if w := WeightOf(&p, 0, 9); w < .49 || w > .51 {
		t.Fatalf("the new joint has %v", w)
	}
	sum := p.Weights[0][0] + p.Weights[0][1] + p.Weights[0][2] + p.Weights[0][3]
	if sum < .999 || sum > 1.001 {
		t.Fatalf("weights add up to %v", sum)
	}
	if p.Weights[1] != (Vec4{1, 0, 0, 0}) {
		t.Fatalf("a vertex out of reach became %v", p.Weights[1])
	}
	PaintWeight(&p, places, 9, Vec3{}, 1, -.5)
	if w := WeightOf(&p, 0, 9); w > .01 {
		t.Fatalf("taking away left %v", w)
	}
}
