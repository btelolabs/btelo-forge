package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// Every tool opens and saves files through the file picker: the resource manager's own list, in a dialog over the
// window, with its pictures, filters, search, jump to a folder visited and path bar. The list is a filesDoc like a
// file manager tab's, held by a doc that is in no tab and stands in for the tab in front while the dialog is drawn.
// Only the picker's button leads to the system's own dialogs.

// pickMode is what the picker is asked for.
type pickMode int

const (
	pickOpen   pickMode = iota // one or more files that are there
	pickFolder                 // a folder
	pickSave                   // a name in a folder, to write a file to
)

// pickRequest is what a tool asks the picker for.
type pickRequest struct {
	key      string   // the folder shown last is kept under it, to open there the next time
	title    string   // says what is being chosen
	kinds    string   // names exts, for the switch between them and all files
	exts     []string // the files offered, or none for all of them; saving, the first is added to a name without it
	multiple bool     // several files can be chosen
	dir      string   // the folder to open in, before the one kept
	name     string   // the name proposed for a file to save, with its extension
}

// filePick is the dialog open.
type filePick struct {
	pickRequest
	mode    pickMode
	doc     *doc // holds the list, in no tab
	fd      *filesDoc
	done    func([]string)
	all     bool     // files of every kind are shown
	replace string   // the file the dialog asks whether to replace
	focus   string   // what had the keys before, given them back
	ended   bool     // the dialog closes at the end of this frame
	result  []string // what done is given, nil when it was cancelled
}

// pickNameID is the box a file to save is named in.
const pickNameID = "pick/name"

// pickPictures are the pictures the Office tools insert.
var pickPictures = []string{".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp", ".tif", ".tiff"}

// pickFiles asks for files that are there; done is not called when the dialog is cancelled.
func (a *App) pickFiles(r pickRequest, done func(paths []string)) {
	a.openPick(r, pickOpen, done)
}

// pickDir asks for a folder.
func (a *App) pickDir(r pickRequest, done func(dir string)) {
	a.openPick(r, pickFolder, func(paths []string) { done(paths[0]) })
}

// pickSaveAs asks where to write a file, having asked before replacing one that is there.
func (a *App) pickSaveAs(r pickRequest, done func(path string)) {
	a.openPick(r, pickSave, func(paths []string) { done(paths[0]) })
}

// openPick shows the dialog; one already open keeps the window. A preview in the tab behind stops, as the dialog's
// list has the only preview.
func (a *App) openPick(r pickRequest, mode pickMode, done func([]string)) {
	if a.filePicker != nil {
		return
	}
	u := &a.u
	if a.files != nil && a.files.preview {
		a.togglePreview(a.files)
	}
	dir := a.pickStart(r)
	pk := &filePick{pickRequest: r, mode: mode, done: done, focus: u.focus}
	p := &filesPane{dir: dir, marked: map[string]bool{}, sort: listByTime}
	a.showGrid(p)
	p.g.size = 120
	p.only = pk.shows
	a.readPane(p)
	if dir != thisPC {
		a.visited(dir)
	}
	pk.fd = &filesDoc{panes: []*filesPane{p}, pick: pk}
	pk.doc = &doc{files: pk.fd}
	a.filePicker = pk
	u.focus = ""
	if mode == pickSave {
		// The name is chosen without its extension, so typing replaces just the name.
		n := len([]rune(strings.TrimSuffix(r.name, filepath.Ext(r.name))))
		u.beginEdit(pickNameID, r.name, false, 0, n)
	}
	a.host.Wake()
}

// pickStart is where the dialog opens: the folder asked for, the one shown last for this kind of file, else the
// default folder.
func (a *App) pickStart(r pickRequest) string {
	if isFolder(r.dir) {
		return filepath.Clean(r.dir)
	}
	if d := a.settings.Files.PickDirs[r.key]; isFolder(d) {
		return filepath.Clean(d)
	}
	return a.defaultFolder()
}

// shows reports whether the list shows the file called name; a folder is always shown.
func (pk *filePick) shows(name string) bool {
	switch {
	case pk.mode == pickFolder:
		return false
	case pk.all || len(pk.exts) == 0:
		return true
	}
	return slices.ContainsFunc(pk.exts, func(e string) bool { return strings.EqualFold(filepath.Ext(name), e) })
}

// filesDocs are the file manager's lists in use: those of the tabs, and the dialog's while it is open.
func (a *App) filesDocs() []*filesDoc {
	var out []*filesDoc
	for _, d := range a.docs {
		if d.files != nil {
			out = append(out, d.files)
		}
	}
	if a.filePicker != nil {
		out = append(out, a.filePicker.fd)
	}
	return out
}

// pickInput is the input of a frame, kept from the tab and the dialogs behind the picker.
type pickInput struct {
	mx, my, wheel float32
	pressed       bool
	double        bool
	rightAt       *Pointer
	keys          []Key
	typed         []rune
}

// takeInput keeps the frame's input for the picker, leaving nothing for what is drawn behind it.
func (u *ctx) takeInput() pickInput {
	in := pickInput{u.mx, u.my, u.wheel, u.pressed, u.double, u.rightAt, u.keys, u.typed}
	u.mx, u.my, u.wheel, u.pressed, u.double, u.rightAt, u.keys, u.typed = -1<<20, -1<<20, 0, false, false, nil, nil, nil
	return in
}

// giveInput hands the picker the input kept for it.
func (u *ctx) giveInput(in pickInput) {
	u.mx, u.my, u.wheel, u.pressed, u.double, u.rightAt, u.keys, u.typed = in.mx, in.my, in.wheel, in.pressed, in.double, in.rightAt, in.keys, in.typed
}

// drawFilePick draws the dialog over everything else, its list standing in for the tab in front, and when it was
// answered closes it and passes the answer on, with that tab in front again.
func (a *App) drawFilePick() {
	pk := a.filePicker
	if pk == nil {
		return
	}
	front := a.doc
	a.doc = pk.doc
	func() {
		defer func() { a.doc = front }()
		a.drawPickDialog(pk)
	}()
	if pk.ended {
		a.closePick(pk)
		if pk.result != nil {
			pk.done(pk.result)
		}
	}
}

// closePick takes the dialog away and gives the keys back to what had them.
func (a *App) closePick(pk *filePick) {
	a.filePicker = nil
	a.leaveFiles(pk.doc, true)
	a.u.focus = ""
	if pk.focus != "" {
		a.u.setFocus(pk.focus)
	}
}

// pickCancel closes the dialog at the end of the frame, doing nothing.
func (pk *filePick) pickCancel() { pk.ended = true }

// pickFinish answers the dialog, keeping the folder for the next time.
func (a *App) pickFinish(pk *filePick, paths []string) {
	dir := paths[0]
	if pk.mode != pickFolder {
		dir = filepath.Dir(dir)
	}
	if pk.key != "" && a.settings.Files.PickDirs[pk.key] != dir {
		if a.settings.Files.PickDirs == nil {
			a.settings.Files.PickDirs = map[string]string{}
		}
		a.settings.Files.PickDirs[pk.key] = dir
		a.saveSettings()
	}
	pk.result, pk.ended = paths, true
}

// pickFilesChosen are the files the Open button would give: the marked ones, or the one under the cursor or found.
func (pk *filePick) pickFilesChosen() []string {
	p := pk.fd.pane()
	var out []string
	if p.search != nil {
		if path, _ := p.search.chosen(); path != "" && !isFolder(path) && pk.shows(filepath.Base(path)) {
			out = append(out, path)
		}
		return out
	}
	if !pk.multiple {
		if e := p.current(); e != nil && !e.Dir {
			out = append(out, p.path(e))
		}
		return out
	}
	for _, path := range p.selected() {
		if !isFolder(path) {
			out = append(out, path)
		}
	}
	return out
}

// pickClicked follows a click on an item: saving, a file's name becomes the name, to write over it or start from it.
func (a *App) pickClicked(fd *filesDoc) {
	pk := fd.pick
	if pk == nil || pk.mode != pickSave {
		return
	}
	if e := fd.pane().current(); e != nil && !e.Dir {
		pk.name = e.Name
		a.u.focus = ""
	}
}

// pickEnter does for the item under the cursor what a double click or Enter means: a folder is gone into, a file is
// the answer. What a search found and the filter hides is shown in its folder, for all files.
func (a *App) pickEnter(fd *filesDoc) {
	pk, p := fd.pick, fd.pane()
	if p.search != nil {
		path, _ := p.search.chosen()
		switch {
		case path == "":
		case isFolder(path):
			a.closeSearch(p)
			a.goTo(p, path, "")
		case pk.mode == pickSave:
			pk.name = filepath.Base(path)
			a.openFound(fd, p)
		case pk.mode == pickFolder:
			a.openFound(fd, p)
		case pk.shows(filepath.Base(path)):
			a.pickFinish(pk, []string{path})
		default:
			a.pickShowAll(pk, true)
			a.openFound(fd, p)
		}
		return
	}
	e := p.current()
	if e == nil {
		return
	}
	path := p.path(e)
	switch {
	case e.Dir:
		a.goTo(p, path, "")
	case a.busyJob(path) != nil:
	case pk.mode == pickSave:
		pk.name = e.Name
		a.pickConfirm(pk)
	case p.marked[e.Name]:
		a.pickFinish(pk, pk.pickFilesChosen())
	default:
		a.pickFinish(pk, []string{path})
	}
}

// pickConfirm does what the dialog's main button says.
func (a *App) pickConfirm(pk *filePick) {
	p := pk.fd.pane()
	switch pk.mode {
	case pickFolder:
		if p.search != nil {
			a.closeSearch(p)
		}
		if p.atDrives() {
			if e := p.current(); e != nil {
				a.pickFinish(pk, []string{p.path(e)})
			}
			return
		}
		a.pickFinish(pk, []string{p.dir})
	case pickSave:
		a.pickSaveHere(pk)
	default:
		if chosen := pk.pickFilesChosen(); len(chosen) > 0 {
			a.pickFinish(pk, chosen)
			return
		}
		a.pickEnter(pk.fd)
	}
}

// pickSaveHere answers with the name typed in the folder shown, the extension added if it is left out, asking first
// when that would replace a file.
func (a *App) pickSaveHere(pk *filePick) {
	u := &a.u
	p := pk.fd.pane()
	name := strings.TrimSpace(pk.name)
	if name == "" || a.noDrives(p) {
		return
	}
	if strings.ContainsAny(name, `<>:"/\|?*`) {
		a.toastOnly("pick/name", u.t(`A file name cannot hold any of < > : " / \ | ? *`))
		return
	}
	if len(pk.exts) > 0 && !slices.ContainsFunc(pk.exts, func(e string) bool { return strings.EqualFold(filepath.Ext(name), e) }) {
		name += pk.exts[0]
	}
	if p.search != nil {
		a.closeSearch(p)
	}
	to := filepath.Join(p.dir, name)
	switch info, err := os.Stat(to); {
	case err != nil:
		a.pickFinish(pk, []string{to})
	case info.IsDir():
		a.goTo(p, to, "")
	default:
		pk.replace = to
	}
}

// pickSystem takes the answer from the system's own dialog; saving, the folder chosen there is shown here, where
// the file is named.
func (a *App) pickSystem(pk *filePick) {
	switch pk.mode {
	case pickFolder:
		if dir := a.host.ChooseFolder(pk.title); dir != "" {
			a.pickFinish(pk, []string{dir})
		}
	case pickSave:
		if dir := a.host.ChooseFolder(pk.title); dir != "" {
			a.goTo(pk.fd.pane(), dir, "")
		}
	default:
		filter := [][2]string{{a.u.t("All files"), "*.*"}}
		if len(pk.exts) > 0 {
			filter = slices.Insert(filter, 0, [2]string{pk.kinds, extPattern(pk.exts)})
		}
		if paths := a.host.OpenFiles(pk.title, filter, pk.multiple); len(paths) > 0 {
			a.pickFinish(pk, paths)
		}
	}
}

// pickShowAll shows the files of every kind, or only those asked for.
func (a *App) pickShowAll(pk *filePick, all bool) {
	if pk.all == all {
		return
	}
	pk.all = all
	p := pk.fd.pane()
	p.g.countsKey, p.g.layoutKey = "", ""
	a.arrange(p)
}

// pickDropped goes to a file or folder dropped on the dialog.
func (a *App) pickDropped(paths []string) {
	if pk := a.filePicker; len(paths) > 0 {
		a.goToTyped(pk.fd.pane(), paths[0])
	}
}

// pickKeys takes the keys that answer the dialog: those of the question whether to replace a file, and Enter and Esc
// in the box a name is typed in.
func (a *App) pickKeys(pk *filePick) {
	u := &a.u
	if pk.replace != "" {
		for _, k := range u.keys {
			switch {
			case u.repeated(k):
			case k.Code == keyEnter:
				a.pickFinish(pk, []string{pk.replace})
			case k.Code == keyEscape:
				pk.replace = ""
			}
		}
		u.keys, u.typed = nil, nil
		return
	}
	if u.focus != pickNameID {
		return
	}
	var rest []Key
	for _, k := range u.keys {
		switch k.Code {
		case keyEnter:
			a.pickConfirm(pk)
		case keyEscape:
			pk.pickCancel()
		default:
			rest = append(rest, k)
		}
	}
	u.keys = rest
}

// pickLayout places the dialog over the window: what it asks for above, the list, and the row that answers it.
func (a *App) pickLayout() (box, head, list, foot gfx.Rect) {
	u := &a.u
	box = a.screen.Inset(min(u.px(32), min(a.screen.W, a.screen.H)*0.03))
	head = rectAt(box.X, box.Y, box.W, u.px(44))
	foot = rectAt(box.X, box.Bottom()-u.px(56), box.W, u.px(56))
	list = rectAt(box.X, head.Bottom(), box.W, foot.Y-head.Bottom())
	return box, head, list, foot
}

// drawPickDialog draws the dialog: what it asks for above, the file manager's list, and the row that answers it.
func (a *App) drawPickDialog(pk *filePick) {
	u := &a.u
	u.c.Fill(a.screen, gfx.Alpha(colBlack, 0.5))
	box, head, list, foot := a.pickLayout()
	panel(u, box)
	u.c.Fill(head, colBackground)
	u.c.Fill(foot, colBackground)
	u.label(u.bold(14.5), rectAt(head.X+u.px(16), head.Y, head.W-u.px(64), head.H), pk.title, colText, 0)
	size := u.px(28)
	if u.iconButton("pick/close", rectAt(head.Right()-u.px(8)-size, head.Y+(head.H-size)/2, size, size), icClose, u.t("Cancel")+"  Esc", false, pk.replace == "") {
		pk.pickCancel()
	}
	if !pk.multiple {
		clear(pk.fd.pane().marked)
	}
	a.pickKeys(pk)
	asking := pk.replace != ""
	u.blocked = u.blocked || asking
	u.c.Push(list)
	a.drawFiles(list)
	u.c.Pop()
	a.drawPickFoot(pk, foot)
	if asking {
		u.blocked = false
		a.drawPickReplace(pk)
	}
}

// drawPickFoot draws the row that answers the dialog: the way to the system's dialog and the switch to all files on
// the left, what is chosen or the name typed in the middle, and the buttons on the right.
func (a *App) drawPickFoot(pk *filePick, r gfx.Rect) {
	u := &a.u
	h := u.px(34)
	y := r.Y + (r.H-h)/2
	x := r.X + u.px(16)
	system := u.t("From the system's file manager…")
	sw := u.buttonWidth(icFolder, system)
	if u.button("pick/system", rectAt(x, y, sw, h), icFolder, system, ghost, true) {
		a.pickSystem(pk)
	}
	x += sw + u.px(10)
	if pk.mode != pickFolder && len(pk.exts) > 0 {
		names := []string{pk.kinds, u.t("All files")}
		f := u.face(12.5)
		w := 2 * (max(f.Measure(names[0]), f.Measure(names[1])) + u.px(24))
		was := 0
		if pk.all {
			was = 1
		}
		if now := u.chips("pick/kinds", rectAt(x, y, w, h), names, was, true); now != was {
			a.pickShowAll(pk, now == 1)
		}
		x += w + u.px(10)
	}

	right := r.Right() - u.px(16)
	cancel := u.t("Cancel") + "  Esc"
	cw := u.buttonWidth("", cancel)
	right -= cw
	if u.button("pick/cancel", rectAt(right, y, cw, h), "", cancel, ghost, true) {
		pk.pickCancel()
	}
	p := pk.fd.pane()
	label, ok := u.t("Open"), p.current() != nil || p.search != nil
	switch pk.mode {
	case pickFolder:
		label, ok = u.t("Choose this folder"), !p.atDrives() || p.current() != nil
	case pickSave:
		label, ok = u.t("Save"), strings.TrimSpace(pk.name) != "" && !p.atDrives()
	}
	bw := u.buttonWidth("", label) + u.px(24)
	right -= bw + u.px(8)
	if u.button("pick/confirm", rectAt(right, y, bw, h), "", label, primary, ok) {
		a.pickConfirm(pk)
	}

	mid := rectAt(x, y, right-u.px(10)-x, h)
	if mid.W <= 0 {
		return
	}
	switch pk.mode {
	case pickSave:
		u.textBox(pickNameID, mid, &pk.name, u.t("File name"), false)
	case pickFolder:
		u.label(u.face(12.5), mid, ellipsizeStart(u.face(12.5), p.dir, mid.W), colDim, 0)
	default:
		chosen := pk.pickFilesChosen()
		text := ""
		switch len(chosen) {
		case 0:
		case 1:
			text = filepath.Base(chosen[0])
		default:
			text = u.tf("%d files", len(chosen))
		}
		u.label(u.face(12.5), mid, u.face(12.5).Ellipsize(text, mid.W), colDim, 0)
	}
}

// drawPickReplace asks whether to replace the file that has the name given.
func (a *App) drawPickReplace(pk *filePick) {
	u := &a.u
	u.c.Fill(a.screen, gfx.Alpha(colBlack, 0.45))
	box := rectCentered(a.screen, min(u.px(460), a.screen.W-u.px(32)), u.px(146))
	panel(u, box)
	in := box.Inset(u.px(20))
	u.label(u.bold(15), rectAt(in.X, in.Y, in.W, u.px(26)), u.tf("Replace %s?", filepath.Base(pk.replace)), colText, 0)
	u.label(u.face(12.5), rectAt(in.X, in.Y+u.px(30), in.W, u.px(22)), u.t("A file of that name is in this folder; it will be written over."), colDim, 0)
	by := box.Bottom() - u.px(20) - u.px(34)
	cancel := u.t("Cancel") + "  Esc"
	cw := u.buttonWidth("", cancel)
	if u.button("pick/replace/no", rectAt(in.Right()-cw, by, cw, u.px(34)), "", cancel, ghost, true) {
		pk.replace = ""
		return
	}
	yes := u.t("Replace") + "  Enter"
	yw := u.buttonWidth("", yes)
	if u.button("pick/replace/yes", rectAt(in.Right()-cw-u.px(8)-yw, by, yw, u.px(34)), "", yes, danger, true) {
		a.pickFinish(pk, []string{pk.replace})
	}
}

// pickCommands are the file manager's commands the dialog keeps, those that find a file; what opens or changes files
// belongs to the file manager.
var pickCommands = map[string]bool{
	"files.down": true, "files.up": true, "files.parent": true, "files.enter": true, "files.left": true, "files.right": true,
	"files.top": true, "files.bottom": true, "files.page-down": true, "files.page-up": true, "files.preview": true,
	"files.view": true, "files.filter": true, "files.new": true, "files.jump": true, "files.rename": true, "files.path": true,
	"files.find": true, "files.grep": true, "files.hidden": true, "files.sort-size": true, "files.sort-name": true,
	"files.sort-items": true, "files.sort-modified": true, "files.refresh": true, "files.escape": true,
}

// keeps reports whether the dialog offers the file manager command id.
func (pk *filePick) keeps(id string) bool {
	return pickCommands[id] || pk.multiple && (id == "files.mark" || id == "files.mark-all")
}
