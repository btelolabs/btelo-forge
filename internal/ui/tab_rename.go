package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/paint"
	"github.com/btelolabs/btelo-forge/internal/project"
)

// A double-click on a tab names what it holds: a canvas's file, kept where it is under the new name (with the
// picture a drawing on a video shows, and the overlays that show it, following); a smart object's own file, its
// name, as its link id names the file; an edit's file, saved under the name the first time.

// renameable reports whether a tab holds something a name can be given: a canvas, an edit or a mind map.
func (d *doc) renameable() bool { return d.art != nil || d.p != nil || d.board != nil }

// tabName is the name a tab's thing has now, as its name box starts.
func (a *App) tabName(d *doc) string {
	switch {
	case d.art != nil && d.art.d.SmartSource() != "":
		return d.art.d.SmartName()
	case d.board != nil:
		return d.board.title()
	case d.art != nil:
		return strings.TrimSuffix(filepath.Base(d.art.path), paint.Ext)
	case d.file != "":
		return strings.TrimSuffix(filepath.Base(d.file), project.EditExt)
	case d.p != nil:
		return strings.TrimSuffix(filepath.Base(d.p.Sources[0].Path), filepath.Ext(d.p.Sources[0].Path))
	}
	return ""
}

// errNameTaken means a file of the name asked for is there already.
var errNameTaken = errors.New("a file of that name is there already")

// renameTab gives the thing in tab d the name typed, telling the user why when it cannot.
func (a *App) renameTab(d *doc, name string) {
	name = strings.TrimSpace(name)
	if name == "" || name == a.tabName(d) {
		return
	}
	var err error
	switch {
	case d.art != nil && d.art.d.SmartSource() != "":
		ad := d.art
		a.artRun(ad, func() error { ad.d.RenameSmart(name); return nil })
	case d.art != nil:
		err = a.renameCanvas(d.art, safeFileName(name))
	case d.board != nil:
		err = a.renameMindFile(d.board, safeFileName(name))
	case d.p != nil:
		err = a.renameEdit(d, safeFileName(name))
	}
	if err != nil {
		a.toast(a.u.tf("Could not rename it: %s", a.describe(err)), true)
	}
}

// renameCanvas moves a canvas's file to the new name beside it, with a drawing's picture, and makes the overlays
// that show that picture follow.
func (a *App) renameCanvas(ad *artDoc, name string) error {
	to := filepath.Join(filepath.Dir(ad.path), name+paint.Ext)
	if _, err := os.Stat(to); err == nil {
		return errNameTaken
	}
	// Saved first, so what is moved is what is shown.
	ad.dirty = true
	if err := a.saveArt(ad, true); err != nil {
		return err
	}
	if err := os.Rename(ad.path, to); err != nil {
		return err
	}
	from := ad.path
	ad.path = to
	oldPic, newPic := drawingImage(from), drawingImage(to)
	if _, err := os.Stat(oldPic); err == nil {
		if err := os.Rename(oldPic, newPic); err != nil {
			return err
		}
	}
	for _, o := range a.docs {
		if o.p == nil {
			continue
		}
		for ti := range o.p.Overlays {
			items := o.p.Overlays[ti].Items
			for i := range items {
				if items[i].Canvas != "" && filepath.Clean(items[i].Canvas) == filepath.Clean(from) {
					items[i].Canvas, items[i].Image, o.dirty = to, newPic, true
				}
			}
		}
	}
	a.drawingsChanged([]string{oldPic, newPic})
	return nil
}

// renameEdit names an edit's file: it is moved, or saved under the name the first time.
func (a *App) renameEdit(d *doc, name string) error {
	if d.file == "" {
		dir, err := a.uploadDir()
		if err != nil {
			return err
		}
		to := filepath.Join(dir, name+project.EditExt)
		if _, err := os.Stat(to); err == nil {
			return errNameTaken
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := project.WriteFile(to, d.p); err != nil {
			return err
		}
		d.file = to
		return nil
	}
	to := filepath.Join(filepath.Dir(d.file), name+project.EditExt)
	if _, err := os.Stat(to); err == nil {
		return errNameTaken
	}
	if err := os.Rename(d.file, to); err != nil {
		return err
	}
	d.file = to
	return nil
}
