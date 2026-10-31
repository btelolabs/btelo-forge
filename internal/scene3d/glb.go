package scene3d

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"os"
	"slices"
)

// Encode writes the asset as a .glb. What the file held that is not understood but belongs to a material or a texture,
// such as an extension, is written back as it was.
func Encode(a *Asset) ([]byte, error) {
	w := &glbWriter{a: a, textures: map[textureKey]int{}}
	js, err := json.Marshal(w.document())
	if err != nil {
		return nil, err
	}
	for len(js)%4 != 0 {
		js = append(js, ' ')
	}
	for w.bin.Len()%4 != 0 {
		w.bin.WriteByte(0)
	}
	var out bytes.Buffer
	head := []uint32{glbMagic, 2, uint32(12 + 8 + len(js) + 8 + w.bin.Len()), uint32(len(js)), chunkJSON}
	binary.Write(&out, binary.LittleEndian, head)
	out.Write(js)
	binary.Write(&out, binary.LittleEndian, []uint32{uint32(w.bin.Len()), chunkBIN})
	out.Write(w.bin.Bytes())
	return out.Bytes(), nil
}

// Save writes the asset to a .glb file, through a temporary file so that a failure leaves what was there.
func Save(a *Asset, path string) error {
	data, err := Encode(a)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type textureKey struct{ image, sampler int }

type glbWriter struct {
	a         *Asset
	bin       bytes.Buffer
	views     []map[string]any
	accessors []map[string]any
	textures  map[textureKey]int
	texList   []map[string]any
}

const (
	targetVertices = 34962
	targetIndices  = 34963
)

// view adds data to the binary chunk, aligned for any number in it.
func (w *glbWriter) view(data []byte, target int) int {
	for w.bin.Len()%4 != 0 {
		w.bin.WriteByte(0)
	}
	v := map[string]any{"buffer": 0, "byteOffset": w.bin.Len(), "byteLength": len(data)}
	if target != 0 {
		v["target"] = target
	}
	w.bin.Write(data)
	w.views = append(w.views, v)
	return len(w.views) - 1
}

func raw[T any](s []T) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, s)
	return b.Bytes()
}

// floats adds an accessor of floats with width numbers for each item, of the given type.
func (w *glbWriter) floats(values []float32, kind string, width, target int, bounds bool) int {
	acc := map[string]any{"bufferView": w.view(raw(values), target), "componentType": gltfFloat, "count": len(values) / width, "type": kind}
	if bounds && len(values) > 0 {
		lo, hi := make([]float32, width), make([]float32, width)
		copy(lo, values)
		copy(hi, values)
		for i := 0; i < len(values); i += width {
			for c := range width {
				lo[c], hi[c] = min(lo[c], values[i+c]), max(hi[c], values[i+c])
			}
		}
		acc["min"], acc["max"] = lo, hi
	}
	w.accessors = append(w.accessors, acc)
	return len(w.accessors) - 1
}

func flat2(s []Vec2) []float32 {
	return slices.Concat(sliceOf(s, func(v Vec2) []float32 { return v[:] })...)
}
func flat3(s []Vec3) []float32 {
	return slices.Concat(sliceOf(s, func(v Vec3) []float32 { return v[:] })...)
}
func flat4(s []Vec4) []float32 {
	return slices.Concat(sliceOf(s, func(v Vec4) []float32 { return v[:] })...)
}

func sliceOf[T any](s []T, f func(T) []float32) [][]float32 {
	out := make([][]float32, len(s))
	for i, v := range s {
		out[i] = f(v)
	}
	return out
}

// worthWriting reports whether a list has anything apart from a value that would be assumed anyway.
func worthWriting[T comparable](s []T, assumed T) bool {
	return slices.ContainsFunc(s, func(v T) bool { return v != assumed })
}

func (w *glbWriter) prim(p *Prim) map[string]any {
	attrs := map[string]any{
		"POSITION": w.floats(flat3(p.Pos), "VEC3", 3, targetVertices, true),
		"NORMAL":   w.floats(flat3(p.Normal), "VEC3", 3, targetVertices, false),
	}
	// A tangent with no direction means the mesh was edited and has none to give: glTF allows none at all, not some.
	if p.Tangent != nil && !slices.ContainsFunc(p.Tangent, func(t Vec4) bool { return t[3] == 0 }) {
		attrs["TANGENT"] = w.floats(flat4(p.Tangent), "VEC4", 4, targetVertices, false)
	}
	if p.UV != nil {
		attrs["TEXCOORD_0"] = w.floats(flat2(p.UV), "VEC2", 2, targetVertices, false)
	}
	if p.UV2 != nil && worthWriting(p.UV2, Vec2{}) {
		attrs["TEXCOORD_1"] = w.floats(flat2(p.UV2), "VEC2", 2, targetVertices, false)
	}
	if p.Color != nil && worthWriting(p.Color, Vec4{1, 1, 1, 1}) {
		attrs["COLOR_0"] = w.floats(flat4(p.Color), "VEC4", 4, targetVertices, false)
	}
	if p.Joints != nil {
		w.accessors = append(w.accessors, map[string]any{"bufferView": w.view(raw(p.Joints), targetVertices), "componentType": gltfUshort, "count": len(p.Joints), "type": "VEC4"})
		attrs["JOINTS_0"] = len(w.accessors) - 1
		attrs["WEIGHTS_0"] = w.floats(flat4(p.Weights), "VEC4", 4, targetVertices, false)
	}
	w.accessors = append(w.accessors, map[string]any{"bufferView": w.view(raw(p.Indices), targetIndices), "componentType": gltfUint, "count": len(p.Indices), "type": "SCALAR"})
	out := map[string]any{"attributes": attrs, "indices": len(w.accessors) - 1}
	if p.Material >= 0 {
		out["material"] = p.Material
	}
	if len(p.Morphs) > 0 {
		targets := make([]map[string]any, len(p.Morphs))
		for i := range p.Morphs {
			m := &p.Morphs[i]
			targets[i] = map[string]any{"POSITION": w.floats(flat3(m.Pos), "VEC3", 3, targetVertices, true)}
			if m.Normal != nil {
				targets[i]["NORMAL"] = w.floats(flat3(m.Normal), "VEC3", 3, targetVertices, false)
			}
		}
		out["targets"] = targets
	}
	return out
}

// texture is a material's reference to a picture, or nil for none.
func (w *glbWriter) texture(t TexRef, extra map[string]any) map[string]any {
	if t.Image < 0 {
		return nil
	}
	k := textureKey{t.Image, t.Sampler}
	at, ok := w.textures[k]
	if !ok {
		at = len(w.texList)
		x := map[string]any{"source": t.Image}
		if t.Sampler >= 0 {
			x["sampler"] = t.Sampler
		}
		w.textures[k] = at
		w.texList = append(w.texList, x)
	}
	out := map[string]any{"index": at}
	if t.UV != 0 {
		out["texCoord"] = t.UV
	}
	if t.Extensions != nil {
		out["extensions"] = t.Extensions
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func (w *glbWriter) material(m *Material) map[string]any {
	pbr := map[string]any{"baseColorFactor": m.BaseColor, "metallicFactor": m.Metallic, "roughnessFactor": m.Roughness}
	if t := w.texture(m.BaseTex, nil); t != nil {
		pbr["baseColorTexture"] = t
	}
	if t := w.texture(m.MetalRoughTex, nil); t != nil {
		pbr["metallicRoughnessTexture"] = t
	}
	out := map[string]any{"pbrMetallicRoughness": pbr}
	if m.Name != "" {
		out["name"] = m.Name
	}
	if m.DoubleSided {
		out["doubleSided"] = true
	}
	if m.Emissive != (Vec3{}) {
		out["emissiveFactor"] = m.Emissive
	}
	if t := w.texture(m.NormalTex, map[string]any{"scale": m.NormalScale}); t != nil {
		out["normalTexture"] = t
	}
	if t := w.texture(m.OcclusionTex, map[string]any{"strength": m.Occlusion}); t != nil {
		out["occlusionTexture"] = t
	}
	if t := w.texture(m.EmissiveTex, nil); t != nil {
		out["emissiveTexture"] = t
	}
	switch m.AlphaMode {
	case AlphaMask:
		out["alphaMode"], out["alphaCutoff"] = "MASK", m.AlphaCutoff
	case AlphaBlend:
		out["alphaMode"] = "BLEND"
	}
	if m.Extensions != nil {
		out["extensions"] = m.Extensions
	}
	return out
}

func (w *glbWriter) node(n *Node) map[string]any {
	out := map[string]any{}
	if n.Name != "" {
		out["name"] = n.Name
	}
	if len(n.Children) > 0 {
		out["children"] = n.Children
	}
	if n.Translation != (Vec3{}) {
		out["translation"] = n.Translation
	}
	if n.Rotation != (Quat{0, 0, 0, 1}) {
		out["rotation"] = n.Rotation
	}
	if n.Scale != (Vec3{1, 1, 1}) {
		out["scale"] = n.Scale
	}
	if n.Mesh >= 0 {
		out["mesh"] = n.Mesh
	}
	if n.Skin >= 0 {
		out["skin"] = n.Skin
	}
	if n.Weights != nil {
		out["weights"] = n.Weights
	}
	return out
}

func (w *glbWriter) clip(c *Clip) map[string]any {
	paths := map[Path]string{PathTranslation: "translation", PathRotation: "rotation", PathScale: "scale", PathWeights: "weights"}
	interp := map[Interpolation]string{Linear: "LINEAR", Step: "STEP", CubicSpline: "CUBICSPLINE"}
	kinds := map[int]string{1: "SCALAR", 3: "VEC3", 4: "VEC4"}
	var channels, samplers []map[string]any
	for i := range c.Channels {
		ch := &c.Channels[i]
		kind, ok := kinds[ch.Width]
		if !ok {
			continue
		}
		samplers = append(samplers, map[string]any{"input": w.floats(ch.Times, "SCALAR", 1, 0, true), "output": w.floats(ch.Values, kind, ch.Width, 0, false), "interpolation": interp[ch.Interp]})
		channels = append(channels, map[string]any{"sampler": len(samplers) - 1, "target": map[string]any{"node": ch.Node, "path": paths[ch.Path]}})
	}
	out := map[string]any{"channels": channels, "samplers": samplers}
	if c.Name != "" {
		out["name"] = c.Name
	}
	return out
}

func listOf[T any](s []T, f func(*T) map[string]any) []map[string]any {
	out := make([]map[string]any, len(s))
	for i := range s {
		out[i] = f(&s[i])
	}
	return out
}

func (w *glbWriter) document() map[string]any {
	a := w.a
	doc := map[string]any{
		"asset":  map[string]any{"version": "2.0", "generator": "Btelo Forge"},
		"scene":  0,
		"scenes": []map[string]any{{"nodes": a.Roots}},
	}
	if a.Roots == nil {
		doc["scenes"] = []map[string]any{{"nodes": []int{}}}
	}
	put := func(name string, s []map[string]any) {
		if len(s) > 0 {
			doc[name] = s
		}
	}
	put("nodes", listOf(a.Nodes, w.node))
	put("meshes", listOf(a.Meshes, func(m *Mesh) map[string]any {
		out := map[string]any{"primitives": listOf(m.Prims, w.prim)}
		if m.Name != "" {
			out["name"] = m.Name
		}
		if len(m.Weights) > 0 {
			out["weights"] = m.Weights
		}
		if len(m.Keys) > 0 {
			out["extras"] = map[string]any{"targetNames": m.Keys}
		}
		return out
	}))
	put("materials", listOf(a.Materials, w.material))
	put("images", listOf(a.Images, func(im *Image) map[string]any {
		mime := im.Mime
		if mime == "" {
			mime = http.DetectContentType(im.Data)
		}
		out := map[string]any{"bufferView": w.view(im.Data, 0), "mimeType": mime}
		if im.Name != "" {
			out["name"] = im.Name
		}
		return out
	}))
	put("skins", listOf(a.Skins, func(s *Skin) map[string]any {
		values := make([]float32, 0, len(s.InverseBind)*16)
		for i := range s.InverseBind {
			values = append(values, s.InverseBind[i][:]...)
		}
		w.accessors = append(w.accessors, map[string]any{"bufferView": w.view(raw(values), 0), "componentType": gltfFloat, "count": len(s.InverseBind), "type": "MAT4"})
		out := map[string]any{"joints": s.Joints, "inverseBindMatrices": len(w.accessors) - 1}
		if s.Name != "" {
			out["name"] = s.Name
		}
		return out
	}))
	put("animations", listOf(a.Clips, w.clip))
	put("textures", w.texList)
	if len(a.Samplers) > 0 {
		doc["samplers"] = a.Samplers
	}
	if len(a.ExtensionsUsed) > 0 {
		doc["extensionsUsed"] = a.ExtensionsUsed
	}
	if len(a.ExtensionsRequired) > 0 {
		doc["extensionsRequired"] = a.ExtensionsRequired
	}
	put("accessors", w.accessors)
	put("bufferViews", w.views)
	if w.bin.Len() > 0 {
		doc["buffers"] = []map[string]any{{"byteLength": (w.bin.Len() + 3) &^ 3}}
	}
	return doc
}
