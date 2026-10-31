// Package ui is the editor: an immediate-mode interface drawn into a canvas, independent of the window that shows it.
package ui

import (
	"context"
	"image"
	"path/filepath"
	"sync"
	"time"

	"github.com/btelolabs/btelo-forge/internal/assets"
	"github.com/btelolabs/btelo-forge/internal/caption"
	"github.com/btelolabs/btelo-forge/internal/capture"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/gpufx"
	"github.com/btelolabs/btelo-forge/internal/library"
	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/office/flow"
	"github.com/btelolabs/btelo-forge/internal/paint"
	"github.com/btelolabs/btelo-forge/internal/playback"
	"github.com/btelolabs/btelo-forge/internal/printing"
	"github.com/btelolabs/btelo-forge/internal/render3d"
	"github.com/btelolabs/btelo-forge/internal/transcribe"
)

const (
	tabMusic = iota
	tabOverlays
	tabSubtitles
	tabSpeech
	tabExport
)

// App is the whole editor. The embedded doc is the active tab's document; every method runs on the UI goroutine
// except where noted.
type App struct {
	*doc
	docs []*doc

	host        Host
	u           ctx
	lib         *library.Library
	player      *playback.Player
	audition    *playback.Audition
	captions    *caption.Renderer
	saveDir     string
	stateDir    string
	officeFonts *flow.Book // what Office documents are drawn with, once one is open
	print       *printDialog
	printTarget *printing.Target // the printer last chosen, with its settings
	printJobs   []*printJob
	printSeq    int
	cacheDir    string
	jobs        chan func()

	life     context.Context // ends when the app closes
	stopLife context.CancelFunc
	running  sync.WaitGroup // background work started with background

	settings        settings
	settingsOpen    bool
	tab             int
	media           mediaCache
	music           musicPanel
	subtitles       subtitlePanel
	speech          speechPanel
	ai              aiState
	mail            mailState
	orb             orbState
	export          *exportJob
	last            lastExport
	known           map[string]library.Track
	toasts          []toast
	settingsPage    int
	extensions      extensionsPage
	mcpScroll       float32 // how far the AI setup text in Settings is scrolled
	exportForm      exportForm
	encoders        media.Encoders // nil until probed
	tools           toolsState
	panelScroll     [5]float32
	rec             recordState
	onAir           liveState   // the live stream
	cam             cameraState // the camera on the screen
	startScroll     float32     // how far the new tab is scrolled, in a window too low for it
	startContent    float32     // and its height at the last frame
	hotkeys         hotkeyState
	watch           watchState
	keysDomain      int         // the domain the Hotkeys page shows
	mcpDomain       int         // the domain whose AI setup the MCP page shows
	keysScroll      float32     // how far its list is scrolled
	keyCapture      *keyCapture // the command whose new key the Hotkeys page waits for
	keys            keyboardState
	brush           paint.Brush // shared by every canvas
	artTool         artTool
	marqueeEllipse  bool               // the marquee draws ellipses
	toolSlots       map[uint16]artTool // the tool each shared place in the tool box shows, by its key
	zoomHeld        *zoomHold          // the zoom tool's key held down, zooming for a moment
	renamingTab     *doc               // the tab whose name is being typed
	tabNameText     string
	vecClip         *vectorClip                // vector shapes cut or copied on a canvas
	nodeClip        *nodeClip                  // a layer, group or smart object cut or copied on a canvas
	typedOpacity    *opacityTyping             // the digit just typed for an opacity, waiting for a second
	tipShapes       map[string]*paint.TipShape // the picture brush tips read, by file
	brushShape      brushOutline               // the brush's shape at the pointer, as last worked out
	smartKeep       smartKeeper                // writes smart objects' shared layers to their folder
	pickBackground  bool                       // the colour panel sets the background colour
	fillStrokeSwap  bool                       // the fill and stroke were swapped: their swatches trade places
	picker          colorPicker
	filePicker      *filePick // the dialog that opens and saves files
	menu            *popupMenu
	dialog          artDialog     // the canvas editor's open dialog
	adjust          *adjustDialog // an adjustment or filter being tried
	smartList       *artDoc       // the canvas whose smart object's filters are listed
	style           *styleDialog  // a layer's effects being edited
	sidePage        int           // the canvas side panel's lower page: layers or history
	layerOpacity    float64       // the layer opacity slider while it is dragged
	opacityMark     paint.Mark    // the picture before that drag
	layersScroll    float32
	thumbs          map[int]*layerThumb // by layer id
	over            screenState         // the windows over the screen
	panelContent    [5]float32          // height of each panel's content at the last frame
	screen          gfx.Rect
	services        *services
	fm              fileManager             // what the file manager tabs share
	fileThumbs      thumbCache              // small pictures of files, for the asset manager and file lists
	videoList       *filesPane              // the video editor's list of folders and videos, made when first shown
	preview         filePreview             // the file previewed in full
	previewPlayer   *playback.Player        // plays the previewed video, apart from the editor's player
	positions       map[string]playPosition // where the videos and sounds previewed were left, by positionKey
	assetLib        *assets.Library         // the tags and categories of files
	assetLibVersion int                     // counts changes to them, for what is laid out from them
	mindPics        *mindPics               // the pictures the mind maps show, read in the background
	mindClip        *mindClipboard          // nodes copied from a mind map
	mindThemeShots  map[string]*image.RGBA  // the theme panel's previews, by theme and size
	upd             updateState             // the newest release and the update under way
	tabDrag         *tabDrag                // the tab held by the mouse, if any
	models          modelState              // what the 3D tabs share
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
	if a.brush.Tip == paint.TipShaped {
		if p, ok := paint.PresetByID(a.settings.Paint.Preset); ok {
			a.brush.Shape = p.Brush.Shape
		} else if a.brush.Shape = a.tipShape(a.settings.Paint.ShapeTip); a.brush.Shape == nil {
			a.brush.Tip = paint.TipRound // its picture is gone
		}
	}
	host.SetTitle(a.Title())
	a.updateStarted()
	a.player = playback.New(host.Wake)
	a.audition = playback.NewAudition(host.Wake)
	a.music.init()
	a.subtitles.config = transcribe.DefaultConfig()
	a.subtitles.manager = transcribe.NewManager(filepath.Join(cacheDir, "whisper"))
	a.speech.init(cacheDir, stateDir)
	a.ai.init(cacheDir, stateDir, host.Wake)
	paint.SetAccelerator(gpufx.New(newModelDevice, &modelCardTurn))
	a.mail.init(stateDir, host.Wake)
	a.ai.rt.Track("speech", a.speechPid)
	a.tools.config = media.DefaultToolsConfig()
	a.models.skies, _ = render3d.ParseSkies(render3d.DefaultSkiesJSON)
	a.onAir.platforms = capture.DefaultPlatforms()
	a.initKeyboard()
	a.loadConfigs()
	a.startKeyboard()
	a.prepareTools()
	a.applyServices()
	a.hotkeys.capturing = -1
	a.keysDomain = domainRecording // the Hotkeys page opens on its first tab, the system-wide hotkeys
	a.applyHotkeys()
	a.refreshFileTypes()
	a.restoreTabs()
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
func (a *App) Frame(in Input, c *gfx.Canvas, scale float32) (next time.Duration) {
	defer func() {
		if r := recover(); r != nil {
			// The frame stopped part way: what it held is let go, and the next one is drawn at once.
			c.ResetClip()
			a.u.active, a.u.focus, a.menu = "", "", nil
			a.failed("drawing a frame", r, true)
			next = redrawNow
		}
	}()
	a.drainJobs()
	a.openFromSystem(in.Opened)
	a.recordTick(in)
	a.screenTick()
	a.updateTick()
	u := &a.u
	u.c, u.s = c, scale*a.windowZoom()
	u.begin(in)
	u.tipSeen, u.blinking = false, false
	a.screen = gfx.Rect{W: float32(c.Img.Rect.Dx()), H: float32(c.Img.Rect.Dy())}
	c.Clear(colBackground)

	a.handleDrops(in.Dropped)
	a.syncPlayback()
	a.preToasts()
	// The file picker is over everything: what is behind it, a dialog that opened it included, gets no input.
	pick := a.filePicker != nil
	var pickIn pickInput
	if pick {
		pickIn = u.takeInput()
	}
	u.blocked = pick || a.export != nil || a.settingsOpen || a.rec.open || a.cam.open || a.menu != nil || a.dialog.kind != dialogNone || a.adjust != nil || a.style != nil || a.print != nil || a.smartList != nil
	held := u.blocked
	a.preTasks()

	a.reservedKeys()
	l := a.layout()
	if a.watch.on && !a.watching() {
		a.watch.on = false // the edit was closed, or another tab came forward
	}
	switch {
	case a.watching():
		a.drawWatching(a.screen)
	case a.slideshowOn():
		a.drawSlideshow(a.screen)
	case a.art != nil:
		a.drawTabs(l.tabs)
		a.drawArt(l.body)
	case a.files != nil:
		a.drawTabs(l.tabs)
		a.drawFiles(l.body)
	case a.board != nil:
		a.drawTabs(l.tabs)
		a.drawMind(l.body)
	case a.office != nil:
		a.drawTabs(l.tabs)
		a.drawOffice(l.body)
	case a.liveTab:
		a.drawTabs(l.tabs)
		a.drawLiveTab(l.body)
	case a.keyboardTab:
		a.drawTabs(l.tabs)
		a.drawKeyboardTab(l.body)
	case a.model != nil:
		a.drawTabs(l.tabs)
		a.drawModel(l.body)
	case a.aiTab:
		a.drawTabs(l.tabs)
		a.drawAITab(l.body)
	case a.mailTab:
		a.drawTabs(l.tabs)
		a.drawMailTab(l.body)
	case a.p == nil && a.editing:
		a.drawTabs(l.tabs)
		a.drawEmptyEditor(l)
	case a.p == nil:
		a.drawTabs(l.tabs)
		a.drawEmpty(l.body)
	default:
		a.drawTabs(l.tabs)
		a.drawPreview(l.preview)
		a.drawTransport(l.transport)
		a.drawTimeline(l.timeline)
		a.drawPanel(l.panel)
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
	a.drawTasks(!held)
	a.drawExportModal()
	if a.export == nil {
		a.drawSettings()
		a.drawRecorder()
		a.drawCameraDialog()
		a.drawArtDialog()
		a.drawAdjustDialog()
		a.drawSmartFilters()
		a.drawStyleDialog()
		a.drawPrintDialog()
	}
	if pick {
		u.giveInput(pickIn)
		u.blocked = a.menu != nil
		a.drawFilePick()
		u.blocked = false
	}
	if pick || a.dialog.kind == dialogNone && (a.adjust == nil || a.menuOpen("adjust/page")) && a.style == nil && a.smartList == nil {
		a.drawOpenMenu()
	}
	a.drawToasts()
	wait := u.drawTip(a.screen)
	u.end()
	a.pumpThumbs()

	if u.focus != "" {
		if f, ok := a.host.(imeFontHost); ok {
			f.TextInputFont(u.imeFont, u.imeFamily)
		}
		a.host.TextInput(true, u.imeAt[0], u.imeAt[1], u.imeAt[2])
	} else {
		a.host.TextInput(false, 0, 0, 0)
	}
	a.host.SetCursor(u.cursor)
	a.autosave()
	a.syncSmart()
	a.autosaveArt()
	a.autosaveMind()
	a.autosaveOffice()
	a.rememberTabs()
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
	if key, _ := a.audition.Playing(); key != "" || a.music.downloading() || a.subtitles.busy() || a.speech.busy() {
		sooner(time.Second / 10)
	}
	if d := a.orbFrame(); d > 0 {
		sooner(d)
	}
	if a.ai.animating() && (a.aiTab || a.settingsOpen) {
		sooner(time.Second / 20)
	}
	if a.u.blinking && !a.u.inactive { // a caret only blinks in the window being typed in
		sooner(265 * time.Millisecond)
	}
	// Edits are saved a moment after they settle: wake then, rather than ticking until it is time.
	for _, d := range a.docs {
		if d.p != nil && d.dirty {
			sooner(max(time.Until(d.savedAt.Add(videoSaveInterval)), redrawNow))
		}
		if ad := d.art; ad != nil {
			if due, ok := a.artSaveDue(ad); ok {
				sooner(max(time.Until(due), redrawNow))
			}
		}
		if md := d.board; md != nil {
			if due, ok := mindSaveDue(md); ok {
				sooner(max(time.Until(due), redrawNow))
			}
		}
		if od := d.office; od != nil {
			if due, ok := officeSaveDue(od); ok {
				sooner(max(time.Until(due), redrawNow))
			}
		}
	}
	sooner(a.mindWait())
	sooner(a.officeWait())
	if a.upd.job != nil {
		sooner(time.Second / 10) // the update's progress
	}
	if _, wait := a.updateDue(); wait > 0 {
		sooner(wait) // the daily check, in a window left alone for days
	}
	if a.art != nil && a.art.layerDrag != nil {
		sooner(time.Second / 60) // a drag in the layers panel scrolls past the list's ends
	}
	if a.pieMoving() {
		sooner(time.Second / 60) // a pie turning to the item under the cursor
	}
	if a.tabsMoving() {
		sooner(time.Second / 60) // tabs sliding to their places
	}
	if a.modelMoving() {
		sooner(time.Second / 60) // the 3D view easing, or being moved by keys held down
	}
	if a.files != nil && a.files.sweep != nil && a.u.rightDown {
		sooner(time.Second / 60) // a sweep scrolls past the list's edge, and opens the menu once the button rests
	}
	// Progress and clocks that change without telling: an export's progress, a recording's time, toasts running
	// out. Work that posts its result (opening a video, checking the screen recorder) wakes the window itself.
	if a.export != nil || len(a.toasts) > 0 || a.recording() || a.streaming() || a.cameraSettling() || a.over.bar != nil || a.fileWork() || a.thumbsBusy() {
		sooner(time.Second / 10)
	}
	if sd := a.over.draw; sd != nil {
		sooner(sd.wait) // a hint on the drawing's panel
	}
	sooner(a.previewWait())
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
	if a.settings.VerticalTabs {
		l.tabs = gfx.Rect{W: min(u.px(180), s.W*0.4), H: s.H}
		l.body = gfx.Rect{X: l.tabs.W, W: s.W - l.tabs.W, H: s.H}
	} else {
		l.tabs = gfx.Rect{W: s.W, H: u.px(40)}
		l.body = gfx.Rect{Y: l.tabs.H, W: s.W, H: s.H - l.tabs.H}
	}
	panelW := min(u.px(380), l.body.W*0.34)
	// The timeline is as tall as its tracks need, between a usual height and half the window; the preview gets the rest.
	timelineH := min(u.px(300), l.body.H*0.46)
	if a.p != nil {
		_, lanes := a.laneOrder()
		timelineH = max(timelineH, min(a.timelineChrome()+lanes, l.body.H*0.5))
	}
	l.panel = gfx.Rect{X: s.W - panelW, Y: l.body.Y, W: panelW, H: l.body.H - timelineH}
	l.timeline = gfx.Rect{X: l.body.X, Y: s.H - timelineH, W: l.body.W, H: timelineH}
	main := gfx.Rect{X: l.body.X, Y: l.body.Y, W: l.body.W - panelW, H: l.body.H - timelineH}
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
	a.keys.service.Close()
	a.finishRecordingForClose()
	a.finishLiveForClose()
	a.closeCamera()
	a.closeScreen()
	a.stopFileWork()
	a.rememberPosition()
	a.rememberTabs()
	for _, d := range a.docs {
		a.keepModel(d.model)
	}
	a.stopLife()
	a.player.Stop()
	a.audition.Stop()
	if a.previewPlayer != nil {
		a.previewPlayer.Stop()
	}
	if a.export != nil {
		a.export.cancel()
	}
	a.subtitles.cancel()
	a.speech.close()
	a.ai.close()
	a.mail.close()
	for _, d := range a.docs {
		d.save(a.saveDir)
		if d.art != nil {
			a.saveArt(d.art, true)
		}
		if d.board != nil {
			a.closeMind(d.board)
		}
		if d.office != nil {
			a.closeOffice(d.office)
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
		if a.previewPlayer != nil {
			a.previewPlayer.Wait()
		}
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
		a.safely("work in the background", work)(a.life)
	}()
}

// OpenFromSystem opens files the system hands the app, as when they are opened from Explorer: each where it
// belongs, in a tab of its own (see openFromSystem).
func (a *App) OpenFromSystem(paths []string) { a.openFromSystem(paths) }

// Idle reports whether nothing is loading or decoding in the background, for tools that capture settled frames.
func (a *App) Idle() bool {
	if a.opening != "" || a.shown.busy || len(a.jobs) > 0 || !a.tools.ready && a.tools.err == nil {
		return false
	}
	if a.p != nil && !a.media.settled(a.p) {
		return false
	}
	if a.thumbsBusy() || a.preview.loading || !a.modelSettled() {
		return false
	}
	return !a.music.downloading() && !a.pieMoving()
}
