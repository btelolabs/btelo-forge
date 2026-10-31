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
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/btelolabs/btelo-forge/internal/gpu"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

//go:embed filters.wgsl
var shaderSource string

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

// kit is what filters are made of on the device.
type kit struct {
	d                                 gpu.Device
	shader                            gpu.Shader
	layout                            gpu.BindLayout
	across, down, light, guide, toned gpu.ComputePipeline
	params                            gpu.Buffer
}

func newKit(d gpu.Device) (*kit, error) {
	k := &kit{d: d}
	var err error
	if k.shader, err = d.Shader("filters", shaderSource); err != nil {
		return nil, err
	}
	if k.layout, err = d.BindLayout([]gpu.BindEntry{{Slot: 0, Stage: gpu.StageCompute, Kind: gpu.BindUniform}, {Slot: 1, Stage: gpu.StageCompute, Kind: gpu.BindStorageRead},
		{Slot: 2, Stage: gpu.StageCompute, Kind: gpu.BindStorage}, {Slot: 3, Stage: gpu.StageCompute, Kind: gpu.BindStorageRead}}); err != nil {
		return nil, err
	}
	for _, p := range []struct {
		entry string
		to    *gpu.ComputePipeline
	}{{"across", &k.across}, {"down", &k.down}, {"light", &k.light}, {"guide", &k.guide}, {"tones", &k.toned}} {
		if *p.to, err = d.ComputePipeline(gpu.ComputeDesc{Label: p.entry, Shader: k.shader, Entry: p.entry, Layouts: []gpu.BindLayout{k.layout}}); err != nil {
			return nil, err
		}
	}
	k.params, err = d.Buffer(gpu.BufferDesc{Label: "filter params", Size: 80, Usage: gpu.BufferUniform | gpu.BufferCopyDst})
	return k, err
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

// job is the buffers of one filter: one picture's size each, and a place to read the result from.
type job struct {
	k       *kit
	w, h    int
	size    uint64
	buffers []gpu.Buffer
	groups  []gpu.BindGroup
	enc     gpu.Encoder
	pass    gpu.ComputePass
}

func (k *kit) newJob(w, h int) *job { return &job{k: k, w: w, h: h, size: uint64(w) * uint64(h) * 16} }

func (j *job) buffer(usage gpu.BufferUsage) (gpu.Buffer, error) {
	b, err := j.k.d.Buffer(gpu.BufferDesc{Label: "filter pixels", Size: j.size, Usage: usage})
	if err == nil {
		j.buffers = append(j.buffers, b)
	}
	return b, err
}

// release lets go of everything the job made.
func (j *job) release() {
	for _, g := range j.groups {
		g.Release()
	}
	for _, b := range j.buffers {
		b.Release()
	}
}

// step records one dispatch; lines is lines for a pipeline that does one a go, 0 for one by pixels.
func (j *job) step(pipe gpu.ComputePipeline, from, into, also gpu.Buffer, lines uint32) error {
	g, err := j.k.d.BindGroup(j.k.layout, []gpu.Binding{{Slot: 0, Buffer: j.k.params, Size: 80}, {Slot: 1, Buffer: from, Size: j.size},
		{Slot: 2, Buffer: into, Size: j.size}, {Slot: 3, Buffer: also, Size: j.size}})
	if err != nil {
		return err
	}
	j.groups = append(j.groups, g)
	if j.enc == nil {
		if j.enc, err = j.k.d.Encoder(); err != nil {
			return err
		}
	}
	p, err := j.enc.Compute()
	if err != nil {
		return err
	}
	p.SetPipeline(pipe)
	p.SetBindGroup(0, g)
	if lines > 0 {
		p.Dispatch(1, lines, 1)
	} else {
		p.Dispatch((uint32(j.w)*uint32(j.h)+63)/64, 1, 1)
	}
	return p.End()
}

// box records box blurs of a buffer into another, leaving the result in into.
func (j *job) box(from, into gpu.Buffer) error {
	if err := j.step(j.k.across, from, into, from, uint32(j.h)); err != nil {
		return err
	}
	if err := j.submit(); err != nil {
		return err
	}
	if err := j.step(j.k.down, into, from, from, uint32(j.w)); err != nil {
		return err
	}
	return j.submit()
}

// submit sends what is recorded; a buffer written by a pass is only read by the next once the card has it.
func (j *job) submit() error {
	if j.enc == nil {
		return nil
	}
	err := j.enc.Submit()
	j.enc = nil
	return err
}

// floats are the pixels of src as the shaders take them.
func floats(src *image.RGBA) []byte {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	out := make([]byte, w*h*16)
	eachRow(h, func(y int) {
		row := src.Pix[y*src.Stride:][:w*4]
		dst := out[y*w*16:][:w*16]
		for i, v := range row {
			binary.LittleEndian.PutUint32(dst[i*4:], math.Float32bits(float32(v)))
		}
	})
	return out
}

// eachRow calls fn for each of h rows, on all the processor's cores.
func eachRow(h int, fn func(y int)) {
	var next atomic.Int64
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for {
				y := int(next.Add(1)) - 1
				if y >= h {
					return
				}
				fn(y)
			}
		})
	}
	wg.Wait()
}

// read is a buffer as a picture of area, the part r of it, rounded as the processor's code rounds.
func (j *job) read(b gpu.Buffer, area, r image.Rectangle) (*image.RGBA, error) {
	staging, err := j.buffer(gpu.BufferMapRead | gpu.BufferCopyDst)
	if err != nil {
		return nil, err
	}
	if j.enc == nil {
		if j.enc, err = j.k.d.Encoder(); err != nil {
			return nil, err
		}
	}
	j.enc.CopyBuffer(b, staging, j.size)
	if err := j.submit(); err != nil {
		return nil, err
	}
	data := make([]byte, j.size)
	if err := j.k.d.ReadBuffer(context.Background(), staging, data); err != nil {
		return nil, err
	}
	out := image.NewRGBA(r)
	w := area.Dx()
	eachRow(r.Dy(), func(j int) {
		y := r.Min.Y + j
		row := data[((y-area.Min.Y)*w+r.Min.X-area.Min.X)*16:]
		dst := out.Pix[out.PixOffset(r.Min.X, y):][:r.Dx()*4]
		for i := range dst {
			v := math.Float32frombits(binary.LittleEndian.Uint32(row[i*4:]))
			dst[i] = uint8(min(max(v+0.5, 0), 255))
		}
	})
	return out, nil
}

func (k *kit) writeParams(values ...float64) error {
	data := make([]byte, 80)
	for i, v := range values {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(float32(v)))
	}
	return k.d.WriteBuffer(k.params, 0, data)
}

const storage = gpu.BufferStorage | gpu.BufferCopyDst | gpu.BufferCopySrc

// Blur does three box blurs each way, as paint's blur does, on as much of src as it reads.
func (c *Card) Blur(src *image.RGBA, r image.Rectangle, radius float64) (*image.RGBA, error) {
	return c.with(func(k *kit) (*image.RGBA, error) {
		area := r.Inset(-(int(math.Ceil(radius*3)) + 1)).Intersect(src.Rect)
		sub := src.SubImage(area).(*image.RGBA)
		j := k.newJob(area.Dx(), area.Dy())
		defer j.release()
		box := max(int(math.Round(radius/math.Sqrt(3))), 1)
		if err := k.writeParams(float64(j.w), float64(j.h), float64(box)); err != nil {
			return nil, err
		}
		a, err := j.buffer(storage)
		if err != nil {
			return nil, err
		}
		b, err := j.buffer(storage)
		if err != nil {
			return nil, err
		}
		if err := k.d.WriteBuffer(a, 0, floats(sub)); err != nil {
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
		pic, err := j.buffer(storage)
		if err != nil {
			return nil, err
		}
		a, err := j.buffer(storage)
		if err != nil {
			return nil, err
		}
		b, err := j.buffer(storage)
		if err != nil {
			return nil, err
		}
		out, err := j.buffer(storage)
		if err != nil {
			return nil, err
		}
		if err := k.d.WriteBuffer(pic, 0, floats(src)); err != nil {
			return nil, err
		}
		if t.Radius > 0 {
			steps := []func() error{
				func() error { return j.step(k.light, pic, a, pic, 0) },
				j.submit,
				func() error { return j.box(a, b) },
				func() error { return j.step(k.guide, a, b, pic, 0) },
				j.submit,
				func() error { return j.box(b, a) },
			}
			for _, s := range steps {
				if err := s(); err != nil {
					return nil, err
				}
			}
		}
		if err := j.step(k.toned, pic, out, b, 0); err != nil {
			return nil, err
		}
		if err := j.submit(); err != nil {
			return nil, err
		}
		return j.read(out, area, r)
	})
}
