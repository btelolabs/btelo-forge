package ui

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/btelolabs/btelo-forge/internal/assets"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/gpu"
	gpuwgpu "github.com/btelolabs/btelo-forge/internal/gpu/wgpu"
	"github.com/btelolabs/btelo-forge/internal/render3d"
	"github.com/btelolabs/btelo-forge/internal/scene3d"
)

const icModel = ""

// colHintBack keeps words readable over whatever the view shows, and modelHue marks the tool and its files.
var (
	colHintBack = color.RGBA{A: 150}
	modelHue    = gfx.Hex(0x56c7b8)
)

// modelExtensions are the 3D files the tool opens.
var modelExtensions = assets.ModelExtensions

func isModelFile(path string) bool {
	return assets.KindOf(path) == assets.Model
}

// newModelDevice connects to the graphics card; tests replace it.
var newModelDevice = gpuwgpu.Shared

// modelCardTurn lets one goroutine at a time use the card: one that is ending may still be letting go of what it holds.
var modelCardTurn sync.Mutex

// The words that reach the tool as data.
var (
	modelSkyNames = msgs("Clear day", "Overcast", "Sunset", "Night", "Studio")
	modelNotes    = msgs("Points and lines are not shown.", "Some pictures of the file were not found.")
	modelChannels = msgs("Base colour", "Roughness", "Metallic", "Emission")
	modelCounts   = msgs("Triangles", "Vertices", "Materials", "Pictures", "Bones", "Animations")
	modelPanels   = msgs("Model", "Material", "Paint", "Clips", "View", "Sky", "File")
)

// The panel's tabs, in the order of modelPanels.
const (
	modelPanelShape = iota
	modelPanelMaterial
	modelPanelPaint
	modelPanelClips
	modelPanelView
	modelPanelSky
	modelPanelFile
)

// modelDoc is a 3D tab: one asset and how it is looked at.
type modelDoc struct {
	path     string // the file, if any
	loading  bool
	asset    *scene3d.Asset
	surfaces []scene3d.Surface
	bounds   scene3d.Box
	stats    scene3d.Stats
	version  int // counts the assets shown in this tab, to tell the card when to replace what it holds
	geometry int // counts the shapes
	overlay  int // counts what is drawn over them
	tris     []float32
	lines    []float32
	picker   *scene3d.Picker
	edit     *modelEdit // the mesh being modelled, if any
	chosen   int        // the chosen object's node, or -1
	undo     []*modelShot
	redo     []*modelShot

	clip          int     // the animation shown, or -1 for the file's own pose
	clipTime      float64 // seconds into it
	playing       bool
	speed         int          // into modelSpeeds
	bones         bool         // the skeleton shows
	pivot         scene3d.Vec3 // the middle of what is chosen, for the handles
	hasPivot      bool
	weights       bool         // the mouse paints the chosen bone's weights
	ikChain       int          // bones a moved bone pulls along, itself included, or 0
	paintChannel  int          // what the brush paints, into paintChannels
	paintValue    float64      // how much of it, for what is one number
	uvOpen        bool         // the UV editor shows
	uvX, uvY      float32      // where a drag there last was
	links         []*modelLink // pictures open in the drawing tool
	traceTick     int          // counts frames of the tracer asked for
	traceSamples  int
	tracedDone    bool
	bone          int                   // the chosen bone's node, or -1
	lastObject    int                   // the object chosen last, which a bind goes to
	poseEdits     int                   // counts changes to a pose
	pictures      map[int]*modelPicture // those opened for painting
	stroke        *modelPicture         // the one a stroke is on
	strokeAt      scene3d.Vec3
	painted       int // counts changes to pictures
	paintImage    int
	paintVersion  int
	paintStart    *modelPicture // waits to go to the card
	thumb         *image.RGBA
	thumbGeometry int
	material      int  // the material the panel shows, or -1
	materialFor   int  // the object it was chosen for
	materials     int  // counts changes to materials' numbers
	keyDrag       bool // a shape key's slider is held
	wander        bool // the clip may carry the model across the ground

	objectTool  *objectTool // moving the chosen object, if it is
	listScroll  float32
	listContent float32
	dirty       bool

	cam      *render3d.Camera
	options  render3d.Options
	sky      string // the chosen sky's id, or a .hdr file's path
	exposure float64
	turnSky  float64 // degrees

	picture *image.RGBA // the last frame the card finished
	spare   *image.RGBA // the one before, drawn on next
	drawn   modelFrame  // what picture shows, or is being drawn
	stepped time.Time
	lastX   float32
	lastY   float32
	turning bool
	panning bool
	holding uint16 // the key that is turning the view
	zooming bool   // Z is zooming the view, at the point (zoomX, zoomY)
	zoomX   float32
	zoomY   float32
	panel   int // the panel's tab in front
	scroll  float32
	content float32
}

// modelFrame is everything a frame depends on, to see when another is needed.
type modelFrame struct {
	doc        *modelDoc
	version    int
	geometry   int
	overlay    int
	pos        scene3d.Vec3
	yaw, pitch float32
	options    render3d.Options
	sky        string
	skyVersion int
	w, h       int
	clip       int
	time       float32
	bones      bool
	wander     bool
	materials  int
	painted    int
	poseEdits  int
	bone       int
	trace      int
}

// modelState is what the 3D tabs share: the goroutine that owns the graphics card.
type modelState struct {
	skies      render3d.Skies
	skyVersion int // counts changes to the list
	requests   chan modelRequest
	stop       context.CancelFunc
	busy       bool
	card       gpu.Info
	failed     string // why nothing can be drawn
}

// modelRequest asks the card for one frame.
type modelRequest struct {
	frame    modelFrame
	cam      render3d.Camera
	asset    *scene3d.Asset
	surfaces []scene3d.Surface
	bounds   scene3d.Box
	tris     []float32
	lines    []float32
	sky      func() (render3d.Panorama, render3d.Sun, error)
	out      *image.RGBA
	// materials is a copy of the asset's, which the window may change while the card reads.
	materials []scene3d.Material
	// What was painted: paintFrom is a copy of a whole picture the card has to get ready for, paintPix a copy of paintRect.
	paintFrom  *image.NRGBA
	paintColor bool // it is read as colour
	paintIndex int
	paintRect  image.Rectangle
	paintPix   []byte
}

func (md *modelDoc) title(a *App) string {
	name := a.u.t("3D assets")
	if md.path != "" {
		name = filepath.Base(md.path)
	}
	if md.dirty {
		return "• " + name
	}
	return name
}

// openModelTab shows an empty 3D tab.
func (a *App) openModelTab() {
	if a.occupied() {
		a.newTab()
	}
	a.model = a.newModelDoc("")
	a.requestFrame()
}

func (a *App) newModelDoc(path string) *modelDoc {
	return &modelDoc{path: path, paintValue: .5, bone: -1, lastObject: -1, paintImage: -1, chosen: -1, materialFor: -1, material: 0, clip: -1, speed: 2, bounds: scene3d.EmptyBox(), cam: render3d.NewCamera(scene3d.EmptyBox()), exposure: 1, sky: a.models.skies.Skies[0].ID,
		options: render3d.Options{Wireframe: true, Shadows: true, Grid: true, ShowSky: true, Exposure: 1}, stepped: time.Now()}
}

// openModelFile opens a 3D file in a tab of its own, or brings forward the one that has it.
func (a *App) openModelFile(path string) {
	for i, d := range a.docs {
		if d.model != nil && d.model.path == path {
			a.switchTab(i)
			return
		}
	}
	if a.showRestoring(path) {
		return
	}
	if a.model == nil || a.model.path != "" || a.model.asset != nil {
		if a.occupied() {
			a.newTab()
		}
		a.model = a.newModelDoc(path)
	}
	md := a.model
	md.path, md.loading = path, true
	a.requestFrame()
	a.background(func(context.Context) {
		asset, err := scene3d.Load(path)
		if err != nil {
			a.post(func() {
				md.loading = false
				a.toast(a.u.tf("Cannot open %s: %s", filepath.Base(path), a.describe(err)), true)
			})
			return
		}
		surfaces, bounds := asset.Surfaces()
		picker := scene3d.NewPicker(surfaces)
		a.post(func() {
			md.loading = false
			md.asset, md.surfaces, md.bounds, md.stats = asset, surfaces, bounds, asset.Stats()
			md.version++
			md.geometry++
			md.picker = picker
			md.cam.Pick = picker.Ray
			md.cam.Frame(bounds)
			a.requestFrame()
		})
	})
}

// chooseModel asks for a file to open.
func (a *App) chooseModel() {
	a.pickFiles(pickRequest{key: "model", title: a.u.t("Open a 3D file"), kinds: a.u.t("3D files"), exts: modelExtensions}, func(paths []string) {
		a.openModelFile(paths[0])
	})
}

// chooseSkyFile asks for a picture of a sky.
func (a *App) chooseSkyFile(md *modelDoc) {
	a.pickFiles(pickRequest{key: "sky", title: a.u.t("Open a sky picture"), kinds: a.u.t("HDR pictures"), exts: []string{".hdr"}}, func(paths []string) {
		md.sky = paths[0]
		a.requestFrame()
	})
}

// startModelCard starts the goroutine that owns the graphics card, unless it runs.
func (a *App) startModelCard() {
	m := &a.models
	if m.requests != nil {
		return
	}
	requests := make(chan modelRequest, 1)
	ctx, stop := context.WithCancel(a.life)
	m.requests, m.stop = requests, stop
	a.background(func(context.Context) { a.modelCard(ctx, requests) })
}

// stopModelCard lets the graphics card go once no 3D tab is left, with all it holds.
func (a *App) stopModelCard() {
	m := &a.models
	if m.stop == nil || slices.ContainsFunc(a.docs, func(d *doc) bool { return d.model != nil }) {
		return
	}
	m.stop()
	m.requests, m.stop, m.busy, m.failed = nil, nil, false, ""
}

// modelCard draws what is asked, one frame at a time, away from the window's goroutine.
func (a *App) modelCard(ctx context.Context, requests <-chan modelRequest) {
	modelCardTurn.Lock()
	defer modelCardTurn.Unlock()
	if ctx.Err() != nil {
		return
	}
	d, err := newModelDevice()
	if err != nil {
		a.post(func() { a.models.failed = err.Error() })
		return
	}
	r, err := render3d.New(d)
	if err != nil {
		a.post(func() { a.models.failed = err.Error() })
		return
	}
	defer r.Close()
	info := d.Info()
	a.post(func() { a.models.card = info })
	var held struct {
		doc                        *modelDoc
		version, geometry, overlay int
		sky                        string
		skies                      int
		posed, boned               bool
		materials                  int
	}
	for {
		var q modelRequest
		select {
		case <-ctx.Done():
			return
		case q = <-requests:
		}
		f := q.frame
		samples, done := 0, false
		err := func() error {
			if held.sky != f.sky || held.skies != f.skyVersion {
				p, sun, err := q.sky()
				if err != nil {
					return err
				}
				if err := r.SetSky(p, sun); err != nil {
					return err
				}
				held.sky, held.skies = f.sky, f.skyVersion
			}
			if held.doc != f.doc || held.version != f.version {
				asset := q.asset
				if asset == nil {
					asset = &scene3d.Asset{}
				}
				problems, err := r.SetAsset(asset)
				if err != nil {
					return err
				}
				held.doc, held.version, held.geometry, held.overlay, held.posed = f.doc, f.version, -1, -1, false
				if len(problems) > 0 {
					a.post(func() { a.toast(a.u.tf("Some pictures could not be read: %s", strings.Join(problems, "; ")), true) })
				}
			}
			if held.materials != f.materials {
				if err := r.UpdateMaterials(q.materials); err != nil {
					return err
				}
				held.materials = f.materials
			}
			if held.geometry != f.geometry {
				if err := r.SetSurfaces(q.surfaces, q.bounds); err != nil {
					return err
				}
				held.geometry, held.posed = f.geometry, false
			}
			if f.clip >= 0 && held.overlay != -2 { // what marks the file's own pose would hang in the air
				if err := r.SetOverlay(nil, nil); err != nil {
					return err
				}
				held.overlay = -2
			} else if f.clip < 0 && held.overlay != f.overlay {
				if err := r.SetOverlay(q.tris, q.lines); err != nil {
					return err
				}
				held.overlay = f.overlay
			}
			if q.paintFrom != nil {
				if err := r.BeginPaint(q.paintIndex, q.paintFrom, q.paintColor); err != nil {
					return err
				}
			}
			if q.paintPix != nil {
				if err := r.Paint(q.paintRect, q.paintPix); err != nil {
					return err
				}
			}
			if err := a.modelPose(r, q, &held.posed, &held.boned); err != nil {
				return err
			}
			if err := r.Draw(&q.cam, f.options, f.w, f.h, q.out); err != nil {
				return err
			}
			samples, done = r.Samples()
			return nil
		}()
		a.post(func() {
			a.models.busy = false
			md := f.doc
			if err != nil {
				a.toast(a.u.tf("The 3D view could not be drawn: %s", err.Error()), true)
				md.drawn = modelFrame{}
				if err := a.modelSkyFallback(md, f.sky); err != nil {
					a.models.failed = err.Error()
				}
				return
			}
			md.spare, md.picture = md.picture, q.out
			md.traceSamples, md.tracedDone = samples, done && f.options.View == render3d.ViewTraced
			a.requestFrame()
		})
	}
}

// modelPose gives the card the frame's pose and skeleton. It runs beside the window, as skinning takes the time of a frame
// on a big model; the asset holds still meanwhile, as editing it stops the clip.
func (a *App) modelPose(r *render3d.Renderer, q modelRequest, posed, boned *bool) error {
	f := q.frame
	if q.asset == nil {
		return nil
	}
	if f.clip < 0 && !f.bones {
		if *boned {
			*boned = false
			if err := r.SetBones(nil, nil); err != nil {
				return err
			}
		}
		if *posed {
			*posed = false
			return r.SetSurfaces(q.surfaces, q.bounds)
		}
		return nil
	}
	var worlds []scene3d.Mat4
	if f.clip >= 0 {
		if f.wander {
			worlds = q.asset.WorldsAt(f.clip, f.time)
		} else {
			worlds = q.asset.WorldsInPlace(f.clip, f.time)
		}
		surfaces, bounds := q.asset.SurfacesIn(worlds, q.asset.KeysAt(f.clip, f.time))
		if err := r.Repose(surfaces, bounds); err != nil {
			return err
		}
		*posed = true
	} else {
		worlds = q.asset.Worlds()
		if *posed {
			*posed = false
			if err := r.SetSurfaces(q.surfaces, q.bounds); err != nil {
				return err
			}
		}
	}
	if !f.bones {
		if *boned {
			*boned = false
			return r.SetBones(nil, nil)
		}
		return nil
	}
	*boned = true
	tris, lines := modelSkeleton(q.asset, worlds, &q.cam, float32(f.w), float32(f.h), f.options.LineScale, f.bone)
	return r.SetBones(tris, lines)
}

// modelSkyFallback goes back to the first sky when the chosen one was what failed, as a broken picture is.
func (a *App) modelSkyFallback(md *modelDoc, sky string) error {
	if a.skyOf(sky) != nil {
		return fmt.Errorf("the graphics card stopped working")
	}
	md.sky = a.models.skies.Skies[0].ID
	return nil
}

func (a *App) skyOf(id string) *render3d.Sky {
	for i := range a.models.skies.Skies {
		if a.models.skies.Skies[i].ID == id {
			return &a.models.skies.Skies[i]
		}
	}
	return nil
}

// skySource is how to get the sky md shows, on the card's goroutine.
func (a *App) skySource(md *modelDoc) func() (render3d.Panorama, render3d.Sun, error) {
	if k := a.skyOf(md.sky); k != nil {
		sky := *k
		return func() (render3d.Panorama, render3d.Sun, error) { return sky.Build(), sky.Sun, nil }
	}
	path := md.sky
	return func() (render3d.Panorama, render3d.Sun, error) {
		p, err := render3d.LoadHDR(path)
		if err != nil {
			return p, render3d.Sun{}, err
		}
		p, sun := p.ExtractSun()
		return p, sun, nil
	}
}

// modelMoving reports whether the view in front wants frames of its own: the camera easing, or keys held.
func (a *App) modelMoving() bool {
	md := a.model
	if md == nil || a.u.inactive {
		return false
	}
	return md.turning || md.panning || md.cam.Moving() || a.modelKeysHeld() || md.tooling() || md.playing || md.options.View == render3d.ViewTraced && !md.tracedDone
}

func (a *App) modelKeysHeld() bool {
	u := &a.u
	return u.focus == "" && !u.ctrl && (a.model == nil || !a.model.tooling()) && (u.isHeld('W') || u.isHeld('A') || u.isHeld('S') || u.isHeld('D'))
}

// modelSettled reports whether the 3D view in front shows what it should, or says why it cannot.
func (a *App) modelSettled() bool {
	md := a.model
	if md == nil || a.models.failed != "" {
		return true
	}
	return !md.loading && md.picture != nil && !a.models.busy && !md.cam.Moving() && md.drawn.pos == md.cam.Pos
}

// modelHeld reports whether code is held as a way of moving the view, which takes the mouse without a button.
func (a *App) modelHeld(md *modelDoc, code uint16) bool {
	u := &a.u
	return u.isHeld(code) && u.focus == "" && !u.ctrl && !u.alt && !u.blocked && !md.tooling()
}

// modelHolding reports whether a key is moving the view.
func (a *App) modelHolding(md *modelDoc) bool {
	return a.modelHeld(md, keySpace) || a.modelHeld(md, 'Z') || a.modelHeld(md, 'C')
}

// navigateModel moves the camera by the mouse and keys over the view r: the right button or C turns, the middle button
// or Space moves and Z zooms by moving sideways, the keys needing no button.
func (a *App) navigateModel(md *modelDoc, r gfx.Rect) {
	u := &a.u
	cam := md.cam
	x, y := u.mx-r.X, u.my-r.Y
	dx, dy := u.mx-md.lastX, u.my-md.lastY
	md.lastX, md.lastY = u.mx, u.my
	now := time.Now()
	dt := min(float32(now.Sub(md.stepped).Seconds()), .1)
	md.stepped = now
	a.stepClip(md, dt)

	inside := !u.blocked && r.Contains(u.mx, u.my)
	modal := md.tooling()
	if md.turning && !u.rightDown && (md.holding == 0 || !a.modelHeld(md, md.holding)) {
		md.turning, md.holding = false, 0
		cam.Release()
	}
	if !modal && u.rightAt != nil && !u.blocked && r.Contains(u.rightAt.X, u.rightAt.Y) {
		md.turning = true
		cam.Grab(u.rightAt.X-r.X, u.rightAt.Y-r.Y, r.W, r.H, !u.alt)
		dx, dy = 0, 0
	}
	if !md.turning && inside && a.modelHeld(md, 'C') {
		md.turning, md.holding = true, 'C'
		cam.Grab(x, y, r.W, r.H, true)
		dx, dy = 0, 0
	}
	if md.turning {
		cam.Turn(dx, dy, r.H)
	}
	md.panning = !u.blocked && (u.middleDown || a.modelHeld(md, keySpace)) && (md.panning || inside)
	if md.panning {
		cam.Pan(dx, dy, r.H)
	}
	if !md.zooming && inside && a.modelHeld(md, 'Z') {
		md.zooming, md.zoomX, md.zoomY = true, x, y
	} else if md.zooming && !a.modelHeld(md, 'Z') {
		md.zooming = false
	}
	if md.zooming {
		cam.Zoom(dx/u.px(modelZoomScrub), md.zoomX, md.zoomY, r.W, r.H)
	}
	if inside {
		cam.Zoom(u.wheel, x, y, r.W, r.H)
		if md.panning {
			u.cursor = CursorMove
		} else if md.zooming {
			u.cursor = CursorCross
		}
	}
	if a.modelKeysHeld() {
		var right, forward float32
		for _, k := range []struct {
			code    uint16
			rx, fwd float32
		}{{'W', 0, 1}, {'S', 0, -1}, {'D', 1, 0}, {'A', -1, 0}} {
			if u.isHeld(k.code) {
				right += k.rx
				forward += k.fwd
			}
		}
		cam.Move(right, forward, dt, u.shift)
	}
	cam.Advance(dt)
}

// askModelFrame sends a frame to the card when the view has changed and the card is free.
func (a *App) askModelFrame(md *modelDoc, w, h int) {
	m := &a.models
	a.startModelCard()
	md.options.Exposure = float32(md.exposure)
	md.options.SkyRotation = float32(md.turnSky) * 3.14159265 / 180
	md.options.LineScale = a.u.px(1)
	options := md.options
	options.Wireframe = md.options.Wireframe && md.edit == nil // while modelling the overlay shows the polygons
	if options.View == render3d.ViewTraced {
		if md.edit != nil || md.clip >= 0 && md.playing {
			options.View = render3d.ViewMaterial // while it moves all the time, or has lines to show
		} else if !md.tracedDone {
			md.traceTick++
		}
	}
	f := modelFrame{trace: md.traceTick, poseEdits: md.poseEdits, bone: md.bone, painted: md.painted, materials: md.materials, clip: md.clip, time: float32(md.clipTime), bones: md.bones, wander: md.wander, doc: md, version: md.version, geometry: md.geometry, overlay: md.overlay, pos: md.cam.Pos, yaw: md.cam.Yaw, pitch: md.cam.Pitch, options: options, sky: md.sky, skyVersion: m.skyVersion, w: w, h: h}
	if m.busy || m.failed != "" || f == md.drawn {
		return
	}
	out := md.spare
	md.spare = nil
	if out == nil || out.Rect.Dx() != w || out.Rect.Dy() != h {
		out = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	select {
	case m.requests <- a.takePaint(md, modelRequest{materials: materialsOf(md), frame: f, cam: *md.cam, asset: cardAsset(md), surfaces: md.surfaces, bounds: md.bounds, tris: md.tris, lines: md.lines, sky: a.skySource(md), out: out}):
		m.busy, md.drawn = true, f
	default:
	}
}

// typedNow is the number being typed for a tool.
func (md *modelDoc) typedNow() string {
	switch {
	case md.objectTool != nil:
		return md.objectTool.typed
	case md.edit != nil && md.edit.modal != nil:
		return md.edit.modal.typed
	}
	return ""
}

// cardAsset is the asset as it now is for the card, whose goroutine reads it while the window goes on: what changes during
// a tool is replaced, not changed in place, so a copy of the top holds still.
func cardAsset(md *modelDoc) *scene3d.Asset {
	if md.asset == nil {
		return nil
	}
	v := *md.asset
	return &v
}

// takePaint adds to a request what the card has to know of what was painted.
func (a *App) takePaint(md *modelDoc, q modelRequest) modelRequest {
	if start := md.paintStart; start != nil {
		md.paintStart = nil
		q.paintIndex, q.paintColor = start.image, start.slot != slotMetalRough
		q.paintFrom = &image.NRGBA{Pix: slices.Clone(start.im.Pix), Stride: start.im.Stride, Rect: start.im.Rect}
	}
	if p := md.pictures[md.paintImage]; p != nil && !p.pending.Empty() {
		q.paintRect = p.pending
		q.paintPix = copyPatch(p.image, p.im.Pix, p.im.Rect.Dx(), p.pending).pix
		p.pending = image.Rectangle{}
	}
	return q
}

func materialsOf(md *modelDoc) []scene3d.Material {
	if md.asset == nil {
		return nil
	}
	return md.asset.Materials
}

// drawModel is a 3D tab: the view, with the panel beside it.
func (a *App) drawModel(r gfx.Rect) {
	u := &a.u
	md := a.model
	a.pullDrawings(md)
	side := u.px(264)
	if r.W < u.px(700) {
		side = 0
	}
	view := rectAt(r.X, r.Y, r.W-side, r.H)
	if side > 0 {
		a.drawModelPanel(md, rectAt(view.Right(), r.Y, side, r.H))
	}
	if md.uvOpen && md.edit != nil {
		half := view.W / 2
		a.drawUVEditor(md, rectAt(view.X+half, view.Y, half, view.H))
		view.W = half
	}
	a.modelKeys(md, view)
	if !u.blocked {
		if !md.paintingNow() && !md.paintingWeights() {
			a.gizmoPress(md, view)
		}
		if md.edit != nil {
			a.editMouse(md, view)
		} else if md.paintingNow() {
			a.paintMouse(md, view)
		} else if md.paintingWeights() {
			a.weightMouse(md, view)
		} else if md.asset != nil {
			a.objectMouse(md, view)
		}
	}
	a.navigateModel(md, view)
	u.c.Fill(view, colBackground)
	w, h := int(view.W), int(view.H)
	if w > 0 && h > 0 {
		a.askModelFrame(md, w, h)
	}
	if p := md.picture; p != nil {
		u.c.Push(view)
		u.c.Blit(p, int(view.X), int(view.Y), true)
		u.c.Pop()
	}
	if md.paintingNow() || md.paintingWeights() {
		a.drawBrushRing(md, view)
	} else {
		a.drawGizmo(md, view)
	}
	a.drawClipBar(md, view)
	a.drawModelOverlay(md, view)
	if e := md.edit; e != nil && e.boxing && u.dragging("model/pick") {
		box := rectAt(min(e.boxX, u.mx), min(e.boxY, u.my), max(u.mx-e.boxX, e.boxX-u.mx), max(u.my-e.boxY, e.boxY-u.my))
		u.c.Outline(box, max(1, u.px(1)), colWhite)
	}
}

// modelZoomScrub is how far, in logical pixels, moving sideways with Z held counts as a notch of the wheel.
const modelZoomScrub = 24

// modelKeys does what the keys pressed do in the tab, leaving the others to the window.
func (a *App) modelKeys(md *modelDoc, view gfx.Rect) {
	u := &a.u
	if u.focus != "" || u.blocked {
		return
	}
	kept := u.keys[:0:0]
	for _, k := range u.keys {
		if k.Code == 'F' && !k.Ctrl && !k.Alt && !k.Shift {
			md.cam.Frame(md.bounds)
			continue
		}
		if (k.Code == keySpace || k.Code == 'Z' || k.Code == 'C') && !k.Ctrl && !k.Alt && !md.tooling() {
			continue // held to move the view
		}
		if !a.modelKey(md, view, k) && (md.edit != nil || !a.objectKey(md, view, k)) {
			kept = append(kept, k)
		}
	}
	u.keys = kept
}

// drawModelOverlay says what the view lacks, and how to move around in it.
func (a *App) drawModelOverlay(md *modelDoc, view gfx.Rect) {
	u := &a.u
	m := &a.models
	center := rectAt(view.X, view.Y+view.H/2-u.px(44), view.W, u.px(24))
	switch {
	case m.failed != "":
		u.label(u.bold(14), center, u.t("The 3D view needs a graphics card"), colDangerText, .5)
		u.label(u.face(12), rectAt(view.X+u.px(24), center.Bottom(), view.W-u.px(48), u.px(22)), m.failed, colDim, .5)
	case md.loading:
		u.label(u.bold(14), center, u.tf("Opening %s …", filepath.Base(md.path)), colText, .5)
	case md.asset == nil:
		u.label(u.bold(14), center, u.t("Drop a .glb, .gltf or .fbx file here"), colText, .5)
		text := u.t("Open a 3D file…")
		w := u.buttonWidth(icFolder, text)
		if u.button("model/open-empty", rectAt(view.X+(view.W-w)/2, center.Bottom()+u.px(10), w, u.px(32)), icFolder, text, ghost, true) {
			a.chooseModel()
		}
	}
	if md.clip >= 0 {
		view.H -= u.px(timelineHigh)
	}
	if md.asset != nil {
		text := u.t("Click chooses an object · G moves, R turns, T scales · X deletes · Tab edits it")
		if md.edit != nil {
			text = u.t("Click chooses, Shift adds, drag chooses a box · G moves, R turns, T scales · X, Y, Z hold to an axis · Esc takes it back")
		}
		if md.tooling() {
			text = u.t("Move the mouse · Click or Enter keeps it · Right click or Esc takes it back · X, Y, Z hold to an axis")
		}
		f := u.face(11.5)
		text = f.Ellipsize(text, view.W-u.px(40))
		hint := rectAt(view.X+u.px(10), view.Bottom()-u.px(58), f.Measure(text)+u.px(16), u.px(22))
		u.c.Fill(hint, colHintBack)
		u.label(f, hint, text, colText, .5)
	}
	if typed := md.typedNow(); typed != "" {
		f := u.bold(14)
		box := rectAt(u.mx+u.px(16), u.my+u.px(16), f.Measure(typed)+u.px(16), u.px(26))
		u.c.Fill(box, colHintBack)
		u.label(f, box, typed, colWhite, .5)
	}
	f := u.face(11.5)
	text := f.Ellipsize(u.t("Right drag or C turns · Middle drag or Space moves · Wheel or Z zooms · WASD moves · F shows everything"), view.W-u.px(40))
	hint := rectAt(view.X+u.px(10), view.Bottom()-u.px(32), f.Measure(text)+u.px(16), u.px(22))
	u.c.Fill(hint, colHintBack)
	u.label(f, hint, text, colText, .5)
}

// drawModelPanel is what the tab offers beside the view: the file's buttons, and the rest behind tabs of its own.
func (a *App) drawModelPanel(md *modelDoc, r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colPanel)
	u.c.Fill(rectAt(r.X, r.Y, max(1, u.px(1)), r.H), colBorder)
	pad := u.px(14)
	x, w := r.X+pad, r.W-pad*2
	y := r.Y + u.px(12)
	half := (w - u.px(6)) / 2
	if u.button("model/open", rectAt(x, y, half, u.px(30)), icFolder, u.t("Open"), plain, !md.loading) {
		a.chooseModel()
	}
	if u.button("model/save", rectAt(x+half+u.px(6), y, half, u.px(30)), "", u.t("Save"), plain, md.asset != nil) {
		a.saveModel(md)
	}
	y += u.px(38)
	for row, span := range [][2]int{{0, 4}, {4, 7}} {
		from := span[0]
		names := modelPanels[span[0]:span[1]]
		shown := md.panel - from
		if shown < 0 || shown >= len(names) {
			shown = -1
		}
		if pick := u.chips(fmt.Sprintf("model/panel/tab%d", row), rectAt(x, y, w, u.px(26)), u.ts(names), shown, true); pick != shown && pick >= 0 {
			md.panel, md.scroll = from+pick, 0
		}
		y += u.px(30)
	}
	y += u.px(8)
	u.c.Fill(rectAt(r.X, y-max(1, u.px(1)), r.W, max(1, u.px(1))), colBorder)
	body := rectAt(r.X, y, r.W, r.Bottom()-y)
	a.scrolledBy("model/panel", body, &md.scroll, &md.content, func(view gfx.Rect) float32 {
		top := view.Y + u.px(12)
		switch md.panel {
		case modelPanelView:
			return a.drawModelView(md, x, top, w)
		case modelPanelSky:
			return a.drawModelSky(md, x, top, w)
		case modelPanelClips:
			return a.drawModelClips(md, x, top, w)
		case modelPanelMaterial:
			return a.drawModelMaterial(md, x, top, w)
		case modelPanelPaint:
			return a.drawModelPaint(md, x, top, w)
		case modelPanelFile:
			return a.drawModelFile(md, x, top, w)
		}
		return a.drawModelShape(md, x, top, w)
	})
}

// drawModelShape is the panel's tab for changing the mesh.
func (a *App) drawModelShape(md *modelDoc, x, y, w float32) float32 {
	u := &a.u
	y = a.drawObjectList(md, x, y, w)
	y = a.drawShapeKeys(md, x, y, w)
	label := u.t("Edit the mesh")
	if md.edit != nil {
		label = u.t("Stop editing")
	}
	if u.button("model/edit", rectAt(x, y, w, u.px(30)), "", label, kindOfSky(md.edit != nil), md.asset != nil) {
		if md.edit != nil {
			a.leaveEdit(md)
		} else {
			a.enterEdit(md, gfx.Rect{})
		}
	}
	u.hint("model/edit", withKey(label, "Tab"), rectAt(x, y, w, u.px(30)))
	y += u.px(36)
	if md.edit != nil {
		y = a.drawEditPanel(md, x, y, w)
	}
	half := (w - u.px(6)) / 2
	y += u.px(6)
	u.label(u.bold(12), rectAt(x, y, w, u.px(20)), u.t("Add"), colDim, 0)
	y += u.px(22)
	for _, row := range [][2]int{{0, 1}, {2, 3}} {
		for c, p := range row {
			if a.modelTool(fmt.Sprintf("model/add%d", p), rectAt(x+float32(c)*(half+u.px(6)), y, half, u.px(28)), u.t(primitiveNames[p]), fmt.Sprintf("Shift+%d", p+1), !md.tooling()) {
				a.addPrimitive(md, scene3d.Primitive(p))
			}
		}
		y += u.px(32)
	}
	return y
}

// drawModelView is the panel's tab for how the model is shown.
func (a *App) drawModelView(md *modelDoc, x, y, w float32) float32 {
	u := &a.u
	modes := []string{u.t("Material"), u.t("Solid"), u.t("Normals"), u.t("UV"), u.t("Traced")}
	md.options.View = render3d.ViewMode(u.chips("model/mode", rectAt(x, y, w, u.px(28)), modes, int(md.options.View), true))
	y += u.px(36)
	for _, c := range []struct {
		id, text string
		on       *bool
	}{{"model/wire", u.t("Wireframe"), &md.options.Wireframe}, {"model/grid", u.t("Grid"), &md.options.Grid}, {"model/shadows", u.t("Shadows"), &md.options.Shadows}} {
		*c.on = u.checkbox(c.id, rectAt(x, y, w, u.px(26)), c.text, *c.on)
		y += u.px(26)
	}
	if md.options.View == render3d.ViewTraced {
		md.options.NoBounces = !u.checkbox("model/gi", rectAt(x, y, w, u.px(26)), u.t("Bounced light"), !md.options.NoBounces)
		y += u.px(30)
		u.label(u.face(12), rectAt(x, y, w, u.px(18)), u.tf("%d samples", md.traceSamples), colDim, 0)
		y += u.px(22)
	}
	return y
}

// drawModelSky is the panel's tab for the light.
func (a *App) drawModelSky(md *modelDoc, x, y, w float32) float32 {
	u := &a.u
	md.options.ShowSky = u.checkbox("model/showsky", rectAt(x, y, w, u.px(26)), u.t("Show the sky"), md.options.ShowSky)
	y += u.px(32)
	half := (w - u.px(6)) / 2
	skies := a.models.skies.Skies
	for i, k := range skies {
		r := rectAt(x+float32(i%2)*(half+u.px(6)), y+float32(i/2)*u.px(32), half, u.px(28))
		if u.button("model/sky/"+k.ID, r, "", u.t(k.Name), kindOfSky(md.sky == k.ID), true) {
			md.sky = k.ID
		}
	}
	y += float32((len(skies)+1)/2) * u.px(32)
	custom := a.skyOf(md.sky) == nil
	name := u.t("A picture…")
	if custom {
		name = filepath.Base(md.sky)
	}
	if u.button("model/sky/file", rectAt(x, y, w, u.px(28)), "", name, kindOfSky(custom), true) {
		a.chooseSkyFile(md)
	}
	y += u.px(38)
	for _, s := range []struct {
		id, text string
		value    *float64
		lo, hi   float64
	}{{"model/exposure", u.t("Exposure"), &md.exposure, .1, 4}, {"model/turnsky", u.t("Turn the sky"), &md.turnSky, 0, 360}} {
		u.label(u.face(12.5), rectAt(x, y, w, u.px(18)), s.text, colDim, 0)
		y += u.px(20)
		u.slider(s.id, rectAt(x, y, w, u.px(22)), s.value, s.lo, s.hi, true)
		y += u.px(26)
	}
	return y
}

// drawModelFile is the panel's tab for what the file holds and what draws it.
func (a *App) drawModelFile(md *modelDoc, x, y, w float32) float32 {
	u := &a.u
	if md.asset != nil {
		if u.button("model/fbx", rectAt(x, y, w, u.px(30)), "", u.t("Export as FBX"), ghost, true) {
			a.exportFBX(md)
		}
		y += u.px(40)
		s := md.stats
		for i, n := range []int{s.Triangles, s.Vertices, s.Materials, s.Images, s.Joints, s.Clips} {
			row := rectAt(x, y, w, u.px(20))
			u.label(u.face(12.5), row, u.t(modelCounts[i]), colDim, 0)
			u.label(u.face(12.5), row, fmt.Sprint(n), colText, 1)
			y += u.px(20)
		}
		y += u.px(14)
		f := u.face(11.5)
		notes := make([]string, 0, len(md.asset.Notes)+len(md.asset.Unshown))
		for _, n := range md.asset.Notes {
			notes = append(notes, u.t(n))
		}
		for _, e := range md.asset.Unshown {
			notes = append(notes, u.tf("%s is not shown.", e))
		}
		for _, note := range notes {
			for _, line := range f.Wrap(note, w) {
				u.label(f, rectAt(x, y, w, u.px(17)), line, colDim, 0)
				y += u.px(17)
			}
		}
	}
	if c := a.models.card; c.Name != "" {
		f := u.face(11)
		for _, line := range f.Wrap(c.Name+" ("+c.Backend+")", w) {
			u.label(f, rectAt(x, y, w, u.px(16)), line, colFaint, 0)
			y += u.px(16)
		}
	}
	return y
}

func kindOfSky(chosen bool) buttonKind {
	if chosen {
		return primary
	}
	return ghost
}
