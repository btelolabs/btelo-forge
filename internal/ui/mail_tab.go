package ui

import (
	"fmt"
	"hash/fnv"
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/ai"
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
	case wide:
		a.drawMailList(rectAt(main.X, main.Y, listW, main.H), side == 0)
		edge := rectAt(main.X+listW, main.Y, max(1, u.px(1)), main.H)
		u.c.Fill(edge, colBorder)
		reader := rectAt(edge.Right(), main.Y, main.W-listW-edge.W, main.H)
		if hasOpen {
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
		heading(u.t("Views"))
		total, waiting := 0, len(s.offers)
		for _, id := range s.chosen() {
			total += s.counts[id]
		}
		views := []struct {
			glyph, text string
			count       int
		}{{icMail, u.t("Combined inbox"), total}, {icSort, u.t("Important"), 0}, {icCheck, u.t("Suggestions"), waiting}, {icRestore, u.t("History"), 0}}
		for i, v := range views {
			if a.mailRow(fmt.Sprintf("mail/view/%d", i), rectAt(view.X, y, view.W, rowH), v.glyph, v.text, v.count, s.view == mailView(i)) {
				s.view, s.stale, s.sideOn = mailView(i), true, false
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
	organise := rectAt(action.X-h-u.px(6), y, h, h)
	if u.iconButton("mail/sync", action, icSync, u.t("Look for new mail"), syncing, !syncing) {
		a.mailSync()
	}
	if s.organised {
		if u.iconButton("mail/stop", organise, icStop, u.t("Stop reading"), true, true) {
			s.stop()
		}
	} else if u.iconButton("mail/organise", organise, icThink, u.t("Have the model read new mail"), false, true) {
		a.mailOrganise(true)
	}
	before := s.search
	u.textBox("mail/search", rectAt(x, y, organise.X-x-u.px(6), h), &s.search, u.t("Search mail…"), false)
	if s.search != before {
		s.stale = true
	}
	y += h + u.px(6)
	x, w = r.X+pad, r.W-pad*2
	if s.syncText != "" {
		f := u.face(11.5)
		u.c.Text(f, x, y, f.Ellipsize(s.syncText, w), colDim)
		y += f.Height() + u.px(6)
	}
	if s.notice != "" {
		for _, l := range u.face(12).Wrap(s.notice, w) {
			u.c.Text(u.face(12), x, y, l, colDangerText)
			y += u.face(12).Height() * 1.3
		}
		y += u.px(6)
	}
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
		if s.syncing {
			text = u.t("Fetching your mail…")
		}
		a.paragraphCentred(rectAt(x, list.Y+u.px(60), w, u.px(60)), text, 13, colFaint)
		return
	}
	a.scrolledBy("mail/list", list, &s.scroll[mailScrollList], &s.content[mailScrollList], func(view gfx.Rect) float32 {
		y := view.Y
		for i := range s.list {
			y = a.drawMailRow(view, y, s.list[i])
		}
		return y
	})
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
	return mail.Snippet(m.Text, 120)
}

// drawMailRow is a message in the list: the bar at its edge says which mailbox it is of.
func (a *App) drawMailRow(view gfx.Rect, y float32, m mail.Message) float32 {
	u := &a.u
	s := &a.mail
	r := rectAt(view.X, y, view.W, u.px(70))
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
	name := u.face(13)
	if !m.Seen {
		name = u.bold(13)
	}
	u.c.Text(name, x, r.Y+u.px(6), name.Ellipsize(m.From, w-small.Measure(date)-u.px(10)), colText)
	left := x
	if m.Priority == mail.PriorityHigh {
		u.c.Circle(x+u.px(4), r.Y+u.px(36), u.px(3.5), colDanger)
		left += u.px(14)
	}
	u.c.Text(u.face(12.5), left, r.Y+u.px(27), u.face(12.5).Ellipsize(m.Subject, x+w-left), colText)
	u.c.Text(small, x, r.Y+u.px(48), small.Ellipsize(mailOr(m, m.Summary), w), colDim)
	u.c.Fill(rectAt(r.X, r.Bottom()-max(1, u.px(1)), r.W, max(1, u.px(1))), colBorder)
	if u.clicked(id, r) {
		s.current, s.scroll[mailScrollReader] = m.ID, 0
	}
	return r.Bottom()
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
			if e.Action == mail.ActionLabel {
				text = u.tf("Labelled %s", e.Value)
			}
			bw := u.px(76)
			u.c.Text(f, row.X, row.Y+u.px(6), f.Ellipsize(text+"  ·  "+e.Subject, row.W-bw-u.px(10)), colText)
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

// mailCategories name what a model sorts mail into, in the order of mail.Categories.
var mailCategories = msgs("Personal", "Work", "Money", "Shopping", "Newsletter", "Notification", "Spam")

func (a *App) mailCategoryName(c string) string {
	if i := slices.Index(mail.Categories, c); i >= 0 {
		return a.u.t(mailCategories[i])
	}
	return ""
}

// drawMailReader is the message open, what the model made of it, what can be done with it, and its text. A message is
// shown as text only, so nothing in it loads and the sender cannot tell that it was read.
func (a *App) drawMailReader(r gfx.Rect, m mail.Message, back bool) {
	u := &a.u
	s := &a.mail
	a.scrolledBy("mail/reader", r, &s.scroll[mailScrollReader], &s.content[mailScrollReader], func(view gfx.Rect) float32 {
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
		y = a.drawMailOffers(x, y, w, m)
		ac, known := s.account(m.Account)
		bw := u.px(120)
		if u.button("mail/archive", rectAt(x, y, bw, u.px(30)), icArchive, u.t("Archive"), plain, known && m.Inbox && s.busy == "") {
			a.mailApply(mail.Suggestion{Message: m.ID, Action: mail.ActionArchive}, ac)
			s.current = 0
		}
		if u.button("mail/reply", rectAt(x+bw+u.px(8), y, u.px(150), u.px(30)), "", u.t("Draft a reply"), plain, known && !s.drafting) {
			a.mailDraftReply(m)
		}
		y += u.px(44)
		if s.draftFor == m.ID {
			y = a.drawMailDraft(x, y, w, m)
		}
		u.c.Fill(rectAt(x, y, w, max(1, u.px(1))), colBorder)
		y += u.px(14)
		body := m.Text
		if body == "" {
			body = u.t("This message has no text")
		}
		for _, part := range strings.Split(body, "\n") {
			if strings.TrimSpace(part) == "" {
				y += u.px(10)
				continue
			}
			y = a.paragraph(x, y, w, part, 13.5, colText)
		}
		return y + u.px(30)
	})
}

func (a *App) drawMailSummary(x, y, w float32, m mail.Message) float32 {
	u := &a.u
	f := u.face(12.5)
	lines := f.Wrap(m.Summary, w-u.px(24))
	h := float32(len(lines))*f.Height()*1.35 + u.px(16)
	var tags []string
	if m.Priority == mail.PriorityHigh {
		tags = append(tags, u.t("Important"))
	}
	if c := a.mailCategoryName(m.Category); c != "" {
		tags = append(tags, c)
	}
	if len(tags) > 0 {
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
	if len(tags) > 0 {
		u.c.Text(u.face(11.5), box.X+u.px(12), ty+u.px(2), strings.Join(tags, "  ·  "), colDim)
	}
	return box.Bottom() + u.px(10)
}

// drawMailOffers lists what the model proposes for the message: nothing happens on the server without a click here.
func (a *App) drawMailOffers(x, y, w float32, m mail.Message) float32 {
	u := &a.u
	s := &a.mail
	ac, known := s.account(m.Account)
	for _, g := range s.offers {
		if g.Message != m.ID {
			continue
		}
		f := u.face(12.5)
		text := u.t("Archive this message?")
		if g.Action == mail.ActionLabel {
			text = u.tf("Label it %s?", g.Value)
		}
		if g.Reason != "" {
			text += "  " + g.Reason
		}
		row := rectAt(x, y, w, u.px(36))
		yes, no := rectAt(row.Right()-u.px(160), row.Y+u.px(3), u.px(84), u.px(30)), rectAt(row.Right()-u.px(70), row.Y+u.px(3), u.px(70), u.px(30))
		u.c.Text(f, row.X, row.Y+(row.H-f.Height())/2, f.Ellipsize(text, yes.X-row.X-u.px(10)), colText)
		if u.button(fmt.Sprintf("mail/yes/%d", g.ID), yes, "", u.t("Do it"), primary, known && s.busy == "") {
			a.mailApply(g, ac)
		}
		if u.button(fmt.Sprintf("mail/no/%d", g.ID), no, "", u.t("Dismiss"), plain, true) {
			if err := s.mailer.Store.Dismiss(g.ID); err != nil {
				s.notice = a.describe(err)
			}
			s.stale = true
		}
		y = row.Bottom() + u.px(4)
	}
	return y + u.px(6)
}

// drawMailDraft is the reply being written; it goes to the mailbox's drafts, and there is no way to send it from here.
func (a *App) drawMailDraft(x, y, w float32, m mail.Message) float32 {
	u := &a.u
	s := &a.mail
	if s.drafting {
		u.c.Text(u.face(12.5), x, y, u.t("The model is writing a reply…"), colDim)
		return y + u.px(30)
	}
	h := u.px(150)
	u.textBox("mail/draft", rectAt(x, y, w, h), &s.draft, u.t("Write the reply…"), true)
	y += h + u.px(8)
	if u.button("mail/save-draft", rectAt(x, y, u.px(170), u.px(30)), "", u.t("Save to drafts"), primary, strings.TrimSpace(s.draft) != "" && s.busy == "") {
		a.mailSaveDraft(m)
	}
	if u.button("mail/cancel-draft", rectAt(x+u.px(178), y, u.px(80), u.px(30)), "", u.t("Cancel"), plain, true) {
		s.draft, s.draftFor = "", 0
	}
	f := u.face(11.5)
	u.c.Text(f, x+u.px(270), y+(u.px(30)-f.Height())/2, f.Ellipsize(u.t("It will not be sent"), w-u.px(270)), colFaint)
	return y + u.px(44)
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
		y = a.paragraph(x, y, w, u.t("Your mail stays on this computer and is read by a model on this computer. You can bind several mailboxes, see them together, and choose which to show."), 13, colDim) + u.px(16)
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
		}
		u.secretBox("mail/password", rectAt(x, y, w, u.px(34)), &s.password, u.t("App password or authorisation code"))
		y += u.px(42)
		if p.note != "" {
			y = a.paragraph(x, y, w, u.t(p.note), 12.5, colDim) + u.px(6)
		}
		y = a.paragraph(x, y, w, u.t("This is not your usual password, and it can read everything in the mailbox, so it is kept sealed on this computer. The app never sends or deletes mail."), 12, colFaint) + u.px(12)
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
	lang := string(u.lang)
	u.c.Text(u.bold(13), view.X, y, u.t("Mail"), colText)
	y += u.px(24)
	var models []ai.Choice
	for _, ch := range a.ai.config.Choices() {
		if ch.Kind == ai.KindLLM && ch.Local != nil {
			if _, have := a.ai.rt.Files(*ch.Local); have {
				models = append(models, ch)
			}
		}
	}
	if len(models) > 0 {
		current, _ := a.mailModel()
		names := make([]string, len(models))
		at := 0
		for i, ch := range models {
			names[i] = ch.Name.In(lang)
			if ch.ID == current.ID {
				at = i
			}
		}
		pick, h := u.flowChips("settings/mail/model", rectAt(view.X, y, view.W, u.px(28)), names, at)
		if pick >= 0 && pick != at {
			a.settings.MailModel = models[pick].ID
			a.saveSettings()
		}
		y += h + u.px(8)
	}
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
