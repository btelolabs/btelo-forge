package ui

import (
	"bytes"
	_ "embed"
	"image"
	"image/draw"
	"image/jpeg"
	"sync"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
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

// LogoRounding is the corner radius of the mark as a share of its side.
const LogoRounding = 0.18

var (
	logoScaler  gfx.Scaler
	logoScaled  *image.RGBA
	logoRounded *image.RGBA
)

// drawLogo draws the mark in r with rounded corners, resampled only when the size changes.
func drawLogo(u *ctx, r gfx.Rect) {
	p := r.Pixels()
	scaled := logoScaler.Scaled(logo(), p.Dx(), p.Dy())
	if scaled == nil {
		return
	}
	if scaled != logoScaled || logoRounded == nil || logoRounded.Rect != scaled.Rect {
		logoScaled = scaled
		logoRounded = image.NewRGBA(scaled.Rect)
		copy(logoRounded.Pix, scaled.Pix)
		gfx.RoundCorners(logoRounded, LogoRounding*float32(min(p.Dx(), p.Dy())))
	}
	u.c.Blit(logoRounded, p.Min.X, p.Min.Y, false)
}
