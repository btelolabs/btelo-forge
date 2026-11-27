package ui

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"github.com/btelolabs/btelo-forge/internal/capture"
	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// shotNotice is the line at the top of a screenshot's display that says it was taken, over whatever program is in
// front, with a button that puts on the clipboard what the screenshot's own target did not: the file's path, or, for
// a screenshot whose path was copied already, the picture.
type shotNotice struct {
	s      *surface
	to     shotTarget
	shot   shotTaken
	until  time.Time // it closes then, unless the pointer is over it
	copied bool      // the button has done its work
	busy   bool      // the button's work is going on in the background
	dirty  bool      // to be drawn again although no input came
}

// The notice's measures, in logical pixels, and how long it stays.
const (
	shotNoticeTop    = 40 // from the top of the display, where the picker's bar was
	shotNoticeHeight = 44
	shotNoticeDetail = 360 // the widest the detail may be before it is cut short

	shotNoticeLife   = 5 * time.Second
	shotNoticeLinger = 2 * time.Second // after the pointer leaves it, or the button has done its work
)

// copiesPicture reports whether the notice's button copies the picture rather than the file's path.
func (n *shotNotice) copiesPicture() bool { return n.to == shotPath }

// shotNoticeTexts are the notice's title, the detail after it and its button's label.
func (a *App) shotNoticeTexts(n *shotNotice) (title, detail, button string) {
	u := &a.u
	title = u.t("Screenshot taken")
	switch n.to {
	case shotClipboard:
		detail = u.tf("Copied to the clipboard · %d × %d", n.shot.size.X, n.shot.size.Y)
	case shotPath:
		detail = u.tf("Its path is on the clipboard · %s", filepath.Base(n.shot.path))
	default:
		detail = filepath.Base(n.shot.path)
	}
	switch {
	case n.copiesPicture() && n.copied:
		button = u.t("Copied the picture")
	case n.copiesPicture():
		button = u.t("Copy the picture")
	case n.copied:
		button = u.t("Copied the path")
	default:
		button = u.t("Copy the picture's path")
	}
	return title, detail, button
}

// hasButton reports whether the notice can offer its button: a picture goes onto the clipboard only where the
// system lets it.
func (a *App) shotNoticeHasButton(n *shotNotice) bool {
	_, pictures := a.host.(imageClipboard)
	return !n.copiesPicture() || pictures
}

// shotNoticeParts lays the notice out from its left edge: the mark, the title, the detail, the button and the close
// button. The button is as wide as the longer of its two labels, so the notice keeps its size when it changes.
func (a *App) shotNoticeParts(u *ctx, n *shotNotice) (mark, title, detail, button, close gfx.Rect, width float32) {
	t, d, _ := a.shotNoticeTexts(n)
	h := u.px(shotNoticeHeight)
	pad, gap := u.px(16), u.px(12)
	x := pad
	mark = gfx.Rect{X: x, W: u.px(24), H: h}
	x = mark.Right() + u.px(8)
	title = gfx.Rect{X: x, W: u.bold(14).Measure(t), H: h}
	x = title.Right() + gap
	detail = gfx.Rect{X: x, W: min(u.face(13).Measure(d), u.px(shotNoticeDetail)), H: h}
	x = detail.Right() + gap
	if a.shotNoticeHasButton(n) {
		copied := *n
		copied.copied = !n.copied
		_, _, before := a.shotNoticeTexts(n)
		_, _, after := a.shotNoticeTexts(&copied)
		bw := max(u.buttonWidth(icCopy, before), u.buttonWidth(icCheck, after))
		button = gfx.Rect{X: x, Y: u.px(7), W: bw, H: h - u.px(14)}
		x = button.Right() + u.px(6)
	}
	close = gfx.Rect{X: x, Y: u.px(8), W: h - u.px(16), H: h - u.px(16)}
	return mark, title, detail, button, close, close.Right() + u.px(8)
}

// overlayScale is the scale of the windows opened over the screen.
func (a *App) overlayScale() float32 {
	if host, ok := a.host.(interface{ Scale() float32 }); ok {
		return host.Scale()
	}
	return 1
}

// showShotNotice says at the top of display d that a screenshot was taken, in place of any notice before it. Without
// windows over the screen there is only the toast in the window.
func (a *App) showShotNotice(d capture.Display, to shotTarget, shot shotTaken) {
	a.closeShotNotice()
	n := &shotNotice{to: to, shot: shot, until: time.Now().Add(shotNoticeLife)}
	layout := func(scale float32) image.Rectangle {
		measure := &ctx{fonts: a.u.fonts, s: scale, lang: a.u.lang}
		_, _, _, _, _, w := a.shotNoticeParts(measure, n)
		width, height := int(w+0.5), int(shotNoticeHeight*scale)
		at := image.Pt(d.X+(d.Width-width)/2, d.Y+int(shotNoticeTop*scale))
		return image.Rectangle{Min: at, Max: at.Add(image.Pt(width, height))}
	}
	s, err := a.openScaledSurface(OverlayOptions{Excluded: true}, layout)
	if err != nil {
		return
	}
	n.s = s
	a.over.notice = n
}

// closeShotNotice takes the notice off the screen.
func (a *App) closeShotNotice() {
	if n := a.over.notice; n != nil {
		n.s.close()
		a.over.notice = nil
	}
}

// shotNoticeTick draws the notice when something changed and closes it when its time is up and the pointer is not
// over it.
func (a *App) shotNoticeTick(n *shotNotice) {
	_, changed := a.beginSurface(n.s)
	now := time.Now()
	if host, ok := a.host.(overlayHost); ok && host.Pointer().In(n.s.o.Bounds()) {
		n.until = later(n.until, now.Add(shotNoticeLinger))
	}
	if now.After(n.until) && !n.busy {
		a.closeShotNotice()
		return
	}
	if !changed && !n.dirty {
		return
	}
	n.dirty = false
	u, c := &n.s.u, n.s.c
	c.ClearTo(color.RGBA{})
	full := gfx.Rect{W: float32(c.Img.Rect.Dx()), H: float32(c.Img.Rect.Dy())}
	c.Fill(full, gfx.Alpha(colPanel, 0.96))
	c.Outline(full, max(1, u.px(1)), colBorder)
	c.Fill(gfx.Rect{W: u.px(3), H: full.H}, colSuccess)
	mark, title, detail, button, closeBox, _ := a.shotNoticeParts(u, n)
	t, d, label := a.shotNoticeTexts(n)
	u.icon(icCheck, mark, 20, colSuccess)
	u.label(u.bold(14), title, t, colText, 0)
	u.label(u.face(13), detail, d, colDim, 0)
	if button.W > 0 {
		glyph, kind := icCopy, primary
		if n.copied {
			glyph, kind = icCheck, plain
		}
		if u.button("notice/copy", button, glyph, label, kind, !n.busy) {
			a.shotNoticeCopy(n)
		}
	}
	if u.iconButton("notice/close", closeBox, icClose, u.t("Close"), false, true) {
		a.closeShotNotice()
		return
	}
	n.s.end(image.Rectangle{})
}

// later is the later of two times.
func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// shotNoticeCopy does what the notice's button says: the picture onto the clipboard, or the file's path, saving a
// screenshot that went only onto the clipboard into the pictures folder first, as its path is of no use otherwise.
func (a *App) shotNoticeCopy(n *shotNotice) {
	done := func(err error) {
		n.busy, n.dirty = false, true
		if err != nil {
			a.toast(a.u.tf("The screenshot could not be copied: %s", a.describe(err)), true)
			return
		}
		n.copied = true
		n.until = time.Now().Add(shotNoticeLinger)
	}
	switch {
	case n.copiesPicture():
		cb, ok := a.host.(imageClipboard)
		if !ok {
			return
		}
		n.busy, n.dirty = true, true
		img, path := n.shot.img, n.shot.path
		a.background(func(ctx context.Context) {
			var err error
			if img == nil {
				img, err = readPNG(path)
			}
			if err == nil {
				err = cb.SetClipboardImage(img)
			}
			a.post(func() { done(err) })
		})
	case n.shot.path != "":
		done(a.host.SetClipboard(n.shot.path))
	default:
		dir, err := a.categoryDir(categoryTools)
		if err != nil {
			done(err)
			return
		}
		path := a.datedPath(dir, a.u.t("Screenshot %s"), ".png", time.Now())
		img := n.shot.img
		n.busy, n.dirty = true, true
		a.background(func(ctx context.Context) {
			err := capture.WritePNG(path, img, true)
			a.post(func() {
				if err == nil {
					n.shot.path = path
					a.refreshPanes([]string{filepath.Dir(path)})
					a.toastActions(a.u.tf("Saved the screenshot %s and copied its path", filepath.Base(path)), false, a.shotActions(path))
					err = a.host.SetClipboard(path)
				}
				done(err)
			})
		})
	}
}

// readPNG decodes the PNG at path.
func readPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}
