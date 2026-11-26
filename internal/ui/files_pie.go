package ui

import (
	"cmp"
	"fmt"
	"image/color"
	"math"
	"path/filepath"
	"slices"
	"time"

	"github.com/btelolabs/btelo-forge/internal/files"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/office/dml"
)

// A list showing an analysis has a map of its folder floating over its rows, in the manner of DaisyDisk: rings around the
// folder's size, the inner ring its items and each ring further out what the folders of the one inside hold, coloured
// through the spectrum by where they lie, with what is too small to draw in grey. The item under the cursor turns to
// twelve o'clock and stands out a little, both on springs, so moving down the list, with the keys or by pointing at
// rows, turns the rings smoothly from item to item. A click on the inner ring puts the cursor on its item, one further
// out opens the folder that holds it, and one in the middle goes up. The arc pointed at is outlined. The map is a card
// in the list's bottom right corner, and slides to the top right while a row in use lies behind it (pieCard).

// pieSlots is how many items have a wedge of their own in the inner ring; the rest share the last wedge.
const pieSlots = 32

// pieOther is the key of the wedge the items without one of their own share.
const pieOther = "\x00other"

// pieOtherColor is what is no one item: a grey.
var pieOtherColor = gfx.Hex(0x6b6e78)

// pieEdges are where the rings start and end, of the map's radius; the rings narrow outwards.
var pieEdges = [...]float32{0.30, 0.50, 0.67, 0.81, 0.92, 1}

// pieMinArc is the narrowest arc drawn, in radians: what is narrower goes into the grey arc of its folder.
const pieMinArc = 0.012

// pieColor is the colour of what lies at an angle, in a ring: red at the start round to violet at the end, a little
// lighter outwards.
func pieColor(angle float64, ring int) color.RGBA {
	return dml.HSL(angle/(2*math.Pi)*275, 0.74, 0.56+0.025*float64(ring))
}

// pieArc is a piece of a ring.
type pieArc struct {
	ring      int // 0 is the inner ring
	from, to  float64
	name      string // pieOther for the grey arc
	size      int64
	top       string // the key of the wedge of the inner ring it lies in
	dir, land string // for rings further out: the folder holding it, and the name to land on
	col       color.RGBA
}

// pieArcs lays out the rings of a folder around its wedges.
func pieArcs(node *files.Node, wedges []pieWedge) []pieArc {
	var out []pieArc
	var grow func(n *files.Node, top string, ring int, from, to float64)
	grow = func(n *files.Node, top string, ring int, from, to float64) {
		if ring >= len(pieEdges)-1 || n.Size <= 0 {
			return
		}
		kids := slices.Clone(n.Children)
		slices.SortFunc(kids, func(x, y *files.Node) int { return cmp.Compare(y.Size, x.Size) })
		scale := (to - from) / float64(n.Size)
		dir, at, left := n.Path(), from, n.Size
		for _, k := range kids {
			span := float64(k.Size) * scale
			if span < pieMinArc {
				break
			}
			mid := at + span/2
			out = append(out, pieArc{ring: ring, from: at, to: at + span, name: k.Name, size: k.Size, top: top, dir: dir, land: k.Name, col: pieColor(mid, ring)})
			grow(k, top, ring+1, at, at+span)
			at += span
			left -= k.Size
		}
		if to-at >= pieMinArc {
			out = append(out, pieArc{ring: ring, from: at, to: to, name: pieOther, size: left, top: top, dir: dir, col: pieOtherColor})
		}
	}
	for _, w := range wedges {
		out = append(out, pieArc{from: w.from, to: w.to, name: w.key, size: w.size, top: w.key, col: w.col})
		// While an analysis runs only the inner ring is known: what lies under its folders is only added up.
		if w.key != pieOther && !node.Live {
			if c := node.Child(w.key); c != nil {
				grow(c, w.key, 1, w.from, w.to)
			}
		}
	}
	return out
}

// pieWedge is a wedge of a folder's pie: an item, or the items that share the last wedge.
type pieWedge struct {
	key      string // the item's name, or pieOther
	size     int64
	from, to float64 // radians clockwise from twelve o'clock, before the pie is turned
	col      color.RGBA
	count    int // for pieOther, how many of the items listed it holds
}

// pieMotion is how a list's pie is turned and which wedges stand out, moving on springs.
type pieMotion struct {
	dir    string // the folder it moves for: another starts still, with no turn to make
	turn   spring // radians clockwise
	target float64
	focus  string             // the key of the wedge turned to
	pops   map[string]*spring // how far each wedge stands out, from 0 to 1
	at     time.Time
	moving bool
	shown  bool // drawn this frame: a pie out of sight does not keep the window drawing
	// cx, cy and radius are where it was drawn last, before a wedge stands out, on the card at card.
	cx, cy, radius float32
	card           gfx.Rect
	// arcs are the rings of the folder laidOf at its size laidAt, around laidFor, kept as walking the tree each frame
	// is not free.
	arcs    []pieArc
	laidFor []pieWedge
	laidOf  *files.Node
	laidAt  int64
	// place is where the card is, from 0 in the bottom corner to 1 in the top one, sliding to placeGoal; it stays
	// where it is from folder to folder.
	place     spring
	placeGoal float64
}

// How quickly the pie turns and a wedge stands out, and how damped: the turn settles with hardly a sway, the wedge
// with a small bounce.
const (
	pieTurnFreq, pieTurnRatio = 1.7, 0.82
	piePopFreq, piePopRatio   = 2.6, 0.6
	// The card slides from corner to corner without a bounce.
	pieSlideFreq, pieSlideRatio = 2.4, 1
	// piePop is how much larger the wedge turned to grows, of the pie's radius.
	piePop = 0.08
)

// pieWedges divides the folder's size among the items a list shows, largest first: the largest have wedges of
// their own and the rest, with whatever the list leaves out (hidden files), share one. Items too small to see as a
// wedge join the rest too.
func (a *App) pieWedges(p *filesPane, node *files.Node) []pieWedge {
	if node == nil || node.Size <= 0 {
		return nil
	}
	type item struct {
		name string
		size int64
	}
	var items []item
	var listed int64
	for _, r := range p.rows {
		e := &p.entries[r]
		if s := a.entrySize(node, e); s > 0 {
			items = append(items, item{e.Name, s})
			listed += s
		}
	}
	slices.SortStableFunc(items, func(x, y item) int {
		if c := cmp.Compare(y.size, x.size); c != 0 {
			return c
		}
		return naturalCompare(x.name, y.name)
	})
	total := float64(max(node.Size, listed))
	var out []pieWedge
	rest := pieWedge{key: pieOther, col: pieOtherColor, size: max(node.Size-listed, 0)}
	for _, it := range items {
		if len(out) < pieSlots && float64(it.size)/total >= 0.005 {
			out = append(out, pieWedge{key: it.name, size: it.size})
			continue
		}
		rest.size += it.size
		rest.count++
	}
	if rest.size > 0 {
		out = append(out, rest)
	}
	at := 0.0
	for i := range out {
		out[i].from = at
		at += 2 * math.Pi * float64(out[i].size) / total
		out[i].to = at
		if out[i].key != pieOther {
			out[i].col = pieColor((out[i].from+out[i].to)/2, 0)
		}
	}
	if n := len(out); n > 0 {
		out[n-1].to = 2 * math.Pi // the last closes the circle whatever the rounding
	}
	return out
}

// pieKeyOf is the key of the wedge an item is in, or "" when it is in none, being empty.
func (a *App) pieKeyOf(wedges []pieWedge, node *files.Node, e *files.Entry) string {
	if e == nil || a.entrySize(node, e) <= 0 {
		return ""
	}
	for _, w := range wedges {
		if w.key == e.Name {
			return w.key
		}
	}
	if n := len(wedges); n > 0 && wedges[n-1].key == pieOther {
		return pieOther
	}
	return ""
}

// pieColorOf is the colour of the wedge an item is in, for its size bar.
func (a *App) pieColorOf(wedges []pieWedge, node *files.Node, e *files.Entry) (color.RGBA, bool) {
	key := a.pieKeyOf(wedges, node, e)
	for _, w := range wedges {
		if w.key == key {
			return w.col, true
		}
	}
	return color.RGBA{}, false
}

// pieNow is the time the pies move by; tests set fm.clock to step it themselves.
func (a *App) pieNow() time.Time {
	if a.fm.clock != nil {
		return a.fm.clock()
	}
	return time.Now()
}

// advance moves the pie to now, turning the wedge focus (a key, or "" for none) to twelve o'clock.
func (m *pieMotion) advance(dir string, wedges []pieWedge, focus string, now time.Time) {
	i := slices.IndexFunc(wedges, func(w pieWedge) bool { return w.key == focus })
	if m.dir != dir || m.pops == nil {
		// A folder seen anew starts turned to its wedge, which grows out from the rest, in the card where it was.
		*m = pieMotion{dir: dir, pops: map[string]*spring{}, at: now, focus: focus, place: m.place, placeGoal: m.placeGoal, moving: m.moving}
		if i >= 0 {
			m.target = -(wedges[i].from + wedges[i].to) / 2
			m.turn.pos = m.target
		}
	}
	if i >= 0 {
		// The turn goes the shorter way round from where the pie is when the wedge changes; while the same wedge
		// stays in front, its target stays put though the wedges' sizes change.
		goal := -(wedges[i].from + wedges[i].to) / 2
		from := m.target
		if focus != m.focus {
			from = m.turn.pos
		}
		m.target = goal + 2*math.Pi*math.Round((from-goal)/(2*math.Pi))
	}
	m.focus = focus
	dt := 0.0
	if m.moving {
		dt = min(now.Sub(m.at).Seconds(), 1.0/20) // a long wait between frames is not a leap
	}
	m.at = now
	moving := m.turn.step(m.target, dt, pieTurnFreq, pieTurnRatio)
	if m.place.step(m.placeGoal, dt, pieSlideFreq, pieSlideRatio) {
		moving = true
	}
	for _, w := range wedges {
		s := m.pops[w.key]
		if s == nil {
			s = &spring{}
			m.pops[w.key] = s
		}
		goal := 0.0
		if w.key == focus {
			goal = 1
		}
		if s.step(goal, dt, piePopFreq, piePopRatio) {
			moving = true
		}
	}
	for k := range m.pops {
		if !slices.ContainsFunc(wedges, func(w pieWedge) bool { return w.key == k }) {
			delete(m.pops, k)
		}
	}
	m.moving, m.shown = moving, true
}

// pieMoving reports whether a pie in the file manager tab in front is still turning or growing.
func (a *App) pieMoving() bool {
	if a.files == nil {
		return false
	}
	for _, p := range a.files.panes {
		if p.pie.shown && p.pie.moving {
			return true
		}
	}
	return false
}

// pieCardSize is the size of a list's pie card, or zero when the list is too small to spare the room: it must leave
// the rows' names beside it, and fit twice in the list's height, so that a row is always clear of one of its places.
func (a *App) pieCardSize(body gfx.Rect) (w, h float32) {
	u := &a.u
	side := min(body.W*0.4, (body.H-3*pieCardMargin(u))/2-pieCardExtra(u), u.px(300))
	if side < u.px(170) || body.W-side < u.px(280) {
		return 0, 0
	}
	return side, side + pieCardExtra(u)
}

// pieCardMargin is how far the card keeps from the list's edges.
func pieCardMargin(u *ctx) float32 { return u.px(12) }

// pieCardExtra is how much taller than wide the card is: the line naming what is shown, under the rings.
func pieCardExtra(u *ctx) float32 { return u.px(32) }

// pieCardAt is where the card of size w by h lies in the list's body at place, from 0 in the bottom corner to 1 in the
// top one; the scroll bar stays clear beside it.
func pieCardAt(u *ctx, body gfx.Rect, w, h float32, place float64) gfx.Rect {
	m := pieCardMargin(u)
	bottom, top := body.Bottom()-m-h, body.Y+m
	return gfx.Rect{X: body.Right() - m - u.px(6) - w, Y: bottom + (top-bottom)*float32(place), W: w, H: h}
}

// placePieCard sets where the card slides to: away from a corner where it hides a row in use (rows, by their place in
// the list), when the other corner hides none of them; otherwise it stays.
func (m *pieMotion) placePieCard(u *ctx, body gfx.Rect, w, h, top float32, rows ...int) {
	rowH := u.fileRowH()
	covers := func(place float64) bool {
		card := pieCardAt(u, body, w, h, place)
		for _, row := range rows {
			if row < 0 {
				continue
			}
			y := body.Y + (float32(row)-top)*rowH
			if y < card.Bottom() && y+rowH > card.Y {
				return true
			}
		}
		return false
	}
	other := 1 - m.placeGoal
	if covers(m.placeGoal) && !covers(other) {
		m.placeGoal = other
	}
}

// rings is the arcs of a folder, laid out again only when it or its wedges change.
func (m *pieMotion) rings(node *files.Node, wedges []pieWedge) []pieArc {
	if m.laidOf != node || m.laidAt != node.Size || !slices.Equal(m.laidFor, wedges) {
		m.arcs, m.laidOf, m.laidAt, m.laidFor = pieArcs(node, wedges), node, node.Size, wedges
	}
	return m.arcs
}

// arcAt is the arc at a distance and angle (before the turn) from the centre, or nil.
func (m *pieMotion) arcAt(arcs []pieArc, dist, angle float64) *pieArc {
	for i := range arcs {
		arc := &arcs[i]
		scale := float64(m.radius) * (1 + piePop*m.pop(arc.top))
		if angle >= arc.from && angle < arc.to &&
			dist >= float64(pieEdges[arc.ring])*scale && dist <= float64(pieEdges[arc.ring+1])*scale {
			return arc
		}
	}
	return nil
}

// drawPie draws the rings of a list's folder in r, with what the arc pointed at, or the wedge in front, holds under
// them.
func (a *App) drawPie(fd *filesDoc, i int, p *filesPane, id string, r gfx.Rect, node *files.Node, wedges []pieWedge) {
	u := &a.u
	// A card like those of the work in the corner, floating over the rows.
	u.c.Outline(r.Inset(-u.px(1)), u.px(4), colShadow)
	u.c.Fill(r, colSurface)
	u.c.Outline(r, max(1, u.px(1)), colBorderHi)
	labelH := u.px(46)
	area := gfx.Rect{X: r.X, Y: r.Y + u.px(10), W: r.W, H: r.H - u.px(10) - labelH}
	radius := float32(math.Max(float64(min(area.W-u.px(24), area.H)/2/(1+piePop)), 1))
	cx, cy := area.X+area.W/2, area.Y+area.H/2

	m := &p.pie
	m.cx, m.cy, m.radius, m.card = cx, cy, radius, r
	arcs := m.rings(node, wedges)
	// The arc under the pointer, found where the last frame drew it.
	var hovered *pieArc
	pid := id + "/pie"
	disc := gfx.Rect{X: cx - radius*(1+piePop), Y: cy - radius*(1+piePop), W: 2 * radius * (1 + piePop), H: 2 * radius * (1 + piePop)}
	middle := false
	if fd.prompt == nil && u.hover(pid, disc) {
		dx, dy := float64(u.mx-cx), float64(u.my-cy)
		angle := math.Mod(math.Atan2(dx, -dy)-m.turn.pos, 2*math.Pi)
		if angle < 0 {
			angle += 2 * math.Pi
		}
		dist := math.Hypot(dx, dy)
		hovered = m.arcAt(arcs, dist, angle)
		middle = dist < float64(pieEdges[0]*radius) && node.Parent != nil
		if hovered != nil || middle {
			u.cursor = CursorHand
		}
	}

	if fd.prompt == nil && u.clicked(pid, disc) {
		switch {
		case middle:
			a.focusPane(fd, i)
			a.goUp(p)
			return
		case hovered != nil && hovered.ring > 0:
			a.focusPane(fd, i)
			a.goTo(p, hovered.dir, hovered.land)
			return
		case hovered != nil:
			// The cursor goes on the item, or on the first of those sharing the last wedge.
			for row, ri := range p.rows {
				if a.pieKeyOf(wedges, node, &p.entries[ri]) == hovered.top {
					a.focusPane(fd, i)
					p.cursor = row
					a.keepInView(fd, p)
					break
				}
			}
		}
	}

	focus := a.pieKeyOf(wedges, node, p.current())
	m.advance(p.dir, wedges, focus, a.pieNow())

	gap := max(u.px(1), 1)
	shapes := make([]gfx.Wedge, len(arcs))
	outlined := -1
	for k := range arcs {
		arc := &arcs[k]
		if arc == hovered {
			outlined = k
		}
		pop := m.pop(arc.top)
		scale := radius * float32(1+piePop*pop)
		shapes[k] = gfx.Wedge{
			From: float32(arc.from + m.turn.pos), To: float32(arc.to + m.turn.pos),
			Inner: pieEdges[arc.ring]*scale + gap, Radius: pieEdges[arc.ring+1] * scale,
			Col: gfx.Mix(arc.col, colWhite, float32(0.16*pop)),
		}
	}
	u.c.Pie(cx, cy, shapes, gap)
	if k := outlined; k >= 0 {
		// The arc pointed at is outlined: drawn again over a copy grown by the line's width, in the colour of text, so
		// that even a sliver stands clear of its neighbours.
		line := max(u.px(2), 1.5)
		edge := shapes[k]
		edge.Inner, edge.Radius, edge.Col = max(edge.Inner-line, 0), edge.Radius+line, colText
		u.c.Pie(cx, cy, []gfx.Wedge{edge}, gap-2*line)
		u.c.Pie(cx, cy, shapes[k:k+1], gap)
	}

	// The whole folder in the middle.
	colTotal := colText
	if middle {
		if isDarkTheme() {
			colTotal = colWhite
		} else {
			colTotal = colAccent
		}
	}
	u.label(u.bold(14), gfx.Rect{X: cx - radius*pieEdges[0], Y: cy - u.px(10), W: 2 * radius * pieEdges[0], H: u.px(20)}, sizeText(node.Size), colTotal, 0.5)

	// What is shown under it: the arc pointed at, else the wedge in front, else the whole folder.
	lr := gfx.Rect{X: r.X + u.px(12), Y: area.Bottom(), W: r.W - u.px(24), H: labelH}
	name, size, sw := filepath.Base(p.dir), node.Size, colFaint
	var count int
	if hovered != nil {
		name, size, sw = hovered.name, hovered.size, hovered.col
	}
	for _, w := range wedges {
		if hovered == nil && w.key == focus {
			name, size, sw = w.key, w.size, w.col
		}
		if w.key == pieOther && name == pieOther && (hovered == nil || hovered.ring == 0) {
			count = w.count
		}
	}
	if name == pieOther {
		name = u.t("Everything else")
	}
	detail := sizeText(size)
	if node.Size > 0 {
		detail += " · " + fmt.Sprintf("%.1f%%", float64(size)/float64(node.Size)*100)
	}
	if count > 0 {
		detail += " · " + itemCount(u, int64(count))
	}
	bold := u.bold(13)
	dot := u.px(10)
	name = bold.Ellipsize(name, lr.W-dot-u.px(8))
	nw := bold.Measure(name) + dot + u.px(8)
	x := lr.X + (lr.W-nw)/2
	line := u.px(20)
	u.c.Circle(x+dot/2, lr.Y+line/2, dot/2, sw)
	u.c.Text(bold, x+dot+u.px(8), lr.Y+(line-bold.Height())/2, name, colText)
	u.label(u.face(12), gfx.Rect{X: lr.X, Y: lr.Y + line, W: lr.W, H: u.px(18)}, detail, colDim, 0.5)
}

// pop is how far the wedge key stands out, from 0 to about 1.
func (m *pieMotion) pop(key string) float64 {
	if s := m.pops[key]; s != nil {
		return s.pos
	}
	return 0
}
