// Package gpufx does the drawing tool's slowest filters on a graphics card, as paint.Accelerator. It holds no pictures
// between calls, and leaves the card alone while the 3D tool has it.
package gpufx

import (
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"math"
	"sync"
	"sync/atomic"

	"github.com/btelolabs/btelo-forge/internal/gpu"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

//go:embed filters.wgsl
var shaderSource string

// packShaderSource converts packed RGBA8 pixels (4 bytes/pixel) to/from vec4f on the GPU, avoiding CPU conversion and
// cutting host↔GPU bandwidth by 4×.
const packShaderSource = `
struct PackParams {
    dims: vec4u, // srcW, offX, offY, dstW
    tail: vec4u, // dstH, srcTotal, dstTotal, 0
}

@group(0) @binding(0) var<uniform> pp: PackParams;
@group(0) @binding(1) var<storage, read> packedIn: array<u32>;
@group(0) @binding(2) var<storage, read_write> floatOut: array<vec4f>;

@compute @workgroup_size(256)
fn unpack(@builtin(global_invocation_id) id: vec3u) {
    let i = id.x;
    if (i >= pp.tail.y) { return; }
    let v = packedIn[i];
    floatOut[i] = vec4f(f32(v & 255u), f32((v >> 8u) & 255u), f32((v >> 16u) & 255u), f32(v >> 24u));
}
`

const cropPackShaderSource = `
struct PackParams {
    dims: vec4u, // srcW, offX, offY, dstW
    tail: vec4u, // dstH, srcTotal, dstTotal, 0
}

@group(0) @binding(0) var<uniform> pp: PackParams;
@group(0) @binding(1) var<storage, read> floatIn: array<vec4f>;
@group(0) @binding(2) var<storage, read_write> packedOut: array<u32>;

@compute @workgroup_size(256)
fn pack(@builtin(global_invocation_id) id: vec3u) {
    let i = id.x;
    if (i >= pp.tail.z) { return; }
    let dx = i % pp.dims.w;
    let dy = i / pp.dims.w;
    let v = clamp(floatIn[(dy + pp.dims.z) * pp.dims.x + (dx + pp.dims.y)] + vec4f(0.5), vec4f(0.0), vec4f(255.0));
    let u = vec4u(v);
    packedOut[i] = u.r | (u.g << 8u) | (u.b << 16u) | (u.a << 24u);
}
`

// ErrBusy means the card is in use, and the work is better done on the processor than waited for.
var ErrBusy = errors.New("the graphics card is busy")

// Turn is whose turn it is on the card; a sync.Mutex is one.
type Turn interface {
	TryLock() bool
	Unlock()
}

// Card is the card, opened the first time a filter needs it. A card that fails is not asked again.
type Card struct {
	open   func() (gpu.Device, error)
	turn   Turn
	failed atomic.Bool
	done   atomic.Int64 // pieces it has worked out
	mu     sync.Mutex
	kit    *kit
}

// New is a Card reached through open and shared by turn.
func New(open func() (gpu.Device, error), turn Turn) *Card { return &Card{open: open, turn: turn} }

// Close releases the GPU pipelines and scratch buffers held by the card.
func (c *Card) Close() {
	c.mu.Lock()
	k := c.kit
	c.kit = nil
	c.mu.Unlock()
	if k != nil {
		k.release()
	}
}

// kit is what filters are made of on the device, along with reusable scratch buffers sized to the largest piece seen.
type kit struct {
	d                                 gpu.Device
	shader, unpackShader, packShader  gpu.Shader
	layout, packLayout                gpu.BindLayout
	across, down, light, guide, toned gpu.ComputePipeline
	unpackPipe, packPipe              gpu.ComputePipeline
	params, packParams                gpu.Buffer

	// Reusable per-piece buffers to avoid creating/destroying Metal/Vulkan buffers on every filter call or tile.
	floatCap uint64
	floats   [4]gpu.Buffer
	rawCap   uint64
	rawIn    gpu.Buffer
	rawOut   gpu.Buffer
	staging  gpu.Buffer
	upload   []byte
}

func newKit(d gpu.Device) (*kit, error) {
	k := &kit{d: d}
	var err error
	fail := func(err error) (*kit, error) {
		k.release()
		return nil, err
	}
	if k.shader, err = d.Shader("filters", shaderSource); err != nil {
		return fail(err)
	}
	if k.unpackShader, err = d.Shader("filters unpack", packShaderSource); err != nil {
		return fail(err)
	}
	if k.packShader, err = d.Shader("filters pack", cropPackShaderSource); err != nil {
		return fail(err)
	}
	if k.layout, err = d.BindLayout([]gpu.BindEntry{
		{Slot: 0, Stage: gpu.StageCompute, Kind: gpu.BindUniform},
		{Slot: 1, Stage: gpu.StageCompute, Kind: gpu.BindStorageRead},
		{Slot: 2, Stage: gpu.StageCompute, Kind: gpu.BindStorage},
		{Slot: 3, Stage: gpu.StageCompute, Kind: gpu.BindStorageRead},
	}); err != nil {
		return fail(err)
	}
	if k.packLayout, err = d.BindLayout([]gpu.BindEntry{
		{Slot: 0, Stage: gpu.StageCompute, Kind: gpu.BindUniform},
		{Slot: 1, Stage: gpu.StageCompute, Kind: gpu.BindStorageRead},
		{Slot: 2, Stage: gpu.StageCompute, Kind: gpu.BindStorage},
	}); err != nil {
		return fail(err)
	}
	for _, p := range []struct {
		entry string
		to    *gpu.ComputePipeline
	}{{"across", &k.across}, {"down", &k.down}, {"light", &k.light}, {"guide", &k.guide}, {"tones", &k.toned}} {
		if *p.to, err = d.ComputePipeline(gpu.ComputeDesc{Label: p.entry, Shader: k.shader, Entry: p.entry, Layouts: []gpu.BindLayout{k.layout}}); err != nil {
			return fail(err)
		}
	}
	if k.unpackPipe, err = d.ComputePipeline(gpu.ComputeDesc{Label: "unpack", Shader: k.unpackShader, Entry: "unpack", Layouts: []gpu.BindLayout{k.packLayout}}); err != nil {
		return fail(err)
	}
	if k.packPipe, err = d.ComputePipeline(gpu.ComputeDesc{Label: "pack", Shader: k.packShader, Entry: "pack", Layouts: []gpu.BindLayout{k.packLayout}}); err != nil {
		return fail(err)
	}
	if k.params, err = d.Buffer(gpu.BufferDesc{Label: "filter params", Size: 80, Usage: gpu.BufferUniform | gpu.BufferCopyDst}); err != nil {
		return fail(err)
	}
	if k.packParams, err = d.Buffer(gpu.BufferDesc{Label: "filter pack params", Size: 32, Usage: gpu.BufferUniform | gpu.BufferCopyDst}); err != nil {
		return fail(err)
	}
	return k, nil
}

func (k *kit) releaseScratch() {
	for i, b := range k.floats {
		if b != nil {
			b.Release()
			k.floats[i] = nil
		}
	}
	k.floatCap = 0
	for _, p := range []*gpu.Buffer{&k.rawIn, &k.rawOut, &k.staging} {
		if *p != nil {
			(*p).Release()
			*p = nil
		}
	}
	k.rawCap = 0
}

func (k *kit) release() {
	if k.d != nil {
		_ = k.d.Idle()
	}
	k.releaseScratch()
	for _, r := range []interface{ Release() }{
		k.params, k.packParams,
		k.across, k.down, k.light, k.guide, k.toned, k.unpackPipe, k.packPipe,
		k.layout, k.packLayout,
		k.shader, k.unpackShader, k.packShader,
	} {
		if r != nil {
			r.Release()
		}
	}
}

const storage = gpu.BufferStorage | gpu.BufferCopyDst | gpu.BufferCopySrc

// ensureScratch ensures at least count float buffers of floatSize bytes and packed buffers of rawSize bytes exist.
func (k *kit) ensureScratch(count int, floatSize, rawSize uint64) error {
	needGrowFloat := floatSize > k.floatCap
	needGrowRaw := rawSize > k.rawCap
	if needGrowFloat || needGrowRaw {
		_ = k.d.Idle()
	}
	if needGrowFloat {
		for i, b := range k.floats {
			if b != nil {
				b.Release()
				k.floats[i] = nil
			}
		}
		k.floatCap = floatSize
	}
	for i := range count {
		if k.floats[i] == nil {
			b, err := k.d.Buffer(gpu.BufferDesc{Label: "filter pixels", Size: k.floatCap, Usage: storage})
			if err != nil {
				return err
			}
			k.floats[i] = b
		}
	}
	if needGrowRaw {
		for _, p := range []*gpu.Buffer{&k.rawIn, &k.rawOut, &k.staging} {
			if *p != nil {
				(*p).Release()
				*p = nil
			}
		}
		k.rawCap = rawSize
	}
	if k.rawIn == nil {
		b, err := k.d.Buffer(gpu.BufferDesc{Label: "filter raw in", Size: k.rawCap, Usage: gpu.BufferStorage | gpu.BufferCopyDst})
		if err != nil {
			return err
		}
		k.rawIn = b
	}
	if k.rawOut == nil {
		b, err := k.d.Buffer(gpu.BufferDesc{Label: "filter raw out", Size: k.rawCap, Usage: gpu.BufferStorage | gpu.BufferCopySrc})
		if err != nil {
			return err
		}
		k.rawOut = b
	}
	if k.staging == nil {
		b, err := k.d.Buffer(gpu.BufferDesc{Label: "filter staging", Size: k.rawCap, Usage: gpu.BufferMapRead | gpu.BufferCopyDst})
		if err != nil {
			return err
		}
		k.staging = b
	}
	return nil
}

// with runs fn on the card when it is free and works.
func (c *Card) with(fn func(k *kit) (*image.RGBA, error)) (out *image.RGBA, err error) {
	if c.failed.Load() {
		return nil, gpu.ErrUnavailable
	}
	if !c.turn.TryLock() {
		return nil, ErrBusy
	}
	defer c.turn.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	// A driver that panics is a card not to use again, not a reason to lose the picture.
	defer func() {
		if r := recover(); r != nil {
			c.failed.Store(true)
			out, err = nil, fmt.Errorf("the graphics card failed: %v", r)
		}
	}()
	if c.kit == nil {
		d, err := c.open()
		if err == nil {
			c.kit, err = newKit(d)
		}
		if err != nil {
			c.failed.Store(true)
			return nil, err
		}
	}
	out, err = fn(c.kit)
	if err != nil {
		c.failed.Store(true)
		return nil, err
	}
	c.done.Add(1)
	return out, nil
}

// job records all compute passes and the final readback copy for one filter into a single submission.
type job struct {
	k       *kit
	w, h    int
	size    uint64 // w * h * 16 (float buffer size)
	rawSize uint64 // w * h * 4 (packed input size)
	groups  []gpu.BindGroup
	enc     gpu.Encoder
}

func (k *kit) newJob(w, h int) *job {
	px := uint64(w) * uint64(h)
	return &job{k: k, w: w, h: h, size: px * 16, rawSize: px * 4}
}

// release lets go of the bind groups created for this job.
func (j *job) release() {
	for _, g := range j.groups {
		g.Release()
	}
}

func (j *job) encoder() (gpu.Encoder, error) {
	if j.enc == nil {
		var err error
		if j.enc, err = j.k.d.Encoder(); err != nil {
			return nil, err
		}
	}
	return j.enc, nil
}

// unpack encodes unpacking packed RGBA8 pixels from k.rawIn into dst float buffer.
func (j *job) unpack(dst gpu.Buffer) error {
	g, err := j.k.d.BindGroup(j.k.packLayout, []gpu.Binding{
		{Slot: 0, Buffer: j.k.packParams, Size: 32},
		{Slot: 1, Buffer: j.k.rawIn, Size: j.rawSize},
		{Slot: 2, Buffer: dst, Size: j.size},
	})
	if err != nil {
		return err
	}
	j.groups = append(j.groups, g)
	enc, err := j.encoder()
	if err != nil {
		return err
	}
	p, err := enc.Compute()
	if err != nil {
		return err
	}
	p.SetPipeline(j.k.unpackPipe)
	p.SetBindGroup(0, g)
	p.Dispatch((uint32(j.w)*uint32(j.h)+255)/256, 1, 1)
	return p.End()
}

// step records one filter dispatch; lines is row/column count for line shaders, 0 for per-pixel shaders.
func (j *job) step(pipe gpu.ComputePipeline, from, into, also gpu.Buffer, lines uint32) error {
	g, err := j.k.d.BindGroup(j.k.layout, []gpu.Binding{
		{Slot: 0, Buffer: j.k.params, Size: 80},
		{Slot: 1, Buffer: from, Size: j.size},
		{Slot: 2, Buffer: into, Size: j.size},
		{Slot: 3, Buffer: also, Size: j.size},
	})
	if err != nil {
		return err
	}
	j.groups = append(j.groups, g)
	enc, err := j.encoder()
	if err != nil {
		return err
	}
	p, err := enc.Compute()
	if err != nil {
		return err
	}
	p.SetPipeline(pipe)
	p.SetBindGroup(0, g)
	if lines > 0 {
		p.Dispatch((lines+63)/64, 1, 1)
	} else {
		p.Dispatch((uint32(j.w)*uint32(j.h)+255)/256, 1, 1)
	}
	return p.End()
}

// box records horizontal and vertical box blur passes (leaving the result in from).
func (j *job) box(from, into gpu.Buffer) error {
	if err := j.step(j.k.across, from, into, from, uint32(j.h)); err != nil {
		return err
	}
	return j.step(j.k.down, into, from, from, uint32(j.w))
}

// uploadPacked uploads RGBA8 pixels tightly packed into k.rawIn without CPU float conversion.
func (k *kit) uploadPacked(src *image.RGBA) error {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	n := w * h * 4
	var data []byte
	if src.Stride == w*4 && len(src.Pix) >= n {
		data = src.Pix[:n]
	} else {
		if cap(k.upload) < n {
			k.upload = make([]byte, n)
		}
		data = k.upload[:n]
		for y := range h {
			copy(data[y*w*4:(y+1)*w*4], src.Pix[y*src.Stride:y*src.Stride+w*4])
		}
	}
	return k.d.WriteBuffer(k.rawIn, 0, data)
}

func (k *kit) writePackParams(srcW, offX, offY, dstW, dstH, srcTotal, dstTotal uint32) error {
	var data [32]byte
	vals := [8]uint32{srcW, offX, offY, dstW, dstH, srcTotal, dstTotal, 0}
	for i, v := range vals {
		binary.LittleEndian.PutUint32(data[i*4:], v)
	}
	return k.d.WriteBuffer(k.packParams, 0, data[:])
}

// read packs the cropped rectangle r of b into RGBA8 on the GPU, submits all recorded passes in one batch, and reads
// the result straight into the output image's Pix buffer.
func (j *job) read(b gpu.Buffer, area, r image.Rectangle) (*image.RGBA, error) {
	rw, rh := uint32(r.Dx()), uint32(r.Dy())
	outBytes := uint64(rw) * uint64(rh) * 4
	g, err := j.k.d.BindGroup(j.k.packLayout, []gpu.Binding{
		{Slot: 0, Buffer: j.k.packParams, Size: 32},
		{Slot: 1, Buffer: b, Size: j.size},
		{Slot: 2, Buffer: j.k.rawOut, Size: outBytes},
	})
	if err != nil {
		return nil, err
	}
	j.groups = append(j.groups, g)
	enc, err := j.encoder()
	if err != nil {
		return nil, err
	}
	p, err := enc.Compute()
	if err != nil {
		return nil, err
	}
	p.SetPipeline(j.k.packPipe)
	p.SetBindGroup(0, g)
	p.Dispatch((rw*rh+255)/256, 1, 1)
	if err := p.End(); err != nil {
		return nil, err
	}
	enc.CopyBuffer(j.k.rawOut, j.k.staging, outBytes)
	if err := enc.Submit(); err != nil {
		return nil, err
	}
	j.enc = nil
	out := image.NewRGBA(r)
	if err := j.k.d.ReadBuffer(context.Background(), j.k.staging, out.Pix); err != nil {
		return nil, err
	}
	return out, nil
}

func (k *kit) writeParams(values ...float64) error {
	var data [80]byte
	for i, v := range values {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(float32(v)))
	}
	return k.d.WriteBuffer(k.params, 0, data[:])
}

// Blur does three box blurs each way, as paint's blur does, on as much of src as it reads.
func (c *Card) Blur(src *image.RGBA, r image.Rectangle, radius float64) (*image.RGBA, error) {
	return c.with(func(k *kit) (*image.RGBA, error) {
		area := r.Inset(-(int(math.Ceil(radius*3)) + 1)).Intersect(src.Rect)
		sub := src.SubImage(area).(*image.RGBA)
		j := k.newJob(area.Dx(), area.Dy())
		defer j.release()
		if err := k.ensureScratch(2, j.size, j.rawSize); err != nil {
			return nil, err
		}
		box := max(int(math.Round(radius/math.Sqrt(3))), 1)
		if err := k.writeParams(float64(j.w), float64(j.h), float64(box)); err != nil {
			return nil, err
		}
		if err := k.uploadPacked(sub); err != nil {
			return nil, err
		}
		a, b := k.floats[0], k.floats[1]
		if err := k.writePackParams(uint32(j.w), uint32(r.Min.X-area.Min.X), uint32(r.Min.Y-area.Min.Y),
			uint32(r.Dx()), uint32(r.Dy()), uint32(j.w*j.h), uint32(r.Dx()*r.Dy())); err != nil {
			return nil, err
		}
		if err := j.unpack(a); err != nil {
			return nil, err
		}
		for range 3 {
			if err := j.box(a, b); err != nil {
				return nil, err
			}
		}
		return j.read(a, area, r)
	})
}

// Tones does Shadows/Highlights or Develop.
func (c *Card) Tones(src *image.RGBA, r image.Rectangle, t paint.ToneJob) (*image.RGBA, error) {
	return c.with(func(k *kit) (*image.RGBA, error) {
		area := src.Rect
		j := k.newJob(area.Dx(), area.Dy())
		defer j.release()
		if err := k.ensureScratch(4, j.size, j.rawSize); err != nil {
			return nil, err
		}
		on := func(b bool) float64 {
			if b {
				return 1
			}
			return 0
		}
		if err := k.writeParams(float64(j.w), float64(j.h), math.Ceil(t.Radius), on(t.Radius > 0),
			t.Shadows, t.Highlights, t.Clarity, on(t.Develop),
			t.Gain[0], t.Gain[1], t.Gain[2], t.Multiply,
			t.Black, t.Span, t.Steep, 0,
			t.Vibrance, t.Saturation, 0, 0); err != nil {
			return nil, err
		}
		if err := k.uploadPacked(src); err != nil {
			return nil, err
		}
		pic, a, b, out := k.floats[0], k.floats[1], k.floats[2], k.floats[3]
		if err := k.writePackParams(uint32(j.w), uint32(r.Min.X-area.Min.X), uint32(r.Min.Y-area.Min.Y),
			uint32(r.Dx()), uint32(r.Dy()), uint32(j.w*j.h), uint32(r.Dx()*r.Dy())); err != nil {
			return nil, err
		}
		if err := j.unpack(pic); err != nil {
			return nil, err
		}
		if t.Radius > 0 {
			if err := j.step(k.light, pic, a, pic, 0); err != nil {
				return nil, err
			}
			if err := j.box(a, b); err != nil {
				return nil, err
			}
			if err := j.step(k.guide, a, b, pic, 0); err != nil {
				return nil, err
			}
			if err := j.box(b, a); err != nil {
				return nil, err
			}
		}
		if err := j.step(k.toned, pic, out, b, 0); err != nil {
			return nil, err
		}
		return j.read(out, area, r)
	})
}
