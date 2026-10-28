// The Cocoa side covers the event loop, layer presentation, input methods, drops, clipboard and file panels.
// Cocoa requires all of it on the main thread, which the package locks at start-up.

package mac

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=11.0
#cgo LDFLAGS: -framework Cocoa -framework QuartzCore -mmacosx-version-min=11.0
#include <stdlib.h>
#include "window.h"
*/
import "C"

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unsafe"
)

func init() { runtime.LockOSThread() }

// Window is the one application window.
type Window struct {
	input   inputQueue
	typing  bool
	refresh func()
}

var current *Window

// Open creates the window with a content area of width×height points.
func Open(title string, width, height int, background bool) (*Window, error) {
	t := C.CString(title)
	defer C.free(unsafe.Pointer(t))
	bg := C.int(0)
	if background {
		bg = 1
	}
	if C.forge_open(t, C.int(width), C.int(height), bg) == 0 {
		return nil, errors.New("the window could not be created")
	}
	current = &Window{}
	return current, nil
}

// Poll dispatches pending events and makes everything collected since the previous Poll, including during Wait, the
// current input; it returns false once the window is closed.
func (w *Window) Poll() bool {
	open := C.forge_poll() != 0
	w.input.flip()
	return open
}

// Wait blocks until an event arrives, Wake is called, or timeout passes.
func (w *Window) Wait(timeout time.Duration) { C.forge_wait(C.double(timeout.Seconds())) }

// Wake makes a pending Wait return; it is safe to call from any goroutine.
func (w *Window) Wake() { C.forge_wake() }

// Input returns the events collected by the last Poll.
func (w *Window) Input() *Input { return &w.input.last }

// Size is the content area in pixels.
func (w *Window) Size() (int, int) {
	var width, height C.int
	C.forge_size(&width, &height)
	return int(width), int(height)
}

// Scale is the display's pixels per point.
func (w *Window) Scale() float32 { return float32(C.forge_scale()) }

// Focused reports whether the window has the keyboard focus.
func (w *Window) Focused() bool { return C.forge_key_window() != 0 }

// Present shows an RGBA image of the content size.
func (w *Window) Present(rgba []byte, width, height int) error {
	if width <= 0 || height <= 0 {
		return nil
	}
	if len(rgba) < width*height*4 {
		return errors.New("present: image smaller than its size")
	}
	C.forge_present(unsafe.Pointer(&rgba[0]), C.int(width), C.int(height))
	runtime.KeepAlive(rgba)
	return nil
}

// SetRefresh sets what draws a frame during a live resize, when the main loop is not running.
func (w *Window) SetRefresh(f func()) { w.refresh = f }

func (w *Window) SetTitle(title string) {
	t := C.CString(title)
	defer C.free(unsafe.Pointer(t))
	C.forge_title(t)
}

// Cursor kinds understood by SetCursor.
const (
	Arrow = iota
	Hand
	IBeam
	ResizeLeftRight
	Move
)

func (w *Window) SetCursor(kind int) { C.forge_cursor(C.int(kind)) }

// TextInput turns text entry on or off, placing input-method windows at the caret (pixels).
func (w *Window) TextInput(on bool, x, y, height float32) {
	v := C.int(0)
	if on {
		v = 1
	}
	w.typing = on
	C.forge_text_input(v, C.double(x), C.double(y), C.double(height))
}

func takeString(p *C.char) string {
	if p == nil {
		return ""
	}
	defer C.forge_free(unsafe.Pointer(p))
	return C.GoString(p)
}

// Languages lists the user's preferred languages as BCP 47 tags, most preferred first.
func Languages() []string {
	if s := takeString(C.forge_languages()); s != "" {
		return strings.Split(s, "\n")
	}
	return nil
}

func (w *Window) Clipboard() (string, error) {
	s := takeString(C.forge_clipboard_text())
	if s == "" {
		return "", errors.New("clipboard has no text")
	}
	return s, nil
}

func (w *Window) SetClipboard(text string) error {
	t := C.CString(text)
	defer C.free(unsafe.Pointer(t))
	C.forge_set_clipboard(t)
	return nil
}

// Paste reads an image, copied files, or text from the clipboard.
func (w *Window) Paste() (image.Image, []string, string) {
	var n C.int
	if p := C.forge_clipboard_png(&n); p != nil {
		data := C.GoBytes(p, n)
		C.forge_free(p)
		if img, err := png.Decode(bytes.NewReader(data)); err == nil {
			return img, nil, ""
		}
	}
	if files := takeString(C.forge_clipboard_files()); files != "" {
		return nil, strings.Split(files, "\n"), ""
	}
	text, _ := w.Clipboard()
	return nil, nil, strings.TrimSpace(text)
}

// OpenFiles shows the open panel; filter patterns such as "*.mp4;*.mov" become allowed extensions.
func (w *Window) OpenFiles(title string, filter [][2]string, multiple bool) []string {
	var exts []string
	for _, f := range filter {
		for _, p := range strings.Split(f[1], ";") {
			if e := strings.TrimPrefix(strings.TrimSpace(p), "*."); e != "" && e != "*" {
				exts = append(exts, e)
			}
		}
		break
	}
	return w.panel(title, strings.Join(exts, ";"), multiple, false)
}

// ChooseFolder shows the open panel for a folder.
func (w *Window) ChooseFolder(title string) string {
	if got := w.panel(title, "", false, true); len(got) > 0 {
		return got[0]
	}
	return ""
}

func (w *Window) panel(title, exts string, multiple, folders bool) []string {
	t, e := C.CString(title), C.CString(exts)
	defer C.free(unsafe.Pointer(t))
	defer C.free(unsafe.Pointer(e))
	m, f := C.int(0), C.int(0)
	if multiple {
		m = 1
	}
	if folders {
		f = 1
	}
	w.input.last.Down, w.input.next.Down = false, false
	if s := takeString(C.forge_open_panel(t, e, m, f)); s != "" {
		return strings.Split(s, "\n")
	}
	return nil
}

// Close hides the window.
func (w *Window) Close() { C.forge_close() }

// Reveal shows path in Finder.
func Reveal(path string) error { return exec.Command("open", "-R", path).Start() }

// Launch opens path with its default application.
func Launch(path string) error { return exec.Command("open", path).Start() }

// LaunchURL opens a web page in the default browser.
func LaunchURL(url string) error { return exec.Command("open", url).Start() }

// Alert shows a blocking error message.
func Alert(message string) {
	m := C.CString(message)
	defer C.free(unsafe.Pointer(m))
	C.forge_alert(m)
}
