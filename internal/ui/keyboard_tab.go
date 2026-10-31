package ui

import (
	"errors"
	"fmt"
	"image/color"
	"os"
	"runtime"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/keyboard"
)

// The configurations behind the keyboard tool, for the MCP tools and for the tab that edits them.
const (
	keyboardRules = "keyboard_rules"
	keyboardRemap = "keyboard_remap"
)

// The system's keyboard; tests put fakes in their place, so that none ever hooks the real one or writes its registry.
var (
	newKeyboardAdapter = keyboard.NewAdapter
	newSystemMap       = keyboard.NewSystemMap
)

// The panels of the keyboard tool.
const (
	keyboardRulesPanel = iota
	keyboardRemapPanel
)

// remapNames are what the second panel is called: after the system's own way of remapping keys.
var remapNames = msgs("Registry remaps", "hidutil remaps", "System remaps")

// keyboardPane is the text of one panel: what is in the editor and what is in effect.
type keyboardPane struct {
	text   string
	saved  string // the saved lines, to tell whether there are unsaved changes
	opened bool
	view   codeView
	box    string // the id of its editor
}

// keyboardState is the keyboard rules and system remaps, and the tab that edits them.
type keyboardState struct {
	service *keyboard.Service
	config  *keyboard.Config
	problem error // why the saved rules do not work; they are kept, for the user to correct

	system       keyboard.SystemMap
	remap        *keyboard.Config
	remapped     []keyboard.Mapping // the saved remaps, read
	remapProblem error
	current      []keyboard.Mapping // the table in Windows, as last read
	readErr      error
	imported     bool // the editor shows remaps found in Windows
	writing      bool

	panel  int
	rules  keyboardPane
	remaps keyboardPane
	asking bool // the explanation before turning the rules on is showing
}

type keyboardExample struct {
	name string // message
	rule string
}

// exampleNames mark the example buttons' names for translation.
var exampleNames = msgs("Change a key", "Press a combination", "Open a program", "Open a website", "Type text", "Abbreviation",
	"Make Caps Lock a Ctrl", "Turn a key off")

// keyboardExamples are the buttons above the rules: each adds a rule for the user to change.
var keyboardExamples = []keyboardExample{
	{exampleNames[0], "CapsLock -> Esc"},
	{exampleNames[1], "F1 -> Ctrl+Z"},
	{exampleNames[2], exampleProgram()},
	{exampleNames[3], `Ctrl+Alt+G -> open "https://github.com"`},
	{exampleNames[4], `Ctrl+Alt+D -> type "{date}"`},
	{exampleNames[5], ";mail => me@example.com"},
}

// remapExamples are the same for the system remaps.
var remapExamples = []keyboardExample{
	{exampleNames[0], "CapsLock -> Esc"},
	{exampleNames[6], "CapsLock -> Left-Ctrl"},
	{exampleNames[7], "ScrollLock -> None"},
}

func exampleProgram() string {
	if runtime.GOOS == "darwin" {
		return `Ctrl+Alt+T -> open "/System/Applications/Utilities/Terminal.app"`
	}
	return `Ctrl+Alt+T -> open "wt.exe"`
}

const icKeyboard = ""

// openKeyboardTab shows the keyboard tool's tab, of which there is one at most.
func (a *App) openKeyboardTab() {
	for i, d := range a.docs {
		if d.keyboardTab {
			a.switchTab(i)
			return
		}
	}
	if a.occupied() {
		a.newTab()
	}
	a.keyboardTab = true
	a.requestFrame()
}

// drawKeyboardTab is the tool's tab: its page in a column down the middle.
func (a *App) drawKeyboardTab(r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colPanel)
	w := min(r.W-u.px(48), u.px(1000))
	a.keyboardPage(rectAt(r.X+(r.W-w)/2, r.Y+u.px(24), w, r.H-u.px(48)))
}

// initKeyboard prepares the rules, which stay off until the user's choice is applied by startKeyboard.
func (a *App) initKeyboard() {
	k := &a.keys
	k.service = keyboard.NewService(newKeyboardAdapter(), func(err error) {
		a.post(func() { a.toast(a.u.tf("A keyboard rule failed: %s", err.Error()), true) })
	})
	k.config, _ = keyboard.ParseConfig(keyboard.DefaultJSON)
	k.remap, _ = keyboard.ParseConfig(keyboard.DefaultRemapJSON)
	k.system = newSystemMap()
	complete := func(remap bool) func(string) (string, []string) {
		return func(before string) (string, []string) { return keyboard.Complete(before, remap) }
	}
	k.rules.view.complete, k.remaps.view.complete = complete(false), complete(true)
}

// startKeyboard turns the rules on at start if the user has chosen that, and says so when the saved ones cannot run.
func (a *App) startKeyboard() {
	if !a.settings.KeyboardRules {
		return
	}
	a.keys.service.SetActive(true)
	if err := a.keys.problem; err != nil {
		a.toastActions(a.describeRule(err), true, []toastAction{{a.u.t("Settings"), a.openKeyboardTab}})
	}
}

// applyKeyboard puts a configuration in effect. Rules with a mistake are still a saved file, so this is no failure:
// they are kept and none of them work until it is corrected.
func (a *App) applyKeyboard(data []byte) error {
	c, err := keyboard.ParseConfig(data)
	if err != nil {
		return err
	}
	a.keys.config = c
	a.keys.problem = a.keys.service.Apply(c)
	return nil
}

// applyRemap keeps the lines of the system remaps; they reach the system only when the user writes them.
func (a *App) applyRemap(data []byte) error {
	c, err := keyboard.ParseConfig(data)
	if err != nil {
		return err
	}
	a.keys.remap = c
	a.keys.remapped, a.keys.remapProblem = keyboard.ParseRemap(c.Rules)
	return nil
}

// setKeyboardActive is the user's choice, the only thing that turns the rules on.
func (a *App) setKeyboardActive(on bool) {
	a.settings.KeyboardRules = on
	a.saveSettings()
	a.keys.asking = false
	a.keys.service.SetActive(on)
}

func linesText(c *keyboard.Config) string { return strings.Join(c.Rules, "\n") }

// keyboardPaneNow keeps a panel's text in step with its configuration: lines changed over MCP show at once, unless the user
// is in the middle of changes of their own.
func (a *App) keyboardPaneNow(p *keyboardPane, c *keyboard.Config) {
	now := linesText(c)
	switch {
	case !p.opened:
		p.text, p.saved, p.opened = now, now, true
	case now != p.saved:
		if p.text == p.saved {
			p.text = now
			p.view.reset()
			a.u.focus = ""
		}
		p.saved = now
	}
}

// saveKeyboardPane checks and keeps a panel's text. A mistake is reported by the panel and the text is kept.
func (a *App) saveKeyboardPane(p *keyboardPane, id string, c func() *keyboard.Config) bool {
	u := &a.u
	lines := strings.Split(strings.ReplaceAll(strings.TrimRight(p.text, " \t\r\n"), "\r", ""), "\n")
	config := keyboard.Config{Version: 1, Rules: lines}
	kind, err := findConfig(id)
	if err == nil {
		err = a.saveConfig(kind, config.JSON())
	}
	if err != nil {
		a.toast(u.tf("%s could not be saved: %v", u.t(kind.title), err), true)
		return false
	}
	p.text, p.saved = linesText(c()), linesText(c())
	return true
}

// saveKeyboardRules keeps the rules, which change at once.
func (a *App) saveKeyboardRules() {
	k := &a.keys
	if !a.saveKeyboardPane(&k.rules, keyboardRules, func() *keyboard.Config { return k.config }) {
		return
	}
	if k.problem != nil {
		a.toast(a.describeRule(k.problem), true)
		return
	}
	a.toast(a.u.tf("%s saved", a.u.t("Keyboard rules")), false)
}

// saveKeyboardRemaps keeps the system remaps' lines.
func (a *App) saveKeyboardRemaps() {
	k := &a.keys
	if !a.saveKeyboardPane(&k.remaps, keyboardRemap, func() *keyboard.Config { return k.remap }) {
		return
	}
	k.imported = false
	if k.remapProblem != nil {
		a.toast(a.describeRemap(k.remapProblem), true)
		return
	}
	a.toast(a.u.tf("%s saved", a.remapName()), false)
}

// readSystemRemaps looks up the table in Windows, and shows remaps made elsewhere, as with SharpKeys, when nothing
// has been saved here.
func (a *App) readSystemRemaps() {
	k := &a.keys
	k.current, k.readErr = k.system.Read()
	kind, err := findConfig(keyboardRemap)
	if err != nil || k.readErr != nil || len(k.current) == 0 {
		return
	}
	if _, err := os.Stat(a.configPath(kind)); os.IsNotExist(err) {
		k.remaps.text = strings.Join(keyboard.FormatRemap(k.current), "\n")
		k.imported = true
	}
}

// remapWritten reports whether the saved remaps are what Windows has.
func (a *App) remapWritten() bool {
	return a.keys.remapProblem == nil && a.keys.readErr == nil && keyboard.SameRemap(a.keys.current, a.keys.remapped)
}

// writeRemaps saves the lines and asks the system to take them; it waits for the user away from the interface.
func (a *App) writeRemaps() {
	k := &a.keys
	if k.remaps.text != k.remaps.saved || k.imported {
		a.saveKeyboardRemaps()
	}
	if k.remapProblem != nil || k.writing {
		return
	}
	k.writing = true
	table, system := k.remapped, k.system
	go func() {
		err := system.Write(table)
		current, readErr := system.Read()
		a.post(func() {
			k.writing, k.current, k.readErr = false, current, readErr
			if err != nil {
				a.toast(a.u.tf("The remaps could not be applied: %s", a.describeWrite(err)), true)
				return
			}
			if system.Kind() == keyboard.HIDMap {
				a.toast(a.u.t("Applied. The keys have changed, and stay changed after a restart."), false)
				return
			}
			a.toast(a.u.t("Written to the registry. Restart the computer for them to take effect."), false)
		})
	}()
}

func (a *App) describeWrite(err error) string {
	switch {
	case errors.Is(err, keyboard.ErrCancelled):
		return a.u.t("the approval was turned down")
	case errors.Is(err, keyboard.ErrNotWritten):
		return a.u.t("Windows did not take them; check that your account may approve this")
	}
	return err.Error()
}

// addExample puts a rule at the caret of a panel's text, or at the end if the caret has not been there, and leaves
// the caret after it for the rule to be changed.
func (a *App) addExample(p *keyboardPane, rule string) {
	p.view.remember(&editor{text: []rune(p.text)}, false)
	caret := len([]rune(p.text))
	if p.view.placed {
		caret = p.view.at
	}
	p.text, caret = insertLine(p.text, caret, rule)
	p.view.at, p.view.placed = caret, true
	if p.box != "" {
		a.u.beginEdit(p.box, p.text, true, caret, caret)
	}
}

// insertLine puts line on the blank line at caret, or under the line there when it has words, and returns the new
// text and the caret at the end of line.
func insertLine(text string, caret int, line string) (string, int) {
	rs := []rune(text)
	caret = min(caret, len(rs))
	start, end := caret, caret
	for start > 0 && rs[start-1] != '\n' {
		start--
	}
	for end < len(rs) && rs[end] != '\n' {
		end++
	}
	before, after := string(rs[:start]), string(rs[end:])
	length := len([]rune(line))
	if strings.TrimSpace(string(rs[start:end])) == "" {
		return before + line + after, start + length
	}
	return string(rs[:end]) + "\n" + line + after, end + 1 + length
}

// ruleWhy says what is wrong with a line.
func (a *App) ruleWhy(re *keyboard.RuleError) string {
	u := &a.u
	switch re.Problem {
	case keyboard.BadKey:
		return u.tf("“%s” is not a key or combination", re.Text)
	case keyboard.BadAbbr:
		return u.tf("“%s” cannot be an abbreviation: use at least two characters a keyboard types", re.Text)
	case keyboard.BadText:
		return u.t("the quoted text is not closed")
	case keyboard.EmptyAction:
		return u.t("nothing follows the arrow")
	case keyboard.DoubleTrigger:
		return u.tf("%s is already used on line %d", re.Text, re.Other)
	case keyboard.Unavailable:
		return u.tf("%s cannot be remapped on this system", re.Text)
	case keyboard.BadRemap:
		return u.t("write a key, -> and the key it becomes, or None")
	case keyboard.NotOneKey:
		return u.tf("“%s” is not a single key the system can remap", re.Text)
	}
	return u.t("write a key, -> and what it does, or an abbreviation, => and its text")
}

// describeRule says what is wrong with the rules.
func (a *App) describeRule(err error) string {
	var re *keyboard.RuleError
	if !errors.As(err, &re) {
		return a.u.tf("The keyboard rules could not start: %s", a.describeKeyboard(err))
	}
	return a.u.tf("Saved, but no rule works until line %d is corrected: %s", re.Line, a.ruleWhy(re))
}

func (a *App) describeRemap(err error) string {
	var re *keyboard.RuleError
	if !errors.As(err, &re) {
		return err.Error()
	}
	return a.u.tf("Saved, but it cannot be written until line %d is corrected: %s", re.Line, a.ruleWhy(re))
}

func (a *App) describeKeyboard(err error) string {
	switch {
	case errors.Is(err, keyboard.ErrPermission):
		return a.u.t("the system has not allowed it; allow Btelo Forge in System Settings → Privacy & Security → Accessibility, then try again")
	case errors.Is(err, keyboard.ErrUnsupported):
		return a.u.t("this system is not supported")
	}
	return err.Error()
}

// keyboardPage is the keyboard tool: rules that work while the app runs, and system remaps that need nothing running,
// each edited as text on a panel of its own.
func (a *App) keyboardPage(r gfx.Rect) {
	u := &a.u
	k := &a.keys
	if k.asking {
		a.keyboardConsent(r)
		return
	}
	names := []string{u.t("Global live remaps"), a.remapName()}
	if pick := u.chips("keyboard/rules/panel", rectAt(r.X, r.Y, min(r.W, u.px(340)), u.px(30)), names, k.panel, true); pick != k.panel {
		k.panel = pick
		u.focus = ""
	}
	r = rectAt(r.X, r.Y+u.px(42), r.W, r.H-u.px(42))
	if k.panel == keyboardRemapPanel {
		a.settingsRemaps(r)
		return
	}
	a.settingsRules(r)
}

// remapName is the second panel's name on this system.
func (a *App) remapName() string {
	switch a.keys.system.Kind() {
	case keyboard.RegistryMap:
		return a.u.t(remapNames[0])
	case keyboard.HIDMap:
		return a.u.t(remapNames[1])
	}
	return a.u.t(remapNames[2])
}

// keyboardHead draws the status line with a button at its right, and returns the y below it and whether the button
// was pressed.
func (a *App) keyboardHead(r gfx.Rect, status string, col color.RGBA, id, button string, kind buttonKind, enabled bool) (float32, bool) {
	u := &a.u
	width := u.buttonWidth("", button)
	f := u.face(12.5)
	lines := f.Wrap(status, r.W-width-u.px(12))
	for i, l := range lines {
		u.c.Text(f, r.X, r.Y+float32(i)*f.Height()*1.3, l, col)
	}
	pressed := u.button(id, rectAt(r.Right()-width, r.Y, width, u.px(32)), "", button, kind, enabled)
	return r.Y + max(u.px(32), float32(len(lines))*f.Height()*1.3) + u.px(12), pressed
}

// keyboardEditor draws what both panels share: the example buttons, the editor and the row below it. It returns
// whether saving was asked for.
func (a *App) keyboardEditor(id string, r gfx.Rect, y float32, p *keyboardPane, examples []keyboardExample, problem error, help string, describe func(error) string) (save bool) {
	u := &a.u
	x := r.X
	for i, e := range examples {
		name := u.t(e.name)
		w := u.buttonWidth(icAdd, name)
		if x > r.X && x+w > r.Right() {
			x, y = r.X, y+u.px(36)
		}
		if u.button(fmt.Sprintf("%s/example/%d", id, i), rectAt(x, y, w, u.px(30)), icAdd, name, plain, true) {
			a.addExample(p, e.rule)
		}
		x += w + u.px(6)
	}
	y += u.px(40)
	footer := u.px(84)
	for i := 0; i < len(u.keys); i++ {
		if key := u.keys[i]; key.Ctrl && key.Code == 'S' {
			save = true
			u.keys = append(u.keys[:i], u.keys[i+1:]...)
			i--
		}
	}
	p.box = id + "/code"
	errLine := 0
	var re *keyboard.RuleError
	dirty := p.text != p.saved
	if errors.As(problem, &re) && !dirty {
		errLine = re.Line
	}
	if u.codeBox(id+"/code", rectAt(r.X, y, r.W, r.Bottom()-y-footer), &p.text, &p.view, errLine) {
		dirty = p.text != p.saved
	}
	y = r.Bottom() - footer + u.px(10)
	msg, col := help, colFaint
	switch {
	case dirty:
		msg, col = u.t("Unsaved changes"), colWarning
	case problem != nil:
		msg, col = describe(problem), colDangerText
	}
	f := u.face(11.5)
	lines := f.Wrap(msg, r.W)
	for i, l := range lines[:min(len(lines), 2)] {
		u.c.Text(f, r.X, y+float32(i)*f.Height()*1.2, l, col)
	}
	y += u.px(36)
	save = u.button(id+"/save", rectAt(r.Right()-u.px(160), y, u.px(160), u.px(34)), icCheck, u.t("Save (Ctrl+S)"), primary, dirty) || save && dirty
	if u.button(id+"/discard", rectAt(r.Right()-u.px(296), y, u.px(128), u.px(34)), "", u.t("Discard changes"), plain, dirty) {
		p.text = p.saved
		p.view.reset()
		u.focus = ""
	}
	if u.button(id+"/guide", rectAt(r.X, y, u.px(120), u.px(34)), icCopy, u.t("Copy guide"), ghost, true) {
		guide := keyboard.FormatGuide
		if p == &a.keys.remaps {
			guide = keyboard.RemapGuide
		}
		a.copyText(guide)
	}
	return save
}

// settingsRules is the panel of the rules that work while the app runs.
func (a *App) settingsRules(r gfx.Rect) {
	u := &a.u
	k := &a.keys
	a.keyboardPaneNow(&k.rules, k.config)
	on := a.settings.KeyboardRules
	status, col := a.keyboardStatus()
	button, kind := u.t("Turn on…"), primary
	if on {
		button, kind = u.t("Turn off"), plain
	}
	y, pressed := a.keyboardHead(r, status, col, "keyboard/rules/switch", button, kind, true)
	if pressed {
		if on {
			a.setKeyboardActive(false)
		} else {
			k.asking = true
		}
	}
	if on && k.problem == nil && k.service.Err() != nil {
		retry := u.t("Try again")
		w := u.buttonWidth("", retry)
		if u.button("keyboard/rules/retry", rectAt(r.Right()-u.buttonWidth("", button)-w-u.px(8), r.Y, w, u.px(32)), "", retry, ghost, true) {
			k.service.Retry()
		}
	}
	help := u.t("Saving checks the rules and applies them at once. An AI connected over MCP can change them too (get_config, set_config).")
	if a.keyboardEditor("keyboard/rules", r, y, &k.rules, keyboardExamples, k.problem, help, a.describeRule) {
		a.saveKeyboardRules()
	}
}

// settingsRemaps is the panel of the remaps kept by the system.
func (a *App) settingsRemaps(r gfx.Rect) {
	u := &a.u
	k := &a.keys
	kind := k.system.Kind()
	if kind == keyboard.NoMap {
		a.paragraph(r.X, r.Y, r.W, u.t("This system has no lasting way to remap keys that this app can use."), 12.5, colDim)
		return
	}
	first := !k.remaps.opened
	a.keyboardPaneNow(&k.remaps, k.remap)
	if first {
		a.readSystemRemaps()
	}
	status, col := u.t("These lines are not in the registry yet. Writing them asks for an administrator's approval and changes the keys for all users after you restart the computer, as with SharpKeys. Nothing runs and no antivirus warning comes of it; write an empty list to undo."), colDim
	button := u.t("Write to the registry…")
	written := u.t("These remaps are written. They take effect after you restart the computer, for all its users.")
	if kind == keyboard.HIDMap {
		status = u.t("These lines are not applied yet. Applying them changes the keys at once for this user, and they stay changed after a restart. Nothing of this app runs and no warning comes of it; apply an empty list to undo.")
		button = u.t("Apply")
		written = u.t("These remaps are applied, now and after each restart.")
	}
	switch {
	case k.readErr != nil:
		status, col = u.tf("The remaps could not be read: %s", k.readErr.Error()), colDangerText
	case k.imported:
		status = u.t("Remaps not made here are already in the system; they are shown below. Save to keep them here.")
	case k.remaps.text == k.remaps.saved && a.remapWritten() && len(k.remapped) > 0:
		status, col = written, colSuccess
	}
	if k.writing {
		button = u.t("Writing…")
	}
	dirty := k.remaps.text != k.remaps.saved
	enabled := !k.writing && k.readErr == nil && (dirty || k.imported || k.remapProblem == nil && !a.remapWritten())
	y, pressed := a.keyboardHead(r, status, col, "keyboard/rules/write", button, primary, enabled)
	if pressed {
		a.writeRemaps()
	}
	help := u.t("Saving checks the lines and keeps them; the button at the top applies them. An AI connected over MCP can change the lines too, but only you can apply them.")
	if a.keyboardEditor("keyboard/remap", r, y, &k.remaps, remapExamples, k.remapProblem, help, a.describeRemap) {
		a.saveKeyboardRemaps()
	}
}

// keyboardStatus says whether the rules are working.
func (a *App) keyboardStatus() (string, color.RGBA) {
	u := &a.u
	k := &a.keys
	switch {
	case !a.settings.KeyboardRules:
		return u.t("Off. Nothing watches the keyboard, and these rules wait until you turn them on."), colDim
	case k.problem != nil:
		return u.t("On, but the rules have a mistake, so none of them work."), colDangerText
	case k.service.Err() != nil:
		return u.tf("The keyboard rules could not start: %s", a.describeKeyboard(k.service.Err())), colDangerText
	case !k.service.Watching():
		return u.t("On. Add a rule below; the keyboard is watched once there is one."), colDim
	}
	return u.t("On. Rules work in every program while this app runs."), colSuccess
}

// keyboardConsent explains what turning the rules on means, before it happens.
func (a *App) keyboardConsent(r gfx.Rect) {
	u := &a.u
	y := r.Y
	u.heading(rectAt(r.X, y, r.W, u.px(22)), u.t("Before you turn on keyboard rules"))
	y += u.px(32)
	for _, point := range keyboardConsentPoints {
		y = a.paragraph(r.X, y, r.W, u.t(point), 13, colText) + u.px(10)
	}
	y += u.px(10)
	turnOn := u.t("I understand, turn it on")
	w := u.buttonWidth("", turnOn)
	if u.button("keyboard/rules/consent", rectAt(r.X, y, w, u.px(34)), "", turnOn, primary, true) {
		a.setKeyboardActive(true)
	}
	if u.button("keyboard/rules/cancel", rectAt(r.X+w+u.px(8), y, u.px(90), u.px(34)), "", u.t("Cancel"), plain, true) {
		a.keys.asking = false
	}
}

// keyboardConsentPoints are what the user agrees to.
var keyboardConsentPoints = msgs(
	"To work in every program, this app has to see every key you press, in any program, while it runs. It uses a system feature called a low-level keyboard hook.",
	"Keys are only compared with your rules, on this computer. They are not recorded, kept or sent anywhere.",
	"The app also presses keys, types text and opens what your rules name, as if you had done it.",
	"Antivirus software may warn about this or block it, because keyloggers use the same feature. If it does, allow Btelo Forge or turn this off.",
	"On macOS the system asks you to allow Btelo Forge under Accessibility. Programs run as administrator on Windows do not take these rules unless this app is run as administrator too.",
	"Only you can turn this on: an AI connected over MCP can change the rules, but not this switch. Turn it off here at any time and the keyboard is left alone at once.",
)
