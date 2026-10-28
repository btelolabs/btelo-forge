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
)

// A list showing an analysis has a pie of its folder under its rows: a wedge for each of the largest items, the
// rest together as one more. The wedge of the item under the cursor turns to twelve o'clock and stands out a little,
// both on springs, so moving down the list, with the keys or by pointing at rows, turns the pie smoothly from item
// to item. A click on a wedge puts the cursor on its item.

// pieSlots is how many items have a wedge of their own, as many as there are colours told apart at a glance; the
// rest share the last wedge.
const pieSlots = 8

// pieOther is the key of the wedge the items without one of their own share.
const pieOther = "\x00other"

// pieColors tell the wedges apart, largest first: eight hues kept apart for colour-blind eyes as well, each at least
// 3:1 against the panel.
var pieColors = [pieSlots]color.RGBA{
	gfx.Hex(0x3987e5), gfx.Hex(0xd95926), gfx.Hex(0x199e70), gfx.Hex(0xc98500),
	gfx.Hex(0xd55181), gfx.Hex(0x008300), gfx.Hex(0x9085e9), gfx.Hex(0xe66767),
}

// pieOtherColor is the wedge of everything else: a grey, as it is no one item.
var pieOtherColor = gfx.Hex(0x6b6e78)

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
	// cx, cy and radius are where it was drawn last, before a wedge stands out.
	cx, cy, radius float32
}

// How quickly the pie turns and a wedge stands out, and how damped: the turn settles with hardly a sway, the wedge
// with a small bounce.
const (
	pieTurnFreq, pieTurnRatio = 1.7, 0.82
	piePopFreq, piePopRatio   = 2.6, 0.6
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
			out = append(out, pieWedge{key: it.name, size: it.size, col: pieColors[len(out)]})
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
		// A folder seen anew starts turned to its wedge, which grows out from the rest.
		*m = pieMotion{dir: dir, pops: map[string]*spring{}, at: now, focus: focus}
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

// pieHeight is how much of a list's body its pie takes, or 0 when the list is too small to spare the room.
func (a *App) pieHeight(body gfx.Rect) float32 {
	u := &a.u
	h := min(body.W*0.72, body.H*0.45, u.px(330))
	if h < u.px(170) || body.H-h < 4*u.fileRowH() {
		return 0
	}
	return h
}

// drawPie draws the pie of a list's folder in r, with what the wedge in front, or the one pointed at, holds under
// it.
func (a *App) drawPie(fd *filesDoc, i int, p *filesPane, id string, r gfx.Rect, node *files.Node, wedges []pieWedge) {
	u := &a.u
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Y, W: r.W, H: max(1, u.px(1))}, colBorder)
	labelH := u.px(46)
	area := gfx.Rect{X: r.X, Y: r.Y + u.px(12), W: r.W, H: r.H - u.px(12) - labelH}
	radius := float32(math.Max(float64(min(area.W-u.px(24), area.H)/2/(1+piePop)), 1))
	cx, cy := area.X+area.W/2, area.Y+area.H/2

	m := &p.pie
	// The wedge under the pointer, found where the last frame drew it.
	hovered := ""
	pid := id + "/pie"
	disc := gfx.Rect{X: cx - radius*(1+piePop), Y: cy - radius*(1+piePop), W: 2 * radius * (1 + piePop), H: 2 * radius * (1 + piePop)}
	if fd.prompt == nil && u.hover(pid, disc) {
		dx, dy := float64(u.mx-cx), float64(u.my-cy)
		angle := math.Mod(math.Atan2(dx, -dy)-m.turn.pos, 2*math.Pi)
		if angle < 0 {
			angle += 2 * math.Pi
		}
		for _, w := range wedges {
			if angle >= w.from && angle < w.to && math.Hypot(dx, dy) <= float64(radius)*(1+piePop*m.pop(w.key)) {
				hovered = w.key
				u.cursor = CursorHand
			}
		}
	}

	// A click on a wedge puts the cursor on its item, or on the first of those sharing the last wedge.
	if fd.prompt == nil && u.clicked(pid, disc) && hovered != "" {
		for row, ri := range p.rows {
			if a.pieKeyOf(wedges, node, &p.entries[ri]) == hovered {
				a.focusPane(fd, i)
				p.cursor = row
				a.keepInView(fd, p)
				break
			}
		}
	}

	focus := a.pieKeyOf(wedges, node, p.current())
	m.advance(p.dir, wedges, focus, a.pieNow())
	m.cx, m.cy, m.radius = cx, cy, radius

	shapes := make([]gfx.Wedge, len(wedges))
	for k, w := range wedges {
		pop := m.pop(w.key)
		col := gfx.Mix(w.col, colWhite, float32(0.16*pop))
		if w.key == hovered && w.key != focus {
			col = gfx.Mix(w.col, colWhite, 0.1)
		}
		shapes[k] = gfx.Wedge{
			From: float32(w.from + m.turn.pos), To: float32(w.to + m.turn.pos),
			Radius: radius * float32(1+piePop*pop), Col: col,
		}
	}
	u.c.Pie(cx, cy, shapes, max(u.px(2), 1))

	// What the wedge holds: the one pointed at, else the one in front, else the whole folder.
	shown := hovered
	if shown == "" {
		shown = focus
	}
	lr := gfx.Rect{X: r.X + u.px(12), Y: area.Bottom(), W: r.W - u.px(24), H: labelH}
	name, size, sw := filepath.Base(p.dir), node.Size, colFaint
	var count int
	for _, w := range wedges {
		if w.key == shown {
			name, size, sw, count = w.key, w.size, w.col, w.count
			if w.key == pieOther {
				name = u.t("Everything else")
			}
		}
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
