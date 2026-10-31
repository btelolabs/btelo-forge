package ui

import (
	"image/color"
	"math"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	s3 "github.com/btelolabs/btelo-forge/internal/scene3d"
)

// The UV editor: while modelling, the view gives up its right half to the layout over its picture. A click chooses the
// chart there, a drag moves it, the wheel scales it and R turns it a quarter.

const uvMargin = .012

// markSeam marks the chosen edges as seams, or clears them.
func (a *App) markSeam(md *modelDoc, on bool) {
	edges := md.edit.chosenEdges()
	if !a.editNeed(len(edges) > 0, hintEdges) {
		return
	}
	a.editChange(md, func(e *modelEdit) error {
		if e.em.Seams == nil {
			e.em.Seams = map[s3.Edge]bool{}
		}
		for _, edge := range edges {
			if on {
				e.em.Seams[edge] = true
			} else {
				delete(e.em.Seams, edge)
			}
		}
		return nil
	})
}

// unwrapSeams lays the mesh out along its seams.
func (a *App) unwrapSeams(md *modelDoc) {
	a.editKeepingPictures(md, md.surfaces, func(e *modelEdit) { e.em.Unwrap(uvMargin) })
}

// editKeepingPictures changes the layout as a step of modelling, and undo of it brings the pictures back with it.
func (a *App) editKeepingPictures(md *modelDoc, before []s3.Surface, change func(e *modelEdit)) {
	md.flushPaint()
	e := md.edit
	old := md.asset.Images
	a.editChange(md, func(e *modelEdit) error { change(e); return nil })
	a.carryPictures(md, before)
	if e.pictures == nil {
		e.pictures = map[*s3.EditMesh][]s3.Image{}
	}
	e.pictures[e.undo[len(e.undo)-1]], e.pictures[e.em] = old, md.asset.Images
}

// restorePictures puts back the pictures of the state of the mesh undo came to, if it has its own.
func (a *App) restorePictures(md *modelDoc) {
	if images, ok := md.edit.pictures[md.edit.em]; ok {
		md.asset.Images = images
		md.pictures = nil
		md.version++
		md.thumb = nil
		md.paintVersion = -1
	}
}

// uvRect is the square of the editor in the part r of the window.
func (a *App) uvRect(r gfx.Rect) gfx.Rect {
	side := min(r.W, r.H) - a.u.px(24)
	return rectAt(r.X+(r.W-side)/2, r.Y+(r.H-side)/2, side, side)
}

// uvFaceAt is the face under the point (x, y) of the unit square, or -1.
func uvFaceAt(em *s3.EditMesh, x, y float32) int32 {
	var split [][3]int32
	for f := range em.Faces {
		if em.Faces[f].Count == 0 {
			continue
		}
		c := em.FaceCorners(f)
		split = em.Triangles(f, split[:0])
		for _, t := range split {
			a, b, p := c[t[0]].UV, c[t[1]].UV, c[t[2]].UV
			d0 := (b[0]-a[0])*(y-a[1]) - (b[1]-a[1])*(x-a[0])
			d1 := (p[0]-b[0])*(y-b[1]) - (p[1]-b[1])*(x-b[0])
			d2 := (a[0]-p[0])*(y-p[1]) - (a[1]-p[1])*(x-p[0])
			if (d0 >= 0 && d1 >= 0 && d2 >= 0) || (d0 <= 0 && d1 <= 0 && d2 <= 0) {
				return int32(f)
			}
		}
	}
	return -1
}

// uvCenter is the middle of the faces in UV space.
func uvCenter(em *s3.EditMesh, faces []int32) s3.Vec2 {
	box := s3.EmptyBox()
	for _, f := range faces {
		for _, c := range em.FaceCorners(int(f)) {
			box.Grow(s3.Vec3{c.UV[0], c.UV[1], 0})
		}
	}
	m := box.Center()
	return s3.Vec2{m[0], m[1]}
}

// uvPick chooses the chart under the pointer in the editor's square r.
func (a *App) uvPick(md *modelDoc, r gfx.Rect) {
	e := md.edit
	f := uvFaceAt(e.em, (a.u.mx-r.X)/r.W, (a.u.my-r.Y)/r.H)
	if f < 0 {
		e.island = nil
	} else {
		e.island = e.em.IslandOf(f)
	}
	md.thumb = nil
}

// uvEdit changes the chosen chart as one step of undo, or as part of the drag that made the step.
func (a *App) uvEdit(md *modelDoc, final bool, change func(em *s3.EditMesh, faces []int32)) {
	e := md.edit
	if len(e.island) == 0 {
		return
	}
	if !e.uvMoving {
		e.pushUndo()
		e.uvMoving = true
	}
	change(e.em, e.island)
	md.dirty, md.thumb = true, nil
	a.editRefresh(md, final)
	if final {
		e.uvMoving = false
	}
}

// drawUVEditor is the editor over the part r of the window.
func (a *App) drawUVEditor(md *modelDoc, r gfx.Rect) {
	u := &a.u
	e := md.edit
	u.c.Fill(r, colBackground)
	square := a.uvRect(r)
	var p *modelPicture
	if m := md.materialOfEdit(); m >= 0 && m < len(md.asset.Materials) && md.asset.Materials[m].BaseTex.Image >= 0 {
		p = a.pictureOf(md, m)
	}
	u.c.Blit(a.uvThumb(md, p, int(square.W)), int(square.X), int(square.Y), true)
	if len(e.island) > 0 {
		chosen := color.RGBA{R: 255, G: 150, B: 40, A: 255}
		for _, f := range e.island {
			c := e.em.FaceCorners(int(f))
			for i := range c {
				a0, b0 := c[i].UV, c[(i+1)%len(c)].UV
				u.c.Line(square.X+a0[0]*square.W, square.Y+a0[1]*square.H, square.X+b0[0]*square.W, square.Y+b0[1]*square.H, max(1, u.px(1)), chosen)
			}
		}
	}
	const id = "model/uv"
	if u.press(id, square) {
		a.uvPick(md, square)
		md.uvX, md.uvY = u.mx, u.my
	}
	if u.dragging(id) && (u.mx != md.uvX || u.my != md.uvY) {
		shift := s3.Vec2{(u.mx - md.uvX) / square.W, (u.my - md.uvY) / square.H}
		md.uvX, md.uvY = u.mx, u.my
		a.uvEdit(md, false, func(em *s3.EditMesh, faces []int32) { em.TransformUV(faces, s3.Vec2{}, 0, 1, shift) })
	}
	if u.letGo(id) && e.uvMoving {
		a.uvEdit(md, true, func(*s3.EditMesh, []int32) {})
	}
	if u.wheel != 0 && square.Contains(u.mx, u.my) {
		factor := float32(math.Pow(1.08, float64(u.wheel)))
		u.wheel = 0
		a.uvEdit(md, true, func(em *s3.EditMesh, faces []int32) { em.TransformUV(faces, uvCenter(em, faces), 0, factor, s3.Vec2{}) })
	}
}

// materialOfEdit is the material of the mesh being modelled.
func (md *modelDoc) materialOfEdit() int {
	if e := md.edit; e != nil {
		prims := md.asset.Meshes[e.mesh].Prims
		if len(prims) > 0 {
			return prims[0].Material
		}
	}
	return -1
}
