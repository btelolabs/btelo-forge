package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/assets"
	"github.com/btelolabs/btelo-forge/internal/files"
)

// A file manager list shown as a grid browses its folder as Eagle does: its pictures, videos, sounds, canvases,
// edits and texts as small pictures, filtered by kind, category or tag and searched by name or tag. Tags and
// categories belong to the files wherever they are browsed from (assets.Library), and follow them when the file
// manager renames or moves them.

// assetFilter is what a grid shows.
type assetFilter struct {
	what filterWhat
	kind assets.Kind
	name string // a category or tag
	// folders lets the folders through as well, to go into, as the video editor's list of videos does.
	folders bool
}

type filterWhat int

const (
	showAll filterWhat = iota
	showKind
	showUntagged
	showUncategorized
	showCategory
	showTag
)

// kindNames name each kind in assets.Kinds' order.
var kindNames = msgs("Pictures", "Animations", "Videos", "Sounds", "Canvases", "Edits", "Mind maps", "Office documents", "3D models", "Markdown", "Code", "Texts", "Other files")

// assetLibrary is the tags and categories, opened the first time they are needed.
func (a *App) assetLibrary() *assets.Library {
	if a.assetLib == nil {
		path := filepath.Join(a.stateDir, "assets.json")
		lib, err := assets.OpenLibrary(path)
		if err != nil {
			// A damaged file is kept aside, not written over, so what it held can still be recovered.
			aside := path + ".damaged-" + time.Now().Format("20060102-150405")
			os.Rename(path, aside)
			a.toast(a.u.tf("The tags and categories could not be read (%s); the file was kept as %s", err, filepath.Base(aside)), true)
			lib, _ = assets.OpenLibrary(path)
		}
		a.assetLib = lib
	}
	return a.assetLib
}

// assetEntry is a list's entry as the asset library and the pictures know it.
func assetEntry(p *filesPane, e *files.Entry) assets.Entry {
	kind := assets.KindOf(e.Name)
	if e.Dir {
		kind = assets.Folder
	}
	return assets.Entry{Path: p.path(e), Name: e.Name, Kind: kind, Size: e.Size, Mod: e.Mod}
}

// passes reports whether an entry passes a grid's filter and search.
func (a *App) passes(p *filesPane, fe *files.Entry) bool {
	e := assetEntry(p, fe)
	if q := strings.TrimSpace(p.g.search); q != "" {
		it := a.assetLibrary().Of(e.Path)
		hay := strings.ToLower(e.Name + " " + strings.Join(it.Tags, " ") + " " + strings.Join(it.Categories, " "))
		for _, w := range strings.Fields(strings.ToLower(q)) {
			if !strings.Contains(hay, w) {
				return false
			}
		}
	}
	f := p.g.filter
	if f.what == showAll {
		return true
	}
	if e.Kind == assets.Folder {
		return f.folders // a filter shows files
	}
	it := a.assetLibrary().Of(e.Path)
	switch f.what {
	case showKind:
		if f.kind == assets.Animation {
			return e.Kind == assets.Animation || a.animated(e)
		}
		return e.Kind == f.kind
	case showUntagged:
		return len(it.Tags) == 0
	case showUncategorized:
		return len(it.Categories) == 0
	case showCategory:
		return containsFold(it.Categories, f.name)
	case showTag:
		return containsFold(it.Tags, f.name)
	}
	return true
}

// containsFold reports whether list has s, ignoring case.
func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

// animated reports whether a picture is known to move, such as an animated WebP.
func (a *App) animated(e assets.Entry) bool {
	if it := a.fileThumbs.items[e.Path]; it != nil && it.ready {
		return it.info.Animated
	}
	return false
}

// naturalCompare orders names as people count: "2.png" before "10.png", ignoring case.
func naturalCompare(a, b string) int {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for a != "" && b != "" {
		da, db := digitsPrefix(a), digitsPrefix(b)
		if da != "" && db != "" {
			na, nb := strings.TrimLeft(da, "0"), strings.TrimLeft(db, "0")
			if len(na) != len(nb) {
				return len(na) - len(nb)
			}
			if c := strings.Compare(na, nb); c != 0 {
				return c
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		if a[0] != b[0] {
			return int(a[0]) - int(b[0])
		}
		a, b = a[1:], b[1:]
	}
	return len(a) - len(b)
}

func digitsPrefix(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

// opensInApp reports whether a file of the kind opens in a tab of the app rather than in the system's program.
func opensInApp(kind assets.Kind) bool {
	switch kind {
	case assets.Video, assets.Edit, assets.Canvas, assets.Image, assets.Animation, assets.MindMap, assets.Office, assets.Model:
		return true
	}
	return false
}

// openAsset opens a file where it belongs: a video, edit or canvas in a tab of its own, a picture as a canvas,
// anything else with the program the system has for it.
func (a *App) openAsset(path string) {
	if isPSD(path) {
		a.openPSD(path)
		return
	}
	switch assets.KindOf(path) {
	case assets.Video:
		a.newTab()
		a.openVideo(path, false)
	case assets.Edit:
		a.openEditFile(path)
	case assets.Canvas:
		a.openCanvasFile(path)
	case assets.MindMap:
		a.openMindFile(path)
	case assets.Office:
		a.openOfficeFile(path)
	case assets.Model:
		a.openModelFile(path)
	case assets.Image, assets.Animation:
		a.newTab()
		a.openImageAsArt(path)
	default:
		if err := a.host.Launch(path); err != nil {
			a.toast(a.describe(err), true)
		}
	}
}

// fileOps are the file operations of the window the app runs in, where it has them.
type fileOps interface {
	SetClipboardFiles(paths []string) error
	Trash(paths []string) error
}

// errNoFileOps means the window cannot copy files or move them to the Recycle Bin.
var errNoFileOps = errors.New("this system does not offer that here")

func (a *App) fileOps() (fileOps, error) {
	if ops, ok := a.host.(fileOps); ok {
		return ops, nil
	}
	return nil, errNoFileOps
}

// countFiles names the files: the one's name, or how many.
func (a *App) countFiles(paths []string) string {
	if len(paths) == 1 {
		return filepath.Base(paths[0])
	}
	return a.u.tf("%d files", len(paths))
}

// tagAssets adds a tag to the files.
func (a *App) tagAssets(paths []string, tag string) error {
	if len(paths) == 0 {
		return nil
	}
	if err := a.assetLibrary().AddTag(paths, tag); err != nil {
		return err
	}
	a.assetLibVersion++
	return nil
}

// addTag tags the files, saying so when it fails.
func (a *App) addTag(paths []string, name string) {
	if err := a.tagAssets(paths, name); err != nil {
		a.toast(a.describe(err), true)
		return
	}
	a.toastOnly("files/tag", a.u.tf("Tagged %s with %s", a.countFiles(paths), strings.Join(strings.Fields(name), " ")))
}

// setCategory puts the files in a category or takes them out.
func (a *App) setCategory(paths []string, name string, in bool) {
	if err := a.assetLibrary().SetCategory(paths, name, in); err != nil {
		a.toast(a.describe(err), true)
		return
	}
	a.assetLibVersion++
	if in {
		a.toast(a.u.tf("Added %s to %s", a.countFiles(paths), name), false)
	}
}

// openFromSystem opens files opened from outside the app: a folder in the file manager as pictures; a video, edit,
// canvas or picture in a tab of its own; a sound, text or any other file in the file manager, at its folder, with
// its preview open.
func (a *App) openFromSystem(paths []string) {
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			a.toast(a.u.tf("Cannot open %s: %s", filepath.Base(p), a.describe(err)), true)
			continue
		}
		if st.IsDir() {
			a.openAssets(p)
			continue
		}
		if isPSD(p) {
			a.openPSD(p)
			continue
		}
		switch assets.KindOf(p) {
		case assets.Video:
			if a.occupied() {
				a.newTab()
			}
			a.openVideo(p, false)
		case assets.Edit:
			a.openEditFile(p)
		case assets.Canvas:
			a.openCanvasFile(p)
		case assets.MindMap:
			a.openMindFile(p)
		case assets.Office:
			a.openOfficeFile(p)
		case assets.Model:
			a.openModelFile(p)
		case assets.Image, assets.Animation:
			a.openImageAsArt(p)
		default:
			if pane := a.openAssets(filepath.Dir(p)); pane != nil {
				pane.land = filepath.Base(p)
				a.files.preview = true
			}
		}
	}
}
