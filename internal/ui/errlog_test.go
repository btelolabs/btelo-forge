package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/capture"
)

func TestUIErrorsAreLoggedWithRawDetails(t *testing.T) {
	x := newHarness(t)
	a := x.a
	raw := errors.Join(capture.ErrSystemSound, errors.New("coreaudio stream failed: -10851"))
	a.toast(a.u.tf("Recording could not start: %s", a.describe(raw)), true)

	logPath := a.errorLogPath()
	wantSuffix := filepath.Join("logs", "error.log")
	if !strings.HasSuffix(logPath, wantSuffix) {
		t.Fatalf("errorLogPath = %q, want suffix %q", logPath, wantSuffix)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("error log not written: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "[ERROR] [ui]") ||
		!strings.Contains(got, "Recording could not start:") ||
		!strings.Contains(got, "coreaudio stream failed: -10851") {
		t.Fatalf("log entry missing user or raw details:\n%s", got)
	}
}

func TestClosingMovieTabMidFrameDoesNotPanic(t *testing.T) {
	x := newHarness(t)
	a := x.a
	a.newTab()
	a.doc.movie = &movieDoc{path: filepath.Join(x.dir, "clip.mp4")}
	x.frame(Input{})

	// Click the close button on the active movie tab (tab index 1).
	u := &a.u
	tabW := u.px(220)
	tabX := u.px(8) + tabW
	closeRect := rectAt(tabX+tabW-u.px(28), u.px(6)+(u.px(34)-u.px(20))/2, u.px(20), u.px(20))
	x.mx, x.my = tabX+tabW/2, u.px(20)
	x.frame(Input{})
	x.click(closeRect)
	x.frame(Input{})

	if a.movie != nil {
		t.Error("movie tab was not closed")
	}
	if len(a.toasts) > 0 && a.toasts[len(a.toasts)-1].err {
		t.Fatalf("unexpected crash toast when closing movie tab: %q", a.toasts[len(a.toasts)-1].text)
	}
}
