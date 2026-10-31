package ui

import (
	"image"
	"image/draw"
	"slices"
	"time"

	"github.com/btelolabs/btelo-forge/internal/paint"
)

// A model's picture can be open in the drawing tool, in a tab of its own, with all that tool has: layers, brushes, filters.
// What is drawn there shows on the model when its tab comes back, and strokes on the model land on a layer of the canvas,
// so the two never part.

// modelLink is a picture open in the drawing tool.
type modelLink struct {
	art   *artDoc
	image int
	seen  time.Time   // the canvas's last change that came across
	layer *paint.Node // takes what is painted on the model
}

func (a *App) linkOf(md *modelDoc, image int) *modelLink {
	for _, l := range md.links {
		if l.image == image {
			return l
		}
	}
	return nil
}

// artTab is the tab of a canvas, or -1.
func (a *App) artTab(ad *artDoc) int {
	return slices.IndexFunc(a.docs, func(d *doc) bool { return d.art == ad })
}

// editInDrawing opens the picture in the drawing tool, or brings forward the tab that has it.
func (a *App) editInDrawing(md *modelDoc, p *modelPicture) {
	if l := a.linkOf(md, p.image); l != nil {
		if i := a.artTab(l.art); i >= 0 {
			a.switchTab(i)
			return
		}
	}
	d, err := paint.FromImage(p.im, a.u.t("Picture"))
	if err != nil {
		a.toast(a.describe(err), true)
		return
	}
	path, err := a.capturePath(categoryCreative, a.u.t("Texture %s"), paint.Ext, time.Now())
	if err != nil {
		a.toast(a.describe(err), true)
		return
	}
	a.openArt(d, path)
	md.links = append(md.links, &modelLink{art: a.art, image: p.image})
}

// pullDrawings brings to the model what changed on its canvases. A closed canvas gives its last and lets go.
func (a *App) pullDrawings(md *modelDoc) {
	kept := md.links[:0]
	for _, l := range md.links {
		if !l.art.changed.Equal(l.seen) {
			l.seen = l.art.changed
			a.pullDrawing(md, l)
		}
		if a.artTab(l.art) >= 0 {
			kept = append(kept, l)
		}
	}
	md.links = kept
}

func (a *App) pullDrawing(md *modelDoc, l *modelLink) {
	p := md.pictures[l.image]
	if p == nil {
		return
	}
	shown := l.art.d.Composite(p.im.Rect)
	md.remember(&modelShot{paint: copyPatch(p.image, p.im.Pix, p.im.Rect.Dx(), p.im.Rect)})
	draw.Draw(p.im, p.im.Rect, shown, image.Point{}, draw.Src)
	p.pending, p.changed = p.im.Rect, true
	a.paintFocus(md, p)
	md.painted++
	md.dirty, md.thumb = true, nil
	a.requestFrame()
}

// pushStroke puts what a stroke changed on the canvas of the picture, if it has one.
func (a *App) pushStroke(md *modelDoc, p *modelPicture) {
	l := a.linkOf(md, p.image)
	if l == nil || a.artTab(l.art) < 0 {
		return
	}
	if l.layer == nil {
		l.layer = l.art.d.AddLayer(a.u.t("Painted on the model"))
	}
	r := p.dirty
	over := l.layer.Pixels.Read(r)
	w := p.im.Rect.Dx()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			from, to := (y*w+x)*4, over.PixOffset(x, y)
			if !slices.Equal(p.before[from:from+4], p.im.Pix[from:from+4]) {
				copy(over.Pix[to:to+3], p.im.Pix[from:from+3])
				over.Pix[to+3] = 255
			}
		}
	}
	l.layer.Pixels.Write(over)
	l.art.changedAll()
	l.seen = l.art.changed
}
