// Package anim plays animated GIF and WebP files frame by frame, composing each frame onto the canvas the way
// browsers do. Frames are decoded as they are asked for, so a long animation costs one canvas of memory, not one
// per frame. ffmpeg cannot read an animated WebP, which is why this package exists.
package anim

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
	"image/gif"
	"io"
	"time"

	"golang.org/x/image/webp"
)

// ErrNotAnimated means the file is a still image, or not a GIF or WebP at all.
var ErrNotAnimated = errors.New("not an animated GIF or WebP")

// errCorrupt means the file claims to be an animation but its chunks do not add up.
var errCorrupt = errors.New("the animation is damaged")

// minDelay is the shortest a frame shows: browsers slow faster frames to this, so a file that asks for none plays
// at the speed people know it by.
const minDelay = 100 * time.Millisecond

// frame is where one frame sits on the canvas and how it is drawn.
type frame struct {
	rect    image.Rectangle
	delay   time.Duration
	blend   bool // drawn over what is there, rather than replacing it
	dispose bool // its rectangle is cleared before the next frame
	decode  func() (image.Image, error)
}

// Animation is an animated image ready to be played.
type Animation struct {
	W, H   int
	frames []frame
	canvas *image.NRGBA
	next   int  // the frame Next draws
	clear  bool // the previous frame's rectangle is cleared before the next is drawn
	prev   image.Rectangle
	length time.Duration
}

// Len is the number of frames.
func (a *Animation) Len() int { return len(a.frames) }

// Duration is how long one loop plays.
func (a *Animation) Duration() time.Duration { return a.length }

// Next draws the next frame, starting over after the last, and returns the canvas and how long it shows. The
// canvas is reused: copy it to keep a frame.
func (a *Animation) Next() (*image.NRGBA, time.Duration, error) {
	if a.next == 0 {
		clear(a.canvas.Pix)
		a.clear = false
	}
	f := a.frames[a.next]
	if a.clear {
		draw.Draw(a.canvas, a.prev, image.Transparent, image.Point{}, draw.Src)
	}
	img, err := f.decode()
	if err != nil {
		return nil, 0, err
	}
	op := draw.Src
	if f.blend {
		op = draw.Over
	}
	draw.Draw(a.canvas, f.rect, img, img.Bounds().Min, op)
	a.clear, a.prev = f.dispose, f.rect
	a.next = (a.next + 1) % len(a.frames)
	return a.canvas, f.delay, nil
}

// Rewind makes Next start again from the first frame.
func (a *Animation) Rewind() { a.next = 0 }

// Decode reads an animated GIF or WebP; a file with one frame is not an animation.
func Decode(data []byte) (*Animation, error) {
	var a *Animation
	var err error
	switch {
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		a, err = decodeWebP(data)
	case len(data) >= 6 && (string(data[:6]) == "GIF89a" || string(data[:6]) == "GIF87a"):
		a, err = decodeGIF(data)
	default:
		return nil, ErrNotAnimated
	}
	if err != nil {
		return nil, err
	}
	if len(a.frames) < 2 {
		return nil, ErrNotAnimated
	}
	if a.W <= 0 || a.H <= 0 || a.W*a.H > 1<<26 {
		return nil, errCorrupt
	}
	a.canvas = image.NewNRGBA(image.Rect(0, 0, a.W, a.H))
	for _, f := range a.frames {
		a.length += f.delay
	}
	return a, nil
}

func delay(d time.Duration) time.Duration {
	if d < 20*time.Millisecond {
		return minDelay
	}
	return d
}

// decodeGIF lays out a GIF's frames; they are already decoded by the standard library.
func decodeGIF(data []byte) (*Animation, error) {
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	a := &Animation{W: g.Config.Width, H: g.Config.Height}
	for i, p := range g.Image {
		f := frame{rect: p.Bounds(), delay: delay(time.Duration(g.Delay[i]) * 10 * time.Millisecond), blend: true}
		if i < len(g.Disposal) {
			// Restoring the previous frame is rare and drawn as a clear, which is what most players fall back to.
			f.dispose = g.Disposal[i] == gif.DisposalBackground || g.Disposal[i] == gif.DisposalPrevious
		}
		f.decode = func() (image.Image, error) { return p, nil }
		a.frames = append(a.frames, f)
	}
	return a, nil
}

// chunk is one RIFF chunk: its four-character code and payload.
type chunk struct {
	id   string
	data []byte
}

// chunks splits RIFF payload into its chunks, which are padded to an even length.
func chunks(b []byte) ([]chunk, error) {
	var out []chunk
	for len(b) > 0 {
		if len(b) < 8 {
			return nil, errCorrupt
		}
		n := binary.LittleEndian.Uint32(b[4:8])
		if uint64(n) > uint64(len(b)-8) {
			return nil, errCorrupt
		}
		out = append(out, chunk{string(b[:4]), b[8 : 8+n]})
		b = b[8+n:]
		if n%2 == 1 && len(b) > 0 {
			b = b[1:]
		}
	}
	return out, nil
}

func u24(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }

// decodeWebP lays out an animated WebP's frames; each frame's bitstream is decoded when it is drawn.
func decodeWebP(data []byte) (*Animation, error) {
	size := binary.LittleEndian.Uint32(data[4:8])
	if uint64(size) < 4 || uint64(size)+8 > uint64(len(data)) {
		return nil, errCorrupt
	}
	top, err := chunks(data[12 : 8+size])
	if err != nil {
		return nil, err
	}
	a := &Animation{}
	animated := false
	for _, c := range top {
		switch c.id {
		case "VP8X":
			if len(c.data) < 10 {
				return nil, errCorrupt
			}
			animated = c.data[0]&0x02 != 0
			a.W, a.H = u24(c.data[4:])+1, u24(c.data[7:])+1
		case "ANMF":
			if len(c.data) < 16 {
				return nil, errCorrupt
			}
			d := c.data
			f := frame{
				rect:    image.Rect(2*u24(d[0:]), 2*u24(d[3:]), 0, 0),
				delay:   delay(time.Duration(u24(d[12:])) * time.Millisecond),
				blend:   d[15]&0x02 == 0,
				dispose: d[15]&0x01 != 0,
			}
			w, h := u24(d[6:])+1, u24(d[9:])+1
			f.rect.Max = f.rect.Min.Add(image.Pt(w, h))
			sub, err := chunks(d[16:])
			if err != nil {
				return nil, err
			}
			still, err := standalone(sub, w, h)
			if err != nil {
				return nil, err
			}
			f.decode = func() (image.Image, error) {
				img, err := webp.Decode(bytes.NewReader(still))
				if err != nil {
					return nil, err
				}
				if !img.Bounds().Size().Eq(image.Pt(w, h)) {
					return nil, errCorrupt
				}
				return img, nil
			}
			a.frames = append(a.frames, f)
		}
	}
	if !animated {
		return nil, ErrNotAnimated
	}
	for _, f := range a.frames {
		if !f.rect.In(image.Rect(0, 0, a.W, a.H)) {
			return nil, errCorrupt
		}
	}
	return a, nil
}

// standalone wraps one frame's bitstream as a still WebP file the image decoder reads.
func standalone(sub []chunk, w, h int) ([]byte, error) {
	var alpha, bits *chunk
	for i := range sub {
		switch sub[i].id {
		case "ALPH":
			alpha = &sub[i]
		case "VP8 ", "VP8L":
			bits = &sub[i]
		}
	}
	if bits == nil {
		return nil, errCorrupt
	}
	var body bytes.Buffer
	put := func(id string, data []byte) {
		body.WriteString(id)
		binary.Write(&body, binary.LittleEndian, uint32(len(data)))
		body.Write(data)
		if len(data)%2 == 1 {
			body.WriteByte(0)
		}
	}
	if alpha != nil && bits.id == "VP8 " {
		x := make([]byte, 10)
		x[0] = 0x10 // alpha
		x[4], x[5], x[6] = byte(w-1), byte((w-1)>>8), byte((w-1)>>16)
		x[7], x[8], x[9] = byte(h-1), byte((h-1)>>8), byte((h-1)>>16)
		put("VP8X", x)
		put("ALPH", alpha.data)
	}
	put(bits.id, bits.data)
	var out bytes.Buffer
	out.WriteString("RIFF")
	binary.Write(&out, binary.LittleEndian, uint32(4+body.Len()))
	out.WriteString("WEBP")
	io.Copy(&out, &body)
	return out.Bytes(), nil
}

// First is the first frame of an animation, or of a still GIF or WebP, as a picture of its own.
func First(data []byte) (image.Image, error) {
	a, err := Decode(data)
	if err != nil {
		if errors.Is(err, ErrNotAnimated) {
			img, _, err := image.Decode(bytes.NewReader(data))
			return img, err
		}
		return nil, err
	}
	img, _, err := a.Next()
	if err != nil {
		return nil, err
	}
	return &image.NRGBA{Pix: bytes.Clone(img.Pix), Stride: img.Stride, Rect: img.Rect}, nil
}
