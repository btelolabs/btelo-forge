package ui

import (
	"slices"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/assets"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/library"
	"github.com/btelolabs/btelo-forge/internal/paint"
	"github.com/btelolabs/btelo-forge/internal/project"
)

// The kinds of file the app can be set to open from Explorer, chosen in Settings → File types. Windows keeps the
// choice of a common type's default program to the user, so turning a kind on makes the app one of its programs
// (in "Open with" and in Settings → Default apps) and the page opens Windows' own choice; the app's canvases and
// edits, which no other program opens, it opens by default at once.

// FileType is a kind of file the app declares it opens: its ProgID, the name the system shows, its extensions,
// and whether it is the app's own format.
type FileType struct {
	ProgID string
	Name   string
	Exts   []string
	Own    bool
}

// fileTypesHost is what a window that can declare file associations offers.
type fileTypesHost interface {
	RegisterFileTypes(types []FileType) error
	UnregisterFileTypes(types []FileType) error
	FileTypeRegistered(t FileType) bool
	// FileTypeDefaults counts the extensions of t whose default program the app is.
	FileTypeDefaults(t FileType) int
	OpenDefaultApps() error
}

// fileTypeGroup is a row of the File types page: one or more file types turned on and off together.
type fileTypeGroup struct {
	id, name string
	types    []FileType
}

// fileTypeGroups are the kinds of file offered, the app's own first.
func fileTypeGroups() []fileTypeGroup {
	return []fileTypeGroup{
		{"own", "Btelo Forge canvases and edits", []FileType{
			{ProgID: "BteloForge.Canvas", Name: ProductName + " canvas", Exts: []string{paint.Ext}, Own: true},
			{ProgID: "BteloForge.Edit", Name: ProductName + " edit", Exts: []string{project.EditExt}, Own: true},
		}},
		{"video", "Videos", []FileType{{ProgID: "BteloForge.Video", Name: "Video", Exts: assets.VideoExtensions}}},
		{"picture", "Pictures", []FileType{{ProgID: "BteloForge.Picture", Name: "Picture", Exts: []string{".png", ".jpg", ".jpeg", ".webp", ".bmp", ".tif", ".tiff"}}}},
		{"animation", "GIF animations", []FileType{{ProgID: "BteloForge.Animation", Name: "Animation", Exts: assets.AnimationExtensions}}},
		{"sound", "Sounds", []FileType{{ProgID: "BteloForge.Sound", Name: "Sound", Exts: library.AudioExtensions}}},
		{"text", "Markdown and text", []FileType{{ProgID: "BteloForge.Text", Name: "Text", Exts: append(slices.Clone(assets.MarkdownExtensions), ".txt", ".log", ".csv")}}},
	}
}

var fileTypeGroupNames = msgs("Btelo Forge canvases and edits", "Videos", "Pictures", "GIF animations", "Sounds", "Markdown and text")

// fileTypes is the window's file associations, or nil where it has none.
func (a *App) fileTypes() fileTypesHost {
	h, _ := a.host.(fileTypesHost)
	return h
}

// setFileTypes turns a group of file types on or off, and remembers it.
func (a *App) setFileTypes(g fileTypeGroup, on bool) {
	h := a.fileTypes()
	if h == nil {
		return
	}
	var err error
	if on {
		err = h.RegisterFileTypes(g.types)
	} else {
		err = h.UnregisterFileTypes(g.types)
	}
	if err != nil {
		a.toast(a.u.tf("The file types could not be changed: %s", a.describe(err)), true)
		return
	}
	a.settings.FileTypes = slices.DeleteFunc(a.settings.FileTypes, func(id string) bool { return id == g.id })
	if on {
		a.settings.FileTypes = append(a.settings.FileTypes, g.id)
	}
	a.saveSettings()
}

// refreshFileTypes declares again the file types turned on whose declaration no longer names this program, as
// after the app was moved or updated to another folder.
func (a *App) refreshFileTypes() {
	h := a.fileTypes()
	if h == nil {
		return
	}
	for _, g := range fileTypeGroups() {
		if !slices.Contains(a.settings.FileTypes, g.id) {
			continue
		}
		if slices.ContainsFunc(g.types, func(t FileType) bool { return !h.FileTypeRegistered(t) }) {
			if err := h.RegisterFileTypes(g.types); err != nil {
				a.toast(a.u.tf("The file types could not be changed: %s", a.describe(err)), true)
				return
			}
		}
	}
}

// settingsFileTypes is the File types page.
func (a *App) settingsFileTypes(r gfx.Rect) {
	u := &a.u
	h := a.fileTypes()
	y := a.paragraph(r.X, r.Y, r.W, u.tf("Choose the kinds of file to open with %s from Explorer. A video, canvas, edit or picture opens in a tab of its own; a sound or a text opens in the resource manager, at its folder, previewed. Files opened while the app runs join its window.", ProductName), 12.5, colDim) + u.px(14)
	if h == nil {
		a.paragraph(r.X, y, r.W, u.tf("This system chooses the programs for file types itself; open a file with %s from its menu.", ProductName), 12.5, colFaint)
		return
	}
	for _, g := range fileTypeGroups() {
		on := slices.Contains(a.settings.FileTypes, g.id)
		var exts []string
		for _, t := range g.types {
			exts = append(exts, t.Exts...)
		}
		row := gfx.Rect{X: r.X, Y: y, W: r.W, H: u.px(26)}
		if pick := u.checkbox("settings/types/"+g.id, row, u.t(g.name), on); pick != on {
			a.setFileTypes(g, pick)
			on = pick
		}
		y += u.px(26)
		y = a.paragraph(r.X+u.px(30), y, r.W-u.px(30), strings.Join(exts, "  "), 11, colFaint)
		if on {
			defaults, total := 0, 0
			for _, t := range g.types {
				defaults += h.FileTypeDefaults(t)
				total += len(t.Exts)
			}
			status, col := u.tf("Opens with %s by default", ProductName), colSuccess
			switch {
			case defaults == 0:
				status, col = u.t("Listed under Open with; make it the default in Windows Settings"), colWarning
			case defaults < total:
				status, col = u.tf("The default for %d of %d; choose the rest in Windows Settings", defaults, total), colWarning
			}
			y = a.paragraph(r.X+u.px(30), y+u.px(2), r.W-u.px(30), status, 11.5, col)
		}
		y += u.px(12)
	}
	y += u.px(4)
	label := u.t("Choose the defaults in Windows Settings…")
	if u.button("settings/types/windows", gfx.Rect{X: r.X, Y: y, W: u.buttonWidth(icLaunch, label), H: u.px(34)}, icLaunch, label, plain, len(a.settings.FileTypes) > 0) {
		if err := h.OpenDefaultApps(); err != nil {
			a.toast(a.describe(err), true)
		}
	}
}
