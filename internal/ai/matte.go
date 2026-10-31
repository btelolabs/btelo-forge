package ai

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"net/http"
	"strings"

	xdraw "golang.org/x/image/draw"

	"github.com/btelolabs/btelo-forge/internal/matting"
)

// Matte says how much of each pixel of img is its subject: 255 is, 0 is not. The model sees the picture squeezed to its
// own size, on white where the picture is transparent, and what it says is brought back to the picture's.
func (e Endpoint) Matte(ctx context.Context, img *image.RGBA) (*image.Alpha, error) {
	side := image.Rect(0, 0, matting.Side, matting.Side)
	seen := image.NewRGBA(side)
	draw.Draw(seen, side, image.NewUniform(color.White), image.Point{}, draw.Src)
	xdraw.BiLinear.Scale(seen, side, img, img.Rect, xdraw.Over, nil)
	rgb := make([]byte, 0, 3*matting.Side*matting.Side)
	for i := 0; i < len(seen.Pix); i += 4 {
		rgb = append(rgb, seen.Pix[i:i+3]...)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(e.URL, "/v1")+"/matte", bytes.NewReader(rgb))
	if err != nil {
		return nil, err
	}
	resp, err := e.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, matting.Side*matting.Side+1))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the model answered: %s", strings.TrimSpace(string(body)))
	}
	if len(body) != matting.Side*matting.Side {
		return nil, fmt.Errorf("the model answered with %d values", len(body))
	}
	small := &image.Alpha{Pix: body, Stride: matting.Side, Rect: side}
	out := image.NewAlpha(img.Rect)
	xdraw.CatmullRom.Scale(out, out.Rect, small, side, xdraw.Src, nil)
	return out, nil
}

// MattingModel is the model that finds a picture's subject and can run here.
func (c *Config) MattingModel() (Model, bool) {
	for _, m := range c.Models {
		if m.Kind.Or() == KindMatting && len(c.Available(m)) > 0 {
			return m, true
		}
	}
	return Model{}, false
}
