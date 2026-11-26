//go:build darwin

package capture

/*
#include <stdlib.h>
#include "capture_darwin.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"image"
	"net"
	"runtime/cgo"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/btelolabs/btelo-forge/internal/media"
)

type screenCallback struct {
	onFrame   func(y []byte, yStride int, uv []byte, uvStride int, w, h int, ts100ns int64)
	onStopped func(err error)
}

//export goScreenFrame
func goScreenFrame(handle C.uintptr_t, yPtr unsafe.Pointer, yStride C.int, uvPtr unsafe.Pointer, uvStride C.int, w C.int, h C.int, ts100ns C.int64_t) {
	cb, ok := cgo.Handle(handle).Value().(*screenCallback)
	if !ok || cb == nil || cb.onFrame == nil {
		return
	}
	wi, hi := int(w), int(h)
	ys, uvs := int(yStride), int(uvStride)
	if wi <= 0 || hi <= 0 || ys < wi || uvs < wi {
		return
	}
	ySlice := unsafe.Slice((*byte)(yPtr), ys*hi)
	uvSlice := unsafe.Slice((*byte)(uvPtr), uvs*(hi/2))
	cb.onFrame(ySlice, ys, uvSlice, uvs, wi, hi, int64(ts100ns))
}

//export goScreenStopped
func goScreenStopped(handle C.uintptr_t, msg *C.char) {
	cb, ok := cgo.Handle(handle).Value().(*screenCallback)
	if !ok || cb == nil || cb.onStopped == nil {
		return
	}
	cb.onStopped(errors.New(C.GoString(msg)))
}

type darwinScreen struct {
	ctx    unsafe.Pointer
	handle cgo.Handle
	once   sync.Once
}

func startDarwinScreen(d Display, area image.Rectangle, outW, outH, fps int, cursor bool, cb *screenCallback) (*darwinScreen, error) {
	if !isAppBundle() && !hasScreenPermission() {
		return nil, ErrScreenPermission
	}
	sx, sy, sw, sh := displaySourceRect(d, struct{ MinX, MinY, W, H int }{area.Min.X, area.Min.Y, area.Dx(), area.Dy()})
	h := cgo.NewHandle(cb)
	cur := C.int(0)
	if cursor {
		cur = 1
	}
	var errCode C.int
	var errBuf [256]C.char
	ctx := C.cap_screen_start(
		C.uint32_t(displayIDOf(d)),
		C.double(sx), C.double(sy), C.double(sw), C.double(sh),
		C.int(outW), C.int(outH), C.int(fps), cur,
		C.uintptr_t(h), &errCode, &errBuf[0], C.int(len(errBuf)),
	)
	if ctx == nil {
		h.Delete()
		if errCode == C.CAP_ERR_PERMISSION {
			return nil, ErrScreenPermission
		}
		return nil, fmt.Errorf("screen capture: %s", C.GoString(&errBuf[0]))
	}
	return &darwinScreen{ctx: ctx, handle: h}, nil
}

func (ds *darwinScreen) stop() {
	if ds == nil {
		return
	}
	ds.once.Do(func() {
		C.cap_screen_stop(ds.ctx)
		ds.ctx = nil
		ds.handle.Delete()
	})
}

// blitNV12Letterbox copies an NV12 frame (srcW×srcH with arbitrary plane strides) centered into a dstW×dstH NV12 canvas
// that is already filled with video-range black (Y=16, UV=128) in its border regions.
func blitNV12Letterbox(dst []byte, dstW, dstH int, ySrc []byte, yStride int, uvSrc []byte, uvStride int, srcW, srcH int) {
	luma := dstW * dstH
	if srcW > dstW {
		srcW = dstW
	}
	if srcH > dstH {
		srcH = dstH
	}
	srcW &^= 1
	srcH &^= 1
	if srcW <= 0 || srcH <= 0 || len(dst) < luma*3/2 {
		return
	}
	padX := ((dstW - srcW) / 2) &^ 1
	padY := ((dstH - srcH) / 2) &^ 1
	if padX == 0 && padY == 0 && srcW == dstW && srcH == dstH && yStride == dstW && uvStride == dstW {
		copy(dst[:luma], ySrc[:luma])
		copy(dst[luma:luma+luma/2], uvSrc[:luma/2])
		return
	}
	for y := range srcH {
		copy(dst[(padY+y)*dstW+padX:(padY+y)*dstW+padX+srcW], ySrc[y*yStride:y*yStride+srcW])
	}
	uvH := srcH / 2
	uvPadY := padY / 2
	for y := range uvH {
		dstOff := luma + (uvPadY+y)*dstW + padX
		srcOff := y * uvStride
		copy(dst[dstOff:dstOff+srcW], uvSrc[srcOff:srcOff+srcW])
	}
}

func fillNV12Black(buf []byte, w, h int) {
	luma := w * h
	for i := range luma {
		buf[i] = 16
	}
	for i := luma; i < len(buf); i++ {
		buf[i] = 128
	}
}

// darwinRecordArgs builds ffmpeg arguments for recording a macOS segment from a local NV12 video socket (and optional
// mixed audio socket) using VideoToolbox or libx264. Note: input -thread_queue_size is omitted because FFmpeg 9+
// removed it as a demuxer option (making it muxer-only).
func darwinRecordArgs(o Options, w, h int, video, audio, out string) []string {
	fps := o.fps()
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "rawvideo", "-pix_fmt", "nv12",
		"-video_size", fmt.Sprintf("%dx%d", w, h), "-framerate", strconv.Itoa(fps), "-i", video,
	}
	if audio != "" {
		args = append(args, "-probesize", "32", "-analyzeduration", "0",
			"-f", "f32le", "-ar", strconv.Itoa(sampleRate), "-ac", strconv.Itoa(channels), "-i", audio)
	}
	args = append(args, "-map", "0:v")
	if audio != "" {
		args = append(args, "-map", "1:a", "-c:a", "flac")
	}
	args = append(args, "-fps_mode", "passthrough")
	args = append(args, encodeArgs(o.Encoder, fps)...)
	return append(args, "-flush_packets", "1", "-cluster_time_limit", "1000", "-f", "matroska", out)
}

// startRecordSegment captures macOS screen frames via ScreenCaptureKit and paces them into ffmpeg alongside audio
// timed from the exact same mach_absolute_time origin.
func startRecordSegment(o Options, part string, hooks segmentHooks) (*segment, error) {
	if !isAppBundle() && !hasScreenPermission() {
		return nil, ErrScreenPermission
	}
	area := o.area()
	if area.Empty() {
		return nil, ErrNoDisplay
	}
	outSize := darwinRecordSize(area.Size(), o.Encoder.Hardware())
	w, h := outSize.X, outSize.Y
	fps := o.fps()

	frames := newFrameStore(outSize)
	owner := &screenFeed{}
	frames.own(owner)
	buf := make([]byte, frames.size)
	fillNV12Black(buf, w, h)

	firstFrame := make(chan struct{})
	var firstOnce sync.Once
	var streamErrMu sync.Mutex
	var streamErr error
	streamDone := make(chan struct{})
	var doneOnce sync.Once

	sc, err := startDarwinScreen(o.Display, area, w, h, fps, o.Cursor, &screenCallback{
		onFrame: func(y []byte, yStride int, uv []byte, uvStride int, fw, fh int, _ int64) {
			blitNV12Letterbox(buf, w, h, y, yStride, uv, uvStride, fw, fh)
			buf = frames.put(owner, buf)
			firstOnce.Do(func() { close(firstFrame) })
		},
		onStopped: func(err error) {
			streamErrMu.Lock()
			streamErr = err
			streamErrMu.Unlock()
			doneOnce.Do(func() { close(streamDone) })
		},
	})
	if err != nil {
		return nil, err
	}

	// Wait briefly for the first real frame so frame 0 isn't black.
	select {
	case <-firstFrame:
	case <-streamDone:
		sc.stop()
		streamErrMu.Lock()
		se := streamErr
		streamErrMu.Unlock()
		return nil, se
	case <-time.After(2 * time.Second):
	}

	videoListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		sc.stop()
		return nil, err
	}
	videoAddr := "tcp://" + videoListener.Addr().String()

	started := make(chan *segment, 1)
	var pacing sync.WaitGroup
	pacing.Add(1)
	go func() {
		defer pacing.Done()
		paceRecordVideo(videoListener, started, streamDone, frames, fps)
	}()

	var stopOnce sync.Once
	cleanup := func() {
		stopOnce.Do(func() {
			videoListener.Close()
			sc.stop()
			pacing.Wait()
		})
	}

	wrappedHooks := segmentHooks{
		started: func(s *segment) {
			s.onStop = cleanup
			hooks.started(s)
			started <- s
		},
		levels: hooks.levels,
		failed: hooks.failed,
	}

	seg, err := startSegment(o, func(_ int64, audio string) []string {
		return darwinRecordArgs(o, w, h, videoAddr, audio, part)
	}, nil, wrappedHooks)
	if err != nil {
		close(started)
		cleanup()
		return nil, err
	}
	return seg, nil
}

func paceRecordVideo(l net.Listener, started <-chan *segment, streamDone <-chan struct{}, frames *frameStore, fps int) {
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := l.Accept()
		l.Close()
		if err != nil {
			conn = nil
		}
		accepted <- conn
	}()
	seg, ok := <-started
	if !ok {
		l.Close()
		if conn := <-accepted; conn != nil {
			conn.Close()
		}
		return
	}
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-seg.exited:
		l.Close()
		return
	case <-streamDone:
		l.Close()
		return
	}
	if conn == nil {
		return
	}
	defer conn.Close()
	fps64, freq := int64(fps), counterFrequency()
	frame := make([]byte, frames.size)
	tick := time.NewTicker(time.Second / time.Duration(fps) / 2)
	defer tick.Stop()
	var sent int64
	for {
		due := (counter()-seg.origin)*fps64/freq + 1
		for ; sent < due; sent++ {
			frames.copyTo(frame)
			if _, err := conn.Write(frame); err != nil {
				return
			}
		}
		select {
		case <-seg.exited:
			return
		case <-seg.quit:
			return
		case <-streamDone:
			return
		case <-tick.C:
		}
	}
}

// startScreenFeed captures a display/region via ScreenCaptureKit hardware scaling directly into NV12 and blits it
// centered onto the live-stream canvas without spawning a separate ffmpeg grabber process.
func startScreenFeed(o Options, so StreamOptions, _ bool, frames *frameStore, ended func(f *screenFeed, err error, delivered bool)) (*screenFeed, error) {
	if !isAppBundle() && !hasScreenPermission() {
		return nil, ErrScreenPermission
	}
	area := evenArea(o.Display, o.Region)
	if area.Empty() {
		return nil, ErrNoDisplay
	}
	w, h := fitInside(area.Dx(), area.Dy(), so.Size.X, so.Size.Y)
	ctx, cancel := context.WithCancel(context.Background())
	f := &screenFeed{opts: o, gpu: false, cancel: cancel, done: make(chan struct{})}
	frames.own(f)

	buf := make([]byte, frames.size)
	fillNV12Black(buf, so.Size.X, so.Size.Y)
	var mu sync.Mutex
	delivered := false
	stoppedCh := make(chan error, 1)

	sc, err := startDarwinScreen(o.Display, area, w, h, so.FPS, o.Cursor, &screenCallback{
		onFrame: func(y []byte, yStride int, uv []byte, uvStride int, fw, fh int, _ int64) {
			mu.Lock()
			if len(buf) != frames.size {
				buf = make([]byte, frames.size)
				fillNV12Black(buf, so.Size.X, so.Size.Y)
			}
			// If frameStore swapped in a brand-new buffer (first put), initialize its letterbox borders to black.
			if !delivered && (w != so.Size.X || h != so.Size.Y) {
				fillNV12Black(buf, so.Size.X, so.Size.Y)
			}
			blitNV12Letterbox(buf, so.Size.X, so.Size.Y, y, yStride, uv, uvStride, fw, fh)
			next := frames.put(f, buf)
			if next != nil && len(next) == frames.size && (w != so.Size.X || h != so.Size.Y) {
				// Ensure swapped-in buffer has clean black borders on first use (cheap check on corner pixel).
				if next[0] == 0 {
					fillNV12Black(next, so.Size.X, so.Size.Y)
				}
			}
			buf = next
			delivered = true
			mu.Unlock()
		},
		onStopped: func(err error) {
			select {
			case stoppedCh <- err:
			default:
			}
		},
	})
	if err != nil {
		cancel()
		frames.release(f)
		return nil, err
	}

	go func() {
		defer close(f.done)
		defer cancel()
		var stopErr error
		select {
		case <-ctx.Done():
			sc.stop()
			frames.release(f)
			return
		case stopErr = <-stoppedCh:
			sc.stop()
			frames.release(f)
		}
		if ctx.Err() != nil {
			return
		}
		mu.Lock()
		del := delivered
		mu.Unlock()
		ended(f, stopErr, del)
	}()
	return f, nil
}

func runFFmpegQuick(ctx context.Context, args []string) error {
	cmd, err := media.Command(ctx, "ffmpeg", args...)
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, lastLine(stderr.String()))
	}
	return nil
}
