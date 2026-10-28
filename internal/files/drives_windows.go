package files

import (
	"syscall"
	"unsafe"
)

var (
	getLogicalDrives = kernel32.NewProc("GetLogicalDrives")
	getDiskFreeSpace = kernel32.NewProc("GetDiskFreeSpaceExW")
	getVolumeInfo    = kernel32.NewProc("GetVolumeInformationW")
	setErrorMode     = kernel32.NewProc("SetErrorMode")
)

// Drives lists the drive letters in use, with their names and space. A drive without a disc or card in it is
// listed without asking Windows to show a dialog about it.
func Drives() []Drive {
	mask, _, _ := getLogicalDrives.Call()
	const failCriticalErrors = 0x1
	old, _, _ := setErrorMode.Call(failCriticalErrors)
	defer setErrorMode.Call(old)
	var out []Drive
	for i := range 26 {
		if mask&(1<<i) == 0 {
			continue
		}
		name := string(rune('A'+i)) + ":"
		d := Drive{Path: name + `\`, Name: name}
		root, _ := syscall.UTF16PtrFromString(d.Path)
		var free, total, totalFree uint64
		if r, _, _ := getDiskFreeSpace.Call(uintptr(unsafe.Pointer(root)), uintptr(unsafe.Pointer(&free)),
			uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&totalFree))); r != 0 {
			d.Total, d.Free = int64(total), int64(free)
		}
		label := make([]uint16, 261)
		if r, _, _ := getVolumeInfo.Call(uintptr(unsafe.Pointer(root)), uintptr(unsafe.Pointer(&label[0])), uintptr(len(label)),
			0, 0, 0, 0, 0); r != 0 {
			d.Label = syscall.UTF16ToString(label)
		}
		out = append(out, d)
	}
	return out
}
