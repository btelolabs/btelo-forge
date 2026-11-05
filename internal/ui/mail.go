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
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/mail"
)

// mailView is what the mail tab's list shows.
type mailView int

const (
	mailMust mailView = iota
	mailMaybe
	mailNoise
	mailNew // what no model has read
	mailStarred
	mailSent
	mailAll
	mailHistory
)

// mailJob is a change to many messages on the servers, which a card shows while it runs.
type mailJob struct {
	title     string
	done, all int
	stop      context.CancelFunc
	stopping  bool
}

// mailProvider is a kind of mailbox the wizard knows.
type mailProvider struct {
	name, host, smtp, passwordURL string
	domains                       []string // addresses that are told to be this mailbox
	note                          string   // what to do for this mailbox, by message
}

// mailProviders are offered when an account is bound, the most used first; the last is any other server.
var mailProviders = []mailProvider{
	{name: "Gmail", host: "imap.gmail.com:993", smtp: "smtp.gmail.com:465", passwordURL: "https://myaccount.google.com/apppasswords",
		domains: []string{"gmail.com", "googlemail.com"}, note: mailNotes[0]},
	{name: "iCloud", host: "imap.mail.me.com:993", smtp: "smtp.mail.me.com:587", passwordURL: "https://account.apple.com",
		domains: []string{"icloud.com", "me.com", "mac.com"}, note: mailNotes[1]},
	{name: "Yahoo", host: "imap.mail.yahoo.com:993", smtp: "smtp.mail.yahoo.com:465", passwordURL: "https://login.yahoo.com/account/security",
		domains: []string{"yahoo.com", "ymail.com", "rocketmail.com", "sbcglobal.net"}, note: mailNotes[2]},
	{name: "AOL / Verizon", host: "imap.aol.com:993", smtp: "smtp.aol.com:465", passwordURL: "https://login.aol.com/account/security",
		domains: []string{"aol.com", "verizon.net"}, note: mailNotes[2]},
	{name: "Proton", host: "127.0.0.1:1143", smtp: "127.0.0.1:1025", passwordURL: "https://proton.me/mail/bridge",
		domains: []string{"proton.me", "protonmail.com", "pm.me"}, note: mailNotes[3]},
	{name: "Fastmail", host: "imap.fastmail.com:993", smtp: "smtp.fastmail.com:465", passwordURL: "https://app.fastmail.com/settings/security",
		domains: []string{"fastmail.com", "fastmail.fm"}, note: mailNotes[4]},
	{name: "Zoho", host: "imap.zoho.com:993", smtp: "smtp.zoho.com:465", passwordURL: "https://accounts.zoho.com",
		domains: []string{"zoho.com", "zohomail.com"}, note: mailNotes[5]},
	{name: "Xfinity", host: "imap.comcast.net:993", smtp: "smtp.comcast.net:587", domains: []string{"comcast.net", "xfinity.com"}, note: mailNotes[6]},
	{name: "AT&T", host: "imap.mail.att.net:993", smtp: "smtp.mail.att.net:465", domains: []string{"att.net"}, note: mailNotes[6]},
	{name: "GMX", host: "imap.gmx.com:993", smtp: "mail.gmx.com:465", domains: []string{"gmx.com", "gmx.us"}, note: mailNotes[7]},
	{name: "mail.com", host: "imap.mail.com:993", smtp: "smtp.mail.com:465", domains: []string{"mail.com"}, note: mailNotes[7]},
	{name: "QQ", host: "imap.qq.com:993", smtp: "smtp.qq.com:465", passwordURL: "https://service.mail.qq.com/detail/0/75", domains: []string{"qq.com"}, note: mailNotes[8]},
	{name: "163", host: "imap.163.com:993", smtp: "smtp.163.com:465", passwordURL: "https://mail.163.com", domains: []string{"163.com", "126.com"}, note: mailNotes[8]},
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

	accounts   []mail.Account
	hidden     map[string]bool // accounts left out of the list
	view       mailView
	viewChosen bool // by the user, so it is left alone
	search     string
	list       []mail.Message
	counts     map[string]int
	tiers      map[string]mail.TierCount
	senders    map[string]string // what the user said of an address, by address in lower case
	log        []mail.LogEntry
	loaded     bool // the list has been read once, so an empty one means no mail
	stale      bool
	loads      int // which load is the newest, as they finish in the background

	current int64 // the message open, 0 for none
	page    mailPage
	web     WebView  // the system's browser engine, opened for the first page
	webOff  bool     // there is none to be had
	webAt   gfx.Rect // where this frame wants it
	webLast gfx.Rect // where the last frame had it, which what is drawn after the page goes around
	sideOn  bool     // a narrow window shows the mailboxes instead of the list
	scroll  [4]float32
	content [4]float32

	binding  bool // the form that binds an account is open
	provider int
	email    string
	password string
	host     string
	smtp     string
	bindErr  string
	signing  bool
	removing string // the account whose removal waits for confirmation

	syncing   bool
	syncText  string
	syncRatio float64 // how far the sync has got; below 0 when that cannot be told
	syncStop  context.CancelFunc
	lastSync  time.Time
	unread    int    // how many messages of the accounts chosen the model has to read
	organised bool   // the model is working
	read      [2]int // how many messages the model has read, of how many
	stop      context.CancelFunc
	job       *mailJob
	trashing  bool // the question whether to delete a tier is showing
	notice    string
	drafting  bool
	letter    *mailLetter // the letter being written
	busy      string      // the change being made on a server
}

func (s *mailState) init(stateDir string, wake func()) {
	s.path = filepath.Join(stateDir, "mail-accounts.json")
	s.keys, s.keysErr = ai.OpenKeys(filepath.Join(stateDir, "mail-keys.json"))
	s.hidden, s.counts, s.tiers, s.senders = map[string]bool{}, map[string]int{}, map[string]mail.TierCount{}, map[string]string{}
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
	if s.web != nil {
		s.web.Close()
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
	switch s.view {
	case mailMust:
		filter.Tiers = []string{mail.TierMust}
	case mailMaybe:
		filter.Tiers = []string{mail.TierMaybe}
	case mailNoise:
		filter.Tiers = []string{mail.TierNoise}
	case mailNew:
		filter.Tiers = []string{""}
	case mailStarred:
		filter.Inbox, filter.Starred = false, true
	case mailSent:
		filter.Inbox, filter.Box = false, mail.BoxSent
	}
	none := len(filter.Accounts) == 0
	model, _ := a.mailModel()
	a.background(func(context.Context) {
		var list []mail.Message
		tiers := map[string]mail.TierCount{}
		unread := 0
		var err error
		if !none {
			list, err = store.List(filter)
		}
		if err == nil && !none {
			tiers, err = store.TierCounts(filter.Accounts)
		}
		if err == nil && !none {
			unread, err = store.CountUnorganised(filter.Accounts, model.ID)
		}
		counts, cerr := store.Counts()
		if err == nil {
			err = cerr
		}
		senders, serr := store.Senders()
		if err == nil {
			err = serr
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
			s.list, s.tiers, s.unread, s.senders, s.counts, s.log, s.loaded = list, tiers, unread, senders, counts, log, true
			if !s.viewChosen && s.view == mailMust && len(list) == 0 && tiers[""].All > 0 {
				s.view, s.stale = mailNew, true // the first view would stand empty
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
	case errors.Is(err, mail.ErrNoTrash):
		return u.t("This mailbox has no trash folder, so nothing was deleted")
	case errors.Is(err, mail.ErrNoRecipient):
		return u.t("Say who this letter is for")
	case errors.Is(err, mail.ErrTooLarge):
		return u.t("The files of this letter are too large to send: 25 MB at most")
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
	ac := mail.Account{ID: id, Email: email, Host: host, SMTP: strings.TrimSpace(s.smtp)}
	if ac.SMTP != "" && !strings.Contains(ac.SMTP, ":") {
		ac.SMTP += ":465"
	}
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
			s.binding, s.email, s.password, s.host, s.smtp = false, "", "", "", ""
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

// mailSync brings every account up to date, one after another.
func (a *App) mailSync() {
	s := &a.mail
	if s.syncing || len(s.accounts) == 0 {
		return
	}
	if err := a.mailOpen(); err != nil {
		s.notice = a.describe(err)
		return
	}
	ctx, stop := context.WithCancel(a.life)
	s.syncing, s.syncStop, s.syncRatio, s.notice = true, stop, -1, ""
	s.lastSync = time.Now()
	accounts := slices.Clone(s.accounts)
	mailer := s.mailer
	progress, paging := a.u.t("%s: %d of %d"), a.u.t("Fetching the pages of %s: %d of %d")
	a.background(func(context.Context) {
		defer stop()
		var failures []string
		show := func(text string, done, total int) {
			a.post(func() {
				a.mail.syncText, a.mail.syncRatio = text, float64(done)/float64(max(total, 1))
				a.mail.stale = true
			})
		}
		for _, ac := range accounts {
			_, err := mailer.Sync(ctx, ac, func(done, total int) { show(fmt.Sprintf(progress, ac.Email, done, total), done, total) })
			if err == nil {
				err = mailer.FillPages(ctx, ac, func(done, total int) { show(fmt.Sprintf(paging, ac.Email, done, total), done, total) })
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				failures = append(failures, ac.Email+": "+a.describeMail(err))
			}
		}
		a.post(func() {
			s := &a.mail
			s.syncing, s.syncText, s.stale = false, "", true
			s.notice = strings.Join(failures, "\n")
		})
	})
}

// mailBestModel sorted real mail best of the models tried, thinking off: 27 of 32 hard messages, against 22 and 23 of the others.
const mailBestModel = "qwen3-8b"

// mailModel is the model that reads mail: the user's choice, else mailBestModel, else the first downloaded. A model of a
// service is only ever the user's choice, as the mail then leaves this computer.
func (a *App) mailModel() (ai.Choice, bool) {
	var first ai.Choice
	found, best := false, false
	for _, ch := range a.mailModels() {
		if ch.ID == a.settings.MailModel {
			return ch, true
		}
		if ch.Online() {
			continue
		}
		if !found || (!best && ch.ID == mailBestModel) {
			first, found, best = ch, true, ch.ID == mailBestModel
		}
	}
	return first, found
}

// mailAsk is how the mail asks a model, which goes through the one place models are started.
func (a *App) mailAsk(ch ai.Choice, opt ai.Options) mail.Ask {
	cfg, engine := a.ai.config, a.settings.AIEngine
	return func(ctx context.Context, system, text string) (string, error) {
		ep, err := a.aiEndpoint(ctx, cfg, ch, engine, &aiAnswer{})
		if err != nil {
			return "", err
		}
		return ep.Complete(ctx, []ai.Message{{Role: ai.System, Text: system}, {Role: ai.User, Text: text}}, opt)
	}
}

// mailOrganiseOptions make a verdict quick and steady: thinking takes a minute a message and changes no answer asked of a list.
func mailOrganiseOptions() ai.Options {
	temperature := 0.1
	return ai.Options{Temperature: &temperature, MaxTokens: 300, NoThinking: true}
}

// mailOrganise has the model read mail no model has read, of the accounts chosen. A model holds gigabytes, so only the
// user's click starts one, says how many of the newest it reads, and it stops when the reading ends (mailRelease).
func (a *App) mailOrganise(limit int) {
	s := &a.mail
	if s.organised || !s.opened {
		return
	}
	ch, ok := a.mailModel()
	if !ok {
		s.notice = a.u.t("Download a language model or add an API key in Settings → AI models first")
		return
	}
	accounts := s.chosen()
	if len(accounts) == 0 {
		return
	}
	ctx, stop := context.WithCancel(a.life)
	s.organised, s.stop = true, stop
	store, ask := s.mailer.Store, a.mailAsk(ch, mailOrganiseOptions())
	a.background(func(context.Context) {
		defer stop()
		skipped, err := store.OrganiseSome(ctx, ask, accounts, ch.ID, limit, func(done, total int) {
			a.post(func() {
				a.mail.read = [2]int{done, total}
				a.mail.stale = true
			})
		})
		a.post(func() {
			s := &a.mail
			s.organised, s.read, s.stale = false, [2]int{}, true
			a.mailRelease()
			switch {
			case err != nil && !errors.Is(err, context.Canceled):
				s.notice = a.describeAI(err)
			case skipped > 0:
				s.notice = a.u.tf("The model did not answer for %d messages; they stay unread by it", skipped)
			}
		})
	})
}

// mailArchive takes a message out of the inbox on the server, which only a click of the user's ever starts.
func (a *App) mailArchive(id int64, ac mail.Account) {
	s := &a.mail
	if s.busy != "" {
		return
	}
	s.busy = ac.ID
	mailer := s.mailer
	a.background(func(ctx context.Context) {
		err := mailer.Archive(ctx, ac, id)
		a.post(func() {
			s := &a.mail
			s.busy, s.stale = "", true
			if err != nil {
				s.notice = a.describeMail(err)
			}
		})
	})
}

// mailRun does work on the servers behind a card, which says how far it has got and can stop it.
func (a *App) mailRun(title string, work func(ctx context.Context, progress func(done, all int)) error) {
	s := &a.mail
	if s.busy != "" {
		return
	}
	ctx, stop := context.WithCancel(a.life)
	job := &mailJob{title: title, stop: stop}
	s.busy, s.job, s.trashing = "job", job, false
	a.background(func(context.Context) {
		defer stop()
		err := work(ctx, func(done, all int) {
			a.post(func() {
				job.done, job.all = done, all
				a.mail.stale = true
			})
		})
		a.post(func() {
			s := &a.mail
			s.busy, s.job, s.stale = "", nil, true
			if err != nil && !errors.Is(err, context.Canceled) {
				s.notice = a.describeMail(err)
			}
		})
	})
}

// mailChange changes all the mail of a tier on the servers of the chosen mailboxes, at the user's click.
func (a *App) mailChange(tier, action string) {
	s := &a.mail
	store, mailer, chosen := s.mailer.Store, s.mailer, s.chosen()
	accounts := map[string]mail.Account{}
	for _, id := range chosen {
		accounts[id], _ = s.account(id)
	}
	title := a.u.t("Marking mail read")
	change := mailer.MarkRead
	if action == mail.ActionTrash {
		title, change = a.u.t("Moving mail to the trash"), mailer.Trash
	}
	a.mailRun(title, func(ctx context.Context, progress func(done, all int)) error {
		target, err := store.Target(chosen, tier, action)
		if err != nil {
			return err
		}
		all, done := 0, 0
		for _, ids := range target {
			all += len(ids)
		}
		for id, ids := range target {
			if err := change(ctx, accounts[id], ids, func(n, _ int) { progress(done+n, all) }); err != nil {
				return err
			}
			done += len(ids)
		}
		return nil
	})
}

// mailUndo reverses a change of the log on the server.
func (a *App) mailUndo(e mail.LogEntry) {
	s := &a.mail
	ac, ok := s.account(e.Account)
	if !ok {
		return
	}
	mailer := s.mailer
	a.mailRun(a.u.t("Undoing the change"), func(ctx context.Context, progress func(done, all int)) error {
		return mailer.Undo(ctx, ac, e.ID, progress)
	})
}

// mailTasks are the cards of the work the mail is doing, whichever tab is in front.
func (a *App) mailTasks() []task {
	s := &a.mail
	u := &a.u
	var out []task
	if s.syncing {
		out = append(out, task{id: "mail/sync", glyph: icSync, title: u.t("Looking for new mail"), status: s.syncText, fraction: s.syncRatio,
			cancel: s.syncStop, open: a.openMailTab})
	}
	if s.organised {
		t := task{id: "mail/read", glyph: icThink, title: u.t("The model is sorting mail"), fraction: -1, cancel: s.stop, open: a.openMailTab}
		if s.read[1] > 0 {
			t.status, t.fraction = u.tf("%d of %d", s.read[0], s.read[1]), float64(s.read[0])/float64(s.read[1])
		}
		out = append(out, t)
	}
	if j := s.job; j != nil {
		t := task{id: "mail/job", glyph: icMail, title: j.title, fraction: -1, stopping: j.stopping, open: a.openMailTab}
		t.cancel = func() {
			j.stopping = true
			j.stop()
		}
		if j.all > 0 {
			t.status, t.fraction = u.tf("%d of %d", j.done, j.all), float64(j.done)/float64(j.all)
		}
		out = append(out, t)
	}
	return out
}

// mailRelease stops the mail's model once nothing of the mail needs it, unless the chat may be using it.
func (a *App) mailRelease() {
	s := &a.mail
	if s.organised || s.drafting {
		return
	}
	if ch, ok := a.mailModel(); ok && !slices.ContainsFunc(a.docs, func(d *doc) bool { return d.aiTab }) {
		a.ai.rt.Unload(ch.ID)
	}
}

// mailClosed is what closing the tab ends: the model's reading and the model.
func (a *App) mailClosed() {
	s := &a.mail
	if s.stop != nil {
		s.stop()
	}
	a.mailRelease()
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
