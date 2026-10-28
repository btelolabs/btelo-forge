package ui

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"

	"github.com/btelolabs/btelo-video-cut/internal/media"
)

// toolsState is how far finding or installing ffmpeg has got.
type toolsState struct {
	config      *media.ToolsConfig
	ready       bool
	found       media.Tools
	downloading bool
	fraction    float64
	err         error
	stop        context.CancelFunc
	waiting     []string // videos dropped before ffmpeg was ready, opened once it is
}

func (a *App) toolsDir() string { return filepath.Join(a.cacheDir, "ffmpeg") }

// prepareTools finds a new enough ffmpeg, or downloads one, in the background; it starts over when called again.
func (a *App) prepareTools() {
	t := &a.tools
	if t.stop != nil {
		t.stop()
	}
	ctx, stop := context.WithCancel(a.life)
	t.stop, t.ready, t.downloading, t.fraction, t.err = stop, false, false, 0, nil
	config, dir := t.config, a.toolsDir()
	a.background(func(context.Context) {
		found, err := media.Locate(ctx, config, dir)
		if err != nil {
			a.post(func() {
				if ctx.Err() == nil {
					t.downloading = true
				}
			})
			found, err = media.Install(ctx, config, dir, http.DefaultClient, func(f float64) {
				a.post(func() { t.fraction = f })
			})
		}
		if ctx.Err() != nil {
			return
		}
		var enc media.Encoders
		if err == nil {
			media.Use(found)
			enc, _ = media.AvailableEncoders()
		}
		a.post(func() {
			if ctx.Err() != nil {
				return
			}
			t.downloading, t.err = false, err
			if err != nil {
				return
			}
			t.ready, t.found, a.encoders = true, found, enc
			a.checkRecording()
			waiting := t.waiting
			t.waiting = nil
			for i, path := range waiting {
				if i > 0 {
					a.newTab()
				}
				a.openVideo(path, false)
			}
		})
	})
}

// errToolsNotReady means ffmpeg is still being found or downloaded.
var errToolsNotReady = errors.New("ffmpeg is still being prepared")

// toolsStatus is a line about ffmpeg for the interface, empty once it is ready.
func (a *App) toolsStatus() (text string, failed bool) {
	u := &a.u
	t := &a.tools
	switch {
	case t.ready:
		return "", false
	case t.err != nil:
		return u.tf("ffmpeg could not be prepared: %s", a.describe(t.err)), true
	case t.downloading:
		return u.tf("Downloading ffmpeg (needed once) · %d%%", int(t.fraction*100)), false
	}
	return u.t("Looking for ffmpeg…"), false
}
