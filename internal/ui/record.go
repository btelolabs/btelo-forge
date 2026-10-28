package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/btelolabs/btelo-video-cut/internal/capture"
	"github.com/btelolabs/btelo-video-cut/internal/gfx"
)

// recorderHost is what a window offers beyond Host for recording the screen. A window without it, or a system the
// capture package cannot record, shows no recording controls.
type recorderHost interface {
	Minimize()
	Restore()
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
	Minimize     bool   `json:"minimize"`
	HideWindow   bool   `json:"hideWindow"`
}

func defaultRecordSettings() recordSettings {
	return recordSettings{FPS: 30, Cursor: true, SystemAudio: true, Minimize: true, HideWindow: true}
}

var recordRates = []int{30, 60}

// recordAreas are the choices of what part of the screen to record.
var recordAreas = msgs("The whole screen", "A region, picked at the start")

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
	return a.capturePath(a.u.t("Screen recording %s"), ".mp4", at)
}

// capturePath is a new file in that folder, named by the translated format with the time filled in.
func (a *App) capturePath(format, ext string, at time.Time) (string, error) {
	dir, err := a.uploadDir()
	if err != nil {
		return "", err
	}
	base := fmt.Sprintf(format, at.Format("2006-01-02 15.04.05"))
	path := filepath.Join(dir, base+ext)
	for i := 2; ; i++ {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
		path = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
	}
}

// startRecording begins recording with the saved settings; done, when not nil, hears whether it started.
func (a *App) startRecording(done func(path string, err error)) {
	a.startRecordingIn(image.Rectangle{}, false, done)
}

// startRecordingRegion begins recording a region already picked (empty for the whole display).
func (a *App) startRecordingRegion(region image.Rectangle, done func(path string, err error)) {
	a.startRecordingIn(region, true, done)
}

// errRegionCancelled means the user cancelled picking the region to record or capture.
var errRegionCancelled = errors.New("no region was chosen")

func (a *App) startRecordingIn(region image.Rectangle, pickedRegion bool, done func(path string, err error)) {
	r := &a.rec
	report := func(path string, err error) {
		if done != nil {
			done(path, err)
		}
	}
	host := a.recorder()
	switch {
	case host == nil:
		report("", capture.ErrUnsupported)
		return
	case a.recording():
		report("", errors.New(a.u.t("A recording is already running")))
		return
	}
	if reason, _ := a.recordBlocker(); reason != "" {
		report("", errors.New(reason))
		return
	}
	d, ok := a.chosenDisplay()
	if !ok {
		err := r.listErr
		if err == nil {
			err = errors.New(a.u.t("No screen to record was found"))
		}
		report("", err)
		return
	}
	if a.settings.Record.Region && !pickedRegion {
		r.open = false
		a.pickRegion(d, func(region image.Rectangle, ok bool) {
			if !ok {
				report("", errRegionCancelled)
				return
			}
			a.startRecordingRegion(region, done)
		})
		return
	}
	path, err := a.recordingPath(time.Now())
	if err != nil {
		report("", err)
		return
	}
	s := a.settings.Record
	o := capture.Options{Display: d, FPS: s.FPS, Cursor: s.Cursor, SystemAudio: s.SystemAudio, Microphone: s.Microphone, MicrophoneID: s.MicrophoneID,
		Region: region, Path: path}
	e, known := r.encoders[encoderKey(d, s.FPS)]
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
			r.rec = rec
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
	if a.recording() || a.over.bar != nil || a.over.pick != nil {
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

// drawRecordControl is the recording button at the end of the tab strip, or the running recording's time, sound
// levels and buttons; it returns where it begins.
func (a *App) drawRecordControl(end, y, h float32) float32 {
	u := &a.u
	r := &a.rec
	if a.recorder() == nil {
		return end
	}
	reason, checking := a.recordBlocker()
	b, shot := a.recordButtons(end, y, h)
	if r.rec == nil {
		label := u.t("Record")
		hint := withKey(u.t("Record the screen"), a.hotkeyLabel(hotkeyRecord))
		switch {
		case reason != "":
			hint = u.tf("Screen recording is not available: %s", reason)
		case checking:
			hint = u.t("Checking how this computer can record…")
		}
		if u.button("tabs/record", b, icRecord, label, plain, !r.starting && a.export == nil) {
			a.openRecorder()
		}
		u.hint("tabs/record", hint, b)
		if reason != "" {
			// A small warning mark: the button still opens the dialog that explains.
			u.c.Circle(b.Right()-u.px(7), b.Y+u.px(7), u.px(3), colWarning)
		} else if r.starting {
			u.c.Circle(b.Right()-u.px(7), b.Y+u.px(7), u.px(3), colDanger)
		}
		a.drawScreenshotButton(shot)
		return b.X
	}
	a.drawScreenshotButton(shot)
	end = shot.X - u.px(4)
	st := r.rec.Status()
	pill := gfx.Rect{X: end - u.px(236), Y: y, W: u.px(232), H: h}
	u.c.RoundRect(pill, h/2, gfx.Mix(colSurface, colDanger, 0.18))
	dot := colDanger
	if st.Paused {
		dot = colDim
	} else if time.Now().UnixMilli()/500%2 == 0 {
		dot = gfx.Mix(colDanger, colPanel, 0.5)
	}
	u.c.Circle(pill.X+u.px(14), pill.Y+h/2, u.px(4.5), dot)
	u.label(u.bold(12.5), rectAt(pill.X+u.px(24), pill.Y, u.px(56), h), shortClock(st.Elapsed.Seconds()), colText, 0)
	switch {
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
	meters := rectAt(pill.X+u.px(136), pill.Y+h/2-u.px(3)*float32(len(levels)), u.px(24), u.px(6)*float32(len(levels)))
	for i, level := range levels {
		bar := rectAt(meters.X, meters.Y+float32(i)*u.px(6), meters.W, u.px(4))
		u.c.RoundRect(bar, u.px(2), colSurfaceHi)
		u.c.RoundRect(rectAt(bar.X, bar.Y, max(bar.W*level, 0), bar.H), u.px(2), colAccent)
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

// recordButtons are where the tab strip puts the labelled recording button and, right of it, the screenshot button,
// ending at end.
func (a *App) recordButtons(end, y, h float32) (record, shot gfx.Rect) {
	u := &a.u
	shot = gfx.Rect{X: end - u.px(36), Y: y, W: u.px(32), H: h}
	w := u.buttonWidth(icRecord, u.t("Record"))
	return gfx.Rect{X: shot.X - u.px(4) - w, Y: y, W: w, H: h}, shot
}

// drawScreenshotButton takes a screenshot of the chosen screen.
func (a *App) drawScreenshotButton(b gfx.Rect) {
	u := &a.u
	hint := withKey(u.t("Take a screenshot of the screen chosen for recording"), a.hotkeyLabel(hotkeyScreenshot))
	if u.iconButton("tabs/screenshot", b, icCamera, hint, a.rec.shooting, !a.rec.shooting && a.over.pick == nil) {
		a.screenshotRegion()
	}
}

// screenshotRegion lets the user pick a region of the chosen screen, or click for all of it, and saves it.
func (a *App) screenshotRegion() {
	d, ok := a.chosenDisplay()
	if !ok || a.rec.shooting {
		a.takeScreenshot(image.Rectangle{}, nil) // says why it cannot
		return
	}
	a.pickRegion(d, func(region image.Rectangle, ok bool) {
		if ok {
			a.takeScreenshot(region, nil)
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

// takeScreenshot saves the chosen screen as a PNG in the folder recordings go to, leaving this window out when the
// recording settings say so; done, when not nil, hears the file.
func (a *App) takeScreenshot(region image.Rectangle, done func(path string, err error)) {
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
	d, ok := a.chosenDisplay()
	if !ok {
		report("", errors.New(a.u.t("No screen to record was found")))
		return
	}
	path, err := a.capturePath(a.u.t("Screenshot %s"), ".png", time.Now())
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
		a.post(func() {
			r.shooting = false
			if hide {
				host.ExcludeFromCapture(false)
			}
			if err != nil {
				report("", err)
				return
			}
			actions := []toastAction{{a.u.t("Edit"), func() { a.openImageAsArt(path) }}, {a.u.t("Show in folder"), func() { a.host.Reveal(path) }}}
			if a.p != nil {
				actions = append([]toastAction{{a.u.t("Add to the edit"), func() { a.addImage(path) }}}, actions...)
			}
			a.toastActions(a.u.tf("Saved the screenshot %s", filepath.Base(path)), false, actions)
			report(path, nil)
		})
	})
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
	dir, err := a.uploadDir()
	if err != nil {
		dir = err.Error()
	}
	y += u.px(10)
	line := rectAt(in.X, y, in.W-u.px(158), u.px(32))
	u.c.RoundRect(line, u.px(7), colSurface)
	u.label(u.face(12), line.Inset(u.px(10)), u.tf("Saved in %s", dir), colText, 0)
	u.hint("record/dir/path", u.t("Recordings open here for editing as soon as they stop"), line)
	if u.button("record/dir", rectAt(in.Right()-u.px(150), y+u.px(1), u.px(150), u.px(30)), icFolder, u.t("Choose folder…"), plain, true) {
		a.chooseExportDir()
	}
	return y + u.px(32)
}
