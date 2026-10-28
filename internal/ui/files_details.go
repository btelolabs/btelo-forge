package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/assets"
	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// The details of the item under the cursor, under its preview: what it is, and its tags and categories to change
// with the mouse, as Eagle's inspector shows them. The keys reach the same through t and the menu.

// actedOn are the paths the commands act on in the list in use: the marked items, the one under the cursor, or
// in a search, the one found.
func (a *App) actedOn(fd *filesDoc) []string {
	p := fd.pane()
	if p.search != nil {
		if path, _ := p.search.chosen(); path != "" {
			return []string{path}
		}
		return nil
	}
	return p.selected()
}

// tagMatches are the tags in use that start as typed, or hold it, that not every one of paths has yet.
func (a *App) tagMatches(typed string, paths []string) []string {
	lib := a.assetLibrary()
	q := strings.ToLower(strings.TrimSpace(typed))
	var starts, holds []string
	for _, t := range lib.Tags() {
		all := true
		for _, p := range paths {
			all = all && containsFold(lib.Of(p).Tags, t.Name)
		}
		lower := strings.ToLower(t.Name)
		switch {
		case all:
		case strings.HasPrefix(lower, q):
			starts = append(starts, t.Name)
		case strings.Contains(lower, q):
			holds = append(holds, t.Name)
		}
	}
	return append(starts, holds...)
}

// drawTagMatches lists the tags in use under the tag prompt's box, to pick with the arrows or a click.
func (a *App) drawTagMatches(fd *filesDoc, pr *filesPrompt, r gfx.Rect, rowH float32) {
	u := &a.u
	f := u.face(12.5)
	for i := range int(r.H / rowH) {
		row := rectAt(r.X, r.Y+float32(i)*rowH, r.W, rowH)
		id := fmt.Sprintf("files/tags/%d", i)
		if u.hover(id, row) && (u.mx != fd.mouseX || u.my != fd.mouseY || u.pressed) {
			pr.pick = i
		}
		if i == pr.pick {
			u.c.Fill(row, colSelected)
		}
		u.icon(icLabel, rectAt(row.X+u.px(4), row.Y, u.px(22), row.H), 13, gfx.Hex(0x7f8cff))
		u.label(f, rectAt(row.X+u.px(32), row.Y, row.W-u.px(36), row.H), pr.matches[i], colText, 0)
		if u.clicked(id, row) {
			pr.pick = i
			a.answerPrompt(fd)
			return
		}
	}
}

// categoryItems are the categories as a menu, each checked when every one of paths is in it.
func (a *App) categoryItems(fd *filesDoc, paths []string) []menuItem {
	u := &a.u
	lib := a.assetLibrary()
	var items []menuItem
	for _, c := range lib.Categories() {
		name := c.Name
		in := len(paths) > 0
		for _, p := range paths {
			in = in && containsFold(lib.Of(p).Categories, name)
		}
		items = append(items, menuItem{label: name, checked: in, run: func() { a.setCategory(paths, name, !in) }})
	}
	if len(items) > 0 {
		items = append(items, menuSep)
	}
	return append(items, menuItem{label: u.t("New category…"), run: func() { a.openPrompt(fd, promptCategory) }})
}

// detailsMinH is how tall the preview panel must be to show the details under the preview.
func (u *ctx) detailsMinH() float32 { return u.px(460) }

// drawFileDetails draws what is known of the items the commands act on: the one's kind, size and length, or how
// many there are; then their tags and categories.
func (a *App) drawFileDetails(fd *filesDoc, r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colBackground)
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: max(1, u.px(1))}, colBorder)
	paths := a.actedOn(fd)
	if len(paths) == 0 || fd.pane().atDrives() {
		return
	}
	a.scrolledBy("files/details", r.Inset(u.px(14)), &fd.detailScroll, &fd.detailH, func(c gfx.Rect) float32 {
		y := c.Y
		if len(paths) == 1 {
			e, info, ok := a.entryAt(paths[0])
			if !ok {
				return y
			}
			u.label(u.bold(13), gfx.Rect{X: c.X, Y: y, W: c.W, H: u.px(22)}, e.Name, colText, 0)
			y = a.drawProperties(c.X, y+u.px(28), c.W, e, info)
		} else {
			var total int64
			for _, p := range paths {
				if st, err := os.Stat(p); err == nil && !st.IsDir() {
					total += st.Size()
				}
			}
			u.label(u.bold(13), gfx.Rect{X: c.X, Y: y, W: c.W, H: u.px(22)}, u.tf("%d marked", len(paths)), colText, 0)
			u.label(u.face(12), gfx.Rect{X: c.X, Y: y + u.px(24), W: c.W, H: u.px(20)}, assets.FormatSize(total), colDim, 0)
			y += u.px(52)
		}
		y = a.drawTagEditor(fd, c.X, y, c.W, paths)
		return a.drawCategoryPicker(fd, c.X, y, c.W, paths)
	})
}

// entryAt is what is known of the item at path: what the asset library knows it as, and its picture's details.
func (a *App) entryAt(path string) (assets.Entry, assets.Info, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return assets.Entry{}, assets.Info{}, false
	}
	e := assets.Entry{Path: path, Name: filepath.Base(path), Kind: assets.KindOf(path), Size: st.Size(), Mod: st.ModTime()}
	if st.IsDir() {
		e.Kind, e.Size = assets.Folder, 0
		return e, assets.Info{}, true
	}
	_, info, _ := a.thumbOf(e)
	return e, info, true
}

// propertyRow draws a name and its value, the value wrapping under it when long; it returns the y below.
func (a *App) propertyRow(x, y, w float32, name, value string) float32 {
	u := &a.u
	nameW := u.px(84)
	u.label(u.face(11.5), gfx.Rect{X: x, Y: y, W: nameW, H: u.px(20)}, name, colFaint, 0)
	lines := u.face(12).Wrap(value, w-nameW)
	if len(lines) == 0 {
		lines = []string{""}
	}
	for i, l := range lines[:min(len(lines), 3)] {
		u.label(u.face(12), gfx.Rect{X: x + nameW, Y: y + float32(i)*u.px(18), W: w - nameW, H: u.px(20)}, l, colText, 0)
	}
	return y + float32(min(len(lines), 3))*u.px(18) + u.px(6)
}

// drawProperties lists what is known of a file.
func (a *App) drawProperties(x, y, w float32, e assets.Entry, info assets.Info) float32 {
	u := &a.u
	kind := u.t("Folder")
	if e.Kind != assets.Folder {
		i := slices.Index(assets.Kinds, e.Kind)
		kind = strings.ToUpper(strings.TrimPrefix(filepath.Ext(e.Name), "."))
		if i >= 0 {
			kind = strings.TrimSpace(kind + " · " + u.t(kindNames[i]))
		}
	}
	y = a.propertyRow(x, y, w, u.t("Kind"), kind)
	if info.W > 0 {
		y = a.propertyRow(x, y, w, u.t("Dimensions"), fmt.Sprintf("%d × %d", info.W, info.H))
	}
	if info.Duration > 0 {
		length := clock(info.Duration)
		if info.Frames > 0 {
			length += " · " + u.tn(info.Frames, "%d frame", "%d frames", info.Frames)
		}
		y = a.propertyRow(x, y, w, u.t("Length"), length)
	}
	if e.Kind != assets.Folder {
		y = a.propertyRow(x, y, w, u.t("File size"), assets.FormatSize(e.Size))
	}
	y = a.propertyRow(x, y, w, u.t("Modified"), e.Mod.Format("2006-01-02 15:04"))
	if info.Problem != "" {
		y = a.propertyRow(x, y, w, u.t("Problem"), info.Problem)
	}
	return y + u.px(8)
}

// tagBoxID is the box in the details where a tag is typed.
const tagBoxID = "files/details/tag"

// drawTagEditor shows the tags the items share as chips to remove, and a box to add one.
func (a *App) drawTagEditor(fd *filesDoc, x, y, w float32, paths []string) float32 {
	u := &a.u
	lib := a.assetLibrary()
	u.heading(gfx.Rect{X: x, Y: y, W: w, H: u.px(22)}, u.t("Tags"))
	y += u.px(26)
	// The tags every item has, then those only some have (shown fainter).
	counts := map[string]int{}
	var order []string
	for _, p := range paths {
		for _, t := range lib.Of(p).Tags {
			k := strings.ToLower(t)
			if counts[k] == 0 {
				order = append(order, t)
			}
			counts[k]++
		}
	}
	cx := x
	chipH := u.px(26)
	f := u.face(12)
	for i, t := range order {
		cw := f.Measure(t) + u.px(34)
		if cx+cw > x+w && cx > x {
			cx, y = x, y+chipH+u.px(6)
		}
		chip := gfx.Rect{X: cx, Y: y, W: cw, H: chipH}
		bg, fg := gfx.Mix(colSurface, gfx.Hex(0x7f8cff), 0.22), colText
		if counts[strings.ToLower(t)] != len(paths) {
			bg, fg = colSurface, colDim
		}
		u.c.Fill(chip, bg)
		u.label(f, gfx.Rect{X: chip.X + u.px(10), Y: chip.Y, W: cw - u.px(30), H: chipH}, t, fg, 0)
		xr := gfx.Rect{X: chip.Right() - u.px(24), Y: chip.Y + u.px(3), W: u.px(20), H: u.px(20)}
		id := fmt.Sprintf("files/details/untag/%d", i)
		col := colFaint
		if u.hover(id, xr) {
			col = colText
			u.cursor = CursorHand
		}
		u.icon(icClose, xr, 11, col)
		u.hint(id, u.tf("Take the tag %s off", t), xr)
		if u.clicked(id, xr) {
			if err := lib.RemoveTag(paths, t); err != nil {
				a.toast(a.describe(err), true)
			}
			a.assetLibVersion++
		}
		cx += cw + u.px(6)
	}
	if len(order) > 0 {
		y += chipH + u.px(8)
	}
	box := gfx.Rect{X: x, Y: y, W: w, H: u.px(32)}
	esc := u.focus == tagBoxID && slices.Contains(u.keys, Key{Code: keyEscape})
	if _, done := u.textBox(tagBoxID, box, &fd.tagText, withKey(u.t("Add a tag"), a.fileKeyHint("tag")), false); done {
		if !esc && strings.TrimSpace(fd.tagText) != "" {
			a.addTag(paths, fd.tagText)
		}
		fd.tagText = ""
	}
	return y + u.px(44)
}

// drawCategoryPicker lists the categories with a box each for whether the items are in it.
func (a *App) drawCategoryPicker(fd *filesDoc, x, y, w float32, paths []string) float32 {
	u := &a.u
	lib := a.assetLibrary()
	u.heading(gfx.Rect{X: x, Y: y, W: w, H: u.px(22)}, u.t("Categories"))
	add := gfx.Rect{X: x + w - u.px(24), Y: y - u.px(1), W: u.px(24), H: u.px(24)}
	if u.iconButton("files/details/category/new", add, icAdd, u.t("New category"), false, true) {
		a.openPrompt(fd, promptCategory)
	}
	y += u.px(26)
	cats := lib.Categories()
	if len(cats) == 0 {
		return a.paragraph(x, y, w, u.t("Make a category with +, then tick it here or drag files onto it above the grid."), 11, colFaint) + u.px(8)
	}
	for _, c := range cats {
		in := true
		for _, p := range paths {
			in = in && containsFold(lib.Of(p).Categories, c.Name)
		}
		if on := u.checkbox("files/details/category/"+c.Name, gfx.Rect{X: x, Y: y, W: w, H: u.px(26)}, c.Name, in); on != in {
			a.setCategory(paths, c.Name, on)
		}
		y += u.px(28)
	}
	return y + u.px(6)
}
