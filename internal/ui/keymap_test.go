package ui

import (
	"encoding/json"
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
	// Choosing: the brush tool's key becomes Q; a key with Ctrl is refused for a tool.
	a.keyCapture = &keyCapture{id: toolID(toolBrush), domain: domainDrawing, tool: true}
	x.frame(Input{Keys: []Key{{Code: 'Q', Ctrl: true}}})
	if a.keyCapture == nil || !strings.Contains(a.keyCapture.refused, "single key") {
		t.Fatalf("a tool key with Ctrl was taken: %+v", a.keyCapture)
	}
	x.frame(Input{})
	x.shot("settings-keys-choosing")
	x.frame(Input{Keys: []Key{{Code: 'Q'}}})
	if a.keyCapture != nil {
		t.Fatalf("Q was not taken: %+v", a.keyCapture)
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
	x.key('Q', false)
	if a.artTool != toolBrush {
		t.Errorf("Q picked tool %d", a.artTool)
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
	if saved.Keys[toolID(toolBrush)][0].String() != "Q" || saved.Keys["draw.copy"][0].String() != "Ctrl+Shift+K" || saved.Keys["video.split"][0].String() != "X" {
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
