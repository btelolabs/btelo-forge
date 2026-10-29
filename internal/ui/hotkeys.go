package ui

import (
	"fmt"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// Hotkey is a key combination that works system-wide; Key is a Windows virtual-key code, 0 for none.
type Hotkey struct {
	Ctrl  bool   `json:"ctrl,omitempty"`
	Alt   bool   `json:"alt,omitempty"`
	Shift bool   `json:"shift,omitempty"`
	Key   uint16 `json:"key"`
}

// The actions that have a hotkey, in the order Settings shows them and the window registers them.
const (
	hotkeyWindow         = iota // hide the window, or bring it back and in front
	hotkeyDraw                  // draw on the screen, or give the mouse back while keeping the drawing
	hotkeyScreenshot            // take a screenshot onto the clipboard
	hotkeyScreenshotFile        // take a screenshot into the pictures folder
	hotkeyScreenshotPath        // take a screenshot into the pictures folder and copy its path
	hotkeyRecord                // start or stop recording
	hotkeyPause
	hotkeyCount
)

var hotkeyNames = msgs("Show or hide this window", "Draw on the screen", "Take a screenshot onto the clipboard",
	"Take a screenshot into the pictures folder", "Take a screenshot and copy its path", "Start or stop recording", "Pause or resume recording")

// hotkeySettings are the hotkeys the user chose, kept between runs. The clipboard screenshot keeps the name the one
// screenshot hotkey had, so the combination chosen for it carries over.
type hotkeySettings struct {
	Window         Hotkey `json:"window"`
	Draw           Hotkey `json:"draw"`
	Screenshot     Hotkey `json:"screenshot"`
	ScreenshotFile Hotkey `json:"screenshotFile"`
	ScreenshotPath Hotkey `json:"screenshotPath"`
	Record         Hotkey `json:"record"`
	Pause          Hotkey `json:"pause"`
}

// defaultHotkeys number the actions in the order Settings shows them, Ctrl+Shift with the digit, avoiding the
// combinations other programs often take, such as Ctrl+Alt+R and NVIDIA's Alt+F9. The screenshots saved as files
// and pausing have none: most people want one kind of screenshot at hand, and pausing has its button on the
// recording toolbar.
func defaultHotkeys() hotkeySettings {
	digit := func(d uint16) Hotkey { return Hotkey{Ctrl: true, Shift: true, Key: '0' + d} }
	return hotkeySettings{Window: digit(1), Draw: digit(2), Screenshot: digit(3), Record: digit(4)}
}

func (h *hotkeySettings) list() [hotkeyCount]*Hotkey {
	return [hotkeyCount]*Hotkey{&h.Window, &h.Draw, &h.Screenshot, &h.ScreenshotFile, &h.ScreenshotPath, &h.Record, &h.Pause}
}

// hotkeyState is what registering the hotkeys found, and the one being chosen in Settings.
type hotkeyState struct {
	errs      [hotkeyCount]error // another program has taken it
	capturing int                // the action waiting for a key press in Settings, or -1
	refused   string             // why the last press while choosing was not taken
	warned    bool               // the user has been told about taken hotkeys this run
}

// keyNames name the keys a hotkey may use besides letters, digits and F1 to F24.
var keyNames = map[uint16]string{
	0x08: "Backspace", 0x09: "Tab", 0x0d: "Enter", 0x13: "Pause", 0x1b: "Esc", 0x20: "Space", 0x21: "PageUp", 0x22: "PageDown",
	0x23: "End", 0x24: "Home", 0x25: "Left", 0x26: "Up", 0x27: "Right", 0x28: "Down", 0x2d: "Insert", 0x2e: "Delete",
	0x6a: "Num*", 0x6b: "Num+", 0x6d: "Num-", 0x6e: "Num.", 0x6f: "Num/", 0xba: ";", 0xbb: "=", 0xbc: ",", 0xbd: "-",
	0xbe: ".", 0xbf: "/", 0xc0: "`", 0xdb: "[", 0xdc: `\`, 0xdd: "]", 0xde: "'",
}

func keyName(vk uint16) string {
	switch {
	case vk >= 'A' && vk <= 'Z', vk >= '0' && vk <= '9':
		return string(rune(vk))
	case vk >= 0x70 && vk <= 0x87:
		return fmt.Sprintf("F%d", vk-0x6f)
	case vk >= 0x60 && vk <= 0x69:
		return fmt.Sprintf("Num%d", vk-0x60)
	}
	if name, ok := keyNames[vk]; ok {
		return name
	}
	return fmt.Sprintf("0x%02X", vk)
}

// String is the combination as people write it, such as Ctrl+Shift+F9, or "" for none.
func (h Hotkey) String() string {
	if h.Key == 0 {
		return ""
	}
	var parts []string
	for _, m := range []struct {
		on   bool
		name string
	}{{h.Ctrl, "Ctrl"}, {h.Alt, "Alt"}, {h.Shift, "Shift"}} {
		if m.on {
			parts = append(parts, m.name)
		}
	}
	return strings.Join(append(parts, keyName(h.Key)), "+")
}

// usable reports whether a combination may be taken system-wide: with Ctrl or Alt, or a function key, so that it
// never swallows plain typing in other programs.
func (h Hotkey) usable() bool {
	return h.Key != 0 && (h.Ctrl || h.Alt || h.Key >= 0x70 && h.Key <= 0x87)
}

// applyHotkeys registers the chosen hotkeys with the window, or none while one is being chosen (so pressing a
// registered one reaches Settings as a key press), and tells the user once about any another program has taken.
func (a *App) applyHotkeys() {
	host := a.recorder()
	if host == nil {
		return
	}
	var keys []Hotkey
	if a.hotkeys.capturing < 0 {
		for _, k := range a.settings.Hotkeys.list() {
			keys = append(keys, *k)
		}
	}
	errs := host.SetHotkeys(keys)
	a.hotkeys.errs = [hotkeyCount]error{}
	var taken []string
	for i, err := range errs {
		if i < hotkeyCount && err != nil {
			a.hotkeys.errs[i] = err
			taken = append(taken, keys[i].String())
		}
	}
	if len(taken) > 0 && !a.hotkeys.warned {
		a.hotkeys.warned = true
		a.toastActions(a.u.tf("Another program has taken %s; choose other hotkeys in Settings", strings.Join(taken, ", ")), true,
			[]toastAction{{a.u.t("Settings"), func() { a.settingsOpen, a.settingsPage, a.keysDomain = true, settingsHotkeysPage, domainRecording }}})
	}
}

// hotkeyLabel is an action's hotkey for hints, or "" when it has none that works.
func (a *App) hotkeyLabel(action int) string {
	if a.hotkeys.errs[action] != nil {
		return ""
	}
	return a.settings.Hotkeys.list()[action].String()
}

// handleHotkeys acts on hotkey presses; they arrive while another program has the focus or the window is minimized.
func (a *App) handleHotkeys(pressed []int) {
	r := &a.rec
	for _, action := range pressed {
		switch action {
		case hotkeyRecord:
			switch {
			case r.rec != nil:
				a.stopRecording(nil)
			case !a.recording():
				a.startRecording(func(_ string, err error) {
					if err != nil {
						a.toast(a.u.tf("Recording could not start: %s", a.describe(err)), true)
					}
				})
			}
		case hotkeyPause:
			if r.rec != nil && !r.stopping && !r.busy {
				a.pauseRecording(!r.rec.Status().Paused, nil)
			}
		case hotkeyScreenshot, hotkeyScreenshotFile, hotkeyScreenshotPath:
			if a.over.pick == nil {
				a.screenshotRegion(shotTargets[action-hotkeyScreenshot])
			}
		case hotkeyDraw:
			a.toggleScreenDraw()
		case hotkeyWindow:
			a.toggleWindow()
		}
	}
}

// toggleWindow hides the window when it is in front, and otherwise brings it back and in front: a window behind
// another program's comes forward rather than vanishing, as people expect of a key that shows an app.
func (a *App) toggleWindow() {
	host := a.recorder()
	if host == nil {
		return
	}
	if host.Shown() {
		host.Hide()
	} else {
		host.Restore()
	}
}

// settingsHotkeys is the Settings page where the keys are chosen by pressing them, for each kind of work on its
// own: the general shortcuts, video editing, drawing, and the recording hotkeys that work system-wide.
func (a *App) settingsHotkeys(r gfx.Rect) {
	u := &a.u
	domain := a.keyDomainTabs(rectAt(r.X, r.Y, r.W, u.px(34)))
	r = rectAt(r.X, r.Y+u.px(46), r.W, r.H-u.px(46))
	if domain != domainRecording {
		y := a.paragraph(r.X, r.Y, r.W, a.domainHelp(domain), 12.5, colDim) + u.px(10)
		a.settingsKeys(domain, rectAt(r.X, y, r.W, r.Bottom()-y))
		return
	}
	a.settingsRecordingHotkeys(r)
}

// domainHelp explains an in-window domain's keys at the top of its page.
func (a *App) domainHelp(domain int) string {
	u := &a.u
	switch domain {
	case domainVideo:
		return u.t("These keys work while a video is being edited. Click a command's keys, then press the new combination.")
	case domainDrawing:
		return u.t("These keys work on a canvas, as Photoshop's and Affinity Designer's do. Click a command's keys, then press the new combination.")
	case domainFiles:
		return u.t("These keys work in the resource manager, as gdu's and vim's do, the same whether a list shows rows or pictures: on the item under the pointer or the cursor, or on the marked items. Click a command's keys, then press the new combination.")
	}
	return u.t("These keys work in every tab. Click a command's keys, then press the new combination.")
}

// settingsRecordingHotkeys chooses the hotkeys that work system-wide: recording's, and showing the window.
func (a *App) settingsRecordingHotkeys(r gfx.Rect) {
	u := &a.u
	h := &a.hotkeys
	y := a.paragraph(r.X, r.Y, r.W, u.t("These keys work everywhere, also while another program is in front or this window is minimized. Click one, then press the new combination."), 12.5, colDim)
	y += u.px(14)
	keys := a.settings.Hotkeys.list()
	for i, name := range hotkeyNames {
		u.label(u.face(13), rectAt(r.X, y, u.px(220), u.px(34)), u.t(name), colText, 0)
		box := rectAt(r.X+u.px(230), y, u.px(220), u.px(34))
		id := fmt.Sprintf("settings/hotkey/%d", i)
		capturing := h.capturing == i
		bg, fg, text := colSurface, colText, keys[i].String()
		switch {
		case capturing:
			bg, fg, text = gfx.Mix(colSurface, colAccent, 0.25), colAccentHi, u.t("Press the keys… (Esc cancels)")
		case text == "":
			fg, text = colFaint, u.t("None")
		}
		if u.hover(id, box) {
			u.cursor = CursorHand
			if !capturing {
				bg = colSurfaceHi
			}
		}
		u.c.Fill(box, bg)
		if capturing {
			u.c.Outline(box, u.px(1.5), colAccent)
		}
		u.label(u.bold(12.5), box.Inset(u.px(12)), text, fg, 0.5)
		if u.clicked(id, box) && !capturing {
			h.capturing, h.refused = i, ""
			a.applyHotkeys()
		}
		if keys[i].Key != 0 && u.button(id+"/clear", rectAt(box.Right()+u.px(8), y+u.px(2), u.px(90), u.px(30)), "", u.t("Clear"), ghost, !capturing) {
			*keys[i] = Hotkey{}
			a.saveSettings()
			a.applyHotkeys()
		}
		y += u.px(40)
		var note string
		switch {
		case capturing && h.refused != "":
			note = h.refused
		case h.errs[i] != nil:
			note = u.t("Another program has taken this combination; choose another")
		}
		if note != "" {
			y = a.paragraph(box.X, y-u.px(2), r.W-(box.X-r.X), note, 11.5, colWarning) + u.px(6)
		}
	}
	y += u.px(10)
	if u.button("settings/hotkeys/reset", rectAt(r.X, y, u.px(160), u.px(32)), "", u.t("Restore the defaults"), plain, h.capturing < 0) {
		a.settings.Hotkeys = defaultHotkeys()
		a.saveSettings()
		a.applyHotkeys()
	}
	if h.capturing >= 0 {
		a.captureHotkey()
	}
}

// captureHotkey takes the next key press as the hotkey being chosen.
func (a *App) captureHotkey() {
	u := &a.u
	h := &a.hotkeys
	for _, k := range u.keys {
		if k.Code == keyEscape && !k.Ctrl && !k.Alt && !k.Shift {
			h.capturing = -1
			a.applyHotkeys()
			break
		}
		key := Hotkey{Ctrl: k.Ctrl, Alt: k.Alt, Shift: k.Shift, Key: k.Code}
		if !key.usable() {
			h.refused = u.tf("%s would get in the way of typing; add Ctrl or Alt, or use F1 to F24", key.String())
			continue
		}
		keys := a.settings.Hotkeys.list()
		other := -1
		for i, existing := range keys {
			if i != h.capturing && *existing == key {
				other = i
			}
		}
		if other >= 0 {
			h.refused = u.tf("%s is already the hotkey for “%s”", key.String(), u.t(hotkeyNames[other]))
			continue
		}
		*keys[h.capturing] = key
		h.capturing, h.refused = -1, ""
		a.saveSettings()
		a.applyHotkeys()
		break
	}
	u.keys = nil
}
