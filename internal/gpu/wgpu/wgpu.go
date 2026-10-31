// Package wgpu implements gpu.Device on WebGPU, through github.com/gogpu/wgpu: pure Go, on Vulkan, DirectX 12 or Metal.
package wgpu

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"

	"github.com/gogpu/gputypes"
	gogpu "github.com/gogpu/wgpu"

	"github.com/btelolabs/btelo-forge/internal/gpu"
)

// backends are tried in order, the fastest and best tested on each system first. On Windows that is Vulkan: on
// DirectX 12 the library rejects shaders that Vulkan runs.
func backends() []gputypes.Backends {
	switch runtime.GOOS {
	case "darwin":
		return []gputypes.Backends{gputypes.BackendsMetal}
	case "windows":
		return []gputypes.Backends{gputypes.BackendsVulkan, gputypes.BackendsDX12}
	}
	return []gputypes.Backends{gputypes.BackendsVulkan}
}

// Open connects to the best graphics card, or gpu.ErrUnavailable wrapped with why not.
func Open() (gpu.Device, error) {
	var last error
	for _, b := range backends() {
		d, err := open(b)
		if err == nil {
			return d, nil
		}
		last = err
	}
	return nil, fmt.Errorf("%w: %v", gpu.ErrUnavailable, last)
}

func open(b gputypes.Backends) (*device, error) {
	inst, err := gogpu.CreateInstance(&gogpu.InstanceDescriptor{Backends: b})
	if err != nil {
		return nil, err
	}
	ad, err := inst.RequestAdapter(&gogpu.RequestAdapterOptions{PowerPreference: gputypes.PowerPreferenceHighPerformance})
	if err != nil {
		inst.Release()
		return nil, err
	}
	info := ad.Info()
	if info.DeviceType == gputypes.DeviceTypeCPU {
		ad.Release()
		inst.Release()
		return nil, errors.New("only a software renderer was found")
	}
	dev, err := ad.RequestDevice(nil)
	if err != nil {
		ad.Release()
		inst.Release()
		return nil, err
	}
	return &device{inst: inst, ad: ad, dev: dev, q: dev.Queue(), info: gpu.Info{Name: info.Name, Backend: info.Backend.String()},
		limits: gpu.Limits{TextureSide: dev.Limits().MaxTextureDimension2D}}, nil
}

// handle turns what the library made into the adapter's interface, leaving it nil on failure rather than holding a nil
// pointer that compares as set.
func handle[I any, T any](v T, err error) (I, error) {
	var none I
	if err != nil {
		return none, err
	}
	return any(v).(I), nil
}

type device struct {
	inst   *gogpu.Instance
	ad     *gogpu.Adapter
	dev    *gogpu.Device
	q      *gogpu.Queue
	info   gpu.Info
	limits gpu.Limits
}

func (d *device) Info() gpu.Info     { return d.info }
func (d *device) Limits() gpu.Limits { return d.limits }

// Shared is the process's one connection to the card, opened at the first call. The library does not survive a device
// being closed and another opened in the same process: the driver crashes on a later draw, about one run in six. So a
// user of the card closes what it made and leaves this open; only one may use it at a time.
func Shared() (gpu.Device, error) {
	shared.Lock()
	defer shared.Unlock()
	if shared.d != nil {
		return shared.d, nil
	}
	d, err := Open()
	if err != nil {
		return nil, err
	}
	shared.d = d
	return d, nil
}

var shared struct {
	sync.Mutex
	d gpu.Device
}

func (d *device) Close() {
	d.Idle()
	d.dev.Release()
	d.ad.Release()
	d.inst.Release()
}

// Idle sends what is queued before waiting for the card, as the library holds written textures and buffers until the next
// submission, and what is released after this must not be among them.
func (d *device) Idle() error {
	e, err := d.dev.CreateCommandEncoder(nil)
	if err != nil {
		return err
	}
	cmd, err := e.Finish()
	if err != nil {
		return err
	}
	if _, err := d.q.Submit(cmd); err != nil {
		return err
	}
	return d.dev.WaitIdle()
}

func (d *device) Buffer(b gpu.BufferDesc) (gpu.Buffer, error) {
	var u gputypes.BufferUsage
	for _, m := range []struct {
		from gpu.BufferUsage
		to   gputypes.BufferUsage
	}{{gpu.BufferVertex, gogpu.BufferUsageVertex}, {gpu.BufferIndex, gogpu.BufferUsageIndex}, {gpu.BufferUniform, gogpu.BufferUsageUniform},
		{gpu.BufferCopyDst, gogpu.BufferUsageCopyDst}, {gpu.BufferMapRead, gogpu.BufferUsageMapRead},
		{gpu.BufferStorage, gogpu.BufferUsageStorage}, {gpu.BufferCopySrc, gogpu.BufferUsageCopySrc}} {
		if b.Usage&m.from != 0 {
			u |= m.to
		}
	}
	return handle[gpu.Buffer](d.dev.CreateBuffer(&gogpu.BufferDescriptor{Label: b.Label, Size: b.Size, Usage: u}))
}

func format(f gpu.Format) gputypes.TextureFormat {
	switch f {
	case gpu.RGBA8Srgb:
		return gputypes.TextureFormatRGBA8UnormSrgb
	case gpu.RGBA16Float:
		return gputypes.TextureFormatRGBA16Float
	case gpu.Depth32Float:
		return gputypes.TextureFormatDepth32Float
	}
	return gputypes.TextureFormatRGBA8Unorm
}

type texture struct {
	d *device
	t *gogpu.Texture
}

func (t *texture) Release() { t.t.Release() }

func (t *texture) View(mip int) (gpu.View, error) {
	if mip < 0 {
		return handle[gpu.View](t.d.dev.CreateTextureView(t.t, nil))
	}
	return handle[gpu.View](t.d.dev.CreateTextureView(t.t, &gogpu.TextureViewDescriptor{BaseMipLevel: uint32(mip), MipLevelCount: 1}))
}

func (d *device) Texture(t gpu.TextureDesc) (gpu.Texture, error) {
	var u gputypes.TextureUsage
	for _, m := range []struct {
		from gpu.TextureUsage
		to   gputypes.TextureUsage
	}{{gpu.TextureSampled, gputypes.TextureUsageTextureBinding}, {gpu.TextureTarget, gputypes.TextureUsageRenderAttachment},
		{gpu.TextureCopySrc, gputypes.TextureUsageCopySrc}, {gpu.TextureCopyDst, gputypes.TextureUsageCopyDst}} {
		if t.Usage&m.from != 0 {
			u |= m.to
		}
	}
	made, err := d.dev.CreateTexture(&gogpu.TextureDescriptor{Label: t.Label, Size: gogpu.Extent3D{Width: t.Width, Height: t.Height, DepthOrArrayLayers: 1},
		MipLevelCount: max(t.Mips, 1), SampleCount: max(t.Samples, 1), Dimension: gputypes.TextureDimension2D, Format: format(t.Format), Usage: u})
	if err != nil {
		return nil, err
	}
	return &texture{d, made}, nil
}

func (d *device) Sampler(s gpu.SamplerDesc) (gpu.Sampler, error) {
	address := func(repeat bool) gputypes.AddressMode {
		if repeat {
			return gputypes.AddressModeRepeat
		}
		return gputypes.AddressModeClampToEdge
	}
	filter := gputypes.FilterModeNearest
	if s.Linear {
		filter = gputypes.FilterModeLinear
	}
	desc := &gogpu.SamplerDescriptor{Label: s.Label, AddressModeU: address(s.Repeat || s.RepeatU), AddressModeV: address(s.Repeat), AddressModeW: gputypes.AddressModeClampToEdge,
		MagFilter: filter, MinFilter: filter, MipmapFilter: filter, LodMaxClamp: 32, Anisotropy: max(s.Anisotropy, 1)}
	if s.CompareLess {
		desc.Compare = gputypes.CompareFunctionLess
	}
	return handle[gpu.Sampler](d.dev.CreateSampler(desc))
}

func (d *device) Shader(label, wgsl string) (gpu.Shader, error) {
	return handle[gpu.Shader](d.dev.CreateShaderModule(&gogpu.ShaderModuleDescriptor{Label: label, WGSL: wgsl}))
}

func (d *device) BindLayout(entries []gpu.BindEntry) (gpu.BindLayout, error) {
	out := make([]gputypes.BindGroupLayoutEntry, len(entries))
	for i, e := range entries {
		var stages gputypes.ShaderStages
		if e.Stage&gpu.StageVertex != 0 {
			stages |= gogpu.ShaderStageVertex
		}
		if e.Stage&gpu.StageFragment != 0 {
			stages |= gogpu.ShaderStageFragment
		}
		if e.Stage&gpu.StageCompute != 0 {
			stages |= gogpu.ShaderStageCompute
		}
		o := gputypes.BindGroupLayoutEntry{Binding: e.Slot, Visibility: stages}
		switch e.Kind {
		case gpu.BindUniform:
			o.Buffer = &gputypes.BufferBindingLayout{Type: gputypes.BufferBindingTypeUniform}
		case gpu.BindTexture:
			o.Texture = &gputypes.TextureBindingLayout{SampleType: gputypes.TextureSampleTypeFloat, ViewDimension: gputypes.TextureViewDimension2D}
		case gpu.BindDepthTexture:
			o.Texture = &gputypes.TextureBindingLayout{SampleType: gputypes.TextureSampleTypeDepth, ViewDimension: gputypes.TextureViewDimension2D}
		case gpu.BindSampler:
			o.Sampler = &gputypes.SamplerBindingLayout{Type: gputypes.SamplerBindingTypeFiltering}
		case gpu.BindCompareSampler:
			o.Sampler = &gputypes.SamplerBindingLayout{Type: gputypes.SamplerBindingTypeComparison}
		case gpu.BindStorageRead:
			o.Buffer = &gputypes.BufferBindingLayout{Type: gputypes.BufferBindingTypeReadOnlyStorage}
		case gpu.BindStorage:
			o.Buffer = &gputypes.BufferBindingLayout{Type: gputypes.BufferBindingTypeStorage}
		}
		out[i] = o
	}
	return handle[gpu.BindLayout](d.dev.CreateBindGroupLayout(&gogpu.BindGroupLayoutDescriptor{Entries: out}))
}

func (d *device) BindGroup(l gpu.BindLayout, bindings []gpu.Binding) (gpu.BindGroup, error) {
	out := make([]gogpu.BindGroupEntry, len(bindings))
	for i, b := range bindings {
		e := gogpu.BindGroupEntry{Binding: b.Slot, Size: b.Size}
		if b.Buffer != nil {
			e.Buffer = b.Buffer.(*gogpu.Buffer)
		}
		if b.View != nil {
			e.TextureView = b.View.(*gogpu.TextureView)
		}
		if b.Sampler != nil {
			e.Sampler = b.Sampler.(*gogpu.Sampler)
		}
		out[i] = e
	}
	return handle[gpu.BindGroup](d.dev.CreateBindGroup(&gogpu.BindGroupDescriptor{Layout: l.(*gogpu.BindGroupLayout), Entries: out}))
}

func (d *device) Pipeline(p gpu.PipelineDesc) (gpu.Pipeline, error) {
	layouts := make([]*gogpu.BindGroupLayout, len(p.Layouts))
	for i, l := range p.Layouts {
		layouts[i] = l.(*gogpu.BindGroupLayout)
	}
	layout, err := d.dev.CreatePipelineLayout(&gogpu.PipelineLayoutDescriptor{Label: p.Label, BindGroupLayouts: layouts})
	if err != nil {
		return nil, err
	}
	defer layout.Release()
	shader := p.Shader.(*gogpu.ShaderModule)
	var attrs []gputypes.VertexAttribute
	for _, a := range p.Attrs {
		f := gputypes.VertexFormatFloat32x4
		switch a.Format {
		case gpu.Float2:
			f = gputypes.VertexFormatFloat32x2
		case gpu.Float3:
			f = gputypes.VertexFormatFloat32x3
		}
		attrs = append(attrs, gputypes.VertexAttribute{Format: f, Offset: a.Offset, ShaderLocation: a.Location})
	}
	desc := &gogpu.RenderPipelineDescriptor{Label: p.Label, Layout: layout,
		Vertex:      gogpu.VertexState{Module: shader, EntryPoint: p.Vertex},
		Multisample: gputypes.MultisampleState{Count: max(p.Samples, 1), Mask: 0xffffffff}}
	if p.Stride > 0 {
		step := gputypes.VertexStepModeVertex
		if p.Instance {
			step = gputypes.VertexStepModeInstance
		}
		desc.Vertex.Buffers = []gputypes.VertexBufferLayout{{ArrayStride: p.Stride, StepMode: step, Attributes: attrs}}
	}
	desc.Primitive.Topology = gputypes.PrimitiveTopologyTriangleList
	if p.Topology == gpu.Lines {
		desc.Primitive.Topology = gputypes.PrimitiveTopologyLineList
	}
	switch p.Cull {
	case gpu.CullBack:
		desc.Primitive.CullMode = gputypes.CullModeBack
	case gpu.CullFront:
		desc.Primitive.CullMode = gputypes.CullModeFront
	}
	if p.Fragment != "" {
		target := gputypes.ColorTargetState{Format: format(p.Color), WriteMask: gputypes.ColorWriteMaskAll}
		if p.Blend {
			target.Blend = &gputypes.BlendState{
				Color: gputypes.BlendComponent{SrcFactor: gputypes.BlendFactorSrcAlpha, DstFactor: gputypes.BlendFactorOneMinusSrcAlpha, Operation: gputypes.BlendOperationAdd},
				Alpha: gputypes.BlendComponent{SrcFactor: gputypes.BlendFactorOne, DstFactor: gputypes.BlendFactorOneMinusSrcAlpha, Operation: gputypes.BlendOperationAdd}}
		}
		desc.Fragment = &gogpu.FragmentState{Module: shader, EntryPoint: p.Fragment, Targets: []gputypes.ColorTargetState{target}}
	}
	if s := p.Depth; s != nil {
		compare := gputypes.CompareFunctionLess
		switch s.Compare {
		case gpu.CompareLessEqual:
			compare = gputypes.CompareFunctionLessEqual
		case gpu.CompareAlways:
			compare = gputypes.CompareFunctionAlways
		}
		desc.DepthStencil = &gogpu.DepthStencilState{Format: gputypes.TextureFormatDepth32Float, DepthWriteEnabled: s.Write, DepthCompare: compare,
			DepthBias: s.Bias, DepthBiasSlopeScale: s.SlopeBias, DepthBiasClamp: s.BiasClamped}
	}
	return handle[gpu.Pipeline](d.dev.CreateRenderPipeline(desc))
}

func (d *device) ComputePipeline(p gpu.ComputeDesc) (gpu.ComputePipeline, error) {
	layouts := make([]*gogpu.BindGroupLayout, len(p.Layouts))
	for i, l := range p.Layouts {
		layouts[i] = l.(*gogpu.BindGroupLayout)
	}
	layout, err := d.dev.CreatePipelineLayout(&gogpu.PipelineLayoutDescriptor{Label: p.Label, BindGroupLayouts: layouts})
	if err != nil {
		return nil, err
	}
	defer layout.Release()
	return handle[gpu.ComputePipeline](d.dev.CreateComputePipeline(&gogpu.ComputePipelineDescriptor{Label: p.Label, Layout: layout, Module: p.Shader.(*gogpu.ShaderModule), EntryPoint: p.Entry}))
}

func (d *device) WriteBuffer(b gpu.Buffer, offset uint64, data []byte) error {
	return d.q.WriteBuffer(b.(*gogpu.Buffer), offset, data)
}

func (d *device) WriteTexture(t gpu.Texture, mip int, data []byte, bytesPerRow, width, height uint32) error {
	return d.q.WriteTexture(&gogpu.ImageCopyTexture{Texture: t.(*texture).t, MipLevel: uint32(mip)}, data,
		&gogpu.ImageDataLayout{BytesPerRow: bytesPerRow, RowsPerImage: height}, &gogpu.Extent3D{Width: width, Height: height, DepthOrArrayLayers: 1})
}

func (d *device) WriteTextureRegion(t gpu.Texture, mip int, data []byte, bytesPerRow, x, y, width, height uint32) error {
	return d.q.WriteTexture(&gogpu.ImageCopyTexture{Texture: t.(*texture).t, MipLevel: uint32(mip), Origin: gogpu.Origin3D{X: x, Y: y}}, data,
		&gogpu.ImageDataLayout{BytesPerRow: bytesPerRow, RowsPerImage: height}, &gogpu.Extent3D{Width: width, Height: height, DepthOrArrayLayers: 1})
}

func (d *device) ReadBuffer(ctx context.Context, b gpu.Buffer, dst []byte) error {
	buf := b.(*gogpu.Buffer)
	size := uint64(len(dst))
	if err := buf.Map(ctx, gogpu.MapModeRead, 0, size); err != nil {
		return err
	}
	r, err := buf.MappedRange(0, size)
	if err != nil {
		buf.Unmap()
		return err
	}
	copy(dst, r.Bytes())
	return buf.Unmap()
}

func (d *device) Encoder() (gpu.Encoder, error) {
	e, err := d.dev.CreateCommandEncoder(nil)
	if err != nil {
		return nil, err
	}
	return &encoder{d: d, enc: e}, nil
}

// encoder records into one command encoder, or several: the library refuses a texture copied from in the encoder that
// drew on it, although WebGPU allows that, so a copy after a pass starts a new one.
type encoder struct {
	d      *device
	enc    *gogpu.CommandEncoder
	done   []*gogpu.CommandBuffer
	drawn  bool
	failed error
}

func (e *encoder) Pass(p gpu.PassDesc) (gpu.Pass, error) {
	if e.failed != nil {
		return nil, e.failed
	}
	var desc gogpu.RenderPassDescriptor
	if c := p.Color; c != nil {
		a := gogpu.RenderPassColorAttachment{View: c.View.(*gogpu.TextureView), LoadOp: gputypes.LoadOpLoad, StoreOp: gputypes.StoreOpStore}
		if c.Resolve != nil {
			a.ResolveTarget = c.Resolve.(*gogpu.TextureView)
			a.StoreOp = gputypes.StoreOpDiscard
		}
		if c.Clear != nil {
			a.LoadOp, a.ClearValue = gputypes.LoadOpClear, gputypes.Color{R: c.Clear[0], G: c.Clear[1], B: c.Clear[2], A: c.Clear[3]}
		}
		desc.ColorAttachments = []gogpu.RenderPassColorAttachment{a}
	}
	if t := p.Depth; t != nil {
		a := &gogpu.RenderPassDepthStencilAttachment{View: t.View.(*gogpu.TextureView), DepthLoadOp: gputypes.LoadOpClear, DepthStoreOp: gputypes.StoreOpDiscard, DepthClearValue: t.Clear}
		if t.Load {
			a.DepthLoadOp = gputypes.LoadOpLoad
		}
		if t.Keep {
			a.DepthStoreOp = gputypes.StoreOpStore
		}
		desc.DepthStencilAttachment = a
	}
	raw, err := e.enc.BeginRenderPass(&desc)
	if err != nil {
		e.failed = err
		return nil, err
	}
	e.drawn = true
	return &pass{raw}, nil
}

// fresh starts a new command encoder when the one in use has drawn; it reports whether recording can go on.
func (e *encoder) fresh() bool {
	if e.failed != nil {
		return false
	}
	if e.drawn {
		next, err := e.d.dev.CreateCommandEncoder(nil)
		if err != nil {
			e.failed = err
			return false
		}
		cmd, err := e.enc.Finish()
		if err != nil {
			e.failed = err
			return false
		}
		e.done, e.enc, e.drawn = append(e.done, cmd), next, false
	}
	return true
}

type computePass struct{ p *gogpu.ComputePassEncoder }

func (p *computePass) SetPipeline(l gpu.ComputePipeline) { p.p.SetPipeline(l.(*gogpu.ComputePipeline)) }
func (p *computePass) SetBindGroup(i uint32, g gpu.BindGroup) {
	p.p.SetBindGroup(i, g.(*gogpu.BindGroup), nil)
}
func (p *computePass) Dispatch(x, y, z uint32) { p.p.Dispatch(x, y, z) }
func (p *computePass) End() error              { return p.p.End() }

func (e *encoder) Compute() (gpu.ComputePass, error) {
	if !e.fresh() {
		return nil, e.failed
	}
	e.drawn = true
	p, err := e.enc.BeginComputePass(nil)
	if err != nil {
		return nil, err
	}
	return &computePass{p}, nil
}

func (e *encoder) CopyBuffer(src, dst gpu.Buffer, size uint64) {
	if e.fresh() {
		e.enc.CopyBufferToBuffer(src.(*gogpu.Buffer), 0, dst.(*gogpu.Buffer), 0, size)
	}
}

func (e *encoder) CopyTexture(src, dst gpu.Texture, mip int, width, height uint32) {
	if !e.fresh() {
		return
	}
	e.enc.CopyTextureToTexture(src.(*texture).t, dst.(*texture).t, []gogpu.TextureCopy{{
		Source:      gogpu.ImageCopyTexture{Texture: src.(*texture).t},
		Destination: gogpu.ImageCopyTexture{Texture: dst.(*texture).t, MipLevel: uint32(mip)},
		Size:        gogpu.Extent3D{Width: width, Height: height, DepthOrArrayLayers: 1}}})
}

func (e *encoder) CopyToBuffer(t gpu.Texture, b gpu.Buffer, bytesPerRow, width, height uint32) {
	if !e.fresh() {
		return
	}
	tex := t.(*texture).t
	e.enc.CopyTextureToBuffer(tex, b.(*gogpu.Buffer), []gogpu.BufferTextureCopy{{
		BufferLayout: gogpu.ImageDataLayout{BytesPerRow: bytesPerRow, RowsPerImage: height},
		TextureBase:  gogpu.ImageCopyTexture{Texture: tex},
		Size:         gogpu.Extent3D{Width: width, Height: height, DepthOrArrayLayers: 1}}})
}

func (e *encoder) Submit() error {
	if e.failed != nil {
		return e.failed
	}
	cmd, err := e.enc.Finish()
	if err != nil {
		return err
	}
	_, err = e.d.q.Submit(append(e.done, cmd)...)
	return err
}

type pass struct{ p *gogpu.RenderPassEncoder }

func (p *pass) SetPipeline(l gpu.Pipeline) { p.p.SetPipeline(l.(*gogpu.RenderPipeline)) }
func (p *pass) SetBindGroup(i uint32, g gpu.BindGroup) {
	p.p.SetBindGroup(i, g.(*gogpu.BindGroup), nil)
}
func (p *pass) SetVertexBuffer(b gpu.Buffer) { p.p.SetVertexBuffer(0, b.(*gogpu.Buffer), 0) }
func (p *pass) SetIndexBuffer(b gpu.Buffer) {
	p.p.SetIndexBuffer(b.(*gogpu.Buffer), gputypes.IndexFormatUint32, 0)
}
func (p *pass) Draw(n uint32) { p.p.Draw(gputypes.DrawArgs{VertexCount: n, InstanceCount: 1}) }
func (p *pass) DrawInstanced(n, instances uint32) {
	p.p.Draw(gputypes.DrawArgs{VertexCount: n, InstanceCount: instances})
}
func (p *pass) DrawIndexed(n, first uint32) {
	p.p.DrawIndexed(gputypes.DrawIndexedArgs{IndexCount: n, InstanceCount: 1, FirstIndex: first})
}
func (p *pass) End() error { return p.p.End() }
