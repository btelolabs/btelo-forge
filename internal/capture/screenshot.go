package capture

import (
	"context"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/media"
)

// Screenshot saves the display, or a region of it (empty for all), as it is now to a PNG at path, grabbed through
// Desktop Duplication like a recording.
func Screenshot(ctx context.Context, d Display, region image.Rectangle, cursor bool, path string) error {
	if !Supported() {
		return ErrUnsupported
	}
	if err := hasGrabber(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
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
		"-filter_complex", fmt.Sprintf("ddagrab=output_idx=%d:framerate=10:draw_mouse=%s%s,hwdownload,format=bgra,format=rgb24[v]", d.Output, mouse, crop(d, region)),
		"-map", "[v]", "-frames:v", "1", "-update", "1", path}
}
