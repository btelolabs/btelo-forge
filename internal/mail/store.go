package mail

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // the database, without cgo
)

// Account is a mailbox the user bound. Its password is kept apart, sealed.
type Account struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Host  string `json:"host"` // host:port
	Name  string `json:"name,omitempty"`
}

// iCloudHost asks for the part of the address before the @ on many accounts, and the whole address on others.
const iCloudHost = "imap.mail.me.com:993"

// Priorities a model gives a message.
const (
	PriorityHigh   = "high"
	PriorityNormal = "normal"
	PriorityLow    = "low"
)

// Message is a message kept here. One database holds every account's, which is what makes the common inbox and a choice
// of accounts one query.
type Message struct {
	ID        int64
	Account   string
	UID       uint32
	MessageID string
	GmailID   string
	Thread    string
	InReplyTo string
	From      string
	Address   string
	To        string
	Subject   string
	Date      time.Time
	Seen      bool
	Inbox     bool
	Labels    []string
	Text      string
	// What the model made of it; Organised is the model's id, "" until then.
	Summary   string
	Priority  string
	Category  string
	Organised string
}

// Filter chooses messages.
type Filter struct {
	Accounts []string // the accounts to show; none means all of them
	Inbox    bool     // only what is in an inbox
	Priority string
	Search   string
	Limit    int
}

// Suggestion is something the model proposes doing to a message on the server, which waits for the user.
type Suggestion struct {
	ID      int64
	Message int64
	Action  string // ActionLabel or ActionArchive
	Value   string
	Reason  string
}

// LogEntry is a change made on a server.
type LogEntry struct {
	ID      int64
	At      time.Time
	Message int64
	Account string
	Subject string
	Action  string
	Value   string
	Undone  bool
}

// The changes the app can make to a message on a server.
const (
	ActionLabel   = "label"
	ActionArchive = "archive"
)

const schema = `
CREATE TABLE IF NOT EXISTS messages (
	id INTEGER PRIMARY KEY,
	account TEXT NOT NULL, validity INTEGER NOT NULL, uid INTEGER NOT NULL,
	message_id TEXT NOT NULL DEFAULT '', gmail_id TEXT NOT NULL DEFAULT '', thread TEXT NOT NULL DEFAULT '',
	in_reply_to TEXT NOT NULL DEFAULT '',
	sender TEXT NOT NULL DEFAULT '', address TEXT NOT NULL DEFAULT '', recipient TEXT NOT NULL DEFAULT '',
	subject TEXT NOT NULL DEFAULT '', date INTEGER NOT NULL DEFAULT 0,
	seen INTEGER NOT NULL DEFAULT 0, inbox INTEGER NOT NULL DEFAULT 1, labels TEXT NOT NULL DEFAULT '[]',
	body TEXT NOT NULL DEFAULT '',
	summary TEXT NOT NULL DEFAULT '', priority TEXT NOT NULL DEFAULT '', category TEXT NOT NULL DEFAULT '',
	organised TEXT NOT NULL DEFAULT '',
	UNIQUE (account, validity, uid)
);
CREATE INDEX IF NOT EXISTS messages_date ON messages (date DESC);
CREATE TABLE IF NOT EXISTS state (account TEXT PRIMARY KEY, validity INTEGER NOT NULL, last_uid INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS suggestions (
	id INTEGER PRIMARY KEY,
	message INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
	action TEXT NOT NULL, value TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT '',
	UNIQUE (message, action, value)
);
CREATE TABLE IF NOT EXISTS log (
	id INTEGER PRIMARY KEY, at INTEGER NOT NULL,
	message INTEGER NOT NULL, account TEXT NOT NULL, subject TEXT NOT NULL,
	action TEXT NOT NULL, value TEXT NOT NULL DEFAULT '', undone INTEGER NOT NULL DEFAULT 0
);`

// Store is the mail kept on this computer.
type Store struct {
	db *sql.DB
}

// OpenStore opens the database at path, making it when it is not there.
func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // sync and the window take turns
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// RemoveAccount forgets everything kept of an account. The mailbox itself is not touched.
func (s *Store) RemoveAccount(id string) error {
	for _, q := range []string{"DELETE FROM messages WHERE account=?", "DELETE FROM state WHERE account=?", "DELETE FROM log WHERE account=?"} {
		if _, err := s.db.Exec(q, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) syncState(account string) (validity, last uint32, err error) {
	err = s.db.QueryRow("SELECT validity, last_uid FROM state WHERE account=?", account).Scan(&validity, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	return validity, last, err
}

func (s *Store) setSyncState(account string, validity, last uint32) error {
	_, err := s.db.Exec(`INSERT INTO state (account, validity, last_uid) VALUES (?, ?, ?)
		ON CONFLICT (account) DO UPDATE SET validity=excluded.validity, last_uid=excluded.last_uid`, account, validity, last)
	return err
}

// put keeps a message that has arrived. One that is already here, as a sync interrupted may fetch it twice, stays.
func (s *Store) put(account string, validity uint32, f Fetched) error {
	p := Parse(f.Raw)
	labels, _ := json.Marshal(f.Labels)
	_, err := s.db.Exec(`INSERT INTO messages (account, validity, uid, message_id, gmail_id, thread, in_reply_to, sender, address,
		recipient, subject, date, seen, inbox, labels, body) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,1,?,?)
		ON CONFLICT (account, validity, uid) DO NOTHING`,
		account, validity, f.UID, p.MessageID, f.GmailID, f.ThreadID, p.InReplyTo, p.From, p.Address, p.To, p.Subject,
		p.Date.Unix(), hasFlag(f.Flags, `\Seen`), string(labels), p.Text)
	return err
}

func hasFlag(flags []string, flag string) bool {
	for _, f := range flags {
		if strings.EqualFold(f, flag) {
			return true
		}
	}
	return false
}

// refresh brings what is known of the inbox up to date: flags and labels of what is in it, and what left it by another
// program.
func (s *Store) refresh(account string, validity uint32, present map[uint32]Fetched) error {
	rows, err := s.db.Query("SELECT id, uid FROM messages WHERE account=? AND validity=? AND inbox=1", account, validity)
	if err != nil {
		return err
	}
	type row struct {
		id  int64
		uid uint32
	}
	var known []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.uid); err != nil {
			rows.Close()
			return err
		}
		known = append(known, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range known {
		f, ok := present[r.uid]
		if !ok {
			if _, err := s.db.Exec("UPDATE messages SET inbox=0 WHERE id=?", r.id); err != nil {
				return err
			}
			continue
		}
		labels, _ := json.Marshal(f.Labels)
		if _, err := s.db.Exec("UPDATE messages SET seen=?, labels=? WHERE id=?", hasFlag(f.Flags, `\Seen`), string(labels), r.id); err != nil {
			return err
		}
	}
	return nil
}

const columns = `id, account, uid, message_id, gmail_id, thread, in_reply_to, sender, address, recipient, subject, date,
	seen, inbox, labels, body, summary, priority, category, organised`

func scanMessage(row interface{ Scan(...any) error }) (Message, error) {
	var m Message
	var date int64
	var labels string
	err := row.Scan(&m.ID, &m.Account, &m.UID, &m.MessageID, &m.GmailID, &m.Thread, &m.InReplyTo, &m.From, &m.Address, &m.To,
		&m.Subject, &date, &m.Seen, &m.Inbox, &labels, &m.Text, &m.Summary, &m.Priority, &m.Category, &m.Organised)
	if err != nil {
		return m, err
	}
	m.Date = time.Unix(date, 0)
	json.Unmarshal([]byte(labels), &m.Labels)
	return m, nil
}

func (f Filter) where() (string, []any) {
	var conds []string
	var args []any
	if len(f.Accounts) > 0 {
		conds = append(conds, "account IN (?"+strings.Repeat(",?", len(f.Accounts)-1)+")")
		for _, a := range f.Accounts {
			args = append(args, a)
		}
	}
	if f.Inbox {
		conds = append(conds, "inbox=1")
	}
	if f.Priority != "" {
		conds = append(conds, "priority=?")
		args = append(args, f.Priority)
	}
	if term := strings.TrimSpace(f.Search); term != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(term) + "%"
		conds = append(conds, `(subject LIKE ? ESCAPE '\' OR sender LIKE ? ESCAPE '\' OR address LIKE ? ESCAPE '\' OR body LIKE ? ESCAPE '\')`)
		args = append(args, like, like, like, like)
	}
	if len(conds) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// List gives the messages a filter chooses, the newest first, from all the accounts it names together.
func (s *Store) List(f Filter) ([]Message, error) {
	where, args := f.where()
	limit := f.Limit
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.Query("SELECT "+columns+" FROM messages"+where+" ORDER BY date DESC, id DESC LIMIT ?", append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Message gives one message.
func (s *Store) Message(id int64) (Message, error) {
	return scanMessage(s.db.QueryRow("SELECT "+columns+" FROM messages WHERE id=?", id))
}

// Counts says how many unread messages each account has in its inbox.
func (s *Store) Counts() (map[string]int, error) {
	rows, err := s.db.Query("SELECT account, COUNT(*) FROM messages WHERE inbox=1 AND seen=0 GROUP BY account")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var a string
		var n int
		if err := rows.Scan(&a, &n); err != nil {
			return nil, err
		}
		out[a] = n
	}
	return out, rows.Err()
}

// Unorganised gives the inbox messages of the chosen accounts that no model has read, the newest first.
func (s *Store) Unorganised(accounts []string, limit int) ([]Message, error) {
	where, args := Filter{Accounts: accounts, Inbox: true}.where()
	rows, err := s.db.Query("SELECT "+columns+" FROM messages"+where+" AND organised='' ORDER BY date DESC LIMIT ?", append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SaveOrganised keeps what a model made of a message and what it proposes, in one step.
func (s *Store) SaveOrganised(id int64, model string, v Verdict) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE messages SET summary=?, priority=?, category=?, organised=? WHERE id=?", v.Summary, v.Priority, v.Category, model, id); err != nil {
		return err
	}
	for _, a := range v.Actions {
		if _, err := tx.Exec("INSERT INTO suggestions (message, action, value, reason) VALUES (?,?,?,?) ON CONFLICT DO NOTHING", id, a.Action, a.Value, a.Reason); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Suggestions gives what waits for the user, for the chosen accounts.
func (s *Store) Suggestions(accounts []string) ([]Suggestion, error) {
	where, args := Filter{Accounts: accounts}.where()
	where = strings.ReplaceAll(where, "account IN", "m.account IN")
	rows, err := s.db.Query("SELECT g.id, g.message, g.action, g.value, g.reason FROM suggestions g JOIN messages m ON m.id=g.message"+where+" ORDER BY g.id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Suggestion
	for rows.Next() {
		var g Suggestion
		if err := rows.Scan(&g.ID, &g.Message, &g.Action, &g.Value, &g.Reason); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// Dismiss drops a suggestion.
func (s *Store) Dismiss(id int64) error {
	_, err := s.db.Exec("DELETE FROM suggestions WHERE id=?", id)
	return err
}

func (s *Store) logChange(m Message, action, value string) (int64, error) {
	r, err := s.db.Exec("INSERT INTO log (at, message, account, subject, action, value) VALUES (?,?,?,?,?,?)",
		time.Now().Unix(), m.ID, m.Account, m.Subject, action, value)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

// Log gives the changes made on the servers, the newest first.
func (s *Store) Log(limit int) ([]LogEntry, error) {
	rows, err := s.db.Query("SELECT id, at, message, account, subject, action, value, undone FROM log ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogEntry
	for rows.Next() {
		var e LogEntry
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.Message, &e.Account, &e.Subject, &e.Action, &e.Value, &e.Undone); err != nil {
			return nil, err
		}
		e.At = time.Unix(at, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) entry(id int64) (LogEntry, error) {
	var e LogEntry
	var at int64
	err := s.db.QueryRow("SELECT id, at, message, account, subject, action, value, undone FROM log WHERE id=?", id).
		Scan(&e.ID, &at, &e.Message, &e.Account, &e.Subject, &e.Action, &e.Value, &e.Undone)
	return e, err
}

func (s *Store) markUndone(id int64) error {
	_, err := s.db.Exec("UPDATE log SET undone=1 WHERE id=?", id)
	return err
}

func (s *Store) setInbox(id int64, in bool) error {
	_, err := s.db.Exec("UPDATE messages SET inbox=? WHERE id=?", in, id)
	return err
}

func (s *Store) setLabels(id int64, labels []string) error {
	data, _ := json.Marshal(labels)
	_, err := s.db.Exec("UPDATE messages SET labels=? WHERE id=?", string(data), id)
	return err
}

// Mailer reaches the servers of the accounts: it signs in, syncs and makes the changes the user approved.
type Mailer struct {
	Store    *Store
	Password func(account string) (string, error)
	// Dial connects to a server; the tests point it at a server of their own.
	Dial func(ctx context.Context, address string) (*Conn, error)
	Now  func() time.Time
	// Days is how far back the first sync of an account goes.
	Days int
}

func (m *Mailer) connect(ctx context.Context, a Account) (*Conn, error) {
	password, err := m.Password(a.ID)
	if err != nil {
		return nil, err
	}
	return m.Sign(ctx, a, password)
}

func (m *Mailer) reach(ctx context.Context, a Account) (*Conn, error) {
	dial := m.Dial
	if dial == nil {
		dial = func(ctx context.Context, address string) (*Conn, error) { return Dial(ctx, address, nil) }
	}
	c, err := dial(ctx, a.Host)
	if err != nil {
		return nil, err
	}
	if err := c.capabilities(); err != nil {
		c.nc.Close()
		return nil, err
	}
	return c, nil
}

// Sign connects and signs in with a password, which is also how a new account is tested.
func (m *Mailer) Sign(ctx context.Context, a Account, password string) (*Conn, error) {
	c, err := m.reach(ctx, a)
	if err != nil {
		return nil, err
	}
	if c.Has("LOGINDISABLED") {
		c.nc.Close()
		return nil, errors.New("this mail server does not allow signing in with a password")
	}
	err = c.Login(a.Email, password)
	if user, _, _ := strings.Cut(a.Email, "@"); errors.Is(err, ErrLogin) && a.Host == iCloudHost {
		err = c.Login(user, password)
	}
	if err != nil {
		c.nc.Close()
		return nil, err
	}
	return c, nil
}

// fetchBatch is how many messages are asked for at once.
const fetchBatch = 40

// Sync brings an account's inbox up to date and reports how many messages arrived. It keeps its place, so a sync cut
// short goes on from there.
func (m *Mailer) Sync(ctx context.Context, a Account, progress func(done, total int)) (int, error) {
	c, err := m.connect(ctx, a)
	if err != nil {
		return 0, err
	}
	defer c.Close()
	sel, err := c.Select("INBOX")
	if err != nil {
		return 0, err
	}
	validity, last, err := m.Store.syncState(a.ID)
	if err != nil {
		return 0, err
	}
	if validity != sel.Validity {
		last = 0 // the server renumbered its messages
	}
	days := m.Days
	if days <= 0 {
		days = 90
	}
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	all, err := c.SearchSince(now().AddDate(0, 0, -days))
	if err != nil {
		return 0, err
	}
	var fresh []uint32
	for _, u := range all {
		if u > last {
			fresh = append(fresh, u)
		}
	}
	for i := 0; i < len(fresh); i += fetchBatch {
		if err := ctx.Err(); err != nil {
			return i, err
		}
		batch := fresh[i:min(i+fetchBatch, len(fresh))]
		var failed error
		err := c.Fetch(batch, true, func(f Fetched) {
			if err := m.Store.put(a.ID, sel.Validity, f); err != nil && failed == nil {
				failed = err
			}
		})
		if err == nil {
			err = failed
		}
		if err != nil {
			return i, err
		}
		if err := m.Store.setSyncState(a.ID, sel.Validity, batch[len(batch)-1]); err != nil {
			return i, err
		}
		if progress != nil {
			progress(i+len(batch), len(fresh))
		}
	}
	present := make(map[uint32]Fetched, len(all))
	for i := 0; i < len(all); i += fetchBatch * 10 {
		batch := all[i:min(i+fetchBatch*10, len(all))]
		if err := c.Fetch(batch, false, func(f Fetched) { present[f.UID] = f }); err != nil {
			return len(fresh), err
		}
	}
	return len(fresh), m.Store.refresh(a.ID, sel.Validity, present)
}

// ErrNoLabels means the account's server has no labels to put on a message.
var ErrNoLabels = errors.New("this mailbox has no labels")

// Apply makes a change the user approved on the server, writes it in the log and drops the suggestion.
func (m *Mailer) Apply(ctx context.Context, a Account, g Suggestion) error {
	msg, err := m.Store.Message(g.Message)
	if err != nil {
		return err
	}
	if msg.Account != a.ID {
		return errors.New("the message is not of this account")
	}
	c, err := m.connect(ctx, a)
	if err != nil {
		return err
	}
	defer c.Close()
	folders, err := c.FindFolders()
	if err != nil {
		return err
	}
	if _, err := c.Select(folders.Inbox); err != nil {
		return err
	}
	switch g.Action {
	case ActionLabel:
		if !c.Gmail() {
			return ErrNoLabels
		}
		if err := c.AddLabel(msg.UID, g.Value); err != nil {
			return err
		}
		if err := m.Store.setLabels(msg.ID, append(msg.Labels, g.Value)); err != nil {
			return err
		}
	case ActionArchive:
		if c.Gmail() {
			err = c.RemoveLabel(msg.UID, `\Inbox`)
		} else if folders.Archive == "" {
			err = errors.New("this mailbox has no archive folder")
		} else {
			err = c.Move(msg.UID, folders.Archive)
		}
		if err != nil {
			return err
		}
		if err := m.Store.setInbox(msg.ID, false); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown action %q", g.Action)
	}
	if _, err := m.Store.logChange(msg, g.Action, g.Value); err != nil {
		return err
	}
	return m.Store.Dismiss(g.ID)
}

// Undo reverses a change of the log on the server.
func (m *Mailer) Undo(ctx context.Context, a Account, entryID int64) error {
	e, err := m.Store.entry(entryID)
	if err != nil {
		return err
	}
	if e.Undone {
		return nil
	}
	msg, err := m.Store.Message(e.Message)
	if err != nil {
		return err
	}
	c, err := m.connect(ctx, a)
	if err != nil {
		return err
	}
	defer c.Close()
	folders, err := c.FindFolders()
	if err != nil {
		return err
	}
	switch e.Action {
	case ActionLabel:
		if _, err := c.Select(folders.Inbox); err != nil {
			return err
		}
		if err := c.RemoveLabel(msg.UID, e.Value); err != nil {
			return err
		}
		var kept []string
		for _, l := range msg.Labels {
			if l != e.Value {
				kept = append(kept, l)
			}
		}
		if err := m.Store.setLabels(msg.ID, kept); err != nil {
			return err
		}
	case ActionArchive:
		// The message has another UID where it is now, and is found by its Message-ID.
		if folders.Archive == "" || msg.MessageID == "" {
			return errors.New("the message cannot be found in the archive")
		}
		if _, err := c.Select(folders.Archive); err != nil {
			return err
		}
		uids, err := c.FindByID(msg.MessageID)
		if err != nil {
			return err
		}
		if len(uids) == 0 {
			return errors.New("the message cannot be found in the archive")
		}
		if c.Gmail() {
			err = c.AddLabel(uids[0], `\Inbox`)
		} else {
			err = c.Move(uids[0], folders.Inbox)
		}
		if err != nil {
			return err
		}
		if err := m.Store.setInbox(msg.ID, true); err != nil {
			return err
		}
	}
	return m.Store.markUndone(e.ID)
}

// SaveDraft saves a reply in the account's drafts; it is not sent.
func (m *Mailer) SaveDraft(ctx context.Context, a Account, msg Message, text string, at time.Time) error {
	c, err := m.connect(ctx, a)
	if err != nil {
		return err
	}
	defer c.Close()
	folders, err := c.FindFolders()
	if err != nil {
		return err
	}
	if folders.Drafts == "" {
		return errors.New("this mailbox has no drafts folder")
	}
	reply := Reply{From: a.Email, To: msg.Address, Subject: msg.Subject, InReplyTo: msg.MessageID, Text: text}
	return c.Append(folders.Drafts, reply.Build(at))
}
