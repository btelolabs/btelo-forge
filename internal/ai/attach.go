package ai

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"os"

	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// MaxSendSide is the longest side a picture is sent at: models take pictures of about this size at most, and a larger
// one only makes the request slower.
const MaxSendSide = 1568

// maxSendBytes is how large a picture may be sent as the file holds it, when its sides are small enough.
const maxSendBytes = 4 << 20

// LoadImage reads the picture in the file at path to be sent to a model: as it is when it is of a type and size
// every model takes, else scaled down to MaxSendSide and written as JPEG (a picture with transparency on white).
func LoadImage(path string) (Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Image{}, err
	}
	src, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Image{}, fmt.Errorf("%s is not a picture that can be read: %w", path, err)
	}
	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return Image{}, fmt.Errorf("%s is an empty picture", path)
	}
	if (format == "png" || format == "jpeg") && max(b.Dx(), b.Dy()) <= MaxSendSide && len(data) <= maxSendBytes {
		return Image{MIME: "image/" + format, Data: data}, nil
	}
	w, h := b.Dx(), b.Dy()
	if side := max(w, h); side > MaxSendSide {
		w, h = max(1, w*MaxSendSide/side), max(1, h*MaxSendSide/side)
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Rect, image.White, image.Point{}, draw.Src)
	xdraw.CatmullRom.Scale(dst, dst.Rect, src, b, draw.Over, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 90}); err != nil {
		return Image{}, err
	}
	return Image{MIME: "image/jpeg", Data: out.Bytes()}, nil
}
