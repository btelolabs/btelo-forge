package ui

import (
	"path/filepath"
	"slices"
	"time"

	"github.com/btelolabs/btelo-forge/internal/project"
)

type selKind int

const (
	selNone selKind = iota
	selClip
	selMusic
	selOverlay
	selTrack
)

// group is a kind of track.
type group int

const (
	groupVideo group = iota
	groupOverlay
	groupMusic
)

// selection is what the user picked: an item by id, or a whole track by group and index.
type selection struct {
	kind  selKind
	id    int
	group group
	track int
}

// doc is one tab: an edit with its own history, playhead, selection and view. An empty tab has no project.
type doc struct {
	p          *project.Project
	hist       project.History
	dragBefore *project.Project
	bodyBefore *project.Project
	dirty      bool
	savedAt    time.Time
	playhead   float64
	sel        selection
	picked     map[int]bool // items selected besides sel
	marquee    *marquee
	view       timelineView
	shown      shownFrame
	times      timeFields
	opening    string
	art        *artDoc // a canvas tab instead of an edit
}

func (d *doc) title(a *App) string {
	switch {
	case d.art != nil:
		return a.artTitle(d.art)
	case d.p != nil:
		return filepath.Base(d.p.Sources[0].Path)
	case d.opening != "":
		return filepath.Base(d.opening)
	}
	return a.u.t("New tab")
}

// save writes the edit when it has unsaved changes.
func (d *doc) save(dir string) error {
	if d.p == nil || !d.dirty {
		return nil
	}
	d.dirty, d.savedAt = false, time.Now()
	return project.Save(project.SavePath(dir, d.p.Sources[0].Path), d.p)
}

// newTab opens an empty tab and switches to it.
func (a *App) newTab() {
	a.docs = append(a.docs, &doc{})
	a.switchTab(len(a.docs) - 1)
}

func (a *App) switchTab(i int) {
	if a.doc == a.docs[i] {
		return
	}
	a.player.Stop()
	a.u.focus = ""
	a.doc = a.docs[i]
	a.requestFrame()
}

// closeTab saves and closes tab i; the last tab is replaced by an empty one.
func (a *App) closeTab(i int) {
	d := a.docs[i]
	if err := d.save(a.saveDir); err != nil {
		a.toast(a.u.tf("The edit could not be saved: %v", err), true)
	}
	if d.art != nil {
		if d.art.stroke != nil {
			d.art.stroke.End()
		}
		if err := a.saveArt(d.art, true); err != nil {
			a.toast(a.u.tf("The canvas could not be saved: %s", a.describe(err)), true)
		}
	}
	if a.doc == d {
		a.player.Stop()
	}
	a.docs = slices.Delete(a.docs, i, i+1)
	if len(a.docs) == 0 {
		a.doc = nil
		a.newTab()
		return
	}
	if a.doc == d {
		a.doc = nil
		a.switchTab(min(i, len(a.docs)-1))
	}
}

// change applies an edit and records it for undo when it changed anything.
func (a *App) change(edit func()) {
	before := a.p.Clone()
	edit()
	a.committed(before)
}

// committed records before as an undo step when the project differs from it.
func (a *App) committed(before *project.Project) {
	if before == nil || a.p == nil || project.Equal(before, a.p) {
		return
	}
	a.hist.Commit(before)
	a.edited()
}

// edited reacts to any project change: clamps the view, saves later and refreshes what is shown or heard.
func (a *App) edited() {
	a.dirty = true
	a.playhead = min(a.playhead, a.p.Duration())
	if !a.selectionExists() {
		a.sel = selection{}
	}
	a.pruneSelection()
	if a.player.Playing() {
		a.play(a.playhead)
	}
	a.requestFrame()
}

func (a *App) selectionExists() bool {
	switch a.sel.kind {
	case selClip:
		_, _, ok := a.p.FindClip(a.sel.id)
		return ok
	case selMusic:
		_, _, ok := a.p.FindMusic(a.sel.id)
		return ok
	case selOverlay:
		_, _, ok := a.p.FindOverlay(a.sel.id)
		return ok
	case selTrack:
		return a.sel.track < a.trackCount(a.sel.group)
	}
	return true
}

func (a *App) trackCount(g group) int {
	switch g {
	case groupVideo:
		return len(a.p.Video)
	case groupOverlay:
		return len(a.p.Overlays)
	}
	return len(a.p.Music)
}

func (a *App) undo() {
	if a.p == nil {
		return
	}
	if prev := a.hist.Undo(a.p); prev != nil {
		a.p = prev
		a.edited()
	}
}

func (a *App) redo() {
	if a.p == nil {
		return
	}
	if next := a.hist.Redo(a.p); next != nil {
		a.p = next
		a.edited()
	}
}

func (a *App) autosave() {
	for _, d := range a.docs {
		if d.p == nil || !d.dirty || time.Since(d.savedAt) < time.Second {
			continue
		}
		if err := d.save(a.saveDir); err != nil {
			a.toast(a.u.tf("The edit could not be saved: %v", err), true)
		}
	}
}
