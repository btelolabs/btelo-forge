package ui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/assets"
	"github.com/btelolabs/btelo-forge/internal/files"
	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// moveCursor moves the cursor by rows, keeping it in view.
func (a *App) moveCursor(fd *filesDoc, by int) {
	p := fd.pane()
	if p.search != nil {
		p.search.move(by)
		return
	}
	if len(p.rows) == 0 {
		return
	}
	p.cursor = min(max(p.cursor+by, 0), len(p.rows)-1)
	a.keepInView(fd, p)
}

// moveRows moves the cursor down (by > 0) or up a number of rows: of a list, or of cards in a grid.
func (a *App) moveRows(fd *filesDoc, by int) {
	if p := fd.pane(); p.grid && p.search == nil {
		a.gridCursorRow(fd, p, by)
		return
	}
	a.moveCursor(fd, by)
}

// pageBy moves the cursor a page down (dir 1) or up (-1): a list's rows, or the rows of cards a grid shows.
func (a *App) pageBy(fd *filesDoc, dir int) {
	if p := fd.pane(); p.grid && p.search == nil {
		a.gridCursorRow(fd, p, dir*a.gridPageRows(p))
		return
	}
	a.moveCursor(fd, dir*max(fd.pageRows-1, 1))
}

// sideways is what ← and → do: in a grid, go to the card before or after; in a list, go up a folder or into one.
func (a *App) sideways(fd *filesDoc, dir int) {
	if p := fd.pane(); p.grid && p.search == nil {
		a.moveCursor(fd, dir)
		return
	}
	if dir < 0 {
		a.goUp(fd.pane())
	} else {
		a.enter(fd)
	}
}

// keepInView scrolls a list so its cursor shows.
func (a *App) keepInView(fd *filesDoc, p *filesPane) {
	if p.grid {
		a.revealCard(p)
		return
	}
	rows := float32(max(fd.pageRows, 1))
	if c := float32(p.cursor); c < p.top {
		p.top = c
	} else if c >= p.top+rows {
		p.top = c - rows + 1
	}
}

// goUp goes to the folder above, the cursor on the folder it came from.
func (a *App) goUp(p *filesPane) {
	if p.search != nil {
		return
	}
	if p.atDrives() {
		return
	}
	parent := filepath.Dir(p.dir)
	if parent == p.dir {
		// Above a drive's own folder are the drives, where there are any.
		if drives := a.knownDrives(); len(drives) > 0 {
			a.goTo(p, thisPC, driveNameFor(drives, p.dir))
		}
		return
	}
	a.goTo(p, parent, filepath.Base(p.dir))
}

// enter goes into the folder under the cursor, or opens the file in a tab of its own when the app works on it (a
// video, an edit, a canvas, a picture) and previews it otherwise; in a search, it opens what was found the same way.
func (a *App) enter(fd *filesDoc) {
	if fd.pick != nil {
		a.pickEnter(fd)
		return
	}
	p := fd.pane()
	if p.search != nil {
		a.openChosen(p)
		return
	}
	e := p.current()
	if e == nil {
		return
	}
	if e.Dir {
		a.goTo(p, p.path(e), "")
		return
	}
	path := p.path(e)
	if a.busyJob(path) != nil {
		return
	}
	if opensInApp(assets.KindOf(e.Name)) {
		a.openAsset(path)
		return
	}
	fd.preview = true
}

// togglePreview opens the preview of the item under the cursor over the lists, or closes it.
func (a *App) togglePreview(fd *filesDoc) {
	if !fd.preview && a.previewPath(fd) == "" {
		return
	}
	fd.preview = !fd.preview
	fd.previewFocus = false
	a.endPeek()
	if !fd.preview {
		a.closeFilePreview()
	}
}

// previewPath is the file the preview shows: the one under the cursor, or the one a search found.
func (a *App) previewPath(fd *filesDoc) string {
	p := fd.pane()
	if p.search != nil {
		if path, _ := p.search.chosen(); path != "" {
			return path
		}
		return ""
	}
	if e := p.current(); e != nil && a.busyJob(p.path(e)) == nil {
		return p.path(e)
	}
	return ""
}

// previewEntry is the item the preview shows, as its list read it, or as a search found it and it was read since,
// in the background: whether it is a folder is known at once either way.
func (a *App) previewEntry(fd *filesDoc) (string, files.Entry) {
	path := a.previewPath(fd)
	if path == "" {
		return "", files.Entry{}
	}
	p := fd.pane()
	if p.search == nil {
		return path, *p.current()
	}
	e := files.Entry{Name: filepath.Base(path), Dir: p.search.chosenDir()}
	if st, ok := a.statOf(path); ok && st.Err == nil {
		e = st.Entry
	}
	return path, e
}

// drawPreviewOverlay shows the item under the cursor over the whole tab, with beside it, or under it in a narrow
// window, its details, tags and categories. A click in the preview gives it the keys; a click outside closes it.
func (a *App) drawPreviewOverlay(fd *filesDoc, r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, gfxShade)
	margin := min(u.px(28), r.W/16, r.H/16)
	card := r.Inset(margin)
	u.c.Outline(card.Inset(-u.px(2)), u.px(6), colShadow)
	u.c.Fill(card, colBackground)
	u.c.Outline(card, max(1, u.px(1)), colBorderHi)
	path, entry := a.previewEntry(fd)

	headH := u.px(42)
	head := rectAt(card.X, card.Y, card.W, headH)
	u.c.Fill(rectAt(head.X, head.Bottom()-max(1, u.px(1)), head.W, max(1, u.px(1))), colBorder)
	size := u.px(28)
	closeAt := rectAt(head.Right()-u.px(8)-size, head.Y+(headH-size)/2, size, size)
	if path != "" {
		u.label(u.bold(13), rectAt(head.X+u.px(16), head.Y, closeAt.X-head.X-u.px(24), headH), a.previewHeading(path, entry), colText, 0)
	}
	inner := rectAt(card.X, head.Bottom(), card.W, card.Bottom()-head.Bottom())
	var details gfx.Rect
	if path != "" && !fd.pane().atDrives() {
		line := max(1, u.px(1))
		if inner.W >= u.px(720) {
			dw := min(inner.W*0.36, u.px(340))
			details = rectAt(inner.Right()-dw, inner.Y, dw, inner.H)
			inner.W -= dw
			u.c.Fill(rectAt(details.X-line, details.Y, line, details.H), colBorder)
		} else {
			dh := inner.H * 0.42
			details = rectAt(inner.X, inner.Bottom()-dh, inner.W, dh)
			inner.H -= dh
			u.c.Fill(rectAt(details.X, details.Y-line, details.W, line), colBorder)
		}
	}
	defer func() {
		if u.iconButton("files/preview/close", closeAt, icClose, withKey(u.t("Close"), a.fileKeyHint("preview")), false, true) {
			a.togglePreview(fd)
		} else if !card.Contains(u.pressX, u.pressY) && u.clicked("files/preview/away", r) {
			a.togglePreview(fd)
		}
	}()
	if u.pressed && inner.Contains(u.pressX, u.pressY) && !u.blocked {
		fd.previewFocus = true
	} else if u.pressed && !inner.Contains(u.pressX, u.pressY) {
		fd.previewFocus = false
	}
	if path == "" {
		u.label(u.face(13), inner, u.t("Nothing to preview"), colFaint, 0.5)
		return
	}
	if details.W > 0 {
		a.drawFileDetails(fd, details)
	}
	if entry.Dir {
		a.drawFolderPreview(path, inner)
		return
	}
	a.drawFilePreview("files/preview", inner, path)
	if fd.previewFocus {
		u.c.Outline(inner, u.px(1.5), gfx.Alpha(colAccent, 0.6))
	}
}

// previewHeading is the line above the preview: the item's name, and what is known of it.
func (a *App) previewHeading(path string, e files.Entry) string {
	if e.Dir {
		return filepath.Base(path)
	}
	_, info, _ := a.fileThumb(path)
	return previewTitle(path, info, assets.KindOf(path), e.Size)
}

// drawFolderPreview shows what a folder holds, as its first names, and its size when it has been analysed.
func (a *App) drawFolderPreview(dir string, r gfx.Rect) {
	u := &a.u
	f := u.face(12.5)
	y := r.Y + u.px(16)
	u.label(u.bold(14), rectAt(r.X+u.px(16), y, r.W-u.px(32), u.px(22)), filepath.Base(dir), colText, 0)
	y += u.px(26)
	if n := a.analysis(dir); n != nil {
		u.label(f, rectAt(r.X+u.px(16), y, r.W-u.px(32), u.px(20)), u.tf("%s in %s", sizeText(n.Size), itemCount(u, n.Items)), colDim, 0)
		y += u.px(24)
	}
	l, known := a.listingOf(dir)
	switch {
	case !known:
		u.label(f, rectAt(r.X+u.px(16), y, r.W-u.px(32), u.px(20)), u.t("Loading the preview…"), colFaint, 0)
		return
	case l.err != nil:
		u.label(f, rectAt(r.X+u.px(16), y, r.W-u.px(32), u.px(20)), u.tf("This folder cannot be read: %v", l.err), colDangerText, 0)
		return
	}
	for _, e := range l.entries {
		if y+u.px(22) > r.Bottom() {
			break
		}
		if a.settings.Files.HideHidden && e.Hidden {
			continue
		}
		glyph, col := fileGlyph(&e)
		u.icon(glyph, rectAt(r.X+u.px(16), y, u.px(18), u.px(22)), 14, col)
		u.label(f, rectAt(r.X+u.px(42), y, r.W-u.px(58), u.px(22)), e.Name, colDim, 0)
		y += u.px(22)
	}
}

// toggleMark marks or unmarks the item under the cursor and moves down, as a list of marks is made.
func (a *App) toggleMark(fd *filesDoc) {
	p := fd.pane()
	if p.search != nil || p.current() == nil {
		return
	}
	a.toggleMarkAt(p, p.cursor)
	a.moveCursor(fd, 1)
}

func (a *App) toggleMarkAt(p *filesPane, row int) {
	if row < 0 || row >= len(p.rows) {
		return
	}
	name := p.entries[p.rows[row]].Name
	if p.marked[name] {
		delete(p.marked, name)
	} else {
		p.marked[name] = true
	}
}

// markAll marks every item shown, or unmarks them all when they all are.
func (a *App) markAll(fd *filesDoc) {
	p := fd.pane()
	if p.search != nil {
		return
	}
	all := len(p.marked) == len(p.rows)
	p.marked = map[string]bool{}
	if !all {
		for _, r := range p.rows {
			p.marked[p.entries[r].Name] = true
		}
	}
}

// filesMaxPanes is as many lists as fit side by side and stay readable.
const filesMaxPanes = 6

// splitPane opens another list on the right of this one, at the same folder.
func (a *App) splitPane(fd *filesDoc) {
	if len(fd.panes) >= filesMaxPanes {
		a.toastOnly("files/panes", a.u.tf("At most %d lists fit side by side", filesMaxPanes))
		return
	}
	from := fd.pane()
	p := &filesPane{dir: from.dir, marked: map[string]bool{}, sort: from.sort, desc: from.desc, sorted: from.sorted}
	if from.grid {
		p.g.size = from.g.size
		a.showGrid(p)
	}
	a.readPane(p)
	fd.panes = slices.Insert(fd.panes, fd.at+1, p)
	a.focusPane(fd, fd.at+1)
}

// closePane closes the list in use; the last one stays.
func (a *App) closePane(fd *filesDoc) {
	if len(fd.panes) == 1 {
		return
	}
	a.closeSearch(fd.panes[fd.at])
	fd.panes[fd.at].gen++ // a listing still being read is dropped
	fd.panes = slices.Delete(fd.panes, fd.at, fd.at+1)
	a.focusPane(fd, min(fd.at, len(fd.panes)-1))
}

// focusPane makes list i the one the keys act on; it goes round from one end to the other.
func (a *App) focusPane(fd *filesDoc, i int) {
	n := len(fd.panes)
	fd.at = (i%n + n) % n
	if fd.prompt != nil && fd.prompt.kind == promptRename {
		fd.prompt = nil
		a.u.focus = ""
	}
}

// clipFiles marks the marked items, or the one under the cursor, to be moved or copied where v is pressed next.
// Nothing happens to the files until then.
func (a *App) clipFiles(fd *filesDoc, cut bool) {
	p := fd.pane()
	if a.noDrives(p) {
		return
	}
	if p.search != nil {
		return
	}
	paths := p.selected()
	if len(paths) == 0 {
		return
	}
	if job := a.busyAny(paths); job != nil {
		a.toastOnly("files/busy", a.u.tf("%s is being worked on: %s", filepath.Base(paths[0]), job.title))
		return
	}
	a.fm.clip = fileClip{paths: paths, cut: cut}
	p.marked = map[string]bool{}
	if !cut {
		// Copied files can be pasted in other programs too, as in a chat or a mail; where the system cannot take
		// them, v still pastes them here.
		if ops, err := a.fileOps(); err == nil {
			ops.SetClipboardFiles(paths)
		}
	}
	if cut {
		a.toastOnly("files/clip", a.u.tf("%d marked to move: go to another folder and press v to move them there", len(paths)))
	} else {
		a.toastOnly("files/clip", a.u.tf("%d copied: go to another folder and press v to copy them there", len(paths)))
	}
}

// inClip reports whether a path was cut or copied.
func (a *App) inClip(path string) bool {
	return slices.ContainsFunc(a.fm.clip.paths, func(c string) bool { return files.SamePath(c, path) })
}

// pasteFiles copies or moves what was cut or copied into the list's folder; with nothing cut or copied here, it
// copies the files copied in another program, such as Explorer.
func (a *App) pasteFiles(fd *filesDoc) {
	p := fd.pane()
	if a.noDrives(p) {
		return
	}
	if p.search != nil {
		return
	}
	dst := p.dir
	if job := a.busyJob(dst); job != nil {
		a.toastOnly("files/busy", a.u.tf("%s is being worked on: %s", filepath.Base(dst), job.title))
		return
	}
	clip := a.fm.clip
	if len(clip.paths) == 0 {
		_, pasted, _ := a.host.Paste()
		if len(pasted) == 0 {
			a.toastOnly("files/clip", a.u.t("Nothing was marked to move or copied: press x or c on a file first"))
			return
		}
		clip = fileClip{paths: pasted}
	}
	if job := a.busyAny(clip.paths); job != nil {
		a.toastOnly("files/busy", a.u.tf("%s is being worked on: %s", filepath.Base(clip.paths[0]), job.title))
		return
	}
	for _, src := range clip.paths {
		if files.Within(dst, src) {
			a.toast(a.u.tf("%s cannot go into itself", filepath.Base(src)), true)
			return
		}
	}
	srcs := slices.Clone(clip.paths)
	if clip.cut {
		a.fm.clip = fileClip{}
		a.startJob(jobMove, srcs, dst)
		return
	}
	a.startJob(jobCopy, srcs, dst)
}

// copyPaths puts the paths of the marked items, or the one under the cursor, on the clipboard, a line each.
func (a *App) copyPaths(fd *filesDoc) {
	paths := fd.pane().selected()
	if p := fd.pane(); p.search != nil {
		path, _ := p.search.chosen()
		paths = []string{path}
		if path == "" {
			return
		}
	}
	if len(paths) == 0 {
		paths = []string{fd.pane().dir}
	}
	a.copyPathList(paths)
}

// copyPathList puts paths on the clipboard, one to a line, and says so.
func (a *App) copyPathList(paths []string) {
	if err := a.host.SetClipboard(strings.Join(paths, "\n")); err != nil {
		a.toast(a.u.tf("Copying failed: %v", err), true)
		return
	}
	if len(paths) == 1 {
		a.toastOnly("files/path", a.u.tf("Copied %s", paths[0]))
	} else {
		a.toastOnly("files/path", a.u.tf("Copied %d paths", len(paths)))
	}
}

// revealFolder opens the list's folder in the system's file manager; in the list of drives, the drive under the
// cursor.
func (a *App) revealFolder(fd *filesDoc) {
	p := fd.pane()
	dir := p.dir
	if p.atDrives() {
		e := p.current()
		if e == nil {
			return
		}
		dir = p.path(e)
	}
	if err := a.host.Launch(dir); err != nil {
		a.toast(a.u.tf("The folder could not be opened: %v", err), true)
	}
}

// openDefault opens the item under the cursor with the program the system opens it with.
func (a *App) openDefault(fd *filesDoc) {
	path := a.previewPath(fd)
	if path == "" {
		return
	}
	if err := a.host.Launch(path); err != nil {
		a.toast(a.u.tf("%s could not be opened: %v", filepath.Base(path), err), true)
	}
}

// toggleHidden shows or hides hidden files in every list.
func (a *App) toggleHidden() {
	a.settings.Files.HideHidden = !a.settings.Files.HideHidden
	a.saveSettings()
	for _, fd := range a.filesDocs() {
		for _, p := range fd.panes {
			a.arrange(p)
		}
	}
	if a.settings.Files.HideHidden {
		a.toastOnly("files/hidden", a.u.t("Hidden files are hidden"))
	} else {
		a.toastOnly("files/hidden", a.u.t("Hidden files are shown"))
	}
}

// setSort orders a list as chosen, starting at the top of the new order.
func (a *App) setSort(p *filesPane, s filesSort, desc bool) {
	p.sort, p.desc, p.sorted = s, desc, true
	a.arrange(p)
	p.cursor, p.top, p.g.scroll = 0, 0, 0
}

// sortBy orders a list; choosing the order it already has turns it round, as gdu does.
func (a *App) sortBy(p *filesPane, s filesSort) {
	desc := false
	if p.sort == s {
		desc = !p.desc
	}
	a.setSort(p, s, desc)
}

// escapeFiles closes the preview, or leaves a search, or unmarks, or shows a filtered grid whole again, or forgets
// what was cut or copied.
func (a *App) escapeFiles(fd *filesDoc) {
	p := fd.pane()
	switch {
	case fd.preview:
		a.togglePreview(fd)
	case p.search != nil:
		a.closeSearch(p)
	case len(p.marked) > 0:
		p.marked = map[string]bool{}
	case p.grid && (p.g.filter.what != showAll || p.g.search != ""):
		p.g.search = ""
		a.setFilter(p, assetFilter{})
	case fd.pick != nil:
		fd.pick.pickCancel()
	case len(a.fm.clip.paths) > 0:
		a.fm.clip = fileClip{}
	}
}

// fileMenu is the menu the right button held on an item opens, at at.
func (a *App) fileMenu(fd *filesDoc, at gfx.Rect) {
	u := &a.u
	e := fd.pane().current()
	if e == nil {
		return
	}
	hint := a.fileKeyHint
	item := func(label, id string, run func()) menuItem { return menuItem{label: label, key: hint(id), run: run} }
	open := item(u.t("Open"), "enter", func() { a.enter(fd) })
	items := []menuItem{
		open,
		item(u.t("Open with the default program"), "open", func() { a.openDefault(fd) }),
	}
	if e.Dir {
		items = append(items, item(u.t("Analyse"), "analyse", func() { a.goTo(fd.pane(), fd.pane().path(e), ""); a.analyse(fd.pane()) }))
	} else {
		items = append(items, item(u.t("Preview"), "preview", func() { fd.preview, fd.previewFocus = true, false }))
	}
	items = append(items, menuSep,
		item(u.t("Mark to move"), "cut", func() { a.clipFiles(fd, true) }),
		item(u.t("Copy"), "copy", func() { a.clipFiles(fd, false) }),
		menuItem{label: u.t("Paste"), key: hint("paste"), run: func() { a.pasteFiles(fd) }, disabled: len(a.fm.clip.paths) == 0},
		item(u.t("Copy the path"), "copy-path", func() { a.copyPaths(fd) }),
		menuSep,
	)
	if p := fd.pane(); !p.atDrives() {
		items = append(items, item(u.t("Add a tag"), "tag", func() { a.openPrompt(fd, promptTag) }),
			menuItem{label: u.t("Categories"), sub: a.categoryItems(fd, p.selected())}, menuSep)
	}
	items = append(items,
		item(u.t("Rename"), "rename", func() { a.openPrompt(fd, promptRename) }),
		item(u.t("Delete"), "delete", func() { a.askDelete(fd, false) }),
		item(u.t("Delete for good"), "delete-forever", func() { a.askDelete(fd, true) }),
		menuSep,
		item(u.t("Show the folder in the system's file manager"), "explorer", func() { a.revealFolder(fd) }),
	)
	a.openMenu("files/menu", at, items)
}

// The work a file job does.
type jobKind int

const (
	jobCopy jobKind = iota
	jobMove
	jobRecycle
	jobDelete
	jobUndo
	jobCompress
	jobExtract
)

// fileJob is a copy, move, deletion or undo running in the background. The items it works on are busy until it
// ends: they cannot be entered or acted on, and their rows say what is happening.
type fileJob struct {
	kind     jobKind
	title    string
	paths    []string // the items worked on: busy until the job ends
	dst      string
	progress files.Progress
	cancel   context.CancelFunc
	started  time.Time
	sizes    map[string]int64 // the length of each file deleted or moved away, to take off the analyses
}

// state is what a busy row says.
func (j *fileJob) state(u *ctx) string {
	switch j.kind {
	case jobCopy:
		return u.t("Copying…")
	case jobMove:
		return u.t("Moving…")
	case jobUndo:
		return u.t("Undoing…")
	case jobCompress:
		return u.t("Compressing…")
	case jobExtract:
		return u.t("Extracting…")
	}
	return u.t("Deleting…")
}

// busyJob is the job working on path or a folder it is in, or nil.
func (a *App) busyJob(path string) *fileJob {
	for _, j := range a.fm.jobs {
		for _, p := range j.paths {
			if files.Within(path, p) {
				return j
			}
		}
	}
	return nil
}

// busyAny is the job working on any of paths, or on anything inside them, or nil.
func (a *App) busyAny(paths []string) *fileJob {
	for _, path := range paths {
		if j := a.busyJob(path); j != nil {
			return j
		}
		for _, j := range a.fm.jobs {
			for _, p := range j.paths {
				if files.Within(p, path) {
					return j
				}
			}
		}
	}
	return nil
}

// startJob starts copying, moving, deleting, compressing or extracting paths in the background; dst is the folder
// copied or moved to, or the archive made.
func (a *App) startJob(kind jobKind, paths []string, dst string) {
	u := &a.u
	n := len(paths)
	name := filepath.Base(paths[0])
	j := &fileJob{kind: kind, paths: paths, dst: dst, started: time.Now(), sizes: map[string]int64{}}
	switch kind {
	case jobCopy:
		j.title = u.tf("Copying %s to %s", itemsText(u, n, name), filepath.Base(dst))
		j.paths = nil // the originals stay free to use
	case jobMove:
		j.title = u.tf("Moving %s to %s", itemsText(u, n, name), filepath.Base(dst))
	case jobRecycle:
		j.title = u.tf("Moving %s to the Recycle Bin", itemsText(u, n, name))
	case jobDelete:
		j.title = u.tf("Deleting %s for good", itemsText(u, n, name))
	case jobCompress:
		j.title = u.tf("Compressing %s into %s", itemsText(u, n, name), filepath.Base(dst))
		j.dst = filepath.Dir(dst)
	case jobExtract:
		j.title = u.tf("Extracting %s", itemsText(u, n, name))
		j.dst = filepath.Dir(paths[0])
	}
	ctx, cancel := context.WithCancel(a.life)
	j.cancel = cancel
	a.fm.jobs = append(a.fm.jobs, j)
	a.background(func(context.Context) {
		sizes := fileSizes(paths)
		if kind == jobCompress {
			// The name was chosen from the listing, which may be older than the folder.
			dst = filepath.Join(filepath.Dir(dst), files.UniqueName(filepath.Dir(dst), filepath.Base(dst)))
		}
		var res files.Result
		var trashed []files.Trashed
		switch kind {
		case jobCopy:
			res = files.Copy(ctx, paths, dst, &j.progress)
		case jobMove:
			res = files.Move(ctx, paths, dst, &j.progress)
		case jobRecycle:
			trashed, res = files.TrashAll(ctx, paths, &j.progress)
		case jobDelete:
			res = files.Remove(ctx, paths, &j.progress)
		case jobCompress:
			if err := files.Compress(ctx, paths, dst, &j.progress); err != nil {
				res.Failed = append(res.Failed, files.Failure{Path: dst, Err: err})
			} else {
				res.Pairs = []files.Pair{{From: paths[0], To: dst}}
			}
		case jobExtract:
			if to, err := files.Extract(ctx, paths[0], &j.progress); err != nil {
				res.Failed = append(res.Failed, files.Failure{Path: paths[0], Err: err})
			} else {
				res.Pairs = []files.Pair{{From: paths[0], To: to}}
			}
		}
		added := scanAdded(ctx, res.Pairs)
		a.post(func() {
			j.sizes = sizes
			a.jobDone(j, res, trashed, added, ctx.Err() != nil)
		})
	})
}

// compressFiles packs the marked items, or the one under the cursor, into a zip file in the list's folder, named
// after the item, or after the folder when there are several.
func (a *App) compressFiles(fd *filesDoc) {
	p := fd.pane()
	if a.noDrives(p) {
		return
	}
	if p.search != nil {
		return
	}
	paths := p.selected()
	if len(paths) == 0 {
		return
	}
	if job := a.busyAny(paths); job != nil {
		a.toastOnly("files/busy", a.u.tf("%s is being worked on: %s", filepath.Base(paths[0]), job.title))
		return
	}
	stem := filepath.Base(p.dir)
	if es := p.selectedEntries(); len(es) == 1 {
		stem = es[0].Name
		if !es[0].Dir {
			stem = strings.TrimSuffix(stem, filepath.Ext(stem))
		}
	}
	taken := map[string]bool{}
	for _, e := range p.entries {
		taken[strings.ToLower(e.Name)] = true
	}
	dst := filepath.Join(p.dir, files.UniqueNameBy(stem+".zip", func(n string) bool { return taken[strings.ToLower(n)] }))
	p.marked = map[string]bool{}
	p.land = filepath.Base(dst)
	a.startJob(jobCompress, paths, dst)
}

// extractFile unpacks the archive under the cursor beside it.
func (a *App) extractFile(fd *filesDoc) {
	p := fd.pane()
	if a.noDrives(p) {
		return
	}
	e := p.current()
	if p.search != nil || e == nil {
		return
	}
	if e.Dir || !files.IsArchive(e.Name) {
		a.toastOnly("files/extract", a.u.t("Put the cursor on an archive, such as a zip, 7z, rar or tar.gz file, to extract it"))
		return
	}
	path := p.path(e)
	if job := a.busyJob(path); job != nil {
		a.toastOnly("files/busy", a.u.tf("%s is being worked on: %s", e.Name, job.title))
		return
	}
	a.startJob(jobExtract, []string{path}, "")
}

// itemsText names what a job works on: the item, or how many.
func itemsText(u *ctx, n int, name string) string {
	if n == 1 {
		return "“" + name + "”"
	}
	return u.tf("%d items", n)
}

// added is a folder or file a job put somewhere, analysed so it can be added to an analysis it went into.
type added struct {
	path  string
	node  *files.Node // a folder's analysis
	bytes int64       // a file's length
}

// fileSizes are the lengths of the files among paths, links and folders left out, read before an operation moves or
// removes them, for what it takes off the analyses; it runs in the background.
func fileSizes(paths []string) map[string]int64 {
	sizes := map[string]int64{}
	for _, p := range paths {
		if info, err := os.Lstat(p); err == nil && info.Mode().IsRegular() {
			sizes[p] = info.Size()
		}
	}
	return sizes
}

// scanAdded analyses what was copied or moved to, for the analyses of the folders it went into.
func scanAdded(ctx context.Context, pairs []files.Pair) []added {
	var out []added
	for _, p := range pairs {
		if p.To == "" {
			continue
		}
		info, err := os.Lstat(p.To)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			out = append(out, added{path: p.To, bytes: info.Size()})
			continue
		}
		if n, err := files.Scan(ctx, p.To, nil); err == nil {
			n.Name = filepath.Base(p.To)
			out = append(out, added{path: p.To, node: n})
		}
	}
	return out
}

// jobDone ends a job on the UI goroutine: the analyses and lists take in what changed, the work can be undone, and
// what failed is told.
func (a *App) jobDone(j *fileJob, res files.Result, trashed []files.Trashed, add []added, cancelled bool) {
	u := &a.u
	a.fm.jobs = slices.DeleteFunc(a.fm.jobs, func(x *fileJob) bool { return x == j })
	dirs := map[string]bool{}
	if j.dst != "" {
		dirs[j.dst] = true
	}
	// What left a folder comes off its analysis; what came into one goes on.
	for _, pair := range res.Pairs {
		if j.kind == jobCopy || j.kind == jobCompress || j.kind == jobExtract {
			continue
		}
		dirs[filepath.Dir(pair.From)] = true
		a.takeFromAnalyses(pair.From, j.sizes[pair.From])
	}
	for _, ad := range add {
		a.addToAnalyses(ad)
	}
	var dirList []string
	for d := range dirs {
		dirList = append(dirList, d)
	}
	a.refreshPanes(dirList)
	a.fm.git = nil // what git counts may have changed

	// Tags and categories follow what moved, and go with what was deleted; what went to the Recycle Bin gets them
	// back when it is undone.
	lib := a.assetLibrary()
	var labels []assets.Item
	switch j.kind {
	case jobMove:
		for _, pair := range res.Pairs {
			a.libraryErr(lib.Move(pair.From, pair.To))
		}
	case jobRecycle, jobDelete:
		var gone []string
		for _, pair := range res.Pairs {
			gone = append(gone, pair.From)
		}
		var err error
		labels, err = lib.Take(gone)
		a.libraryErr(err)
	}
	a.assetLibVersion++

	switch j.kind {
	case jobCopy, jobMove, jobCompress, jobExtract:
		if len(res.Pairs) > 0 {
			kind := files.Copied // undoing a copy, an archive made or an extraction deletes what was made
			if j.kind == jobMove {
				kind = files.Moved
			}
			a.pushUndo(files.Action{Kind: kind, Pairs: res.Pairs}, j.title)
		}
	case jobRecycle:
		var back []files.Trashed
		gone := 0
		for _, t := range trashed {
			if t.Stored != "" {
				back = append(back, t)
			} else {
				gone++
			}
		}
		if len(back) > 0 {
			a.pushUndo(files.Action{Kind: files.Recycled, Trashed: back}, j.title)
			a.fm.undo[len(a.fm.undo)-1].labels = labels
		}
		if gone > 0 {
			a.toast(u.tn(gone, "%d item was too large for the Recycle Bin and was deleted for good",
				"%d items were too large for the Recycle Bin and were deleted for good", gone), true)
		}
	}
	switch {
	case len(res.Failed) > 0:
		f := res.Failed[0]
		more := ""
		if len(res.Failed) > 1 {
			more = u.tf(" (and %d more)", len(res.Failed)-1)
		}
		a.toast(u.tf("%s: %s failed: %v%s", j.title, filepath.Base(f.Path), a.describeFileError(f.Err), more), true)
	case cancelled:
		a.toastOnly("files/job", u.tf("Stopped: %s", j.title))
	case time.Since(j.started) > 2*time.Second:
		a.toastOnly("files/job", u.tf("Done: %s", j.title))
	}
}

// libraryErr tells that the tags and categories could not follow a file operation.
func (a *App) libraryErr(err error) {
	if err != nil {
		a.toastOnly("files/library", a.u.tf("The tags and categories could not be saved: %s", a.describe(err)))
	}
}

// describeFileError says why a file operation failed in the interface's language where it is a known reason.
func (a *App) describeFileError(err error) string {
	u := &a.u
	switch {
	case err == nil:
		return ""
	case errors.Is(err, files.ErrIntoItself):
		return u.t("a folder cannot go into itself")
	case errors.Is(err, files.ErrNoBin):
		return u.t("this drive has no Recycle Bin: delete it for good instead")
	case errors.Is(err, files.ErrUnsafeArchive):
		return u.t("the archive has an entry that points outside it")
	case errors.Is(err, files.ErrOccupied):
		return u.t("something else is there now")
	case os.IsPermission(err):
		return u.t("access is denied")
	case os.IsNotExist(err):
		return u.t("it is no longer there")
	}
	return err.Error()
}

// takeFromAnalyses takes an item that was deleted or moved away off the analyses it was in.
func (a *App) takeFromAnalyses(path string, bytes int64) {
	for i, t := range a.analysisTrees() {
		if files.SamePath(t.Path(), path) {
			a.fm.trees = slices.Delete(a.fm.trees, i, i+1)
			a.analysesChanged()
			return
		}
		if n := t.Find(path); n != nil {
			n.Detach()
			a.analysesChanged()
			return
		}
		if parent := t.Find(filepath.Dir(path)); parent != nil {
			parent.Grow(-bytes, -1)
			a.analysesChanged()
			return
		}
	}
}

// addToAnalyses adds an item that came into an analysed folder to its analysis.
func (a *App) addToAnalyses(ad added) {
	parent := a.analysis(filepath.Dir(ad.path))
	if parent == nil {
		return
	}
	a.analysesChanged()
	if ad.node == nil {
		parent.Grow(ad.bytes, 1)
		return
	}
	if old := parent.Child(ad.node.Name); old != nil {
		old.Detach()
	}
	parent.Attach(ad.node)
}

// filesMaxUndo is how many operations can be undone.
const filesMaxUndo = 50

func (a *App) pushUndo(act files.Action, title string) {
	a.fm.undo = append(a.fm.undo, fileUndo{action: act, title: title})
	if len(a.fm.undo) > filesMaxUndo {
		a.fm.undo = slices.Delete(a.fm.undo, 0, len(a.fm.undo)-filesMaxUndo)
	}
}

// undoFiles undoes the last copy, move, rename or move to the Recycle Bin, in the background.
func (a *App) undoFiles() {
	u := &a.u
	if len(a.fm.undo) == 0 {
		a.toastOnly("files/undo", u.t("Nothing to undo: deleting for good cannot be undone"))
		return
	}
	last := a.fm.undo[len(a.fm.undo)-1]
	var busy []string
	for _, p := range last.action.Pairs {
		busy = append(busy, p.To)
	}
	if job := a.busyAny(busy); job != nil {
		a.toastOnly("files/busy", u.tf("Wait for “%s” to finish", job.title))
		return
	}
	a.fm.undo = a.fm.undo[:len(a.fm.undo)-1]
	j := &fileJob{kind: jobUndo, title: u.tf("Undoing: %s", last.title), paths: busy, started: time.Now(), sizes: map[string]int64{}}
	ctx, cancel := context.WithCancel(a.life)
	j.cancel = cancel
	a.fm.jobs = append(a.fm.jobs, j)
	a.background(func(context.Context) {
		sizes := fileSizes(busy)
		res := files.Undo(ctx, last.action, &j.progress)
		var back []files.Pair
		for _, p := range res.Pairs {
			if p.To != "" {
				back = append(back, files.Pair{To: p.To})
			}
		}
		add := scanAdded(ctx, back)
		a.post(func() {
			j.sizes = sizes
			// Undoing takes the items from where they went and puts them back where they were.
			dirs := map[string]bool{}
			for _, p := range res.Pairs {
				dirs[filepath.Dir(p.From)] = true
				if p.To != "" {
					dirs[filepath.Dir(p.To)] = true
				}
				if last.action.Kind != files.Recycled {
					a.takeFromAnalyses(p.From, j.sizes[p.From])
				}
			}
			for _, ad := range add {
				a.addToAnalyses(ad)
			}
			a.fm.jobs = slices.DeleteFunc(a.fm.jobs, func(x *fileJob) bool { return x == j })
			var list []string
			for d := range dirs {
				list = append(list, d)
			}
			a.refreshPanes(list)
			a.fm.git = nil
			lib := a.assetLibrary()
			switch last.action.Kind {
			case files.Moved, files.Renamed:
				for _, p := range res.Pairs {
					if p.To != "" {
						a.libraryErr(lib.Move(p.From, p.To))
					}
				}
			case files.Recycled:
				a.libraryErr(lib.Put(last.labels))
			}
			a.assetLibVersion++
			if err := res.Err(); err != nil {
				a.toast(u.tf("%s: %s failed: %v", j.title, filepath.Base(res.Failed[0].Path), a.describeFileError(res.Failed[0].Err)), true)
				return
			}
			a.toastOnly("files/undo", u.tf("Undone: %s", last.title))
		})
	})
}

// fileScan is an analysis running.
type fileScan struct {
	dir      string
	panes    []*filesPane // the lists that asked for it, which show it once it is done
	progress files.ScanProgress
	cancel   context.CancelFunc
	// live is what it has found so far of dir and the folders in it, taken from progress every liveEvery while
	// a list shows it, which one with no analysis of dir does (liveFor).
	live   *files.Node
	liveAt time.Time
}

// liveEvery is how often a list showing an analysis as it runs takes what it has found and sorts itself again:
// often enough to see the sizes grow, not so often that rows jump about.
const liveEvery = 250 * time.Millisecond

// liveFor is the analysis running for the list's own folder that the list asked for, while the list has no
// analysis to show of it, or nil.
func (a *App) liveFor(p *filesPane, dir string) *fileScan {
	for _, s := range a.fm.scans {
		if files.SamePath(s.dir, dir) && slices.Contains(s.panes, p) {
			if a.analysis(dir) == nil {
				return s
			}
			return nil
		}
	}
	return nil
}

// followScans takes what each analysis running has found so far for the lists that show it as it runs, and sorts
// them again by it.
func (a *App) followScans() {
	now := time.Now()
	for _, s := range a.fm.scans {
		if now.Sub(s.liveAt) < liveEvery {
			continue
		}
		var showing []*filesPane
		for _, p := range s.panes {
			if p.analysed && a.liveFor(p, p.dir) == s {
				showing = append(showing, p)
			}
		}
		if len(showing) == 0 {
			continue
		}
		s.live, s.liveAt = s.progress.Live(s.dir), now
		for _, p := range showing {
			a.arrange(p)
		}
	}
}

// scanOf is the analysis running on dir or a folder it is in, or nil.
func (a *App) scanOf(dir string) *fileScan {
	for _, s := range a.fm.scans {
		if files.Within(dir, s.dir) {
			return s
		}
	}
	return nil
}

// scanFor is the analysis running that the list asked for, or nil.
func (a *App) scanFor(p *filesPane) *fileScan {
	if s := a.scanOf(p.dir); s != nil && slices.Contains(s.panes, p) {
		return s
	}
	return nil
}

// paneAnalysis is the analysis a list shows: that of its folder, once the list was analysed with i. Another list
// showing the same folder is not changed by it.
func (a *App) paneAnalysis(p *filesPane) *files.Node {
	if !p.analysed {
		return nil
	}
	if n := a.analysis(p.dir); n != nil {
		return n
	}
	if s := a.liveFor(p, p.dir); s != nil {
		return s.live // what it has found so far, while it runs
	}
	return nil
}

// toggleAnalysis is what i does: it turns the list to its folder's analysis, as gdu shows it, or back to the plain
// list when it shows one; pressed while the analysis runs, it stops it.
func (a *App) toggleAnalysis(p *filesPane) {
	if p.search != nil {
		return
	}
	if s := a.scanOf(p.dir); s == nil && p.analysed {
		a.leaveAnalysis(p)
		return
	}
	a.analyse(p)
}

// leaveAnalysis shows the list's folder plainly again, ordered by its default where it was ordered by size.
func (a *App) leaveAnalysis(p *filesPane) {
	p.analysed, p.analyses = false, false
	if p.sort == listBySize {
		p.sort, p.desc, p.sorted = p.defaultSort(), false, false
	}
	a.arrange(p)
}

// refreshFiles reads every list again, and analyses again the list in use when it shows an analysis.
func (a *App) refreshFiles(fd *filesDoc) {
	a.refreshPanes(nil)
	if p := fd.pane(); p.analysed && p.search == nil && a.scanOf(p.dir) == nil {
		a.scanFolder(p)
	}
}

// analyse adds up what every folder in the list's folder holds, in the background, as gdu does, for this list
// alone: once done it shows the sizes, the largest first. A folder already analysed is shown at once; pressed
// while the analysis runs for this list, it stops it.
func (a *App) analyse(p *filesPane) {
	if p.search != nil {
		return
	}
	if p.atDrives() {
		// In the list of drives, the drive under the cursor is analysed, from inside it.
		e := p.current()
		if e == nil {
			return
		}
		a.goTo(p, p.path(e), "")
	}
	dir := p.dir
	if s := a.scanOf(dir); s != nil {
		if slices.Contains(s.panes, p) {
			s.panes = slices.DeleteFunc(s.panes, func(q *filesPane) bool { return q == p })
			if len(s.panes) == 0 {
				s.cancel()
			}
			if a.analysis(dir) == nil {
				a.leaveAnalysis(p) // what it had found so far is not an analysis
			}
			return
		}
		s.panes = append(s.panes, p) // already running for another list: this one shows it too, as it runs
		if files.SamePath(s.dir, dir) || a.analysis(dir) != nil {
			a.showAnalysis(p)
		}
		return
	}
	if a.analysis(dir) != nil {
		a.showAnalysis(p)
		return
	}
	a.scanFolder(p)
	a.showAnalysis(p) // what it finds, as it finds it
}

// scanFolder analyses the list's folder, anew, in the background; the list shows the analysis once it is done, and what
// it has found so far while it runs when it shows its analysis already (liveFor).
func (a *App) scanFolder(p *filesPane) {
	dir := p.dir
	s := &fileScan{dir: dir, panes: []*filesPane{p}}
	ctx, cancel := context.WithCancel(a.life)
	s.cancel = cancel
	a.fm.scans = append(a.fm.scans, s)
	a.background(func(context.Context) {
		scan := a.fm.scan
		if scan == nil {
			scan = files.Scan
		}
		node, err := scan(ctx, dir, &s.progress)
		a.post(func() {
			a.fm.scans = slices.DeleteFunc(a.fm.scans, func(x *fileScan) bool { return x == s })
			if err != nil {
				if ctx.Err() == nil {
					a.toast(a.u.tf("%s could not be analysed: %v", filepath.Base(dir), a.describeFileError(err)), true)
				}
				if errors.Is(err, fs.ErrNotExist) {
					a.takeFromAnalyses(dir, 0) // gone since an earlier run analysed it
				}
				// A list that showed what it was finding has nothing left to show.
				for _, q := range s.panes {
					if q.analysed && files.SamePath(q.dir, dir) && a.analysis(dir) == nil {
						a.leaveAnalysis(q)
					}
				}
				return
			}
			a.keepAnalysis(node)
			for _, d := range a.docs {
				if d.files == nil {
					continue
				}
				for _, q := range d.files.panes {
					switch {
					case slices.Contains(s.panes, q) && files.Within(q.dir, dir) && !q.analysed:
						a.showAnalysis(q)
					case q.analysed:
						a.arrange(q) // the analysis it shows was replaced, or it showed it as it ran: in its order
					}
				}
			}
		})
	})
}

// showAnalysis turns a list to its folder's analysis, ordered by size, the largest first.
func (a *App) showAnalysis(p *filesPane) {
	p.analysed, p.analyses = true, true
	p.sort, p.desc = listBySize, false
	a.arrange(p)
}

// keepAnalysis keeps a folder's analysis: it replaces the analysis of the same folder within an older one, and
// takes the place of those of folders inside it.
func (a *App) keepAnalysis(n *files.Node) {
	dir := n.Path()
	a.analysesChanged()
	for _, t := range a.analysisTrees() {
		if old := t.Find(dir); old != nil && old != t {
			parent := old.Parent
			old.Detach()
			n.Rename(filepath.Base(dir))
			parent.Attach(n)
			return
		}
	}
	a.fm.trees = slices.DeleteFunc(a.fm.trees, func(t *files.Node) bool { return files.Within(t.Path(), dir) })
	a.fm.trees = append(a.fm.trees, n)
}

// gitFor is the state of the git repository dir is in, or nil when it is in none; it is read again in the
// background when it is older than a moment.
func (a *App) gitFor(dir string) *gitState {
	if dir == thisPC {
		return nil
	}
	root, _ := a.repoRoot(dir)
	if root == "" {
		return nil
	}
	if a.fm.git == nil {
		a.fm.git = map[string]*gitState{}
	}
	g := a.fm.git[root]
	if g == nil {
		g = &gitState{status: files.GitStatus{Root: root}}
		a.fm.git[root] = g
	}
	if !g.reading && time.Since(g.at) > gitRefresh {
		g.reading = true
		a.background(func(ctx context.Context) {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			s := files.Git(ctx, root)
			a.post(func() {
				g.status, g.at, g.reading = s, time.Now(), false
			})
		})
	}
	if g.status.Branch == "" && !g.status.Counted && g.at.IsZero() {
		return nil // not read yet
	}
	return g
}

// gitRefresh is how old a repository's state may get while it is shown.
const gitRefresh = 4 * time.Second

// fileTick keeps the lists current: when the window comes back to the front, the folders are read again, as other
// programs may have changed them; the folders visited are written once they settle.
func (a *App) fileTick(fd *filesDoc) {
	if fd.pick == nil {
		a.rememberPanes(fd)
	}
	if a.fm.wasInactive && !a.u.inactive {
		a.refreshPanes(nil) // which reads the drives and the repositories again too
	}
	a.fm.wasInactive = a.u.inactive
	a.saveFileHistory(false)
	a.followScans()
	for _, p := range fd.panes {
		a.refreshAnalysis(p)
	}
	a.saveAnalyses(false)
}

// fileWork reports whether the file manager has work running: whose progress is shown, or a path being looked for.
func (a *App) fileWork() bool {
	return len(a.fm.jobs) > 0 || len(a.fm.scans) > 0 || a.fm.finding > 0 || a.searching()
}

// stopFileWork ends the file manager's work when the app closes: what runs is stopped, the folders visited saved.
func (a *App) stopFileWork() {
	for _, j := range a.fm.jobs {
		j.cancel()
	}
	for _, s := range a.fm.scans {
		s.cancel()
	}
	a.saveFileHistory(true)
	a.saveAnalyses(true)
}

// drawFileJobs floats what is running over the bottom right of the lists: each job's name, the file it is on, its
// progress, and a button that stops it. The lists stay usable under them. It returns the top of what it drew.
func (a *App) drawFileJobs(r gfx.Rect) float32 {
	u := &a.u
	if len(a.fm.jobs) == 0 {
		return r.Bottom()
	}
	w := min(u.px(400), r.W-u.px(24))
	h := u.px(76)
	y := r.Bottom() - u.px(12)
	for i := len(a.fm.jobs) - 1; i >= 0; i-- {
		j := a.fm.jobs[i]
		y -= h
		box := gfx.Rect{X: r.Right() - w - u.px(12), Y: y, W: w, H: h}
		y -= u.px(8)
		u.c.Outline(box.Inset(-u.px(1)), u.px(4), colShadow)
		u.c.Fill(box, colSurface)
		u.c.Outline(box, max(1, u.px(1)), colBorderHi)
		in := box.Inset(u.px(12))
		stop := gfx.Rect{X: in.Right() - u.px(24), Y: in.Y - u.px(2), W: u.px(24), H: u.px(24)}
		if u.iconButton(fmt.Sprintf("files/job/%p", j), stop, icClose, u.t("Stop"), false, true) {
			j.cancel()
		}
		u.label(u.bold(12.5), rectAt(in.X, in.Y, in.W-u.px(30), u.px(20)), j.title, colText, 0)
		frac := j.progress.Fraction()
		detail := ""
		switch {
		case j.progress.Counting.Load():
			detail = u.t("Counting…")
		case j.kind == jobCopy || j.kind == jobCompress || j.kind == jobExtract || j.kind == jobMove && j.progress.Total.Load() > int64(len(j.paths)):
			detail = fmt.Sprintf("%s / %s", sizeText(j.progress.Done.Load()), sizeText(j.progress.Total.Load()))
		default:
			detail = fmt.Sprintf("%s / %s", countText(j.progress.Done.Load()), countText(j.progress.Total.Load()))
		}
		cur := j.progress.Current()
		if cur != "" {
			cur = filepath.Base(cur)
		}
		f := u.face(11.5)
		dw := f.Measure(detail)
		u.label(f, rectAt(in.X, in.Y+u.px(22), in.W-dw-u.px(12), u.px(18)), cur, colDim, 0)
		u.label(f, rectAt(in.Right()-dw, in.Y+u.px(22), dw, u.px(18)), detail, colDim, 1)
		bar := gfx.Rect{X: in.X, Y: in.Bottom() - u.px(5), W: in.W, H: u.px(5)}
		barBg := colPanel
		if !isDarkTheme() {
			barBg = colSurfaceHi
		}
		u.c.Fill(bar, barBg)
		if frac < 0 {
			// Not known yet: a piece sweeps across.
			t := float32(time.Since(j.started).Seconds())
			x := (t*0.6 - float32(int(t*0.6))) * (bar.W + bar.W/4)
			seg := gfx.Rect{X: bar.X + x - bar.W/4, Y: bar.Y, W: bar.W / 4, H: bar.H}
			u.c.Push(bar)
			u.c.Fill(seg, colAccent)
			u.c.Pop()
		} else {
			u.c.Fill(gfx.Rect{X: bar.X, Y: bar.Y, W: bar.W * float32(frac), H: bar.H}, colAccent)
		}
	}
	return y + u.px(8)
}
