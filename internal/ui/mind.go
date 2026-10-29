package ui

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/mind"
)

// Mind maps: a tab whose board holds mind maps, flowcharts, tables, words, pictures, videos, canvases' smart objects
// and other mind maps, each a node (internal/mind). The map is saved as a .btmind file a moment after each change,
// as a canvas is; what it shows from other files is only referred to, and follows when those files change: a smart
// object as it is drawn on, a nested map as it is edited in its own tab.

// Icons of the mind map tab.
const (
	icMind        = "\ue97a" // account_tree
	icShapeTool   = "\ue574" // category
	icTextTool    = "\ue262" // text_fields
	icConnect     = "\ue157" // link
	icFitBoard    = "\ue56b" // fit_screen
	icMinimap     = "\ue55b" // map
	icFold        = "\ue5d6" // unfold_less
	icArrowEnd    = "\ue5c8" // arrow_forward
	icDashed      = "\ue919" // line_style
	icPalette     = "\ue40a" // palette
	icTextSmaller = "\ueadd" // text_decrease
	icTextBigger  = "\ueae2" // text_increase
	icMore        = "\ue5d3" // more_horiz
	icTask        = "\ue834" // check_box
	icFocus       = "\ue3dc" // filter_center_focus
)

// mindDoc is a mind map tab.
type mindDoc struct {
	m     *mind.Map
	path  string
	view  mind.View // OX and OY are from the board's top-left on the screen, in whole pixels
	fit   bool      // fit the view to the map at the next frame
	tiles *mind.Tiles
	whole bool // the last frame showed every tile

	sel      map[mind.ID]bool
	primary  mind.ID     // the node chosen last, which keys act on
	edge     mind.EdgeID // the chosen connector
	act      *mindAction // what the pointer is doing
	text     *mindText   // words being typed into a node or a connector's label
	cell     *mindCell   // a table cell being typed into
	shape    mind.Shape  // the shape the board's menu last added
	video    mind.ID     // the video playing in place
	panning  bool        // Space held, or the hand dragging the view
	themes   bool        // the theme panel is open
	minimap  bool        // the overview is hidden (false shows it)
	overview *mind.Tiles // the overview's own tiles, drawn again only where the board changed
	ovStale  []mind.Rect // what changed since the overview was last drawn again
	ovAll    bool
	ovAt     time.Time
	tableAt  mindCellAt // the table cell last typed into or pointed at, for the table's menu

	dirty   bool
	changed time.Time
	saving  bool
	saveErr error
	saved   uint64 // the map's version when it was last saved
}

// title is the tab's: the map's file name.
func (md *mindDoc) title() string {
	return strings.TrimSuffix(filepath.Base(md.path), mind.Ext)
}

// mindMeasure is how the board's words are measured: with the window's fonts.
func (a *App) mindMeasure() mind.Measurer { return mind.FontMeasurer{Fonts: a.u.fonts} }

// openMindDoc shows a map in a tab, the current one when it is empty.
func (a *App) openMindDoc(m *mind.Map, path string) *mindDoc {
	if a.occupied() {
		a.newTab()
	}
	m.SetRouteBudget(mindRouteBudget)
	md := &mindDoc{m: m, path: path, fit: true, tiles: mind.NewTiles(256), overview: mind.NewTiles(32), sel: map[mind.ID]bool{},
		saved: m.Version()}
	a.board = md
	a.requestFrame()
	return md
}

// newMindMap makes a map with one central topic, saved at once so it is never only in memory.
func (a *App) newMindMap() error {
	path, err := a.capturePath(a.u.t("Mind map %s"), mind.Ext, time.Now())
	if err != nil {
		return err
	}
	m := mind.New(strings.TrimSuffix(filepath.Base(path), mind.Ext), a.mindMeasure())
	id, err := m.Add(mind.Node{Text: a.u.t("Central topic")}, 0, -1)
	if err != nil {
		return err
	}
	m.Saved()
	md := a.openMindDoc(m, path)
	md.selectOnly(id)
	md.dirty = true
	return a.saveMind(md, true)
}

// newMindNow makes a map, telling the user when it cannot.
func (a *App) newMindNow() {
	if err := a.newMindMap(); err != nil {
		a.toast(a.u.tf("The mind map could not be saved: %s", a.describe(err)), true)
	}
}

// openMindFile opens a saved map, or shows the tab that has it open.
func (a *App) openMindFile(path string) {
	for i, d := range a.docs {
		if d.board != nil && mind.NestKey(d.board.path) == mind.NestKey(path) {
			a.switchTab(i)
			return
		}
	}
	measure := a.mindMeasure()
	a.background(func(ctx context.Context) {
		m, err := readMindFile(path, measure)
		a.post(func() {
			if err != nil {
				a.toast(a.u.tf("Cannot open %s: %s", filepath.Base(path), a.describe(err)), true)
				return
			}
			for i, d := range a.docs {
				if d.board != nil && mind.NestKey(d.board.path) == mind.NestKey(path) {
					a.switchTab(i) // opened twice before the first was read
					return
				}
			}
			m.Layout()
			a.openMindDoc(m, path)
		})
	})
}

// readMindFile reads a map file.
func readMindFile(path string, measure mind.Measurer) (*mind.Map, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m, err := mind.Load(bytes.NewReader(data), filepath.Dir(path), measure)
	if err == nil {
		// All of it away from the window, connectors' ways round the nodes too, for up to a second: a map with very
		// many tangled connectors routes the rest in its first frames.
		m.SetRouteBudget(mindLoadRouting)
		m.Layout()
	}
	return m, err
}

// mindRouteBudget is how long a frame may spend routing connectors round the nodes; the rest goes on in the
// frames after.
const mindRouteBudget = 6 * time.Millisecond

// mindLoadRouting is how long reading a map may spend routing its connectors before it is shown.
const mindLoadRouting = 10 * time.Second

// mindSaveDelay is how long after the last change a map is saved.
const mindSaveDelay = 3 * time.Second

// mindChanged notes that the map changed and is to be saved.
func (md *mindDoc) mindChanged() {
	md.dirty, md.changed = true, time.Now()
}

// saveMind writes the map: read here, compressed and written on another goroutine, or here when wait is true
// (closing). Every open map showing it draws it again once it is written.
func (a *App) saveMind(md *mindDoc, wait bool) error {
	if !md.dirty || md.saving && !wait {
		return nil
	}
	snap, err := mind.Snapshot(md.m, filepath.Dir(md.path))
	if err != nil {
		return err
	}
	md.dirty, md.saving = false, true
	version, path := md.m.Version(), md.path
	write := func() error {
		var buf bytes.Buffer
		if err := snap.Write(&buf); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return writeFileWhole(path, buf.Bytes())
	}
	done := func(err error) {
		md.saving, md.saveErr = false, err
		if err != nil {
			md.dirty, md.changed = true, time.Now() // tried again after the usual delay
			a.toast(a.u.tf("The mind map could not be saved: %s", a.describe(err)), true)
			return
		}
		md.saved = version
		md.m.Saved()
		a.mindFileChanged(path)
	}
	if wait {
		err := write()
		done(err)
		return err
	}
	a.background(func(context.Context) {
		err := write()
		a.post(func() { done(err) })
	})
	return nil
}

// autosaveMind saves maps a moment after their last change, and notes changes made since the last frame.
func (a *App) autosaveMind() {
	for _, d := range a.docs {
		md := d.board
		if md == nil {
			continue
		}
		if md.m.Version() != md.saved && !md.dirty && !md.saving && md.text == nil && md.act == nil {
			md.mindChanged()
		}
		if due, ok := mindSaveDue(md); ok && !time.Now().Before(due) {
			a.saveMind(md, false)
		}
	}
}

// mindSaveDue is when a map with changes is saved; ok is false while there is nothing to save, or while a drag or
// typing goes on.
func mindSaveDue(md *mindDoc) (time.Time, bool) {
	if !md.dirty || md.saving || md.text != nil || md.act != nil {
		return time.Time{}, false
	}
	return md.changed.Add(mindSaveDelay), true
}

// closeMind saves a map tab that is being closed, finishing what is being typed.
func (a *App) closeMind(md *mindDoc) {
	a.commitMindText(md)
	a.commitMindCell(md)
	if md.m.Version() != md.saved {
		md.dirty = true
	}
	if err := a.saveMind(md, true); err != nil {
		a.toast(a.u.tf("The mind map could not be saved: %s", a.describe(err)), true)
	}
	if a.preview.id == mindVideoID(md) {
		a.closeFilePreview()
	}
}

// mindFileChanged lets every open map that shows the map file path, at any depth, draw it again.
func (a *App) mindFileChanged(path string) {
	for _, ref := range a.mindPictures().dropFile(path) {
		a.redrawShowing(ref, 0)
	}
}

// mindRefChanged lets every open map that shows ref (a file, or a smart object's link id) read it again.
func (a *App) mindRefChanged(ref string) {
	for _, r := range a.mindPictures().drop(ref) {
		a.redrawShowing(r, 0)
	}
}

// mindPictureChanged lets every open map that shows the picture or canvas file path read it again, as a canvas
// shown on a map is saved.
func (a *App) mindPictureChanged(path string) {
	for _, ref := range a.mindPictures().dropPicture(path) {
		a.redrawShowing(ref, 0)
	}
}

// renameMindFile moves the map's file to the new name beside it, as a tab's name is edited; the maps that show it
// by its old name show it as missing until they are pointed at the new one, as for any moved file.
func (a *App) renameMindFile(md *mindDoc, name string) error {
	to := filepath.Join(filepath.Dir(md.path), name+mind.Ext)
	if _, err := os.Stat(to); err == nil {
		return errNameTaken
	}
	// Saved first, so what is moved is what is shown.
	md.dirty = true
	if err := a.saveMind(md, true); err != nil {
		return err
	}
	if err := os.Rename(md.path, to); err != nil {
		return err
	}
	from := md.path
	md.path = to
	md.m.SetTitle(name)
	a.mindFileChanged(from)
	return nil
}

// overviewLater notes areas of the board that changed, for the overview to draw again.
func (md *mindDoc) overviewLater(regions []mind.Rect, all bool) {
	md.ovAll = md.ovAll || all || len(md.ovStale)+len(regions) > 256
	if !md.ovAll {
		md.ovStale = append(md.ovStale, regions...)
	}
}

// overviewDelay is how often the overview follows the board while it is dragged on or typed into: at one tile a
// map, drawing it again at every key would cost a large map's frames.
const overviewDelay = 500 * time.Millisecond

// overviewNow draws again the overview's tiles over what changed: at once when nothing is dragged or typed, else
// at most every overviewDelay, the window waking for it.
func (md *mindDoc) overviewNow() {
	if len(md.ovStale) == 0 && !md.ovAll {
		return
	}
	if (md.act != nil || md.text != nil) && time.Since(md.ovAt) < overviewDelay {
		return
	}
	md.overview.Invalidate(md.ovStale, md.ovAll)
	md.ovStale, md.ovAll, md.ovAt = md.ovStale[:0], false, time.Now()
}

// overviewDue is when the overview follows changes it is holding back, zero when none.
func (md *mindDoc) overviewDue() time.Time {
	if len(md.ovStale) == 0 && !md.ovAll {
		return time.Time{}
	}
	return md.ovAt.Add(overviewDelay)
}
