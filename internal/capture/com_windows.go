package capture

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	ole32           = syscall.NewLazyDLL("ole32.dll")
	coInitialize    = ole32.NewProc("CoInitializeEx")
	coUninitialize  = ole32.NewProc("CoUninitialize")
	coCreate        = ole32.NewProc("CoCreateInstance")
	coTaskMemFree   = ole32.NewProc("CoTaskMemFree")
	propClear       = ole32.NewProc("PropVariantClear")
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	queryCounter    = kernel32.NewProc("QueryPerformanceCounter")
	queryFrequency  = kernel32.NewProc("QueryPerformanceFrequency")
	preciseFileTime = kernel32.NewProc("GetSystemTimePreciseAsFileTime")
)

type guid struct {
	Data1        uint32
	Data2, Data3 uint16
	Data4        [8]byte
}

// object is a COM interface pointer: its first word points at the method table.
type object struct{ vtbl *[32]uintptr }

// call invokes method i of o, whose first argument is o itself, and returns the HRESULT.
func (o *object) call(i int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(o.vtbl[i], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return r
}

func (o *object) release() {
	if o != nil {
		o.call(2)
	}
}

// hresult turns a failed HRESULT into an error naming the call.
func hresult(op string, hr uintptr) error {
	if int32(hr) >= 0 {
		return nil
	}
	return &comError{op: op, code: uint32(hr)}
}

type comError struct {
	op   string
	code uint32
}

func (e *comError) Error() string { return fmt.Sprintf("%s: 0x%08X", e.op, e.code) }

// comApartment joins the calling OS thread to the multithreaded apartment; the caller has locked the thread and
// calls the returned function when done.
func comApartment() (func(), error) {
	const multithreaded, changedMode = 0x0, 0x80010106
	hr, _, _ := coInitialize.Call(0, multithreaded)
	if uint32(hr) == changedMode {
		return func() {}, nil
	}
	if err := hresult("CoInitializeEx", hr); err != nil {
		return nil, err
	}
	return func() { coUninitialize.Call() }, nil
}

// counter is the performance counter, the clock WASAPI stamps captured audio with.
func counter() int64 {
	var c int64
	queryCounter.Call(uintptr(unsafe.Pointer(&c)))
	return c
}

func counterFrequency() int64 {
	var f int64
	queryFrequency.Call(uintptr(unsafe.Pointer(&f)))
	return f
}

// wallMicros is the precise system time in microseconds since 1970, the clock ffmpeg's RTCTIME reads.
func wallMicros() int64 {
	var ft int64 // 100 ns since 1601
	preciseFileTime.Call(uintptr(unsafe.Pointer(&ft)))
	const epochDelta = 116444736000000000
	return (ft - epochDelta) / 10
}

func utf16At(p *uint16) string {
	if p == nil {
		return ""
	}
	var s []uint16
	for ptr := unsafe.Pointer(p); ; ptr = unsafe.Add(ptr, 2) {
		c := *(*uint16)(ptr)
		if c == 0 {
			break
		}
		s = append(s, c)
	}
	return syscall.UTF16ToString(s)
}
