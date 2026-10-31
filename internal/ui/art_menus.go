package ui

import (
	"fmt"
	"image"
	"math"
	"slices"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/paint"
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
	key       *Key   // shown in the menu
	also      []*Key // more shortcuts
	run       func()
	enabled   func() bool
	checked   func() bool
	sepBefore bool
}

// defaults are the command's shortcuts before any are chosen in Settings.
func (c artCommand) defaults() []Key { return keys(append([]*Key{c.key}, c.also...)...) }

// newLayerKeys add a pixel layer: N, as the vector brush's P beside it, and Photoshop's Ctrl+N and Ctrl+Shift+N;
// newVectorLayerKey adds a vector layer.
var (
	newLayerKeys      = []*Key{key('N', ""), key('N', "c"), key('N', "cs")}
	newVectorLayerKey = key('N', "s")
)

// fitKey fits the picture to the window: Photoshop's Ctrl+0 with Alt, as the canvas's other zoom keys have it,
// since Ctrl with a digit, = or - goes to a tab or zooms the whole window.
var fitKey = key('0', "ca")

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
	pasteShape := u.t("Paste the shape")
	shapeCopied := func() bool { return a.vecClip != nil }
	compound := func() bool {
		n := d.Node(d.Active)
		return n != nil && n.EditedVector() != nil && n.EditedVector().Compound()
	}
	hasClip := func() bool { n := d.Node(d.Active); return n != nil && n.Clip != nil }
	isSmart := func() bool { n := d.Node(d.Active); return n != nil && n.IsSmart() }
	filterable := func() bool { return isLayer() || isSmart() }
	smartObjects, vectorMask := u.t("Smart objects"), u.t("Vector mask")
	modify := u.t("Modify")
	cmds := []artCommand{
		{id: "draw.new-canvas", menu: menuFile, label: u.t("New canvas"), key: key('N', "ca"), run: a.newCanvasNow},
		{id: "draw.save", menu: menuFile, label: u.t("Save"), key: key('S', "c"), run: func() { a.saveArtNow(ad) }},
		{id: "draw.export-as-png", menu: menuFile, label: u.t("Export as PNG"), key: key('S', "cs"), run: func() { a.exportArt(ad, false) }, sepBefore: true},
		{id: "draw.export-the-selection-as-png", menu: menuFile, label: u.t("Export the selection as PNG"), run: func() { a.exportArt(ad, true) }, enabled: selected},
		{id: "draw.export-as-psd", menu: menuFile, label: u.t("Export as PSD"), run: func() { a.exportPSD(ad) }},
		{id: "draw.export-slices", menu: menuFile, label: u.t("Export slices"), key: key('P', "cs"), run: func() { a.exportSlices(ad) }},
		{id: "draw.export-artboards", menu: menuFile, label: u.t("Export artboards"), run: func() { a.exportBoards(ad, 0) }, enabled: func() bool { return len(d.Boards()) > 0 }},
		{id: "draw.print", menu: menuFile, label: u.t("Print…"), key: key('P', "c"), run: plain(func() { a.printCanvas(ad) }), sepBefore: true},
		{id: "draw.show-in-folder", menu: menuFile, label: u.t("Show in folder"), run: func() { a.host.Reveal(ad.path) }, sepBefore: true},
		{id: "draw.show-smart-objects-folder", menu: menuFile, label: u.t("Show the smart objects folder"), run: func() { a.showSmartFolder() }},

		{id: "draw.undo", menu: menuEdit, label: u.t("Undo"), key: key('Z', "c"), run: func() { d.Undo(); ad.changedAll() }, enabled: d.CanUndo},
		{id: "draw.redo", menu: menuEdit, label: u.t("Redo"), key: key('Z', "cs"), also: []*Key{key('Y', "c")}, run: func() { d.Redo(); ad.changedAll() }, enabled: d.CanRedo},
		{id: "draw.cut", menu: menuEdit, label: u.t("Cut"), key: key('X', "c"), run: func() { a.artCut(ad) }, enabled: chosen, sepBefore: true},
		{id: "draw.copy", menu: menuEdit, label: u.t("Copy"), key: key('C', "c"), run: func() { a.artCopy(ad, false) }, enabled: chosen},
		{id: "draw.copy-merged", menu: menuEdit, label: u.t("Copy merged"), key: key('C', "cs"), run: func() { a.artCopy(ad, true) }},
		{id: "draw.paste", menu: menuEdit, label: u.t("Paste"), key: key('V', "c"), run: func() { a.artPaste(ad, paint.OpAdd, false) }},
		{id: "draw.paste-as-layer", menu: menuEdit, label: u.t("Paste as a new layer"), key: key('V', "cs"), run: func() { a.artPaste(ad, paint.OpAdd, true) }},
		{id: "draw.paste-subtract", menu: menuEdit, sub: pasteShape, label: u.tc("boolean", "Subtract"), run: func() { a.artPaste(ad, paint.OpSubtract, false) }, enabled: shapeCopied},
		{id: "draw.paste-intersect", menu: menuEdit, sub: pasteShape, label: u.tc("boolean", "Intersect"), run: func() { a.artPaste(ad, paint.OpIntersect, false) }, enabled: shapeCopied},
		{id: "draw.paste-exclude", menu: menuEdit, sub: pasteShape, label: u.tc("boolean", "Exclude"), run: func() { a.artPaste(ad, paint.OpExclude, false) }, enabled: shapeCopied},
		{id: "draw.fill-with-the-foreground-colour", menu: menuEdit, label: u.t("Fill with the foreground colour"), key: key(keyBack, "a"), run: run(func() error { return d.Fill(a.brush.Color) }), enabled: isLayer, sepBefore: true},
		{id: "draw.fill-with-the-background-colour", menu: menuEdit, label: u.t("Fill with the background colour"), key: key(keyBack, "c"), run: run(func() error { return d.Fill(a.backgroundColor()) }), enabled: isLayer},
		{id: "draw.content-aware-fill", menu: menuEdit, label: u.t("Content-aware fill"), key: key(0x74, "s"), run: func() { a.contentFill(ad) },
			enabled: func() bool { return selected() && isLayer() && !ad.filling }},
		{id: "draw.clear", menu: menuEdit, label: u.t("Clear"), key: key(keyDelete, ""), also: []*Key{key(keyBack, "")}, run: run(d.Clear), enabled: isLayer},
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

		{id: "draw.brightness-contrast", menu: menuImage, sub: adjustments, label: u.t("Brightness/Contrast…"), run: adjust(adjustBrightness), enabled: filterable},
		{id: "draw.levels", menu: menuImage, sub: adjustments, label: u.t("Levels…"), key: key('L', "c"), run: adjust(adjustLevels), enabled: filterable},
		{id: "draw.curves", menu: menuImage, sub: adjustments, label: u.t("Curves…"), key: key('M', "c"), run: adjust(adjustCurves), enabled: filterable},
		{id: "draw.exposure", menu: menuImage, sub: adjustments, label: u.t("Exposure…"), run: adjust(adjustExposure), enabled: filterable},
		{id: "draw.vibrance", menu: menuImage, sub: adjustments, label: u.t("Vibrance…"), run: adjust(adjustVibrance), enabled: filterable, sepBefore: true},
		{id: "draw.hue-saturation", menu: menuImage, sub: adjustments, label: u.t("Hue/Saturation…"), key: key('U', "c"), run: adjust(adjustHue), enabled: filterable},
		{id: "draw.color-balance", menu: menuImage, sub: adjustments, label: u.t("Color balance…"), key: key('B', "c"), run: adjust(adjustColorBalance), enabled: filterable},
		{id: "draw.photo-filter", menu: menuImage, sub: adjustments, label: u.t("Photo filter…"), run: adjust(adjustPhotoFilter), enabled: filterable},
		{id: "draw.invert", menu: menuImage, sub: adjustments, label: u.t("Invert"), key: key('I', "c"), run: run(func() error { return d.Run("Invert", paint.Invert()) }), enabled: isLayer, sepBefore: true},
		{id: "draw.posterize", menu: menuImage, sub: adjustments, label: u.t("Posterize…"), run: adjust(adjustPosterize), enabled: filterable},
		{id: "draw.threshold", menu: menuImage, sub: adjustments, label: u.t("Threshold…"), run: adjust(adjustThreshold), enabled: filterable},
		{id: "draw.gradient-map", menu: menuImage, sub: adjustments, label: u.t("Gradient map…"), run: adjust(adjustGradientMap), enabled: filterable},
		{id: "draw.selective-color", menu: menuImage, sub: adjustments, label: u.t("Selective color…"), run: adjust(adjustSelective), enabled: filterable},
		{id: "draw.shadows-highlights", menu: menuImage, sub: adjustments, label: u.t("Shadows/Highlights…"), run: adjust(adjustShadowsHighlights), enabled: filterable, sepBefore: true},
		{id: "draw.desaturate", menu: menuImage, sub: adjustments, label: u.t("Desaturate"), key: key('U', "cs"), run: run(func() error { return d.Run("Desaturate", paint.Desaturate()) }), enabled: isLayer, sepBefore: true},
		{id: "draw.image-size", menu: menuImage, label: u.t("Image size…"), key: key('I', "ca"), run: func() { a.openDialog(dialogImageSize) }, sepBefore: true},
		{id: "draw.canvas-size", menu: menuImage, label: u.t("Canvas size…"), key: key('C', "ca"), run: func() { a.openDialog(dialogCanvasSize) }},
		{id: "draw.rotate-canvas-180", menu: menuImage, sub: rotation, label: u.t("180°"), run: refit(func() { d.RotateCanvas(2) })},
		{id: "draw.rotate-canvas-90-clockwise", menu: menuImage, sub: rotation, label: u.t("90° clockwise"), run: refit(func() { d.RotateCanvas(1) })},
		{id: "draw.rotate-canvas-90-counter-clockwise", menu: menuImage, sub: rotation, label: u.t("90° counter clockwise"), run: refit(func() { d.RotateCanvas(-1) })},
		{id: "draw.rotate-canvas-arbitrary", menu: menuImage, sub: rotation, label: u.t("Arbitrary…"), run: func() { a.openDialog(dialogRotate) }},
		{id: "draw.flip-canvas-horizontal", menu: menuImage, sub: rotation, label: u.t("Flip canvas horizontal"), run: plain(func() { d.FlipCanvas(true) }), sepBefore: true},
		{id: "draw.flip-canvas-vertical", menu: menuImage, sub: rotation, label: u.t("Flip canvas vertical"), run: plain(func() { d.FlipCanvas(false) })},
		{id: "draw.crop-to-the-selection", menu: menuImage, label: u.t("Crop to the selection"), run: refitErr(func() error { return d.Crop(d.SelectionBounds(), false) }),
			enabled: selected, sepBefore: true},
		{id: "draw.reveal-all", menu: menuImage, label: u.t("Reveal all"), run: refitErr(d.RevealAll)},

		{id: "draw.new-layer", menu: menuLayer, label: u.t("New layer"), key: newLayerKeys[0], also: newLayerKeys[1:], run: plain(func() { d.AddLayer(a.layerName()) })},
		{id: "draw.new-vector-layer", menu: menuLayer, label: u.t("New vector layer"), key: newVectorLayerKey,
			run: plain(func() { d.AddVectorLayer(a.vectorLayerName(), solidPaint(a.shapeColours()[0])) })},
		{id: "draw.adjustment-layer-brightness", menu: menuLayer, sub: newAdjustment, label: u.t("Brightness/Contrast…"), run: adjustLayer(adjustBrightness)},
		{id: "draw.adjustment-layer-levels", menu: menuLayer, sub: newAdjustment, label: u.t("Levels…"), run: adjustLayer(adjustLevels)},
		{id: "draw.adjustment-layer-curves", menu: menuLayer, sub: newAdjustment, label: u.t("Curves…"), run: adjustLayer(adjustCurves)},
		{id: "draw.adjustment-layer-exposure", menu: menuLayer, sub: newAdjustment, label: u.t("Exposure…"), run: adjustLayer(adjustExposure)},
		{id: "draw.adjustment-layer-vibrance", menu: menuLayer, sub: newAdjustment, label: u.t("Vibrance…"), run: adjustLayer(adjustVibrance), sepBefore: true},
		{id: "draw.adjustment-layer-hue", menu: menuLayer, sub: newAdjustment, label: u.t("Hue/Saturation…"), run: adjustLayer(adjustHue)},
		{id: "draw.adjustment-layer-color-balance", menu: menuLayer, sub: newAdjustment, label: u.t("Color balance…"), run: adjustLayer(adjustColorBalance)},
		{id: "draw.adjustment-layer-black-white", menu: menuLayer, sub: newAdjustment, label: u.t("Black & White"), run: plainLayer(paint.AdjustBlackWhite, u.t("Black & White"))},
		{id: "draw.adjustment-layer-photo-filter", menu: menuLayer, sub: newAdjustment, label: u.t("Photo filter…"), run: adjustLayer(adjustPhotoFilter)},
		{id: "draw.adjustment-layer-invert", menu: menuLayer, sub: newAdjustment, label: u.t("Invert"), run: plainLayer(paint.AdjustInvert, u.t("Invert")), sepBefore: true},
		{id: "draw.adjustment-layer-posterize", menu: menuLayer, sub: newAdjustment, label: u.t("Posterize…"), run: adjustLayer(adjustPosterize)},
		{id: "draw.adjustment-layer-threshold", menu: menuLayer, sub: newAdjustment, label: u.t("Threshold…"), run: adjustLayer(adjustThreshold)},
		{id: "draw.adjustment-layer-gradient-map", menu: menuLayer, sub: newAdjustment, label: u.t("Gradient map…"), run: adjustLayer(adjustGradientMap)},
		{id: "draw.adjustment-layer-selective-color", menu: menuLayer, sub: newAdjustment, label: u.t("Selective color…"), run: adjustLayer(adjustSelective)},
		{id: "draw.layer-style", menu: menuLayer, label: u.t("Layer style…"), key: key('Q', ""), run: func() { a.openStyle(ad) }, sepBefore: true,
			enabled: func() bool { n := d.Node(d.Active); return n != nil && !n.IsGroup() && n.Adjust == nil }},
		{id: "draw.clear-layer-style", menu: menuLayer, label: u.t("Clear layer style"), run: run(func() error { return d.SetStyle(d.Active, nil) }),
			enabled: func() bool { n := d.Node(d.Active); return n != nil && n.Style != nil }},
		{id: "draw.adjustment-settings", menu: menuLayer, label: u.t("Adjustment settings…"), run: func() { a.openAdjustmentSettings(ad, d.Active) },
			enabled: func() bool { n := d.Node(d.Active); return n != nil && n.Adjust != nil }},
		{id: "draw.mark-slice", menu: menuLayer, label: u.t("Mark as slice"), key: key('K', ""), run: func() { a.markSlices(ad) }, enabled: chosen,
			checked: func() bool { return activeMarked(d) }},
		{id: "draw.rename-layer", menu: menuLayer, label: u.t("Rename layer…"), key: key('R', ""), run: func() { a.startRename(ad) }, enabled: chosen},
		{id: "draw.new-group", menu: menuLayer, label: u.t("New group"), run: plain(func() { d.AddGroup(a.groupName()) })},
		{id: "draw.duplicate-layer", menu: menuLayer, label: u.t("Duplicate layer"), key: key('J', "c"), run: run(func() error { _, err := d.Duplicate(u.t(" copy")); return err }), enabled: chosen},
		{id: "draw.delete-layer", menu: menuLayer, label: u.t("Delete layer"), run: run(d.Delete), enabled: chosen},
		{id: "draw.remove-background", menu: menuLayer, label: u.t("Remove background"), run: func() { a.removeBackground(ad) },
			enabled: func() bool { return chosen() && !ad.matting }, sepBefore: true},
		{id: "draw.add-layer-mask", menu: menuLayer, label: u.t("Add layer mask"), key: key('M', "s"), run: run(d.AddMask), enabled: func() bool { return chosen() && !hasMask() }, sepBefore: true},
		{id: "draw.apply-layer-mask", menu: menuLayer, label: u.t("Apply layer mask"), run: run(d.ApplyMask), enabled: func() bool { return hasMask() && isLayer() }},
		{id: "draw.delete-layer-mask", menu: menuLayer, label: u.t("Delete layer mask"), run: run(d.DeleteMask), enabled: hasMask},
		{id: "draw.disable-layer-mask", menu: menuLayer, label: u.t("Disable layer mask"), run: plain(func() { n := d.Node(d.Active); d.SetMaskOff(n.ID, !n.MaskOff) }), enabled: hasMask,
			checked: func() bool { n := d.Node(d.Active); return n != nil && n.Mask != nil && n.MaskOff }},
		{id: "draw.group-layers", menu: menuLayer, label: u.t("Group layers"), key: key('G', "c"), run: run(func() error { return a.groupChosen(ad, a.groupName()) }), enabled: chosen, sepBefore: true},
		{id: "draw.ungroup-layers", menu: menuLayer, label: u.t("Ungroup layers"), key: key('G', "cs"), run: run(d.Ungroup), enabled: isGroup},
		{id: "draw.lock-transparent-pixels", menu: menuLayer, label: u.t("Lock transparent pixels"), key: key(0xbf, ""), run: plain(func() { n := d.Node(d.Active); d.SetLockAlpha(n.ID, !n.LockAlpha) }), enabled: isLayer,
			checked: func() bool { n := d.Node(d.Active); return n != nil && n.LockAlpha }},
		{id: "draw.convert-to-smart-object", menu: menuLayer, label: u.t("Convert to smart object"), key: key('F', "c"), sepBefore: true,
			run:     run(func() error { _, err := d.ConvertToSmart(a.smartGroupName()); return err }),
			enabled: func() bool { n := d.Node(d.Active); return n != nil && !n.IsSmart() && !n.IsBoard() }},
		{id: "draw.open-smart-object-file", menu: menuLayer, label: u.t("Open the smart object's file"), run: func() { a.openSmartFile(ad) }, enabled: isSmart},
		{id: "draw.copy-link-id", menu: menuLayer, sub: smartObjects, label: u.t("Copy the link id"), run: func() { a.copyLinkID(ad) }, enabled: isSmart},
		{id: "draw.smart-filters", menu: menuLayer, sub: smartObjects, label: u.t("Smart filters…"), run: func() { a.smartList = ad },
			enabled: func() bool { n := d.Node(d.Active); return n != nil && n.Filters != nil }},
		{id: "draw.break-the-link", menu: menuLayer, sub: smartObjects, label: u.t("Break the link"), run: run(func() error { _, err := d.UnlinkSmart(); return err }), enabled: isSmart},
		{id: "draw.convert-smart-to-group", menu: menuLayer, sub: smartObjects, label: u.t("Convert to a plain group"), key: key('F', "cs"), run: run(d.ReleaseSmart), enabled: isSmart},
		{id: "draw.define-brush-tip", menu: menuLayer, sepBefore: true, label: u.t("Define brush tip"), run: func() { a.defineTip(ad) },
			enabled: func() bool { n := d.Node(d.Active); return n != nil && !n.IsGroup() && n.Adjust == nil }},
		{id: "draw.rasterize", menu: menuLayer, label: u.t("Rasterize"), run: run(d.Rasterize), enabled: func() bool { n := d.Node(d.Active); return n != nil && (n.Text != nil || n.Vector != nil) }},
		{id: "draw.boolean-add", menu: menuLayer, sub: u.t("Combine shapes"), label: u.t(combineNames[paint.BoolAdd]), run: func() { a.runBoolean(ad, paint.BoolAdd) }, enabled: twoVectors},
		{id: "draw.boolean-subtract", menu: menuLayer, sub: u.t("Combine shapes"), label: u.t(combineNames[paint.BoolSubtract]), run: func() { a.runBoolean(ad, paint.BoolSubtract) }, enabled: twoVectors},
		{id: "draw.boolean-intersect", menu: menuLayer, sub: u.t("Combine shapes"), label: u.t(combineNames[paint.BoolIntersect]), run: func() { a.runBoolean(ad, paint.BoolIntersect) }, enabled: twoVectors},
		{id: "draw.boolean-exclude", menu: menuLayer, sub: u.t("Combine shapes"), label: u.t(combineNames[paint.BoolExclude]), run: func() { a.runBoolean(ad, paint.BoolExclude) }, enabled: twoVectors},
		{id: "draw.convert-to-curves", menu: menuLayer, label: u.t("Convert to curves"), run: func() { a.convertToCurves(ad) }, enabled: func() bool {
			n := d.Node(d.Active)
			return n != nil && n.Vector != nil && n.Vector.Kind != paint.VectorCurve
		}},
		{id: "draw.expand-shapes", menu: menuLayer, label: u.t("Merge shape components"), run: run(func() error { return d.ExpandShapes(d.Active) }), enabled: compound},
		{id: "draw.delete-vector-mask", menu: menuLayer, sub: vectorMask, label: u.t("Delete vector mask"), run: run(func() error { return d.DeleteClip(d.Active) }), enabled: hasClip, sepBefore: true},
		{id: "draw.rasterize-vector-mask", menu: menuLayer, sub: vectorMask, label: u.t("Rasterize vector mask"), run: run(func() error { return d.RasterizeClip(d.Active) }), enabled: hasClip},
		{id: "draw.merge", menu: menuLayer, label: mergeLabel, key: key('E', "c"), run: run(merge), enabled: chosen, sepBefore: true},
		{id: "draw.flatten-image", menu: menuLayer, label: u.t("Flatten image"), run: plain(func() { d.Flatten(u.tc("layer", "Background")) })},

		{id: "draw.all", menu: menuSelect, label: u.t("All"), key: key('A', "c"), run: plain(d.SelectAll)},
		{id: "draw.deselect", menu: menuSelect, label: u.t("Deselect"), key: key('D', "c"), run: plain(d.Deselect), enabled: selected},
		{id: "draw.reselect", menu: menuSelect, label: u.t("Reselect"), key: key('D', "cs"), run: plain(d.Reselect)},
		{id: "draw.inverse", menu: menuSelect, label: u.t("Inverse"), key: key('I', "cs"), run: plain(d.InvertSelection)},
		{id: "draw.select-subject", menu: menuSelect, label: u.t("Subject"), run: func() { a.selectSubject(ad) }, enabled: func() bool { return !ad.matting }, sepBefore: true},
		{id: "draw.feather", menu: menuSelect, label: u.t("Feather…"), key: key(0x76, "s"), run: func() { a.openDialog(dialogFeather) }, enabled: selected, sepBefore: true},
		{id: "draw.select-expand", menu: menuSelect, sub: modify, label: u.t("Expand…"), run: func() { a.openDialog(dialogExpand) }, enabled: selected},
		{id: "draw.select-contract", menu: menuSelect, sub: modify, label: u.t("Contract…"), run: func() { a.openDialog(dialogContract) }, enabled: selected},
		{id: "draw.select-smooth", menu: menuSelect, sub: modify, label: u.t("Smooth…"), run: func() { a.openDialog(dialogSmooth) }, enabled: selected},
		{id: "draw.select-border", menu: menuSelect, sub: modify, label: u.t("Border…"), run: func() { a.openDialog(dialogBorder) }, enabled: selected},
		{id: "draw.edit-the-layer-mask", menu: menuSelect, label: u.t("Edit the layer mask"), key: key(0xdc, "c"), run: func() { d.EditMask = !d.EditMask && hasMask() }, enabled: hasMask,
			checked: func() bool { return d.EditMask }, sepBefore: true},

		{id: "draw.develop", menu: menuFilter, label: u.t("Develop…"), key: key('A', "cs"), run: adjust(filterDevelop), enabled: filterable},
		{id: "draw.gaussian-blur", menu: menuFilter, sepBefore: true, sub: u.t("Blur"), label: u.t("Gaussian blur…"), run: adjust(filterBlur), enabled: filterable},
		{id: "draw.add-noise", menu: menuFilter, sub: u.t("Noise"), label: u.t("Add noise…"), run: adjust(filterNoise), enabled: filterable},
		{id: "draw.mosaic", menu: menuFilter, sub: u.t("Pixelate"), label: u.t("Mosaic…"), run: adjust(filterMosaic), enabled: filterable},
		{id: "draw.unsharp-mask", menu: menuFilter, sub: u.t("Sharpen"), label: u.t("Unsharp mask…"), run: adjust(filterSharpen), enabled: filterable},

		{id: "draw.zoom-in", menu: menuView, label: u.t("Zoom in"), key: key(keyEquals, "ca"), run: centre(ad.zoom * 1.25)},
		{id: "draw.zoom-out", menu: menuView, label: u.t("Zoom out"), key: key(keyMinus, "ca"), run: centre(ad.zoom / 1.25)},
		{id: "draw.fit-on-screen", menu: menuView, label: u.t("Fit on screen"), key: fitKey, run: ad.fitView},
		{id: "draw.snapping", menu: menuView, label: u.t("Snapping"), key: key(0xba, "cs"), run: func() { a.settings.Paint.NoSnap = !a.settings.Paint.NoSnap; a.saveSettings() },
			checked: func() bool { return !a.settings.Paint.NoSnap }},
		{id: "draw.100pct", menu: menuView, label: u.t("100%"), key: key('1', "ca"), run: centre(1)},
		{id: "draw.show-the-video-behind-the-drawing", menu: menuView, label: u.t("Show the video behind the drawing"), run: func() { ad.noBack = !ad.noBack; ad.view = nil },
			enabled: func() bool { return ad.backdrop != nil }, checked: func() bool { return ad.backdrop != nil && !ad.noBack }, sepBefore: true},
	}
	// Any command applies Free Transform, and commits the text being typed, first, as Photoshop does; undo then
	// takes it back.
	for i := range cmds {
		run := cmds[i].run
		cmds[i].run = func() { a.applyTransform(ad); a.commitText(ad); a.finishPen(ad); run() }
	}
	if ad.onScreen {
		cmds = slices.DeleteFunc(cmds, func(c artCommand) bool { return screenLeavesOut[c.id] })
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
			u.c.Fill(tr, colSurface)
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
		it := menuItem{label: c.label, key: a.keyHint(c.id, c.defaults()), run: c.run}
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

// selectionAnts are the marching ants of a selection as the view shows them: a picture the view's size, clear but
// for its edge, worked out on the screen's pixels so the edge is a whole line at any zoom (worked out on the
// canvas's pixels, a zoomed-out edge would fall between screen pixels and break up). It is made again when the
// selection, the zoom, the pan or the view changes.
type selectionAnts struct {
	of  *paint.Mask
	key viewKey
	img *image.RGBA // over the view, at at
	at  image.Point
}

// antsOf works out the marching ants of sel over the view: a pixel inside the selection (half selected or more)
// with a neighbour outside is the edge, black and white by turns so it shows on any picture.
func (a *App) antsOf(ad *artDoc, vp gfx.Rect, sel *paint.Mask) selectionAnts {
	u := &a.u
	rp := vp.Pixels()
	out := selectionAnts{of: sel, key: viewKey{vp: rp, zoom: ad.zoom, x: ad.panX, y: ad.panY}}
	sx, sy := ad.origin(vp)
	z := ad.zoom
	// Only around where the selection can change: what it selects, or for one selecting everything beyond its
	// tiles, those.
	b := sel.Bounds()
	if sel.Fill() != 0 {
		b = sel.TileBounds()
	}
	area := image.Rect(int(math.Floor(sx+float64(b.Min.X)*z))-2, int(math.Floor(sy+float64(b.Min.Y)*z))-2,
		int(math.Ceil(sx+float64(b.Max.X)*z))+2, int(math.Ceil(sy+float64(b.Max.Y)*z))+2).Intersect(rp)
	if sel.Fill() != 0 && b.Empty() || area.Empty() {
		return out // all selected, or nothing of the edge in view
	}
	// The selection at the level of detail the zoom needs, read once over what the area covers.
	l := paint.LevelFor(z)
	scale := z * float64(int(1)<<l)
	lr := image.Rect(int(math.Floor((float64(area.Min.X)-sx)/scale))-1, int(math.Floor((float64(area.Min.Y)-sy)/scale))-1,
		int(math.Ceil((float64(area.Max.X)-sx)/scale))+1, int(math.Ceil((float64(area.Max.Y)-sy)/scale))+1)
	m := sel.ReadLevel(l, lr)
	w, h := area.Dx()+2, area.Dy()+2 // a pixel more each side, for the neighbours
	in := make([]bool, w*h)
	for j := range h {
		ly := int(math.Floor((float64(area.Min.Y-1+j)+0.5-sy)/scale)) - lr.Min.Y
		for i := range w {
			lx := int(math.Floor((float64(area.Min.X-1+i)+0.5-sx)/scale)) - lr.Min.X
			v := sel.Fill()
			if lx >= 0 && ly >= 0 && lx < lr.Dx() && ly < lr.Dy() {
				v = m.Pix[ly*m.Stride+lx]
			}
			in[j*w+i] = v >= 128
		}
	}
	out.img, out.at = image.NewRGBA(image.Rect(0, 0, area.Dx(), area.Dy())), area.Min
	dash := max(int(u.px(4)), 2)
	for j := 1; j < h-1; j++ {
		for i := 1; i < w-1; i++ {
			k := j*w + i
			if !in[k] || in[k-1] && in[k+1] && in[k-w] && in[k+w] {
				continue
			}
			// Black and white in tiles a pixel taller than wide, so an edge running any way alternates: in squares, a 45°
			// edge can cross from tile to tile at corners only, and keep one colour all along.
			c := colBlack
			if ((area.Min.X+i)/dash+(area.Min.Y+j)/(dash+1))%2 == 1 {
				c = colWhite
			}
			out.img.SetRGBA(i-1, j-1, c)
		}
	}
	return out
}

// drawSelectionOutline draws the marching ants along the selection's edge.
func (a *App) drawSelectionOutline(ad *artDoc, vp gfx.Rect) {
	sel := ad.d.Selection
	if sel == nil {
		return
	}
	if key := (viewKey{vp: vp.Pixels(), zoom: ad.zoom, x: ad.panX, y: ad.panY}); ad.ants.of != sel || ad.ants.key != key {
		ad.ants = a.antsOf(ad, vp, sel)
	}
	if ad.ants.img != nil {
		a.u.c.Blit(ad.ants.img, ad.ants.at.X, ad.ants.at.Y, false)
	}
}
