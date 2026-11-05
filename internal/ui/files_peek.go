package ui

import (
	"context"
	"image"
	"time"

	"github.com/btelolabs/btelo-forge/internal/assets"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/render3d"
	"github.com/btelolabs/btelo-forge/internal/scene3d"
)

// The small picture that floats at the bottom right of the file manager while the pointer rests on a picture or a 3D
// file. It only shows: it takes no click, and what changes a file is in the preview Space opens. A picture is its
// thumbnail; a 3D file, which has none, is drawn once by the card in a modelDoc no tab shows, and kept as its thumbnail,
// which the preview shows too.

const (
	// peekDelay is how long the pointer rests on a file before its picture shows, so passing over a list loads nothing.
	peekDelay = 200 * time.Millisecond
	// peekSky is the sky a 3D file is shown under, the most neutral one.
	peekSky = "studio"
)

// peekModelSize is the picture drawn of a 3D file, in pixels.
var peekModelSize = image.Pt(1280, 960)

// filePeek is the file the pointer rests on, and the picture being made of it.
type filePeek struct {
	path   string
	since  time.Time
	scaler gfx.Scaler
	model  *modelDoc // a 3D file being read or drawn
	failed bool      // the 3D file could not be read
}

// peeked reports whether the item at path, a folder when dir is set, shows a small picture.
func peeked(path string, dir bool) bool {
	switch assets.KindOf(path) {
	case assets.Image, assets.Animation, assets.Canvas, assets.Model:
		return !dir
	}
	return false
}

// endPeek forgets the file the pointer rested on.
func (a *App) endPeek() { a.fm.peek = filePeek{} }

// peekWait is how soon the small picture is due to show.
func (a *App) peekWait() time.Duration {
	pk := &a.fm.peek
	if pk.path == "" {
		return 0
	}
	return max(time.Until(pk.since.Add(peekDelay)), 0)
}

// drawFilePeek floats the small picture of the file under the pointer at the bottom right of r, above what floats
// there already (above is the top of the file jobs). It draws no control. A pointer under it sends it to the left, as
// it would hide the row the pointer is on.
func (a *App) drawFilePeek(fd *filesDoc, r gfx.Rect, above float32) {
	u := &a.u
	if fd.preview {
		return // it has the picture itself, and the card may be drawing it
	}
	path := fd.hovered
	if fd.pick != nil || u.blocked || u.active != "" || path == "" || !peeked(path, fd.hoveredDir) || a.busyJob(path) != nil {
		a.endPeek()
		return
	}
	pk := &a.fm.peek
	if pk.path != path {
		*pk = filePeek{path: path, since: time.Now()}
	}
	if time.Since(pk.since) < peekDelay {
		return
	}
	img, info, ready := a.fileThumb(path)
	kind := assets.KindOf(path)
	if kind == assets.Model {
		img = a.modelPicture(path)
	}

	margin := u.px(12)
	w := min(u.px(320), r.W-2*margin)
	if w < u.px(120) {
		return
	}
	// The frame holds the picture alone, at its shape.
	line := max(1, u.px(1))
	fit := gfx.Rect{W: w, H: w * 3 / 4}
	if img != nil {
		fit = fitRect(fit, float32(img.Rect.Dx()), float32(img.Rect.Dy()))
	}
	box := gfx.Rect{W: fit.W + 2*line, H: fit.H + 2*line}
	box.X, box.Y = r.Right()-margin-box.W, above-margin-box.H
	box.Y = a.clearOfTasks(box)
	if box.Y < r.Y+margin {
		return
	}
	if box.Contains(u.mx, u.my) {
		box.X = r.X + margin
	}
	u.c.Outline(box.Inset(-u.px(1)), u.px(4), gfx.Alpha(colBlack, 0.35))
	u.c.Outline(box, line, colBorderHi)
	pic := box.Inset(line)
	if img != nil {
		px := pic.Pixels()
		if scaled := pk.scaler.Scaled(img, px.Dx(), px.Dy()); scaled != nil {
			if !img.Opaque() {
				drawChecks(u, pic)
			}
			u.c.Blit(scaled, px.Min.X, px.Min.Y, img.Opaque())
		}
	} else {
		a.drawKindCard(pic, kind, info, ready)
		if kind == assets.Model && a.modelPictureComing() {
			u.label(u.face(12), rectAt(pic.X, pic.Bottom()-u.px(30), pic.W, u.px(24)), u.t("Loading the preview…"), colFaint, 0.5)
		}
	}
}

// clearOfTasks moves box up off the cards in the window's corner, and returns where its top then is.
func (a *App) clearOfTasks(box gfx.Rect) float32 {
	l := a.layoutTasks()
	gap := a.u.px(8)
	off := func(r gfx.Rect) {
		if r.W > 0 && box.Right() > r.X && box.X < r.Right() && box.Bottom() > r.Y && box.Y < r.Bottom() {
			box.Y = r.Y - gap - box.H
		}
	}
	off(l.orb)
	for _, r := range l.cards {
		off(r)
	}
	return box.Y
}

// modelPictureComing reports whether the picture of the 3D file being looked at is still to come.
func (a *App) modelPictureComing() bool { return !a.fm.peek.failed && a.models.failed == "" }

// modelPicture is the picture of the 3D file at path, nil until it is made: it reads the file, has the card draw it, and
// keeps what comes back as the file's thumbnail.
func (a *App) modelPicture(path string) *image.RGBA {
	pk := &a.fm.peek
	if pk.path != path {
		*pk = filePeek{path: path, since: time.Now()}
	}
	img, _, ready := a.fileThumb(path)
	if img != nil || !ready || !a.modelPictureComing() {
		return img
	}
	md := pk.model
	if md == nil {
		md = a.newModelDoc(path)
		md.options = render3d.Options{Shadows: true, ShowSky: true, Exposure: 1}
		if a.skyOf(peekSky) != nil {
			md.sky = peekSky
		}
		md.loading = true
		pk.model = md
		a.background(func(context.Context) {
			asset, err := scene3d.Load(path)
			var surfaces []scene3d.Surface
			var bounds scene3d.Box
			if err == nil {
				surfaces, bounds = asset.Surfaces()
			}
			a.post(func() {
				md.loading = false
				if a.fm.peek.model != md {
					return
				}
				if err != nil {
					a.fm.peek.failed = true
					return
				}
				md.asset, md.surfaces, md.bounds = asset, surfaces, bounds
				md.version++
				md.geometry++
				md.cam.Frame(bounds)
				a.requestFrame()
			})
		})
		return nil
	}
	if md.picture != nil {
		a.fileThumbs.give(path, md.picture)
		pk.model = nil
		return md.picture
	}
	if md.asset != nil {
		a.askModelFrame(md, peekModelSize.X, peekModelSize.Y)
	}
	return nil
}
