package main

import (
	"image"
	"syscall"
	"time"
	"unsafe"

	"github.com/btelolabs/btelo-video-cut/internal/ui"
	"github.com/btelolabs/btelo-video-cut/internal/win"
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

func (h winWindow) Poll() bool                 { return h.w.Poll() }
func (h winWindow) Size() (int, int)           { return h.w.Size() }
func (h winWindow) Scale() float32             { return h.w.Scale() }
func (h winWindow) Close()                     { h.w.Close() }
func (h winWindow) SetRefresh(refresh func())  { h.w.Refresh = refresh }
func (h winWindow) Wait(timeout time.Duration) { h.w.Wait(uint32(max(timeout/time.Millisecond, 1))) }
func (h winWindow) Present(rgba []byte, width, height int) error {
	return h.w.Present(rgba, width, height)
}

func (h winWindow) Input() ui.Input {
	raw := h.w.Input()
	in := ui.Input{X: raw.X, Y: raw.Y, Down: raw.Down[win.Left], Wheel: raw.Wheel, Ctrl: raw.Ctrl, Shift: raw.Shift, Typed: raw.Typed, Dropped: raw.Dropped}
	for _, p := range raw.Pointer {
		if p.Button == win.Middle {
			continue
		}
		in.Pointer = append(in.Pointer, ui.Pointer{X: p.X, Y: p.Y, Right: p.Button == win.Right, Down: p.Down, Double: p.Double})
	}
	for _, k := range raw.Keys {
		in.Keys = append(in.Keys, ui.Key{Code: k.VK, Ctrl: k.Ctrl, Shift: k.Shift, Alt: k.Alt})
	}
	return in
}

var cursors = map[ui.Cursor]win.Cursor{ui.CursorArrow: win.Arrow, ui.CursorHand: win.Hand, ui.CursorText: win.IBeam, ui.CursorResize: win.SizeWE, ui.CursorMove: win.SizeAll}

func (h winWindow) Wake()                                   { h.w.Wake() }
func (h winWindow) SetTitle(title string)                   { h.w.SetTitle(title) }
func (h winWindow) SetCursor(c ui.Cursor)                   { h.w.SetCursor(cursors[c]) }
func (h winWindow) TextInput(on bool, x, y, height float32) { h.w.TextInput(on, x, y, height) }
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

func alert(text string) {
	t, _ := syscall.UTF16PtrFromString(text)
	caption, _ := syscall.UTF16PtrFromString(ui.ProductName)
	const iconError = 0x10
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(caption)), iconError)
}
