package ui

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/btelolabs/btelo-forge/internal/assets"
	"github.com/btelolabs/btelo-forge/internal/files"
)

// The file manager is a tab of folder lists side by side, driven as gdu and vim are: hjkl or the arrows move, a
// folder can be analysed to see what takes the space, and copies, moves and deletions run in the background with
// their progress floating over the lists, so the lists stay usable meanwhile. Each list shows its folder either as
// rows or, as Eagle does, as a grid of pictures filtered by kind, category or tag (q turns one into the other); the
// keys are the same either way. What every file manager tab shares (the analyses, the work running, the cut or
// copied files, what can be undone, the folders visited) is on the App, in fileManager; each tab keeps its lists in
// a filesDoc.

// filesDoc is a file manager tab.
type filesDoc struct {
	panes   []*filesPane
	at      int // the pane the keys act on
	preview bool
	// previewFocus gives the keys to the preview, after a click in it, for playing a video or moving in a picture.
	previewFocus bool
	prompt       *filesPrompt
	confirm      *filesConfirm
	// mouseX, mouseY are where the mouse was at the last frame: a row is pointed at only when the mouse moves, so a
	// mouse resting over the list does not take the place the keys moved to.
	mouseX, mouseY float32
	mouseSeen      bool
	pageRows       int // how many rows a list showed at the last frame, for paging
	// after is what a list's button asked for, done once every list is drawn.
	after func()
	// hovered is the item under the pointer this frame, whose keys the list of keys shows; "" when there is none.
	hovered string
	// tagText is typed in the details' tag box; detailScroll and detailH scroll the details.
	tagText               string
	detailScroll, detailH float32
	// sweep is the right button marking items in a list while it is held.
	sweep *sweep
}

// filesPane is one folder list.
type filesPane struct {
	dir     string
	entries []files.Entry // as read, in the folder's order
	rows    []int         // the entries shown, sorted, as indexes into entries
	cursor  int           // the row the keys act on
	top     float32       // how far the list is scrolled, in rows
	marked  map[string]bool
	sort    filesSort
	desc    bool
	err     error
	reading bool
	gen     int    // counts listings asked for, so a slow one that comes back late is dropped
	land    string // the name to put the cursor on when the listing comes back
	// search is the name or content search shown in place of the listing, when there is one.
	search *filesSearch
	// analysed is set once the pane has been analysed: it is sorted by size as gdu sorts, until sorted otherwise.
	analysed bool
	// drives are the drives a list of them shows, by name, when the list is This PC: dir is then "".
	drives map[string]files.Drive
	// grid shows the folder as pictures rather than rows; g is how, kept while the list shows rows again.
	grid bool
	g    paneGrid
}

// thisPC is the list of the drives, as the folder above each drive's own: there is no such folder, so its dir is
// empty. Only Windows has it; elsewhere / is the top.
const thisPC = ""

// atDrives reports whether the list shows the drives rather than a folder.
func (p *filesPane) atDrives() bool { return p.dir == thisPC }

// noDrives refuses a command that works on a folder's files while the list shows the drives; it reports whether it
// refused.
func (a *App) noDrives(p *filesPane) bool {
	if !p.atDrives() {
		return false
	}
	a.toastOnly("files/drives", a.u.t("Go into a drive first: this list holds the drives, not files"))
	return true
}

type filesSort int

const (
	listByName filesSort = iota
	listBySize
	listByItems
	listByTime
)

// filesSettings are the file manager's choices kept between runs.
type filesSettings struct {
	HideHidden bool `json:"hideHidden,omitempty"` // hidden files are left out of the lists
	// Panes are the lists the file manager tab used last had, left to right, which a new one opens again.
	Panes []savedPane `json:"panes,omitempty"`
}

// savedPane is a list as it is kept between runs: its folder, and whether it shows pictures.
type savedPane struct {
	Dir  string `json:"dir"`
	Grid bool   `json:"grid,omitempty"`
}

// fileManager is what the file manager tabs share.
type fileManager struct {
	trees   []*files.Node // the folders analysed, none inside another
	scans   []*fileScan
	jobs    []*fileJob
	undo    []fileUndo
	clip    fileClip
	history *files.History
	// historyDirty is set when a visit was noted and not yet written.
	historyDirty bool
	// lastDirty is set when the lists shown changed and the settings, which remember them, are not yet written.
	lastDirty   bool
	historyAt   time.Time
	git         map[string]*gitState // by repository root
	repoOf      map[string]string    // a folder's repository root, or "" for none, as found
	wasInactive bool
}

// fileClip is what was cut or copied, to be pasted.
type fileClip struct {
	paths []string
	cut   bool
}

// fileUndo is an operation that can be undone, and how to say it; for items moved to the Recycle Bin, the tags and
// categories they had, given back with them.
type fileUndo struct {
	action files.Action
	title  string
	labels []assets.Item
}

// gitState is a repository's state as last read.
type gitState struct {
	status  files.GitStatus
	at      time.Time
	reading bool
}

// historyPath is where the folders visited are kept.
func (a *App) historyPath() string { return filepath.Join(a.stateDir, "folders.json") }

// fileHistory is the folders visited, read on first use.
func (a *App) fileHistory() *files.History {
	if a.fm.history == nil {
		a.fm.history = files.LoadHistory(a.historyPath())
	}
	return a.fm.history
}

// visited notes a folder in the history; it is written a moment later, with any other visits.
func (a *App) visited(dir string) {
	a.fileHistory().Add(dir, time.Now())
	a.fm.historyDirty = true
	a.fm.historyAt = time.Now()
}

// saveFileHistory writes the folders visited, and the settings that remember the folder last shown, when a visit
// is waiting and has settled, or at once when now is set: a crash or a power cut loses at most the last moment.
func (a *App) saveFileHistory(now bool) {
	if !a.fm.historyDirty && !a.fm.lastDirty || !now && time.Since(a.fm.historyAt) < 2*time.Second {
		return
	}
	if a.fm.lastDirty {
		a.fm.lastDirty = false
		a.saveSettings()
	}
	if !a.fm.historyDirty {
		return
	}
	a.fm.historyDirty = false
	if err := a.fm.history.Save(a.historyPath()); err != nil {
		a.toast(a.u.tf("The folders visited could not be saved: %v", err), true)
	}
}

// newFilesTab opens a file manager tab with one list of dir; grid shows it as pictures.
func (a *App) newFilesTab(dir string, grid bool) *filesDoc {
	return a.openFilesTab([]savedPane{{Dir: dir, Grid: grid}})
}

// openResourceManager opens a file manager tab with the lists the last one had, each at its folder and as rows or
// pictures as it was; the first time, the desktop as rows on the left and as pictures on the right.
func (a *App) openResourceManager() *filesDoc {
	panes := a.settings.Files.Panes
	if len(panes) == 0 {
		desktop := defaultFolder()
		panes = []savedPane{{Dir: desktop}, {Dir: desktop, Grid: true}}
	}
	return a.openFilesTab(panes)
}

// defaultFolder is where a list opens when it has nowhere else to: the desktop, else the home folder.
func defaultFolder() string {
	if dir := files.Desktop(); isFolder(dir) {
		return dir
	}
	home, _ := os.UserHomeDir()
	return home
}

// openFilesTab opens a file manager tab with the lists given, the empty tab in front becoming it when there is
// one. A folder that is no longer there opens as the default folder instead.
func (a *App) openFilesTab(saved []savedPane) *filesDoc {
	fd := &filesDoc{}
	for _, s := range saved[:min(len(saved), filesMaxPanes)] {
		dir := s.Dir
		switch {
		case dir == thisPC && len(files.Drives()) > 0:
		case !isFolder(dir):
			dir = defaultFolder()
		default:
			dir = filepath.Clean(dir)
		}
		p := &filesPane{dir: dir, marked: map[string]bool{}}
		if s.Grid {
			p.sort = listByTime // pictures newest first, as an asset library shows them
			a.showGrid(p)
		}
		a.readPane(p)
		fd.panes = append(fd.panes, p)
		if dir != thisPC {
			a.visited(dir)
		}
	}
	if a.doc != nil && !a.occupied() {
		a.files = fd // the empty tab becomes the file manager
	} else {
		a.docs = append(a.docs, &doc{files: fd})
		a.switchTab(len(a.docs) - 1)
	}
	return fd
}

// rememberPanes keeps the lists of the file manager tab in front, to open them again in the next one; they are
// written with the folders visited, once they settle.
func (a *App) rememberPanes(fd *filesDoc) {
	saved := make([]savedPane, len(fd.panes))
	for i, p := range fd.panes {
		saved[i] = savedPane{Dir: p.dir, Grid: p.grid}
	}
	if slices.Equal(saved, a.settings.Files.Panes) {
		return
	}
	a.settings.Files.Panes = saved
	a.fm.lastDirty, a.fm.historyAt = true, time.Now()
}

// openAssets shows dir (the export folder when empty) as pictures: in the file manager tab in front, else a new one.
func (a *App) openAssets(dir string) *filesPane {
	if dir == "" {
		d, err := a.uploadDir()
		if err != nil {
			a.toast(a.describe(err), true)
			return nil
		}
		dir = d
		os.MkdirAll(dir, 0o755) // the export folder exists before the first export
	}
	if a.files != nil {
		p := a.files.pane()
		if !p.grid {
			a.showGrid(p)
		}
		a.goTo(p, dir, "")
		return p
	}
	return a.newFilesTab(dir, true).pane()
}

// isFolder reports whether path is a folder.
func isFolder(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// openFolder shows a folder opened or dropped on the window: in the file manager tab in front, or in a new one.
func (a *App) openFolder(dir string) {
	if a.files != nil {
		a.goTo(a.files.pane(), dir, "")
		return
	}
	a.newFilesTab(dir, false)
}

// leaveFiles stops what a file manager tab's preview plays when the tab goes to the back or is closed, and when it is
// closed, its searches. The preview is shared with the asset manager, so it is let go only by a tab showing it.
func (a *App) leaveFiles(d *doc, closing bool) {
	if d == nil || d.files == nil {
		return
	}
	if d.files.preview {
		a.closeFilePreview()
	}
	a.saveFileHistory(true) // where it was, for the next file manager tab
	if closing {
		for _, p := range d.files.panes {
			a.closeSearch(p)
			p.gen++ // a listing still being read is dropped
		}
	}
}

// newPane is a list of dir, being read.
func (a *App) newPane(dir string) *filesPane {
	p := &filesPane{dir: filepath.Clean(dir), marked: map[string]bool{}}
	a.readPane(p)
	return p
}

// pane is the pane the keys act on.
func (fd *filesDoc) pane() *filesPane { return fd.panes[fd.at] }

// filesTitle is a file manager tab's title: the folder its pane in use shows.
func (fd *filesDoc) title(u *ctx) string {
	dir := fd.pane().dir
	if dir == thisPC {
		return u.t("This PC")
	}
	if base := filepath.Base(dir); base != string(filepath.Separator) && base != "." && filepath.VolumeName(dir) != dir {
		return base
	}
	return dir
}

// readPane lists the pane's folder in the background; the old listing stays on screen until the new one comes.
func (a *App) readPane(p *filesPane) {
	p.gen++
	gen, dir := p.gen, p.dir
	p.reading = true
	peek := a.dimsPeeker(p)
	a.background(func(ctx context.Context) {
		var entries []files.Entry
		var drives map[string]files.Drive
		var err error
		if dir == thisPC {
			drives = map[string]files.Drive{}
			for _, d := range files.Drives() {
				drives[d.Name] = d
				entries = append(entries, files.Entry{Name: d.Name, Dir: true, Size: d.Total})
			}
		} else {
			entries, err = files.List(dir)
		}
		dims := peek(ctx, dir, entries)
		a.post(func() {
			if p.gen != gen {
				return
			}
			p.reading, p.err, p.drives = false, err, drives
			if dims != nil {
				p.g.dims, p.g.layoutKey = dims, ""
			}
			if err != nil {
				entries = nil
			}
			name := p.land
			if name == "" {
				if e := p.current(); e != nil {
					name = e.Name
				}
			}
			p.entries, p.rows, p.land = entries, nil, "" // the rows index the old entries: arrange makes them anew
			for n := range p.marked {
				if !slices.ContainsFunc(entries, func(e files.Entry) bool { return e.Name == n }) {
					delete(p.marked, n)
				}
			}
			a.arrange(p)
			p.cursorTo(name)
		})
	})
}

// refreshPanes lists again every pane, in every tab, that shows one of dirs, or all of them when dirs is nil.
func (a *App) refreshPanes(dirs []string) {
	for _, d := range a.docs {
		if d.files == nil {
			continue
		}
		for _, p := range d.files.panes {
			if dirs == nil || slices.ContainsFunc(dirs, func(dir string) bool { return files.SamePath(dir, p.dir) }) {
				a.readPane(p)
			}
		}
	}
}

// arrange sorts the pane's entries and leaves out the hidden ones when they are not shown, keeping the cursor on
// the same entry.
func (a *App) arrange(p *filesPane) {
	name := ""
	if e := p.current(); e != nil {
		name = e.Name
	}
	node := a.paneAnalysis(p)
	p.rows = p.rows[:0]
	for i, e := range p.entries {
		if a.settings.Files.HideHidden && e.Hidden {
			continue
		}
		if p.grid && !a.passes(p, &e) {
			continue
		}
		p.rows = append(p.rows, i)
	}
	p.g.arranged = a.filterKey(p)
	size := func(e files.Entry) int64 {
		if e.Dir && node != nil {
			if c := node.Child(e.Name); c != nil {
				return c.Size
			}
		}
		return e.Size
	}
	items := func(e files.Entry) int64 {
		if e.Dir && node != nil {
			if c := node.Child(e.Name); c != nil {
				return c.Items
			}
		}
		return 0
	}
	slices.SortStableFunc(p.rows, func(i, j int) int {
		x, y := p.entries[i], p.entries[j]
		if p.grid && x.Dir != y.Dir { // pictures show folders first in any order, as an asset library does
			if x.Dir {
				return -1
			}
			return 1
		}
		var c int
		switch p.sort {
		case listBySize:
			c = cmp.Compare(size(y), size(x))
		case listByItems:
			c = cmp.Compare(items(y), items(x))
		case listByTime:
			c = y.Mod.Compare(x.Mod)
		}
		if p.desc {
			c = -c
		}
		if c != 0 {
			return c
		}
		if x.Dir != y.Dir { // folders before files, as when sorted by name
			if x.Dir {
				return -1
			}
			return 1
		}
		c = naturalCompare(x.Name, y.Name)
		if p.sort == listByName && p.desc {
			c = -c
		}
		return c
	})
	p.cursorTo(name)
}

// current is the entry under the cursor, or nil in an empty folder.
func (p *filesPane) current() *files.Entry {
	if p.cursor < 0 || p.cursor >= len(p.rows) {
		return nil
	}
	return &p.entries[p.rows[p.cursor]]
}

// path is where an entry is.
func (p *filesPane) path(e *files.Entry) string {
	if p.atDrives() {
		return e.Name + string(filepath.Separator)
	}
	return filepath.Join(p.dir, e.Name)
}

// cursorTo puts the cursor on the entry with that name, or keeps it where it was when there is none.
func (p *filesPane) cursorTo(name string) {
	for i, r := range p.rows {
		if p.entries[r].Name == name {
			p.cursor = i
			return
		}
	}
	p.cursor = min(max(p.cursor, 0), max(len(p.rows)-1, 0))
}

// selected are the paths the commands act on: the marked entries, or the one under the cursor.
func (p *filesPane) selected() []string {
	var out []string
	for _, r := range p.rows {
		if e := &p.entries[r]; p.marked[e.Name] {
			out = append(out, p.path(e))
		}
	}
	if len(out) == 0 {
		if e := p.current(); e != nil {
			out = append(out, p.path(e))
		}
	}
	return out
}

// go to dir in the pane, the cursor on land when it is one of its entries.
func (a *App) goTo(p *filesPane, dir, land string) {
	if dir != thisPC {
		dir = filepath.Clean(dir)
	}
	if job := a.busyJob(dir); job != nil {
		a.toastOnly("files/busy", a.u.tf("%s is being worked on: %s", filepath.Base(dir), job.title))
		return
	}
	if !files.SamePath(dir, p.dir) {
		p.marked = map[string]bool{}
		// A list showing an analysis keeps showing it in the folders inside, as gdu does; leaving it, the list
		// goes back to names.
		p.analysed = p.analysed && dir != thisPC && a.analysis(dir) != nil
		p.top, p.cursor, p.entries, p.rows = 0, 0, nil, nil
		if p.sort == listBySize && !p.analysed {
			p.sort, p.desc = listByName, false
		}
		if dir != thisPC {
			a.visited(dir)
		}
	}
	p.dir, p.land, p.search, p.err = dir, land, nil, nil
	a.readPane(p)
}

// analysis is the analysed folder for dir, or nil when it has not been analysed.
func (a *App) analysis(dir string) *files.Node {
	for _, t := range a.fm.trees {
		if n := t.Find(dir); n != nil {
			return n
		}
	}
	return nil
}
