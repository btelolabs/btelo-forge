package ui

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/keyboard"
)

// fakeKeyboard stands in for the system's keyboard in every test of this package.
type fakeKeyboard struct {
	mu      sync.Mutex
	handle  func(uint16, bool) bool
	starts  int
	stops   int
	failure error
	sent    []keyboard.Stroke
}

var keyboardFakes = struct {
	sync.Mutex
	last *fakeKeyboard
}{}

// fakeSystemMap stands in for the registry.
type fakeSystemMap struct {
	mu      sync.Mutex
	table   []keyboard.Mapping
	writes  int
	failure error
}

func (f *fakeSystemMap) Kind() keyboard.MapKind { return keyboard.RegistryMap }
func (f *fakeSystemMap) Read() ([]keyboard.Mapping, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.table, nil
}
func (f *fakeSystemMap) Write(m []keyboard.Mapping) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	if f.failure != nil {
		return f.failure
	}
	f.table = m
	return nil
}

func init() {
	newSystemMap = func() keyboard.SystemMap { return &fakeSystemMap{} }
	newKeyboardAdapter = func() keyboard.Adapter {
		f := &fakeKeyboard{}
		keyboardFakes.Lock()
		keyboardFakes.last = f
		keyboardFakes.Unlock()
		return f
	}
}

func (f *fakeKeyboard) Start(h func(uint16, bool) bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failure != nil {
		return f.failure
	}
	f.handle = h
	f.starts++
	return nil
}
func (f *fakeKeyboard) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handle = nil
	f.stops++
}
func (f *fakeKeyboard) Send(k []keyboard.Stroke) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, k...)
	return nil
}
func (f *fakeKeyboard) Type(string) error         { return nil }
func (f *fakeKeyboard) Open(string, string) error { return nil }
func (f *fakeKeyboard) KeyDown(uint16) bool       { return false }
func (f *fakeKeyboard) CanRemap(uint16) bool      { return true }

// press sends a key through the watch, and reports whether a rule took it; with no watch nothing can.
func (f *fakeKeyboard) press(vk uint16) bool {
	f.mu.Lock()
	h := f.handle
	f.mu.Unlock()
	return h != nil && h(vk, true)
}

func keyboardHarness(t *testing.T) (*harness, *fakeKeyboard) {
	t.Helper()
	x := newHarness(t)
	keyboardFakes.Lock()
	defer keyboardFakes.Unlock()
	return x, keyboardFakes.last
}

func TestKeyboardRulesStayOffUntilTheUserTurnsThemOn(t *testing.T) {
	x, f := keyboardHarness(t)
	a := x.a
	if a.settings.KeyboardRules {
		t.Fatal("on by default")
	}
	s := connectMCP(t, x)
	config := (&keyboard.Config{Version: 1, Rules: []string{"F1 -> F2"}}).JSON()
	var got struct {
		Summary struct {
			Active bool `json:"active"`
			Rules  int  `json:"rules"`
		} `json:"summary"`
	}
	s.ok("set_config", map[string]any{"name": keyboardRules, "json": config}, &got)
	if got.Summary.Active || got.Summary.Rules != 1 {
		t.Fatalf("%+v", got)
	}
	a.openKeyboardTab()
	x.frame(Input{})
	if f.starts != 0 || f.press(0x70) {
		t.Fatal("rules from an AI began a keyboard hook")
	}
	res := s.call("set_config", map[string]any{"name": keyboardRules, "json": `{"version":1,"enabled":true,"rules":[]}`})
	if !res.IsError {
		t.Fatal("a configuration turned the rules on")
	}
	if strings.Contains(keyboard.FormatGuide, `"enabled"`) {
		t.Fatal("the guide offers a switch")
	}

	a.keys.asking = true
	x.frame(Input{})
	if f.starts != 0 {
		t.Fatal("explaining turned the rules on")
	}
	a.setKeyboardActive(true)
	x.frame(Input{})
	if f.starts != 1 || !f.press(0x70) {
		t.Fatalf("turned on: %d starts", f.starts)
	}
	data, err := os.ReadFile(a.settingsPath())
	if err != nil {
		t.Fatal(err)
	}
	var saved settings
	if err := json.Unmarshal(data, &saved); err != nil || !saved.KeyboardRules {
		t.Fatalf("the choice was not kept: %v", err)
	}
	a.setKeyboardActive(false)
	if f.stops != 1 {
		t.Fatal("turned off, the hook stayed")
	}
}

func TestSavingKeyboardRules(t *testing.T) {
	x, f := keyboardHarness(t)
	a := x.a
	a.setKeyboardActive(true)
	a.openKeyboardTab()
	x.frame(Input{})

	a.keys.rules.text = ""
	a.addExample(&a.keys.rules, keyboardExamples[1].rule)
	a.addExample(&a.keys.rules, keyboardExamples[5].rule)
	if a.keys.rules.text != "F1 -> Ctrl+Z\n;mail => me@example.com" {
		t.Fatalf("%q", a.keys.rules.text)
	}
	a.saveKeyboardRules()
	x.frame(Input{})
	if a.keys.problem != nil || !f.press(0x70) {
		t.Fatalf("hot update: %v", a.keys.problem)
	}
	if f.starts != 1 {
		t.Fatalf("saving restarted the hook: %d", f.starts)
	}

	// A mistake is saved and reported, and no rule works until it is put right.
	a.keys.rules.text = "F1 -> Ctrl+Z\nF2 -> Nothing"
	a.saveKeyboardRules()
	x.frame(Input{})
	if !strings.Contains(a.describeRule(a.keys.problem), "line 2") || a.keys.rules.text != "F1 -> Ctrl+Z\nF2 -> Nothing" {
		t.Fatalf("%v, %q", a.keys.problem, a.keys.rules.text)
	}
	data, err := os.ReadFile(a.configPath(mustConfig(t, keyboardRules)))
	if err != nil || !strings.Contains(string(data), "Nothing") {
		t.Fatalf("the rules with a mistake were not saved: %v", err)
	}
	if f.press(0x70) || a.keys.service.Watching() {
		t.Fatal("a rule worked with a mistake in the file")
	}
	status, _ := a.keyboardStatus()
	if !strings.Contains(status, "mistake") {
		t.Fatal(status)
	}

	a.keys.rules.text = "F1 -> Ctrl+Z"
	a.saveKeyboardRules()
	if a.keys.problem != nil || !f.press(0x70) {
		t.Fatalf("corrected: %v", a.keys.problem)
	}
}

func mustConfig(t *testing.T, id string) configKind {
	t.Helper()
	k, err := findConfig(id)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestAnAIChangesKeyboardRulesAndThePageFollows(t *testing.T) {
	x, f := keyboardHarness(t)
	a := x.a
	a.setKeyboardActive(true)
	a.openKeyboardTab()
	x.frame(Input{})
	s := connectMCP(t, x)

	var read struct {
		Guide string `json:"guide"`
		JSON  string `json:"json"`
	}
	s.ok("get_config", map[string]any{"name": keyboardRules}, &read)
	if !strings.Contains(read.Guide, "CapsLock -> Esc") {
		t.Fatalf("%q", read.Guide)
	}
	bad := (&keyboard.Config{Version: 1, Rules: []string{"F1 -> Nope"}}).JSON()
	var saved struct {
		Summary struct {
			Rules   int    `json:"rules"`
			Problem string `json:"problem"`
		} `json:"summary"`
	}
	s.ok("set_config", map[string]any{"name": keyboardRules, "json": bad}, &saved)
	if !strings.Contains(saved.Summary.Problem, "line 1") || saved.Summary.Rules != 0 {
		t.Fatalf("%+v", saved)
	}
	good := (&keyboard.Config{Version: 1, Rules: []string{"F3 -> F4"}}).JSON()
	s.ok("set_config", map[string]any{"name": keyboardRules, "json": good}, &saved)
	x.frame(Input{})
	if a.keys.rules.text != "F3 -> F4" || !f.press(0x72) {
		t.Fatalf("page %q", a.keys.rules.text)
	}

	a.keys.rules.text = "F3 -> F4\n# mine"
	s.ok("set_config", map[string]any{"name": keyboardRules, "json": bad}, &saved)
	x.frame(Input{})
	if a.keys.rules.text != "F3 -> F4\n# mine" {
		t.Fatalf("the user's unsaved text was replaced: %q", a.keys.rules.text)
	}
	s.ok("reset_config", map[string]any{"name": keyboardRules}, &saved)
}

func TestKeyboardRulesProblemsAreTranslated(t *testing.T) {
	x, _ := keyboardHarness(t)
	a := x.a
	a.u.lang = Chinese
	for _, line := range []string{"nonsense", "Foo -> Esc", "a => x", `A -> type "x`, "A ->"} {
		_, err := keyboard.Parse([]string{line})
		if got := a.describeRule(err); !strings.Contains(got, "第 1 行") {
			t.Errorf("%s: %s", line, got)
		}
	}
}

func TestInsertLine(t *testing.T) {
	for _, c := range []struct {
		text  string
		caret int
		want  string
		after int
	}{
		{"", 0, "X", 1},
		{"a\n\nb", 2, "a\nX\nb", 3},
		{"a\n  \nb", 3, "a\nX\nb", 3},
		{"ab\ncd", 1, "ab\nX\ncd", 4},
		{"ab\ncd", 5, "ab\ncd\nX", 7},
		{"中文\n", 3, "中文\nX", 4},
		{"ab", 99, "ab\nX", 4},
	} {
		got, after := insertLine(c.text, c.caret, "X")
		if got != c.want || after != c.after {
			t.Errorf("%q at %d: %q at %d", c.text, c.caret, got, after)
		}
	}
}

func TestExamplesGoWhereTheCaretWas(t *testing.T) {
	x := newHarness(t)
	a := x.a
	a.openKeyboardTab()
	x.frame(Input{})
	p := &a.keys.rules
	p.text = "F1 -> F2\n\nF3 -> F4"
	a.u.beginEdit("keyboard/rules/code", p.text, true, 9, 9)
	x.frame(Input{})
	a.u.focus = "" // clicking the button takes the keyboard from the editor
	x.frame(Input{})
	a.addExample(p, "CapsLock -> Esc")
	x.frame(Input{})
	if p.text != "F1 -> F2\nCapsLock -> Esc\nF3 -> F4" {
		t.Fatalf("%q", p.text)
	}
	// The caret stays after the rule, so typing goes on changing it.
	x.frame(Input{Typed: []rune("!")})
	if p.text != "F1 -> F2\nCapsLock -> Esc!\nF3 -> F4" {
		t.Fatalf("%q", p.text)
	}
}

func TestTheStartPageOpensTheKeyboardPage(t *testing.T) {
	x := newHarness(t)
	for _, c := range x.a.startCards() {
		if c.id == "empty/keyboard" {
			c.run()
			if !x.a.keyboardTab || x.a.settingsOpen || len(x.a.docs) != 1 {
				t.Fatalf("tab %v, settings %v, %d tabs", x.a.keyboardTab, x.a.settingsOpen, len(x.a.docs))
			}
			x.frame(Input{})
			if got := x.a.docs[0].title(x.a); got != "Keyboard" {
				t.Fatal(got)
			}
			x.a.newTab()
			c.run()
			if len(x.a.docs) != 2 || !x.a.keyboardTab {
				t.Fatalf("a second tab: %d", len(x.a.docs))
			}
			if slices.Contains(settingsPages, "Keyboard") {
				t.Fatal("the tool is in Settings")
			}
			return
		}
	}
	t.Fatal("no keyboard card")
}

func TestKeyboardPageScreens(t *testing.T) {
	if os.Getenv("FORGE_SHOTS") == "" {
		t.Skip("set FORGE_SHOTS to a folder to render the screens")
	}
	x, _ := keyboardHarness(t)
	a := x.a
	a.u.lang = Chinese
	x.frame(Input{})
	x.frame(Input{})
	x.shot("zh-start")
	a.openKeyboardTab()
	for _, lang := range []Language{English, Chinese} {
		a.u.lang = lang
		x.frame(Input{})
		x.shot(string(lang) + "-keyboard-off")
		a.keys.asking = true
		x.frame(Input{})
		x.shot(string(lang) + "-keyboard-consent")
		a.setKeyboardActive(true)
		for _, e := range keyboardExamples {
			a.addExample(&a.keys.rules, e.rule)
		}
		x.frame(Input{})
		x.shot(string(lang) + "-keyboard-editing")
		a.saveKeyboardRules()
		x.frame(Input{})
		x.shot(string(lang) + "-keyboard-on")
		a.keys.rules.text += "\nF2 -> Nothing"
		a.saveKeyboardRules()
		x.frame(Input{})
		x.frame(Input{})
		x.shot(string(lang) + "-keyboard-mistake")
		a.setKeyboardActive(false)
		a.keys.rules.text = ""
		a.saveKeyboardRules()
		a.keys.panel = keyboardRemapPanel
		x.frame(Input{})
		a.addExample(&a.keys.remaps, "CapsLock -> Esc")
		a.addExample(&a.keys.remaps, "Right-Alt -> Left-Ctrl")
		x.frame(Input{})
		x.shot(string(lang) + "-keyboard-remap")
		a.keys.remaps.text += "\nCtrl+A -> B"
		a.saveKeyboardRemaps()
		x.frame(Input{})
		x.frame(Input{})
		x.shot(string(lang) + "-keyboard-remap-mistake")
		a.keys.remaps.text = "CapsLock -> Esc\nRi"
		u := &a.u
		u.focus = "keyboard/remap/code"
		x.frame(Input{})
		u.edit.caret, u.edit.anchor = len([]rune(a.keys.remaps.text)), len([]rune(a.keys.remaps.text))
		x.frame(Input{})
		x.frame(Input{})
		x.shot(string(lang) + "-keyboard-complete")
		u.focus = ""
		a.keys.remaps.text = ""
		a.saveKeyboardRemaps()
		a.keys.panel = keyboardRulesPanel
	}
}

func remapHarness(t *testing.T) (*harness, *fakeSystemMap) {
	t.Helper()
	x := newHarness(t)
	f := &fakeSystemMap{}
	x.a.keys.system = f
	x.a.openKeyboardTab()
	x.a.keys.panel = keyboardRemapPanel
	return x, f
}

func TestWritingSystemRemaps(t *testing.T) {
	x, f := remapHarness(t)
	a := x.a
	x.frame(Input{})
	a.addExample(&a.keys.remaps, "Right-Alt -> Left-Ctrl")
	if f.writes != 0 {
		t.Fatal("editing reached the registry")
	}
	a.saveKeyboardRemaps()
	if f.writes != 0 || a.remapWritten() {
		t.Fatal("saving reached the registry")
	}
	a.writeRemaps()
	x.pump(5*time.Second, func() bool { return !a.keys.writing })
	if f.writes != 1 || !a.remapWritten() || len(f.table) != 1 || f.table[0].From != 0xe038 {
		t.Fatalf("%+v", f.table)
	}
	x.frame(Input{})

	// A mistake is kept, and nothing is written.
	a.keys.remaps.text = "Ctrl+A -> B"
	a.saveKeyboardRemaps()
	a.writeRemaps()
	x.frame(Input{})
	if f.writes != 1 || a.keys.remapProblem == nil || !strings.Contains(a.describeRemap(a.keys.remapProblem), "line 1") {
		t.Fatalf("%d, %v", f.writes, a.keys.remapProblem)
	}
	if data, err := os.ReadFile(a.configPath(mustConfig(t, keyboardRemap))); err != nil || !strings.Contains(string(data), "Ctrl+A") {
		t.Fatalf("not kept: %v", err)
	}

	// Turned down at the prompt: it says so, and the table stays.
	a.keys.remaps.text = "CapsLock -> Esc"
	f.failure = keyboard.ErrCancelled
	a.writeRemaps()
	x.pump(5*time.Second, func() bool { return !a.keys.writing })
	if a.remapWritten() || f.table[0].From != 0xe038 {
		t.Fatal("a turned-down write counted")
	}
}

func TestRemapsMadeElsewhereShow(t *testing.T) {
	x, f := remapHarness(t)
	f.table = []keyboard.Mapping{{From: 0x3a, To: 0x1d}}
	x.frame(Input{})
	if x.a.keys.remaps.text != "CapsLock -> Left-Ctrl" || !x.a.keys.imported {
		t.Fatalf("%q", x.a.keys.remaps.text)
	}
}

func TestAnAIChangesRemapsButCannotWriteThem(t *testing.T) {
	x, f := remapHarness(t)
	s := connectMCP(t, x)
	var got struct {
		Summary struct {
			Remaps  int    `json:"remaps"`
			Written bool   `json:"written"`
			Problem string `json:"problem"`
		} `json:"summary"`
	}
	config := (&keyboard.Config{Version: 1, Rules: []string{"CapsLock -> Esc"}}).JSON()
	s.ok("set_config", map[string]any{"name": keyboardRemap, "json": config}, &got)
	x.frame(Input{})
	if got.Summary.Remaps != 1 || got.Summary.Written || f.writes != 0 || x.a.keys.remaps.text != "CapsLock -> Esc" {
		t.Fatalf("%+v, %d, %q", got, f.writes, x.a.keys.remaps.text)
	}
}

func TestTabCompletesKeyNames(t *testing.T) {
	x := newHarness(t)
	a := x.a
	a.openKeyboardTab()
	x.frame(Input{})
	a.keys.rules.text = ""
	a.u.focus = "keyboard/rules/code"
	x.frame(Input{})
	x.frame(Input{Typed: []rune("cap")})
	x.frame(Input{Keys: []Key{{Code: keyTab}}})
	x.frame(Input{Typed: []rune(" -> ri")})
	x.frame(Input{Keys: []Key{{Code: keyDown}}})
	x.frame(Input{Keys: []Key{{Code: keyEnter}}})
	if a.keys.rules.text != "CapsLock -> Right-Win" { // the second offer, after Right
		t.Fatalf("%q", a.keys.rules.text)
	}
	x.frame(Input{Keys: []Key{{Code: keyEnter}}})
	if a.keys.rules.text != "CapsLock -> Right-Win\n" {
		t.Fatalf("Enter with no offers: %q", a.keys.rules.text)
	}
	x.frame(Input{Typed: []rune("Cap")})
	x.frame(Input{Keys: []Key{{Code: keyEscape}}})
	if a.u.focus == "" {
		t.Fatal("Esc left the editor rather than the offers")
	}
	x.frame(Input{Keys: []Key{{Code: keyTab}}})
	if !strings.HasSuffix(a.keys.rules.text, "Cap  ") {
		t.Fatalf("offers came back: %q", a.keys.rules.text)
	}
}
