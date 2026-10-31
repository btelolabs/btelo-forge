package scene3d

import (
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
)

const fbxSecond = 46186158000 // the file's units of time in a second

// fbxObject is an entry of the file's Objects, with what is joined to it.
type fbxObject struct {
	id       int64
	kind     string // the node's name: Model, Geometry and so on
	name     string
	class    string
	node     *fbxNode
	children []fbxLink // what is joined to this
	parents  []fbxLink
}

type fbxLink struct {
	to   *fbxObject
	prop string // the property it is joined by, if any
}

func (o *fbxObject) kids(kind string) []fbxLink {
	var out []fbxLink
	for _, l := range o.children {
		if l.to.kind == kind {
			out = append(out, l)
		}
	}
	return out
}

// fbxProps is a node's Properties70 by name, each with what follows its four words.
func fbxProps(n *fbxNode) map[string][]any {
	out := map[string][]any{}
	block := n.child("Properties70")
	if block == nil {
		block = n.child("Properties60")
	}
	if block == nil {
		return out
	}
	for _, p := range block.Children {
		if len(p.Props) == 0 {
			continue
		}
		name, _ := p.Props[0].(string)
		skip := min(len(p.Props), 4)
		if block.Name == "Properties60" {
			skip = min(len(p.Props), 3)
		}
		out[name] = p.Props[skip:]
	}
	return out
}

func fbxNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	}
	return 0, false
}

func propVec(p map[string][]any, name string, fallback [3]float64) [3]float64 {
	v := p[name]
	if len(v) < 3 {
		return fallback
	}
	for i := range 3 {
		if f, ok := fbxNumber(v[i]); ok {
			fallback[i] = f
		}
	}
	return fallback
}

func propFloat(p map[string][]any, name string, fallback float64) float64 {
	if v := p[name]; len(v) > 0 {
		if f, ok := fbxNumber(v[0]); ok {
			return f
		}
	}
	return fallback
}

func propInt(p map[string][]any, name string, fallback int) int {
	return int(propFloat(p, name, float64(fallback)))
}

func translation(v [3]float64) Mat4 {
	return Compose(Vec3{float32(v[0]), float32(v[1]), float32(v[2])}, Quat{0, 0, 0, 1}, Vec3{1, 1, 1})
}

func negated(v [3]float64) [3]float64 { return [3]float64{-v[0], -v[1], -v[2]} }

// fbxEuler is a turn of degrees about the axes in the order the file's RotationOrder says, the first applied first.
func fbxEuler(deg [3]float64, order int) Quat {
	axes := [6][3]int{{0, 1, 2}, {0, 2, 1}, {1, 2, 0}, {1, 0, 2}, {2, 0, 1}, {2, 1, 0}}[min(max(order, 0), 5)]
	q := Quat{0, 0, 0, 1}
	for _, a := range axes {
		s, c := math.Sincos(deg[a] * math.Pi / 360)
		var t Quat
		t[a], t[3] = float32(s), float32(c)
		q = quatMul(t, q)
	}
	return q
}

func quatMul(a, b Quat) Quat {
	return Quat{
		a[3]*b[0] + a[0]*b[3] + a[1]*b[2] - a[2]*b[1],
		a[3]*b[1] - a[0]*b[2] + a[1]*b[3] + a[2]*b[0],
		a[3]*b[2] + a[0]*b[1] - a[1]*b[0] + a[2]*b[3],
		a[3]*b[3] - a[0]*b[0] - a[1]*b[1] - a[2]*b[2],
	}
}

// fbxTransform is a model's own place, which an animation changes only in its three main parts.
type fbxTransform struct {
	t, r, s                                             [3]float64
	pre, post, rotOffset, rotPivot, sclOffset, sclPivot [3]float64
	order                                               int
}

func fbxTransformOf(p map[string][]any) fbxTransform {
	return fbxTransform{t: propVec(p, "Lcl Translation", [3]float64{}), r: propVec(p, "Lcl Rotation", [3]float64{}), s: propVec(p, "Lcl Scaling", [3]float64{1, 1, 1}),
		pre: propVec(p, "PreRotation", [3]float64{}), post: propVec(p, "PostRotation", [3]float64{}),
		rotOffset: propVec(p, "RotationOffset", [3]float64{}), rotPivot: propVec(p, "RotationPivot", [3]float64{}),
		sclOffset: propVec(p, "ScalingOffset", [3]float64{}), sclPivot: propVec(p, "ScalingPivot", [3]float64{}),
		order: propInt(p, "RotationOrder", 0)}
}

func (f *fbxTransform) matrix() Mat4 {
	rot := func(deg [3]float64, order int) Mat4 { return Compose(Vec3{}, fbxEuler(deg, order), Vec3{1, 1, 1}) }
	scale := Compose(Vec3{}, Quat{0, 0, 0, 1}, Vec3{float32(f.s[0]), float32(f.s[1]), float32(f.s[2])})
	m := translation(f.t).Mul(translation(f.rotOffset)).Mul(translation(f.rotPivot)).Mul(rot(f.pre, 0)).Mul(rot(f.r, f.order)).Mul(rot(f.post, 0).Inverse())
	return m.Mul(translation(negated(f.rotPivot))).Mul(translation(f.sclOffset)).Mul(translation(f.sclPivot)).Mul(scale).Mul(translation(negated(f.sclPivot)))
}

// fbxDoc is a file on its way to being an asset.
type fbxDoc struct {
	top       *fbxNode
	objects   map[int64]*fbxObject
	asset     *Asset
	read      func(string) ([]byte, error)
	nodeOf    map[*fbxObject]int
	materials map[*fbxObject]int
	images    map[*fbxObject]int
	blender   bool
}

func fbxNameOf(n *fbxNode) (name, class string) {
	if len(n.Props) >= 2 {
		name, _ = n.Props[1].(string)
	}
	if len(n.Props) >= 3 {
		class, _ = n.Props[2].(string)
	}
	if i := strings.Index(name, "\x00\x01"); i >= 0 {
		name = name[:i]
	} else if i := strings.Index(name, "::"); i >= 0 {
		name = name[i+2:]
	}
	return name, class
}

// DecodeFBX reads a binary or text .fbx file; read finds the pictures it names but does not hold.
func DecodeFBX(data []byte, read func(string) ([]byte, error)) (*Asset, error) {
	top, version, err := parseFBX(data)
	if err != nil {
		return nil, err
	}
	if version < 7000 {
		return nil, fmt.Errorf("FBX version %d is too old, it has to be 2011 or later", version)
	}
	d := &fbxDoc{top: top, objects: map[int64]*fbxObject{}, asset: &Asset{}, read: read, nodeOf: map[*fbxObject]int{}, materials: map[*fbxObject]int{}, images: map[*fbxObject]int{}}
	creator := top.child("Creator").word() + top.child("FBXHeaderExtension").child("Creator").word()
	d.blender = strings.Contains(creator, "Blender")
	for _, n := range top.child("Objects").Children {
		ids := n.ints()
		if len(ids) == 0 {
			continue
		}
		o := &fbxObject{id: ids[0], kind: n.Name, node: n}
		o.name, o.class = fbxNameOf(n)
		d.objects[o.id] = o
	}
	for _, c := range top.child("Connections").Children {
		if len(c.Props) < 3 {
			continue
		}
		child, _ := c.Props[1].(int64)
		parent, _ := c.Props[2].(int64)
		prop := ""
		if len(c.Props) > 3 {
			prop, _ = c.Props[3].(string)
		}
		from, to := d.objects[child], d.objects[parent]
		if from != nil && to != nil {
			to.children = append(to.children, fbxLink{from, prop})
			from.parents = append(from.parents, fbxLink{to, prop})
		}
	}
	if err := d.build(); err != nil {
		return nil, err
	}
	return d.asset, nil
}

func sortedObjects(objects map[int64]*fbxObject, kind string) []*fbxObject {
	var out []*fbxObject
	for _, o := range objects {
		if o.kind == kind {
			out = append(out, o)
		}
	}
	slices.SortFunc(out, func(a, b *fbxObject) int {
		switch {
		case a.id < b.id:
			return -1
		case a.id > b.id:
			return 1
		}
		return 0
	})
	return out
}

func (d *fbxDoc) build() error {
	a := d.asset
	models := sortedObjects(d.objects, "Model")
	settings := fbxProps(d.top.child("GlobalSettings"))
	unit := propFloat(settings, "UnitScaleFactor", 1) / 100 // of metres
	up := propInt(settings, "UpAxis", 1)
	root := Node{Name: "FBX", Parent: -1, Mesh: -1, Skin: -1, Scale: Vec3{float32(unit), float32(unit), float32(unit)}, Rotation: Quat{0, 0, 0, 1}}
	if up == 2 {
		root.Rotation = fbxEuler([3]float64{-90, 0, 0}, 0)
	}
	a.Nodes = append(a.Nodes, root)
	a.Roots = []int{0}
	for _, m := range models {
		f := fbxTransformOf(fbxProps(m.node))
		t, r, s := Decompose(f.matrix())
		d.nodeOf[m] = len(a.Nodes)
		a.Nodes = append(a.Nodes, Node{Name: m.name, Parent: 0, Mesh: -1, Skin: -1, Translation: t, Rotation: r, Scale: s})
	}
	for _, m := range models {
		n := d.nodeOf[m]
		for _, l := range m.parents {
			if l.to.kind == "Model" {
				a.Nodes[n].Parent = d.nodeOf[l.to]
			}
		}
		parent := a.Nodes[n].Parent
		a.Nodes[parent].Children = append(a.Nodes[parent].Children, n)
	}
	for _, m := range models {
		for _, g := range m.kids("Geometry") {
			if err := d.mesh(m, g.to); err != nil {
				return fmt.Errorf("mesh %s: %w", m.name, err)
			}
			break
		}
	}
	d.clips(models)
	return nil
}

// fbxLayer reads one layer element for each corner of a mesh.
type fbxLayer struct {
	values []float64
	index  []int64
	width  int
	mode   string
}

func layerOf(g *fbxNode, element, values, index string, width int) *fbxLayer {
	e := g.child(element)
	if e == nil {
		return nil
	}
	l := &fbxLayer{values: e.child(values).floats(), width: width, mode: e.child("MappingInformationType").word()}
	if e.child("ReferenceInformationType").word() != "Direct" {
		l.index = e.child(index).ints()
	}
	if len(l.values) == 0 {
		return nil
	}
	return l
}

// at is the value for a corner, of a polygon and a control point, or nil outside the list.
func (l *fbxLayer) at(corner, polygon, point int) []float64 {
	if l == nil {
		return nil
	}
	i := corner
	switch l.mode {
	case "ByVertice", "ByVertex":
		i = point
	case "ByPolygon":
		i = polygon
	case "AllSame":
		i = 0
	}
	if l.index != nil {
		if i >= len(l.index) {
			return nil
		}
		i = int(l.index[i])
	}
	if i < 0 || (i+1)*l.width > len(l.values) {
		return nil
	}
	return l.values[i*l.width : (i+1)*l.width]
}

type fbxCorner struct {
	point int
	n     Vec3
	uv    Vec2
	c     Vec4
}

func (d *fbxDoc) mesh(m, g *fbxObject) error {
	a := d.asset
	n := d.nodeOf[m]
	vertices := g.node.child("Vertices").floats()
	polys := g.node.child("PolygonVertexIndex").ints()
	if len(vertices) == 0 || len(polys) == 0 {
		return nil
	}
	points := make([]Vec3, len(vertices)/3)
	p := fbxProps(m.node)
	geometric := Compose(vec3Of(propVec(p, "GeometricTranslation", [3]float64{})), fbxEuler(propVec(p, "GeometricRotation", [3]float64{}), 0), vec3Of(propVec(p, "GeometricScaling", [3]float64{1, 1, 1})))
	for i := range points {
		points[i] = geometric.Point(Vec3{float32(vertices[i*3]), float32(vertices[i*3+1]), float32(vertices[i*3+2])})
	}
	normals := layerOf(g.node, "LayerElementNormal", "Normals", "NormalsIndex", 3)
	uvs := layerOf(g.node, "LayerElementUV", "UV", "UVIndex", 2)
	colors := layerOf(g.node, "LayerElementColor", "Colors", "ColorIndex", 4)
	var materialIndex []int64
	materialMode := ""
	if e := g.node.child("LayerElementMaterial"); e != nil {
		materialIndex, materialMode = e.child("Materials").ints(), e.child("MappingInformationType").word()
	}
	slots := m.kids("Material")
	byMaterial := map[int]int{}
	mesh := Mesh{Name: g.name}
	type built struct {
		seen map[fbxCorner]uint32
		from []int
	}
	parts := map[int]*built{}
	var poly []int64
	polygon, corner := 0, 0
	for _, raw := range polys {
		done := raw < 0
		if done {
			raw = ^raw
		}
		if int(raw) >= len(points) {
			return fmt.Errorf("a polygon uses a point that is not there")
		}
		poly = append(poly, raw)
		corner++
		if !done {
			continue
		}
		slot := 0
		if len(materialIndex) > 0 {
			if materialMode == "AllSame" {
				slot = int(materialIndex[0])
			} else if polygon < len(materialIndex) {
				slot = int(materialIndex[polygon])
			}
		}
		at, ok := byMaterial[slot]
		if !ok {
			at = len(mesh.Prims)
			byMaterial[slot] = at
			material := -1
			if slot >= 0 && slot < len(slots) {
				var err error
				if material, err = d.material(slots[slot].to); err != nil {
					return err
				}
			}
			mesh.Prims = append(mesh.Prims, Prim{Material: material})
			parts[at] = &built{seen: map[fbxCorner]uint32{}}
		}
		pr, b := &mesh.Prims[at], parts[at]
		first := corner - len(poly)
		var face Vec3
		if normals == nil && len(poly) >= 3 {
			face = points[poly[1]].Sub(points[poly[0]]).Cross(points[poly[2]].Sub(points[poly[0]])).Norm()
		}
		index := make([]uint32, len(poly))
		for i, q := range poly {
			k := fbxCorner{point: int(q), n: face, c: Vec4{1, 1, 1, 1}}
			if v := normals.at(first+i, polygon, int(q)); v != nil {
				k.n = Vec3{float32(v[0]), float32(v[1]), float32(v[2])}
			}
			if v := uvs.at(first+i, polygon, int(q)); v != nil {
				k.uv = Vec2{float32(v[0]), 1 - float32(v[1])}
			}
			if v := colors.at(first+i, polygon, int(q)); v != nil {
				k.c = Vec4{float32(v[0]), float32(v[1]), float32(v[2]), float32(v[3])}
			}
			done, ok := b.seen[k]
			if !ok {
				done = uint32(len(pr.Pos))
				b.seen[k] = done
				pr.Pos = append(pr.Pos, points[q])
				pr.Normal = append(pr.Normal, k.n)
				pr.UV = append(pr.UV, k.uv)
				if colors != nil {
					pr.Color = append(pr.Color, k.c)
				}
				b.from = append(b.from, int(q))
			}
			index[i] = done
		}
		for i := 2; i < len(index); i++ {
			pr.Indices = append(pr.Indices, index[0], index[i-1], index[i])
		}
		poly = poly[:0]
		polygon++
	}
	from := make([][]int, len(mesh.Prims))
	for i := range mesh.Prims {
		from[i] = parts[i].from
	}
	d.shapes(g, &mesh, from, len(points))
	a.Meshes = append(a.Meshes, mesh)
	a.Nodes[n].Mesh = len(a.Meshes) - 1
	d.skin(n, g, &a.Meshes[len(a.Meshes)-1], from, len(points))
	return nil
}

func vec3Of(v [3]float64) Vec3 { return Vec3{float32(v[0]), float32(v[1]), float32(v[2])} }

// shapes reads the geometry's blend shapes as shape keys.
func (d *fbxDoc) shapes(g *fbxObject, mesh *Mesh, from [][]int, points int) {
	for _, deformer := range g.kids("Deformer") {
		if deformer.to.class != "BlendShape" {
			continue
		}
		for _, channel := range deformer.to.kids("Deformer") {
			shapes := channel.to.kids("Geometry")
			if len(shapes) == 0 {
				continue
			}
			shape := shapes[0].to.node
			index, values := shape.child("Indexes").ints(), shape.child("Vertices").floats()
			normals := shape.child("Normals").floats()
			pos := make([]Vec3, points)
			var turn []Vec3
			if len(normals) == len(values) && len(normals) > 0 {
				turn = make([]Vec3, points)
			}
			for i, q := range index {
				if int(q) < points && (i+1)*3 <= len(values) {
					pos[q] = Vec3{float32(values[i*3]), float32(values[i*3+1]), float32(values[i*3+2])}
					if turn != nil {
						turn[q] = Vec3{float32(normals[i*3]), float32(normals[i*3+1]), float32(normals[i*3+2])}
					}
				}
			}
			for i := range mesh.Prims {
				morph := Morph{Pos: make([]Vec3, len(from[i]))}
				if turn != nil {
					morph.Normal = make([]Vec3, len(from[i]))
				}
				for v, q := range from[i] {
					morph.Pos[v] = pos[q]
					if turn != nil {
						morph.Normal[v] = turn[q]
					}
				}
				mesh.Prims[i].Morphs = append(mesh.Prims[i].Morphs, morph)
			}
			mesh.Keys = append(mesh.Keys, channel.to.name)
			mesh.Weights = append(mesh.Weights, float32(propFloat(fbxProps(channel.to.node), "DeformPercent", 0)/100))
		}
	}
}

// skin binds the mesh to the bones of the geometry's clusters, keeping the four strongest for each vertex.
func (d *fbxDoc) skin(n int, g *fbxObject, mesh *Mesh, from [][]int, points int) {
	a := d.asset
	for _, deformer := range g.kids("Deformer") {
		if deformer.to.class != "Skin" {
			continue
		}
		type pull struct {
			joint  uint16
			weight float32
		}
		pulls := make([][]pull, points)
		skin := Skin{Name: deformer.to.name}
		for _, cluster := range deformer.to.kids("Deformer") {
			bones := cluster.to.kids("Model")
			if len(bones) == 0 {
				continue
			}
			c := cluster.to.node
			transform := c.child("Transform").floats() // the mesh as seen from the bone where it was bound
			if len(transform) != 16 {
				continue
			}
			joint := uint16(len(skin.Joints))
			skin.Joints = append(skin.Joints, d.nodeOf[bones[0].to])
			skin.InverseBind = append(skin.InverseBind, mat4Of(transform))
			index, weights := c.child("Indexes").ints(), c.child("Weights").floats()
			for i, q := range index {
				if int(q) < points && i < len(weights) {
					pulls[q] = append(pulls[q], pull{joint, float32(weights[i])})
				}
			}
		}
		if len(skin.Joints) == 0 {
			return
		}
		for i := range pulls {
			slices.SortFunc(pulls[i], func(x, y pull) int {
				switch {
				case x.weight > y.weight:
					return -1
				case x.weight < y.weight:
					return 1
				}
				return 0
			})
		}
		for i := range mesh.Prims {
			p := &mesh.Prims[i]
			p.Joints, p.Weights = make([][4]uint16, len(p.Pos)), make([]Vec4, len(p.Pos))
			for v, q := range from[i] {
				var sum float32
				for k, w := range pulls[q] {
					if k < 4 {
						p.Joints[v][k], p.Weights[v][k] = w.joint, w.weight
						sum += w.weight
					}
				}
				if sum > 0 {
					p.Weights[v] = Vec4{p.Weights[v][0] / sum, p.Weights[v][1] / sum, p.Weights[v][2] / sum, p.Weights[v][3] / sum}
				} else {
					p.Weights[v][0] = 1
				}
			}
		}
		a.Skins = append(a.Skins, skin)
		a.Nodes[n].Skin = len(a.Skins) - 1
		return
	}
}

func mat4Of(v []float64) Mat4 {
	var m Mat4
	for i := range m {
		m[i] = float32(v[i])
	}
	return m
}

func (d *fbxDoc) material(o *fbxObject) (int, error) {
	if i, ok := d.materials[o]; ok {
		return i, nil
	}
	p := fbxProps(o.node)
	m := DefaultMaterial()
	m.Name = o.name
	base := propVec(p, "DiffuseColor", [3]float64{.8, .8, .8})
	factor := propFloat(p, "DiffuseFactor", 1)
	alpha := 1 - propFloat(p, "TransparencyFactor", 0)
	m.BaseColor = Vec4{float32(base[0] * factor), float32(base[1] * factor), float32(base[2] * factor), float32(alpha)}
	if alpha < 1 {
		m.AlphaMode = AlphaBlend
	}
	glow := propVec(p, "EmissiveColor", [3]float64{})
	f := propFloat(p, "EmissiveFactor", 1)
	m.Emissive = Vec3{float32(glow[0] * f), float32(glow[1] * f), float32(glow[2] * f)}
	shine := propFloat(p, "Shininess", propFloat(p, "ShininessExponent", 20))
	m.Roughness = float32(min(max(1-math.Sqrt(shine/100), .05), 1))
	m.Metallic = 0
	if d.blender {
		m.Metallic = float32(propFloat(p, "ReflectionFactor", 0))
	}
	for _, l := range o.kids("Texture") {
		image, err := d.image(l.to)
		if err != nil {
			return 0, err
		}
		if image < 0 {
			continue
		}
		ref := TexRef{Image: image, Sampler: -1}
		switch l.prop {
		case "DiffuseColor":
			m.BaseTex = ref
		case "NormalMap", "Bump":
			m.NormalTex = ref
		case "EmissiveColor", "EmissiveFactor":
			m.EmissiveTex = ref
		case "TransparentColor", "TransparencyFactor":
			if m.BaseTex.Image == image {
				m.AlphaMode = AlphaBlend
			}
		}
	}
	d.asset.Materials = append(d.asset.Materials, m)
	d.materials[o] = len(d.asset.Materials) - 1
	return d.materials[o], nil
}

// image is the picture of a texture: held in the file, or in a file beside it. One that cannot be found is noted.
func (d *fbxDoc) image(t *fbxObject) (int, error) {
	if i, ok := d.images[t]; ok {
		return i, nil
	}
	var data []byte
	name := t.node.child("RelativeFilename").word()
	if name == "" {
		name = t.node.child("FileName").word()
	}
	for _, v := range t.kids("Video") {
		if c := v.to.node.child("Content"); c != nil && len(c.Props) > 0 {
			if raw, ok := c.Props[0].([]byte); ok && len(raw) > 0 {
				data = raw
			}
		}
	}
	base := filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if data == nil && d.read != nil {
		var err error
		if data, err = d.read(base); err != nil {
			data = nil
		}
	}
	if data == nil {
		d.asset.note("Some pictures of the file were not found.")
		d.images[t] = -1
		return -1, nil
	}
	d.asset.Images = append(d.asset.Images, Image{Name: base, Data: data})
	d.images[t] = len(d.asset.Images) - 1
	return d.images[t], nil
}

// fbxCurve is one number's keys, in seconds.
type fbxCurve struct {
	times  []float64
	values []float64
}

func (c *fbxCurve) at(t, fallback float64) float64 {
	if c == nil || len(c.times) == 0 {
		return fallback
	}
	i, _ := slices.BinarySearch(c.times, t)
	switch {
	case i == 0:
		return c.values[0]
	case i >= len(c.times):
		return c.values[len(c.values)-1]
	}
	share := (t - c.times[i-1]) / (c.times[i] - c.times[i-1])
	return c.values[i-1] + (c.values[i]-c.values[i-1])*share
}

// clips reads each animation stack's first layer, and bakes each moved model's keys into its place, turn and scale, as
// the file's are angles, in its own rotation order, on the far side of its pivots.
func (d *fbxDoc) clips(models []*fbxObject) {
	a := d.asset
	stacks := sortedObjects(d.objects, "AnimationStack")
	slices.SortStableFunc(stacks, func(x, y *fbxObject) int { return strings.Compare(x.name, y.name) })
	for _, stack := range stacks {
		layers := stack.kids("AnimationLayer")
		if len(layers) == 0 {
			continue
		}
		clip := Clip{Name: stack.name}
		curves := map[*fbxObject]map[string]*[3]*fbxCurve{}
		start := math.Inf(1)
		for _, node := range layers[0].to.kids("AnimationCurveNode") {
			for _, parent := range node.to.parents {
				if parent.to.kind != "Model" {
					continue
				}
				axes := &[3]*fbxCurve{}
				for _, c := range node.to.kids("AnimationCurve") {
					curve := &fbxCurve{values: c.to.node.child("KeyValueFloat").floats()}
					for _, k := range c.to.node.child("KeyTime").ints() {
						curve.times = append(curve.times, float64(k)/fbxSecond)
					}
					if len(curve.times) != len(curve.values) || len(curve.times) == 0 {
						continue
					}
					start = min(start, curve.times[0])
					switch c.prop {
					case "d|X":
						axes[0] = curve
					case "d|Y":
						axes[1] = curve
					case "d|Z":
						axes[2] = curve
					}
				}
				if curves[parent.to] == nil {
					curves[parent.to] = map[string]*[3]*fbxCurve{}
				}
				curves[parent.to][parent.prop] = axes
			}
		}
		for _, m := range models {
			moved := curves[m]
			if moved == nil {
				continue
			}
			var times []float64
			for _, axes := range moved {
				for _, c := range axes {
					if c != nil {
						times = append(times, c.times...)
					}
				}
			}
			slices.Sort(times)
			times = slices.Compact(times)
			base := fbxTransformOf(fbxProps(m.node))
			tr, ro, sc := Channel{Node: d.nodeOf[m], Path: PathTranslation, Width: 3}, Channel{Node: d.nodeOf[m], Path: PathRotation, Width: 4}, Channel{Node: d.nodeOf[m], Path: PathScale, Width: 3}
			var last Quat
			for i, t := range times {
				f := base
				read := func(prop string, to *[3]float64) {
					if axes := moved[prop]; axes != nil {
						for k := range 3 {
							to[k] = axes[k].at(t, to[k])
						}
					}
				}
				read("Lcl Translation", &f.t)
				read("Lcl Rotation", &f.r)
				read("Lcl Scaling", &f.s)
				pos, turn, size := Decompose(f.matrix())
				if i > 0 && turn[0]*last[0]+turn[1]*last[1]+turn[2]*last[2]+turn[3]*last[3] < 0 {
					turn = Quat{-turn[0], -turn[1], -turn[2], -turn[3]}
				}
				last = turn
				at := float32(t - start)
				tr.Times, ro.Times, sc.Times = append(tr.Times, at), append(ro.Times, at), append(sc.Times, at)
				tr.Values = append(tr.Values, pos[:]...)
				ro.Values = append(ro.Values, turn[:]...)
				sc.Values = append(sc.Values, size[:]...)
			}
			if len(times) > 0 {
				clip.Channels = append(clip.Channels, tr, ro, sc)
			}
		}
		if len(clip.Channels) > 0 {
			a.Clips = append(a.Clips, clip)
		}
	}
}
