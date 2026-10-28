package win

import (
	"image"
	"os"
	"sync"
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
// clicks it. It moves the mouse, so it runs only when FORGE_OVERLAY_TEST is set.
func TestOverlayShowsBlendsAndPassesClicks(t *testing.T) {
	if os.Getenv("FORGE_OVERLAY_TEST") == "" {
		t.Skip("set FORGE_OVERLAY_TEST to let the test show windows and click")
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

// TestOverlayPresentsFromAnotherGoroutine shows frames from a goroutine other than the window's, as the camera's
// window does, while the window's thread moves it. It shows windows, so it runs only when FORGE_OVERLAY_TEST is set.
func TestOverlayPresentsFromAnotherGoroutine(t *testing.T) {
	if os.Getenv("FORGE_OVERLAY_TEST") == "" {
		t.Skip("set FORGE_OVERLAY_TEST to let the test show windows")
	}
	EnableDPI()
	main, err := Open("overlay test", 200, 120, Foreground)
	if err != nil {
		t.Fatal(err)
	}
	defer main.Close()
	area := image.Rect(100, 100, 260, 220)
	o, err := OpenOverlay(OverlayOptions{Bounds: area})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	var mu sync.Mutex // the camera's window guards the overlay the same way
	colour := func(r, g, b byte) []byte {
		pix := make([]byte, area.Dx()*area.Dy()*4)
		for i := 0; i < len(pix); i += 4 {
			pix[i], pix[i+1], pix[i+2], pix[i+3] = r, g, b, 255
		}
		return pix
	}
	green, blue := colour(0, 200, 0), colour(0, 0, 200)
	stop := make(chan struct{})
	presented := make(chan error, 1)
	go func() {
		var err error
		for i := 0; ; i++ {
			select {
			case <-stop:
				presented <- err
				return
			default:
			}
			pix := green
			if i >= 20 {
				pix = blue
			}
			mu.Lock()
			if e := o.PresentLayered(pix, area.Dx(), area.Dy(), image.Rectangle{}); e != nil && err == nil {
				err = e
			}
			mu.Unlock()
			time.Sleep(10 * time.Millisecond)
		}
	}()
	// Meanwhile the window's own thread moves it.
	moved := area.Add(image.Pt(40, 30))
	pump(main, 100*time.Millisecond)
	mu.Lock()
	o.MoveOverlay(moved)
	mu.Unlock()
	pump(main, 500*time.Millisecond)
	close(stop)
	if err := <-presented; err != nil {
		t.Fatal(err)
	}
	pump(main, 100*time.Millisecond)
	if r, g, b := screenPixel(moved.Max.X-10, moved.Max.Y-10); r > 10 || g > 10 || b < 190 {
		t.Errorf("the moved window shows %d,%d,%d, want the last frame's blue", r, g, b)
	}
	if r, g, b := screenPixel(area.Min.X+5, area.Min.Y+5); b > 150 && g < 50 {
		t.Errorf("the old place still shows the window: %d,%d,%d", r, g, b)
	}
}

// TestOverlayTakesTheKeyboard has an overlay opened without the keyboard take it, get a key, and give it back to the
// window that had it. It moves the mouse and presses a key, so it runs only when FORGE_OVERLAY_TEST is set.
func TestOverlayTakesTheKeyboard(t *testing.T) {
	if os.Getenv("FORGE_OVERLAY_TEST") == "" {
		t.Skip("set FORGE_OVERLAY_TEST to let the test show windows and press keys")
	}
	EnableDPI()
	main, err := Open("overlay test", 200, 120, Foreground)
	if err != nil {
		t.Fatal(err)
	}
	defer main.Close()
	area := image.Rect(100, 100, 300, 200)
	o, err := OpenOverlay(OverlayOptions{Bounds: area})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	if err := o.PresentLayered(make([]byte, area.Dx()*area.Dy()*4), area.Dx(), area.Dy(), image.Rectangle{}); err != nil {
		t.Fatal(err)
	}
	// A click on the main window makes this program the one the user is working in, as using the toolbar does.
	setForeground.Call(main.Handle)
	pump(main, 200*time.Millisecond)
	front := func() uintptr { h, _, _ := getForeground.Call(); return h }
	if front() != main.Handle {
		t.Skip("the test window could not come to the front")
	}
	o.TakeKeyboard(true)
	pump(main, 200*time.Millisecond)
	if front() != o.Handle {
		t.Fatalf("the overlay is not in front after taking the keyboard: %#x, main %#x", front(), main.Handle)
	}
	if focus, _, _ := user32.NewProc("GetFocus").Call(); focus != o.Handle {
		t.Errorf("the keyboard focus is on %#x, not the overlay %#x", focus, o.Handle)
	}
	type keyInput struct {
		Type      uint32
		_         uint32
		VK, Scan  uint16
		Flags     uint32
		Time      uint32
		ExtraInfo uintptr
		_         [8]byte // the size of the union's largest member, the mouse's
	}
	in := [2]keyInput{{Type: 1, VK: 'F'}, {Type: 1, VK: 'F', Flags: 0x0002}} // KEYEVENTF_KEYUP
	sendInput.Call(2, uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0]))
	pump(main, 200*time.Millisecond)
	got := o.TakeInput()
	if len(got.Keys) != 1 || got.Keys[0].VK != 'F' {
		t.Errorf("the overlay got the keys %+v, want F", got.Keys)
	}
	o.TakeKeyboard(false)
	pump(main, 200*time.Millisecond)
	if front() != main.Handle {
		t.Errorf("the keyboard did not go back to the main window: %#x in front", front())
	}
}
