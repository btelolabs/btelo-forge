package ui

import (
	"bytes"
	_ "embed"
	"image"
	"image/draw"
	"image/jpeg"
	"sync"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

//go:embed assets/logo.jpg
var logoJPEG []byte

var (
	logoOnce  sync.Once
	logoImage *image.RGBA
)

// logo is the app's mark, decoded once.
func logo() *image.RGBA {
	logoOnce.Do(func() {
		img, err := jpeg.Decode(bytes.NewReader(logoJPEG))
		if err != nil {
			panic(err) // the embedded file is part of the program
		}
		logoImage = image.NewRGBA(img.Bounds())
		draw.Draw(logoImage, logoImage.Rect, img, img.Bounds().Min, draw.Src)
	})
	return logoImage
}

var logoScaler gfx.Scaler

// drawLogo draws the mark in r, resampled only when the size changes.
func drawLogo(u *ctx, r gfx.Rect) {
	p := r.Pixels()
	if scaled := logoScaler.Scaled(logo(), p.Dx(), p.Dy()); scaled != nil {
		u.c.Blit(scaled, p.Min.X, p.Min.Y, false)
	}
}
