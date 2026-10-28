package ui

import (
	"fmt"
	"image"
	"math"
	"slices"
	"strings"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
	"github.com/btelolabs/btelo-video-cut/internal/paint"
)

// The canvas editor's menus, in Photoshop's order.
const (
	menuFile = iota
	menuEdit
	menuImage
	menuLayer
	menuSelect
	menuFilter
	menuView
	menuCount
)

// They are named in a context: Photoshop's Image menu is 图像, not the 图片 used elsewhere.
var artMenuNames = msgsIn("menu", "File", "Edit", "Image", "Layer", "Select", "Filter", "View")

// artCommand is a command of the canvas editor: where it sits in the menus, its shortcut, and what it does. The
// menus and the keyboard both come from this one list.
type artCommand struct {
	id        string // stable, for the keys chosen for it in Settings
	menu      int
	sub       string // the submenu it is in, by its label, or none
	label     string
	key       *Key // shown in the menu
	also      *Key // a second shortcut
	run       func()
	enabled   func() bool
	checked   func() bool
	sepBefore bool
}

func key(code uint16, mods string) *Key {
	return &Key{Code: code, Ctrl: strings.Contains(mods, "c"), Shift: strings.Contains(mods, "s"), Alt: strings.Contains(mods, "a")}
}

// keyLabel writes a shortcut as Photoshop's menus do.
func keyLabel(k *Key) string {
	if k == nil {
		return ""
	}
	return Hotkey{Ctrl: k.Ctrl, Alt: k.Alt, Shift: k.Shift, Key: k.Code}.String()
}

func (a *App) artCommands(ad *artDoc, vp gfx.Rect) []artCommand {
	u := &a.u
	d := ad.d
	run := func(fn func() error) func() { return func() { a.artRun(ad, fn) } }
	plain := func(fn func()) func() { return func() { a.artRun(ad, func() error { fn(); return nil }) } }
	chosen := func() bool { return d.Node(d.Active) != nil }
	hasMask := func() bool { n := d.Node(d.Active); return n != nil && n.Mask != nil }
	isLayer := func() bool { n := d.Node(d.Active); return n != nil && !n.IsGroup() }
	isGroup := func() bool { n := d.Node(d.Active); return n != nil && n.IsGroup() }
	selected := func() bool { return d.Selection != nil }
	twoVectors := func() bool { return len(ad.chosenVectors()) >= 2 }
	transform, adjustments, rotation := u.t("Transform"), u.t("Adjustments"), u.t("Image rotation")
	newAdjustment := u.t("New adjustment layer")
	adjustLayer := func(kind int) func() { return func() { a.openAdjustLayer(ad, kind) } }
	plainLayer := func(kind paint.AdjustKind, name string) func() {
		return run(func() error {
			_, err := d.AddAdjustment(paint.Adjustment{Kind: kind}, fmt.Sprintf("%s %d", name, len(d.Layers())+1))
			return err
		})
	}
	adjust := func(kind int) func() { return func() { a.openAdjust(ad, kind) } }
	// Commands that change the picture's size fit it to the window again.
	refitErr := func(fn func() error) func() {
		return func() { a.artRun(ad, fn); ad.fitView() }
	}
	refit := func(fn func()) func() { return refitErr(func() error { fn(); return nil }) }
	centre := func(z float64) func() { return func() { ad.zoomAt(vp, vp.X+vp.W/2, vp.Y+vp.H/2, z) } }
	// Ctrl+E merges the chosen layer down, or the chosen group into a layer, as Photoshop's Layer menu does.
	mergeLabel, merge := u.t("Merge down"), d.MergeDown
	if isGroup() {
		mergeLabel, merge = u.t("Merge group"), d.MergeGroup
	}
	idle := func() bool { return ad.xf == nil }
	cmds := []artCommand{
		{id: "draw.new-canvas", menu: menuFile, label: u.t("New canvas"), key: key('N', "c"), run: a.newCanvasNow},
		{id: "draw.save", menu: menuFile, label: u.t("Save"), key: key('S', "c"), run: func() { a.saveArtNow(ad) }},
		{id: "draw.export-as-png", menu: menuFile, label: u.t("Export as PNG"), key: key('S', "cs"), run: func() { a.exportArt(ad, false) }, sepBefore: true},
		{id: "draw.export-the-selection-as-png", menu: menuFile, label: u.t("Export the selection as PNG"), run: func() { a.exportArt(ad, true) }, enabled: selected},
		{id: "draw.export-artboards", menu: menuFile, label: u.t("Export artboards"), run: func() { a.exportBoards(ad, 0) }, enabled: func() bool { return len(d.Boards) > 0 }},
		{id: "draw.show-in-folder", menu: menuFile, label: u.t("Show in folder"), run: func() { a.host.Reveal(ad.path) }, sepBefore: true},

		{id: "draw.undo", menu: menuEdit, label: u.t("Undo"), key: key('Z', "c"), run: func() { d.Undo(); ad.changedAll() }, enabled: d.CanUndo},
		{id: "draw.redo", menu: menuEdit, label: u.t("Redo"), key: key('Z', "cs"), also: key('Y', "c"), run: func() { d.Redo(); ad.changedAll() }, enabled: d.CanRedo},
		{id: "draw.cut", menu: menuEdit, label: u.t("Cut"), key: key('X', "c"), run: func() { a.artCut(ad) }, enabled: isLayer, sepBefore: true},
		{id: "draw.copy", menu: menuEdit, label: u.t("Copy"), key: key('C', "c"), run: func() { a.artCopy(ad, false) }, enabled: isLayer},
		{id: "draw.copy-merged", menu: menuEdit, label: u.t("Copy merged"), key: key('C', "cs"), run: func() { a.artCopy(ad, true) }},
		{id: "draw.paste", menu: menuEdit, label: u.t("Paste"), key: key('V', "c"), run: func() { a.artPaste(ad) }},
		{id: "draw.fill-with-the-foreground-colour", menu: menuEdit, label: u.t("Fill with the foreground colour"), key: key(keyBack, "a"), run: run(func() error { return d.Fill(a.brush.Color) }), enabled: isLayer, sepBefore: true},
		{id: "draw.fill-with-the-background-colour", menu: menuEdit, label: u.t("Fill with the background colour"), key: key(keyBack, "c"), run: run(func() error { return d.Fill(a.backgroundColor()) }), enabled: isLayer},
		{id: "draw.clear", menu: menuEdit, label: u.t("Clear"), key: key(keyDelete, ""), also: key(keyBack, ""), run: run(d.Clear), enabled: isLayer},
		{id: "draw.free-transform", menu: menuEdit, label: u.t("Free transform"), key: key('T', "c"), run: func() { a.beginTransform(ad) },
			enabled: func() bool { return isLayer() && idle() }, sepBefore: true},
		{id: "draw.skew", menu: menuEdit, sub: transform, label: u.t("Skew"), run: func() { a.setTransformMode(ad, xfSkew) }, enabled: isLayer},
		{id: "draw.distort", menu: menuEdit, sub: transform, label: u.t("Distort"), run: func() { a.setTransformMode(ad, xfDistort) }, enabled: isLayer},
		{id: "draw.perspective", menu: menuEdit, sub: transform, label: u.t("Perspective"), run: func() { a.setTransformMode(ad, xfPerspective) }, enabled: isLayer},
		{id: "draw.warp", menu: menuEdit, sub: transform, label: u.t("Warp"), run: func() { a.setTransformMode(ad, xfWarp) }, enabled: isLayer},
		{id: "draw.flip-horizontal", menu: menuEdit, sub: transform, sepBefore: true, label: u.t("Flip horizontal"), run: run(func() error { return d.FlipLayer(true) }), enabled: isLayer},
		{id: "draw.flip-vertical", menu: menuEdit, sub: transform, label: u.t("Flip vertical"), run: run(func() error { return d.FlipLayer(false) }), enabled: isLayer},
		{id: "draw.rotate-180", menu: menuEdit, sub: transform, label: u.t("Rotate 180°"), run: run(func() error { return d.RotateLayer(2) }), enabled: isLayer, sepBefore: true},
		{id: "draw.rotate-90-clockwise", menu: menuEdit, sub: transform, label: u.t("Rotate 90° clockwise"), run: run(func() error { return d.RotateLayer(1) }), enabled: isLayer},
		{id: "draw.rotate-90-counter-clockwise", menu: menuEdit, sub: transform, label: u.t("Rotate 90° counter clockwise"), run: run(func() error { return d.RotateLayer(-1) }), enabled: isLayer},

		{id: "draw.brightness-contrast", menu: menuImage, sub: adjustments, label: u.t("Brightness/Contrast…"), run: adjust(adjustBrightness), enabled: isLayer},
		{id: "draw.levels", menu: menuImage, sub: adjustments, label: u.t("Levels…"), key: key('L', "c"), run: adjust(adjustLevels), enabled: isLayer},
		{id: "draw.curves", menu: menuImage, sub: adjustments, label: u.t("Curves…"), key: key('M', "c"), run: adjust(adjustCurves), enabled: isLayer},
		{id: "draw.hue-saturation", menu: menuImage, sub: adjustments, label: u.t("Hue/Saturation…"), key: key('U', "c"), run: adjust(adjustHue), enabled: isLayer, sepBefore: true},
		{id: "draw.invert", menu: menuImage, sub: adjustments, label: u.t("Invert"), key: key('I', "c"), run: run(func() error { return d.Run("Invert", paint.Invert()) }), enabled: isLayer, sepBefore: true},
		{id: "draw.posterize", menu: menuImage, sub: adjustments, label: u.t("Posterize…"), run: adjust(adjustPosterize), enabled: isLayer},
		{id: "draw.threshold", menu: menuImage, sub: adjustments, label: u.t("Threshold…"), run: adjust(adjustThreshold), enabled: isLayer},
		{id: "draw.desaturate", menu: menuImage, sub: adjustments, label: u.t("Desaturate"), key: key('U', "cs"), run: run(func() error { return d.Run("Desaturate", paint.Desaturate()) }), enabled: isLayer, sepBefore: true},
		{id: "draw.image-size", menu: menuImage, label: u.t("Image size…"), key: key('I', "ca"), run: func() { a.openDialog(dialogImageSize) }, sepBefore: true},
		{id: "draw.canvas-size", menu: menuImage, label: u.t("Canvas size…"), key: key('C', "ca"), run: func() { a.openDialog(dialogCanvasSize) }},
		{id: "draw.rotate-canvas-180", menu: menuImage, sub: rotation, label: u.t("180°"), run: refit(func() { d.RotateCanvas(2) })},
		{id: "draw.rotate-canvas-90-clockwise", menu: menuImage, sub: rotation, label: u.t("90° clockwise"), run: refit(func() { d.RotateCanvas(1) })},
		{id: "draw.rotate-canvas-90-counter-clockwise", menu: menuImage, sub: rotation, label: u.t("90° counter clockwise"), run: refit(func() { d.RotateCanvas(-1) })},
		{id: "draw.rotate-canvas-arbitrary", menu: menuImage, sub: rotation, label: u.t("Arbitrary…"), run: func() { a.openDialog(dialogRotate) }},
		{id: "draw.flip-canvas-horizontal", menu: menuImage, sub: rotation, label: u.t("Flip canvas horizontal"), run: plain(func() { d.FlipCanvas(true) }), sepBefore: true},
		{id: "draw.flip-canvas-vertical", menu: menuImage, sub: rotation, label: u.t("Flip canvas vertical"), run: plain(func() { d.FlipCanvas(false) })},
		{id: "draw.crop-to-the-selection", menu: menuImage, label: u.t("Crop to the selection"), run: refitErr(func() error { return d.Crop(d.SelectionBounds(), !a.settings.Paint.CropKeep) }),
			enabled: selected, sepBefore: true},
		{id: "draw.reveal-all", menu: menuImage, label: u.t("Reveal all"), run: refitErr(d.RevealAll)},

		{id: "draw.new-layer", menu: menuLayer, label: u.t("New layer"), key: key('N', "cs"), run: plain(func() { d.AddLayer(a.layerName()) })},
		{id: "draw.adjustment-layer-brightness", menu: menuLayer, sub: newAdjustment, label: u.t("Brightness/Contrast…"), run: adjustLayer(adjustBrightness)},
		{id: "draw.adjustment-layer-levels", menu: menuLayer, sub: newAdjustment, label: u.t("Levels…"), run: adjustLayer(adjustLevels)},
		{id: "draw.adjustment-layer-curves", menu: menuLayer, sub: newAdjustment, label: u.t("Curves…"), run: adjustLayer(adjustCurves)},
		{id: "draw.adjustment-layer-hue", menu: menuLayer, sub: newAdjustment, label: u.t("Hue/Saturation…"), run: adjustLayer(adjustHue), sepBefore: true},
		{id: "draw.adjustment-layer-black-white", menu: menuLayer, sub: newAdjustment, label: u.t("Black & White"), run: plainLayer(paint.AdjustBlackWhite, u.t("Black & White"))},
		{id: "draw.adjustment-layer-invert", menu: menuLayer, sub: newAdjustment, label: u.t("Invert"), run: plainLayer(paint.AdjustInvert, u.t("Invert")), sepBefore: true},
		{id: "draw.adjustment-layer-posterize", menu: menuLayer, sub: newAdjustment, label: u.t("Posterize…"), run: adjustLayer(adjustPosterize)},
		{id: "draw.adjustment-layer-threshold", menu: menuLayer, sub: newAdjustment, label: u.t("Threshold…"), run: adjustLayer(adjustThreshold)},
		{id: "draw.layer-style", menu: menuLayer, label: u.t("Layer style…"), run: func() { a.openStyle(ad) }, sepBefore: true,
			enabled: func() bool { n := d.Node(d.Active); return n != nil && !n.IsGroup() && n.Adjust == nil }},
		{id: "draw.clear-layer-style", menu: menuLayer, label: u.t("Clear layer style"), run: run(func() error { return d.SetStyle(d.Active, nil) }),
			enabled: func() bool { n := d.Node(d.Active); return n != nil && n.Style != nil }},
		{id: "draw.adjustment-settings", menu: menuLayer, label: u.t("Adjustment settings…"), run: func() { a.openAdjustmentSettings(ad, d.Active) },
			enabled: func() bool { n := d.Node(d.Active); return n != nil && n.Adjust != nil }},
		{id: "draw.new-group", menu: menuLayer, label: u.t("New group"), run: plain(func() { d.AddGroup(a.groupName()) })},
		{id: "draw.duplicate-layer", menu: menuLayer, label: u.t("Duplicate layer"), key: key('J', "c"), run: run(func() error { _, err := d.Duplicate(u.t(" copy")); return err }), enabled: chosen},
		{id: "draw.delete-layer", menu: menuLayer, label: u.t("Delete layer"), run: run(d.Delete), enabled: chosen},
		{id: "draw.add-layer-mask", menu: menuLayer, label: u.t("Add layer mask"), run: run(d.AddMask), enabled: func() bool { return chosen() && !hasMask() }, sepBefore: true},
		{id: "draw.apply-layer-mask", menu: menuLayer, label: u.t("Apply layer mask"), run: run(d.ApplyMask), enabled: func() bool { return hasMask() && isLayer() }},
		{id: "draw.delete-layer-mask", menu: menuLayer, label: u.t("Delete layer mask"), run: run(d.DeleteMask), enabled: hasMask},
		{id: "draw.disable-layer-mask", menu: menuLayer, label: u.t("Disable layer mask"), run: plain(func() { n := d.Node(d.Active); d.SetMaskOff(n.ID, !n.MaskOff) }), enabled: hasMask,
			checked: func() bool { n := d.Node(d.Active); return n != nil && n.Mask != nil && n.MaskOff }},
		{id: "draw.group-layers", menu: menuLayer, label: u.t("Group layers"), key: key('G', "c"), run: run(func() error { _, err := d.GroupChosen(a.groupName()); return err }), enabled: chosen, sepBefore: true},
		{id: "draw.ungroup-layers", menu: menuLayer, label: u.t("Ungroup layers"), key: key('G', "cs"), run: run(d.Ungroup), enabled: isGroup},
		{id: "draw.lock-transparent-pixels", menu: menuLayer, label: u.t("Lock transparent pixels"), key: key(0xbf, ""), run: plain(func() { n := d.Node(d.Active); d.SetLockAlpha(n.ID, !n.LockAlpha) }), enabled: isLayer,
			checked: func() bool { n := d.Node(d.Active); return n != nil && n.LockAlpha }},
		{id: "draw.rasterize", menu: menuLayer, label: u.t("Rasterize"), run: run(d.Rasterize), enabled: func() bool { n := d.Node(d.Active); return n != nil && (n.Text != nil || n.Vector != nil) }},
		{id: "draw.boolean-add", menu: menuLayer, sub: u.t("Boolean"), label: u.tc("boolean", "Add"), run: func() { a.runBoolean(ad, paint.BoolAdd) }, enabled: twoVectors},
		{id: "draw.boolean-subtract", menu: menuLayer, sub: u.t("Boolean"), label: u.tc("boolean", "Subtract"), run: func() { a.runBoolean(ad, paint.BoolSubtract) }, enabled: twoVectors},
		{id: "draw.boolean-intersect", menu: menuLayer, sub: u.t("Boolean"), label: u.tc("boolean", "Intersect"), run: func() { a.runBoolean(ad, paint.BoolIntersect) }, enabled: twoVectors},
		{id: "draw.boolean-exclude", menu: menuLayer, sub: u.t("Boolean"), label: u.tc("boolean", "Exclude"), run: func() { a.runBoolean(ad, paint.BoolExclude) }, enabled: twoVectors},
		{id: "draw.convert-to-curves", menu: menuLayer, label: u.t("Convert to curves"), run: func() { a.convertToCurves(ad) }, enabled: func() bool {
			n := d.Node(d.Active)
			return n != nil && n.Vector != nil && n.Vector.Kind != paint.VectorCurve
		}},
		{id: "draw.merge", menu: menuLayer, label: mergeLabel, key: key('E', "c"), run: run(merge), enabled: chosen, sepBefore: true},
		{id: "draw.flatten-image", menu: menuLayer, label: u.t("Flatten image"), run: plain(func() { d.Flatten(u.tc("layer", "Background")) })},

		{id: "draw.all", menu: menuSelect, label: u.t("All"), key: key('A', "c"), run: plain(d.SelectAll)},
		{id: "draw.deselect", menu: menuSelect, label: u.t("Deselect"), key: key('D', "c"), run: plain(d.Deselect), enabled: selected},
		{id: "draw.reselect", menu: menuSelect, label: u.t("Reselect"), key: key('D', "cs"), run: plain(d.Reselect)},
		{id: "draw.inverse", menu: menuSelect, label: u.t("Inverse"), key: key('I', "cs"), run: plain(d.InvertSelection)},
		{id: "draw.feather", menu: menuSelect, label: u.t("Feather…"), key: key(0x76, "s"), run: func() { a.openDialog(dialogFeather) }, enabled: selected, sepBefore: true},
		{id: "draw.edit-the-layer-mask", menu: menuSelect, label: u.t("Edit the layer mask"), key: key(0xdc, "c"), run: func() { d.EditMask = !d.EditMask && hasMask() }, enabled: hasMask,
			checked: func() bool { return d.EditMask }, sepBefore: true},

		{id: "draw.gaussian-blur", menu: menuFilter, sub: u.t("Blur"), label: u.t("Gaussian blur…"), run: adjust(filterBlur), enabled: isLayer},
		{id: "draw.add-noise", menu: menuFilter, sub: u.t("Noise"), label: u.t("Add noise…"), run: adjust(filterNoise), enabled: isLayer},
		{id: "draw.mosaic", menu: menuFilter, sub: u.t("Pixelate"), label: u.t("Mosaic…"), run: adjust(filterMosaic), enabled: isLayer},
		{id: "draw.unsharp-mask", menu: menuFilter, sub: u.t("Sharpen"), label: u.t("Unsharp mask…"), run: adjust(filterSharpen), enabled: isLayer},

		{id: "draw.zoom-in", menu: menuView, label: u.t("Zoom in"), key: key(0xbb, "c"), run: centre(ad.zoom * 1.25)},
		{id: "draw.zoom-out", menu: menuView, label: u.t("Zoom out"), key: key(0xbd, "c"), run: centre(ad.zoom / 1.25)},
		{id: "draw.fit-on-screen", menu: menuView, label: u.t("Fit on screen"), key: key('0', "c"), run: ad.fitView},
		{id: "draw.snapping", menu: menuView, label: u.t("Snapping"), key: key(0xba, "cs"), run: func() { a.settings.Paint.NoSnap = !a.settings.Paint.NoSnap; a.saveSettings() },
			checked: func() bool { return !a.settings.Paint.NoSnap }},
		{id: "draw.100pct", menu: menuView, label: u.t("100%"), key: key('1', "c"), run: centre(1)},
		{id: "draw.show-the-video-behind-the-drawing", menu: menuView, label: u.t("Show the video behind the drawing"), run: func() { ad.noBack = !ad.noBack; ad.view = nil },
			enabled: func() bool { return ad.backdrop != nil }, checked: func() bool { return ad.backdrop != nil && !ad.noBack }, sepBefore: true},
	}
	// Any command applies Free Transform, and commits the text being typed, first, as Photoshop does; undo then
	// takes it back.
	for i := range cmds {
		run := cmds[i].run
		cmds[i].run = func() { a.applyTransform(ad); a.commitText(ad); a.finishPen(ad); run() }
	}
	return cmds
}

// drawArtMenus is the menu bar.
func (a *App) drawArtMenus(ad *artDoc, r gfx.Rect, vp gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colBackground)
	x := r.X + u.px(8)
	f := u.face(13)
	for m, name := range artMenuNames {
		label := u.tc("menu", name)
		tr := gfx.Rect{X: x, Y: r.Y + u.px(2), W: f.Measure(label) + u.px(20), H: r.H - u.px(4)}
		id := fmt.Sprintf("art/menu/%d", m)
		open := a.menuOpen(id)
		if open || u.hover(id, tr) {
			u.c.RoundRect(tr, u.px(5), colSurface)
			u.cursor = CursorHand
		}
		u.label(f, tr, label, colText, 0.5)
		// Once a menu is open, pointing at another title opens that one, as menu bars do.
		if a.menu != nil && strings.HasPrefix(a.menu.id, "art/menu/") && !open && tr.Contains(u.mx, u.my) {
			a.menu = nil
			a.openMenu(id, tr, a.artMenuItems(ad, vp, m))
		}
		if u.clicked(id, tr) {
			a.openMenu(id, tr, a.artMenuItems(ad, vp, m))
		}
		x = tr.Right() + u.px(2)
	}
}

func (a *App) artMenuItems(ad *artDoc, vp gfx.Rect, menu int) []menuItem {
	var items []menuItem
	for _, c := range a.artCommands(ad, vp) {
		if c.menu != menu {
			continue
		}
		it := menuItem{label: c.label, key: a.keyHint(c.id, keys(c.key, c.also)), run: c.run}
		if c.enabled != nil {
			it.disabled = !c.enabled()
		}
		if c.checked != nil {
			it.checked = c.checked()
		}
		// A command in a submenu goes into the item named after it, made where its first command is.
		into := &items
		if c.sub != "" {
			i := slices.IndexFunc(items, func(m menuItem) bool { return m.sub != nil && m.label == c.sub })
			if i < 0 {
				if c.sepBefore && len(items) > 0 {
					items = append(items, menuSep)
				}
				items = append(items, menuItem{label: c.sub, sub: []menuItem{}})
				i = len(items) - 1
				c.sepBefore = false
			}
			into = &items[i].sub
		}
		if c.sepBefore && len(*into) > 0 {
			*into = append(*into, menuSep)
		}
		*into = append(*into, it)
	}
	return items
}

// saveArtNow saves the canvas at once and says so.
func (a *App) saveArtNow(ad *artDoc) {
	ad.dirty = true
	if err := a.saveArt(ad, true); err != nil {
		a.toast(a.u.tf("The canvas could not be saved: %s", a.describe(err)), true)
		return
	}
	a.toast(a.u.tf("Saved %s", ad.path), false)
}

// selectionOutline is the selection's edge as pixel-edge segments in canvas pixels, worked out once for each
// selection.
type selectionOutline struct {
	of         *paint.Mask
	horizontal [][3]int32 // x0, x1, y: an edge along the top of row y
	vertical   [][3]int32 // y0, y1, x: an edge along the left of column x
}

// selectionOutlineOf works out where a selection's edge runs: between pixels on either side of half selected. It
// looks only where the selection can change: around what it selects, or, for one selecting everything beyond its
// tiles, around those.
func selectionOutlineOf(sel *paint.Mask) selectionOutline {
	area := sel.Bounds()
	if sel.Fill() != 0 {
		area = sel.TileBounds()
	}
	o := outline(sel.Read(area.Inset(-1)), sel.Fill())
	o.of = sel
	return o
}

// outline works out where the edge of an alpha image runs, in its own coordinates; beyond the image it is fill.
func outline(m *image.Alpha, fill uint8) selectionOutline {
	var o selectionOutline
	r := m.Rect
	in := func(x, y int) bool {
		if !(image.Point{x, y}).In(r) {
			return fill >= 128
		}
		return m.Pix[m.PixOffset(x, y)] >= 128
	}
	for y := r.Min.Y; y <= r.Max.Y; y++ {
		start := -1
		for x := r.Min.X; x <= r.Max.X; x++ {
			edge := x < r.Max.X && in(x, y) != in(x, y-1)
			switch {
			case edge && start < 0:
				start = x
			case !edge && start >= 0:
				o.horizontal = append(o.horizontal, [3]int32{int32(start), int32(x), int32(y)})
				start = -1
			}
		}
	}
	for x := r.Min.X; x <= r.Max.X; x++ {
		start := -1
		for y := r.Min.Y; y <= r.Max.Y; y++ {
			edge := y < r.Max.Y && in(x, y) != in(x-1, y)
			switch {
			case edge && start < 0:
				start = y
			case !edge && start >= 0:
				o.vertical = append(o.vertical, [3]int32{int32(start), int32(y), int32(x)})
				start = -1
			}
		}
	}
	return o
}

// drawSelectionOutline draws the marching ants along the selection's edge.
func (a *App) drawSelectionOutline(ad *artDoc, vp gfx.Rect) {
	sel := ad.d.Selection
	if sel == nil {
		return
	}
	if ad.ants.of != sel {
		ad.ants = selectionOutlineOf(sel)
	}
	u := &a.u
	sx, sy := ad.origin(vp)
	z := ad.zoom
	w := max(1, u.px(1))
	dash := float64(max(u.px(4), 2))
	// Dashes are measured along the window, so they look the same at every zoom.
	seg := func(x0, y0, x1, y1 float64) {
		sx0, sy0, sx1, sy1 := sx+x0*z, sy+y0*z, sx+x1*z, sy+y1*z
		if sx1 < float64(vp.X) || sx0 > float64(vp.Right()) || sy1 < float64(vp.Y) || sy0 > float64(vp.Bottom()) {
			return
		}
		horizontal := sy0 == sy1
		from, to := sx0, sx1
		if !horizontal {
			from, to = sy0, sy1
		}
		for p := math.Floor(from/dash) * dash; p < to; p += dash {
			a0, a1 := max(p, from), min(p+dash, to)
			col := colBlack
			if int(math.Floor(p/dash))%2 != 0 {
				col = colWhite
			}
			if horizontal {
				u.c.Fill(gfx.Rect{X: float32(a0), Y: float32(sy0), W: float32(a1 - a0), H: w}, col)
			} else {
				u.c.Fill(gfx.Rect{X: float32(sx0), Y: float32(a0), W: w, H: float32(a1 - a0)}, col)
			}
		}
	}
	for _, h := range ad.ants.horizontal {
		seg(float64(h[0]), float64(h[2]), float64(h[1]), float64(h[2]))
	}
	for _, v := range ad.ants.vertical {
		seg(float64(v[2]), float64(v[0]), float64(v[2]), float64(v[1]))
	}
}
