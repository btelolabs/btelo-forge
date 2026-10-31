package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/btelolabs/btelo-forge/internal/ai"
	"github.com/btelolabs/btelo-forge/internal/mail"
)

// mailView is what the mail tab's list shows.
type mailView int

const (
	mailInbox mailView = iota
	mailImportant
	mailSuggested
	mailHistory
)

// mailProvider is a kind of mailbox the wizard knows.
type mailProvider struct {
	name, host, passwordURL string
	domains                 []string // addresses that are told to be this mailbox
	note                    string   // what to do for this mailbox, by message
}

// mailProviders are offered when an account is bound, the most used first; the last is any other server.
var mailProviders = []mailProvider{
	{name: "Gmail", host: "imap.gmail.com:993", passwordURL: "https://myaccount.google.com/apppasswords",
		domains: []string{"gmail.com", "googlemail.com"}, note: mailNotes[0]},
	{name: "iCloud", host: "imap.mail.me.com:993", passwordURL: "https://account.apple.com",
		domains: []string{"icloud.com", "me.com", "mac.com"}, note: mailNotes[1]},
	{name: "Yahoo", host: "imap.mail.yahoo.com:993", passwordURL: "https://login.yahoo.com/account/security",
		domains: []string{"yahoo.com", "ymail.com", "rocketmail.com", "sbcglobal.net"}, note: mailNotes[2]},
	{name: "AOL / Verizon", host: "imap.aol.com:993", passwordURL: "https://login.aol.com/account/security",
		domains: []string{"aol.com", "verizon.net"}, note: mailNotes[2]},
	{name: "Proton", host: "127.0.0.1:1143", passwordURL: "https://proton.me/mail/bridge",
		domains: []string{"proton.me", "protonmail.com", "pm.me"}, note: mailNotes[3]},
	{name: "Fastmail", host: "imap.fastmail.com:993", passwordURL: "https://app.fastmail.com/settings/security",
		domains: []string{"fastmail.com", "fastmail.fm"}, note: mailNotes[4]},
	{name: "Zoho", host: "imap.zoho.com:993", passwordURL: "https://accounts.zoho.com",
		domains: []string{"zoho.com", "zohomail.com"}, note: mailNotes[5]},
	{name: "Xfinity", host: "imap.comcast.net:993", domains: []string{"comcast.net", "xfinity.com"}, note: mailNotes[6]},
	{name: "AT&T", host: "imap.mail.att.net:993", domains: []string{"att.net"}, note: mailNotes[6]},
	{name: "GMX", host: "imap.gmx.com:993", domains: []string{"gmx.com", "gmx.us"}, note: mailNotes[7]},
	{name: "mail.com", host: "imap.mail.com:993", domains: []string{"mail.com"}, note: mailNotes[7]},
	{name: "QQ", host: "imap.qq.com:993", passwordURL: "https://service.mail.qq.com/detail/0/75", domains: []string{"qq.com"}, note: mailNotes[8]},
	{name: "163", host: "imap.163.com:993", passwordURL: "https://mail.163.com", domains: []string{"163.com", "126.com"}, note: mailNotes[8]},
	{},
}

// mailNotes say where a mailbox's password comes from.
var mailNotes = msgs(
	"Turn on two-step verification, then create an app password in your Google Account.",
	"Turn on two-factor authentication, then create an app-specific password under Sign-In and Security.",
	"Create an app password in your account's security settings.",
	"Install Proton Mail Bridge, a paid plan's program, and use the password it shows under Mailbox details.",
	"Create an app password under Privacy & Security; Fastmail's Basic plan has no IMAP.",
	"Turn on IMAP in the mail settings. With two-factor authentication on, create an app password.",
	"Use your account's mail password, or its Secure Mail Key on AT&T.",
	"Turn on IMAP access in the mailbox's settings first.",
	"Turn on IMAP in the mailbox's settings and use the authorisation code it gives.",
)

// mailRefused are the addresses that cannot be bound: Microsoft takes no password over IMAP from a personal mailbox.
var mailRefused = []string{"outlook.com", "hotmail.com", "live.com", "msn.com"}

func mailCannotBind(email string) bool {
	_, domain, _ := strings.Cut(strings.ToLower(email), "@")
	return slices.Contains(mailRefused, domain)
}

// mailProviderOf is the place in mailProviders of an address, or -1.
func mailProviderOf(email string) int {
	_, domain, ok := strings.Cut(strings.ToLower(strings.TrimSpace(email)), "@")
	if !ok {
		return -1
	}
	for i, p := range mailProviders {
		if slices.Contains(p.domains, domain) {
			return i
		}
	}
	return -1
}

// mailState is the mail tab: the accounts bound, the copy of their mail on this computer, and what is being done.
type mailState struct {
	path    string // the accounts' file
	keys    *ai.Keys
	keysErr error
	mailer  *mail.Mailer
	opened  bool // the database is open
	failed  error
	dial    func(ctx context.Context, address string) (*mail.Conn, error) // the tests' server, else the real one

	accounts []mail.Account
	hidden   map[string]bool // accounts left out of the list
	view     mailView
	search   string
	list     []mail.Message
	counts   map[string]int
	offers   []mail.Suggestion
	log      []mail.LogEntry
	stale    bool
	loads    int // which load is the newest, as they finish in the background

	current int64 // the message open, 0 for none
	sideOn  bool  // a narrow window shows the mailboxes instead of the list
	scroll  [4]float32
	content [4]float32

	binding  bool // the form that binds an account is open
	provider int
	email    string
	password string
	host     string
	bindErr  string
	signing  bool
	removing string // the account whose removal waits for confirmation

	syncing   bool
	syncText  string
	lastSync  time.Time
	organised bool // the model is working
	again     bool // and more mail arrived meanwhile
	stop      context.CancelFunc
	notice    string
	drafting  bool
	draft     string
	draftFor  int64
	busy      string // the change being made on a server
}

func (s *mailState) init(stateDir string, wake func()) {
	s.path = filepath.Join(stateDir, "mail-accounts.json")
	s.keys, s.keysErr = ai.OpenKeys(filepath.Join(stateDir, "mail-keys.json"))
	s.hidden, s.counts = map[string]bool{}, map[string]int{}
	s.stale = true
	if data, err := os.ReadFile(s.path); err == nil {
		s.failed = json.Unmarshal(data, &s.accounts)
	}
}

// close stops what is running and closes the database.
func (s *mailState) close() {
	if s.stop != nil {
		s.stop()
	}
	if s.opened {
		s.mailer.Store.Close()
	}
}

// mailOpen makes sure the database is open, where the user's mail is kept.
func (a *App) mailOpen() error {
	s := &a.mail
	if s.opened {
		return nil
	}
	dir, err := a.categoryDir(categoryIntelligence)
	if err != nil {
		return err
	}
	store, err := mail.OpenStore(filepath.Join(dir, "mail", "mail.db"))
	if err != nil {
		return err
	}
	s.mailer = &mail.Mailer{Store: store, Password: s.keys.Get, Dial: s.dial}
	s.opened = true
	return nil
}

func (s *mailState) saveAccounts() error {
	data, err := json.MarshalIndent(s.accounts, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

func (s *mailState) account(id string) (mail.Account, bool) {
	for _, ac := range s.accounts {
		if ac.ID == id {
			return ac, true
		}
	}
	return mail.Account{}, false
}

// chosen are the ids of the accounts the list shows.
func (s *mailState) chosen() []string {
	out := []string{}
	for _, ac := range s.accounts {
		if !s.hidden[ac.ID] {
			out = append(out, ac.ID)
		}
	}
	return out
}

// mailLoad reads what the list shows, in the background: a mailbox is large enough to be felt in a frame.
func (a *App) mailLoad() {
	s := &a.mail
	s.stale = false
	if !s.opened {
		return
	}
	s.loads++
	load := s.loads
	store := s.mailer.Store
	filter := mail.Filter{Accounts: s.chosen(), Inbox: true, Search: s.search}
	if s.view == mailImportant {
		filter.Priority = mail.PriorityHigh
	}
	none := len(filter.Accounts) == 0
	a.background(func(context.Context) {
		var list []mail.Message
		var offers []mail.Suggestion
		var err error
		if !none {
			list, err = store.List(filter)
		}
		if err == nil && !none {
			offers, err = store.Suggestions(filter.Accounts)
		}
		counts, cerr := store.Counts()
		if err == nil {
			err = cerr
		}
		log, lerr := store.Log(200)
		if err == nil {
			err = lerr
		}
		a.post(func() {
			if load != a.mail.loads {
				return
			}
			s := &a.mail
			if err != nil {
				s.notice = a.describe(err)
				return
			}
			s.list, s.offers, s.counts, s.log = list, offers, counts, log
			if s.view == mailSuggested {
				s.list = slices.DeleteFunc(s.list, func(m mail.Message) bool {
					return !slices.ContainsFunc(offers, func(g mail.Suggestion) bool { return g.Message == m.ID })
				})
			}
		})
	})
}

// describeMail says what went wrong in the user's words.
func (a *App) describeMail(err error) string {
	u := &a.u
	switch {
	case errors.Is(err, mail.ErrLogin):
		return u.t("The mail server refused the sign-in. Use an app password or authorisation code, not your usual password.")
	case errors.Is(err, mail.ErrNoLabels):
		return u.t("This mailbox has no labels")
	}
	return a.describe(err)
}

// mailBind tests an account with the server and keeps it when it signs in.
func (a *App) mailBind() {
	s := &a.mail
	if err := a.mailOpen(); err != nil {
		s.bindErr = a.describe(err)
		return
	}
	p := mailProviders[s.provider]
	host := p.host
	if host == "" {
		host = strings.TrimSpace(s.host)
		if !strings.Contains(host, ":") {
			host += ":993"
		}
	}
	email, password := strings.TrimSpace(s.email), strings.ReplaceAll(s.password, " ", "")
	if !strings.Contains(email, "@") || password == "" || strings.HasPrefix(host, ":") {
		s.bindErr = a.u.t("Fill in the address, the password and the server")
		return
	}
	if mailCannotBind(email) {
		s.bindErr = a.u.t("Outlook, Hotmail and Live mailboxes cannot be bound: Microsoft takes no password from this app")
		return
	}
	id := ai.Slug(email)
	if _, taken := s.account(id); taken {
		s.bindErr = a.u.t("This mailbox is bound already")
		return
	}
	ac := mail.Account{ID: id, Email: email, Host: host}
	s.signing, s.bindErr = true, ""
	mailer := s.mailer
	a.background(func(ctx context.Context) {
		c, err := mailer.Sign(ctx, ac, password)
		if err == nil {
			c.Close()
		}
		a.post(func() {
			s := &a.mail
			s.signing = false
			if err != nil {
				s.bindErr = a.describeMail(err)
				return
			}
			if err := s.keys.Set(ac.ID, password); err != nil {
				s.bindErr = a.describe(err)
				return
			}
			s.accounts = append(s.accounts, ac)
			if err := s.saveAccounts(); err != nil {
				s.notice = a.describe(err)
			}
			s.binding, s.email, s.password, s.host = false, "", "", ""
			a.mailSync()
		})
	})
}

// mailRemove unbinds an account: its password and its copy here go, and the mailbox itself is not touched.
func (a *App) mailRemove(id string) {
	s := &a.mail
	s.accounts = slices.DeleteFunc(s.accounts, func(ac mail.Account) bool { return ac.ID == id })
	delete(s.hidden, id)
	s.removing = ""
	if err := s.keys.Set(id, ""); err != nil {
		s.notice = a.describe(err)
	}
	if err := s.saveAccounts(); err != nil {
		s.notice = a.describe(err)
	}
	if s.opened {
		if err := s.mailer.Store.RemoveAccount(id); err != nil {
			s.notice = a.describe(err)
		}
	}
	s.current, s.stale = 0, true
}

// mailSync brings every account up to date, one after another, then has new mail read when a model is on this computer.
func (a *App) mailSync() {
	s := &a.mail
	if s.syncing || len(s.accounts) == 0 {
		return
	}
	if err := a.mailOpen(); err != nil {
		s.notice = a.describe(err)
		return
	}
	s.syncing, s.notice = true, ""
	s.lastSync = time.Now()
	accounts := slices.Clone(s.accounts)
	mailer := s.mailer
	progress := a.u.t("Syncing %s: %d of %d")
	a.background(func(ctx context.Context) {
		var failures []string
		arrived := 0
		for _, ac := range accounts {
			n, err := mailer.Sync(ctx, ac, func(done, total int) {
				a.post(func() {
					a.mail.syncText = fmt.Sprintf(progress, ac.Email, done, total)
					a.mail.stale = true
				})
			})
			arrived += n
			if err != nil {
				failures = append(failures, ac.Email+": "+a.describeMail(err))
			}
		}
		a.post(func() {
			s := &a.mail
			s.syncing, s.syncText, s.stale = false, "", true
			s.notice = strings.Join(failures, "\n")
			if arrived > 0 {
				a.mailOrganise(false)
			}
		})
	})
}

// mailModel is the model that reads mail: the user's choice among those of this computer, else the first downloaded.
func (a *App) mailModel() (ai.Choice, bool) {
	var first ai.Choice
	found := false
	for _, ch := range a.ai.config.Choices() {
		if ch.Kind != ai.KindLLM || ch.Local == nil {
			continue
		}
		if _, have := a.ai.rt.Files(*ch.Local); !have {
			continue
		}
		if ch.ID == a.settings.MailModel {
			return ch, true
		}
		if !found {
			first, found = ch, true
		}
	}
	return first, found
}

// mailAsk is how the mail asks a model, which goes through the one place models are started.
func (a *App) mailAsk(ch ai.Choice) mail.Ask {
	cfg, engine := a.ai.config, a.settings.AIEngine
	return func(ctx context.Context, system, text string) (string, error) {
		ep, err := a.aiEndpoint(ctx, cfg, ch, engine, &aiAnswer{})
		if err != nil {
			return "", err
		}
		return ep.Complete(ctx, []ai.Message{{Role: ai.System, Text: system}, {Role: ai.User, Text: text}}, ai.Options{})
	}
}

// mailOrganiseBatch is how many messages one round reads.
const mailOrganiseBatch = 200

// mailOrganise has the model read the mail no model has read, of the accounts chosen. A model that is not on this
// computer is not downloaded unasked: asked says the user pressed the button, and is then told.
func (a *App) mailOrganise(asked bool) {
	s := &a.mail
	if s.organised {
		s.again = true // mail that arrived during the reading is read after it
		return
	}
	if !s.opened {
		return
	}
	ch, ok := a.mailModel()
	if !ok {
		if asked {
			s.notice = a.u.t("Download a language model in Settings → AI models first; mail is read only by a model on this computer")
		}
		return
	}
	accounts := s.chosen()
	if len(accounts) == 0 {
		return
	}
	ctx, stop := context.WithCancel(a.life)
	s.organised, s.stop = true, stop
	store, ask := s.mailer.Store, a.mailAsk(ch)
	reading := a.u.t("Reading mail: %d of %d")
	a.background(func(context.Context) {
		defer stop()
		list, err := store.Unorganised(accounts, mailOrganiseBatch)
		skipped := 0
		for i, m := range list {
			if err != nil || ctx.Err() != nil {
				break
			}
			a.post(func() { a.mail.syncText = fmt.Sprintf(reading, i+1, len(list)) })
			var v mail.Verdict
			if v, err = mail.Organise(ctx, ask, m); err != nil {
				if ctx.Err() == nil && strings.Contains(err.Error(), "form asked for") {
					// One answer in the wrong form says nothing of the next message.
					skipped++
					err = nil
				}
				continue
			}
			if err = store.SaveOrganised(m.ID, ch.ID, v); err == nil {
				a.post(func() { a.mail.stale = true })
			}
		}
		a.post(func() {
			s := &a.mail
			s.organised, s.syncText, s.stale = false, "", true
			again := s.again && err == nil
			s.again = false
			defer func() {
				if again {
					a.mailOrganise(false)
				}
			}()
			switch {
			case err != nil && !errors.Is(err, context.Canceled):
				s.notice = a.describeAI(err)
			case skipped > 0:
				s.notice = a.u.tf("The model did not answer for %d messages; they stay unread by it", skipped)
			}
		})
	})
}

// mailApply makes a change on the server, which only a click of the user's ever starts.
func (a *App) mailApply(g mail.Suggestion, ac mail.Account) {
	s := &a.mail
	if s.busy != "" {
		return
	}
	s.busy = ac.ID
	mailer := s.mailer
	a.background(func(ctx context.Context) {
		err := mailer.Apply(ctx, ac, g)
		a.post(func() {
			s := &a.mail
			s.busy, s.stale = "", true
			if err != nil {
				s.notice = a.describeMail(err)
			}
		})
	})
}

// mailUndo reverses a change of the log on the server.
func (a *App) mailUndo(e mail.LogEntry) {
	s := &a.mail
	ac, ok := s.account(e.Account)
	if !ok || s.busy != "" {
		return
	}
	s.busy = ac.ID
	mailer := s.mailer
	a.background(func(ctx context.Context) {
		err := mailer.Undo(ctx, ac, e.ID)
		a.post(func() {
			s := &a.mail
			s.busy, s.stale = "", true
			if err != nil {
				s.notice = a.describeMail(err)
			}
		})
	})
}

// mailDraftReply has the model write a reply into the box, where the user changes it.
func (a *App) mailDraftReply(m mail.Message) {
	s := &a.mail
	ch, ok := a.mailModel()
	if !ok {
		s.draftFor, s.draft = m.ID, ""
		return
	}
	s.drafting, s.draftFor, s.draft = true, m.ID, ""
	ask := a.mailAsk(ch)
	a.background(func(ctx context.Context) {
		text, err := mail.DraftReply(ctx, ask, m)
		a.post(func() {
			s := &a.mail
			s.drafting = false
			if err != nil {
				s.notice = a.describeAI(err)
				return
			}
			if s.draftFor == m.ID {
				s.draft = strings.TrimSpace(text)
			}
		})
	})
}

// mailSaveDraft puts the reply in the account's drafts on the server. It is not sent.
func (a *App) mailSaveDraft(m mail.Message) {
	s := &a.mail
	ac, ok := s.account(m.Account)
	if !ok || s.busy != "" {
		return
	}
	s.busy = ac.ID
	text, mailer := s.draft, s.mailer
	saved := a.u.t("The reply is in the drafts of the mailbox; it was not sent")
	a.background(func(ctx context.Context) {
		err := mailer.SaveDraft(ctx, ac, m, text, time.Now())
		a.post(func() {
			s := &a.mail
			s.busy = ""
			if err != nil {
				s.notice = a.describeMail(err)
				return
			}
			s.draft, s.draftFor = "", 0
			a.toast(saved, false)
		})
	})
}

// mailClosed is what closing the tab ends: the model's reading and the model, unless the chat is using it.
func (a *App) mailClosed() {
	s := &a.mail
	if s.stop != nil {
		s.stop()
	}
	if ch, ok := a.mailModel(); ok && !slices.ContainsFunc(a.docs, func(d *doc) bool { return d.aiTab }) {
		a.ai.rt.Unload(ch.ID)
	}
}

// mailDue reports whether it is time to look for new mail by itself.
func (a *App) mailDue() bool {
	s := &a.mail
	every := a.settings.MailEvery
	if every == 0 {
		every = 15
	}
	return every > 0 && len(s.accounts) > 0 && !s.syncing && time.Since(s.lastSync) >= time.Duration(every)*time.Minute
}
