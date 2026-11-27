package capture

import (
	"bufio"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/media"
)

// GrabScreenshot captures the display, or a region of it (empty for all), directly in memory. When flush is true, it
// waits one DWM frame first so a window just marked excluded from capture is no longer in the composited picture. If
// direct desktop readback is unavailable, it falls back to ffmpeg's Desktop Duplication grabber.
func GrabScreenshot(ctx context.Context, d Display, region image.Rectangle, cursor, flush bool) (*image.RGBA, error) {
	if !Supported() {
		return nil, ErrUnsupported
	}
	area := evenArea(d, region)
	img, err := grabScreen(d, area, cursor, flush)
	if err == nil {
		return img, nil
	}
	if !canFFmpegScreenshot() {
		return nil, err
	}
	tmp, err := os.CreateTemp("", "forge-screenshot-*.png")
	if err != nil {
		return nil, err
	}
	path := tmp.Name()
	tmp.Close()
	defer os.Remove(path)
	if err := ffmpegScreenshot(ctx, d, region, cursor, path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	decoded, err := png.Decode(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	if rgba, ok := decoded.(*image.RGBA); ok {
		return rgba, nil
	}
	b := decoded.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), decoded, b.Min, draw.Src)
	return out, nil
}

// GrabDisplays captures every display in displays as close to simultaneously as possible (typically before any picker
// overlay opens so popups, menus, dropdowns, and hover states stay frozen), returning one *image.RGBA per display.
func GrabDisplays(ctx context.Context, displays []Display, cursor, flush bool) ([]*image.RGBA, error) {
	if !Supported() {
		return nil, ErrUnsupported
	}
	if len(displays) == 0 {
		return nil, ErrNoDisplay
	}
	return grabDisplays(ctx, displays, cursor, flush)
}

// CropDisplay extracts region (empty for the whole display) from a frozen display snapshot, rounding sides down to
// even dimensions as GrabScreenshot does, and returns a standalone tight *image.RGBA.
func CropDisplay(d Display, snap *image.RGBA, region image.Rectangle) *image.RGBA {
	if snap == nil {
		return nil
	}
	area := evenArea(d, region).Intersect(snap.Bounds())
	if area.Empty() {
		return nil
	}
	area.Max.X -= area.Dx() % 2
	area.Max.Y -= area.Dy() % 2
	if area.Empty() {
		return nil
	}
	w, h := area.Dx(), area.Dy()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	rowBytes := w * 4
	for y := range h {
		srcOff := snap.PixOffset(area.Min.X, area.Min.Y+y)
		dstOff := y * out.Stride
		copy(out.Pix[dstOff:dstOff+rowBytes], snap.Pix[srcOff:srcOff+rowBytes])
	}
	return out
}

// Screenshot saves the display, or a region of it (empty for all), as it is now to a PNG at path.
func Screenshot(ctx context.Context, d Display, region image.Rectangle, cursor bool, path string) error {
	if !Supported() {
		return ErrUnsupported
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	area := evenArea(d, region)
	img, err := grabScreen(d, area, cursor, false)
	if err == nil {
		return WritePNG(path, img, false)
	}
	if !canFFmpegScreenshot() {
		return err
	}
	return ffmpegScreenshot(ctx, d, region, cursor, path)
}

// WritePNG writes img to path using fast lossless PNG compression; exclusive refuses to overwrite an existing file.
func WritePNG(path string, img image.Image, exclusive bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if exclusive {
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(f, 256<<10)
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	err = enc.Encode(bw, img)
	if werr := bw.Flush(); err == nil {
		err = werr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
	}
	return err
}

func ffmpegScreenshot(ctx context.Context, d Display, region image.Rectangle, cursor bool, path string) error {
	if err := hasGrabber(ctx); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd, err := media.Command(ctx, "ffmpeg", screenshotArgs(d, region, cursor, path)...)
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.Remove(path)
		return fmt.Errorf("screenshot: %w: %s", err, lastLine(stderr.String()))
	}
	return nil
}

// screenshotArgs grab one frame and save it without its alpha channel, which Desktop Duplication leaves undefined.
func screenshotArgs(d Display, region image.Rectangle, cursor bool, path string) []string {
	mouse := "0"
	if cursor {
		mouse = "1"
	}
	return []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-init_hw_device", fmt.Sprintf("d3d11va=grab:%d", d.Adapter), "-filter_hw_device", "grab",
		"-filter_complex", fmt.Sprintf("ddagrab=output_idx=%d:framerate=60:draw_mouse=%s%s,hwdownload,format=bgra,format=rgb24[v]", d.Output, mouse, crop(d, region)),
		"-map", "[v]", "-frames:v", "1", "-update", "1", path}
}
