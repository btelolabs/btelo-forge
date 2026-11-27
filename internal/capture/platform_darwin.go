//go:build darwin

package capture

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Accelerate -framework AppKit -framework AudioToolbox -framework AVFoundation -framework CoreAudio -framework CoreGraphics -framework CoreMedia -framework CoreVideo -framework Foundation -framework ScreenCaptureKit
#include "capture_darwin.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"image"
	"time"
)

// Supported reports whether this system can record its screen.
func Supported() bool { return true }

// CheckSystem verifies macOS is 12.3 or later (ScreenCaptureKit).
func CheckSystem() error {
	var major, minor C.int
	if C.cap_macos_version_ok(&major, &minor) == 0 {
		return fmt.Errorf("%w: macOS 12.3 or later is required (this is macOS %d.%d)", ErrUnsupported, int(major), int(minor))
	}
	return nil
}

func platformEncoders() []Encoder {
	return []Encoder{{"h264_videotoolbox"}, {"libx264"}}
}

// counter returns monotonic host time in 100 ns units (matching CoreAudio/ScreenCaptureKit host timestamps).
func counter() int64          { return int64(C.cap_host_100ns()) }
func counterFrequency() int64 { return 10_000_000 }
func wallMicros() int64       { return time.Now().UnixMicro() }

func isAppBundle() bool {
	return C.cap_is_app_bundle() != 0
}

func hasScreenPermission() bool {
	return C.cap_has_screen_permission() != 0
}

// Apple's VideoToolbox H.264 hardware encoder rejects dimensions above 4096 pixels (error -12903).
const (
	darwinHardwareMaxWidth  = 4096
	darwinHardwareMaxHeight = 4096
)

func canFFmpegScreenshot() bool { return false }

func probeEncoder(ctx context.Context, d Display, fps int) (Encoder, error) {
	// Outside a bundled .app (e.g., go test / CLI), fail fast without triggering macOS TCC prompts.
	// Inside the bundled .app, probe the encoder cleanly so the Recording dialog stays usable and
	// ScreenCaptureKit presents the native macOS permission sheet when the user actually clicks Record.
	if !isAppBundle() && !hasScreenPermission() {
		return Encoder{}, ErrScreenPermission
	}
	if fps <= 0 {
		fps = 30
	}
	area := evenArea(d, image.Rectangle{})
	if area.Empty() {
		return Encoder{}, ErrNoDisplay
	}
	var errs []error
	for _, e := range encoders {
		err := tryDarwinEncoder(ctx, area.Size(), fps, e)
		if err == nil {
			return e, nil
		}
		if ctx.Err() != nil {
			return Encoder{}, ctx.Err()
		}
		errs = append(errs, fmt.Errorf("%s: %w", e.Codec, err))
	}
	return Encoder{}, fmt.Errorf("%w: %w", ErrNoEncoder, errors.Join(errs...))
}

func tryDarwinEncoder(ctx context.Context, size image.Point, fps int, e Encoder) error {
	if !e.Hardware() {
		fps = min(fps, softwareMaxFPS)
	}
	size = darwinRecordSize(size, e.Hardware())
	// Test encoding a short synthetic NV12 frame with the exact recording encoder arguments.
	return tryStreamEncoderWithArgs(ctx, size, fps, encodeArgs(e, fps))
}

func darwinRecordSize(size image.Point, hw bool) image.Point {
	w, h := size.X, size.Y
	maxW, maxH := softwareMaxWidth, softwareMaxHeight
	if hw {
		maxW, maxH = darwinHardwareMaxWidth, darwinHardwareMaxHeight
	}
	if w > maxW || h > maxH {
		w, h = fitInside(w, h, maxW, maxH)
	}
	w = max(2, w&^1)
	h = max(2, h&^1)
	return image.Pt(w, h)
}

func tryStreamEncoderWithArgs(ctx context.Context, size image.Point, fps int, encArgs []string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-f", "lavfi", "-i",
		fmt.Sprintf("color=c=black:s=%dx%d:r=%d,format=nv12", size.X, size.Y, fps), "-frames:v", "2"}
	args = append(args, encArgs...)
	args = append(args, "-f", "null", "-")
	return runFFmpegQuick(ctx, args)
}
