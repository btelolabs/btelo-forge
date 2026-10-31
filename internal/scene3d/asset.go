// Package scene3d is a 3D asset as data, with no graphics card in sight: nodes, meshes, materials, skeletons and
// animations, shaped after glTF so that whatever a file holds has a place to stay, read from .glb and .gltf files.
package scene3d

import (
	"encoding/json"
	"slices"
)

// AlphaMode is how a material's alpha is read.
type AlphaMode int

const (
	AlphaOpaque AlphaMode = iota
	AlphaMask             // a pixel is there or not, by AlphaCutoff
	AlphaBlend
)

// TexRef points a material at a picture; Image is -1 for none.
type TexRef struct {
	Image   int
	UV      int // which of the mesh's texture coordinates, 0 or 1
	Sampler int // into Asset.Samplers, or -1 for none
	// Extensions are what the file said about this use of the picture, kept as they were.
	Extensions json.RawMessage
}

// Material is glTF's metal-rough PBR.
type Material struct {
	Name        string
	BaseColor   Vec4
	Metallic    float32
	Roughness   float32
	Emissive    Vec3
	NormalScale float32
	Occlusion   float32 // how strongly the occlusion texture applies
	AlphaMode   AlphaMode
	AlphaCutoff float32
	DoubleSided bool

	BaseTex, MetalRoughTex, NormalTex, OcclusionTex, EmissiveTex TexRef

	// Extensions are the file's material extensions, kept as they were whether understood or not.
	Extensions json.RawMessage
}

// DefaultMaterial is what a mesh without a material shows, as glTF defines it.
func DefaultMaterial() Material {
	none := TexRef{Image: -1, Sampler: -1}
	return Material{Name: "default", BaseColor: Vec4{1, 1, 1, 1}, Metallic: 1, Roughness: 1, NormalScale: 1, Occlusion: 1, AlphaCutoff: .5,
		BaseTex: none, MetalRoughTex: none, NormalTex: none, OcclusionTex: none, EmissiveTex: none}
}

// Image is a picture as the file holds it, kept encoded so that saving does not recompress it.
type Image struct {
	Name string
	Mime string
	Data []byte
}

// Prim is a part of a mesh with one material: triangles, with vertices of parallel lists. Those a file left out are nil,
// except Normal, which is worked out when missing.
type Prim struct {
	Pos      []Vec3
	Normal   []Vec3
	Tangent  []Vec4
	UV, UV2  []Vec2
	Color    []Vec4
	Joints   [][4]uint16
	Weights  []Vec4
	Indices  []uint32
	Material int     // -1 for the default
	Morphs   []Morph // one for each of the mesh's shape keys
}

// Morph is what a shape key at full strength adds to each vertex of a part. Normal may be nil.
type Morph struct{ Pos, Normal []Vec3 }

// Triangles is how many triangles the part has.
func (p *Prim) Triangles() int { return len(p.Indices) / 3 }

// Mesh is a shape a node can show.
type Mesh struct {
	Name  string
	Prims []Prim
	// Weights is how strongly each shape key applies, and Keys their names, either of which may be short of the keys.
	Weights []float32
	Keys    []string
}

// KeyCount is how many shape keys the mesh has.
func (m *Mesh) KeyCount() int {
	n := 0
	for i := range m.Prims {
		n = max(n, len(m.Prims[i].Morphs))
	}
	return n
}

// Node is a place in the hierarchy. Mesh and Skin are -1 for none, Parent -1 for a root.
type Node struct {
	Name        string
	Parent      int
	Children    []int
	Translation Vec3
	Rotation    Quat
	Scale       Vec3
	Mesh, Skin  int
	Weights     []float32 // in place of the mesh's shape key strengths, if set
}

// Local is the node's transform relative to its parent.
func (n *Node) Local() Mat4 { return Compose(n.Translation, n.Rotation, n.Scale) }

// Skin binds a mesh's vertices to joints, which are nodes.
type Skin struct {
	Name        string
	Joints      []int
	InverseBind []Mat4
}

// Path is what an animation channel moves.
type Path int

const (
	PathTranslation Path = iota
	PathRotation
	PathScale
	PathWeights
)

// Interpolation is how a channel's keys are joined.
type Interpolation int

const (
	Linear Interpolation = iota
	Step
	CubicSpline
)

// Channel animates one property of one node. Values holds Width numbers for each key (three of those for a spline).
type Channel struct {
	Node   int
	Path   Path
	Interp Interpolation
	Times  []float32
	Values []float32
	Width  int
}

// Clip is one named animation.
type Clip struct {
	Name     string
	Channels []Channel
}

// Duration is the time of the clip's last key.
func (c *Clip) Duration() float32 {
	var d float32
	for i := range c.Channels {
		if t := c.Channels[i].Times; len(t) > 0 {
			d = max(d, t[len(t)-1])
		}
	}
	return d
}

// Asset is a whole file.
type Asset struct {
	Nodes     []Node
	Roots     []int
	Meshes    []Mesh
	Materials []Material
	Images    []Image
	Samplers  []json.RawMessage // kept as the file had them
	Skins     []Skin
	Clips     []Clip
	// ExtensionsUsed and ExtensionsRequired are the file's lists, written back with it.
	ExtensionsUsed, ExtensionsRequired []string
	// Notes are what a file held that is not shown, in English, one for each kind; Unshown are the extensions it uses
	// that are not understood.
	Notes   []string
	Unshown []string
}

// Stats counts what an asset holds.
type Stats struct {
	Triangles, Vertices, Meshes, Materials, Images, Joints, Clips int
}

// Stats counts the asset's contents.
func (a *Asset) Stats() Stats {
	s := Stats{Meshes: len(a.Meshes), Materials: len(a.Materials), Images: len(a.Images), Clips: len(a.Clips)}
	for i := range a.Meshes {
		for j := range a.Meshes[i].Prims {
			p := &a.Meshes[i].Prims[j]
			s.Triangles += p.Triangles()
			s.Vertices += len(p.Pos)
		}
	}
	joints := map[int]bool{}
	for i := range a.Skins {
		for _, j := range a.Skins[i].Joints {
			joints[j] = true
		}
	}
	s.Joints = len(joints)
	return s
}

// Worlds is every node's transform in the world, in the pose its Translation, Rotation and Scale give.
func (a *Asset) Worlds() []Mat4 { return worldsOf(a.Nodes, a.Roots) }

func worldsOf(nodes []Node, roots []int) []Mat4 {
	out := make([]Mat4, len(nodes))
	var walk func(i int, parent Mat4)
	walk = func(i int, parent Mat4) {
		out[i] = parent.Mul(nodes[i].Local())
		for _, c := range nodes[i].Children {
			walk(c, out[i])
		}
	}
	for _, r := range roots {
		walk(r, Identity())
	}
	return out
}

// Surface is a part of the asset in the world, in its current pose: what is drawn and picked.
type Surface struct {
	Pos, Normal []Vec3
	Tangent     []Vec4
	UV, UV2     []Vec2
	Color       []Vec4
	Indices     []uint32
	Material    int
	Node, Part  int // where in the asset it comes from
}

// Surfaces places every shown part in the world, skinned parts by their joints, and returns them with their bounds.
func (a *Asset) Surfaces() ([]Surface, Box) { return a.SurfacesIn(a.Worlds(), nil) }

// SurfacesIn is Surfaces for the pose in which the nodes are at worlds, and the nodes in keys have those strengths of
// their shape keys.
func (a *Asset) SurfacesIn(worlds []Mat4, keys map[int][]float32) ([]Surface, Box) {
	var out []Surface
	bounds := EmptyBox()
	for i := range a.Nodes {
		n := &a.Nodes[i]
		if n.Mesh < 0 {
			continue
		}
		var joints []Mat4
		if n.Skin >= 0 {
			s := &a.Skins[n.Skin]
			joints = make([]Mat4, len(s.Joints))
			for k, j := range s.Joints {
				joints[k] = worlds[j].Mul(s.InverseBind[k])
			}
		}
		for j := range a.Meshes[n.Mesh].Prims {
			p := &a.Meshes[n.Mesh].Prims[j]
			base, normals := morphed(p, a.strengths(i, keys))
			s := Surface{Pos: make([]Vec3, len(p.Pos)), Normal: make([]Vec3, len(p.Pos)), UV: p.UV, UV2: p.UV2, Color: p.Color, Indices: p.Indices, Material: p.Material, Node: i, Part: j}
			if p.Tangent != nil {
				s.Tangent = make([]Vec4, len(p.Pos))
			}
			for v := range p.Pos {
				m := worlds[i]
				if joints != nil && p.Joints != nil {
					m = blend(joints, p.Joints[v], p.Weights[v])
				}
				s.Pos[v], s.Normal[v] = m.Point(base[v]), m.Normal(normals[v])
				if p.Tangent != nil {
					t := m.Dir(Vec3{p.Tangent[v][0], p.Tangent[v][1], p.Tangent[v][2]}).Norm()
					s.Tangent[v] = Vec4{t[0], t[1], t[2], p.Tangent[v][3]}
				}
				bounds.Grow(s.Pos[v])
			}
			if joints == nil && worlds[i].Determinant() < 0 {
				s.Indices = flipped(p.Indices) // a mirror turns triangles inside out
			}
			out = append(out, s)
		}
	}
	return out, bounds
}

// StrengthsOf is how strongly node's shape keys apply in the asset's own pose.
func (a *Asset) StrengthsOf(node int) []float32 { return a.strengths(node, nil) }

// strengths is how strongly node's shape keys apply.
func (a *Asset) strengths(node int, keys map[int][]float32) []float32 {
	if k, ok := keys[node]; ok {
		return k
	}
	n := &a.Nodes[node]
	if n.Weights != nil {
		return n.Weights
	}
	return a.Meshes[n.Mesh].Weights
}

// morphed is the part's vertices and normals with its shape keys at the given strengths.
func morphed(p *Prim, strengths []float32) (pos, normal []Vec3) {
	pos, normal = p.Pos, p.Normal
	for k := range p.Morphs {
		if k >= len(strengths) || strengths[k] == 0 {
			continue
		}
		if len(pos) > 0 && &pos[0] == &p.Pos[0] {
			pos, normal = slices.Clone(p.Pos), slices.Clone(p.Normal)
		}
		w := strengths[k]
		for v := range pos {
			pos[v] = pos[v].Add(p.Morphs[k].Pos[v].Scale(w))
		}
		if p.Morphs[k].Normal != nil {
			for v := range normal {
				normal[v] = normal[v].Add(p.Morphs[k].Normal[v].Scale(w))
			}
		}
	}
	return pos, normal
}

// flipped is the triangles wound the other way.
func flipped(indices []uint32) []uint32 {
	out := make([]uint32, len(indices))
	for i := 0; i+2 < len(indices); i += 3 {
		out[i], out[i+1], out[i+2] = indices[i], indices[i+2], indices[i+1]
	}
	return out
}

// blend is the weighted sum of a vertex's joint transforms.
func blend(joints []Mat4, index [4]uint16, weight Vec4) Mat4 {
	var m Mat4
	for k := range 4 {
		if w := weight[k]; w != 0 {
			for e := range m {
				m[e] += joints[index[k]][e] * w
			}
		}
	}
	return m
}
