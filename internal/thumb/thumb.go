// Package thumb is the small picture every file of the app's own formats ends with, written when it is saved so that
// Explorer, the file manager and anything else can show the file by reading a few kilobytes of it, without opening
// it. The file's end is a JPEG and a footer, which packaging/windows/thumbnailer reads too:
//
//	JPEG | length | width | height | milliseconds | mark
//
// each number 32 bits, little-endian, and width, height and milliseconds what the file holds, not the JPEG's size.
package thumb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"

	xdraw "golang.org/x/image/draw"
)

// Box is the most pixels a picture has each way: as much as the file manager's largest grid uses.
const Box = 512

// MaxSize is the most bytes the end of a file takes: it fits a zip file's comment, which is how a canvas holds it.
const MaxSize = 0xffff

const (
	mark       = "BTFPIC01"
	footerSize = 16 + len(mark)
)

// zipEnd is what a zip reader looks for from the end of a file: the picture must not hold it, or a canvas would no
// longer read.
var zipEnd = []byte("PK\x05\x06")

// qualities are tried in turn until the picture fits and holds no zipEnd, so a noisy picture still gets one.
var qualities = []int{80, 72, 64, 56, 48, 40, 32, 24}

// Meta is what the end of a file says about the file.
type Meta struct {
	W, H     int     // the picture's size in pixels: a canvas's extent, an edit's frame
	Duration float64 // an edit's seconds
}

// ErrNone means a file has no picture at its end.
var ErrNone = errors.New("the file has no small picture")

// Encode makes the end of a file from img, scaled down into Box and laid on a checkerboard where it is transparent.
func Encode(img image.Image, m Meta) ([]byte, error) {
	pic := flat(img)
	footer := make([]byte, footerSize)
	binary.LittleEndian.PutUint32(footer[4:], uint32(m.W))
	binary.LittleEndian.PutUint32(footer[8:], uint32(m.H))
	binary.LittleEndian.PutUint32(footer[12:], uint32(m.Duration*1000))
	copy(footer[16:], mark)
	for _, q := range qualities {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, pic, &jpeg.Options{Quality: q}); err != nil {
			return nil, err
		}
		binary.LittleEndian.PutUint32(footer, uint32(buf.Len()))
		buf.Write(footer)
		if buf.Len() <= MaxSize && !bytes.Contains(buf.Bytes(), zipEnd) {
			return buf.Bytes(), nil
		}
	}
	return nil, errors.New("the small picture does not fit")
}

// flat is img inside Box with nothing transparent left, its top left corner at the origin.
func flat(img image.Image) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > Box || h > Box {
		if w >= h {
			w, h = Box, max(1, h*Box/w)
		} else {
			w, h = max(1, w*Box/h), Box
		}
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	const cell = 12
	light, dark := image.NewUniform(color.Gray{0xff}), image.NewUniform(color.Gray{0xe4})
	for y := 0; y < h; y += cell {
		for x := 0; x < w; x += cell {
			c := light
			if (x+y)/cell%2 == 1 {
				c = dark
			}
			draw.Draw(out, image.Rect(x, y, x+cell, y+cell), c, image.Point{}, draw.Src)
		}
	}
	if w == b.Dx() && h == b.Dy() {
		draw.Draw(out, out.Rect, img, b.Min, draw.Over)
	} else {
		xdraw.ApproxBiLinear.Scale(out, out.Rect, img, b, xdraw.Over, nil)
	}
	return out
}

// parse reads a footer, in a file of the given size: the JPEG's length and what the footer says of the file.
func parse(footer []byte, size int64) (length int, m Meta, ok bool) {
	if len(footer) != footerSize || string(footer[16:]) != mark {
		return 0, Meta{}, false
	}
	n := int64(binary.LittleEndian.Uint32(footer))
	if n == 0 || n+int64(footerSize) > MaxSize || n+int64(footerSize) > size {
		return 0, Meta{}, false
	}
	return int(n), Meta{
		W:        int(binary.LittleEndian.Uint32(footer[4:])),
		H:        int(binary.LittleEndian.Uint32(footer[8:])),
		Duration: float64(binary.LittleEndian.Uint32(footer[12:])) / 1000,
	}, true
}

// Strip is data without the picture at its end, for formats whose reader wants only what it wrote.
func Strip(data []byte) []byte {
	if len(data) < footerSize {
		return data
	}
	n, _, ok := parse(data[len(data)-footerSize:], int64(len(data)))
	if !ok {
		return data
	}
	return data[:len(data)-footerSize-n]
}

// Raw reads the end of the file at path as Encode made it, to be given to another file.
func Raw(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() < int64(footerSize) {
		return nil, ErrNone
	}
	footer := make([]byte, footerSize)
	if _, err := f.ReadAt(footer, st.Size()-int64(footerSize)); err != nil {
		return nil, err
	}
	n, _, ok := parse(footer, st.Size())
	if !ok {
		return nil, ErrNone
	}
	end := make([]byte, n+footerSize)
	if _, err := f.ReadAt(end, st.Size()-int64(len(end))); err != nil {
		return nil, err
	}
	return end, nil
}

// Read is the picture at the end of the file at path and what it says about the file.
func Read(path string) (*image.RGBA, Meta, error) {
	end, err := Raw(path)
	if err != nil {
		return nil, Meta{}, err
	}
	n, m, ok := parse(end[len(end)-footerSize:], int64(len(end)))
	if !ok {
		return nil, Meta{}, ErrNone
	}
	img, err := jpeg.Decode(bytes.NewReader(end[:n]))
	if err != nil {
		return nil, Meta{}, err
	}
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Rect, img, b.Min, draw.Src)
	return out, m, nil
}
