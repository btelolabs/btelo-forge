package ui

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/btelolabs/btelo-video-cut/internal/library"
	"github.com/btelolabs/btelo-video-cut/internal/web"
)

// phoneSite is what the phone page shows and where its uploads go.
func (a *App) phoneSite() web.Site {
	return web.Site{Files: a.phoneFiles, Text: a.phoneText, UploadDir: a.phoneUploadDir, Received: a.phoneReceived, Logo: logoJPEG}
}

// uploadDir is where files sent from a phone are kept: the default export folder, or the user's videos folder when
// exports go beside each original.
func (a *App) uploadDir() (string, error) {
	if a.settings.ExportDir != "" {
		return a.settings.ExportDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	videos := "Videos"
	if runtime.GOOS == "darwin" {
		videos = "Movies"
	}
	return filepath.Join(home, videos, ProductName), nil
}

// phoneFiles runs on a server goroutine, so it asks the UI goroutine for the list.
func (a *App) phoneFiles() []web.File {
	list, _ := call(a, func() ([]web.File, error) {
		var out []web.File
		for _, r := range a.exports() {
			out = append(out, web.File{Path: r.Path, At: r.At})
		}
		return out, nil
	})
	return list
}

func (a *App) phoneUploadDir() (string, error) {
	type dir struct{ path string }
	d, err := call(a, func() (dir, error) {
		p, err := a.uploadDir()
		return dir{p}, err
	})
	return d.path, err
}

// phoneReceived tells the user what arrived from the phone, with a way to open it.
func (a *App) phoneReceived(paths []string) {
	a.post(func() {
		u := &a.u
		first := paths[0]
		actions := []toastAction{{u.t("Show in folder"), func() { a.host.Reveal(first) }}}
		if !library.IsAudio(first) && !isImage(first) {
			actions = append([]toastAction{{u.t("Open in a new tab"), func() {
				a.newTab()
				a.openVideo(first, false)
			}}}, actions...)
		}
		text := u.tf("Received %s from the phone", filepath.Base(first))
		if len(paths) > 1 {
			text = u.tf("Received %d files from the phone", len(paths))
		}
		a.toastActions(text, false, actions)
	})
}

func (a *App) phoneText() web.Text {
	t, _ := call(a, func() (web.Text, error) {
		u := &a.u
		dir, err := a.uploadDir()
		if err != nil {
			dir = err.Error()
		}
		return web.Text{
			Title:      u.tf("%s · phone", ProductName),
			Empty:      u.t("No exported videos yet. Export one in the app and it shows up here."),
			Download:   u.t("Download"),
			Refresh:    u.t("Refresh"),
			Exports:    u.t("Exported videos"),
			Upload:     u.t("Send to the computer"),
			UploadHint: u.tf("Videos, sound and pictures are saved in %s", dir),
			Send:       u.t("Upload"),
			Uploaded:   u.t("Uploaded %d: %s"),
			Refused:    u.t("Skipped %d that are not videos, sound or pictures: %s"),
			Failed:     u.t("Upload failed: %v"),
		}, nil
	})
	return t
}
