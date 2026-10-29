package ui

import (
	"context"
	"errors"
	"image/color"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/update"
)

// A release build asks GitHub once a day whether a newer release is out, or when asked in Settings → About. A newer
// one is offered; once the user agrees it is downloaded, checked, and put in place of this copy, and the app closes
// as it always does (everything saved, a recording finished) and starts again as the new version. Recording, a live
// stream, an export or other work in the background holds the restart back until it ends.

// releasesURL is where the newest release is asked for; the tests point it at a server of their own, or at
// nothing.
var releasesURL = update.LatestURL

// updateInterval is how long after a check the next one is due.
const updateInterval = 24 * time.Hour

// updateRetry is how long after a failed check another is tried.
const updateRetry = time.Hour

// updateSettings are the update preferences kept between runs.
type updateSettings struct {
	Daily   bool      `json:"daily"`             // check once a day
	Checked time.Time `json:"checked,omitzero"`  // the last check that got an answer
	Pending string    `json:"pending,omitempty"` // the version put in place, for the start after it to report
}

// updateState is what is known of the releases and the update under way.
type updateState struct {
	exe      string       // the program an update replaces; the running one unless a test says otherwise
	client   *http.Client // nil is http.DefaultClient
	checking bool         // a check is running
	tried    time.Time    // when the last check began
	err      error        // why the last check failed
	latest   *update.Release
	job      *updateJob    // the download and installation under way
	ready    string        // the version put in place, waiting for the app to start again
	target   update.Target // where it was put
	quit     bool          // the app is to close and start the new version
}

// updateJob is a release being downloaded and installed.
type updateJob struct {
	tag        string
	done       atomic.Int64
	total      atomic.Int64
	installing atomic.Bool
	stopping   bool
	cancel     context.CancelFunc
}

// isRelease reports whether this is a release build, which can compare itself with the releases.
func isRelease() bool { _, ok := update.ParseVersion(Version); return ok }

// newerRelease is the release newer than this one that was found, or nil.
func (a *App) newerRelease() *update.Release {
	if r := a.upd.latest; r != nil && update.Newer(r.Tag, Version) {
		return r
	}
	return nil
}

// updateWaiting reports whether there is something to do about updating: a newer release to install, or one
// installed to start; Settings' button shows a mark then.
func (a *App) updateWaiting() bool {
	return a.upd.ready != "" || a.upd.job == nil && a.newerRelease() != nil
}

// updateStarted reports, once, the update the app was started after, and forgets it.
func (a *App) updateStarted() {
	s := &a.settings.Updates
	if s.Pending == "" {
		return
	}
	if update.Same(s.Pending, Version) {
		a.toast(a.u.tf("%s was updated to %s", ProductName, Version), false)
	}
	s.Pending = ""
	a.saveSettings()
}

// updateDue reports whether the daily check is due, and when it will be otherwise; zero when none is to be made.
func (a *App) updateDue() (bool, time.Duration) {
	s := a.settings.Updates
	if !s.Daily || !isRelease() || releasesURL == "" || a.upd.checking || a.upd.job != nil || a.upd.ready != "" {
		return false, 0
	}
	at := s.Checked.Add(updateInterval)
	if retry := a.upd.tried.Add(updateRetry); retry.After(at) {
		at = retry
	}
	wait := time.Until(at)
	return wait <= 0, max(wait, redrawNow)
}

// updateTick makes the daily check when it is due.
func (a *App) updateTick() {
	if due, _ := a.updateDue(); due {
		a.checkUpdate(false)
	}
}

// checkUpdate asks for the newest release; asked tells the user what was found even when nothing newer is out, and
// why it failed.
func (a *App) checkUpdate(asked bool) {
	if a.upd.checking {
		return
	}
	if releasesURL == "" {
		if asked {
			a.toast(a.u.t("Updates cannot be checked in this build"), true)
		}
		return
	}
	a.upd.checking, a.upd.tried = true, time.Now()
	client := a.updateClient()
	a.background(func(ctx context.Context) {
		r, err := update.Latest(ctx, client, releasesURL)
		a.post(func() {
			a.upd.checking = false
			a.upd.err = err
			if err != nil {
				if asked {
					a.toast(a.u.tf("Checking for a new version failed: %s", a.describe(err)), true)
				}
				return
			}
			a.upd.latest = &r
			a.settings.Updates.Checked = time.Now()
			a.saveSettings()
			switch {
			case a.newerRelease() != nil:
				if a.upd.job == nil && a.upd.ready == "" {
					a.offerUpdate()
				}
			case asked && !isRelease():
				a.toast(a.u.tf("The newest release is %s; this is a development build, which does not update itself", r.Tag), false)
			case asked:
				a.toast(a.u.tf("%s %s is the newest version", ProductName, Version), false)
			}
		})
	})
}

func (a *App) updateClient() *http.Client {
	if a.upd.client != nil {
		return a.upd.client
	}
	return http.DefaultClient
}

// offerUpdate tells the user a newer release is out, with the way to install it.
func (a *App) offerUpdate() {
	r := a.newerRelease()
	if r == nil {
		return
	}
	actions := []toastAction{{a.u.t("Update and restart"), a.startUpdate}}
	if page := r.Page; page != "" {
		actions = append(actions, toastAction{a.u.t("What's new"), func() { a.host.LaunchURL(page) }})
	}
	a.toastActions(a.u.tf("%s %s is out (this is %s)", ProductName, r.Tag, Version), false, actions)
}

// updateTarget is the installed copy an update would replace, and why it cannot be updated, if it cannot.
func (a *App) updateTarget(r *update.Release) (update.Target, update.Asset, error) {
	if !isRelease() {
		return update.Target{}, update.Asset{}, errDevBuild
	}
	exe := a.upd.exe
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return update.Target{}, update.Asset{}, err
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
	}
	t, err := update.TargetFor(runtime.GOOS, exe)
	if err != nil {
		return t, update.Asset{}, err
	}
	asset, ok := r.Asset(update.AssetName(runtime.GOOS, runtime.GOARCH))
	if !ok {
		return t, asset, update.ErrNoDownload
	}
	return t, asset, update.Writable(t.Dir)
}

// errDevBuild means this is not a release build, so there is nothing to compare a release with.
var errDevBuild = errors.New("a development build does not update itself")

// startUpdate downloads the newer release, checks it and puts it in place of this copy, then starts it.
func (a *App) startUpdate() {
	r := a.newerRelease()
	if r == nil || a.upd.job != nil || a.upd.ready != "" {
		return
	}
	t, asset, err := a.updateTarget(r)
	if err != nil {
		var actions []toastAction
		if page := r.Page; page != "" {
			actions = append(actions, toastAction{a.u.t("Open the release page"), func() { a.host.LaunchURL(page) }})
		}
		a.toastActions(a.u.tf("This copy cannot update itself: %s", a.describe(err)), true, actions)
		return
	}
	ctx, cancel := context.WithCancel(a.life)
	job := &updateJob{tag: r.Tag, cancel: cancel}
	job.total.Store(asset.Size)
	a.upd.job = job
	dir := filepath.Join(a.cacheDir, "update")
	client := a.updateClient()
	a.background(func(context.Context) {
		defer cancel()
		err := install(ctx, client, dir, asset, t, job)
		a.post(func() {
			a.upd.job = nil
			switch {
			case err != nil && job.stopping:
				a.toast(a.u.t("The update was cancelled"), false)
			case err != nil:
				a.toast(a.u.tf("Updating to %s failed: %s", job.tag, a.describe(err)), true)
			default:
				a.upd.ready, a.upd.target = job.tag, t
				a.settings.Updates.Pending = job.tag
				a.saveSettings()
				a.restartForUpdate()
			}
		})
	})
}

// install downloads the release's file into dir, unpacks it there and puts it in place of the target; it can be
// cancelled until the files start moving.
func install(ctx context.Context, client *http.Client, dir string, asset update.Asset, t update.Target, job *updateJob) error {
	staged := filepath.Join(dir, "staged")
	archive := filepath.Join(dir, asset.Name)
	os.RemoveAll(dir) // what an update that did not finish left
	defer os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	err := update.Download(ctx, client, asset, archive, func(done, total int64) {
		job.done.Store(done)
		job.total.Store(total)
	})
	if err != nil {
		return err
	}
	job.installing.Store(true)
	if err := update.Unpack(archive, staged); err != nil {
		return err
	}
	pairs, err := t.Pairs(staged)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return update.Replace(pairs)
}

// cancelUpdate stops the download.
func (a *App) cancelUpdate() {
	if j := a.upd.job; j != nil && !j.installing.Load() {
		j.stopping = true
		j.cancel()
	}
}

// updateTask is the card of the update under way, for the tasks in the bottom right corner.
func (a *App) updateTask() (task, bool) {
	j := a.upd.job
	if j == nil {
		return task{}, false
	}
	status, fraction := a.u.t("Downloading"), -1.0
	if total := j.total.Load(); total > 0 {
		fraction = float64(j.done.Load()) / float64(total)
	}
	cancel := a.cancelUpdate
	if j.installing.Load() {
		status, fraction, cancel = a.u.t("Installing"), -1, func() {}
	}
	return task{id: "update", glyph: icDownload, title: a.u.tf("Updating to %s", j.tag), status: status, fraction: fraction,
		stopping: j.stopping, cancel: cancel, open: a.openAbout}, true
}

// openAbout shows Settings → About, where updates are.
func (a *App) openAbout() {
	a.settingsOpen, a.settingsPage = true, settingsAboutPage
}

// updateHeldBy says what holds back starting the new version, "" when nothing does: work the restart would cut
// short.
func (a *App) updateHeldBy() string {
	u := &a.u
	switch {
	case a.recording():
		return u.t("Recording")
	case a.streaming():
		return u.t("Live streaming")
	case a.export != nil:
		return u.t("Exporting")
	}
	for _, t := range a.tasks() {
		if t.id != "update" {
			return t.title
		}
	}
	return ""
}

// restartForUpdate closes the app and starts the version put in place, unless work is under way; the user then
// restarts from Settings → About once it ends.
func (a *App) restartForUpdate() {
	if a.upd.ready == "" {
		return
	}
	if held := a.updateHeldBy(); held != "" {
		a.toastActions(a.u.tf("%s is installed; it starts once you restart, after this ends: %s", a.upd.ready, held), false,
			[]toastAction{{a.u.t("Settings"), a.openAbout}})
		return
	}
	a.upd.quit = true
	a.host.Wake()
}

// Quitting reports whether the app is to close to start the new version.
func (a *App) Quitting() bool { return a.upd.quit }

// Relaunch starts the version put in place, once the app has closed; it does nothing unless an update asked it to.
func (a *App) Relaunch() error {
	if !a.upd.quit {
		return nil
	}
	return update.Restart(a.upd.target)
}

// settingsUpdates draws the updates part of Settings → About from y, and returns the y below it.
func (a *App) settingsUpdates(r gfx.Rect, y float32) float32 {
	u := &a.u
	u.heading(rectAt(r.X, y, r.W, u.px(22)), u.t("Updates"))
	y += u.px(28)
	if on := u.checkbox("settings/update/daily", rectAt(r.X, y, r.W, u.px(26)), u.t("Check for a new version once a day"), a.settings.Updates.Daily); on != a.settings.Updates.Daily {
		a.settings.Updates.Daily = on
		a.saveSettings()
	}
	y += u.px(36)
	status, col := a.updateStatus()
	y = a.paragraph(r.X, y, r.W, status, 12.5, col) + u.px(8)
	x := r.X
	row := func(id, glyph, text string, kind buttonKind, enabled bool) bool {
		w := u.buttonWidth(glyph, text)
		clicked := u.button(id, rectAt(x, y, w, u.px(32)), glyph, text, kind, enabled)
		x += w + u.px(8)
		return clicked
	}
	newer := a.newerRelease()
	switch {
	case a.upd.ready != "":
		held := a.updateHeldBy()
		if row("settings/update/restart", "", u.t("Restart now"), primary, held == "") {
			a.restartForUpdate()
		}
		if held != "" {
			u.hint("settings/update/restart", u.tf("Waiting for this to end: %s", held), rectAt(r.X, y, x-r.X, u.px(32)))
		}
	case newer != nil && a.upd.job == nil:
		if row("settings/update/install", icDownload, u.tf("Update to %s and restart", newer.Tag), primary, isRelease()) {
			a.startUpdate()
		}
	}
	if a.upd.job == nil && a.upd.ready == "" {
		text := u.t("Check now")
		if a.upd.checking {
			text = u.t("Checking…")
		}
		if row("settings/update/check", "", text, plain, !a.upd.checking) {
			a.checkUpdate(true)
		}
	}
	if r := a.upd.latest; r != nil && r.Page != "" {
		if row("settings/update/notes", "", u.t("What's new"), ghost, true) {
			a.host.LaunchURL(r.Page)
		}
	}
	return y + u.px(44)
}

// updateStatus says in a line where updating stands.
func (a *App) updateStatus() (string, color.RGBA) {
	u := &a.u
	newer := a.newerRelease()
	switch {
	case a.upd.ready != "":
		return u.tf("%s is installed and starts when the app restarts.", a.upd.ready), colText
	case a.upd.job != nil:
		return u.tf("Updating to %s…", a.upd.job.tag), colText
	case a.upd.checking:
		return u.t("Checking for a new version…"), colDim
	case a.upd.err != nil:
		return u.tf("Checking for a new version failed: %s", a.describe(a.upd.err)), colDangerText
	case newer != nil:
		return u.tf("%s is out; this is %s.", newer.Tag, Version), colText
	case a.upd.latest != nil && !isRelease():
		return u.tf("The newest release is %s; this is a development build, which does not update itself", a.upd.latest.Tag), colDim
	case a.upd.latest != nil:
		return u.t("This is the newest version."), colDim
	case !isRelease():
		return u.t("This is a development build, which does not update itself"), colDim
	case !a.settings.Updates.Checked.IsZero():
		return u.tf("Last checked %s.", a.settings.Updates.Checked.Local().Format("2006-01-02 15:04")), colDim
	}
	return u.t("Not checked yet."), colDim
}
