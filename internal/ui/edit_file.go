package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/project"
)

// An edit is remembered by the video it starts from, so opening that video again brings it back. Saving it as a
// file (Ctrl+S) keeps it under a name of its own, beside the exports, to open again, share or keep several
// versions of: a ".btcut" file holds the whole edit and points to its videos where they are.

// isEditFile reports whether path is an edit file.
func isEditFile(path string) bool { return strings.EqualFold(filepath.Ext(path), project.EditExt) }

// editFilePath is where an edit of source is saved the first time: in the export folder, named after the video,
// numbered when that name is taken.
func (a *App) editFilePath(source string) (string, error) {
	dir, err := a.uploadDir()
	if err != nil {
		return "", err
	}
	base := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	path := filepath.Join(dir, base+project.EditExt)
	for i := 2; ; i++ {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
		path = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, project.EditExt))
	}
}

// saveEditFile writes the active edit to its file, choosing one the first time, and returns where.
func (a *App) saveEditFile() (string, error) {
	if a.p == nil {
		return "", errNoProject
	}
	path := a.doc.file
	if path == "" {
		var err error
		if path, err = a.editFilePath(a.p.Sources[0].Path); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := project.WriteFile(path, a.p); err != nil {
		return "", err
	}
	a.doc.file = path
	return path, nil
}

// saveEditCommand saves the edit as a file and says where.
func (a *App) saveEditCommand() {
	path, err := a.saveEditFile()
	if err != nil {
		a.toast(a.u.tf("The edit could not be saved: %s", a.describe(err)), true)
		return
	}
	a.toastActions(a.u.tf("Saved the edit as %s", filepath.Base(path)), false, []toastAction{
		{a.u.t("Show in folder"), func() { a.host.Reveal(path) }},
	})
}

// openEditFile opens an edit file in a tab of its own, or brings forward the tab that has it open. Its first video
// is probed, as opening that video would be, to be sure it is still the one the edit was made from.
func (a *App) openEditFile(path string) {
	for i, d := range a.docs {
		if d.file != "" && filepath.Clean(d.file) == filepath.Clean(path) {
			a.switchTab(i)
			return
		}
	}
	if !a.tools.ready {
		a.tools.waiting = append(a.tools.waiting, path)
		a.toast(a.u.tf("%s opens as soon as ffmpeg is ready", filepath.Base(path)), false)
		return
	}
	if a.occupied() {
		a.newTab()
	}
	d := a.doc
	d.opening = path
	a.background(func(ctx context.Context) {
		p, err := project.ReadFile(path)
		if err == nil {
			err = checkEditVideos(ctx, p)
		}
		a.post(func() {
			d.opening = ""
			if err != nil {
				a.toast(a.u.tf("Cannot open %s: %s", filepath.Base(path), a.describe(err)), true)
				return
			}
			d.p, d.hist, d.dirty, d.file = p, project.History{}, false, path
			d.playhead, d.sel, d.picked, d.shown, d.view = 0, selection{}, nil, shownFrame{}, timelineView{}
			for _, s := range p.Sources {
				a.media.start(a, s)
			}
			d.shown.stale = true
		})
	})
}

// errEditVideoMissing means a video an edit uses is no longer where it was.
var errEditVideoMissing = errors.New("a video this edit uses is missing")

// checkEditVideos makes sure every video an edit uses is still there, and its first is still the same video.
func checkEditVideos(ctx context.Context, p *project.Project) error {
	for _, s := range p.Sources {
		if _, err := os.Stat(s.Path); err != nil {
			return fmt.Errorf("%w: %s", errEditVideoMissing, s.Path)
		}
	}
	info, err := media.Probe(ctx, p.Sources[0].Path)
	if err != nil {
		return err
	}
	src := p.Sources[0]
	src.Duration, src.Width, src.Height, src.FPS, src.HasAudio = info.Duration, info.Width, info.Height, info.FPS, info.HasAudio
	if !p.Fits(src) {
		return project.ErrSourceChanged
	}
	return nil
}
