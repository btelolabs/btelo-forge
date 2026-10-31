package ui

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// Keyboard shortcuts, chosen in Settings for each kind of work on its own: the general ones (tabs, opening), video
// editing, drawing, and recording (the system-wide hotkeys in hotkeys.go). Each command has a stable id, under
// which the keys the user chose are kept; a command the user has not changed keeps its defaults.

// The domains shortcuts belong to. Settings shows the system-wide hotkeys first, then these in order.
const (
	domainGeneral = iota
	domainVideo
	domainDrawing
	domainFiles
	domainMind
	domainRecording
)

var domainNames = msgs("General", "Video editing", "Drawing", "Resource manager", "Mind maps", "System-wide")

// shortcut is a command keys can run: its id, domain, name (translated) and default keys. A tool's key picks the
// tool; tools may share one, as Photoshop's do, and Shift with it goes to the next. A brush command acts only while
// a painting tool is chosen, so it may share a key with a command that acts otherwise, and comes first. A reserved
// command's keys act in every tab before any other's, so no command of any domain may have them.
type shortcut struct {
	id       string
	domain   int
	label    string
	defaults []Key
	tool     bool
	brush    bool
	reserved bool
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
	// anywhere says it acts also while a dialog, a menu or Settings is open.
	anywhere bool
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
	// The reserved commands, a browser's: Ctrl (Command) and a digit from 1 to 9 bring that tab forward, and Ctrl
	// with =, - or 0 zooms the whole window, on the main keys or the number pad.
	reserved := func(c appCommand) appCommand {
		c.reserved = true
		return c
	}
	goTab := func(n int) appCommand {
		return reserved(g(fmt.Sprintf("tab.%d", n), u.tf("Go to tab %d", n), func() {
			if n <= len(a.docs) {
				a.switchTab(n - 1)
			}
		}, key('0'+uint16(n), "c"), key(keyNum0+uint16(n), "c")))
	}
	zoom := func(id, label string, run func(), ks ...*Key) appCommand {
		c := reserved(g(id, label, run, ks...))
		c.anywhere = true
		return c
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
		g("tab.new", u.t("New tab"), a.newTab, key('T', "cs")),
		g("tab.close", u.t("Close the tab"), func() { a.closeTab(a.current()) }, key('W', "c")),
		g("tab.next", u.t("Next tab"), func() { a.switchTab((a.current() + 1) % len(a.docs)) }, key(keyTab, "c")),
		g("tab.previous", u.t("Previous tab"), func() { a.switchTab((a.current() + len(a.docs) - 1) % len(a.docs)) }, key(keyTab, "cs")),
		goTab(1), goTab(2), goTab(3), goTab(4), goTab(5), goTab(6), goTab(7), goTab(8), goTab(9),
		zoom("zoom.in", u.t("Zoom the window in"), func() { a.stepWindowZoom(1) }, key(keyEquals, "c"), key(keyEquals, "cs"), key(keyNumPlus, "c")),
		zoom("zoom.out", u.t("Zoom the window out"), func() { a.stepWindowZoom(-1) }, key(keyMinus, "c"), key(keyNumMinus, "c")),
		zoom("zoom.reset", u.t("Reset the window's zoom"), func() { a.setWindowZoom(1) }, key('0', "c"), key(keyNum0, "c")),
		g("open", u.t("Open a video"), a.chooseVideo, key('O', "c")),
		g("paste", u.t("Paste"), a.paste, key('V', "c")),

		v("play", u.t("Play or pause"), a.togglePlay, key(keySpace, "")),
		v("split", u.t("Split at the playhead"), a.split, key('S', "")),
		v("delete", u.t("Delete the selection"), a.deleteSelection, key(keyDelete, ""), key(keyBack, "")),
		v("undo", u.t("Undo"), a.undo, key('Z', "c")),
		v("redo", u.t("Redo"), a.redo, key('Y', "c"), key('Z', "cs")),
		v("text", u.t("Add text"), a.addText, key('T', "")),
		v("export", u.t("Export"), func() { a.startExport() }, key('E', "c")),
		v("save", u.t("Save the edit as a file"), a.saveEditCommand, key('S', "c")),
		v("frame.back", u.t("Back a frame"), step(true, true), key(keyLeft, "")),
		v("frame.forward", u.t("Forward a frame"), step(true, false), key(keyRight, "")),
		v("second.back", u.t("Back a second"), step(false, true), key(keyLeft, "s")),
		v("second.forward", u.t("Forward a second"), step(false, false), key(keyRight, "s")),
		v("start", u.t("Go to the start"), func() { a.seek(0) }, key(keyHome, "")),
		v("end", u.t("Go to the end"), func() { a.seek(a.p.Duration()) }, key(keyEnd, "")),
		v("deselect", u.t("Deselect"), func() { a.selectOnly(selection{}) }, key(keyEscape, "")),
		v("select-all", u.t("Select all"), a.selectAll, key('A', "c")),
		v("watch", u.t("Player mode: hide every tool, leaving the picture and the seek bar"), func() { a.setWatching(!a.watch.on) }, key(keyTab, "")),
	}
}

// reservedKeys runs the reserved commands (going to a tab, zooming the window) the keys pressed this frame are
// bound to, before any tab, tool, field or dialog sees the keys, and takes those keys away. Only choosing a key in
// Settings, which must see every press, keeps them; and a dialog or menu, which belongs to its tab, keeps the tab.
func (a *App) reservedKeys() {
	u := &a.u
	if a.hotkeys.capturing >= 0 || a.keyCapture != nil {
		return
	}
	var reserved []appCommand
	for _, c := range a.appCommands() {
		if c.reserved && (c.anywhere || !u.blocked) {
			reserved = append(reserved, c)
		}
	}
	var rest []Key
	for _, k := range u.keys {
		i := slices.IndexFunc(reserved, func(c appCommand) bool { return a.bound(c.id, c.defaults, k) })
		if i < 0 {
			rest = append(rest, k)
			continue
		}
		reserved[i].run()
	}
	u.keys = rest
}

// shortcuts runs the general and video editing commands the keys pressed this frame are bound to.
func (a *App) shortcuts() {
	u := &a.u
	cmds := a.appCommands()
	watching := a.watching()
	for _, k := range u.keys {
		if watching && k == (Key{Code: keyEscape}) {
			a.setWatching(false)
			continue
		}
		for _, c := range cmds {
			if (!c.video || a.p != nil) && (!watching || watchCommands[c.id]) && a.bound(c.id, c.defaults, k) {
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
		brush    bool
	}{
		{"draw.brush-smaller", keys(key(0xdb, ""), key('D', "")), true},
		{"draw.brush-larger", keys(key(0xdd, ""), key('F', "")), true},
		{"draw.swap-colours", keys(key('X', "")), false},
		{"draw.swap-fill-stroke", keys(key('X', "s")), false},
		{"draw.default-colours", keys(key('D', "")), false},
	}
	drawingKeyNames = msgs("Smaller brush", "Larger brush", "Swap the colours", "Swap the fill and the stroke", "Default colours")
)

// painting reports whether the chosen tool paints with the brush, so the brush commands act.
func (a *App) painting() bool {
	switch a.artTool {
	case toolBrush, toolEraser, toolClone, toolSpot, toolHeal, toolDodge, toolBurn, toolSponge, toolBlur, toolSharpen, toolSmudge, toolLiquify:
		return true
	}
	return false
}

// drawingKey runs the drawing command bound to k that is not in the menus; it reports whether there was one.
func (a *App) drawingKey(k Key) bool {
	for _, dk := range drawingKeys {
		if !a.bound(dk.id, dk.defaults, k) || dk.brush && !a.painting() && a.artTool != toolPencil {
			continue
		}
		if a.artTool == toolPencil && (dk.id == "draw.brush-smaller" || dk.id == "draw.brush-larger") {
			// The vector brush's size, its own.
			size := a.vectorBrush().Size
			if dk.id == "draw.brush-smaller" {
				a.setVectorBrushSize(size / 1.15)
			} else {
				a.setVectorBrushSize(size*1.15 + 0.5)
			}
			return true
		}
		switch dk.id {
		case "draw.swap-colours":
			if _, n := a.paintedVector(); n != nil {
				a.pickBackground = !a.pickBackground // the colours edit the stroke, or the fill again
				a.picker.hex = ""
				break
			}
			a.swapColors()
		case "draw.swap-fill-stroke":
			if ad, n := a.paintedVector(); n != nil {
				a.swapFillAndStroke(ad, n)
			}
		case "draw.default-colours":
			a.resetColors()
		case "draw.brush-smaller":
			a.stepBrushSize(false)
		case "draw.brush-larger":
			a.stepBrushSize(true)
		}
		return true
	}
	return false
}

// stepBrushSize makes the brush a step smaller or larger, and keeps it.
func (a *App) stepBrushSize(larger bool) {
	if larger {
		a.brush.Size = min(maxBrushSize, math.Round(a.brush.Size*1.15+0.5))
	} else {
		a.brush.Size = max(1, math.Round(a.brush.Size/1.15))
	}
	a.saveBrush()
}

// maxBrushSize is the largest brush the options bar, the keys and the screen toolbar give.
const maxBrushSize = 500

// toolFor is the tool a press of k picks, and whether it picks one: of the tools with that key, the chosen one
// (or with Shift the next of them), else the one last used in its place.
func (a *App) toolFor(k Key) (artTool, bool) {
	if k.Ctrl || k.Alt {
		return 0, false
	}
	plain := Key{Code: k.Code}
	var with []artTool
	for _, e := range artTools {
		if a.toolOffered(e.tool) && slices.Contains(a.toolKeys(e.tool), plain) {
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
	return a.keysFor(toolID(t), toolDefaultKeys(t))
}

// drawingShortcuts lists the drawing commands for Settings: the tools, the menus' commands and the others.
func (a *App) drawingShortcuts() []shortcut {
	u := &a.u
	var out []shortcut
	for _, e := range artTools {
		out = append(out, shortcut{id: toolID(e.tool), domain: domainDrawing, label: u.tf("%s tool", u.t(artToolNames[e.tool])),
			defaults: toolDefaultKeys(e.tool), tool: true})
	}
	// The menus' commands, read from a stand-in picture: their ids, names and keys do not depend on the picture.
	stand := &artDoc{d: paint.New(""), zoom: 1}
	for _, c := range a.artCommands(stand, gfx.Rect{}) {
		label := c.label
		if c.sub != "" {
			label = c.sub + " › " + label
		}
		out = append(out, shortcut{id: c.id, domain: domainDrawing, label: label, defaults: c.defaults()})
	}
	for i, k := range drawingKeys {
		out = append(out, shortcut{id: k.id, domain: domainDrawing, label: u.t(drawingKeyNames[i]), defaults: k.defaults, brush: k.brush})
	}
	return out
}

// shortcutsOf lists a domain's commands for Settings.
func (a *App) shortcutsOf(domain int) []shortcut {
	if domain == domainDrawing {
		return a.drawingShortcuts()
	}
	if domain == domainFiles {
		return a.fileShortcuts()
	}
	if domain == domainMind {
		return a.mindShortcuts()
	}
	var out []shortcut
	for _, c := range a.appCommands() {
		if c.domain == domain {
			out = append(out, c.shortcut)
		}
	}
	return out
}

// conflict is the command, other than id, whose keys include k, or "" when none; tools may share keys with tools,
// and brush commands with the others. A domain's keys come before the general ones in its tabs, as a canvas's
// Ctrl+T is Free Transform rather than a new tab, so only commands of the same domain clash; but the reserved
// commands' keys come before every domain's, so they clash with the commands of all.
func (a *App) conflict(domain int, id string, tool bool, k Key) string {
	brush, reserved := false, false
	for _, s := range a.shortcutsOf(domain) {
		if s.id == id {
			brush, reserved = s.brush, s.reserved
		}
	}
	has := func(s shortcut) bool { return s.id != id && slices.Contains(a.keysFor(s.id, s.defaults), k) }
	for _, s := range a.shortcutsOf(domain) {
		if !(tool && s.tool) && s.brush == brush && has(s) {
			return s.label
		}
	}
	for d := domainGeneral; d < domainRecording; d++ {
		if d == domain {
			continue
		}
		for _, s := range a.shortcutsOf(d) {
			if (reserved || s.reserved) && has(s) {
				return s.label
			}
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
		u.c.Fill(box, bg)
		if capturing {
			u.c.Outline(box, u.px(1.5), colAccent)
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
	if a.recorder() != nil { // nothing records here otherwise
		names, domains = append(names, u.t(domainNames[domainRecording])), append(domains, domainRecording)
	}
	for d := domainGeneral; d < domainRecording; d++ {
		names, domains = append(names, u.t(domainNames[d])), append(domains, d)
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
