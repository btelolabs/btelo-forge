package paint

import (
	"errors"
	"image"
	"math"
	"math/rand/v2"
)

// Healing, as Photoshop's: the Healing Brush and the Patch tool lay texture from elsewhere and let it take on the
// tones around where it lands, by solving for a smooth correction across it (Pérez, Gangnet and Blake's Poisson image
// editing); the Spot Healing Brush and Content-Aware Fill need no source, and make what goes there from patches of what
// is around it (Barnes et al.'s PatchMatch, in the manner of Wexler, Shechtman and Irani's completion). Pixels are
// premultiplied, and alpha is healed with the colour.

// ErrNothingToSample means there is too little picture around an area to fill it from.
var ErrNothingToSample = errors.New("there is nothing around the area to sample from")

// ErrHealNeedsPixels means healing was asked of a layer's mask, which has only tones to heal.
var ErrHealNeedsPixels = errors.New("healing works on a layer's pixels, not its mask")

// relaxSweeps is how many times each level of the membrane is smoothed.
const relaxSweeps = 40

// membrane makes v smooth over the pixels inside, keeping the others as the edge it must meet. Relaxing alone would
// take a time that grows with the area squared, so each level starts from the answer of one half its size.
func membrane(v []float32, inside []bool, w, h int) {
	unknown := false
	for _, in := range inside {
		if in {
			unknown = true
			break
		}
	}
	if !unknown {
		return
	}
	if max(w, h) > 4 {
		cw, ch := (w+1)/2, (h+1)/2
		cv := make([]float32, cw*ch)
		cin := make([]bool, cw*ch)
		for y := range ch {
			for x := range cw {
				var sum float32
				cells, hole := 0, false
				for j := y * 2; j < min(y*2+2, h); j++ {
					for i := x * 2; i < min(x*2+2, w); i++ {
						cells++
						if inside[j*w+i] {
							hole = true
						} else {
							sum += v[j*w+i]
						}
					}
				}
				cin[y*cw+x] = hole
				if !hole {
					cv[y*cw+x] = sum / float32(cells)
				}
			}
		}
		membrane(cv, cin, cw, ch)
		for y := range h {
			for x := range w {
				if inside[y*w+x] {
					v[y*w+x] = cv[(y/2)*cw+x/2]
				}
			}
		}
	}
	const over = 1.7 // successive over-relaxation
	for range relaxSweeps {
		for colour := range 2 {
			for y := range h {
				for x := (y + colour) % 2; x < w; x += 2 {
					i := y*w + x
					if !inside[i] {
						continue
					}
					var sum float32
					n := float32(0)
					if x > 0 {
						sum, n = sum+v[i-1], n+1
					}
					if x < w-1 {
						sum, n = sum+v[i+1], n+1
					}
					if y > 0 {
						sum, n = sum+v[i-w], n+1
					}
					if y < h-1 {
						sum, n = sum+v[i+w], n+1
					}
					v[i] += over * (sum/n - v[i])
				}
			}
		}
	}
}

// healInto works out what the pixels marked in inside become when source is laid over target: source's detail with
// target's tones. Both images cover the same rectangle.
func healInto(target, source *image.RGBA, inside []bool) *image.RGBA {
	r := target.Rect
	w, h := r.Dx(), r.Dy()
	out := image.NewRGBA(r)
	copy(out.Pix, target.Pix)
	diff := make([]float32, w*h)
	for c := range 4 {
		for y := range h {
			ti, si := target.PixOffset(r.Min.X, r.Min.Y+y), source.PixOffset(r.Min.X, r.Min.Y+y)
			for x := range w {
				diff[y*w+x] = float32(target.Pix[ti+4*x+c]) - float32(source.Pix[si+4*x+c])
			}
		}
		membrane(diff, inside, w, h)
		for y := range h {
			si, oi := source.PixOffset(r.Min.X, r.Min.Y+y), out.PixOffset(r.Min.X, r.Min.Y+y)
			for x := range w {
				if inside[y*w+x] {
					out.Pix[oi+4*x+c] = uint8(min(max(float32(source.Pix[si+4*x+c])+diff[y*w+x]+0.5, 0), 255))
				}
			}
		}
	}
	for i := 0; i < len(out.Pix); i += 4 {
		a := out.Pix[i+3]
		out.Pix[i], out.Pix[i+1], out.Pix[i+2] = min(out.Pix[i], a), min(out.Pix[i+1], a), min(out.Pix[i+2], a)
	}
	return out
}

// The patches filling is done with, and how much it searches at each level of its pyramid.
const (
	patchRadius   = 3
	patchFit      = 40000 // the distance at which a patch counts half
	fillPasses    = 16    // of guessing and painting, at the coarsest level
	searchSweeps  = 3
	fillMinPasses = 5 // at each finer one
)

// fillLevel is one level of the picture being filled: its colours, what must be made, and where whole patches can be
// taken from.
type fillLevel struct {
	w, h  int
	pix   []float32 // four a pixel
	hole  []bool
	valid []bool // a patch can be taken with this centre
}

// sourcesOf marks the centres of patches with nothing of the hole or of nothing at all in them. A summed-area table
// makes that one look at each pixel, whatever the patch's size.
func (l *fillLevel) sourcesOf() int {
	w, h := l.w, l.h
	sums := make([]int32, (w+1)*(h+1))
	for y := range h {
		var row int32
		for x := range w {
			if l.hole[y*w+x] || l.pix[(y*w+x)*4+3] < 128 {
				row++
			}
			sums[(y+1)*(w+1)+x+1] = sums[y*(w+1)+x+1] + row
		}
	}
	l.valid = make([]bool, w*h)
	count := 0
	for y := patchRadius; y < h-patchRadius; y++ {
		for x := patchRadius; x < w-patchRadius; x++ {
			x0, y0, x1, y1 := x-patchRadius, y-patchRadius, x+patchRadius+1, y+patchRadius+1
			if sums[y1*(w+1)+x1]-sums[y0*(w+1)+x1]-sums[y1*(w+1)+x0]+sums[y0*(w+1)+x0] == 0 {
				l.valid[y*w+x] = true
				count++
			}
		}
	}
	return count
}

// distance is how unlike the patches at two centres are, giving up past limit. Only every other pixel is compared:
// it costs half and finds the same patches.
func (l *fillLevel) distance(ax, ay, bx, by int, limit float32) float32 {
	var sum float32
	w := l.w
	for j := -patchRadius; j <= patchRadius; j++ {
		y0, y1 := ay+j, by+j
		if y0 < 0 || y0 >= l.h {
			continue
		}
		for i := -patchRadius + (j+patchRadius)%2; i <= patchRadius; i += 2 {
			x0 := ax + i
			if x0 < 0 || x0 >= w {
				continue
			}
			a := l.pix[(y0*w+x0)*4:][:3]
			b := l.pix[(y1*w+bx+i)*4:][:3]
			d0, d1, d2 := a[0]-b[0], a[1]-b[1], a[2]-b[2]
			sum += d0*d0 + d1*d1 + d2*d2
		}
		if sum >= limit {
			return sum
		}
	}
	return sum
}

// halved is the level below: a cell is part of the hole if any of its pixels is, and otherwise their average.
func (l *fillLevel) halved() *fillLevel {
	out := &fillLevel{w: (l.w + 1) / 2, h: (l.h + 1) / 2}
	out.pix = make([]float32, out.w*out.h*4)
	out.hole = make([]bool, out.w*out.h)
	for y := range out.h {
		for x := range out.w {
			var sum [4]float32
			n := float32(0)
			hole := false
			for j := y * 2; j < min(y*2+2, l.h); j++ {
				for i := x * 2; i < min(x*2+2, l.w); i++ {
					p := (j*l.w + i)
					hole = hole || l.hole[p]
					for c := range 4 {
						sum[c] += l.pix[p*4+c]
					}
					n++
				}
			}
			out.hole[y*out.w+x] = hole
			for c := range 4 {
				out.pix[(y*out.w+x)*4+c] = sum[c] / n
			}
		}
	}
	return out
}

// widest is how far the hole's middle lies from its edge, in pixels.
func (l *fillLevel) widest() float64 {
	dist := make([]float32, l.w*l.h)
	for i, in := range l.hole {
		if in {
			dist[i] = farAway
		}
	}
	newDistanceLine(max(l.w, l.h)).transform(dist, l.w, l.h)
	var most float32
	for i, in := range l.hole {
		if in && dist[i] < farAway {
			most = max(most, dist[i])
		}
	}
	return math.Sqrt(float64(most))
}

// fillNear holds, for each centre a patch can touch the hole from, the patch it is most like.
type fillNear struct {
	at   []int32 // index of the centre, or -1 when no patch reaches the hole
	cost []float32
}

// targets lists the centres of the patches that touch the hole.
func (l *fillLevel) targets() []int32 {
	w, h := l.w, l.h
	mark := make([]bool, w*h)
	for y := range h {
		for x := range w {
			if !l.hole[y*w+x] {
				continue
			}
			for j := max(y-patchRadius, 0); j <= min(y+patchRadius, h-1); j++ {
				for i := max(x-patchRadius, 0); i <= min(x+patchRadius, w-1); i++ {
					mark[j*w+i] = true
				}
			}
		}
	}
	var out []int32
	for i, m := range mark {
		if m {
			out = append(out, int32(i))
		}
	}
	return out
}

// search improves the guesses: each centre tries what its neighbours found, moved along, then random centres nearer
// and nearer to its best, in sweeps from each corner by turns (PatchMatch).
func (l *fillLevel) search(near *fillNear, targets []int32, rng *rand.Rand) {
	w, h := l.w, l.h
	try := func(t int32, x, y int) {
		if x < 0 || y < 0 || x >= w || y >= h {
			return
		}
		c := int32(y*w + x)
		if !l.valid[c] || c == near.at[t] {
			return
		}
		tx, ty := int(t)%w, int(t)/w
		if cost := l.distance(tx, ty, x, y, near.cost[t]); cost < near.cost[t] {
			near.at[t], near.cost[t] = c, cost
		}
	}
	for s := range searchSweeps {
		step := 1
		first, last := 0, len(targets)
		if s%2 == 1 {
			step, first, last = -1, len(targets)-1, -1
		}
		for k := first; k != last; k += step {
			t := targets[k]
			if at := near.at[int(t)-step]; int(t)%w != 0 && int(t)%w != w-1 && at >= 0 {
				try(t, int(at)%w+step, int(at)/w)
			}
			if up := int(t) - step*w; up >= 0 && up < w*h {
				if at := near.at[up]; at >= 0 {
					try(t, int(at)%w, int(at)/w+step)
				}
			}
			if near.at[t] < 0 {
				continue
			}
			bx, by := int(near.at[t])%w, int(near.at[t])/w
			for reach := max(w, h); reach >= 1; reach /= 2 {
				try(t, bx+rng.IntN(2*reach+1)-reach, by+rng.IntN(2*reach+1)-reach)
			}
		}
	}
}

// paint makes each pixel of the hole what the patches over it say there, the likelier a patch fits the more it counts:
// a plain average of patches that disagree is a blur.
func (l *fillLevel) paint(near *fillNear, targets []int32) {
	w, h := l.w, l.h
	sum := make([]float32, w*h*4)
	weight := make([]float32, w*h)
	for _, t := range targets {
		at := near.at[t]
		if at < 0 {
			continue
		}
		tx, ty, sx, sy := int(t)%w, int(t)/w, int(at)%w, int(at)/w
		fit := 1 / (1 + near.cost[t]/patchFit)
		for j := -patchRadius; j <= patchRadius; j++ {
			y := ty + j
			if y < 0 || y >= h {
				continue
			}
			for i := -patchRadius; i <= patchRadius; i++ {
				x := tx + i
				if x < 0 || x >= w || !l.hole[y*w+x] {
					continue
				}
				src := l.pix[((sy+j)*w+sx+i)*4:][:4]
				dst := sum[(y*w+x)*4:][:4]
				for c := range 4 {
					dst[c] += src[c] * fit
				}
				weight[y*w+x] += fit
			}
		}
	}
	for i, in := range l.hole {
		if in && weight[i] > 0 {
			for c := range 4 {
				l.pix[i*4+c] = sum[i*4+c] / weight[i]
			}
		}
	}
}

// peelCandidates is how many centres each pixel of the coarsest level looks at when the hole is peeled.
const peelCandidates = 4096

// peel fills the hole from its edge in, a ring at a time, each pixel from the patch most like what is really around
// it. A smooth first guess would decide the middle of the hole by itself, with nothing there to go on; peeled, what
// meets at the middle has come from the edges on either side.
func (l *fillLevel) peel(near *fillNear, centres []int32) {
	w, h := l.w, l.h
	known := make([]bool, w*h)
	for i, in := range l.hole {
		known[i] = !in
	}
	stride := max(len(centres)/peelCandidates, 1)
	touches := func(i int) bool {
		x, y := i%w, i/w
		return x > 0 && known[i-1] || x < w-1 && known[i+1] || y > 0 && known[i-w] || y < h-1 && known[i+w]
	}
	for {
		var ring []int
		for i, in := range l.hole {
			if in && !known[i] && touches(i) {
				ring = append(ring, i)
			}
		}
		if len(ring) == 0 {
			return
		}
		for _, t := range ring {
			tx, ty := t%w, t/w
			best, cost := int32(-1), float32(math.Inf(1))
			for k := 0; k < len(centres); k += stride {
				c := int(centres[k])
				sx, sy := c%w, c/w
				var sum, seen float32
				for j := -patchRadius; j <= patchRadius; j++ {
					y := ty + j
					if y < 0 || y >= h {
						continue
					}
					for i := -patchRadius; i <= patchRadius; i++ {
						x := tx + i
						if x < 0 || x >= w || !known[y*w+x] {
							continue
						}
						a, b := l.pix[(y*w+x)*4:][:3], l.pix[((sy+j)*w+sx+i)*4:][:3]
						d0, d1, d2 := a[0]-b[0], a[1]-b[1], a[2]-b[2]
						sum += d0*d0 + d1*d1 + d2*d2
						seen++
					}
				}
				if seen > 0 && sum/seen < cost {
					best, cost = centres[k], sum/seen
				}
			}
			copy(l.pix[t*4:][:4], l.pix[int(best)*4:][:4])
			near.at[t] = best
		}
		for _, t := range ring {
			known[t] = true
		}
	}
}

// settle gives each pixel of the hole the one patch centre that fits it, so the finest level ends sharp.
func (l *fillLevel) settle(near *fillNear, targets []int32) {
	for _, t := range targets {
		if l.hole[t] && near.at[t] >= 0 {
			copy(l.pix[int(t)*4:][:4], l.pix[int(near.at[t])*4:][:4])
		}
	}
}

// solveHole fills the hole of l, the level at its top down: the coarsest level starts from a smooth guess, and each
// finer one from the one below's picture and guesses.
func (l *fillLevel) solveHole(depth int, rng *rand.Rand) (*fillNear, error) {
	if l.sourcesOf() == 0 {
		return nil, ErrNothingToSample
	}
	targets := l.targets()
	near := &fillNear{at: make([]int32, l.w*l.h), cost: make([]float32, l.w*l.h)}
	for i := range near.at {
		near.at[i], near.cost[i] = -1, float32(math.Inf(1))
	}
	passes := fillMinPasses
	if depth > 0 {
		down := l.halved()
		from, err := down.solveHole(depth-1, rng)
		if err != nil {
			if !errors.Is(err, ErrNothingToSample) {
				return nil, err
			}
			from = nil
			depth = 0 // too small to look at: start here
		}
		if from != nil {
			for y := range l.h {
				for x := range l.w {
					i := y*l.w + x
					if l.hole[i] {
						copy(l.pix[i*4:][:4], down.pix[((y/2)*down.w+x/2)*4:][:4])
					}
				}
			}
			for _, t := range targets {
				tx, ty := int(t)%l.w, int(t)/l.w
				if at := from.at[(ty/2)*down.w+tx/2]; at >= 0 {
					sx, sy := int(at)%down.w*2+tx%2, int(at)/down.w*2+ty%2
					if sx < l.w && sy < l.h && l.valid[sy*l.w+sx] {
						near.at[t], near.cost[t] = int32(sy*l.w+sx), l.distance(tx, ty, sx, sy, near.cost[t])
					}
				}
			}
		}
	}
	if depth == 0 {
		passes = fillPasses
	}
	centres := make([]int32, 0, len(l.valid))
	for i, ok := range l.valid {
		if ok {
			centres = append(centres, int32(i))
		}
	}
	if depth == 0 && near.at[targets[0]] < 0 {
		l.peel(near, centres)
	}
	for _, t := range targets {
		if near.at[t] < 0 {
			c := centres[rng.IntN(len(centres))]
			near.at[t] = c
			near.cost[t] = l.distance(int(t)%l.w, int(t)/l.w, int(c)%l.w, int(c)/l.w, float32(math.Inf(1)))
		}
	}
	for range passes {
		for _, t := range targets {
			near.cost[t] = l.distance(int(t)%l.w, int(t)/l.w, int(near.at[t])%l.w, int(near.at[t])/l.w, float32(math.Inf(1)))
		}
		l.search(near, targets, rng)
		l.paint(near, targets)
	}
	return near, nil
}

// fillHole makes the pixels marked in hole from the rest of img, and returns the picture filled.
func fillHole(img *image.RGBA, hole []bool) (*image.RGBA, error) {
	r := img.Rect
	l := &fillLevel{w: r.Dx(), h: r.Dy(), pix: make([]float32, r.Dx()*r.Dy()*4), hole: hole}
	for y := range l.h {
		row := img.Pix[img.PixOffset(r.Min.X, r.Min.Y+y):]
		for x := range l.w * 4 {
			l.pix[y*l.w*4+x] = float32(row[x])
		}
	}
	// Coarse enough that patches span the hole's middle.
	depth := 0
	for w := l.widest(); w > patchRadius*2 && min(l.w>>depth, l.h>>depth) > 4*patchRadius; w /= 2 {
		depth++
	}
	near, err := l.solveHole(depth, rand.New(rand.NewPCG(uint64(l.w), uint64(l.h))))
	if err != nil {
		return nil, err
	}
	l.settle(near, l.targets())
	out := image.NewRGBA(r)
	for y := range l.h {
		row := out.Pix[out.PixOffset(r.Min.X, r.Min.Y+y):]
		for x := range l.w * 4 {
			row[x] = uint8(min(max(l.pix[y*l.w*4+x]+0.5, 0), 255))
		}
	}
	return out, nil
}

// fillArea is how far around a hole of a size filling looks for what to fill it with.
func fillArea(b image.Rectangle) image.Rectangle {
	margin := max(max(b.Dx(), b.Dy())*3/2, 48)
	return b.Inset(-margin)
}

// writeHealed lays a healed picture on the layer, each pixel by how far it is covered, as the layer allows.
func writeHealed(l *Node, healed *image.RGBA, cover func(x, y int) uint32, before *image.RGBA) {
	r := healed.Rect
	l.Pixels.Tiles(r, func(pix []uint8, part image.Rectangle, corner image.Point) {
		for y := part.Min.Y; y < part.Max.Y; y++ {
			for x := part.Min.X; x < part.Max.X; x++ {
				a := cover(x, y)
				if a == 0 {
					continue
				}
				bi, hi := before.PixOffset(x, y), healed.PixOffset(x, y)
				p := pix[((y-corner.Y)*TileSize+x-corner.X)*4:][:4]
				// What is painted lies over the layer: on an empty layer, what all layers show lands whole.
				k := 255 - mul8(uint32(healed.Pix[hi+3]), a)
				for c := range 4 {
					p[c] = uint8(mul8(uint32(healed.Pix[hi+c]), a) + mul8(uint32(before.Pix[bi+c]), k))
				}
				if l.LockAlpha {
					keepAlpha(p, uint32(before.Pix[bi+3]))
				}
			}
		}
	})
}

// sampleOf is what healing reads: the chosen layer or the picture as it looks.
func (d *Doc) sampleOf(l *Node, allLayers bool) *cloneSource {
	if allLayers {
		return &cloneSource{root: snapshotTree(d.Root)}
	}
	return &cloneSource{layer: l.Pixels.Snapshot()}
}

// healable is the chosen layer, when its pixels can be healed.
func (d *Doc) healable() (*Node, error) {
	l, err := d.pixelLayer()
	if err != nil {
		return nil, err
	}
	if d.EditMask && l.Mask != nil || l.Pixels == nil {
		return nil, ErrHealNeedsPixels
	}
	return l, nil
}

// PatchSelection replaces what is selected with what is (dx, dy) away, taking on the tones around the selection, as
// the Patch tool does when the selection is dragged to the source. The selection stays.
func (d *Doc) PatchSelection(dx, dy int, allLayers bool) error {
	l, err := d.healable()
	if err != nil {
		return err
	}
	if d.Selection == nil {
		return ErrNoSelection
	}
	b := d.SelectionBounds().Inset(-2)
	if b.Empty() {
		return nil
	}
	sample := d.sampleOf(l, allLayers)
	target := sample.read(b)
	from := *sample
	from.dx, from.dy = dx, dy
	sel := d.selectionIn(b)
	inside := make([]bool, b.Dx()*b.Dy())
	for i := range inside {
		inside[i] = sel.Pix[i] != 0
	}
	before := l.Pixels.Read(b)
	m := d.Mark()
	d.dropText(l)
	writeHealed(l, healInto(target, from.read(b), inside), func(x, y int) uint32 { return coverage(sel, x, y) }, before)
	d.Commit("Patch", m)
	return nil
}

// ContentFill is Content-Aware Fill being worked out: Compute is slow and only reads what Prepare took, so it may run
// on another goroutine; Apply lays the result on the layer as one step.
type ContentFill struct {
	d      *Doc
	id     int
	area   image.Rectangle
	img    *image.RGBA
	hole   []bool
	sel    *Mask
	result *image.RGBA
}

// PrepareContentFill takes what filling the selection on the chosen layer needs, from the layer or, with allLayers,
// from the picture as it looks.
func (d *Doc) PrepareContentFill(allLayers bool) (*ContentFill, error) {
	l, err := d.healable()
	if err != nil {
		return nil, err
	}
	if d.Selection == nil {
		return nil, ErrNoSelection
	}
	b := d.SelectionBounds()
	if b.Empty() {
		return nil, ErrNoSelection
	}
	area := fillArea(b)
	if !d.Frame.Empty() {
		area = area.Intersect(d.Frame)
	}
	f := &ContentFill{d: d, id: l.ID, area: area, img: d.sampleOf(l, allLayers).read(area), sel: d.Selection}
	sel := d.selectionIn(area)
	f.hole = make([]bool, area.Dx()*area.Dy())
	for i := range f.hole {
		f.hole[i] = sel.Pix[i] != 0
	}
	return f, nil
}

// Compute works the fill out.
func (f *ContentFill) Compute() error {
	out, err := fillHole(f.img, f.hole)
	f.result = out
	return err
}

// Apply lays the fill on the layer it was worked out for, as the selection was then, and returns the area changed.
func (f *ContentFill) Apply() (image.Rectangle, error) {
	d := f.d
	l := d.Node(f.id)
	if l == nil || l.Pixels == nil {
		return image.Rectangle{}, ErrNoLayer
	}
	sel := f.sel.Read(f.area)
	before := l.Pixels.Read(f.area)
	m := d.Mark()
	d.dropText(l)
	writeHealed(l, f.result, func(x, y int) uint32 { return coverage(sel, x, y) }, before)
	d.Commit("Content-aware fill", m)
	return f.area, nil
}

// healing is what a healing stroke does when it ends: the Healing Brush lays what the stroke cloned (from) so that it
// fits, the Spot Healing Brush makes what goes there itself.
type healing struct {
	whole *cloneSource // the picture the stroke heals, unmoved
	spot  bool
}

// BeginHeal starts a Healing Brush stroke at (x, y): as the clone stamp from (dx, dy) away while it is drawn, it takes
// on the tones around it when it ends.
func (d *Doc) BeginHeal(b Brush, dx, dy int, allLayers bool, x, y float64) (*Stroke, image.Rectangle, error) {
	l, err := d.healable()
	if err != nil {
		return nil, image.Rectangle{}, err
	}
	whole := d.sampleOf(l, allLayers)
	src := *whole
	src.dx, src.dy = dx, dy
	s, r, err := d.beginStroke(b, false, &src, x, y)
	if err == nil {
		s.heal = &healing{whole: whole}
	}
	return s, r, err
}

// BeginSpotHeal starts a Spot Healing Brush stroke at (x, y): it marks where it goes, a half-seen grey, and fills what it
// marked when it ends.
func (d *Doc) BeginSpotHeal(b Brush, allLayers bool, x, y float64) (*Stroke, image.Rectangle, error) {
	l, err := d.healable()
	if err != nil {
		return nil, image.Rectangle{}, err
	}
	whole := d.sampleOf(l, allLayers)
	b.Color.R, b.Color.G, b.Color.B, b.Color.A, b.Opacity = 128, 128, 128, 255, 0.5
	s, r, err := d.beginStroke(b, false, nil, x, y)
	if err == nil {
		s.heal = &healing{whole: whole, spot: true}
	}
	return s, r, err
}

// finishHeal puts the layer back as it was under the stroke and lays the healed result in its place. A stroke with
// nothing around it to sample leaves the pixels as they were.
func (s *Stroke) finishHeal() {
	d := s.doc
	b := s.dirty
	sel := d.selectionIn(b)
	cover := func(x, y int) uint32 {
		if !(image.Point{x, y}).In(b) {
			return 0
		}
		return mul8((s.coverOf(x, y)*255+32767)/65535, coverage(sel, x, y))
	}
	before := s.beforePix.Read(b)
	s.layer.Pixels.Write(before)
	var healed *image.RGBA
	if s.heal.spot {
		healed = s.spotted(b, cover)
	} else {
		area := b.Inset(-2)
		inside := make([]bool, area.Dx()*area.Dy())
		for y := area.Min.Y; y < area.Max.Y; y++ {
			for x := area.Min.X; x < area.Max.X; x++ {
				inside[(y-area.Min.Y)*area.Dx()+x-area.Min.X] = cover(x, y) != 0
			}
		}
		healed = healInto(s.heal.whole.read(area), s.clone.read(area), inside)
	}
	if healed != nil {
		writeHealed(s.layer, healed, cover, s.beforePix.Read(healed.Rect))
	}
}

// spotted is the stroke's marks filled from around them; nil when nothing around can.
func (s *Stroke) spotted(b image.Rectangle, cover func(x, y int) uint32) *image.RGBA {
	area := fillArea(b)
	if f := s.doc.Frame; !f.Empty() {
		area = area.Intersect(f)
	}
	hole := make([]bool, area.Dx()*area.Dy())
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			hole[(y-area.Min.Y)*area.Dx()+x-area.Min.X] = cover(x, y) != 0
		}
	}
	out, err := fillHole(s.heal.whole.read(area), hole)
	if err != nil {
		return nil
	}
	return out
}
