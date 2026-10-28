package win

import (
	"image"
	"os"
	"testing"
	"time"
	"unsafe"
)

var (
	getPixel  = gdi32.NewProc("GetPixel")
	sendInput = user32.NewProc("SendInput")
	setCursor = user32.NewProc("SetCursorPos")
)

// screenPixel reads the desktop as it shows now.
func screenPixel(x, y int) (r, g, b uint8) {
	dc, _, _ := getDC.Call(0)
	defer releaseDC.Call(0, dc)
	c, _, _ := getPixel.Call(dc, uintptr(x), uintptr(y))
	return uint8(c), uint8(c >> 8), uint8(c >> 16)
}

// click presses and releases the left button at (x, y) on the desktop.
func click(x, y int) {
	setCursor.Call(uintptr(x), uintptr(y))
	type mouseInput struct {
		Type      uint32
		_         uint32
		Dx, Dy    int32
		Data      uint32
		Flags     uint32
		Time      uint32
		ExtraInfo uintptr
	}
	in := [2]mouseInput{{Type: 0, Flags: 0x0002}, {Type: 0, Flags: 0x0004}} // LEFTDOWN, LEFTUP
	sendInput.Call(2, uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0]))
}

// pump handles the thread's messages for a while, as the app's main loop does.
func pump(w *Window, d time.Duration) {
	for end := time.Now().Add(d); time.Now().Before(end); {
		w.Poll()
		time.Sleep(5 * time.Millisecond)
	}
}

// TestOverlayShowsBlendsAndPassesClicks puts a half-transparent overlay on the screen, reads the screen back and
// clicks it. It moves the mouse, so it runs only when BVC_OVERLAY_TEST is set.
func TestOverlayShowsBlendsAndPassesClicks(t *testing.T) {
	if os.Getenv("BVC_OVERLAY_TEST") == "" {
		t.Skip("set BVC_OVERLAY_TEST to let the test show windows and click")
	}
	EnableDPI()
	main, err := Open("overlay test", 200, 120, Foreground)
	if err != nil {
		t.Fatal(err)
	}
	defer main.Close()
	// An opaque white overlay first, to know what is under the half-transparent red one.
	area := image.Rect(100, 100, 300, 200)
	under, err := OpenOverlay(OverlayOptions{Bounds: area})
	if err != nil {
		t.Fatal(err)
	}
	defer under.Close()
	white := make([]byte, area.Dx()*area.Dy()*4)
	for i := range white {
		white[i] = 255
	}
	if err := under.PresentLayered(white, area.Dx(), area.Dy(), image.Rectangle{}); err != nil {
		t.Fatal(err)
	}
	o, err := OpenOverlay(OverlayOptions{Bounds: area})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	// Premultiplied: red at half opacity is (128, 0, 0, 128); the right half is fully transparent.
	pix := make([]byte, area.Dx()*area.Dy()*4)
	for y := 0; y < area.Dy(); y++ {
		for x := 0; x < area.Dx()/2; x++ {
			i := (y*area.Dx() + x) * 4
			pix[i], pix[i+3] = 128, 128
		}
	}
	if err := o.PresentLayered(pix, area.Dx(), area.Dy(), image.Rectangle{}); err != nil {
		t.Fatal(err)
	}
	pump(main, 300*time.Millisecond)
	if r, g, b := screenPixel(150, 150); r < 250 || g < 120 || g > 135 || b < 120 || b > 135 {
		t.Errorf("half red over white shows as %d,%d,%d, want about 255,127,127", r, g, b)
	}
	if r, g, b := screenPixel(250, 150); r != 255 || g != 255 || b != 255 {
		t.Errorf("the transparent half shows %d,%d,%d, want the white below", r, g, b)
	}
	// It catches a click on its painted half; let through, the click goes to the overlay below.
	o.TakeInput()
	click(150, 150)
	pump(main, 200*time.Millisecond)
	if in := o.TakeInput(); len(in.Pointer) == 0 {
		t.Error("the overlay did not get the click")
	}
	o.SetClickThrough(true)
	under.TakeInput()
	click(150, 150)
	pump(main, 200*time.Millisecond)
	if in := o.TakeInput(); len(in.Pointer) != 0 {
		t.Error("a click-through overlay got the click")
	}
	if in := under.TakeInput(); len(in.Pointer) == 0 {
		t.Error("the click did not reach the window below")
	}
}
