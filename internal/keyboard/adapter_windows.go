package keyboard

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")

	setHook       = user32.NewProc("SetWindowsHookExW")
	unhook        = user32.NewProc("UnhookWindowsHookEx")
	callNextHook  = user32.NewProc("CallNextHookEx")
	getMessage    = user32.NewProc("GetMessageW")
	postThread    = user32.NewProc("PostThreadMessageW")
	setTimer      = user32.NewProc("SetTimer")
	killTimer     = user32.NewProc("KillTimer")
	sendInput     = user32.NewProc("SendInput")
	getAsyncKey   = user32.NewProc("GetAsyncKeyState")
	mapVirtualKey = user32.NewProc("MapVirtualKeyW")

	currentThread = kernel32.NewProc("GetCurrentThreadId")

	shellExecute = shell32.NewProc("ShellExecuteW")
)

// rehookEvery is how often the hook is put back. Windows takes away a hook that once answers too late, without a
// word, so a stall of the whole computer would otherwise end the rules for the rest of the run.
const rehookEvery = 30_000 // milliseconds

type windowsAdapter struct {
	mu     sync.Mutex
	thread uint32        // of the running watch, 0 for none
	done   chan struct{} // closed when its thread has left
}

// NewAdapter is the system's adapter.
func NewAdapter() Adapter { return &windowsAdapter{} }

// hookHandler is the watch in progress: Windows calls hookProc with no context, and there is one hook at a time.
var (
	hookHandler atomic.Pointer[func(uint16, bool) bool]
	// acceptSent makes the hook take keys sent by programs too, so a test can stand in for a keyboard.
	acceptSent atomic.Bool
	hookProc   = syscall.NewCallback(hookCallback)
)

type hookEvent struct {
	vk, scan, flags, time uint32
	extra                 uintptr
}

func hookCallback(code int32, wparam, lparam uintptr) uintptr {
	const keyDown, keyUp, sysKeyDown, sysKeyUp, sent = 0x100, 0x101, 0x104, 0x105, 0x10
	if code == 0 {
		if handle := hookHandler.Load(); handle != nil {
			ev := *(**hookEvent)(unsafe.Pointer(&lparam))
			if ev.flags&sent == 0 || acceptSent.Load() {
				switch wparam {
				case keyDown, sysKeyDown, keyUp, sysKeyUp:
					if (*handle)(uint16(ev.vk), wparam == keyDown || wparam == sysKeyDown) {
						return 1
					}
				}
			}
		}
	}
	r, _, _ := callNextHook.Call(0, uintptr(code), wparam, lparam)
	return r
}

func install() (uintptr, error) {
	const lowLevelKeyboard = 13
	h, _, err := setHook.Call(lowLevelKeyboard, hookProc, 0, 0)
	if h == 0 {
		return 0, fmt.Errorf("SetWindowsHookEx: %w", err)
	}
	return h, nil
}

// Start puts the hook on a thread of its own, which does nothing else, so that nothing the app does can delay a key.
func (a *windowsAdapter) Start(handle func(uint16, bool) bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.thread != 0 {
		return nil
	}
	hookHandler.Store(&handle)
	type started struct {
		thread uint32
		err    error
	}
	ready := make(chan started, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		hook, err := install()
		id, _, _ := currentThread.Call()
		ready <- started{uint32(id), err}
		if err != nil {
			return
		}
		timer, _, _ := setTimer.Call(0, 0, rehookEvery, 0)
		defer killTimer.Call(0, timer)
		var msg struct {
			window         uintptr
			message        uint32
			wparam, lparam uintptr
			time           uint32
			x, y           int32
			private        uint32
		}
		for {
			r, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if r == 0 || int32(r) == -1 {
				break
			}
			const timerMessage = 0x0113
			if msg.message == timerMessage {
				unhook.Call(hook)
				if hook, err = install(); err != nil {
					hook = 0
					break
				}
			}
		}
		if hook != 0 {
			unhook.Call(hook)
		}
	}()
	s := <-ready
	if s.err != nil {
		hookHandler.Store(nil)
		return s.err
	}
	a.thread, a.done = s.thread, done
	return nil
}

func (a *windowsAdapter) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.thread == 0 {
		return
	}
	const quit = 0x0012
	postThread.Call(uintptr(a.thread), quit, 0, 0)
	<-a.done
	hookHandler.Store(nil)
	a.thread = 0
}

// keyInput is an INPUT holding a KEYBDINPUT, padded to the size of the union's largest member.
type keyInput struct {
	kind       uint32
	_          uint32
	vk, scan   uint16
	flags      uint32
	time       uint32
	extra      uintptr
	_, padding uint32
}

const (
	extendedKey = 0x1
	keyRelease  = 0x2
	unicodeKey  = 0x4
)

// extended reports whether a key's scan code has the E0 prefix, which Windows must be told of.
func extended(vk uint16) bool {
	switch vk {
	case 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x2c, 0x2d, 0x2e, 0x5b, 0x5c, 0x5d, 0x6f, 0x90, vkRCtrl, vkRAlt:
		return true
	}
	return false
}

func send(in []keyInput) error {
	for len(in) > 0 {
		n := min(len(in), 256)
		r, _, err := sendInput.Call(uintptr(n), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0]))
		if int(r) != n {
			return fmt.Errorf("SendInput: %w", err)
		}
		in = in[n:]
	}
	return nil
}

func (a *windowsAdapter) Send(keys []Stroke) error {
	in := make([]keyInput, len(keys))
	for i, k := range keys {
		const virtualToScan = 0
		scan, _, _ := mapVirtualKey.Call(uintptr(k.VK), virtualToScan)
		in[i] = keyInput{vk: k.VK, scan: uint16(scan), kind: 1}
		if extended(k.VK) {
			in[i].flags |= extendedKey
		}
		if !k.Down {
			in[i].flags |= keyRelease
		}
	}
	return send(in)
}

func (a *windowsAdapter) Type(text string) error { return send(typeInputs(text)) }

// typeInputs are the key events that enter text; a line break is the Enter key, as programs expect.
func typeInputs(text string) []keyInput {
	var in []keyInput
	for _, c := range text {
		if c == '\n' {
			const enter = 0x0d
			in = append(in, keyInput{kind: 1, vk: enter}, keyInput{kind: 1, vk: enter, flags: keyRelease})
			continue
		}
		units, _ := syscall.UTF16FromString(string(c))
		for _, unit := range units[:len(units)-1] { // without the closing zero
			in = append(in, keyInput{kind: 1, scan: unit, flags: unicodeKey}, keyInput{kind: 1, scan: unit, flags: unicodeKey | keyRelease})
		}
	}
	return in
}

func (a *windowsAdapter) Open(target, args string) error {
	verb, _ := syscall.UTF16PtrFromString("open")
	file, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	params, err := syscall.UTF16PtrFromString(args)
	if err != nil {
		return err
	}
	const showNormal, firstSuccess = 1, 32
	if r, _, e := shellExecute.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), uintptr(unsafe.Pointer(params)), 0, showNormal); r <= firstSuccess {
		return fmt.Errorf("%s: %w", target, e)
	}
	return nil
}

func (a *windowsAdapter) KeyDown(vk uint16) bool {
	r, _, _ := getAsyncKey.Call(uintptr(vk))
	return r&0x8000 != 0
}

func (a *windowsAdapter) CanRemap(uint16) bool { return true }
