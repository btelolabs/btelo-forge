package win

import (
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// Files opened from Explorer go to the app already running, as tabs of its window, rather than each starting a
// window of its own: the new process finds the window that takes them, hands the paths over and quits.

var (
	findWindowEx         = user32.NewProc("FindWindowExW")
	setProp              = user32.NewProc("SetPropW")
	getProp              = user32.NewProc("GetPropW")
	sendMessageTimeout   = user32.NewProc("SendMessageTimeoutW")
	getWindowThreadPID   = user32.NewProc("GetWindowThreadProcessId")
	allowSetForeground   = user32.NewProc("AllowSetForegroundWindow")
	isIconic             = user32.NewProc("IsIconic")
	takesFilesProp, _    = syscall.UTF16PtrFromString("BteloForge.TakesFiles")
	forwardedFilesMarker = uintptr(0x42464f50) // "BFOP", so a stray WM_COPYDATA is not taken for files
)

const wmCopyData = 0x004a

// copyDataStruct is COPYDATASTRUCT.
type copyDataStruct struct {
	dwData uintptr
	cbData uint32
	lpData uintptr
}

// TakeFiles makes this window the one later starts of the app hand their files to; they arrive in Input.Opened.
func (w *Window) TakeFiles() {
	setProp.Call(w.Handle, uintptr(unsafe.Pointer(takesFilesProp)), 1)
}

// ForwardFiles hands paths to the running app's window, bringing it forward; it reports false when no window
// takes files, or it did not answer in time.
func ForwardFiles(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	var target uintptr
	for h := uintptr(0); ; {
		h, _, _ = findWindowEx.Call(0, h, uintptr(unsafe.Pointer(className)), 0)
		if h == 0 {
			break
		}
		if v, _, _ := getProp.Call(h, uintptr(unsafe.Pointer(takesFilesProp))); v != 0 {
			target = h
			break
		}
	}
	if target == 0 {
		return false
	}
	units := utf16.Encode([]rune(strings.Join(paths, "\x00")))
	units = append(units, 0)
	cds := copyDataStruct{dwData: forwardedFilesMarker, cbData: uint32(len(units) * 2), lpData: uintptr(unsafe.Pointer(&units[0]))}
	// The running app may come to the front only when this process, which has the focus, lets it.
	var pid uint32
	getWindowThreadPID.Call(target, uintptr(unsafe.Pointer(&pid)))
	allowSetForeground.Call(uintptr(pid))
	const abortIfHung = 0x0002
	var answer uintptr
	r, _, _ := sendMessageTimeout.Call(target, wmCopyData, 0, uintptr(unsafe.Pointer(&cds)), abortIfHung, 5000, uintptr(unsafe.Pointer(&answer)))
	return r != 0 && answer == 1
}

// takeForwarded reads files handed over by another start of the app; it reports whether the message was that.
func (w *Window) takeForwarded(lp uintptr) bool {
	if lp == 0 {
		return false
	}
	// The message's memory belongs to the sender: it is copied, as the window procedure does with its other structs.
	var cds copyDataStruct
	moveMemory.Call(uintptr(unsafe.Pointer(&cds)), lp, unsafe.Sizeof(cds))
	if cds.dwData != forwardedFilesMarker || cds.cbData < 2 || cds.cbData%2 != 0 || cds.cbData > 1<<20 {
		return false
	}
	units := make([]uint16, cds.cbData/2)
	moveMemory.Call(uintptr(unsafe.Pointer(&units[0])), cds.lpData, uintptr(cds.cbData))
	for _, p := range strings.Split(strings.TrimRight(string(utf16.Decode(units)), "\x00"), "\x00") {
		if p != "" {
			w.input.Opened = append(w.input.Opened, p)
		}
	}
	w.Restore()
	return true
}
