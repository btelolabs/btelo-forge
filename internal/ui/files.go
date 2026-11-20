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
	// hoveredDir says it is a folder, as its listing read.
	hovered    string
	hoveredDir bool
	// tagText is typed in the preview's tag box; detailScroll and detailH scroll the details.
	tagText               string
	detailScroll, detailH float32
	// sweep is the right button marking items in a list while it is held.
	sweep *sweep
	// pick is the dialog this list is in, when it is the file picker's rather than a tab's.
	pick *filePick
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
	sorted  bool // set once the user chooses an order, so switching between rows and pictures keeps it
	err     error
	reading bool
	gen     int // counts listings asked for, so a slow one that comes back late is dropped
	// orElse are the folders the list shows instead, the first that can be read, when dir cannot: a folder kept from
	// the last run may be gone. They are tried in the background and forgotten once one is shown.
	orElse []string
	land   string // the name to put the cursor on when the listing comes back
	// enterLand opens land once the cursor is on it, as Enter on it does: a path typed or pasted to a file opens it.
	enterLand bool
	// search is the name or content search shown in place of the listing, when there is one.
	search *filesSearch
	// analysed is set once the pane has been analysed: it is sorted by size as gdu sorts, until sorted otherwise.
	analysed bool
	// analyses is set by i and cleared by i again: the list shows the analysis kept of any folder it goes to, so
	// going back into a folder analysed shows it at once.
	analyses bool
	// drives are the drives a list of them shows, by name, when the list is This PC: dir is then "".
	drives map[string]files.Drive
	// grid shows the folder as pictures rather than rows; g is how, kept while the list shows rows again.
	grid bool
	g    paneGrid
	// pie turns the pie under an analysed list to the item under the cursor.
	pie pieMotion
	// only says which files the list shows, for the file picker; nil shows them all.
	only func(name string) bool
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

// defaultSort is the order a list takes until sorted otherwise: pictures newest first, rows by name.
func (p *filesPane) defaultSort() filesSort {
	if p.grid {
		return listByTime
	}
	return listByName
}

// filesSettings are the file manager's choices kept between runs.
type filesSettings struct {
	HideHidden bool `json:"hideHidden,omitempty"` // hidden files are left out of the lists
	// Panes are the lists the file manager tab used last had, left to right, which a new one opens again.
	Panes []savedPane `json:"panes,omitempty"`
	// ResourcesOpened says a file manager has opened the resource folder as it is now; until one has, it opens there.
	ResourcesOpened bool `json:"resourcesOpened,omitempty"`
	// VideoDir is the folder the video editor's list of videos showed last, which it opens in again.
	VideoDir string `json:"videoDir,omitempty"`
	// PickDirs are the folders the file picker showed last, by the kind of file it was asked for.
	PickDirs map[string]string `json:"pickDirs,omitempty"`
}

// savedPane is a list as it is kept between runs: its folder, and whether it shows pictures.
type savedPane struct {
	Dir  string `json:"dir"`
	Grid bool   `json:"grid,omitempty"`
}

// fileManager is what the file manager tabs share.
type fileManager struct {
	peek filePeek
	// trees are the folders analysed, none inside another: those of earlier runs, read as the app starts
	// (treesRead, analysisTrees), and this run's. They are written a moment after they change (saveAnalyses).
	trees       []*files.Node
	treesRead   loadAhead[[]*files.Node]
	treesLoaded bool
	treesDirty  bool
	treesAt     time.Time
	treesSaver  analysesSaver
	// since is when this run started: an analysis older is of an earlier run, and is read again in the background
	// when a list shows it, once per folder (refreshed).
	since     time.Time
	refreshed map[string]bool
	scans     []*fileScan
	jobs      []*fileJob
	finding   int // paths typed or picked being looked for, to go to
	undo      []fileUndo
	clip      fileClip
	history   *files.History
	// historyRead is the history being read as the app starts.
	historyRead loadAhead[*files.History]
	// historyDirty is set when a visit was noted and not yet written.
	historyDirty bool
	// lastDirty is set when the lists shown changed and the settings, which remember them, are not yet written.
	lastDirty   bool
	historyAt   time.Time
	git         map[string]*gitState // by repository root
	wasInactive bool
	// clock is the time the pies move by, when a test steps it; nil is the time now.
	clock func() time.Time
	// scan analyses a folder, when a test holds it running; nil is files.Scan.
	scan func(ctx context.Context, root string, progress *files.ScanProgress) (*files.Node, error)
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

// fileHistory is the folders visited, read in the background as the app starts.
func (a *App) fileHistory() *files.History {
	if a.fm.history == nil {
		a.fm.history = a.fm.historyRead.get(a.loadFileHistory)
	}
	return a.fm.history
}

func (a *App) loadFileHistory() *files.History { return files.LoadHistory(a.historyPath()) }

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
// pictures as it was; the first time, and after the resource folder moves, that folder as rows on the left and as
// pictures on the right.
func (a *App) openResourceManager() *filesDoc {
	root, err := a.ensureResourceFolders()
	if err != nil {
		a.toast(a.u.tf("The resource folder could not be made: %s", a.describe(err)), true)
	}
	panes := a.settings.Files.Panes
	if !a.settings.Files.ResourcesOpened || len(panes) == 0 {
		panes = []savedPane{{Dir: root}, {Dir: root, Grid: true}}
		a.settings.Files.ResourcesOpened = true
		a.saveSettings()
	}
	return a.openFilesTab(panes)
}

// defaultFolders are where a list opens when it has nowhere else to, the first that is there: the resource folder,
// else the desktop, else the home folder.
func (a *App) defaultFolders() []string {
	var dirs []string
	if dir, err := a.resourceDir(); err == nil {
		dirs = append(dirs, dir)
	}
	dirs = append(dirs, files.Desktop())
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, home)
	}
	return dirs
}

// openFilesTab opens a file manager tab with the lists given, the empty tab in front becoming it when there is
// one. A folder that is no longer there opens as the default folder instead, which the listing finds out.
func (a *App) openFilesTab(saved []savedPane) *filesDoc {
	fd := &filesDoc{}
	for _, s := range saved[:min(len(saved), filesMaxPanes)] {
		dir := s.Dir
		if dir != thisPC {
			dir = filepath.Clean(dir)
		}
		p := &filesPane{dir: dir, marked: map[string]bool{}, orElse: a.defaultFolders()}
		if s.Grid {
			p.sort = listByTime // pictures newest first, as an asset library shows them
			a.showGrid(p)
		}
		a.readPane(p)
		fd.panes = append(fd.panes, p)
	}
	if a.doc != nil && !a.occupied() {
		a.files = fd // the empty tab becomes the file manager
	} else {
		a.docs = append(a.docs, &doc{files: fd})
		a.switchTab(len(a.docs) - 1)
	}
	return fd
}

// savedPanes are the tab's lists as they are kept between runs.
func (fd *filesDoc) savedPanes() []savedPane {
	saved := make([]savedPane, len(fd.panes))
	for i, p := range fd.panes {
		saved[i] = savedPane{Dir: p.dir, Grid: p.grid}
	}
	return saved
}

// rememberPanes keeps the lists of the file manager tab in front, to open them again in the next one; they are
// written with the folders visited, once they settle.
func (a *App) rememberPanes(fd *filesDoc) {
	saved := fd.savedPanes()
	if slices.Equal(saved, a.settings.Files.Panes) {
		return
	}
	a.settings.Files.Panes = saved
	a.fm.lastDirty, a.fm.historyAt = true, time.Now()
}

// openAssets shows dir (the resource folder when empty) as pictures: in the file manager tab in front, else a new one.
func (a *App) openAssets(dir string) *filesPane {
	if dir == "" {
		d, err := a.ensureResourceFolders()
		if err != nil {
			a.toast(a.describe(err), true)
			return nil
		}
		dir = d
	}
	if a.files != nil {
		p := a.files.pane()
		if !p.grid {
			a.showGrid(p)
			p.cursor, p.g.scroll = 0, 0
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
	a.endPeek()
	a.stopModelCard()
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

// readPane lists the pane's folder in the background; the old listing stays on screen until the new one comes. A
// pane with folders to fall back on shows the first of them that can be read when its own cannot, and notes it as
// visited once it is shown.
func (a *App) readPane(p *filesPane) {
	p.gen++
	gen, dir, orElse := p.gen, p.dir, p.orElse
	p.reading = true
	peek := a.dimsPeeker(p)
	a.background(func(ctx context.Context) {
		dir, entries, drives, err := listFolder(dir, orElse)
		dims := peek(ctx, dir, entries)
		a.post(func() {
			if p.gen != gen {
				return
			}
			p.reading, p.err, p.drives = false, err, drives
			if orElse != nil {
				p.dir, p.orElse = dir, nil
				if dir != thisPC {
					a.visited(dir)
				}
			}
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
			if p.enterLand {
				p.enterLand = false
				a.enterLanded(p, name)
			}
		})
	})
}

// listFolder reads dir, or the drives when it is thisPC; when that fails and there are folders to fall back on, the
// first of them that can be read. It says which it read.
func listFolder(dir string, orElse []string) (string, []files.Entry, map[string]files.Drive, error) {
	var err error
	for i, d := range append([]string{dir}, orElse...) {
		if d == thisPC {
			drives := files.Drives()
			if len(drives) == 0 && i < len(orElse) {
				continue // only Windows has the drives above the folders
			}
			byName := map[string]files.Drive{}
			var entries []files.Entry
			for _, d := range drives {
				byName[d.Name] = d
				entries = append(entries, files.Entry{Name: d.Name, Dir: true, Size: d.Total})
			}
			return thisPC, entries, byName, nil
		}
		var entries []files.Entry
		if entries, err = files.List(d); err == nil || i == len(orElse) {
			return d, entries, nil, err
		}
	}
	return dir, nil, nil, err
}

// refreshPanes lists again every pane, in every tab, that shows one of dirs, or all of them when dirs is nil.
func (a *App) refreshPanes(dirs []string) {
	a.forgetFacts(dirs)
	for _, fd := range a.filesDocs() {
		for _, p := range fd.panes {
			if dirs == nil || slices.ContainsFunc(dirs, func(dir string) bool { return files.SamePath(dir, p.dir) }) {
				a.readPane(p)
			}
		}
	}
	if p := a.videoList; p != nil && (dirs == nil || slices.ContainsFunc(dirs, func(dir string) bool { return files.SamePath(dir, p.dir) })) {
		a.readPane(p)
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
		if a.settings.Files.HideHidden && e.Hidden || p.leavesOut(&e) {
			continue
		}
		if p.grid && !a.passes(p, &e) {
			continue
		}
		p.rows = append(p.rows, i)
	}
	p.g.arranged = a.filterKey(p)
	// What an analysis found of each folder shown is looked up once, not at every comparison.
	var size, items []int64
	if node != nil && (p.sort == listBySize || p.sort == listByItems) {
		size, items = make([]int64, len(p.entries)), make([]int64, len(p.entries))
		for _, r := range p.rows {
			e := &p.entries[r]
			size[r] = e.Size
			if e.Dir {
				if c := node.Child(e.Name); c != nil {
					size[r], items[r] = c.Size, c.Items
				}
			}
		}
	}
	slices.SortStableFunc(p.rows, func(i, j int) int {
		x, y := &p.entries[i], &p.entries[j]
		if p.grid && x.Dir != y.Dir { // pictures show folders first in any order, as an asset library does
			if x.Dir {
				return -1
			}
			return 1
		}
		var c int
		switch p.sort {
		case listBySize:
			if size != nil {
				c = cmp.Compare(size[j], size[i])
			} else {
				c = cmp.Compare(y.Size, x.Size)
			}
		case listByItems:
			if items != nil {
				c = cmp.Compare(items[j], items[i])
			}
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

// leavesOut reports whether the list does not offer a file, being the file picker's for files of some kinds.
func (p *filesPane) leavesOut(e *files.Entry) bool { return p.only != nil && !e.Dir && !p.only(e.Name) }

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
	es := p.selectedEntries()
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = p.path(e)
	}
	return out
}

// selectedEntries are the entries the commands act on, as the listing read them: the marked ones, or the one under
// the cursor.
func (p *filesPane) selectedEntries() []*files.Entry {
	var out []*files.Entry
	if len(p.marked) > 0 {
		for _, r := range p.rows {
			if e := &p.entries[r]; p.marked[e.Name] {
				out = append(out, e)
			}
		}
	}
	if len(out) == 0 {
		if e := p.current(); e != nil {
			out = append(out, e)
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
		// goes back to names, and coming back into a folder analysed, to its analysis, as it was kept.
		was := p.analysed
		p.analysed = p.analyses && dir != thisPC && (a.analysis(dir) != nil || a.liveFor(p, dir) != nil)
		if p.analysed && !was {
			p.sort, p.desc = listBySize, false
		}
		p.top, p.cursor, p.entries, p.rows = 0, 0, nil, nil
		if was && !p.analysed && p.sort == listBySize {
			p.sort, p.desc, p.sorted = p.defaultSort(), false, false
		}
		if dir != thisPC {
			a.visited(dir)
		}
	}
	p.dir, p.land, p.enterLand, p.search, p.err = dir, land, false, nil, nil
	a.readPane(p)
}

// enterLanded does what Enter on the item named name does, now that the listing put the cursor on it, in the list
// whose keys act: a file opens in the app or is previewed, and in the file picker it is chosen.
func (a *App) enterLanded(p *filesPane, name string) {
	e := p.current()
	if e == nil || e.Name != name || e.Dir {
		return // not shown, as a hidden file is while hidden files are
	}
	for _, fd := range a.filesDocs() {
		if fd.pane() == p {
			a.enter(fd)
			return
		}
	}
}

// analysis is the analysed folder for dir, or nil when it has not been analysed.
func (a *App) analysis(dir string) *files.Node {
	for _, t := range a.analysisTrees() {
		if n := t.Find(dir); n != nil {
			return n
		}
	}
	return nil
}
