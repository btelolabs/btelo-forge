package scene3d

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// richAsset has one of everything a file can hold.
func richAsset() *Asset {
	sampler := json.RawMessage(`{"magFilter":9728,"wrapS":33071}`)
	base := TexRef{Image: 0, UV: 1, Sampler: 0, Extensions: json.RawMessage(`{"KHR_texture_transform":{"scale":[2,2]}}`)}
	none := TexRef{Image: -1, Sampler: -1}
	return &Asset{
		Nodes: []Node{
			{Name: "root", Parent: -1, Children: []int{1, 2}, Mesh: -1, Skin: -1, Scale: Vec3{1, 1, 1}, Rotation: Quat{0, 0, 0, 1}, Translation: Vec3{1, 2, 3}},
			{Name: "skinned", Parent: 0, Mesh: 0, Skin: 0, Scale: Vec3{1, 1, 1}, Rotation: Quat{0, 0, 0, 1}},
			{Name: "joint", Parent: 0, Mesh: -1, Skin: -1, Scale: Vec3{2, 2, 2}, Rotation: Quat{0, 1, 0, 0}},
		},
		Roots: []int{0},
		Meshes: []Mesh{{Name: "mesh", Prims: []Prim{{
			Pos: []Vec3{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}}, Normal: []Vec3{{0, 0, 1}, {0, 0, 1}, {0, 0, 1}},
			Tangent: []Vec4{{1, 0, 0, 1}, {1, 0, 0, 1}, {1, 0, 0, -1}}, UV: []Vec2{{0, 0}, {1, 0}, {0, 1}}, UV2: []Vec2{{.5, .5}, {.5, 0}, {0, .5}},
			Color: []Vec4{{1, 0, 0, 1}, {0, 1, 0, 1}, {0, 0, 1, .5}}, Joints: [][4]uint16{{0, 0, 0, 0}, {0, 0, 0, 0}, {0, 0, 0, 0}}, Weights: []Vec4{{1, 0, 0, 0}, {1, 0, 0, 0}, {1, 0, 0, 0}},
			Indices: []uint32{0, 1, 2}, Material: 0}}}},
		Materials: []Material{{Name: "m", BaseColor: Vec4{.5, .25, .125, 1}, Metallic: .75, Roughness: .25, Emissive: Vec3{1, .5, 0}, NormalScale: .5, Occlusion: .75,
			AlphaMode: AlphaMask, AlphaCutoff: .25, DoubleSided: true, BaseTex: base, MetalRoughTex: none, NormalTex: TexRef{Image: 0, Sampler: -1}, OcclusionTex: TexRef{Image: 0, Sampler: -1}, EmissiveTex: none,
			Extensions: json.RawMessage(`{"KHR_materials_specular":{"specularFactor":0.5}}`)}},
		Images:   []Image{{Name: "pic", Mime: "image/png", Data: []byte("\x89PNG\r\n\x1a\nnot really")}},
		Samplers: []json.RawMessage{sampler},
		Skins:    []Skin{{Name: "skin", Joints: []int{2}, InverseBind: []Mat4{Identity()}}},
		Clips: []Clip{{Name: "walk", Channels: []Channel{
			{Node: 2, Path: PathTranslation, Interp: Linear, Times: []float32{0, 1}, Values: []float32{0, 0, 0, 1, 1, 1}, Width: 3},
			{Node: 2, Path: PathRotation, Interp: Step, Times: []float32{0}, Values: []float32{0, 0, 0, 1}, Width: 4}}}},
		ExtensionsUsed: []string{"KHR_materials_specular", "KHR_texture_transform"},
	}
}

func TestSavedFileReadsBackTheSame(t *testing.T) {
	a := richAsset()
	data, err := Encode(a)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Decode(data, noFiles)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Meshes, b.Meshes) {
		t.Errorf("meshes\n%+v\n%+v", a.Meshes, b.Meshes)
	}
	if !reflect.DeepEqual(a.Skins, b.Skins) || !reflect.DeepEqual(a.Clips, b.Clips) || !reflect.DeepEqual(a.Roots, b.Roots) {
		t.Errorf("skins, clips or roots changed")
	}
	if !bytes.Equal(a.Images[0].Data, b.Images[0].Data) || a.Images[0].Mime != b.Images[0].Mime {
		t.Errorf("the picture changed: %+v", b.Images[0])
	}
	got, want := b.Materials[0], a.Materials[0]
	if string(got.Extensions) != string(want.Extensions) || string(got.BaseTex.Extensions) != string(want.BaseTex.Extensions) || got.BaseTex.UV != 1 || got.BaseTex.Sampler != 0 {
		t.Errorf("material %+v", got)
	}
	got.Extensions, want.Extensions, got.BaseTex.Extensions, want.BaseTex.Extensions = nil, nil, nil, nil
	if !reflect.DeepEqual(got, want) {
		t.Errorf("material\n%+v\n%+v", got, want)
	}
	if string(b.Samplers[0]) != string(a.Samplers[0]) || !reflect.DeepEqual(b.ExtensionsUsed, a.ExtensionsUsed) {
		t.Errorf("samplers %s, extensions %v", b.Samplers, b.ExtensionsUsed)
	}
	for i := range a.Nodes {
		x, y := a.Nodes[i], b.Nodes[i]
		if !reflect.DeepEqual(x, y) {
			t.Errorf("node %d\n%+v\n%+v", i, x, y)
		}
	}
}

// A file saved twice is the same file: nothing drifts.
func TestSavingIsStable(t *testing.T) {
	once, err := Encode(richAsset())
	if err != nil {
		t.Fatal(err)
	}
	a, err := Decode(once, noFiles)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Encode(a); err != nil {
		t.Fatal(err)
	}
	b, err := Decode(once, noFiles)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Meshes, b.Meshes) {
		t.Fatal("reading the file again gave something else")
	}
}

func TestEmptyAssetSaves(t *testing.T) {
	data, err := Encode(&Asset{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(data, noFiles); err != nil {
		t.Fatal(err)
	}
}

// What was edited goes through the file too, as polygons turned to triangles, with a tangent-less mesh writing none.
func TestEditedMeshSaves(t *testing.T) {
	a := richAsset()
	m := EditMeshOf(&a.Meshes[0])
	m.Subdivide([]int32{0})
	m.Compact()
	a.Meshes[0].Prims, _ = m.Bake()
	path := filepath.Join(t.TempDir(), "edited.glb")
	if err := Save(a, path); err != nil {
		t.Fatal(err)
	}
	b, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p := b.Meshes[0].Prims[0]; p.Triangles() != 6 || p.Tangent != nil || len(p.Joints) != len(p.Pos) {
		t.Fatalf("%d triangles, tangents %v", p.Triangles(), p.Tangent)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatal("the temporary file stayed")
	}
}

// Shape keys go through a save, and through modelling, where two vertices on one place that part stay two.
func TestShapeKeysSurvive(t *testing.T) {
	p := Prim{Pos: []Vec3{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}, {0, 0, 0}, {-1, 0, 0}, {0, -1, 0}}, Normal: make([]Vec3, 6), Indices: []uint32{0, 1, 2, 3, 4, 5}, Material: -1,
		Morphs: []Morph{{Pos: []Vec3{{0, .5, 0}, {}, {}, {0, -.5, 0}, {}, {}}}}}
	for i := range p.Normal {
		p.Normal[i] = Vec3{0, 0, 1}
	}
	a := &Asset{Roots: []int{0}, Nodes: []Node{{Parent: -1, Mesh: 0, Skin: -1, Scale: Vec3{1, 1, 1}, Rotation: Quat{0, 0, 0, 1}}},
		Meshes: []Mesh{{Prims: []Prim{p}, Weights: []float32{.5}, Keys: []string{"smile"}}}}
	data, err := Encode(a)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Decode(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := &back.Meshes[0]
	if len(m.Prims[0].Morphs) != 1 || m.Keys[0] != "smile" || m.Weights[0] != .5 || m.Prims[0].Morphs[0].Pos[3] != (Vec3{0, -.5, 0}) {
		t.Fatalf("back as %+v", m)
	}
	s, _ := back.Surfaces()
	if got := s[0].Pos[0]; got != (Vec3{0, .25, 0}) {
		t.Fatalf("the key at a half puts the vertex at %v", got)
	}
	em := EditMeshOf(m)
	if len(em.Pos) != 6 {
		t.Fatalf("%d vertices after welding", len(em.Pos))
	}
	em.Subdivide([]int32{0})
	em.Compact()
	prims, _ := em.Bake()
	if len(prims[0].Morphs) != 1 || len(prims[0].Morphs[0].Pos) != len(prims[0].Pos) {
		t.Fatalf("%d keys over %d vertices", len(prims[0].Morphs), len(prims[0].Pos))
	}
}
