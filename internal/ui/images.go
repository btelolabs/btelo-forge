package ui

import (
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"

	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// maxImageSide is the longest side an overlay image is kept at; larger photos are scaled down when loaded.
const maxImageSide = 4096

// image decodes an image file once and keeps it.
func (m *mediaCache) image(path string) (*image.RGBA, error) {
	if img, ok := m.images[path]; ok {
		return img, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return nil, fmt.Errorf("the image is empty")
	}
	w, h := b.Dx(), b.Dy()
	if side := max(w, h); side > maxImageSide {
		w, h = max(1, w*maxImageSide/side), max(1, h*maxImageSide/side)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if w == b.Dx() && h == b.Dy() {
		draw.Draw(img, img.Rect, src, b.Min, draw.Src)
	} else {
		xdraw.CatmullRom.Scale(img, img.Rect, src, b, draw.Src, nil)
	}
	m.images[path] = img
	return img, nil
}
