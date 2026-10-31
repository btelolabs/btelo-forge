package scene3d

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// fbxOut is an asset on its way to being a file.
type fbxOut struct {
	a         *Asset
	next      int64
	objects   []*fbxNode
	links     []*fbxNode
	counts    map[string]int
	model     []int64 // of each node
	materials []int64
	joint     map[int]bool
}

func fbxN(name string, props ...any) *fbxNode { return &fbxNode{Name: name, Props: props} }

func (n *fbxNode) with(children ...*fbxNode) *fbxNode {
	n.Children = append(n.Children, children...)
	return n
}

func (w *fbxOut) id() int64 {
	w.next++
	return w.next
}

// object adds an entry to Objects, named in the file's way of a name and a class joined by two odd bytes.
func (w *fbxOut) object(kind string, id int64, name, class, sub string) *fbxNode {
	n := fbxN(kind, id, name+"\x00\x01"+class, sub)
	w.objects = append(w.objects, n)
	w.counts[kind]++
	return n
}

func (w *fbxOut) link(child, parent int64) {
	w.links = append(w.links, fbxN("C", "OO", child, parent))
}

func (w *fbxOut) linkProp(child, parent int64, prop string) {
	w.links = append(w.links, fbxN("C", "OP", child, parent, prop))
}

func fbxP(name, kind, label, flags string, values ...any) *fbxNode {
	return fbxN("P", append([]any{name, kind, label, flags}, values...)...)
}

func fbxColor(name string, c [3]float64) *fbxNode {
	return fbxP(name, "Color", "", "A", c[0], c[1], c[2])
}

func fbxVector(name string, v [3]float64) *fbxNode {
	return fbxP(name, "Lcl "+strings.TrimPrefix(name, "Lcl "), "", "A", v[0], v[1], v[2])
}

// fbxAngles turns q into the degrees of a turn about X, then Y, then Z, the order the file's models use.
func fbxAngles(q Quat) [3]float64 {
	x, y, z, w := float64(q[0]), float64(q[1]), float64(q[2]), float64(q[3])
	sy := 2 * (w*y - z*x)
	sy = math.Min(math.Max(sy, -1), 1)
	var a, b, c float64
	b = math.Asin(sy)
	if math.Abs(sy) < .99999 {
		a = math.Atan2(2*(w*x+y*z), 1-2*(x*x+y*y))
		c = math.Atan2(2*(w*z+x*y), 1-2*(y*y+z*z))
	} else {
		a = math.Atan2(2*(x*y-w*z), 1-2*(x*x+z*z)) * 0
		c = math.Atan2(-2*(x*y-w*z), 1-2*(y*y+z*z))
	}
	k := 180 / math.Pi
	return [3]float64{a * k, b * k, c * k}
}

func vec64(v Vec3) [3]float64 { return [3]float64{float64(v[0]), float64(v[1]), float64(v[2])} }

func doubles(m Mat4) []float64 {
	out := make([]float64, 16)
	for i, v := range m {
		out[i] = float64(v)
	}
	return out
}

// EncodeFBX writes the asset as a binary .fbx of version 7400, in metres with Y up, pictures held in the file.
func EncodeFBX(a *Asset) ([]byte, error) {
	w := &fbxOut{a: a, next: 100000, counts: map[string]int{}, model: make([]int64, len(a.Nodes)), joint: map[int]bool{}}
	for i := range a.Skins {
		for _, j := range a.Skins[i].Joints {
			w.joint[j] = true
		}
	}
	for i := range a.Materials {
		w.material(i)
	}
	worlds := a.Worlds()
	bound := boundMeshes(a, worlds)
	for i := range a.Nodes {
		w.modelNode(i, bound)
	}
	for i := range a.Nodes {
		parent := a.Nodes[i].Parent
		if b, ok := bound[i]; ok {
			parent = b.holder
		}
		if parent >= 0 {
			w.link(w.model[i], w.model[parent])
		} else {
			w.link(w.model[i], 0)
		}
	}
	for i := range a.Nodes {
		if a.Nodes[i].Mesh >= 0 {
			if err := w.geometry(i, bound); err != nil {
				return nil, err
			}
		}
	}
	for i := range a.Clips {
		w.clip(&a.Clips[i])
	}
	return encodeFBX(w.document()), nil
}

// SaveFBX writes the asset to an .fbx file, through a temporary file so that a failure leaves what was there.
func SaveFBX(a *Asset, path string) error {
	data, err := EncodeFBX(a)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (w *fbxOut) document() []*fbxNode {
	header := fbxN("FBXHeaderExtension").with(fbxN("FBXHeaderVersion", int32(1003)), fbxN("FBXVersion", int32(fbxVersion)), fbxN("Creator", "Btelo Forge"))
	settings := fbxN("GlobalSettings").with(fbxN("Version", int32(1000)), fbxN("Properties70").with(
		fbxP("UpAxis", "int", "Integer", "", int32(1)), fbxP("UpAxisSign", "int", "Integer", "", int32(1)),
		fbxP("FrontAxis", "int", "Integer", "", int32(2)), fbxP("FrontAxisSign", "int", "Integer", "", int32(1)),
		fbxP("CoordAxis", "int", "Integer", "", int32(0)), fbxP("CoordAxisSign", "int", "Integer", "", int32(1)),
		fbxP("UnitScaleFactor", "double", "Number", "", 100.0), fbxP("OriginalUnitScaleFactor", "double", "Number", "", 100.0),
		fbxP("TimeMode", "enum", "", "", int32(6)), fbxP("CustomFrameRate", "double", "Number", "", 30.0)))
	definitions := fbxN("Definitions").with(fbxN("Version", int32(100)))
	total := int32(0)
	for kind, n := range w.counts {
		definitions.with(fbxN("ObjectType", kind).with(fbxN("Count", int32(n))))
		total += int32(n)
	}
	definitions.Children = append(definitions.Children[:1], append([]*fbxNode{fbxN("Count", total)}, definitions.Children[1:]...)...)
	objects := fbxN("Objects").with(w.objects...)
	connections := fbxN("Connections").with(w.links...)
	return []*fbxNode{header, settings, fbxN("Documents").with(fbxN("Count", int32(0))), fbxN("References"), definitions, objects, connections, fbxN("Takes").with(fbxN("Current", ""))}
}

// fbxBound is where a skinned mesh stands in the file: its model's place in the world, and what its vertices need to be
// right from there.
type fbxBound struct {
	world, vertices Mat4
	holder          int // the node it hangs from in the file, that holds the skeleton, or -1
}

// boundMeshes places each skinned mesh with the skeleton it follows. glTF passes over such a node's own place, a mesh
// going straight through its joints, while FBX takes a mesh to be bound where its model is, and importers expect that
// to share the scale of what holds the skeleton.
func boundMeshes(a *Asset, worlds []Mat4) map[int]fbxBound {
	out := map[int]fbxBound{}
	for i := range a.Nodes {
		k := a.Nodes[i].Skin
		if k < 0 || len(a.Skins[k].Joints) == 0 {
			continue
		}
		s := &a.Skins[k]
		rest := worlds[s.Joints[0]].Mul(s.InverseBind[0])
		top := s.Joints[0]
		for a.Nodes[top].Parent >= 0 && slices.Contains(s.Joints, a.Nodes[top].Parent) {
			top = a.Nodes[top].Parent
		}
		place, holder := Identity(), a.Nodes[top].Parent
		for n := holder; n >= 0; n = a.Nodes[n].Parent {
			if n == i { // the mesh cannot hang from itself or what hangs from it
				holder = a.Nodes[i].Parent
				break
			}
		}
		if holder >= 0 {
			place = worlds[holder]
		}
		out[i] = fbxBound{place, place.Inverse().Mul(rest), holder}
	}
	return out
}

func (w *fbxOut) modelNode(i int, bound map[int]fbxBound) {
	n := &w.a.Nodes[i]
	place := *n
	if _, ok := bound[i]; ok {
		place.Translation, place.Rotation, place.Scale = Vec3{}, Quat{0, 0, 0, 1}, Vec3{1, 1, 1}
	}
	n = &place
	id := w.id()
	w.model[i] = id
	name := n.Name
	if name == "" {
		name = fmt.Sprintf("Node %d", i)
	}
	kind := "Null"
	switch {
	case n.Mesh >= 0:
		kind = "Mesh"
	case w.joint[i]:
		kind = "LimbNode"
	}
	if n.Mesh < 0 {
		attribute := w.id()
		w.object("NodeAttribute", attribute, name, "NodeAttribute", kind)
		w.link(attribute, id)
	}
	m := w.object("Model", id, name, "Model", kind)
	m.with(fbxN("Version", int32(232)), fbxN("Properties70").with(
		fbxP("RotationActive", "bool", "", "", int32(1)), fbxP("InheritType", "enum", "", "", int32(1)),
		fbxVector("Lcl Translation", vec64(n.Translation)), fbxVector("Lcl Rotation", fbxAngles(n.Rotation)), fbxVector("Lcl Scaling", vec64(n.Scale))),
		fbxN("Shading", true), fbxN("Culling", "CullingOff"))
}

func (w *fbxOut) material(i int) {
	m := &w.a.Materials[i]
	id := w.id()
	w.materials = append(w.materials, id)
	name := m.Name
	if name == "" {
		name = fmt.Sprintf("Material %d", i+1)
	}
	transparency := 1 - float64(m.BaseColor[3])
	shine := (1 - float64(m.Roughness)) * (1 - float64(m.Roughness)) * 100
	n := w.object("Material", id, name, "Material", "")
	n.with(fbxN("Version", int32(102)), fbxN("ShadingModel", "phong"), fbxN("Properties70").with(
		fbxColor("DiffuseColor", [3]float64{float64(m.BaseColor[0]), float64(m.BaseColor[1]), float64(m.BaseColor[2])}),
		fbxP("DiffuseFactor", "double", "Number", "", 1.0), fbxColor("EmissiveColor", vec64(m.Emissive)), fbxP("EmissiveFactor", "double", "Number", "", 1.0),
		fbxP("TransparencyFactor", "double", "Number", "", transparency), fbxP("ReflectionFactor", "double", "Number", "", float64(m.Metallic)),
		fbxP("Shininess", "double", "Number", "", shine), fbxP("ShininessExponent", "double", "Number", "", shine)))
	for _, t := range []struct {
		ref  TexRef
		prop string
	}{{m.BaseTex, "DiffuseColor"}, {m.NormalTex, "NormalMap"}, {m.EmissiveTex, "EmissiveColor"}} {
		if t.ref.Image >= 0 && t.ref.Image < len(w.a.Images) {
			w.texture(t.ref.Image, id, t.prop)
		}
	}
}

func (w *fbxOut) texture(image int, material int64, prop string) {
	im := &w.a.Images[image]
	name := im.Name
	if name == "" {
		name = fmt.Sprintf("picture%d.png", image+1)
	}
	if filepath.Ext(name) == "" {
		name += map[string]string{"image/jpeg": ".jpg"}[im.Mime]
		if filepath.Ext(name) == "" {
			name += ".png"
		}
	}
	video, texture := w.id(), w.id()
	w.object("Video", video, name, "Video", "Clip").with(fbxN("Type", "Clip"), fbxN("RelativeFilename", name), fbxN("Content", im.Data))
	w.object("Texture", texture, name, "Texture", "").with(fbxN("Type", "TextureVideoClip"), fbxN("Version", int32(202)), fbxN("TextureName", name+"\x00\x01Texture"),
		fbxN("Media", name+"\x00\x01Video"), fbxN("FileName", name), fbxN("RelativeFilename", name))
	w.link(video, texture)
	w.linkProp(texture, material, prop)
}

// geometry writes the mesh of node i, once for each node showing it, with its material list, skin and shape keys.
func (w *fbxOut) geometry(i int, bound map[int]fbxBound) error {
	a := w.a
	fix, fixed := bound[i]
	node := &a.Nodes[i]
	mesh := &a.Meshes[node.Mesh]
	var pos, normals, uvs, colors []float64
	var polys, uvIndex, materials []int32
	var joints [][4]uint16
	var weights []Vec4
	used := map[int]int32{}
	slot := func(m int) int32 {
		if at, ok := used[m]; ok {
			return at
		}
		at := int32(len(used))
		used[m] = at
		return at
	}
	var morphs [][]Vec3
	morphs = make([][]Vec3, mesh.KeyCount())
	base := int32(0)
	for j := range mesh.Prims {
		p := &mesh.Prims[j]
		for v := range p.Pos {
			q := p.Pos[v]
			if fixed {
				q = fix.vertices.Point(q)
			}
			pos = append(pos, float64(q[0]), float64(q[1]), float64(q[2]))
		}
		for k := range morphs {
			if k < len(p.Morphs) {
				for _, d := range p.Morphs[k].Pos {
					if fixed {
						d = fix.vertices.Dir(d)
					}
					morphs[k] = append(morphs[k], d)
				}
			} else {
				morphs[k] = append(morphs[k], make([]Vec3, len(p.Pos))...)
			}
		}
		for _, q := range p.UV {
			uvs = append(uvs, float64(q[0]), 1-float64(q[1]))
		}
		if p.UV == nil {
			uvs = append(uvs, make([]float64, len(p.Pos)*2)...)
		}
		for v := range p.Pos {
			if p.Joints != nil {
				joints, weights = append(joints, p.Joints[v]), append(weights, p.Weights[v])
			}
			if p.Color != nil {
				c := p.Color[v]
				colors = append(colors, float64(c[0]), float64(c[1]), float64(c[2]), float64(c[3]))
			} else {
				colors = append(colors, 1, 1, 1, 1)
			}
		}
		m := slot(p.Material)
		for t := 0; t+2 < len(p.Indices); t += 3 {
			a, b, c := base+int32(p.Indices[t]), base+int32(p.Indices[t+1]), base+int32(p.Indices[t+2])
			polys = append(polys, a, b, ^c)
			uvIndex = append(uvIndex, a, b, c)
			materials = append(materials, m)
			for _, q := range []uint32{p.Indices[t], p.Indices[t+1], p.Indices[t+2]} {
				n := p.Normal[q]
				if fixed {
					n = fix.vertices.Normal(n)
				}
				normals = append(normals, float64(n[0]), float64(n[1]), float64(n[2]))
			}
		}
		base += int32(len(p.Pos))
	}
	id := w.id()
	name := mesh.Name
	if name == "" {
		name = fmt.Sprintf("Mesh %d", node.Mesh)
	}
	layer := func(kind, name string, n int32, extra ...*fbxNode) *fbxNode {
		return fbxN(kind, n).with(append([]*fbxNode{fbxN("Version", int32(101)), fbxN("Name", name)}, extra...)...)
	}
	g := w.object("Geometry", id, name, "Geometry", "Mesh").with(fbxN("Properties70"), fbxN("GeometryVersion", int32(124)), fbxN("Vertices", pos), fbxN("PolygonVertexIndex", polys), fbxN("Edges", []int32{}),
		layer("LayerElementNormal", "", 0, fbxN("MappingInformationType", "ByPolygonVertex"), fbxN("ReferenceInformationType", "Direct"), fbxN("Normals", normals)),
		layer("LayerElementUV", "UVMap", 0, fbxN("MappingInformationType", "ByPolygonVertex"), fbxN("ReferenceInformationType", "IndexToDirect"), fbxN("UV", uvs), fbxN("UVIndex", uvIndex)),
		layer("LayerElementMaterial", "", 0, fbxN("MappingInformationType", "ByPolygon"), fbxN("ReferenceInformationType", "IndexToDirect"), fbxN("Materials", materials)))
	if colorsWorth(colors) {
		g.with(layer("LayerElementColor", "Colors", 0, fbxN("MappingInformationType", "ByPolygonVertex"), fbxN("ReferenceInformationType", "IndexToDirect"), fbxN("Colors", colors), fbxN("ColorIndex", uvIndex)))
	}
	elements := []*fbxNode{fbxN("Version", int32(100)), fbxN("LayerElement").with(fbxN("Type", "LayerElementNormal"), fbxN("TypedIndex", int32(0))),
		fbxN("LayerElement").with(fbxN("Type", "LayerElementUV"), fbxN("TypedIndex", int32(0))),
		fbxN("LayerElement").with(fbxN("Type", "LayerElementMaterial"), fbxN("TypedIndex", int32(0)))}
	g.with(fbxN("Layer", int32(0)).with(elements...))
	w.link(id, w.model[i])
	order := make([]int, len(used))
	for m, at := range used {
		order[at] = m
	}
	for _, m := range order {
		if m < 0 || m >= len(w.materials) {
			m = w.defaultMaterial()
		}
		w.link(w.materials[m], w.model[i])
	}
	if node.Skin >= 0 {
		w.skin(i, id, joints, weights, fix)
	}
	w.shapeKeys(id, mesh, morphs)
	return nil
}

func colorsWorth(colors []float64) bool {
	for _, c := range colors {
		if c != 1 {
			return true
		}
	}
	return false
}

// defaultMaterial is a plain material for parts with none, added once.
func (w *fbxOut) defaultMaterial() int {
	if n := len(w.a.Materials); len(w.materials) > n {
		return n
	}
	id := w.id()
	w.materials = append(w.materials, id)
	w.object("Material", id, "Default", "Material", "").with(fbxN("Version", int32(102)), fbxN("ShadingModel", "phong"), fbxN("Properties70"))
	return len(w.materials) - 1
}

func (w *fbxOut) skin(i int, geometry int64, joints [][4]uint16, weights []Vec4, bound fbxBound) {
	a := w.a
	s := &a.Skins[a.Nodes[i].Skin]
	skin := w.id()
	w.object("Deformer", skin, s.Name, "Deformer", "Skin").with(fbxN("Version", int32(101)), fbxN("Link_DeformAcuracy", 50.0))
	w.link(skin, geometry)
	indexes := make([][]int32, len(s.Joints))
	strengths := make([][]float64, len(s.Joints))
	for v := range joints {
		for k := range 4 {
			if weights[v][k] > 0 && int(joints[v][k]) < len(s.Joints) {
				j := joints[v][k]
				indexes[j] = append(indexes[j], int32(v))
				strengths[j] = append(strengths[j], float64(weights[v][k]))
			}
		}
	}
	for j, node := range s.Joints {
		// The bone where it was bound; Transform is the mesh as seen from there.
		link := bound.world.Mul(bound.vertices).Mul(s.InverseBind[j].Inverse())
		cluster := w.id()
		w.object("Deformer", cluster, a.Nodes[node].Name, "SubDeformer", "Cluster").with(fbxN("Version", int32(100)), fbxN("UserData", "", ""),
			fbxN("Indexes", indexes[j]), fbxN("Weights", strengths[j]), fbxN("Transform", doubles(link.Inverse().Mul(bound.world))), fbxN("TransformLink", doubles(link)), fbxN("TransformAssociateModel", doubles(bound.world)))
		w.link(cluster, skin)
		w.link(w.model[node], cluster)
	}
}

func (w *fbxOut) shapeKeys(geometry int64, mesh *Mesh, morphs [][]Vec3) {
	if len(morphs) == 0 {
		return
	}
	deformer := w.id()
	w.object("Deformer", deformer, mesh.Name, "Deformer", "BlendShape").with(fbxN("Version", int32(100)))
	w.link(deformer, geometry)
	for k, deltas := range morphs {
		name := fmt.Sprintf("Key %d", k+1)
		if k < len(mesh.Keys) && mesh.Keys[k] != "" {
			name = mesh.Keys[k]
		}
		var index []int32
		var values, normals []float64
		for v, d := range deltas {
			if d != (Vec3{}) {
				index = append(index, int32(v))
				values = append(values, float64(d[0]), float64(d[1]), float64(d[2]))
				normals = append(normals, 0, 0, 0)
			}
		}
		strength := 0.0
		if k < len(mesh.Weights) {
			strength = float64(mesh.Weights[k]) * 100
		}
		channel, shape := w.id(), w.id()
		w.object("Deformer", channel, name, "SubDeformer", "BlendShapeChannel").with(fbxN("Version", int32(100)), fbxN("DeformPercent", strength), fbxN("FullWeights", []float64{100}),
			fbxN("Properties70").with(fbxP("DeformPercent", "Number", "", "A", strength)))
		w.object("Geometry", shape, name, "Geometry", "Shape").with(fbxN("Version", int32(100)), fbxN("Indexes", index), fbxN("Vertices", values), fbxN("Normals", normals))
		w.link(channel, deformer)
		w.link(shape, channel)
	}
}

// clip writes an animation stack of one layer, with turns as angles, kept near each other from key to key.
func (w *fbxOut) clip(c *Clip) {
	stack, layer := w.id(), w.id()
	name := c.Name
	if name == "" {
		name = "Take"
	}
	stop := int64(float64(c.Duration()) * fbxSecond)
	w.object("AnimationStack", stack, name, "AnimStack", "").with(fbxN("Properties70").with(fbxP("LocalStop", "KTime", "Time", "", stop), fbxP("ReferenceStop", "KTime", "Time", "", stop)))
	w.object("AnimationLayer", layer, "BaseLayer", "AnimLayer", "")
	w.link(layer, stack)
	for i := range c.Channels {
		ch := &c.Channels[i]
		if ch.Path == PathWeights || ch.Node < 0 || ch.Node >= len(w.model) || len(ch.Times) == 0 {
			continue
		}
		prop := map[Path]string{PathTranslation: "Lcl Translation", PathRotation: "Lcl Rotation", PathScale: "Lcl Scaling"}[ch.Path]
		times := make([]int64, len(ch.Times))
		values := [3][]float32{}
		var last [3]float64
		for k, t := range ch.Times {
			times[k] = int64(float64(t) * fbxSecond)
			var v [3]float64
			if ch.Path == PathRotation {
				v = fbxAngles(ch.rotation(t))
				for a := range v {
					if k > 0 {
						v[a] += 360 * math.Round((last[a]-v[a])/360)
					}
				}
				last = v
			} else {
				s := ch.sample(t)
				v = [3]float64{float64(s[0]), float64(s[1]), float64(s[2])}
			}
			for a := range v {
				values[a] = append(values[a], float32(v[a]))
			}
		}
		node := w.id()
		w.object("AnimationCurveNode", node, strings.ToUpper(prop[4:5]), "AnimCurveNode", "").with(fbxN("Properties70").with(
			fbxP("d|X", "Number", "", "A", float64(values[0][0])), fbxP("d|Y", "Number", "", "A", float64(values[1][0])), fbxP("d|Z", "Number", "", "A", float64(values[2][0]))))
		w.link(node, layer)
		w.linkProp(node, w.model[ch.Node], prop)
		for a, axis := range []string{"d|X", "d|Y", "d|Z"} {
			curve := w.id()
			w.object("AnimationCurve", curve, "", "AnimCurve", "").with(fbxN("Default", float64(values[a][0])), fbxN("KeyVer", int32(4008)), fbxN("KeyTime", times), fbxN("KeyValueFloat", values[a]),
				fbxN("KeyAttrFlags", []int32{4}), fbxN("KeyAttrDataFloat", []float32{0, 0, 0, 0}), fbxN("KeyAttrRefCount", []int32{int32(len(times))}))
			w.linkProp(curve, node, axis)
		}
	}
}
