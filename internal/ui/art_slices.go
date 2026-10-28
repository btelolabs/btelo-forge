package ui

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// Slices, as the Krita named-assets plugin cuts them (paint/slices.go): K marks the chosen layers, groups and
// artboards, adding .png to their names; the layers panel shows an ⓘ by each slice, which explains its name; File ›
// Export slices writes them all beside the canvas file, keeping a copy of each file it replaces.

// sliceBackups is the folder beside the canvas file that keeps the files Export slices replaced, each export in a
// folder of its own named by when it ran.
const sliceBackups = ".slice-backups"

// markSlices marks the chosen layers as slices, or takes the mark away when all of them have it.
func (a *App) markSlices(ad *artDoc) {
	d := ad.d
	ids := slices.Clone(ad.picked)
	if !slices.Contains(ids, d.Active) {
		ids = append(ids, d.Active)
	}
	on := false
	for _, id := range ids {
		if n := d.Node(id); n != nil && !paint.IsSliceName(n.Name) {
			on = true
		}
	}
	a.artRun(ad, func() error {
		if d.Node(d.Active) == nil {
			return paint.ErrNoLayer
		}
		d.MarkSlices(ids, on)
		return nil
	})
}

// activeMarked reports whether the chosen node is marked as a slice, for the menu's check.
func activeMarked(d *paint.Doc) bool {
	n := d.Node(d.Active)
	return n != nil && paint.IsSliceName(n.Name)
}

// exportSlices writes every slice shown as a PNG under its path beside the canvas file. A name that cannot export
// stops it before anything is written; a file already there is replaced, a copy of it kept in sliceBackups.
func (a *App) exportSlices(ad *artDoc) {
	u := &a.u
	var files []paint.Slice
	for _, s := range ad.d.Slices() {
		if s.Hidden {
			continue
		}
		if s.Err != nil {
			a.toast(u.tf("The slice “%s” cannot export: %s", s.Name, a.describeSlice(s.Err)), true)
			return
		}
		if s.Role == paint.SliceFile {
			files = append(files, s)
		}
	}
	if len(files) == 0 {
		a.toast(u.t("There are no slices: K marks the chosen layers, adding .png to their names"), true)
		return
	}
	type out struct {
		path string
		img  *image.RGBA
	}
	base := filepath.Dir(ad.path)
	var outs []out
	var empty []string
	for _, s := range files {
		img, err := ad.d.SlicePicture(s)
		switch {
		case errors.Is(err, paint.ErrSliceEmpty):
			empty = append(empty, s.Name)
			continue
		case err != nil:
			a.toast(u.tf("The slice “%s” cannot export: %s", s.Name, a.describeSlice(err)), true)
			return
		}
		outs = append(outs, out{filepath.Join(base, filepath.FromSlash(s.Path)), img})
	}
	stamp := time.Now().Format("20060102-150405")
	a.background(func(context.Context) {
		var err error
		kept := 0
		for _, o := range outs {
			if _, statErr := os.Stat(o.path); statErr == nil {
				rel, _ := filepath.Rel(base, o.path)
				if err = copyFile(o.path, filepath.Join(base, sliceBackups, stamp, rel)); err != nil {
					break
				}
				kept++
			}
			if err = writePNG(o.path, o.img); err != nil {
				break
			}
		}
		a.post(func() {
			if err != nil {
				a.toast(u.tf("Export failed: %s", a.describe(err)), true)
				return
			}
			msg := u.tf("Exported %d slices to %s", len(outs), base)
			if kept > 0 {
				msg += " · " + u.tf("%d files replaced, the old ones kept in %s", kept, filepath.Join(sliceBackups, stamp))
			}
			if len(empty) > 0 {
				msg += " · " + u.tf("Left out, showing nothing: %s", strings.Join(empty, ", "))
			}
			var actions []toastAction
			if len(outs) > 0 {
				first := outs[0].path
				actions = append(actions, toastAction{u.t("Show in folder"), func() { a.host.Reveal(first) }})
			}
			a.toastActions(msg, len(outs) == 0, actions)
		})
	})
}

// copyFile copies the file at from to to, making its folder.
func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return writeFileWhole(to, data)
}

// describeSlice says why a slice cannot export, in the interface's language.
func (a *App) describeSlice(err error) string {
	u := &a.u
	var se *paint.SliceError
	if !errors.As(err, &se) {
		if errors.Is(err, paint.ErrSliceEmpty) {
			return u.t("it shows nothing")
		}
		return a.describe(err)
	}
	switch se.Problem {
	case paint.SliceAbsolute:
		return u.t("use a relative folder/name, without backslashes or a leading /")
	case paint.SliceEmptyPart:
		return u.t("a folder or file name is empty")
	case paint.SliceDots:
		return u.tf("“%s” cannot be a folder", se.Part)
	case paint.SliceSpaces:
		return u.tf("“%s” starts or ends with a space, or ends with a dot", se.Part)
	case paint.SliceBadChar:
		return u.tf("“%s” has a character file names cannot have (<>:\"|?*)", se.Part)
	case paint.SliceReserved:
		return u.tf("“%s” is a name Windows keeps for itself", se.Part)
	case paint.SliceSizeOnly:
		return u.tf("“%s” alone is a folder group's size, not a file name", se.Part)
	case paint.SliceTooBig:
		return u.tf("%d × %d is over 16384 pixels a side or 64M pixels", se.Size.X, se.Size.Y)
	}
	return u.tf("“%s” exports to the same file", se.Part)
}

// sliceIcon draws the ⓘ by a slice's or folder group's row, its right edge at right, and opens the explanation when
// clicked; it returns where the row's other marks end.
func (a *App) sliceIcon(ad *artDoc, s paint.Slice, id string, rr gfx.Rect, right float32) float32 {
	u := &a.u
	r := gfx.Rect{X: right - u.px(22), Y: rr.Y, W: u.px(22), H: rr.H}
	col := colAccent
	switch {
	case s.Err != nil:
		col = colDanger
	case s.Hidden:
		col = colFaint
	case u.hover(id+"/slice", r):
		col = colText
	}
	u.icon(icInfo, r, 15, col)
	u.hint(id+"/slice", u.t("Slice: click for what it exports and the naming rules"), r)
	if u.clicked(id+"/slice", r) {
		ad.sliceInfo = s.ID
		a.openMenu("art/slice-info", r, nil) // drawn by drawSliceInfo: a panel rather than a list
	}
	return r.X - u.px(4)
}

// sliceSize is the size slice s exports at: its own, an artboard's frame, or what the layer shows, worked out once
// for each change of the picture.
func (a *App) sliceSize(ad *artDoc, s paint.Slice) (image.Point, error) {
	if s.Size != (image.Point{}) {
		return s.Size, nil
	}
	c := ad.sliceSizes
	if c.id == s.ID && c.name == s.Name && c.at.Equal(ad.changed) {
		return c.size, c.err
	}
	img, err := ad.d.SlicePicture(s)
	var size image.Point
	if err == nil {
		size = img.Rect.Size()
	}
	ad.sliceSizes = sliceSizeCache{id: s.ID, name: s.Name, at: ad.changed, size: size, err: err}
	return size, err
}

// sliceSizeCache keeps the size of what the slice explained last shows.
type sliceSizeCache struct {
	id   int
	name string
	at   time.Time
	size image.Point
	err  error
}

// sliceExplanation says what slice s exports, and why at that size.
func (a *App) sliceExplanation(ad *artDoc, s paint.Slice) []string {
	u := &a.u
	if s.Err != nil {
		return []string{u.tf("Cannot export: %s.", a.describeSlice(s.Err))}
	}
	var lines []string
	if s.Role == paint.SliceFolder {
		dir := s.Path
		if dir == "" {
			dir = u.t("the canvas's folder")
		}
		lines = append(lines, u.tf("A folder group: it does not export itself. What is directly in it exports, marked or not, into %s at %d × %d, unless its name gives a size of its own.", dir, s.Size.X, s.Size.Y))
	} else {
		lines = append(lines, u.tf("Exports to %s.", s.Path))
		size, err := a.sliceSize(ad, s)
		switch {
		case err != nil:
			lines = append(lines, u.tf("Nothing to export now: %s.", a.describeSlice(err)))
		case s.From == paint.SizeOwn && s.Board:
			lines = append(lines, u.tf("A %d × %d PNG, the artboard's frame with its background.", size.X, size.Y))
		case s.From == paint.SizeOwn:
			lines = append(lines, u.tf("A %d × %d PNG, cut to the size of what the layer shows.", size.X, size.Y))
		case s.From == paint.SizeInName:
			lines = append(lines, u.tf("A %d × %d PNG, the size after its name: centred on what it shows, padded with transparency or cropped, never scaled.", size.X, size.Y))
		case s.From == paint.SizeInPath:
			lines = append(lines, u.tf("A %d × %d PNG, the size among its folders: centred on what it shows, padded with transparency or cropped, never scaled.", size.X, size.Y))
		default:
			lines = append(lines, u.tf("A %d × %d PNG, the size of the folder group it is in: centred on what it shows, padded with transparency or cropped, never scaled.", size.X, size.Y))
		}
	}
	if s.Hidden {
		lines = append(lines, u.t("It is hidden, so Export slices leaves it out until it is shown."))
	}
	return lines
}

// sliceRules are the naming rules the explanation lists, each an example and what it does.
func (a *App) sliceRules() [][2]string {
	u := &a.u
	return [][2]string{
		{"fish.png", u.t("marked: exports as fish.png, the size of what it shows (K marks and unmarks)")},
		{"assets/fish.png", u.t("into the assets folder")},
		{"fish_300x200.png", u.t("300 × 200: centred, padded or cropped, never scaled")},
		{"200x300/assets", u.t("a folder group: what is directly in it exports into assets/ at 200 × 300")},
		{"200x300/assets/fish.png", u.t("the same for one layer: assets/fish.png at 200 × 300")},
	}
}

// drawSliceInfo is the panel the ⓘ by a slice opens: what it exports, then the naming rules.
func (a *App) drawSliceInfo() {
	m := a.menu
	ad := a.art
	if m == nil || m.id != "art/slice-info" || ad == nil {
		return
	}
	s, ok := ad.d.SliceOf(ad.sliceInfo)
	if !ok {
		a.menu = nil // the name no longer makes a slice
		return
	}
	u := &a.u
	pad := u.px(14)
	w := u.px(430)
	body := u.face(12.5)
	inner := w - 2*pad
	lineH := body.Height() * 1.35
	var said []string
	for _, l := range a.sliceExplanation(ad, s) {
		said = append(said, body.Wrap(l, inner)...)
	}
	rules := a.sliceRules()
	code := u.bold(12)
	codeW := u.px(190)
	ruleLines := make([][]string, len(rules))
	h := pad + u.px(22) + float32(len(said))*lineH + u.px(14) + u.px(22)
	for i, r := range rules {
		ruleLines[i] = body.Wrap(r[1], inner-codeW)
		h += float32(len(ruleLines[i]))*lineH + u.px(4)
	}
	where := u.tf("Files go beside the canvas file, in %s; File › Export slices writes them all.", filepath.Dir(ad.path))
	whereLines := body.Wrap(where, inner)
	h += u.px(10) + float32(len(whereLines))*lineH + pad

	// Under the slice's row, reaching from the ⓘ towards the canvas, within the window.
	box := gfx.Rect{X: m.anchor.Right() - w, Y: m.anchor.Bottom() + u.px(4), W: w, H: h}
	box.X = max(box.X, u.px(4))
	box.Y = max(min(box.Y, a.screen.H-h-u.px(4)), u.px(4))
	u.c.Fill(gfx.Rect{X: box.X, Y: box.Y + u.px(3), W: box.W, H: box.H}, gfx.Alpha(colBlack, 0.35))
	u.c.Fill(box, colSurface)
	u.c.Outline(box, max(1, u.px(1)), colBorder)
	x, y := box.X+pad, box.Y+pad
	title := u.t("Slice")
	if s.Role == paint.SliceFolder {
		title = u.t("Slice folder group")
	}
	u.heading(gfx.Rect{X: x, Y: y, W: inner, H: u.px(20)}, title+" · "+s.Name)
	y += u.px(22)
	for _, l := range said {
		col := colText
		if s.Err != nil {
			col = colDanger
		}
		u.c.Text(body, x, y, l, col)
		y += lineH
	}
	y += u.px(14)
	u.heading(gfx.Rect{X: x, Y: y, W: inner, H: u.px(20)}, u.t("Naming rules"))
	y += u.px(22)
	for i, r := range rules {
		u.c.Text(code, x, y, code.Ellipsize(r[0], codeW-u.px(8)), colAccent)
		for j, l := range ruleLines[i] {
			u.c.Text(body, x+codeW, y+float32(j)*lineH, l, colDim)
		}
		y += float32(len(ruleLines[i]))*lineH + u.px(4)
	}
	y += u.px(10)
	for _, l := range whereLines {
		u.c.Text(body, x, y, l, colDim)
		y += lineH
	}
	// Clicks inside the panel stay with it; one outside, or Esc, closes it.
	if u.pressed && !box.Contains(u.pressX, u.pressY) && !m.anchor.Contains(u.pressX, u.pressY) && m.opened {
		a.menu = nil
		u.pressed = false
	}
	for _, k := range u.keys {
		if k.Code == keyEscape {
			a.menu = nil
		}
	}
	m.opened = true
}
