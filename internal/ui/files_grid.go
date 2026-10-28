package ui

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"path/filepath"
	"slices"
	"strings"

	"github.com/btelolabs/btelo-forge/internal/assets"
	"github.com/btelolabs/btelo-forge/internal/files"
	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// paneGrid is how a list shows its folder as a grid of pictures: the filter and search above it, how large the
// pictures are, and the grid as last laid out.
type paneGrid struct {
	filter assetFilter
	search string
	size   float64 // the rows' height, in points
	scroll float32
	viewH  float32 // how tall the grid's view was at the last frame

	layout    assetGrid
	layoutKey string // what layout was made for
	arranged  string // the filter and search the rows were picked for
	dims      map[string]image.Point
	dimsGen   int

	counts    gridCounts
	countsKey string

	chipScroll float32      // how far the chips' lines are scrolled up, when there are more than show
	barH       float32      // the filter bar's height at the last frame
	targets    []dropTarget // the chips files can be dropped on, as drawn this frame
	drag       []string     // the files being dragged onto a chip
	dragMoved  bool
}

// gridCounts are what the chips above a grid count.
type gridCounts struct {
	all, untagged, uncategorized int
	kinds                        map[assets.Kind]int
}

// assetGrid is a grid laid out: each card's rectangle relative to the grid's top-left, and the height of it all.
type assetGrid struct {
	cards  []gfx.Rect // by row of the list
	height float32
}

// dropTarget is a chip that takes dragged files: a category or a tag.
type dropTarget struct {
	id, name string
	rect     gfx.Rect
}

// gridSizes are the smallest and largest pictures, by the rows' height in points.
const (
	gridSizeMin = 80
	gridSizeMax = 360
)

// showGrid makes the list show its folder as pictures.
func (a *App) showGrid(p *filesPane) {
	p.grid = true
	if p.g.size == 0 {
		p.g.size = 160
	}
	if p.g.dims == nil && len(p.entries) > 0 {
		a.peekDims(p)
	}
}

// toggleView shows the list in use as pictures, or as rows again, the cursor staying on its item.
func (a *App) toggleView(fd *filesDoc) {
	p := fd.pane()
	if p.grid {
		p.grid = false
	} else {
		a.showGrid(p)
	}
	a.arrange(p)
	a.keepInView(fd, p)
}

// filterKey is what a list's rows were picked for: as rows, every entry; as a grid, its filter and search, which
// depend on the tags and categories.
func (a *App) filterKey(p *filesPane) string {
	if !p.grid || p.g.filter.what == showAll && strings.TrimSpace(p.g.search) == "" {
		return ""
	}
	return fmt.Sprintf("%v|%s|%d", p.g.filter, p.g.search, a.assetLibVersion)
}

// setFilter shows only what f lets through.
func (a *App) setFilter(p *filesPane, f assetFilter) {
	p.g.filter, p.g.scroll = f, 0
	a.arrange(p)
}

// dimsPeeker returns what reads the sizes of a grid's pictures from their headers and the cache, so the grid is
// laid out before they are made; a list of rows needs none, and its peeker reads nothing.
func (a *App) dimsPeeker(p *filesPane) func(ctx context.Context, dir string, entries []files.Entry) map[string]image.Point {
	if !p.grid {
		return func(context.Context, string, []files.Entry) map[string]image.Point { return nil }
	}
	a.fileThumbs.init(a.cacheDir)
	store := a.fileThumbs.store
	return func(ctx context.Context, dir string, entries []files.Entry) map[string]image.Point {
		dims := map[string]image.Point{}
		for _, e := range entries {
			if ctx.Err() != nil {
				return dims
			}
			kind := assets.KindOf(e.Name)
			if e.Dir || !kind.Visual() && kind != assets.Edit {
				continue
			}
			if info, ok := store.Peek(assets.Entry{Path: filepath.Join(dir, e.Name), Name: e.Name, Kind: kind, Size: e.Size, Mod: e.Mod}); ok && info.W > 0 {
				dims[e.Name] = image.Pt(info.W, info.H)
			}
		}
		return dims
	}
}

// peekDims reads the sizes of the pictures of a list turned into a grid, in the background.
func (a *App) peekDims(p *filesPane) {
	p.g.dims = map[string]image.Point{}
	p.g.dimsGen++
	gen, listing, dir := p.g.dimsGen, p.gen, p.dir
	entries := slices.Clone(p.entries)
	peek := a.dimsPeeker(p)
	a.background(func(ctx context.Context) {
		dims := peek(ctx, dir, entries)
		a.post(func() {
			if p.g.dimsGen == gen && p.gen == listing {
				p.g.dims, p.g.layoutKey = dims, ""
			}
		})
	})
}

// aspect is how wide a card's picture is for its height.
func (a *App) aspect(p *filesPane, e assets.Entry) float32 {
	var w, h int
	if d, ok := p.g.dims[e.Name]; ok {
		w, h = d.X, d.Y
	} else if it := a.fileThumbs.items[e.Path]; it != nil && it.ready && it.info.W > 0 {
		w, h = it.info.W, it.info.H
	}
	switch {
	case w > 0 && h > 0:
		return min(max(float32(w)/float32(h), 0.45), 2.6)
	case e.Kind == assets.Video || e.Kind == assets.Edit:
		return 16.0 / 9
	case e.Kind == assets.Audio:
		return 1.7
	case e.Kind.Readable():
		return 0.8
	case e.Kind == assets.Folder:
		return 1.3
	}
	return 1
}

// assetLabelH is the room under each card for its name and details.
func (u *ctx) assetLabelH() float32 { return u.px(38) }

// layoutGrid lays a list's rows out in justified rows, as Eagle does: each row as tall as the size chosen,
// stretched to fill the width exactly, its pictures keeping their shapes; the last row is not stretched. A row
// too narrow for even one picture at that size makes it smaller.
func (a *App) layoutGrid(p *filesPane, width float32) assetGrid {
	u := &a.u
	key := fmt.Sprintf("%p|%d|%s|%.1f|%.1f|%.2f|%d|%d", p.entries, len(p.rows), p.g.arranged, width, p.g.size, u.s, a.fileThumbs.made, len(p.g.dims))
	if key == p.g.layoutKey && len(p.g.layout.cards) == len(p.rows) {
		return p.g.layout
	}
	gap := u.px(12)
	rowH := min(u.px(float32(p.g.size)), max(width/1.3, u.px(40)))
	label := u.assetLabelH()
	g := assetGrid{cards: make([]gfx.Rect, len(p.rows))}
	entries := make([]assets.Entry, len(p.rows))
	for k, r := range p.rows {
		entries[k] = assetEntry(p, &p.entries[r])
	}
	y := float32(0)
	start := 0
	sum := float32(0) // the row's pictures' widths at rowH
	place := func(end int, stretch bool) {
		n := end - start
		if n == 0 {
			return
		}
		scale := float32(1)
		if stretch {
			scale = (width - gap*float32(n-1)) / sum
		}
		h := rowH * scale
		x := float32(0)
		for k := start; k < end; k++ {
			w := min(a.aspect(p, entries[k])*h, width)
			g.cards[k] = gfx.Rect{X: x, Y: y, W: w, H: h + label}
			x += w + gap
		}
		y += h + label + gap
	}
	for k, e := range entries {
		w := a.aspect(p, e) * rowH
		if k > start && sum+w+gap*float32(k-start) > width {
			place(k, true)
			start, sum = k, 0
		}
		sum += w
	}
	place(len(entries), sum+gap*float32(len(entries)-start-1) > width)
	g.height = y
	p.g.layout, p.g.layoutKey = g, key
	return g
}

// gridPad is the space around a grid's cards.
func (u *ctx) gridPad() float32 { return u.px(14) }

// The bar of chips above a grid: its chips wrap onto more lines as words do, up to filterLinesMax lines shown; more
// than that scroll up and down under the wheel.
const filterLinesMax = 3

// filterChipH and filterChipGap are a chip's height and the space between chips, in points; filterBarPad is the space
// above and below the chips.
const (
	filterChipH   = 28
	filterChipGap = 6
	filterBarPad  = 7
)

// filterChip is one chip of the bar: a filter, with how many files it lets through.
type filterChip struct {
	id, glyph, name string
	hue             color.RGBA
	count           int // -1 for a chip that counts nothing
	chosen          bool
	drop            func([]string) // files dropped on it, for a category or a tag
	menu            func() []menuItem
	pick            func()
}

// filterChips are the chips above a grid, in order.
func (a *App) filterChips(fd *filesDoc, p *filesPane, id string) []filterChip {
	u := &a.u
	c := a.gridCountsOf(p)
	lib := a.assetLibrary()
	f := p.g.filter
	set := func(to assetFilter) func() { return func() { a.setFilter(p, to) } }
	chips := []filterChip{
		{id: id + "/chip/all", name: u.t("All"), hue: colDim, count: c.all, chosen: f.what == showAll, pick: set(assetFilter{})},
		{id: id + "/chip/untagged", glyph: icLabelOff, name: u.t("Untagged"), hue: colDim, count: c.untagged,
			chosen: f.what == showUntagged, pick: set(assetFilter{what: showUntagged})},
		{id: id + "/chip/uncategorized", glyph: icInbox, name: u.t("Uncategorized"), hue: colDim, count: c.uncategorized,
			chosen: f.what == showUncategorized, pick: set(assetFilter{what: showUncategorized})},
	}
	for k, kind := range assets.Kinds {
		if c.kinds[kind] == 0 {
			continue
		}
		glyph, hue := kindLook(kind)
		chips = append(chips, filterChip{id: fmt.Sprintf("%s/chip/kind/%d", id, kind), glyph: glyph, name: u.t(kindNames[k]), hue: hue,
			count: c.kinds[kind], chosen: f.what == showKind && f.kind == kind, pick: set(assetFilter{what: showKind, kind: kind})})
	}
	for _, cat := range lib.Categories() {
		name := cat.Name
		chips = append(chips, filterChip{id: id + "/chip/category/" + name, glyph: icCategory, name: name, hue: gfx.Hex(0xe8b04b),
			count: cat.Files, chosen: f.what == showCategory && strings.EqualFold(f.name, name),
			drop: func(paths []string) { a.setCategory(paths, name, true) }, menu: func() []menuItem { return a.categoryMenu(fd, name) },
			pick: set(assetFilter{what: showCategory, name: name})})
	}
	for _, t := range lib.Tags() {
		name := t.Name
		chips = append(chips, filterChip{id: id + "/chip/tag/" + name, glyph: icLabel, name: name, hue: gfx.Hex(0x7f8cff),
			count: t.Files, chosen: f.what == showTag && strings.EqualFold(f.name, name),
			drop: func(paths []string) { a.addTag(paths, name) }, menu: func() []menuItem { return a.tagMenu(fd, name) },
			pick: set(assetFilter{what: showTag, name: name})})
	}
	return append(chips, filterChip{id: id + "/chip/new-category", glyph: icAdd, name: u.t("Category"), hue: colDim, count: -1,
		pick: func() { fd.after = func() { a.openPrompt(fd, promptCategory) } }})
}

// label is what a chip says: its name, and how many it counts.
func (c filterChip) label() string {
	if c.count < 0 {
		return c.name
	}
	return fmt.Sprintf("%s  %d", c.name, c.count)
}

// chipWidth is how wide a chip is drawn.
func (u *ctx) chipWidth(c filterChip) float32 {
	w := u.face(12).Measure(c.label()) + u.px(20)
	if c.glyph != "" {
		w += u.px(18)
	}
	return w
}

// filterStrip is the width the chips have in a bar w wide: what the search box and the size slider leave.
func (u *ctx) filterStrip(w float32) float32 {
	right := u.px(8)
	if w > u.px(560) {
		right += u.px(90) + u.px(8)
	}
	return max(w-right-min(u.px(200), w*0.34)-u.px(16), u.px(60))
}

// chipPlaces places chips of the widths given in lines at most w wide, as words wrap: each one's x from the line's
// start and its line, and how many lines there are.
func chipPlaces(widths []float32, w, gap float32) (xs []float32, lines []int, n int) {
	x, line := float32(0), 0
	for _, cw := range widths {
		if x > 0 && x+cw > w {
			x, line = 0, line+1
		}
		xs, lines = append(xs, x), append(lines, line)
		x += cw + gap
	}
	return xs, lines, line + 1
}

// filterBarHeight is the height of the bar for chips on n lines.
func (u *ctx) filterBarHeight(n int) float32 {
	shown := float32(min(max(n, 1), filterLinesMax))
	return u.px(2*filterBarPad + shown*filterChipH + (shown-1)*filterChipGap)
}

// drawGrid draws a list as a grid: the chips that filter it and the search above, the cards under them.
func (a *App) drawGrid(fd *filesDoc, i int, p *filesPane, id string, body gfx.Rect, moved bool) {
	u := &a.u
	if p.g.arranged != a.filterKey(p) {
		a.arrange(p) // a tag or category changed what the filter lets through
	}
	chips := a.filterChips(fd, p, id)
	widths := make([]float32, len(chips))
	for k, c := range chips {
		widths[k] = u.chipWidth(c)
	}
	xs, lines, n := chipPlaces(widths, u.filterStrip(body.W), u.px(filterChipGap))
	bar := gfx.Rect{X: body.X, Y: body.Y, W: body.W, H: u.filterBarHeight(n)}
	p.g.barH = bar.H
	grid := gfx.Rect{X: body.X, Y: bar.Bottom(), W: body.W, H: body.H - bar.H}
	p.g.targets = p.g.targets[:0]
	a.drawGridCards(fd, i, p, id, grid, moved)
	a.drawFilterBar(p, id, bar, chips, widths, xs, lines, n)
	a.drawGridDrag(p, id)
}

// drawFilterBar draws the chips that filter the grid, each with how many files it shows, placed in lines by
// drawGrid; a box to search names and tags; and how large the pictures are.
func (a *App) drawFilterBar(p *filesPane, id string, r gfx.Rect, chips []filterChip, widths, xs []float32, lines []int, n int) {
	u := &a.u
	u.c.Fill(r, colPanel)
	u.c.Fill(gfx.Rect{X: r.X, Y: r.Bottom() - max(1, u.px(1)), W: r.W, H: max(1, u.px(1))}, colBorder)
	y := r.Y + u.px(filterBarPad)
	right := r.Right() - u.px(8)
	if r.W > u.px(560) {
		sw := u.px(90)
		sr := gfx.Rect{X: right - sw, Y: y, W: sw, H: u.px(filterChipH)}
		if changed, _ := u.slider(id+"/size", sr, &p.g.size, gridSizeMin, gridSizeMax, true); changed {
			p.g.layoutKey = ""
		}
		u.hint(id+"/size", u.t("Size of the pictures (Ctrl+wheel)"), sr)
		right = sr.X - u.px(8)
	}
	searchW := min(u.px(200), r.W*0.34)
	search := gfx.Rect{X: right - searchW, Y: y, W: searchW, H: u.px(filterChipH)}
	if changed, _ := u.textBox(filterBoxID(p), search, &p.g.search, withKey(u.t("Search names and tags"), a.fileKeyHint("filter")), false); changed {
		p.g.scroll = 0
		a.arrange(p)
	}

	// The lines of chips, scrolled up and down by the wheel when there are more than show.
	line := u.px(filterChipH + filterChipGap)
	strip := gfx.Rect{X: r.X + u.px(8), Y: y, W: u.filterStrip(r.W), H: r.H - 2*u.px(filterBarPad)}
	hidden := float32(max(n-filterLinesMax, 0)) * line
	if hidden > 0 && u.hover(id+"/chips", strip) && u.wheel != 0 && !u.blocked {
		p.g.chipScroll -= u.wheel * line
		u.wheel = 0
	}
	p.g.chipScroll = min(max(p.g.chipScroll, 0), hidden)
	u.c.Push(strip)
	for k, c := range chips {
		cr := gfx.Rect{X: strip.X + xs[k], Y: strip.Y + float32(lines[k])*line - p.g.chipScroll, W: widths[k], H: u.px(filterChipH)}
		if cr.Bottom() < strip.Y || cr.Y > strip.Bottom() {
			continue
		}
		if a.drawFilterChip(p, id, strip, cr, c) {
			c.pick()
		}
		if c.count < 0 {
			u.hint(c.id, u.t("New category: drag files onto it to put them in"), intersect(cr, strip))
		}
	}
	u.c.Pop()
	// A line at the edge where more chips are hidden.
	if p.g.chipScroll < hidden {
		u.c.Fill(gfx.Rect{X: strip.X, Y: strip.Bottom() - u.px(1), W: strip.W, H: u.px(2)}, colBorderHi)
	}
	if p.g.chipScroll > 0 {
		u.c.Fill(gfx.Rect{X: strip.X, Y: strip.Y - u.px(1), W: strip.W, H: u.px(2)}, colBorderHi)
	}
}

// drawFilterChip draws a chip at cr, cut to the strip, as a target for dragged files when it takes them; it reports
// a click on it.
func (a *App) drawFilterChip(p *filesPane, id string, strip, cr gfx.Rect, c filterChip) bool {
	u := &a.u
	visible := intersect(cr, strip)
	hot := u.hover(c.id, visible)
	if c.drop != nil {
		p.g.targets = append(p.g.targets, dropTarget{id: c.id, name: c.name, rect: visible})
	}
	target := c.drop != nil && len(p.g.drag) > 0 && p.g.dragMoved && visible.Contains(u.mx, u.my)
	bg, fg := colSurface, colDim
	switch {
	case target:
		bg, fg = gfx.Mix(colSurface, colAccent, 0.35), colText
	case c.chosen:
		bg, fg = colSelected, colText
	case hot:
		bg, fg = colSurfaceHi, colText
		u.cursor = CursorHand
	}
	u.c.Fill(cr, bg)
	if c.chosen || target {
		u.c.Outline(cr, u.px(1.2), colAccent)
	}
	f := u.face(12)
	tx := cr.X + u.px(10)
	if c.glyph != "" {
		u.icon(c.glyph, gfx.Rect{X: tx - u.px(2), Y: cr.Y, W: u.px(18), H: cr.H}, 11, c.hue)
		tx += u.px(18)
	}
	u.c.Text(f, tx, cr.Y+(cr.H-f.Height())/2, c.label(), fg)
	if target && u.letGo(id+"/drag") {
		c.drop(p.g.drag)
		p.g.drag = nil
	}
	if c.menu != nil && u.rightAt != nil && visible.Contains(u.rightAt.X, u.rightAt.Y) && !u.blocked {
		a.openMenu(c.id+"/menu", gfx.Rect{X: u.rightAt.X, Y: u.rightAt.Y, W: 1, H: 1}, c.menu())
		u.rightAt = nil
	}
	return u.clicked(c.id, visible)
}

// filterBoxID is the id of a grid's search box.
func filterBoxID(p *filesPane) string { return fmt.Sprintf("files/filter/%p", p) }

// gridCountsOf counts the list's files for the chips: all of them, those with no tag or category, and each kind.
func (a *App) gridCountsOf(p *filesPane) gridCounts {
	key := fmt.Sprintf("%p|%d|%d|%v", p.entries, len(p.entries), a.assetLibVersion, a.settings.Files.HideHidden)
	if key == p.g.countsKey {
		return p.g.counts
	}
	lib := a.assetLibrary()
	c := gridCounts{kinds: map[assets.Kind]int{}}
	for k := range p.entries {
		fe := &p.entries[k]
		if a.settings.Files.HideHidden && fe.Hidden {
			continue
		}
		c.all++
		e := assetEntry(p, fe)
		if e.Kind == assets.Folder {
			continue
		}
		kind := e.Kind
		if a.animated(e) {
			kind = assets.Animation
		}
		c.kinds[kind]++
		it := lib.Of(e.Path)
		if len(it.Tags) == 0 {
			c.untagged++
		}
		if len(it.Categories) == 0 {
			c.uncategorized++
		}
	}
	p.g.counts, p.g.countsKey = c, key
	return c
}

// drawGridCards draws the cards that show, asks for their pictures, and handles the pointer and the wheel over
// them: pointing at a card puts the cursor on it, as pointing at a row does.
func (a *App) drawGridCards(fd *filesDoc, i int, p *filesPane, id string, r gfx.Rect, moved bool) {
	u := &a.u
	u.c.Fill(r, colPanel)
	pad := u.gridPad()
	inner := gfx.Rect{X: r.X + pad, Y: r.Y + pad, W: r.W - 2*pad - u.px(6), H: r.H - pad}
	p.g.viewH = inner.H
	if fd.at == i {
		fd.pageRows = 0 // a page is a grid's view, not a number of rows
	}
	if len(p.rows) == 0 {
		msg := u.t("This folder is empty")
		switch {
		case p.err != nil:
			msg = u.tf("This folder cannot be read: %v", p.err)
		case p.reading:
			msg = u.t("Reading…")
		case p.g.filter.what != showAll || strings.TrimSpace(p.g.search) != "":
			msg = u.t("Nothing here matches")
		case len(p.entries) > 0:
			msg = u.t("Only hidden files: press . to show them")
		}
		u.label(u.face(13), gfx.Rect{X: r.X + u.px(16), Y: r.Y + u.px(16), W: r.W - u.px(32), H: u.px(24)}, msg, colDim, 0.5)
		return
	}
	g := a.layoutGrid(p, inner.W)
	if u.hover(id+"/grid", r) && u.wheel != 0 && fd.prompt == nil {
		if u.ctrl {
			p.g.size = min(max(p.g.size*float64(1+0.1*u.wheel), gridSizeMin), gridSizeMax)
		} else {
			p.g.scroll -= u.wheel * u.px(90)
		}
		u.wheel = 0
	}
	s := fd.sweeping(u, p)
	if s != nil {
		var beyond float32
		if u.my < r.Y {
			beyond = (u.my - r.Y) / u.px(90)
		} else if u.my > r.Bottom() {
			beyond = (u.my - r.Bottom()) / u.px(90)
		}
		p.g.scroll += s.edgeScroll(beyond) * u.px(90)
	}
	p.g.scroll = min(max(p.g.scroll, 0), max(g.height-inner.H+pad, 0))
	active := fd.at == i
	u.c.Push(r)
	hit := -1
	for k, cr := range g.cards {
		card := gfx.Rect{X: inner.X + cr.X, Y: inner.Y + cr.Y - p.g.scroll, W: cr.W, H: cr.H}
		if card.Bottom() < r.Y || card.Y > r.Bottom() {
			continue
		}
		a.drawGridCard(p, k, card, active)
		if card.Contains(u.mx, u.my) && r.Contains(u.mx, u.my) && !u.blocked {
			hit = k
		}
	}
	u.c.Pop()
	// Pictures just below the view are asked for too, so scrolling finds them made.
	for k, cr := range g.cards {
		if y := inner.Y + cr.Y - p.g.scroll; y > r.Bottom() && y < r.Bottom()+r.H {
			a.thumbOf(assetEntry(p, &p.entries[p.rows[k]]))
		}
	}
	if fd.prompt == nil && hit >= 0 {
		if moved && active && u.active == "" {
			p.cursor = hit
		}
		fd.hovered = p.path(&p.entries[p.rows[hit]])
		if u.rightAt != nil && !u.blocked {
			a.startSweep(fd, i, p, hit)
		}
		if u.press(id+"/card", r) {
			a.clickCard(fd, p, hit)
		}
	} else if u.press(id+"/empty", r) && !u.ctrl && !u.shift {
		p.marked = map[string]bool{}
	}
	u.letGo(id + "/empty")
	if s := fd.sweeping(u, p); s != nil {
		a.sweepTo(fd, p, s, hit) // between cards the sweep keeps the card it reached
	}
	if u.dragging(id + "/card") {
		p.g.dragMoved = p.g.dragMoved || u.drag.moved
	}
	if u.active == id+"/card" && !u.down {
		u.active = id + "/drag" // let go: the chip under the pointer takes the files, else nothing does
	}
	if g.height > inner.H {
		h := max(r.H*r.H/(g.height+pad), u.px(24))
		y := r.Y + (r.H-h)*p.g.scroll/max(g.height-inner.H+pad, 1)
		u.c.Fill(gfx.Rect{X: r.Right() - u.px(5), Y: y, W: u.px(3), H: h}, colBorderHi)
	}
}

// clickCard puts the cursor on a card, as file managers select: Ctrl marks or unmarks it, Shift marks the cards
// from the cursor to it, a double click opens it. Pressing also starts a drag of what the commands act on.
func (a *App) clickCard(fd *filesDoc, p *filesPane, k int) {
	u := &a.u
	switch {
	case u.double:
		p.cursor = k
		a.enter(fd)
		u.active = ""
		return
	case u.ctrl:
		a.toggleMarkAt(p, k)
	case u.shift:
		for j := min(k, p.cursor); j <= max(k, p.cursor); j++ {
			p.marked[p.entries[p.rows[j]].Name] = true
		}
	}
	p.cursor = k
	p.g.drag, p.g.dragMoved = p.selected(), false
	u.drag.x, u.drag.y, u.drag.moved = u.mx, u.my, false
}

// drawGridDrag draws the files being dragged by the pointer, and ends the drag where it was let go.
func (a *App) drawGridDrag(p *filesPane, id string) {
	u := &a.u
	if u.active == id+"/drag" {
		u.active = "" // let go anywhere but on a category or tag
	}
	if u.active != id+"/card" || !p.g.dragMoved || len(p.g.drag) == 0 {
		if u.active == "" {
			p.g.drag = nil
		}
		return
	}
	u.cursor = CursorMove
	text := a.countFiles(p.g.drag)
	for _, t := range p.g.targets {
		if t.rect.Contains(u.mx, u.my) {
			text += "  →  " + t.name
		}
	}
	f := u.bold(12)
	w := f.Measure(text) + u.px(36)
	chip := gfx.Rect{X: u.mx + u.px(14), Y: u.my + u.px(10), W: w, H: u.px(28)}
	u.c.RoundRect(chip, u.px(6), colAccent)
	u.icon(icCategory, gfx.Rect{X: chip.X + u.px(4), Y: chip.Y, W: u.px(24), H: chip.H}, 12, colOnPrimary)
	u.label(f, gfx.Rect{X: chip.X + u.px(26), Y: chip.Y, W: w - u.px(30), H: chip.H}, text, colOnPrimary, 0)
}

// drawGridCard draws the card of row k: its picture, its name and details under it, the cursor around it and a
// mark when it is marked.
func (a *App) drawGridCard(p *filesPane, k int, card gfx.Rect, active bool) {
	u := &a.u
	fe := &p.entries[p.rows[k]]
	e := assetEntry(p, fe)
	label := u.assetLabelH()
	pic := gfx.Rect{X: card.X, Y: card.Y, W: card.W, H: card.H - label}
	busy := a.busyJob(e.Path)
	var info assets.Info
	switch {
	case p.atDrives():
		u.c.RoundRect(pic, u.px(6), colSurface)
		u.icon(icFmDrive, pic, min(pic.W, pic.H)*0.35/u.s, colDim)
		if d := p.drives[fe.Name]; d.Total > 0 {
			used := float32(d.Total-d.Free) / float32(d.Total)
			bar := gfx.Rect{X: pic.X + u.px(12), Y: pic.Bottom() - u.px(16), W: pic.W - u.px(24), H: u.px(6)}
			u.c.Fill(bar, colPanel)
			col := colAccent
			if used > 0.9 {
				col = colDanger
			}
			u.c.Fill(gfx.Rect{X: bar.X, Y: bar.Y, W: bar.W * used, H: bar.H}, col)
		}
	case e.Kind == assets.Folder:
		u.c.RoundRect(pic, u.px(6), gfx.Mix(colSurface, gfx.Hex(0xe8b04b), 0.06))
		glyph := icFolder
		if fe.Link {
			glyph = icFmLink
		}
		u.icon(glyph, pic, min(pic.W, pic.H)*0.4/u.s, gfx.Hex(0xe8b04b))
	default:
		img, inf, ready := a.thumbOf(e)
		info = inf
		u.c.RoundRect(pic, u.px(6), colSurface)
		u.c.Push(pic)
		a.drawThumbIn(pic, a.fileThumbs.items[e.Path], img, info, ready, e.Kind)
		u.c.Pop()
		a.drawBadges(pic, e, info)
	}
	if busy != nil || fe.Hidden || a.inClip(e.Path) && a.fm.clip.cut {
		u.c.RoundRect(pic, u.px(6), gfx.Alpha(colPanel, 0.55))
	}
	marked := p.marked[fe.Name]
	if marked {
		u.c.RoundRect(pic, u.px(6), gfx.Alpha(colAccent, 0.18))
		tick := gfx.Rect{X: pic.X + u.px(8), Y: pic.Bottom() - u.px(28), W: u.px(20), H: u.px(20)}
		u.c.RoundRect(tick, u.px(10), colAccent)
		u.icon(icCheck, tick, 12, colOnPrimary)
	}
	cursor := k == p.cursor
	switch {
	case cursor && active:
		u.c.RoundOutline(pic.Inset(-u.px(3)), u.px(8), u.px(2.5), colAccent)
	case cursor:
		u.c.RoundOutline(pic.Inset(-u.px(2)), u.px(7), u.px(1.5), colBorderHi)
	}
	nameCol := colText
	if cursor && active {
		nameCol = colAccentHi
	}
	if fe.Hidden || busy != nil {
		nameCol = colDim
	}
	nameR := gfx.Rect{X: card.X + u.px(2), Y: pic.Bottom() + u.px(4), W: card.W - u.px(4), H: u.px(16)}
	if pr := a.files.prompt; pr != nil && pr.kind == promptRename && cursor && active {
		a.drawRenameBox(a.files, gfx.Rect{X: card.X, Y: pic.Bottom() + u.px(2), W: card.W, H: u.px(30)})
	} else {
		u.label(u.face(12), nameR, fe.Name, nameCol, 0.5)
	}
	details := a.cardDetails(p, fe, e, info)
	if busy != nil {
		details = busy.state(u)
	}
	u.label(u.face(10.5), gfx.Rect{X: card.X + u.px(2), Y: pic.Bottom() + u.px(20), W: card.W - u.px(4), H: u.px(14)}, details, colFaint, 0.5)
	// A mark when it has tags, as a glance tells.
	if it := a.assetLibrary().Of(e.Path); len(it.Tags) > 0 && e.Kind != assets.Folder {
		dot := gfx.Rect{X: pic.X + u.px(8), Y: pic.Y + u.px(8), W: u.px(18), H: u.px(18)}
		u.c.RoundRect(dot, u.px(9), gfx.Alpha(colBlack, 0.6))
		u.icon(icLabel, dot, 10, gfx.Hex(0x9fa8ff))
	}
}

// drawBadges marks a card with its format and a video's length, as Eagle does.
func (a *App) drawBadges(pic gfx.Rect, e assets.Entry, info assets.Info) {
	u := &a.u
	f := u.bold(9.5)
	pad := u.px(6)
	ext := strings.ToUpper(strings.TrimPrefix(filepath.Ext(e.Name), "."))
	if ext != "" && pic.W > u.px(70) {
		w := f.Measure(ext) + u.px(10)
		br := gfx.Rect{X: pic.Right() - w - pad, Y: pic.Y + pad, W: w, H: u.px(16)}
		u.c.RoundRect(br, u.px(4), gfx.Alpha(colBlack, 0.6))
		u.label(f, br, ext, colText, 0.5)
	}
	if info.Duration > 0 && (e.Kind == assets.Video || e.Kind == assets.Edit || e.Kind == assets.Audio || info.Animated) {
		text := mediaLength(info.Duration)
		w := f.Measure(text) + u.px(10)
		br := gfx.Rect{X: pic.Right() - w - pad, Y: pic.Bottom() - u.px(16) - pad, W: w, H: u.px(16)}
		u.c.RoundRect(br, u.px(4), gfx.Alpha(colBlack, 0.6))
		u.label(f, br, text, colText, 0.5)
	}
}

// cardDetails is the line under a card's name: its size in pixels or its length, and its size on disk; for a
// folder, what its analysis found.
func (a *App) cardDetails(p *filesPane, fe *files.Entry, e assets.Entry, info assets.Info) string {
	u := &a.u
	if p.atDrives() {
		if d := p.drives[fe.Name]; d.Total > 0 {
			return u.tf("%s free", sizeText(d.Free)) + " · " + sizeText(d.Total)
		}
		return u.t("No disc or card")
	}
	if e.Kind == assets.Folder {
		if n := a.paneAnalysis(p); n != nil {
			if c := n.Child(fe.Name); c != nil {
				return sizeText(c.Size)
			}
		}
		return u.t("Folder")
	}
	var parts []string
	if info.W > 0 && info.H > 0 {
		parts = append(parts, fmt.Sprintf("%d×%d", info.W, info.H))
	} else if d, ok := p.g.dims[fe.Name]; ok {
		parts = append(parts, fmt.Sprintf("%d×%d", d.X, d.Y))
	}
	if info.Duration > 0 && (e.Kind == assets.Audio || e.Kind == assets.Video) && len(parts) == 0 {
		parts = append(parts, shortClock(info.Duration))
	}
	parts = append(parts, assets.FormatSize(e.Size))
	return strings.Join(parts, " · ")
}

// mediaLength writes a length for a badge: tenths of a second when it is short, as a brief animation is.
func mediaLength(seconds float64) string {
	if seconds < 10 {
		return fmt.Sprintf("%.1fs", seconds)
	}
	return shortClock(seconds)
}

// gridCursorRow moves the cursor of a grid a row of cards down (by > 0) or up, to the card whose middle is
// nearest the one it is on, as the arrow keys move in a grid of pictures.
func (a *App) gridCursorRow(fd *filesDoc, p *filesPane, by int) {
	g := p.g.layout
	if len(p.rows) == 0 || len(g.cards) != len(p.rows) {
		return
	}
	at := min(max(p.cursor, 0), len(p.rows)-1)
	for range max(by, -by) {
		cur := g.cards[at]
		mid := cur.X + cur.W/2
		best, bestD := at, float32(-1)
		for k, cr := range g.cards {
			if by > 0 && cr.Y <= cur.Y+1 || by < 0 && cr.Y >= cur.Y-1 {
				continue
			}
			d := abs32(cr.Y-cur.Y)*1000 + abs32(cr.X+cr.W/2-mid)
			if bestD < 0 || d < bestD {
				best, bestD = k, d
			}
		}
		if best == at {
			// No row beyond: the first or last card, as a list goes to its end.
			if by > 0 {
				best = len(p.rows) - 1
			} else {
				best = 0
			}
		}
		at = best
	}
	p.cursor = at
	a.keepInView(fd, p)
}

// gridPageRows is how many rows of cards a grid's view holds, for paging.
func (a *App) gridPageRows(p *filesPane) int {
	rowH := a.u.px(float32(p.g.size)) + a.u.assetLabelH() + a.u.px(12)
	return max(int(p.g.viewH/rowH), 1)
}

// revealCard scrolls a grid so its cursor shows.
func (a *App) revealCard(p *filesPane) {
	g := p.g.layout
	if p.cursor < 0 || p.cursor >= len(g.cards) {
		return
	}
	cr := g.cards[p.cursor]
	switch {
	case cr.Y < p.g.scroll:
		p.g.scroll = cr.Y
	case cr.Bottom() > p.g.scroll+p.g.viewH:
		p.g.scroll = cr.Bottom() - p.g.viewH
	}
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// categoryMenu is the menu of a category's chip.
func (a *App) categoryMenu(fd *filesDoc, name string) []menuItem {
	u := &a.u
	lib := a.assetLibrary()
	return []menuItem{
		{label: u.t("Rename"), run: func() { a.openLabelRename(fd, true, name) }},
		{label: u.t("Delete the category (the files stay)"), run: func() {
			if err := lib.DeleteCategory(name); err != nil {
				a.toast(a.describe(err), true)
			}
			a.assetLibVersion++
			a.dropFilter(showCategory, name)
		}},
	}
}

// tagMenu is the menu of a tag's chip.
func (a *App) tagMenu(fd *filesDoc, name string) []menuItem {
	u := &a.u
	lib := a.assetLibrary()
	return []menuItem{
		{label: u.t("Rename"), run: func() { a.openLabelRename(fd, false, name) }},
		{label: u.t("Delete the tag from every file"), run: func() {
			if err := lib.DeleteTag(name); err != nil {
				a.toast(a.describe(err), true)
			}
			a.assetLibVersion++
			a.dropFilter(showTag, name)
		}},
	}
}

// dropFilter shows everything again in the grids filtered by a category or tag that is gone.
func (a *App) dropFilter(what filterWhat, name string) {
	for _, d := range a.docs {
		if d.files == nil {
			continue
		}
		for _, p := range d.files.panes {
			if p.g.filter.what == what && strings.EqualFold(p.g.filter.name, name) {
				a.setFilter(p, assetFilter{})
			}
		}
	}
}

// renameFilter follows a category or tag renamed in the grids filtered by it.
func (a *App) renameFilter(what filterWhat, old, name string) {
	for _, d := range a.docs {
		if d.files == nil {
			continue
		}
		for _, p := range d.files.panes {
			if p.g.filter.what == what && strings.EqualFold(p.g.filter.name, old) {
				p.g.filter.name = name
			}
		}
	}
}
