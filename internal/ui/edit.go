package ui

import (
	"crypto/sha1"
	"encoding/hex"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"github.com/btelolabs/btelo-video-cut/internal/project"
)

func (a *App) current() int {
	for i, d := range a.docs {
		if d == a.doc {
			return i
		}
	}
	return 0
}

func (a *App) seek(t float64) {
	a.playhead = min(max(t, 0), a.p.Duration())
	if a.player.Playing() {
		a.play(a.playhead)
	}
	a.requestFrame()
}

// split cuts the selected item at the playhead when it spans it, else the main-track clip under the playhead.
func (a *App) split() {
	t := a.playhead
	a.change(func() {
		ok := false
		switch a.sel.kind {
		case selMusic:
			ok = a.p.SplitMusic(a.sel.id, t)
		case selOverlay:
			ok = a.p.SplitOverlay(a.sel.id, t)
		case selClip:
			ok = a.p.SplitClip(a.sel.id, t)
		}
		if !ok {
			i, _ := a.p.Locate(t)
			id := a.p.Video[0].Clips[i].ID
			if ok = a.p.SplitClip(id, t); ok {
				a.sel = selection{kind: selClip, id: a.p.Video[0].Clips[i+1].ID}
			}
		}
		if !ok {
			a.toast(a.u.t("Too close to the edge of a segment to split"), false)
		}
	})
}

func (a *App) deleteSelection() {
	if ids := a.selectedIDs(); len(ids) > 1 {
		a.deleteItems(ids)
		return
	}
	switch a.sel.kind {
	case selClip:
		if k, _, _ := a.p.FindClip(a.sel.id); k == 0 && len(a.p.Video[0].Clips) <= 1 {
			a.toast(a.u.t("At least one segment has to stay"), false)
			return
		}
		a.change(func() { a.p.DeleteClip(a.sel.id) })
	case selMusic:
		a.change(func() { a.p.DeleteMusic(a.sel.id) })
	case selOverlay:
		a.change(func() { a.p.DeleteOverlay(a.sel.id) })
	default:
		return
	}
	a.selectOnly(selection{})
}

// focusBody puts the caret in the selected text overlay's editor with its text selected.
func (a *App) focusBody(body string) {
	a.showPanel(tabOverlays)
	a.u.setFocus("overlay/body")
	a.u.edit = editor{text: []rune(body), multiline: true}
	a.u.edit.anchor, a.u.edit.caret = 0, len(a.u.edit.text)
}

func (a *App) addText() {
	if a.p == nil {
		return
	}
	body := a.u.t("Type your text")
	a.change(func() { a.sel = selection{kind: selOverlay, id: a.p.AddText(body, a.playhead)} })
	a.focusBody(body)
}

// addImage places an image file at the playhead.
func (a *App) addImage(path string) {
	if a.p == nil {
		return
	}
	img, err := a.media.image(path)
	if err != nil {
		a.toast(a.u.tf("Cannot open the image %s: %s", filepath.Base(path), a.describe(err)), true)
		return
	}
	b := img.Bounds()
	a.change(func() {
		a.sel = selection{kind: selOverlay, id: a.p.AddImage(path, float64(b.Dx())/float64(b.Dy()), a.playhead)}
	})
	a.showPanel(tabOverlays)
}

// paste adds what the clipboard holds: an image, files, or text as a caption.
func (a *App) paste() {
	img, files, text := a.host.Paste()
	switch {
	case img != nil && a.p != nil:
		path, err := a.storeImage(img)
		if err != nil {
			a.toast(a.u.tf("The pasted image could not be saved: %v", err), true)
			return
		}
		a.addImage(path)
	case len(files) > 0:
		a.handleDrops(files)
	case text != "" && a.p != nil:
		a.change(func() { a.sel = selection{kind: selOverlay, id: a.p.AddText(text, a.playhead)} })
		a.showPanel(tabOverlays)
	}
}

// storeImage keeps a pasted image as a PNG named after its content, so saved edits can find it again.
func (a *App) storeImage(img image.Image) (string, error) {
	dir := filepath.Join(a.stateDir, "pasted")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, "paste-*.png")
	if err != nil {
		return "", err
	}
	h := sha1.New()
	if err := png.Encode(tmp, img); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	data, err := os.ReadFile(tmp.Name())
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	h.Write(data)
	path := filepath.Join(dir, hex.EncodeToString(h.Sum(nil)[:10])+".png")
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return path, nil
}

// targetClip is the video clip the speed and volume controls act on: the selected one, else the main-track clip
// under the playhead.
func (a *App) targetClip() project.Clip {
	if a.sel.kind == selClip {
		if c, ok := a.p.Clip(a.sel.id); ok {
			return c
		}
	}
	i, _ := a.p.Locate(a.playhead)
	return a.p.Video[0].Clips[i]
}
