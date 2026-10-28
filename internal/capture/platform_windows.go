package capture

import (
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
