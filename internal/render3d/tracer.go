package render3d

import (
	"context"
	_ "embed"
	"fmt"
	"image"
	"image/draw"
	"math"
	"time"
	"unsafe"

	"github.com/btelolabs/btelo-forge/internal/gpu"
	s3 "github.com/btelolabs/btelo-forge/internal/scene3d"
	xdraw "golang.org/x/image/draw"
)

//go:embed trace.wgsl
var traceWGSL string

const (
	traceSide    = 512  // a picture's side on the tracer, which reads them from a buffer
	traceMost    = 1024 // samples of a view, past which it stands
	traceBounces = 5
	traceBusy    = 120 * time.Millisecond // a band should take no longer, for the system not to take the card for stuck
	traceBands   = 32
	traceBinding = 128 << 20 // the most a shader can see of a buffer on any card
)

// traceKey is what a view's samples depend on.
type traceKey struct {
	pos        s3.Vec3
	yaw, pitch float32
	options    Options
	w, h       int
	scene      int
}

// tracer is what the path tracer holds on the card.
type tracer struct {
	layout                    gpu.BindLayout
	shader                    gpu.Shader
	pipe                      gpu.ComputePipeline
	params                    gpu.Buffer
	nodes, tris, mats, texels gpu.Buffer
	sums, shown               gpu.Buffer
	group                     gpu.BindGroup
	pictures                  []*image.NRGBA // on the card, by image, nil for those that could not be read
	materials                 []s3.Material
	surfaces                  []s3.Surface
	scene, built              int // what has changed, and what the buffers hold
	key                       traceKey
	sample, band, bands, w, h int
	failed                    error
}

// Samples is how many samples the traced view has so far, and whether it has stopped at the most it takes.
func (r *Renderer) Samples() (int, bool) { return r.trace.sample, r.trace.sample >= traceMost }

func (r *Renderer) prepareTracer() error {
	t := &r.trace
	var err error
	if t.layout, err = r.d.BindLayout([]gpu.BindEntry{
		{Slot: 0, Stage: gpu.StageCompute, Kind: gpu.BindUniform},
		{Slot: 1, Stage: gpu.StageCompute, Kind: gpu.BindStorageRead},
		{Slot: 2, Stage: gpu.StageCompute, Kind: gpu.BindStorageRead},
		{Slot: 3, Stage: gpu.StageCompute, Kind: gpu.BindStorageRead},
		{Slot: 4, Stage: gpu.StageCompute, Kind: gpu.BindStorageRead},
		{Slot: 5, Stage: gpu.StageCompute, Kind: gpu.BindStorage},
		{Slot: 6, Stage: gpu.StageCompute, Kind: gpu.BindStorage},
		{Slot: 7, Stage: gpu.StageCompute, Kind: gpu.BindTexture},
		{Slot: 8, Stage: gpu.StageCompute, Kind: gpu.BindSampler}}); err != nil {
		return err
	}
	if t.shader, err = r.d.Shader("trace", traceWGSL); err != nil {
		return fmt.Errorf("trace shader: %w", err)
	}
	if t.pipe, err = r.d.ComputePipeline(gpu.ComputeDesc{Label: "trace", Shader: t.shader, Entry: "main", Layouts: []gpu.BindLayout{t.layout}}); err != nil {
		return fmt.Errorf("trace pipeline: %w", err)
	}
	t.params, err = r.d.Buffer(gpu.BufferDesc{Label: "trace params", Size: 128, Usage: gpu.BufferUniform | gpu.BufferCopyDst})
	return err
}

func (t *tracer) close() {
	release(t.group, t.nodes, t.tris, t.mats, t.texels, t.sums, t.shown, t.params, t.pipe, t.shader, t.layout)
}

// traceTriangle is a triangle as the shader reads it.
type traceTriangle struct {
	p, n     [12]float32
	material [4]uint32
}

// traceMaterial is a material as the shader reads it.
type traceMaterial struct {
	base, emissive      [4]float32
	metallic, roughness float32
	picture, side       uint32
}

// buildTrace puts the shapes on the card, with a tree to find them by.
func (r *Renderer) buildTrace() error {
	t := &r.trace
	if err := r.d.Idle(); err != nil {
		return err
	}
	release(t.nodes, t.tris, t.mats, t.texels, t.group)
	t.nodes, t.tris, t.mats, t.texels, t.group = nil, nil, nil, nil, nil
	picker := s3.NewPicker(t.surfaces)
	nodes, order := picker.Flat()
	mats := make([]traceMaterial, 0, len(t.materials)+1)
	var texels []uint32
	placed := map[int]uint32{}
	for i := range t.materials {
		m := &t.materials[i]
		q := traceMaterial{base: [4]float32(m.BaseColor), metallic: m.Metallic, roughness: m.Roughness}
		q.emissive = [4]float32{m.Emissive[0], m.Emissive[1], m.Emissive[2], 0}
		if m.AlphaMode != s3.AlphaOpaque {
			q.emissive[3] = 1
		}
		if image := m.BaseTex.Image; image >= 0 && image < len(t.pictures) && t.pictures[image] != nil {
			at, ok := placed[image]
			if !ok {
				at = uint32(len(texels))
				placed[image] = at
				texels = append(texels, shrunk(t.pictures[image])...)
			}
			q.picture, q.side = at, traceSide
		}
		mats = append(mats, q)
	}
	fallback := s3.DefaultMaterial()
	mats = append(mats, traceMaterial{base: [4]float32(fallback.BaseColor), roughness: fallback.Roughness})
	tris := make([]traceTriangle, 0, len(order))
	for _, ref := range order {
		s := &t.surfaces[ref.Surface]
		var q traceTriangle
		for k := range 3 {
			v := s.Indices[int(ref.Triangle)*3+k]
			var uv s3.Vec2
			if s.UV != nil {
				uv = s.UV[v]
			}
			copy(q.p[k*4:], []float32{s.Pos[v][0], s.Pos[v][1], s.Pos[v][2], uv[0]})
			copy(q.n[k*4:], []float32{s.Normal[v][0], s.Normal[v][1], s.Normal[v][2], uv[1]})
		}
		q.material[0] = uint32(len(mats) - 1)
		if s.Material >= 0 && s.Material < len(t.materials) {
			q.material[0] = uint32(s.Material)
		}
		tris = append(tris, q)
	}
	if uint64(len(tris))*uint64(unsafe.Sizeof(traceTriangle{})) > traceBinding || uint64(len(texels))*4 > traceBinding {
		return fmt.Errorf("the scene is too big to trace: %d triangles", len(tris))
	}
	if len(nodes) == 0 { // a buffer cannot be empty, and the shader tells by its first node's box
		nodes = make([]s3.FlatNode, 1)
		nodes[0].Low, nodes[0].High = s3.Vec3{1, 1, 1}, s3.Vec3{0, 0, 0}
		tris = make([]traceTriangle, 1)
	}
	if len(texels) == 0 {
		texels = make([]uint32, 1)
	}
	var err error
	add := func(label string, data []byte) gpu.Buffer {
		var b gpu.Buffer
		if err == nil {
			b, err = r.buffer(label, gpu.BufferStorage, data)
		}
		return b
	}
	t.nodes = add("trace nodes", bytesOf(nodes))
	t.tris = add("trace triangles", bytesOf(tris))
	t.mats = add("trace materials", bytesOf(mats))
	t.texels = add("trace texels", bytesOf(texels))
	return err
}

// shrunk is a picture of traceSide by traceSide, as bytes held in numbers.
func shrunk(im *image.NRGBA) []uint32 {
	out := image.NewNRGBA(image.Rect(0, 0, traceSide, traceSide))
	if im.Rect.Dx() == traceSide && im.Rect.Dy() == traceSide {
		draw.Draw(out, out.Rect, im, image.Point{}, draw.Src)
	} else {
		xdraw.ApproxBiLinear.Scale(out, out.Rect, im, im.Rect, xdraw.Src, nil)
	}
	return unsafe.Slice((*uint32)(unsafe.Pointer(&out.Pix[0])), traceSide*traceSide)
}

func (r *Renderer) traceSize(w, h int) error {
	t := &r.trace
	if t.w == w && t.h == h && t.sums != nil {
		return nil
	}
	if err := r.d.Idle(); err != nil {
		return err
	}
	release(t.sums, t.shown, t.group)
	t.sums, t.shown, t.group = nil, nil, nil
	t.w, t.h = 0, 0
	var err error
	if t.sums, err = r.d.Buffer(gpu.BufferDesc{Label: "trace sums", Size: uint64(w*h) * 16, Usage: gpu.BufferStorage}); err != nil {
		return err
	}
	if t.shown, err = r.d.Buffer(gpu.BufferDesc{Label: "traced", Size: uint64(w*h) * 4, Usage: gpu.BufferStorage | gpu.BufferCopySrc}); err != nil {
		return err
	}
	t.w, t.h = w, h
	return nil
}

// Trace adds to the path traced picture of the view and shows it in out. It is the same view as Draw's, in light that has
// bounced, with soft shadows; a change to anything starts it again.
func (r *Renderer) Trace(cam *Camera, o Options, w, h int, out *image.RGBA) error {
	t := &r.trace
	if r.frameGroup == nil {
		return fmt.Errorf("no sky is set")
	}
	if err := r.resize(uint32(w), uint32(h)); err != nil {
		return err
	}
	if err := r.traceSize(w, h); err != nil {
		return err
	}
	if t.built != t.scene || t.nodes == nil {
		if err := r.buildTrace(); err != nil {
			return err
		}
		t.built = t.scene
	}
	if t.group == nil {
		var err error
		if t.group, err = r.d.BindGroup(t.layout, []gpu.Binding{{Slot: 0, Buffer: t.params, Size: 128}, {Slot: 1, Buffer: t.nodes}, {Slot: 2, Buffer: t.tris}, {Slot: 3, Buffer: t.mats},
			{Slot: 4, Buffer: t.texels}, {Slot: 5, Buffer: t.sums}, {Slot: 6, Buffer: t.shown}, {Slot: 7, View: r.skyView}, {Slot: 8, Sampler: r.skySampler}}); err != nil {
			return err
		}
	}
	key := traceKey{cam.Pos, cam.Yaw, cam.Pitch, o, w, h, t.scene}
	if key != t.key {
		t.key, t.sample, t.band = key, 0, 0
		t.bands = max(t.bands, 1)
	}
	if t.sample < traceMost {
		start := time.Now()
		if err := r.traceBand(cam, o, w, h); err != nil {
			return err
		}
		if time.Since(start) > traceBusy && t.bands < traceBands {
			t.bands *= 2
		}
	}
	return r.readTraced(w, h, out)
}

func (r *Renderer) traceBand(cam *Camera, o Options, w, h int) error {
	t := &r.trace
	right, up, ahead := cam.Axes()
	tall := float32(math.Tan(float64(cam.FovY) / 2))
	wide := tall * float32(w) / float32(h)
	dir := r.sunDir(o)
	size := float32(-1)
	if o.Shadows && r.sun.Intensity > 0 {
		size = float32(math.Sin(float64(max(r.sun.Size, .5)) * math.Pi / 360))
	}
	exposure := o.Exposure
	if exposure <= 0 {
		exposure = 1
	}
	s, c := sincos(o.SkyRotation)
	bounces := float32(traceBounces)
	if o.NoBounces {
		bounces = 0
	}
	high := max(h/t.bands, 1)
	row := t.band * high
	if row+high*2 > h {
		high = h - row
	}
	bounds := r.bounds
	if bounds.Empty() {
		bounds = s3.Box{Min: s3.Vec3{-1, 0, -1}, Max: s3.Vec3{1, 1, 1}}
	}
	ground, sky := float32(0), float32(0)
	if o.Grid || o.Shadows {
		ground = 1
	}
	if o.ShowSky {
		sky = 1
	}
	size2 := max(bounds.Size().Length(), 1e-4)
	reach := max(max(cam.Pos[1]-bounds.Min[1], size2*.05)*30, size2*6)
	sun := r.sun.Color
	params := []float32{cam.Pos[0], cam.Pos[1], cam.Pos[2], exposure,
		right[0] * wide, right[1] * wide, right[2] * wide, float32(w),
		up[0] * tall, up[1] * tall, up[2] * tall, float32(h),
		ahead[0], ahead[1], ahead[2], float32(t.sample),
		dir[0], dir[1], dir[2], size,
		sun[0] * r.sun.Intensity, sun[1] * r.sun.Intensity, sun[2] * r.sun.Intensity, bounces,
		c, s, sky, bounds.Min[1],
		float32(row), float32(high), ground, reach}
	if err := r.d.WriteBuffer(t.params, 0, bytesOf(params)); err != nil {
		return err
	}
	enc, err := r.d.Encoder()
	if err != nil {
		return err
	}
	p, err := enc.Compute()
	if err != nil {
		return err
	}
	p.SetPipeline(t.pipe)
	p.SetBindGroup(0, t.group)
	p.Dispatch(uint32(w+15)/16, uint32(high+15)/16, 1)
	if err := p.End(); err != nil {
		return err
	}
	if err := enc.Submit(); err != nil {
		return err
	}
	if row+high >= h {
		t.band = 0
		t.sample++
	} else {
		t.band++
	}
	return nil
}

func (r *Renderer) readTraced(w, h int, out *image.RGBA) error {
	t := &r.trace
	enc, err := r.d.Encoder()
	if err != nil {
		return err
	}
	size := uint64(w * h * 4)
	enc.CopyBuffer(t.shown, r.staging, size)
	if err := enc.Submit(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	if err := r.d.ReadBuffer(ctx, r.staging, r.row[:size]); err != nil {
		return err
	}
	for y := range h {
		copy(out.Pix[y*out.Stride:y*out.Stride+w*4], r.row[y*w*4:(y+1)*w*4])
	}
	return nil
}
