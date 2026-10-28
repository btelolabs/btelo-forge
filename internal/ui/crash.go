package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"
)

// A failure in the app — a frame's work, or work done away from the window — never takes the app and the user's
// work down with it: it is reported to a file in the crashes folder next to the settings, with what was going on,
// everything open is saved at once, and the user is told; the app goes on. Work interrupted half way is left as it
// was, and the next frame starts clean.

// crashDir is where failure reports are kept.
func (a *App) crashDir() string { return filepath.Join(a.stateDir, "crashes") }

// survive, deferred, turns a panic of the work named where into a report, a save and a word to the user. Frames
// call it on the window's goroutine, with saving at once; work away from the window posts its part to the window.
func (a *App) survive(where string, onWindow bool) {
	if r := recover(); r != nil {
		a.failed(where, r, onWindow)
	}
}

// failed reports a failure, saves everything open and tells the user.
func (a *App) failed(where string, r any, onWindow bool) {
	path := a.writeCrash(where, r, debug.Stack())
	tell := func() {
		a.rescue()
		a.toast(a.u.tf("Something went wrong (%s); your work was saved, and the details were kept in %s", fmt.Sprint(r), path), true)
	}
	if onWindow {
		tell()
		return
	}
	a.post(tell)
}

// writeCrash keeps a report of a failure, and returns where; "" when it could not be written.
func (a *App) writeCrash(where string, r any, stack []byte) string {
	now := time.Now()
	report := fmt.Sprintf("Btelo Forge failure, %s\nwhere: %s\nwhat: %v\n\n%s", now.Format(time.RFC3339), where, r, stack)
	if err := os.MkdirAll(a.crashDir(), 0o755); err != nil {
		return ""
	}
	path := filepath.Join(a.crashDir(), "crash-"+now.Format("20060102-150405.000")+".txt")
	if os.WriteFile(path, []byte(report), 0o644) != nil {
		return ""
	}
	return path
}

// rescue saves every edit and canvas now, each on its own, so one that cannot be saved leaves the rest saved.
func (a *App) rescue() {
	for _, d := range a.docs {
		func() {
			defer func() { recover() }() // a save that fails as well is given up, not the others
			d.save(a.saveDir)
		}()
		if d.art != nil {
			func() {
				defer func() { recover() }()
				d.art.dirty = true
				a.saveArt(d.art, true)
			}()
		}
	}
}

// safely runs work away from the window, surviving a failure in it.
func (a *App) safely(where string, work func(ctx context.Context)) func(ctx context.Context) {
	return func(ctx context.Context) {
		defer a.survive(where, false)
		work(ctx)
	}
}
