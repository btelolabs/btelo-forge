package main

import (
	"image"
	"time"

	"github.com/btelolabs/btelo-forge/internal/mac"
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

func (h macWindow) Poll() bool                 { return h.w.Poll() }
func (h macWindow) Size() (int, int)           { return h.w.Size() }
func (h macWindow) Scale() float32             { return h.w.Scale() }
func (h macWindow) Close()                     { h.w.Close() }
func (h macWindow) SetRefresh(refresh func())  { h.w.SetRefresh(refresh) }
func (h macWindow) Wait(timeout time.Duration) { h.w.Wait(timeout) }
func (h macWindow) Present(rgba []byte, width, height int) error {
	return h.w.Present(rgba, width, height)
}

func (h macWindow) Input() ui.Input {
	raw := h.w.Input()
	in := ui.Input{X: raw.X, Y: raw.Y, Down: raw.Down, Wheel: raw.Wheel, Ctrl: raw.Ctrl, Shift: raw.Shift, Up: raw.Up, Typed: raw.Typed, Dropped: raw.Dropped,
		Inactive: !h.w.Focused()}
	for _, p := range raw.Pointer {
		in.Pointer = append(in.Pointer, ui.Pointer{X: p.X, Y: p.Y, Right: p.Right, Middle: p.Middle, Down: p.Down, Double: p.Double})
	}
	for _, k := range raw.Keys {
		in.Keys = append(in.Keys, ui.Key{Code: k.VK, Ctrl: k.Ctrl, Shift: k.Shift, Alt: k.Alt})
	}
	return in
}

var cursors = map[ui.Cursor]int{ui.CursorArrow: mac.Arrow, ui.CursorHand: mac.Hand, ui.CursorText: mac.IBeam, ui.CursorResize: mac.ResizeLeftRight, ui.CursorMove: mac.Move, ui.CursorResizeCorner: mac.Move}

func (h macWindow) Wake()                                   { h.w.Wake() }
func (h macWindow) SetTitle(title string)                   { h.w.SetTitle(title) }
func (h macWindow) SetCursor(c ui.Cursor)                   { h.w.SetCursor(cursors[c]) }
func (h macWindow) TextInput(on bool, x, y, height float32) { h.w.TextInput(on, x, y, height) }
func (h macWindow) Clipboard() (string, error)              { return h.w.Clipboard() }
func (h macWindow) SetClipboard(text string) error          { return h.w.SetClipboard(text) }
func (h macWindow) Paste() (image.Image, []string, string)  { return h.w.Paste() }
func (h macWindow) ChooseFolder(title string) string        { return h.w.ChooseFolder(title) }
func (h macWindow) OpenFiles(title string, filter [][2]string, multiple bool) []string {
	return h.w.OpenFiles(title, filter, multiple)
}
func (h macWindow) Reveal(path string) error   { return mac.Reveal(path) }
func (h macWindow) Launch(path string) error   { return mac.Launch(path) }
func (h macWindow) LaunchURL(url string) error { return mac.LaunchURL(url) }
func (h macWindow) Languages() []string        { return mac.Languages() }

func alert(text string) { mac.Alert(text) }
