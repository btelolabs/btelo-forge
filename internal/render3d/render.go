package render3d

import (
	"context"
	_ "embed"
	"fmt"
	"image"
	"math"
	"slices"
	"time"
	"unsafe"

	"github.com/btelolabs/btelo-forge/internal/gpu"
	s3 "github.com/btelolabs/btelo-forge/internal/scene3d"
)

//go:embed scene.wgsl
var sceneWGSL string

//go:embed prefilter.wgsl
var prefilterWGSL string

// ViewMode is how surfaces are shown.
type ViewMode int

const (
	ViewMaterial ViewMode = iota
	ViewSolid
	ViewNormals
	ViewUV
	ViewTraced // light that has bounced, by the path tracer: see Trace
)

const (
	samples      = 4
	shadowSide   = 2048
	specularW    = 256
	specularH    = 128
	specularMips = 6
	frameSize    = 448
	materialSize = 80
	vertexFloats = 18
	readTimeout  = 10 * time.Second
)

// Options is what the view shows.
type Options struct {
	View        ViewMode
	Wireframe   bool
	Grid        bool
	Shadows     bool
	ShowSky     bool
	Exposure    float32
	SkyRotation float32 // radians
	LineScale   float32 // the pixels of a line's unit of width, 1 when zero
	NoBounces   bool    // the tracer stops at direct light
}

type material struct {
	group                 gpu.BindGroup
	uniform               gpu.Buffer
	normalMap             bool
	double, blend, cutout bool
}

type part struct {
	vertices, indices gpu.Buffer
	count             uint32
	material          *material
	center            s3.Vec3
}

// Renderer draws one asset at a time. It is used from one goroutine, as its Device is.
type Renderer struct {
	d gpu.Device

	frameLayout, materialLayout, prefilterLayout gpu.BindLayout
	scene, prefilter                             gpu.Shader
	pipes                                        map[string]gpu.Pipeline
	texSampler, skySampler, shadowSampler        gpu.Sampler
	white, flat                                  gpu.Texture
	whiteView, flatView                          gpu.View

	frameBuf    gpu.Buffer
	shadow      gpu.Texture
	shadowView  gpu.View
	cutoutColor gpu.Texture // what the pass of cut-out shadows needs to draw on; made for the first asset with any
	cutoutView  gpu.View
	frameGroup  gpu.BindGroup
	sky         gpu.Texture
	skyView     gpu.View
	specular    gpu.Texture
	specularAll gpu.View
	harmonics   Harmonics
	sun         Sun

	parts                             []part
	geometry                          []gpu.Buffer
	materials                         []material
	textures                          []gpu.Texture
	colorTex, plainTex                map[int]gpu.Texture // by picture, those read as colour and as numbers
	painted                           *paintLevels
	views                             []gpu.View
	buffers                           []gpu.Buffer
	overlayTris, overlayLines         gpu.Buffer
	overlayTriCount, overlayLineCount uint32
	wires                             gpu.Buffer // the mesh lines, made when first shown
	wireCount                         uint32
	wireSurfaces                      []s3.Surface // what they are still to be made from
	wireEdges                         [][]uint32   // the pairs of vertices of each surface's lines
	boneTris, boneLines               gpu.Buffer
	boneTriCount, boneLineCount       uint32
	bounds                            s3.Box
	shadowFresh                       bool
	shadowKey                         [5]float32

	trace                    tracer
	w, h                     uint32
	color, depth             gpu.Texture
	resolve                  gpu.Texture
	colorV, depthV, resolveV gpu.View
	staging                  gpu.Buffer
	row                      []byte
}

// New prepares a renderer on d.
func New(d gpu.Device) (*Renderer, error) {
	r := &Renderer{d: d, pipes: map[string]gpu.Pipeline{}, bounds: s3.EmptyBox()}
	if err := r.prepare(); err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}

func (r *Renderer) prepare() error {
	d := r.d
	var err error
	if r.frameLayout, err = d.BindLayout([]gpu.BindEntry{
		{Slot: 0, Stage: gpu.StageVertex | gpu.StageFragment, Kind: gpu.BindUniform},
		{Slot: 1, Stage: gpu.StageFragment, Kind: gpu.BindTexture},
		{Slot: 2, Stage: gpu.StageFragment, Kind: gpu.BindTexture},
		{Slot: 3, Stage: gpu.StageFragment, Kind: gpu.BindSampler},
		{Slot: 4, Stage: gpu.StageFragment, Kind: gpu.BindDepthTexture},
		{Slot: 5, Stage: gpu.StageFragment, Kind: gpu.BindCompareSampler}}); err != nil {
		return err
	}
	entries := []gpu.BindEntry{{Slot: 0, Stage: gpu.StageFragment, Kind: gpu.BindUniform}}
	for i := range 5 {
		entries = append(entries, gpu.BindEntry{Slot: uint32(i + 1), Stage: gpu.StageFragment, Kind: gpu.BindTexture})
	}
	entries = append(entries, gpu.BindEntry{Slot: 6, Stage: gpu.StageFragment, Kind: gpu.BindSampler})
	if r.materialLayout, err = d.BindLayout(entries); err != nil {
		return err
	}
	if r.prefilterLayout, err = d.BindLayout([]gpu.BindEntry{
		{Slot: 0, Stage: gpu.StageFragment, Kind: gpu.BindUniform},
		{Slot: 1, Stage: gpu.StageFragment, Kind: gpu.BindTexture},
		{Slot: 2, Stage: gpu.StageFragment, Kind: gpu.BindSampler}}); err != nil {
		return err
	}
	if r.scene, err = d.Shader("scene", sceneWGSL); err != nil {
		return fmt.Errorf("scene shader: %w", err)
	}
	if r.prefilter, err = d.Shader("prefilter", prefilterWGSL); err != nil {
		return fmt.Errorf("prefilter shader: %w", err)
	}
	if r.texSampler, err = d.Sampler(gpu.SamplerDesc{Label: "texture", Repeat: true, Linear: true, Anisotropy: 8}); err != nil {
		return err
	}
	if r.skySampler, err = d.Sampler(gpu.SamplerDesc{Label: "sky", RepeatU: true, Linear: true}); err != nil {
		return err
	}
	if r.shadowSampler, err = d.Sampler(gpu.SamplerDesc{Label: "shadow", Linear: true, CompareLess: true}); err != nil {
		return err
	}
	if r.white, err = solid(d, "white", 255, 255, 255, 255); err != nil {
		return err
	}
	if r.flat, err = solid(d, "flat", 128, 128, 255, 255); err != nil {
		return err
	}
	if r.whiteView, err = r.white.View(-1); err != nil {
		return err
	}
	if r.flatView, err = r.flat.View(-1); err != nil {
		return err
	}
	if r.frameBuf, err = d.Buffer(gpu.BufferDesc{Label: "frame", Size: frameSize, Usage: gpu.BufferUniform | gpu.BufferCopyDst}); err != nil {
		return err
	}
	if r.shadow, err = d.Texture(gpu.TextureDesc{Label: "shadow", Width: shadowSide, Height: shadowSide, Format: gpu.Depth32Float, Usage: gpu.TextureTarget | gpu.TextureSampled}); err != nil {
		return err
	}
	if r.shadowView, err = r.shadow.View(-1); err != nil {
		return err
	}
	if r.specular, err = d.Texture(gpu.TextureDesc{Label: "specular", Width: specularW, Height: specularH, Mips: specularMips, Format: gpu.RGBA16Float, Usage: gpu.TextureCopyDst | gpu.TextureSampled}); err != nil {
		return err
	}
	if r.specularAll, err = r.specular.View(-1); err != nil {
		return err
	}
	if err := r.makePipelines(); err != nil {
		return err
	}
	return r.prepareTracer()
}

func (r *Renderer) makePipelines() error {
	attrs := []gpu.Attr{{Location: 0, Offset: 0, Format: gpu.Float3}, {Location: 1, Offset: 12, Format: gpu.Float3}, {Location: 2, Offset: 24, Format: gpu.Float4},
		{Location: 3, Offset: 40, Format: gpu.Float2}, {Location: 4, Offset: 48, Format: gpu.Float2}, {Location: 5, Offset: 56, Format: gpu.Float4}}
	both := []gpu.BindLayout{r.frameLayout, r.materialLayout}
	one := []gpu.BindLayout{r.frameLayout}
	write := &gpu.DepthState{Write: true, Compare: gpu.CompareLess}
	read := &gpu.DepthState{Compare: gpu.CompareLessEqual}
	surface := func(label string, cull gpu.Cull, blend bool, depth *gpu.DepthState) gpu.PipelineDesc {
		return gpu.PipelineDesc{Label: label, Shader: r.scene, Vertex: "vs_surface", Fragment: "fs_surface", Layouts: both, Stride: vertexFloats * 4, Attrs: attrs,
			Cull: cull, Color: gpu.RGBA8Unorm, Blend: blend, Depth: depth, Samples: samples}
	}
	overlay := func(label string, topology gpu.Topology) gpu.PipelineDesc {
		return gpu.PipelineDesc{Label: label, Shader: r.scene, Vertex: "vs_overlay", Fragment: "fs_overlay", Layouts: one, Stride: OverlayVertex * 4, Topology: topology, Blend: true,
			Attrs: []gpu.Attr{{Location: 0, Offset: 0, Format: gpu.Float3}, {Location: 1, Offset: 12, Format: gpu.Float4}, {Location: 2, Offset: 28, Format: gpu.Float2}},
			Color: gpu.RGBA8Unorm, Depth: &gpu.DepthState{Compare: gpu.CompareLessEqual, Bias: -8, SlopeBias: -1.5}, Samples: samples}
	}
	line := func(label, fragment string) gpu.PipelineDesc {
		return gpu.PipelineDesc{Label: label, Shader: r.scene, Vertex: "vs_line", Fragment: fragment, Layouts: one, Stride: OverlayLine * 4, Instance: true, Blend: true,
			Attrs: []gpu.Attr{{Location: 0, Offset: 0, Format: gpu.Float3}, {Location: 1, Offset: 12, Format: gpu.Float3}, {Location: 2, Offset: 24, Format: gpu.Float4}},
			Color: gpu.RGBA8Unorm, Depth: read, Samples: samples}
	}
	bone := func(label, fragment string) gpu.PipelineDesc {
		d := line(label, fragment)
		d.Vertex, d.Depth = "vs_bone", &gpu.DepthState{Compare: gpu.CompareAlways}
		return d
	}
	joints := overlay("joints", gpu.Triangles)
	joints.Depth = &gpu.DepthState{Compare: gpu.CompareAlways}
	shadowDepth := &gpu.DepthState{Write: true, Compare: gpu.CompareLess, Bias: 4, SlopeBias: 2.5}
	for name, p := range map[string]gpu.PipelineDesc{
		"opaque": surface("opaque", gpu.CullBack, false, write),
		"double": surface("double", gpu.CullNone, false, write),
		"blend":  surface("blend", gpu.CullNone, true, read),
		"sky": {Label: "sky", Shader: r.scene, Vertex: "vs_sky", Fragment: "fs_sky", Layouts: one, Color: gpu.RGBA8Unorm,
			Depth: &gpu.DepthState{Compare: gpu.CompareAlways}, Samples: samples},
		"ground":    {Label: "ground", Shader: r.scene, Vertex: "vs_ground", Fragment: "fs_ground", Layouts: one, Color: gpu.RGBA8Unorm, Blend: true, Depth: read, Samples: samples},
		"shadow":    {Label: "shadow", Shader: r.scene, Vertex: "vs_shadow", Layouts: both, Stride: vertexFloats * 4, Attrs: attrs, Depth: shadowDepth},
		"cutout":    {Label: "cutout shadow", Shader: r.scene, Vertex: "vs_shadow", Fragment: "fs_shadow", Layouts: both, Stride: vertexFloats * 4, Attrs: attrs, Color: gpu.RGBA8Unorm, Depth: shadowDepth},
		"overlay":   overlay("overlay", gpu.Triangles),
		"line rim":  line("line rim", "fs_line_rim"),
		"line core": line("line core", "fs_line_core"),
		"bone rim":  bone("bone rim", "fs_line_rim"),
		"bone core": bone("bone core", "fs_line_core"),
		"joints":    joints,
		"prefilter": {Label: "prefilter", Shader: r.prefilter, Vertex: "vs", Fragment: "fs", Layouts: []gpu.BindLayout{r.prefilterLayout}, Color: gpu.RGBA16Float},
	} {
		made, err := r.d.Pipeline(p)
		if err != nil {
			return fmt.Errorf("%s pipeline: %w", name, err)
		}
		r.pipes[name] = made
	}
	return nil
}

// SetSky lights the scene with panorama p and sun.
func (r *Renderer) SetSky(p Panorama, sun Sun) error {
	if p.W < 2 || p.H < 2 || len(p.Pix) != p.W*p.H*4 {
		return fmt.Errorf("a panorama of %d×%d is broken", p.W, p.H)
	}
	d := r.d
	sky, err := uploadPanorama(d, p)
	if err != nil {
		return err
	}
	view, err := sky.View(-1)
	if err != nil {
		sky.Release()
		return err
	}
	if err := r.filter(view); err != nil {
		view.Release()
		sky.Release()
		return err
	}
	group, err := d.BindGroup(r.frameLayout, []gpu.Binding{{Slot: 0, Buffer: r.frameBuf, Size: frameSize}, {Slot: 1, View: view}, {Slot: 2, View: r.specularAll},
		{Slot: 3, Sampler: r.skySampler}, {Slot: 4, View: r.shadowView}, {Slot: 5, Sampler: r.shadowSampler}})
	if err != nil {
		view.Release()
		sky.Release()
		return err
	}
	if err := d.Idle(); err != nil { // what is released below may be in use
		return err
	}
	release(r.frameGroup, r.skyView, r.sky)
	r.sky, r.skyView, r.frameGroup = sky, view, group
	r.harmonics, r.sun = p.Harmonics(), sun
	r.shadowFresh = false
	return nil
}

// filter renders the specular levels from the sky. Each is drawn on a texture of its own size and copied in place, as
// the library's Vulkan side gets the size of a single level wrong when it is drawn on directly.
func (r *Renderer) filter(sky gpu.View) error {
	d := r.d
	buf, err := d.Buffer(gpu.BufferDesc{Label: "prefilter", Size: 16, Usage: gpu.BufferUniform | gpu.BufferCopyDst})
	if err != nil {
		return err
	}
	defer buf.Release()
	group, err := d.BindGroup(r.prefilterLayout, []gpu.Binding{{Slot: 0, Buffer: buf, Size: 16}, {Slot: 1, View: sky}, {Slot: 2, Sampler: r.skySampler}})
	if err != nil {
		return err
	}
	defer group.Release()
	for m := range specularMips {
		if err := r.filterLevel(m, buf, group); err != nil {
			return err
		}
	}
	return nil
}

func (r *Renderer) filterLevel(m int, buf gpu.Buffer, group gpu.BindGroup) error {
	d := r.d
	w, h := uint32(specularW>>m), uint32(specularH>>m)
	if err := d.WriteBuffer(buf, 0, bytesOf([]float32{float32(m) / (specularMips - 1), 0, 0, 0})); err != nil {
		return err
	}
	level, err := d.Texture(gpu.TextureDesc{Label: "specular level", Width: w, Height: h, Format: gpu.RGBA16Float, Usage: gpu.TextureTarget | gpu.TextureCopySrc})
	if err != nil {
		return err
	}
	defer level.Release()
	view, err := level.View(-1)
	if err != nil {
		return err
	}
	defer view.Release()
	enc, err := d.Encoder()
	if err != nil {
		return err
	}
	p, err := enc.Pass(gpu.PassDesc{Color: &gpu.ColorTarget{View: view, Clear: &[4]float64{}}})
	if err != nil {
		return err
	}
	p.SetPipeline(r.pipes["prefilter"])
	p.SetBindGroup(0, group)
	p.Draw(3)
	if err := p.End(); err != nil {
		return err
	}
	enc.CopyTexture(level, r.specular, m, w, h)
	if err := enc.Submit(); err != nil {
		return err
	}
	return d.Idle() // the uniform changes for the next level, and the level is let go
}

type releaser interface{ Release() }

func release(items ...releaser) {
	for _, i := range items {
		if i != nil {
			i.Release()
		}
	}
}

func bytesOf[T any](s []T) []byte {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), len(s)*int(unsafe.Sizeof(s[0])))
}

// MeshLines lists each edge of the surfaces' triangles once, in OverlayLine floats.
func MeshLines(surfaces []s3.Surface, color [4]float32) []float32 {
	return lineData(surfaces, meshEdges(surfaces), color)
}

// lineData is the lines between the pairs of vertices of each surface, in OverlayLine floats.
func lineData(surfaces []s3.Surface, pairs [][]uint32, color [4]float32) []float32 {
	var out []float32
	for i, edges := range pairs {
		pos := surfaces[i].Pos
		for e := 0; e+1 < len(edges); e += 2 {
			p, q := pos[edges[e]], pos[edges[e+1]]
			out = append(out, p[0], p[1], p[2], q[0], q[1], q[2], color[0], color[1], color[2], color[3])
		}
	}
	return out
}

// meshEdges is each edge of each surface's triangles, once, as pairs of vertices. Vertices split along a seam or a hard
// edge count as one, so that such an edge is not drawn twice.
func meshEdges(surfaces []s3.Surface) [][]uint32 {
	out := make([][]uint32, len(surfaces))
	for i := range surfaces {
		s := &surfaces[i]
		same := make(map[s3.Vec3]uint32, len(s.Pos))
		welded := make([]uint32, len(s.Pos))
		for v, p := range s.Pos {
			first, ok := same[p]
			if !ok {
				first = uint32(v)
				same[p] = first
			}
			welded[v] = first
		}
		seen := make(map[uint64]bool, len(s.Indices))
		add := func(a, b uint32) {
			a, b = welded[a], welded[b]
			k := uint64(min(a, b))<<32 | uint64(max(a, b))
			if a != b && !seen[k] {
				seen[k] = true
				out[i] = append(out[i], a, b)
			}
		}
		for t := 0; t+2 < len(s.Indices); t += 3 {
			a, b, c := s.Indices[t], s.Indices[t+1], s.Indices[t+2]
			add(a, b)
			add(b, c)
			add(c, a)
		}
	}
	return out
}

// vertexData packs a surface's vertices. A missing tangent stays zero, which the shader recognises.
func vertexData(s *s3.Surface) []float32 {
	out := make([]float32, len(s.Pos)*vertexFloats)
	for i := range s.Pos {
		v := out[i*vertexFloats:]
		copy(v[0:], s.Pos[i][:])
		copy(v[3:], s.Normal[i][:])
		if s.Tangent != nil {
			copy(v[6:], s.Tangent[i][:])
		}
		if s.UV != nil {
			copy(v[10:], s.UV[i][:])
		}
		if s.UV2 != nil {
			copy(v[12:], s.UV2[i][:])
		}
		if s.Color != nil {
			copy(v[14:], s.Color[i][:])
		} else {
			v[14], v[15], v[16], v[17] = 1, 1, 1, 1
		}
	}
	return out
}

func (r *Renderer) clearGeometry() {
	for _, b := range r.geometry {
		release(b)
	}
	release(r.wires)
	r.parts, r.geometry, r.wires, r.wireCount, r.wireSurfaces = nil, nil, nil, 0, nil
	r.bounds = s3.EmptyBox()
}

func (r *Renderer) clearMaterials() {
	for _, m := range r.materials {
		release(m.group)
	}
	for _, b := range r.buffers {
		release(b)
	}
	for _, v := range r.views {
		release(v)
	}
	for _, t := range r.textures {
		release(t)
	}
	r.materials, r.buffers, r.views, r.textures = nil, nil, nil, nil
}

func (r *Renderer) buffer(label string, usage gpu.BufferUsage, data []byte) (gpu.Buffer, error) {
	b, err := r.d.Buffer(gpu.BufferDesc{Label: label, Size: uint64(len(data)), Usage: usage | gpu.BufferCopyDst})
	if err != nil {
		return nil, err
	}
	return b, r.d.WriteBuffer(b, 0, data)
}

// materialData is what the shader reads of a material.
func materialData(m *s3.Material, normalMap bool) []float32 {
	cutoff := float32(-1)
	if m.AlphaMode == s3.AlphaMask {
		cutoff = m.AlphaCutoff
	}
	hasNormal := float32(0)
	if normalMap {
		hasNormal = 1
	}
	return []float32{m.BaseColor[0], m.BaseColor[1], m.BaseColor[2], m.BaseColor[3],
		m.Emissive[0], m.Emissive[1], m.Emissive[2], m.NormalScale,
		m.Metallic, m.Roughness, m.Occlusion, cutoff,
		float32(m.BaseTex.UV), float32(m.MetalRoughTex.UV), float32(m.NormalTex.UV), float32(m.OcclusionTex.UV),
		float32(m.EmissiveTex.UV), hasNormal, 0, 0}
}

// UpdateMaterials changes the numbers of materials that SetAsset put on the card, which keep their pictures.
func (r *Renderer) UpdateMaterials(materials []s3.Material) error {
	for i := range materials {
		if i >= len(r.materials) {
			break
		}
		q := &r.materials[i]
		m := &materials[i]
		if err := r.d.WriteBuffer(q.uniform, 0, bytesOf(materialData(m, q.normalMap))); err != nil {
			return err
		}
		q.double, q.blend, q.cutout = m.DoubleSided, m.AlphaMode == s3.AlphaBlend, m.AlphaMode == s3.AlphaMask
	}
	r.trace.materials = materials
	r.trace.scene++
	r.shadowFresh = false
	return nil
}

// SetAsset puts a's materials on the card, in place of those before. It returns the pictures that could not be read,
// whose materials go without them. SetSurfaces gives it its shapes.
func (r *Renderer) SetAsset(a *s3.Asset) ([]string, error) {
	if err := r.d.Idle(); err != nil {
		return nil, err
	}
	r.clearGeometry()
	r.clearMaterials()
	r.colorTex, r.plainTex, r.painted = map[int]gpu.Texture{}, map[int]gpu.Texture{}, nil
	decoded, errs := decodeImages(a.Images)
	r.trace.pictures, r.trace.materials = decoded, a.Materials
	r.trace.scene++
	var problems []string
	for i, err := range errs {
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", name(a.Images[i], i), err))
		}
	}
	type key struct {
		image int
		srgb  bool
	}
	cached := map[key]gpu.View{}
	view := func(t s3.TexRef, srgb bool, fallback gpu.View) (gpu.View, error) {
		if t.Image < 0 || decoded[t.Image] == nil {
			return fallback, nil
		}
		k := key{t.Image, srgb}
		if v, ok := cached[k]; ok {
			return v, nil
		}
		tex, err := upload(r.d, name(a.Images[t.Image], t.Image), decoded[t.Image], srgb)
		if err != nil {
			return nil, err
		}
		r.textures = append(r.textures, tex)
		if srgb {
			r.colorTex[t.Image] = tex
		} else {
			r.plainTex[t.Image] = tex
		}
		v, err := tex.View(-1)
		if err != nil {
			return nil, err
		}
		r.views = append(r.views, v)
		cached[k] = v
		return v, nil
	}
	// The last one stands for parts with no material of their own.
	for _, m := range append(slices.Clone(a.Materials), s3.DefaultMaterial()) {
		normalMap := m.NormalTex.Image >= 0 && decoded[m.NormalTex.Image] != nil
		ub, err := r.buffer("material", gpu.BufferUniform, bytesOf(materialData(&m, normalMap)))
		if err != nil {
			return problems, err
		}
		r.buffers = append(r.buffers, ub)
		views := make([]gpu.View, 5)
		for k, t := range []struct {
			ref      s3.TexRef
			srgb     bool
			fallback gpu.View
		}{{m.BaseTex, true, r.whiteView}, {m.MetalRoughTex, false, r.whiteView}, {m.NormalTex, false, r.flatView}, {m.OcclusionTex, false, r.whiteView}, {m.EmissiveTex, true, r.whiteView}} {
			if views[k], err = view(t.ref, t.srgb, t.fallback); err != nil {
				return problems, err
			}
		}
		group, err := r.d.BindGroup(r.materialLayout, []gpu.Binding{{Slot: 0, Buffer: ub, Size: materialSize}, {Slot: 1, View: views[0]}, {Slot: 2, View: views[1]},
			{Slot: 3, View: views[2]}, {Slot: 4, View: views[3]}, {Slot: 5, View: views[4]}, {Slot: 6, Sampler: r.texSampler}})
		if err != nil {
			return problems, err
		}
		r.materials = append(r.materials, material{group: group, uniform: ub, normalMap: normalMap, double: m.DoubleSided, blend: m.AlphaMode == s3.AlphaBlend, cutout: m.AlphaMode == s3.AlphaMask})
	}
	return problems, nil
}

// SetSurfaces puts the shapes on the card, in place of those before, keeping the materials.
func (r *Renderer) SetSurfaces(surfaces []s3.Surface, bounds s3.Box) error {
	if err := r.d.Idle(); err != nil {
		return err
	}
	r.clearGeometry()
	r.wireSurfaces, r.wireEdges = surfaces, nil
	r.trace.surfaces = surfaces
	r.trace.scene++
	for i := range surfaces {
		s := &surfaces[i]
		if len(s.Indices) == 0 {
			continue
		}
		m := len(r.materials) - 1
		if s.Material >= 0 && s.Material < m {
			m = s.Material
		}
		p := part{count: uint32(len(s.Indices)), material: &r.materials[m]}
		box := s3.EmptyBox()
		for _, v := range s.Pos {
			box.Grow(v)
		}
		p.center = box.Center()
		var err error
		add := func(label string, usage gpu.BufferUsage, data []byte) gpu.Buffer {
			var b gpu.Buffer
			if err == nil {
				if b, err = r.buffer(label, usage, data); err == nil {
					r.geometry = append(r.geometry, b)
				}
			}
			return b
		}
		p.vertices = add("vertices", gpu.BufferVertex, bytesOf(vertexData(s)))
		p.indices = add("indices", gpu.BufferIndex, bytesOf(s.Indices))
		if err != nil {
			return err
		}
		r.parts = append(r.parts, p)
	}
	r.bounds = bounds
	r.shadowFresh = false
	return nil
}

const (
	// OverlayVertex is the size in floats of a vertex of the overlay: position, colour and a shift in pixels.
	OverlayVertex = 9
	// OverlayLine is the size in floats of a line of the overlay: its two ends and a colour.
	OverlayLine = 10
)

// lineColor is the mesh lines' core; each has a dark rim, so that the one or the other stands out from any surface.
var lineColor = [4]float32{.96, .97, 1, .9}

// SetOverlay replaces what is drawn over the surfaces: triangles in OverlayVertex floats for each vertex, and lines in
// OverlayLine floats for each.
func (r *Renderer) SetOverlay(triangles, lines []float32) error {
	if err := r.d.Idle(); err != nil {
		return err
	}
	release(r.overlayTris, r.overlayLines)
	r.overlayTris, r.overlayLines, r.overlayTriCount, r.overlayLineCount = nil, nil, 0, 0
	var err error
	if len(triangles) > 0 {
		if r.overlayTris, err = r.buffer("overlay", gpu.BufferVertex, bytesOf(triangles)); err != nil {
			return err
		}
		r.overlayTriCount = uint32(len(triangles) / OverlayVertex)
	}
	if len(lines) > 0 {
		if r.overlayLines, err = r.buffer("overlay lines", gpu.BufferVertex, bytesOf(lines)); err != nil {
			return err
		}
		r.overlayLineCount = uint32(len(lines) / OverlayLine)
	}
	return nil
}

func name(im s3.Image, i int) string {
	if im.Name != "" {
		return im.Name
	}
	return fmt.Sprintf("picture %d", i+1)
}

func (r *Renderer) resize(w, h uint32) error {
	if r.w == w && r.h == h {
		return nil
	}
	if err := r.d.Idle(); err != nil {
		return err
	}
	release(r.colorV, r.depthV, r.resolveV, r.color, r.depth, r.resolve, r.staging)
	r.w, r.h = 0, 0
	d := r.d
	var err error
	if r.color, err = d.Texture(gpu.TextureDesc{Label: "colour", Width: w, Height: h, Samples: samples, Format: gpu.RGBA8Unorm, Usage: gpu.TextureTarget}); err != nil {
		return err
	}
	if r.depth, err = d.Texture(gpu.TextureDesc{Label: "depth", Width: w, Height: h, Samples: samples, Format: gpu.Depth32Float, Usage: gpu.TextureTarget}); err != nil {
		return err
	}
	if r.resolve, err = d.Texture(gpu.TextureDesc{Label: "resolved", Width: w, Height: h, Format: gpu.RGBA8Unorm, Usage: gpu.TextureTarget | gpu.TextureCopySrc}); err != nil {
		return err
	}
	if r.colorV, err = r.color.View(-1); err != nil {
		return err
	}
	if r.depthV, err = r.depth.View(-1); err != nil {
		return err
	}
	if r.resolveV, err = r.resolve.View(-1); err != nil {
		return err
	}
	size := uint64(gpu.RowPitch(w)) * uint64(h)
	if r.staging, err = d.Buffer(gpu.BufferDesc{Label: "readback", Size: size, Usage: gpu.BufferCopyDst | gpu.BufferMapRead}); err != nil {
		return err
	}
	r.row = slices.Grow(r.row[:0], int(size))[:size]
	r.w, r.h = w, h
	return nil
}

// lightMatrix is the sun's view of the scene: a box tight around it, as all it casts falls within that box as the sun
// sees it, and the width in the world of one texel of the shadow map.
func lightMatrix(bounds s3.Box, dir s3.Vec3) (s3.Mat4, float32) {
	center := bounds.Center()
	radius := max(bounds.Size().Length()*.5, 1e-4) * 1.05
	up := s3.Vec3{0, 1, 0}
	if math.Abs(float64(dir[1])) > .99 {
		up = s3.Vec3{1, 0, 0}
	}
	x := up.Cross(dir).Norm()
	y := dir.Cross(x)
	eye := center.Add(dir.Scale(radius * 2))
	view := s3.Mat4{x[0], y[0], dir[0], 0, x[1], y[1], dir[1], 0, x[2], y[2], dir[2], 0, -x.Dot(eye), -y.Dot(eye), -dir.Dot(eye), 1}
	// Depth from 0 at the eye's plane to 1 at twice the radius past the centre.
	proj := s3.Mat4{1 / radius, 0, 0, 0, 0, 1 / radius, 0, 0, 0, 0, -1 / (radius * 4), 0, 0, 0, 0, 1}
	return proj.Mul(view), radius * 2 / shadowSide
}

// inverse is the inverse of a transform.
func inverse(m s3.Mat4) s3.Mat4 {
	var inv s3.Mat4
	a := func(c, r int) float32 { return m[c*4+r] }
	cof := func(c0, c1, c2, r0, r1, r2 int) float32 {
		return a(c0, r0)*(a(c1, r1)*a(c2, r2)-a(c2, r1)*a(c1, r2)) - a(c1, r0)*(a(c0, r1)*a(c2, r2)-a(c2, r1)*a(c0, r2)) + a(c2, r0)*(a(c0, r1)*a(c1, r2)-a(c1, r1)*a(c0, r2))
	}
	others := func(i int) [3]int {
		o := [3]int{}
		n := 0
		for k := range 4 {
			if k != i {
				o[n] = k
				n++
			}
		}
		return o
	}
	var det float32
	for c := range 4 {
		for r := range 4 {
			cs, rs := others(c), others(r)
			v := cof(cs[0], cs[1], cs[2], rs[0], rs[1], rs[2])
			if (c+r)%2 == 1 {
				v = -v
			}
			inv[r*4+c] = v
			if r == 0 {
				det += a(c, 0) * v
			}
		}
	}
	for i := range inv {
		inv[i] /= det
	}
	return inv
}

// Draw renders the scene from cam with the given options into pixels, resized to w×h as needed. It waits for the card.
func (r *Renderer) Draw(cam *Camera, o Options, w, h int, out *image.RGBA) error {
	if r.frameGroup == nil {
		return fmt.Errorf("no sky is set")
	}
	if err := r.resize(uint32(w), uint32(h)); err != nil {
		return err
	}
	if o.View == ViewTraced {
		return r.Trace(cam, o, w, h, out)
	}
	if err := r.writeFrame(cam, o, w, h); err != nil {
		return err
	}
	if o.Wireframe && r.wireSurfaces != nil {
		if err := r.makeWires(); err != nil {
			return err
		}
	}
	enc, err := r.d.Encoder()
	if err != nil {
		return err
	}
	if o.Shadows && !r.shadowCurrent(o) {
		if err := r.drawShadows(enc); err != nil {
			return err
		}
		r.shadowFresh = true
	}
	if err := r.drawScene(enc, o, cam); err != nil {
		return err
	}
	pitch := gpu.RowPitch(uint32(w))
	enc.CopyToBuffer(r.resolve, r.staging, pitch, uint32(w), uint32(h))
	if err := enc.Submit(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	if err := r.d.ReadBuffer(ctx, r.staging, r.row); err != nil {
		return err
	}
	for y := range h {
		copy(out.Pix[y*out.Stride:y*out.Stride+w*4], r.row[y*int(pitch):])
	}
	return nil
}

// makeWires puts the mesh lines on the card.
func (r *Renderer) makeWires() error {
	r.wireEdges = meshEdges(r.wireSurfaces)
	lines := lineData(r.wireSurfaces, r.wireEdges, lineColor)
	r.wireSurfaces = nil
	if len(lines) == 0 {
		return nil
	}
	var err error
	r.wires, err = r.buffer("mesh lines", gpu.BufferVertex, bytesOf(lines))
	r.wireCount = uint32(len(lines) / OverlayLine)
	return err
}

// Repose moves the shapes put there by SetSurfaces to surfaces, the same shapes in another pose, in place. The bounds
// only grow, as the shadow would swim with them.
func (r *Renderer) Repose(surfaces []s3.Surface, bounds s3.Box) error {
	at := 0
	for i := range surfaces {
		s := &surfaces[i]
		if len(s.Indices) == 0 {
			continue
		}
		if at >= len(r.parts) {
			return fmt.Errorf("a pose of %d parts for %d shapes", at+1, len(r.parts))
		}
		if err := r.d.WriteBuffer(r.parts[at].vertices, 0, bytesOf(vertexData(s))); err != nil {
			return err
		}
		at++
	}
	if r.wireSurfaces != nil {
		r.wireSurfaces = surfaces
	} else if r.wires != nil {
		if err := r.d.WriteBuffer(r.wires, 0, bytesOf(lineData(surfaces, r.wireEdges, lineColor))); err != nil {
			return err
		}
	}
	if !bounds.Empty() {
		r.bounds.Grow(bounds.Min)
		r.bounds.Grow(bounds.Max)
	}
	r.shadowFresh = false
	r.trace.surfaces = surfaces
	r.trace.scene++
	return nil
}

// SetBones replaces the bones, drawn over everything so that the body hides none: joints as triangles in OverlayVertex
// floats for each vertex, and bones in OverlayLine floats for each.
func (r *Renderer) SetBones(triangles, lines []float32) error {
	if err := r.d.Idle(); err != nil {
		return err
	}
	release(r.boneTris, r.boneLines)
	r.boneTris, r.boneLines, r.boneTriCount, r.boneLineCount = nil, nil, 0, 0
	var err error
	if len(triangles) > 0 {
		if r.boneTris, err = r.buffer("joints", gpu.BufferVertex, bytesOf(triangles)); err != nil {
			return err
		}
		r.boneTriCount = uint32(len(triangles) / OverlayVertex)
	}
	if len(lines) > 0 {
		if r.boneLines, err = r.buffer("bones", gpu.BufferVertex, bytesOf(lines)); err != nil {
			return err
		}
		r.boneLineCount = uint32(len(lines) / OverlayLine)
	}
	return nil
}

// shadowCurrent reports whether the shadow map still holds for the sun in o, and remembers the sun's place.
func (r *Renderer) shadowCurrent(o Options) bool {
	dir := r.sunDir(o)
	key := [5]float32{dir[0], dir[1], dir[2], r.bounds.Min[1], r.bounds.Max[1]}
	same := r.shadowFresh && key == r.shadowKey
	r.shadowKey = key
	return same
}

// sunDir is the direction to the sun with the sky turned.
func (r *Renderer) sunDir(o Options) s3.Vec3 {
	d := r.sun.Dir()
	s, c := sincos(o.SkyRotation)
	return s3.Vec3{c*d[0] + s*d[2], d[1], -s*d[0] + c*d[2]}
}

func (r *Renderer) writeFrame(cam *Camera, o Options, w, h int) error {
	bounds := r.bounds
	if bounds.Empty() {
		bounds = s3.Box{Min: s3.Vec3{-1, 0, -1}, Max: s3.Vec3{1, 1, 1}}
	}
	size := max(bounds.Size().Length(), 1e-4)
	dist := bounds.Center().Sub(cam.Pos).Length()
	// Near enough for the closest surface the camera has come to, whatever the size of the scene.
	near := max(min(dist-size, cam.Distance*.05), size*1e-4)
	far := dist + size*40
	vp := cam.Projection(float32(w)/float32(h), near, far).Mul(cam.View())
	dir := r.sunDir(o)
	light, texel := lightMatrix(bounds, dir)
	shadows := float32(0)
	if o.Shadows && r.sun.Intensity > 0 {
		shadows = 1
	}
	exposure := o.Exposure
	if exposure <= 0 {
		exposure = 1
	}
	height := max(cam.Pos[1]-bounds.Min[1], size*.05)
	cell := float32(math.Pow(10, math.Floor(math.Log10(float64(height)))))
	reach := max(height*30, size*6)
	opacity := float32(0)
	if o.Grid {
		opacity = 1
	}
	showSky := float32(0)
	if o.ShowSky {
		showSky = 1
	}
	s, c := sincos(o.SkyRotation)
	data := make([]float32, frameSize/4)
	copy(data[0:], vp[:])
	inv := inverse(vp)
	copy(data[16:], inv[:])
	copy(data[32:], light[:])
	copy(data[48:], []float32{cam.Pos[0], cam.Pos[1], cam.Pos[2], exposure})
	copy(data[52:], []float32{dir[0], dir[1], dir[2], shadows})
	copy(data[56:], []float32{r.sun.Color[0], r.sun.Color[1], r.sun.Color[2], r.sun.Intensity})
	for i, h := range r.harmonics {
		copy(data[60+i*4:], h[:])
	}
	copy(data[96:], []float32{float32(o.View), c, s, bounds.Min[1]})
	copy(data[100:], []float32{0, 0, 0, opacity})
	copy(data[104:], []float32{cell, reach, texel, showSky})
	lineScale := o.LineScale
	if lineScale <= 0 {
		lineScale = 1
	}
	copy(data[108:], []float32{2 / float32(w), 2 / float32(h), lineScale, 0})
	return r.d.WriteBuffer(r.frameBuf, 0, bytesOf(data))
}

// drawShadows fills the shadow map: solid surfaces first, then, as a fragment shader cannot go without a colour target
// in the library, the cut-out ones in a pass of their own.
func (r *Renderer) drawShadows(enc gpu.Encoder) error {
	p, err := enc.Pass(gpu.PassDesc{Depth: &gpu.DepthTarget{View: r.shadowView, Clear: 1, Keep: true}})
	if err != nil {
		return err
	}
	p.SetPipeline(r.pipes["shadow"])
	p.SetBindGroup(0, r.frameGroup)
	cutouts := false
	for i := range r.parts {
		q := &r.parts[i]
		cutouts = cutouts || q.material.cutout
		if !q.material.blend && !q.material.cutout {
			r.drawPart(p, q)
		}
	}
	if err := p.End(); err != nil || !cutouts {
		return err
	}
	if r.cutoutColor == nil {
		if r.cutoutColor, err = r.d.Texture(gpu.TextureDesc{Label: "cutout shadow", Width: shadowSide, Height: shadowSide, Format: gpu.RGBA8Unorm, Usage: gpu.TextureTarget}); err != nil {
			return err
		}
		if r.cutoutView, err = r.cutoutColor.View(-1); err != nil {
			return err
		}
	}
	p, err = enc.Pass(gpu.PassDesc{Color: &gpu.ColorTarget{View: r.cutoutView, Clear: &[4]float64{}}, Depth: &gpu.DepthTarget{View: r.shadowView, Load: true, Keep: true}})
	if err != nil {
		return err
	}
	p.SetPipeline(r.pipes["cutout"])
	p.SetBindGroup(0, r.frameGroup)
	for i := range r.parts {
		if q := &r.parts[i]; q.material.cutout {
			r.drawPart(p, q)
		}
	}
	return p.End()
}

func (r *Renderer) drawPart(p gpu.Pass, q *part) {
	p.SetBindGroup(1, q.material.group)
	p.SetVertexBuffer(q.vertices)
	p.SetIndexBuffer(q.indices)
	p.DrawIndexed(q.count, 0)
}

func (r *Renderer) drawScene(enc gpu.Encoder, o Options, cam *Camera) error {
	p, err := enc.Pass(gpu.PassDesc{
		Color: &gpu.ColorTarget{View: r.colorV, Resolve: r.resolveV, Clear: &[4]float64{0, 0, 0, 1}},
		Depth: &gpu.DepthTarget{View: r.depthV, Clear: 1}})
	if err != nil {
		return err
	}
	p.SetPipeline(r.pipes["sky"])
	p.SetBindGroup(0, r.frameGroup)
	p.Draw(3)
	var blended []*part
	for i := range r.parts {
		q := &r.parts[i]
		if q.material.blend && o.View == ViewMaterial {
			blended = append(blended, q)
			continue
		}
		if q.material.double {
			p.SetPipeline(r.pipes["double"])
		} else {
			p.SetPipeline(r.pipes["opaque"])
		}
		r.drawPart(p, q)
	}
	if o.Grid || o.Shadows {
		p.SetPipeline(r.pipes["ground"])
		p.Draw(6)
	}
	// Far to near, as what is blended has to be.
	slices.SortFunc(blended, func(a, b *part) int {
		da, db := a.center.Sub(cam.Pos).Length(), b.center.Sub(cam.Pos).Length()
		switch {
		case da > db:
			return -1
		case da < db:
			return 1
		}
		return 0
	})
	p.SetPipeline(r.pipes["blend"])
	for _, q := range blended {
		r.drawPart(p, q)
	}
	if o.Wireframe {
		r.drawLines(p, r.wires, r.wireCount)
	}
	if r.overlayLineCount > 0 {
		r.drawLines(p, r.overlayLines, r.overlayLineCount)
	}
	if r.overlayTriCount > 0 {
		p.SetPipeline(r.pipes["overlay"])
		p.SetVertexBuffer(r.overlayTris)
		p.Draw(r.overlayTriCount)
	}
	if r.boneLineCount > 0 {
		for _, pipe := range []string{"bone rim", "bone core"} {
			p.SetPipeline(r.pipes[pipe])
			p.SetVertexBuffer(r.boneLines)
			p.DrawInstanced(6, r.boneLineCount)
		}
	}
	if r.boneTriCount > 0 {
		p.SetPipeline(r.pipes["joints"])
		p.SetVertexBuffer(r.boneTris)
		p.Draw(r.boneTriCount)
	}
	return p.End()
}

// drawLines draws every rim before any core, so that where lines meet no rim crosses a core.
func (r *Renderer) drawLines(p gpu.Pass, lines gpu.Buffer, count uint32) {
	if count == 0 {
		return
	}
	for _, pipe := range []string{"line rim", "line core"} {
		p.SetPipeline(r.pipes[pipe])
		p.SetVertexBuffer(lines)
		p.DrawInstanced(6, count)
	}
}

// Close frees everything on the card, but not the Device.
func (r *Renderer) Close() {
	if r.d == nil {
		return
	}
	r.d.Idle()
	r.trace.close()
	r.clearGeometry()
	r.clearMaterials()
	release(r.overlayTris, r.overlayLines, r.wires, r.boneTris, r.boneLines)
	release(r.colorV, r.depthV, r.resolveV, r.color, r.depth, r.resolve, r.staging)
	release(r.frameGroup, r.skyView, r.sky, r.specularAll, r.specular, r.shadowView, r.shadow, r.cutoutView, r.cutoutColor, r.frameBuf)
	release(r.whiteView, r.flatView, r.white, r.flat, r.texSampler, r.skySampler, r.shadowSampler)
	for _, p := range r.pipes {
		p.Release()
	}
	release(r.scene, r.prefilter, r.frameLayout, r.materialLayout, r.prefilterLayout)
	r.d = nil
}
