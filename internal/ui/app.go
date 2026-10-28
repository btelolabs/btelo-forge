// Package ui is the editor: an immediate-mode interface drawn into a canvas, independent of the window that shows it.
package ui

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/btelolabs/btelo-forge/internal/caption"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/library"
	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/paint"
	"github.com/btelolabs/btelo-forge/internal/playback"
	"github.com/btelolabs/btelo-forge/internal/transcribe"
)

const (
	tabMusic = iota
	tabOverlays
	tabSubtitles
	tabExport
)

// App is the whole editor. The embedded doc is the active tab's document; every method runs on the UI goroutine
// except where noted.
type App struct {
	*doc
	docs []*doc

	host     Host
	u        ctx
	lib      *library.Library
	player   *playback.Player
	audition *playback.Audition
	captions *caption.Renderer
	saveDir  string
	stateDir string
	cacheDir string
	jobs     chan func()

	life     context.Context // ends when the app closes
	stopLife context.CancelFunc
	running  sync.WaitGroup // background work started with background

	settings       settings
	settingsOpen   bool
	tab            int
	media          mediaCache
	music          musicPanel
	subtitles      subtitlePanel
	export         *exportJob
	last           lastExport
	known          map[string]library.Track
	toasts         []toast
	settingsPage   int
	extensions     extensionsPage
	mcpScroll      float32 // how far the AI setup text in Settings is scrolled
	exportForm     exportForm
	encoders       media.Encoders // nil until probed
	tools          toolsState
	panelScroll    [4]float32
	rec            recordState
	hotkeys        hotkeyState
	keysDomain     int         // the domain the Hotkeys page shows
	mcpDomain      int         // the domain whose AI setup the MCP page shows
	keysScroll     float32     // how far its list is scrolled
	keyCapture     *keyCapture // the command whose new key the Hotkeys page waits for
	brush          paint.Brush // shared by every canvas
	artTool        artTool
	marqueeEllipse bool               // the marquee draws ellipses
	toolSlots      map[uint16]artTool // the tool each shared place in the tool box shows, by its key
	pickBackground bool               // the colour panel sets the background colour
	picker         colorPicker
	menu           *popupMenu
	dialog         artDialog     // the canvas editor's open dialog
	adjust         *adjustDialog // an adjustment or filter being tried
	style          *styleDialog  // a layer's effects being edited
	sidePage       int           // the canvas side panel's lower page: layers or history
	layerOpacity   float64       // the layer opacity slider while it is dragged
	opacityMark    paint.Mark    // the picture before that drag
	layersScroll   float32
	thumbs         map[int]*layerThumb // by layer id
	over           screenState         // the windows over the screen
	panelContent   [4]float32          // height of each panel's content at the last frame
	screen         gfx.Rect
	services       *services
}

// New builds the editor; cacheDir holds downloads and models, stateDir remembers settings and unfinished edits.
func New(host Host, fonts gfx.Fonts, cacheDir, stateDir string) *App {
	a := &App{
		host:     host,
		lib:      library.New(filepath.Join(cacheDir, "library"), library.DefaultConfig()),
		captions: caption.New(fonts.Bold),
		saveDir:  filepath.Join(stateDir, "edits"),
		stateDir: stateDir,
		cacheDir: cacheDir,
		jobs:     make(chan func(), 1024),
		media:    newMediaCache(),
		known:    map[string]library.Track{},
		thumbs:   map[int]*layerThumb{},
	}
	a.life, a.stopLife = context.WithCancel(context.Background())
	a.u.fonts, a.u.host = fonts, host
	a.loadSettings()
	a.u.lang = a.settings.Language
	a.brush = a.settings.Paint.brush()
	host.SetTitle(a.Title())
	a.player = playback.New(host.Wake)
	a.audition = playback.NewAudition(host.Wake)
	a.music.init()
	a.subtitles.config = transcribe.DefaultConfig()
	a.subtitles.manager = transcribe.NewManager(filepath.Join(cacheDir, "whisper"))
	a.tools.config = media.DefaultToolsConfig()
	a.loadConfigs()
	a.prepareTools()
	a.newTab()
	a.applyServices()
	a.hotkeys.capturing = -1
	a.applyHotkeys()
	return a
}

// post runs fn on the UI goroutine at the next frame; it may be called from any goroutine, and work posted after
// Close is dropped.
func (a *App) post(fn func()) {
	select {
	case a.jobs <- fn:
		a.host.Wake()
	case <-a.life.Done():
	}
}

// Frame handles input and draws everything; it returns how soon it wants to be drawn again (0 = only on input).
func (a *App) Frame(in Input, c *gfx.Canvas, scale float32) time.Duration {
	a.drainJobs()
	a.recordTick(in)
	a.screenTick()
	u := &a.u
	u.c, u.s = c, scale
	u.begin(in)
	u.tipSeen, u.blinking = false, false
	a.screen = gfx.Rect{W: float32(c.Img.Rect.Dx()), H: float32(c.Img.Rect.Dy())}
	c.Clear(colBackground)

	a.handleDrops(in.Dropped)
	a.syncPlayback()
	a.preToasts()
	u.blocked = a.export != nil || a.settingsOpen || a.rec.open || a.menu != nil || a.dialog.kind != dialogNone || a.adjust != nil || a.style != nil

	l := a.layout()
	a.drawTabs(l.tabs)
	switch {
	case a.art != nil:
		a.drawArt(l.body)
	case a.p == nil:
		a.drawEmpty(l.body)
	default:
		a.drawPreview(l.preview)
		a.drawTransport(l.transport)
		a.drawTimeline(l.timeline)
		if !a.settings.PanelHidden {
			a.drawPanel(l.panel)
		}
	}
	// A dialog or menu that is open takes the keys (Esc, Enter), so the window's shortcuts wait.
	if u.focus == "" && a.hotkeys.capturing < 0 && a.keyCapture == nil && !u.blocked {
		a.shortcuts()
	}
	if a.bodyBefore != nil && u.focus != "overlay/body" {
		a.committed(a.bodyBefore)
		a.bodyBefore = nil
	}
	u.blocked = false
	a.drawExportModal()
	if a.export == nil {
		a.drawSettings()
		a.drawRecorder()
		a.drawArtDialog()
		a.drawAdjustDialog()
		a.drawStyleDialog()
	}
	if a.menu != nil && a.dialog.kind == dialogNone && a.adjust == nil && a.style == nil {
		if a.menu.id == "art/tipshape" {
			a.drawTipShape()
		} else {
			a.drawMenu()
		}
	}
	a.drawToasts()
	wait := u.drawTip(a.screen)
	u.end()

	if u.focus != "" {
		a.host.TextInput(true, u.imeAt[0], u.imeAt[1], u.imeAt[2])
	} else {
		a.host.TextInput(false, 0, 0, 0)
	}
	a.host.SetCursor(u.cursor)
	a.autosave()
	a.autosaveArt()
	return a.nextFrame(wait, in.acted())
}

// redrawNow is the wait before drawing again at once.
const redrawNow = time.Millisecond

// nextFrame is how long the window may wait for input before drawing again; zero waits for input alone.
func (a *App) nextFrame(tip time.Duration, acted bool) time.Duration {
	next := tip
	sooner := func(d time.Duration) {
		if d > 0 && (next == 0 || d < next) {
			next = d
		}
	}
	// A click or key is handled where its control is drawn, after the layout and the parts above it were already
	// drawn from the old state, so the frame does not show its result yet.
	if acted {
		sooner(redrawNow)
	}
	if a.player.Playing() {
		sooner(time.Second / 60)
	}
	if key, _ := a.audition.Playing(); key != "" || a.music.downloading() || a.subtitles.busy() {
		sooner(time.Second / 10)
	}
	if a.u.blinking && !a.u.inactive { // a caret only blinks in the window being typed in
		sooner(265 * time.Millisecond)
	}
	// Edits are saved a moment after they settle: wake then, rather than ticking until it is time.
	for _, d := range a.docs {
		if d.p != nil && d.dirty {
			sooner(max(time.Until(d.savedAt.Add(time.Second)), redrawNow))
		}
		if ad := d.art; ad != nil {
			if due, ok := a.artSaveDue(ad); ok {
				sooner(max(time.Until(due), redrawNow))
			}
		}
	}
	// Progress and clocks that change without telling: an export's progress, a recording's time, toasts running
	// out. Work that posts its result (opening a video, checking the screen recorder) wakes the window itself.
	if a.export != nil || len(a.toasts) > 0 || a.recording() || a.over.bar != nil {
		sooner(time.Second / 10)
	}
	return next
}

// drainJobs runs the work other goroutines posted for the UI goroutine.
func (a *App) drainJobs() {
	for {
		select {
		case fn := <-a.jobs:
			fn()
		default:
			return
		}
	}
}

type layout struct {
	tabs, body, preview, transport, timeline, panel gfx.Rect
}

func (a *App) layout() layout {
	u := &a.u
	s := a.screen
	var l layout
	l.tabs = gfx.Rect{X: 0, Y: 0, W: s.W, H: u.px(40)}
	l.body = gfx.Rect{X: 0, Y: l.tabs.H, W: s.W, H: s.H - l.tabs.H}
	panelW := min(u.px(380), s.W*0.34)
	if a.settings.PanelHidden {
		panelW = 0
	}
	// The timeline is as tall as its tracks need, between a usual height and half the window; the preview gets the rest.
	timelineH := min(u.px(300), l.body.H*0.46)
	if a.p != nil {
		_, lanes := a.laneOrder()
		timelineH = max(timelineH, min(a.timelineChrome()+lanes, l.body.H*0.5))
	}
	l.panel = gfx.Rect{X: s.W - panelW, Y: l.body.Y, W: panelW, H: l.body.H - timelineH}
	l.timeline = gfx.Rect{X: 0, Y: s.H - timelineH, W: s.W, H: timelineH}
	main := gfx.Rect{X: 0, Y: l.body.Y, W: s.W - panelW, H: l.body.H - timelineH}
	l.transport = gfx.Rect{X: main.X, Y: main.Bottom() - u.px(52), W: main.W, H: u.px(52)}
	l.preview = gfx.Rect{X: main.X, Y: main.Y, W: main.W, H: main.H - l.transport.H}
	return l
}

// Close writes every unsaved edit, stops playback, exports and other background work, and waits a moment for their
// decoders to let go of the files.
func (a *App) Close() {
	if a.life.Err() != nil {
		return
	}
	a.finishRecordingForClose()
	a.closeScreen()
	a.stopLife()
	a.player.Stop()
	a.audition.Stop()
	if a.export != nil {
		a.export.cancel()
	}
	a.subtitles.cancel()
	for _, d := range a.docs {
		d.save(a.saveDir)
		if d.art != nil {
			a.saveArt(d.art, true)
		}
	}
	if a.services != nil {
		a.services.stop()
	}
	done := make(chan struct{})
	go func() {
		a.running.Wait()
		a.player.Wait()
		a.audition.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(closeWait):
	}
}

// closeWait bounds how long Close waits for cancelled background work, such as an ffmpeg being stopped.
const closeWait = 10 * time.Second

// background runs work on another goroutine with a context that ends when the app closes; Close waits for it, so
// no decoder is left holding a file.
func (a *App) background(work func(ctx context.Context)) {
	a.running.Add(1)
	go func() {
		defer a.running.Done()
		work(a.life)
	}()
}

// Open opens a file, a video, a picture, a sound or a canvas, as if it had been dropped on the window.
func (a *App) Open(path string) { a.openFiles([]string{path}, func() bool { return false }) }

// Idle reports whether nothing is loading or decoding in the background, for tools that capture settled frames.
func (a *App) Idle() bool {
	if a.opening != "" || a.shown.busy || len(a.jobs) > 0 || !a.tools.ready && a.tools.err == nil {
		return false
	}
	if a.p != nil && !a.media.settled(a.p) {
		return false
	}
	return !a.music.downloading()
}
