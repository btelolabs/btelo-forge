package ui

import (
	"math"
	"slices"
	"strings"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
	"github.com/btelolabs/btelo-video-cut/internal/paint"
)

// Keyboard shortcuts, chosen in Settings for each kind of work on its own: the general ones (tabs, opening), video
// editing, drawing, and recording (the system-wide hotkeys in hotkeys.go). Each command has a stable id, under
// which the keys the user chose are kept; a command the user has not changed keeps its defaults.

// The domains shortcuts belong to, in the order Settings shows them.
const (
	domainGeneral = iota
	domainVideo
	domainDrawing
	domainRecording
)

var domainNames = msgs("General", "Video editing", "Drawing", "Recording")

// shortcut is a command keys can run: its id, domain, name (translated) and default keys. A tool's key picks the
// tool; tools may share one, as Photoshop's do, and Shift with it goes to the next.
type shortcut struct {
	id       string
	domain   int
	label    string
	defaults []Key
	tool     bool
}

// hotkeyOf and keyOf convert between a key press and a stored combination.
func hotkeyOf(k Key) Hotkey { return Hotkey{Ctrl: k.Ctrl, Alt: k.Alt, Shift: k.Shift, Key: k.Code} }
func keyOf(h Hotkey) Key    { return Key{Code: h.Key, Ctrl: h.Ctrl, Alt: h.Alt, Shift: h.Shift} }

// keysFor are the keys that run the command id: those chosen in Settings, or its defaults.
func (a *App) keysFor(id string, defaults []Key) []Key {
	chosen, ok := a.settings.Keys[id]
	if !ok {
		return defaults
	}
	out := make([]Key, len(chosen))
	for i, h := range chosen {
		out[i] = keyOf(h)
	}
	return out
}

// bound reports whether the press k runs the command id.
func (a *App) bound(id string, defaults []Key, k Key) bool {
	return slices.Contains(a.keysFor(id, defaults), k)
}

// keyHint is the first key that runs the command id, written for a menu or a hint, or "" when none does.
func (a *App) keyHint(id string, defaults []Key) string {
	if ks := a.keysFor(id, defaults); len(ks) > 0 {
		return hotkeyOf(ks[0]).String()
	}
	return ""
}

// keys lists default keys: each given as a code and modifiers, as key does.
func keys(ks ...*Key) []Key {
	var out []Key
	for _, k := range ks {
		if k != nil {
			out = append(out, *k)
		}
	}
	return out
}

// appCommand is a general or video editing command with what it does.
type appCommand struct {
	shortcut
	run func()
	// video says it needs a project open.
	video bool
}

// appCommands are the general and the video editing commands keys can run.
func (a *App) appCommands() []appCommand {
	u := &a.u
	g := func(id, label string, run func(), ks ...*Key) appCommand {
		return appCommand{shortcut: shortcut{id: "general." + id, domain: domainGeneral, label: label, defaults: keys(ks...)}, run: run}
	}
	v := func(id, label string, run func(), ks ...*Key) appCommand {
		return appCommand{shortcut: shortcut{id: "video." + id, domain: domainVideo, label: label, defaults: keys(ks...)}, run: run, video: true}
	}
	step := func(frames bool, back bool) func() {
		return func() {
			s := 1.0
			if frames {
				s = 1 / max(a.p.Canvas.FPS, 1)
			}
			if back {
				s = -s
			}
			a.seek(a.playhead + s)
		}
	}
	return []appCommand{
		g("tab.new", u.t("New tab"), a.newTab, key('T', "c")),
		g("tab.close", u.t("Close the tab"), func() { a.closeTab(a.current()) }, key('W', "c")),
		g("tab.next", u.t("Next tab"), func() { a.switchTab((a.current() + 1) % len(a.docs)) }, key(keyTab, "c")),
		g("tab.previous", u.t("Previous tab"), func() { a.switchTab((a.current() + len(a.docs) - 1) % len(a.docs)) }, key(keyTab, "cs")),
		g("open", u.t("Open a video"), a.chooseVideo, key('O', "c")),
		g("paste", u.t("Paste"), a.paste, key('V', "c")),

		v("play", u.t("Play or pause"), a.togglePlay, key(keySpace, "")),
		v("split", u.t("Split at the playhead"), a.split, key('S', "")),
		v("delete", u.t("Delete the selection"), a.deleteSelection, key(keyDelete, ""), key(keyBack, "")),
		v("undo", u.t("Undo"), a.undo, key('Z', "c")),
		v("redo", u.t("Redo"), a.redo, key('Y', "c"), key('Z', "cs")),
		v("text", u.t("Add text"), a.addText, key('T', "")),
		v("export", u.t("Export"), func() { a.startExport() }, key('E', "c")),
		v("frame.back", u.t("Back a frame"), step(true, true), key(keyLeft, "")),
		v("frame.forward", u.t("Forward a frame"), step(true, false), key(keyRight, "")),
		v("second.back", u.t("Back a second"), step(false, true), key(keyLeft, "s")),
		v("second.forward", u.t("Forward a second"), step(false, false), key(keyRight, "s")),
		v("start", u.t("Go to the start"), func() { a.seek(0) }, key(keyHome, "")),
		v("end", u.t("Go to the end"), func() { a.seek(a.p.Duration()) }, key(keyEnd, "")),
		v("deselect", u.t("Deselect"), func() { a.selectOnly(selection{}) }, key(keyEscape, "")),
		v("select-all", u.t("Select all"), a.selectAll, key('A', "c")),
	}
}

// shortcuts runs the general and video editing commands the keys pressed this frame are bound to.
func (a *App) shortcuts() {
	u := &a.u
	cmds := a.appCommands()
	for _, k := range u.keys {
		for _, c := range cmds {
			if (!c.video || a.p != nil) && a.bound(c.id, c.defaults, k) {
				c.run()
				break
			}
		}
	}
	u.keys = nil
}

// drawingKeys are the drawing commands that are not in the menus, named by drawingKeyNames.
var (
	drawingKeys = []struct {
		id       string
		defaults []Key
	}{
		{"draw.swap-colours", keys(key('X', ""))},
		{"draw.default-colours", keys(key('D', ""))},
		{"draw.brush-smaller", keys(key(0xdb, ""))},
		{"draw.brush-larger", keys(key(0xdd, ""))},
	}
	drawingKeyNames = msgs("Swap the colours", "Default colours", "Smaller brush", "Larger brush")
)

// drawingKey runs the drawing command bound to k that is not in the menus; it reports whether there was one.
func (a *App) drawingKey(k Key) bool {
	for _, dk := range drawingKeys {
		if !a.bound(dk.id, dk.defaults, k) {
			continue
		}
		switch dk.id {
		case "draw.swap-colours":
			a.swapColors()
		case "draw.default-colours":
			a.resetColors()
		case "draw.brush-smaller":
			a.brush.Size = max(1, math.Round(a.brush.Size/1.15))
			a.saveBrush()
		case "draw.brush-larger":
			a.brush.Size = min(500, math.Round(a.brush.Size*1.15+0.5))
			a.saveBrush()
		}
		return true
	}
	return false
}

// toolFor is the tool a press of k picks, and whether it picks one: of the tools with that key, the chosen one
// (or with Shift the next of them), else the one last used in its place.
func (a *App) toolFor(k Key) (artTool, bool) {
	if k.Ctrl || k.Alt {
		return 0, false
	}
	plain := Key{Code: k.Code}
	var with []artTool
	for _, e := range artTools {
		if slices.Contains(a.toolKeys(e.tool), plain) {
			with = append(with, e.tool)
		}
	}
	if len(with) == 0 {
		return 0, false
	}
	i := slices.Index(with, a.artTool)
	if i < 0 {
		i = 0
		for j, t := range with {
			if a.slotTool(toolPlace(t)) == t {
				i = j
				break
			}
		}
		if !k.Shift {
			return with[i], true
		}
	}
	if k.Shift {
		return with[(i+1)%len(with)], true
	}
	return with[i], true
}

// toolID is the id of a tool's key.
func toolID(t artTool) string {
	return "draw.tool." + strings.ReplaceAll(strings.ToLower(artToolNames[t]), " ", "-")
}

// toolKeys are the keys that pick tool t.
func (a *App) toolKeys(t artTool) []Key {
	return a.keysFor(toolID(t), keys(key(toolPlace(t), "")))
}

// drawingShortcuts lists the drawing commands for Settings: the tools, the menus' commands and the others.
func (a *App) drawingShortcuts() []shortcut {
	u := &a.u
	var out []shortcut
	for _, e := range artTools {
		out = append(out, shortcut{id: toolID(e.tool), domain: domainDrawing, label: u.tf("%s tool", u.t(artToolNames[e.tool])),
			defaults: keys(key(e.key, "")), tool: true})
	}
	// The menus' commands, read from a stand-in picture: their ids, names and keys do not depend on the picture.
	stand := &artDoc{d: paint.New(""), zoom: 1}
	for _, c := range a.artCommands(stand, gfx.Rect{}) {
		label := c.label
		if c.sub != "" {
			label = c.sub + " › " + label
		}
		out = append(out, shortcut{id: c.id, domain: domainDrawing, label: label, defaults: keys(c.key, c.also)})
	}
	for i, k := range drawingKeys {
		out = append(out, shortcut{id: k.id, domain: domainDrawing, label: u.t(drawingKeyNames[i]), defaults: k.defaults})
	}
	return out
}

// shortcutsOf lists a domain's commands for Settings.
func (a *App) shortcutsOf(domain int) []shortcut {
	if domain == domainDrawing {
		return a.drawingShortcuts()
	}
	var out []shortcut
	for _, c := range a.appCommands() {
		if c.domain == domain {
			out = append(out, c.shortcut)
		}
	}
	return out
}

// conflict is the command of the domain, other than id, whose keys include k, or "" when none; tools may share
// keys with tools. A domain's keys come before the general ones in its tabs, as a canvas's Ctrl+T is Free
// Transform rather than a new tab, so only commands of the same domain clash.
func (a *App) conflict(domain int, id string, tool bool, k Key) string {
	for _, s := range a.shortcutsOf(domain) {
		if s.id != id && !(tool && s.tool) && slices.Contains(a.keysFor(s.id, s.defaults), k) {
			return s.label
		}
	}
	return ""
}

// keyCapture is the command whose new key Settings is waiting for.
type keyCapture struct {
	id      string
	domain  int
	tool    bool
	refused string
}

// settingsKeys is the part of the Hotkeys page for one domain's commands: each with its keys, a click on which
// waits for a new one, and a way to clear it.
func (a *App) settingsKeys(domain int, r gfx.Rect) {
	u := &a.u
	list := a.shortcutsOf(domain)
	rowH := u.px(38)
	foot := u.px(44)
	area := gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H - foot}
	if u.hover("settings/keys", area) && u.wheel != 0 {
		a.keysScroll -= u.wheel * rowH * 2
		u.wheel = 0
	}
	a.keysScroll = min(max(a.keysScroll, 0), max(float32(len(list))*rowH-area.H, 0))
	u.c.Push(area)
	for i, s := range list {
		y := area.Y + float32(i)*rowH - a.keysScroll
		if y+rowH < area.Y || y > area.Bottom() {
			continue
		}
		u.label(u.face(13), rectAt(r.X, y, u.px(300), u.px(34)), s.label, colText, 0)
		box := rectAt(r.X+u.px(310), y, u.px(200), u.px(34))
		id := "settings/key/" + s.id
		capturing := a.keyCapture != nil && a.keyCapture.id == s.id
		bound := a.keysFor(s.id, s.defaults)
		var names []string
		for _, k := range bound {
			names = append(names, hotkeyOf(k).String())
		}
		bg, fg, text := colSurface, colText, strings.Join(names, ", ")
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
		u.c.RoundRect(box, u.px(7), bg)
		if capturing {
			u.c.RoundOutline(box, u.px(7), u.px(1.5), colAccent)
		}
		u.label(u.bold(12), box.Inset(u.px(10)), text, fg, 0.5)
		if u.clicked(id, box) && !capturing {
			a.keyCapture = &keyCapture{id: s.id, domain: domain, tool: s.tool}
		}
		if len(bound) > 0 && u.button(id+"/clear", rectAt(box.Right()+u.px(8), y+u.px(2), u.px(80), u.px(30)), "", u.t("Clear"), ghost, !capturing) {
			a.setKeys(s.id, nil)
		}
		if _, changed := a.settings.Keys[s.id]; changed && u.button(id+"/reset", rectAt(box.Right()+u.px(94), y+u.px(2), u.px(80), u.px(30)), "", u.t("Default"), ghost, !capturing) {
			delete(a.settings.Keys, s.id)
			a.saveSettings()
		}
	}
	u.c.Pop()
	y := area.Bottom() + u.px(8)
	if a.keyCapture != nil && a.keyCapture.refused != "" {
		u.label(u.face(12), rectAt(r.X+u.px(180), y, r.W-u.px(180), u.px(30)), a.keyCapture.refused, colWarning, 0)
	}
	if u.button("settings/keys/reset", rectAt(r.X, y, u.px(170), u.px(32)), "", u.t("Restore the defaults"), plain, a.keyCapture == nil) {
		for _, s := range list {
			delete(a.settings.Keys, s.id)
		}
		a.saveSettings()
	}
	if a.keyCapture != nil {
		a.captureKey(list)
	}
}

// setKeys chooses the keys of the command id.
func (a *App) setKeys(id string, ks []Key) {
	if a.settings.Keys == nil {
		a.settings.Keys = map[string][]Hotkey{}
	}
	hs := make([]Hotkey, len(ks))
	for i, k := range ks {
		hs[i] = hotkeyOf(k)
	}
	a.settings.Keys[id] = hs
	a.saveSettings()
}

// captureKey takes the next key press as the key of the command being chosen, unless another command has it.
func (a *App) captureKey(list []shortcut) {
	u := &a.u
	c := a.keyCapture
	for _, k := range u.keys {
		if k == (Key{Code: keyEscape}) {
			a.keyCapture = nil
			break
		}
		if isModifier(k.Code) {
			continue
		}
		if c.tool && (k.Ctrl || k.Alt || k.Shift) {
			c.refused = u.t("A tool's key is a single key: Shift with it goes to the next tool in its place")
			continue
		}
		if other := a.conflict(c.domain, c.id, c.tool, k); other != "" {
			c.refused = u.tf("%s is already the key for “%s”", hotkeyOf(k).String(), other)
			continue
		}
		a.setKeys(c.id, []Key{k})
		a.keyCapture = nil
		break
	}
	u.keys = nil
}

// isModifier reports whether a virtual-key code is a modifier on its own.
func isModifier(code uint16) bool {
	switch code {
	case 0x10, 0x11, 0x12, 0x5b, 0x5c, 0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5:
		return true
	}
	return false
}

// keyDomainTabs is the Hotkeys page's choice of domain; it returns the chosen one.
func (a *App) keyDomainTabs(r gfx.Rect) int {
	u := &a.u
	var names []string
	var domains []int
	for d, name := range domainNames {
		if d == domainRecording && a.recorder() == nil {
			continue // nothing records here
		}
		names, domains = append(names, u.t(name)), append(domains, d)
	}
	at := max(slices.Index(domains, a.keysDomain), 0)
	if pick := u.tabs("settings/keys/domain", r, names, at); pick != at {
		a.keysDomain, a.keysScroll, a.keyCapture = domains[pick], 0, nil
		if a.hotkeys.capturing >= 0 {
			a.hotkeys.capturing = -1
			a.applyHotkeys()
		}
	}
	return domains[max(slices.Index(domains, a.keysDomain), 0)]
}
