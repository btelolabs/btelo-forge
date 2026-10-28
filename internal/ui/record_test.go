package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-video-cut/internal/capture"
	"github.com/btelolabs/btelo-video-cut/internal/gfx"
)

// recordingHarness is an editor that can record the screen, saving into a test folder; it skips where there is no
// screen to record, as on build machines.
func recordingHarness(t *testing.T) *harness {
	t.Helper()
	if !capture.Supported() {
		t.Skip(capture.ErrUnsupported)
	}
	if _, err := capture.Displays(); err != nil {
		t.Skip(err)
	}
	x := newHarness(t)
	x.a.settings.ExportDir = x.mediaDir()
	// The app checks what it can record once ffmpeg is ready.
	x.pump(time.Minute, func() bool {
		_, checking := x.a.recordBlocker()
		return !checking
	})
	if reason, _ := x.a.recordBlocker(); reason != "" {
		t.Skip(reason)
	}
	return x
}

func TestRecordingExplainsWhyItCannot(t *testing.T) {
	x := newHarness(t)
	a := x.a
	if a.recorder() == nil {
		t.Skip("screen recording is not offered here")
	}
	x.pump(time.Minute, func() bool { return a.rec.listed })
	a.rec.listErr = &capture.OldWindowsError{Major: 10, Minor: 0, Build: 17763}
	for _, lang := range []Language{English, Chinese} {
		a.u.lang = lang
		reason, checking := a.recordBlocker()
		if checking || !strings.Contains(reason, "17763") {
			t.Errorf("%s: reason %q, checking %v", lang, reason, checking)
		}
		a.openRecorder()
		x.frame(Input{})
		x.frame(Input{})
		if !a.rec.open {
			t.Fatal("the dialog does not open to say why")
		}
		x.shot("record-unavailable-" + string(lang))
		a.rec.open = false
	}
	done := make(chan error, 1)
	a.startRecording(func(_ string, err error) { done <- err })
	if err := <-done; err == nil || a.rec.rec != nil || a.rec.starting {
		t.Errorf("a recording started on a computer that cannot record: %v", err)
	}
	a.rec.listErr = capture.ErrNoGrabber
	if reason, _ := a.recordBlocker(); !strings.Contains(reason, "ddagrab") {
		t.Errorf("reason %q", reason)
	}
}

// recordButton is where the tab strip draws the recording button, left of the settings gear.
func (x *harness) recordButton() gfx.Rect {
	gear := float32(x.c.Img.Rect.Dx()) - 40
	record, _ := x.a.recordButtons(gear, 5, 30)
	return record
}

// startButton is the dialog's start button, laid out as drawRecorder does at scale 1.
func (x *harness) startButton() gfx.Rect {
	w, h := float32(x.c.Img.Rect.Dx()), float32(x.c.Img.Rect.Dy())
	box := rectCentered(gfx.Rect{W: w, H: h}, min(640, w-40), min(24+44+x.a.rec.content+56+24+1, h-40))
	in := box.Inset(24)
	return gfx.Rect{X: in.Right() - 198, Y: in.Bottom() - 38, W: 198, H: 38}
}

func TestRecordFromTheWindowAndEditIt(t *testing.T) {
	x := recordingHarness(t)
	a, h := x.a, x.h
	a.settings.Record.Minimize = true
	x.click(x.recordButton())
	if !a.rec.open {
		t.Fatal("the recording button did not open the dialog")
	}
	d, _ := a.chosenDisplay()
	x.pump(time.Minute, func() bool {
		d, _ = a.chosenDisplay()
		_, ok := a.rec.encoders[encoderKey(d, a.settings.Record.FPS)]
		return a.rec.listed && (ok || a.rec.failures[encoderKey(d, a.settings.Record.FPS)] != nil)
	})
	if err := a.rec.failures[encoderKey(d, a.settings.Record.FPS)]; err != nil {
		t.Skip(err)
	}
	x.shot("record-dialog")
	a.settings.Record.Microphone = true
	a.u.lang = Chinese
	x.frame(Input{})
	x.frame(Input{})
	x.shot("record-dialog-zh")
	a.u.lang = English
	a.settings.Record.Microphone = false
	x.click(x.startButton())
	x.pump(30*time.Second, func() bool { return a.rec.rec != nil || !a.rec.starting })
	if a.rec.rec == nil {
		t.Fatalf("the recording did not start; toasts %+v", a.toasts)
	}
	if !h.minimized || !h.excluded {
		t.Errorf("window while recording: minimized %v, left out %v", h.minimized, h.excluded)
	}
	path := a.rec.rec.Path()
	if filepath.Dir(path) != a.settings.ExportDir || !strings.HasSuffix(path, ".mp4") {
		t.Errorf("recording into %s, want a new MP4 in %s", path, a.settings.ExportDir)
	}
	// Minimized, the window draws nothing and the main loop calls Tick instead.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		a.Tick(Input{})
		time.Sleep(50 * time.Millisecond)
	}
	x.frame(Input{})
	x.shot("record-running-sound")
	if !strings.Contains(h.title, "0:0") {
		t.Errorf("the title does not show the time: %q", h.title)
	}
	a.Tick(Input{Hotkeys: []int{hotkeyRecord}})
	x.pump(time.Minute, func() bool { return a.rec.rec == nil && a.p != nil && a.opening == "" })
	if h.minimized || h.excluded {
		t.Errorf("window after recording: minimized %v, left out %v", h.minimized, h.excluded)
	}
	if got := a.p.Sources[0].Path; got != path {
		t.Fatalf("opened %s, want the recording %s", got, path)
	}
	if src := a.p.Sources[0]; !src.HasAudio || src.Duration < 1.5 || src.Width != d.Width {
		t.Errorf("the recording is %+v", src)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if h.title != a.Title() {
		t.Errorf("the title stayed %q", h.title)
	}
}

func TestRecordingIntoAnEditOpensANewTab(t *testing.T) {
	x := recordingHarness(t)
	a := x.a
	x.open(x.fixture())
	a.settings.Record.Minimize = false
	a.settings.Record.SystemAudio = false
	a.openRecorder()
	x.pump(time.Minute, func() bool { return a.rec.listed })
	started := make(chan error, 1)
	a.startRecording(func(_ string, err error) { started <- err })
	x.pump(time.Minute, func() bool { return len(started) > 0 })
	if err := <-started; err != nil {
		t.Skip(err)
	}
	if a.rec.open {
		t.Error("the dialog stayed open while recording")
	}
	time.Sleep(1500 * time.Millisecond)
	x.frame(Input{})
	x.shot("record-running")
	paused := make(chan error, 1)
	a.pauseRecording(true, func(err error) { paused <- err })
	x.pump(30*time.Second, func() bool { return len(paused) > 0 })
	if err := <-paused; err != nil || !a.rec.rec.Status().Paused {
		t.Fatalf("pause: %v", err)
	}
	a.pauseRecording(false, func(err error) { paused <- err })
	x.pump(30*time.Second, func() bool { return len(paused) > 0 })
	if err := <-paused; err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	saved := make(chan string, 1)
	a.stopRecording(func(path string, err error) {
		if err != nil {
			t.Error(err)
		}
		saved <- path
	})
	x.pump(time.Minute, func() bool { return len(saved) > 0 && a.opening == "" && a.p != nil })
	path := <-saved
	if len(a.docs) != 2 || a.p.Sources[0].Path != path {
		t.Fatalf("%d tabs, the active one shows %v; want the recording %s in a second tab", len(a.docs), a.p.Sources, path)
	}
	if a.p.Sources[0].HasAudio {
		t.Error("a recording without sound has a sound track")
	}
}

func TestRecordingNameIsNew(t *testing.T) {
	x := newHarness(t)
	a := x.a
	a.settings.ExportDir = x.mediaDir()
	at := time.Date(2026, 10, 27, 14, 3, 5, 0, time.Local)
	first, err := a.recordingPath(at)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(first) != "Screen recording 2026-10-27 14.03.05.mp4" {
		t.Errorf("first name %s", filepath.Base(first))
	}
	os.WriteFile(first, nil, 0o644)
	second, _ := a.recordingPath(at)
	if filepath.Base(second) != "Screen recording 2026-10-27 14.03.05 (2).mp4" {
		t.Errorf("second name %s", filepath.Base(second))
	}
	a.u.lang = Chinese
	if zh, _ := a.recordingPath(at); filepath.Base(zh) != "屏幕录制 2026-10-27 14.03.05.mp4" {
		t.Errorf("Chinese name %s", filepath.Base(zh))
	}
}

func TestRecordSettingsDefaultForOldSettingsFiles(t *testing.T) {
	x := newHarness(t)
	if got, want := x.a.settings.Record, defaultRecordSettings(); got != want {
		t.Errorf("fresh settings %+v, want %+v", got, want)
	}
	os.WriteFile(x.a.settingsPath(), []byte(`{"language":"zh","mcp":true}`), 0o600)
	x.a.loadSettings()
	if got := x.a.settings.Record; got != defaultRecordSettings() {
		t.Errorf("settings from before recording %+v", got)
	}
}
