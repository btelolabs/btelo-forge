package paint

import (
	"image"
	"math"
	"runtime"
	"sync"
)

// Select › Modify, as Photoshop's: Expand and Contract move the selection's edge by a distance, with round corners
// as a brush of that radius would leave them, Smooth takes the jags off it, and Border selects a band along it.

// softLevels is how many slices of a soft selection Expand and Contract move: a feathered edge keeps its softness,
// and steps of 255/32 cannot be seen in it.
const softLevels = 32

// ExpandSelection moves the selection's edge out by radius pixels.
func (d *Doc) ExpandSelection(radius int) { d.modifySelection("Expand", radius, expandMask) }

// ContractSelection moves the selection's edge in by radius pixels.
func (d *Doc) ContractSelection(radius int) { d.modifySelection("Contract", radius, contractMask) }

// SmoothSelection evens the selection's edge over about radius pixels, closing small holes and taking off small bumps.
func (d *Doc) SmoothSelection(radius int) { d.modifySelection("Smooth", radius, smoothMask) }

// BorderSelection replaces the selection with a band width pixels wide, half of it either side of the edge.
func (d *Doc) BorderSelection(width int) { d.modifySelection("Border", width, borderMask) }

func (d *Doc) modifySelection(name string, size int, fn func(*Mask, int) *Mask) {
	if d.Selection == nil || size <= 0 {
		return
	}
	m := d.Mark()
	d.setSelection(fn(d.Selection, size))
	d.Commit(name, m)
}

// expandMask is m grown by radius. Beyond its tiles, where it is all its fill, only what reaches out of them changes.
func expandMask(m *Mask, radius int) *Mask {
	area := m.TileBounds()
	out := NewMask(m.Fill())
	if area.Empty() {
		return out
	}
	out.Write(dilate(m.Read(area.Inset(-radius-1)), radius))
	return out
}

// contractMask is m shrunk by radius: what it does not select, grown.
func contractMask(m *Mask, radius int) *Mask {
	inverse := m.Snapshot()
	inverse.Invert()
	out := expandMask(inverse, radius)
	out.Invert()
	return out
}

// smoothMask blurs m's edge and draws it back up steeply, so it stays an edge: what most of its surroundings are
// wins, which is what takes the jags off.
func smoothMask(m *Mask, radius int) *Mask {
	steep := func(v uint8) uint8 {
		return uint8(min(max((float64(v)-127.5)*float64(2*radius)+127.5, 0), 255) + 0.5)
	}
	blurred := featherMask(m, float64(radius))
	out := NewMask(steep(blurred.Fill()))
	area := blurred.TileBounds()
	if area.Empty() {
		return out
	}
	img := blurred.Read(area)
	for i, v := range img.Pix {
		img.Pix[i] = steep(v)
	}
	out.Write(img)
	return out
}

// borderMask is the band between m grown and m shrunk.
func borderMask(m *Mask, width int) *Mask {
	outer, inner := m, m
	if grow := (width + 1) / 2; grow > 0 {
		outer = expandMask(m, grow)
	}
	if shrink := width / 2; shrink > 0 {
		inner = contractMask(m, shrink)
	}
	return combine(outer, inner, func(a, b uint32) uint32 { return mul8(a, 255-b) })
}

// dilate is the largest value within radius of each pixel, past a pixel's edge by fractions of one so a curve keeps
// a smooth outline. A selection is the sum of its slices at each value, so each slice is grown on its own and added
// up; an edge no wider than anti-aliasing leaves is one slice.
func dilate(src *image.Alpha, radius int) *image.Alpha {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	levels := 1
	if softEdge(src) {
		levels = softLevels
	}
	sum := make([]float32, w*h)
	var mu sync.Mutex
	var wg sync.WaitGroup
	next := make(chan int)
	for range min(levels, runtime.GOMAXPROCS(0)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dist := make([]float32, w*h)
			part := make([]float32, w*h)
			line := newDistanceLine(max(w, h))
			for k := range next {
				cut := uint8((float64(k) + 0.5) * 255 / float64(levels))
				for i := range h {
					row := src.Pix[i*src.Stride:][:w]
					for j, v := range row {
						if v >= cut {
							dist[i*w+j] = 0
						} else {
							dist[i*w+j] = farAway
						}
					}
				}
				line.transform(dist, w, h)
				for i, d := range dist {
					part[i] = min(max(float32(radius)+1-float32(math.Sqrt(float64(d))), 0), 1)
				}
				mu.Lock()
				for i, v := range part {
					sum[i] += v
				}
				mu.Unlock()
			}
		}()
	}
	for k := range levels {
		next <- k
	}
	close(next)
	wg.Wait()
	out := image.NewAlpha(src.Rect)
	for i, v := range sum {
		out.Pix[i] = uint8(min(v*255/float32(levels)+0.5, 255))
	}
	return out
}

// softEdge reports whether an edge is wider than anti-aliasing leaves one: a partly selected pixel with only partly
// selected pixels around it.
func softEdge(a *image.Alpha) bool {
	w, h := a.Rect.Dx(), a.Rect.Dy()
	partial := func(i int) bool { return a.Pix[i] != 0 && a.Pix[i] != 255 }
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			i := y*a.Stride + x
			if partial(i) && partial(i-1) && partial(i+1) && partial(i-a.Stride) && partial(i+a.Stride) &&
				partial(i-a.Stride-1) && partial(i-a.Stride+1) && partial(i+a.Stride-1) && partial(i+a.Stride+1) {
				return true
			}
		}
	}
	return false
}

// farAway stands for a pixel not reached yet, further than any picture is wide squared.
const farAway = 1e12

// distanceLine is the working space of the Euclidean distance transform of Felzenszwalb and Huttenlocher, which
// costs the same at any radius.
type distanceLine struct {
	v    []int
	z, d []float32
}

func newDistanceLine(n int) *distanceLine {
	return &distanceLine{v: make([]int, n), z: make([]float32, n+1), d: make([]float32, n)}
}

// transform turns dist, 0 at the pixels to measure from, into each pixel's squared distance to the nearest of them.
func (l *distanceLine) transform(dist []float32, w, h int) {
	for x := range w {
		l.pass(dist[x:], w, h)
	}
	for y := range h {
		l.pass(dist[y*w:], 1, w)
	}
}

// pass is the transform along n values stride apart: the lower envelope of the parabolas rooted at each.
func (l *distanceLine) pass(f []float32, stride, n int) {
	k := -1
	for q := range n {
		fq := f[q*stride]
		if fq >= farAway {
			continue
		}
		var s float32
		for k >= 0 {
			p := l.v[k]
			s = (fq + float32(q*q) - f[p*stride] - float32(p*p)) / float32(2*(q-p))
			if s > l.z[k] {
				break
			}
			k--
		}
		k++
		l.v[k] = q
		if k == 0 {
			l.z[0] = float32(math.Inf(-1))
		} else {
			l.z[k] = s
		}
		l.z[k+1] = float32(math.Inf(1))
	}
	if k < 0 {
		return // nothing to measure from on this line
	}
	k = 0
	for q := range n {
		for l.z[k+1] < float32(q) {
			k++
		}
		p := l.v[k]
		l.d[q] = float32((q-p)*(q-p)) + f[p*stride]
	}
	for q := range n {
		f[q*stride] = l.d[q]
	}
}
