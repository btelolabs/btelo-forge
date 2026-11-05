package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/ai"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/mail"
)

// mailLetter is the letter being written, which takes the place of the message open.
type mailLetter struct {
	account           string
	to, cc, bcc       string
	subject, text     string
	inReplyTo, refers string
	files             []string
	copies            bool // the Cc and Bcc lines are showing
	sending           bool
	saving            bool
	err               string
}

// mailWrite opens a letter from an account, in place of the one being written.
func (a *App) mailWrite(account string, l mail.Letter) {
	s := &a.mail
	s.letter = &mailLetter{account: account, to: l.To, cc: l.Cc, bcc: l.Bcc, subject: l.Subject, text: l.Text,
		inReplyTo: l.InReplyTo, refers: l.References, copies: l.Cc != "" || l.Bcc != ""}
	a.requestFrame()
}

// mailWriteNew opens an empty letter from the first mailbox chosen.
func (a *App) mailWriteNew() {
	s := &a.mail
	if chosen := s.chosen(); len(chosen) > 0 {
		a.mailWrite(chosen[0], mail.Letter{})
	} else if len(s.accounts) > 0 {
		a.mailWrite(s.accounts[0].ID, mail.Letter{})
	}
}

// mailReply opens an answer to a message, and with a model the words of it are written there.
func (a *App) mailReply(m mail.Message, all, byModel bool) {
	s := &a.mail
	ac, ok := s.account(m.Account)
	if !ok {
		return
	}
	a.mailWrite(ac.ID, m.Answer(ac.Email, all))
	if !byModel {
		return
	}
	ch, ok := a.mailModel()
	if !ok {
		s.letter.err = a.u.t("Download a language model or add an API key in Settings → AI models first")
		return
	}
	s.drafting = true
	letter := s.letter
	ask := a.mailAsk(ch, ai.Options{})
	a.background(func(ctx context.Context) {
		text, err := mail.DraftReply(ctx, ask, m)
		a.post(func() {
			s := &a.mail
			s.drafting = false
			a.mailRelease()
			switch {
			case err != nil:
				letter.err = a.describeAI(err)
			case s.letter == letter:
				letter.text = strings.TrimSpace(text) + letter.text
			}
		})
	})
}

func (l *mailLetter) letter(ac mail.Account) mail.Letter {
	return mail.Letter{From: ac.Email, To: l.to, Cc: l.cc, Bcc: l.bcc, Subject: l.subject, Text: l.text, InReplyTo: l.inReplyTo,
		References: l.refers, Files: l.files}
}

// mailOutgoing is the outgoing server of a mailbox: the one it was bound with, its provider's, or the one beside its IMAP
// server by the usual name.
func mailOutgoing(ac mail.Account) string {
	if ac.SMTP != "" {
		return ac.SMTP
	}
	for _, p := range mailProviders {
		if p.host == ac.Host {
			return p.smtp
		}
	}
	host, _, _ := strings.Cut(ac.Host, ":")
	return "smtp." + strings.TrimPrefix(host, "imap.") + ":465"
}

// mailSend sends the letter, which only this click does.
func (a *App) mailSend() {
	s := &a.mail
	l := s.letter
	if l == nil || l.sending || l.saving {
		return
	}
	ac, ok := s.account(l.account)
	if !ok {
		return
	}
	ac.SMTP = mailOutgoing(ac)
	letter := l.letter(ac)
	l.sending, l.err = true, ""
	mailer := s.mailer
	sent, unfiled := a.u.t("Sent"), a.u.t("Sent, but it could not be filed in the sent folder; do not send it again")
	a.background(func(ctx context.Context) {
		err := mailer.Send(ctx, ac, letter, time.Now())
		a.post(func() {
			s := &a.mail
			l.sending = false
			switch {
			case errors.Is(err, mail.ErrNotFiled):
				a.toast(unfiled, true)
			case err != nil:
				l.err = a.describeMail(err)
				return
			default:
				a.toast(sent, false)
			}
			if s.letter == l {
				s.letter = nil
			}
			a.mailSync() // what was sent shows from the server's sent folder
		})
	})
}

// mailSaveLetter puts the letter in the mailbox's drafts.
func (a *App) mailSaveLetter() {
	s := &a.mail
	l := s.letter
	if l == nil || l.sending || l.saving {
		return
	}
	ac, ok := s.account(l.account)
	if !ok {
		return
	}
	letter := l.letter(ac)
	l.saving, l.err = true, ""
	mailer := s.mailer
	saved := a.u.t("The letter is in the drafts of the mailbox")
	a.background(func(ctx context.Context) {
		err := mailer.SaveDraft(ctx, ac, letter, time.Now())
		a.post(func() {
			s := &a.mail
			l.saving = false
			if err != nil {
				l.err = a.describeMail(err)
				return
			}
			if s.letter == l {
				s.letter = nil
			}
			a.toast(saved, false)
		})
	})
}

// drawMailLetter is the letter being written.
func (a *App) drawMailLetter(r gfx.Rect, l *mailLetter) {
	u := &a.u
	s := &a.mail
	pad := u.px(20)
	x, w := r.X+pad, min(r.W-pad*2, u.px(760))
	y := r.Y + u.px(14)
	u.c.Text(u.bold(15), x, y+u.px(6), u.t("New letter"), colText)
	if u.iconButton("mail/letter/close", rectAt(x+w-u.px(30), y, u.px(30), u.px(30)), icClose, u.t("Discard"), false, !l.sending) {
		s.letter = nil
		return
	}
	y += u.px(40)
	if len(s.accounts) > 1 {
		names := make([]string, len(s.accounts))
		at := 0
		for i, ac := range s.accounts {
			names[i] = ac.Email
			if ac.ID == l.account {
				at = i
			}
		}
		pick, h := u.flowChips("mail/letter/from", rectAt(x, y, w, u.px(28)), names, at)
		if pick >= 0 {
			l.account = s.accounts[pick].ID
		}
		y += h + u.px(8)
	}
	line := func(id, hint string, value *string, right float32) {
		u.textBox(id, rectAt(x, y, w-right, u.px(32)), value, hint, false)
		y += u.px(38)
	}
	toggle := float32(0)
	if !l.copies {
		toggle = u.px(82)
		if u.button("mail/letter/copies", rectAt(x+w-u.px(76), y, u.px(76), u.px(32)), "", u.t("Cc / Bcc"), plain, true) {
			l.copies = true
		}
	}
	line("mail/letter/to", u.t("To"), &l.to, toggle)
	if l.copies {
		line("mail/letter/cc", u.t("Cc"), &l.cc, 0)
		line("mail/letter/bcc", u.t("Bcc, which nobody else sees"), &l.bcc, 0)
	}
	line("mail/letter/subject", u.t("Subject:"), &l.subject, 0)
	foot := u.px(46) + float32(len(l.files))*u.px(28)
	if l.err != "" {
		foot += float32(len(u.face(12.5).Wrap(l.err, w)))*u.face(12.5).Height()*1.35 + u.px(8)
	}
	if s.drafting {
		foot += u.px(24)
	}
	body := max(r.Bottom()-y-foot-u.px(10), u.px(120))
	u.textBox("mail/letter/body", rectAt(x, y, w, body), &l.text, u.t("Write your letter…"), true)
	y += body + u.px(8)
	if s.drafting {
		u.c.Text(u.face(12.5), x, y, u.t("The model is writing a reply…"), colDim)
		y += u.px(24)
	}
	for i, path := range l.files {
		f := u.face(12.5)
		text := filepath.Base(path)
		if info, err := os.Stat(path); err == nil {
			text += "  ·  " + size(info.Size())
		}
		u.c.Text(f, x, y+(u.px(26)-f.Height())/2, f.Ellipsize(text, w-u.px(36)), colDim)
		if u.iconButton(fmt.Sprintf("mail/letter/file/%d", i), rectAt(x+w-u.px(28), y, u.px(26), u.px(26)), icClose, u.t("Remove"), false, true) {
			l.files = slices.Delete(l.files, i, i+1)
			break
		}
		y += u.px(28)
	}
	if l.err != "" {
		y = a.paragraph(x, y, w, l.err, 12.5, colDangerText) + u.px(8)
	}
	idle := !l.sending && !l.saving
	send := u.t("Send")
	if l.sending {
		send = u.t("Sending…")
	}
	sw := u.px(110)
	if u.button("mail/letter/send", rectAt(x, y, sw, u.px(32)), icSend, send, primary, idle && strings.TrimSpace(l.to+l.cc+l.bcc) != "") {
		a.mailSend()
	}
	dw := u.px(130)
	if u.button("mail/letter/save", rectAt(x+sw+u.px(8), y, dw, u.px(32)), "", u.t("Save to drafts"), plain, idle) {
		a.mailSaveLetter()
	}
	if u.button("mail/letter/attach", rectAt(x+sw+dw+u.px(16), y, u.px(100), u.px(32)), icAttach, u.t("Attach"), plain, idle) {
		a.pickFiles(pickRequest{key: "mail/attach", title: u.t("Attach files"), multiple: true}, func(paths []string) {
			l.files = append(l.files, paths...)
		})
	}
}

// mailModels are the models that can read mail: those on this computer, and those of a service with a key saved.
func (a *App) mailModels() []ai.Choice {
	var out []ai.Choice
	for _, ch := range a.ai.config.Choices() {
		if ch.Kind != ai.KindLLM {
			continue
		}
		if ch.Online() {
			if a.ai.keys.Has(ch.Provider.ID) {
				out = append(out, ch)
			}
		} else if ch.Local != nil {
			if _, have := a.ai.rt.Files(*ch.Local); have {
				out = append(out, ch)
			}
		}
	}
	return out
}

func (a *App) mailModelName(ch ai.Choice) string {
	lang := string(a.u.lang)
	if ch.Online() {
		return ch.Provider.Name.In(lang) + " · " + ch.Name.In(lang)
	}
	return ch.Name.In(lang)
}

// drawMailModels offers the models and says where mail goes with the one chosen.
func (a *App) drawMailModels(id string, x, y, w float32) float32 {
	u := &a.u
	models := a.mailModels()
	if len(models) == 0 {
		return a.paragraph(x, y, w, u.t("Download a language model or add an API key in Settings → AI models first"), 12, colDim) + u.px(6)
	}
	current, ok := a.mailModel()
	names := make([]string, len(models))
	at := -1
	for i, ch := range models {
		names[i] = a.mailModelName(ch)
		if ok && ch.ID == current.ID {
			at = i
		}
	}
	pick, h := u.flowChips(id, rectAt(x, y, w, u.px(28)), names, at)
	if pick >= 0 && pick != at {
		a.settings.MailModel = models[pick].ID
		a.saveSettings()
	}
	y += h + u.px(6)
	if ok && current.Online() {
		y = a.paragraph(x, y, w, u.tf("Each message analysed is sent to %s", current.Provider.Name.In(string(u.lang))), 12, colDangerText) + u.px(6)
	}
	return y
}
