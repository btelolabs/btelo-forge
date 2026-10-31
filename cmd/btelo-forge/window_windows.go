package main

import (
	"image"
	"syscall"
	"time"
	"unsafe"

	"github.com/btelolabs/btelo-forge/internal/printing"
	"github.com/btelolabs/btelo-forge/internal/ui"
	"github.com/btelolabs/btelo-forge/internal/win"
)

// winWindow adapts the Win32 window to the editor.
type winWindow struct{ w *win.Window }

func openWindow(title string, width, height int, background bool) (window, error) {
	win.EnableDPI()
	placement := win.Foreground
	if background {
		placement = win.Background
	}
	w, err := win.Open(title, width, height, placement)
	if err != nil {
		return nil, err
	}
	return winWindow{w}, nil
}

func (h winWindow) Poll() bool                { return h.w.Poll() }
func (h winWindow) Size() (int, int)          { return h.w.Size() }
func (h winWindow) Scale() float32            { return h.w.Scale() }
func (h winWindow) Close()                    { h.w.Close() }
func (h winWindow) SetRefresh(refresh func()) { h.w.Refresh = refresh }
func (h winWindow) Wait(timeout time.Duration) {
	if timeout < 0 {
		h.w.Wait(win.Forever)
		return
	}
	h.w.Wait(uint32(max(timeout/time.Millisecond, 1)))
}
func (h winWindow) Present(rgba []byte, width, height int) error {
	return h.w.Present(rgba, width, height)
}

func (h winWindow) Input() ui.Input { return uiInput(h.w.Input()) }

// uiInput converts a window's input for the editor.
func uiInput(raw *win.Input) ui.Input {
	in := ui.Input{X: raw.X, Y: raw.Y, Down: raw.Down[win.Left], Wheel: raw.Wheel, Ctrl: raw.Ctrl, Shift: raw.Shift, Alt: raw.Alt, Up: raw.Up, Typed: raw.Typed, Dropped: raw.Dropped, Opened: raw.Opened, Hotkeys: raw.Hotkeys,
		Inactive: raw.Inactive}
	for _, p := range raw.Pointer {
		in.Pointer = append(in.Pointer, ui.Pointer{X: p.X, Y: p.Y, Right: p.Button == win.Right, Middle: p.Button == win.Middle, Down: p.Down, Double: p.Double})
	}
	for _, k := range raw.Keys {
		in.Keys = append(in.Keys, ui.Key{Code: k.VK, Ctrl: k.Ctrl, Shift: k.Shift, Alt: k.Alt})
	}
	return in
}

var cursors = map[ui.Cursor]win.Cursor{ui.CursorArrow: win.Arrow, ui.CursorHand: win.Hand, ui.CursorText: win.IBeam, ui.CursorResize: win.SizeWE, ui.CursorMove: win.SizeAll, ui.CursorCross: win.Cross, ui.CursorResizeCorner: win.SizeNWSE}

func (h winWindow) Wake()                                   { h.w.Wake() }
func (h winWindow) SetTitle(title string)                   { h.w.SetTitle(title) }
func (h winWindow) SetCursor(c ui.Cursor)                   { h.w.SetCursor(cursors[c]) }
func (h winWindow) TextInput(on bool, x, y, height float32) { h.w.TextInput(on, x, y, height) }
func (h winWindow) TextInputFont(px float32, family string) { h.w.TextInputFont(px, family) }
func (h winWindow) Clipboard() (string, error)              { return h.w.Clipboard() }
func (h winWindow) SetClipboard(text string) error          { return h.w.SetClipboard(text) }
func (h winWindow) Paste() (image.Image, []string, string)  { return h.w.Paste() }
func (h winWindow) ChooseFolder(title string) string        { return h.w.ChooseFolder(title) }
func (h winWindow) OpenFiles(title string, filter [][2]string, multiple bool) []string {
	return h.w.OpenFiles(title, filter, multiple)
}
func (h winWindow) Reveal(path string) error   { return win.Reveal(path) }
func (h winWindow) Launch(path string) error   { return win.Launch(path) }
func (h winWindow) LaunchURL(url string) error { return win.LaunchURL(url) }
func (h winWindow) Languages() []string        { return win.Languages() }

// ChoosePrinter shows the system's print dialog.
func (h winWindow) ChoosePrinter(last *printing.Target) (*printing.Target, error) {
	t, err := printing.ChoosePrinter(h.w.Handle, last)
	h.w.ForgetButtons()
	return t, err
}

// SetClipboardImage lets canvases copy pictures.
func (h winWindow) SetClipboardImage(img image.Image) error { return h.w.SetClipboardImage(img) }

// The window can keep itself out of screen recordings, get out of the way and take a system-wide hotkey, so the
// editor offers screen recording.
func (h winWindow) Minimize()                        { h.w.Minimize() }
func (h winWindow) Restore()                         { h.w.Restore() }
func (h winWindow) Hide()                            { h.w.Hide() }
func (h winWindow) Shown() bool                      { return h.w.Shown() }
func (h winWindow) ExcludeFromCapture(on bool) error { return h.w.ExcludeFromCapture(on) }
func (h winWindow) SetHotkeys(keys []ui.Hotkey) []error {
	list := make([]win.Hotkey, len(keys))
	for i, k := range keys {
		list[i] = win.Hotkey{Ctrl: k.Ctrl, Alt: k.Alt, Shift: k.Shift, VK: k.Key}
	}
	return h.w.SetHotkeys(list)
}

func alert(text string) {
	t, _ := syscall.UTF16PtrFromString(text)
	caption, _ := syscall.UTF16PtrFromString(ui.ProductName)
	const iconError = 0x10
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(caption)), iconError)
}

// OpenOverlay opens a window over the screen: the region picker, the drawing on the screen or the floating toolbar.
func (h winWindow) OpenOverlay(o ui.OverlayOptions) (ui.Overlay, error) {
	w, err := win.OpenOverlay(win.OverlayOptions{Bounds: o.Bounds, Activate: o.Activate, ClickThrough: o.ClickThrough, Excluded: o.Excluded})
	if err != nil {
		return nil, err
	}
	return winOverlay{w}, nil
}

// Pointer is where the mouse pointer is on the desktop, for opening an overlay on the display it is on.
func (h winWindow) Pointer() image.Point { return win.PointerAt() }

// winOverlay adapts an overlay window to the editor.
type winOverlay struct{ w *win.Window }

func (o winOverlay) Bounds() image.Rectangle { return o.w.Bounds() }
func (o winOverlay) Scale() float32          { return o.w.Scale() }
func (o winOverlay) SetClickThrough(on bool) { o.w.SetClickThrough(on) }
func (o winOverlay) TakeKeyboard(on bool)    { o.w.TakeKeyboard(on) }
func (o winOverlay) Raise()                  { o.w.Raise() }
func (o winOverlay) Move(r image.Rectangle)  { o.w.MoveOverlay(r) }
func (o winOverlay) SetCursor(c ui.Cursor)   { o.w.SetCursor(cursors[c]) }
func (o winOverlay) Closed() bool            { return o.w.Closed() }
func (o winOverlay) Close()                  { o.w.Close() }
func (o winOverlay) TextInput(on bool, x, y, height float32) {
	o.w.TextInput(on, x, y, height)
}
func (o winOverlay) Present(img *image.RGBA, dirty image.Rectangle) error {
	return o.w.PresentLayered(img.Pix, img.Rect.Dx(), img.Rect.Dy(), dirty)
}

func (o winOverlay) Input() ui.Input {
	raw := o.w.TakeInput()
	return uiInput(&raw)
}
