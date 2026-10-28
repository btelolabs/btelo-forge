package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"time"

	xdraw "golang.org/x/image/draw"

	"github.com/btelolabs/btelo-forge/internal/anim"
	"github.com/btelolabs/btelo-forge/internal/assets"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/paint"
	"github.com/btelolabs/btelo-forge/internal/playback"
	"github.com/btelolabs/btelo-forge/internal/project"
)

// A file previewed in full: a picture to zoom and move around, an animation or video playing, a sound with its
// waveform, a text, Markdown or code read with its formatting. One file is previewed at a time; showing another
// lets go of the first, and closeFilePreview lets go of everything.

// filePreview is the file being previewed.
type filePreview struct {
	id, path string
	kind     assets.Kind
	gen      int // counts the files previewed, so work for one that is no longer shown is dropped
	loading  bool
	problem  string

	// A picture, a canvas or the poster of a video.
	img    *image.RGBA
	zoom   float32 // 0 fits the view; otherwise screen pixels per picture pixel
	centre [2]float32
	scaler gfx.Scaler

	// An animation.
	anim   *anim.Animation
	frame  *image.RGBA
	due    time.Time
	paused bool

	// A video, a sound or an edit; it starts playing once it is shown, from where it was left last time.
	proj      *project.Project
	autoplay  bool
	length    float64
	at        float64     // the position while paused
	resume    bool        // playing when the seek bar was pressed
	playSize  image.Point // the size the video plays at, fitted to the view
	sound     bool        // played by the audition, without a picture
	soundFrom float64
	peaks     []float32
	still     *image.RGBA // the picture at at while paused
	stillAt   float64
	stillBusy bool

	// A text.
	text      string
	truncated bool
	page      textPage
}

// maxPreviewPixels is the largest picture kept for a preview, by its longer side; larger ones are scaled down.
const maxPreviewPixels = 8192

// maxPreviewText is how much of a text is read for its preview.
const maxPreviewText = 2 << 20

// closeFilePreview stops what the preview plays, keeping where it was, and lets go of what it holds.
func (a *App) closeFilePreview() {
	a.rememberPosition()
	p := &a.preview
	if a.previewPlayer != nil {
		a.previewPlayer.Stop()
	}
	if key, _ := a.audition.Playing(); key == previewSoundKey(p.path) {
		a.audition.Stop()
	}
	*p = filePreview{gen: p.gen + 1}
}

func previewSoundKey(path string) string { return "preview:" + path }

// previewPlayback is the preview's own video player, apart from the editor's.
func (a *App) previewPlayback() *playback.Player {
	if a.previewPlayer == nil {
		a.previewPlayer = playback.New(a.host.Wake)
	}
	return a.previewPlayer
}

// openPreview starts previewing path, reading it in the background.
func (a *App) openPreview(id, path string) {
	a.closeFilePreview()
	p := &a.preview
	p.id, p.path, p.kind, p.loading = id, path, assets.KindOf(path), true
	gen := p.gen
	kind := p.kind
	done := func(fn func(p *filePreview)) {
		a.post(func() {
			if a.preview.gen != gen {
				return
			}
			a.preview.loading = false
			fn(&a.preview)
		})
	}
	fail := func(err error) { done(func(p *filePreview) { p.problem = a.describe(err) }) }
	draw := a.mindFileDrawer()
	a.background(func(ctx context.Context) {
		switch {
		case kind == assets.Image || kind == assets.Animation:
			data, err := os.ReadFile(path)
			if err != nil {
				fail(err)
				return
			}
			if an, err := anim.Decode(data); err == nil {
				done(func(p *filePreview) { p.anim = an })
				return
			}
			img, err := decodePreview(ctx, path, data)
			if err != nil {
				fail(err)
				return
			}
			done(func(p *filePreview) { p.img = img })
		case kind == assets.MindMap:
			img, err := draw(path, 2048)
			if err != nil {
				fail(err)
				return
			}
			done(func(p *filePreview) { p.img = img })
		case kind == assets.Canvas:
			img, err := canvasPreview(path)
			if err != nil {
				fail(err)
				return
			}
			done(func(p *filePreview) { p.img = img })
		case kind == assets.Video || kind == assets.Audio:
			info, err := media.Probe(ctx, path)
			if err != nil {
				fail(err)
				return
			}
			var peaks []float32
			if !info.HasVideo {
				_, ti, _ := a.fileThumbs.store.Get(ctx, assets.Entry{Path: path, Kind: assets.Audio, Size: fileSize(path), Mod: fileMod(path)})
				peaks = ti.Peaks
			}
			done(func(p *filePreview) {
				p.length, p.sound, p.peaks = info.Duration, !info.HasVideo, peaks
				if info.HasVideo {
					p.proj = project.New(project.Source{Path: path, Duration: info.Duration, Width: info.Width, Height: info.Height,
						FPS: info.FPS, HasVideo: true, HasAudio: info.HasAudio}, project.Export{})
				}
				p.at, p.autoplay = a.resumeAt(path, info.Duration), true
			})
		case kind == assets.Edit:
			proj, err := project.ReadFile(path)
			if err == nil {
				err = checkEditVideos(ctx, proj)
			}
			if err != nil {
				fail(err)
				return
			}
			done(func(p *filePreview) {
				p.proj, p.length = proj, proj.Duration()
				p.at, p.autoplay = a.resumeAt(path, p.length), true
			})
		case kind.Readable():
			text, truncated, err := assets.ReadText(path, maxPreviewText)
			if err != nil {
				fail(err)
				return
			}
			done(func(p *filePreview) { p.text, p.truncated = text, truncated })
		default:
			done(func(p *filePreview) {})
		}
	})
}

func fileSize(path string) int64 {
	if st, err := os.Stat(path); err == nil {
		return st.Size()
	}
	return 0
}

func fileMod(path string) time.Time {
	if st, err := os.Stat(path); err == nil {
		return st.ModTime()
	}
	return time.Time{}
}

// decodePreview reads a picture at full size, up to maxPreviewPixels a side, through ffmpeg when Go cannot.
func decodePreview(ctx context.Context, path string, data []byte) (*image.RGBA, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		info, perr := media.Probe(ctx, path)
		if perr != nil || !info.HasVideo || info.Width <= 0 {
			return nil, err
		}
		w, h := previewSize(info.Width, info.Height)
		return media.Frame(ctx, path, 0, w, h)
	}
	b := img.Bounds()
	w, h := previewSize(b.Dx(), b.Dy())
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	if w == b.Dx() && h == b.Dy() {
		xdraw.Draw(out, out.Rect, img, b.Min, xdraw.Src)
	} else {
		xdraw.CatmullRom.Scale(out, out.Rect, img, b, xdraw.Src, nil)
	}
	return out, nil
}

func previewSize(w, h int) (int, int) {
	s := math.Min(1, float64(maxPreviewPixels)/float64(max(w, h)))
	return max(1, int(float64(w)*s)), max(1, int(float64(h)*s))
}

// canvasPreview composes a canvas at the detail level nearest 2048 pixels a side.
func canvasPreview(path string) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	d, err := paint.Load(f, st.Size())
	if err != nil {
		return nil, err
	}
	ext := d.Extent()
	if ext.Empty() {
		return nil, errEmptyCanvas
	}
	l := 0
	for max(ext.Dx(), ext.Dy())>>l > 2048 {
		l++
	}
	r := image.Rect(ext.Min.X>>l, ext.Min.Y>>l, (ext.Max.X+(1<<l)-1)>>l, (ext.Max.Y+(1<<l)-1)>>l)
	dst := image.NewRGBA(r)
	d.Render(dst, r, l)
	dst.Rect = dst.Rect.Sub(r.Min)
	return dst, nil
}

var errEmptyCanvas = errors.New("nothing is drawn on this canvas yet")

// drawFilePreview previews the file at path in r. id names the place it is shown in, for its controls.
func (a *App) drawFilePreview(id string, r gfx.Rect, path string) {
	u := &a.u
	p := &a.preview
	if p.path != path || p.id != id {
		a.openPreview(id, path)
	}
	u.c.Push(r)
	defer u.c.Pop()
	switch {
	case p.problem != "":
		a.drawKindCard(r, p.kind, assets.Info{}, true)
		u.label(u.face(13), gfx.Rect{X: r.X, Y: r.Y + r.H*0.72, W: r.W, H: u.px(24)}, p.problem, colDangerText, 0.5)
	case p.loading:
		u.label(u.face(13), r, u.t("Loading the preview…"), colFaint, 0.5)
	case p.anim != nil:
		a.drawAnimPreview(id, r)
	case p.img != nil:
		a.drawPicturePreview(id, r, p.img, p.img.Opaque())
	case p.proj != nil || p.sound:
		a.drawMediaPreview(id, r)
	case p.kind.Readable():
		a.drawTextPreview(id, r)
	default:
		a.drawKindCard(r, p.kind, assets.Info{}, true)
		u.label(u.face(13), gfx.Rect{X: r.X, Y: r.Y + r.H*0.72, W: r.W, H: u.px(24)}, u.t("No preview for this kind of file"), colDim, 0.5)
	}
}

// previewWait is how soon the preview needs drawing again on its own: the next frame of an animation, the clock of
// what plays.
func (a *App) previewWait() time.Duration {
	p := &a.preview
	switch {
	case p.anim != nil && !p.paused:
		return max(time.Until(p.due), time.Millisecond)
	case a.previewPlayer != nil && a.previewPlayer.Playing():
		return time.Second / 60
	case p.sound:
		if key, _ := a.audition.Playing(); key == previewSoundKey(p.path) {
			return time.Second / 20
		}
	case p.loading || p.stillBusy:
		return 0 // the work posts its result, which wakes the window
	}
	return 0
}

// drawPicturePreview shows a picture fitted in r, or zoomed: the wheel zooms about the pointer, dragging moves it,
// and a double click goes between fitted and actual size.
func (a *App) drawPicturePreview(id string, r gfx.Rect, img *image.RGBA, opaque bool) {
	u := &a.u
	p := &a.preview
	iw, ih := float32(img.Rect.Dx()), float32(img.Rect.Dy())
	fitScale := min(r.W/iw, r.H/ih)
	if fitScale > 1 {
		fitScale = 1 // a small picture is shown at its size, not blown up
	}
	scale := p.zoom
	if scale == 0 {
		scale = fitScale
		p.centre = [2]float32{iw / 2, ih / 2}
	}
	hot := u.hover(id+"/picture", r)
	if hot && u.wheel != 0 {
		step := float32(math.Pow(1.2, float64(u.wheel)))
		next := min(max(scale*step, fitScale*0.5, 0.02), 32)
		// Keep the picture point under the pointer where it is.
		px := p.centre[0] + (u.mx-(r.X+r.W/2))/scale
		py := p.centre[1] + (u.my-(r.Y+r.H/2))/scale
		p.centre[0] = px - (u.mx-(r.X+r.W/2))/next
		p.centre[1] = py - (u.my-(r.Y+r.H/2))/next
		p.zoom = next
		if math.Abs(float64(next-fitScale)) < 0.001 {
			p.zoom = 0
		}
		scale = next
		u.wheel = 0
	}
	key := id + "/picture"
	if u.press(key, r) {
		if u.double {
			if p.zoom == 0 {
				p.zoom = 1
				p.centre = [2]float32{p.centre[0] + (u.mx-(r.X+r.W/2))/scale, p.centre[1] + (u.my-(r.Y+r.H/2))/scale}
			} else {
				p.zoom = 0
			}
		}
		u.drag.x, u.drag.y = u.mx, u.my
		u.drag.values[0], u.drag.values[1] = float64(p.centre[0]), float64(p.centre[1])
	}
	if u.dragging(key) && p.zoom != 0 {
		u.cursor = CursorMove
		p.centre[0] = float32(u.drag.values[0]) - (u.mx-u.drag.x)/scale
		p.centre[1] = float32(u.drag.values[1]) - (u.my-u.drag.y)/scale
	}
	u.letGo(key)
	dst := gfx.Rect{W: iw * scale, H: ih * scale}
	dst.X = r.X + r.W/2 - p.centre[0]*scale
	dst.Y = r.Y + r.H/2 - p.centre[1]*scale
	if p.zoom == 0 {
		dst = fitRect(r, iw, ih)
		if fitScale >= 1 {
			dst = gfx.Rect{X: r.X + (r.W-iw)/2, Y: r.Y + (r.H-ih)/2, W: iw, H: ih}
		}
	}
	if !opaque {
		drawChecks(u, intersect(dst, r)) // only where it shows, however far it is zoomed
	}
	if p.zoom == 0 {
		px := dst.Pixels()
		if scaled := p.scaler.Scaled(img, px.Dx(), px.Dy()); scaled != nil {
			u.c.Blit(scaled, px.Min.X, px.Min.Y, opaque)
		}
	} else {
		u.c.Image(img, dst)
	}
	if hot {
		u.hint(key, u.tf("%d×%d · %d%% · wheel zooms, double-click for actual size", img.Rect.Dx(), img.Rect.Dy(), int(scale*100+0.5)), r)
	}
}

// drawAnimPreview plays an animation, a click pausing it.
func (a *App) drawAnimPreview(id string, r gfx.Rect) {
	u := &a.u
	p := &a.preview
	if p.frame == nil || !p.paused && !time.Now().Before(p.due) {
		img, d, err := p.anim.Next()
		if err != nil {
			p.problem = a.describe(err)
			return
		}
		if p.frame == nil || p.frame.Rect != img.Rect {
			p.frame = image.NewRGBA(img.Rect)
		}
		// The canvas is straight alpha; the screen wants it premultiplied.
		for i := 0; i < len(img.Pix); i += 4 {
			al := uint32(img.Pix[i+3])
			p.frame.Pix[i] = uint8(uint32(img.Pix[i]) * al / 255)
			p.frame.Pix[i+1] = uint8(uint32(img.Pix[i+1]) * al / 255)
			p.frame.Pix[i+2] = uint8(uint32(img.Pix[i+2]) * al / 255)
			p.frame.Pix[i+3] = img.Pix[i+3]
		}
		p.scaler = gfx.Scaler{} // a new frame in the same image
		p.due = time.Now().Add(d)
	}
	a.drawPicturePreview(id, r, p.frame, false)
	bar := gfx.Rect{X: r.X + u.px(10), Y: r.Bottom() - u.px(30), W: u.px(160), H: u.px(22)}
	u.c.Fill(bar, gfx.Alpha(colBlack, 0.55))
	u.label(u.face(11.5), bar, u.tf("%d frames · %s", p.anim.Len(), mediaLength(p.anim.Duration().Seconds())), colDim, 0.5)
}

// previewPlaying reports whether the previewed video or sound is playing.
func (a *App) previewPlaying() bool {
	p := &a.preview
	if p.sound {
		key, _ := a.audition.Playing()
		return key == previewSoundKey(p.path)
	}
	return a.previewPlayer != nil && a.previewPlayer.Playing()
}

// previewPosition is where the previewed video or sound is.
func (a *App) previewPosition() float64 {
	p := &a.preview
	if p.sound {
		if key, heard := a.audition.Playing(); key == previewSoundKey(p.path) {
			return min(p.soundFrom+heard, p.length)
		}
		return p.at
	}
	if a.previewPlayer != nil {
		if t, ok := a.previewPlayer.Position(); ok {
			return t
		}
	}
	return p.at
}

// togglePreviewPlay plays or pauses the previewed animation, video or sound.
func (a *App) togglePreviewPlay() {
	p := &a.preview
	switch {
	case p.anim != nil:
		p.paused = !p.paused
		p.due = time.Now()
	case p.sound:
		if a.previewPlaying() {
			p.at = a.previewPosition()
			a.audition.Stop()
			return
		}
		if p.at >= p.length-0.05 {
			p.at = 0
		}
		p.soundFrom = p.at
		a.audition.PlayFrom(previewSoundKey(p.path), p.path, "", p.at)
	case p.proj != nil:
		pl := a.previewPlayback()
		if pl.Playing() {
			p.at = a.previewPosition()
			pl.Stop()
			return
		}
		if p.at >= p.length-0.05 {
			p.at = 0
		}
		w, h := p.playSize.X, p.playSize.Y
		pl.Start(p.proj, p.at, max(w, 2), max(h, 2))
	}
}

// seekPreview moves the previewed video or sound to t, playing on from there if it was playing.
func (a *App) seekPreview(t float64) {
	p := &a.preview
	playing := a.previewPlaying()
	if playing {
		a.togglePreviewPlay()
	}
	p.at = min(max(t, 0), p.length)
	if playing {
		a.togglePreviewPlay()
	}
}

// previewKey handles a key pressed while the preview has the keyboard: Space plays and pauses, the arrows seek
// five seconds, Home goes to the start, and +, - and 0 zoom a picture. It reports whether it used the key.
func (a *App) previewKey(k Key) bool {
	p := &a.preview
	if p.path == "" || k.Ctrl || k.Alt {
		return false
	}
	media := p.proj != nil || p.sound
	switch k.Code {
	case keySpace:
		if media || p.anim != nil {
			a.togglePreviewPlay()
			return true
		}
	case keyLeft, keyRight:
		if media {
			step := 5.0
			if k.Code == keyLeft {
				step = -step
			}
			a.seekPreview(a.previewPosition() + step)
			return true
		}
	case keyHome:
		if media {
			a.seekPreview(0)
			return true
		}
	case 0xbb, 0x6b: // = and the keypad's +
		if p.img != nil || p.anim != nil {
			p.zoom = max(p.zoom, 0.05) * 1.25
			return true
		}
	case 0xbd, 0x6d: // - and the keypad's -
		if p.img != nil || p.anim != nil {
			p.zoom = max(p.zoom, 0.05) / 1.25
			return true
		}
	case '0', 0x60:
		if p.img != nil || p.anim != nil {
			p.zoom = 0
			return true
		}
	}
	return false
}

// drawMediaPreview shows a video or an edit playing, or a sound's waveform, with a bar to play and seek.
func (a *App) drawMediaPreview(id string, r gfx.Rect) {
	u := &a.u
	p := &a.preview
	barH := u.px(48)
	view := gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H - barH}
	bar := gfx.Rect{X: r.X, Y: view.Bottom(), W: r.W, H: barH}
	playing := a.previewPlaying()
	if !playing && a.previewPlayer != nil {
		if err := a.previewPlayer.Err(); err != nil {
			p.problem = a.describe(err)
		}
	}
	if p.sound {
		mid := view.Y + view.H/2
		if len(p.peaks) > 0 {
			bw := view.W / float32(len(p.peaks))
			at := float32(a.previewPosition() / max(p.length, 0.001))
			for i, v := range p.peaks {
				h := max(u.px(1), v*view.H*0.4)
				col := gfx.Alpha(gfx.Hex(0x3fc28a), 0.45)
				if float32(i)/float32(len(p.peaks)) < at {
					col = gfx.Hex(0x3fc28a)
				}
				u.c.Fill(gfx.Rect{X: view.X + float32(i)*bw + bw*0.2, Y: mid - h, W: max(bw*0.6, 1), H: 2 * h}, col)
			}
		} else {
			u.icon(icMusic, view, 48, gfx.Hex(0x3fc28a))
		}
	} else {
		fit := fitRect(view, float32(p.proj.Canvas.Width), float32(p.proj.Canvas.Height))
		p.playSize = evenSize(fit)
		u.c.Fill(fit, colBlack)
		var img *image.RGBA
		if playing {
			if f, ok := a.previewPlayer.Frame(); ok {
				img = f.Image
				p.still = nil
			}
		} else {
			a.fetchPreviewStill(fit)
			img = p.still
		}
		if img == nil && playing {
			img = p.still // the player's first picture is still being decoded: the paused one stays until it comes
		}
		if img == nil && (p.kind == assets.Edit || playing) {
			if t, _, _ := a.fileThumb(p.path); t != nil {
				img = t
			}
		}
		if img != nil {
			u.c.Image(img, fitRect(fit, float32(img.Rect.Dx()), float32(img.Rect.Dy())))
		}
		if u.press(id+"/video", view) {
			a.togglePreviewPlay()
		}
		u.letGo(id + "/video")
	}
	if p.autoplay {
		// Shown now, and for a video at the size it plays at: it starts as a player opening a file does.
		p.autoplay = false
		if !playing && p.problem == "" {
			a.togglePreviewPlay()
			playing = a.previewPlaying()
		}
	}

	u.c.Fill(bar, gfx.Alpha(colBlack, 0.35))
	glyph := icPlay
	if playing {
		glyph = icPause
	}
	pr := gfx.Rect{X: bar.X + u.px(8), Y: bar.Y + (bar.H-u.px(32))/2, W: u.px(36), H: u.px(32)}
	if u.iconButton(id+"/play", pr, glyph, u.t("Play or pause (Space)"), false, true) {
		a.togglePreviewPlay()
	}
	at := a.previewPosition()
	f := u.face(12)
	times := clock(at) + " / " + clock(p.length)
	tw := f.Measure(times) + u.px(12)
	u.label(f, gfx.Rect{X: bar.Right() - tw - u.px(8), Y: bar.Y, W: tw, H: bar.H}, times, colDim, 1)
	seek := gfx.Rect{X: pr.Right() + u.px(10), Y: bar.Y + (bar.H-u.px(16))/2, W: bar.Right() - tw - u.px(18) - pr.Right() - u.px(10), H: u.px(16)}
	if to, state := a.seekBar(id+"/seek", seek, at, p.length); state != seekIdle {
		switch state {
		case seekPressed:
			p.resume = playing
			if playing {
				a.togglePreviewPlay()
			}
			p.at = to
		case seekDragged:
			p.at = to
		case seekReleased:
			p.at = to
			if p.resume {
				p.resume = false
				a.togglePreviewPlay()
			}
		}
	}
}

// fetchPreviewStill decodes the paused video's picture at its position, one at a time, the newest wanted winning.
func (a *App) fetchPreviewStill(fit gfx.Rect) {
	p := &a.preview
	if p.kind == assets.Edit || p.stillBusy || p.still != nil && p.stillAt == p.at {
		return
	}
	size := evenSize(fit)
	path, at, gen := p.path, min(p.at, max(p.length-0.05, 0)), p.gen
	if at == 0 {
		at = math.Min(p.length*0.1, 10) // the same picture the grid shows
	}
	want := p.at
	p.stillBusy = true
	a.background(func(ctx context.Context) {
		img, err := media.Frame(ctx, path, at, size.X, size.Y)
		a.post(func() {
			if a.preview.gen != gen {
				return
			}
			a.preview.stillBusy = false
			if err == nil {
				a.preview.still, a.preview.stillAt = img, want
			} else {
				a.preview.stillAt = want // nothing there: do not try again for this position
			}
		})
	})
}

// seekState is what a seek bar did this frame.
type seekState int

const (
	seekIdle seekState = iota
	seekPressed
	seekDragged
	seekReleased
)

// seekBar draws a bar for a position at of length; pressing or dragging it says where, with the time under the
// pointer shown above it.
func (a *App) seekBar(id string, r gfx.Rect, at, length float64) (float64, seekState) {
	u := &a.u
	hot := u.hover(id, r) || u.active == id
	thick := u.px(4)
	if hot {
		thick = u.px(6)
		u.cursor = CursorHand
	}
	track := gfx.Rect{X: r.X, Y: r.Y + (r.H-thick)/2, W: r.W, H: thick}
	u.c.Fill(track, colSurfaceHi)
	done := float32(0)
	if length > 0 {
		done = float32(min(max(at/length, 0), 1))
	}
	u.c.Fill(gfx.Rect{X: track.X, Y: track.Y, W: track.W * done, H: track.H}, colAccent)
	pos := func(x float32) float64 { return float64(min(max((x-r.X)/max(r.W, 1), 0), 1)) * length }
	if hot {
		knob := u.px(12)
		x := track.X + track.W*done
		u.c.Fill(gfx.Rect{X: x - knob/2, Y: track.Y + track.H/2 - knob/2, W: knob, H: knob}, colPlayhead)
		if u.active == "" {
			f := u.face(11.5)
			text := clock(pos(u.mx))
			w := f.Measure(text) + u.px(12)
			tip := gfx.Rect{X: min(max(u.mx-w/2, r.X-u.px(10)), r.Right()-w+u.px(10)), Y: r.Y - u.px(26), W: w, H: u.px(22)}
			u.c.Fill(tip, gfx.Alpha(colBlack, 0.75))
			u.label(f, tip, text, colText, 0.5)
		}
	}
	switch {
	case u.press(id, r):
		return pos(u.mx), seekPressed
	case u.dragging(id):
		return pos(u.mx), seekDragged
	case u.letGo(id):
		return pos(u.mx), seekReleased
	}
	return at, seekIdle
}

// previewTitle is a line naming what is previewed, for the quick look's header.
func previewTitle(path string, info assets.Info, kind assets.Kind, size int64) string {
	parts := []string{filepath.Base(path)}
	if info.W > 0 {
		parts = append(parts, fmt.Sprintf("%d×%d", info.W, info.H))
	}
	if info.Duration > 0 && (kind != assets.Image || info.Animated) {
		parts = append(parts, mediaLength(info.Duration))
	}
	if size > 0 {
		parts = append(parts, assets.FormatSize(size))
	}
	out := parts[0]
	for _, s := range parts[1:] {
		out += "  ·  " + s
	}
	return out
}

// intersect is the part of a inside b.
func intersect(a, b gfx.Rect) gfx.Rect {
	x0, y0 := max(a.X, b.X), max(a.Y, b.Y)
	x1, y1 := min(a.Right(), b.Right()), min(a.Bottom(), b.Bottom())
	return gfx.Rect{X: x0, Y: y0, W: max(x1-x0, 0), H: max(y1-y0, 0)}
}
