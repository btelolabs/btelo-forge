// Package caption draws overlays (text and images); the preview and the export use the same drawing so they match.
package caption

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"strings"
	"sync"

	"golang.org/x/image/draw"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/project"
)

// Image is a drawn caption and where its top-left corner sits in the frame.
type Image struct {
	*image.RGBA
	At image.Point
}

type key struct {
	body          string
	x, y, size    float64
	color         [3]uint8
	outline, box  bool
	opacity       float64
	width, height int
}

// Renderer draws captions with one font and remembers recent results.
type Renderer struct {
	font     *gfx.Font
	mu       sync.Mutex
	cache    map[key]Image
	pictures map[pictureKey]*image.RGBA
}

// New draws with font.
func New(font *gfx.Font) *Renderer {
	return &Renderer{font: font, cache: map[key]Image{}, pictures: map[pictureKey]*image.RGBA{}}
}

// Draw renders t for a w×h frame; an empty body gives an image of zero size.
func (r *Renderer) Draw(t project.Overlay, w, h int) Image {
	k := key{t.Body, t.X, t.Y, t.Size, t.Color, t.Outline, t.Box, t.Opacity, w, h}
	r.mu.Lock()
	if img, ok := r.cache[k]; ok {
		r.mu.Unlock()
		return img
	}
	r.mu.Unlock()
	img := r.draw(t, w, h)
	r.mu.Lock()
	if len(r.cache) > 256 {
		clear(r.cache)
	}
	r.cache[k] = img
	r.mu.Unlock()
	return img
}

func (r *Renderer) draw(t project.Overlay, w, h int) Image {
	body := strings.TrimRight(t.Body, "\n ")
	size := float32(math.Max(t.Size*float64(h), 6))
	if strings.TrimSpace(body) == "" {
		return Image{RGBA: image.NewRGBA(image.Rect(0, 0, 0, 0)), At: image.Pt(int(t.X*float64(w)), int(t.Y*float64(h)))}
	}
	face := r.font.Face(size)
	lines := face.Wrap(body, float32(w)*0.9)
	lineH := face.Height() * 1.12
	blockW := float32(0)
	for _, l := range lines {
		blockW = max(blockW, face.Measure(l))
	}
	blockH := lineH * float32(len(lines))
	outline := float32(0)
	if t.Outline {
		outline = max(1, size*0.07)
	}
	padX, padY := outline+1, outline+1
	if t.Box {
		padX, padY = max(padX, size*0.35), max(padY, size*0.18)
	}
	iw, ih := int(math.Ceil(float64(blockW+2*padX))), int(math.Ceil(float64(blockH+2*padY)))
	img := image.NewRGBA(image.Rect(0, 0, iw, ih))
	c := gfx.Wrap(img)
	if t.Box {
		c.RoundRect(gfx.Rect{X: 0, Y: 0, W: float32(iw), H: float32(ih)}, size*0.25, color.RGBA{0, 0, 0, 150})
	}
	fill := color.RGBA{t.Color[0], t.Color[1], t.Color[2], 255}
	for i, l := range lines {
		x := padX + (blockW-face.Measure(l))/2
		y := padY + float32(i)*lineH + (lineH-face.Height())/2
		if outline > 0 {
			dark := color.RGBA{0, 0, 0, 255}
			if lum := 0.3*float32(t.Color[0]) + 0.59*float32(t.Color[1]) + 0.11*float32(t.Color[2]); lum < 90 {
				dark = color.RGBA{255, 255, 255, 255}
			}
			for _, ring := range []float32{outline, outline / 2} {
				for k := 0; k < 16; k++ {
					a := float64(k) * math.Pi / 8
					c.Text(face, x+ring*float32(math.Cos(a)), y+ring*float32(math.Sin(a)), l, dark)
				}
			}
		}
		c.Text(face, x, y, l, fill)
	}
	cx, cy := t.X*float64(w), t.Y*float64(h)
	at := image.Pt(int(math.Round(cx-float64(iw)/2)), int(math.Round(cy-float64(ih)/2)))
	at.X = min(max(at.X, 0), max(w-iw, 0))
	at.Y = min(max(at.Y, 0), max(h-ih, 0))
	return Image{RGBA: gfx.Fade(img, t.Opacity), At: at}
}

type pictureKey struct {
	src           *image.RGBA
	size, opacity float64
	width, height int
}

// Picture renders an image overlay for a w×h frame: src scaled to the overlay's height and centred at its position,
// kept inside the frame. Only the scaling is cached, so dragging the image does not resample it.
func (r *Renderer) Picture(o project.Overlay, src *image.RGBA, w, h int) Image {
	k := pictureKey{src, o.Size, o.Opacity, w, h}
	r.mu.Lock()
	out, ok := r.pictures[k]
	r.mu.Unlock()
	if !ok {
		ih := max(1, int(math.Round(o.Size*float64(h))))
		iw := max(1, int(math.Round(float64(ih)*float64(src.Rect.Dx())/float64(src.Rect.Dy()))))
		if iw > w {
			iw, ih = w, max(1, int(math.Round(float64(w)*float64(src.Rect.Dy())/float64(src.Rect.Dx()))))
		}
		out = image.NewRGBA(image.Rect(0, 0, iw, ih))
		draw.CatmullRom.Scale(out, out.Rect, src, src.Rect, draw.Src, nil)
		out = gfx.Fade(out, o.Opacity)
		r.mu.Lock()
		if len(r.pictures) > 64 {
			clear(r.pictures)
		}
		r.pictures[k] = out
		r.mu.Unlock()
	}
	iw, ih := out.Rect.Dx(), out.Rect.Dy()
	at := image.Pt(int(math.Round(o.X*float64(w)-float64(iw)/2)), int(math.Round(o.Y*float64(h)-float64(ih)/2)))
	at.X = min(max(at.X, 0), max(w-iw, 0))
	at.Y = min(max(at.Y, 0), max(h-ih, 0))
	return Image{RGBA: out, At: at}
}

// WritePNG saves the caption for ffmpeg's overlay filter.
func (img Image) WritePNG(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img.RGBA); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
