package ui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/capture"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/vault"
)

// Live streaming works as OBS Studio's outputs do: the stream is its own output, with a canvas the platforms see
// from start to end, encoded once and sent to every destination. Recording is another output. The stream shows the
// screen while a recording runs and black between recordings, and goes on through both: stopping a recording, or
// starting the next on another part of the screen, never interrupts it.

const (
	icLive    = "\ue639" // live_tv
	icLaunch  = "\ue89e" // open_in_new
	icVisible = "\ue8f4" // visibility
	icHidden  = "\ue8f5" // visibility_off
)

// liveSettings are the stream's choices, kept between runs.
type liveSettings struct {
	Destinations []liveDestination `json:"destinations"`
	Width        int               `json:"width"` // the canvas
	Height       int               `json:"height"`
	FPS          int               `json:"fps"`
	Kbps         int               `json:"kbps"`
	SystemAudio  bool              `json:"systemAudio"`
	Microphone   bool              `json:"microphone"`
	MicrophoneID string            `json:"microphoneId"` // "" follows the system's default microphone
	// Record starts recording the screen when the stream starts without a recording, so that it is on the stream.
	Record bool `json:"record"`
}

// liveDestination is a place the user streams to.
type liveDestination struct {
	ID       string     `json:"id"`
	Platform string     `json:"platform"` // a platform id; one no longer configured counts as a custom server
	Name     string     `json:"name,omitempty"`
	Server   string     `json:"server"`
	Key      sealedText `json:"key,omitempty"`
	On       bool       `json:"on"` // streamed to when the stream starts
}

// sealedText is a secret kept in the settings file sealed for this user (see package vault). One sealed by another
// user or on another computer reads as empty, and the destination asks for its key again.
type sealedText string

func (s sealedText) MarshalJSON() ([]byte, error) {
	sealed, err := vault.Seal(string(s))
	if err != nil {
		return nil, err
	}
	return json.Marshal(sealed)
}

func (s *sealedText) UnmarshalJSON(data []byte) error {
	var sealed string
	if err := json.Unmarshal(data, &sealed); err != nil {
		return err
	}
	plain, err := vault.Open(sealed)
	if err != nil {
		plain = ""
	}
	*s = sealedText(plain)
	return nil
}

// liveCanvases are the canvas sizes offered: landscape for computers and televisions, upright for phone platforms.
var liveCanvases = []image.Point{{1920, 1080}, {1280, 720}, {1080, 1920}, {720, 1280}}

var (
	liveRates = []int{30, 60}
	liveKbps  = []int{2500, 4500, 6000, 8000}
)

func defaultLiveSettings() liveSettings {
	return liveSettings{Width: 1920, Height: 1080, FPS: 30, Kbps: 4500, SystemAudio: true, Record: true}
}

// canvas is the chosen canvas, or the default for a size not offered.
func (s liveSettings) canvas() image.Point {
	c := image.Pt(s.Width, s.Height)
	if !slices.Contains(liveCanvases, c) {
		return liveCanvases[0]
	}
	return c
}

func (s liveSettings) fps() int {
	if !slices.Contains(liveRates, s.FPS) {
		return liveRates[0]
	}
	return s.FPS
}

func (s liveSettings) kbps() int {
	if s.Kbps < 500 || s.Kbps > 50000 {
		return 4500
	}
	return s.Kbps
}

// liveState is the stream in progress and what its tab shows.
type liveState struct {
	platforms *capture.Platforms
	stream    *capture.Streamer
	starting  bool
	stopping  bool
	encoders  map[string]capture.Encoder // by canvas and frame rate
	states    map[string]capture.TargetState
	screenErr string // why the screen stopped showing on the stream, as last reported
	edit      *liveEdit
	scroll    float32
	content   float32
	preview   *image.RGBA // the stream's picture as last shown in its tab
	previewAt time.Time
	// Frames the encoder took late: a frame now and then is the system's timer; many in a row mean the computer
	// cannot keep up. The count is taken every few seconds.
	lateAt     time.Time
	lateBefore int
	lateRecent int
}

// liveEdit is a destination being added or changed in the stream's tab.
type liveEdit struct {
	index  int // in the settings; -1 for a new one
	d      liveDestination
	key    string
	other  bool // the server is typed rather than chosen from the platform's
	reveal bool
}

func newLiveID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// streaming reports whether a stream is running, starting or stopping.
func (a *App) streaming() bool { return a.onAir.stream != nil || a.onAir.starting }

// platform is a destination's platform, or a custom server when its platform is no longer configured.
func (a *App) platform(id string) capture.Platform {
	if p, ok := a.onAir.platforms.Find(id); ok {
		return p
	}
	if p, ok := a.onAir.platforms.Find("custom"); ok {
		return p
	}
	return capture.Platform{ID: id}
}

// destinationName is how a destination is called: the user's name for it, or its platform's.
func (a *App) destinationName(d liveDestination) string {
	if strings.TrimSpace(d.Name) != "" {
		return strings.TrimSpace(d.Name)
	}
	if name := a.platform(d.Platform).Name.In(string(a.u.lang)); name != "" {
		return name
	}
	return a.u.t("Custom server")
}

// target is a destination as the stream sends to it.
func (a *App) target(d liveDestination) capture.Target {
	return capture.Target{ID: d.ID, Name: a.destinationName(d), URL: capture.StreamURL(d.Server, string(d.Key))}
}

// destinationProblem says what keeps a destination from being streamed to, or "".
func (a *App) destinationProblem(d liveDestination) string {
	if strings.TrimSpace(d.Server) == "" {
		return a.u.t("No server address")
	}
	if err := a.target(d).Check(); err != nil {
		return a.describe(err)
	}
	return ""
}

func encoderKeyFor(canvas image.Point, fps int) string {
	return fmt.Sprintf("%dx%d@%d", canvas.X, canvas.Y, fps)
}

// liveBlocker says why a stream cannot start now, or "".
func (a *App) liveBlocker() string {
	l := &a.onAir
	switch {
	case !capture.Supported():
		return a.u.t("Live streaming works on Windows")
	case a.tools.err != nil:
		text, _ := a.toolsStatus()
		return text
	case !a.tools.ready:
		return a.describe(errToolsNotReady)
	case l.stream != nil || l.starting:
		return a.u.t("The stream is already live")
	}
	on := 0
	for _, d := range a.settings.Live.Destinations {
		if !d.On {
			continue
		}
		if p := a.destinationProblem(d); p != "" {
			return a.destinationName(d) + ": " + p
		}
		on++
	}
	if on == 0 {
		return a.describe(capture.ErrNoTargets)
	}
	return ""
}

// startLive starts streaming to the chosen destinations; done, when not nil, hears whether it started.
func (a *App) startLive(done func(error)) {
	l := &a.onAir
	report := func(err error) {
		if done != nil {
			done(err)
		}
	}
	if reason := a.liveBlocker(); reason != "" {
		report(errors.New(reason))
		return
	}
	s := a.settings.Live
	o := capture.StreamOptions{Size: s.canvas(), FPS: s.fps(), Kbps: s.kbps(), SystemAudio: s.SystemAudio, Microphone: s.Microphone, MicrophoneID: s.MicrophoneID}
	for _, d := range s.Destinations {
		if d.On {
			o.Targets = append(o.Targets, a.target(d))
		}
	}
	key := encoderKeyFor(o.Size, o.FPS)
	e, known := l.encoders[key]
	l.starting, l.edit = true, nil
	a.background(func(ctx context.Context) {
		var err error
		if !known {
			e, err = capture.ProbeStreamEncoder(ctx, o.Size, o.FPS)
		}
		var st *capture.Streamer
		if err == nil {
			o.Encoder = e
			st, err = capture.StartStream(o)
		}
		a.post(func() {
			l.starting = false
			if err != nil {
				a.toast(a.u.tf("The stream could not start: %s", a.describe(err)), true)
				report(err)
				return
			}
			if l.encoders == nil {
				l.encoders = map[string]capture.Encoder{}
			}
			l.encoders[key] = e
			l.stream, l.states = st, map[string]capture.TargetState{}
			l.lateAt, l.lateBefore, l.lateRecent = time.Now(), 0, 0
			if a.rec.rec != nil {
				a.liveFollow()
			} else if s.Record && a.recorder() != nil && !a.rec.starting {
				a.startRecording(func(_ string, err error) {
					if err != nil && !errors.Is(err, errRegionCancelled) {
						a.toast(a.u.tf("Recording could not start: %s", a.describe(err)), true)
					}
				})
			}
			if len(o.Targets) == 1 {
				a.liveToast(a.u.tf("Live on %s", o.Targets[0].Name))
			} else {
				a.liveToast(a.u.tf("Live on %d destinations", len(o.Targets)))
			}
			report(nil)
		})
	})
}

// liveToast tells that the stream went live or ended, unless its tab is in front, whose badge says so already.
func (a *App) liveToast(text string) {
	if !a.liveTab {
		a.toast(text, false)
	}
}

// stopLive ends the stream; the recording, if any, goes on.
func (a *App) stopLive(done func(error)) {
	l := &a.onAir
	st := l.stream
	if st == nil || l.stopping {
		if done != nil {
			done(errors.New(a.u.t("The stream is not live")))
		}
		return
	}
	l.stopping = true
	a.background(func(context.Context) {
		err := st.Stop()
		a.post(func() {
			l.stream, l.stopping, l.states = nil, false, nil
			a.liveToast(a.u.t("The stream has ended"))
			if done != nil {
				done(err)
			}
		})
	})
}

// liveFollow puts what is being recorded on the stream, or black while nothing is; it is called whenever a
// recording starts or stops.
func (a *App) liveFollow() {
	st := a.onAir.stream
	if st == nil || a.onAir.stopping {
		return
	}
	if a.rec.rec == nil || a.rec.stopping {
		st.ShowNothing()
		return
	}
	if err := st.ShowScreen(a.rec.rec.Options()); err != nil {
		a.toast(a.u.tf("The recording could not be shown on the stream: %s", a.describe(err)), true)
	}
}

// setDestinationOn chooses whether a destination is streamed to; while live it joins or leaves the stream at once.
func (a *App) setDestinationOn(i int, on bool) error {
	ds := a.settings.Live.Destinations
	if i < 0 || i >= len(ds) {
		return errors.New("no such destination")
	}
	d := ds[i]
	if on && a.destinationProblem(d) != "" {
		return errors.New(a.destinationProblem(d))
	}
	if st := a.onAir.stream; st != nil && on != d.On {
		var err error
		if on {
			err = st.AddTarget(a.target(d))
		} else {
			err = st.RemoveTarget(d.ID)
		}
		if err != nil {
			return err
		}
	}
	ds[i].On = on
	a.saveSettings()
	return nil
}

// removeDestination forgets a destination; while live it leaves the stream.
func (a *App) removeDestination(i int) error {
	ds := a.settings.Live.Destinations
	if i < 0 || i >= len(ds) {
		return errors.New("no such destination")
	}
	if st := a.onAir.stream; st != nil && ds[i].On {
		if err := st.RemoveTarget(ds[i].ID); err != nil {
			return err
		}
	}
	a.settings.Live.Destinations = slices.Delete(ds, i, i+1)
	a.saveSettings()
	return nil
}

// saveDestination adds a destination or replaces one, keeping its place; while live a changed destination that is
// streamed to reconnects with its new address.
func (a *App) saveDestination(index int, d liveDestination) error {
	if p := a.destinationProblem(d); p != "" {
		return errors.New(p)
	}
	ds := &a.settings.Live.Destinations
	if index < 0 || index >= len(*ds) {
		if d.ID == "" {
			d.ID = newLiveID()
		}
		*ds = append(*ds, d)
		a.saveSettings()
		return nil
	}
	old := (*ds)[index]
	d.ID = old.ID
	if st := a.onAir.stream; st != nil && old.On {
		st.RemoveTarget(old.ID)
		if d.On {
			if err := st.AddTarget(a.target(d)); err != nil {
				d.On = false
				(*ds)[index] = d
				a.saveSettings()
				return err
			}
		}
	}
	(*ds)[index] = d
	a.saveSettings()
	return nil
}

// liveTick looks after a running stream every frame, also while the window is minimized: a stream whose encoder
// failed ends, and a destination that is given up is reported.
func (a *App) liveTick() {
	l := &a.onAir
	st := l.stream
	if st == nil || l.stopping {
		return
	}
	select {
	case <-st.Ended():
		a.toast(a.u.tf("The stream stopped: %s", a.describe(st.Err())), true)
		a.stopLive(nil)
		return
	default:
	}
	status := st.Status()
	// The screen that cannot be shown leaves the stream black: that must never pass unnoticed.
	why := ""
	if status.ScreenErr != nil {
		why = a.describe(status.ScreenErr)
	}
	if why != l.screenErr && why != "" {
		a.toast(a.u.tf("The screen could not be shown on the stream, which is black: %s", why), true)
	}
	l.screenErr = why
	for _, t := range status.Targets {
		if l.states[t.ID] != t.State && t.State == capture.TargetFailed {
			a.toast(a.u.tf("%s could not be reached and is no longer tried: %s", t.Name, t.Err), true)
		}
		l.states[t.ID] = t.State
	}
}

// finishLiveForClose ends a stream still live when the app closes.
func (a *App) finishLiveForClose() {
	if st := a.onAir.stream; st != nil && !a.onAir.stopping {
		st.Stop()
	}
}

// targetStatus is a destination's state while live, if it is in the stream.
func (a *App) targetStatus(id string) (capture.TargetStatus, bool) {
	if a.onAir.stream == nil {
		return capture.TargetStatus{}, false
	}
	for _, t := range a.onAir.stream.Status().Targets {
		if t.ID == id {
			return t, true
		}
	}
	return capture.TargetStatus{}, false
}

// describeTarget says in words how a destination is doing, with the colour to show it in.
func (a *App) describeTarget(t capture.TargetStatus) (string, color.RGBA) {
	u := &a.u
	switch t.State {
	case capture.TargetLive:
		return u.tf("Live · %.1f Mb/s", t.Kbps/1000), colSuccess
	case capture.TargetConnecting:
		return u.t("Connecting…"), colDim
	case capture.TargetRetrying:
		wait := max(time.Until(t.Retry).Seconds(), 0)
		return u.tf("Retrying in %.0f s: %s", wait, t.Err), colWarning
	case capture.TargetFailed:
		return u.tf("Not reached: %s", t.Err), colDangerText
	}
	return u.t("Stopped"), colDim
}

// liveCard is the new tab's card for streaming, with a button beside its action for the camera on the screen.
func (a *App) liveCard() startCard {
	u := &a.u
	l := &a.onAir
	hint := ""
	if !capture.Supported() {
		hint = u.t("Live streaming works on Windows")
	}
	action := u.t("Go live…")
	if l.stream != nil {
		action = u.t("See the live stream…")
	}
	_, overlays := a.host.(overlayHost)
	cameraHint := u.t("Show your camera on the screen, for recordings and streams")
	if a.cam.bubble != nil {
		cameraHint = u.t("The camera is on the screen — open its settings")
	}
	return startCard{id: "empty/live", glyph: icLive, hue: gfx.Hex(0xe8618c), title: u.t("Go live"),
		text:   u.t("Stream the screen to several platforms at once, such as Bilibili, Douyin and YouTube, with your camera in a corner."),
		action: action, enabled: capture.Supported() && !l.starting && a.export == nil, hint: hint, run: a.openLive,
		extra: &startExtra{id: "empty/camera", glyph: icVideo, hint: cameraHint, enabled: capture.Supported() && overlays && a.export == nil,
			run: func() {
				a.cam.open = true
				a.listCameras(nil)
			}}}
}

// liveControlRect is where the tab strip puts the live stream's pill — its time, destinations and stop button —
// ending at end. It is empty while nothing is streamed, since streaming starts from the new tab's card, and while
// the stream's own tab is in front, which shows all of that.
func (a *App) liveControlRect(end, y, h float32) gfx.Rect {
	if a.onAir.stream == nil || a.liveTab {
		return gfx.Rect{X: end, Y: y, H: h}
	}
	return gfx.Rect{X: end - a.u.px(4) - a.u.px(178), Y: y, W: a.u.px(178), H: h}
}

// drawLiveControl is, while live, the stream's time, destinations and stop button in the tab strip, so that it can
// be seen and ended from any tab; it returns where it begins.
func (a *App) drawLiveControl(end, y, h float32) float32 {
	u := &a.u
	l := &a.onAir
	if l.stream == nil || a.liveTab {
		return end
	}
	st := l.stream.Status()
	live := 0
	for _, t := range st.Targets {
		if t.State == capture.TargetLive {
			live++
		}
	}
	pill := a.liveControlRect(end, y, h)
	if u.clicked("tabs/live/pill", rectAt(pill.X, pill.Y, pill.W-u.px(32), pill.H)) {
		a.openLive()
	}
	u.c.Fill(pill, gfx.Mix(colSurface, colDanger, 0.18))
	badge := rectAt(pill.X+u.px(6), pill.Y+(h-u.px(18))/2, u.px(38), u.px(18))
	u.c.Fill(badge, colDanger)
	u.label(u.bold(10.5), badge, u.t("LIVE"), colWhite, 0.5)
	u.label(u.bold(12.5), rectAt(badge.Right()+u.px(8), pill.Y, u.px(52), h), shortClock(st.Elapsed.Seconds()), colText, 0)
	col := colSuccess
	if live < len(st.Targets) {
		col = colWarning
	}
	if live == 0 {
		col = colDangerText
	}
	u.label(u.face(11.5), rectAt(badge.Right()+u.px(60), pill.Y, u.px(40), h), fmt.Sprintf("%d/%d", live, len(st.Targets)), col, 0)
	hint := u.tn(len(st.Targets), "Live on %d of %d destination — open to see it", "Live on %d of %d destinations — open to see each", live, len(st.Targets))
	if !st.Screen {
		hint += "\n" + u.t("The stream shows black: nothing is being recorded")
	}
	u.hint("tabs/live/pill", hint, pill)
	stop := rectAt(pill.Right()-u.px(32), pill.Y+u.px(2), u.px(28), h-u.px(4))
	if u.iconButton("tabs/live/stop", stop, icStop, u.t("End the stream"), false, !l.stopping) {
		a.stopLive(nil)
	}
	return pill.X
}

// liveBody draws the destinations and the stream's choices, and returns the bottom of them.
func (a *App) liveBody(in gfx.Rect) float32 {
	l := &a.onAir
	u := &a.u
	s := &a.settings.Live
	y := in.Y
	u.heading(rectAt(in.X, y+u.px(5), in.W-u.px(200), u.px(20)), u.t("Destinations"))
	if u.button("live/add", rectAt(in.Right()-u.px(190), y, u.px(182), u.px(30)), icAdd, u.t("Add destination"), plain, true) {
		first := a.onAir.platforms.Platforms[0]
		l.edit = &liveEdit{index: -1, d: liveDestination{Platform: first.ID, On: true}}
		if len(first.Servers) > 0 {
			l.edit.d.Server = first.Servers[0].URL
		}
	}
	y += u.px(38)
	if len(s.Destinations) == 0 {
		y = a.paragraph(in.X, y, in.W-u.px(8), u.t("Add the platforms to stream to. Every destination gets the same stream, encoded once, so streaming to several costs no more of the computer than streaming to one — only more upload."), 12.5, colDim)
	}
	for i := 0; i < len(s.Destinations); i++ {
		d := s.Destinations[i]
		row := rectAt(in.X, y, in.W-u.px(8), u.px(54))
		u.c.Fill(row, colSurface)
		id := "live/dest/" + d.ID
		problem := a.destinationProblem(d)
		if l.stream == nil {
			if on := u.checkbox(id+"/on", rectAt(row.X+u.px(10), row.Y+u.px(14), u.px(26), u.px(26)), "", d.On); on != d.On {
				if err := a.setDestinationOn(i, on); err != nil {
					a.toast(a.describe(err), true)
				}
			}
		}
		text := rectAt(row.X+u.px(42), row.Y+u.px(8), u.liveNameWidth(row.W), u.px(20))
		u.label(u.bold(13), text, a.destinationName(d), colText, 0)
		detail, col := a.platform(d.Platform).Name.In(string(u.lang))+" · "+serverHost(d.Server), colFaint
		switch {
		case problem != "":
			detail, col = problem, colWarning
		case d.Key == "":
			detail, col = u.t("No stream key — the address must hold it"), colWarning
		}
		if t, ok := a.targetStatus(d.ID); ok {
			detail, col = a.describeTarget(t)
		} else if l.stream != nil {
			detail, col = u.t("Not in this stream"), colFaint
		}
		u.label(u.face(11.5), rectAt(text.X, text.Y+u.px(20), row.Right()-text.X-u.px(90), u.px(18)), detail, col, 0)
		bx := row.Right() - u.px(38)
		if l.stream == nil {
			if u.iconButton(id+"/remove", rectAt(bx, row.Y+u.px(12), u.px(30), u.px(30)), icDelete, u.t("Remove this destination"), false, true) {
				if err := a.removeDestination(i); err != nil {
					a.toast(a.describe(err), true)
				}
				i--
				continue
			}
			bx -= u.px(34)
			if u.iconButton(id+"/edit", rectAt(bx, row.Y+u.px(12), u.px(30), u.px(30)), icPencil, u.t("Change this destination"), false, true) {
				l.edit = a.editFor(i)
			}
		} else {
			t, inStream := a.targetStatus(d.ID)
			switch {
			case inStream && (t.State == capture.TargetRetrying || t.State == capture.TargetFailed):
				if u.iconButton(id+"/retry", rectAt(bx-u.px(34), row.Y+u.px(12), u.px(30), u.px(30)), icRestore, u.t("Try again now"), false, true) {
					l.stream.RetryTarget(d.ID)
				}
				fallthrough
			case inStream:
				if u.iconButton(id+"/leave", rectAt(bx, row.Y+u.px(12), u.px(30), u.px(30)), icStop, u.t("Stop streaming here; the others go on"), false, true) {
					if err := a.setDestinationOn(i, false); err != nil {
						a.toast(a.describe(err), true)
					}
				}
			default:
				if u.iconButton(id+"/join", rectAt(bx, row.Y+u.px(12), u.px(30), u.px(30)), icPlay, u.t("Stream here too"), false, problem == "") {
					if err := a.setDestinationOn(i, true); err != nil {
						a.toast(a.describe(err), true)
					}
				}
			}
		}
		y += u.px(58)
	}
	changed := false
	enabled := l.stream == nil && !l.starting
	y += u.px(14)
	u.heading(rectAt(in.X, y, in.W, u.px(20)), u.t("Picture"))
	y += u.px(24)
	y = a.paragraph(in.X, y, in.W-u.px(8), u.t("The stream shows the screen while it is being recorded, fitted in with black bars, and black between recordings. Stopping a recording, or recording another part of the screen, never interrupts the stream."), 12, colDim)
	y += u.px(8)
	canvases := []string{u.t("1920×1080 landscape"), u.t("1280×720 landscape"), u.t("1080×1920 upright"), u.t("720×1280 upright")}
	current := max(slices.Index(liveCanvases, s.canvas()), 0)
	pick, used := u.flowChips("live/canvas", rectAt(in.X, y, in.W-u.px(8), u.px(30)), canvases, current)
	if pick != current && enabled {
		s.Width, s.Height, changed = liveCanvases[pick].X, liveCanvases[pick].Y, true
	}
	y += used + u.px(8)
	rates := make([]string, len(liveRates))
	for i, fps := range liveRates {
		rates[i] = u.tf("%d frames a second", fps)
	}
	current = max(slices.Index(liveRates, s.fps()), 0)
	if pick := u.chips("live/fps", rectAt(in.X, y, min(in.W, u.px(320)), u.px(30)), rates, current, enabled); pick != current {
		s.FPS, changed = liveRates[pick], true
	}
	y += u.px(38)
	kbps := make([]string, len(liveKbps))
	for i, k := range liveKbps {
		kbps[i] = u.tf("%d kb/s", k)
	}
	current = slices.Index(liveKbps, s.kbps())
	if pick := u.chips("live/kbps", rectAt(in.X, y, min(in.W, u.px(440)), u.px(30)), kbps, current, enabled); pick != current && pick >= 0 {
		s.Kbps, changed = liveKbps[pick], true
	}
	y += u.px(36)
	for _, note := range a.liveNotes() {
		y = a.paragraph(in.X, y, in.W-u.px(8), note.text, 12, note.col) + u.px(2)
	}
	y += u.px(12)
	u.heading(rectAt(in.X, y, in.W, u.px(20)), u.t("Sound"))
	y += u.px(24)
	for _, o := range []struct {
		id, text string
		value    *bool
	}{
		{"live/system", u.t("Sound playing on this computer"), &s.SystemAudio},
		{"live/mic", u.t("Microphone"), &s.Microphone},
	} {
		if on := u.checkbox(o.id, rectAt(in.X, y, in.W, u.px(26)), o.text, *o.value); on != *o.value && enabled {
			*o.value, changed = on, true
		}
		y += u.px(30)
	}
	if s.Microphone && len(a.rec.mics) > 0 {
		labels := []string{u.t("System default")}
		current := 0
		for i, m := range a.rec.mics {
			labels = append(labels, m.Name)
			if m.ID == s.MicrophoneID {
				current = i + 1
			}
		}
		pick, used := u.flowChips("live/mics", rectAt(in.X+u.px(26), y, in.W-u.px(34), u.px(28)), labels, current)
		if pick != current && enabled {
			s.MicrophoneID, changed = "", true
			if pick > 0 {
				s.MicrophoneID = a.rec.mics[pick-1].ID
			}
		}
		y += used + u.px(6)
	}
	if a.recorder() != nil && l.stream == nil {
		y += u.px(6)
		on, h := u.checkboxWrapped("live/record", in.X, y, in.W-u.px(8), u.t("Start recording the screen too, so that it is on the stream"), s.Record)
		if on != s.Record {
			s.Record, changed = on, true
		}
		y += h + u.px(4)
	}
	if changed {
		a.saveSettings()
	}
	return y
}

// liveNote is a line under the picture's choices.
type liveNote struct {
	text string
	col  color.RGBA
}

// liveNotes are what the chosen destinations say about the picture: platforms watched upright on a landscape canvas
// or the other way round, bit rates above what a platform takes, and the upload all of them need.
func (a *App) liveNotes() []liveNote {
	u := &a.u
	s := a.settings.Live
	upright := s.canvas().Y > s.canvas().X
	var notes []liveNote
	on := 0
	for _, d := range s.Destinations {
		if !d.On {
			continue
		}
		on++
		p := a.platform(d.Platform)
		name := a.destinationName(d)
		if p.Portrait && !upright {
			notes = append(notes, liveNote{u.tf("%s is watched upright on phones; an upright canvas fills its screen", name), colWarning})
		}
		if p.MaxKbps > 0 && s.kbps() > p.MaxKbps {
			notes = append(notes, liveNote{u.tf("%s takes at most %d kb/s; choose a lower rate", name, p.MaxKbps), colWarning})
		}
	}
	total := float64(on*(s.kbps()+160)) / 1000
	switch {
	case on == 1:
		notes = append(notes, liveNote{u.tf("Uploads about %.1f Mb/s", total), colFaint})
	case on > 1:
		notes = append(notes, liveNote{u.tf("Uploads about %.1f Mb/s in all for %d destinations", total, on), colFaint})
	}
	return notes
}

// editFor starts changing a destination.
func (a *App) editFor(i int) *liveEdit {
	d := a.settings.Live.Destinations[i]
	e := &liveEdit{index: i, d: d, key: string(d.Key)}
	e.other = !slices.ContainsFunc(a.platform(d.Platform).Servers, func(s capture.Server) bool { return s.URL == d.Server })
	return e
}

// liveEditor draws the destination being added or changed, and returns the bottom of it.
func (a *App) liveEditor(in gfx.Rect) float32 {
	u := &a.u
	e := a.onAir.edit
	lang := string(u.lang)
	y := in.Y
	u.heading(rectAt(in.X, y, in.W, u.px(20)), u.t("Platform"))
	y += u.px(26)
	platforms := a.onAir.platforms.Platforms
	names := make([]string, len(platforms))
	current := -1
	for i, p := range platforms {
		names[i] = p.Name.In(lang)
		if p.ID == e.d.Platform {
			current = i
		}
	}
	pick, used := u.flowChips("live/edit/platform", rectAt(in.X, y, in.W-u.px(8), u.px(30)), names, current)
	if pick != current && pick >= 0 {
		p := platforms[pick]
		e.d.Platform, e.other = p.ID, len(p.Servers) == 0
		e.d.Server = ""
		if len(p.Servers) > 0 {
			e.d.Server = p.Servers[0].URL
		}
	}
	y += used + u.px(8)
	p := a.platform(e.d.Platform)
	if help := p.Help.In(lang); help != "" {
		y = a.paragraph(in.X, y, in.W-u.px(8), help, 12.5, colDim) + u.px(4)
	}
	if p.Page != "" {
		label := u.tf("Open %s", serverHost(p.Page))
		if u.button("live/edit/page", rectAt(in.X, y, u.buttonWidth(icLaunch, label), u.px(28)), icLaunch, label, ghost, true) {
			if err := a.host.LaunchURL(p.Page); err != nil {
				a.toast(err.Error(), true)
			}
		}
		y += u.px(34)
	}
	y += u.px(8)
	u.heading(rectAt(in.X, y, in.W, u.px(20)), u.t("Server"))
	y += u.px(26)
	if len(p.Servers) > 0 {
		labels := make([]string, 0, len(p.Servers)+1)
		current := len(p.Servers)
		for i, s := range p.Servers {
			labels = append(labels, s.Name.In(lang))
			if !e.other && s.URL == e.d.Server {
				current = i
			}
		}
		labels = append(labels, u.t("Another address"))
		pick, used := u.flowChips("live/edit/servers", rectAt(in.X, y, in.W-u.px(8), u.px(30)), labels, current)
		if pick != current {
			e.other = pick == len(p.Servers)
			if !e.other {
				e.d.Server = p.Servers[pick].URL
			}
		}
		y += used + u.px(6)
	}
	if len(p.Servers) == 0 || e.other {
		u.textBox("live/edit/server", rectAt(in.X, y, in.W-u.px(8), u.px(34)), &e.d.Server, u.t("rtmp://… — the server address the platform gives"), false)
		y += u.px(42)
	} else {
		u.label(u.face(12), rectAt(in.X, y, in.W-u.px(8), u.px(20)), e.d.Server, colFaint, 0)
		y += u.px(26)
	}
	u.heading(rectAt(in.X, y, in.W, u.px(20)), u.t("Stream key"))
	y += u.px(26)
	field := rectAt(in.X, y, in.W-u.px(8)-u.px(40), u.px(34))
	if e.reveal {
		u.textBox("live/edit/key", field, &e.key, u.t("Paste the stream key"), false)
	} else {
		u.secretBox("live/edit/key", field, &e.key, u.t("Paste the stream key"))
	}
	glyph, hint := icVisible, u.t("Show the key")
	if e.reveal {
		glyph, hint = icHidden, u.t("Hide the key")
	}
	if u.iconButton("live/edit/reveal", rectAt(field.Right()+u.px(6), y+u.px(2), u.px(30), u.px(30)), glyph, hint, e.reveal, true) {
		e.reveal = !e.reveal
	}
	y += u.px(40)
	y = a.paragraph(in.X, y, in.W-u.px(8), u.t("The key is kept encrypted for your Windows account and never shown to AI assistants."), 11.5, colFaint) + u.px(10)
	u.heading(rectAt(in.X, y, in.W, u.px(20)), u.t("Name"))
	y += u.px(26)
	u.textBox("live/edit/name", rectAt(in.X, y, in.W-u.px(8), u.px(34)), &e.d.Name, p.Name.In(lang), false)
	y += u.px(42)
	e.d.Key = sealedText(strings.TrimSpace(e.key))
	if problem := a.destinationProblem(e.d); problem != "" && strings.TrimSpace(e.d.Server) != "" {
		y = a.paragraph(in.X, y, in.W-u.px(8), problem, 12, colDangerText)
	}
	return y
}

// serverHost is the host of an address, to name a server without its stream key.
func serverHost(address string) string {
	if u, err := url.Parse(strings.TrimSpace(address)); err == nil && u.Host != "" {
		return u.Host
	}
	return strings.TrimSpace(address)
}
