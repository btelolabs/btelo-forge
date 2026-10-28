package ui

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
)

func TestShortcutIDsAreUnique(t *testing.T) {
	x := newHarness(t)
	seen := map[string]bool{}
	for d := domainGeneral; d < domainRecording; d++ {
		list := x.a.shortcutsOf(d)
		if len(list) == 0 {
			t.Errorf("domain %d has no commands", d)
		}
		for _, s := range list {
			if seen[s.id] || s.id == "" || s.label == "" {
				t.Errorf("command %+v: a repeated or empty id or name", s)
			}
			seen[s.id] = true
		}
	}
	// The defaults never clash within a domain; only tools share.
	for d := domainGeneral; d < domainRecording; d++ {
		for _, s := range x.a.shortcutsOf(d) {
			for _, k := range s.defaults {
				if other := x.a.conflict(d, s.id, s.tool, k); other != "" {
					t.Errorf("%s: %s is also the key for %s", s.label, hotkeyOf(k), other)
				}
			}
		}
	}
}

func TestShortcutsAreChosenForEachDomain(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	a.settingsOpen, a.settingsPage, a.keysDomain = true, settingsHotkeysPage, domainDrawing
	x.frame(Input{})
	x.shot("settings-keys-drawing")
	// Choosing: the brush tool's key becomes K, once Mark as slice lets it go (every letter has a use by
	// default); a key with Ctrl is refused for a tool.
	a.setKeys("draw.mark-slice", nil)
	a.keyCapture = &keyCapture{id: toolID(toolBrush), domain: domainDrawing, tool: true}
	x.frame(Input{Keys: []Key{{Code: 'K', Ctrl: true}}})
	if a.keyCapture == nil || !strings.Contains(a.keyCapture.refused, "single key") {
		t.Fatalf("a tool key with Ctrl was taken: %+v", a.keyCapture)
	}
	x.frame(Input{})
	x.shot("settings-keys-choosing")
	x.frame(Input{Keys: []Key{{Code: 'K'}}})
	if a.keyCapture != nil {
		t.Fatalf("K was not taken: %+v", a.keyCapture)
	}
	// Ctrl+J, Duplicate layer's, is refused for Copy; Ctrl+Alt+C is Canvas size's.
	a.keyCapture = &keyCapture{id: "draw.copy", domain: domainDrawing}
	x.frame(Input{Keys: []Key{{Code: 'J', Ctrl: true}}})
	if a.keyCapture == nil || !strings.Contains(a.keyCapture.refused, "Ctrl+J") {
		t.Fatalf("a key of another command was taken: %+v", a.keyCapture)
	}
	x.frame(Input{Keys: []Key{{Code: 'K', Ctrl: true, Shift: true}}})
	if a.keyCapture != nil {
		t.Fatal("Ctrl+Shift+K was not taken")
	}
	// Esc while waiting cancels; leaving the page ends the wait too.
	a.keyCapture = &keyCapture{id: "draw.paste", domain: domainDrawing}
	x.frame(Input{Keys: []Key{{Code: keyEscape}}})
	if a.keyCapture != nil || !a.settingsOpen {
		t.Fatalf("Esc: %+v, open %v", a.keyCapture, a.settingsOpen)
	}
	a.keyCapture = &keyCapture{id: "draw.paste", domain: domainDrawing}
	a.settingsOpen = false
	x.frame(Input{})
	if a.keyCapture != nil {
		t.Fatal("closing Settings left a key being chosen")
	}

	// The new keys work on the canvas: Q picks the brush, B no longer does; Ctrl+Shift+K copies.
	x.key('E', false)
	x.key('B', false)
	if a.artTool != toolEraser {
		t.Errorf("B still picks a tool: %d", a.artTool)
	}
	x.key('K', false)
	if a.artTool != toolBrush {
		t.Errorf("K picked tool %d", a.artTool)
	}
	ad.d.SelectAll()
	if got := a.keyHint("draw.copy", nil); got != "Ctrl+Shift+K" {
		t.Errorf("copy's key shows as %q", got)
	}
	// The video editor's split moves to X; S no longer splits.
	a.setKeys("video.split", []Key{{Code: 'X'}})
	cmds := a.appCommands()
	for _, c := range cmds {
		if c.id == "video.split" && !a.bound(c.id, c.defaults, Key{Code: 'X'}) || c.id == "video.split" && a.bound(c.id, c.defaults, Key{Code: 'S'}) {
			t.Error("split is not on X alone")
		}
	}
	// The choices are kept between runs, by command id.
	data, err := os.ReadFile(a.settingsPath())
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Keys map[string][]Hotkey `json:"keys"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Keys[toolID(toolBrush)][0].String() != "K" || saved.Keys["draw.copy"][0].String() != "Ctrl+Shift+K" || saved.Keys["video.split"][0].String() != "X" {
		t.Errorf("saved keys %v", saved.Keys)
	}
	// Restoring a domain's defaults leaves the others' choices.
	a.settingsOpen, a.keysDomain = true, domainDrawing
	for _, s := range a.shortcutsOf(domainDrawing) {
		delete(a.settings.Keys, s.id)
	}
	if _, ok := a.settings.Keys["video.split"]; !ok || len(a.toolKeys(toolBrush)) != 1 || a.toolKeys(toolBrush)[0].Code != 'B' {
		t.Errorf("after restoring drawing: %v", a.settings.Keys)
	}
}

func TestVideoShortcutsFollowTheChosenKeys(t *testing.T) {
	x := newHarness(t)
	x.open(x.fixture())
	a := x.a
	clips := len(a.p.Video[0].Clips)
	a.seek(1)
	a.setKeys("video.split", []Key{{Code: 'K', Ctrl: true}})
	x.key('S', false)
	if len(a.p.Video[0].Clips) != clips {
		t.Fatal("S still splits")
	}
	x.key('K', true)
	if len(a.p.Video[0].Clips) != clips+1 {
		t.Errorf("Ctrl+K did not split: %d clips", len(a.p.Video[0].Clips))
	}
	a.settingsOpen, a.settingsPage, a.keysDomain = true, settingsHotkeysPage, domainVideo
	x.frame(Input{})
	x.shot("settings-keys-video")
}

func TestCtrlDigitsGoToTabsEverywhere(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	canvas := a.doc
	a.newTab()
	x.open(x.fixture())
	video := a.doc
	a.newTab()
	empty := a.doc
	x.frame(Input{})
	if len(a.docs) != 3 || a.docs[1] != video {
		t.Fatalf("%d tabs, the video's at %d", len(a.docs), a.current())
	}
	// From the canvas, Ctrl+3 goes to the empty tab, and from the video the number pad's 2 with Ctrl comes back.
	a.switchTab(0)
	x.frame(Input{})
	x.key('2', true)
	if a.doc != video {
		t.Fatalf("Ctrl+2 on a canvas did not go to the second tab")
	}
	x.key(keyNum0+3, true)
	if a.doc != empty {
		t.Fatalf("Ctrl+Num3 on a video did not go to the third tab")
	}
	// A tab that is not there leaves the tab as it is.
	x.key('9', true)
	if a.doc != empty {
		t.Fatalf("Ctrl+9 with three tabs changed the tab")
	}
	x.key('1', true)
	if a.doc != canvas {
		t.Fatalf("Ctrl+1 did not go to the first tab")
	}
	// The canvas's zoom keys gave Ctrl with digits, = and - up: they have Alt too.
	x.frame(Input{Keys: []Key{{Code: '1', Ctrl: true, Alt: true}}})
	if ad.zoom != 1 || ad.fit {
		t.Fatalf("Ctrl+Alt+1 did not zoom to 100%%: zoom %v, fit %v", ad.zoom, ad.fit)
	}
	x.frame(Input{Keys: []Key{{Code: keyEquals, Ctrl: true, Alt: true}}})
	if ad.zoom != 1.25 || a.windowZoom() != 1 {
		t.Fatalf("Ctrl+Alt+= zoomed the canvas to %v and the window to %v", ad.zoom, a.windowZoom())
	}
	x.frame(Input{Keys: []Key{{Code: keyMinus, Ctrl: true, Alt: true}}})
	if ad.zoom != 1 {
		t.Fatalf("Ctrl+Alt+- zoomed the canvas to %v", ad.zoom)
	}
	x.frame(Input{Keys: []Key{{Code: '0', Ctrl: true, Alt: true}}})
	if !ad.fit {
		t.Fatal("Ctrl+Alt+0 did not fit the picture")
	}
	// Even a canvas command the user gave Ctrl+3 before yields it.
	a.settings.Keys = map[string][]Hotkey{"draw.100pct": {{Ctrl: true, Key: '3'}}}
	x.key('3', true)
	if a.doc != empty {
		t.Fatal("a canvas command bound to Ctrl+3 kept it from the tabs")
	}
}

func TestTabKeysAreReservedInEveryDomain(t *testing.T) {
	x := newHarness(t)
	a := x.a
	ctrl3 := Key{Code: '3', Ctrl: true}
	for _, d := range []int{domainVideo, domainDrawing} {
		id := a.shortcutsOf(d)[0].id
		if other := a.conflict(d, id, false, ctrl3); other != a.u.tf("Go to tab %d", 3) {
			t.Errorf("domain %d: Ctrl+3 for %s clashes with %q, not the third tab", d, id, other)
		}
	}
	if other := a.conflict(domainDrawing, "draw.zoom-in", false, Key{Code: keyEquals, Ctrl: true}); other != a.u.t("Zoom the window in") {
		t.Errorf("Ctrl+= for the canvas's zoom clashes with %q, not the window's zoom", other)
	}
	// A tab's key may not be one a domain's command has: Ctrl+J is Duplicate layer's.
	if other := a.conflict(domainGeneral, "general.tab.3", false, Key{Code: 'J', Ctrl: true}); other == "" {
		t.Error("Ctrl+J was free for a tab though a canvas command has it")
	}
	// Other general commands share keys with the domains', which come first in their tabs.
	if other := a.conflict(domainGeneral, "general.open", false, Key{Code: 'J', Ctrl: true}); other != "" {
		t.Errorf("Ctrl+J for Open clashes with %q", other)
	}
	a.settingsOpen, a.settingsPage, a.keysDomain = true, settingsHotkeysPage, domainGeneral
	x.frame(Input{})
	x.shot("settings-keys-general")
}

func TestCtrlZoomsTheWholeWindow(t *testing.T) {
	x, ad := canvasHarness(t)
	a := x.a
	press := func(k Key) {
		t.Helper()
		x.frame(Input{Keys: []Key{k}})
	}
	x.frame(Input{Keys: []Key{{Code: '1', Ctrl: true, Alt: true}}}) // the canvas at 100%
	steps := []struct {
		k    Key
		zoom float32
	}{
		{Key{Code: keyEquals, Ctrl: true}, 1.1},
		{Key{Code: keyEquals, Ctrl: true, Shift: true}, 1.25}, // Ctrl and +
		{Key{Code: keyNumPlus, Ctrl: true}, 1.5},
		{Key{Code: keyMinus, Ctrl: true}, 1.25},
		{Key{Code: keyNumMinus, Ctrl: true}, 1.1},
	}
	for _, s := range steps {
		press(s.k)
		if a.windowZoom() != s.zoom || a.u.s != x.scale*s.zoom {
			t.Fatalf("%s: the window's zoom is %v, its scale %v", hotkeyOf(s.k), a.windowZoom(), a.u.s)
		}
	}
	// The canvas grew with the interface, and the zoom is kept.
	if math.Abs(ad.zoom-1.1) > 1e-6 || a.settings.Zoom != 1.1 {
		t.Fatalf("the canvas is at %v, the zoom kept %v", ad.zoom, a.settings.Zoom)
	}
	// Pressing again and again shows one toast, the latest.
	n := 0
	for _, to := range a.toasts {
		if to.kind == "zoom" {
			n++
			if to.text != "Zoom 110%" {
				t.Errorf("the zoom's toast says %q", to.text)
			}
		}
	}
	if n != 1 {
		t.Errorf("%d zoom toasts", n)
	}
	x.shot("window-zoom-canvas")
	// Ctrl+0 brings the window and the canvas back, and the zoom is no longer written.
	press(Key{Code: '0', Ctrl: true})
	if a.windowZoom() != 1 || math.Abs(ad.zoom-1) > 1e-6 || a.settings.Zoom != 0 {
		t.Fatalf("Ctrl+0: the window at %v, the canvas at %v, kept %v", a.windowZoom(), ad.zoom, a.settings.Zoom)
	}
	// The steps stop at the ends.
	for range 20 {
		press(Key{Code: keyEquals, Ctrl: true})
	}
	if a.windowZoom() != 3 {
		t.Fatalf("zoomed in to %v", a.windowZoom())
	}
	press(Key{Code: keyNum0, Ctrl: true})
	for range 20 {
		press(Key{Code: keyMinus, Ctrl: true})
	}
	if a.windowZoom() != 0.5 {
		t.Fatalf("zoomed out to %v", a.windowZoom())
	}
	press(Key{Code: '0', Ctrl: true})
	// The zoom acts while typing in a field and with Settings open; the tabs wait for Settings to close.
	a.u.focus = "some/field"
	press(Key{Code: keyEquals, Ctrl: true})
	if a.windowZoom() != 1.1 {
		t.Fatalf("Ctrl+= while typing left the zoom at %v", a.windowZoom())
	}
	a.u.focus = ""
	a.newTab()
	a.switchTab(0)
	a.settingsOpen = true
	press(Key{Code: keyEquals, Ctrl: true})
	press(Key{Code: '2', Ctrl: true})
	if a.windowZoom() != 1.25 || a.current() != 0 {
		t.Fatalf("with Settings open: the zoom %v, tab %d", a.windowZoom(), a.current())
	}
	x.shot("window-zoom-settings")
	// Except while Settings waits for a key, which takes Ctrl+= as the key.
	a.settingsPage, a.keysDomain = settingsHotkeysPage, domainGeneral
	a.keyCapture = &keyCapture{id: "general.paste", domain: domainGeneral}
	press(Key{Code: keyEquals, Ctrl: true})
	if a.windowZoom() != 1.25 || a.keyCapture == nil || !strings.Contains(a.keyCapture.refused, "Zoom the window in") {
		t.Fatalf("choosing a key: the zoom %v, %+v", a.windowZoom(), a.keyCapture)
	}
}
