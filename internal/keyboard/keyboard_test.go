package keyboard

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeWorld is a keyboard and a program in front, for the engine alone.
type fakeWorld struct {
	down map[uint16]bool
}

func (w *fakeWorld) KeyDown(vk uint16) bool { return w.down[vk] }

func engine(t *testing.T, lines ...string) *Engine {
	t.Helper()
	rules, err := Parse(lines)
	if err != nil {
		t.Fatal(err)
	}
	return NewEngine(rules)
}

func TestParseRules(t *testing.T) {
	rules, err := Parse(strings.Split(`# comment

CapsLock -> Esc
Right-Alt -> Ctrl
Ctrl+Alt+T -> open "C:\\Program Files\\x.exe" --new -v
Win+B -> open https://github.com
Ctrl+Alt+D -> type "{date}\n"
;mail => a@b.c
;sig => "two  spaces"
F1 -> Ctrl+Z`, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 8 {
		t.Fatalf("%d rules: %+v", len(rules), rules)
	}
	if r := rules[0]; r.From.Key != vkCapsLock || r.To.Key != 0x1b || !r.hold() || r.Line != 3 {
		t.Errorf("remap %+v", r)
	}
	if r := rules[1]; r.From.Key != vkRAlt || r.To.Key != vkCtrl {
		t.Errorf("side %+v", r)
	}
	if r := rules[2]; r.Kind != OpenItem || r.Text != `C:\Program Files\x.exe` || r.Args != "--new -v" || !r.From.Ctrl || !r.From.Alt {
		t.Errorf("open %+v", r)
	}
	if r := rules[3]; r.Kind != OpenItem || r.Text != "https://github.com" || !r.From.Win {
		t.Errorf("address %+v", r)
	}
	if r := rules[4]; r.Kind != TypeText || r.Text != "{date}\n" {
		t.Errorf("type %+v", r)
	}
	if r := rules[6]; r.Abbr != ";sig" || r.Text != "two  spaces" {
		t.Errorf("quoted abbreviation %+v", r)
	}
	if r := rules[7]; r.To != (Combo{Ctrl: true, Key: 'Z'}) || r.hold() {
		t.Errorf("combination %+v", r)
	}
}

func TestOnlyEnglishWords(t *testing.T) {
	for _, line := range []string{"右Alt -> Esc", `F1 -> 打开 "x"`, `F1 -> 输入 "x"`, "F1 -> 回车", "F1 → F2"} {
		if _, err := Parse([]string{line}); err == nil {
			t.Errorf("%s was taken", line)
		}
	}
}

func TestRuleProblems(t *testing.T) {
	for _, c := range []struct {
		lines   []string
		line    int
		problem int
	}{
		{[]string{"nonsense"}, 1, BadLine},
		{[]string{"", "Foo -> Esc"}, 2, BadKey},
		{[]string{"A -> Bar"}, 1, BadKey},
		{[]string{"A ->"}, 1, EmptyAction},
		{[]string{`A -> type "open`}, 1, BadText},
		{[]string{"中文 => x"}, 1, BadAbbr},
		{[]string{"a => x"}, 1, BadAbbr},
		{[]string{"A -> B", "# skip", "a -> C"}, 3, DoubleTrigger},
	} {
		_, err := Parse(c.lines)
		var re *RuleError
		if !errors.As(err, &re) || re.Line != c.line || re.Problem != c.problem {
			t.Errorf("%q: %v", c.lines, err)
		}
	}
}

func TestComboNames(t *testing.T) {
	for _, text := range []string{"Ctrl+Alt+Shift+Win+F24", "Num+", "Ctrl+Num+", "Right-Ctrl", "Ctrl+;", "Num7", "PageDown"} {
		c, ok := ParseCombo(text)
		if !ok || c.String() != text {
			t.Errorf("%s came back as %s, %v", text, c, ok)
		}
	}
	if _, ok := ParseCombo("A+B"); ok {
		t.Error("two keys that are not modifiers were taken")
	}
}

func TestRemapHoldsAsLongAsTheKey(t *testing.T) {
	e := engine(t, "CapsLock -> Ctrl")
	w := &fakeWorld{}
	out := e.Key(vkCapsLock, true, w)
	if !out.Swallow || !slices.Equal(out.Keys, []Stroke{{vkCtrl, true}}) {
		t.Fatalf("press %+v", out)
	}
	if out = e.Key(vkCapsLock, true, w); !slices.Equal(out.Keys, []Stroke{{vkCtrl, true}}) {
		t.Fatalf("repeat %+v", out)
	}
	if out = e.Key(vkCapsLock, false, w); !out.Swallow || !slices.Equal(out.Keys, []Stroke{{vkCtrl, false}}) {
		t.Fatalf("release %+v", out)
	}
	if out = e.Key('A', true, w); out.Swallow || out.Keys != nil {
		t.Fatalf("another key %+v", out)
	}
}

func TestReleaseLetsGoOfHeldKeys(t *testing.T) {
	e := engine(t, "CapsLock -> Ctrl", "F1 -> Ctrl+Z")
	w := &fakeWorld{}
	e.Key(vkCapsLock, true, w)
	e.Key(0x70, true, w)
	if keys := e.Release(); !slices.Equal(keys, []Stroke{{vkCtrl, false}}) {
		t.Fatalf("%+v", keys)
	}
	if out := e.Key(vkCapsLock, false, w); out.Swallow {
		t.Fatalf("the release after it was taken: %+v", out)
	}
}

func TestComboTapsWithOnlyItsModifiers(t *testing.T) {
	e := engine(t, "Ctrl+J -> Down", "F1 -> Ctrl+Z")
	w := &fakeWorld{down: map[uint16]bool{vkLCtrl: true}}
	out := e.Key('J', true, w)
	want := []Stroke{{vkLCtrl, false}, {0x28, true}, {0x28, false}, {vkLCtrl, true}}
	if !out.Swallow || !slices.Equal(out.Keys, want) {
		t.Fatalf("Ctrl+J %+v", out)
	}
	if out = e.Key('J', true, w); !slices.Equal(out.Keys, want) {
		t.Fatalf("repeat %+v", out)
	}
	if out = e.Key('J', false, w); !out.Swallow || out.Keys != nil {
		t.Fatalf("release %+v", out)
	}
	w.down = nil
	out = e.Key(0x70, true, w)
	want = []Stroke{{vkLCtrl, true}, {'Z', true}, {'Z', false}, {vkLCtrl, false}}
	if !slices.Equal(out.Keys, want) {
		t.Fatalf("F1 %+v", out)
	}
}

func TestModifiersMustMatchExactly(t *testing.T) {
	e := engine(t, "Ctrl+J -> Down")
	w := &fakeWorld{down: map[uint16]bool{vkRCtrl: true, vkLShift: true}}
	if out := e.Key('J', true, w); out.Swallow {
		t.Fatalf("Ctrl+Shift+J took Ctrl+J's rule: %+v", out)
	}
	w.down = map[uint16]bool{vkRCtrl: true}
	if out := e.Key('J', true, w); !out.Swallow {
		t.Fatal("the right Ctrl did not match Ctrl")
	}
}

func TestWinAndAltAreMasked(t *testing.T) {
	e := engine(t, `Win+B -> open "x"`, "Alt+Q -> Esc")
	w := &fakeWorld{down: map[uint16]bool{vkLWin: true}}
	out := e.Key('B', true, w)
	if out.Open != "x" || !slices.Equal(out.Keys, []Stroke{{vkMask, true}, {vkMask, false}}) {
		t.Fatalf("Win+B %+v", out)
	}
	w.down = map[uint16]bool{vkLAlt: true}
	out = e.Key('Q', true, w)
	if len(out.Keys) < 2 || out.Keys[0] != (Stroke{vkMask, true}) {
		t.Fatalf("Alt+Q %+v", out)
	}
}

func TestTypingLetsModifiersGo(t *testing.T) {
	e := engine(t, `Ctrl+Alt+D -> type "{date} {time}"`)
	e.now = func() time.Time { return time.Date(2026, 10, 30, 9, 5, 0, 0, time.UTC) }
	w := &fakeWorld{down: map[uint16]bool{vkLCtrl: true, vkRAlt: true}}
	out := e.Key('D', true, w)
	if out.Type != "2026-10-30 09:05" {
		t.Fatalf("text %q", out.Type)
	}
	if !slices.Contains(out.Keys, Stroke{vkLCtrl, false}) || !slices.Contains(out.Keys, Stroke{vkRAlt, false}) {
		t.Fatalf("keys %+v", out.Keys)
	}
	if slices.Contains(out.Keys, Stroke{vkLCtrl, true}) {
		t.Fatalf("a modifier was pressed back and could stick: %+v", out.Keys)
	}
	if out = e.Key('D', true, w); out.Type != "" || !out.Swallow {
		t.Fatalf("a repeat typed again: %+v", out)
	}
}

func typeAll(e *Engine, w *fakeWorld, keys ...uint16) Effect {
	var out Effect
	for _, k := range keys {
		out = e.Key(k, true, w)
		e.Key(k, false, w)
	}
	return out
}

func TestAbbreviation(t *testing.T) {
	e := engine(t, ";mail => a@b.c")
	w := &fakeWorld{}
	out := typeAll(e, w, 'X', 0xba, 'M', 'A', 'I', 'L')
	if !out.Swallow || out.Type != "a@b.c" {
		t.Fatalf("%+v", out)
	}
	if n := len(out.Keys); n != 8 { // four keys reached the program, the last did not
		t.Fatalf("%d strokes", n)
	}
	if out = typeAll(e, w, 0xba, 'M', 0x25, 'A', 'I', 'L'); out.Swallow {
		t.Fatal("an arrow key did not end the typing")
	}
	if out = typeAll(e, w, 0xba, 'M', 'X', vkBackspace, 'A', 'I', 'L'); !out.Swallow {
		t.Fatal("Backspace did not take the wrong key back")
	}
	w.down = map[uint16]bool{vkLCtrl: true}
	if out = typeAll(e, w, 0xba, 'M', 'A', 'I', 'L'); out.Swallow {
		t.Fatal("Ctrl shortcuts spelled an abbreviation")
	}
}

func TestAbbreviationWithShift(t *testing.T) {
	e := engine(t, ":Hi => hello")
	w := &fakeWorld{down: map[uint16]bool{vkLShift: true}}
	typeAll(e, w, 0xba, 'H')
	w.down = nil
	if out := typeAll(e, w, 'I'); out.Type != "hello" {
		t.Fatalf("%+v", out)
	}
}

// fakeAdapter records what the service does with a system.
type fakeAdapter struct {
	mu      sync.Mutex
	handle  func(uint16, bool) bool
	starts  int
	stops   int
	sent    []Stroke
	typed   []string
	opened  []string
	failure error
}

func (f *fakeAdapter) Start(h func(uint16, bool) bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failure != nil {
		return f.failure
	}
	f.handle = h
	f.starts++
	return nil
}
func (f *fakeAdapter) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handle = nil
	f.stops++
}
func (f *fakeAdapter) Send(k []Stroke) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, k...)
	return nil
}
func (f *fakeAdapter) Type(s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.typed = append(f.typed, s)
	return nil
}
func (f *fakeAdapter) Open(t, a string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, t)
	return errors.New("no such program")
}
func (f *fakeAdapter) KeyDown(uint16) bool { return false }
func (f *fakeAdapter) CanRemap(vk uint16) bool {
	return vk != vkCapsLock || f.failure == nil
}

func TestServiceWatchesOnlyWhenTheUserSaidSo(t *testing.T) {
	f := &fakeAdapter{}
	s := NewService(f, func(error) {})
	defer s.Close()
	apply := func(rules ...string) {
		t.Helper()
		if err := s.Apply(&Config{Version: 1, Rules: rules}); err != nil {
			t.Fatal(err)
		}
	}
	apply("A -> B")
	s.Retry()
	if f.starts != 0 || s.Watching() {
		t.Fatal("a hook before the user turned the rules on")
	}
	apply("# only a comment")
	s.SetActive(true)
	if f.starts != 0 {
		t.Fatal("no rules, but a hook")
	}
	apply("A -> B")
	if f.starts != 1 {
		t.Fatalf("%d starts", f.starts)
	}
	apply("A -> C")
	if f.starts != 1 || f.stops != 0 {
		t.Fatalf("changing rules restarted the hook: %d %d", f.starts, f.stops)
	}
	s.SetActive(false)
	if f.stops != 1 || s.Watching() {
		t.Fatalf("%d stops", f.stops)
	}
	apply("A -> D")
	if f.starts != 1 {
		t.Fatal("new rules turned the hook back on")
	}
}

func TestServiceReleasesKeysWhenRulesChange(t *testing.T) {
	f := &fakeAdapter{}
	s := NewService(f, func(error) {})
	defer s.Close()
	s.SetActive(true)
	s.Apply(&Config{Version: 1, Rules: []string{"CapsLock -> Ctrl"}})
	f.handle(vkCapsLock, true)
	s.Apply(&Config{Version: 1, Rules: []string{"A -> B"}})
	if !slices.Equal(f.sent, []Stroke{{vkCtrl, true}, {vkCtrl, false}}) {
		t.Fatalf("%+v", f.sent)
	}
	s.Apply(&Config{Version: 1, Rules: []string{"CapsLock -> Ctrl"}})
	f.handle(vkCapsLock, true)
	f.sent = nil
	s.SetActive(false)
	if !slices.Equal(f.sent, []Stroke{{vkCtrl, false}}) {
		t.Fatalf("turning off left a key down: %+v", f.sent)
	}
}

func TestServiceReportsActionsOffTheHook(t *testing.T) {
	f := &fakeAdapter{}
	failed := make(chan error, 1)
	s := NewService(f, func(err error) { failed <- err })
	defer s.Close()
	s.SetActive(true)
	s.Apply(&Config{Version: 1, Rules: []string{`F1 -> open "nothing"`, `F2 -> type "hi"`}})
	if !f.handle(0x70, true) {
		t.Fatal("not swallowed")
	}
	select {
	case err := <-failed:
		if !strings.Contains(err.Error(), "no such program") {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the failure was not reported")
	}
	f.handle(0x71, true)
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(time.Millisecond) {
		f.mu.Lock()
		n := len(f.typed)
		f.mu.Unlock()
		if n == 1 {
			return
		}
	}
	t.Fatal("the text was not typed")
}

func TestServiceSaysWhenTheSystemRefuses(t *testing.T) {
	f := &fakeAdapter{failure: ErrPermission}
	s := NewService(f, func(error) {})
	defer s.Close()
	s.SetActive(true)
	err := s.Apply(&Config{Version: 1, Rules: []string{"A -> B"}})
	if err != nil || !errors.Is(s.Err(), ErrPermission) || s.Watching() {
		t.Fatalf("%v, %v", err, s.Err())
	}
	f.failure = nil
	s.Retry()
	if s.Err() != nil || !s.Watching() {
		t.Fatalf("retry: %v", s.Err())
	}
	f.failure = ErrUnsupported
	err = s.Apply(&Config{Version: 1, Rules: []string{"", "CapsLock -> Esc"}})
	var re *RuleError
	if !errors.As(err, &re) || re.Problem != Unavailable || re.Line != 2 {
		t.Fatalf("%v", err)
	}
	if s.Watching() {
		t.Fatal("a mistake left the old rules running")
	}
}

func TestConfig(t *testing.T) {
	c, err := ParseConfig(DefaultJSON)
	if err != nil || len(c.Rules) == 0 {
		t.Fatalf("%+v, %v", c, err)
	}
	back, err := ParseConfig([]byte(c.JSON()))
	if err != nil || !slices.Equal(back.Rules, c.Rules) {
		t.Fatalf("%+v, %v", back, err)
	}
	if !strings.Contains(c.JSON(), "->") {
		t.Fatal("arrows were escaped")
	}
	if _, err := ParseConfig([]byte(`{"version":1,"rules":["nonsense"]}`)); err != nil {
		t.Fatalf("a rule with a mistake must still be kept: %v", err)
	}
	if _, err := ParseConfig([]byte(`{"version":2}`)); err == nil {
		t.Fatal("a later version passed")
	}
	if _, err := ParseConfig([]byte(`{"version":1,"enabled":true}`)); err == nil {
		t.Fatal("a file turned the rules on") // the switch is the user's, and not in the file
	}
}

func TestMacCodesCoverTheKeysAndReadBack(t *testing.T) {
	seen := map[uint16]uint16{}
	for vk, code := range macKeyCodes {
		if vk == vkShift || vk == vkAlt || vk == vkCtrl {
			continue
		}
		if other, ok := seen[code]; ok {
			t.Errorf("code %d is both %s and %s", code, KeyName(vk), KeyName(other))
		}
		seen[code] = vk
		if macVirtualKeys[code] != vk {
			t.Errorf("%s does not read back", KeyName(vk))
		}
	}
	for c := uint16('A'); c <= 'Z'; c++ {
		if _, ok := macKeyCodes[c]; !ok {
			t.Errorf("no code for %c", c)
		}
	}
	for _, vk := range []uint16{vkShift, vkAlt, vkCtrl} {
		if _, ok := macVirtualKeys[macKeyCodes[vk]]; !ok {
			t.Errorf("plain %s has no code", KeyName(vk))
		}
	}
}
