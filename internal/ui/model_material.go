package ui

import (
	"fmt"
	"image/color"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	s3 "github.com/btelolabs/btelo-forge/internal/scene3d"
)

// The kinds of picture a material holds, in the order of materialSlots.
var modelTextures = msgs("Base colour", "Metal and rough", "Normal", "Occlusion", "Emission")

// The slots of materialSlots that painting comes to.
const (
	slotBase       = 0
	slotMetalRough = 1
	slotEmissive   = 4
)

func materialSlots(m *s3.Material) []*s3.TexRef {
	return []*s3.TexRef{&m.BaseTex, &m.MetalRoughTex, &m.NormalTex, &m.OcclusionTex, &m.EmissiveTex}
}

// beginMaterialChange makes one step of undo of a drag of a material's sliders.
func (md *modelDoc) beginMaterialChange() {
	if !md.keyDrag {
		md.keyDrag = true
		md.remember(md.shot())
	}
}

// sameTextures reports whether the card's pictures still serve materials b as they served a.
func sameTextures(a, b []s3.Material) bool {
	return slices.EqualFunc(a, b, func(x, y s3.Material) bool {
		p, q := materialSlots(&x), materialSlots(&y)
		for i := range p {
			if p[i].Image != q[i].Image {
				return false
			}
		}
		return x.AlphaMode == y.AlphaMode
	})
}

// addMaterial makes a new material and chooses it, giving it to the chosen object or faces.
func (a *App) addMaterial(md *modelDoc) {
	md.remember(md.shot())
	m := s3.DefaultMaterial()
	m.Name = a.u.tf("Material %d", len(md.asset.Materials)+1)
	md.asset.Materials = append(slices.Clone(md.asset.Materials), m)
	md.material = len(md.asset.Materials) - 1
	md.dirty = true
	md.version++
	a.requestFrame()
}

// assignMaterial gives the material shown to the chosen faces while modelling, else to the chosen object.
func (a *App) assignMaterial(md *modelDoc) {
	if e := md.edit; e != nil {
		faces := e.chosenFaces()
		if a.editNeed(len(faces) > 0, hintFaces) {
			chosen := int32(md.material)
			a.editChange(md, func(e *modelEdit) error {
				for _, f := range faces {
					e.em.Faces[f].Material = chosen
				}
				return nil
			})
		}
		return
	}
	if md.chosen < 0 {
		a.toast(a.u.t(editHints[hintSomething]), true)
		return
	}
	md.remember(md.shot())
	md.asset.Meshes = slices.Clone(md.asset.Meshes)
	for n := range md.underChosen() {
		if i := md.asset.Nodes[n].Mesh; i >= 0 {
			prims := slices.Clone(md.asset.Meshes[i].Prims)
			for p := range prims {
				prims[p].Material = md.material
			}
			md.asset.Meshes[i].Prims = prims
		}
	}
	md.dirty = true
	a.reshape(md, true)
	a.objectOverlay(md)
}

// chooseTexture puts a picture from a file in a slot of the material shown.
func (a *App) chooseTexture(md *modelDoc, slot int) {
	a.pickFiles(pickRequest{key: "texture", title: a.u.t("Open a picture"), kinds: a.u.t("Pictures"), exts: []string{".png", ".jpg", ".jpeg"}}, func(paths []string) {
		data, err := os.ReadFile(paths[0])
		if err != nil {
			a.toast(a.u.tf("Cannot open %s: %s", filepath.Base(paths[0]), a.describe(err)), true)
			return
		}
		a.setTexture(md, slot, s3.Image{Name: filepath.Base(paths[0]), Data: data, Mime: http.DetectContentType(data)})
	})
}

// setTexture is chooseTexture once the picture is at hand.
func (a *App) setTexture(md *modelDoc, slot int, im s3.Image) {
	md.remember(md.shot())
	md.asset.Images = append(slices.Clone(md.asset.Images), im)
	md.asset.Materials = slices.Clone(md.asset.Materials)
	materialSlots(&md.asset.Materials[md.material])[slot].Image = len(md.asset.Images) - 1
	md.dirty = true
	md.version++
	a.requestFrame()
}

// clearTexture takes the picture out of a slot of the material shown.
func (a *App) clearTexture(md *modelDoc, slot int) {
	md.remember(md.shot())
	md.asset.Materials = slices.Clone(md.asset.Materials)
	materialSlots(&md.asset.Materials[md.material])[slot].Image = -1
	md.dirty = true
	md.version++
	a.requestFrame()
}

// materialOfChosen is the material of the chosen object, or -1.
func (md *modelDoc) materialOfChosen() int {
	for n := range md.underChosen() {
		if i := md.asset.Nodes[n].Mesh; i >= 0 && len(md.asset.Meshes[i].Prims) > 0 {
			return md.asset.Meshes[i].Prims[0].Material
		}
	}
	return -1
}

// drawModelMaterial is the panel's tab for the file's materials.
func (a *App) drawModelMaterial(md *modelDoc, x, y, w float32) float32 {
	u := &a.u
	as := md.asset
	if as == nil {
		return y
	}
	if md.chosen != md.materialFor {
		md.materialFor = md.chosen
		if m := md.materialOfChosen(); m >= 0 {
			md.material = m
		}
	}
	md.material = min(md.material, len(as.Materials)-1)
	half := (w - u.px(6)) / 2
	if u.button("model/material/new", rectAt(x, y, half, u.px(28)), "", u.t("New material"), ghost, true) {
		a.addMaterial(md)
	}
	if u.button("model/material/assign", rectAt(x+half+u.px(6), y, half, u.px(28)), "", u.t("Assign"), ghost, md.material >= 0) {
		a.assignMaterial(md)
	}
	y += u.px(36)
	f := u.face(12.5)
	high := u.px(26)
	for i := range as.Materials {
		r := rectAt(x, y, w, high)
		y += high
		id := fmt.Sprintf("model/material/%d", i)
		if u.hover(id, r) {
			u.c.Fill(r, colBorder)
		}
		colour := colText
		if md.material == i {
			u.c.Fill(r, modelHue)
			colour = colBackground
		}
		if u.press(id, r) {
			md.material = i
		}
		c := as.Materials[i].BaseColor
		u.c.Fill(rectAt(x+u.px(6), r.Y+u.px(6), u.px(14), u.px(14)), linearSwatch(c))
		name := as.Materials[i].Name
		if name == "" {
			name = u.tf("Material %d", i+1)
		}
		u.label(f, rectAt(x+u.px(26), r.Y, w-u.px(32), high), f.Ellipsize(name, w-u.px(32)), colour, 0)
	}
	if md.material < 0 {
		return y
	}
	y += u.px(10)
	m := as.Materials[md.material]
	changed := false
	slide := func(id, text string, value *float32, hi float32) {
		u.label(u.face(12), rectAt(x, y, w, u.px(18)), text, colDim, 0)
		v := float64(*value)
		u.label(u.face(12), rectAt(x, y, w, u.px(18)), fmt.Sprintf("%.2f", v), colText, 1)
		y += u.px(18)
		if c, done := u.slider("model/mat/"+id, rectAt(x, y, w, u.px(22)), &v, 0, float64(hi), true); c || done {
			md.beginMaterialChange()
			*value, changed = float32(v), true
			if done {
				md.keyDrag = false
			}
		}
		y += u.px(24)
	}
	hsv := func(id string, c *[3]float32) {
		u.c.Fill(rectAt(x, y, w, u.px(10)), linearSwatch(s3.Vec4{c[0], c[1], c[2], 1}))
		y += u.px(14)
		h, s, v := lightToHSV(*c)
		hue, sat, val := h, s, v
		slide(id+"/h", u.t("Hue"), &hue, 1)
		slide(id+"/s", u.t("Saturation"), &sat, 1)
		slide(id+"/v", u.t("Brightness"), &val, 1)
		if hue != h || sat != s || val != v {
			*c = hsvToLight(hue, sat, val)
		}
	}
	base := [3]float32{m.BaseColor[0], m.BaseColor[1], m.BaseColor[2]}
	hsv("base", &base)
	m.BaseColor[0], m.BaseColor[1], m.BaseColor[2] = base[0], base[1], base[2]
	slide("metal", u.t("Metallic"), &m.Metallic, 1)
	slide("rough", u.t("Roughness"), &m.Roughness, 1)
	slide("alpha", u.t("Opacity"), &m.BaseColor[3], 1)
	glow := [3]float32(m.Emissive)
	u.label(u.bold(12), rectAt(x, y, w, u.px(20)), u.t("Emission"), colDim, 0)
	y += u.px(22)
	hsv("glow", &glow)
	m.Emissive = s3.Vec3(glow)
	if m.NormalTex.Image >= 0 {
		slide("normal", u.t("Normal strength"), &m.NormalScale, 2)
	}
	if m.OcclusionTex.Image >= 0 {
		slide("occlusion", u.t("Occlusion strength"), &m.Occlusion, 1)
	}
	modes := []string{u.t("Opaque"), u.t("Cut out"), u.t("Blend")}
	if pick := u.chips("model/mat/mode", rectAt(x, y, w, u.px(26)), modes, int(m.AlphaMode), true); pick != int(m.AlphaMode) {
		md.remember(md.shot())
		m.AlphaMode, changed = s3.AlphaMode(pick), true
	}
	y += u.px(32)
	if m.AlphaMode == s3.AlphaMask {
		slide("cutoff", u.t("Cut out at"), &m.AlphaCutoff, 1)
	}
	if on := u.checkbox("model/mat/double", rectAt(x, y, w, u.px(26)), u.t("Both sides"), m.DoubleSided); on != m.DoubleSided {
		md.remember(md.shot())
		m.DoubleSided, changed = on, true
	}
	y += u.px(32)
	if changed {
		md.asset.Materials = slices.Clone(md.asset.Materials) // a step of undo holds the list before
		md.asset.Materials[md.material] = m
		md.dirty = true
		md.materials++
		a.requestFrame()
	}
	u.label(u.bold(12), rectAt(x, y, w, u.px(20)), u.t("Pictures"), colDim, 0)
	y += u.px(22)
	slots := materialSlots(&m)
	clear := u.px(26)
	for i, s := range slots {
		name := u.t("None")
		if s.Image >= 0 && s.Image < len(as.Images) {
			name = as.Images[s.Image].Name
			if name == "" {
				name = u.tf("Picture %d", s.Image+1)
			}
		}
		u.label(u.face(11.5), rectAt(x, y, w, u.px(16)), u.t(modelTextures[i]), colDim, 0)
		y += u.px(17)
		left := w - clear - u.px(4)
		if s.Image < 0 {
			left = w
		}
		if u.button(fmt.Sprintf("model/mat/pic/%d", i), rectAt(x, y, left, u.px(26)), "", f.Ellipsize(name, left-u.px(16)), ghost, true) {
			a.chooseTexture(md, i)
		}
		if s.Image >= 0 && u.button(fmt.Sprintf("model/mat/clear/%d", i), rectAt(x+left+u.px(4), y, clear, u.px(26)), "", "×", ghost, true) {
			a.clearTexture(md, i)
		}
		y += u.px(32)
	}
	return y
}

// linearSwatch is a colour of light as the screen shows it.
func linearSwatch(c s3.Vec4) color.RGBA {
	return gfx.Hex(uint32(srgbByte(c[0]))<<16 | uint32(srgbByte(c[1]))<<8 | uint32(srgbByte(c[2])))
}

func srgbByte(v float32) uint8 {
	v = min(max(v, 0), 1)
	if v <= .0031308 {
		return uint8(v*12.92*255 + .5)
	}
	return uint8((1.055*float32(math.Pow(float64(v), 1/2.4))-.055)*255 + .5)
}

func lightToHSV(c [3]float32) (h, s, v float32) {
	hi, lo := max(c[0], c[1], c[2]), min(c[0], c[1], c[2])
	d := hi - lo
	v = hi
	if hi > 0 {
		s = d / hi
	}
	switch {
	case d == 0:
	case hi == c[0]:
		h = (c[1] - c[2]) / d
		if h < 0 {
			h += 6
		}
	case hi == c[1]:
		h = (c[2]-c[0])/d + 2
	default:
		h = (c[0]-c[1])/d + 4
	}
	return h / 6, s, v
}

func hsvToLight(h, s, v float32) [3]float32 {
	h = float32(math.Mod(float64(h), 1)) * 6
	sector := int(h)
	f := h - float32(sector)
	p, q, t := v*(1-s), v*(1-s*f), v*(1-s*(1-f))
	return [][3]float32{{v, t, p}, {q, v, p}, {p, v, t}, {p, q, v}, {t, p, v}, {v, p, q}}[sector%6]
}
