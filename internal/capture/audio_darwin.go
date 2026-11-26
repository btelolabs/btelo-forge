//go:build darwin

package capture

/*
#include <stdlib.h>
#include "capture_darwin.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime/cgo"
	"time"
	"unsafe"
)

// Microphones lists the active audio input devices on macOS, the system default first.
func Microphones() ([]AudioDevice, error) {
	var raw [64]C.cap_audio_device_info
	n := int(C.cap_list_mics(&raw[0], C.int(len(raw))))
	var list []AudioDevice
	for i := range n {
		m := AudioDevice{
			ID:      C.GoString(&raw[i].id[0]),
			Name:    C.GoString(&raw[i].name[0]),
			Default: raw[i].is_default != 0,
		}
		if m.Default {
			list = append([]AudioDevice{m}, list...)
		} else {
			list = append(list, m)
		}
	}
	return list, nil
}

func defaultMicUID() string {
	var buf [256]C.char
	if C.cap_default_mic_uid(&buf[0], C.int(len(buf))) == 0 {
		return ""
	}
	return C.GoString(&buf[0])
}

type audioCallback struct {
	origin  int64
	deliver func(samples []float32, frames int, at int64)
	onError func(err error)
}

//export goAudioSamples
func goAudioSamples(handle C.uintptr_t, samples *C.float, frames C.int, ts100ns C.int64_t) {
	cb, ok := cgo.Handle(handle).Value().(*audioCallback)
	if !ok || cb == nil || cb.deliver == nil || frames <= 0 || samples == nil {
		return
	}
	n := int(frames)
	pcm := unsafe.Slice((*float32)(unsafe.Pointer(samples)), n*channels)
	at := (int64(ts100ns) - cb.origin) * sampleRate / 10_000_000
	cb.deliver(pcm, n, at)
}

//export goAudioError
func goAudioError(handle C.uintptr_t, msg *C.char) {
	cb, ok := cgo.Handle(handle).Value().(*audioCallback)
	if !ok || cb == nil || cb.onError == nil {
		return
	}
	cb.onError(errors.New(C.GoString(msg)))
}

func ensureMicPermission() error {
	var hasDesc C.int
	status := int(C.cap_mic_auth_status(&hasDesc))
	switch status {
	case 3: // AVAuthorizationStatusAuthorized
		return nil
	case 1, 2: // Restricted or Denied
		return ErrMicPermission
	default: // 0: NotDetermined
		// Never request TCC permission when Info.plist lacks NSMicrophoneUsageDescription (e.g. go test binary),
		// because macOS terminates the process with SIGABRT.
		if hasDesc == 0 {
			return ErrMicPermission
		}
		if C.cap_request_mic_permission() == 0 {
			return ErrMicPermission
		}
		return nil
	}
}

// captureAudio captures either macOS system audio (loopback=true, via ScreenCaptureKit) or a microphone
// (loopback=false, via CoreAudio AudioQueue) until stop is closed.
func captureAudio(loopback bool, id string, origin int64, deliver func(samples []float32, frames int, at int64), opened func(error), stop <-chan struct{}) {
	if loopback {
		captureSystemAudioDarwin(origin, deliver, opened, stop)
		return
	}
	captureMicDarwin(id, origin, deliver, opened, stop)
}

func captureSystemAudioDarwin(origin int64, deliver func(samples []float32, frames int, at int64), opened func(error), stop <-chan struct{}) {
	if !isAppBundle() && !hasScreenPermission() {
		opened(ErrScreenPermission)
		return
	}
	errCh := make(chan error, 1)
	h := cgo.NewHandle(&audioCallback{
		origin:  origin,
		deliver: deliver,
		onError: func(err error) {
			select {
			case errCh <- err:
			default:
			}
		},
	})
	var errCode C.int
	var errBuf [256]C.char
	ctx := C.cap_sysaudio_start(C.uintptr_t(h), &errCode, &errBuf[0], C.int(len(errBuf)))
	if ctx == nil {
		h.Delete()
		if errCode == C.CAP_ERR_PERMISSION {
			opened(ErrScreenPermission)
			return
		}
		opened(fmt.Errorf("system audio: %s", C.GoString(&errBuf[0])))
		return
	}
	opened(nil)
	defer func() {
		C.cap_sysaudio_stop(ctx)
		h.Delete()
	}()
	select {
	case <-stop:
	case <-errCh:
		// Wait for stop so the mixer fills silence if system audio ends unexpectedly.
		<-stop
	}
}

func openMicDarwin(uid string, h cgo.Handle) (unsafe.Pointer, string, error) {
	actual := uid
	if actual == "" {
		actual = defaultMicUID()
		if actual == "" {
			return nil, "", errNoMicDevice
		}
	}
	cUID := C.CString(actual)
	defer C.free(unsafe.Pointer(cUID))
	var errCode C.int
	var errBuf [256]C.char
	ctx := C.cap_mic_start(cUID, C.uintptr_t(h), &errCode, &errBuf[0], C.int(len(errBuf)))
	if ctx == nil {
		if errCode == C.CAP_ERR_NO_DEVICE {
			return nil, "", errNoMicDevice
		}
		return nil, "", fmt.Errorf("microphone: %s", C.GoString(&errBuf[0]))
	}
	return ctx, actual, nil
}

var errNoMicDevice = errors.New("no such audio device")

func captureMicDarwin(id string, origin int64, deliver func(samples []float32, frames int, at int64), opened func(error), stop <-chan struct{}) {
	if err := ensureMicPermission(); err != nil {
		opened(err)
		return
	}
	h := cgo.NewHandle(&audioCallback{
		origin:  origin,
		deliver: deliver,
	})
	ctx, curUID, err := openMicDarwin(id, h)
	if err != nil && (id != "" || !errors.Is(err, errNoMicDevice)) {
		h.Delete()
		opened(err)
		return
	}
	opened(nil)
	defer func() {
		if ctx != nil {
			C.cap_mic_stop(ctx)
		}
		h.Delete()
	}()

	// Follow default microphone changes when id == "", and retry if a device was unplugged.
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		if ctx == nil {
			ctx, curUID, err = openMicDarwin(id, h)
			if err != nil {
				ctx = nil
			}
			continue
		}
		if id == "" {
			if def := defaultMicUID(); def != "" && def != curUID {
				C.cap_mic_stop(ctx)
				ctx, curUID, err = openMicDarwin("", h)
				if err != nil {
					ctx = nil
				}
			}
		}
	}
}
