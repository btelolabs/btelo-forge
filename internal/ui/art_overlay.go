package ui

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"

	"github.com/btelolabs/btelo-video-cut/internal/gfx"
	"github.com/btelolabs/btelo-video-cut/internal/paint"
	"github.com/btelolabs/btelo-video-cut/internal/project"
)

// A drawing on the video is an image overlay the size of the frame whose picture is exported from a canvas: it is
// drawn in a canvas tab, over the video's picture, and the overlay follows each time the canvas is saved.

// drawingBackdrop is the video's picture at the playhead fitted into a w×h frame, to draw over; nil when there is
// no picture yet.
func (a *App) drawingBackdrop(w, h int) *image.RGBA {
	img := a.shown.img
	if img == nil {
		return nil
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(out, out.Rect, image.NewUniform(color.Black), image.Point{}, draw.Src)
	target := out.Rect
	if !a.shown.full {
		if c, _, ok := a.p.Top(a.playhead); ok {
			src := a.p.SourceOf(c)
			target = fitRect(gfx.Rect{W: float32(w), H: float32(h)}, float32(src.Width), float32(src.Height)).Pixels()
		}
	}
	xdraw.ApproxBiLinear.Scale(out, target, img, img.Rect, draw.Over, nil)
	return out
}

// addDrawing puts a new, empty drawing over the whole frame at the playhead and opens it to draw in.
func (a *App) addDrawing() {
	if a.p == nil {
		return
	}
	u := &a.u
	w, h := a.p.Canvas.Width, a.p.Canvas.Height
	d, err := paint.NewFramed(w, h, u.tf("Layer %d", 1), color.NRGBA{})
	if err != nil {
		a.toast(u.tf("The drawing could not be made: %s", a.describe(err)), true)
		return
	}
	path, err := a.capturePath(u.t("Drawing %s"), paint.Ext, time.Now())
	if err == nil {
		// The overlay needs its picture from the start: an empty one until something is drawn.
		err = writePNG(drawingImage(path), image.NewRGBA(image.Rect(0, 0, w, h)))
	}
	if err != nil {
		a.toast(u.tf("The drawing could not be made: %s", a.describe(err)), true)
		return
	}
	backdrop := a.drawingBackdrop(w, h)
	a.change(func() {
		a.sel = selection{kind: selOverlay, id: a.p.AddDrawing(drawingImage(path), path, float64(w)/float64(h), a.playhead)}
	})
	a.openArt(d, path)
	a.art.backdrop = backdrop
	a.art.dirty = true
	if err := a.saveArt(a.art, true); err != nil {
		a.toast(u.tf("The canvas could not be saved: %s", a.describe(err)), true)
	}
}

// overlayLook is the icon and name an overlay is listed by: a caption's first line, an image's file, a drawing's
// canvas.
func overlayLook(o project.Overlay) (glyph, label string) {
	switch {
	case o.Canvas != "":
		return icBrush, strings.TrimSuffix(baseName(o.Canvas), paint.Ext)
	case o.Kind == project.ImageOverlay:
		return icImage, baseName(o.Image)
	}
	return icFont, firstLine(o.Body)
}

// drawingImage is where a drawing's flattened picture is kept: beside its canvas.
func drawingImage(canvas string) string { return strings.TrimSuffix(canvas, paint.Ext) + ".png" }

// editDrawing opens a drawing's canvas, over the video's picture at the playhead.
func (a *App) editDrawing(o project.Overlay) {
	if o.Canvas == "" {
		return
	}
	backdrop := a.drawingBackdrop(a.p.Canvas.Width, a.p.Canvas.Height)
	a.openCanvasFileWith(o.Canvas, backdrop)
}

// drawingImages are the pictures, on any timeline, of drawings made in the canvas at path.
func (a *App) drawingImages(path string) []string {
	var out []string
	for _, d := range a.docs {
		if d.p == nil {
			continue
		}
		for _, tr := range d.p.Overlays {
			for _, o := range tr.Items {
				if o.Canvas != "" && filepath.Clean(o.Canvas) == filepath.Clean(path) && !slices.Contains(out, o.Image) {
					out = append(out, o.Image)
				}
			}
		}
	}
	return out
}

// drawingsChanged lets the previews and exports take the new pictures of drawings.
func (a *App) drawingsChanged(images []string) {
	for _, path := range images {
		delete(a.media.images, path)
	}
}

// writePNG writes a picture whole or not at all.
func writePNG(path string, img image.Image) error {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".saving"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// openCanvasFileWith opens a saved canvas, drawing it over backdrop when there is one; a canvas already open is
// shown.
func (a *App) openCanvasFileWith(path string, backdrop *image.RGBA) {
	for i, d := range a.docs {
		if d.art != nil && filepath.Clean(d.art.path) == filepath.Clean(path) {
			a.switchTab(i)
			if backdrop != nil {
				d.art.backdrop, d.art.view = backdrop, nil
			}
			return
		}
	}
	a.background(func(ctx context.Context) {
		f, err := os.Open(path)
		var d *paint.Doc
		if err == nil {
			var st os.FileInfo
			if st, err = f.Stat(); err == nil {
				d, err = paint.Load(f, st.Size())
			}
			f.Close()
		}
		a.post(func() {
			if err != nil {
				a.toast(a.u.tf("Cannot open %s: %s", filepath.Base(path), a.describe(err)), true)
				return
			}
			a.openArt(d, path)
			a.art.backdrop = backdrop
		})
	})
}
