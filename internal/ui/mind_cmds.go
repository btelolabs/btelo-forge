package ui

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/assets"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/mind"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// The map tab's commands, with keys as in XMind: Tab adds a child and Enter a sibling, F2 or typing types into the
// chosen topic, the arrows go to the node that way, Alt with them moves it among its siblings, Ctrl+/ folds and
// unfolds. Each is listed in Settings under Mind maps, where its keys can be changed.

// mindCommand is a map tab command keys can run.
type mindCommand struct {
	shortcut
	run func()
}

// mindCommands are the map tab's commands, in the order Settings shows them.
func (a *App) mindCommands() []mindCommand {
	u := &a.u
	md := a.board
	c := func(id, label string, run func(), ks ...*Key) mindCommand {
		return mindCommand{shortcut: shortcut{id: "mind." + id, domain: domainMind, label: label, defaults: keys(ks...)},
			run: func() {
				if md != nil {
					run()
				}
			}}
	}
	return []mindCommand{
		c("add-child", u.t("Add a child topic"), func() { a.mindAddChild(md) }, key(keyTab, ""), key(keyInsert, "")),
		c("add-sibling", u.t("Add a topic after this one"), func() { a.mindAddSibling(md, true) }, key(keyEnter, "")),
		c("add-sibling-before", u.t("Add a topic before this one"), func() { a.mindAddSibling(md, false) }, key(keyEnter, "s")),
		c("add-parent", u.t("Add a topic above this one"), func() { a.mindAddParent(md) }, key(keyEnter, "c")),
		c("edit", u.t("Type into the chosen node"), func() { a.mindEditChosen(md) }, key(keyF2, "")),
		c("delete", u.t("Delete"), func() { a.mindDelete(md) }, key(keyDelete, ""), key(keyBack, "")),
		c("select-all", u.t("Choose everything"), func() { a.mindSelectAll(md) }, key('A', "c")),
		c("copy", u.t("Copy"), func() { a.mindCopy(md, false) }, key('C', "c")),
		c("cut", u.t("Cut"), func() { a.mindCopy(md, true) }, key('X', "c")),
		c("paste", u.t("Paste"), func() { a.mindPaste(md) }, key('V', "c")),
		c("duplicate", u.t("Duplicate"), func() { a.mindDuplicate(md) }, key('D', "c")),
		c("undo", u.t("Undo"), func() { a.mindUndo(md) }, key('Z', "c")),
		c("redo", u.t("Redo"), func() { a.mindRedo(md) }, key('Y', "c"), key('Z', "cs")),
		c("fold", u.t("Fold or unfold the chosen topics"), func() { a.mindFold(md) }, key(keySlash, "c")),
		c("left", u.t("Go to the node on the left"), func() { a.mindGo(md, -1, 0) }, key(keyLeft, "")),
		c("right", u.t("Go to the node on the right"), func() { a.mindGo(md, 1, 0) }, key(keyRight, "")),
		c("up", u.t("Go to the node above"), func() { a.mindGo(md, 0, -1) }, key(keyUp, "")),
		c("down", u.t("Go to the node below"), func() { a.mindGo(md, 0, 1) }, key(keyDown, "")),
		c("move-up", u.t("Move the topic before its sibling"), func() { a.mindReorder(md, -1) }, key(keyUp, "a")),
		c("move-down", u.t("Move the topic after its sibling"), func() { a.mindReorder(md, 1) }, key(keyDown, "a")),
		c("connect", u.t("Connect the chosen nodes"), func() { a.mindConnectChosen(md) }, key('L', "c")),
		c("bold", u.t("Bold"), func() { a.mindToggleBold(md) }, key('B', "c")),
		c("bigger", u.t("Larger words"), func() { a.mindStepSize(md, 1) }, key(0xdd, "c")),
		c("smaller", u.t("Smaller words"), func() { a.mindStepSize(md, -1) }, key(0xdb, "c")),
		c("fit", u.t("Show the whole board"), func() { md.fit = true }, key('1', "s")),
		c("fit-selection", u.t("Zoom to the chosen nodes"), func() { a.mindZoomToSelection(md, a.layout().body) }, key('2', "s")),
		c("actual-size", u.t("Actual size"), func() { md.zoomAt(a.layout().body.W/2, a.layout().body.H/2, 1) }, key('0', "s")),
		c("save", u.t("Save now"), func() { md.dirty = true; a.saveMind(md, false) }, key('S', "c")),
		c("export", u.t("Export as a picture…"), func() { a.exportMindPicture(md) }, key('E', "cs")),
		c("copy-outline", u.t("Copy as an outline"), func() { a.mindCopyOutline(md) }, key('C', "cs")),
		c("escape", u.t("Stop: drop the choice and the tool"), func() { a.mindEscape(md) }, key(keyEscape, "")),
	}
}

// keyInsert is the Insert key, which adds a child topic in XMind.
const keyInsert = 0x2d

// mindShortcuts lists the map tab's commands for Settings.
func (a *App) mindShortcuts() []shortcut {
	var out []shortcut
	for _, c := range a.mindCommands() {
		out = append(out, c.shortcut)
	}
	return out
}

// mindKeyHint is the first key of a map tab command, for hints.
func (a *App) mindKeyHint(id string) string {
	for _, c := range a.mindCommands() {
		if c.id == id {
			return a.keyHint(c.id, c.defaults)
		}
	}
	return ""
}

// mindKeys runs the map tab commands bound to the keys pressed, starts typing into the chosen topic when a letter
// is typed, and lets the other keys through to the general shortcuts.
func (a *App) mindKeys(md *mindDoc, r gfx.Rect) {
	u := &a.u
	cmds := a.mindCommands()
	var rest []Key
	for _, k := range u.keys {
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
		if md.text != nil || md.cell != nil {
			u.keys = nil
			return // what follows types into what the command opened
		}
	}
	u.keys = rest
	if len(u.typed) > 0 && !u.ctrl && md.text == nil && md.cell == nil {
		// Space alone is held to move the view, not typed.
		typed := strings.TrimFunc(string(u.typed), func(r rune) bool { return r < 0x20 })
		if n := md.m.Node(md.primary); n != nil && strings.TrimSpace(typed) != "" && len(md.sel) == 1 && n.Kind != mind.Table {
			u.typed = nil
			a.editMindText(md, n.ID, typed, false)
		}
	}
}

// mindToolLabel names a tool and gives its icon.
func (a *App) mindToolLabel(t mindTool) (string, string) {
	u := &a.u
	switch t {
	case mtMap:
		return u.t("New mind map: click on the board"), icNewMap
	case mtTopic:
		return u.t("Topic: click on the board"), icTopic
	case mtShape:
		return u.t("Flowchart shape: click on the board"), icShapeTool
	case mtText:
		return u.t("Text: click on the board"), icTextTool
	case mtTable:
		return u.t("Table: click on the board"), icTable
	case mtPicture:
		return u.t("Add pictures, animations or canvases…"), icPicture
	case mtVideo:
		return u.t("Add videos…"), icVideoNode
	case mtNested:
		return u.t("Add a mind map inside this one…"), icMind
	}
	return u.t("Choose and move"), icSelect
}

// pickMindTool chooses a tool from the tool bar; the file tools ask for files at once, the shape tool for a shape.
func (a *App) pickMindTool(md *mindDoc, t mindTool, at gfx.Rect) {
	u := &a.u
	a.commitMindText(md)
	a.commitMindCell(md)
	switch t {
	case mtPicture, mtVideo, mtNested:
		md.tool = mtSelect
		a.mindChooseFiles(md, t)
	case mtShape:
		var items []menuItem
		for _, s := range mindShapes {
			items = append(items, menuItem{label: u.t(mindShapeNames[s]), checked: md.shape == s, run: func() {
				md.tool, md.shape = mtShape, s
			}})
		}
		a.openMenu("mind/shapes", at, items)
	default:
		if md.tool == t {
			md.tool = mtSelect
		} else {
			md.tool = t
		}
	}
}

// mindShapes are the shapes offered, named by mindShapeNames.
var (
	mindShapes = []mind.Shape{mind.ShapeRounded, mind.ShapeRect, mind.ShapeCapsule, mind.ShapeEllipse, mind.ShapeDiamond, mind.ShapeParallelogram, mind.ShapeHexagon, mind.ShapeUnderline, mind.ShapeNone}
	// mindShapeNames name the shapes, in mind.Shape's order.
	mindShapeNames = msgs("As the map's place", "Rounded box (process)", "Box", "Pill (start or end)", "Ellipse",
		"Diamond (decision)", "Slanted box (input or output)", "Hexagon (preparation)", "Underline", "No outline (text)")
	mindLayoutNames = msgs("Mind map", "Logic chart to the right", "Logic chart to the left", "Org chart down", "Org chart up", "Outline")
	mindRouteNames  = msgs("Curve", "Right angles", "Straight")
	mindArrowNames  = msgs("Arrow at the end", "No arrows", "Arrows at both ends", "Arrow at the start")
	mindColorNames  = msgs("Blue", "Orange", "Green", "Purple", "Yellow", "Red", "Cyan", "Pink")
)

// mindChooseFiles asks for files of the tool's kind and adds them where the view is.
func (a *App) mindChooseFiles(md *mindDoc, t mindTool) {
	u := &a.u
	var title string
	var filter [][2]string
	switch t {
	case mtPicture:
		title = u.t("Add pictures, animations or canvases")
		exts := append(append(slices.Clone(assets.ImageExtensions), assets.AnimationExtensions...), paint.Ext)
		filter = [][2]string{{u.t("Pictures"), extPattern(exts)}}
	case mtVideo:
		title = u.t("Add videos")
		filter = [][2]string{{u.t("Videos"), extPattern(assets.VideoExtensions)}}
	default:
		title = u.t("Add a mind map inside this one")
		filter = [][2]string{{u.t("Mind maps"), "*" + mind.Ext}}
	}
	paths := a.host.OpenFiles(title, filter, true)
	if len(paths) > 0 {
		a.mindAddFiles(md, paths, a.mindViewCentre(md))
	}
}

// extPattern is a file dialog's pattern for extensions.
func extPattern(exts []string) string {
	var parts []string
	for _, e := range exts {
		parts = append(parts, "*"+e)
	}
	return strings.Join(parts, ";")
}

// mindViewCentre is the board point in the middle of the view.
func (a *App) mindViewCentre(md *mindDoc) mind.Vec {
	b := a.layout().body
	return md.view.Board(float64(b.W)/2, float64(b.H)/2)
}

// mindPointerOr is the board point under the pointer when it is on the board, else the middle of the view.
func (a *App) mindPointerOr(md *mindDoc) mind.Vec {
	b := a.layout().body
	if b.Contains(a.u.mx, a.u.my) {
		return md.pointer(&a.u, b)
	}
	return a.mindViewCentre(md)
}

// mindKindOf is the kind of node a file is shown by, and ok when it can be shown.
func mindKindOf(path string) (mind.Kind, bool) {
	switch assets.KindOf(path) {
	case assets.Image, assets.Animation, assets.Canvas:
		return mind.Picture, true
	case assets.Video:
		return mind.Video, true
	case assets.MindMap:
		return mind.Nested, true
	}
	return 0, false
}

// mindAddFiles adds a node for each file, side by side from the board point at, as one step; those that cannot be
// shown are named in a word to the user.
func (a *App) mindAddFiles(md *mindDoc, paths []string, at mind.Vec) {
	u := &a.u
	m := md.m
	var skipped []string
	var added []mind.ID
	m.Begin(u.t("Add"))
	x := at.X
	for _, p := range paths {
		kind, ok := mindKindOf(p)
		if !ok {
			skipped = append(skipped, filepath.Base(p))
			continue
		}
		id, err := m.Add(mind.Node{Kind: kind, Ref: p, X: math.Round(x), Y: math.Round(at.Y)}, 0, -1)
		if err != nil {
			a.toast(a.describe(err), true)
			continue
		}
		added = append(added, id)
		m.Layout()
		x += m.Box(m.Node(id)).W() + 24
	}
	a.mindMakeRoom(md, added)
	m.Commit()
	if len(added) > 0 {
		clear(md.sel)
		for _, id := range added {
			md.sel[id] = true
		}
		md.primary = added[len(added)-1]
	}
	if len(skipped) > 0 {
		a.toast(u.tf("These files cannot be shown on a mind map: %s", strings.Join(skipped, ", ")), true)
	}
}

// mindAddChild adds a topic under the chosen one and types into it.
func (a *App) mindAddChild(md *mindDoc) {
	n := md.m.Node(md.primary)
	if n == nil {
		return
	}
	id, err := md.m.Add(mind.Node{}, n.ID, -1)
	if a.mindDo(md, err) {
		md.selectOnly(id)
		a.editMindText(md, id, "", false)
	}
}

// mindAddSibling adds a topic beside the chosen one, after or before it; beside a free node it adds a child.
func (a *App) mindAddSibling(md *mindDoc, after bool) {
	n := md.m.Node(md.primary)
	if n == nil {
		return
	}
	if n.Parent == 0 {
		a.mindAddChild(md)
		return
	}
	p := md.m.Node(n.Parent)
	i := slices.Index(p.Children, n.ID)
	if after {
		i++
	}
	id, err := md.m.Add(mind.Node{Side: n.Side}, p.ID, i)
	if a.mindDo(md, err) {
		md.selectOnly(id)
		a.editMindText(md, id, "", false)
	}
}

// mindAddParent puts a new topic between the chosen one and its parent.
func (a *App) mindAddParent(md *mindDoc) {
	m := md.m
	n := m.Node(md.primary)
	if n == nil {
		return
	}
	m.Begin(a.u.t("Add"))
	var id mind.ID
	var err error
	if n.Parent == 0 {
		pos := m.Pos(n)
		id, err = m.Add(mind.Node{X: pos.X, Y: pos.Y, Layout: n.Layout}, 0, -1)
		if err == nil {
			err = m.Move(n.ID, id, -1, 0, 0)
		}
	} else {
		p := m.Node(n.Parent)
		id, err = m.Add(mind.Node{Side: n.Side}, p.ID, slices.Index(p.Children, n.ID))
		if err == nil {
			err = m.Move(n.ID, id, -1, 0, 0)
		}
	}
	m.Commit()
	if a.mindDo(md, err) {
		md.selectOnly(id)
		a.editMindText(md, id, "", false)
	}
}

// mindEditChosen types into the chosen node, or the chosen connector's label.
func (a *App) mindEditChosen(md *mindDoc) {
	if e := md.m.Edge(md.edge); e != nil {
		a.editEdgeLabel(md, e)
		return
	}
	n := md.m.Node(md.primary)
	if n == nil {
		return
	}
	if n.Kind == mind.Table {
		a.editMindCell(md, n.ID, 0, 0)
		return
	}
	a.editMindText(md, n.ID, "", false)
}

// mindDelete deletes the chosen nodes with their subtrees, or the chosen connector; the parent of a lone deleted
// topic is chosen next, as in XMind.
func (a *App) mindDelete(md *mindDoc) {
	m := md.m
	if md.edge != 0 {
		a.mindDo(md, m.DeleteEdge(md.edge))
		md.edge = 0
		return
	}
	ids := md.chosen()
	if len(ids) == 0 {
		return
	}
	next := mind.ID(0)
	if n := m.Node(md.primary); n != nil && len(ids) == 1 {
		next = n.Parent
	}
	if a.mindDo(md, m.Delete(ids...)) {
		md.selectOnly(next)
	}
}

// mindSelectAll chooses every node shown.
func (a *App) mindSelectAll(md *mindDoc) {
	clear(md.sel)
	for _, n := range md.m.Visible() {
		md.sel[n.ID] = true
		md.primary = n.ID
	}
	md.edge = 0
}

// mindUndo and mindRedo take the map's last step back or make it again.
func (a *App) mindUndo(md *mindDoc) {
	a.commitMindText(md)
	a.commitMindCell(md)
	if md.m.Undo() {
		md.pruneSelection()
	}
}

func (a *App) mindRedo(md *mindDoc) {
	a.commitMindText(md)
	a.commitMindCell(md)
	if md.m.Redo() {
		md.pruneSelection()
	}
}

// mindFold folds the chosen topics that have children, or unfolds them when all are folded.
func (a *App) mindFold(md *mindDoc) {
	m := md.m
	var ids []mind.ID
	all := true
	for _, id := range md.chosen() {
		if n := m.Node(id); n != nil && len(n.Children) > 0 {
			ids = append(ids, id)
			all = all && n.Folded
		}
	}
	if len(ids) == 0 {
		return
	}
	m.Begin(a.u.t("Fold"))
	for _, id := range ids {
		m.Update(id, func(n *mind.Node) { n.Folded = !all })
	}
	m.Commit()
}

// mindGo chooses the nearest node in the direction dx, dy from the chosen one.
func (a *App) mindGo(md *mindDoc, dx, dy float64) {
	m := md.m
	n := m.Node(md.primary)
	if n == nil {
		if ns := m.Visible(); len(ns) > 0 {
			md.selectOnly(ns[0].ID)
		}
		return
	}
	b := m.Box(n)
	reach := 3000.0
	area := mind.Rect{X0: b.X0 - reach, Y0: b.Y0 - reach, X1: b.X1 + reach, Y1: b.Y1 + reach}
	best, bestD := mind.ID(0), math.Inf(1)
	for _, c := range m.In(area, true) {
		if c.ID == n.ID {
			continue
		}
		cb := m.Box(c)
		ox, oy := cb.CX()-b.CX(), cb.CY()-b.CY()
		along := ox*dx + oy*dy
		if along <= 0 {
			continue
		}
		// Gaps rather than centres: a node beside this one counts as near however wide it is.
		gx := math.Max(0, math.Max(cb.X0-b.X1, b.X0-cb.X1))
		gy := math.Max(0, math.Max(cb.Y0-b.Y1, b.Y0-cb.Y1))
		across := math.Abs(ox*dy) + math.Abs(oy*dx)
		d := math.Hypot(gx, gy) + across*2
		if d < bestD {
			best, bestD = c.ID, d
		}
	}
	if best != 0 {
		md.selectOnly(best)
		a.mindKeepInView(md, m.Node(best))
	}
}

// mindKeepInView moves the view so node n shows.
func (a *App) mindKeepInView(md *mindDoc, n *mind.Node) {
	r := a.layout().body
	sb := md.view.ScreenRect(md.m.Box(n))
	margin := float32(60)
	dx, dy := float32(0), float32(0)
	if sb.X < margin {
		dx = margin - sb.X
	} else if sb.Right() > r.W-margin {
		dx = r.W - margin - sb.Right()
	}
	if sb.Y < margin+40 {
		dy = margin + 40 - sb.Y
	} else if sb.Bottom() > r.H-margin {
		dy = r.H - margin - sb.Bottom()
	}
	md.view.OX = math.Round(md.view.OX + float64(dx))
	md.view.OY = math.Round(md.view.OY + float64(dy))
}

// mindReorder moves the chosen topic before (-1) or after (1) its neighbour among its siblings.
func (a *App) mindReorder(md *mindDoc, step int) {
	m := md.m
	n := m.Node(md.primary)
	if n == nil || n.Parent == 0 {
		return
	}
	p := m.Node(n.Parent)
	i := slices.Index(p.Children, n.ID) + step
	if i < 0 || i >= len(p.Children) {
		return
	}
	a.mindDo(md, m.Move(n.ID, p.ID, i, 0, 0))
}

// mindConnectChosen connects the chosen nodes one to the next, in the order they lie on the board.
func (a *App) mindConnectChosen(md *mindDoc) {
	m := md.m
	ids := md.chosen()
	if len(ids) < 2 {
		a.toast(a.u.t("Choose two nodes or more to connect them"), false)
		return
	}
	slices.SortFunc(ids, func(x, y mind.ID) int {
		bx, by := m.Box(m.Node(x)), m.Box(m.Node(y))
		if math.Abs(bx.CY()-by.CY()) > 20 {
			return int(math.Copysign(1, bx.CY()-by.CY()))
		}
		return int(math.Copysign(1, bx.CX()-by.CX()))
	})
	m.Begin(a.u.t("Connect"))
	for i := 1; i < len(ids); i++ {
		if _, err := m.AddEdge(mind.Edge{From: ids[i-1], To: ids[i]}); err != nil {
			a.toast(a.describe(err), true)
			break
		}
	}
	m.Commit()
}

// mindStyleEach changes every chosen node with fn, as one step.
func (a *App) mindStyleEach(md *mindDoc, fn func(n *mind.Node)) {
	m := md.m
	m.Begin(a.u.t("Change"))
	for _, id := range md.chosen() {
		m.Update(id, fn)
	}
	m.Commit()
}

// mindToggleBold makes the chosen nodes' words bold, or not when all are.
func (a *App) mindToggleBold(md *mindDoc) {
	all := true
	for _, id := range md.chosen() {
		all = all && md.m.Node(id).Bold
	}
	a.mindStyleEach(md, func(n *mind.Node) { n.Bold = !all })
}

// mindStepSize makes the chosen nodes' words a step larger or smaller.
func (a *App) mindStepSize(md *mindDoc, step int) {
	sizes := []float32{10, 11, 12, 13, 14, 15, 16, 18, 20, 22, 24, 28, 32, 36, 42, 48, 60, 72}
	a.mindStyleEach(md, func(n *mind.Node) {
		_, _, _, _, cur := md.m.Look(n)
		i, _ := slices.BinarySearch(sizes, cur)
		if step > 0 {
			for i < len(sizes) && sizes[i] <= cur {
				i++
			}
			n.Size = sizes[min(i, len(sizes)-1)]
		} else {
			n.Size = sizes[max(i-1, 0)]
		}
	})
}

// mindZoomToSelection shows the chosen nodes, or everything when none is chosen, in the board r.
func (a *App) mindZoomToSelection(md *mindDoc, r gfx.Rect) {
	ids := md.chosen()
	if len(ids) == 0 {
		md.fit = true
		return
	}
	b := md.m.Box(md.m.Node(ids[0]))
	for _, id := range ids[1:] {
		b = b.Union(md.m.Box(md.m.Node(id)))
	}
	md.fitTo(b.Grow(40), r.W, r.H)
}

// mindEscape drops the tool, the chosen connector and the choice, in that order.
func (a *App) mindEscape(md *mindDoc) {
	switch {
	case md.tool != mtSelect:
		md.tool = mtSelect
	case md.video != 0:
		md.video = 0
		a.closeFilePreview()
	default:
		md.selectOnly(0)
	}
}

// mindClipboard is what was copied from a map: its nodes, and the outline put on the system clipboard with them,
// by which a paste knows the clipboard still holds them.
type mindClipboard struct {
	m       *mind.Map
	outline string
}

// mindCopy copies the chosen nodes with their subtrees, and their outline as text; cut deletes them.
func (a *App) mindCopy(md *mindDoc, cut bool) {
	ids := md.chosen()
	if len(ids) == 0 {
		return
	}
	tops := md.m.Tops(ids)
	clip := &mindClipboard{m: md.m.Extract(ids), outline: md.m.Outline(tops...)}
	a.mindClip = clip
	if err := a.host.SetClipboard(clip.outline); err != nil {
		a.toast(a.u.tf("Could not copy: %s", a.describe(err)), true)
	}
	if cut {
		a.mindDelete(md)
	}
}

// mindCopyOutline copies the chosen subtrees, or the whole board, as a Markdown outline.
func (a *App) mindCopyOutline(md *mindDoc) {
	ids := md.m.Tops(md.chosen())
	if len(ids) == 0 {
		for _, n := range md.m.Roots() {
			ids = append(ids, n.ID)
		}
	}
	if err := a.host.SetClipboard(md.m.Outline(ids...)); err != nil {
		a.toast(a.u.tf("Could not copy: %s", a.describe(err)), true)
		return
	}
	a.toast(a.u.t("Copied as an outline"), false)
}

// mindDuplicate copies the chosen subtrees beside themselves.
func (a *App) mindDuplicate(md *mindDoc) {
	m := md.m
	ids := md.chosen()
	if len(ids) == 0 {
		return
	}
	m.Begin(a.u.t("Duplicate"))
	var added []mind.ID
	var err error
	for _, top := range m.Tops(ids) {
		n := m.Node(top)
		one := m.Extract([]mind.ID{top})
		if n.Parent != 0 {
			p := m.Node(n.Parent)
			var got []mind.ID
			got, err = m.Insert(one, p.ID, slices.Index(p.Children, top)+1, mind.Vec{})
			added = append(added, got...)
		} else {
			b := m.SubBox(n)
			var got []mind.ID
			got, err = m.Insert(one, 0, -1, mind.Vec{X: b.X0 + 30, Y: b.Y1 + 30})
			added = append(added, got...)
		}
		if err != nil {
			break
		}
	}
	m.Commit()
	if a.mindDo(md, err) && len(added) > 0 {
		clear(md.sel)
		for _, id := range added {
			md.sel[id] = true
		}
		md.primary = added[len(added)-1]
	}
}

// mindPaste pastes what the clipboard holds: nodes copied from a map, a smart object copied from a canvas, a
// picture, files, a table copied from a spreadsheet, an outline or words. They go under the chosen node when one is
// chosen and they are topics, else free where the pointer is.
func (a *App) mindPaste(md *mindDoc) {
	u := &a.u
	m := md.m
	img, files, text := a.host.Paste()
	at := a.mindPointerOr(md)
	parent := mind.ID(0)
	if n := m.Node(md.primary); n != nil && len(md.sel) == 1 {
		parent = n.ID
	}
	m.Begin(u.t("Paste"))
	defer m.Commit()
	choose := func(ids []mind.ID, err error) {
		if !a.mindDo(md, err) || len(ids) == 0 {
			return
		}
		a.mindMakeRoom(md, ids)
		clear(md.sel)
		for _, id := range ids {
			md.sel[id] = true
		}
		md.primary = ids[len(ids)-1]
	}
	if c := a.mindClip; c != nil && img == nil && len(files) == 0 && text == c.outline {
		choose(m.Insert(c.m, parent, -1, at))
		return
	}
	if n := a.pastedNode(img, files); n != nil && n.IsSmart() {
		id, err := a.mindAddCentred(md, mind.Node{Kind: mind.Smart, Ref: n.Smart.ID, Text: n.Name}, at)
		choose([]mind.ID{id}, err)
		return
	}
	switch {
	case len(files) > 0:
		a.mindAddFiles(md, files, at)
	case img != nil:
		path, err := a.storeImage(img)
		if err != nil {
			a.toast(u.tf("The pasted image could not be saved: %v", err), true)
			return
		}
		id, err := a.mindAddCentred(md, mind.Node{Kind: mind.Picture, Ref: path}, at)
		choose([]mind.ID{id}, err)
	case strings.TrimSpace(text) == "":
	default:
		if g, ok := mind.ParseTSV(text); ok {
			id, err := a.mindAddCentred(md, mind.Node{Kind: mind.Table, Cells: g}, at)
			choose([]mind.ID{id}, err)
			return
		}
		choose(m.AddOutline(strings.TrimSpace(text), parent, -1, at.X, at.Y))
	}
}

// openSmartByID opens the smart object's own file of link id in a canvas tab.
func (a *App) openSmartByID(id string) {
	if src := a.sourceTab(id); src != nil {
		for i, d := range a.docs {
			if d.art == src {
				a.switchTab(i)
			}
		}
		return
	}
	path := a.smartPath(id)
	if _, err := os.Stat(path); err != nil {
		if c, ok := a.currentSmart(id, nil); ok {
			var buf bytes.Buffer
			if err := paint.SaveSmart(&buf, c); err == nil {
				os.MkdirAll(a.smartDir(), 0o755)
				writeFileWhole(path, buf.Bytes())
			}
		}
	}
	if _, err := os.Stat(path); err != nil {
		a.toast(a.u.tf("Smart object %s is not kept any more; open a canvas that holds it, then paste it again", id), true)
		return
	}
	a.openCanvasFileWith(path, nil)
}

// exportMindPicture writes the whole board as a PNG beside the map, at full size up to 8192 pixels a side.
func (a *App) exportMindPicture(md *mindDoc) {
	u := &a.u
	m := md.m
	b := m.Bounds()
	if b.Empty() {
		a.toast(u.t("The board is empty"), false)
		return
	}
	scale := math.Min(1, 8192/math.Max(b.W(), b.H()))
	w, h := int(b.W()*scale)+64, int(b.H()*scale)+64
	out := strings.TrimSuffix(md.path, mind.Ext) + ".png"
	img := mind.RenderFit(m, w, h, &mind.Options{Fonts: u.fonts, Pictures: mindExportPictures{a, md.path}, Labels: a.mindLabels()})
	a.background(func(context.Context) {
		err := writePNG(out, img)
		a.post(func() {
			if err != nil {
				a.toast(u.tf("Could not export: %s", a.describe(err)), true)
				return
			}
			a.toastActions(u.tf("Exported %s", filepath.Base(out)), false, []toastAction{{u.t("Show in folder"), func() { a.host.Reveal(out) }}})
		})
	})
}

// mindExportPictures gives an export its pictures read at once, at the size they are drawn.
type mindExportPictures struct {
	a     *App
	chain string
}

func (p mindExportPictures) Picture(n *mind.Node, px int) (*image.RGBA, mind.PictureState, bool) {
	if n.Kind == mind.Nested {
		t := mind.DrawNested(n.Ref, []string{p.chain}, px, p.a.u.fonts, p.a.mindLabels(), mindSource{p.a})
		return t.Img, t.State, false
	}
	if img := (mindSource{p.a}).Still(n, px); img != nil {
		return img, mind.PicReady, false
	}
	return nil, mind.PicMissing, false
}

// mindStyleItem is a control on the style bar.
type mindStyleItem struct {
	id, glyph, text, hint string
	on                    bool
	run                   func(at gfx.Rect)
}

func (it mindStyleItem) width(u *ctx) float32 {
	if it.text == "" {
		return u.px(34)
	}
	return u.buttonWidth(it.glyph, it.text) + u.px(4)
}

// mindStyleItems are the style bar's controls for what is chosen: a connector's route, arrows, dashes and colour,
// or the nodes' shape, colour, words and a root's layout.
func (a *App) mindStyleItems(md *mindDoc) []mindStyleItem {
	u := &a.u
	m := md.m
	if e := m.Edge(md.edge); e != nil {
		return []mindStyleItem{
			{id: "route", glyph: icConnect, text: u.t(mindRouteNames[e.Route]), run: func(at gfx.Rect) { a.openMenu("mind/route", at, a.mindRouteMenu(md, e)) }},
			{id: "arrow", glyph: icArrowEnd, text: u.t(mindArrowNames[e.Arrow]), run: func(at gfx.Rect) { a.openMenu("mind/arrow", at, a.mindArrowMenu(md, e)) }},
			{id: "dashed", glyph: icDashed, hint: u.t("Dashed"), on: e.Dashed, run: func(gfx.Rect) {
				a.mindDo(md, m.UpdateEdge(e.ID, func(x *mind.Edge) { x.Dashed = !x.Dashed }))
			}},
			{id: "colour", glyph: icPalette, hint: u.t("Colour"), run: func(at gfx.Rect) { a.openMenu("mind/edge-colour", at, a.mindEdgeColourMenu(md, e)) }},
			{id: "label", glyph: icTextTool, hint: u.t("Label"), run: func(gfx.Rect) { a.editEdgeLabel(md, e) }},
			{id: "delete", glyph: icDelete, hint: u.t("Delete"), run: func(gfx.Rect) { a.mindDelete(md) }},
		}
	}
	ids := md.chosen()
	if len(ids) == 0 {
		return nil
	}
	n := m.Node(md.primary)
	if n == nil {
		n = m.Node(ids[0])
	}
	items := []mindStyleItem{}
	if n.Kind == mind.Topic {
		shape, _, _, _, _ := m.Look(n)
		items = append(items, mindStyleItem{id: "shape", glyph: icShapeTool, hint: u.t("Shape"), text: u.t(shortShape(shape)), run: func(at gfx.Rect) {
			a.openMenu("mind/shape", at, a.mindShapeMenu(md))
		}})
	}
	items = append(items,
		mindStyleItem{id: "colour", glyph: icPalette, hint: u.t("Colour"), run: func(at gfx.Rect) { a.openMenu("mind/colour", at, a.mindColourMenu(md)) }},
		mindStyleItem{id: "bold", glyph: icBold, hint: withKey(u.t("Bold"), a.mindKeyHint("mind.bold")), on: n.Bold, run: func(gfx.Rect) { a.mindToggleBold(md) }},
		mindStyleItem{id: "smaller", glyph: icTextSmaller, hint: withKey(u.t("Smaller words"), a.mindKeyHint("mind.smaller")), run: func(gfx.Rect) { a.mindStepSize(md, -1) }},
		mindStyleItem{id: "bigger", glyph: icTextBigger, hint: withKey(u.t("Larger words"), a.mindKeyHint("mind.bigger")), run: func(gfx.Rect) { a.mindStepSize(md, 1) }},
	)
	if n.Parent == 0 && len(n.Children) > 0 {
		items = append(items, mindStyleItem{id: "layout", glyph: icMind, text: u.t(mindLayoutNames[n.Layout]), run: func(at gfx.Rect) {
			a.openMenu("mind/layout", at, a.mindLayoutMenu(md, n))
		}})
	}
	if len(n.Children) > 0 {
		items = append(items, mindStyleItem{id: "fold", glyph: icFold, hint: withKey(u.t("Fold or unfold"), a.mindKeyHint("mind.fold")), on: n.Folded,
			run: func(gfx.Rect) { a.mindFold(md) }})
	}
	if len(ids) >= 2 {
		items = append(items, mindStyleItem{id: "connect", glyph: icConnect, hint: withKey(u.t("Connect the chosen nodes"), a.mindKeyHint("mind.connect")),
			run: func(gfx.Rect) { a.mindConnectChosen(md) }})
	}
	items = append(items, mindStyleItem{id: "more", glyph: icMore, hint: u.t("More"), run: func(at gfx.Rect) {
		a.openMenu("mind/node-more", at, a.mindNodeMenu(md, n))
	}})
	return items
}

// shortShape names a shape briefly for the style bar.
func shortShape(s mind.Shape) string {
	name := mindShapeNames[s]
	if i := strings.Index(name, " ("); i > 0 {
		return name[:i]
	}
	return name
}

// mindStyleAnchor is the screen box the style bar sits above: the chosen connector's label, or the chosen nodes.
func (a *App) mindStyleAnchor(md *mindDoc, r gfx.Rect) gfx.Rect {
	v := md.screenView(r)
	if e := md.m.Edge(md.edge); e != nil {
		if _, label, ok := md.m.EdgePath(e); ok {
			x, y := v.Screen(label)
			return gfx.Rect{X: x, Y: y - 10, W: 0, H: 20}
		}
	}
	ids := md.chosen()
	if len(ids) == 0 {
		return gfx.Rect{X: r.X + r.W/2, Y: r.Y + r.H/2}
	}
	b := md.m.Box(md.m.Node(ids[0]))
	for _, id := range ids[1:] {
		b = b.Union(md.m.Box(md.m.Node(id)))
	}
	sr := v.ScreenRect(b)
	return gfx.Rect{X: sr.X + sr.W/2, Y: sr.Y, W: 0, H: sr.H}
}

// drawMindStyle draws the style bar.
func (a *App) drawMindStyle(md *mindDoc, r gfx.Rect) {
	if r.W == 0 {
		return
	}
	u := &a.u
	floatPanel(u, r)
	x := r.X + u.px(4)
	for _, it := range a.mindStyleItems(md) {
		w := it.width(u)
		b := gfx.Rect{X: x, Y: r.Y + u.px(3), W: w, H: r.H - u.px(6)}
		id := "mind/style/" + it.id
		var hit bool
		if it.text == "" {
			hit = u.iconButton(id, b, it.glyph, it.hint, it.on, true)
		} else {
			hot := u.hover(id, b)
			if hot || it.on {
				u.c.RoundRect(b, u.px(6), colSurfaceHi)
				u.cursor = CursorHand
			}
			u.icon(it.glyph, gfx.Rect{X: b.X + u.px(6), Y: b.Y, W: u.px(18), H: b.H}, 14, colDim)
			u.label(u.face(12.5), gfx.Rect{X: b.X + u.px(28), Y: b.Y, W: b.W - u.px(32), H: b.H}, it.text, colText, 0)
			u.hint(id, it.hint, b)
			hit = u.clicked(id, b)
		}
		if hit {
			it.run(gfx.Rect{X: b.X, Y: r.Y, W: b.W, H: r.H})
		}
		x += w
	}
}

// mindShapeMenu sets the chosen topics' shape.
func (a *App) mindShapeMenu(md *mindDoc) []menuItem {
	u := &a.u
	cur := mind.ShapeAuto
	if n := md.m.Node(md.primary); n != nil {
		cur = n.Shape
	}
	var items []menuItem
	for _, s := range append([]mind.Shape{mind.ShapeAuto}, mindShapes...) {
		items = append(items, menuItem{label: u.t(mindShapeNames[s]), checked: cur == s, run: func() {
			a.mindStyleEach(md, func(n *mind.Node) {
				if n.Kind == mind.Topic {
					n.Shape = s
				}
			})
		}})
	}
	return items
}

// mindColours are the colours offered: the branches' colours, in turn.
func mindColours() []color.RGBA {
	out := make([]color.RGBA, len(mindColorNames))
	for i := range out {
		out[i] = mind.BranchColor(i)
	}
	return out
}

// mindColourMenu colours the chosen nodes: filled for a box, their line for a leaf.
func (a *App) mindColourMenu(md *mindDoc) []menuItem {
	u := &a.u
	items := []menuItem{{label: u.t("As the map's place"), run: func() {
		a.mindStyleEach(md, func(n *mind.Node) { n.Fill, n.Line, n.Ink = color.RGBA{}, color.RGBA{}, color.RGBA{} })
	}}, menuSep}
	for i, c := range mindColours() {
		items = append(items, menuItem{label: u.t(mindColorNames[i]), run: func() {
			a.mindStyleEach(md, func(n *mind.Node) {
				shape, _, _, _, _ := md.m.Look(n)
				n.Line = c
				if shape != mind.ShapeUnderline && shape != mind.ShapeNone {
					n.Fill = gfx.Mix(mind.Paper, c, 0.3)
				}
			})
		}})
	}
	items = append(items, menuSep, menuItem{label: u.t("Light box, dark words"), run: func() {
		a.mindStyleEach(md, func(n *mind.Node) { n.Fill, n.Ink = gfx.Hex(0xe6e7ea), gfx.Hex(0x111216) })
	}})
	return items
}

// mindLayoutMenu sets how a root lays out its tree.
func (a *App) mindLayoutMenu(md *mindDoc, n *mind.Node) []menuItem {
	u := &a.u
	var items []menuItem
	for l, name := range mindLayoutNames {
		items = append(items, menuItem{label: u.t(name), checked: n.Layout == mind.Layout(l), run: func() {
			a.mindDo(md, md.m.Update(n.ID, func(x *mind.Node) { x.Layout = mind.Layout(l) }))
		}})
	}
	return items
}

func (a *App) mindRouteMenu(md *mindDoc, e *mind.Edge) []menuItem {
	u := &a.u
	var items []menuItem
	for r, name := range mindRouteNames {
		items = append(items, menuItem{label: u.t(name), checked: e.Route == mind.Route(r), run: func() {
			a.mindDo(md, md.m.UpdateEdge(e.ID, func(x *mind.Edge) { x.Route = mind.Route(r) }))
		}})
	}
	return items
}

func (a *App) mindArrowMenu(md *mindDoc, e *mind.Edge) []menuItem {
	u := &a.u
	var items []menuItem
	for r, name := range mindArrowNames {
		items = append(items, menuItem{label: u.t(name), checked: e.Arrow == mind.Arrow(r), run: func() {
			a.mindDo(md, md.m.UpdateEdge(e.ID, func(x *mind.Edge) { x.Arrow = mind.Arrow(r) }))
		}})
	}
	items = append(items, menuSep, menuItem{label: u.t("Reverse"), run: func() {
		a.mindDo(md, md.m.UpdateEdge(e.ID, func(x *mind.Edge) { x.From, x.To, x.FromSide, x.ToSide = x.To, x.From, x.ToSide, x.FromSide }))
	}})
	return items
}

func (a *App) mindEdgeColourMenu(md *mindDoc, e *mind.Edge) []menuItem {
	u := &a.u
	items := []menuItem{{label: u.t("Grey"), checked: e.Color.A == 0, run: func() {
		a.mindDo(md, md.m.UpdateEdge(e.ID, func(x *mind.Edge) { x.Color = color.RGBA{} }))
	}}}
	for i, c := range mindColours() {
		items = append(items, menuItem{label: u.t(mindColorNames[i]), checked: e.Color == c, run: func() {
			a.mindDo(md, md.m.UpdateEdge(e.ID, func(x *mind.Edge) { x.Color = c }))
		}})
	}
	return items
}

// mindNodeMenu are the commands for the chosen node, as its context menu shows them.
func (a *App) mindNodeMenu(md *mindDoc, n *mind.Node) []menuItem {
	u := &a.u
	m := md.m
	key := a.mindKeyHint
	items := []menuItem{
		{label: u.t("Type into it"), key: key("mind.edit"), run: func() { a.mindEditChosen(md) }},
		{label: u.t("Add a child topic"), key: key("mind.add-child"), run: func() { a.mindAddChild(md) }},
		{label: u.t("Add a topic after this one"), key: key("mind.add-sibling"), run: func() { a.mindAddSibling(md, true) }, disabled: n.Parent == 0},
		{label: u.t("Add a topic above this one"), key: key("mind.add-parent"), run: func() { a.mindAddParent(md) }},
	}
	if len(n.Children) > 0 {
		label := u.t("Fold")
		if n.Folded {
			label = u.t("Unfold")
		}
		items = append(items, menuItem{label: label, key: key("mind.fold"), run: func() { a.mindFold(md) }})
	}
	if n.Parent != 0 {
		items = append(items, menuItem{label: u.t("Detach: make it free"), run: func() {
			pos := m.Pos(n)
			a.mindDo(md, m.Move(n.ID, 0, -1, pos.X+40, pos.Y+40))
		}})
	}
	switch n.Kind {
	case mind.Table:
		items = append(items, menuSep, menuItem{label: u.t("Table"), sub: a.mindTableMenu(md, n)})
	case mind.Nested:
		items = append(items, menuSep, menuItem{label: u.t("Open this mind map"), run: func() { a.openMindFile(n.Ref) }})
	case mind.Smart:
		items = append(items, menuSep, menuItem{label: u.t("Edit the smart object"), run: func() { a.openSmartByID(n.Ref) }})
	case mind.Video:
		items = append(items, menuSep, menuItem{label: u.t("Play here"), run: func() { md.video = n.ID }},
			menuItem{label: u.t("Edit the video"), run: func() { a.newTab(); a.openVideo(n.Ref, false) }})
	case mind.Picture:
		items = append(items, menuSep, menuItem{label: u.t("Open as a canvas"), run: func() { a.openAsset(n.Ref) }})
	}
	if n.Kind != mind.Topic && n.Kind != mind.Smart && n.Kind != mind.Table {
		items = append(items, menuItem{label: u.t("Show in folder"), run: func() { a.host.Reveal(n.Ref) }})
	}
	if n.W != 0 || n.H != 0 {
		items = append(items, menuItem{label: u.t("Size to fit its content"), run: func() { a.mindResetSize(md, n) }})
	}
	items = append(items, menuSep,
		menuItem{label: u.t("Cut"), key: key("mind.cut"), run: func() { a.mindCopy(md, true) }},
		menuItem{label: u.t("Copy"), key: key("mind.copy"), run: func() { a.mindCopy(md, false) }},
		menuItem{label: u.t("Copy as an outline"), key: key("mind.copy-outline"), run: func() { a.mindCopyOutline(md) }},
		menuItem{label: u.t("Duplicate"), key: key("mind.duplicate"), run: func() { a.mindDuplicate(md) }},
		menuSep,
		menuItem{label: u.t("Delete"), key: key("mind.delete"), run: func() { a.mindDelete(md) }},
	)
	return items
}

// mindTableMenu adds and removes a table's rows and columns, by the cell last typed into or the first.
func (a *App) mindTableMenu(md *mindDoc, n *mind.Node) []menuItem {
	u := &a.u
	row, col := 0, 0
	if md.tableAt.id == n.ID {
		row, col = md.tableAt.row, md.tableAt.col
	}
	grid := func(fn func(g *mind.Grid)) func() {
		return func() {
			a.mindDo(md, md.m.Update(n.ID, func(x *mind.Node) {
				if x.Cells == nil {
					x.Cells = &mind.Grid{Rows: 1, Cols: 1, Cells: []string{""}}
				}
				fn(x.Cells)
			}))
		}
	}
	return []menuItem{
		{label: u.t("Add a row above"), run: grid(func(g *mind.Grid) { g.InsertRow(row) })},
		{label: u.t("Add a row below"), run: grid(func(g *mind.Grid) { g.InsertRow(row + 1) })},
		{label: u.t("Add a column on the left"), run: grid(func(g *mind.Grid) { g.InsertCol(col) })},
		{label: u.t("Add a column on the right"), run: grid(func(g *mind.Grid) { g.InsertCol(col + 1) })},
		menuSep,
		{label: u.t("Delete the row"), run: grid(func(g *mind.Grid) { g.DeleteRow(row) }), disabled: n.Cells == nil || n.Cells.Rows <= 1},
		{label: u.t("Delete the column"), run: grid(func(g *mind.Grid) { g.DeleteCol(col) }), disabled: n.Cells == nil || n.Cells.Cols <= 1},
		menuSep,
		{label: u.t("Copy the cells"), run: func() {
			if n.Cells != nil {
				a.host.SetClipboard(mind.TSV(n.Cells))
			}
		}},
	}
}

// mindBoardMenu are the commands for a place on the board, at the board point p.
func (a *App) mindBoardMenu(md *mindDoc, p mind.Vec) []menuItem {
	u := &a.u
	place := func(t mindTool) func() { return func() { a.mindPlace(md, t, p) } }
	var shapes []menuItem
	for _, s := range mindShapes {
		shapes = append(shapes, menuItem{label: u.t(mindShapeNames[s]), run: func() {
			md.shape = s
			a.mindPlace(md, mtShape, p)
		}})
	}
	return []menuItem{
		{label: u.t("New mind map here"), run: place(mtMap)},
		{label: u.t("Topic here"), run: place(mtTopic)},
		{label: u.t("Flowchart shape"), sub: shapes},
		{label: u.t("Text here"), run: place(mtText)},
		{label: u.t("Table here"), run: place(mtTable)},
		menuSep,
		{label: u.t("Add pictures, animations or canvases…"), run: func() { a.mindChooseFiles(md, mtPicture) }},
		{label: u.t("Add videos…"), run: func() { a.mindChooseFiles(md, mtVideo) }},
		{label: u.t("Add a mind map inside this one…"), run: func() { a.mindChooseFiles(md, mtNested) }},
		{label: u.t("Smart objects"), sub: a.mindSmartMenu(md, p)},
		menuSep,
		{label: u.t("Paste"), key: a.mindKeyHint("mind.paste"), run: func() { a.mindPaste(md) }},
		{label: u.t("Choose everything"), key: a.mindKeyHint("mind.select-all"), run: func() { a.mindSelectAll(md) }},
		{label: u.t("Show the whole board"), key: a.mindKeyHint("mind.fit"), run: func() { md.fit = true }},
	}
}

// mindSmartMenu lists the smart objects kept, the latest first, to put one on the board at p; a smart object
// copied on a canvas is pasted with Ctrl+V as well.
func (a *App) mindSmartMenu(md *mindDoc, p mind.Vec) []menuItem {
	u := &a.u
	entries, _ := os.ReadDir(a.smartDir())
	type kept struct {
		id   string
		name string
		mod  int64
	}
	var list []kept
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), paint.Ext)
		if !ok {
			continue
		}
		if _, ok := paint.NormalLinkID(id); !ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		list = append(list, kept{id: id, mod: info.ModTime().UnixNano()})
	}
	slices.SortFunc(list, func(x, y kept) int {
		if x.mod > y.mod {
			return -1
		}
		return 1
	})
	list = list[:min(len(list), 16)]
	var items []menuItem
	for _, k := range list {
		name := k.id
		if c, err := a.readSmart(k.id); err == nil && c.Name != "" {
			name = c.Name + " #" + k.id
		}
		items = append(items, menuItem{label: name, run: func() {
			id, err := a.mindAddCentred(md, mind.Node{Kind: mind.Smart, Ref: k.id}, p)
			if a.mindDo(md, err) {
				md.selectOnly(id)
			}
		}})
	}
	if len(items) == 0 {
		items = append(items, menuItem{label: u.t("None yet: copy a smart object on a canvas, then paste it here"), disabled: true})
	}
	return items
}

// mindContext opens the context menu for what is at the window point x, y.
func (a *App) mindContext(md *mindDoc, r gfx.Rect, v mind.View, x, y float32) {
	a.commitMindText(md)
	a.commitMindCell(md)
	p := v.Board(float64(x), float64(y))
	at := gfx.Rect{X: x, Y: y}
	if n, _ := md.m.At(p.X, p.Y); n != nil {
		if !md.sel[n.ID] {
			md.selectOnly(n.ID)
		}
		md.primary = n.ID
		if n.Kind == mind.Table {
			if row, col, ok := md.m.CellAt(n, p.X, p.Y); ok {
				md.tableAt = mindCellAt{n.ID, row, col}
			}
		}
		a.openMenu("mind/context", at, a.mindNodeMenu(md, n))
		return
	}
	if e := md.m.EdgeAt(p.X, p.Y, 6/v.Zoom); e != nil {
		clear(md.sel)
		md.primary, md.edge = 0, e.ID
		a.openMenu("mind/context", at, append(a.mindRouteMenu(md, e), menuSep,
			menuItem{label: a.u.t("Label"), run: func() { a.editEdgeLabel(md, e) }},
			menuItem{label: a.u.t("Delete"), run: func() { a.mindDelete(md) }}))
		return
	}
	a.openMenu("mind/context", at, a.mindBoardMenu(md, p))
}

// mindCellAt is a table's cell the context menu was opened on.
type mindCellAt struct {
	id       mind.ID
	row, col int
}

// mindMoreMenu is the tool bar's menu: the whole map's commands.
func (a *App) mindMoreMenu(md *mindDoc) []menuItem {
	u := &a.u
	key := a.mindKeyHint
	return []menuItem{
		{label: u.t("Export as a picture…"), key: key("mind.export"), run: func() { a.exportMindPicture(md) }},
		{label: u.t("Copy as an outline"), key: key("mind.copy-outline"), run: func() { a.mindCopyOutline(md) }},
		{label: u.t("Show the file in its folder"), run: func() { a.host.Reveal(md.path) }},
		{label: u.t("Open in the asset manager"), run: func() { a.openAssets(filepath.Dir(md.path)) }},
		menuSep,
		{label: u.t("Save now"), key: key("mind.save"), run: func() { md.dirty = true; a.saveMind(md, false) }},
	}
}
