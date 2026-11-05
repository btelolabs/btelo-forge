package ui

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"image/color"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/mail"
)

const (
	icMail    = ""
	icArchive = ""
	icSync    = ""
)

// The scroll areas of the tab, by place in mailState.scroll.
const (
	mailScrollSide = iota
	mailScrollList
	mailScrollReader
	mailScrollForm
)

// openMailTab shows the mail tab, of which there is one at most.
func (a *App) openMailTab() {
	for i, d := range a.docs {
		if d.mailTab {
			a.switchTab(i)
			return
		}
	}
	if a.occupied() {
		a.newTab()
	}
	a.mailTab = true
	s := &a.mail
	s.stale = true
	if len(s.accounts) == 0 {
		s.binding = true
	} else {
		a.mailOpenOrSay()
		a.mailSync()
	}
	a.requestFrame()
}

func (a *App) mailOpenOrSay() {
	if err := a.mailOpen(); err != nil {
		a.mail.notice = a.describe(err)
	}
}

// mailHue is the colour that tells an account's mail apart in the common inbox.
func mailHue(id string) color.RGBA {
	palette := []uint32{0x6aa9ff, 0x5fd0a5, 0xf2b35e, 0xe58ab8, 0xb48cff, 0x7fd4e6, 0xe8836b}
	h := fnv.New32a()
	h.Write([]byte(id))
	return gfx.Hex(palette[h.Sum32()%uint32(len(palette))])
}

// drawMailTab is the mail tab: the mailboxes at the left, the list, and the message open beside it. A narrow window
// shows one of them at a time.
func (a *App) drawMailTab(r gfx.Rect) {
	if len(a.mail.list) > 0 {
		a.mailWebView() // the browser takes a moment to start, which the first message opened should not wait for
	}
	u := &a.u
	s := &a.mail
	u.c.Fill(r, colPanel)
	if s.stale {
		a.mailLoad()
	}
	if a.mailDue() {
		a.mailSync()
	}
	if s.binding || len(s.accounts) == 0 {
		a.drawMailForm(r)
		return
	}
	side := float32(0)
	if r.W >= u.px(760) {
		side = min(u.px(250), r.W*0.26)
		a.drawMailSide(rectAt(r.X, r.Y, side, r.H))
	} else if s.sideOn {
		a.drawMailSide(r)
		return
	}
	main := rectAt(r.X+side, r.Y, r.W-side, r.H)
	listW := main.W
	wide := main.W >= u.px(800)
	if wide {
		listW = min(u.px(400), main.W*0.42)
	}
	open, hasOpen := a.mailCurrent()
	switch {
	case s.letter != nil && !wide:
		a.drawMailLetter(main, s.letter)
	case wide:
		a.drawMailList(rectAt(main.X, main.Y, listW, main.H), side == 0)
		edge := rectAt(main.X+listW, main.Y, max(1, u.px(1)), main.H)
		u.c.Fill(edge, colBorder)
		reader := rectAt(edge.Right(), main.Y, main.W-listW-edge.W, main.H)
		if s.letter != nil {
			a.drawMailLetter(reader, s.letter)
		} else if hasOpen {
			a.drawMailReader(reader, open, false)
		} else {
			a.drawMailEmpty(reader)
		}
	case hasOpen:
		a.drawMailReader(main, open, true)
	default:
		a.drawMailList(main, side == 0)
	}
}

func (a *App) mailCurrent() (mail.Message, bool) {
	s := &a.mail
	if s.current == 0 {
		return mail.Message{}, false
	}
	for _, m := range s.list {
		if m.ID == s.current {
			return m, true
		}
	}
	return mail.Message{}, false
}

func (a *App) drawMailEmpty(r gfx.Rect) {
	u := &a.u
	a.paragraphCentred(rectAt(r.X+u.px(24), r.Y+r.H*0.4, r.W-u.px(48), u.px(60)), u.t("Pick a message to read it"), 13, colFaint)
}

// mailRow draws a line of the left column, which can be clicked, with a count at its right.
func (a *App) mailRow(id string, r gfx.Rect, glyph, text string, count int, selected bool) bool {
	u := &a.u
	hot := u.hover(id, r)
	switch {
	case selected:
		u.c.Fill(r, colSelected)
	case hot:
		u.c.Fill(r, colSurfaceHi)
	}
	if hot {
		u.cursor = CursorHand
	}
	u.icon(glyph, rectAt(r.X+u.px(6), r.Y, u.px(26), r.H), 17, colDim)
	f := u.face(13)
	right := r.Right() - u.px(10)
	if count > 0 {
		n := strconv.Itoa(count)
		right -= f.Measure(n)
		u.c.Text(f, right, r.Y+(r.H-f.Height())/2, n, colDim)
		right -= u.px(8)
	}
	left := r.X + u.px(38)
	u.c.Text(f, left, r.Y+(r.H-f.Height())/2, f.Ellipsize(text, right-left), colText)
	return u.clicked(id, r)
}

// drawMailSide is the left column: the mailboxes, each of which can be left out of the list, and the views.
func (a *App) drawMailSide(r gfx.Rect) {
	u := &a.u
	s := &a.mail
	u.c.Fill(r, colSurface)
	u.c.Fill(rectAt(r.Right()-max(1, u.px(1)), r.Y, max(1, u.px(1)), r.H), colBorder)
	a.scrolledBy("mail/side", r.Inset(u.px(10)), &s.scroll[mailScrollSide], &s.content[mailScrollSide], func(view gfx.Rect) float32 {
		y := view.Y
		rowH := u.px(34)
		heading := func(text string) {
			u.c.Text(u.bold(11.5), view.X+u.px(6), y+u.px(8), text, colFaint)
			y += u.px(30)
		}
		if u.button("mail/write", rectAt(view.X, y, view.W, u.px(34)), icPencil, u.t("Write"), primary, len(s.accounts) > 0) {
			a.mailWriteNew()
			s.sideOn = false
		}
		y += u.px(42)
		heading(u.t("Views"))
		total := 0
		for _, id := range s.chosen() {
			total += s.counts[id]
		}
		views := []struct {
			glyph, text string
			count       int
		}{{icSort, a.mailTierName(mail.TierMust), s.tiers[mail.TierMust].Unread},
			{icMail, a.mailTierName(mail.TierMaybe), s.tiers[mail.TierMaybe].Unread},
			{icClose, a.mailTierName(mail.TierNoise), s.tiers[mail.TierNoise].Unread},
			{icThink, u.t("Not analysed"), s.tiers[""].All},
			{icStar, u.t("Starred"), 0}, {icSend, u.t("Sent"), 0}, {icMail, u.t("All mail"), total}, {icRestore, u.t("History"), 0}}
		for i, v := range views {
			if a.mailRow(fmt.Sprintf("mail/view/%d", i), rectAt(view.X, y, view.W, rowH), v.glyph, v.text, v.count, s.view == mailView(i)) {
				s.view, s.viewChosen, s.stale, s.sideOn, s.trashing = mailView(i), true, true, false, false
				s.scroll[mailScrollList] = 0
			}
			y += rowH + u.px(2)
		}
		y += u.px(8)
		heading(u.t("Mailboxes"))
		all := len(s.chosen()) == len(s.accounts)
		glyph := icCheck
		if !all {
			glyph = icClose
		}
		if a.mailRow("mail/all", rectAt(view.X, y, view.W, rowH), glyph, u.t("All mailboxes"), 0, false) {
			for _, ac := range s.accounts {
				s.hidden[ac.ID] = all
			}
			s.stale = true
		}
		y += rowH + u.px(2)
		for _, ac := range s.accounts {
			y = a.drawMailAccount(view, y, ac)
		}
		y += u.px(6)
		if u.button("mail/add", rectAt(view.X, y, view.W, u.px(32)), icAdd, u.t("Bind a mailbox"), plain, true) {
			s.binding, s.bindErr = true, ""
		}
		return y + u.px(40)
	})
}

// drawMailAccount is a mailbox in the left column: a click takes it in or out of the list.
func (a *App) drawMailAccount(view gfx.Rect, y float32, ac mail.Account) float32 {
	u := &a.u
	s := &a.mail
	r := rectAt(view.X, y, view.W, u.px(34))
	id := "mail/account/" + ac.ID
	if s.removing == ac.ID {
		f := u.face(12)
		u.c.Text(f, r.X+u.px(6), r.Y+(r.H-f.Height())/2, f.Ellipsize(u.t("Unbind?"), r.W-u.px(140)), colDangerText)
		if u.button(id+"/yes", rectAt(r.Right()-u.px(132), r.Y+u.px(3), u.px(66), r.H-u.px(6)), "", u.t("Unbind"), danger, true) {
			a.mailRemove(ac.ID)
		}
		if u.button(id+"/no", rectAt(r.Right()-u.px(62), r.Y+u.px(3), u.px(62), r.H-u.px(6)), "", u.t("Cancel"), plain, true) {
			s.removing = ""
		}
		return y + r.H + u.px(2)
	}
	hot := u.hover(id, r)
	if hot {
		u.c.Fill(r, colSurfaceHi)
		u.cursor = CursorHand
	}
	on := !s.hidden[ac.ID]
	box := rectAt(r.X+u.px(10), r.Y+(r.H-u.px(14))/2, u.px(14), u.px(14))
	if on {
		u.c.Fill(box, mailHue(ac.ID))
		u.icon(icCheck, box, 12, colPanel)
	} else {
		u.c.Outline(box, max(1, u.px(1)), colBorderHi)
	}
	f := u.face(12.5)
	col := colText
	if !on {
		col = colFaint
	}
	right := r.Right() - u.px(10)
	if hot {
		right -= u.px(26)
	} else if n := s.counts[ac.ID]; n > 0 {
		text := strconv.Itoa(n)
		right -= f.Measure(text)
		u.c.Text(f, right, r.Y+(r.H-f.Height())/2, text, colDim)
		right -= u.px(8)
	}
	left := r.X + u.px(38)
	u.c.Text(f, left, r.Y+(r.H-f.Height())/2, f.Ellipsize(ac.Email, right-left), col)
	remove := rectAt(r.Right()-u.px(30), r.Y+u.px(3), u.px(28), r.H-u.px(6))
	if hot && u.iconButton(id+"/remove", remove, icDelete, u.t("Unbind this mailbox"), false, true) {
		s.removing = ac.ID
	} else if u.clicked(id, r) && !remove.Contains(u.pressX, u.pressY) {
		s.hidden[ac.ID] = on
		s.stale = true
	}
	return y + r.H + u.px(2)
}

// drawMailList is the search, the buttons and the list of the view chosen.
func (a *App) drawMailList(r gfx.Rect, narrow bool) {
	u := &a.u
	s := &a.mail
	pad := u.px(12)
	x := r.X + pad
	w := r.W - pad*2
	y := r.Y + pad
	h := u.px(32)
	if narrow {
		if u.iconButton("mail/side", rectAt(x, y, h, h), icMail, u.t("Mailboxes"), false, true) {
			s.sideOn = true
		}
		x += h + u.px(6)
		w -= h + u.px(6)
	}
	syncing := s.syncing
	action := rectAt(x+w-h, y, h, h)
	if u.iconButton("mail/sync", action, icSync, u.t("Look for new mail"), syncing, !syncing) {
		a.mailSync()
	}
	before := s.search
	u.textBox("mail/search", rectAt(x, y, action.X-x-u.px(6), h), &s.search, u.t("Search mail…"), false)
	if s.search != before {
		s.stale = true
	}
	y += h + u.px(6)
	x, w = r.X+pad, r.W-pad*2
	if s.notice != "" {
		for _, l := range u.face(12).Wrap(s.notice, w) {
			u.c.Text(u.face(12), x, y, l, colDangerText)
			y += u.face(12).Height() * 1.3
		}
		y += u.px(6)
	}
	y = a.drawMailBatch(x, y, w)
	list := rectAt(r.X, y, r.W, r.Bottom()-y)
	if len(s.chosen()) == 0 {
		a.paragraphCentred(rectAt(x, list.Y+u.px(60), w, u.px(60)), u.t("No mailbox is chosen. Tick one at the left."), 13, colFaint)
		return
	}
	if s.view == mailHistory {
		a.drawMailHistory(list)
		return
	}
	if len(s.list) == 0 {
		text := u.t("No mail here yet")
		switch {
		case !s.loaded:
			text = u.t("Loading…")
		case s.syncing:
			text = u.t("Fetching your mail…")
		case s.tiers[""].All > 0 && (s.view == mailMust || s.view == mailMaybe || s.view == mailNoise):
			text = u.tf("%d messages are waiting in Not analysed", s.tiers[""].All)
		}
		a.paragraphCentred(rectAt(x, list.Y+u.px(60), w, u.px(60)), text, 13, colFaint)
		return
	}
	a.scrolledBy("mail/list", list, &s.scroll[mailScrollList], &s.content[mailScrollList], func(view gfx.Rect) float32 {
		y, rowH := view.Y, u.px(mailRowHeight)
		for i := range s.list {
			if y+rowH > list.Y && y < list.Bottom() { // a mailbox is far longer than what shows of it
				a.drawMailRow(view, y, s.list[i])
			}
			y += rowH
		}
		return y
	})
}

// drawMailBatch offers what can be done with all the mail of a view, above its list: having the model read what it has not,
// which is the only thing that starts a model here, and clearing the lower tiers. Deleting asks first.
func (a *App) drawMailBatch(x, y, w float32) float32 {
	u := &a.u
	s := &a.mail
	if s.unread > 0 && !s.organised && s.view == mailNew && strings.TrimSpace(s.search) == "" && len(s.chosen()) > 0 {
		return a.drawMailAnalyse(x, y, w)
	}
	tier := ""
	switch s.view {
	case mailMaybe:
		tier = mail.TierMaybe
	case mailNoise:
		tier = mail.TierNoise
	}
	if tier == "" || strings.TrimSpace(s.search) != "" || len(s.chosen()) == 0 {
		return y
	}
	h := u.px(30)
	count := s.tiers[tier]
	if s.trashing {
		y = a.paragraph(x, y, w, u.tf("Move %d messages to the mailbox's trash? You can undo it in History.", count.All), 12.5, colText) + u.px(6)
		bw := (w - u.px(6)) / 2
		if u.button("mail/trash/yes", rectAt(x, y, bw, h), "", u.t("Delete"), danger, s.busy == "") {
			a.mailChange(tier, mail.ActionTrash)
		}
		if u.button("mail/trash/no", rectAt(x+bw+u.px(6), y, bw, h), "", u.t("Cancel"), plain, true) {
			s.trashing = false
		}
		return y + h + u.px(8)
	}
	bw := w
	if tier == mail.TierNoise {
		bw = (w - u.px(6)) / 2
		if u.button("mail/trash", rectAt(x+bw+u.px(6), y, bw, h), icDelete, u.t("Delete all"), plain, s.busy == "" && count.All > 0) {
			s.trashing = true
		}
	}
	if u.button("mail/read-all", rectAt(x, y, bw, h), icCheck, u.t("Mark all as read"), plain, s.busy == "" && count.Unread > 0) {
		a.mailChange(tier, mail.ActionRead)
	}
	return y + h + u.px(8)
}

// mailShareStart is how much of the mail not analysed the slider starts at: a model takes seconds a message.
const mailShareStart = 0.1

// drawMailAnalyse says how much of the mail the model is to read, the newest first, and starts it.
func (a *App) drawMailAnalyse(x, y, w float32) float32 {
	u := &a.u
	s := &a.mail
	y = a.drawMailModels("mail/model", x, y, w)
	share := a.settings.MailShare
	if share <= 0 {
		share = mailShareStart
	}
	if s.unread > 1 {
		f := u.face(12)
		text := u.tf("%d%% of the %d messages not analysed", int(math.Round(share*100)), s.unread)
		u.c.Text(f, x, y, f.Ellipsize(text, w), colDim)
		y += f.Height() * 1.3
		changed, done := u.slider("mail/share", rectAt(x, y, w, u.px(20)), &share, 0.01, 1, true)
		if changed {
			a.settings.MailShare = share
		}
		if done {
			a.saveSettings()
		}
		y += u.px(24)
	}
	count := max(1, int(math.Round(share*float64(s.unread))))
	if u.button("mail/organise", rectAt(x, y, w, u.px(30)), icThink, u.tf("Analyse the newest %d", count), primary, true) {
		a.mailOrganise(count)
	}
	return y + u.px(38)
}

func mailDate(t time.Time) string {
	now := time.Now()
	switch {
	case t.IsZero() || t.Unix() <= 0:
		return ""
	case t.YearDay() == now.YearDay() && t.Year() == now.Year():
		return t.Format("15:04")
	case t.Year() == now.Year():
		return t.Format("01-02")
	}
	return t.Format("2006-01-02")
}

func mailOr(m mail.Message, text string) string {
	if text != "" {
		return text
	}
	return m.Preview
}

const mailRowHeight = 70

// drawMailRow is a message in the list: the bar at its edge says which mailbox it is of.
func (a *App) drawMailRow(view gfx.Rect, y float32, m mail.Message) {
	u := &a.u
	s := &a.mail
	r := rectAt(view.X, y, view.W, u.px(mailRowHeight))
	id := fmt.Sprintf("mail/row/%d", m.ID)
	switch {
	case m.ID == s.current:
		u.c.Fill(r, colSelected)
	case u.hover(id, r):
		u.c.Fill(r, colSurfaceHi)
		u.cursor = CursorHand
	}
	u.c.Fill(rectAt(r.X, r.Y+u.px(6), u.px(3), r.H-u.px(12)), mailHue(m.Account))
	x, w := r.X+u.px(14), r.W-u.px(26)
	date := mailDate(m.Date)
	small := u.face(11.5)
	u.c.Text(small, x+w-small.Measure(date), r.Y+u.px(8), date, colFaint)
	if m.Starred {
		u.icon(icStar, rectAt(x+w-small.Measure(date)-u.px(20), r.Y+u.px(5), u.px(16), u.px(18)), 13, colAccent)
	}
	name := u.face(13)
	if !m.Seen {
		name = u.bold(13)
	}
	who := m.From
	if m.Box == mail.BoxSent {
		who = u.tf("To %s", m.To)
	}
	u.c.Text(name, x, r.Y+u.px(6), name.Ellipsize(who, w-small.Measure(date)-u.px(10)), colText)
	left := x
	if m.Tier == mail.TierMust {
		u.c.Circle(x+u.px(4), r.Y+u.px(36), u.px(3.5), colDanger)
		left += u.px(14)
	}
	u.c.Text(u.face(12.5), left, r.Y+u.px(27), u.face(12.5).Ellipsize(m.Subject, x+w-left), colText)
	u.c.Text(small, x, r.Y+u.px(48), small.Ellipsize(mailOr(m, m.Summary), w), colDim)
	u.c.Fill(rectAt(r.X, r.Bottom()-max(1, u.px(1)), r.W, max(1, u.px(1))), colBorder)
	if u.clicked(id, r) {
		s.current, s.scroll[mailScrollReader], s.letter = m.ID, 0, nil
	}
}

// drawMailHistory is the changes made on the servers, each of which can be undone.
func (a *App) drawMailHistory(r gfx.Rect) {
	u := &a.u
	s := &a.mail
	if len(s.log) == 0 {
		a.paragraphCentred(rectAt(r.X+u.px(12), r.Y+u.px(60), r.W-u.px(24), u.px(60)), u.t("Nothing has been changed on a server yet"), 13, colFaint)
		return
	}
	a.scrolledBy("mail/history", r, &s.scroll[mailScrollList], &s.content[mailScrollList], func(view gfx.Rect) float32 {
		y := view.Y
		f, small := u.face(13), u.face(11.5)
		for _, e := range s.log {
			row := rectAt(view.X+u.px(12), y, view.W-u.px(24), u.px(52))
			text := u.t("Archived")
			switch e.Action {
			case mail.ActionLabel:
				text = u.tf("Labelled %s", e.Value)
			case mail.ActionRead:
				text = u.tf("Marked %s messages read", e.Value)
			case mail.ActionTrash:
				text = u.tf("Moved %s messages to the trash", e.Value)
			}
			if e.Subject != "" {
				text += "  ·  " + e.Subject
			}
			bw := u.px(76)
			u.c.Text(f, row.X, row.Y+u.px(6), f.Ellipsize(text, row.W-bw-u.px(10)), colText)
			u.c.Text(small, row.X, row.Y+u.px(28), small.Ellipsize(e.At.Format("2006-01-02 15:04")+"  ·  "+a.mailAccountName(e.Account), row.W-bw-u.px(10)), colFaint)
			if e.Undone {
				u.c.Text(small, row.Right()-small.Measure(u.t("Undone")), row.Y+u.px(16), u.t("Undone"), colFaint)
			} else if u.button(fmt.Sprintf("mail/undo/%d", e.ID), rectAt(row.Right()-bw, row.Y+u.px(10), bw, u.px(30)), "", u.t("Undo"), plain, s.busy == "") {
				a.mailUndo(e)
			}
			y = row.Bottom()
		}
		return y
	})
}

func (a *App) mailAccountName(id string) string {
	if ac, ok := a.mail.account(id); ok {
		return ac.Email
	}
	return id
}

// mailTierNames name the tiers, in the order of mail.Tiers.
var mailTierNames = msgs("Must see", "Worth a look", "No value")

func (a *App) mailTierName(tier string) string {
	if i := slices.Index(mail.Tiers, tier); i >= 0 {
		return a.u.t(mailTierNames[i])
	}
	return ""
}

// mailHeadShare is how much of the reader the part above a message's page may take: past it that part scrolls.
const mailHeadShare = 0.45

// drawMailReader is the message open, what the model made of it, what can be done with it, and its page, or its text
// when it has none or the system has no view to show it in.
func (a *App) drawMailReader(r gfx.Rect, m mail.Message, back bool) {
	u := &a.u
	s := &a.mail
	p := a.mailPageOf(m)
	page := (p.known && p.body.HTML != "" || !p.known && p.held) && a.mailWebView() != nil
	head := r
	if page {
		head.H = r.H * mailHeadShare
		if used := s.content[mailScrollReader]; used > 0 {
			head.H = min(head.H, used)
		}
		edge := head.Bottom()
		a.drawMailPage(rectAt(r.X, edge, r.W, r.Bottom()-edge), p)
	}
	a.scrolledBy("mail/reader", head, &s.scroll[mailScrollReader], &s.content[mailScrollReader], func(view gfx.Rect) float32 {
		pad := u.px(20)
		x, w := view.X+pad, min(view.W-pad*2, u.px(760))
		y := view.Y + u.px(14)
		if back {
			if u.iconButton("mail/back", rectAt(x, y, u.px(30), u.px(30)), icBack, u.t("Back"), false, true) {
				s.current = 0
			}
			y += u.px(36)
		}
		y = a.paragraph(x, y, w, m.Subject, 17, colText) + u.px(6)
		small := u.face(12)
		line := m.From
		if m.Address != "" && m.Address != m.From {
			line += "  <" + m.Address + ">"
		}
		u.c.Text(small, x, y, small.Ellipsize(line, w), colDim)
		y += small.Height() + u.px(4)
		if m.To != "" {
			line = u.tf("To %s", m.To)
			if m.Cc != "" {
				line += "  ·  " + u.tf("Cc %s", m.Cc)
			}
			u.c.Text(small, x, y, small.Ellipsize(line, w), colDim)
			y += small.Height() + u.px(4)
		}
		line = a.mailAccountName(m.Account)
		if !m.Date.IsZero() && m.Date.Unix() > 0 {
			line = m.Date.Format("2006-01-02 15:04") + "  ·  " + line
		}
		u.c.Fill(rectAt(x, y+u.px(3), u.px(10), u.px(10)), mailHue(m.Account))
		u.c.Text(small, x+u.px(16), y, small.Ellipsize(line, w-u.px(16)), colFaint)
		y += small.Height() + u.px(12)
		if m.Summary != "" {
			y = a.drawMailSummary(x, y, w, m)
		}
		y = a.drawMailChoices(x, y, w, m)
		ac, known := s.account(m.Account)
		bx := x
		for _, b := range []struct {
			id, text string
			click    func()
		}{{"mail/reply", u.t("Reply"), func() { a.mailReply(m, false, false) }}, {"mail/reply-all", u.t("Reply to all"), func() { a.mailReply(m, true, false) }},
			{"mail/forward", u.t("Forward…"), func() { a.mailWrite(m.Account, m.Forwarded(ac.Email)) }}} {
			bw := u.buttonWidth("", b.text)
			if u.button(b.id, rectAt(bx, y, bw, u.px(30)), "", b.text, plain, known) {
				b.click()
			}
			bx += bw + u.px(6)
		}
		y += u.px(38)
		bw, rw := u.px(120), u.px(150)
		if u.button("mail/archive", rectAt(x, y, bw, u.px(30)), icArchive, u.t("Archive"), plain, known && m.Inbox && s.busy == "") {
			a.mailArchive(m.ID, ac)
			s.current = 0
		}
		if u.button("mail/ai-reply", rectAt(x+bw+u.px(8), y, rw, u.px(30)), "", u.t("Draft a reply"), plain, known && !s.drafting) {
			a.mailReply(m, false, true)
		}
		if page {
			y = a.drawMailPictures(x, y, w, bw+rw+u.px(20), m, p)
		} else {
			y += u.px(44)
		}
		if page {
			return y
		}
		u.c.Fill(rectAt(x, y, w, max(1, u.px(1))), colBorder)
		y += u.px(14)
		if !p.known || p.fetching { // the plain copy is only what is shown when the page cannot be had
			small := u.face(12.5)
			if p.fetching {
				u.c.Text(small, x, y, u.t("Loading the message..."), colDim)
			}
			return y + small.Height() + u.px(30)
		}
		body := m.Text
		if body == "" {
			body = u.t("This message has no text")
		}
		for _, part := range strings.Split(body, "\n") {
			if strings.TrimSpace(part) == "" {
				y += u.px(10)
				continue
			}
			y = a.paragraph(x, y, w, strings.TrimRight(part, "\r"), 13.5, colText)
		}
		return y + u.px(30)
	})
}

func (a *App) drawMailSummary(x, y, w float32, m mail.Message) float32 {
	u := &a.u
	f := u.face(12.5)
	lines := f.Wrap(m.Summary, w-u.px(24))
	h := float32(len(lines))*f.Height()*1.35 + u.px(16)
	tag := m.Why
	if tag != "" {
		h += u.px(20)
	}
	box := rectAt(x, y, w, h)
	u.c.Fill(box, colSurface)
	u.c.Outline(box, max(1, u.px(1)), colBorder)
	ty := box.Y + u.px(8)
	for _, l := range lines {
		u.c.Text(f, box.X+u.px(12), ty, l, colText)
		ty += f.Height() * 1.35
	}
	if tag != "" {
		u.c.Text(u.face(11.5), box.X+u.px(12), ty+u.px(2), u.face(11.5).Ellipsize(tag, w-u.px(24)), colDim)
	}
	return box.Bottom() + u.px(10)
}

// drawMailChoices is what the user can say of a message: its tier, which they change with a click, a star, and its
// sender. A star goes to the server as well; the rest stays on this computer. Each button undoes itself.
func (a *App) drawMailChoices(x, y, w float32, m mail.Message) float32 {
	u := &a.u
	s := &a.mail
	store := s.mailer.Store
	done := func(err error) {
		s.stale = true
		if err != nil {
			s.notice = a.describe(err)
		}
	}
	if m.Inbox {
		names := make([]string, len(mail.Tiers))
		for i, t := range mail.Tiers {
			names[i] = a.mailTierName(t)
		}
		at := slices.Index(mail.Tiers, m.Tier)
		if pick := u.chips("mail/tier", rectAt(x, y, min(w, u.px(300)), u.px(30)), names, at, true); pick != at {
			done(store.Pick(m.ID, mail.Tiers[pick]))
		}
		y += u.px(38)
	}
	rule := s.senders[strings.ToLower(m.Address)]
	toggles := []struct {
		id, glyph, off, on string
		set                bool
		click              func() error
	}{
		{"star", icStar, u.t("Star"), u.t("Starred"), m.Starred, func() error { return a.mailStar(m) }},
		{"person", icSort, u.t("Important person"), u.t("Important person"), rule == mail.TierMust, func() error { return a.mailSetSender(m, rule, mail.TierMust) }},
		{"mute", icClose, u.t("Hide this sender"), u.t("Sender hidden"), rule == mail.TierNoise, func() error { return a.mailSetSender(m, rule, mail.TierNoise) }},
	}
	if !m.Inbox {
		toggles = toggles[:1] // what the user sent, or what lies outside the inbox, is in no tier
	}
	bx := x
	for _, t := range toggles {
		text := t.off
		kind := plain
		if t.set {
			text, kind = t.on, primary
		}
		bw := u.buttonWidth(t.glyph, text)
		if bx > x && bx+bw > x+w {
			bx, y = x, y+u.px(36)
		}
		if u.button("mail/"+t.id, rectAt(bx, y, bw, u.px(30)), t.glyph, text, kind, t.id == "star" || m.Address != "") {
			done(t.click())
		}
		bx += bw + u.px(6)
	}
	return y + u.px(40)
}

// mailStar stars a message or takes the star off, here at once and on the server behind it; what the server could not be
// told of goes with the next sync.
func (a *App) mailStar(m mail.Message) error {
	s := &a.mail
	if err := s.mailer.Store.Star(m.ID, !m.Starred); err != nil {
		return err
	}
	if ac, ok := s.account(m.Account); ok {
		mailer := s.mailer
		a.background(func(ctx context.Context) {
			if err := mailer.PushStars(ctx, ac); err != nil && !errors.Is(err, context.Canceled) {
				a.post(func() { a.mail.notice = a.describeMail(err) })
			}
		})
	}
	return nil
}

// mailSetSender says what a sender's mail is, or takes that back when it was said already.
func (a *App) mailSetSender(m mail.Message, rule, tier string) error {
	if rule == tier {
		tier = ""
	}
	return a.mail.mailer.Store.SetSender(m.Address, tier)
}

// drawMailForm binds a mailbox.
func (a *App) drawMailForm(r gfx.Rect) {
	u := &a.u
	s := &a.mail
	a.scrolledBy("mail/form", r, &s.scroll[mailScrollForm], &s.content[mailScrollForm], func(view gfx.Rect) float32 {
		w := min(view.W-u.px(48), u.px(520))
		x := view.X + (view.W-w)/2
		y := view.Y + u.px(36)
		y = a.paragraph(x, y, w, u.t("Bind a mailbox"), 18, colText) + u.px(8)
		y = a.paragraph(x, y, w, u.t("A copy of your mail stays on this computer. You can bind several mailboxes, see them together, write from any of them, and have a model sort them."), 13, colDim) + u.px(16)
		names := make([]string, len(mailProviders))
		for i, p := range mailProviders {
			names[i] = p.name
			if names[i] == "" {
				names[i] = u.t("Other")
			}
		}
		box := func(id, hint string, text *string) {
			before := *text
			u.textBox(id, rectAt(x, y, w, u.px(34)), text, hint, false)
			y += u.px(42)
			if id == "mail/email" && *text != before {
				if i := mailProviderOf(*text); i >= 0 {
					s.provider = i
				}
			}
		}
		box("mail/email", u.t("Email address"), &s.email)
		pick, h := u.flowChips("mail/provider", rectAt(x, y, w, u.px(28)), names, s.provider)
		if pick >= 0 {
			s.provider = pick
		}
		y += h + u.px(12)
		p := mailProviders[s.provider]
		if p.host == "" {
			box("mail/host", u.t("IMAP server, such as imap.example.com:993"), &s.host)
			box("mail/smtp", u.t("Outgoing server, such as smtp.example.com:465"), &s.smtp)
		}
		u.secretBox("mail/password", rectAt(x, y, w, u.px(34)), &s.password, u.t("App password or authorisation code"))
		y += u.px(42)
		if p.note != "" {
			y = a.paragraph(x, y, w, u.t(p.note), 12.5, colDim) + u.px(6)
		}
		y = a.paragraph(x, y, w, u.t("This is not your usual password, and it can read everything in the mailbox and send from it, so it is kept sealed on this computer. Mail is sent only when you press Send, and what you delete goes to the mailbox's trash."), 12, colFaint) + u.px(12)
		bw := u.px(150)
		connect := u.t("Bind")
		if s.signing {
			connect = u.t("Signing in…")
		}
		if u.button("mail/bind", rectAt(x, y, bw, u.px(34)), "", connect, primary, !s.signing) {
			a.mailBind()
		}
		next := x + bw + u.px(8)
		if p.passwordURL != "" {
			gw := u.px(190)
			if u.button("mail/get", rectAt(next, y, gw, u.px(34)), icLaunch, u.t("Create an app password"), ghost, true) {
				a.host.LaunchURL(p.passwordURL)
			}
			next += gw + u.px(8)
		}
		if len(s.accounts) > 0 && u.button("mail/cancel", rectAt(next, y, u.px(80), u.px(34)), "", u.t("Cancel"), plain, true) {
			s.binding, s.password, s.bindErr = false, "", ""
		}
		y += u.px(46)
		if s.bindErr != "" {
			y = a.paragraph(x, y, w, s.bindErr, 12.5, colDangerText)
		}
		return y + u.px(20)
	})
}

// settingsMail holds the preferences of the mail tab: which model reads mail, and how often it is fetched.
func (a *App) settingsMail(view gfx.Rect, y float32) float32 {
	u := &a.u
	u.c.Text(u.bold(13), view.X, y, u.t("Mail"), colText)
	y += u.px(24)
	y = a.drawMailModels("settings/mail/model", view.X, y, view.W) + u.px(2)
	every := []int{15, 60, -1}
	names := []string{u.t("Every 15 minutes"), u.t("Every hour"), u.t("Only when asked")}
	at := 0
	for i, v := range every {
		if v == a.settings.MailEvery {
			at = i
		}
	}
	pick, h := u.flowChips("settings/mail/every", rectAt(view.X, y, view.W, u.px(28)), names, at)
	if pick >= 0 && pick != at {
		a.settings.MailEvery = every[pick]
		a.saveSettings()
	}
	return y + h + u.px(18)
}
