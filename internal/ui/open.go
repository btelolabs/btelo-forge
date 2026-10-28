package ui

import (
	"context"
	"image"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/library"
	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/mind"
	"github.com/btelolabs/btelo-forge/internal/paint"
	"github.com/btelolabs/btelo-forge/internal/project"
)

// videoExtensions are offered in the open dialog; any file ffprobe understands can still be dropped.
var videoExtensions = []string{".mp4", ".mov", ".mkv", ".avi", ".webm", ".m4v", ".wmv", ".flv", ".ts", ".mts", ".m2ts", ".3gp", ".mpg", ".mpeg"}

// imageExtensions are accepted as image overlays.
var imageExtensions = []string{".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp"}

func isImage(path string) bool {
	return slices.Contains(imageExtensions, strings.ToLower(filepath.Ext(path)))
}

func (a *App) chooseVideo() {
	var patterns []string
	for _, e := range videoExtensions {
		patterns = append(patterns, "*"+e)
	}
	files := a.host.OpenFiles(a.u.t("Choose a video to edit"), [][2]string{{a.u.t("Videos"), strings.Join(patterns, ";")}, {a.u.t("All files"), "*.*"}}, true)
	for _, f := range files {
		a.openVideo(f, false)
	}
}

// chooseAnyFile opens whatever is chosen as dropping it would: a video to edit, a picture or canvas to draw on, a
// mind map.
func (a *App) chooseAnyFile() {
	var patterns []string
	for _, e := range slices.Concat(videoExtensions, imageExtensions, []string{paint.Ext, mind.Ext}) {
		patterns = append(patterns, "*"+e)
	}
	files := a.host.OpenFiles(a.u.t("Open a file"), [][2]string{{a.u.t("Videos, pictures, canvases and mind maps"), strings.Join(patterns, ";")}, {a.u.t("All files"), "*.*"}}, true)
	a.openFiles(files, func() bool { return false })
}

func (a *App) chooseImage() {
	var patterns []string
	for _, e := range imageExtensions {
		patterns = append(patterns, "*"+e)
	}
	for _, f := range a.host.OpenFiles(a.u.t("Choose an image"), [][2]string{{a.u.t("Images"), strings.Join(patterns, ";")}}, true) {
		a.addImage(f)
	}
}

// handleDrops sorts dropped files: sounds become music, images overlays, anything else is opened as video.
// In a mind map tab what is dropped goes on the board where it is let go.
func (a *App) handleDrops(paths []string) {
	if md := a.board; md != nil && len(paths) > 0 {
		a.mindAddFiles(md, paths, a.mindPointerOr(md))
		return
	}
	a.openFiles(paths, a.dropUpper)
}

// openFiles opens files by what they are, as dropping them does: a canvas as a canvas, sounds as music, a picture as
// a canvas in an empty tab or a layer or overlay where something is open, anything else as video, on an upper
// track when upper says so.
func (a *App) openFiles(paths []string, upper func() bool) {
	var audio []string
	for _, p := range paths {
		switch {
		case isFolder(p):
			a.openFolder(p)
		case strings.EqualFold(filepath.Ext(p), paint.Ext):
			a.openCanvasFile(p)
		case strings.EqualFold(filepath.Ext(p), mind.Ext):
			a.openMindFile(p)
		case isEditFile(p):
			a.openEditFile(p)
		case library.IsAudio(p):
			audio = append(audio, p)
		case isImage(p) && a.p == nil && a.art == nil:
			a.openImageAsArt(p)
		case isImage(p) && a.art != nil:
			if img, err := loadImage(p); err == nil {
				a.art.d.Paste(img, filepath.Base(p), nil)
				a.art.changedAll()
			} else {
				a.toast(a.u.tf("Cannot open %s: %s", filepath.Base(p), a.describe(err)), true)
			}
		case isImage(p):
			a.addImage(p)
		case a.export == nil:
			a.openVideo(p, upper())
		}
	}
	if len(audio) > 0 {
		a.importAudio(audio, a.p != nil)
	}
}

// dropUpper reports whether a dropped video should go on an upper track: when the mouse is over one.
func (a *App) dropUpper() bool {
	if a.p == nil {
		return false
	}
	t := a.tracks(a.layout().timeline)
	for _, lane := range t.lanes {
		if lane.group == groupVideo && lane.track > 0 && lane.rect.Contains(a.u.mx, a.u.my) {
			return true
		}
	}
	return false
}

// occupied reports whether the tab holds something, so what is opened next needs a tab of its own.
func (a *App) occupied() bool {
	return a.p != nil || a.art != nil || a.opening != "" || a.files != nil || a.board != nil || a.liveTab || a.editing
}

// openVideo probes path in the background. An empty tab opens it, restoring an unfinished edit of the same file;
// a tab with an edit appends it to the main track, or places it at the playhead on an upper track.
func (a *App) openVideo(path string, upper bool) {
	if !a.tools.ready {
		a.tools.waiting = append(a.tools.waiting, path)
		a.toast(a.u.tf("%s opens as soon as ffmpeg is ready", filepath.Base(path)), false)
		return
	}
	if a.files != nil || a.art != nil {
		a.newTab() // a video dropped on a tab browsing files or drawing gets a tab of its own
	}
	a.openVideoIn(a.doc, path, upper)
}

// queuedVideo is a video waiting for the one before it to open in the same tab.
type queuedVideo struct {
	path  string
	upper bool
}

// openVideoIn opens a video in tab d; several chosen or dropped at once open one after another, in order.
func (a *App) openVideoIn(d *doc, path string, upper bool) {
	if d.opening != "" {
		d.queued = append(d.queued, queuedVideo{path, upper})
		return
	}
	d.opening = path
	a.background(func(ctx context.Context) {
		info, err := media.Probe(ctx, path)
		a.post(func() {
			d.opening = ""
			defer func() {
				if len(d.queued) > 0 && slices.Contains(a.docs, d) {
					next := d.queued[0]
					d.queued = d.queued[1:]
					a.openVideoIn(d, next.path, next.upper)
				}
			}()
			if err != nil {
				a.toast(a.u.tf("Cannot open %s: %s", filepath.Base(path), a.describe(err)), true)
				return
			}
			if !info.HasVideo {
				a.toast(a.u.tf("%s has no picture; drop sound files to use them as music", filepath.Base(path)), true)
				return
			}
			src := project.Source{Path: path, Duration: info.Duration, Width: info.Width, Height: info.Height, FPS: info.FPS, HasVideo: true, HasAudio: info.HasAudio}
			a.media.start(a, src)
			if d.p == nil {
				a.load(d, src)
				return
			}
			before := d.p.Clone()
			index := d.p.AddSource(src)
			id := 0
			if upper {
				id = d.p.Place(index, d.playhead)
			} else {
				id = d.p.Append(index)
			}
			d.sel = selection{kind: selClip, id: id}
			if d == a.doc {
				a.committed(before)
			} else {
				d.hist.Commit(before)
				d.dirty = true
			}
		})
	})
}

func (a *App) load(d *doc, src project.Source) {
	p, err := project.Load(project.SavePath(a.saveDir, src.Path), src)
	restored := err == nil
	if !restored {
		p = project.New(src, a.settings.Export)
	}
	d.p, d.hist, d.dirty = p, project.History{}, false
	d.playhead, d.sel, d.picked, d.shown, d.view = 0, selection{}, nil, shownFrame{}, timelineView{}
	for _, s := range p.Sources {
		a.media.start(a, s)
	}
	d.shown.stale = true
	if restored {
		a.toast(a.u.t("Restored your unfinished edit of this video"), false)
	}
}

// sourceMedia is what was decoded from one source for the timeline.
type sourceMedia struct {
	thumbStep  float64
	thumbs     []*image.RGBA
	thumbsLeft int
	thumbW     int
	thumbH     int
	peaks      []float32
	peaksDone  bool
}

// mediaCache keeps decoded thumbnails, waveforms and images by file, shared by every tab.
type mediaCache struct {
	sources    map[string]*sourceMedia
	tiles      map[*image.RGBA]*gfx.Scaler
	musicPeaks map[string][]float32
	musicBusy  map[string]bool
	images     map[string]*image.RGBA
}

func newMediaCache() mediaCache {
	return mediaCache{sources: map[string]*sourceMedia{}, tiles: map[*image.RGBA]*gfx.Scaler{}, musicPeaks: map[string][]float32{},
		musicBusy: map[string]bool{}, images: map[string]*image.RGBA{}}
}

const peaksPerSecond = 50

// settled reports whether every source of p has its thumbnails and waveform.
func (m *mediaCache) settled(p *project.Project) bool {
	for _, s := range p.Sources {
		sm := m.sources[s.Path]
		if sm == nil || sm.thumbsLeft > 0 || !sm.peaksDone {
			return false
		}
	}
	return true
}

// start decodes the timeline thumbnails and the waveform of src in the background, once per file.
func (m *mediaCache) start(a *App, src project.Source) {
	if _, ok := m.sources[src.Path]; ok {
		return
	}
	count := int(math.Min(240, math.Max(24, src.Duration/1.5)))
	sm := &sourceMedia{thumbStep: src.Duration / float64(count), thumbH: 90, thumbs: make([]*image.RGBA, count), thumbsLeft: count, peaksDone: !src.HasAudio}
	sm.thumbW = max(2, int(math.Round(float64(sm.thumbH)*float64(src.Width)/float64(max(src.Height, 1))/2))*2)
	m.sources[src.Path] = sm
	order := thumbOrder(count)
	var next sync.Mutex
	pos := 0
	for worker := 0; worker < 4; worker++ {
		a.background(func(ctx context.Context) {
			for {
				next.Lock()
				if pos >= len(order) || ctx.Err() != nil {
					next.Unlock()
					return
				}
				i := order[pos]
				pos++
				next.Unlock()
				at := (float64(i) + 0.5) * sm.thumbStep
				img, err := media.Thumbnail(ctx, src.Path, at, sm.thumbW, sm.thumbH)
				if err != nil && ctx.Err() == nil {
					img, err = media.Frame(ctx, src.Path, at, sm.thumbW, sm.thumbH)
				}
				a.post(func() {
					if err == nil {
						sm.thumbs[i] = img
					}
					sm.thumbsLeft--
				})
			}
		})
	}
	if src.HasAudio {
		a.background(func(ctx context.Context) {
			peaks, err := media.Peaks(ctx, src.Path, peaksPerSecond)
			a.post(func() {
				sm.peaks, sm.peaksDone = normalized(peaks), true
				if err != nil && ctx.Err() == nil {
					a.toast(a.u.tf("The waveform could not be read: %s", a.describe(err)), true)
				}
			})
		})
	}
}

// normalized scales a waveform so its loudest point fills the lane; near-silent audio stays flat.
func normalized(peaks []float32) []float32 {
	top := float32(0)
	for _, p := range peaks {
		top = max(top, p)
	}
	if top < 0.01 {
		return peaks
	}
	out := make([]float32, len(peaks))
	for i, p := range peaks {
		out[i] = p / top
	}
	return out
}

// thumbOrder visits thumbnails coarse-to-fine so the whole strip fills in quickly.
func thumbOrder(n int) []int {
	seen := make([]bool, n)
	var order []int
	for step := 1 << 10; step >= 1; step /= 2 {
		for i := 0; i < n; i += step {
			if !seen[i] {
				seen[i] = true
				order = append(order, i)
			}
		}
	}
	return order
}

// musicPeaks returns the waveform of a music file, starting its decoding the first time it is asked for.
func (a *App) musicPeaks(path string) []float32 {
	m := &a.media
	if p, ok := m.musicPeaks[path]; ok {
		return p
	}
	if !m.musicBusy[path] {
		m.musicBusy[path] = true
		a.background(func(ctx context.Context) {
			peaks, err := media.Peaks(ctx, path, peaksPerSecond)
			a.post(func() {
				if err == nil {
					m.musicPeaks[path] = normalized(peaks)
				}
			})
		})
	}
	return nil
}

// importAudio probes sound files, adds them to the local library and, when place is set, to the timeline at the
// playhead.
func (a *App) importAudio(paths []string, place bool) {
	if !a.tools.ready {
		a.toast(a.describe(errToolsNotReady), true)
		return
	}
	d := a.doc
	a.background(func(ctx context.Context) {
		for _, path := range paths {
			info, err := media.Probe(ctx, path)
			if err != nil || !info.HasAudio {
				a.post(func() { a.toast(a.u.tf("Cannot read the sound file %s", filepath.Base(path)), true) })
				continue
			}
			track, err := a.lib.AddLocal(path, info.Duration)
			a.post(func() {
				if err != nil {
					a.toast(a.u.tf("Import failed: %s", a.describe(err)), true)
					return
				}
				a.music.refreshLocal(a)
				if place && d == a.doc && a.p != nil {
					a.placeTrack(track, path)
				}
			})
		}
	})
}

// placeTrack puts a downloaded or local sound on the timeline at the playhead and selects it.
func (a *App) placeTrack(t library.Track, path string) {
	if a.p == nil {
		return
	}
	total := t.Duration
	if total <= 0 {
		total = a.p.Duration()
	}
	a.change(func() {
		a.sel = selection{kind: selMusic, id: a.p.AddMusic(path, t.Title, t.Credit, total, a.playhead)}
	})
	a.showPanel(tabMusic)
	if t.Duration <= 0 {
		d := a.doc
		a.background(func(ctx context.Context) {
			if info, err := media.Probe(ctx, path); err == nil {
				a.post(func() { a.fixMusicLength(d, path, info.Duration) })
			}
		})
	}
}

// fixMusicLength corrects clips placed before their file's real duration was known.
func (a *App) fixMusicLength(d *doc, path string, total float64) {
	if d.p == nil {
		return
	}
	for k := range d.p.Music {
		for i := range d.p.Music[k].Clips {
			m := &d.p.Music[k].Clips[i]
			if m.Path == path && m.Total != total {
				m.Total = total
				m.Length = math.Min(m.Length, total-m.Offset)
			}
		}
	}
	d.dirty = true
}
