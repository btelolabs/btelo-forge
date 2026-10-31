package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/capture"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/mind"
)

// recorderHost is what a window offers beyond Host for recording the screen. A window without it, or a system the
// capture package cannot record, shows no recording controls.
type recorderHost interface {
	Minimize()
	Restore() // also brings back a hidden window
	Hide()
	Shown() bool // on the screen and in front, not minimized, hidden or behind another program
	ExcludeFromCapture(on bool) error
	SetHotkeys(keys []Hotkey) []error
}

// recordSettings are the choices in the recording dialog, kept between runs.
type recordSettings struct {
	Display      string `json:"display"` // capture.Display.Key; "" or a missing one records the main screen
	FPS          int    `json:"fps"`
	Cursor       bool   `json:"cursor"`
	SystemAudio  bool   `json:"systemAudio"`
	Microphone   bool   `json:"microphone"`
	MicrophoneID string `json:"microphoneId"` // "" follows the system's default microphone
	Region       bool   `json:"region"`       // pick a region of the screen when recording starts
	// Minimize sends this window to the taskbar when a recording starts. It is off by default — the floating toolbar
	// controls the recording, and the app is often what is recorded — and named anew so that the old default, on, is
	// not carried over.
	Minimize bool `json:"minimizeOnStart"`
	// HideWindow leaves this window out of recordings and screenshots. It is off by default — recording how the app
	// is used is a common reason to record — and named anew so that the old default, on, is not carried over.
	HideWindow bool `json:"leaveWindowOut"`
}

func defaultRecordSettings() recordSettings {
	return recordSettings{FPS: 30, Cursor: true, SystemAudio: true}
}

var recordRates = []int{30, 60}

// recordAreas are the choices of what part of the screen to record.
var recordAreas = msgs("The whole screen", "A region of any screen, picked at the start")

// recordState is the recording dialog and the recording in progress.
type recordState struct {
	open     bool
	listed   bool // displays and microphones have been looked up
	listing  bool
	displays []capture.Display
	mics     []capture.AudioDevice
	listErr  error                      // why this computer cannot record at all: too old a Windows, or no screen
	encoders map[string]capture.Encoder // by encoderKey
	failures map[string]error
	probing  string // the encoderKey being probed

	rec      *capture.Recorder
	starting bool
	stopping bool
	busy     bool // pausing or resuming
	shooting bool // a screenshot is being taken
	title    string
	scroll   float32 // the dialog's scroll offset
	content  float32 // and its content height at the last frame
}

// recorder is the window's recording support, or nil where screen recording is not offered at all (macOS, or a
// window that cannot get out of the way); on Windows the controls are shown even when this computer cannot record,
// and say why.
func (a *App) recorder() recorderHost {
	if !capture.Supported() {
		return nil
	}
	h, _ := a.host.(recorderHost)
	return h
}

func encoderKey(d capture.Display, fps int) string { return d.Key() + "@" + strconv.Itoa(fps) }

// recording reports whether a recording is running, paused or being saved.
func (a *App) recording() bool { return a.rec.rec != nil || a.rec.starting }

// openRecorder shows the recording dialog and looks up what can be recorded.
func (a *App) openRecorder() {
	if a.recorder() == nil || a.recording() {
		return
	}
	a.rec.open = true
	a.checkRecording()
}

// checkRecording finds out, once, whether and what this computer can record: the Windows version, the displays and
// microphones, then the encoder for the chosen display once ffmpeg is ready. It runs when the app has found ffmpeg,
// so the controls can tell before they are used.
func (a *App) checkRecording() {
	r := &a.rec
	if a.recorder() == nil {
		return
	}
	if r.listed || r.listing {
		a.probeEncoder()
		return
	}
	r.listing = true
	a.background(func(context.Context) {
		var displays []capture.Display
		var mics []capture.AudioDevice
		err := capture.CheckSystem()
		if err == nil {
			displays, err = capture.Displays()
			mics, _ = capture.Microphones() // without a microphone the system's sound can still be recorded
		}
		a.post(func() {
			r.listing, r.listed = false, true
			r.displays, r.mics, r.listErr = displays, mics, err
			a.probeEncoder()
		})
	})
}

// chosenDisplay is the display the settings name, or the main one.
func (a *App) chosenDisplay() (capture.Display, bool) {
	list := a.rec.displays
	if len(list) == 0 {
		return capture.Display{}, false
	}
	for _, d := range list {
		if d.Key() == a.settings.Record.Display {
			return d, true
		}
	}
	for _, d := range list {
		if d.Primary {
			return d, true
		}
	}
	return list[0], true
}

// probeEncoder finds, once per display and frame rate, which encoder records the chosen display.
func (a *App) probeEncoder() {
	r := &a.rec
	d, ok := a.chosenDisplay()
	if !ok || !a.tools.ready {
		return
	}
	key := encoderKey(d, a.settings.Record.FPS)
	if _, done := r.encoders[key]; done || r.failures[key] != nil || r.probing != "" {
		return
	}
	r.probing = key
	fps := a.settings.Record.FPS
	a.background(func(ctx context.Context) {
		e, err := capture.ProbeEncoder(ctx, d, fps)
		a.post(func() {
			r.probing = ""
			if err != nil {
				if r.failures == nil {
					r.failures = map[string]error{}
				}
				r.failures[key] = err
			} else {
				if r.encoders == nil {
					r.encoders = map[string]capture.Encoder{}
				}
				r.encoders[key] = e
			}
			// The choice may have changed while this one was probed.
			a.probeEncoder()
		})
	})
}

// recordBlocker says why a recording cannot start now: a reason for the user, or checking while that is still
// being found out; both empty means it can.
func (a *App) recordBlocker() (reason string, checking bool) {
	r := &a.rec
	u := &a.u
	switch {
	case a.tools.err != nil:
		text, _ := a.toolsStatus()
		return text, false
	case r.listErr != nil:
		return a.describe(r.listErr), false
	case !r.listed || !a.tools.ready:
		return "", true
	}
	d, ok := a.chosenDisplay()
	if !ok {
		return u.t("No screen to record was found"), false
	}
	key := encoderKey(d, a.settings.Record.FPS)
	if err := r.failures[key]; err != nil {
		return u.tf("This screen cannot be recorded: %s", a.describe(err)), false
	}
	if _, found := r.encoders[key]; !found {
		return "", true
	}
	return "", false
}

// recordingPath is a new file for a recording in the folder recordings and phone uploads go to.
func (a *App) recordingPath(at time.Time) (string, error) {
	return a.capturePath(categoryTools, a.u.t("Screen recording %s"), ".mp4", at)
}

// capturePath is a new file in the folder of a kind of work, named by the translated format with the time filled in.
func (a *App) capturePath(category int, format, ext string, at time.Time) (string, error) {
	dir, err := a.categoryDir(category)
	if err != nil {
		return "", err
	}
	return a.datedPath(dir, format, ext, at), nil
}

// datedPath is a free name in dir for a file made at a time, format putting the time in its name.
func (a *App) datedPath(dir, format, ext string, at time.Time) string {
	return a.freePath(filepath.Join(dir, fmt.Sprintf(format, at.Format("2006-01-02 15.04.05"))+ext))
}

// freePath is path, or path with " (2)", " (3)"… before its extension, the first that no file has and no open
// canvas or map is to be saved to: a canvas's file is written only with its first change, so two made in the same
// second must not share a name.
func (a *App) freePath(path string) string {
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 2; ; i++ {
		if a.pathFree(path) {
			return path
		}
		path = fmt.Sprintf("%s (%d)%s", base, i, ext)
	}
}

// pathFree reports whether a new file may be named path: nothing is there, and no open tab will save there.
func (a *App) pathFree(path string) bool {
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		return false
	}
	key := mind.NestKey(path)
	for _, d := range a.docs {
		if d.art != nil && mind.NestKey(d.art.path) == key || d.board != nil && mind.NestKey(d.board.path) == key {
			return false
		}
	}
	return true
}

// startRecording begins recording with the saved settings, first letting the user pick a region of any display when
// they say so; done, when not nil, hears whether it started.
func (a *App) startRecording(done func(path string, err error)) {
	d, err := a.recordingReady()
	if err != nil {
		reportStart(done, err)
		return
	}
	if !a.settings.Record.Region {
		a.startRecordingOn(d, image.Rectangle{}, done)
		return
	}
	a.rec.open = false
	a.pickRegion(a.recordPick(), func(d capture.Display, region image.Rectangle, ok bool) {
		if !ok {
			reportStart(done, errRegionCancelled)
			return
		}
		a.startRecordingOn(d, region, done)
	})
}

// startRecordingRegion begins recording a region of the chosen display (empty for the whole display).
func (a *App) startRecordingRegion(region image.Rectangle, done func(path string, err error)) {
	d, err := a.recordingReady()
	if err != nil {
		reportStart(done, err)
		return
	}
	a.startRecordingOn(d, region, done)
}

// errRegionCancelled means the user cancelled picking the region to record or capture.
var errRegionCancelled = errors.New("no region was chosen")

// reportStart tells done, when not nil, how starting a recording went.
func reportStart(done func(path string, err error), err error) {
	if done != nil {
		done("", err)
	}
}

// recordingReady is the chosen display when a recording can start now, or why it cannot.
func (a *App) recordingReady() (capture.Display, error) {
	r := &a.rec
	switch {
	case a.recorder() == nil:
		return capture.Display{}, capture.ErrUnsupported
	case a.recording():
		return capture.Display{}, errors.New(a.u.t("A recording is already running"))
	}
	if reason, _ := a.recordBlocker(); reason != "" {
		return capture.Display{}, errors.New(reason)
	}
	d, ok := a.chosenDisplay()
	if !ok {
		err := r.listErr
		if err == nil {
			err = errors.New(a.u.t("No screen to record was found"))
		}
		return capture.Display{}, err
	}
	return d, nil
}

// startRecordingOn begins recording a region of a display (empty for all of it), checking again that it can: much
// may have changed while the region was picked.
func (a *App) startRecordingOn(d capture.Display, region image.Rectangle, done func(path string, err error)) {
	r := &a.rec
	report := func(path string, err error) {
		if done != nil {
			done(path, err)
		}
	}
	if _, err := a.recordingReady(); err != nil {
		report("", err)
		return
	}
	host := a.recorder()
	path, err := a.recordingPath(time.Now())
	if err != nil {
		report("", err)
		return
	}
	s := a.settings.Record
	o := capture.Options{Display: d, FPS: s.FPS, Cursor: s.Cursor, SystemAudio: s.SystemAudio, Microphone: s.Microphone, MicrophoneID: s.MicrophoneID,
		Region: region, Path: path}
	key := encoderKey(d, s.FPS)
	e, known := r.encoders[key]
	r.starting, r.open = true, false
	a.player.Stop()
	a.audition.Stop()
	a.background(func(ctx context.Context) {
		var err error
		if !known {
			e, err = capture.ProbeEncoder(ctx, d, s.FPS)
		}
		var rec *capture.Recorder
		if err == nil {
			o.Encoder = e
			rec, err = capture.Start(o)
		}
		a.post(func() {
			r.starting = false
			if err != nil {
				a.toast(a.u.tf("Recording could not start: %s", a.describe(err)), true)
				report("", err)
				return
			}
			if !known {
				// Another display than the chosen one is probed as it is first recorded, and remembered.
				if r.encoders == nil {
					r.encoders = map[string]capture.Encoder{}
				}
				r.encoders[key] = e
			}
			r.rec = rec
			a.liveFollow()
			a.showRegionFrame(d, rec.Options().Region)
			if s.HideWindow {
				if err := host.ExcludeFromCapture(true); err != nil {
					a.toast(a.u.tf("The window could not be left out of the recording: %s", err), true)
				}
			}
			if s.Minimize {
				host.Minimize()
			}
			report(path, nil)
		})
	})
}

// pauseRecording pauses or resumes the recording.
func (a *App) pauseRecording(pause bool, done func(error)) {
	r := &a.rec
	rec := r.rec
	if rec == nil || r.stopping || r.busy {
		if done != nil {
			done(errors.New(a.u.t("No recording is running")))
		}
		return
	}
	r.busy = true
	a.background(func(context.Context) {
		var err error
		if pause {
			err = rec.Pause()
		} else {
			err = rec.Resume()
		}
		a.post(func() {
			r.busy = false
			if err != nil {
				a.toast(a.u.tf("Recording failed: %s", a.describe(err)), true)
			}
			if done != nil {
				done(err)
			}
		})
	})
}

// stopRecording finishes the recording, saves it and opens it in a tab for editing; done, when not nil, hears the
// saved file.
func (a *App) stopRecording(done func(path string, err error)) {
	r := &a.rec
	rec := r.rec
	if rec == nil || r.stopping {
		if done != nil {
			done("", errors.New(a.u.t("No recording is running")))
		}
		return
	}
	r.stopping = true
	a.liveFollow() // the stream goes black, and on
	failure := rec.Err()
	// The joining goes on while the app closes, so it does not use the app's lifetime.
	a.background(func(context.Context) {
		// The grabbing ends first (a pause), so the window coming back is not in the recording; then the window
		// shows the recording being saved, which takes a while for a long one.
		rec.Pause()
		a.post(func() {
			a.releaseRecordingWindow()
			if host := a.recorder(); host != nil {
				host.Restore()
			}
			a.host.SetTitle(a.u.tf("Saving the recording — %s", ProductName))
		})
		ctx, cancel := context.WithTimeout(context.Background(), joinWait)
		defer cancel()
		path, err := rec.Stop(ctx)
		a.post(func() {
			r.rec, r.stopping = nil, false
			a.host.SetTitle(a.Title())
			r.title = ""
			if failure != nil {
				a.toast(a.u.tf("The recording stopped early: %s", a.describe(failure)), true)
			}
			if err != nil {
				a.toast(a.u.tf("The recording could not be saved: %s", a.describe(err)), true)
				if done != nil {
					done("", err)
				}
				return
			}
			if a.p != nil || a.opening != "" {
				a.newTab()
			}
			a.openVideo(path, false)
			a.toastActions(a.u.tf("Saved the recording %s", filepath.Base(path)), false, []toastAction{
				{a.u.t("Show in folder"), func() { a.host.Reveal(path) }},
			})
			if done != nil {
				done(path, nil)
			}
		})
	})
}

// joinWait bounds how long saving a recording may take; the picture is copied and only the sound encoded, so even
// hours take less.
const joinWait = 10 * time.Minute

// releaseRecordingWindow undoes what starting a recording did to the window.
func (a *App) releaseRecordingWindow() {
	host := a.recorder()
	if host == nil {
		return
	}
	host.ExcludeFromCapture(false)
}

// recordTick looks after a running recording every frame, also while the window is minimized: the hotkey, a
// recording that stopped by itself, and the elapsed time in the window title (shown on the taskbar).
func (a *App) recordTick(in Input) {
	r := &a.rec
	a.handleHotkeys(in.Hotkeys)
	a.liveTick()
	a.cameraTick()
	if r.rec == nil {
		return
	}
	select {
	case <-r.rec.Ended():
		if !r.stopping {
			a.stopRecording(nil)
			return
		}
	default:
	}
	if r.stopping {
		return
	}
	st := r.rec.Status()
	title := a.u.tf("● Recording %s — %s", shortClock(st.Elapsed.Seconds()), ProductName)
	if st.Paused {
		title = a.u.tf("Recording paused %s — %s", shortClock(st.Elapsed.Seconds()), ProductName)
	}
	if title != r.title {
		r.title = title
		a.host.SetTitle(title)
	}
}

// Tick does the work of a frame while the window has nothing to draw into (minimized); it returns how soon it
// wants to run again.
func (a *App) Tick(in Input) time.Duration {
	a.drainJobs()
	a.recordTick(in)
	a.screenTick()
	a.updateTick()
	if sd := a.over.draw; sd != nil && (sd.wait > 0 || sd.again) {
		return max(min(sd.wait, 250*time.Millisecond), redrawNow) // a hint on the drawing's panel, or its options wrapped
	}
	if a.recording() || a.streaming() || a.cameraSettling() || a.over.bar != nil || a.over.pick != nil || a.over.draw != nil {
		return 250 * time.Millisecond
	}
	return 0
}

// finishRecordingForClose saves a recording that is still running when the app closes.
func (a *App) finishRecordingForClose() {
	rec := a.rec.rec
	if rec == nil || a.rec.stopping {
		return
	}
	a.releaseRecordingWindow()
	ctx, cancel := context.WithTimeout(context.Background(), joinWait)
	defer cancel()
	rec.Stop(ctx)
}

// encoderName says in words how the recording is encoded.
func (a *App) encoderName(e capture.Encoder) string {
	u := &a.u
	name := map[string]string{"h264_nvenc": "NVIDIA NVENC", "h264_amf": "AMD AMF", "h264_mf": "Media Foundation"}[e.Codec]
	text := u.tf("The graphics card encodes (%s), so recording costs the processor almost nothing", name)
	if !e.Hardware() {
		text = u.t("No graphics-card encoder works here, so the processor encodes, at up to 1080p and 30 frames a second")
	}
	return text
}

// displayLabel names a display the way the Windows display settings number it.
func (a *App) displayLabel(d capture.Display, i int) string {
	u := &a.u
	n := i + 1
	if v, err := strconv.Atoi(strings.TrimPrefix(d.Device, `\\.\DISPLAY`)); err == nil {
		n = v
	}
	parts := []string{u.tf("Screen %d", n)}
	if d.Name != "" {
		parts = append(parts, d.Name)
	}
	parts = append(parts, fmt.Sprintf("%d×%d", d.Width, d.Height))
	if d.Primary {
		parts = append(parts, u.t("main"))
	}
	return strings.Join(parts, " · ")
}

// drawRecordControl is, while recording, the recording's time, sound levels and buttons at the end of the tab strip;
// it returns where it begins. It is w wide; narrower than its usual 232, down the side strip, it has no room for the
// size of the file, and the word for saving takes the time's place. Recording and screenshots start from the new tab's
// card or their hotkeys.
func (a *App) drawRecordControl(end, y, h, w float32) float32 {
	u := &a.u
	r := &a.rec
	if r.rec == nil {
		return end
	}
	end -= u.px(4)
	st := r.rec.Status()
	pill := gfx.Rect{X: end - w - u.px(4), Y: y, W: w, H: h}
	narrow := w < u.px(232)
	u.c.Fill(pill, gfx.Mix(colSurface, colDanger, 0.18))
	dot := colDanger
	if st.Paused {
		dot = colDim
	} else if time.Now().UnixMilli()/500%2 == 0 {
		dot = gfx.Mix(colDanger, colPanel, 0.5)
	}
	u.c.Circle(pill.X+u.px(14), pill.Y+h/2, u.px(4.5), dot)
	if narrow && r.stopping {
		u.label(u.face(11), rectAt(pill.X+u.px(24), pill.Y, w-u.px(90), h), u.t("Saving…"), colDim, 0)
	} else {
		u.label(u.bold(12.5), rectAt(pill.X+u.px(24), pill.Y, u.px(56), h), shortClock(st.Elapsed.Seconds()), colText, 0)
	}
	switch {
	case narrow:
	case r.stopping:
		u.label(u.face(11), rectAt(pill.X+u.px(78), pill.Y, u.px(90), h), u.t("Saving…"), colDim, 0)
	case st.Bytes > 1<<10:
		u.label(u.face(11), rectAt(pill.X+u.px(78), pill.Y, u.px(60), h), size(st.Bytes), colDim, 0)
	}
	// A level bar for each sound being recorded: the computer's, then the microphone's.
	o := r.rec.Options()
	var levels []float32
	var names []string
	if r.stopping {
		o.SystemAudio, o.Microphone = false, false // the sound has stopped; the space shows the saving instead
	}
	if o.SystemAudio {
		levels, names = append(levels, st.System), append(names, u.t("the computer's sound"))
	}
	if o.Microphone {
		levels, names = append(levels, st.Mic), append(names, u.t("the microphone"))
	}
	// Before the pause button, and in a narrow pill shorter and closer, to clear the time.
	meterX, meterW := pill.Right()-u.px(96), u.px(24)
	if narrow {
		meterX, meterW = pill.Right()-u.px(86), u.px(20)
	}
	meters := rectAt(meterX, pill.Y+h/2-u.px(3)*float32(len(levels)), meterW, u.px(6)*float32(len(levels)))
	for i, level := range levels {
		bar := rectAt(meters.X, meters.Y+float32(i)*u.px(6), meters.W, u.px(4))
		u.c.Fill(bar, colSurfaceHi)
		u.c.Fill(rectAt(bar.X, bar.Y, max(bar.W*level, 0), bar.H), colAccent)
	}
	if len(names) > 0 {
		u.hint("tabs/rec/meters", u.tf("Sound level of %s", strings.Join(names, u.t(" and "))), meters)
	}
	pause := rectAt(pill.Right()-u.px(62), pill.Y+u.px(2), u.px(28), h-u.px(4))
	glyph, hint := icPause, u.t("Pause the recording")
	if st.Paused {
		glyph, hint = icRecord, u.t("Resume the recording")
	}
	hint = withKey(hint, a.hotkeyLabel(hotkeyPause))
	if u.iconButton("tabs/rec/pause", pause, glyph, hint, false, !r.busy && !r.stopping) {
		a.pauseRecording(!st.Paused, nil)
	}
	stop := rectAt(pill.Right()-u.px(32), pill.Y+u.px(2), u.px(28), h-u.px(4))
	if u.iconButton("tabs/rec/stop", stop, icStop, withKey(u.t("Stop and edit the recording"), a.hotkeyLabel(hotkeyRecord)), false, !r.stopping) {
		a.stopRecording(nil)
	}
	return pill.X
}

// shotTarget is where a screenshot goes.
type shotTarget int

const (
	shotClipboard shotTarget = iota // onto the clipboard, as a picture, and nowhere else
	shotFile                        // into the pictures folder
	shotPath                        // into the pictures folder, with its path on the clipboard
)

// shotTargets are the targets of the screenshot hotkeys, from hotkeyScreenshot on.
var shotTargets = [...]shotTarget{shotClipboard, shotFile, shotPath}

// screenshotRegion lets the user pick a region of any display, or click for all of one, and sends it to the target.
func (a *App) screenshotRegion(to shotTarget) {
	if _, ok := a.chosenDisplay(); !ok || a.rec.shooting {
		a.takeScreenshot(capture.Display{}, image.Rectangle{}, to, nil) // says why it cannot
		return
	}
	a.pickRegion(a.shotPick(to), func(d capture.Display, region image.Rectangle, ok bool) {
		if ok {
			a.takeScreenshot(d, region, to, nil)
		}
	})
}

// withKey adds a hotkey to a hint, when there is one.
func withKey(hint, key string) string {
	if key == "" {
		return hint
	}
	return hint + " (" + key + ")"
}

// takeScreenshot takes a PNG of a region of a display (the chosen one when d is zero, all of it when region is
// empty), leaving this window out when the recording settings say so, and sends it to the target; done, when not nil, hears the file, "" for one only on the clipboard.
func (a *App) takeScreenshot(d capture.Display, region image.Rectangle, to shotTarget, done func(path string, err error)) {
	r := &a.rec
	report := func(path string, err error) {
		if err != nil {
			a.toast(a.u.tf("The screenshot could not be taken: %s", a.describe(err)), true)
		}
		if done != nil {
			done(path, err)
		}
	}
	host := a.recorder()
	if host == nil {
		report("", capture.ErrUnsupported)
		return
	}
	if r.shooting {
		report("", errors.New(a.u.t("A screenshot is already being taken")))
		return
	}
	if !a.tools.ready || r.listErr != nil || !r.listed {
		reason, _ := a.recordBlocker()
		if reason == "" {
			reason = a.u.t("Checking how this computer can record…")
		}
		report("", errors.New(reason))
		return
	}
	if d.Width == 0 {
		var ok bool
		if d, ok = a.chosenDisplay(); !ok {
			report("", errors.New(a.u.t("No screen to record was found")))
			return
		}
	}
	cb, pictures := a.host.(imageClipboard)
	if to == shotClipboard && !pictures {
		report("", errors.New(a.u.t("Pictures cannot be put on the clipboard on this system")))
		return
	}
	var path string
	var err error
	if to == shotClipboard {
		path, err = tempShotPath()
	} else {
		var dir string
		if dir, err = a.categoryDir(categoryTools); err == nil {
			path = a.datedPath(dir, a.u.t("Screenshot %s"), ".png", time.Now())
		}
	}
	if err != nil {
		report("", err)
		return
	}
	s := a.settings.Record
	hide := s.HideWindow && r.rec == nil // while recording the window is already handled
	if hide {
		host.ExcludeFromCapture(true)
	}
	r.shooting = true
	a.background(func(ctx context.Context) {
		err := capture.Screenshot(ctx, d, region, s.Cursor, path)
		var img image.Image
		if err == nil && to == shotClipboard {
			img, err = readPNG(path)
			os.Remove(path)
		}
		a.post(func() {
			r.shooting = false
			if hide {
				host.ExcludeFromCapture(false)
			}
			if err == nil && img != nil {
				err = cb.SetClipboardImage(img)
			}
			if err == nil && to == shotPath {
				err = a.host.SetClipboard(path)
			}
			if err != nil {
				report("", err)
				return
			}
			if to == shotClipboard {
				b := img.Bounds()
				a.toast(a.u.tf("Copied a screenshot of %d × %d", b.Dx(), b.Dy()), false)
				report("", nil)
				return
			}
			actions := []toastAction{{a.u.t("Edit"), func() { a.openImageAsArt(path) }}, {a.u.t("Show in folder"), func() { a.host.Reveal(path) }}}
			if a.p != nil {
				actions = append([]toastAction{{a.u.t("Add to the edit"), func() { a.addImage(path) }}}, actions...)
			}
			msg := a.u.tf("Saved the screenshot %s", filepath.Base(path))
			if to == shotPath {
				msg = a.u.tf("Saved the screenshot %s and copied its path", filepath.Base(path))
			}
			a.toastActions(msg, false, actions)
			report(path, nil)
		})
	})
}

// tempShotPath is a free name in the temporary folder for a screenshot that goes only onto the clipboard.
func tempShotPath() (string, error) {
	f, err := os.CreateTemp("", "forge-screenshot-*.png")
	if err != nil {
		return "", err
	}
	f.Close()
	return f.Name(), nil
}

// readPNG decodes the PNG at path.
func readPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

// drawRecorder is the dialog that sets up a recording. It is as tall as its content, which scrolls when the
// window is too low for it.
func (a *App) drawRecorder() {
	r := &a.rec
	if !r.open {
		return
	}
	u := &a.u
	u.c.Fill(a.screen, gfxShade)
	pad, header, footer := u.px(24), u.px(44), u.px(56)
	content := r.content
	if content == 0 {
		content = u.px(520)
	}
	w := min(u.px(640), a.screen.W-u.px(40))
	// One pixel of slack keeps rounding from showing a scroll bar for content that fits.
	h := min(pad+header+content+footer+pad+1, a.screen.H-u.px(40))
	box := rectCentered(a.screen, w, h)
	panel(u, box)
	in := box.Inset(pad)
	u.label(u.bold(17), rectAt(in.X, in.Y, in.W-u.px(40), u.px(26)), u.t("Record the screen"), colText, 0)
	if u.iconButton("record/close", rectAt(in.Right()-u.px(30), in.Y-u.px(2), u.px(30), u.px(30)), icClose, u.t("Close"), false, true) {
		r.open = false
	}
	body := rectAt(in.X, in.Y+header, in.W+u.px(8), in.H-header-footer)
	draw := a.recorderBody
	if r.listErr != nil {
		draw = a.recorderUnavailable
	}
	a.scrolledBy("record/scroll", body, &r.scroll, &r.content, draw)
	foot := in.Bottom() - u.px(38)
	if r.listErr != nil {
		if u.button("record/close/foot", rectAt(in.Right()-u.px(110), foot, u.px(110), u.px(38)), "", u.t("Close"), plain, true) {
			r.open = false
		}
	} else {
		if key := a.hotkeyLabel(hotkeyRecord); key != "" {
			u.label(u.face(12), rectAt(in.X, foot, in.W-u.px(330), u.px(38)), u.tf("Stop at any time with %s", key), colFaint, 0)
		}
		if u.button("record/cancel", rectAt(in.Right()-u.px(316), foot, u.px(110), u.px(38)), "", u.t("Cancel"), ghost, true) {
			r.open = false
		}
		if u.button("record/start", rectAt(in.Right()-u.px(198), foot, u.px(198), u.px(38)), icRecord, u.t("Start recording"), primary, a.recorderReady() && a.export == nil) {
			a.startRecording(nil)
		}
	}
	for _, k := range u.keys {
		if k.Code == keyEscape && u.focus == "" {
			r.open = false
		}
	}
	u.keys = nil
}

// recorderReady reports whether a recording can start at once.
func (a *App) recorderReady() bool {
	reason, checking := a.recordBlocker()
	return reason == "" && !checking
}

// recorderUnavailable is the dialog's body on a computer that cannot record at all: why, and what would help.
func (a *App) recorderUnavailable(in gfx.Rect) float32 {
	u := &a.u
	u.heading(rectAt(in.X, in.Y, in.W, u.px(20)), u.t("Screen recording is not available on this computer"))
	reason, _ := a.recordBlocker()
	y := a.paragraph(in.X, in.Y+u.px(30), in.W, reason, 13, colWarning)
	return a.paragraph(in.X, y+u.px(10), in.W, u.t("Everything else in the editor works as usual."), 12, colDim)
}

// recorderBody draws the dialog's choices and returns the bottom of them.
func (a *App) recorderBody(in gfx.Rect) float32 {
	r := &a.rec
	u := &a.u
	s := &a.settings.Record
	changed := false
	y := in.Y
	u.heading(rectAt(in.X, y, in.W, u.px(20)), u.t("Display"))
	y += u.px(26)
	switch {
	case r.listing:
		u.label(u.face(12.5), rectAt(in.X, y, in.W, u.px(30)), u.t("Looking for screens…"), colDim, 0)
		y += u.px(30)
	case len(r.displays) == 0:
		text := u.t("No screen to record was found")
		if r.listErr != nil {
			text += ": " + a.describe(r.listErr)
		}
		y = a.paragraph(in.X, y+u.px(6), in.W, text, 12.5, colDanger)
	default:
		labels := make([]string, len(r.displays))
		current := 0
		chosen, _ := a.chosenDisplay()
		for i, d := range r.displays {
			labels[i] = a.displayLabel(d, i)
			if d.Key() == chosen.Key() {
				current = i
			}
		}
		pick, used := u.flowChips("record/display", rectAt(in.X, y, in.W, u.px(30)), labels, current)
		if pick != current {
			s.Display, changed = r.displays[pick].Key(), true
		}
		y += used
	}
	if _, ok := a.host.(overlayHost); ok {
		y += u.px(16)
		u.heading(rectAt(in.X, y, in.W, u.px(20)), u.t("Area"))
		y += u.px(26)
		current := 0
		if s.Region {
			current = 1
		}
		if pick := u.chips("record/area", rectAt(in.X, y, min(in.W, u.px(420)), u.px(30)), u.ts(recordAreas), current, true); pick != current {
			s.Region, changed = pick == 1, true
		}
		y += u.px(30)
	}
	y += u.px(16)
	u.heading(rectAt(in.X, y, in.W, u.px(20)), u.t("Frame rate"))
	y += u.px(26)
	rates := make([]string, len(recordRates))
	for i, fps := range recordRates {
		rates[i] = u.tf("%d frames a second", fps)
	}
	current := max(slices.Index(recordRates, s.FPS), 0)
	if pick := u.chips("record/fps", rectAt(in.X, y, min(in.W, u.px(320)), u.px(30)), rates, current, true); pick != current {
		s.FPS, changed = recordRates[pick], true
	}
	y += u.px(46)
	u.heading(rectAt(in.X, y, in.W, u.px(20)), u.t("Sound"))
	y += u.px(24)
	if on := u.checkbox("record/system", rectAt(in.X, y, in.W, u.px(26)), u.t("Sound playing on this computer"), s.SystemAudio); on != s.SystemAudio {
		s.SystemAudio, changed = on, true
	}
	y += u.px(30)
	if on := u.checkbox("record/mic", rectAt(in.X, y, in.W, u.px(26)), u.t("Microphone"), s.Microphone); on != s.Microphone {
		s.Microphone, changed = on, true
	}
	y += u.px(30)
	if s.Microphone {
		if len(r.mics) == 0 && r.listed {
			u.label(u.face(12), rectAt(in.X+u.px(26), y, in.W-u.px(26), u.px(24)), u.t("No microphone found; the system default is used once one is plugged in"), colFaint, 0)
			y += u.px(28)
		} else if len(r.mics) > 0 {
			labels := []string{u.t("System default")}
			current := 0
			for i, m := range r.mics {
				labels = append(labels, m.Name)
				if m.ID == s.MicrophoneID {
					current = i + 1
				}
			}
			pick, used := u.flowChips("record/mics", rectAt(in.X+u.px(26), y, in.W-u.px(26), u.px(28)), labels, current)
			if pick != current {
				s.MicrophoneID, changed = "", true
				if pick > 0 {
					s.MicrophoneID = r.mics[pick-1].ID
				}
			}
			y += used + u.px(6)
		}
	}
	y += u.px(10)
	u.heading(rectAt(in.X, y, in.W, u.px(20)), u.t("While recording"))
	y += u.px(24)
	for _, o := range []struct {
		id, text string
		value    *bool
	}{
		{"record/cursor", u.t("Show the mouse pointer"), &s.Cursor},
		{"record/minimize", u.t("Minimize this window when recording starts"), &s.Minimize},
		{"record/hide", u.t("Leave this window out of the recording"), &s.HideWindow},
	} {
		if on := u.checkbox(o.id, rectAt(in.X, y, in.W, u.px(26)), o.text, *o.value); on != *o.value {
			*o.value, changed = on, true
		}
		y += u.px(30)
	}
	if changed {
		a.saveSettings()
		a.probeEncoder()
	}
	y += u.px(12)
	status, col := "", colDim
	switch reason, checking := a.recordBlocker(); {
	case reason != "":
		status, col = reason, colDanger
	case checking:
		status = u.t("Checking how this computer can record…")
	default:
		d, _ := a.chosenDisplay()
		e := r.encoders[encoderKey(d, s.FPS)]
		status = a.encoderName(e)
		if !e.Hardware() {
			col = colWarning
		}
	}
	y = a.paragraph(in.X, y, in.W, status, 12, col)
	dir, err := a.categoryDir(categoryTools)
	if err != nil {
		dir = err.Error()
	}
	y += u.px(10)
	line := rectAt(in.X, y, in.W-u.px(158), u.px(32))
	u.c.Fill(line, colSurface)
	u.label(u.face(12), line.Inset(u.px(10)), u.tf("Saved in %s", dir), colText, 0)
	u.hint("record/dir/path", u.t("Recordings open here for editing as soon as they stop"), line)
	if u.button("record/dir", rectAt(in.Right()-u.px(150), y+u.px(1), u.px(150), u.px(30)), icFolder, u.t("Choose folder…"), plain, true) {
		a.chooseResourceDir()
	}
	return y + u.px(32)
}
