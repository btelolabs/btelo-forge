package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg" // the pictures a model may hold
	"image/png"
	"math"
	"slices"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	s3 "github.com/btelolabs/btelo-forge/internal/scene3d"
)

// Painting on a model: the brush is the drawing tool's, sized in pixels of the view, and goes over the surface as a ball,
// so that a stroke crosses the UV charts' edges as if they were not there.

// modelPicture is a picture of the file open for painting.
type modelPicture struct {
	image    int
	slot     int // of materialSlots
	material int
	geometry int // what painter was made from
	im       *image.NRGBA
	painter  *s3.Painter
	mask     []uint8 // how far the stroke has covered each texel
	before   []byte  // the picture as the stroke found it
	dirty    image.Rectangle
	pending  image.Rectangle // changed and not yet on the card
	changed  bool            // differs from the file's bytes
}

// paintPatch is what a stroke replaced, as a step of undo.
type paintPatch struct {
	image int
	rect  image.Rectangle
	pix   []byte
}

// flushPaint puts what was painted in the asset's own pictures.
func (md *modelDoc) flushPaint() {
	for _, p := range md.pictures {
		if !p.changed {
			continue
		}
		var out bytes.Buffer
		if png.Encode(&out, p.im) != nil {
			continue
		}
		md.asset.Images = slices.Clone(md.asset.Images)
		md.asset.Images[p.image].Data = out.Bytes()
		md.asset.Images[p.image].Mime = "image/png"
		p.changed = false
	}
}

// pictureOf opens the picture that material paints on; it is nil when it has none that can be read.
func (a *App) pictureOf(md *modelDoc, material int) *modelPicture {
	if material < 0 || material >= len(md.asset.Materials) {
		return nil
	}
	slot := paintChannels[md.paintChannel].slot
	index := materialSlots(&md.asset.Materials[material])[slot].Image
	if index < 0 || index >= len(md.asset.Images) {
		return nil
	}
	p := md.pictures[index]
	if p == nil {
		decoded, _, err := image.Decode(bytes.NewReader(md.asset.Images[index].Data))
		if err != nil {
			a.toast(a.u.tf("The picture could not be read: %s", a.describe(err)), true)
			return nil
		}
		b := decoded.Bounds()
		im := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		draw.Draw(im, im.Rect, decoded, b.Min, draw.Src)
		p = &modelPicture{image: index, slot: slot, im: im, geometry: -1, material: material}
		if md.pictures == nil {
			md.pictures = map[int]*modelPicture{}
		}
		md.pictures[index] = p
	}
	if p.geometry != md.geometry || p.material != material {
		w, h := p.im.Rect.Dx(), p.im.Rect.Dy()
		p.painter, p.geometry, p.material = s3.NewPainter(md.surfaces, material, w, h), md.geometry, material
		p.mask = make([]uint8, w*h)
	}
	return p
}

// worldBrush is the radius in the world of the brush at a distance from the eye, in a view h high.
func (a *App) worldBrush(md *modelDoc, distance, h float32) float32 {
	return float32(a.brush.Size) / 2 * 2 * float32(math.Tan(float64(md.cam.FovY)/2)) * distance / h
}

// beginStroke starts painting at the point of the view r under the pointer, if a surface with a picture is there.
func (a *App) beginStroke(md *modelDoc, r gfx.Rect, x, y float32) {
	if md.picker == nil {
		return
	}
	dir := md.cam.Ray(x, y, r.W, r.H)
	distance, s, _, ok := md.picker.Hit(md.cam.Pos, dir)
	if !ok {
		return
	}
	p := a.pictureOf(md, md.surfaces[s].Material)
	if p == nil {
		a.toast(a.u.t("This object has no picture to paint on: make one in the panel first"), true)
		return
	}
	a.paintFocus(md, p)
	clear(p.mask)
	p.before = slices.Clone(p.im.Pix)
	p.dirty = image.Rectangle{}
	md.stroke = p
	at := md.cam.Pos.Add(dir.Scale(distance))
	md.strokeAt = at
	a.dab(md, p, at, dir, a.worldBrush(md, distance, r.H))
}

// paintFocus sends the card the picture to change, unless it has it.
func (a *App) paintFocus(md *modelDoc, p *modelPicture) {
	if md.paintImage != p.image || md.paintVersion != md.version {
		md.paintImage, md.paintVersion, md.paintStart = p.image, md.version, p
	}
}

// strokeTo goes on to the point under the pointer, with dabs close enough to make a line.
func (a *App) strokeTo(md *modelDoc, r gfx.Rect, x, y float32) {
	p := md.stroke
	dir := md.cam.Ray(x, y, r.W, r.H)
	distance, s, _, ok := md.picker.Hit(md.cam.Pos, dir)
	if !ok || md.surfaces[s].Material != p.material {
		return
	}
	at := md.cam.Pos.Add(dir.Scale(distance))
	radius := a.worldBrush(md, distance, r.H)
	gap := max(radius*float32(max(a.brush.Spacing, .1)), 1e-6)
	length := at.Sub(md.strokeAt).Length()
	if length > radius*8 {
		md.strokeAt = at // a jump across a gap, not a line through the air
	}
	for go2 := gap; go2 <= length; go2 += gap {
		a.dab(md, p, md.strokeAt.Add(at.Sub(md.strokeAt).Scale(go2/length)), dir, radius)
	}
	if length >= gap {
		md.strokeAt = md.strokeAt.Add(at.Sub(md.strokeAt).Scale(math32Floor(length/gap) * gap / length))
	}
}

func math32Floor(v float32) float32 { return float32(math.Floor(float64(v))) }

// dab lays one touch of the brush at a point of the surface.
func (a *App) dab(md *modelDoc, p *modelPicture, center, view s3.Vec3, radius float32) {
	b := &a.brush
	soft := 1 - float32(min(max(b.Hardness, 0), 1))
	flow := float32(max(b.Flow, .02))
	opacity := float32(b.Opacity)
	w := p.im.Rect.Dx()
	h := p.im.Rect.Dy()
	pix := p.im.Pix
	c := b.Color
	box := image.Rectangle{Min: image.Pt(w, h)}
	channel := paintChannels[md.paintChannel]
	if channel.part >= 0 {
		v := uint8(md.paintValue*255 + .5)
		c.R, c.G, c.B = v, v, v
	}
	p.painter.Dab(center, view, radius, func(x, y int, distance float32) {
		edge := float32(1)
		if t := distance / radius; soft > 0 && t > 1-soft {
			edge = (1 - t) / soft
			edge = edge * edge * (3 - 2*edge)
		}
		i := y*w + x
		m := float32(p.mask[i]) / 255
		m += (1 - m) * flow * edge
		p.mask[i] = uint8(m*255 + .5)
		k := m * opacity
		o := p.before[i*4:]
		q := pix[i*4:]
		for n, to := range [3]uint8{c.R, c.G, c.B} {
			if channel.part < 0 || channel.part == n {
				q[n] = uint8(float32(o[n]) + (float32(to)-float32(o[n]))*k + .5)
			}
		}
		box.Min.X, box.Min.Y, box.Max.X, box.Max.Y = min(box.Min.X, x-2), min(box.Min.Y, y-2), max(box.Max.X, x+3), max(box.Max.Y, y+3)
		// What lies outside the charts follows, for the smaller levels to find no old colour at their edges.
		for _, d := range [...][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}, {-2, 0}, {2, 0}, {0, -2}, {0, 2}} {
			nx, ny := x+d[0], y+d[1]
			if nx >= 0 && ny >= 0 && nx < w && ny < h && !p.painter.Covered(nx, ny) {
				copy(pix[(ny*w+nx)*4:(ny*w+nx)*4+3], q[:3])
			}
		}
	})
	if box.Min.X >= box.Max.X {
		return
	}
	box = box.Intersect(p.im.Rect)
	p.dirty, p.pending = p.dirty.Union(box), p.pending.Union(box)
	p.changed = true
	md.painted++
	a.requestFrame()
}

// endStroke makes the stroke a step of undo.
func (a *App) endStroke(md *modelDoc) {
	p := md.stroke
	md.stroke = nil
	if p == nil || p.dirty.Empty() {
		return
	}
	md.remember(&modelShot{paint: copyPatch(p.image, p.before, p.im.Rect.Dx(), p.dirty)})
	a.pushStroke(md, p)
	p.before = nil
	md.dirty, md.thumb = true, nil
}

func copyPatch(image int, pix []byte, w int, r image.Rectangle) *paintPatch {
	out := &paintPatch{image: image, rect: r, pix: make([]byte, r.Dx()*r.Dy()*4)}
	for y := range r.Dy() {
		copy(out.pix[y*r.Dx()*4:(y+1)*r.Dx()*4], pix[((r.Min.Y+y)*w+r.Min.X)*4:])
	}
	return out
}

// undoPaint swaps a picture's pixels with those of a step, and gives the step that swaps them back.
func (a *App) undoPaint(md *modelDoc, s *paintPatch) *paintPatch {
	p := md.pictures[s.image]
	if p == nil {
		return s
	}
	w := p.im.Rect.Dx()
	back := copyPatch(s.image, p.im.Pix, w, s.rect)
	for y := range s.rect.Dy() {
		copy(p.im.Pix[((s.rect.Min.Y+y)*w+s.rect.Min.X)*4:], s.pix[y*s.rect.Dx()*4:(y+1)*s.rect.Dx()*4])
	}
	p.pending, p.changed = p.pending.Union(s.rect), true
	a.paintFocus(md, p)
	md.painted++
	md.dirty, md.thumb = true, nil
	a.requestFrame()
	return back
}

// paintMouse paints with the left button in the view r.
func (a *App) paintMouse(md *modelDoc, r gfx.Rect) {
	u := &a.u
	const id = "model/paint"
	if !a.modelHolding(md) && u.press(id, r) {
		a.beginStroke(md, r, u.pressX-r.X, u.pressY-r.Y)
	}
	if md.stroke != nil && u.dragging(id) {
		a.strokeTo(md, r, u.mx-r.X, u.my-r.Y)
	}
	if u.letGo(id) {
		a.endStroke(md)
	}
	if r.Contains(u.mx, u.my) && !u.blocked {
		u.cursor = CursorCross
	}
}

// paintingNow reports whether the mouse paints in the view.
func (md *modelDoc) paintingNow() bool {
	return md.panel == modelPanelPaint && md.edit == nil && md.clip < 0 && md.asset != nil
}

// drawBrushRing shows the brush's reach at the pointer.
func (a *App) drawBrushRing(md *modelDoc, r gfx.Rect) {
	u := &a.u
	if u.blocked || !r.Contains(u.mx, u.my) {
		return
	}
	half := float32(a.brush.Size) / 2
	ring := rectAt(u.mx-half, u.my-half, half*2, half*2)
	u.c.Push(r)
	u.c.RoundOutline(ring, half, max(1, u.px(1)), colWhite)
	u.c.Pop()
}

// carryPictures draws the materials' pictures again for the layout now, from the surfaces as they were before it changed.
func (a *App) carryPictures(md *modelDoc, before []s3.Surface) {
	md.flushPaint()
	moved := false
	images := slices.Clone(md.asset.Images)
	done := map[int]bool{}
	for m := range md.asset.Materials {
		mat := md.asset.Materials[m]
		for _, ref := range materialSlots(&mat) {
			if ref.Image < 0 || ref.Image >= len(images) || done[ref.Image] {
				continue
			}
			done[ref.Image] = true
			decoded, _, err := image.Decode(bytes.NewReader(images[ref.Image].Data))
			if err != nil {
				continue
			}
			src := image.NewNRGBA(image.Rect(0, 0, decoded.Bounds().Dx(), decoded.Bounds().Dy()))
			draw.Draw(src, src.Rect, decoded, decoded.Bounds().Min, draw.Src)
			var out bytes.Buffer
			if png.Encode(&out, s3.TransferUV(before, md.surfaces, m, src)) == nil {
				images[ref.Image].Data, images[ref.Image].Mime = out.Bytes(), "image/png"
				moved = true
			}
		}
	}
	if moved {
		md.asset.Images = images
		md.pictures = nil
		md.version++
		md.thumb = nil
		md.paintVersion = -1
	}
}

// unwrap lays out the chosen object's mesh, or the one being modelled, or the whole file's, in new UVs, keeping the
// pictures on the model.
func (a *App) unwrap(md *modelDoc) {
	const limit, margin = 66, .012
	before := md.surfaces
	if md.edit != nil {
		a.editKeepingPictures(md, before, func(e *modelEdit) { e.em.AutoUnwrap(limit, margin) })
		return
	}
	md.remember(md.shot())
	md.asset.Meshes = slices.Clone(md.asset.Meshes)
	done := map[int]bool{}
	nodes := md.underChosen()
	if md.chosen < 0 {
		for n := range md.asset.Nodes {
			nodes[n] = true
		}
	}
	for n := range nodes {
		i := md.asset.Nodes[n].Mesh
		if i < 0 || done[i] {
			continue
		}
		done[i] = true
		em := s3.EditMeshOf(&md.asset.Meshes[i])
		em.AutoUnwrap(limit, margin)
		md.asset.Meshes[i].Prims, _ = em.Bake()
	}
	md.dirty, md.thumb = true, nil
	a.reshape(md, true)
	a.carryPictures(md, before)
	a.objectOverlay(md)
}

// paintChannels is what a brush can paint on: a slot of a material and, for a picture of numbers, the part of each pixel,
// or -1 for all three.
var paintChannels = []struct {
	name       string
	slot, part int
}{{"Base colour", slotBase, -1}, {"Roughness", slotMetalRough, 1}, {"Metallic", slotMetalRough, 2}, {"Emission", slotEmissive, -1}}

// makePicture gives the chosen object, or the file's first, a picture of side pixels to paint on, for the channel in hand.
func (a *App) makePicture(md *modelDoc, side int) {
	material := md.materialOfChosen()
	if md.chosen < 0 && len(md.surfaces) > 0 {
		material = md.surfaces[0].Material
	}
	if material < 0 {
		a.addMaterial(md)
		a.assignToAll(md)
		material = md.material
	}
	slot := paintChannels[md.paintChannel].slot
	mat := md.asset.Materials[material]
	fill := [4]uint8{255, 255, 255, 255}
	switch slot {
	case slotMetalRough: // the picture takes over from the numbers, which go to one
		fill = [4]uint8{255, uint8(mat.Roughness*255 + .5), uint8(mat.Metallic*255 + .5), 255}
	case slotEmissive:
		fill = [4]uint8{0, 0, 0, 255}
	}
	im := image.NewNRGBA(image.Rect(0, 0, side, side))
	for i := 0; i < len(im.Pix); i += 4 {
		copy(im.Pix[i:], fill[:])
	}
	var out bytes.Buffer
	if err := png.Encode(&out, im); err != nil {
		a.toast(a.describe(err), true)
		return
	}
	md.material = material
	a.setTexture(md, slot, s3.Image{Name: fmt.Sprintf("paint-%d.png", len(md.asset.Images)+1), Data: out.Bytes(), Mime: "image/png"})
	md.asset.Materials = slices.Clone(md.asset.Materials)
	m := &md.asset.Materials[material]
	switch slot {
	case slotBase:
		m.BaseColor = s3.Vec4{1, 1, 1, m.BaseColor[3]}
	case slotMetalRough:
		m.Metallic, m.Roughness = 1, 1
	case slotEmissive:
		m.Emissive = s3.Vec3{1, 1, 1}
	}
	md.materials++
	md.thumb = nil
}

// assignToAll gives the material shown to every part that has none.
func (a *App) assignToAll(md *modelDoc) {
	md.asset.Meshes = slices.Clone(md.asset.Meshes)
	for i := range md.asset.Meshes {
		prims := slices.Clone(md.asset.Meshes[i].Prims)
		for p := range prims {
			if prims[p].Material < 0 {
				prims[p].Material = md.material
			}
		}
		md.asset.Meshes[i].Prims = prims
	}
	a.reshape(md, true)
}

// uvThumb draws the layout over the picture, kept until either changes.
func (a *App) uvThumb(md *modelDoc, p *modelPicture, side int) *image.RGBA {
	if md.thumb != nil && md.thumbGeometry == md.geometry && md.thumb.Rect.Dx() == side {
		return md.thumb
	}
	c := gfx.NewCanvas(side, side)
	c.Clear(colBackground)
	if p != nil {
		w, h := p.im.Rect.Dx(), p.im.Rect.Dy()
		for y := range side {
			for x := range side {
				q := p.im.PixOffset(x*w/side, y*h/side)
				c.Img.SetRGBA(x, y, color.RGBA{p.im.Pix[q], p.im.Pix[q+1], p.im.Pix[q+2], 255})
			}
		}
	}
	line := color.RGBA{R: 86, G: 199, B: 184, A: 60}
	scale := float32(side)
	const most = 60000 // of edges, past which the layout is a solid mass anyway
	drawn := 0
	for i := range md.surfaces {
		s := &md.surfaces[i]
		if s.UV == nil {
			continue
		}
		for t := 0; t+2 < len(s.Indices) && drawn < most; t += 3 {
			for k := range 3 {
				a, b := s.UV[s.Indices[t+k]], s.UV[s.Indices[t+(k+1)%3]]
				c.Line(a[0]*scale, a[1]*scale, b[0]*scale, b[1]*scale, 1, line)
				drawn++
			}
		}
	}
	md.thumb, md.thumbGeometry = c.Img, md.geometry
	return c.Img
}

// drawModelPaint is the panel's tab for UVs and painting.
func (a *App) drawModelPaint(md *modelDoc, x, y, w float32) float32 {
	u := &a.u
	if md.asset == nil {
		return y
	}
	var p *modelPicture
	material := md.materialOfChosen()
	if md.chosen < 0 && len(md.surfaces) > 0 {
		material = md.surfaces[0].Material
	}
	names := make([]string, len(paintChannels))
	for i, c := range paintChannels {
		names[i] = u.t(c.name)
	}
	if pick := u.chips("model/paint/channel", rectAt(x, y, w, u.px(26)), names, md.paintChannel, true); pick != md.paintChannel {
		md.paintChannel, md.thumb = pick, nil
	}
	y += u.px(34)
	p = a.pictureOf(md, material)
	side := int(w)
	u.c.Blit(a.uvThumb(md, p, side), int(x), int(y), true)
	y += w + u.px(8)
	if a.modelTool("model/unwrap", rectAt(x, y, w, u.px(28)), u.t("Unwrap automatically"), "U", true) {
		a.unwrap(md)
	}
	y += u.px(34)
	if p != nil {
		if a.modelTool("model/drawing", rectAt(x, y, w, u.px(28)), u.t("Edit in the drawing tool"), "", true) {
			a.editInDrawing(md, p)
		}
		y += u.px(34)
	}
	if p == nil {
		sizes := []int{1024, 2048, 4096}
		half := (w - u.px(12)) / 3
		u.label(u.face(12), rectAt(x, y, w, u.px(18)), u.t("Make a picture to paint on"), colDim, 0)
		y += u.px(20)
		for i, s := range sizes {
			if u.button(fmt.Sprintf("model/picture/%d", s), rectAt(x+float32(i)*(half+u.px(6)), y, half, u.px(28)), "", fmt.Sprint(s), ghost, true) {
				a.makePicture(md, s)
			}
		}
		y += u.px(36)
	}
	b := &a.brush
	for _, s := range []struct {
		id, text string
		value    *float64
		lo, hi   float64
	}{{"size", u.t("Size"), &b.Size, 1, 400}, {"hard", u.t("Hardness"), &b.Hardness, 0, 1}, {"flow", u.t("Flow"), &b.Flow, .02, 1}, {"opacity", u.t("Opacity"), &b.Opacity, .02, 1}} {
		u.label(u.face(12), rectAt(x, y, w, u.px(18)), s.text, colDim, 0)
		u.label(u.face(12), rectAt(x, y, w, u.px(18)), fmt.Sprintf("%.2f", *s.value), colText, 1)
		y += u.px(18)
		if _, done := u.slider("model/brush/"+s.id, rectAt(x, y, w, u.px(22)), s.value, s.lo, s.hi, true); done {
			a.saveBrush()
		}
		y += u.px(24)
	}
	if paintChannels[md.paintChannel].part >= 0 {
		u.label(u.face(12), rectAt(x, y, w, u.px(18)), u.t("Value"), colDim, 0)
		u.label(u.face(12), rectAt(x, y, w, u.px(18)), fmt.Sprintf("%.2f", md.paintValue), colText, 1)
		y += u.px(18)
		u.slider("model/paint/value", rectAt(x, y, w, u.px(22)), &md.paintValue, 0, 1, true)
		return y + u.px(30)
	}
	a.drawColorPicker(rectAt(x, y, w, u.px(226)))
	return y + u.px(232)
}
