package ui

import (
	"fmt"
	"image/color"
	"math"
	"path/filepath"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/assets"
	"github.com/btelolabs/btelo-forge/internal/files"
	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// The file manager's symbols, from Material Icons.
const (
	icFmFolder  = ""
	icFmFile    = ""
	icFmText    = ""
	icFmImage   = ""
	icFmMovie   = ""
	icFmAudio   = ""
	icFmCode    = ""
	icFmLink    = ""
	icFmArchive = ""
	icFmBranch  = ""
	icFmFiles   = ""
	icFmDrive   = ""
)

// pictured reports whether a file is shown in the list by a picture of it rather than a symbol.
func pictured(name string) bool { return assets.KindOf(name).Visual() }

func fileGlyph(e *files.Entry) (string, color.RGBA) {
	if e.Dir {
		if e.Link {
			return icFmLink, gfx.Hex(0x8fb8ff)
		}
		return icFmFolder, gfx.Hex(0xe8b75b)
	}
	if files.IsArchive(e.Name) {
		return icFmArchive, gfx.Hex(0xd99a6c)
	}
	switch assets.KindOf(e.Name) {
	case assets.MindMap:
		return icMind, gfx.Hex(0x4fb3ff)
	case assets.Office:
		return officeGlyph(e.Name)
	case assets.Model:
		return icModel, modelHue
	case assets.Image, assets.Animation, assets.Canvas:
		return icFmImage, gfx.Hex(0x6fcf97)
	case assets.Video, assets.Edit:
		return icFmMovie, gfx.Hex(0x5aa9ff)
	case assets.Audio:
		return icFmAudio, gfx.Hex(0xbb8cff)
	case assets.Markdown, assets.Text:
		return icFmText, colDim
	case assets.Code:
		return icFmCode, gfx.Hex(0x56c7c2)
	}
	if e.Link {
		return icFmLink, colDim
	}
	return icFmFile, colDim
}

// sizeText writes a number of bytes as gdu does, in powers of 1024.
func sizeText(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	v := float64(n)
	for _, s := range []string{"KB", "MB", "GB", "TB", "PB"} {
		v /= unit
		if v < unit || s == "PB" {
			if v < 10 {
				return fmt.Sprintf("%.1f %s", v, s)
			}
			return fmt.Sprintf("%.0f %s", v, s)
		}
	}
	return ""
}

// countText writes a count with thousands separated.
func countText(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// itemCount says how many items there are, one or more.
func itemCount(u *ctx, n int64) string {
	if n == 1 {
		return u.t("1 item")
	}
	return u.tf("%s items", countText(n))
}

// drawFiles is a file manager tab: its lists side by side, the preview beside them when it is open, what is
// running over them, and at the bottom the state of the folder and the keys.
func (a *App) drawFiles(r gfx.Rect) {
	u := &a.u
	fd := a.files
	u.c.Fill(r, colPanel)
	if fd == nil {
		return // the tab strip above closed this tab, or switched from it, in this frame
	}
	a.fileTick(fd)

	moved := !fd.mouseSeen || u.mx != fd.mouseX || u.my != fd.mouseY
	defer func() { fd.mouseX, fd.mouseY, fd.mouseSeen = u.mx, u.my, true }()
	fd.hovered = ""
	if fd.prompt != nil {
		if u.focus != promptID {
			a.closePrompt(fd) // its box was not drawn, as when the row being renamed scrolled away
		} else {
			a.promptKeys(fd)
		}
	}

	keysH := a.keyListHeight(r.W)
	statusH := u.px(28)
	foot := gfx.Rect{X: r.X, Y: r.Bottom() - keysH - statusH, W: r.W, H: statusH + keysH}
	main := gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: foot.Y - r.Y}
	lists := main
	var pv gfx.Rect
	if fd.preview {
		pw := max(min(main.W*0.42, u.px(760)), min(u.px(300), main.W/2))
		pv = gfx.Rect{X: main.Right() - pw, Y: main.Y, W: pw, H: main.H}
		lists.W -= pw
	}

	// A dialog takes the mouse and the keys from the lists behind it.
	modal := fd.confirm != nil
	if modal {
		u.blocked = true
	}
	w := lists.W / float32(len(fd.panes))
	for i, p := range fd.panes {
		pr := gfx.Rect{X: lists.X + float32(i)*w, Y: lists.Y, W: w, H: lists.H}
		if i > 0 {
			u.c.Fill(gfx.Rect{X: pr.X, Y: pr.Y, W: max(1, u.px(1)), H: pr.H}, colBorderHi)
			pr.X += max(1, u.px(1))
			pr.W -= max(1, u.px(1))
		}
		a.drawPane(fd, i, p, pr, moved)
	}
	if fd.after != nil {
		fd.after()
		fd.after = nil
	}
	if fd.preview {
		a.drawPreviewPanel(fd, pv)
	}
	a.drawFileStatus(fd, rectAt(foot.X, foot.Y, foot.W, statusH))
	a.drawKeyList(fd, rectAt(foot.X, foot.Y+statusH, foot.W, keysH))
	if fd.prompt != nil && fd.prompt.kind != promptRename && fd.prompt.kind != promptPath {
		a.drawPromptBox(fd, lists)
	}
	a.drawFileJobs(rectAt(main.X, main.Y, lists.W, main.H))
	if modal {
		u.blocked = false
		a.drawConfirm(fd, r)
	}
	// The keys go to what has them: a dialog, a text box, else the commands.
	switch {
	case fd.confirm != nil:
		a.confirmKeys(fd)
	case u.focus == "":
		a.fileKeys(fd)
	}
}

// drawPane draws one list: its folder above, then its rows, or the search typed in it and what it found.
func (a *App) drawPane(fd *filesDoc, i int, p *filesPane, r gfx.Rect, moved bool) {
	u := &a.u
	active := i == fd.at
	id := fmt.Sprintf("files/pane/%d", i)
	head := gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: u.px(38)}
	bg := colBackground
	if active && len(fd.panes) > 1 {
		bg = gfx.Mix(colBackground, colAccent, 0.08)
	}
	u.c.Fill(head, bg)
	u.c.Fill(gfx.Rect{X: head.X, Y: head.Bottom() - max(1, u.px(1)), W: head.W, H: max(1, u.px(1))}, colBorder)
	if active && len(fd.panes) > 1 {
		u.c.Fill(gfx.Rect{X: head.X, Y: head.Y, W: head.W, H: u.px(2)}, colAccent)
	}
	body := gfx.Rect{X: r.X, Y: head.Bottom(), W: r.W, H: r.Bottom() - head.Bottom()}
	p.pie.shown = false // drawRows draws it, when the list shows an analysis as rows

	// The mouse moving over a list makes it the one the keys act on, as pointing at a row puts the cursor there.
	if moved && body.Contains(u.mx, u.my) && !u.blocked && u.active == "" && fd.at != i && fd.prompt == nil && fd.sweep == nil {
		a.focusPane(fd, i)
		active = true
	}
	if u.pressed && r.Contains(u.pressX, u.pressY) && !u.blocked && fd.at != i {
		a.focusPane(fd, i)
		active = true
	}

	head.W -= a.drawPaneButtons(fd, i, id, head)
	if p.search != nil {
		a.drawSearch(fd, i, p, head, body, moved)
		return
	}
	if pr := fd.prompt; pr != nil && pr.kind == promptPath && i == fd.at {
		box := rectAt(head.X+u.px(6), head.Y+u.px(4), head.W-u.px(10), head.H-u.px(8))
		if _, done := u.textBox(promptID, box, &pr.text, u.t("Type or paste a folder or file to go to"), false); done && fd.prompt == pr {
			a.closePrompt(fd) // a click elsewhere
		}
	} else if a.drawPathBar(p, id, head, true) {
		a.focusPane(fd, i)
		a.openPrompt(fd, promptPath)
	}
	if p.grid {
		a.drawGrid(fd, i, p, id, body, moved)
		return
	}
	a.drawRows(fd, i, p, id, body, moved)
}

// drawPaneButtons draws, at the right of a list's bar, a button to show it as pictures or as rows, as q does, + to
// open another list beside it, as e does, and × to close it, which the last list has not; it returns how much of
// the bar they take. What they do waits for the end of the
// frame, as the lists are being drawn in turn.
func (a *App) drawPaneButtons(fd *filesDoc, i int, id string, head gfx.Rect) float32 {
	u := &a.u
	size := u.px(26)
	y := head.Y + (head.H-size)/2
	x := head.Right() - u.px(6) - size
	if len(fd.panes) > 1 {
		hint := withKey(u.t("Close this list"), a.fileKeyHint("close-pane"))
		if u.iconButton(id+"/close", rectAt(x, y, size, size), icClose, hint, false, true) {
			fd.after = func() {
				a.focusPane(fd, i)
				a.closePane(fd)
			}
		}
		x -= size + u.px(2)
	}
	if fd.pick == nil { // the dialog has one list
		hint := withKey(u.t("Open another list beside this one"), a.fileKeyHint("split"))
		if u.iconButton(id+"/split", rectAt(x, y, size, size), icAdd, hint, false, len(fd.panes) < filesMaxPanes) {
			fd.after = func() {
				a.focusPane(fd, i)
				a.splitPane(fd)
			}
		}
		x -= size + u.px(2)
	}
	glyph, hint := icCategory, withKey(u.t("Show as pictures, with tags"), a.fileKeyHint("view"))
	if fd.panes[i].grid {
		glyph, hint = icFmFiles, withKey(u.t("Show as a list"), a.fileKeyHint("view"))
	}
	if u.iconButton(id+"/view", rectAt(x, y, size, size), glyph, hint, false, true) {
		fd.after = func() {
			a.focusPane(fd, i)
			a.toggleView(fd)
		}
	}
	return head.Right() - x + u.px(4)
}

// fileKeyHint is the first key of the file manager command id, for a hint.
func (a *App) fileKeyHint(id string) string {
	for _, c := range a.fileCommands() {
		if c.id == "files."+id {
			return a.keyHint(c.id, c.defaults)
		}
	}
	return ""
}

// drawPathBar draws the folder as its parts, each of which can be clicked to go there. A file manager's list (manager)
// shows on the right what is known of it, an analysis running or done and the order, and the bar reports a click on
// its empty part, which turns it into a box to type or paste a path in, as Explorer's address bar does.
func (a *App) drawPathBar(p *filesPane, id string, r gfx.Rect, manager bool) bool {
	u := &a.u
	f := u.face(13)
	info := ""
	if manager {
		info = a.paneInfo(p)
	}
	inf := u.face(11.5)
	iw := min(inf.Measure(info)+u.px(16), r.W*0.4)
	if info != "" {
		u.label(inf, gfx.Rect{X: r.Right() - iw, Y: r.Y, W: iw - u.px(10), H: r.H}, info, colDim, 1)
	}
	room := r.W - iw - u.px(16)
	var parts []pathPart
	if p.atDrives() || filepath.VolumeName(p.dir) != "" && len(files.Drives()) > 0 {
		parts = append(parts, pathPart{u.t("This PC"), thisPC})
	}
	if !p.atDrives() {
		parts = append(parts, pathParts(p.dir)...)
	}
	sep := " › "
	sw := f.Measure(sep)
	// The folder itself is always shown, then as many of the folders above it as fit, the nearest first; the rest
	// become an ellipsis.
	bold := u.bold(13)
	avail := room - u.px(12)
	prefix := f.Measure("…" + sep)
	first := len(parts) - 1
	used := bold.Measure(parts[first].name)
	for first > 0 {
		w := f.Measure(parts[first-1].name) + sw
		need := used + w
		if first-1 > 0 {
			need += prefix // parts above it would still be left out
		}
		if need > avail {
			break
		}
		used += w
		first--
	}
	x := r.X + u.px(12)
	if first > 0 {
		u.c.Text(f, x, r.Y+(r.H-f.Height())/2, "…"+sep, colFaint)
		x += f.Measure("…" + sep)
	}
	for j := first; j < len(parts); j++ {
		part := parts[j]
		last := j == len(parts)-1
		face := f
		if last {
			face = bold
		}
		w := face.Measure(part.name)
		if last {
			w = max(min(w, r.X+u.px(12)+avail-x), 0) // only a very narrow list shortens the folder's own name
		}
		seg := gfx.Rect{X: x - u.px(3), Y: r.Y + u.px(6), W: w + u.px(6), H: r.H - u.px(12)}
		col := colDim
		if last {
			col = colText
		}
		sid := fmt.Sprintf("%s/path/%d", id, j)
		if !last && u.hover(sid, seg) {
			u.c.Fill(seg, colSurfaceHi)
			u.cursor = CursorHand
			col = colText
		}
		u.c.Text(face, x, r.Y+(r.H-face.Height())/2, face.Ellipsize(part.name, w+1), col)
		if !last && u.clicked(sid, seg) {
			land := filepath.Base(parts[j+1].path)
			if part.path == thisPC {
				land = filepath.VolumeName(parts[j+1].path)
			}
			a.goTo(p, part.path, land)
		}
		x += w
		if !last {
			u.c.Text(f, x, r.Y+(r.H-f.Height())/2, sep, colFaint)
			x += sw
		}
	}
	empty := gfx.Rect{X: x + u.px(4), Y: r.Y, W: r.X + room - x - u.px(4), H: r.H}
	if empty.W <= 0 || !manager {
		return false
	}
	if u.hover(id+"/edit", empty) {
		u.cursor = CursorText
	}
	u.hint(id+"/edit", withKey(u.t("Type or paste a path"), a.fileKeyHint("path")), empty)
	return u.clicked(id+"/edit", empty)
}

// pathPart is a folder on the way to another.
type pathPart struct{ name, path string }

// pathParts splits a folder into the folders on the way to it, from its drive.
func pathParts(dir string) []pathPart {
	var out []pathPart
	for {
		parent := filepath.Dir(dir)
		name := filepath.Base(dir)
		if parent == dir {
			out = append(out, pathPart{strings.TrimSuffix(dir, string(filepath.Separator)), dir})
			break
		}
		out = append(out, pathPart{name, dir})
		dir = parent
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if out[0].name == "" { // the root of a Unix file system
		out[0].name = "/"
	}
	return out
}

// paneInfo is what is known of a list's folder: its analysis, the order, or that it is being read.
func (a *App) paneInfo(p *filesPane) string {
	u := &a.u
	if p.atDrives() {
		return u.tn(len(p.rows), "%d drive", "%d drives", len(p.rows))
	}
	if s := a.scanFor(p); s != nil {
		return u.tf("Analysing… %s files · %s", countText(s.progress.Files.Load()), sizeText(s.progress.Bytes.Load()))
	}
	var parts []string
	if n := a.paneAnalysis(p); n != nil {
		total := u.tf("%s in %s", sizeText(n.Size), itemCount(u, n.Items))
		if n.Err {
			total = u.tf("at least %s", total)
		}
		parts = append(parts, total)
	}
	order := []string{u.t("by name"), u.t("by size"), u.t("by items"), u.t("by time")}[p.sort]
	if p.desc {
		order += " ↑"
	}
	parts = append(parts, order)
	return strings.Join(parts, " · ")
}

// fileColumns are where a row's parts go across a list of a width: the size bar and the number of items only once
// the folder is analysed, the time modified where there is room.
type fileColumns struct {
	icon, name, bar, percent, size, items, mod gfx.Rect
}

func (a *App) fileColumns(r gfx.Rect, analysed bool) fileColumns {
	u := &a.u
	var c fileColumns
	pad := u.px(8)
	x := r.X + pad
	c.icon = gfx.Rect{X: x, Y: r.Y + u.px(2), W: r.H - u.px(4), H: r.H - u.px(4)}
	right := r.Right() - pad
	take := func(w float32) gfx.Rect {
		right -= w
		return gfx.Rect{X: right, Y: r.Y, W: w - u.px(10), H: r.H}
	}
	room := r.W - c.icon.W - 2*pad
	if room > u.px(520) {
		c.mod = take(u.px(132))
	}
	if analysed && room > u.px(430) {
		c.items = take(u.px(74))
	}
	c.size = take(u.px(78))
	if analysed {
		if room > u.px(360) {
			c.bar = take(u.px(84))
		}
		c.percent = take(u.px(52))
	}
	c.name = gfx.Rect{X: c.icon.Right() + u.px(10), Y: r.Y, W: right - c.icon.Right() - u.px(16), H: r.H}
	return c
}

// fileRowH is the height of a list's rows, room for a picture of the file.
func (u *ctx) fileRowH() float32 { return u.px(32) }

// drawRows draws a list's rows and handles the mouse and the wheel over them.
func (a *App) drawRows(fd *filesDoc, i int, p *filesPane, id string, body gfx.Rect, moved bool) {
	u := &a.u
	rowH := u.fileRowH()
	// An analysis has its pie under the rows, where there is room for it.
	node := a.paneAnalysis(p)
	var wedges []pieWedge
	var chart gfx.Rect
	if node != nil && len(p.rows) > 0 && !p.atDrives() {
		if h := a.pieHeight(body); h > 0 {
			if wedges = a.pieWedges(p, node); len(wedges) > 0 {
				chart = gfx.Rect{X: body.X, Y: body.Bottom() - h, W: body.W, H: h}
				body.H -= h
			}
		}
	}
	visible := int(body.H / rowH)
	if fd.at == i {
		fd.pageRows = visible
	}
	if u.hover(id, body) && u.wheel != 0 && fd.prompt == nil {
		p.top -= u.wheel * 3
		u.wheel = 0
	}
	maxTop := float32(max(len(p.rows)-visible, 0))
	s := fd.sweeping(u, p)
	if s != nil {
		var beyond float32
		if u.my < body.Y {
			beyond = (u.my - body.Y) / rowH
		} else if u.my > body.Bottom() {
			beyond = (u.my - body.Bottom()) / rowH
		}
		p.top += s.edgeScroll(beyond)
	}
	p.top = min(max(p.top, 0), maxTop)
	if s != nil && len(p.rows) > 0 {
		under := int(p.top) + int(math.Floor(float64((u.my-body.Y)/rowH)))
		a.sweepTo(fd, p, s, min(max(under, 0), len(p.rows)-1))
	}

	if len(p.rows) == 0 {
		msg := u.t("This folder is empty")
		switch {
		case p.err != nil:
			msg = u.tf("This folder cannot be read: %v", p.err)
		case p.reading:
			msg = u.t("Reading…")
		case len(p.entries) > 0:
			msg = u.t("Only hidden files: press . to show them")
		}
		u.label(u.face(13), gfx.Rect{X: body.X + u.px(16), Y: body.Y + u.px(16), W: body.W - u.px(32), H: u.px(24)}, msg, colDim, 0.5)
		return
	}

	var largest int64
	if node != nil {
		for _, r := range p.rows {
			largest = max(largest, a.entrySize(node, &p.entries[r]))
		}
	}
	u.c.Push(body)
	first := int(p.top)
	for row := first; row < len(p.rows) && float32(row-first)*rowH < body.H; row++ {
		e := &p.entries[p.rows[row]]
		rr := gfx.Rect{X: body.X, Y: body.Y + float32(row-first)*rowH, W: body.W, H: rowH}
		rid := fmt.Sprintf("%s/row/%s", id, e.Name)
		hot := u.hover(rid, rr) && fd.prompt == nil
		if hot && moved && fd.at == i && fd.pick == nil {
			p.cursor = row
		}
		if hot {
			fd.hovered = p.path(e)
		}
		if fd.prompt == nil && u.rightAt != nil && rr.Contains(u.rightAt.X, u.rightAt.Y) && !u.blocked {
			a.startSweep(fd, i, p, row)
		}
		a.drawFileRow(fd, p, row, e, rr, node, wedges, largest, a.fileColumns(rr, node != nil), fd.at == i)
		if fd.prompt != nil {
			continue
		}
		if u.press(rid, rr) {
			p.cursor = row
			a.pickClicked(fd)
			switch {
			case u.ctrl:
				a.toggleMarkAt(p, row)
			case u.double:
				a.enter(fd)
			}
		}
		u.letGo(rid)
	}
	u.c.Pop()
	// A scroll bar, while the list is longer than its place.
	if maxTop > 0 {
		h := max(body.H*float32(visible)/float32(len(p.rows)), u.px(24))
		y := body.Y + (body.H-h)*p.top/maxTop
		u.c.Fill(gfx.Rect{X: body.Right() - u.px(5), Y: y, W: u.px(3), H: h}, colBorderHi)
	}
	if len(wedges) > 0 {
		a.drawPie(fd, i, p, id, chart, node, wedges)
	}
}

// entrySize is an entry's size: a file's length, or what an analysed folder holds.
func (a *App) entrySize(node *files.Node, e *files.Entry) int64 {
	if e.Dir && node != nil {
		if c := node.Child(e.Name); c != nil {
			return c.Size
		}
		return 0
	}
	return e.Size
}

// drawFileRow draws one entry: its picture or symbol, its name, and its size and time; the size bar takes the colour
// of the entry's wedge when the list has a pie.
func (a *App) drawFileRow(fd *filesDoc, p *filesPane, row int, e *files.Entry, r gfx.Rect, node *files.Node, wedges []pieWedge, largest int64, c fileColumns, active bool) {
	u := &a.u
	path := p.path(e)
	busy := a.busyJob(path)
	cursor := row == p.cursor
	switch {
	case cursor && active:
		u.c.Fill(r, colSelected)
		u.c.Fill(gfx.Rect{X: r.X, Y: r.Y, W: u.px(2), H: r.H}, colAccent)
	case cursor:
		u.c.Outline(r.Inset(u.px(1)), max(1, u.px(1)), colBorderHi)
	}
	if p.marked[e.Name] {
		u.c.Fill(r, gfx.Alpha(colAccent, 0.16))
		u.c.Fill(gfx.Rect{X: r.Right() - u.px(3), Y: r.Y, W: u.px(3), H: r.H}, colAccent)
	}
	fg := colText
	if e.Hidden || busy != nil {
		fg = colDim
	}
	clipped := a.inClip(path)
	if clipped && a.fm.clip.cut {
		fg = colFaint
	}

	// A picture of an image or video, else a symbol for what the file holds.
	if !e.Dir && pictured(e.Name) && busy == nil {
		a.drawFileThumb("files/thumb/"+path, c.icon, path)
	} else {
		glyph, col := fileGlyph(e)
		if p.atDrives() {
			glyph, col = icFmDrive, colDim
		}
		if busy != nil {
			col = colFaint
		}
		u.icon(glyph, c.icon, 18, col)
	}

	f := u.face(13)
	name := e.Name
	if prompt := fd.prompt; prompt != nil && prompt.kind == promptRename && cursor && active {
		a.drawRenameBox(fd, gfx.Rect{X: c.name.X - u.px(4), Y: r.Y + u.px(3), W: c.name.W + u.px(4), H: r.H - u.px(6)})
	} else {
		nameW := c.name.W
		if clipped {
			nameW -= u.px(22)
		}
		shown := f.Ellipsize(name, nameW)
		u.c.Text(f, c.name.X, r.Y+(r.H-f.Height())/2, shown, fg)
		if clipped {
			glyph := icCopy
			if a.fm.clip.cut {
				glyph = icCut
			}
			u.icon(glyph, gfx.Rect{X: c.name.X + f.Measure(shown) + u.px(4), Y: r.Y, W: u.px(18), H: r.H}, 12, colAccentHi)
		}
	}

	small := u.face(12)
	if p.atDrives() {
		a.drawDriveColumns(p.drives[e.Name], r, c)
		return
	}
	if busy != nil {
		u.label(small, gfx.Rect{X: c.size.X - u.px(140), Y: r.Y, W: c.size.Right() - c.size.X + u.px(140), H: r.H}, busy.state(u), colWarning, 1)
		return
	}
	size := a.entrySize(node, e)
	known := !e.Dir || node != nil && node.Child(e.Name) != nil
	if known {
		u.label(small, c.size, sizeText(size), colDim, 1)
	}
	if node != nil && c.percent.W > 0 && known {
		pct := 0.0
		if node.Size > 0 {
			pct = float64(size) / float64(node.Size) * 100
		}
		u.label(small, c.percent, fmt.Sprintf("%.1f%%", pct), colFaint, 1)
		if c.bar.W > 0 && largest > 0 {
			bar := gfx.Rect{X: c.bar.X, Y: r.Y + r.H/2 - u.px(4), W: c.bar.W, H: u.px(8)}
			u.c.Fill(bar, colSurface)
			fill := bar
			fill.W = float32(math.Max(float64(bar.W)*float64(size)/float64(largest), 0))
			col := gfx.Mix(colAccent, colWarning, float32(size)/float32(largest)*0.6)
			if wc, ok := a.pieColorOf(wedges, node, e); ok {
				col = wc
			}
			u.c.Fill(fill, col)
		}
	}
	if node != nil && e.Dir && c.items.W > 0 {
		if n := node.Child(e.Name); n != nil {
			u.label(small, c.items, countText(n.Items), colFaint, 1)
		}
	}
	if c.mod.W > 0 && !e.Mod.IsZero() {
		u.label(small, c.mod, e.Mod.Format("2006-01-02 15:04"), colFaint, 1)
	}
}

// drawFileStatus is the line under the lists: the item under the cursor, the marks and what is cut or copied on
// the left, the git repository on the right.
func (a *App) drawFileStatus(fd *filesDoc, r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colBackground)
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: max(1, u.px(1))}, colBorder)
	f := u.face(12)
	p := fd.pane()
	right := r.Right() - u.px(12)
	if g := a.gitFor(p.dir); g != nil {
		right = a.drawGitStatus(g, r, right)
	}
	var parts []string
	if e := p.current(); e != nil && p.search == nil {
		info := e.Name
		if !e.Dir {
			info += " · " + sizeText(e.Size)
		} else if n := a.paneAnalysis(p); n != nil {
			if c := n.Child(e.Name); c != nil {
				info += " · " + sizeText(c.Size)
			}
		}
		if !e.Mod.IsZero() {
			info += " · " + e.Mod.Format("2006-01-02 15:04")
		}
		parts = append(parts, info)
	}
	if n := len(p.marked); n > 0 {
		parts = append(parts, u.tf("%d marked", n))
	}
	if n := len(a.fm.clip.paths); n > 0 {
		if a.fm.clip.cut {
			parts = append(parts, u.tf("%d marked to move, moved with v", n))
		} else {
			parts = append(parts, u.tf("%d copied, to paste with v", n))
		}
	}
	if len(p.rows) > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d", p.cursor+1, len(p.rows)))
	}
	u.label(f, gfx.Rect{X: r.X + u.px(12), Y: r.Y, W: right - r.X - u.px(24), H: r.H}, strings.Join(parts, "   "), colDim, 0)
}

// drawGitStatus draws the branch and how many files are changed, ending at right; it returns where it begins.
func (a *App) drawGitStatus(g *gitState, r gfx.Rect, right float32) float32 {
	u := &a.u
	s := g.status
	f := u.face(12)
	var text string
	branch := s.Branch
	if branch == "" {
		branch = "?"
	}
	if s.Detached {
		branch = u.tf("detached at %s", branch)
	}
	text = branch
	if s.Ahead > 0 {
		text += fmt.Sprintf(" ↑%d", s.Ahead)
	}
	if s.Behind > 0 {
		text += fmt.Sprintf(" ↓%d", s.Behind)
	}
	col := colSuccess
	if s.Counted {
		if n := s.Changed(); n > 0 {
			text += "  " + u.tn(n, "%d file changed", "%d files changed", n)
			col = colWarning
		} else {
			text += "  " + u.t("nothing to commit")
		}
		if s.Conflicts > 0 {
			text += "  " + u.tf("%d in conflict", s.Conflicts)
			col = colDanger
		}
	}
	w := f.Measure(text)
	x := right - w
	u.c.Text(f, x, r.Y+(r.H-f.Height())/2, text, col)
	u.icon(icFmBranch, gfx.Rect{X: x - u.px(22), Y: r.Y, W: u.px(18), H: r.H}, 13, col)
	u.hint("files/git", s.Root, gfx.Rect{X: x - u.px(22), Y: r.Y, W: w + u.px(22), H: r.H})
	return x - u.px(34)
}

// keyListLines lays a list of keys out in lines of a width.
func (a *App) keyListLines(w float32, list []string) [][]string {
	u := &a.u
	f, kf := u.face(11.5), u.bold(11.5)
	gap := u.px(18)
	var lines [][]string
	var line []string
	used := float32(0)
	for _, k := range list {
		key, name, _ := strings.Cut(k, " ")
		kw := kf.Measure(key) + u.px(5) + f.Measure(name)
		if len(line) > 0 && used+gap+kw > w-u.px(24) {
			lines, line, used = append(lines, line), nil, 0
		}
		if len(line) > 0 {
			used += gap
		}
		line, used = append(line, k), used+kw
	}
	return append(lines, line)
}

func (a *App) keyListHeight(w float32) float32 {
	return float32(len(a.keyListLines(w, a.keyList())))*a.u.px(19) + a.u.px(10)
}

// drawKeyList lists every key of the file manager, each with what it does, the key bright and the name dim; with the
// pointer over an item, the keys that act on it, after its name.
func (a *App) drawKeyList(fd *filesDoc, r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colBackground)
	f := u.face(11.5)
	kf := u.bold(11.5)
	gap := u.px(18)
	list := a.keyList()
	x0 := r.X + u.px(12)
	if fd.hovered != "" && fd.prompt == nil && fd.confirm == nil {
		list = a.itemKeys(fd.hovered)
		name := filepath.Base(fd.hovered)
		if name == string(filepath.Separator) || name == "." {
			name = fd.hovered
		}
		y := r.Y + u.px(3)
		u.icon(icMouse, rectAt(x0, y, u.px(16), u.px(19)), 11, colAccentHi)
		x0 += u.px(20)
		name = u.face(11.5).Ellipsize(name, min(u.px(260), r.W*0.3))
		x0 = u.c.Text(u.bold(11.5), x0, y+(u.px(19)-kf.Height())/2, name, colAccentHi) + gap
	}
	for li, line := range a.keyListLines(r.W-(x0-r.X-u.px(12)), list) {
		x := x0
		y := r.Y + u.px(3) + float32(li)*u.px(19)
		for _, item := range line {
			k, name, _ := strings.Cut(item, " ")
			x = u.c.Text(kf, x, y+(u.px(19)-kf.Height())/2, k, colText) + u.px(5)
			x = u.c.Text(f, x, y+(u.px(19)-f.Height())/2, name, colFaint) + gap
		}
	}
}

// drawDriveColumns shows a drive's name, how full it is as a bar, and the space free of the whole, as This PC does.
func (a *App) drawDriveColumns(d files.Drive, r gfx.Rect, c fileColumns) {
	u := &a.u
	small := u.face(12)
	f := u.face(13)
	if d.Label != "" {
		x := c.name.X + f.Measure(d.Name) + u.px(10)
		u.label(small, rectAt(x, r.Y, c.name.Right()-x, r.H), d.Label, colDim, 0)
	}
	if d.Total <= 0 {
		u.label(small, rectAt(c.size.X-u.px(200), r.Y, c.size.Right()-c.size.X+u.px(200), r.H), u.t("No disc or card"), colFaint, 1)
		return
	}
	used := float32(d.Total-d.Free) / float32(d.Total)
	bw := min(u.px(160), r.W*0.25)
	bar := gfx.Rect{X: c.size.X - u.px(16) - bw, Y: r.Y + r.H/2 - u.px(4), W: bw, H: u.px(8)}
	u.c.Fill(bar, colSurface)
	col := colAccent
	if used > 0.9 {
		col = colDanger // as Explorer warns of a drive nearly full
	}
	u.c.Fill(gfx.Rect{X: bar.X, Y: bar.Y, W: bar.W * used, H: bar.H}, col)
	u.label(small, c.size, sizeText(d.Total), colDim, 1)
	if c.mod.W > 0 {
		u.label(small, c.mod, u.tf("%s free", sizeText(d.Free)), colFaint, 1)
	}
}
