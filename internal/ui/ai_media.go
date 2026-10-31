package ui

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	"path/filepath"

	xdraw "golang.org/x/image/draw"

	"github.com/btelolabs/btelo-forge/internal/ai"
	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// aiPictureSide is the longest side a picture of a conversation is kept at in memory: it is shown smaller than that.
const aiPictureSide = 1024

// aiPic is a picture of the conversation, read from its file in the background.
type aiPic struct {
	img     *image.RGBA
	err     error
	loading bool
}

// aiPicture is the picture in the file at path; it is still loading, or has err set, until it can be drawn.
func (a *App) aiPicture(path string) *aiPic {
	s := &a.ai
	if p, ok := s.pics[path]; ok {
		return p
	}
	p := &aiPic{loading: true}
	s.pics[path] = p
	a.background(func(context.Context) {
		img, err := readAIPicture(path)
		a.post(func() { p.img, p.err, p.loading = img, err, false })
	})
	return p
}

// readAIPicture reads an image file and scales it down to aiPictureSide.
func readAIPicture(path string) (*image.RGBA, error) {
	src, err := loadImage(path)
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return nil, fmt.Errorf("the picture is empty")
	}
	w, h := b.Dx(), b.Dy()
	if side := max(w, h); side > aiPictureSide {
		w, h = max(1, w*aiPictureSide/side), max(1, h*aiPictureSide/side)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if w == b.Dx() && h == b.Dy() {
		draw.Draw(img, img.Rect, src, b.Min, draw.Src)
	} else {
		xdraw.CatmullRom.Scale(img, img.Rect, src, b, draw.Src, nil)
	}
	return img, nil
}

// aiMediaMax is the longest side a picture is shown at, in points.
const aiMediaMax = 380

// aiMediaGlyph is the symbol of a file a model made.
var aiMediaGlyph = map[ai.Kind]string{ai.KindImage: icImage, ai.KindVoice: icVolume, ai.KindVideo: icVideo, ai.KindModel3D: icModel}

// drawAIMedia draws what a model made, from y and at x, and returns the y below it. A picture is shown in place;
// anything else is a row with its name. Each has a button that shows the file in its folder.
func (a *App) drawAIMedia(x, y, w float32, i int, media []ai.Media) float32 {
	u := &a.u
	for j, m := range media {
		id := fmt.Sprintf("ai/media/%d/%d", i, j)
		name := filepath.Base(m.Path)
		var area gfx.Rect
		var pic *aiPic
		if m.Kind == ai.KindImage {
			pic = a.aiPicture(m.Path)
		}
		if pic != nil && pic.img != nil {
			b := pic.img.Bounds()
			scale := min(u.px(aiMediaMax)/float32(max(b.Dx(), b.Dy())), 1)
			area = rectAt(x, y, float32(b.Dx())*scale, float32(b.Dy())*scale)
			u.c.Image(pic.img, area)
			u.c.Outline(area, max(1, u.px(1)), colBorder)
		} else {
			area = rectAt(x, y, min(w, u.px(320)), u.px(48))
			u.c.RoundRect(area, u.px(8), colSurface)
			u.c.RoundOutline(area, u.px(8), max(1, u.px(1)), colBorder)
			glyph, ok := aiMediaGlyph[m.Kind]
			if !ok {
				glyph = icFmFiles
			}
			symbol := rectAt(area.X+u.px(6), area.Y, u.px(36), area.H)
			if m.Kind == ai.KindVoice {
				// A recording plays from its card.
				key := "ai/" + m.Path
				if playing, _ := a.audition.Playing(); playing == key {
					if u.iconButton(id+"/play", symbol, icStop, u.t("Stop"), true, true) {
						a.audition.Stop()
					}
				} else if u.iconButton(id+"/play", symbol, icPlay, u.t("Play"), false, true) {
					a.player.Stop()
					a.audition.Play(key, m.Path, "")
				}
			} else if m.Kind == ai.KindVideo {
				// A video plays in the system's player.
				if u.iconButton(id+"/play", symbol, icPlay, u.t("Play"), false, true) {
					if err := a.host.Launch(m.Path); err != nil {
						a.toast(a.describe(err), true)
					}
				}
			} else {
				u.icon(glyph, symbol, 20, colDim)
			}
			label, col := name, colText
			switch {
			case pic != nil && pic.loading:
				label, col = u.t("Loading the picture…"), colFaint
			case pic != nil && pic.err != nil:
				label, col = u.t("The file is not there any more"), colDangerText
			}
			u.label(u.face(12.5), rectAt(area.X+u.px(46), area.Y, area.W-u.px(54), area.H), label, col, 0)
		}
		y = area.Bottom() + u.px(4)
		f := u.face(11)
		bw := u.px(26)
		// A card has the name on it already.
		if pic != nil && pic.img != nil {
			u.c.Text(f, x, y+(bw-f.Height())/2, f.Ellipsize(name, min(area.W, u.px(aiMediaMax))-bw*2-u.px(12)), colFaint)
		}
		if u.iconButton(id+"/show", rectAt(area.Right()-bw, y, bw, bw), icFmFiles, u.t("Show in folder"), false, true) {
			a.host.Reveal(m.Path)
		}
		// What the app itself does with the file.
		open := rectAt(area.Right()-bw*2-u.px(4), y, bw, bw)
		switch m.Kind {
		case ai.KindVideo:
			if u.iconButton(id+"/open", open, icVideo, u.t("Open in the video editor"), false, true) {
				a.openVideo(m.Path, false)
			}
		case ai.KindModel3D:
			if u.iconButton(id+"/open", open, icModel, u.t("Open in the 3D tool"), false, true) {
				a.openModelFile(m.Path)
			}
		}
		y += bw + u.px(8)
	}
	return y
}
