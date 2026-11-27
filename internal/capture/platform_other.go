//go:build !windows && !darwin

package capture

import (
	"context"
	"image"
)

// Supported reports whether this system can record its screen.
func Supported() bool { return false }

// CheckSystem says why this system cannot record.
func CheckSystem() error { return ErrUnsupported }

// Displays lists the screens that can be recorded.
func Displays() ([]Display, error) { return nil, ErrUnsupported }

// Microphones lists the recording devices.
func Microphones() ([]AudioDevice, error) { return nil, ErrUnsupported }

func captureAudio(loopback bool, id string, origin int64, deliver func(samples []float32, frames int, at int64), opened func(error), stop <-chan struct{}) {
	opened(ErrUnsupported)
}

func counter() int64          { return 0 }
func counterFrequency() int64 { return 1 }
func wallMicros() int64       { return 0 }

func grabScreen(d Display, area image.Rectangle, cursor, flush bool) (*image.RGBA, error) {
	return nil, ErrUnsupported
}

func grabDisplays(context.Context, []Display, bool, bool) ([]*image.RGBA, error) {
	return nil, ErrUnsupported
}

func platformEncoders() []Encoder {
	return []Encoder{{"libx264"}}
}

func probeEncoder(context.Context, Display, int) (Encoder, error) {
	return Encoder{}, ErrUnsupported
}

func startRecordSegment(Options, string, segmentHooks) (*segment, error) {
	return nil, ErrUnsupported
}

func startScreenFeed(Options, StreamOptions, bool, *frameStore, func(*screenFeed, error, bool)) (*screenFeed, error) {
	return nil, ErrUnsupported
}

func canFFmpegScreenshot() bool { return false }

func listCameras(context.Context) ([]Camera, error) {
	return nil, ErrUnsupported
}

func startCameraPlatform(CameraOptions, func([]byte)) (*CameraFeed, error) {
	return nil, ErrUnsupported
}
