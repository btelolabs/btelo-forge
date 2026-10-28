package ui

import (
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHotkeyNames(t *testing.T) {
	for _, c := range []struct {
		key  Hotkey
		want string
	}{
		{Hotkey{Ctrl: true, Shift: true, Key: 0x78}, "Ctrl+Shift+F9"},
		{Hotkey{Ctrl: true, Alt: true, Key: 'R'}, "Ctrl+Alt+R"},
		{Hotkey{Alt: true, Key: 0x61}, "Alt+Num1"},
		{Hotkey{Ctrl: true, Key: 0xc0}, "Ctrl+`"},
		{Hotkey{Key: 0x87}, "F24"},
		{Hotkey{}, ""},
	} {
		if got := c.key.String(); got != c.want {
			t.Errorf("%+v is %q, want %q", c.key, got, c.want)
		}
	}
	for _, c := range []struct {
		key    Hotkey
		usable bool
	}{
		{Hotkey{Key: 'A'}, false}, {Hotkey{Shift: true, Key: 'A'}, false}, {Hotkey{Ctrl: true, Key: 'A'}, true},
		{Hotkey{Alt: true, Key: '1'}, true}, {Hotkey{Key: 0x7b}, true}, {Hotkey{}, false},
	} {
		if c.key.usable() != c.usable {
			t.Errorf("%s usable %v, want %v", c.key, !c.usable, c.usable)
		}
	}
	// The defaults differ from each other and stay usable.
	d := defaultHotkeys()
	seen := map[Hotkey]bool{}
	for _, k := range d.list() {
		if !k.usable() || seen[*k] {
			t.Errorf("default %s", k)
		}
		seen[*k] = true
	}
}

func TestHotkeysAreRegisteredAndChosenByPressing(t *testing.T) {
	x := newHarness(t)
	a, h := x.a, x.h
	if a.recorder() == nil {
		t.Skip("hotkeys are not offered here")
	}
	if len(h.hotkeys) != hotkeyCount || h.hotkeys[hotkeyRecord].String() != "Ctrl+Shift+F9" {
		t.Fatalf("registered at start: %v", h.hotkeys)
	}
	a.settingsOpen, a.settingsPage, a.keysDomain = true, settingsHotkeysPage, domainRecording
	x.frame(Input{})
	x.shot("settings-hotkeys")
	a.hotkeys.capturing = hotkeyScreenshot
	a.applyHotkeys()
	if len(h.hotkeys) != 0 {
		t.Fatalf("hotkeys stay registered while one is chosen: %v", h.hotkeys)
	}
	x.frame(Input{})
	x.shot("settings-hotkeys-choosing")
	// Plain typing and the record hotkey are refused; the dialog stays open.
	x.frame(Input{Keys: []Key{{Code: 'P'}}})
	if a.hotkeys.capturing != hotkeyScreenshot || !strings.Contains(a.hotkeys.refused, "P") {
		t.Fatalf("a plain key was taken: capturing %d, refused %q", a.hotkeys.capturing, a.hotkeys.refused)
	}
	x.frame(Input{Keys: []Key{{Code: 0x78, Ctrl: true, Shift: true}}})
	if a.hotkeys.capturing != hotkeyScreenshot || !strings.Contains(a.hotkeys.refused, "Ctrl+Shift+F9") {
		t.Fatalf("a hotkey of another action was taken: refused %q", a.hotkeys.refused)
	}
	x.frame(Input{Keys: []Key{{Code: 'P', Ctrl: true, Alt: true}}})
	if a.hotkeys.capturing != -1 || !a.settingsOpen {
		t.Fatalf("capturing %d, settings open %v", a.hotkeys.capturing, a.settingsOpen)
	}
	if got := a.settings.Hotkeys.Screenshot.String(); got != "Ctrl+Alt+P" {
		t.Errorf("screenshot hotkey %q", got)
	}
	if len(h.hotkeys) != hotkeyCount || h.hotkeys[hotkeyScreenshot].String() != "Ctrl+Alt+P" {
		t.Errorf("registered after choosing: %v", h.hotkeys)
	}
	// Esc while choosing cancels without closing Settings or changing anything.
	a.hotkeys.capturing = hotkeyPause
	a.applyHotkeys()
	x.frame(Input{Keys: []Key{{Code: keyEscape}}})
	if a.hotkeys.capturing != -1 || !a.settingsOpen || a.settings.Hotkeys.Pause != defaultHotkeys().Pause {
		t.Errorf("Esc: capturing %d, settings open %v, pause %s", a.hotkeys.capturing, a.settingsOpen, a.settings.Hotkeys.Pause)
	}
	// The choice is kept between runs.
	data, err := os.ReadFile(a.settingsPath())
	if err != nil {
		t.Fatal(err)
	}
	var saved settings
	if err := json.Unmarshal(data, &saved); err != nil || saved.Hotkeys.Screenshot.String() != "Ctrl+Alt+P" {
		t.Errorf("saved %+v, %v", saved.Hotkeys, err)
	}
}

func TestScreenshotByHotkey(t *testing.T) {
	x := recordingHarness(t)
	a, h := x.a, x.h
	x.open(x.fixture())
	a.settings.Record.HideWindow = true
	a.Tick(Input{Hotkeys: []int{hotkeyScreenshot}})
	if !a.rec.shooting || !h.excluded {
		t.Fatalf("shooting %v, window left out %v", a.rec.shooting, h.excluded)
	}
	x.pump(time.Minute, func() bool { return !a.rec.shooting })
	if h.excluded {
		t.Error("the window stays left out of captures")
	}
	shots, _ := filepath.Glob(filepath.Join(a.settings.ExportDir, "Screenshot *.png"))
	if len(shots) != 1 {
		t.Fatalf("screenshots %v; toasts %+v", shots, a.toasts)
	}
	f, err := os.Open(shots[0])
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.DecodeConfig(f)
	f.Close()
	d, _ := a.chosenDisplay()
	if err != nil || img.Width != d.Width || img.Height != d.Height {
		t.Errorf("screenshot %dx%d (%v), screen %dx%d", img.Width, img.Height, err, d.Width, d.Height)
	}
	last := a.toasts[len(a.toasts)-1]
	action := func(label string) func() {
		for _, act := range last.actions {
			if act.label == label {
				return act.run
			}
		}
		t.Fatalf("the toast has no %q: %+v", label, last)
		return nil
	}
	overlays := len(a.p.Overlays[0].Items)
	action("Add to the edit")()
	if len(a.p.Overlays[0].Items) != overlays+1 {
		t.Error("the screenshot was not added to the edit")
	}
	// Edit opens it on a canvas in a new tab, to draw on.
	action("Edit")()
	x.pump(10*time.Second, func() bool { return a.art != nil })
	if a.art.d.Frame.Dx() != d.Width || filepath.Ext(a.art.path) != ".btcanvas" {
		t.Errorf("canvas %d wide at %s", a.art.d.Frame.Dx(), a.art.path)
	}
}

func TestPauseHotkeyWhileRecording(t *testing.T) {
	x := recordingHarness(t)
	a := x.a
	a.settings.Record.Minimize, a.settings.Record.SystemAudio = false, false
	a.Tick(Input{Hotkeys: []int{hotkeyRecord}})
	x.pump(time.Minute, func() bool { return a.rec.rec != nil || !a.rec.starting })
	if a.rec.rec == nil {
		t.Fatalf("the record hotkey did not start a recording; toasts %+v", a.toasts)
	}
	time.Sleep(time.Second)
	a.Tick(Input{Hotkeys: []int{hotkeyPause}})
	x.pump(30*time.Second, func() bool { return !a.rec.busy })
	if !a.rec.rec.Status().Paused {
		t.Fatal("the pause hotkey did not pause")
	}
	a.Tick(Input{Hotkeys: []int{hotkeyPause}})
	x.pump(30*time.Second, func() bool { return !a.rec.busy })
	if a.rec.rec.Status().Paused {
		t.Fatal("the pause hotkey did not resume")
	}
	time.Sleep(time.Second)
	a.Tick(Input{Hotkeys: []int{hotkeyRecord}})
	x.pump(time.Minute, func() bool { return a.rec.rec == nil && a.p != nil && a.opening == "" })
	if !strings.HasPrefix(filepath.Base(a.p.Sources[0].Path), "Screen recording") {
		t.Errorf("opened %s", a.p.Sources[0].Path)
	}
}

func TestWindowHotkeyHidesAndBringsBack(t *testing.T) {
	x := newHarness(t)
	a, h := x.a, x.h
	if a.recorder() == nil {
		t.Skip("hotkeys are not offered here")
	}
	if got := h.hotkeys[hotkeyWindow].String(); got != "Ctrl+Shift+F8" {
		t.Fatalf("registered %q for the window", got)
	}
	a.Tick(Input{Hotkeys: []int{hotkeyWindow}})
	if !h.hidden {
		t.Fatal("the window in front was not hidden")
	}
	a.Tick(Input{Hotkeys: []int{hotkeyWindow}})
	if h.hidden {
		t.Fatal("the hidden window did not come back")
	}
	// A minimized window comes back rather than being hidden.
	h.minimized = true
	a.Tick(Input{Hotkeys: []int{hotkeyWindow}})
	if h.minimized || h.hidden {
		t.Errorf("minimized %v, hidden %v", h.minimized, h.hidden)
	}
}
