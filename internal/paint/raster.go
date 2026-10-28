package paint

import (
	"image"
	"image/color"
	"math"
	"slices"
)

// Tiles are 256×256 pixels; a tile's key is its position in tiles.
const (
	tileShift = 8
	TileSize  = 1 << tileShift
	tileMask  = TileSize - 1
)

// tile is one tile's pixels, bpp bytes each, row after row. A frozen tile is shared with a snapshot and is never
// written again: writing clones it first.
type tile struct {
	pix    []uint8
	frozen bool
}

// plane is a sparse, unbounded grid of tiles: a raster's colour (4 bytes a pixel) or a mask's coverage (1 byte).
// Where there is no tile every byte of every pixel is fill. Positions on the canvas are the plane's own plus off, so
// moving a whole plane is free.
type plane struct {
	bpp   int
	fill  uint8
	off   image.Point
	tiles map[image.Point]*tile
	mips  []map[image.Point]*tile // halved levels, made on demand; a write drops the tiles above what it changed
}

func newPlane(bpp int, fill uint8) plane {
	return plane{bpp: bpp, fill: fill, tiles: map[image.Point]*tile{}}
}

// tileKey is the tile holding plane position p.
func tileKey(p image.Point) image.Point { return image.Pt(p.X>>tileShift, p.Y>>tileShift) }

// tileRect is the plane area of the tile with key k.
func tileRect(k image.Point) image.Rectangle {
	return image.Rect(k.X<<tileShift, k.Y<<tileShift, (k.X+1)<<tileShift, (k.Y+1)<<tileShift)
}

// keysOver are the keys of the tiles a plane area touches.
func keysOver(r image.Rectangle) (k0, k1 image.Point) {
	return image.Pt(r.Min.X>>tileShift, r.Min.Y>>tileShift), image.Pt((r.Max.X-1)>>tileShift, (r.Max.Y-1)>>tileShift)
}

func (p *plane) newTile() *tile {
	t := &tile{pix: make([]uint8, TileSize*TileSize*p.bpp)}
	if p.fill != 0 {
		for i := range t.pix {
			t.pix[i] = p.fill
		}
	}
	return t
}

// writable is the tile at k ready to be written: made when missing, cloned when frozen.
func (p *plane) writable(k image.Point) *tile {
	t := p.tiles[k]
	switch {
	case t == nil:
		t = p.newTile()
		p.tiles[k] = t
	case t.frozen:
		t = &tile{pix: slices.Clone(t.pix)}
		p.tiles[k] = t
	}
	p.invalidate(k)
	return t
}

// invalidate drops the level tiles above the tile at k, to be made again from the new pixels when asked for.
func (p *plane) invalidate(k image.Point) {
	for _, level := range p.mips {
		k = image.Pt(k.X>>1, k.Y>>1)
		delete(level, k)
	}
}

// drop removes the tile at k, which holds only the fill.
func (p *plane) drop(k image.Point) {
	delete(p.tiles, k)
	p.invalidate(k)
}

// snapshot copies the tile map and freezes every tile, so the copy and the original share them until either writes.
func (p *plane) snapshot() plane {
	c := *p
	c.tiles = make(map[image.Point]*tile, len(p.tiles))
	for k, t := range p.tiles {
		t.frozen = true
		c.tiles[k] = t
	}
	c.mips = nil
	return c
}

// same reports whether two planes hold the same pixels by sharing every tile, as a plane and an unchanged snapshot
// of it do.
func (p *plane) same(o *plane) bool {
	if p.off != o.off || p.fill != o.fill || len(p.tiles) != len(o.tiles) {
		return false
	}
	for k, t := range p.tiles {
		if o.tiles[k] != t {
			return false
		}
	}
	return true
}

// tileBounds is the canvas area the tiles cover: an upper bound of where the plane differs from its fill.
func (p *plane) tileBounds() image.Rectangle {
	var b image.Rectangle
	for k := range p.tiles {
		b = b.Union(tileRect(k))
	}
	return b.Add(p.off)
}

// read copies the canvas area r into dst's pixels (stride bytes a row, starting at r.Min), fill where no tile is.
func (p *plane) read(dst []uint8, stride int, r image.Rectangle) {
	local := r.Sub(p.off)
	bpp := p.bpp
	k0, k1 := keysOver(local)
	if local.Empty() {
		return
	}
	// Fill first only where tiles are missing: most areas have them or are wholly empty.
	for ky := k0.Y; ky <= k1.Y; ky++ {
		for kx := k0.X; kx <= k1.X; kx++ {
			k := image.Pt(kx, ky)
			part := tileRect(k).Intersect(local)
			t := p.tiles[k]
			for y := part.Min.Y; y < part.Max.Y; y++ {
				row := dst[(y-local.Min.Y)*stride+(part.Min.X-local.Min.X)*bpp:][:part.Dx()*bpp]
				if t == nil {
					if p.fill == 0 {
						clear(row)
					} else {
						for i := range row {
							row[i] = p.fill
						}
					}
					continue
				}
				src := ((y&tileMask)*TileSize + (part.Min.X & tileMask)) * bpp
				copy(row, t.pix[src:src+len(row)])
			}
		}
	}
}

// write copies src's pixels (stride bytes a row) into the canvas area r. Tiles left wholly at the fill are
// dropped, so the plane stays sparse.
func (p *plane) write(src []uint8, stride int, r image.Rectangle) {
	local := r.Sub(p.off)
	if local.Empty() {
		return
	}
	bpp := p.bpp
	k0, k1 := keysOver(local)
	for ky := k0.Y; ky <= k1.Y; ky++ {
		for kx := k0.X; kx <= k1.X; kx++ {
			k := image.Pt(kx, ky)
			part := tileRect(k).Intersect(local)
			if p.tiles[k] == nil && allFill(src, stride, part.Sub(local.Min), bpp, p.fill) {
				continue // nothing to store
			}
			t := p.writable(k)
			for y := part.Min.Y; y < part.Max.Y; y++ {
				row := src[(y-local.Min.Y)*stride+(part.Min.X-local.Min.X)*bpp:][:part.Dx()*bpp]
				dst := ((y&tileMask)*TileSize + (part.Min.X & tileMask)) * bpp
				copy(t.pix[dst:], row)
			}
			if part == tileRect(k) && allFill(t.pix, TileSize*bpp, image.Rect(0, 0, TileSize, TileSize), bpp, p.fill) {
				p.drop(k)
			}
		}
	}
}

// allFill reports whether the area r of pixels (stride bytes a row) is all fill.
func allFill(pix []uint8, stride int, r image.Rectangle, bpp int, fill uint8) bool {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := pix[y*stride+r.Min.X*bpp:][:r.Dx()*bpp]
		for _, v := range row {
			if v != fill {
				return false
			}
		}
	}
	return true
}

// compactKeys drops those of the tiles at keys k0 to k1 that hold only the fill, after writes in place.
func (p *plane) compactKeys(k0, k1 image.Point) {
	for ky := k0.Y; ky <= k1.Y; ky++ {
		for kx := k0.X; kx <= k1.X; kx++ {
			k := image.Pt(kx, ky)
			if t := p.tiles[k]; t != nil && allFill(t.pix, TileSize*p.bpp, image.Rect(0, 0, TileSize, TileSize), p.bpp, p.fill) {
				p.drop(k)
			}
		}
	}
}

// tilesOver calls fn with each tile the canvas area a touches, made writable, the canvas area of the tile inside a,
// and the tile's corner on the canvas; tiles left holding only the fill are dropped afterwards.
func (p *plane) tilesOver(a image.Rectangle, fn func(pix []uint8, part image.Rectangle, corner image.Point)) {
	local := a.Sub(p.off)
	if local.Empty() {
		return
	}
	k0, k1 := keysOver(local)
	for ky := k0.Y; ky <= k1.Y; ky++ {
		for kx := k0.X; kx <= k1.X; kx++ {
			k := image.Pt(kx, ky)
			t := p.writable(k)
			fn(t.pix, tileRect(k).Intersect(local).Add(p.off), tileRect(k).Min.Add(p.off))
		}
	}
	p.compactKeys(k0, k1)
}

// level is the plane halved l times (0 is the plane itself), in tiles of its own: a tile of level l covers 2^l
// tiles of level 0 each way. Level tiles are made when first asked for and kept until the tiles under them change.
func (p *plane) level(l int) map[image.Point]*tile {
	for len(p.mips) < l {
		p.mips = append(p.mips, map[image.Point]*tile{})
	}
	below := p.tiles
	for i := range l {
		up := p.mips[i]
		for k := range below {
			pk := image.Pt(k.X>>1, k.Y>>1)
			if up[pk] == nil {
				up[pk] = p.halve(below, pk)
			}
		}
		below = up
	}
	return below
}

// halve makes the tile at key k of the level above below: each pixel the mean of four below it.
func (p *plane) halve(below map[image.Point]*tile, k image.Point) *tile {
	out := p.newTile()
	bpp := p.bpp
	for qy := range 2 {
		for qx := range 2 {
			src := below[image.Pt(k.X*2+qx, k.Y*2+qy)]
			if src == nil {
				continue // already the fill
			}
			for y := range TileSize / 2 {
				for x := range TileSize / 2 {
					d := ((qy*TileSize/2+y)*TileSize + qx*TileSize/2 + x) * bpp
					s := ((2*y)*TileSize + 2*x) * bpp
					for c := range bpp {
						sum := uint32(src.pix[s+c]) + uint32(src.pix[s+bpp+c]) + uint32(src.pix[s+TileSize*bpp+c]) + uint32(src.pix[s+TileSize*bpp+bpp+c])
						out.pix[d+c] = uint8((sum + 2) / 4)
					}
				}
			}
		}
	}
	return out
}

// readLevel copies the area r of level l (in that level's pixels: the canvas halved l times) into dst. A plane
// moved by an amount that is not a multiple of 2^l is read from the nearest whole position, less than one level
// pixel away, which is what drawing at that level can show anyway.
func (p *plane) readLevel(l int, dst []uint8, stride int, r image.Rectangle) {
	if l == 0 {
		p.read(dst, stride, r)
		return
	}
	tiles := p.level(l)
	off := image.Pt(p.off.X>>l, p.off.Y>>l)
	tmp := plane{bpp: p.bpp, fill: p.fill, off: off, tiles: tiles}
	tmp.read(dst, stride, r)
}

// ---- Raster: a pixel layer's colour.

// Raster is premultiplied RGBA on an unbounded canvas, transparent where nothing was painted.
type Raster struct{ p plane }

// NewRaster is an empty raster.
func NewRaster() *Raster { return &Raster{p: newPlane(4, 0)} }

// RasterFrom copies an image onto a new raster, at the image's own position.
func RasterFrom(img *image.RGBA) *Raster {
	r := NewRaster()
	r.Write(img)
	return r
}

// Snapshot is a copy that shares the tiles until either is written.
func (r *Raster) Snapshot() *Raster { return &Raster{p: r.p.snapshot()} }

// Same reports whether two rasters hold the same tiles, as a raster and an unchanged snapshot of it do.
func (r *Raster) Same(o *Raster) bool { return r == o || r != nil && o != nil && r.p.same(&o.p) }

// Read copies the area a of the canvas.
func (r *Raster) Read(a image.Rectangle) *image.RGBA {
	img := image.NewRGBA(a)
	r.p.read(img.Pix, img.Stride, a)
	return img
}

// ReadLevel copies the area a of level l, in that level's coordinates (the canvas halved l times).
func (r *Raster) ReadLevel(l int, a image.Rectangle) *image.RGBA {
	img := image.NewRGBA(a)
	r.p.readLevel(l, img.Pix, img.Stride, a)
	return img
}

// Write copies img onto the canvas at img's position.
func (r *Raster) Write(img *image.RGBA) { r.p.write(img.Pix, img.Stride, img.Rect) }

// At is the colour at (x, y).
func (r *Raster) At(x, y int) color.RGBA {
	q := image.Pt(x, y).Sub(r.p.off)
	t := r.p.tiles[tileKey(q)]
	if t == nil {
		return color.RGBA{}
	}
	i := ((q.Y&tileMask)*TileSize + q.X&tileMask) * 4
	return color.RGBA{t.pix[i], t.pix[i+1], t.pix[i+2], t.pix[i+3]}
}

// Move shifts the whole raster by (dx, dy); it costs nothing.
func (r *Raster) Move(dx, dy int) { r.p.off = r.p.off.Add(image.Pt(dx, dy)) }

// Offset is how far the raster has been moved.
func (r *Raster) Offset() image.Point { return r.p.off }

// Empty reports whether nothing was painted.
func (r *Raster) Empty() bool { return len(r.p.tiles) == 0 }

// TileBounds is the area the raster's tiles cover, quick to know and never smaller than its content.
func (r *Raster) TileBounds() image.Rectangle { return r.p.tileBounds() }

// Bounds is the smallest rectangle holding every pixel that is not fully transparent.
func (r *Raster) Bounds() image.Rectangle {
	var b image.Rectangle
	for k, t := range r.p.tiles {
		tb := opaqueIn(t.pix, 4, 3)
		if !tb.Empty() {
			b = b.Union(tb.Add(tileRect(k).Min))
		}
	}
	return b.Add(r.p.off)
}

// opaqueIn is where in a tile the byte at offset c of each pixel is not zero.
func opaqueIn(pix []uint8, bpp, c int) image.Rectangle {
	minX, minY, maxX, maxY := TileSize, TileSize, -1, -1
	for y := range TileSize {
		row := pix[y*TileSize*bpp:][:TileSize*bpp]
		first, last := -1, -1
		for x := range TileSize {
			if row[x*bpp+c] != 0 {
				if first < 0 {
					first = x
				}
				last = x
			}
		}
		if first >= 0 {
			minX, maxX = min(minX, first), max(maxX, last)
			minY, maxY = min(minY, y), y
		}
	}
	if maxX < 0 {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX+1, maxY+1)
}

// Tiles calls fn with each tile the area a touches, made writable (created or cloned as needed), and the canvas
// area of the tile inside a; the tile's pixels are indexed from the tile's corner. Tiles left empty are dropped
// afterwards.
func (r *Raster) Tiles(a image.Rectangle, fn func(pix []uint8, part image.Rectangle, corner image.Point)) {
	r.p.tilesOver(a, fn)
}

// ---- Mask: a layer mask's or the selection's coverage.

// Mask is 8-bit coverage on an unbounded canvas; beyond what was written it is Fill.
type Mask struct{ p plane }

// NewMask is a mask that is fill everywhere.
func NewMask(fill uint8) *Mask { return &Mask{p: newPlane(1, fill)} }

// MaskFrom copies an alpha image onto a new mask that is fill beyond it.
func MaskFrom(img *image.Alpha, fill uint8) *Mask {
	m := NewMask(fill)
	m.Write(img)
	return m
}

// Fill is the mask's value where nothing was written.
func (m *Mask) Fill() uint8 { return m.p.fill }

func (m *Mask) Snapshot() *Mask        { return &Mask{p: m.p.snapshot()} }
func (m *Mask) Same(o *Mask) bool      { return m == o || m != nil && o != nil && m.p.same(&o.p) }
func (m *Mask) Write(img *image.Alpha) { m.p.write(img.Pix, img.Stride, img.Rect) }
func (m *Mask) Move(dx, dy int)        { m.p.off = m.p.off.Add(image.Pt(dx, dy)) }
func (m *Mask) Offset() image.Point    { return m.p.off }

// Read copies the area a.
func (m *Mask) Read(a image.Rectangle) *image.Alpha {
	img := image.NewAlpha(a)
	m.p.read(img.Pix, img.Stride, a)
	return img
}

// ReadLevel copies the area a of level l, in that level's coordinates.
func (m *Mask) ReadLevel(l int, a image.Rectangle) *image.Alpha {
	img := image.NewAlpha(a)
	m.p.readLevel(l, img.Pix, img.Stride, a)
	return img
}

// At is the value at (x, y).
func (m *Mask) At(x, y int) uint8 {
	q := image.Pt(x, y).Sub(m.p.off)
	t := m.p.tiles[tileKey(q)]
	if t == nil {
		return m.p.fill
	}
	return t.pix[(q.Y&tileMask)*TileSize+q.X&tileMask]
}

// TileBounds is the area the mask's tiles cover: beyond it the mask is Fill.
func (m *Mask) TileBounds() image.Rectangle { return m.p.tileBounds() }

// Bounds is the smallest rectangle beyond which the mask is Fill; for a mask whose fill is 0, where it is not 0.
func (m *Mask) Bounds() image.Rectangle {
	var b image.Rectangle
	for k, t := range m.p.tiles {
		tb := differsIn(t.pix, m.p.fill)
		if !tb.Empty() {
			b = b.Union(tb.Add(tileRect(k).Min))
		}
	}
	return b.Add(m.p.off)
}

// differsIn is where in an 8-bit tile the value is not fill.
func differsIn(pix []uint8, fill uint8) image.Rectangle {
	if fill == 0 {
		return opaqueIn(pix, 1, 0)
	}
	inv := make([]uint8, len(pix))
	for i, v := range pix {
		inv[i] = v ^ fill
	}
	return opaqueIn(inv, 1, 0)
}

// Invert turns every value v into 255−v, everywhere: the fill too.
func (m *Mask) Invert() {
	for k := range m.p.tiles {
		t := m.p.writable(k)
		for i, v := range t.pix {
			t.pix[i] = 255 - v
		}
	}
	m.p.fill = 255 - m.p.fill
	m.p.mips = nil // the levels' empty places changed too
}

// Tiles calls fn with each tile the area a touches, made writable, as Raster.Tiles does.
func (m *Mask) Tiles(a image.Rectangle, fn func(pix []uint8, part image.Rectangle, corner image.Point)) {
	m.p.tilesOver(a, fn)
}

// LevelFor is the detail level to draw at a zoom: the largest halving that still has at least one pixel for every
// pixel shown.
func LevelFor(zoom float64) int {
	if zoom >= 1 || zoom <= 0 {
		return 0
	}
	return min(int(math.Floor(math.Log2(1/zoom))), 12)
}
