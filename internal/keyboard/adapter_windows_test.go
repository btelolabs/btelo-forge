package keyboard

import (
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

const vkF22, vkF23, vkF24 = 0x85, 0x86, 0x87

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal(what)
}

// TestHookOnARealKeyboard runs the whole path through Windows, with sent keys standing in for pressed ones: a key is
// swallowed so the system never sees it down, another takes its place, and the hook comes back after it is put back.
func TestHookOnARealKeyboard(t *testing.T) {
	acceptSent.Store(true)
	defer acceptSent.Store(false)
	a := NewAdapter().(*windowsAdapter)
	s := NewService(a, func(err error) { t.Error(err) })
	defer s.Close()
	if err := s.Apply(&Config{Version: 1, Rules: []string{"F22 -> F23"}}); err != nil {
		t.Fatal(err)
	}
	if s.Watching() {
		t.Fatal("watching before it was switched on")
	}
	s.SetActive(true)
	if !s.Watching() || s.Err() != nil {
		t.Fatalf("not watching: %v", s.Err())
	}
	defer a.Send([]Stroke{{vkF22, false}, {vkF23, false}})

	// The rule's own F23 comes back through the hook here, which must not deadlock or match anything.
	if err := a.Send([]Stroke{{vkF22, true}}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the rule's key did not go down", func() bool { return a.KeyDown(vkF23) })
	if a.KeyDown(vkF22) {
		t.Fatal("the swallowed key reached the system")
	}
	a.Send([]Stroke{{vkF22, false}})
	eventually(t, "the rule's key did not come up", func() bool { return !a.KeyDown(vkF23) })

	// A key no rule names passes.
	a.Send([]Stroke{{vkF24, true}})
	eventually(t, "a plain key was held back", func() bool { return a.KeyDown(vkF24) })
	a.Send([]Stroke{{vkF24, false}})

	// Stopped, the same key goes through as it is.
	s.SetActive(false)
	a.Send([]Stroke{{vkF22, true}})
	eventually(t, "the hook outlived the rules", func() bool { return a.KeyDown(vkF22) })
	if a.KeyDown(vkF23) {
		t.Fatal("the rule ran after it was turned off")
	}
	a.Send([]Stroke{{vkF22, false}})
}

func TestHookIgnoresSentKeysUnlessTesting(t *testing.T) {
	a := NewAdapter().(*windowsAdapter)
	calls := make(chan uint16, 8)
	if err := a.Start(func(vk uint16, down bool) bool {
		if vk == vkF24 { // the person at this computer may be typing
			calls <- vk
		}
		return false
	}); err != nil {
		t.Fatal(err)
	}
	defer a.Stop()
	a.Send([]Stroke{{vkF24, true}, {vkF24, false}})
	select {
	case <-calls:
		t.Fatal("the hook took a sent key")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestStartAndStopRepeatedly(t *testing.T) {
	a := NewAdapter()
	for range 5 {
		if err := a.Start(func(uint16, bool) bool { return false }); err != nil {
			t.Fatal(err)
		}
		a.Stop()
	}
	a.Stop()
}

func TestOpen(t *testing.T) {
	a := NewAdapter()
	if err := a.Open(`Z:\no\such\program.exe`, ""); err == nil {
		t.Fatal("opening nothing did not fail")
	}
	marker := t.TempDir() + `\opened`
	script := t.TempDir() + `\touch.cmd`
	if err := os.WriteFile(script, []byte("@echo %1> \""+marker+"\"\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.Open(script, "with-arguments"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the program did not run", func() bool {
		data, err := os.ReadFile(marker)
		return err == nil && strings.Contains(string(data), "with-arguments")
	})
}

func TestTypeInputs(t *testing.T) {
	if size := unsafe.Sizeof(keyInput{}); size != 40 {
		t.Fatalf("INPUT is 40 bytes on 64-bit Windows, not %d", size)
	}
	in := typeInputs("中😀\n")
	if len(in) != 2+4+2 { // a press and release for each unit, the emoji having two, and Enter
		t.Fatalf("%d events", len(in))
	}
	if in[0].scan != 0x4e2d || in[0].flags != unicodeKey || in[1].flags != unicodeKey|keyRelease {
		t.Fatalf("%+v", in[:2])
	}
	if in[2].scan != 0xd83d || in[4].scan != 0xde00 || in[6].vk != 0x0d {
		t.Fatalf("%+v", in[2:])
	}
}

// TestTypeReachesAWindow types into an edit box of the test's own and reads it back. It never types with any other
// window in front, as that would write into whatever the person at this computer is doing.
func TestTypeReachesAWindow(t *testing.T) {
	if os.Getenv("FORGE_TYPE_TEST") == "" {
		t.Skip("set FORGE_TYPE_TEST to try it: the test's window takes the keyboard from whoever is at this computer")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var (
		createWindow  = user32.NewProc("CreateWindowExW")
		destroyWindow = user32.NewProc("DestroyWindow")
		setForeground = user32.NewProc("SetForegroundWindow")
		foreground    = user32.NewProc("GetForegroundWindow")
		setFocus      = user32.NewProc("SetFocus")
		peekMessage   = user32.NewProc("PeekMessageW")
		translate     = user32.NewProc("TranslateMessage")
		dispatch      = user32.NewProc("DispatchMessageW")
		sendMessage   = user32.NewProc("SendMessageW")
	)
	class, _ := syscall.UTF16PtrFromString("EDIT")
	const visibleOverlapped, multiline = 0x10CF0000, 0x4
	box, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(class)), 0, visibleOverlapped|multiline, 100, 100, 300, 120, 0, 0, 0, 0)
	if box == 0 {
		t.Fatal(err)
	}
	defer destroyWindow.Call(box)
	var msg [48]byte
	pump := func() {
		for {
			if r, _, _ := peekMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 1); r == 0 {
				return
			}
			translate.Call(uintptr(unsafe.Pointer(&msg)))
			dispatch.Call(uintptr(unsafe.Pointer(&msg)))
		}
	}
	setForeground.Call(box)
	setFocus.Call(box)
	pump()
	if front, _, _ := foreground.Call(); front != box {
		t.Skip("Windows did not bring the test's window to the front")
	}
	if err := NewAdapter().Type("中😀\nok"); err != nil {
		t.Fatal(err)
	}
	const wmGetText = 0x000d
	text := make([]uint16, 64)
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		pump()
		n, _, _ := sendMessage.Call(box, wmGetText, uintptr(len(text)), uintptr(unsafe.Pointer(&text[0])))
		if strings.Contains(syscall.UTF16ToString(text[:n]), "中😀\r\nok") {
			return
		}
	}
	t.Fatalf("the box holds %q", syscall.UTF16ToString(text))
}
