package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/btelolabs/btelo-forge/internal/paint"
	"github.com/btelolabs/btelo-forge/internal/project"
)

// Smart objects across canvases. Each link's shared layers are kept in the smart objects folder next to the
// settings, as the smart object's own file, <link id>.btcanvas, with its picture, <link id>.png, beside it for the
// video editor. What is kept there comes first: a canvas opened takes it into its smart objects, and every change
// to shared layers, in any canvas or in the smart object's own file opened as a canvas, is kept there at once and
// taken into the other open canvases. Each canvas file also holds its smart objects' layers, as a spare: when the
// kept file is missing, the spare is kept in its place, and when it is damaged the spare is used and the damaged
// file set aside, both with a word to the user. A smart object pasted into a video is an image overlay of its
// picture, which follows each change.

// icSmart is a smart object's icon: stacked copies.
const (
	icSmart       = "\ue3e0"
	icSmartFilter = "\ue152" // showing only the smart objects: a filter
)

// smartDir is the folder the smart objects' shared layers are kept in.
func (a *App) smartDir() string { return filepath.Join(a.stateDir, "smart objects") }

// smartPath is where the shared layers of link id are kept, as the smart object's own file.
func (a *App) smartPath(id string) string { return filepath.Join(a.smartDir(), id+paint.Ext) }

// smartImage is where the picture of the shared layers of link id is kept, for the video editor.
func (a *App) smartImage(id string) string { return filepath.Join(a.smartDir(), id+".png") }

// smartKeeper writes shared layers to the smart objects folder away from the window: for each link one write at a
// time, and of the versions waiting meanwhile only the latest, so a run of quick changes is written once it ends
// rather than once for each; never an older version over a newer one. It is used on the window's goroutine only.
type smartKeeper struct {
	busy    map[string]bool          // the links being written
	waiting map[string]smartKeepWork // the latest version of each link waiting for the write before it
	newest  map[string]int64         // the latest version of each link given to be written
}

// smartKeepWork is a version of shared layers to keep, and whether its file is written as well as its picture.
type smartKeepWork struct {
	c    paint.SmartContent
	file bool
}

// sourceTab is the canvas tab the smart object's own file of link id is open in, or nil.
func (a *App) sourceTab(id string) *artDoc {
	for _, d := range a.docs {
		if d.art != nil && d.art.d.SmartSource() == id {
			return d.art
		}
	}
	return nil
}

// syncSmart keeps the shared layers changed in any canvas since the last frame and takes them into the others, and
// lets the video editor's pictures of them follow.
func (a *App) syncSmart() {
	for _, d := range a.docs {
		ad := d.art
		if ad == nil {
			continue
		}
		for _, c := range ad.d.SmartChanges() {
			a.keepSmart(c, true)
			for _, o := range a.docs {
				if o.art != nil && o.art != ad && o.art.d.ApplySmart(c) {
					o.art.changedAll()
				}
			}
		}
	}
}

// keepSmart writes shared layers to the smart objects folder, the smart object's own file unless a canvas tab has
// it open (that tab saves it) or file is false, and always its picture, which the video editor then takes again.
// While the link is being written it waits, the latest version taking the place of any older one waiting.
func (a *App) keepSmart(c paint.SmartContent, file bool) {
	k := &a.smartKeep
	if k.busy == nil {
		k.busy, k.waiting, k.newest = map[string]bool{}, map[string]smartKeepWork{}, map[string]int64{}
	}
	if c.Version < k.newest[c.ID] {
		return // a newer one is kept already, or is being
	}
	if w, ok := k.waiting[c.ID]; ok {
		file = file || w.file // the file waiting to be written is written with the newer layers
		if w.c.Version > c.Version {
			c = w.c
		}
	}
	k.waiting[c.ID] = smartKeepWork{c: c, file: file}
	if !k.busy[c.ID] {
		a.writeSmart(c.ID)
	}
}

// writeSmart writes the version of link id waiting, on another goroutine, and then the one waiting after it, if any.
func (a *App) writeSmart(id string) {
	k := &a.smartKeep
	w := k.waiting[id]
	delete(k.waiting, id)
	k.busy[id], k.newest[id] = true, w.c.Version
	c, file := w.c, w.file && a.sourceTab(id) == nil
	dir, path, picPath := a.smartDir(), a.smartPath(id), a.smartImage(id)
	a.background(func(context.Context) {
		err := os.MkdirAll(dir, 0o755)
		if err == nil && file {
			var buf bytes.Buffer
			if err = paint.SaveSmart(&buf, c); err == nil {
				err = writeFileWhole(path, buf.Bytes())
			}
		}
		aspect := 1.0
		if err == nil {
			pic := paint.SmartPicture(c)
			if pic == nil {
				pic = image.NewRGBA(image.Rect(0, 0, 1, 1)) // shows nothing, as the layers do
			}
			aspect = float64(pic.Rect.Dx()) / float64(pic.Rect.Dy())
			err = writePNG(picPath, pic)
		}
		a.post(func() {
			k.busy[id] = false
			if err != nil {
				a.toast(a.u.tf("Smart object %s could not be kept: %s", id, a.describe(err)), true)
			} else {
				a.drawingsChanged([]string{picPath})
				a.smartPictureChanged(id, aspect)
			}
			if _, ok := k.waiting[id]; ok {
				a.writeSmart(id)
			}
		})
	})
}

// writeFileWhole writes a file whole or not at all. Windows refuses to replace a file another reader has open, as
// a preview reading the picture may for a moment, so the replacing is tried again for a while.
func writeFileWhole(path string, data []byte) error {
	tmp := path + ".saving"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	var err error
	for range 40 {
		if err = os.Rename(tmp, path); err == nil {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	os.Remove(tmp)
	return err
}

// readSmart reads the kept shared layers of link id.
func (a *App) readSmart(id string) (paint.SmartContent, error) {
	data, err := os.ReadFile(a.smartPath(id))
	if err != nil {
		return paint.SmartContent{}, err
	}
	c, err := paint.LoadSmart(bytes.NewReader(data), int64(len(data)))
	if err == nil && c.ID != id {
		err = fmt.Errorf("%w: it holds smart object %s", paint.ErrNotACanvas, c.ID)
	}
	return c, err
}

// linkSmart takes what is kept of a newly opened canvas's smart objects into them: from a tab that has a smart
// object's own file open, else from the smart objects folder. A missing kept file is made from the canvas's spare,
// and a damaged one set aside and made again from it, telling the user; the canvas keeps its spare either way.
func (a *App) linkSmart(ad *artDoc) {
	if id := ad.d.SmartSource(); id != "" {
		return // the kept file itself
	}
	for _, id := range ad.d.SmartLinks() {
		if src := a.sourceTab(id); src != nil {
			if c, ok := src.d.SmartContentOf(id); ok && ad.d.ApplySmart(c) {
				ad.changedAll()
			}
			continue
		}
		spare, _ := ad.d.SmartContentOf(id)
		a.background(func(context.Context) {
			c, err := a.readSmart(id)
			a.post(func() {
				switch {
				case err == nil:
					if ad.d.ApplySmart(c) {
						ad.changedAll()
						a.syncSmart() // its layers changed: the other open canvases follow
					}
					a.ensureSmartImage(c)
				case errors.Is(err, fs.ErrNotExist):
					a.keepSmart(spare, true)
					a.toast(a.u.tf("The file of smart object %s was missing; it was made again from the copy kept in this canvas", id), false)
				default:
					os.Rename(a.smartPath(id), a.smartPath(id)+".damaged")
					a.keepSmart(spare, true)
					a.toast(a.u.tf("The file of smart object %s could not be read (%s); it was set aside and made again from the copy kept in this canvas",
						id, a.describe(err)), true)
				}
			})
		})
	}
}

// ensureSmartImage makes the picture of kept shared layers when it is missing.
func (a *App) ensureSmartImage(c paint.SmartContent) {
	if _, err := os.Stat(a.smartImage(c.ID)); errors.Is(err, fs.ErrNotExist) {
		a.keepSmart(c, false)
	}
}

// openSmartFile opens the smart object's own file of the chosen smart object in a canvas tab, where its shared
// layers are edited as any picture's; a missing file is made first from the canvas's copy.
func (a *App) openSmartFile(ad *artDoc) {
	n := ad.d.Node(ad.d.Active)
	if n == nil || !n.IsSmart() {
		a.toast(a.describe(paint.ErrNotSmart), true)
		return
	}
	id := n.Smart.ID
	if src := a.sourceTab(id); src != nil {
		for i, d := range a.docs {
			if d.art == src {
				a.switchTab(i)
			}
		}
		return
	}
	path := a.smartPath(id)
	if _, err := os.Stat(path); err != nil {
		c, _ := ad.d.SmartContentOf(id)
		var buf bytes.Buffer
		err = os.MkdirAll(a.smartDir(), 0o755)
		if err == nil {
			err = paint.SaveSmart(&buf, c)
		}
		if err == nil {
			err = writeFileWhole(path, buf.Bytes())
		}
		if err != nil {
			a.toast(a.u.tf("Smart object %s could not be kept: %s", id, a.describe(err)), true)
			return
		}
	}
	a.openCanvasFileWith(path, nil)
}

// smartTitle is a smart object's own file's tab title: its name and link id.
func (a *App) smartTitle(ad *artDoc) string {
	name := ad.d.SmartName()
	if name == "" {
		name = a.u.t("Smart object")
	}
	return name + " #" + ad.d.SmartSource()
}

// smartGroupName names the group a layer made a smart object is put into.
func (a *App) smartGroupName() string { return a.u.t("Smart object") }

// copyLinkID puts the chosen smart object's link id on the clipboard, to compare with another's.
func (a *App) copyLinkID(ad *artDoc) {
	n := ad.d.Node(ad.d.Active)
	if n == nil || !n.IsSmart() {
		return
	}
	if err := a.host.SetClipboard(n.Smart.ID); err != nil {
		a.toast(a.u.tf("Could not copy: %s", a.describe(err)), true)
		return
	}
	a.toast(a.u.tf("Copied the link id %s", n.Smart.ID), false)
}

// showSmartFolder shows the smart objects folder, made if it is not there yet.
func (a *App) showSmartFolder() {
	if err := os.MkdirAll(a.smartDir(), 0o755); err != nil {
		a.toast(a.describe(err), true)
		return
	}
	a.host.Reveal(a.smartDir())
}

// currentSmart is what is kept now of link id's shared layers, for a canvas other than but: from the tab that has
// the smart object's own file open, else from another open canvas holding a copy, else from the smart objects
// folder.
func (a *App) currentSmart(id string, but *artDoc) (paint.SmartContent, bool) {
	if src := a.sourceTab(id); src != nil && src != but {
		return src.d.SmartContentOf(id)
	}
	for _, d := range a.docs {
		if d.art != nil && d.art != but && d.art.d.SourceOrCopy(id) {
			return d.art.d.SmartContentOf(id)
		}
	}
	c, err := a.readSmart(id)
	return c, err == nil
}

// smartPictureChanged keeps the video overlays of link id's picture in its proportions, which a change to its
// layers may alter.
func (a *App) smartPictureChanged(id string, aspect float64) {
	a.mindRefChanged(id) // the mind maps showing it read it again
	for _, d := range a.docs {
		if d.p == nil {
			continue
		}
		for ti := range d.p.Overlays {
			items := d.p.Overlays[ti].Items
			for i := range items {
				if items[i].Smart == id && items[i].Aspect != aspect {
					items[i].Aspect, d.dirty = aspect, true
				}
			}
		}
	}
	a.requestFrame()
}

// addSmartOverlay places the picture of link id's shared layers over the video at the playhead; it follows every
// change to them.
func (a *App) addSmartOverlay(id string) {
	if a.p == nil {
		return
	}
	if _, err := a.placeSmartOverlay(id, a.playhead, nil); errors.Is(err, errSmartEmpty) {
		a.toast(a.u.tf("Smart object %s shows nothing yet", id), false)
		return
	} else if err != nil {
		a.toast(a.describe(err), true)
		return
	}
	a.showPanel(tabOverlays)
}

// errSmartEmpty is a smart object with nothing drawn in it yet.
var errSmartEmpty = errors.New("the smart object shows nothing yet")

// placeSmartOverlay puts the picture of link id's shared layers over the edit from at, chosen, as one step with
// what place then does to it, and returns the overlay's id; the picture follows every change to the layers.
func (a *App) placeSmartOverlay(id string, at float64, place func(p *project.Project, overlay int) error) (int, error) {
	u := &a.u
	c, ok := a.currentSmart(id, nil)
	if !ok {
		return 0, errors.New(u.tf("Smart object %s is not kept any more; open a canvas that holds it, then paste it again", id))
	}
	pic := paint.SmartPicture(c)
	if pic == nil {
		return 0, errSmartEmpty
	}
	path := a.smartImage(id)
	if err := writePNG(path, pic); err != nil {
		return 0, errors.New(u.tf("The pasted image could not be saved: %v", err))
	}
	a.drawingsChanged([]string{path})
	aspect := float64(pic.Rect.Dx()) / float64(pic.Rect.Dy())
	var overlay int
	err := a.edit(func(p *project.Project) error {
		overlay = p.AddSmart(path, id, aspect, at)
		a.sel = selection{kind: selOverlay, id: overlay}
		if place != nil {
			return place(p, overlay)
		}
		return nil
	})
	return overlay, err
}
