package main

import (
	"image"
	"time"

	"github.com/btelolabs/btelo-forge/internal/mac"
	"github.com/btelolabs/btelo-forge/internal/printing"
	"github.com/btelolabs/btelo-forge/internal/ui"
)

// macWindow adapts the Cocoa window to the editor.
type macWindow struct{ w *mac.Window }

func openWindow(title string, width, height int, background bool) (window, error) {
	w, err := mac.Open(title, width, height, background)
	if err != nil {
		return nil, err
	}
	return macWindow{w}, nil
}

// OpenWebView opens the system's browser engine in the window, for mail.
func (h macWindow) OpenWebView(_ string, wake func()) (ui.WebView, error) {
	return h.w.OpenWebView(wake), nil
}

func (h macWindow) Poll() bool                 { return h.w.Poll() }
func (h macWindow) Size() (int, int)           { return h.w.Size() }
func (h macWindow) Scale() float32             { return h.w.Scale() }
func (h macWindow) Close()                     { h.w.Close() }
func (h macWindow) SetRefresh(refresh func())  { h.w.SetRefresh(refresh) }
func (h macWindow) Wait(timeout time.Duration) { h.w.Wait(timeout) }
func (h macWindow) Present(rgba []byte, width, height int) error {
	return h.w.Present(rgba, width, height)
}

func uiInput(raw *mac.Input, inactive bool) ui.Input {
	in := ui.Input{
		X: raw.X, Y: raw.Y, Down: raw.Down, Wheel: raw.Wheel,
		Ctrl: raw.Ctrl, Shift: raw.Shift, Alt: raw.Alt,
		Up: raw.Up, Typed: raw.Typed, Dropped: raw.Dropped, Opened: raw.Opened, Hotkeys: raw.Hotkeys,
		Inactive: inactive,
	}
	for _, p := range raw.Pointer {
		in.Pointer = append(in.Pointer, ui.Pointer{X: p.X, Y: p.Y, Right: p.Right, Middle: p.Middle, Down: p.Down, Double: p.Double})
	}
	for _, k := range raw.Keys {
		in.Keys = append(in.Keys, ui.Key{Code: k.VK, Ctrl: k.Ctrl, Shift: k.Shift, Alt: k.Alt})
	}
	return in
}

func (h macWindow) Input() ui.Input {
	return uiInput(h.w.Input(), !h.w.Focused())
}

var cursors = map[ui.Cursor]int{
	ui.CursorArrow:        mac.Arrow,
	ui.CursorHand:         mac.Hand,
	ui.CursorText:         mac.IBeam,
	ui.CursorResize:       mac.ResizeLeftRight,
	ui.CursorMove:         mac.Move,
	ui.CursorCross:        mac.Cross,
	ui.CursorResizeCorner: mac.ResizeCorner,
	ui.CursorNone:         mac.Hidden,
}

func (h macWindow) Wake()                                   { h.w.Wake() }
func (h macWindow) SetTitle(title string)                   { h.w.SetTitle(title) }
func (h macWindow) SetCursor(c ui.Cursor)                   { h.w.SetCursor(cursors[c]) }
func (h macWindow) TextInput(on bool, x, y, height float32) { h.w.TextInput(on, x, y, height) }
func (h macWindow) TextInputFont(px float32, family string) { h.w.TextInputFont(px, family) }
func (h macWindow) Clipboard() (string, error)              { return h.w.Clipboard() }
func (h macWindow) SetClipboard(text string) error          { return h.w.SetClipboard(text) }
func (h macWindow) SetClipboardImage(img image.Image) error { return h.w.SetClipboardImage(img) }
func (h macWindow) Paste() (image.Image, []string, string)  { return h.w.Paste() }
func (h macWindow) ChooseFolder(title string) string        { return h.w.ChooseFolder(title) }
func (h macWindow) OpenFiles(title string, filter [][2]string, multiple bool) []string {
	return h.w.OpenFiles(title, filter, multiple)
}
func (h macWindow) Reveal(path string) error   { return mac.Reveal(path) }
func (h macWindow) Launch(path string) error   { return mac.Launch(path) }
func (h macWindow) LaunchURL(url string) error { return mac.LaunchURL(url) }
func (h macWindow) Languages() []string        { return mac.Languages() }
func (h macWindow) SystemDark() bool           { return mac.SystemDark() }
func (h macWindow) SetDark(dark bool)          { h.w.SetDark(dark) }

// ChoosePrinter shows the native macOS print dialog.
func (h macWindow) ChoosePrinter(last *printing.Target) (*printing.Target, error) {
	t, err := printing.ChoosePrinter(0, last)
	h.w.ForgetButtons()
	return t, err
}

// Recorder host methods: window visibility, capture exclusion and system-wide hotkeys.
func (h macWindow) Minimize()                        { h.w.Minimize() }
func (h macWindow) Restore()                         { h.w.Restore() }
func (h macWindow) Hide()                            { h.w.Hide() }
func (h macWindow) Shown() bool                      { return h.w.Shown() }
func (h macWindow) ExcludeFromCapture(on bool) error { return h.w.ExcludeFromCapture(on) }
func (h macWindow) SetHotkeys(keys []ui.Hotkey) []error {
	list := make([]mac.Hotkey, len(keys))
	for i, k := range keys {
		list[i] = mac.Hotkey{Ctrl: k.Ctrl, Alt: k.Alt, Shift: k.Shift, VK: k.Key}
	}
	return h.w.SetHotkeys(list)
}

// OpenOverlay opens a transparent borderless always-on-top panel over the screen.
func (h macWindow) OpenOverlay(o ui.OverlayOptions) (ui.Overlay, error) {
	ov, err := mac.OpenOverlay(mac.OverlayOptions{Bounds: o.Bounds, Activate: o.Activate, ClickThrough: o.ClickThrough, Excluded: o.Excluded})
	if err != nil {
		return nil, err
	}
	return macOverlay{o: ov}, nil
}

// Pointer is where the mouse pointer is on the desktop, in physical pixels.
func (h macWindow) Pointer() image.Point { return mac.PointerAt() }

// macOverlay adapts a Cocoa overlay panel to the editor.
type macOverlay struct{ o *mac.Overlay }

func (o macOverlay) Bounds() image.Rectangle { return o.o.Bounds() }
func (o macOverlay) Scale() float32          { return o.o.Scale() }
func (o macOverlay) SetClickThrough(on bool) { o.o.SetClickThrough(on) }
func (o macOverlay) TakeKeyboard(on bool)    { o.o.TakeKeyboard(on) }
func (o macOverlay) Raise()                  { o.o.Raise() }
func (o macOverlay) Move(r image.Rectangle)  { o.o.MoveOverlay(r) }
func (o macOverlay) SetCursor(c ui.Cursor)   { o.o.SetCursor(cursors[c]) }
func (o macOverlay) Closed() bool            { return o.o.Closed() }
func (o macOverlay) Close()                  { o.o.Close() }
func (o macOverlay) TextInput(on bool, x, y, height float32) {
	o.o.TextInput(on, x, y, height)
}
func (o macOverlay) Present(img *image.RGBA, dirty image.Rectangle) error {
	return o.o.PresentLayered(img.Pix, img.Rect.Dx(), img.Rect.Dy(), dirty)
}
func (o macOverlay) Input() ui.Input {
	raw := o.o.TakeInput()
	return uiInput(&raw, false)
}

func alert(text string) { mac.Alert(text) }
