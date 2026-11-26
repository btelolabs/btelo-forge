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
	"runtime/cgo"
	"sync"
	"time"
	"unsafe"
)

type cameraCallback struct {
	onFrame func(rgba []byte)
	onError func(err error)
}

//export goCameraFrame
func goCameraFrame(handle C.uintptr_t, rgba unsafe.Pointer, n C.int) {
	cb, ok := cgo.Handle(handle).Value().(*cameraCallback)
	if !ok || cb == nil || cb.onFrame == nil || rgba == nil || n <= 0 {
		return
	}
	cb.onFrame(unsafe.Slice((*byte)(rgba), int(n)))
}

//export goCameraError
func goCameraError(handle C.uintptr_t, msg *C.char) {
	cb, ok := cgo.Handle(handle).Value().(*cameraCallback)
	if !ok || cb == nil || cb.onError == nil {
		return
	}
	cb.onError(errors.New(C.GoString(msg)))
}

func listCameras(_ context.Context) ([]Camera, error) {
	var raw [32]C.cap_camera_info
	n := int(C.cap_list_cameras(&raw[0], C.int(len(raw))))
	cams := make([]Camera, 0, n)
	for i := range n {
		cams = append(cams, Camera{
			ID:   C.GoString(&raw[i].id[0]),
			Name: C.GoString(&raw[i].name[0]),
		})
	}
	return cams, nil
}

func ensureCameraPermission() error {
	var hasDesc C.int
	status := int(C.cap_camera_auth_status(&hasDesc))
	switch status {
	case 3: // AVAuthorizationStatusAuthorized
		return nil
	case 1, 2: // Restricted or Denied
		return ErrCameraPermission
	default: // 0: NotDetermined
		// Never request TCC permission when Info.plist lacks NSCameraUsageDescription (e.g. go test binary),
		// because macOS terminates the process with SIGABRT.
		if hasDesc == 0 {
			return ErrCameraPermission
		}
		if C.cap_request_camera_permission() == 0 {
			return ErrCameraPermission
		}
		return nil
	}
}

func startCameraPlatform(o CameraOptions, frame func(rgba []byte)) (*CameraFeed, error) {
	if err := ensureCameraPermission(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoCamera, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	f := &CameraFeed{opts: o, cancel: cancel, done: make(chan struct{})}
	first := make(chan error, 1)
	errCh := make(chan error, 1)
	var firstOnce sync.Once

	h := cgo.NewHandle(&cameraCallback{
		onFrame: func(rgba []byte) {
			firstOnce.Do(func() { first <- nil })
			frame(rgba)
		},
		onError: func(err error) {
			select {
			case errCh <- err:
			default:
			}
			firstOnce.Do(func() { first <- err })
		},
	})

	cUID := C.CString(o.Camera.ID)
	defer C.free(unsafe.Pointer(cUID))
	mirror := C.int(0)
	if o.Mirror {
		mirror = 1
	}
	var errCode C.int
	var errBuf [256]C.char
	camCtx := C.cap_camera_start(cUID, C.int(o.Size.X), C.int(o.Size.Y), mirror,
		C.uintptr_t(h), &errCode, &errBuf[0], C.int(len(errBuf)))
	if camCtx == nil {
		cancel()
		h.Delete()
		return nil, fmt.Errorf("%w: %s: %s", ErrNoCamera, o.Camera.Name, C.GoString(&errBuf[0]))
	}

	go func() {
		defer close(f.done)
		defer cancel()
		var runErr error
		select {
		case <-ctx.Done():
		case runErr = <-errCh:
		}
		C.cap_camera_stop(camCtx)
		h.Delete()
		if ctx.Err() == nil && runErr != nil {
			f.mu.Lock()
			f.err = runErr
			f.mu.Unlock()
		}
	}()

	select {
	case err := <-first:
		if err != nil {
			f.Stop()
			return nil, fmt.Errorf("%w: %s: %w", ErrNoCamera, o.Camera.Name, err)
		}
	case <-time.After(15 * time.Second):
		f.Stop()
		return nil, fmt.Errorf("%w: %s: the camera sent no picture", ErrNoCamera, o.Camera.Name)
	}
	return f, nil
}
