package ui

import (
	"context"
	"errors"
	"image"
	"image/draw"
	"math"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"

	"github.com/btelolabs/btelo-forge/internal/anim"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/mind"
	"github.com/btelolabs/btelo-forge/internal/paint"
)

// The pictures the boards show: pictures (animated or not, and canvases), videos' frames, smart objects' pictures
// and nested maps, shared by every map tab. They are read in the background at the size they are shown, in steps
// (a small one first, a larger one as the view zooms in), and kept within a budget, the least recently shown going
// first. The tiles ask from several goroutines at once and never wait: what is not ready is drawn as loading, and
// the nodes showing it are drawn again when it is.

// picLevels are the widths pictures are read at, a step for each.
var picLevels = []int{192, 640, 2048, 4096}

// picBudget is how many bytes of pictures are kept.
const picBudget = 320 << 20

// picWorkers is how many pictures are read at once.
const picWorkers = 4

type picKey struct {
	ref   string // as compared: lower case
	kind  mind.Kind
	level int
	chain string // a nested map is drawn inside this map, its chain's outermost
}

type picItem struct {
	ref      string // as given
	img      *image.RGBA
	state    mind.PictureState
	animated bool
	aspect   float64
	files    []string // a nested map's files: it is drawn again when one of them changes
	loading  bool
	redo     bool // its file changed while it was read: what is read is stale, and it is read again
	used     uint64
}

type mindPics struct {
	mu    sync.Mutex
	items map[picKey]*picItem
	want  []picKey
	busy  int
	bytes int
	clock uint64
	anims map[string]*picAnim // by file, on the UI goroutine only
}

// mindPictures is the board's picture cache, made on first use.
func (a *App) mindPictures() *mindPics {
	if a.mindPics == nil {
		a.mindPics = &mindPics{items: map[picKey]*picItem{}, anims: map[string]*picAnim{}}
	}
	return a.mindPics
}

func picLevel(px int) int {
	for i, w := range picLevels {
		if px <= w {
			return i
		}
	}
	return len(picLevels) - 1
}

// boardPictures gives one map's nodes their pictures: a nested map is drawn inside the map at chain.
type boardPictures struct {
	p     *mindPics
	chain string
}

func (b boardPictures) Picture(n *mind.Node, px int) (*image.RGBA, mind.PictureState, bool) {
	if n.Ref == "" {
		return nil, mind.PicMissing, false
	}
	return b.p.get(n, px, b.chain)
}

func (p *mindPics) key(n *mind.Node, level int, chain string) picKey {
	k := picKey{ref: strings.ToLower(n.Ref), kind: n.Kind, level: level}
	if n.Kind == mind.Nested {
		k.chain = mind.NestKey(chain)
	}
	return k
}

// get is the picture of n at about px wide: the nearest step ready while the right one is read.
func (p *mindPics) get(n *mind.Node, px int, chain string) (*image.RGBA, mind.PictureState, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clock++
	level := picLevel(px)
	k := p.key(n, level, chain)
	it := p.items[k]
	if it == nil {
		it = &picItem{ref: n.Ref, loading: true}
		p.items[k] = it
		p.want = append(p.want, k)
	}
	it.used = p.clock
	if !it.loading {
		return it.img, it.state, it.animated
	}
	// Another step meanwhile, the largest ready; a file found missing is missing at every size.
	for l := len(picLevels) - 1; l >= 0; l-- {
		if o := p.items[p.key(n, l, chain)]; o != nil && !o.loading {
			o.used = p.clock
			return o.img, o.state, o.animated
		}
	}
	return nil, mind.PicLoading, false
}

// pump starts reading the pictures asked for, a few at once; it runs on the UI goroutine.
func (a *App) pumpMindPictures() {
	p := a.mindPics
	if p == nil {
		return
	}
	p.mu.Lock()
	var start []picKey
	for p.busy < picWorkers && len(p.want) > 0 {
		k := p.want[len(p.want)-1] // the latest asked first: what is in view now
		p.want = p.want[:len(p.want)-1]
		if it := p.items[k]; it != nil && it.loading {
			start = append(start, k)
			p.busy++
		}
	}
	refs := map[picKey]string{}
	for _, k := range start {
		refs[k] = p.items[k].ref
	}
	p.mu.Unlock()
	labels := a.mindLabels()
	fonts := a.u.fonts
	src := mindSource{a}
	for _, k := range start {
		ref := refs[k]
		a.background(func(ctx context.Context) {
			it := loadMindPicture(ctx, a, k, ref, fonts, labels, src)
			a.post(func() { a.pictureLoaded(k, it) })
		})
	}
}

// pictureLoaded keeps a picture read and draws again the nodes that show it.
func (a *App) pictureLoaded(k picKey, it *picItem) {
	p := a.mindPics
	p.mu.Lock()
	p.busy--
	old := p.items[k]
	if old == nil || !old.loading {
		p.mu.Unlock()
		return // dropped meanwhile: read again when asked
	}
	if old.redo {
		old.redo = false
		p.want = append(p.want, k)
		p.mu.Unlock()
		a.requestFrame()
		return
	}
	it.used = old.used
	p.items[k] = it
	if it.img != nil {
		p.bytes += len(it.img.Pix)
	}
	p.evict()
	p.mu.Unlock()
	a.redrawShowing(it.ref, it.aspect)
}

// redrawShowing draws again, in every map tab, the nodes showing ref, and gives those that do not know it yet the
// proportions of what it shows.
func (a *App) redrawShowing(ref string, aspect float64) {
	for _, d := range a.docs {
		md := d.board
		if md == nil {
			continue
		}
		var regions []mind.Rect
		for _, id := range md.m.Showing(ref) {
			n := md.m.Node(id)
			if n == nil {
				continue
			}
			if aspect > 0 && n.W == 0 && math.Abs(n.Aspect-aspect) > 0.001 {
				md.m.SetAspect(id, aspect)
			}
			regions = append(regions, md.m.Box(n))
		}
		md.tiles.Invalidate(regions, false)
	}
	a.requestFrame()
}

// evict drops the pictures shown longest ago while over the budget; p.mu is held.
func (p *mindPics) evict() {
	if p.bytes <= picBudget {
		return
	}
	keys := make([]picKey, 0, len(p.items))
	for k, it := range p.items {
		if !it.loading && it.img != nil {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, func(a, b picKey) int {
		if p.items[a].used < p.items[b].used {
			return -1
		}
		return 1
	})
	for _, k := range keys {
		if p.bytes <= picBudget*3/4 {
			break
		}
		p.bytes -= len(p.items[k].img.Pix)
		delete(p.items, k)
	}
}

// drop forgets every picture of ref, so it is read again when next shown; it returns the references dropped.
func (p *mindPics) drop(ref string) []string {
	r := strings.ToLower(ref)
	delete(p.anims, ref)
	return p.dropWhere(func(k picKey, it *picItem) bool { return k.ref == r })
}

// dropFile forgets the pictures of the map file path and of every nested map drawn from it; it returns the
// references dropped.
func (p *mindPics) dropFile(path string) []string {
	key := mind.NestKey(path)
	return p.dropWhere(func(k picKey, it *picItem) bool {
		if k.kind != mind.Nested {
			return false
		}
		hit := mind.NestKey(it.ref) == key
		for _, f := range it.files {
			hit = hit || mind.NestKey(f) == key
		}
		return hit
	})
}

// dropPicture forgets the pictures read from the file path, a picture or a canvas; it returns the references
// dropped.
func (p *mindPics) dropPicture(path string) []string {
	key := mind.NestKey(path)
	return p.dropWhere(func(k picKey, it *picItem) bool { return k.kind == mind.Picture && mind.NestKey(it.ref) == key })
}

// dropWhere forgets the pictures that match; one being read is read again once it is.
func (p *mindPics) dropWhere(match func(picKey, *picItem) bool) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := map[string]bool{}
	var refs []string
	for k, it := range p.items {
		if !match(k, it) {
			continue
		}
		if !seen[it.ref] {
			seen[it.ref] = true
			refs = append(refs, it.ref)
		}
		if it.loading {
			it.redo = true
			continue
		}
		if it.img != nil {
			p.bytes -= len(it.img.Pix)
		}
		delete(p.items, k)
	}
	return refs
}

// loadMindPicture reads one picture, at its step's width.
func loadMindPicture(ctx context.Context, a *App, k picKey, ref string, fonts gfx.Fonts, labels mind.Labels, src mindSource) *picItem {
	box := picLevels[k.level]
	it := &picItem{ref: ref, state: mind.PicReady}
	var err error
	switch k.kind {
	case mind.Nested:
		t := mind.DrawNested(ref, []string{k.chain}, box, fonts, labels, src)
		it.img, it.state, it.aspect, it.files = t.Img, t.State, t.Aspect, t.Files
		return it
	case mind.Video:
		it.img, it.aspect, err = videoStill(ctx, ref, box)
	case mind.Smart:
		it.img, err = readStill(ctx, a.smartImage(ref), box)
	default:
		it.img, it.animated, err = readPicture(ctx, ref, box)
	}
	if err != nil || it.img == nil {
		return &picItem{ref: ref, state: mind.PicMissing}
	}
	if it.aspect == 0 {
		it.aspect = float64(it.img.Rect.Dx()) / float64(max(it.img.Rect.Dy(), 1))
	}
	return it
}

// shrinkTo scales img down to fit box pixels wide (and as tall), never up.
func shrinkTo(img image.Image, box int) *image.RGBA {
	b := img.Bounds()
	s := math.Min(1, float64(box)/float64(max(b.Dx(), b.Dy())))
	w, h := max(1, int(float64(b.Dx())*s+0.5)), max(1, int(float64(b.Dy())*s+0.5))
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	if w == b.Dx() && h == b.Dy() {
		draw.Draw(out, out.Rect, img, b.Min, draw.Src)
	} else {
		xdraw.CatmullRom.Scale(out, out.Rect, img, b, xdraw.Src, nil)
	}
	return out
}

// readPicture reads a picture file for the board: a still or an animation's first frame, or a canvas composed.
func readPicture(ctx context.Context, path string, box int) (*image.RGBA, bool, error) {
	if strings.EqualFold(pathExt(path), paint.Ext) {
		img, err := canvasPreview(path)
		if err != nil {
			return nil, false, err
		}
		return shrinkTo(img, box), false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	if an, err := anim.Decode(data); err == nil && an.Len() > 1 {
		first, _, err := an.Next()
		if err != nil {
			return nil, false, err
		}
		return shrinkTo(premultiplied(first), box), true, nil
	}
	img, err := decodePreview(ctx, path, data)
	if err != nil {
		return nil, false, err
	}
	return shrinkTo(img, box), false, nil
}

// readStill reads a still picture file.
func readStill(ctx context.Context, path string, box int) (*image.RGBA, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	img, err := decodePreview(ctx, path, data)
	if err != nil {
		return nil, err
	}
	return shrinkTo(img, box), nil
}

// videoStill is a frame a little way into a video, and its proportions.
func videoStill(ctx context.Context, path string, box int) (*image.RGBA, float64, error) {
	info, err := media.Probe(ctx, path)
	if err != nil {
		return nil, 0, err
	}
	if !info.HasVideo || info.Width <= 0 || info.Height <= 0 {
		return nil, 0, errors.New("no picture in this file")
	}
	aspect := float64(info.Width) / float64(info.Height)
	s := math.Min(1, float64(box)/float64(max(info.Width, info.Height)))
	img, err := media.Frame(ctx, path, math.Min(info.Duration*0.1, 10), max(2, int(float64(info.Width)*s)), max(2, int(float64(info.Height)*s)))
	return img, aspect, err
}

// premultiplied is a straight-alpha picture with its colours multiplied by their alpha, as the screen wants.
func premultiplied(img *image.NRGBA) *image.RGBA {
	out := image.NewRGBA(img.Rect)
	for i := 0; i < len(img.Pix); i += 4 {
		al := uint32(img.Pix[i+3])
		out.Pix[i] = uint8(uint32(img.Pix[i]) * al / 255)
		out.Pix[i+1] = uint8(uint32(img.Pix[i+1]) * al / 255)
		out.Pix[i+2] = uint8(uint32(img.Pix[i+2]) * al / 255)
		out.Pix[i+3] = img.Pix[i+3]
	}
	return out
}

func pathExt(path string) string {
	if i := strings.LastIndexByte(path, '.'); i >= 0 && !strings.ContainsAny(path[i:], `\/`) {
		return path[i:]
	}
	return ""
}

// mindSource reads the pictures inside nested maps, away from the window, waiting for each.
type mindSource struct{ a *App }

func (s mindSource) Still(n *mind.Node, px int) *image.RGBA {
	ctx := s.a.life
	var img *image.RGBA
	switch n.Kind {
	case mind.Video:
		img, _, _ = videoStill(ctx, n.Ref, px)
	case mind.Smart:
		img, _ = readStill(ctx, s.a.smartImage(n.Ref), px)
	default:
		img, _, _ = readPicture(ctx, n.Ref, px)
	}
	return img
}

// mindLabels are the board's placeholder words in the user's language.
func (a *App) mindLabels() mind.Labels {
	u := &a.u
	return mind.Labels{Missing: u.t("File not found"), Cycle: u.t("This map holds itself"), TooDeep: u.t("Nested too deep to show")}
}

// mindFileDrawer draws map files for the asset manager's grid and preview, their pictures read as they are drawn;
// it is made on the UI goroutine and may be called from any other.
func (a *App) mindFileDrawer() func(path string, box int) (*image.RGBA, error) {
	fonts, labels, src := a.u.fonts, a.mindLabels(), mindSource{a}
	return func(path string, box int) (*image.RGBA, error) {
		return mind.DrawFile(path, box, fonts, labels, src)
	}
}

// picAnim is an animated picture playing on the boards: its frames are read one after another as they fall due.
type picAnim struct {
	an      *anim.Animation
	frame   *image.RGBA
	due     time.Time
	loading bool
	failed  bool
}

// animFrame is the frame of the animated picture at path to show now, reading the next when it is due; it runs on
// the UI goroutine and returns nil until the file is read.
func (a *App) animFrame(path string) *image.RGBA {
	p := a.mindPictures()
	pa := p.anims[path]
	if pa == nil {
		pa = &picAnim{loading: true}
		p.anims[path] = pa
		a.background(func(context.Context) {
			data, err := os.ReadFile(path)
			var an *anim.Animation
			if err == nil {
				an, err = anim.Decode(data)
			}
			a.post(func() {
				if cur := p.anims[path]; cur == pa {
					pa.loading, pa.an, pa.failed = false, an, err != nil
					a.requestFrame()
				}
			})
		})
	}
	if pa.loading || pa.failed {
		return nil
	}
	if pa.frame == nil || !time.Now().Before(pa.due) {
		img, d, err := pa.an.Next()
		if err != nil {
			pa.failed = true
			return pa.frame
		}
		pa.frame = premultiplied(img)
		pa.due = time.Now().Add(d)
	}
	return pa.frame
}

// animDue is when the next frame of any animation shown is due, zero when none plays.
func (p *mindPics) animDue() time.Time {
	var due time.Time
	for _, pa := range p.anims {
		if pa.an != nil && !pa.failed && (due.IsZero() || pa.due.Before(due)) {
			due = pa.due
		}
	}
	return due
}

// animated reports the files of the animated pictures read so far.
func (p *mindPics) animatedRefs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for _, it := range p.items {
		if it.animated && !seen[it.ref] {
			seen[it.ref] = true
			out = append(out, it.ref)
		}
	}
	return out
}

// pictureBusy reports whether pictures are being read.
func (p *mindPics) pictureBusy() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.busy > 0 || len(p.want) > 0
}
