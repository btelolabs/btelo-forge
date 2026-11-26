package capture

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

// Supported reports whether this system can record its screen at all; CheckSystem says whether this version can.
func Supported() bool { return true }

// CheckSystem reports an *OldWindowsError when this Windows is too old to record.
func CheckSystem() error {
	// RtlGetVersion tells the real version; GetVersionEx answers as an old Windows to programs without a manifest.
	v := struct {
		Size, Major, Minor, Build, Platform uint32
		ServicePack                         [128]uint16
	}{}
	v.Size = uint32(unsafe.Sizeof(v))
	if r, _, _ := syscall.NewLazyDLL("ntdll.dll").NewProc("RtlGetVersion").Call(uintptr(unsafe.Pointer(&v))); r != 0 {
		return fmt.Errorf("RtlGetVersion: 0x%08X", r)
	}
	if v.Major < 10 || v.Major == 10 && v.Build < minimumBuild {
		return &OldWindowsError{v.Major, v.Minor, v.Build}
	}
	return nil
}

func platformEncoders() []Encoder {
	return []Encoder{{"h264_nvenc"}, {"h264_amf"}, {"h264_mf"}, {"libx264"}}
}

func probeEncoder(ctx context.Context, d Display, fps int) (Encoder, error) {
	if err := hasGrabber(ctx); err != nil {
		return Encoder{}, err
	}
	var errs []error
	for _, e := range encoders {
		err := tryEncoder(ctx, Options{Display: d, FPS: fps, Encoder: e})
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

func startRecordSegment(o Options, part string, hooks segmentHooks) (*segment, error) {
	return startSegment(o, func(t0 int64, audio string) []string { return recordArgs(o, t0, audio, part) }, nil, hooks)
}

func startScreenFeed(o Options, so StreamOptions, gpuScale bool, frames *frameStore, ended func(f *screenFeed, err error, delivered bool)) (*screenFeed, error) {
	return startFFmpegFeed(o, so, gpuScale, frames, ended)
}

func canFFmpegScreenshot() bool { return true }

func listCameras(ctx context.Context) ([]Camera, error) {
	return listCamerasDShow(ctx)
}

func startCameraPlatform(o CameraOptions, frame func(rgba []byte)) (*CameraFeed, error) {
	return startCameraDShow(o, frame)
}
