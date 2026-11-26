package ui

import (
	"context"
	"fmt"
	"image"
	"math"
	"path/filepath"
	"slices"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/playback"
	"github.com/btelolabs/btelo-forge/internal/project"
)

// A video opened to be watched plays in a tab of its own, the player: the file decoded as it is, at its own frame
// rate and scaled by ffmpeg to the size shown, with no edit between it and the screen (playback.File). Its bar plays
// and seeks, sets the speed and the volume, fills the window, and has an Edit button that turns the tab into the video
// editor at the picture being watched. A video is opened in the editor at once only where editing was asked for: the
// editor's own list and open dialog, a video dropped on an edit, the AI's and the tools' "edit" commands.

// movieDoc is a player tab.
type movieDoc struct {
	path    string
	info    media.Info
	probing bool
	probed  bool
	problem string

	player   *playback.Player
	at       float64 // the position while paused
	speed    float64 // seconds of the file a second; 0 is 1
	autoplay bool    // starts playing once it is shown, from where it was left
	resume   bool    // playing when the seek bar was pressed, and goes on when it is let go
	playSize image.Point
	// sized is the picture size wanted while it plays at another, since when: a window that stops changing size
	// has the decoder started again at its new size.
	sized    image.Point
	sizedAt  time.Time
	wasOn    bool // playing at the last frame, to notice it reached the end
	full     bool // fills the window, the tabs hidden
	scrubbed bool // the seek bar is held: the stills are the nearest key pictures, quick to find

	shown      *image.RGBA // the picture on the screen, kept while paused
	still      *image.RGBA // the picture at stillAt, decoded while paused
	stillAt    float64
	stillSize  image.Point // the size it was asked for at stillAt
	stillExact bool        // still is the picture at stillAt, not the key picture nearest it
	stillBusy  bool
	scaler     gfx.Scaler

	osd      string // what a key or the wheel just changed, over the picture for a moment
	osdUntil time.Time
	moved    time.Time // the pointer last moved over the tab, which shows the bar in a full window
	mouse    [2]float32
}

// movieSpeeds are the speeds offered, each one tempo filter apart from none.
var movieSpeeds = []float64{0.5, 0.75, 1, 1.25, 1.5, 1.75, 2}

// movieIdle is how long the pointer may rest over a full window that plays before the bar and the pointer hide.
const movieIdle = 2500 * time.Millisecond

// movieOSD is how long what a key changed stays over the picture.
const movieOSD = 1200 * time.Millisecond

// movieResize is how long a window must keep its size before a playing video is decoded again at it.
const movieResize = 250 * time.Millisecond

// movieStep is how far the arrow keys seek, and movieStepMore how far J and L do.
const (
	movieStep     = 5.0
	movieStepMore = 10.0
)

// playerSettings are the player's preferences kept between runs.
type playerSettings struct {
	Volume float64 `json:"volume"`
	Muted  bool    `json:"muted,omitempty"`
}

// openMovie plays the video at path in a player tab: the tab already playing it comes forward, an empty tab takes it,
// and otherwise a new one does.
func (a *App) openMovie(path string) {
	for i, d := range a.docs {
		if d.movie != nil && samePath(d.movie.path, path) {
			a.switchTab(i)
			return
		}
	}
	if a.showRestoring(path) {
		return
	}
	if a.occupied() {
		a.newTab()
	}
	a.doc.movie = &movieDoc{path: path, autoplay: true}
}

// moviePlayer is the tab's own player, made when it first plays.
func (a *App) moviePlayer(m *movieDoc) *playback.Player {
	if m.player == nil {
		m.player = playback.New(a.host.Wake)
	}
	return m.player
}

// probeMovie reads what the file is, once ffmpeg is ready.
func (a *App) probeMovie(m *movieDoc) {
	if m.probed || m.probing || !a.tools.ready {
		return
	}
	m.probing = true
	path := m.path
	a.background(func(ctx context.Context) {
		info, err := media.Probe(ctx, path)
		a.post(func() {
			m.probing, m.probed = false, true
			switch {
			case err != nil:
				m.problem = a.u.tf("Cannot open %s: %s", filepath.Base(path), a.describe(err))
				a.logError("movie", m.problem, err)
			case !info.HasVideo && !info.HasAudio || info.Duration <= 0:
				m.problem = a.u.tf("%s has neither pictures nor sound to play", filepath.Base(path))
			default:
				m.info = info
				m.at = a.resumeAt(path, info.Duration)
				// Said only of a video that starts playing, in a tab still open: a pinned one comes back paused there.
				if m.at > 0 && m.autoplay && slices.ContainsFunc(a.docs, func(d *doc) bool { return d.movie == m }) {
					a.toastActions(a.u.tf("Playing on from %s, where it was left", playClock(m.at)), false,
						[]toastAction{{a.u.t("Start over"), func() { a.seekMovie(m, 0) }}})
				}
			}
		})
	})
}

// movieFile is what the player decodes: the file, at the tab's speed.
func (m *movieDoc) file() playback.File {
	return playback.File{Path: m.path, Duration: m.info.Duration, FPS: m.info.FPS, HasVideo: m.info.HasVideo, HasAudio: m.info.HasAudio, Speed: m.speed}
}

// ready reports whether the file was read and can play.
func (m *movieDoc) ready() bool { return m.probed && m.problem == "" }

// playing reports whether the tab's video plays.
func (m *movieDoc) playing() bool { return m.player != nil && m.player.Playing() }

// position is where the video is.
func (m *movieDoc) position() float64 {
	if m.player != nil {
		if t, ok := m.player.Position(); ok {
			return t
		}
	}
	return m.at
}

// currentSpeed is the speed the video plays at.
func (m *movieDoc) currentSpeed() float64 {
	if m.speed <= 0 {
		return 1
	}
	return m.speed
}

// volume is the gain the player's sound is played at: the volume chosen, or none while muted.
func (a *App) volume() float64 {
	if a.settings.Player.Muted {
		return 0
	}
	return a.settings.Player.Volume
}

// playMovie starts the video from where it is, at the size it is shown; at its end it starts over.
func (a *App) playMovie(m *movieDoc) {
	if !m.ready() {
		return
	}
	if m.at >= m.info.Duration-0.05 {
		m.at = 0
	}
	pl := a.moviePlayer(m)
	pl.SetVolume(a.volume())
	size := m.playSize
	if size.X < 2 || size.Y < 2 {
		size = image.Pt(2, 2)
	}
	pl.StartFile(m.file(), m.at, size.X, size.Y)
	m.wasOn, m.sized = true, size
}

// pauseMovie stops the video where it is, the picture staying.
func (a *App) pauseMovie(m *movieDoc) {
	if !m.playing() {
		return
	}
	m.at = m.position()
	m.player.Stop()
	m.wasOn = false
	m.keepShown()
}

// keepShown keeps the picture on the screen as the one where the video stopped.
func (m *movieDoc) keepShown() {
	m.still, m.stillAt, m.stillExact = m.shown, m.at, true
	if m.shown != nil {
		m.stillSize = m.shown.Rect.Size()
	}
}

// toggleMovie plays or pauses the video.
func (a *App) toggleMovie(m *movieDoc) {
	if m.playing() {
		a.pauseMovie(m)
		return
	}
	a.playMovie(m)
}

// seekMovie moves the video to t, playing on from there if it was playing.
func (a *App) seekMovie(m *movieDoc, t float64) {
	if !m.ready() {
		return
	}
	playing := m.playing()
	if playing {
		m.player.Stop()
	}
	m.at = min(max(t, 0), m.info.Duration)
	if playing && m.at < m.info.Duration-0.05 {
		a.playMovie(m)
	}
}

// stepMovie pauses the video and moves it a picture back or forward.
func (a *App) stepMovie(m *movieDoc, pictures int) {
	if !m.ready() || !m.info.HasVideo {
		return
	}
	a.pauseMovie(m)
	fps := m.info.FPS
	if fps <= 0 {
		fps = playback.FPS
	}
	// The place is put on a picture's middle, so the picture decoded there is the one meant, never its neighbour.
	k := math.Floor(m.at*fps) + float64(pictures)
	m.at = min(max((k+0.5)/fps, 0), max(m.info.Duration-0.5/fps, 0))
	a.movieNote(m, a.u.tf("Frame %d", int(k)+1))
}

// setMovieSpeed plays the video at speed, from where it is.
func (a *App) setMovieSpeed(m *movieDoc, speed float64) {
	if math.Abs(speed-m.currentSpeed()) < 1e-9 {
		return
	}
	playing := m.playing()
	if playing {
		m.at = m.position()
		m.player.Stop()
	}
	m.speed = speed
	if playing {
		a.playMovie(m)
	}
	a.movieNote(m, a.u.tf("Speed %s", speedText(speed)))
}

// stepMovieSpeed goes to the next speed offered, slower or faster.
func (a *App) stepMovieSpeed(m *movieDoc, faster bool) {
	i := slices.IndexFunc(movieSpeeds, func(s float64) bool { return math.Abs(s-m.currentSpeed()) < 1e-9 })
	if i < 0 {
		i = slices.Index(movieSpeeds, 1)
	}
	if faster {
		i = min(i+1, len(movieSpeeds)-1)
	} else {
		i = max(i-1, 0)
	}
	a.setMovieSpeed(m, movieSpeeds[i])
}

// speedText writes a speed as the menu does: 1.25×.
func speedText(s float64) string {
	return fmt.Sprintf("%s×", trimFloat(s))
}

// trimFloat writes v with no zeros after its last digit.
func trimFloat(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	for s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}

// setVolume chooses the player's volume, unmuting it, and keeps it for the next video.
func (a *App) setVolume(v float64) {
	ps := &a.settings.Player
	ps.Volume, ps.Muted = min(max(v, 0), 1), false
	a.applyVolume()
	a.saveSettings()
}

// toggleMute mutes or unmutes the player.
func (a *App) toggleMute() {
	a.settings.Player.Muted = !a.settings.Player.Muted
	a.applyVolume()
	a.saveSettings()
}

// applyVolume gives the volume to the tab playing, and says so over its picture.
func (a *App) applyVolume() {
	m := a.movie
	if m == nil {
		return
	}
	if m.player != nil {
		m.player.SetVolume(a.volume())
	}
	if a.settings.Player.Muted {
		a.movieNote(m, a.u.t("Muted"))
	} else {
		a.movieNote(m, a.u.tf("Volume %d%%", int(math.Round(a.settings.Player.Volume*100))))
	}
}

// movieNote shows text over the picture for a moment.
func (a *App) movieNote(m *movieDoc, text string) {
	m.osd, m.osdUntil = text, time.Now().Add(movieOSD)
}

// setMovieFull fills the window with the video, or gives the tabs back.
func (a *App) setMovieFull(m *movieDoc, on bool) {
	m.full = on
	m.moved = time.Now()
	a.menu = nil
	a.u.active = ""
}

// leaveMovie pauses the video of tab d, which goes behind another or closes, and keeps where it was left.
func (a *App) leaveMovie(d *doc) {
	if d == nil || d.movie == nil {
		return
	}
	m := d.movie
	if m.playing() {
		m.at = m.position()
		m.player.Stop()
		m.keepShown()
	}
	m.wasOn = false
	if m.ready() {
		a.keepPosition(m.path, m.at, m.info.Duration)
	}
}

// editMovie turns the player tab into the video editor, with the video and the playhead where it was watched; an
// unfinished edit of the same video comes back, its playhead on the same picture if the edit still shows it.
func (a *App) editMovie() {
	d := a.doc
	m := d.movie
	if m == nil || !m.ready() {
		return
	}
	if !m.info.HasVideo {
		a.toast(a.u.tf("%s has no picture; drop sound files to use them as music", filepath.Base(m.path)), true)
		return
	}
	a.leaveMovie(d)
	at := m.at
	src := project.Source{Path: m.path, Duration: m.info.Duration, Width: m.info.Width, Height: m.info.Height, FPS: m.info.FPS, HasVideo: true, HasAudio: m.info.HasAudio}
	d.movie = nil
	a.media.start(a, src)
	a.load(d, src)
	if t, ok := d.p.TimeOf(0, at); ok {
		d.playhead = min(t, d.p.Duration())
	}
	a.requestFrame()
}

// closeMovie lets go of a player tab that closes.
func (a *App) closeMovie(d *doc) {
	a.leaveMovie(d)
}

// movieWait is how soon the player tab needs drawing again on its own: its next picture while it plays, the bar
// hiding or a note going.
func (a *App) movieWait() time.Duration {
	m := a.movie
	if m == nil {
		return 0
	}
	var next time.Duration
	sooner := func(d time.Duration) {
		if d > 0 && (next == 0 || d < next) {
			next = d
		}
	}
	if m.playing() {
		// The next picture is drawn when it is due; with none decoded yet, the player wakes the window when one comes.
		// What plays without pictures has only its clock to show.
		if due, ok := m.player.Due(); ok {
			sooner(max(due, redrawNow))
		}
		if !m.info.HasVideo {
			sooner(time.Second / 4)
		}
		if m.full {
			sooner(max(time.Until(m.moved.Add(movieIdle)), redrawNow))
		}
	}
	if m.osd != "" {
		sooner(max(time.Until(m.osdUntil), redrawNow))
	}
	if !m.sized.Eq(m.playSize) && m.playing() {
		sooner(max(time.Until(m.sizedAt.Add(movieResize)), redrawNow))
	}
	return next
}

// movieSettled reports whether the player tab in front shows what it has to: the file read, and its picture decoded.
// A video that plays is settled once its pictures come.
func (a *App) movieSettled() bool {
	m := a.movie
	if m == nil || m.problem != "" {
		return true
	}
	return m.probed && !m.stillBusy && !m.autoplay && (!m.info.HasVideo || m.shown != nil)
}

// syncMovie follows the player: the end reached, a failure, and a window whose size changed while it plays.
func (a *App) syncMovie(m *movieDoc) {
	if m.player != nil {
		if err := m.player.Err(); err != nil {
			m.problem = a.u.tf("Playback failed: %s", a.describe(err))
			m.wasOn = false
		}
	}
	if m.wasOn && !m.playing() {
		m.wasOn = false
		m.at = m.info.Duration // played to its end
		m.keepShown()
	}
}

// drawMovie draws the player tab in r, or over the whole window when it fills it.
func (a *App) drawMovie(r gfx.Rect) {
	u := &a.u
	m := a.movie
	if m == nil {
		return
	}
	a.probeMovie(m)
	a.syncMovie(m)
	if u.mx != m.mouse[0] || u.my != m.mouse[1] {
		m.mouse, m.moved = [2]float32{u.mx, u.my}, time.Now()
	}
	u.c.Fill(r, colBlack)
	barH := u.px(64)
	bar := gfx.Rect{X: r.X, Y: r.Bottom() - barH, W: r.W, H: barH}
	view := gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: bar.Y - r.Y}
	showBar := true
	if m.full {
		// In a full window the picture takes it all, and the bar shows over it while the pointer moves or rests on
		// it, or the video is paused.
		view = r
		idle := time.Since(m.moved) > movieIdle && !bar.Contains(u.mx, u.my) && u.active == "" && a.menu == nil
		showBar = !(m.playing() && idle)
		if !showBar && !u.inactive {
			u.cursor = CursorNone
		}
	}
	switch {
	case m.problem != "":
		u.icon(icVideo, rectAt(view.X, view.Y+view.H/2-u.px(60), view.W, u.px(48)), 40, darkPalette.faint)
		u.label(u.face(13), rectAt(view.X+u.px(16), view.Y+view.H/2, view.W-u.px(32), u.px(24)), m.problem, darkPalette.dangerText, 0.5)
	case !a.tools.ready:
		u.label(u.face(13), view, u.tf("%s opens as soon as ffmpeg is ready", filepath.Base(m.path)), darkPalette.faint, 0.5)
	case !m.probed:
		u.label(u.face(13), view, u.t("Loading the preview…"), darkPalette.faint, 0.5)
	default:
		a.drawMoviePicture(m, view)
	}
	if m.ready() {
		pic := view
		if showBar && m.full {
			pic.H = bar.Y - pic.Y // the bar over the picture keeps its clicks
		}
		a.movieInput(m, pic)
	}
	if m.osd != "" {
		if time.Now().After(m.osdUntil) {
			m.osd = ""
		} else {
			f := u.bold(15)
			w := f.Measure(m.osd) + u.px(28)
			box := rectAt(view.Right()-w-u.px(20), view.Y+u.px(20), w, u.px(38))
			u.c.Fill(box, gfx.Alpha(colBlack, 0.6))
			u.label(f, box, m.osd, colWhite, 0.5)
		}
	}
	if showBar {
		if m.full {
			if isDarkTheme() {
				u.c.Fill(bar, gfx.Alpha(colBlack, 0.6))
			} else {
				u.c.Fill(bar, gfx.Alpha(colPanel, 0.92))
			}
		} else {
			u.c.Fill(bar, colPanel)
		}
		a.drawMovieBar(m, bar)
	}
}

// drawMoviePicture shows the picture playing, or the one where the video is paused, fitted in view.
func (a *App) drawMoviePicture(m *movieDoc, view gfx.Rect) {
	u := &a.u
	if !m.info.HasVideo {
		u.icon(icMusic, view, 64, gfx.Hex(0x3fc28a))
		u.label(u.bold(14), rectAt(view.X, view.Y+view.H/2+u.px(44), view.W, u.px(24)), filepath.Base(m.path), darkPalette.dim, 0.5)
		a.movieAutoplay(m)
		return
	}
	fit := fitRect(view, float32(m.info.Width), float32(m.info.Height))
	want := evenSize(fit)
	if want != m.playSize {
		m.playSize, m.sizedAt = want, time.Now()
	}
	// A window that keeps its new size for a moment has the decoder started again at it, so what plays is never
	// scaled twice; until then the pictures are scaled to fit.
	if m.playing() && m.sized != m.playSize && time.Since(m.sizedAt) >= movieResize {
		m.at = m.position()
		a.playMovie(m)
	}
	a.movieAutoplay(m)
	if m.playing() {
		if f, ok := m.player.Frame(); ok {
			m.shown = f.Image
		}
	} else {
		a.fetchMovieStill(m)
		if m.still != nil {
			m.shown = m.still
		}
	}
	img := m.shown
	if img == nil {
		u.label(u.face(13), fit, u.t("Loading the picture…"), colFaint, 0.5)
		return
	}
	pr := fit.Pixels()
	if dw, dh := img.Rect.Dx()-pr.Dx(), img.Rect.Dy()-pr.Dy(); dw < -2 || dw > 2 || dh < -2 || dh > 2 {
		img = m.scaler.Scaled(img, pr.Dx(), pr.Dy())
	}
	u.c.Blit(img, pr.Min.X+(pr.Dx()-img.Rect.Dx())/2, pr.Min.Y+(pr.Dy()-img.Rect.Dy())/2, true)
}

// movieAutoplay starts a video just opened, once its picture's size is known.
func (a *App) movieAutoplay(m *movieDoc) {
	if !m.autoplay {
		return
	}
	m.autoplay = false
	a.playMovie(m)
}

// fetchMovieStill decodes the picture where the paused video is, one at a time, the newest place wanted winning:
// while the seek bar is held, the nearest key picture, which is found at once.
func (a *App) fetchMovieStill(m *movieDoc) {
	if m.stillBusy || m.playSize.X < 2 {
		return
	}
	if m.stillAt == m.at && (m.stillExact || m.scrubbed) && m.stillSize == m.playSize {
		return // asked for already; a picture for another size, as the window's was, is decoded again at this one
	}
	size, at, want, quick := m.playSize, min(m.at, max(m.info.Duration-0.05, 0)), m.at, m.scrubbed
	path := m.path
	m.stillBusy = true
	a.background(func(ctx context.Context) {
		var img *image.RGBA
		var err error
		if quick {
			img, err = media.Thumbnail(ctx, path, at, size.X, size.Y)
		}
		if !quick || err != nil {
			img, err = media.Frame(ctx, path, at, size.X, size.Y)
		}
		a.post(func() {
			m.stillBusy = false
			if err == nil {
				m.still = img
			}
			// A place with nothing to decode is not asked for again; a key picture stands in only while the bar is held.
			m.stillAt, m.stillSize, m.stillExact = want, size, !quick || err != nil
		})
	})
}

// movieInput is what the picture takes: a click plays and pauses, a double click fills the window or gives the tabs
// back, and the wheel sets the volume.
func (a *App) movieInput(m *movieDoc, view gfx.Rect) {
	u := &a.u
	id := "movie/picture"
	if u.hover(id, view) && u.wheel != 0 {
		a.setVolume(a.volume() + float64(u.wheel)*0.05)
		u.wheel = 0
	}
	if u.press(id, view) {
		a.toggleMovie(m)
		if u.double {
			a.toggleMovie(m) // the first click of the two played or paused it
			a.setMovieFull(m, !m.full)
		}
	}
	u.letGo(id)
}

// drawMovieBar draws the seek bar along the top of r and the controls under it.
func (a *App) drawMovieBar(m *movieDoc, r gfx.Rect) {
	u := &a.u
	length := m.info.Duration
	at := m.position()
	seek := gfx.Rect{X: r.X + u.px(14), Y: r.Y + u.px(4), W: r.W - u.px(28), H: u.px(18)}
	if to, state := a.seekBar("movie/seek", seek, at, length); state != seekIdle && m.ready() {
		switch state {
		case seekPressed:
			m.resume = m.playing()
			a.pauseMovie(m)
			m.at, m.scrubbed = to, true
		case seekDragged:
			m.at = to
		case seekReleased:
			m.at, m.scrubbed = to, false
			if m.resume {
				m.resume = false
				a.playMovie(m)
			}
		}
	}

	row := gfx.Rect{X: r.X + u.px(10), Y: seek.Bottom(), W: r.W - u.px(20), H: r.Bottom() - seek.Bottom() - u.px(4)}
	bh := u.px(34)
	y := row.Y + (row.H-bh)/2
	x := row.X
	ready := m.ready()
	glyph, hint := icPlay, u.tf("Play (%s)", a.commandKey("player.play"))
	if m.playing() {
		glyph, hint = icPause, u.tf("Pause (%s)", a.commandKey("player.play"))
	}
	if u.iconButton("movie/play", rectAt(x, y, u.px(38), bh), glyph, hint, false, ready) {
		a.toggleMovie(m)
	}
	x += u.px(42)
	if u.iconButton("movie/back", rectAt(x, y, u.px(34), bh), icReplay10, u.tf("Back %d seconds (%s)", int(movieStepMore), a.commandKey("player.back-more")), false, ready) {
		a.seekMovie(m, m.position()-movieStepMore)
	}
	x += u.px(36)
	if u.iconButton("movie/forward", rectAt(x, y, u.px(34), bh), icForward10, u.tf("Forward %d seconds (%s)", int(movieStepMore), a.commandKey("player.forward-more")), false, ready) {
		a.seekMovie(m, m.position()+movieStepMore)
	}
	x += u.px(46)
	f := u.bold(13)
	now := playClock(at)
	u.c.Text(f, x, row.Y+(row.H-f.Height())/2, now, colText)
	x += f.Measure(now) + u.px(6)
	fd := u.face(12.5)
	u.c.Text(fd, x, row.Y+(row.H-fd.Height())/2+u.px(1), "/ "+playClock(length), colDim)

	// From the right: the window, Edit, the volume, the speed.
	right := row.Right()
	fullGlyph, fullHint := icFullscreen, u.tf("Fill the window (%s)", a.commandKey("player.full"))
	if m.full {
		fullGlyph, fullHint = icFullscreenExit, u.tf("Show the tabs again (%s or Esc)", a.commandKey("player.full"))
	}
	right -= u.px(36)
	if u.iconButton("movie/full", rectAt(right, y, u.px(36), bh), fullGlyph, fullHint, false, true) {
		a.setMovieFull(m, !m.full)
	}
	edit := u.t("Edit")
	ew := u.buttonWidth(icEdit, edit) + u.px(8)
	right -= ew + u.px(10)
	if u.button("movie/edit", rectAt(right, y, ew, bh), icEdit, edit, primary, ready && m.info.HasVideo) {
		a.editMovie()
		return
	}
	u.hint("movie/edit", u.tf("Edit this video in the video editor, from this picture (%s)", a.commandKey("player.edit")), rectAt(right, y, ew, bh))

	volW := u.px(96)
	right -= volW + u.px(14)
	vol := a.volume()
	sr := rectAt(right, y, volW, bh)
	if changed, done := u.slider("movie/volume", sr, &vol, 0, 1, true); changed || done {
		a.settings.Player.Volume, a.settings.Player.Muted = vol, false
		if m.player != nil {
			m.player.SetVolume(a.volume())
		}
		if done {
			a.saveSettings()
		}
	}
	u.hint("movie/volume", u.tf("Volume %d%% (%s and %s, or the wheel over the picture)", int(math.Round(vol*100)),
		a.commandKey("player.volume-up"), a.commandKey("player.volume-down")), sr)
	right -= u.px(34)
	muteGlyph, muteHint := icVolume, u.tf("Mute (%s)", a.commandKey("player.mute"))
	if a.settings.Player.Muted || a.settings.Player.Volume == 0 {
		muteGlyph, muteHint = icMute, u.tf("Unmute (%s)", a.commandKey("player.mute"))
	}
	if u.iconButton("movie/mute", rectAt(right, y, u.px(34), bh), muteGlyph, muteHint, false, true) {
		if a.settings.Player.Volume == 0 {
			a.setVolume(1)
		} else {
			a.toggleMute()
		}
	}

	speed := speedText(m.currentSpeed())
	sw := max(u.face(12.5).Measure(speed)+u.px(24), u.px(54))
	right -= sw + u.px(10)
	spr := rectAt(right, y+u.px(4), sw, bh-u.px(8))
	if right > x+fd.Measure("/ "+playClock(length))+u.px(16) {
		hot := ready && u.hover("movie/speed", spr)
		bg := colSurface
		if hot || a.menuOpen("movie/speed") {
			bg = colSurfaceHi
			u.cursor = CursorHand
		}
		u.c.Fill(spr, bg)
		u.c.Outline(spr, max(1, u.px(1)), colBorder)
		u.label(u.face(12.5), spr, speed, colText, 0.5)
		u.hint("movie/speed", u.tf("Speed (%s slower, %s faster)", a.commandKey("player.slower"), a.commandKey("player.faster")), spr)
		if ready && u.clicked("movie/speed", spr) {
			var items []menuItem
			for _, s := range movieSpeeds {
				items = append(items, menuItem{label: speedText(s), checked: math.Abs(s-m.currentSpeed()) < 1e-9, run: func() { a.setMovieSpeed(m, s) }})
			}
			a.openMenu("movie/speed", spr, items)
		}
	}
}

// playClock writes a place in a video as a player does: m:ss, or h:mm:ss, in whole seconds gone by.
func playClock(t float64) string {
	s := int(math.Floor(max(t, 0) + 1e-6))
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// movieCommands are the player's commands keys can run, in a tab that plays a video.
func (a *App) movieCommands() []appCommand {
	u := &a.u
	m := func() *movieDoc { return a.movie }
	p := func(id, label string, run func(m *movieDoc), ks ...*Key) appCommand {
		return appCommand{shortcut: shortcut{id: "player." + id, domain: domainPlayer, label: label, defaults: keys(ks...)}, run: func() {
			if mv := m(); mv != nil && mv.ready() {
				run(mv)
			}
		}, player: true}
	}
	seek := func(by float64) func(*movieDoc) {
		return func(mv *movieDoc) {
			a.seekMovie(mv, mv.position()+by)
			a.movieNote(mv, playClock(mv.position())+" / "+playClock(mv.info.Duration))
		}
	}
	return []appCommand{
		p("play", u.t("Play or pause"), a.toggleMovie, key(keySpace, ""), key('K', "")),
		p("back", u.tf("Back %d seconds", int(movieStep)), seek(-movieStep), key(keyLeft, "")),
		p("forward", u.tf("Forward %d seconds", int(movieStep)), seek(movieStep), key(keyRight, "")),
		p("back-more", u.tf("Back %d seconds", int(movieStepMore)), seek(-movieStepMore), key('J', "")),
		p("forward-more", u.tf("Forward %d seconds", int(movieStepMore)), seek(movieStepMore), key('L', "")),
		p("frame.back", u.t("Back a frame"), func(mv *movieDoc) { a.stepMovie(mv, -1) }, key(keyComma, "")),
		p("frame.forward", u.t("Forward a frame"), func(mv *movieDoc) { a.stepMovie(mv, 1) }, key(keyPeriod, "")),
		p("start", u.t("Go to the start"), func(mv *movieDoc) { a.seekMovie(mv, 0) }, key(keyHome, "")),
		p("end", u.t("Go to the end"), func(mv *movieDoc) { a.seekMovie(mv, mv.info.Duration) }, key(keyEnd, "")),
		p("slower", u.t("Slower"), func(mv *movieDoc) { a.stepMovieSpeed(mv, false) }, key(keyComma, "s")),
		p("faster", u.t("Faster"), func(mv *movieDoc) { a.stepMovieSpeed(mv, true) }, key(keyPeriod, "s")),
		p("volume-up", u.t("Louder"), func(*movieDoc) { a.setVolume(a.volume() + 0.05) }, key(keyUp, "")),
		p("volume-down", u.t("Quieter"), func(*movieDoc) { a.setVolume(a.volume() - 0.05) }, key(keyDown, "")),
		p("mute", u.t("Mute or unmute"), func(*movieDoc) { a.toggleMute() }, key('M', "")),
		p("full", u.t("Fill the window with the video, or show the tabs again"), func(mv *movieDoc) { a.setMovieFull(mv, !mv.full) }, key('F', "")),
		p("edit", u.t("Edit the video in the video editor"), func(*movieDoc) { a.editMovie() }, key('E', "")),
	}
}
