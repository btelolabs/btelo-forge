package render3d

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg" // pictures in the formats glTF allows
	_ "image/png"
	"math"
	"runtime"
	"slices"
	"sync"

	_ "golang.org/x/image/webp"

	"github.com/btelolabs/btelo-forge/internal/gpu"
	s3 "github.com/btelolabs/btelo-forge/internal/scene3d"
)

// srgbToLinear turns a byte of sRGB colour into light.
var srgbToLinear = func() (t [256]float32) {
	for i := range t {
		c := float64(i) / 255
		if c <= .04045 {
			t[i] = float32(c / 12.92)
		} else {
			t[i] = float32(math.Pow((c+.055)/1.055, 2.4))
		}
	}
	return
}()

func linearToSRGB(l float32) uint8 {
	c := float64(l)
	if c <= .0031308 {
		c *= 12.92
	} else {
		c = 1.055*math.Pow(c, 1/2.4) - .055
	}
	return uint8(min(max(c*255+.5, 0), 255))
}

// decodeImages decodes the pictures on all cores; one that cannot be read stays nil, with its error.
func decodeImages(images []s3.Image) ([]*image.NRGBA, []error) {
	out := make([]*image.NRGBA, len(images))
	errs := make([]error, len(images))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(runtime.GOMAXPROCS(0), max(len(images), 1)) {
		wg.Go(func() {
			for i := range jobs {
				im, _, err := image.Decode(bytes.NewReader(images[i].Data))
				if err != nil {
					errs[i] = err
					continue
				}
				b := im.Bounds()
				n := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
				draw.Draw(n, n.Rect, im, b.Min, draw.Src)
				out[i] = n
			}
		})
	}
	for i := range images {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return out, errs
}

// halve is a level half the size in each direction, averaged as light for colour pictures.
func halve(pix []byte, w, h int, srgb bool) ([]byte, int, int) {
	nw, nh := max(w/2, 1), max(h/2, 1)
	out := make([]byte, nw*nh*4)
	for y := range nh {
		y0, y1 := y*h/nh, min((y*h/nh)+h/nh, h)
		for x := range nw {
			x0, x1 := x*w/nw, min((x*w/nw)+w/nw, w)
			var sum [4]float32
			n := float32((y1 - y0) * (x1 - x0))
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					p := pix[(yy*w+xx)*4:]
					if srgb {
						sum[0] += srgbToLinear[p[0]]
						sum[1] += srgbToLinear[p[1]]
						sum[2] += srgbToLinear[p[2]]
					} else {
						sum[0] += float32(p[0])
						sum[1] += float32(p[1])
						sum[2] += float32(p[2])
					}
					sum[3] += float32(p[3])
				}
			}
			o := out[(y*nw+x)*4:]
			for c := range 3 {
				if srgb {
					o[c] = linearToSRGB(sum[c] / n)
				} else {
					o[c] = uint8(sum[c]/n + .5)
				}
			}
			o[3] = uint8(sum[3]/n + .5)
		}
	}
	return out, nw, nh
}

// upload makes a texture of a picture with all its levels, no wider than the card allows.
func upload(d gpu.Device, label string, im *image.NRGBA, srgb bool) (gpu.Texture, error) {
	pix, w, h := im.Pix, im.Rect.Dx(), im.Rect.Dy()
	if im.Stride != w*4 {
		return nil, fmt.Errorf("%s: unexpected pixel layout", label)
	}
	limit := int(d.Limits().TextureSide)
	for limit > 0 && (w > limit || h > limit) {
		pix, w, h = halve(pix, w, h, srgb)
	}
	mips := bits(max(w, h))
	format := gpu.RGBA8Unorm
	if srgb {
		format = gpu.RGBA8Srgb
	}
	t, err := d.Texture(gpu.TextureDesc{Label: label, Width: uint32(w), Height: uint32(h), Mips: uint32(mips), Format: format, Usage: gpu.TextureSampled | gpu.TextureCopyDst})
	if err != nil {
		return nil, err
	}
	for m := range mips {
		if err := d.WriteTexture(t, m, pix, uint32(w*4), uint32(w), uint32(h)); err != nil {
			t.Release()
			return nil, err
		}
		if m+1 < mips {
			pix, w, h = halve(pix, w, h, srgb)
		}
	}
	return t, nil
}

// bits is how many levels a side of v halves through, down to one pixel.
func bits(v int) int {
	n := 1
	for ; v > 1; v >>= 1 {
		n++
	}
	return n
}

// solid is a one-pixel texture.
func solid(d gpu.Device, label string, r, g, b, a byte) (gpu.Texture, error) {
	im := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	copy(im.Pix, []byte{r, g, b, a})
	return upload(d, label, im, false)
}

// half is a float in 16 bits.
func half(f float32) uint16 {
	b := math.Float32bits(f)
	sign := uint16(b>>16) & 0x8000
	exp := int(b>>23&0xff) - 127 + 15
	frac := b & 0x7fffff
	switch {
	case exp >= 31:
		return sign | 0x7c00
	case exp <= 0:
		if exp < -10 {
			return sign
		}
		frac |= 0x800000
		return sign | uint16(frac>>uint(14-exp))
	}
	return sign | uint16(exp)<<10 | uint16(frac>>13)
}

// halveFloat averages a panorama down one level.
func halveFloat(pix []float32, w, h int) ([]float32, int, int) {
	nw, nh := max(w/2, 1), max(h/2, 1)
	out := make([]float32, nw*nh*4)
	for y := range nh {
		y0, y1 := y*h/nh, (y+1)*h/nh
		for x := range nw {
			x0, x1 := x*w/nw, (x+1)*w/nw
			n := float32((y1 - y0) * (x1 - x0))
			at := (y*nw + x) * 4
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					from := (yy*w + xx) * 4
					for c := range 4 {
						out[at+c] += pix[from+c]
					}
				}
			}
			for c := range 4 {
				out[at+c] /= n
			}
		}
	}
	return out, nw, nh
}

// uploadPanorama makes a floating-point texture with its levels, cut down to panoramaW across when larger.
func uploadPanorama(d gpu.Device, p Panorama) (gpu.Texture, error) {
	pix, w, h := p.Pix, p.W, p.H
	for w > panoramaW*2 {
		pix, w, h = halveFloat(pix, w, h)
	}
	mips := bits(max(w, h))
	t, err := d.Texture(gpu.TextureDesc{Label: "sky", Width: uint32(w), Height: uint32(h), Mips: uint32(mips), Format: gpu.RGBA16Float, Usage: gpu.TextureSampled | gpu.TextureCopyDst})
	if err != nil {
		return nil, err
	}
	for m := range mips {
		data := make([]byte, len(pix)*2)
		for i, v := range pix {
			h := half(min(v, 60000))
			data[i*2], data[i*2+1] = byte(h), byte(h>>8)
		}
		if err := d.WriteTexture(t, m, data, uint32(w*8), uint32(w), uint32(h)); err != nil {
			t.Release()
			return nil, err
		}
		if m+1 < mips {
			pix, w, h = halveFloat(pix, w, h)
		}
	}
	return t, nil
}

// paintLevels keeps the levels of a picture being painted, to work out the smaller ones again around what changed.
type paintLevels struct {
	image   int
	srgb    bool
	texture gpu.Texture
	pix     [][]byte
	w, h    []int
}

// BeginPaint makes ready to change the picture, whose pixels are im, read as colour or as numbers.
func (r *Renderer) BeginPaint(image int, im *image.NRGBA, srgb bool) error {
	t := r.plainTex[image]
	if srgb {
		t = r.colorTex[image]
	}
	if t == nil {
		return fmt.Errorf("picture %d is not on the card", image)
	}
	w, h := im.Rect.Dx(), im.Rect.Dy()
	levels := &paintLevels{image: image, texture: t, srgb: srgb}
	pix := im.Pix
	for {
		levels.pix, levels.w, levels.h = append(levels.pix, pix), append(levels.w, w), append(levels.h, h)
		if w == 1 && h == 1 {
			break
		}
		pix, w, h = halve(pix, w, h, srgb)
	}
	levels.pix[0] = slices.Clone(im.Pix)
	r.painted = levels
	return nil
}

// Paint changes the picture of BeginPaint within rect, whose pixels are pix, row after row, and its smaller levels.
func (r *Renderer) Paint(rect image.Rectangle, pix []byte) error {
	l := r.painted
	if l == nil {
		return fmt.Errorf("nothing is being painted")
	}
	w := l.w[0]
	for y := range rect.Dy() {
		copy(l.pix[0][((rect.Min.Y+y)*w+rect.Min.X)*4:], pix[y*rect.Dx()*4:(y+1)*rect.Dx()*4])
	}
	if err := r.d.WriteTextureRegion(l.texture, 0, pix, uint32(rect.Dx()*4), uint32(rect.Min.X), uint32(rect.Min.Y), uint32(rect.Dx()), uint32(rect.Dy())); err != nil {
		return err
	}
	for m := 1; m < len(l.pix); m++ {
		// Each texel of a level is the mean of two by two of the one above.
		x0, y0 := rect.Min.X>>m, rect.Min.Y>>m
		x1, y1 := min(((rect.Max.X-1)>>m)+1, l.w[m]), min(((rect.Max.Y-1)>>m)+1, l.h[m])
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				var sum [4]float32
				n := float32(0)
				for _, c := range [][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
					px, py := min(x*2+c[0], l.w[m-1]-1), min(y*2+c[1], l.h[m-1]-1)
					if l.w[m-1] == 1 {
						px = 0
					}
					if l.h[m-1] == 1 {
						py = 0
					}
					p := l.pix[m-1][(py*l.w[m-1]+px)*4:]
					for k := range 3 {
						if l.srgb {
							sum[k] += srgbToLinear[p[k]]
						} else {
							sum[k] += float32(p[k])
						}
					}
					sum[3] += float32(p[3])
					n++
				}
				o := l.pix[m][(y*l.w[m]+x)*4:]
				for k := range 3 {
					if l.srgb {
						o[k] = linearToSRGB(sum[k] / n)
					} else {
						o[k] = uint8(sum[k]/n + .5)
					}
				}
				o[3] = uint8(sum[3]/n + .5)
			}
		}
		pitch := l.w[m] * 4
		if err := r.d.WriteTextureRegion(l.texture, m, l.pix[m][(y0*l.w[m]+x0)*4:], uint32(pitch), uint32(x0), uint32(y0), uint32(x1-x0), uint32(y1-y0)); err != nil {
			return err
		}
	}
	return nil
}
