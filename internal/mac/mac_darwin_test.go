package mac

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestWindowPresentIOSurfaceAndHideSize(t *testing.T) {
	requireGUI(t)
	runOnMain(func() {
		w, err := Open("present test", 640, 400, true)
		if err != nil {
			t.Fatal(err)
		}
		defer w.Close()

		width, height := w.Size()
		if width <= 0 || height <= 0 {
			t.Fatalf("visible window size = %dx%d", width, height)
		}
		rgba := make([]byte, width*height*4)
		for i := 0; i < len(rgba); i += 4 {
			rgba[i], rgba[i+1], rgba[i+2], rgba[i+3] = 40, 120, 220, 255
		}
		// Present multiple frames to exercise the full triple-buffered IOSurface ring.
		start := time.Now()
		for range 10 {
			if err := w.Present(rgba, width, height); err != nil {
				t.Fatalf("Present: %v", err)
			}
		}
		t.Logf("10× IOSurface+vImage Present (%dx%d) took %v (%.2f ms/frame)", width, height, time.Since(start), float64(time.Since(start).Microseconds())/10000.0)

		// Cursor & TextInput caching should accept all cursor kinds cleanly.
		for _, c := range []int{Arrow, Hand, IBeam, ResizeLeftRight, Move, Hidden, Cross, ResizeCorner, Arrow} {
			w.SetCursor(c)
		}
		w.TextInput(true, 100, 100, 24)
		w.TextInput(true, 100, 100, 24) // cached no-op
		w.TextInput(false, 0, 0, 0)

		// ExcludeFromCapture should succeed.
		if err := w.ExcludeFromCapture(true); err != nil {
			t.Fatalf("ExcludeFromCapture(true): %v", err)
		}
		if err := w.ExcludeFromCapture(false); err != nil {
			t.Fatalf("ExcludeFromCapture(false): %v", err)
		}

		// Hide should make Size() return 0,0 so the main loop stops drawing.
		w.Hide()
		if hw, hh := w.Size(); hw != 0 || hh != 0 {
			t.Errorf("hidden window Size() = %dx%d, want 0x0", hw, hh)
		}
	})
}

func TestSystemDarkAndSetDark(t *testing.T) {
	requireGUI(t)
	runOnMain(func() {
		w, err := Open("theme test", 320, 240, true)
		if err != nil {
			t.Fatal(err)
		}
		defer w.Close()

		sys := SystemDark()
		// Overriding the window's appearance must not corrupt OS system theme detection.
		w.SetDark(!sys)
		if got := SystemDark(); got != sys {
			t.Fatalf("after SetDark(%v), SystemDark() = %v, want %v", !sys, got, sys)
		}
		w.SetDark(sys)
		if got := SystemDark(); got != sys {
			t.Fatalf("after SetDark(%v), SystemDark() = %v, want %v", sys, got, sys)
		}
	})
}

func TestHotkeysRegisterAndUnregister(t *testing.T) {
	requireGUI(t)
	runOnMain(func() {
		w, err := Open("hotkey test", 320, 240, true)
		if err != nil {
			t.Fatal(err)
		}
		defer w.Close()

		// Default recording hotkeys: Ctrl+Shift+1..4 (mapped to Control+Shift on macOS so no Cmd+Shift+3/4 collision).
		errs := w.SetHotkeys([]Hotkey{
			{Ctrl: true, Shift: true, VK: '1'},
			{Ctrl: true, Shift: true, VK: '2'},
			{Ctrl: true, Shift: true, VK: '3'},
			{Ctrl: true, Shift: true, VK: '4'},
			{VK: 0}, // empty slot skipped
		})
		for i, e := range errs {
			if e != nil {
				t.Errorf("hotkey %d failed to register: %v", i, e)
			}
		}
		// Clearing hotkeys should leave no error.
		if errs := w.SetHotkeys(nil); len(errs) != 0 {
			t.Errorf("clear hotkeys errs = %v", errs)
		}
	})
}

func TestOverlayLifecycleAndThreadSafePresent(t *testing.T) {
	requireGUI(t)
	runOnMain(func() {
		pt := PointerAt()
		t.Logf("desktop pointer at %v", pt)

		ov, err := OpenOverlay(OverlayOptions{
			Bounds:       image.Rect(100, 100, 340, 260),
			Activate:     false,
			ClickThrough: true,
			Excluded:     true,
		})
		if err != nil {
			t.Fatalf("OpenOverlay: %v", err)
		}
		defer ov.Close()

		if got := ov.Bounds(); got != image.Rect(100, 100, 340, 260) {
			t.Fatalf("Bounds = %v", got)
		}
		if sc := ov.Scale(); sc <= 0 {
			t.Fatalf("Scale = %v", sc)
		}

		// Present from the main thread.
		rgba := make([]byte, 240*160*4)
		for i := 0; i < len(rgba); i += 4 {
			rgba[i], rgba[i+1], rgba[i+2], rgba[i+3] = 10, 200, 80, 180
		}
		if err := ov.PresentLayered(rgba, 240, 160, image.Rectangle{}); err != nil {
			t.Fatalf("main-thread PresentLayered: %v", err)
		}

		// Present concurrently from a background goroutine (as internal/ui/camera.go does).
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 5 {
				if err := ov.PresentLayered(rgba, 240, 160, image.Rectangle{}); err != nil {
					t.Errorf("bg PresentLayered: %v", err)
				}
			}
		}()
		wg.Wait()

		ov.SetClickThrough(false)
		ov.SetCursor(Cross)
		ov.MoveOverlay(image.Rect(140, 120, 380, 280))
		if got := ov.Bounds(); got != image.Rect(140, 120, 380, 280) {
			t.Fatalf("Bounds after MoveOverlay = %v", got)
		}
		ov.Raise()
		_ = ov.TakeInput()
	})
}

func TestClipboardImageFilesAndTrash(t *testing.T) {
	requireGUI(t)
	runOnMain(func() {
		w, err := Open("clip test", 320, 200, true)
		if err != nil {
			t.Fatal(err)
		}
		defer w.Close()

		// Save existing clipboard text to be polite.
		prevText, prevErr := w.Clipboard()
		defer func() {
			if prevErr == nil {
				_ = w.SetClipboard(prevText)
			}
		}()

		// 1. Image clipboard with transparency round-trip.
		img := image.NewNRGBA(image.Rect(0, 0, 12, 8))
		img.SetNRGBA(0, 0, color.NRGBA{200, 50, 30, 255})
		img.SetNRGBA(1, 0, color.NRGBA{10, 120, 240, 128})
		if err := w.SetClipboardImage(img); err != nil {
			t.Fatalf("SetClipboardImage: %v", err)
		}
		gotImg, _, _ := w.Paste()
		if gotImg == nil {
			t.Fatal("Paste returned nil image after SetClipboardImage")
		}
		if gotImg.Bounds().Dx() != 12 || gotImg.Bounds().Dy() != 8 {
			t.Fatalf("pasted image size = %v, want 12x8", gotImg.Bounds())
		}

		// 2. Native file clipboard round-trip (including backslash & quote in filename to test escaping safety).
		dir := t.TempDir()
		f1 := filepath.Join(dir, `quote"and\slash.txt`)
		if err := os.WriteFile(f1, []byte("hello"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := w.SetClipboardFiles([]string{f1}); err != nil {
			t.Fatalf("SetClipboardFiles: %v", err)
		}
		_, gotFiles, _ := w.Paste()
		if !slices.Equal(gotFiles, []string{f1}) {
			t.Fatalf("Paste files = %q, want [%q]", gotFiles, f1)
		}

		// 3. Native Trash via NSFileManager.
		if err := Trash([]string{f1}); err != nil {
			t.Fatalf("Trash: %v", err)
		}
		if _, err := os.Stat(f1); !os.IsNotExist(err) {
			t.Fatalf("trashed file still exists: %v", err)
		}
	})
}

func BenchmarkPresentIOSurface1440x900(b *testing.B) {
	runOnMain(func() {
		w, err := Open("bench", 1440, 900, true)
		if err != nil {
			b.Fatal(err)
		}
		defer w.Close()
		width, height := w.Size()
		rgba := make([]byte, width*height*4)
		// Warm up surface ring allocation.
		_ = w.Present(rgba, width, height)
		b.ResetTimer()
		for range b.N {
			_ = w.Present(rgba, width, height)
		}
	})
}
