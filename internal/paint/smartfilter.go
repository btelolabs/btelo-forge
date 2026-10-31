package paint

import (
	"errors"
	"hash/maphash"
	"image"
	"math"
	"slices"
	"sync"
)

// Smart filters, as Photoshop's: filters held by a smart object rather than laid on pixels, worked out from what its
// layers show each time they are drawn, in order, so any can be changed, turned off or taken away later. They are
// worked out tile by tile at the detail level being drawn, a radius going down with the level, and kept until
// something in the smart object changes.

// The filters a smart object can hold besides adjustments.
const (
	SmartBlur    = "gaussian_blur"      // Values: radius
	SmartUnsharp = "unsharp_mask"       // Values: amount, radius, threshold
	SmartNoise   = "add_noise"          // Values: amount; On: monochromatic
	SmartMosaic  = "mosaic"             // Values: cell size
	SmartTones   = "shadows_highlights" // Values: shadows, highlights, radius
	SmartDevelop = "develop"            // Values: as Develop, in its order
	SmartAdjust  = "adjust"             // Adjust
)

// smartValues is how many values each kind takes.
var smartValues = map[string]int{SmartBlur: 1, SmartUnsharp: 3, SmartNoise: 1, SmartMosaic: 1, SmartTones: 3, SmartDevelop: 11}

// SmartFilter is a filter a smart object holds. It is a value never changed in place.
type SmartFilter struct {
	Kind   string      `json:"kind"`
	Values []float64   `json:"values,omitempty"`
	On     bool        `json:"on,omitempty"`
	Seed   uint64      `json:"seed,omitempty"` // add_noise: the same grain each time
	Adjust *Adjustment `json:"adjust,omitempty"`
	Off    bool        `json:"off,omitempty"` // turned off in the list
}

// FilterStack is a smart object's filters, first applied first. It is a value never changed in place.
type FilterStack struct {
	List []SmartFilter `json:"list"`
}

// ErrNoSmartFilter means a smart object has no filter at that place.
var ErrNoSmartFilter = errors.New("the smart object has no such filter")

func (f SmartFilter) valid() bool {
	if f.Kind == SmartAdjust {
		return f.Adjust != nil && f.Adjust.valid()
	}
	n, ok := smartValues[f.Kind]
	if !ok || len(f.Values) != n {
		return false
	}
	for _, v := range f.Values {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1e6 {
			return false
		}
	}
	return true
}

func (s *FilterStack) valid() bool {
	if s == nil {
		return true
	}
	for _, f := range s.List {
		if !f.valid() {
			return false
		}
	}
	return true
}

// shown lists the filters that are on; nil for none.
func (s *FilterStack) shown() []SmartFilter {
	if s == nil {
		return nil
	}
	var out []SmartFilter
	for _, f := range s.List {
		if !f.Off {
			out = append(out, f)
		}
	}
	return out
}

// reach is how far the filters together read around a pixel at full size.
func (s *FilterStack) reach() int {
	sum := 0
	for _, f := range s.shown() {
		sum += f.filter(0).reach
	}
	return sum
}

// Filter is the filter at full size.
func (f SmartFilter) Filter() Filter { return f.filter(0) }

// filter is the filter as it works at detail level l, where a pixel is 2^l of the picture's.
func (f SmartFilter) filter(l int) Filter {
	scale := 1 / float64(int(1)<<l)
	v := func(i int) float64 {
		if i < len(f.Values) {
			return f.Values[i]
		}
		return 0
	}
	switch f.Kind {
	case SmartBlur:
		return GaussianBlur(min(max(v(0), 0), 2000) * scale)
	case SmartUnsharp:
		return UnsharpMask(v(0), min(max(v(1), 0.1), 2000)*scale, v(2))
	case SmartNoise:
		return AddNoise(v(0), f.On, f.Seed)
	case SmartMosaic:
		return Mosaic(max(int(min(max(v(0), 1), 2000)*scale), 1))
	case SmartTones:
		return ShadowsHighlights(v(0), v(1), min(max(v(2), 1), 2000)*scale)
	case SmartDevelop:
		p := Develop{Temperature: v(0), Tint: v(1), Exposure: v(2), Contrast: v(3), Highlights: v(4), Shadows: v(5), Whites: v(6),
			Blacks: v(7), Clarity: v(8), Vibrance: v(9), Saturate: v(10), radius: developRadius * scale}
		return p.Filter()
	case SmartAdjust:
		if f.Adjust != nil {
			return f.Adjust.Filter()
		}
	}
	return Filter{apply: copyArea}
}

// withFilters is a copy of a stack changed by fn.
func withFilters(s *FilterStack, fn func(list []SmartFilter) []SmartFilter) *FilterStack {
	var list []SmartFilter
	if s != nil {
		list = slices.Clone(s.List)
	}
	if list = fn(list); len(list) == 0 {
		return nil
	}
	return &FilterStack{List: list}
}

// smartNode is the smart object id.
func (d *Doc) smartNode(id int) (*Node, error) {
	n := d.Node(id)
	if n == nil {
		return nil, ErrNoLayer
	}
	if !n.IsSmart() {
		return nil, ErrNotSmart
	}
	return n, nil
}

// SmartFilterSession is a smart object's filter being tried, shown as it changes and recorded as one step.
type SmartFilterSession struct {
	d    *Doc
	n    *Node
	at   int
	mark Mark
}

// BeginSmartFilter starts adding f after the smart object's other filters: End keeps it, Cancel takes it away.
func (d *Doc) BeginSmartFilter(id int, f SmartFilter) (*SmartFilterSession, error) {
	n, err := d.smartNode(id)
	if err != nil {
		return nil, err
	}
	if !f.valid() {
		return nil, errors.New("the filter's settings do not fit its kind")
	}
	s := &SmartFilterSession{d: d, n: n, mark: d.Mark()}
	n.Filters = withFilters(n.Filters, func(l []SmartFilter) []SmartFilter { return append(l, f) })
	s.at = len(n.Filters.List) - 1
	return s, nil
}

// EditSmartFilter starts changing the filter at place i.
func (d *Doc) EditSmartFilter(id, i int) (*SmartFilterSession, error) {
	n, err := d.smartNode(id)
	if err != nil {
		return nil, err
	}
	if n.Filters == nil || i < 0 || i >= len(n.Filters.List) {
		return nil, ErrNoSmartFilter
	}
	return &SmartFilterSession{d: d, n: n, at: i, mark: d.Mark()}, nil
}

// Filter is the filter as it is now.
func (s *SmartFilterSession) Filter() SmartFilter { return s.n.Filters.List[s.at] }

// Set shows the settings of f, which keeps the filter's kind.
func (s *SmartFilterSession) Set(f SmartFilter) error {
	if !f.valid() {
		return errors.New("the filter's settings do not fit its kind")
	}
	s.n.Filters = withFilters(s.n.Filters, func(l []SmartFilter) []SmartFilter { l[s.at] = f; return l })
	return nil
}

// End records the change as one step.
func (s *SmartFilterSession) End() { s.d.Commit("Smart filter", s.mark) }

// Cancel puts the filters back.
func (s *SmartFilterSession) Cancel() { s.d.restore(s.mark.s) }

// changeFilters changes a smart object's filters as one step.
func (d *Doc) changeFilters(id, i int, fn func(list []SmartFilter) []SmartFilter) error {
	n, err := d.smartNode(id)
	if err != nil {
		return err
	}
	if n.Filters == nil || i < 0 || i >= len(n.Filters.List) {
		return ErrNoSmartFilter
	}
	m := d.Mark()
	n.Filters = withFilters(n.Filters, fn)
	d.Commit("Smart filter", m)
	return nil
}

// RemoveSmartFilter takes away the filter at place i.
func (d *Doc) RemoveSmartFilter(id, i int) error {
	return d.changeFilters(id, i, func(l []SmartFilter) []SmartFilter { return slices.Delete(l, i, i+1) })
}

// ToggleSmartFilter turns the filter at place i off or on.
func (d *Doc) ToggleSmartFilter(id, i int) error {
	return d.changeFilters(id, i, func(l []SmartFilter) []SmartFilter { l[i].Off = !l[i].Off; return l })
}

// MoveSmartFilter swaps the filter at place i with the one by places away.
func (d *Doc) MoveSmartFilter(id, i, by int) error {
	return d.changeFilters(id, i, func(l []SmartFilter) []SmartFilter {
		if j := i + by; j >= 0 && j < len(l) {
			l[i], l[j] = l[j], l[i]
		}
		return l
	})
}

// smartTileLimit is how many tiles of filtered pictures are kept, a quarter of a gigabyte.
const smartTileLimit = 1024

// smartTile is a tile being or having been worked out; those who want it wait for the one working.
type smartTile struct {
	once sync.Once
	img  *image.RGBA
}

type smartKey struct {
	id, level int
	tile      image.Point
}

// smartStore is the tiles of a picture's smart filters, each kept with the state of the smart object it shows.
type smartStore struct {
	mu    sync.Mutex
	tiles map[smartKey]*smartTile
	state map[int]uint64
}

var treeSeed = maphash.MakeSeed()

// treeState is a number that changes when anything in the smart object that shows does.
func treeState(n *Node) uint64 {
	var h maphash.Hash
	h.SetSeed(treeSeed)
	var add func(n *Node)
	num := func(v uint64) { maphash.WriteComparable(&h, v) }
	add = func(n *Node) {
		maphash.WriteComparable(&h, n.ID)
		maphash.WriteComparable(&h, n.Visible)
		maphash.WriteComparable(&h, n.Opacity)
		maphash.WriteComparable(&h, n.Blend)
		maphash.WriteComparable(&h, n.MaskOff)
		maphash.WriteComparable(&h, n.Vector)
		maphash.WriteComparable(&h, n.Adjust)
		maphash.WriteComparable(&h, n.Style)
		maphash.WriteComparable(&h, n.Text)
		maphash.WriteComparable(&h, n.Clip)
		maphash.WriteComparable(&h, n.Filters)
		if n.Pixels != nil {
			maphash.WriteComparable(&h, n.Pixels)
			num(n.Pixels.p.version)
			maphash.WriteComparable(&h, n.Pixels.p.off)
		}
		if n.Mask != nil {
			maphash.WriteComparable(&h, n.Mask)
			num(n.Mask.p.version)
			maphash.WriteComparable(&h, n.Mask.p.off)
		}
		for _, c := range n.Children {
			add(c)
		}
	}
	add(n)
	return h.Sum64()
}

// smartPicture is what the smart object n shows over r at detail level l, its filters applied.
func (d *Doc) smartPicture(n *Node, r image.Rectangle, l int) *image.RGBA {
	out := image.NewRGBA(r)
	state := treeState(n)
	k0, k1 := keysOver(r)
	for ky := k0.Y; ky <= k1.Y; ky++ {
		for kx := k0.X; kx <= k1.X; kx++ {
			key := image.Pt(kx, ky)
			img := d.smartTileOf(n, smartKey{n.ID, l, key}, state)
			copyRect(out, img, tileRect(key).Intersect(r))
		}
	}
	return out
}

func (d *Doc) smartTileOf(n *Node, key smartKey, state uint64) *image.RGBA {
	st := &d.filtered
	st.mu.Lock()
	if st.tiles == nil {
		st.tiles, st.state = map[smartKey]*smartTile{}, map[int]uint64{}
	}
	if st.state[n.ID] != state {
		st.state[n.ID] = state
		for k := range st.tiles {
			if k.id == n.ID {
				delete(st.tiles, k)
			}
		}
	}
	t := st.tiles[key]
	if t == nil {
		if len(st.tiles) >= smartTileLimit {
			clear(st.tiles) // those being worked out go on for those waiting
		}
		t = &smartTile{}
		st.tiles[key] = t
	}
	st.mu.Unlock()
	t.once.Do(func() { t.img = d.smartTile(n, tileRect(key.tile), key.level) })
	return t.img
}

// smartTile works out the filters over r: the group as it looks, reaching as far around r as the filters read
// together, then each filter on what the one before left.
func (d *Doc) smartTile(n *Node, r image.Rectangle, l int) *image.RGBA {
	var chain []Filter
	reach := 0
	for _, f := range n.Filters.shown() {
		fn := f.filter(l)
		chain = append(chain, fn)
		reach += fn.reach
	}
	area := r.Inset(-reach)
	frame := levelDown(d.Frame, l)
	inner := area
	if !frame.Empty() {
		inner = area.Intersect(frame)
	}
	if inner.Empty() {
		return image.NewRGBA(r) // beyond the picture, however far the filters read
	}
	cur := image.NewRGBA(inner)
	d.compositeInto(cur, n, inner, l)
	cur = extendEdge(cur, area)
	for _, fn := range chain {
		reach -= fn.reach
		cur = fn.apply(cur, r.Inset(-reach))
	}
	return cur
}

// extendEdge is img over r with its edge going on beyond it, as filters see a picture's edge, so a blur does not
// darken it.
func extendEdge(img *image.RGBA, r image.Rectangle) *image.RGBA {
	inner := img.Rect
	if inner == r || inner.Empty() {
		return img
	}
	out := image.NewRGBA(r)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		sy := min(max(y, inner.Min.Y), inner.Max.Y-1)
		for x := r.Min.X; x < r.Max.X; x++ {
			sx := min(max(x, inner.Min.X), inner.Max.X-1)
			copy(out.Pix[out.PixOffset(x, y):][:4], img.Pix[img.PixOffset(sx, sy):][:4])
		}
	}
	return out
}
