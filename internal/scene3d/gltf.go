package scene3d

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Limits on what a file may claim, so that a broken one fails rather than asks for all the memory there is.
const (
	glbMagic      = 0x46546c67 // "glTF"
	chunkJSON     = 0x4e4f534a
	chunkBIN      = 0x004e4942
	maxAccessor   = 1 << 28 // items in one list
	gltfByte      = 5120
	gltfUbyte     = 5121
	gltfShort     = 5122
	gltfUshort    = 5123
	gltfUint      = 5125
	gltfFloat     = 5126
	modeTriangles = 4
	modeStrip     = 5
	modeFan       = 6
)

// knownExtensions are understood, or safe to ignore: without the others a file may show wrongly.
var knownExtensions = []string{"KHR_mesh_quantization", "EXT_texture_webp", "FB_ngon_encoding"}

// unreadable are the extensions a file cannot be read without, with what they are.
var unreadable = map[string]string{
	"KHR_draco_mesh_compression": "Draco mesh compression",
	"EXT_meshopt_compression":    "meshopt compression",
	"KHR_meshopt_compression":    "meshopt compression",
}

type gltfAccessor struct {
	BufferView    *int
	ByteOffset    int
	ComponentType int
	Normalized    bool
	Count         int
	Type          string
	Sparse        *struct {
		Count   int
		Indices struct {
			BufferView, ByteOffset, ComponentType int
		}
		Values struct{ BufferView, ByteOffset int }
	}
}

type gltfTexInfo struct {
	Index      int
	TexCoord   int
	Scale      *float32
	Strength   *float32
	Extensions json.RawMessage
}

type gltfDoc struct {
	Asset  struct{ Version string }
	Scene  *int
	Scenes []struct{ Nodes []int }
	Nodes  []struct {
		Name        string
		Mesh, Skin  *int
		Children    []int
		Matrix      []float32
		Translation []float32
		Rotation    []float32
		Scale       []float32
	}
	Meshes []struct {
		Name       string
		Primitives []struct {
			Attributes map[string]int
			Indices    *int
			Material   *int
			Mode       *int
			Targets    []map[string]int
		}
		Weights []float32
		Extras  json.RawMessage
	}
	Accessors   []gltfAccessor
	BufferViews []struct{ Buffer, ByteOffset, ByteLength, ByteStride int }
	Buffers     []struct {
		URI        string
		ByteLength int
	}
	Materials []struct {
		Name                 string
		PbrMetallicRoughness *struct {
			BaseColorFactor          []float32
			MetallicFactor           *float32
			RoughnessFactor          *float32
			BaseColorTexture         *gltfTexInfo
			MetallicRoughnessTexture *gltfTexInfo
		}
		NormalTexture, OcclusionTexture, EmissiveTexture *gltfTexInfo
		EmissiveFactor                                   []float32
		AlphaMode                                        string
		AlphaCutoff                                      *float32
		DoubleSided                                      bool
		Extensions                                       json.RawMessage
	}
	Textures []struct {
		Source     *int
		Sampler    *int
		Extensions map[string]struct{ Source int }
	}
	Samplers []json.RawMessage
	Images   []struct {
		Name, URI, MimeType string
		BufferView          *int
	}
	Skins []struct {
		Name                string
		InverseBindMatrices *int
		Joints              []int
	}
	Animations []struct {
		Name     string
		Channels []struct {
			Sampler int
			Target  struct {
				Node *int
				Path string
			}
		}
		Samplers []struct {
			Input, Output int
			Interpolation string
		}
	}
	ExtensionsUsed, ExtensionsRequired []string
}

// Load reads a .glb, .gltf or .fbx file, with the files beside it that it names.
func Load(path string) (*Asset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	read := func(name string) ([]byte, error) {
		// A file may only name what is beside it or under it.
		clean := filepath.Clean(filepath.FromSlash(name))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%q is outside the file's folder", name)
		}
		return os.ReadFile(filepath.Join(dir, clean))
	}
	if strings.EqualFold(filepath.Ext(path), ".fbx") {
		return DecodeFBX(data, read)
	}
	return Decode(data, read)
}

// Decode reads a .glb or .gltf held in memory; read fetches the files it names.
func Decode(data []byte, read func(name string) ([]byte, error)) (*Asset, error) {
	js, bin, err := splitGLB(data)
	if err != nil {
		return nil, err
	}
	var doc gltfDoc
	if err := json.Unmarshal(js, &doc); err != nil {
		return nil, fmt.Errorf("the file's description is broken: %w", err)
	}
	if !strings.HasPrefix(doc.Asset.Version, "2.") {
		return nil, fmt.Errorf("glTF version %q is not supported", doc.Asset.Version)
	}
	for _, e := range slices.Concat(doc.ExtensionsRequired, doc.ExtensionsUsed) {
		if what, ok := unreadable[e]; ok {
			return nil, fmt.Errorf("the file uses %s, which cannot be read yet", what)
		}
	}
	d := &decoder{doc: &doc}
	if err := d.loadBuffers(bin, read); err != nil {
		return nil, err
	}
	return d.asset(read)
}

// splitGLB finds the JSON and the binary chunk of a .glb; a .gltf is JSON alone.
func splitGLB(data []byte) (js, bin []byte, err error) {
	if len(data) < 4 || binary.LittleEndian.Uint32(data) != glbMagic {
		return data, nil, nil
	}
	if len(data) < 12 {
		return nil, nil, errors.New("the file is cut short")
	}
	if v := binary.LittleEndian.Uint32(data[4:]); v != 2 {
		return nil, nil, fmt.Errorf("GLB version %d is not supported", v)
	}
	for p := 12; p+8 <= len(data); {
		n := int(binary.LittleEndian.Uint32(data[p:]))
		kind := binary.LittleEndian.Uint32(data[p+4:])
		if n < 0 || p+8+n > len(data) {
			return nil, nil, errors.New("the file is cut short")
		}
		switch kind {
		case chunkJSON:
			js = data[p+8 : p+8+n]
		case chunkBIN:
			bin = data[p+8 : p+8+n]
		}
		p += 8 + n
	}
	if js == nil {
		return nil, nil, errors.New("the file has no description")
	}
	return js, bin, nil
}

type decoder struct {
	doc     *gltfDoc
	buffers [][]byte
}

func (d *decoder) loadBuffers(bin []byte, read func(string) ([]byte, error)) error {
	d.buffers = make([][]byte, len(d.doc.Buffers))
	for i, b := range d.doc.Buffers {
		switch {
		case b.URI == "":
			if i != 0 || bin == nil {
				return fmt.Errorf("buffer %d has no data", i)
			}
			d.buffers[i] = bin
		default:
			data, err := fetch(b.URI, read)
			if err != nil {
				return fmt.Errorf("buffer %d: %w", i, err)
			}
			d.buffers[i] = data
		}
		if len(d.buffers[i]) < b.ByteLength {
			return fmt.Errorf("buffer %d is cut short", i)
		}
	}
	return nil
}

// fetch reads what a URI points to: embedded data, or a file named relative to the asset.
func fetch(uri string, read func(string) ([]byte, error)) ([]byte, error) {
	if rest, ok := strings.CutPrefix(uri, "data:"); ok {
		_, payload, found := strings.Cut(rest, ",")
		if !found {
			return nil, errors.New("embedded data is broken")
		}
		if strings.Contains(rest[:len(rest)-len(payload)], ";base64") {
			return base64.StdEncoding.DecodeString(payload)
		}
		s, err := url.PathUnescape(payload)
		return []byte(s), err
	}
	name, err := url.PathUnescape(uri)
	if err != nil {
		name = uri
	}
	return read(name)
}

func componentSize(t int) int {
	switch t {
	case gltfByte, gltfUbyte:
		return 1
	case gltfShort, gltfUshort:
		return 2
	case gltfUint, gltfFloat:
		return 4
	}
	return 0
}

func typeWidth(t string) int {
	switch t {
	case "SCALAR":
		return 1
	case "VEC2":
		return 2
	case "VEC3":
		return 3
	case "VEC4", "MAT2":
		return 4
	case "MAT3":
		return 9
	case "MAT4":
		return 16
	}
	return 0
}

// component reads one number at b as a float, unpacked when normalized.
func component(b []byte, t int, normalized bool) float32 {
	switch t {
	case gltfByte:
		v := float32(int8(b[0]))
		if normalized {
			return max(v/127, -1)
		}
		return v
	case gltfUbyte:
		if normalized {
			return float32(b[0]) / 255
		}
		return float32(b[0])
	case gltfShort:
		v := float32(int16(binary.LittleEndian.Uint16(b)))
		if normalized {
			return max(v/32767, -1)
		}
		return v
	case gltfUshort:
		v := float32(binary.LittleEndian.Uint16(b))
		if normalized {
			return v / 65535
		}
		return v
	case gltfUint:
		return float32(binary.LittleEndian.Uint32(b))
	}
	return math.Float32frombits(binary.LittleEndian.Uint32(b))
}

// view is the bytes of a buffer view from offset on, and the distance between its items.
func (d *decoder) view(i, offset, item int) ([]byte, int, error) {
	if i < 0 || i >= len(d.doc.BufferViews) {
		return nil, 0, fmt.Errorf("buffer view %d is missing", i)
	}
	v := d.doc.BufferViews[i]
	if v.Buffer < 0 || v.Buffer >= len(d.buffers) {
		return nil, 0, fmt.Errorf("buffer %d is missing", v.Buffer)
	}
	end := v.ByteOffset + v.ByteLength
	if v.ByteOffset < 0 || v.ByteLength < 0 || end > len(d.buffers[v.Buffer]) || offset < 0 || offset > v.ByteLength {
		return nil, 0, fmt.Errorf("buffer view %d reaches past its buffer", i)
	}
	stride := v.ByteStride
	if stride == 0 {
		stride = item
	}
	return d.buffers[v.Buffer][v.ByteOffset+offset : end], stride, nil
}

// floats is an accessor's numbers, width of them for each item, sparse changes applied.
func (d *decoder) floats(i int, want ...string) ([]float32, int, error) {
	if i < 0 || i >= len(d.doc.Accessors) {
		return nil, 0, fmt.Errorf("accessor %d is missing", i)
	}
	a := &d.doc.Accessors[i]
	if len(want) > 0 && !slices.Contains(want, a.Type) {
		return nil, 0, fmt.Errorf("accessor %d is %s, not %s", i, a.Type, strings.Join(want, " or "))
	}
	size, width := componentSize(a.ComponentType), typeWidth(a.Type)
	if size == 0 || width == 0 || a.Count < 0 || a.Count > maxAccessor {
		return nil, 0, fmt.Errorf("accessor %d is broken", i)
	}
	out := make([]float32, a.Count*width)
	if a.BufferView != nil {
		data, stride, err := d.view(*a.BufferView, a.ByteOffset, size*width)
		if err != nil {
			return nil, 0, err
		}
		if a.Count > 0 && (a.Count-1)*stride+size*width > len(data) {
			return nil, 0, fmt.Errorf("accessor %d reaches past its data", i)
		}
		for k := range a.Count {
			for c := range width {
				out[k*width+c] = component(data[k*stride+c*size:], a.ComponentType, a.Normalized)
			}
		}
	}
	if s := a.Sparse; s != nil {
		indexSize := componentSize(s.Indices.ComponentType)
		if indexSize == 0 || s.Count < 0 || s.Count > a.Count {
			return nil, 0, fmt.Errorf("accessor %d has broken sparse data", i)
		}
		at, _, err := d.view(s.Indices.BufferView, s.Indices.ByteOffset, indexSize)
		if err != nil {
			return nil, 0, err
		}
		values, _, err := d.view(s.Values.BufferView, s.Values.ByteOffset, size*width)
		if err != nil {
			return nil, 0, err
		}
		if s.Count*indexSize > len(at) || s.Count*size*width > len(values) {
			return nil, 0, fmt.Errorf("accessor %d has broken sparse data", i)
		}
		for k := range s.Count {
			index := int(component(at[k*indexSize:], s.Indices.ComponentType, false))
			if index < 0 || index >= a.Count {
				return nil, 0, fmt.Errorf("accessor %d has broken sparse data", i)
			}
			for c := range width {
				out[index*width+c] = component(values[(k*width+c)*size:], a.ComponentType, a.Normalized)
			}
		}
	}
	return out, width, nil
}

func vecs2(f []float32) []Vec2 {
	out := make([]Vec2, len(f)/2)
	for i := range out {
		out[i] = Vec2{f[i*2], f[i*2+1]}
	}
	return out
}

func vecs3(f []float32) []Vec3 {
	out := make([]Vec3, len(f)/3)
	for i := range out {
		out[i] = Vec3{f[i*3], f[i*3+1], f[i*3+2]}
	}
	return out
}

func vecs4(f []float32) []Vec4 {
	out := make([]Vec4, len(f)/4)
	for i := range out {
		out[i] = Vec4{f[i*4], f[i*4+1], f[i*4+2], f[i*4+3]}
	}
	return out
}

// attribute reads an optional vertex attribute: it must have one item for each vertex.
func (d *decoder) attribute(attrs map[string]int, name string, count int, types ...string) ([]float32, int, error) {
	a, ok := attrs[name]
	if !ok {
		return nil, 0, nil
	}
	f, w, err := d.floats(a, types...)
	if err != nil {
		return nil, 0, fmt.Errorf("%s: %w", name, err)
	}
	if len(f) != count*w {
		return nil, 0, fmt.Errorf("%s has %d items for %d vertices", name, len(f)/w, count)
	}
	return f, w, nil
}

func (d *decoder) prim(attrs map[string]int, indices *int, mode int) (Prim, error) {
	var p Prim
	pos, _, err := d.floats(attrs["POSITION"], "VEC3")
	if _, ok := attrs["POSITION"]; !ok {
		err = errors.New("it has no positions")
	}
	if err != nil {
		return p, err
	}
	p.Pos = vecs3(pos)
	n := len(p.Pos)
	if f, _, err := d.attribute(attrs, "NORMAL", n, "VEC3"); err != nil {
		return p, err
	} else if f != nil {
		p.Normal = vecs3(f)
		for i := range p.Normal {
			p.Normal[i] = p.Normal[i].Norm()
		}
	}
	if f, _, err := d.attribute(attrs, "TANGENT", n, "VEC4"); err != nil {
		return p, err
	} else if f != nil {
		p.Tangent = vecs4(f)
	}
	if f, _, err := d.attribute(attrs, "TEXCOORD_0", n, "VEC2"); err != nil {
		return p, err
	} else if f != nil {
		p.UV = vecs2(f)
	}
	if f, _, err := d.attribute(attrs, "TEXCOORD_1", n, "VEC2"); err != nil {
		return p, err
	} else if f != nil {
		p.UV2 = vecs2(f)
	}
	if f, w, err := d.attribute(attrs, "COLOR_0", n, "VEC3", "VEC4"); err != nil {
		return p, err
	} else if f != nil {
		p.Color = make([]Vec4, n)
		for i := range p.Color {
			p.Color[i] = Vec4{f[i*w], f[i*w+1], f[i*w+2], 1}
			if w == 4 {
				p.Color[i][3] = f[i*4+3]
			}
		}
	}
	joints, _, err := d.attribute(attrs, "JOINTS_0", n, "VEC4")
	if err != nil {
		return p, err
	}
	weights, _, err := d.attribute(attrs, "WEIGHTS_0", n, "VEC4")
	if err != nil {
		return p, err
	}
	if joints != nil && weights != nil {
		p.Joints, p.Weights = make([][4]uint16, n), vecs4(weights)
		for i := range p.Joints {
			p.Joints[i] = [4]uint16{uint16(joints[i*4]), uint16(joints[i*4+1]), uint16(joints[i*4+2]), uint16(joints[i*4+3])}
		}
	}
	var order []uint32
	if indices != nil {
		f, _, err := d.floatsExact(*indices)
		if err != nil {
			return p, err
		}
		order = f
	} else {
		order = make([]uint32, n)
		for i := range order {
			order[i] = uint32(i)
		}
	}
	for _, i := range order {
		if int(i) >= n {
			return p, fmt.Errorf("an index reaches vertex %d of %d", i, n)
		}
	}
	p.Indices = triangles(order, mode)
	if p.Normal == nil {
		p.Normal = FlatNormals(p.Pos, p.Indices)
	}
	return p, nil
}

// floatsExact is an accessor of indices. Those of 32 bits do not fit a float, so they are read as they are.
func (d *decoder) floatsExact(i int) ([]uint32, int, error) {
	if i < 0 || i >= len(d.doc.Accessors) {
		return nil, 0, fmt.Errorf("accessor %d is missing", i)
	}
	a := &d.doc.Accessors[i]
	size := componentSize(a.ComponentType)
	if a.Type != "SCALAR" || a.ComponentType == gltfFloat || size == 0 || a.BufferView == nil || a.Count < 0 || a.Count > maxAccessor {
		return nil, 0, fmt.Errorf("accessor %d cannot hold indices", i)
	}
	data, stride, err := d.view(*a.BufferView, a.ByteOffset, size)
	if err != nil {
		return nil, 0, err
	}
	if a.Count > 0 && (a.Count-1)*stride+size > len(data) {
		return nil, 0, fmt.Errorf("accessor %d reaches past its data", i)
	}
	out := make([]uint32, a.Count)
	for k := range out {
		b := data[k*stride:]
		switch size {
		case 1:
			out[k] = uint32(b[0])
		case 2:
			out[k] = uint32(binary.LittleEndian.Uint16(b))
		default:
			out[k] = binary.LittleEndian.Uint32(b)
		}
	}
	return out, 1, nil
}

// triangles turns strips and fans into a plain list of triangles.
func triangles(order []uint32, mode int) []uint32 {
	switch mode {
	case modeStrip:
		out := make([]uint32, 0, max(len(order)-2, 0)*3)
		for i := 0; i+2 < len(order); i++ {
			if i%2 == 0 {
				out = append(out, order[i], order[i+1], order[i+2])
			} else {
				out = append(out, order[i+1], order[i], order[i+2])
			}
		}
		return out
	case modeFan:
		out := make([]uint32, 0, max(len(order)-2, 0)*3)
		for i := 1; i+1 < len(order); i++ {
			out = append(out, order[0], order[i], order[i+1])
		}
		return out
	}
	return order[:len(order)/3*3]
}

// FlatNormals is one normal for each vertex, the mean of its triangles' by their area.
func FlatNormals(pos []Vec3, indices []uint32) []Vec3 {
	out := make([]Vec3, len(pos))
	for i := 0; i+2 < len(indices); i += 3 {
		a, b, c := indices[i], indices[i+1], indices[i+2]
		f := pos[b].Sub(pos[a]).Cross(pos[c].Sub(pos[a]))
		out[a], out[b], out[c] = out[a].Add(f), out[b].Add(f), out[c].Add(f)
	}
	for i := range out {
		out[i] = out[i].Norm()
	}
	return out
}

func (d *decoder) texRef(t *gltfTexInfo) (TexRef, error) {
	if t == nil {
		return TexRef{Image: -1, Sampler: -1}, nil
	}
	if t.Index < 0 || t.Index >= len(d.doc.Textures) {
		return TexRef{}, fmt.Errorf("texture %d is missing", t.Index)
	}
	x := d.doc.Textures[t.Index]
	image := -1
	if x.Source != nil {
		image = *x.Source
	}
	for _, name := range []string{"EXT_texture_webp"} { // preferred over the fallback, when it has one
		if e, ok := x.Extensions[name]; ok {
			image = e.Source
		}
	}
	if image < -1 || image >= len(d.doc.Images) {
		return TexRef{}, fmt.Errorf("image %d is missing", image)
	}
	sampler := -1
	if x.Sampler != nil {
		if *x.Sampler < 0 || *x.Sampler >= len(d.doc.Samplers) {
			return TexRef{}, fmt.Errorf("sampler %d is missing", *x.Sampler)
		}
		sampler = *x.Sampler
	}
	return TexRef{Image: image, UV: t.TexCoord, Sampler: sampler, Extensions: t.Extensions}, nil
}

func (d *decoder) material(i int) (Material, error) {
	src := &d.doc.Materials[i]
	m := DefaultMaterial()
	m.Name, m.DoubleSided, m.Extensions = src.Name, src.DoubleSided, src.Extensions
	var err error
	take := func(to *TexRef, from *gltfTexInfo) {
		if err == nil {
			*to, err = d.texRef(from)
		}
	}
	if p := src.PbrMetallicRoughness; p != nil {
		if len(p.BaseColorFactor) == 4 {
			copy(m.BaseColor[:], p.BaseColorFactor)
		}
		if p.MetallicFactor != nil {
			m.Metallic = *p.MetallicFactor
		}
		if p.RoughnessFactor != nil {
			m.Roughness = *p.RoughnessFactor
		}
		take(&m.BaseTex, p.BaseColorTexture)
		take(&m.MetalRoughTex, p.MetallicRoughnessTexture)
	}
	take(&m.NormalTex, src.NormalTexture)
	take(&m.OcclusionTex, src.OcclusionTexture)
	take(&m.EmissiveTex, src.EmissiveTexture)
	if t := src.NormalTexture; t != nil && t.Scale != nil {
		m.NormalScale = *t.Scale
	}
	if t := src.OcclusionTexture; t != nil && t.Strength != nil {
		m.Occlusion = *t.Strength
	}
	if len(src.EmissiveFactor) == 3 {
		copy(m.Emissive[:], src.EmissiveFactor)
	}
	switch src.AlphaMode {
	case "MASK":
		m.AlphaMode = AlphaMask
	case "BLEND":
		m.AlphaMode = AlphaBlend
	}
	if src.AlphaCutoff != nil {
		m.AlphaCutoff = *src.AlphaCutoff
	}
	return m, err
}

func (d *decoder) asset(read func(string) ([]byte, error)) (*Asset, error) {
	doc := d.doc
	a := &Asset{Samplers: doc.Samplers, ExtensionsUsed: doc.ExtensionsUsed, ExtensionsRequired: doc.ExtensionsRequired}
	for i, src := range doc.Images {
		im := Image{Name: src.Name, Mime: src.MimeType}
		var err error
		switch {
		case src.BufferView != nil:
			im.Data, _, err = d.view(*src.BufferView, 0, 1)
			if err == nil {
				im.Data = im.Data[:min(len(im.Data), doc.BufferViews[*src.BufferView].ByteLength)]
			}
		case src.URI != "":
			im.Data, err = fetch(src.URI, read)
		}
		if err != nil {
			return nil, fmt.Errorf("image %d: %w", i, err)
		}
		a.Images = append(a.Images, im)
	}
	for i := range doc.Materials {
		m, err := d.material(i)
		if err != nil {
			return nil, fmt.Errorf("material %d: %w", i, err)
		}
		a.Materials = append(a.Materials, m)
	}
	for i, src := range doc.Meshes {
		mesh := Mesh{Name: src.Name, Weights: src.Weights}
		var extras struct{ TargetNames []string }
		if json.Unmarshal(src.Extras, &extras) == nil {
			mesh.Keys = extras.TargetNames
		}
		for j, pr := range src.Primitives {
			mode := modeTriangles
			if pr.Mode != nil {
				mode = *pr.Mode
			}
			if mode != modeTriangles && mode != modeStrip && mode != modeFan {
				a.note("Points and lines are not shown.")
				continue
			}
			p, err := d.prim(pr.Attributes, pr.Indices, mode)
			if err != nil {
				return nil, fmt.Errorf("mesh %d part %d: %w", i, j, err)
			}
			p.Material = -1
			if pr.Material != nil {
				if *pr.Material < 0 || *pr.Material >= len(a.Materials) {
					return nil, fmt.Errorf("mesh %d part %d: material %d is missing", i, j, *pr.Material)
				}
				p.Material = *pr.Material
			}
			for k, target := range pr.Targets {
				morph := Morph{}
				for name, to := range map[string]*[]Vec3{"POSITION": &morph.Pos, "NORMAL": &morph.Normal} {
					values, _, err := d.attribute(target, name, len(p.Pos), "VEC3")
					if err != nil {
						return nil, fmt.Errorf("mesh %d part %d shape key %d: %w", i, j, k, err)
					}
					if values != nil {
						*to = vecs3(values)
					}
				}
				if morph.Pos == nil {
					morph.Pos = make([]Vec3, len(p.Pos)) // a key may change the normals alone
				}
				p.Morphs = append(p.Morphs, morph)
			}
			mesh.Prims = append(mesh.Prims, p)
		}
		a.Meshes = append(a.Meshes, mesh)
	}
	if err := d.nodes(a); err != nil {
		return nil, err
	}
	if err := d.skins(a); err != nil {
		return nil, err
	}
	if err := d.clips(a); err != nil {
		return nil, err
	}
	for _, e := range doc.ExtensionsUsed {
		if !slices.Contains(knownExtensions, e) {
			a.Unshown = append(a.Unshown, e)
		}
	}
	return a, nil
}

func (a *Asset) note(s string) {
	if !slices.Contains(a.Notes, s) {
		a.Notes = append(a.Notes, s)
	}
}

func (d *decoder) nodes(a *Asset) error {
	doc := d.doc
	a.Nodes = make([]Node, len(doc.Nodes))
	for i, src := range doc.Nodes {
		n := Node{Name: src.Name, Parent: -1, Mesh: -1, Skin: -1, Scale: Vec3{1, 1, 1}, Rotation: Quat{0, 0, 0, 1}}
		switch {
		case len(src.Matrix) == 16:
			var m Mat4
			copy(m[:], src.Matrix)
			n.Translation, n.Rotation, n.Scale = Decompose(m)
		default:
			if len(src.Translation) == 3 {
				copy(n.Translation[:], src.Translation)
			}
			if len(src.Rotation) == 4 {
				copy(n.Rotation[:], src.Rotation)
			}
			if len(src.Scale) == 3 {
				copy(n.Scale[:], src.Scale)
			}
		}
		if src.Mesh != nil {
			if *src.Mesh < 0 || *src.Mesh >= len(a.Meshes) {
				return fmt.Errorf("node %d: mesh %d is missing", i, *src.Mesh)
			}
			n.Mesh = *src.Mesh
		}
		if src.Skin != nil {
			if *src.Skin < 0 || *src.Skin >= len(doc.Skins) {
				return fmt.Errorf("node %d: skin %d is missing", i, *src.Skin)
			}
			n.Skin = *src.Skin
		}
		n.Children = src.Children
		a.Nodes[i] = n
	}
	for i := range a.Nodes {
		for _, c := range a.Nodes[i].Children {
			if c < 0 || c >= len(a.Nodes) || a.Nodes[c].Parent >= 0 || c == i {
				return fmt.Errorf("node %d has a broken child %d", i, c)
			}
			a.Nodes[c].Parent = i
		}
	}
	// The scene's roots, or else every node without a parent. A loop of nodes has no root, and is unreachable.
	switch {
	case doc.Scene != nil && *doc.Scene >= 0 && *doc.Scene < len(doc.Scenes):
		a.Roots = doc.Scenes[*doc.Scene].Nodes
	case len(doc.Scenes) > 0:
		a.Roots = doc.Scenes[0].Nodes
	default:
		for i := range a.Nodes {
			if a.Nodes[i].Parent < 0 {
				a.Roots = append(a.Roots, i)
			}
		}
	}
	for _, r := range a.Roots {
		if r < 0 || r >= len(a.Nodes) || a.Nodes[r].Parent >= 0 {
			return fmt.Errorf("root %d is broken", r)
		}
	}
	return nil
}

func (d *decoder) skins(a *Asset) error {
	for i, src := range d.doc.Skins {
		s := Skin{Name: src.Name, Joints: src.Joints}
		for _, j := range src.Joints {
			if j < 0 || j >= len(a.Nodes) {
				return fmt.Errorf("skin %d: joint %d is missing", i, j)
			}
		}
		s.InverseBind = make([]Mat4, len(src.Joints))
		for k := range s.InverseBind {
			s.InverseBind[k] = Identity()
		}
		if src.InverseBindMatrices != nil {
			f, _, err := d.floats(*src.InverseBindMatrices, "MAT4")
			if err != nil {
				return fmt.Errorf("skin %d: %w", i, err)
			}
			if len(f) != 16*len(src.Joints) {
				return fmt.Errorf("skin %d has %d matrices for %d joints", i, len(f)/16, len(src.Joints))
			}
			for k := range s.InverseBind {
				copy(s.InverseBind[k][:], f[k*16:])
			}
		}
		a.Skins = append(a.Skins, s)
	}
	// Every vertex of a skinned part must reach joints that exist, as Surfaces trusts them.
	for i := range a.Nodes {
		n := &a.Nodes[i]
		if n.Mesh < 0 || n.Skin < 0 {
			continue
		}
		limit := uint16(len(a.Skins[n.Skin].Joints))
		for j := range a.Meshes[n.Mesh].Prims {
			for _, v := range a.Meshes[n.Mesh].Prims[j].Joints {
				for _, k := range v {
					if k >= limit {
						return fmt.Errorf("node %d: a vertex reaches joint %d of %d", i, k, limit)
					}
				}
			}
		}
	}
	return nil
}

func (d *decoder) clips(a *Asset) error {
	for i, src := range d.doc.Animations {
		c := Clip{Name: src.Name}
		for k, ch := range src.Channels {
			if ch.Target.Node == nil {
				continue // aimed at an extension's target
			}
			if ch.Sampler < 0 || ch.Sampler >= len(src.Samplers) || *ch.Target.Node < 0 || *ch.Target.Node >= len(a.Nodes) {
				return fmt.Errorf("animation %d channel %d is broken", i, k)
			}
			s := src.Samplers[ch.Sampler]
			times, _, err := d.floats(s.Input, "SCALAR")
			if err != nil {
				return fmt.Errorf("animation %d: %w", i, err)
			}
			values, width, err := d.floats(s.Output)
			if err != nil {
				return fmt.Errorf("animation %d: %w", i, err)
			}
			out := Channel{Node: *ch.Target.Node, Times: times, Values: values, Width: width}
			switch ch.Target.Path {
			case "translation":
				out.Path = PathTranslation
			case "rotation":
				out.Path = PathRotation
			case "scale":
				out.Path = PathScale
			case "weights":
				out.Path, out.Width = PathWeights, 1
			default:
				continue
			}
			switch s.Interpolation {
			case "STEP":
				out.Interp = Step
			case "CUBICSPLINE":
				out.Interp = CubicSpline
			}
			c.Channels = append(c.Channels, out)
		}
		a.Clips = append(a.Clips, c)
	}
	return nil
}
