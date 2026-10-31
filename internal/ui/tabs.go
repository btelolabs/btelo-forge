package ui

import (
	"fmt"
	"image/color"
	"path/filepath"
	"slices"

	"github.com/btelolabs/btelo-forge/internal/gfx"
)

// drawTabs is the browser-like strip: one tab per edit, a button for a new tab, and settings at the end. It runs along
// the top, or down the left side when the settings say so; a tab does the same either way.
func (a *App) drawTabs(r gfx.Rect) {
	u := &a.u
	u.c.Fill(r, colBackground)
	vertical := a.settings.VerticalTabs
	// Every button in the strip is the same size and sits on the tabs' own centre line.
	top := r.Y + u.px(6)
	gear := gfx.Rect{X: r.Right() - u.px(40), Y: top + (r.H-top-u.px(30))/2, W: u.px(32), H: u.px(30)}
	if vertical {
		gear = gfx.Rect{X: r.X + u.px(6), Y: r.Bottom() - u.px(36), W: r.W - u.px(12), H: gear.H}
	}
	// A newer version to install, or one installed to start, marks the button, which then opens About.
	waiting := a.updateWaiting()
	hint := u.t("Settings")
	if waiting {
		hint = u.t("Settings: a new version is ready")
	}
	var opened bool
	if vertical {
		opened = a.drawStripRow("tabs/settings", gear, icSettings, u.t("Settings"), hint, a.settingsOpen, a.export == nil)
	} else {
		opened = u.iconButton("tabs/settings", gear, icSettings, hint, a.settingsOpen, a.export == nil)
	}
	if opened {
		a.settingsOpen = true
		if waiting {
			a.settingsPage = settingsAboutPage
		}
	}
	if waiting {
		u.c.Circle(gear.Right()-u.px(6), gear.Y+u.px(6), u.px(3.5), colAccent)
	}
	// Along the top the recording and the stream sit before the settings; down the side, each on a line above them.
	end, bottom := gear.X, gear.Y
	rowH := gear.H + u.px(4)
	if vertical {
		// A control ends 4 before the end it is given, and these are as wide as the rows.
		recEnd, liveEnd := gear.Right()+u.px(8), gear.Right()+u.px(4)
		if a.drawRecordControl(recEnd, bottom-rowH, gear.H, gear.W) != recEnd {
			bottom -= rowH
		}
		if a.drawLiveControl(liveEnd, bottom-rowH, gear.H) != liveEnd {
			bottom -= rowH
		}
	} else {
		end = a.drawLiveControl(a.drawRecordControl(gear.X, gear.Y, gear.H, u.px(232)), gear.Y, gear.H)
	}
	if a.p != nil {
		watchHint := u.tf("Player mode (%s)", a.commandKey("video.watch"))
		var watching bool
		if vertical {
			bottom -= rowH
			watching = a.drawStripRow("tabs/watch", gfx.Rect{X: gear.X, Y: bottom, W: gear.W, H: gear.H}, icFullscreen, u.t("Player mode"), watchHint, false, true)
		} else {
			watch := gfx.Rect{X: end - u.px(36), Y: gear.Y, W: gear.W, H: gear.H}
			watching = u.iconButton("tabs/watch", watch, icFullscreen, watchHint, false, true)
			end = watch.X
		}
		if watching {
			a.setWatching(true)
		}
	}
	plusW := u.px(32)
	// first and step are where the tabs start and how far apart they are, across the strip or down it.
	var first, step float32
	var place func(at float32) gfx.Rect
	if vertical {
		// The row for a new tab takes the place of one more tab.
		first = top
		step = min(u.px(36), (bottom-u.px(4)-top)/float32(len(a.docs)+1))
		place = func(at float32) gfx.Rect {
			return gfx.Rect{X: r.X + u.px(6), Y: at, W: r.W - u.px(12), H: step - u.px(2)}
		}
	} else {
		first = r.X + u.px(8)
		space := end - u.px(8) - r.X - u.px(8) - plusW - u.px(4)
		step = min(u.px(220), space/float32(len(a.docs)))
		place = func(at float32) gfx.Rect {
			return gfx.Rect{X: at, Y: top, W: step - u.px(2), H: r.Bottom() - top}
		}
	}
	slot := func(i int) float32 { return first + float32(i)*step }
	closeAt, unpin := -1, (*doc)(nil)
	draw := func(i int, tr gfx.Rect) {
		closing, unpinning := a.drawTab(i, tr)
		if closing {
			closeAt = i
		}
		if unpinning {
			unpin = a.docs[i]
		}
	}
	held := -1
	if a.tabDrag != nil {
		held = slices.Index(a.docs, a.tabDrag.d)
	}
	for i, d := range a.docs {
		d.tabShift = settleTab(d.tabShift, u.px(0.5))
		if i != held {
			draw(i, place(slot(i)+d.tabShift))
		}
	}
	if held >= 0 {
		// Over the others, and following the pointer once it has moved, within the tabs pinned or not as it is.
		at := slot(held)
		if a.tabDrag.moved {
			pointer := u.mx
			if vertical {
				pointer = u.my
			}
			lo, hi := a.tabGroup(held)
			at = max(min(pointer-a.tabDrag.grab, slot(hi)), slot(lo))
		}
		draw(held, place(at))
		a.dragTab(held, at, first, step)
	} else if a.tabDrag != nil && !slices.Contains(a.docs, a.tabDrag.d) {
		a.tabDrag = nil // closed while it was held
	}
	newHint := u.t("New tab (Ctrl+Shift+T)")
	var adding bool
	if vertical {
		adding = a.drawStripRow("tabs/new", place(slot(len(a.docs))), icAdd, u.t("New tab"), newHint, false, true)
	} else {
		plus := gfx.Rect{X: slot(len(a.docs)) + u.px(4), Y: gear.Y, W: plusW, H: gear.H}
		adding = u.iconButton("tabs/new", plus, icAdd, newHint, false, true)
	}
	if adding {
		a.newTab()
	}
	if closeAt >= 0 {
		a.closeTab(closeAt)
	}
	if unpin != nil {
		a.pinTab(unpin, false)
	}
	edge := max(1, u.px(1))
	if vertical {
		u.c.Fill(gfx.Rect{X: r.Right() - edge, Y: r.Y, W: edge, H: r.H}, colPanel)
	} else {
		u.c.Fill(gfx.Rect{X: r.X, Y: r.Bottom() - edge, W: r.W, H: edge}, colPanel)
	}
}

// drawStripRow is a button down the side strip, a whole row with its symbol and name where a tab has them, and reports a click.
func (a *App) drawStripRow(id string, r gfx.Rect, glyph, name, hint string, on, enabled bool) bool {
	u := &a.u
	hot := enabled && u.hover(id, r)
	fg := colDim
	switch {
	case !enabled:
		fg = colFaint
	case on:
		u.c.Fill(r, colPanel)
		fg = colText
	case hot:
		u.c.Fill(r, colSurface)
		fg = colText
	}
	if hot {
		u.cursor = CursorHand
	}
	u.icon(glyph, gfx.Rect{X: r.X + u.px(6), Y: r.Y, W: u.px(24), H: r.H}, 16, fg)
	u.label(u.face(12.5), gfx.Rect{X: r.X + u.px(34), Y: r.Y, W: r.W - u.px(40), H: r.H}, name, fg, 0)
	u.hint(id, hint, r)
	return enabled && u.clicked(id, r)
}

// tabID names tab d's controls: it follows the tab when it moves along the strip.
func tabID(d *doc) string { return fmt.Sprintf("tabs/%p", d) }

// drawTab draws tab i in tr, and reports whether its close or pin button was clicked. A press on it holds it, to be
// dragged along the strip or let go to come forward (dragTab).
func (a *App) drawTab(i int, tr gfx.Rect) (closing, unpin bool) {
	u := &a.u
	d := a.docs[i]
	id := tabID(d)
	active := d == a.doc
	hot := u.hover(id, tr) || a.tabDrag != nil && a.tabDrag.d == d && a.tabDrag.moved
	bg := colBackground
	switch {
	case active:
		bg = colPanel
	case hot:
		bg = colSurface
	}
	face := tr
	if !a.settings.VerticalTabs {
		face.H += u.px(10) // running into the page below, as the tab in front does
	}
	u.c.Fill(face, bg)
	fg := colDim
	if active {
		fg = colText
	}
	u.icon(d.glyph(), gfx.Rect{X: tr.X + u.px(6), Y: tr.Y, W: u.px(24), H: tr.H}, 16, fg)
	cb := gfx.Rect{X: tr.Right() - u.px(30), Y: tr.Y + (tr.H-u.px(24))/2, W: u.px(24), H: u.px(24)}
	nameBox := gfx.Rect{X: tr.X + u.px(34), Y: tr.Y, W: cb.X - tr.X - u.px(38), H: tr.H}
	if a.renamingTab == d {
		// Its name being typed: kept when Enter is pressed or the box is left.
		box := gfx.Rect{X: nameBox.X, Y: nameBox.Y + u.px(4), W: nameBox.W, H: nameBox.H - u.px(8)}
		if _, done := u.textBox("tabs/rename", box, &a.tabNameText, "", false); done || u.focus != "tabs/rename" {
			a.renamingTab = nil
			a.renameTab(d, a.tabNameText)
		}
	} else {
		u.label(u.face(12.5), nameBox, d.title(a), fg, 0)
	}
	if d.renameable() && u.double && u.pressedIn(nameBox) {
		// A double-click names it.
		a.switchTab(i)
		a.renamingTab, a.tabNameText = d, a.tabName(d)
		u.focusEdit("tabs/rename", a.tabNameText, false)
	}
	// Coloured like the strip's other buttons: dim until pointed at.
	button := func(glyph, id string, size float32) bool {
		cfg := colDim
		if u.hover(id, cb) {
			u.c.Fill(cb, colSurfaceHi)
			u.cursor = CursorHand
			cfg = colText
		}
		u.icon(glyph, cb, size, cfg)
		return u.clicked(id, cb)
	}
	switch {
	case d.pinned:
		// A pinned tab has no close button to catch a slip; it closes from its menu or by the key.
		unpin = button(icPin, id+"/unpin", 14)
		u.hint(id+"/unpin", u.t("Unpin the tab"), cb)
	case hot || active:
		closing = button(icClose, id+"/close", 16)
	}
	if d.p != nil {
		u.hint(id, d.p.Sources[0].Path, tr)
	} else if name := d.title(a); u.face(12.5).Measure(name) > nameBox.W {
		u.hint(id, name, tr) // cut short in its tab
	}
	if at := u.rightAt; at != nil && tr.Contains(at.X, at.Y) && !u.blocked {
		a.openMenu("tabs/menu", gfx.Rect{X: at.X, Y: at.Y, W: 1, H: 1}, a.tabMenu(d))
		u.rightAt = nil
	}
	if u.press(id, tr) {
		grab := u.pressX - tr.X
		if a.settings.VerticalTabs {
			grab = u.pressY - tr.Y
		}
		a.tabDrag = &tabDrag{d: d, grab: grab}
	}
	return closing, unpin
}

// startCard is one of the kinds of work a new tab offers, on an equal footing with those of its group.
type startCard struct {
	id, glyph, title, text, action string
	hue                            color.RGBA
	enabled                        bool
	hint                           string
	run                            func()
	// extra is a second, symbol-only action in the card's corner, when it has one.
	extra *startExtra
	group int // its place in startGroups
}

// startGroups head the groups of cards, in the order of the categories.
var startGroups = msgs("General tools", "Creative", "Documents", "Intelligence")

// startExtra is a card's second action.
type startExtra struct {
	id, glyph, hint string
	enabled         bool
	run             func()
}

// startCards are the new tab's kinds of work, group by group.
func (a *App) startCards() []startCard {
	u := &a.u
	recordHint := ""
	if a.recorder() == nil {
		recordHint = u.t("Screen recording works on Windows")
	} else if reason, _ := a.recordBlocker(); reason != "" {
		recordHint = u.tf("Screen recording is not available: %s", reason)
	}
	cards := []startCard{
		{id: "empty/resources", glyph: icFmFiles, hue: gfx.Hex(0xe8b75b), title: u.t("Manage resources"),
			text:   u.t("Browse, tag and sort files, and see what takes the space."),
			action: u.t("Open the resource manager"), enabled: true, run: func() { a.openResourceManager() }},
		{id: "empty/record", glyph: icRecord, hue: gfx.Hex(0xf0605d), title: u.t("Record the screen"),
			text:   u.t("The screen with sound and microphone, or a screenshot."),
			action: u.t("Record…"), enabled: a.recorder() != nil && !a.recording() && a.export == nil, hint: recordHint,
			run: a.openRecorder,
			extra: &startExtra{id: "empty/screenshot", glyph: icCamera,
				hint:    withKey(u.t("Take a screenshot of any screen"), a.hotkeyLabel(hotkeyScreenshotFile)),
				enabled: a.recorder() != nil && !a.rec.shooting && a.over.pick == nil, run: func() { a.screenshotRegion(shotFile) }}},
		a.liveCard(),
		{id: "empty/keyboard", glyph: icKeyboard, hue: gfx.Hex(0x6fcf97), title: u.t("Remap the keyboard"),
			text:   u.t("Remap keys, open programs and expand abbreviations."),
			action: u.t("Open the keyboard tool"), enabled: true, run: a.openKeyboardTab},
	}
	for _, c := range []startCard{
		{id: "empty/video", glyph: icVideo, hue: gfx.Hex(0x5aa9ff), title: u.t("Edit a video"),
			text:   u.t("Cut, add music and subtitles, and export at any size."),
			action: u.t("Start editing"), enabled: a.opening == "", run: a.newEditor},
		{id: "empty/canvas", glyph: icBrush, hue: gfx.Hex(0xf2994a), title: u.t("Paint and draw"),
			text:   u.t("Layers, brushes and vectors on an infinite canvas."),
			action: u.t("New canvas"), enabled: true, run: a.newCanvasNow},
		{id: "empty/mind", glyph: icMind, hue: gfx.Hex(0x4fb3ff), title: u.t("Think in mind maps"),
			text:   u.t("Mind maps and flowcharts on one infinite board."),
			action: u.t("New mind map"), enabled: true, run: a.newMindNow},
		{id: "empty/model", glyph: icModel, hue: modelHue, title: u.t("Make 3D assets"),
			text:   u.t("View models, with materials, skies and shadows."),
			action: u.t("Open the 3D tool"), enabled: true, run: a.openModelTab},
	} {
		c.group = categoryCreative
		cards = append(cards, c)
	}
	for _, c := range a.officeCards() {
		c.group = categoryDocuments
		cards = append(cards, c)
	}
	for _, c := range []startCard{
		{id: "empty/ai", glyph: icAI, hue: gfx.Hex(0xb48cff), title: u.t("Local models"),
			text:   u.t("Chat with AI models on this computer, or online."),
			action: u.t("Open the chat"), enabled: true, run: a.openAITab},
		{id: "empty/mail", glyph: icMail, hue: gfx.Hex(0x6aa9ff), title: u.t("Mail"),
			text:   u.t("Bind your mailboxes and have a local model sort them."),
			action: u.t("Open mail"), enabled: true, run: a.openMailTab},
	} {
		c.group = categoryIntelligence
		cards = append(cards, c)
	}
	return cards
}

// startPage is where the new tab puts its parts.
type startPage struct {
	head    gfx.Rect   // the logo, the name and the line beside them, with Open a file at the end
	labels  []gfx.Rect // the heading of each group with cards
	groups  []int      // and which group it is
	cards   []gfx.Rect // one per start card
	stacked bool       // the cards are one above another, in a narrow window
	foot    gfx.Rect   // the reminder that files can be dropped
}

// startCardH is a card's height: its symbol, or its title and two lines of text.
func (u *ctx) startCardH() float32 { return u.px(88) }

// startLayout places the head, the groups of cards and the footer, centred in r: as many cards in a row as fit, four
// at most, so that a group reads as a row of its own, and one above another where not even two fit.
func (a *App) startLayout(r gfx.Rect, cards []startCard) startPage {
	u := &a.u
	var p startPage
	gap := u.px(12)
	w := min(u.px(1120), r.W-u.px(48))
	cols := min(max(int((w+gap)/(u.px(220)+gap)), 1), 4)
	cw := (w - float32(cols-1)*gap) / float32(cols)
	p.stacked = cols < 2
	ch := u.startCardH()
	headH, labelH := u.px(48), u.px(26)
	groupGap, footH := u.px(22), u.px(44)
	if status, _ := a.toolsStatus(); status != "" {
		footH += u.px(70)
	}
	left := r.X + (r.W-w)/2
	p.cards = make([]gfx.Rect, len(cards))
	y := headH
	for g := range startGroups {
		var members []int
		for i, c := range cards {
			if c.group == g {
				members = append(members, i)
			}
		}
		if len(members) == 0 {
			continue
		}
		y += groupGap
		p.groups = append(p.groups, g)
		p.labels = append(p.labels, gfx.Rect{X: left, Y: y, W: w, H: labelH})
		y += labelH
		for k, i := range members {
			p.cards[i] = gfx.Rect{X: left + float32(k%cols)*(cw+gap), Y: y + float32(k/cols)*(ch+gap), W: cw, H: ch}
		}
		rows := (len(members) + cols - 1) / cols
		y += float32(rows)*ch + float32(rows-1)*gap
	}
	// Laid out from zero, the page is then centred.
	top := r.Y + max(u.px(24), (r.H-y-footH)/2)
	p.head = gfx.Rect{X: left, Y: top, W: w, H: headH}
	for i := range p.labels {
		p.labels[i].Y += top
	}
	for i := range p.cards {
		p.cards[i].Y += top
	}
	y += top
	p.foot = gfx.Rect{X: left, Y: y + u.px(12), W: w, H: u.px(32)}
	return p
}

// drawEmpty is a new tab: the kinds of work as groups of equal cards, with a way to open any file and a reminder that
// files can be dropped anywhere.
func (a *App) drawEmpty(r gfx.Rect) {
	a.u.c.Fill(r, colPanel)
	// In a window too low for every card the page scrolls.
	a.scrolledBy("empty/scroll", r, &a.startScroll, &a.startContent, func(view gfx.Rect) float32 {
		return a.drawStartPage(r, view.Y-r.Y)
	})
}

// drawStartPage draws the new tab laid out in r and moved down by dy (up, when scrolled), and returns its bottom.
func (a *App) drawStartPage(r gfx.Rect, dy float32) float32 {
	u := &a.u
	cards := a.startCards()
	p := a.startLayout(r, cards)
	p.head.Y += dy
	p.foot.Y += dy
	for i := range p.cards {
		p.cards[i].Y += dy
	}
	bottom := p.foot.Bottom()

	logo := gfx.Rect{X: p.head.X, Y: p.head.Y, W: u.px(44), H: u.px(44)}
	drawLogo(u, logo)
	open := u.t("Open a file…")
	ow := u.buttonWidth(icFolder, open)
	if u.button("empty/open", gfx.Rect{X: p.head.Right() - ow, Y: p.head.Y + u.px(6), W: ow, H: u.px(32)}, icFolder, open, ghost, a.opening == "" && a.export == nil) {
		a.chooseAnyFile()
	}
	tx, tw := logo.Right()+u.px(14), p.head.W-logo.W-ow-u.px(26)
	u.label(u.bold(18), gfx.Rect{X: tx, Y: p.head.Y - u.px(1), W: tw, H: u.px(26)}, "Btelo Forge", colText, 0)
	sub := u.t("What would you like to make?")
	if a.opening != "" {
		sub = u.tf("Opening %s …", filepath.Base(a.opening))
	}
	u.label(u.face(12.5), gfx.Rect{X: tx, Y: p.head.Y + u.px(24), W: tw, H: u.px(20)}, sub, colDim, 0)

	for i, l := range p.labels {
		u.label(u.bold(12), gfx.Rect{X: l.X, Y: l.Y + dy, W: l.W, H: u.px(18)}, u.t(startGroups[p.groups[i]]), colFaint, 0)
	}
	for i, c := range cards {
		a.drawStartCard(c, p.cards[i])
	}

	if status, failed := a.toolsStatus(); status != "" {
		col := colDim
		if failed {
			col = colDangerText
		}
		line := gfx.Rect{X: p.foot.X, Y: p.foot.Bottom() + u.px(18), W: p.foot.W, H: u.px(20)}
		u.label(u.face(12), line, status, col, 0.5)
		if a.tools.downloading {
			bar := gfx.Rect{X: line.X + line.W/4, Y: line.Bottom() + u.px(8), W: line.W / 2, H: u.px(4)}
			u.c.Fill(bar, colSurface)
			u.c.Fill(gfx.Rect{X: bar.X, Y: bar.Y, W: max(bar.W*float32(a.tools.fraction), bar.H), H: bar.H}, colAccent)
		}
		bottom = line.Bottom() + u.px(12)
		if failed {
			retry := gfx.Rect{X: r.X + r.W/2 - u.px(60), Y: line.Bottom() + u.px(10), W: u.px(120), H: u.px(32)}
			if u.button("empty/retry", retry, "", u.t("Retry"), plain, true) {
				a.prepareTools()
			}
			bottom = retry.Bottom()
		}
	}
	return bottom
}

// drawStartCard draws one kind of work as a card the whole of which can be clicked: its symbol in its own colour, what
// it is and what it does; what clicking does shows as the card's tip.
func (a *App) drawStartCard(c startCard, box gfx.Rect) {
	u := &a.u
	hot := c.enabled && u.hover(c.id, box)
	bg, edge := colSurface, colBorder
	if hot {
		bg, edge = colSurfaceHi, gfx.Mix(colBorderHi, c.hue, 0.6)
		u.cursor = CursorHand
	}
	if u.active == c.id && c.enabled {
		bg = gfx.Mix(bg, colBlack, 0.12)
	}
	u.c.Fill(box, bg)
	u.c.Outline(box, max(1, u.px(1)), edge)
	hue, fg := c.hue, colText
	if !c.enabled {
		hue, fg = gfx.Mix(colSurface, c.hue, 0.45), colFaint
	}

	pad := u.px(16)
	tile := gfx.Rect{X: box.X + pad, Y: box.Y + (box.H-u.px(40))/2, W: u.px(40), H: u.px(40)}
	u.c.Fill(tile, gfx.Mix(bg, c.hue, 0.14))
	u.icon(c.glyph, tile, 22, hue)

	tx := tile.Right() + u.px(14)
	titleW := box.Right() - pad - tx
	if c.extra != nil {
		titleW -= u.px(32)
	}
	tf := u.face(12)
	lines := tf.Wrap(c.text, box.Right()-pad-tx)
	lines = lines[:min(len(lines), 2)]
	lineH := u.px(17)
	ty := box.Y + (box.H-u.px(24)-float32(len(lines))*lineH)/2
	u.label(u.bold(14), gfx.Rect{X: tx, Y: ty, W: titleW, H: u.px(22)}, c.title, fg, 0)
	for i, line := range lines {
		u.c.Text(tf, tx, ty+u.px(24)+float32(i)*lineH+(lineH-tf.Height())/2, line, colDim)
	}

	if e := c.extra; e != nil {
		// The second action sits in the corner; it is drawn, and so takes the click, before the card.
		er := gfx.Rect{X: box.Right() - u.px(36), Y: box.Y + u.px(8), W: u.px(28), H: u.px(28)}
		ehot := e.enabled && u.hover(e.id, er)
		ecol := colDim
		if !e.enabled {
			ecol = colFaint
		}
		if ehot {
			u.c.Fill(er, colSelected)
			ecol = colText
			u.cursor = CursorHand
		}
		u.icon(e.glyph, er, 15, ecol)
		u.hint(e.id, e.hint, er)
		if e.enabled && u.clicked(e.id, er) {
			e.run()
		}
	}

	tip := c.hint
	if tip == "" {
		tip = c.action
	}
	u.hint(c.id, tip, box)
	if c.enabled && u.clicked(c.id, box) {
		c.run()
	}
}
