package win

import (
	"fmt"
	"math"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// Button is a mouse button.
type Button int

const (
	Left Button = iota
	Right
	Middle
)

// PointerEvent is a button transition at a client-area position.
type PointerEvent struct {
	X, Y   float32
	Button Button
	Down   bool
	Double bool
}

// KeyEvent is a key press with the modifiers held at that moment; VK is the Windows virtual-key code.
type KeyEvent struct {
	VK               uint16
	Ctrl, Shift, Alt bool
}

// Input holds the state at the last Poll and the events since the Poll before it.
type Input struct {
	X, Y     float32
	Down     [3]bool
	Pointer  []PointerEvent
	Wheel    float32
	Keys     []KeyEvent
	Up       []uint16 // the keys let go, as virtual-key codes; modifiers alone are not reported
	Typed    []rune
	Dropped  []string
	Opened   []string // files another start of the app handed over (TakeFiles)
	Hotkeys  []int    // indexes, in the list given to SetHotkeys, of the hotkeys pressed
	Woken    bool
	Inactive bool // the window does not have the keyboard focus
	Ctrl     bool
	Shift    bool
	Alt      bool
}

var (
	imm32                   = syscall.NewLazyDLL("imm32.dll")
	immAssociateContextEx   = imm32.NewProc("ImmAssociateContextEx")
	immGetContext           = imm32.NewProc("ImmGetContext")
	immReleaseContext       = imm32.NewProc("ImmReleaseContext")
	immSetCompositionWindow = imm32.NewProc("ImmSetCompositionWindow")
	immSetCandidateWindow   = imm32.NewProc("ImmSetCandidateWindow")
	shell32                 = syscall.NewLazyDLL("shell32.dll")
	dragAcceptFiles         = shell32.NewProc("DragAcceptFiles")
	dragQueryFile           = shell32.NewProc("DragQueryFileW")
	dragFinish              = shell32.NewProc("DragFinish")
	changeMessageFilter     = user32.NewProc("ChangeWindowMessageFilterEx")
	setCursorProc           = user32.NewProc("SetCursor")
	loadCursor              = user32.NewProc("LoadCursorW")
)

func (w *Window) mouse(msg uint32, wp, lp uintptr) bool {
	at := func() (float32, float32) { return float32(int16(lp)), float32(int16(lp >> 16)) }
	press := func(b Button, down, double bool) {
		w.input.X, w.input.Y = at()
		w.input.Down[b] = down
		w.input.Pointer = append(w.input.Pointer, PointerEvent{X: w.input.X, Y: w.input.Y, Button: b, Down: down, Double: double})
		if down {
			setCapture.Call(w.Handle)
		} else if !w.input.Down[Left] && !w.input.Down[Right] && !w.input.Down[Middle] {
			releaseCapture.Call()
		}
	}
	switch msg {
	case 0x0200: // WM_MOUSEMOVE
		w.input.X, w.input.Y = at()
	case 0x0201, 0x0202, 0x0203: // WM_LBUTTONDOWN / UP / DBLCLK
		press(Left, msg != 0x0202, msg == 0x0203)
	case 0x0204, 0x0205, 0x0206:
		press(Right, msg != 0x0205, msg == 0x0206)
	case 0x0207, 0x0208, 0x0209:
		press(Middle, msg != 0x0208, msg == 0x0209)
	case 0x020a: // WM_MOUSEWHEEL
		w.input.Wheel += float32(int16(wp>>16)) / 120
	case 0x0007: // WM_SETFOCUS
		w.input.Inactive = false
		return false
	case 0x0008, 0x0215: // WM_KILLFOCUS / WM_CAPTURECHANGED release buttons another window took over
		if msg == 0x0008 || lp != w.Handle {
			for b := range w.input.Down {
				if w.input.Down[b] {
					w.input.Pointer = append(w.input.Pointer, PointerEvent{X: w.input.X, Y: w.input.Y, Button: Button(b)})
				}
			}
			w.input.Down = [3]bool{}
		}
		if msg == 0x0008 {
			w.input.Inactive = true
		}
		return false
	default:
		return false
	}
	return true
}

func (w *Window) key(msg uint32, wp uintptr) bool {
	switch msg {
	case 0x0100, 0x0104: // WM_KEYDOWN / WM_SYSKEYDOWN
	case 0x0101, 0x0105: // WM_KEYUP / WM_SYSKEYUP
		if vk := uint16(wp); vk != 0xe5 && vk != 0x12 && vk != 0x10 && vk != 0x11 {
			w.input.Up = append(w.input.Up, vk)
		}
		return msg == 0x0101
	default:
		return false
	}
	vk := uint16(wp)
	if vk == 0xe5 { // VK_PROCESSKEY: the IME owns this key
		return false
	}
	if vk == 0x12 || vk == 0x10 || vk == 0x11 { // Alt, Shift, Ctrl alone
		return msg == 0x0100
	}
	// WM_SYSKEYDOWN also comes for F10 alone, so Alt is read from the keyboard.
	alt := held(0x12)
	if alt && vk == 0x73 { // Alt+F4 stays with the system
		return false
	}
	w.input.Keys = append(w.input.Keys, KeyEvent{VK: vk, Ctrl: held(0x11), Shift: held(0x10), Alt: alt})
	return msg == 0x0100
}

// char collects WM_CHAR while text input is on; the IME delivers committed text through it as well.
func (w *Window) char(msg uint32, wp uintptr) bool {
	if msg != 0x0102 {
		return false
	}
	if !w.typing {
		return true
	}
	u := rune(uint16(wp))
	switch {
	case utf16.IsSurrogate(u) && u < 0xdc00:
		w.high = uint16(u)
		return true
	case utf16.IsSurrogate(u):
		if w.high == 0 {
			return true
		}
		u = utf16.DecodeRune(rune(w.high), u)
	}
	w.high = 0
	if u < 0x20 || u == 0x7f {
		return true
	}
	w.input.Typed = append(w.input.Typed, u)
	return true
}

// acceptFiles lets Explorer drop files on the window; when elevated, the low-integrity drop messages are let through too.
func acceptFiles(hwnd uintptr) error {
	for _, m := range [...]uintptr{0x0233, 0x004a, 0x0049} {
		const allow = 1
		if r, _, e := changeMessageFilter.Call(hwnd, m, allow, 0); r == 0 {
			return fmt.Errorf("ChangeWindowMessageFilterEx(%#x): %w", m, e)
		}
	}
	dragAcceptFiles.Call(hwnd, 1)
	return nil
}

func (w *Window) drop(msg uint32, wp uintptr) bool {
	if msg != 0x0233 { // WM_DROPFILES
		return false
	}
	defer dragFinish.Call(wp)
	count, _, _ := dragQueryFile.Call(wp, 0xffffffff, 0, 0)
	for i := uintptr(0); i < count; i++ {
		n, _, _ := dragQueryFile.Call(wp, i, 0, 0)
		if n == 0 {
			continue
		}
		buf := make([]uint16, n+1)
		if got, _, _ := dragQueryFile.Call(wp, i, uintptr(unsafe.Pointer(&buf[0])), n+1); got != 0 {
			w.input.Dropped = append(w.input.Dropped, syscall.UTF16ToString(buf))
		}
	}
	return true
}

func associateIME(hwnd uintptr, on bool) error {
	const iaceDefault = 0x0010
	flags := uintptr(0)
	if on {
		flags = iaceDefault
	}
	if r, _, e := immAssociateContextEx.Call(hwnd, 0, flags); r == 0 {
		return fmt.Errorf("ImmAssociateContextEx(%t): %w", on, e)
	}
	return nil
}

// TextInput turns text entry on or off; while on the IME is attached and its windows sit at the caret (client pixels).
func (w *Window) TextInput(on bool, x, y, height float32) {
	if on != w.typing {
		if associateIME(w.Handle, on) != nil {
			return
		}
		w.typing, w.high = on, 0
	}
	if !on {
		return
	}
	ctx, _, _ := immGetContext.Call(w.Handle)
	if ctx == 0 {
		return
	}
	defer immReleaseContext.Call(w.Handle, ctx)
	type point struct{ X, Y int32 }
	cx, cy, ch := int32(math.Round(float64(x))), int32(math.Round(float64(y))), int32(math.Round(float64(height)))
	comp := struct {
		Style uint32
		Pos   point
		Area  rect
	}{Style: 0x0002, Pos: point{cx, cy}}
	immSetCompositionWindow.Call(ctx, uintptr(unsafe.Pointer(&comp)))
	cand := struct {
		Index, Style uint32
		Pos          point
		Area         rect
	}{Style: 0x0080, Pos: point{cx, cy + ch}, Area: rect{cx, cy, cx + 1, cy + ch}}
	immSetCandidateWindow.Call(ctx, uintptr(unsafe.Pointer(&cand)))
}

// Cursor is a system cursor id (IDC_*).
type Cursor uintptr

const (
	Arrow    Cursor = 32512
	IBeam    Cursor = 32513
	Wait     Cursor = 32514
	SizeNWSE Cursor = 32642
	SizeWE   Cursor = 32644
	SizeAll  Cursor = 32646
	Hand     Cursor = 32649
)

// SetCursor changes the cursor shown over the client area.
func (w *Window) SetCursor(c Cursor) {
	if c == w.cursor {
		return
	}
	w.cursor = c
	w.applyCursor()
}

func (w *Window) applyCursor() {
	h, _, _ := loadCursor.Call(0, uintptr(w.cursor))
	setCursorProc.Call(h)
}
