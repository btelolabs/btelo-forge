package ui

import (
	"context"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/assets"
	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// Small pictures of files, for the asset manager and anything else that lists files. A picture is asked for
// while it is drawn; those asked for in a frame and not yet known are made in the background after it, at most
// thumbWorkers at a time and in the order they were asked for (top of the view first), so scrolling fast through a
// large folder makes only what is shown. Made pictures are kept on disk by assets.Thumbs, and in memory up to
// thumbBudget bytes, the least recently shown going first.

const (
	thumbWorkers = 4
	thumbBudget  = 192 << 20
)

// thumbCache is the pictures known and the ones asked for this frame.
type thumbCache struct {
	store    *assets.Thumbs
	items    map[string]*thumbItem // by path
	wanted   []*thumbItem          // asked for this frame and not yet made, in the order asked
	inflight int
	made     int // pictures made so far: what is laid out from their sizes is laid out again
	frame    uint64
	bytes    int
}

// thumbItem is one file's picture and what was found out about it.
type thumbItem struct {
	entry   assets.Entry
	img     *image.RGBA
	info    assets.Info
	ready   bool // made (img may still be nil: a sound, a text, a file that could not be read)
	loading bool
	used    uint64 // the last frame it was asked for
	scaler  gfx.Scaler
}

func (c *thumbCache) init(cacheDir string) {
	if c.items == nil {
		c.store = assets.NewThumbs(filepath.Join(cacheDir, "thumbs"))
		c.items = map[string]*thumbItem{}
	}
}

// thumbOf is e's picture: ok once it is made, and until then asked for.
func (a *App) thumbOf(e assets.Entry) (*image.RGBA, assets.Info, bool) {
	c := &a.fileThumbs
	c.init(a.cacheDir)
	it := c.items[e.Path]
	if it != nil && (it.entry.Size != e.Size || !it.entry.Mod.Equal(e.Mod)) && !it.loading {
		c.drop(it) // the file changed: make it again
		it = nil
	}
	if it == nil {
		it = &thumbItem{entry: e}
		c.items[e.Path] = it
	}
	if it.used != c.frame {
		it.used = c.frame
		if !it.ready && !it.loading {
			c.wanted = append(c.wanted, it)
		}
	}
	return it.img, it.info, it.ready
}

// fileThumb is the picture of the file at path, as thumbOf, for callers that have only the path. It looks the file
// up once; refreshThumbs makes it look again.
func (a *App) fileThumb(path string) (*image.RGBA, assets.Info, bool) {
	a.fileThumbs.init(a.cacheDir)
	if it := a.fileThumbs.items[path]; it != nil {
		return a.thumbOf(it.entry)
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, assets.Info{Problem: err.Error()}, true
	}
	return a.thumbOf(assets.Entry{Path: path, Name: filepath.Base(path), Kind: assets.KindOf(path), Size: st.Size(), Mod: st.ModTime()})
}

// refreshThumbs forgets the pictures not being made, so files changed since are read again when next shown; the
// disk cache keeps those that did not change.
func (a *App) refreshThumbs() {
	for _, it := range a.fileThumbs.items {
		if !it.loading {
			a.fileThumbs.drop(it)
		}
	}
}

func (c *thumbCache) drop(it *thumbItem) {
	if it.img != nil {
		c.bytes -= len(it.img.Pix)
	}
	delete(c.items, it.entry.Path)
}

// pumpThumbs starts making the pictures asked for this frame, and lets go of old ones over the budget; it runs once
// a frame, after everything is drawn.
func (a *App) pumpThumbs() {
	c := &a.fileThumbs
	if c.items == nil {
		return
	}
	if c.store.Maps == nil {
		c.store.Maps = a.mindFileDrawer() // mind maps are drawn with the window's fonts
	}
	for _, it := range c.wanted {
		if c.inflight >= thumbWorkers {
			break
		}
		if it.ready || it.loading || c.items[it.entry.Path] != it {
			continue
		}
		it.loading = true
		c.inflight++
		e := it.entry
		store := c.store
		a.background(func(ctx context.Context) {
			img, info, err := store.Get(ctx, e)
			if err != nil && ctx.Err() == nil && info.Problem == "" {
				info.Problem = err.Error() // the cache could not be written; the picture is still good
			}
			a.post(func() {
				c.inflight--
				it.loading = false
				if c.items[e.Path] != it {
					return // dropped while it was made
				}
				it.img, it.info, it.ready = img, info, true
				c.made++
				if img != nil {
					c.bytes += len(img.Pix)
				}
				c.evict()
			})
		})
	}
	c.wanted = c.wanted[:0]
	c.frame++
}

// evict lets go of the pictures shown longest ago while the memory used is over the budget; what was shown in the
// last frames stays.
func (c *thumbCache) evict() {
	for c.bytes > thumbBudget {
		var oldest *thumbItem
		for _, it := range c.items {
			if it.img != nil && it.used+2 < c.frame && (oldest == nil || it.used < oldest.used) {
				oldest = it
			}
		}
		if oldest == nil {
			return
		}
		c.drop(oldest)
	}
}

// thumbsBusy reports whether pictures are being made, so the window keeps drawing until they arrive.
func (a *App) thumbsBusy() bool { return a.fileThumbs.inflight > 0 }

// drawFileThumb draws the picture of the file at path fitted in r, or a card for its kind while it has none.
func (a *App) drawFileThumb(id string, r gfx.Rect, path string) {
	img, info, ready := a.fileThumb(path)
	a.drawThumbIn(r, a.fileThumbs.items[path], img, info, ready, assets.KindOf(path))
}

// drawThumbIn draws a picture fitted inside r, scaled once and kept, or the card of its kind.
func (a *App) drawThumbIn(r gfx.Rect, it *thumbItem, img *image.RGBA, info assets.Info, ready bool, kind assets.Kind) {
	u := &a.u
	if img == nil || it == nil {
		a.drawKindCard(r, kind, info, ready)
		return
	}
	fit := fitRect(r, float32(img.Rect.Dx()), float32(img.Rect.Dy())).Pixels()
	scaled := it.scaler.Scaled(img, fit.Dx(), fit.Dy())
	if scaled == nil {
		return
	}
	if !img.Opaque() {
		drawChecks(u, gfx.Rect{X: float32(fit.Min.X), Y: float32(fit.Min.Y), W: float32(fit.Dx()), H: float32(fit.Dy())})
	}
	u.c.Blit(scaled, fit.Min.X, fit.Min.Y, img.Opaque())
}

// drawChecks draws the grey checks that show where a picture is transparent.
func drawChecks(u *ctx, r gfx.Rect) {
	u.c.Push(r)
	defer u.c.Pop()
	u.c.Fill(r, gfx.Hex(0x2a2b30))
	step := u.px(8)
	for y, row := r.Y, 0; y < r.Bottom(); y, row = y+step, row+1 {
		for x, col := r.X, 0; x < r.Right(); x, col = x+step, col+1 {
			if (row+col)%2 == 0 {
				u.c.Fill(gfx.Rect{X: x, Y: y, W: step, H: step}, gfx.Hex(0x35363c))
			}
		}
	}
}

// kindLook is the symbol and colour a kind of file is drawn with when it has no picture.
func kindLook(k assets.Kind) (string, color.RGBA) {
	switch k {
	case assets.Folder:
		return icFolder, gfx.Hex(0xe8b04b)
	case assets.Image, assets.Animation:
		return icImage, gfx.Hex(0x5aa9ff)
	case assets.Video, assets.Edit:
		return icVideo, gfx.Hex(0x7f8cff)
	case assets.Audio:
		return icMusic, gfx.Hex(0x3fc28a)
	case assets.MindMap:
		return icMind, gfx.Hex(0x4fb3ff)
	case assets.Office:
		return icOfficeWord, gfx.Hex(0x4d8bf0)
	case assets.Model:
		return icModel, modelHue
	case assets.Canvas:
		return icBrush, gfx.Hex(0xf2994a)
	case assets.Markdown:
		return icDescription, gfx.Hex(0xb48cff)
	case assets.Code:
		return icCode, gfx.Hex(0x4fd1c5)
	case assets.Text:
		return icDescription, gfx.Hex(0xc9ccd4)
	}
	return icFile, gfx.Hex(0x8b8e98)
}

// drawKindCard draws a file without a picture: a sound's waveform, the start of a text, or its kind's symbol.
func (a *App) drawKindCard(r gfx.Rect, kind assets.Kind, info assets.Info, ready bool) {
	u := &a.u
	glyph, hue := kindLook(kind)
	u.c.Fill(r, gfx.Mix(colSurface, hue, 0.07))
	switch {
	case kind == assets.Audio && len(info.Peaks) > 0:
		mid := r.Y + r.H/2
		bw := r.W / float32(len(info.Peaks))
		for i, p := range info.Peaks {
			h := max(u.px(1), p*r.H*0.42)
			u.c.Fill(gfx.Rect{X: r.X + float32(i)*bw + bw*0.2, Y: mid - h, W: max(bw*0.6, 1), H: 2 * h}, gfx.Alpha(hue, 0.85))
		}
		return
	case kind.Readable() && info.Snippet != "":
		snippet := info.Snippet
		if kind == assets.Markdown {
			lines := strings.Split(snippet, "\n")
			for i, l := range lines {
				lines[i] = inlinePlain(strings.TrimLeft(l, "#> "))
			}
			snippet = strings.Join(lines, "\n")
		}
		f := u.mono(max(7, min(11, r.W/u.px(40)*11)))
		if kind == assets.Markdown || kind == assets.Text {
			f = u.face(max(7, min(11.5, r.W/u.px(40)*11.5)))
		}
		pad := max(u.px(6), r.W*0.06)
		u.c.Push(r)
		y := r.Y + pad
		for _, line := range strings.Split(snippet, "\n") {
			if y > r.Bottom() {
				break
			}
			line = strings.ReplaceAll(line, "\t", "    ")
			u.c.Text(f, r.X+pad, y, f.Ellipsize(line, r.W-2*pad), colDim)
			y += f.Height() * 1.25
		}
		u.c.Pop()
		// The text fades out towards the bottom, as a page going on.
		fade := min(r.H*0.3, u.px(40))
		for i := float32(0); i < fade; i += u.px(2) {
			u.c.Fill(gfx.Rect{X: r.X, Y: r.Bottom() - fade + i, W: r.W, H: u.px(2)}, gfx.Alpha(gfx.Mix(colSurface, hue, 0.07), i/fade))
		}
		return
	}
	size := min(r.W, r.H) * 0.32
	col := gfx.Alpha(hue, 0.9)
	if !ready && kind.Visual() {
		col = gfx.Alpha(hue, 0.35) // its picture is on its way
	}
	u.icon(glyph, r, size/u.s, col)
}
