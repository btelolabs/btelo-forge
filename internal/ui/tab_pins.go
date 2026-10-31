package ui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
)

// The strip's tabs can be dragged into another order, and pinned from their menu. Pinned tabs stay at the left, a drag
// keeps them there, and each opens again with the app holding what it held: the settings keep where its work is, not
// the work, which every tab keeps in its own file as it goes.

// The kinds of tab that can be opened again. A new kind of tab needs its line in tabSpec and in restoreTab.
const (
	tabCanvas   = "canvas"
	tabMind     = "mind"
	tabOffice   = "office"
	tabModel    = "model"
	tabEdit     = "edit"
	tabVideo    = "video"
	tabEditor   = "editor" // the video editor, before it has a video
	tabFiles    = "files"
	tabLive     = "live"
	tabKeyboard = "keyboard"
	tabAI       = "ai"
	tabMail     = "mail"
)

// savedTab is a pinned tab as it is kept between runs.
type savedTab struct {
	Kind  string      `json:"kind"`
	Path  string      `json:"path,omitempty"`  // the file it has open
	Panes []savedPane `json:"panes,omitempty"` // a file manager's lists
	Front bool        `json:"front,omitempty"` // it was the tab in front
}

// glyph is the symbol of a tab that is still opening again, its kind's as it will be once it is in.
func (s *savedTab) glyph() string {
	switch s.Kind {
	case tabCanvas:
		return icBrush
	case tabMind:
		return icMind
	case tabOffice:
		if f := officeFormatOf(s.Path); f != nil {
			return f.glyph
		}
	}
	return icVideo
}

func sameTab(a, b savedTab) bool {
	return a.Kind == b.Kind && a.Path == b.Path && a.Front == b.Front && slices.Equal(a.Panes, b.Panes)
}

// tabSpec says how to open tab d again; a tab with nothing to open again, or that only exists beside another as a
// smart object's does, has none.
func tabSpec(d *doc) (savedTab, bool) {
	switch {
	case d.restoring != nil:
		return *d.restoring, true
	case d.art != nil:
		return savedTab{Kind: tabCanvas, Path: d.art.path}, d.art.d.SmartSource() == ""
	case d.files != nil:
		return savedTab{Kind: tabFiles, Panes: d.files.savedPanes()}, true
	case d.board != nil:
		return savedTab{Kind: tabMind, Path: d.board.path}, true
	case d.office != nil:
		return savedTab{Kind: tabOffice, Path: d.office.path}, true
	case d.liveTab:
		return savedTab{Kind: tabLive}, true
	case d.keyboardTab:
		return savedTab{Kind: tabKeyboard}, true
	case d.aiTab:
		return savedTab{Kind: tabAI}, true
	case d.mailTab:
		return savedTab{Kind: tabMail}, true
	case d.model != nil:
		return savedTab{Kind: tabModel, Path: d.model.path}, true
	case d.p != nil && d.file != "":
		return savedTab{Kind: tabEdit, Path: d.file}, true
	case d.p != nil:
		return savedTab{Kind: tabVideo, Path: d.p.Sources[0].Path}, true
	case d.opening != "" && isEditFile(d.opening):
		return savedTab{Kind: tabEdit, Path: d.opening}, true
	case d.opening != "":
		return savedTab{Kind: tabVideo, Path: d.opening}, true
	case d.editing:
		return savedTab{Kind: tabEditor}, true
	}
	return savedTab{}, false
}

// tabMenu is what a right-click on a tab offers.
func (a *App) tabMenu(d *doc) []menuItem {
	u := &a.u
	_, keepable := tabSpec(d)
	pin := menuItem{label: u.t("Pin the tab"), run: func() { a.pinTab(d, true) }, disabled: !keepable}
	if d.pinned {
		pin = menuItem{label: u.t("Unpin the tab"), run: func() { a.pinTab(d, false) }}
	}
	return []menuItem{pin, menuSep, {label: u.t("Close the tab"), run: func() {
		if i := slices.Index(a.docs, d); i >= 0 {
			a.closeTab(i)
		}
	}}}
}

// pinTab pins or unpins tab d. A new canvas has no file until its first change, so pinning writes it: there is
// something to open again.
func (a *App) pinTab(d *doc, pin bool) {
	if pin && d.art != nil {
		d.art.dirty = true
		if err := a.saveArt(d.art, true); err != nil {
			a.toast(a.u.tf("The canvas could not be saved: %s", a.describe(err)), true)
			return
		}
	}
	d.pinned = pin
	a.sortTabs()
	a.rememberTabs()
}

// sortTabs puts the pinned tabs before the others, each keeping its order.
func (a *App) sortTabs() {
	slices.SortStableFunc(a.docs, func(x, y *doc) int {
		switch {
		case x.pinned == y.pinned:
			return 0
		case x.pinned:
			return -1
		}
		return 1
	})
}

// rememberTabs writes the pinned tabs to the settings when they, what they hold, or the one in front changed. A tab
// that lost what it held, a video that could not be opened again, is pinned no longer.
func (a *App) rememberTabs() {
	var saved []savedTab
	lost := false
	for _, d := range a.docs {
		if !d.pinned {
			continue
		}
		s, ok := tabSpec(d)
		if !ok {
			d.pinned, lost = false, true
			continue
		}
		s.Front = d == a.doc
		saved = append(saved, s)
	}
	if lost {
		a.sortTabs()
	}
	if !slices.EqualFunc(saved, a.settings.PinnedTabs, sameTab) {
		a.settings.PinnedTabs = saved
		a.saveSettings()
	}
}

// restoreTabs opens the pinned tabs of the last run, in their order, each in its place on the strip at once while its
// file is read; with none there is an empty tab.
func (a *App) restoreTabs() {
	for _, s := range a.settings.PinnedTabs {
		d := &doc{pinned: true, restoring: &s, opening: s.Path}
		a.docs = append(a.docs, d)
		if s.Front || a.doc == nil {
			a.doc = d
		}
	}
	if len(a.docs) == 0 {
		a.newTab()
		return
	}
	for _, d := range slices.Clone(a.docs) {
		a.restoreTab(d)
	}
}

// restoreTab opens tab d again. What it held comes into that tab, whichever is in front by then; a file that can no
// longer be read closes the tab and says so.
func (a *App) restoreTab(d *doc) {
	s := *d.restoring
	path := s.Path
	fill := func(show func()) {
		if !slices.Contains(a.docs, d) {
			return // closed meanwhile
		}
		d.restoring, d.opening = nil, ""
		front := a.doc
		a.doc = d
		show()
		a.doc = front
		a.requestFrame()
	}
	lost := func(err error) {
		if i := slices.Index(a.docs, d); i >= 0 {
			a.closeTab(i)
			a.toast(a.u.tf("Cannot open %s: %s", filepath.Base(path), a.describe(err)), true)
		}
	}
	switch s.Kind {
	case tabCanvas:
		a.background(func(context.Context) {
			c, err := readCanvas(path)
			a.post(func() {
				if err != nil {
					lost(err)
					return
				}
				fill(func() { a.openArt(c, path) })
			})
		})
	case tabMind:
		measure := a.mindMeasure()
		a.background(func(context.Context) {
			m, err := readMindFile(path, measure)
			a.post(func() {
				if err != nil {
					lost(err)
					return
				}
				fill(func() { a.openMindDoc(m, path) })
			})
		})
	case tabOffice:
		f := officeFormatOf(path)
		if f == nil {
			lost(os.ErrInvalid)
			return
		}
		a.background(func(context.Context) {
			data, err := os.ReadFile(path)
			a.post(func() {
				if err != nil {
					lost(err)
					return
				}
				fill(func() {
					if _, err := a.openOffice(f, path, data); err != nil {
						lost(err)
					}
				})
			})
		})
	case tabModel:
		fill(func() {
			if path == "" {
				a.openModelTab()
			} else {
				a.openModelFile(path)
			}
		})
	case tabEdit:
		a.whenToolsReady(func() { fill(func() { a.openEditFile(path) }) })
	case tabVideo:
		a.whenToolsReady(func() { fill(func() { a.openVideoIn(d, path, false) }) })
	case tabEditor:
		fill(a.newEditor)
	case tabFiles:
		fill(func() { a.openFilesTab(s.Panes) })
	case tabLive:
		fill(a.openLive)
	case tabKeyboard:
		fill(a.openKeyboardTab)
	case tabAI:
		fill(a.openAITab)
	case tabMail:
		fill(a.openMailTab)
	default:
		lost(os.ErrInvalid) // written by another version
	}
}

// whenToolsReady runs fn once ffmpeg is ready, which may be now.
func (a *App) whenToolsReady(fn func()) {
	if a.tools.ready {
		fn()
		return
	}
	a.tools.whenReady = append(a.tools.whenReady, fn)
}

// showRestoring brings forward the pinned tab that is opening path again, if one is, for what would open it twice.
func (a *App) showRestoring(path string) bool {
	for i, d := range a.docs {
		if d.restoring != nil && d.restoring.Path != "" && samePath(d.restoring.Path, path) {
			a.switchTab(i)
			return true
		}
	}
	return false
}

// tabDrag is a tab held by the mouse.
type tabDrag struct {
	d     *doc
	grab  float32 // where in the tab it was taken
	moved bool    // the pointer has really moved, so it follows
}

// tabGroup is the first and last place on the strip tab i can be dragged to: among the pinned tabs, or the others.
func (a *App) tabGroup(i int) (lo, hi int) {
	pinned := 0
	for _, d := range a.docs {
		if d.pinned {
			pinned++
		}
	}
	if a.docs[i].pinned {
		return 0, pinned - 1
	}
	return pinned, len(a.docs) - 1
}

// dragTab goes on with the held tab, at index i and drawn at at, on a strip whose tabs start at first, step apart: past
// the middle of a neighbour it takes that one's place, the tabs it passes sliding aside. Let go, it comes forward, as a
// click on it does.
func (a *App) dragTab(i int, at, first, step float32) {
	u := &a.u
	t := a.tabDrag
	id := tabID(t.d)
	switch {
	case u.dragging(id):
		if !u.drag.moved {
			return
		}
		t.moved = true
		lo, hi := a.tabGroup(i)
		to := max(min(int((at-first)/step+0.5), hi), lo)
		for ; i < to; i++ {
			a.docs[i], a.docs[i+1] = a.docs[i+1], a.docs[i]
			a.docs[i].tabShift += step
		}
		for ; i > to; i-- {
			a.docs[i], a.docs[i-1] = a.docs[i-1], a.docs[i]
			a.docs[i].tabShift -= step
		}
	case u.letGo(id):
		a.tabDrag = nil
		t.d.tabShift = at - (first + float32(i)*step)
		a.switchTab(i)
	default:
		a.tabDrag = nil // the frame that held it failed, or something else took the mouse
	}
}

// settleTab moves a tab's distance from its place towards none.
func settleTab(shift, near float32) float32 {
	if shift > -near && shift < near {
		return 0
	}
	return shift * 0.65
}

// tabsMoving reports whether a tab is still sliding to its place.
func (a *App) tabsMoving() bool {
	return slices.ContainsFunc(a.docs, func(d *doc) bool { return d.tabShift != 0 })
}
