// Package gpu is the adapter between the 3D tool and a graphics API: the renderer speaks only these interfaces, with
// its shaders in WGSL, and each implementation lives in a package of its own (internal/gpu/wgpu is the first).
//
// It offers what the renderer uses and no more, as WebGPU defines it, so that an implementation can sit on WebGPU in a
// browser as well as directly on Vulkan.
package gpu

import (
	"context"
	"errors"
)

// ErrUnavailable is returned by an implementation's Open when the computer has no graphics card it can use.
var ErrUnavailable = errors.New("no usable graphics card")

// Info describes the card in use.
type Info struct {
	Name    string // the card, as its driver names it
	Backend string // the API under the implementation, such as Vulkan
}

// Limits are the sizes a device can handle.
type Limits struct {
	TextureSide uint32
}

// Format is the layout of a texture's pixels.
type Format int

const (
	RGBA8Unorm Format = iota
	RGBA8Srgb
	RGBA16Float
	Depth32Float
)

// BufferUsage says what a buffer may do.
type BufferUsage uint32

const (
	BufferVertex BufferUsage = 1 << iota
	BufferIndex
	BufferUniform
	BufferCopyDst
	BufferMapRead
	BufferStorage
	BufferCopySrc
)

// TextureUsage says what a texture may do.
type TextureUsage uint32

const (
	TextureSampled TextureUsage = 1 << iota
	TextureTarget
	TextureCopySrc
	TextureCopyDst
)

// BufferDesc describes a buffer.
type BufferDesc struct {
	Label string
	Size  uint64
	Usage BufferUsage
}

// TextureDesc describes a 2D texture; Mips and Samples of 0 mean 1.
type TextureDesc struct {
	Label         string
	Width, Height uint32
	Mips, Samples uint32
	Format        Format
	Usage         TextureUsage
}

// SamplerDesc describes a sampler.
type SamplerDesc struct {
	Label       string
	Repeat      bool // otherwise the edge is clamped
	RepeatU     bool // the first axis repeats alone, as a panorama's does
	Linear      bool // otherwise the nearest pixel
	Anisotropy  uint16
	CompareLess bool // compares with a reference value instead of filtering, for shadows
}

// Stage is where a binding is seen.
type Stage uint32

const (
	StageVertex Stage = 1 << iota
	StageFragment
	StageCompute
)

// BindKind is the kind of a binding.
type BindKind int

const (
	BindUniform BindKind = iota
	BindTexture
	BindDepthTexture
	BindSampler
	BindCompareSampler
	BindStorageRead // a buffer a shader reads
	BindStorage     // a buffer a shader writes
)

// BindEntry is one slot of a bind layout.
type BindEntry struct {
	Slot  uint32
	Stage Stage
	Kind  BindKind
}

// Binding fills one slot of a bind group: a buffer (Size bytes from its start), a view or a sampler.
type Binding struct {
	Slot    uint32
	Buffer  Buffer
	Size    uint64
	View    View
	Sampler Sampler
}

// AttrFormat is the type of a vertex attribute.
type AttrFormat int

const (
	Float2 AttrFormat = iota
	Float3
	Float4
)

// Attr is one attribute of a vertex.
type Attr struct {
	Location uint32
	Offset   uint64
	Format   AttrFormat
}

// Topology is how vertices make shapes.
type Topology int

const (
	Triangles Topology = iota
	Lines
)

// Cull is which faces are dropped.
type Cull int

const (
	CullNone Cull = iota
	CullBack
	CullFront
)

// Compare is the depth test.
type Compare int

const (
	CompareLess Compare = iota
	CompareLessEqual
	CompareAlways
)

// DepthState turns the depth test on for a pipeline.
type DepthState struct {
	Write       bool
	Compare     Compare
	Bias        int32
	SlopeBias   float32
	BiasClamped float32
}

// PipelineDesc describes a render pipeline. With no Fragment entry it draws on no colour target.
type PipelineDesc struct {
	Label    string
	Shader   Shader
	Vertex   string // the entry points
	Fragment string
	Layouts  []BindLayout
	Stride   uint64
	Attrs    []Attr
	Instance bool // the vertex buffer holds one entry for each instance, not for each vertex
	Topology Topology
	Cull     Cull
	Color    Format
	Blend    bool // over what is there, the source's alpha premultiplying nothing
	Depth    *DepthState
	Samples  uint32 // 0 means 1
}

// ComputeDesc describes a compute pipeline.
type ComputeDesc struct {
	Label   string
	Shader  Shader
	Entry   string
	Layouts []BindLayout
}

// ComputePipeline is the whole state of a dispatch.
type ComputePipeline interface{ Release() }

// ComputePass records dispatches.
type ComputePass interface {
	SetPipeline(ComputePipeline)
	SetBindGroup(index uint32, g BindGroup)
	Dispatch(x, y, z uint32)
	End() error
}

// ColorTarget is what a pass draws on: View, resolved into Resolve when multisampled. Clear of nil keeps what is there.
type ColorTarget struct {
	View    View
	Resolve View
	Clear   *[4]float64
}

// DepthTarget is the pass's depth, cleared to Clear unless Load carries on from what is there; Keep stores it for a
// later pass.
type DepthTarget struct {
	View  View
	Clear float32
	Load  bool
	Keep  bool
}

// PassDesc describes a render pass.
type PassDesc struct {
	Color *ColorTarget
	Depth *DepthTarget
}

// Buffer, Texture and the others are handles; Release frees what they hold, and a Device's Close frees the rest.
type Buffer interface{ Release() }

// Texture is an image on the card.
type Texture interface {
	// View is the whole texture, or its one level Mip when Mip is not negative.
	View(mip int) (View, error)
	Release()
}

// View is a way to read or draw on a texture.
type View interface{ Release() }

// Sampler says how a shader reads a texture.
type Sampler interface{ Release() }

// Shader is compiled WGSL.
type Shader interface{ Release() }

// BindLayout is the shape of a bind group.
type BindLayout interface{ Release() }

// BindGroup is the resources a shader sees.
type BindGroup interface{ Release() }

// Pipeline is the whole state of a draw.
type Pipeline interface{ Release() }

// Pass records draws.
type Pass interface {
	SetPipeline(Pipeline)
	SetBindGroup(index uint32, g BindGroup)
	SetVertexBuffer(Buffer)
	SetIndexBuffer(Buffer) // of 32-bit indices
	Draw(vertices uint32)
	DrawIndexed(indices, first uint32)
	DrawInstanced(vertices, instances uint32)
	End() error
}

// Encoder records work for the card. It is used once.
type Encoder interface {
	Pass(PassDesc) (Pass, error)
	Compute() (ComputePass, error)
	// CopyBuffer copies size bytes from the start of src to the start of dst.
	CopyBuffer(src, dst Buffer, size uint64)
	// CopyToBuffer copies a width×height texture to b, row after row, bytesPerRow apart; see RowPitch.
	CopyToBuffer(t Texture, b Buffer, bytesPerRow, width, height uint32)
	// CopyTexture copies the whole of src, width×height, to mip level mip of dst.
	CopyTexture(src, dst Texture, mip int, width, height uint32)
	Submit() error
}

// Device is a connection to a graphics card. It is used from one goroutine at a time.
type Device interface {
	Info() Info
	Limits() Limits
	Buffer(BufferDesc) (Buffer, error)
	Texture(TextureDesc) (Texture, error)
	Sampler(SamplerDesc) (Sampler, error)
	Shader(label, wgsl string) (Shader, error)
	BindLayout([]BindEntry) (BindLayout, error)
	BindGroup(BindLayout, []Binding) (BindGroup, error)
	Pipeline(PipelineDesc) (Pipeline, error)
	ComputePipeline(ComputeDesc) (ComputePipeline, error)
	Encoder() (Encoder, error)
	WriteBuffer(b Buffer, offset uint64, data []byte) error
	// WriteTexture fills mip level mip, of width×height pixels, from tightly packed data.
	WriteTexture(t Texture, mip int, data []byte, bytesPerRow, width, height uint32) error
	// WriteTextureRegion writes a part of level mip, width×height at (x, y), from data with rows bytesPerRow apart.
	WriteTextureRegion(t Texture, mip int, data []byte, bytesPerRow, x, y, width, height uint32) error
	// ReadBuffer copies a buffer made with BufferMapRead to dst, waiting for the card as needed.
	ReadBuffer(ctx context.Context, b Buffer, dst []byte) error
	// Idle waits until the card has finished everything submitted.
	Idle() error
	Close()
}

// RowPitch is the row length a texture copy needs for width pixels of 4 bytes.
func RowPitch(width uint32) uint32 { return (width*4 + 255) &^ 255 }
