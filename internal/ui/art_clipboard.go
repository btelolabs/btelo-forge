package ui

import (
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"slices"

	"github.com/btelolabs/btelo-forge/internal/paint"
)

// Cut, copy and paste on the canvas. A layer, group or smart object chosen with nothing selected is kept here whole,
// with its picture on the system clipboard, and pastes into any canvas where it was, its masks, settings and
// layers with it; a smart object pasted keeps its link, taking what is kept of it. Pixels go through the system
// clipboard as a picture. A vector object, or the
// shapes of it the node tool has chosen, is kept here as an object, with its picture on the system clipboard for
// other programs; pasting it combines it, as a shape, into the chosen vector layer, or makes it the vector mask of
// the chosen pixel layer or group, or else puts it on a layer of its own.

// nodeClip is a layer, group or smart object cut or copied on a canvas, frozen; sig and system are as vectorClip's.
type nodeClip struct {
	n      *paint.Node
	sig    uint64
	system bool
}

// copyNode keeps node n of the canvas as the layer clipboard and puts its picture on the system clipboard.
func (a *App) copyNode(ad *artDoc, n *paint.Node) {
	frozen, err := ad.d.CopyNode(n.ID)
	if err != nil {
		a.artFailed(ad, err)
		return
	}
	nc := &nodeClip{n: frozen}
	if cb, ok := a.host.(imageClipboard); ok {
		if pic := paint.NodePicture(frozen); pic != nil && cb.SetClipboardImage(pic) == nil {
			if back, _, _ := a.host.Paste(); back != nil {
				nc.sig, nc.system = clipSig(back), true
			}
		}
	}
	a.nodeClip, a.vecClip = nc, nil
	a.toast(a.u.tf("Copied “%s”", n.Name), false)
}

// pastedNode is the layer clipboard when the system clipboard still holds it, or holds nothing of its own.
func (a *App) pastedNode(img image.Image, files []string) *paint.Node {
	nc := a.nodeClip
	switch {
	case nc == nil:
		return nil
	case img == nil && len(files) == 0, nc.system && img != nil && clipSig(img) == nc.sig:
		return nc.n
	}
	return nil
}

// pasteNode pastes a copied layer, group or smart object into the canvas; its smart objects take what is kept of
// their links, so an old clipboard does not take a change back.
func (a *App) pasteNode(ad *artDoc, n *paint.Node) {
	a.artRun(ad, func() error {
		ad.d.PasteNode(n)
		return nil
	})
	for _, id := range ad.d.SmartLinks() {
		if c, ok := a.currentSmart(id, ad); ok && ad.d.ApplySmart(c) {
			ad.changedAll()
		}
	}
}

// vectorClip is a vector object cut or copied on the canvas. sig is its picture as it came back from the system
// clipboard, so a paste can tell whether the clipboard still holds it or something copied since; system says the
// picture is there at all.
type vectorClip struct {
	v      paint.Vector
	sig    uint64
	system bool
}

// copiedShapes is what a copy of vectors would take from the chosen layer: with the node tool, the shapes its
// chosen nodes are in, of the object or vector mask it edits; else a vector layer's whole object, when nothing is
// selected. all says every shape of it is taken, and clip that it is a vector mask.
func (a *App) copiedShapes(ad *artDoc) (n *paint.Node, v paint.Vector, shapes []int, clip, ok bool) {
	n = ad.d.Node(ad.d.Active)
	if n == nil {
		return nil, v, nil, false, false
	}
	ev := n.EditedVector()
	if a.artTool == toolNode && ev != nil && len(ad.nodeState().sel) > 0 {
		for _, r := range ad.nodeState().sel {
			if i := ev.ShapeOf(r.Sub); i >= 0 && !slices.Contains(shapes, i) {
				shapes = append(shapes, i)
			}
		}
		slices.Sort(shapes)
		v, _ = ev.Shape(shapes[0])
		for _, i := range shapes[1:] {
			s, _ := ev.Shape(i)
			v = v.WithShape(s, ev.ShapeOpOf(i))
		}
		return n, v, shapes, n.Vector == nil, true
	}
	if n.Vector == nil || ad.d.Selection != nil {
		return nil, v, nil, false, false
	}
	for i := range n.Vector.Shapes() {
		shapes = append(shapes, i)
	}
	return n, *n.Vector, shapes, false, true
}

// clipSig is a quick fingerprint of a picture: its size and a grid of its pixels, unpremultiplied as clipboards
// keep them.
func clipSig(img image.Image) uint64 {
	h := fnv.New64a()
	b := img.Bounds()
	fmt.Fprint(h, b.Dx(), b.Dy())
	const grid = 64
	for j := range grid {
		for i := range grid {
			x, y := b.Min.X+i*b.Dx()/grid, b.Min.Y+j*b.Dy()/grid
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if c.A == 0 {
				c = color.NRGBA{}
			}
			h.Write([]byte{c.R, c.G, c.B, c.A})
		}
	}
	return h.Sum64()
}

// copyVector keeps v as the vector clipboard and puts its picture on the system clipboard.
func (a *App) copyVector(v paint.Vector) {
	vc := &vectorClip{v: v}
	if cb, ok := a.host.(imageClipboard); ok {
		r := v.PixelBounds()
		if !r.Empty() {
			img := image.NewRGBA(r)
			v.Render(img, r, 1)
			if cb.SetClipboardImage(img) == nil {
				if back, _, _ := a.host.Paste(); back != nil {
					vc.sig, vc.system = clipSig(back), true
				}
			}
		}
	}
	a.vecClip, a.nodeClip = vc, nil
}

// artCopy puts the chosen vector shapes on the clipboard, or else the selected part of the chosen layer, or of the
// picture as it looks.
func (a *App) artCopy(ad *artDoc, merged bool) {
	if !merged {
		if _, v, shapes, _, ok := a.copiedShapes(ad); ok {
			a.copyVector(v)
			a.toast(a.u.tn(len(shapes), "Copied %d shape", "Copied %d shapes", len(shapes)), false)
			return
		}
		if n := ad.d.Node(ad.d.Active); n != nil && ad.d.Selection == nil {
			a.copyNode(ad, n)
			return
		}
	}
	cb, ok := a.host.(imageClipboard)
	if !ok {
		a.toast(a.u.t("Pictures cannot be put on the clipboard on this system"), true)
		return
	}
	img, err := ad.d.Copy(merged)
	if err == nil {
		err = cb.SetClipboardImage(img)
	}
	if err != nil {
		a.toast(a.u.tf("Could not copy: %s", a.describe(err)), true)
		return
	}
	a.vecClip, a.nodeClip = nil, nil
	a.toast(a.u.tf("Copied %d × %d", img.Rect.Dx(), img.Rect.Dy()), false)
}

// artCut is a copy that takes away what it copied: the shapes from their object (the object's layer, or the vector
// mask, when they are all of it), or the selected pixels.
func (a *App) artCut(ad *artDoc) {
	if n, v, shapes, clip, ok := a.copiedShapes(ad); ok {
		a.copyVector(v)
		ev := n.EditedVector()
		if len(shapes) == len(ev.Shapes()) {
			if clip {
				a.artRun(ad, func() error { return ad.d.DeleteClip(n.ID) })
			} else {
				a.artRun(ad, ad.d.Delete)
			}
			return
		}
		rest := *ev
		for _, i := range slices.Backward(shapes) {
			rest = rest.WithoutShape(i)
		}
		ad.nodeState().sel = nil
		a.artRun(ad, func() error { ad.d.SetVector(n.ID, rest, "Cut"); return nil })
		return
	}
	if n := ad.d.Node(ad.d.Active); n != nil && ad.d.Selection == nil {
		a.copyNode(ad, n)
		a.artRun(ad, ad.d.Delete)
		return
	}
	cb, ok := a.host.(imageClipboard)
	if !ok {
		a.toast(a.u.t("Pictures cannot be put on the clipboard on this system"), true)
		return
	}
	img, err := ad.d.Cut()
	if err == nil {
		err = cb.SetClipboardImage(img)
	}
	if err != nil {
		a.toast(a.u.tf("Could not cut: %s", a.describe(err)), true)
		return
	}
	a.vecClip, a.nodeClip = nil, nil
	ad.changedAll()
}

// pastedVector is the vector clipboard when the system clipboard still holds it, or holds nothing of its own.
func (a *App) pastedVector(img image.Image, files []string) *paint.Vector {
	vc := a.vecClip
	switch {
	case vc == nil:
		return nil
	case img == nil && len(files) == 0, vc.system && img != nil && clipSig(img) == vc.sig:
		return &vc.v
	}
	return nil
}

// artPaste pastes the clipboard: copied vectors as a shape combined by op into the chosen vector layer, or as the
// vector mask of the chosen layer or group, or with newLayer on a layer of their own; else its picture on a new
// layer, at the selection's corner or in the middle.
func (a *App) artPaste(ad *artDoc, op paint.ShapeOp, newLayer bool) {
	img, files, _ := a.host.Paste()
	if n := a.pastedNode(img, files); n != nil {
		a.pasteNode(ad, n)
		return
	}
	if v := a.pastedVector(img, files); v != nil {
		a.pasteShape(ad, *v, op, newLayer)
		return
	}
	if img == nil {
		for _, f := range files {
			if isImage(f) {
				img, _ = loadImage(f)
				break
			}
		}
	}
	if img == nil {
		a.toast(a.u.t("There is no picture on the clipboard"), false)
		return
	}
	var at *image.Point
	if s := ad.d.SelectionBounds(); !s.Empty() {
		at = &s.Min
	}
	ad.d.Paste(img, a.u.t("Pasted"), at)
	ad.changedAll()
}

// pasteShape pastes a copied vector object as artPaste says.
func (a *App) pasteShape(ad *artDoc, v paint.Vector, op paint.ShapeOp, newLayer bool) {
	u := &a.u
	d := ad.d
	n := d.Node(d.Active)
	if newLayer || n == nil || n.Adjust != nil && n.Vector == nil {
		a.artRun(ad, func() error {
			d.AddVector(v, u.tf("Shape %d", len(d.Layers())+1))
			return nil
		})
		return
	}
	before := 0 // the subpaths it has, a smart shape's as the curve it becomes
	if ev := n.EditedVector(); ev != nil {
		before = len(ev.Outline())
	}
	had := n.Vector != nil || n.Clip != nil
	a.artRun(ad, func() error { return d.AddShape(n.ID, v, op) })
	n = d.Node(n.ID)
	ev := n.EditedVector()
	if ev == nil {
		return
	}
	// The pasted shape's nodes are chosen, so the node tool's buttons change how it combines.
	ne := ad.nodeState()
	ne.layer, ne.sel = n.ID, nil
	for si := before; si < len(ev.Path); si++ {
		for ni := range ev.Path[si].Nodes {
			ne.sel = append(ne.sel, paint.NodeRef{Sub: si, Node: ni})
		}
	}
	switch {
	case n.Vector != nil:
		a.toast(u.tf("Pasted into “%s” as a shape: the node tool chooses how it combines", n.Name), false)
	case had:
		a.toast(u.tf("Pasted into the vector mask of “%s”", n.Name), false)
	default:
		a.toast(u.tf("Pasted as the vector mask of “%s”: the layer shows inside it", n.Name), false)
	}
}
