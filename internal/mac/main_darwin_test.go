package mac

import (
	"os"
	"runtime"
	"testing"
)

var mainWork = make(chan func())

// requireGUI skips tests that need a macOS Aqua WindowServer session on truly headless runners.
func requireGUI(t *testing.T) {
	t.Helper()
	if !hasGUISession() {
		t.Skip("no macOS GUI WindowServer session")
	}
}

// runOnMain executes fn on the OS main thread required by AppKit and WebKit.
func runOnMain(fn func()) {
	done := make(chan struct{})
	mainWork <- func() {
		defer close(done)
		fn()
	}
	<-done
}

func TestMain(m *testing.M) {
	runtime.LockOSThread()
	code := make(chan int, 1)
	go func() {
		c := m.Run()
		close(mainWork)
		code <- c
	}()
	for fn := range mainWork {
		fn()
	}
	os.Exit(<-code)
}
