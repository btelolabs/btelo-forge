package ui

import (
	"path/filepath"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/assets"
)

// fileCommand is a file manager command keys can run: its shortcut for Settings, a short name for the list of keys
// at the bottom of the tab, and what it does.
type fileCommand struct {
	shortcut
	short string
	run   func()
	// listed puts it in the list of keys at the bottom; moving commands are summed up there instead.
	listed bool
}

// The file manager's virtual keys besides letters: '.', '/', '[', ']' and F2, F5.
const (
	keyPeriod  = 0xbe
	keySlash   = 0xbf
	keyBracket = 0xdb
	keyClose   = 0xdd
	keyF2      = 0x71
	keyF5      = 0x74
)

// fileCommands are the file manager's commands, in the order Settings and the list of keys show them.
func (a *App) fileCommands() []fileCommand {
	u := &a.u
	fd := a.files
	c := func(id, label, short string, listed bool, run func(), ks ...*Key) fileCommand {
		return fileCommand{shortcut: shortcut{id: "files." + id, domain: domainFiles, label: label, defaults: keys(ks...)},
			short: short, run: run, listed: listed}
	}
	return []fileCommand{
		c("down", u.t("Move down"), u.t("down"), false, func() { a.moveRows(fd, 1) }, key('J', ""), key(keyDown, "")),
		c("up", u.t("Move up"), u.t("up"), false, func() { a.moveRows(fd, -1) }, key('K', ""), key(keyUp, "")),
		c("parent", u.t("Go to the folder above"), u.t("up a folder"), false, func() { a.goUp(fd.pane()) }, key('H', ""), key(keyBack, "")),
		c("enter", u.t("Open the folder, or the file in its own tab"), u.t("open"), false, func() { a.enter(fd) }, key('L', ""), key(keyEnter, "")),
		c("left", u.t("Left: the picture before, or the folder above in a list"), u.t("left"), false, func() { a.sideways(fd, -1) }, key(keyLeft, "")),
		c("right", u.t("Right: the picture after, or into the folder in a list"), u.t("right"), false, func() { a.sideways(fd, 1) }, key(keyRight, "")),
		c("top", u.t("Go to the first item"), u.t("first"), false, func() { a.moveCursor(fd, -1<<30) }, key(keyHome, "")),
		c("bottom", u.t("Go to the last item"), u.t("last"), false, func() { a.moveCursor(fd, 1<<30) }, key(keyEnd, ""), key('G', "s")),
		c("page-down", u.t("Down a page"), u.t("page down"), false, func() { a.pageBy(fd, 1) }, key(keyPageDn, ""), key('D', "c")),
		c("page-up", u.t("Up a page"), u.t("page up"), false, func() { a.pageBy(fd, -1) }, key(keyPageUp, ""), key('U', "c")),
		c("preview", u.t("Preview the file, playing a video or sound, or close the preview"), u.t("preview"), true, func() { a.togglePreview(fd) }, key(keySpace, "")),
		c("view", u.t("Show this list as pictures with tags, or as a list"), u.t("pictures/list"), true, func() { a.toggleView(fd) }, key('Q', "")),
		c("mark", u.t("Mark or unmark the item"), u.t("mark"), true, func() { a.toggleMark(fd) }, key('M', "")),
		c("mark-all", u.t("Mark every item"), u.t("mark all"), false, func() { a.markAll(fd) }, key('A', "c")),
		c("tag", u.t("Tag the item, or the marked items"), u.t("tag"), true, func() { a.openPrompt(fd, promptTag) }, key('T', "")),
		c("filter", u.t("Search the pictures by name and tag"), u.t("search pictures"), true, func() { a.focusFilter(fd) }, key(keySlash, ""), key('F', "c")),
		c("split", u.t("Open another list beside this one"), u.t("split"), true, func() { a.splitPane(fd) }, key('E', "")),
		c("close-pane", u.t("Close this list"), u.t("close list"), true, func() { a.closePane(fd) }, key('W', "")),
		c("pane-previous", u.t("Go to the list on the left"), u.t("left list"), true, func() { a.focusPane(fd, fd.at-1) }, key(keyBracket, "")),
		c("pane-next", u.t("Go to the list on the right"), u.t("right list"), true, func() { a.focusPane(fd, fd.at+1) }, key(keyClose, "")),
		c("cut", u.t("Mark to move: they move when pasted with v"), u.t("mark to move"), true, func() { a.clipFiles(fd, true) }, key('X', ""), key('X', "c")),
		c("copy", u.t("Copy"), u.t("copy"), true, func() { a.clipFiles(fd, false) }, key('C', ""), key('C', "c")),
		c("paste", u.t("Paste"), u.t("paste"), true, func() { a.pasteFiles(fd) }, key('V', ""), key('V', "c")),
		c("delete", u.t("Delete (to the Recycle Bin)"), u.t("delete"), true, func() { a.askDelete(fd, false) }, key('D', ""), key(keyDelete, "")),
		c("delete-forever", u.t("Delete for good"), u.t("delete for good"), true, func() { a.askDelete(fd, true) }, key('D', "s"), key(keyDelete, "s")),
		c("new", u.t("New file, or folder when the name has no extension"), u.t("new"), true, func() { a.openPrompt(fd, promptNew) }, key('N', "")),
		c("compress", u.t("Compress the marked items into a zip file"), u.t("compress"), true, func() { a.compressFiles(fd) }, key('Z', "s")),
		c("extract", u.t("Extract the archive beside it"), u.t("extract"), true, func() { a.extractFile(fd) }, key('U', "s")),
		c("copy-path", u.t("Copy the path"), u.t("copy path"), true, func() { a.copyPaths(fd) }, key('Y', "")),
		c("analyse", u.t("Analyse what takes the space in this folder, or leave the analysis"), u.t("analyse"), true, func() { a.toggleAnalysis(fd.pane()) }, key('I', "")),
		c("undo", u.t("Undo"), u.t("undo"), true, func() { a.undoFiles() }, key('U', ""), key('Z', "c")),
		c("jump", u.t("Jump to a folder visited"), u.t("jump"), true, func() { a.openPrompt(fd, promptJump) }, key('Z', "")),
		c("rename", u.t("Rename"), u.t("rename"), true, func() { a.openPrompt(fd, promptRename) }, key('R', ""), key(keyF2, "")),
		c("path", u.t("Type or paste a path to go to"), u.t("path"), true, func() { a.openPrompt(fd, promptPath) }, key('L', "c")),
		c("find", u.t("Find files by name in this folder"), u.t("find names"), true, func() { a.openSearch(fd, false) }, key('F', "")),
		c("grep", u.t("Find text in the files of this folder"), u.t("find text"), true, func() { a.openSearch(fd, true) }, key('G', "")),
		c("explorer", u.t("Show the folder in the system's file manager"), u.t("system file manager"), true, func() { a.revealFolder(fd) }, key('P', "")),
		c("open", u.t("Open with the default program"), u.t("open with"), true, func() { a.openDefault(fd) }, key('O', "")),
		c("hidden", u.t("Show or hide hidden files"), u.t("hidden files"), true, func() { a.toggleHidden() }, key(keyPeriod, "")),
		c("sort-size", u.t("Sort by size"), u.t("by size"), true, func() { a.sortBy(fd.pane(), listBySize) }, key('S', "s")),
		c("sort-name", u.t("Sort by name"), u.t("by name"), true, func() { a.sortBy(fd.pane(), listByName) }, key('N', "s")),
		c("sort-items", u.t("Sort by the number of items"), u.t("by items"), true, func() { a.sortBy(fd.pane(), listByItems) }, key('C', "s")),
		c("sort-modified", u.t("Sort by the time modified"), u.t("by time"), true, func() { a.sortBy(fd.pane(), listByTime) }, key('M', "s")),
		c("refresh", u.t("Read the folders again"), u.t("refresh"), true, func() { a.refreshFiles(fd) }, key(keyF5, "")),
		c("escape", u.t("Unmark, or leave the search"), u.t("unmark"), true, func() { a.escapeFiles(fd) }, key(keyEscape, "")),
	}
}

// fileShortcuts lists the file manager's commands for Settings; their ids, names and keys do not depend on a tab.
func (a *App) fileShortcuts() []shortcut {
	var out []shortcut
	for _, c := range a.fileCommands() {
		out = append(out, c.shortcut)
	}
	return out
}

// fileKeys runs the file manager commands the keys pressed this frame are bound to, and lets the others through to
// the general shortcuts, such as Ctrl+T and Ctrl+W.
func (a *App) fileKeys(fd *filesDoc) {
	u := &a.u
	cmds := a.fileCommands()
	var rest []Key
	for _, k := range u.keys {
		if fd.previewFocus && k.Code != keyEscape && !(k.Code == keySpace && !k.Ctrl) && a.previewKey(k) {
			continue
		}
		if fd.previewFocus && k.Code == keyEscape {
			fd.previewFocus = false
			continue
		}
		ran := false
		for _, c := range cmds {
			if a.bound(c.id, c.defaults, k) {
				c.run()
				ran = true
				break
			}
		}
		if !ran {
			rest = append(rest, k)
		}
		if a.files != fd || fd.prompt != nil || fd.confirm != nil {
			break // the tab was closed, or a command opened a prompt that takes the keys that follow
		}
	}
	u.keys = rest
	u.typed = nil // the letters were commands, not text
}

// firstKey is the first key of a command as the list of keys writes it, or "" when it has none.
func (a *App) firstKey(c fileCommand) string {
	if ks := a.keysFor(c.id, c.defaults); len(ks) > 0 {
		return fileKeyLabel(strings.ToLower(hotkeyOf(ks[0]).String()))
	}
	return ""
}

// keyList is the list of keys at the bottom: each command's first key with its short name, the moving keys summed
// up first.
func (a *App) keyList() []string {
	u := &a.u
	out := []string{"hjkl/←↓↑→ " + u.t("move")}
	for _, c := range a.fileCommands() {
		if !c.listed {
			continue
		}
		if k := a.firstKey(c); k != "" {
			out = append(out, k+" "+c.short)
		}
	}
	return out
}

// itemKeys are the keys that act on the item at path, for the list of keys while the pointer is over it: what
// opens or previews it first, then what changes it.
func (a *App) itemKeys(path string) []string {
	u := &a.u
	ids := []string{"enter", "preview", "tag", "rename", "copy-path", "copy", "cut", "delete", "open", "mark", "explorer"}
	kind := assets.KindOf(path)
	dir := isFolder(path)
	switch {
	case dir && filepath.Dir(path) == path:
		ids = []string{"enter", "explorer"} // a drive
	case dir:
		ids = []string{"enter", "preview", "analyse", "tag", "rename", "copy-path", "copy", "cut", "delete", "mark", "explorer"}
	}
	cmds := a.fileCommands()
	var out []string
	for _, id := range ids {
		for _, c := range cmds {
			if c.id != "files."+id {
				continue
			}
			k := a.firstKey(c)
			if k == "" {
				break
			}
			short := c.short
			switch {
			case id == "preview" && (kind == assets.Video || kind == assets.Audio || kind == assets.Edit):
				short = u.t("play")
			case id == "enter" && !dir && !opensInApp(kind):
				short = u.t("preview")
			case id == "enter" && !dir && kind == assets.Video:
				short = u.t("edit")
			}
			out = append(out, k+" "+short)
		}
	}
	return out
}

// focusFilter puts the keys in the box that searches the list's pictures by name and tag, showing the list as
// pictures first when it is a list of rows.
func (a *App) focusFilter(fd *filesDoc) {
	p := fd.pane()
	if p.search != nil {
		return
	}
	if !p.grid {
		a.toggleView(fd)
	}
	a.u.focusEdit(filterBoxID(p), p.g.search, false)
}

// fileKeyLabel writes a key as the list of keys shows it: a capital for Shift with a letter, as vim writes it.
func fileKeyLabel(k string) string {
	if rest, ok := strings.CutPrefix(k, "shift+"); ok && len(rest) == 1 && rest[0] >= 'a' && rest[0] <= 'z' {
		return strings.ToUpper(rest)
	}

	return k
}
