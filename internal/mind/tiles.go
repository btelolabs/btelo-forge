package mind

import (
	"image"
	"math"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// The view is drawn from tiles: squares of TileSize pixels at one zoom, cut from the board at whole multiples of
// their size from its origin, so moving the view only moves them and a tile is drawn once until what it shows
// changes. Missing tiles are drawn on every core at once, the nearest the middle of the view first, for as long as a
// frame's budget allows; where one is still missing, tiles of another zoom are stretched over its place until it
// is ready, and once the view is whole the tiles around it are drawn ahead, for moving into. The map is only read
// meanwhile: Draw and Prefetch are called on the goroutine that changes it, and wait for the tiles they draw.

// TileSize is a tile's side in pixels.
const TileSize = 256

type tileKey struct {
	zoom uint64
	x, y int32
}

type tile struct {
	img  *image.RGBA
	used uint64 // the frame it was last shown in
}

// Tiles is the tile cache of one view of a map.
type Tiles struct {
	cache map[tileKey]*tile
	frame uint64
	zooms []uint64 // the zooms tiles were drawn at, the latest last
	limit int      // how many tiles are kept
}

// NewTiles is an empty cache keeping at most limit tiles.
func NewTiles(limit int) *Tiles {
	return &Tiles{cache: map[tileKey]*tile{}, limit: max(limit, 16)}
}

// SetLimit changes how many tiles are kept, as the view's size changes.
func (t *Tiles) SetLimit(limit int) { t.limit = max(limit, 16) }

// Len is how many tiles are kept.
func (t *Tiles) Len() int { return len(t.cache) }

// Clear drops every tile.
func (t *Tiles) Clear() { clear(t.cache) }

// tileBoard is the part of the board a tile shows.
func tileBoard(k tileKey) Rect {
	z := math.Float64frombits(k.zoom)
	return Rect{float64(k.x) * TileSize / z, float64(k.y) * TileSize / z, float64(k.x+1) * TileSize / z, float64(k.y+1) * TileSize / z}
}

// Invalidate drops the tiles showing any of the regions of the board, or all of them.
func (t *Tiles) Invalidate(regions []Rect, all bool) {
	if all {
		t.Clear()
		return
	}
	if len(regions) == 0 {
		return
	}
	for k := range t.cache {
		b := tileBoard(k)
		for _, r := range regions {
			if r.Overlaps(b) {
				delete(t.cache, k)
				break
			}
		}
	}
}

// keysIn are the tiles covering the pixels area of a view, nearest its middle first.
func keysIn(v View, area image.Rectangle) []tileKey {
	zb := math.Float64bits(v.Zoom)
	x0 := int32(math.Floor((float64(area.Min.X) - v.OX) / TileSize))
	x1 := int32(math.Floor((float64(area.Max.X-1) - v.OX) / TileSize))
	y0 := int32(math.Floor((float64(area.Min.Y) - v.OY) / TileSize))
	y1 := int32(math.Floor((float64(area.Max.Y-1) - v.OY) / TileSize))
	var out []tileKey
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			out = append(out, tileKey{zb, x, y})
		}
	}
	cx, cy := float64(x0+x1)/2, float64(y0+y1)/2
	slices.SortFunc(out, func(a, b tileKey) int {
		da := math.Hypot(float64(a.x)-cx, float64(a.y)-cy)
		db := math.Hypot(float64(b.x)-cx, float64(b.y)-cy)
		switch {
		case da < db:
			return -1
		case da > db:
			return 1
		}
		return 0
	})
	return out
}

// renderTiles draws the tiles keys on every core until they are done or the deadline passes, and keeps those drawn.
func (t *Tiles) renderTiles(m *Map, zoom float64, o *Options, keys []tileKey, deadline time.Time) {
	if len(keys) == 0 {
		return
	}
	out := make([]*image.RGBA, len(keys))
	var next atomic.Int64
	var wg sync.WaitGroup
	workers := min(runtime.NumCPU(), len(keys))
	opts := *o
	opts.Paper = true
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= len(keys) || i > 0 && time.Now().After(deadline) {
					return
				}
				k := keys[i]
				c := gfx.NewCanvas(TileSize, TileSize)
				Render(c, m, View{Zoom: zoom, OX: -float64(k.x) * TileSize, OY: -float64(k.y) * TileSize}, &opts)
				out[i] = c.Img
			}
		}()
	}
	wg.Wait()
	for i, img := range out {
		if img != nil {
			t.cache[keys[i]] = &tile{img: img, used: t.frame}
		}
	}
}

// Draw shows the part of the map in the pixels area of c through v, whose offsets must be whole pixels; it draws
// missing tiles until budget is spent and reports whether the view is whole. The map must be laid out.
func (t *Tiles) Draw(c *gfx.Canvas, area image.Rectangle, m *Map, v View, o *Options, budget time.Duration) bool {
	t.frame++
	deadline := time.Now().Add(budget)
	zb := math.Float64bits(v.Zoom)
	if len(t.zooms) == 0 || t.zooms[len(t.zooms)-1] != zb {
		t.zooms = append(slices.DeleteFunc(t.zooms, func(z uint64) bool { return z == zb }), zb)
		if len(t.zooms) > 4 {
			t.zooms = t.zooms[len(t.zooms)-4:]
		}
	}
	keys := keysIn(v, area)
	var missing []tileKey
	for _, k := range keys {
		if _, ok := t.cache[k]; !ok {
			missing = append(missing, k)
		}
	}
	t.renderTiles(m, v.Zoom, o, missing, deadline)
	c.Push(gfx.Rect{X: float32(area.Min.X), Y: float32(area.Min.Y), W: float32(area.Dx()), H: float32(area.Dy())})
	defer c.Pop()
	whole := true
	for _, k := range keys {
		x, y := int(float64(k.x)*TileSize+v.OX), int(float64(k.y)*TileSize+v.OY)
		if tl, ok := t.cache[k]; ok {
			tl.used = t.frame
			c.Blit(tl.img, x, y, true)
			continue
		}
		whole = false
		t.stand(c, k, v, x, y)
	}
	t.evict()
	return whole
}

// stand fills the place of a missing tile with tiles of other zooms stretched to this one, or the board's colour.
func (t *Tiles) stand(c *gfx.Canvas, k tileKey, v View, x, y int) {
	place := gfx.Rect{X: float32(x), Y: float32(y), W: TileSize, H: TileSize}
	c.Fill(place, Paper)
	b := tileBoard(k)
	c.Push(place)
	defer c.Pop()
	for i := len(t.zooms) - 1; i >= 0; i-- {
		zb := t.zooms[i]
		if zb == k.zoom {
			continue
		}
		z := math.Float64frombits(zb)
		// The other zoom's tiles over the same part of the board, at most a few: past that the stretch is a blur.
		x0, x1 := int32(math.Floor(b.X0*z/TileSize)), int32(math.Floor(b.X1*z/TileSize))
		y0, y1 := int32(math.Floor(b.Y0*z/TileSize)), int32(math.Floor(b.Y1*z/TileSize))
		if (x1-x0+1)*(y1-y0+1) > 16 {
			continue
		}
		for ty := y0; ty <= y1; ty++ {
			for tx := x0; tx <= x1; tx++ {
				tl, ok := t.cache[tileKey{zb, tx, ty}]
				if !ok {
					continue
				}
				r := v.ScreenRect(tileBoard(tileKey{zb, tx, ty}))
				c.Image(tl.img, r)
			}
		}
	}
}

// Prefetch draws, until budget is spent, the missing tiles within margin tiles around the pixels area, so the view
// can move into them; it reports whether none is left to draw.
func (t *Tiles) Prefetch(area image.Rectangle, m *Map, v View, o *Options, margin int, budget time.Duration) bool {
	grown := area.Inset(-margin * TileSize)
	var missing []tileKey
	for _, k := range keysIn(v, grown) {
		if _, ok := t.cache[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) == 0 {
		return true
	}
	// Room is left for the view itself: ahead of it only what the cache can keep besides.
	if room := t.limit - len(keysIn(v, area)) - 8; len(missing) > room {
		missing = missing[:max(room, 0)]
		if len(missing) == 0 {
			return true
		}
	}
	t.renderTiles(m, v.Zoom, o, missing, time.Now().Add(budget))
	for _, k := range missing {
		if tl, ok := t.cache[k]; ok {
			tl.used = t.frame
		}
	}
	for _, k := range missing {
		if _, ok := t.cache[k]; !ok {
			return false
		}
	}
	return true
}

// evict drops the tiles shown longest ago while there are more than the limit.
func (t *Tiles) evict() {
	if len(t.cache) <= t.limit {
		return
	}
	keys := make([]tileKey, 0, len(t.cache))
	for k := range t.cache {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b tileKey) int {
		ua, ub := t.cache[a].used, t.cache[b].used
		switch {
		case ua < ub:
			return -1
		case ua > ub:
			return 1
		}
		return 0
	})
	for _, k := range keys[:len(keys)-t.limit] {
		if t.cache[k].used == t.frame {
			break // never what is shown now
		}
		delete(t.cache, k)
	}
}
