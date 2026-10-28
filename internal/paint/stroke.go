package paint

import (
	"errors"
	"image"
	"image/color"
	"math"
)

// ErrHidden means the chosen layer is hidden, so painting on it would not be seen.
var ErrHidden = errors.New("the chosen layer is hidden")

// Stroke is a brush or eraser stroke being painted. Each dab adds the brush's flow to the stroke's coverage,
// building up where dabs overlap, and the stroke is laid onto the layer's pixels from before it at the brush's
// opacity, so a stroke never goes beyond its opacity however often it crosses itself, as in Photoshop. On a layer
// mask being edited it paints the brush colour's brightness into the mask.
//
// The canvas has no bounds, so the coverage is kept in tiles made where the brush goes, and the pixels from before
// are a snapshot of the layer, which shares its tiles until the stroke changes them.
type Stroke struct {
	doc        *Doc
	layer      *Node
	toMask     bool
	brush      Brush
	erase      bool
	beforePix  *Raster // the layer's pixels when the stroke began
	beforeMask *Mask   // or its mask
	cover      map[image.Point][]uint16
	lastKey    image.Point // the coverage tile last looked up, which the next pixel is almost always in
	lastTile   []uint16
	clip       image.Rectangle
	dirty      image.Rectangle
	x, y       float64 // where the last dab went
	tx, ty     float64 // where the pointer is; with smoothing the brush trails it
	carry      float64 // distance travelled since the last dab
	mark       Mark
	leash      float64 // the smoothing's string length: the brush moves once the pointer is this far from it
	clone      *cloneSource
	retouch    *Retouch  // a retouching brush's settings, or nil for painting
	carried    []float32 // the colour smudge carries from dab to dab
	dir        float64   // the stroke's direction in degrees, which a tip that follows it turns with
	rnd        uint64    // the shape dynamics' random state
}

// cloneSource is where the clone stamp paints from, as it was when the stroke began, and how far from the brush it
// is sampled.
type cloneSource struct {
	layer  *Raster // the chosen layer; or, when nil, the picture as it looks:
	root   *Node   // a snapshot of the layers
	dx, dy int
}

// read is the source's pixels for the canvas area r, before the offset.
func (c *cloneSource) read(r image.Rectangle) *image.RGBA {
	src := r.Add(image.Pt(c.dx, c.dy))
	var img *image.RGBA
	if c.layer != nil {
		img = c.layer.Read(src)
	} else {
		img = image.NewRGBA(src)
		(&Doc{}).compositeInto(img, c.root, src, 0)
	}
	img.Rect = r // the same pixels, placed where they are painted
	return img
}

// everywhere stands for no clip on the unbounded canvas.
var everywhere = image.Rect(-1<<30, -1<<30, 1<<30, 1<<30)

// BeginStroke starts painting (or erasing) at (x, y) in canvas pixels on the chosen layer, within the selection
// when there is one; it lays the first dab, so a click makes a dot.
func (d *Doc) BeginStroke(b Brush, erase bool, x, y float64) (*Stroke, image.Rectangle, error) {
	return d.beginStroke(b, erase, nil, x, y)
}

// BeginClone starts a clone stamp stroke at (x, y): it paints, with the brush's shape, what is (dx, dy) away,
// taken from the chosen layer or, with allLayers, from the picture as it looks, as the stroke begins.
func (d *Doc) BeginClone(b Brush, dx, dy int, allLayers bool, x, y float64) (*Stroke, image.Rectangle, error) {
	l, err := d.pixelLayer()
	if err != nil {
		return nil, image.Rectangle{}, err
	}
	src := &cloneSource{dx: dx, dy: dy}
	if l.Pixels == nil && !allLayers {
		return nil, image.Rectangle{}, ErrAdjustmentLayer // a mask of a layer without pixels: nothing to clone from
	}
	if allLayers {
		src.root = snapshotTree(d.Root)
	} else {
		src.layer = l.Pixels.Snapshot()
	}
	return d.beginStroke(b, false, src, x, y)
}

func (d *Doc) beginStroke(b Brush, erase bool, clone *cloneSource, x, y float64) (*Stroke, image.Rectangle, error) {
	return d.beginStrokeOf(b, erase, clone, nil, x, y)
}

// beginStrokeWith starts a retouching stroke.
func (d *Doc) beginStrokeWith(b Brush, rt *Retouch, x, y float64) (*Stroke, image.Rectangle, error) {
	return d.beginStrokeOf(b, false, nil, rt, x, y)
}

func (d *Doc) beginStrokeOf(b Brush, erase bool, clone *cloneSource, rt *Retouch, x, y float64) (*Stroke, image.Rectangle, error) {
	if d.stroke != nil {
		d.stroke.End()
	}
	l, err := d.pixelLayer()
	if err != nil {
		return nil, image.Rectangle{}, err
	}
	if !l.Visible {
		return nil, image.Rectangle{}, ErrHidden
	}
	clip := everywhere
	if d.Selection != nil && d.Selection.Fill() == 0 {
		clip = d.Selection.Bounds()
	}
	b = b.normalized()
	s := &Stroke{doc: d, layer: l, brush: b, erase: erase, clip: clip, x: x, y: y, tx: x, ty: y, mark: d.Mark(),
		leash: b.Smooth * (b.Size + 40), clone: clone, retouch: rt, cover: map[image.Point][]uint16{},
		rnd: math.Float64bits(x)*31 ^ math.Float64bits(y)}
	if s.toMask = d.EditMask && l.Mask != nil; s.toMask {
		s.beforeMask = l.Mask.Snapshot()
	} else {
		d.dropText(l)
		s.beforePix = l.Pixels.Snapshot()
	}
	d.stroke = s
	return s, s.laid(s.dab(x, y)), nil
}

// laid shows the dabs laid within r: the pixels there are worked out once from what they were and the coverage,
// however many dabs crossed them. It returns r.
func (s *Stroke) laid(r image.Rectangle) image.Rectangle {
	if !r.Empty() && !(s.retouch != nil && s.retouch.Kind == RetouchSmudge) {
		s.compose(r)
	}
	return r
}

// To continues the stroke toward (x, y) and returns the area it changed. With smoothing the brush follows the
// pointer on a string, so jitter shorter than the string never reaches the picture.
func (s *Stroke) To(x, y float64) image.Rectangle {
	s.tx, s.ty = x, y
	dist := math.Hypot(x-s.x, y-s.y)
	if dist <= s.leash {
		return image.Rectangle{}
	}
	f := (dist - s.leash) / dist
	return s.lineTo(s.x+(x-s.x)*f, s.y+(y-s.y)*f)
}

// lineTo lays dabs every spacing along the way to (x, y), starting where the previous segment left off.
func (s *Stroke) lineTo(x, y float64) image.Rectangle {
	step := max(s.brush.Size*s.brush.Spacing, 0.5)
	dist := math.Hypot(x-s.x, y-s.y)
	var changed image.Rectangle
	if dist == 0 {
		return changed
	}
	s.dir = math.Atan2(y-s.y, x-s.x) * 180 / math.Pi
	t := step - s.carry
	for ; t <= dist; t += step {
		f := t / dist
		changed = changed.Union(s.dab(s.x+(x-s.x)*f, s.y+(y-s.y)*f))
	}
	s.carry = dist - (t - step)
	s.x, s.y = x, y
	return s.laid(changed)
}

// coverAt is the coverage cell for canvas pixel (x, y), its tile made when missing.
func (s *Stroke) coverAt(x, y int) *uint16 {
	return &s.coverTile(x, y)[(y&tileMask)*TileSize+x&tileMask]
}

// coverTile is the coverage tile holding canvas pixel (x, y), made when missing.
func (s *Stroke) coverTile(x, y int) []uint16 {
	k := image.Pt(x>>tileShift, y>>tileShift)
	t := s.cover[k]
	if t == nil {
		t = make([]uint16, TileSize*TileSize)
		s.cover[k] = t
		s.lastTile = nil // it may have been looked up as missing
	}
	return t
}

// coverRow is the coverage cells of row y from canvas pixel x to the end of its tile, the tile made when missing.
func (s *Stroke) coverRow(x, y int) []uint16 {
	return s.coverTile(x, y)[(y&tileMask)*TileSize+x&tileMask : (y&tileMask+1)*TileSize]
}

// coverOf is the coverage of canvas pixel (x, y), 0 to 65535.
func (s *Stroke) coverOf(x, y int) uint32 {
	k := image.Pt(x>>tileShift, y>>tileShift)
	if k != s.lastKey || s.lastTile == nil {
		s.lastKey, s.lastTile = k, s.cover[k]
		if s.lastTile == nil {
			return 0
		}
	}
	return uint32(s.lastTile[(y&tileMask)*TileSize+x&tileMask])
}

// dab lays one dab centred at (cx, cy) into the stroke's coverage and returns the pixels it touched, which laid
// then shows (a smudge's dab shows itself, as it carries colour from one dab to the next).
func (s *Stroke) dab(cx, cy float64) image.Rectangle {
	if s.retouch != nil && s.retouch.Kind == RetouchSmudge {
		box := s.smudgeDab(cx, cy)
		s.dirty = s.dirty.Union(box)
		return box
	}
	b := s.brush
	if b.dynamic() {
		b, cx, cy = s.vary(b, cx, cy)
	}
	r := b.reach()
	box := image.Rect(int(math.Floor(cx-r)), int(math.Floor(cy-r)), int(math.Ceil(cx+r)), int(math.Ceil(cy+r))).Intersect(s.clip)
	if box.Empty() {
		return box
	}
	flow := b.Flow
	shape := b.shape()
	for y := box.Min.Y; y < box.Max.Y; y++ {
		py := float64(y) + 0.5
		// A row goes tile by tile, each tile's coverage looked up once.
		for x0 := box.Min.X; x0 < box.Max.X; {
			x1 := min(box.Max.X, (x0>>tileShift+1)<<tileShift)
			row := s.coverRow(x0, y)
			for x := x0; x < x1; x++ {
				c := shape.at(float64(x)+0.5, py, cx, cy) * flow
				if c <= 0 {
					continue
				}
				cell := &row[x-x0]
				m := float64(*cell) / 65535
				*cell = uint16((m+(1-m)*c)*65535 + 0.5)
			}
			x0 = x1
		}
	}
	s.dirty = s.dirty.Union(box)
	return box
}

// vary is the brush and centre of the next dab with the brush's shape dynamics: turned with the stroke, then
// thrown aside, turned and made smaller at random.
func (s *Stroke) vary(b Brush, cx, cy float64) (Brush, float64, float64) {
	if b.Follow {
		b.Angle += s.dir
	}
	if b.Scatter > 0 {
		// Anywhere within the scatter's circle, more often near the line.
		sn, cs := math.Sincos(s.random() * 2 * math.Pi)
		d := s.random() * b.Scatter * b.Size
		cx, cy = cx+cs*d, cy+sn*d
	}
	if b.AngleJitter > 0 {
		b.Angle += (s.random()*2 - 1) * 180 * b.AngleJitter
	}
	if b.SizeJitter > 0 {
		b.Size = max(b.Size*(1-b.SizeJitter*s.random()), 1)
	}
	return b, cx, cy
}

// random is the next of the stroke's random numbers, from 0 up to 1 (splitmix64).
func (s *Stroke) random() float64 {
	s.rnd += 0x9e3779b97f4a7c15
	z := s.rnd
	z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
	z = (z ^ z>>27) * 0x94d049bb133111eb
	z ^= z >> 31
	return float64(z>>11) / (1 << 53)
}

// compose sets the layer's pixels (or mask) in r from what they were before the stroke and the stroke's coverage,
// within the selection.
func (s *Stroke) compose(r image.Rectangle) {
	d := s.doc
	opacity := uint32(s.brush.Opacity*65535 + 0.5)
	col := s.brush.Color
	sel := d.selectionIn(r)
	cover := func(x, y int) uint32 {
		// Coverage × opacity × selection, in 0..255 with rounding.
		a := (s.coverOf(x, y)*opacity/65535*255 + 32767) / 65535
		return mul8(a, coverage(sel, x, y))
	}
	var src *image.RGBA
	if s.clone != nil {
		src = s.clone.read(r)
	}
	if s.toMask {
		before := s.beforeMask.Read(r)
		v := uint32(luma(col))
		if s.erase {
			v = 255 // the eraser reveals, as painting the default background white does in Photoshop
		}
		s.layer.Mask.Tiles(r, func(pix []uint8, part image.Rectangle, corner image.Point) {
			for y := part.Min.Y; y < part.Max.Y; y++ {
				for x := part.Min.X; x < part.Max.X; x++ {
					a := cover(x, y)
					val := v
					if src != nil {
						c := src.Pix[src.PixOffset(x, y):][:4]
						if c[3] == 0 {
							continue
						}
						ca := uint32(c[3])
						val = uint32(luma(color.NRGBA{R: uint8(uint32(c[0]) * 255 / ca), G: uint8(uint32(c[1]) * 255 / ca),
							B: uint8(uint32(c[2]) * 255 / ca), A: 255}))
						a = mul8(a, ca)
					}
					pix[(y-corner.Y)*TileSize+x-corner.X] = uint8(mul8(val, a) + mul8(uint32(before.Pix[before.PixOffset(x, y)]), 255-a))
				}
			}
		})
		return
	}
	before := s.beforePix.Read(r)
	lock := s.layer.LockAlpha
	if s.retouch != nil {
		target := s.retouchTarget(before, r)
		s.layer.Pixels.Tiles(r, func(pix []uint8, part image.Rectangle, corner image.Point) {
			for y := part.Min.Y; y < part.Max.Y; y++ {
				for x := part.Min.X; x < part.Max.X; x++ {
					a := cover(x, y)
					if a == 0 {
						continue
					}
					k := 255 - a
					bi, ti := before.PixOffset(x, y), target.PixOffset(x, y)
					p := pix[((y-corner.Y)*TileSize+x-corner.X)*4:][:4]
					for c := range 4 {
						p[c] = uint8(mul8(uint32(target.Pix[ti+c]), a) + mul8(uint32(before.Pix[bi+c]), k))
					}
					if lock {
						keepAlpha(p, uint32(before.Pix[bi+3]))
					}
				}
			}
		})
		return
	}
	s.layer.Pixels.Tiles(r, func(pix []uint8, part image.Rectangle, corner image.Point) {
		for y := part.Min.Y; y < part.Max.Y; y++ {
			for x := part.Min.X; x < part.Max.X; x++ {
				a := cover(x, y)
				bi := before.PixOffset(x, y)
				b0, b1, b2, b3 := uint32(before.Pix[bi]), uint32(before.Pix[bi+1]), uint32(before.Pix[bi+2]), uint32(before.Pix[bi+3])
				p := pix[((y-corner.Y)*TileSize+x-corner.X)*4:][:4]
				k := 255 - a
				switch {
				case src != nil:
					c := src.Pix[src.PixOffset(x, y):][:4]
					k = 255 - mul8(uint32(c[3]), a)
					p[0], p[1] = uint8(mul8(uint32(c[0]), a)+mul8(b0, k)), uint8(mul8(uint32(c[1]), a)+mul8(b1, k))
					p[2], p[3] = uint8(mul8(uint32(c[2]), a)+mul8(b2, k)), uint8(mul8(uint32(c[3]), a)+mul8(b3, k))
				case s.erase:
					if lock {
						continue // locked transparency cannot be erased
					}
					p[0], p[1], p[2], p[3] = uint8(mul8(b0, k)), uint8(mul8(b1, k)), uint8(mul8(b2, k)), uint8(mul8(b3, k))
					continue
				default:
					p[0] = uint8(mul8(uint32(col.R), a) + mul8(b0, k))
					p[1] = uint8(mul8(uint32(col.G), a) + mul8(b1, k))
					p[2] = uint8(mul8(uint32(col.B), a) + mul8(b2, k))
					p[3] = uint8(a + mul8(b3, k))
				}
				if lock {
					keepAlpha(p, b3)
				}
			}
		}
	})
}

// Dirty is everything the stroke has changed so far.
func (s *Stroke) Dirty() image.Rectangle { return s.dirty }

// End finishes the stroke as one step of the history, first bringing a trailing brush to where the pointer is.
func (s *Stroke) End() image.Rectangle {
	d := s.doc
	if d.stroke != s {
		return image.Rectangle{}
	}
	caught := s.lineTo(s.tx, s.ty)
	d.stroke = nil
	if s.dirty.Empty() {
		d.restore(s.mark.s) // nothing was painted, so a text layer stays text
		return caught
	}
	name := "Brush"
	switch {
	case s.erase:
		name = "Eraser"
	case s.clone != nil:
		name = "Clone stamp"
	case s.retouch != nil:
		name = retouchNames[s.retouch.Kind]
	}
	d.Commit(name, s.mark)
	return caught
}

// Cancel takes the stroke back, as if it had not been painted.
func (s *Stroke) Cancel() image.Rectangle {
	d := s.doc
	if d.stroke != s {
		return image.Rectangle{}
	}
	d.stroke = nil
	d.restore(s.mark.s)
	return s.dirty
}
