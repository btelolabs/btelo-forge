package mail

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite" // the database, without cgo
)

// Account is a mailbox the user bound. Its password is kept apart, sealed.
type Account struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Host  string `json:"host"`           // host:port
	SMTP  string `json:"smtp,omitempty"` // the outgoing server, host:port; 465 starts in TLS, any other port moves to it
	Name  string `json:"name,omitempty"`
}

// iCloudHost asks for the part of the address before the @ on many accounts, and the whole address on others.
const iCloudHost = "imap.mail.me.com:993"

// Tiers say what a message is worth to its owner. "" is mail no model has read, which has a view of its own.
const (
	TierMust  = "must"  // the owner really has to know it or answer it
	TierMaybe = "maybe" // of some use, and nothing is lost if it stays unread
	TierNoise = "noise" // of no use at all
)

// Tiers are the tiers, the highest first.
var Tiers = []string{TierMust, TierMaybe, TierNoise}

// Boxes are the server's folders kept here. UIDs mean something only within one.
const (
	BoxInbox   = "inbox"
	BoxSent    = "sent"
	BoxStarred = "starred" // what Gmail has starred and is in neither of the others
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
	Box       string
	Cc        string
	ReplyTo   string
	Labels    []string
	Text      string
	Preview   string // the start of Text for a list, made once as it is read: a list is drawn in every frame
	// What the model made of it; Organised is the model's id, "" until then (unansweredBy when it could not say).
	Summary   string
	Why       string
	Organised string
	Tier      string // where it is shown, which the user's choices decide before the model's (shownTier)
	Starred   bool
}

// Filter chooses messages.
type Filter struct {
	Accounts []string // the accounts to show; none means all of them
	Inbox    bool     // only what is in an inbox
	Box      string   // only what is in a box
	Tiers    []string // the tiers to show; none means all of them
	Starred  bool     // only what was starred
	Search   string
	Limit    int
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
	ActionLabel   = "label" // no longer made, and kept for what the log holds
	ActionArchive = "archive"
	ActionRead    = "read"  // many messages, marked read
	ActionTrash   = "trash" // many messages, moved to the mailbox's trash
)

// messageColumns are the table's columns and its one constraint.
const messageColumns = `
	id INTEGER PRIMARY KEY,
	account TEXT NOT NULL, validity INTEGER NOT NULL, uid INTEGER NOT NULL,
	message_id TEXT NOT NULL DEFAULT '', gmail_id TEXT NOT NULL DEFAULT '', thread TEXT NOT NULL DEFAULT '',
	in_reply_to TEXT NOT NULL DEFAULT '',
	sender TEXT NOT NULL DEFAULT '', address TEXT NOT NULL DEFAULT '', recipient TEXT NOT NULL DEFAULT '',
	subject TEXT NOT NULL DEFAULT '', date INTEGER NOT NULL DEFAULT 0,
	seen INTEGER NOT NULL DEFAULT 0, inbox INTEGER NOT NULL DEFAULT 1, labels TEXT NOT NULL DEFAULT '[]',
	body TEXT NOT NULL DEFAULT '',
	summary TEXT NOT NULL DEFAULT '', tier TEXT NOT NULL DEFAULT '', why TEXT NOT NULL DEFAULT '',
	organised TEXT NOT NULL DEFAULT '',
	pick TEXT NOT NULL DEFAULT '', starred INTEGER NOT NULL DEFAULT 0, forged INTEGER NOT NULL DEFAULT 0,
	html TEXT NOT NULL DEFAULT '',
	whole INTEGER NOT NULL DEFAULT 0, -- the page holds all of the message
	paged INTEGER NOT NULL DEFAULT 0, -- its page was fetched, if it has one
	box TEXT NOT NULL DEFAULT 'inbox', cc TEXT NOT NULL DEFAULT '', reply_to TEXT NOT NULL DEFAULT '',
	star_dirty INTEGER NOT NULL DEFAULT 0, -- starred differs from the server's until it is told
	UNIQUE (account, box, validity, uid)`

// messageKept are the columns a database from before the boxes has.
const messageKept = `id, account, validity, uid, message_id, gmail_id, thread, in_reply_to, sender, address, recipient, subject, date, seen,
	inbox, labels, body, summary, tier, why, organised, pick, starred, forged, html, whole, paged`

const schema = `
CREATE TABLE IF NOT EXISTS messages (` + messageColumns + `);
CREATE INDEX IF NOT EXISTS messages_date ON messages (date DESC);
CREATE TABLE IF NOT EXISTS state (account TEXT PRIMARY KEY, validity INTEGER NOT NULL, last_uid INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS log (
	id INTEGER PRIMARY KEY, at INTEGER NOT NULL,
	message INTEGER NOT NULL, account TEXT NOT NULL, subject TEXT NOT NULL,
	action TEXT NOT NULL, value TEXT NOT NULL DEFAULT '', undone INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS log_items (entry INTEGER NOT NULL, message INTEGER NOT NULL, PRIMARY KEY (entry, message));
CREATE TABLE IF NOT EXISTS trusted (address TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS senders (address TEXT PRIMARY KEY, tier TEXT NOT NULL);`

// upgrade brings a database of an earlier version up to date. Its messages have no page yet: FillPages fetches them.
func upgrade(db *sql.DB) error {
	has := func(column string) (bool, error) {
		var n int
		err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name=?", column).Scan(&n)
		return n > 0, err
	}
	var steps []string
	switch html, err := has("html"); {
	case err != nil:
		return err
	case !html:
		steps = append(steps,
			"ALTER TABLE messages ADD COLUMN html TEXT NOT NULL DEFAULT ''",
			"ALTER TABLE messages ADD COLUMN whole INTEGER NOT NULL DEFAULT 0", // the page holds all of the message
			"UPDATE messages SET body=replace(body, char(13), '')")
	}
	switch paged, err := has("paged"); {
	case err != nil:
		return err
	case !paged:
		steps = append(steps,
			"ALTER TABLE messages ADD COLUMN paged INTEGER NOT NULL DEFAULT 0", // its page was fetched, if it has one
			"UPDATE messages SET paged=1 WHERE whole=1 OR html<>''")
	}
	switch tier, err := has("tier"); {
	case err != nil:
		return err
	case !tier:
		// The model sorted by other rules, so everything is read again; the summaries stay meanwhile.
		steps = append(steps,
			"ALTER TABLE messages ADD COLUMN tier TEXT NOT NULL DEFAULT ''",
			"ALTER TABLE messages ADD COLUMN why TEXT NOT NULL DEFAULT ''",
			"ALTER TABLE messages ADD COLUMN pick TEXT NOT NULL DEFAULT ''",
			"ALTER TABLE messages ADD COLUMN starred INTEGER NOT NULL DEFAULT 0",
			"ALTER TABLE messages ADD COLUMN forged INTEGER NOT NULL DEFAULT 0",
			"ALTER TABLE messages DROP COLUMN priority",
			"ALTER TABLE messages DROP COLUMN category",
			"UPDATE messages SET organised=''",
			"DROP TABLE IF EXISTS suggestions")
	}
	switch box, err := has("box"); {
	case err != nil:
		return err
	case !box:
		// UIDs are now unique within a box, which only a new table can say. Stars were kept here alone: the server is told.
		steps = append(steps, "CREATE TABLE messages_boxed ("+messageColumns+")",
			"INSERT INTO messages_boxed ("+messageKept+") SELECT "+messageKept+" FROM messages",
			"DROP TABLE messages", "ALTER TABLE messages_boxed RENAME TO messages",
			"UPDATE messages SET star_dirty=1 WHERE starred=1",
			"CREATE INDEX IF NOT EXISTS messages_date ON messages (date DESC)")
	}
	steps = append(steps, "CREATE INDEX IF NOT EXISTS messages_starred ON messages (account) WHERE starred=1",
		"CREATE INDEX IF NOT EXISTS messages_gmail_id ON messages (account, gmail_id)",
		"CREATE INDEX IF NOT EXISTS messages_message_id ON messages (account, message_id)")
	tx, err := db.Begin() // nothing of a user's mail may hang between two tables
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range steps {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return tx.Commit()
}

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
	if err := upgrade(db); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.settleCodes(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// settleCodes holds the mail a model read before OneTimeCode was asked to the same rule: a code is never a must. Only a
// model's tier changes; the owner's pick is a column of its own. It reads the few musts, so it costs nothing at each opening.
func (s *Store) settleCodes() error {
	rows, err := s.db.Query("SELECT id, subject, body FROM messages WHERE tier=? AND organised<>''", TierMust)
	if err != nil {
		return err
	}
	var codes []int64
	for rows.Next() {
		var id int64
		var subject, body string
		if err := rows.Scan(&id, &subject, &body); err != nil {
			rows.Close()
			return err
		}
		if OneTimeCode(subject, body) {
			codes = append(codes, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range codes {
		if _, err := s.db.Exec("UPDATE messages SET tier=? WHERE id=?", TierMaybe, id); err != nil {
			return err
		}
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// RemoveAccount forgets everything kept of an account. The mailbox itself is not touched.
func (s *Store) RemoveAccount(id string) error {
	for _, q := range []string{"DELETE FROM messages WHERE account=?", "DELETE FROM state WHERE account=?1 OR account=?1||'/sent'",
		"DELETE FROM log_items WHERE entry IN (SELECT id FROM log WHERE account=?)", "DELETE FROM log WHERE account=?"} {
		if _, err := s.db.Exec(q, id); err != nil {
			return err
		}
	}
	return nil
}

// stateKey is where a box's place in a sync is kept.
func stateKey(account, box string) string {
	if box == BoxInbox {
		return account
	}
	return account + "/" + box
}

func (s *Store) syncState(account, box string) (validity, last uint32, err error) {
	err = s.db.QueryRow("SELECT validity, last_uid FROM state WHERE account=?", stateKey(account, box)).Scan(&validity, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	return validity, last, err
}

func (s *Store) setSyncState(account, box string, validity, last uint32) error {
	_, err := s.db.Exec(`INSERT INTO state (account, validity, last_uid) VALUES (?, ?, ?)
		ON CONFLICT (account) DO UPDATE SET validity=excluded.validity, last_uid=excluded.last_uid`, stateKey(account, box), validity, last)
	return err
}

// put keeps a message that has arrived. One that is already here, as a sync interrupted may fetch it twice, stays. One that
// comes back under a new number, moved back by another program or renumbered by the server, is the row it had: what a model
// and the user made of it stays, so no model reads it again. A copy still in the inbox under its own number is left alone.
func (s *Store) put(account, box string, validity uint32, f Fetched) error {
	p := Parse(f.Raw)
	labels, _ := json.Marshal(f.Labels)
	inbox := box == BoxInbox
	back, err := s.db.Exec(`UPDATE messages SET box=?, validity=?, uid=?, seen=?, inbox=?, labels=? WHERE id=(
			SELECT id FROM messages WHERE account=? AND box IN (?, 'starred') AND (inbox=0 OR validity<>?)
				AND ((gmail_id<>'' AND gmail_id=?) OR (message_id<>'' AND message_id=?)) ORDER BY id LIMIT 1)
		AND NOT EXISTS (SELECT 1 FROM messages WHERE account=? AND box=? AND validity=? AND uid=?)`,
		box, validity, f.UID, hasFlag(f.Flags, `\Seen`), inbox, string(labels), account, box, validity, f.GmailID, p.MessageID, account, box, validity, f.UID)
	if err != nil {
		return err
	}
	if n, err := back.RowsAffected(); err != nil || n > 0 {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO messages (account, box, validity, uid, message_id, gmail_id, thread, in_reply_to, sender, address,
		recipient, cc, reply_to, subject, date, seen, inbox, starred, labels, body, html, whole, paged, forged)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,?)
		ON CONFLICT (account, box, validity, uid) DO NOTHING`,
		account, box, validity, f.UID, p.MessageID, f.GmailID, f.ThreadID, p.InReplyTo, p.From, p.Address, p.To, p.Cc, p.ReplyTo, p.Subject,
		p.Date.Unix(), hasFlag(f.Flags, `\Seen`), inbox, hasFlag(f.Flags, `\Flagged`), string(labels), p.Text, p.HTML, len(f.Raw) < bodyPart, p.Forged)
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

// refresh brings what is known of a box up to date: flags and labels of what is in it, and what left it by another program.
// A star the server has not been told of yet stays. What left an inbox is kept, with what was made of it, and a copy of what
// left the sent folder has no other use.
func (s *Store) refresh(account, box string, validity uint32, present map[uint32]Fetched) error {
	gone := "UPDATE messages SET inbox=0 WHERE id=?"
	if box != BoxInbox {
		gone = "DELETE FROM messages WHERE id=?"
	}
	// What a renumbering did not bring back under a new number has left.
	left := "UPDATE messages SET inbox=0 WHERE account=? AND box=? AND validity<>? AND inbox=1"
	if box != BoxInbox {
		left = "DELETE FROM messages WHERE account=? AND box=? AND validity<>?"
	}
	if _, err := s.db.Exec(left, account, box, validity); err != nil {
		return err
	}
	rows, err := s.db.Query("SELECT id, uid FROM messages WHERE account=? AND box=? AND validity=? AND (inbox=1 OR box<>'inbox')", account, box, validity)
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
			if _, err := s.db.Exec(gone, r.id); err != nil {
				return err
			}
			continue
		}
		labels, _ := json.Marshal(f.Labels)
		if _, err := s.db.Exec("UPDATE messages SET seen=?, labels=?, starred=CASE WHEN star_dirty=1 THEN starred ELSE ? END WHERE id=?",
			hasFlag(f.Flags, `\Seen`), string(labels), hasFlag(f.Flags, `\Flagged`), r.id); err != nil {
			return err
		}
	}
	return nil
}

// shownTier works out where a message m is shown, the first that says so deciding: what the user chose for it, a star,
// an important person, a sender the user does not want to see, a star in its conversation, then the model. A star on
// another message of the sender also keeps its mail out of the lowest tier. It is worked out at each query, so that taking
// a star or a person back undoes all of it. A sender's address says nothing of a message the server found forged, so that
// never makes it important. This runs for every message listed, so the look for stars stays on the few that have one: left
// to itself SQLite reads every message of the account for each, which takes a mailbox from milliseconds to seconds.
const shownTier = `CASE
	WHEN m.pick<>'' THEN m.pick
	WHEN m.starred=1 THEN 'must'
	WHEN m.forged=0 AND EXISTS (SELECT 1 FROM senders x WHERE x.address=lower(m.address) AND x.tier='must') THEN 'must'
	WHEN EXISTS (SELECT 1 FROM senders x WHERE x.address=lower(m.address) AND x.tier='noise') THEN 'noise'
	WHEN EXISTS (SELECT 1 FROM messages s INDEXED BY messages_starred WHERE s.starred=1 AND s.account=m.account AND ((m.thread<>'' AND s.thread=m.thread)
		OR (m.in_reply_to<>'' AND s.message_id=m.in_reply_to) OR (s.in_reply_to<>'' AND m.message_id=s.in_reply_to))) THEN 'must'
	WHEN m.tier='noise' AND EXISTS (SELECT 1 FROM messages s INDEXED BY messages_starred WHERE s.starred=1 AND lower(s.address)=lower(m.address)) THEN 'maybe'
	ELSE m.tier END`

// columns leave out a message's page, which is large and read for the one message open (Body). They are read from messages
// under the name m.
const columns = `id, account, uid, message_id, gmail_id, thread, in_reply_to, sender, address, recipient, subject, date,
	seen, inbox, labels, body, summary, why, organised, (` + shownTier + `), starred, box, cc, reply_to`

func scanMessage(row interface{ Scan(...any) error }) (Message, error) {
	var m Message
	var date int64
	var labels string
	err := row.Scan(&m.ID, &m.Account, &m.UID, &m.MessageID, &m.GmailID, &m.Thread, &m.InReplyTo, &m.From, &m.Address, &m.To,
		&m.Subject, &date, &m.Seen, &m.Inbox, &labels, &m.Text, &m.Summary, &m.Why, &m.Organised, &m.Tier, &m.Starred, &m.Box, &m.Cc, &m.ReplyTo)
	if err != nil {
		return m, err
	}
	m.Date = time.Unix(date, 0)
	m.Preview = Preview(m.Text, previewRunes)
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
	if f.Box != "" {
		conds = append(conds, "box=?")
		args = append(args, f.Box)
	}
	if f.Starred {
		conds = append(conds, "starred=1")
	}
	if len(f.Tiers) > 0 {
		conds = append(conds, "("+shownTier+") IN (?"+strings.Repeat(",?", len(f.Tiers)-1)+")")
		for _, t := range f.Tiers {
			args = append(args, t)
		}
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
	rows, err := s.db.Query("SELECT "+columns+" FROM messages m"+where+" ORDER BY date DESC, id DESC LIMIT ?", append(args, limit)...)
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
	return scanMessage(s.db.QueryRow("SELECT "+columns+" FROM messages m WHERE id=?", id))
}

// Body is a message's page.
type Body struct {
	HTML  string
	Whole bool // it holds all of the message: a page cut short at bodyPart, or of an earlier version, does not
}

// Body gives a message's page.
func (s *Store) Body(id int64) (Body, error) {
	var b Body
	err := s.db.QueryRow("SELECT html, whole FROM messages WHERE id=?", id).Scan(&b.HTML, &b.Whole)
	return b, err
}

type unpagedRow struct {
	id            int64
	uid, validity uint32
}

// unpaged lists the messages of an account in an inbox whose page was not fetched, the newest first.
func (s *Store) unpaged(account string) ([]unpagedRow, error) {
	rows, err := s.db.Query("SELECT id, uid, validity FROM messages WHERE account=? AND inbox=1 AND paged=0 ORDER BY date DESC, id DESC", account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []unpagedRow
	for rows.Next() {
		var r unpagedRow
		if err := rows.Scan(&r.id, &r.uid, &r.validity); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) setPage(id int64, html string, whole bool) error {
	_, err := s.db.Exec("UPDATE messages SET html=?, whole=?, paged=1 WHERE id=?", html, whole, id)
	return err
}

func (s *Store) markPaged(uids []uint32, ids map[uint32]int64) error {
	for _, uid := range uids {
		if _, err := s.db.Exec("UPDATE messages SET paged=1 WHERE id=?", ids[uid]); err != nil {
			return err
		}
	}
	return nil
}

// Trust says that the pictures of an address's mail may be fetched.
func (s *Store) Trust(address string) error {
	_, err := s.db.Exec("INSERT INTO trusted (address) VALUES (?) ON CONFLICT DO NOTHING", strings.ToLower(address))
	return err
}

// Trusted reports whether the pictures of an address's mail may be fetched.
func (s *Store) Trusted(address string) (bool, error) {
	var n int
	err := s.db.QueryRow("SELECT COUNT(*) FROM trusted WHERE address=?", strings.ToLower(address)).Scan(&n)
	return n > 0, err
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

// unansweredBy is what organised holds for a message a model could not answer for, which only another model reads again.
func unansweredBy(model string) string { return "!" + model }

// unorganised is the condition on the mail a model has to read; its one argument is unansweredBy that model.
const unorganised = " AND (organised='' OR (organised LIKE '!%' AND organised<>?))"

// Unorganised gives the inbox messages of the chosen accounts that a model has to read, the newest first.
func (s *Store) Unorganised(accounts []string, model string, limit int) ([]Message, error) {
	where, args := Filter{Accounts: accounts, Inbox: true}.where()
	rows, err := s.db.Query("SELECT "+columns+" FROM messages m"+where+unorganised+" ORDER BY date DESC LIMIT ?", append(args, unansweredBy(model), limit)...)
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

// CountUnorganised says how many messages Unorganised has to give.
func (s *Store) CountUnorganised(accounts []string, model string) (int, error) {
	where, args := Filter{Accounts: accounts, Inbox: true}.where()
	var n int
	err := s.db.QueryRow("SELECT COUNT(*) FROM messages m"+where+unorganised, append(args, unansweredBy(model))...).Scan(&n)
	return n, err
}

// SaveOrganised keeps what a model made of a message.
func (s *Store) SaveOrganised(id int64, model string, v Verdict) error {
	_, err := s.db.Exec("UPDATE messages SET summary=?, tier=?, why=?, organised=? WHERE id=?", v.Summary, v.Tier, v.Why, model, id)
	return err
}

// SaveUnanswered keeps that a model could not answer for a message, which stays where unread mail is shown.
func (s *Store) SaveUnanswered(id int64, model string) error {
	_, err := s.db.Exec("UPDATE messages SET organised=? WHERE id=?", unansweredBy(model), id)
	return err
}

// Pick puts a message in a tier by the user's word, over everything else; "" gives the choice back.
func (s *Store) Pick(id int64, tier string) error {
	_, err := s.db.Exec("UPDATE messages SET pick=? WHERE id=?", tier, id)
	return err
}

// Star keeps a message apart, which also lifts its conversation and its sender (shownTier). It is kept here, not on the
// server.
func (s *Store) Star(id int64, on bool) error {
	_, err := s.db.Exec("UPDATE messages SET starred=?, star_dirty=1 WHERE id=?", on, id)
	return err
}

// SetSender says that all of an address's mail is TierMust, of an important person, or TierNoise, which the user does not
// want to see; "" takes that back.
func (s *Store) SetSender(address, tier string) error {
	address = strings.ToLower(address)
	if tier == "" {
		_, err := s.db.Exec("DELETE FROM senders WHERE address=?", address)
		return err
	}
	_, err := s.db.Exec("INSERT INTO senders (address, tier) VALUES (?, ?) ON CONFLICT (address) DO UPDATE SET tier=excluded.tier", address, tier)
	return err
}

// Senders gives what SetSender was told, by address in lower case.
func (s *Store) Senders() (map[string]string, error) {
	rows, err := s.db.Query("SELECT address, tier FROM senders")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var address, tier string
		if err := rows.Scan(&address, &tier); err != nil {
			return nil, err
		}
		out[address] = tier
	}
	return out, rows.Err()
}

// TierCount is how much mail of a tier is in the inboxes.
type TierCount struct{ All, Unread int }

// TierCounts counts the inbox mail of the chosen accounts by tier; mail no model has read is under "".
func (s *Store) TierCounts(accounts []string) (map[string]TierCount, error) {
	where, args := Filter{Accounts: accounts, Inbox: true}.where()
	rows, err := s.db.Query("SELECT t, COUNT(*), COALESCE(SUM(1-seen), 0) FROM (SELECT ("+shownTier+") t, seen FROM messages m"+where+") GROUP BY t", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]TierCount{}
	for rows.Next() {
		var t string
		var c TierCount
		if err := rows.Scan(&t, &c.All, &c.Unread); err != nil {
			return nil, err
		}
		out[t] = c
	}
	return out, rows.Err()
}

// Target says which messages a change to many is for: what is in the inboxes of the chosen accounts in a tier, only what
// is unread when marking it read, and never what was starred when moving it to the trash.
func (s *Store) Target(accounts []string, tier, action string) (map[string][]int64, error) {
	where, args := Filter{Accounts: accounts, Inbox: true, Tiers: []string{tier}}.where()
	switch action {
	case ActionRead:
		where += " AND seen=0"
	case ActionTrash:
		where += " AND starred=0"
	}
	rows, err := s.db.Query("SELECT account, id FROM messages m"+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]int64{}
	for rows.Next() {
		var account string
		var id int64
		if err := rows.Scan(&account, &id); err != nil {
			return nil, err
		}
		out[account] = append(out[account], id)
	}
	return out, rows.Err()
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

// batchRow is what a change to many messages needs of each.
type batchRow struct {
	id        int64
	uid       uint32
	validity  uint32
	messageID string
}

// batchRows reads rows by id, a few at a time as a query holds only so many values.
func (s *Store) batchRows(ids []int64) ([]batchRow, error) {
	var out []batchRow
	for i := 0; i < len(ids); i += 500 {
		part := ids[i:min(i+500, len(ids))]
		args := make([]any, len(part))
		for k, id := range part {
			args[k] = id
		}
		rows, err := s.db.Query("SELECT id, uid, validity, message_id FROM messages WHERE id IN (?"+strings.Repeat(",?", len(part)-1)+")", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var r batchRow
			if err := rows.Scan(&r.id, &r.uid, &r.validity, &r.messageID); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// logBatch writes in the log that a part of a change to many was made, and what the local copy now says, in one step.
func (s *Store) logBatch(entry int64, part []batchRow, update string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range part {
		if _, err := tx.Exec("INSERT INTO log_items (entry, message) VALUES (?, ?) ON CONFLICT DO NOTHING", entry, r.id); err != nil {
			return err
		}
		if _, err := tx.Exec(update, r.id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("UPDATE log SET value=(SELECT COUNT(*) FROM log_items WHERE entry=?) WHERE id=?", entry, entry); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) logItems(entry int64) ([]int64, error) {
	rows, err := s.db.Query("SELECT message FROM log_items WHERE entry=?", entry)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Mailer reaches the servers of the accounts: it signs in, syncs and makes the changes the user approved.
type Mailer struct {
	Store    *Store
	Password func(account string) (string, error)
	// Dial connects to a server; the tests point it at a server of their own.
	Dial func(ctx context.Context, address string) (*Conn, error)
	// DialSMTP connects to an outgoing server, the same way.
	DialSMTP func(ctx context.Context, address string) (net.Conn, error)
	Now      func() time.Time
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

// Sync tells the server of the stars set here, brings an account's inbox, sent folder and stars up to date and reports how
// many messages arrived in the inbox. It keeps its place, so a sync cut short goes on from there.
func (m *Mailer) Sync(ctx context.Context, a Account, progress func(done, total int)) (int, error) {
	c, err := m.connect(ctx, a)
	if err != nil {
		return 0, err
	}
	defer c.Close()
	folders, err := c.FindFolders()
	if err != nil {
		return 0, err
	}
	if err := m.pushStars(c, a, folders); err != nil {
		return 0, err
	}
	n, err := m.syncBox(ctx, c, a, BoxInbox, "INBOX", progress)
	if err != nil {
		return n, err
	}
	if folders.Sent != "" {
		if _, err := m.syncBox(ctx, c, a, BoxSent, folders.Sent, nil); err != nil {
			return n, err
		}
	}
	if c.Gmail() && folders.Flagged != "" {
		return n, m.syncStarred(ctx, c, a, folders.Flagged)
	}
	return n, nil
}

// PushStars tells the server of the stars set here. One it could not be told of stays marked for the next sync.
func (m *Mailer) PushStars(ctx context.Context, a Account) error {
	c, err := m.connect(ctx, a)
	if err != nil {
		return err
	}
	defer c.Close()
	folders, err := c.FindFolders()
	if err != nil {
		return err
	}
	return m.pushStars(c, a, folders)
}

func (m *Mailer) pushStars(c *Conn, a Account, folders Folders) error {
	type row struct {
		id       int64
		uid      uint32
		validity uint32
		box      string
		gmail    string
		on       bool
	}
	rows, err := m.Store.db.Query("SELECT id, uid, validity, box, gmail_id, starred FROM messages WHERE account=? AND star_dirty=1", a.ID)
	if err != nil {
		return err
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.uid, &r.validity, &r.box, &r.gmail, &r.on); err != nil {
			rows.Close()
			return err
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	open, validity := "", uint32(0)
	sel := func(name string) error {
		if name == open {
			return nil
		}
		s, err := c.Select(name)
		open, validity = name, s.Validity
		return err
	}
	for _, r := range list {
		var uids []uint32
		switch {
		case c.Gmail() && r.gmail != "" && folders.Archive != "": // all of Gmail's mail is in one folder, wherever it shows
			if err := sel(folders.Archive); err != nil {
				return err
			}
			if uids, err = c.FindByGmailID(r.gmail); err != nil {
				return err
			}
		case r.box == BoxInbox || r.box == BoxSent && folders.Sent != "":
			name := "INBOX"
			if r.box == BoxSent {
				name = folders.Sent
			}
			if err := sel(name); err != nil {
				return err
			}
			if validity == r.validity {
				uids = []uint32{r.uid}
			}
		}
		if len(uids) > 0 {
			if err := c.Flag(uids, r.on); err != nil {
				return err
			}
		}
		// A star changed again meanwhile goes in the next round. What the server no longer has can only stay here.
		if _, err := m.Store.db.Exec("UPDATE messages SET star_dirty=0 WHERE id=? AND starred=?", r.id, r.on); err != nil {
			return err
		}
	}
	return nil
}

// syncStarred brings what Gmail has starred here, wherever it lies, and takes off the stars Gmail no longer has.
func (m *Mailer) syncStarred(ctx context.Context, c *Conn, a Account, folder string) error {
	sel, err := c.Select(folder)
	if err != nil {
		return err
	}
	all, err := c.SearchSince(time.Unix(0, 0))
	if err != nil {
		return err
	}
	on := make(map[string]bool, len(all))
	var fresh []uint32
	for i := 0; i < len(all); i += fetchBatch * 10 {
		if err := c.Fetch(all[i:min(i+fetchBatch*10, len(all))], false, func(f Fetched) {
			on[f.GmailID] = true
			r, err := m.Store.db.Exec("UPDATE messages SET starred=1 WHERE account=? AND gmail_id=? AND star_dirty=0", a.ID, f.GmailID)
			if err == nil {
				var kept int
				m.Store.db.QueryRow("SELECT COUNT(*) FROM messages WHERE account=? AND gmail_id=?", a.ID, f.GmailID).Scan(&kept)
				if n, _ := r.RowsAffected(); n == 0 && kept == 0 {
					fresh = append(fresh, f.UID)
				}
			}
		}); err != nil {
			return err
		}
	}
	for i := 0; i < len(fresh); i += fetchBatch {
		if err := ctx.Err(); err != nil {
			return err
		}
		var failed error
		if err := c.Fetch(fresh[i:min(i+fetchBatch, len(fresh))], true, func(f Fetched) {
			f.Flags = append(f.Flags, `\Flagged`)
			if err := m.Store.put(a.ID, BoxStarred, sel.Validity, f); err != nil && failed == nil {
				failed = err
			}
		}); err != nil {
			return err
		}
		if failed != nil {
			return failed
		}
	}
	rows, err := m.Store.db.Query("SELECT id, gmail_id FROM messages WHERE account=? AND starred=1 AND star_dirty=0 AND gmail_id<>''", a.ID)
	if err != nil {
		return err
	}
	var off []int64
	for rows.Next() {
		var id int64
		var gmail string
		if err := rows.Scan(&id, &gmail); err != nil {
			rows.Close()
			return err
		}
		if !on[gmail] {
			off = append(off, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range off {
		if _, err := m.Store.db.Exec("UPDATE messages SET starred=0 WHERE id=?", id); err != nil {
			return err
		}
	}
	return nil
}

// syncBox brings one of the server's folders up to date.
func (m *Mailer) syncBox(ctx context.Context, c *Conn, a Account, box, name string, progress func(done, total int)) (int, error) {
	sel, err := c.Select(name)
	if err != nil {
		return 0, err
	}
	validity, last, err := m.Store.syncState(a.ID, box)
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
			if err := m.Store.put(a.ID, box, sel.Validity, f); err != nil && failed == nil {
				failed = err
			}
		})
		if err == nil {
			err = failed
		}
		if err != nil {
			return i, err
		}
		if err := m.Store.setSyncState(a.ID, box, sel.Validity, batch[len(batch)-1]); err != nil {
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
	return len(fresh), m.Store.refresh(a.ID, box, sel.Validity, present)
}

// Complete fetches all of a message that is kept only in part, for its page and inline pictures, and gives the page. A
// message that left its folder or was renumbered cannot be found by its UID and stays as it is.
func (m *Mailer) Complete(ctx context.Context, a Account, id int64) (Body, error) {
	b, err := m.Store.Body(id)
	if err != nil || b.Whole {
		return b, err
	}
	var uid, validity uint32
	var inbox bool
	var box string
	err = m.Store.db.QueryRow("SELECT uid, validity, inbox, box FROM messages WHERE id=? AND account=?", id, a.ID).Scan(&uid, &validity, &inbox, &box)
	if err != nil || !inbox && box == BoxInbox {
		return b, err
	}
	c, err := m.connect(ctx, a)
	if err != nil {
		return b, err
	}
	defer c.Close()
	name := "INBOX"
	if box != BoxInbox {
		folders, err := c.FindFolders()
		if err != nil {
			return b, err
		}
		if name = folders.Sent; box == BoxStarred {
			name = folders.Flagged
		}
		if name == "" {
			return b, nil
		}
	}
	sel, err := c.Select(name)
	if err != nil {
		return b, err
	}
	if sel.Validity != validity {
		return b, nil
	}
	raw, err := c.FetchWhole(uid)
	if err != nil || raw == nil {
		return b, err
	}
	b = Body{HTML: Parse(raw).HTML, Whole: true}
	_, err = m.Store.db.Exec("UPDATE messages SET html=?, whole=1, paged=1 WHERE id=?", b.HTML, id)
	return b, err
}

// FillPages fetches the pages of an account's messages that have none yet, as those of an earlier version have not, so
// that opening a message does not wait for a server. The newest come first, all over one connection, and an interrupted
// run goes on from where it stopped. progress is told after each batch.
func (m *Mailer) FillPages(ctx context.Context, a Account, progress func(done, total int)) error {
	rows, err := m.Store.unpaged(a.ID)
	if err != nil || len(rows) == 0 {
		return err
	}
	c, err := m.connect(ctx, a)
	if err != nil {
		return err
	}
	defer c.Close()
	sel, err := c.Select("INBOX")
	if err != nil {
		return err
	}
	var uids []uint32
	ids := make(map[uint32]int64, len(rows))
	for _, r := range rows {
		if r.validity == sel.Validity { // those of an earlier numbering cannot be found
			uids = append(uids, r.uid)
			ids[r.uid] = r.id
		}
	}
	for i := 0; i < len(uids); i += fetchBatch {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch := uids[i:min(i+fetchBatch, len(uids))]
		var failed error
		if err := c.Fetch(batch, true, func(f Fetched) {
			if err := m.Store.setPage(ids[f.UID], Parse(f.Raw).HTML, len(f.Raw) < bodyPart); err != nil && failed == nil {
				failed = err
			}
		}); err != nil {
			return err
		}
		if failed != nil {
			return failed
		}
		// What the server no longer has is not asked for again.
		if err := m.Store.markPaged(batch, ids); err != nil {
			return err
		}
		if progress != nil {
			progress(i+len(batch), len(uids))
		}
	}
	return nil
}

// ErrNoTrash means the account's mailbox has no trash folder. Nothing is then done, as what is deleted here can always be
// brought back.
var ErrNoTrash = errors.New("this mailbox has no trash folder")

// Archive takes a message out of the inbox on the server at the user's click and writes it in the log.
func (m *Mailer) Archive(ctx context.Context, a Account, id int64) error {
	msg, err := m.Store.Message(id)
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
	if c.Gmail() {
		err = c.RemoveLabel(msg.UID, `\Inbox`)
	} else if folders.Archive == "" {
		err = errors.New("this mailbox has no archive folder")
	} else {
		err = c.Move([]uint32{msg.UID}, folders.Archive)
	}
	if err != nil {
		return err
	}
	if err := m.Store.setInbox(msg.ID, false); err != nil {
		return err
	}
	_, err = m.Store.logChange(msg, ActionArchive, "")
	return err
}

// storeBatch is how many messages one command changes.
const storeBatch = 200

// MarkRead marks messages of an account read on the server at the user's click: one line in the log, which undoes them all.
func (m *Mailer) MarkRead(ctx context.Context, a Account, ids []int64, progress func(done, total int)) error {
	return m.change(ctx, a, ids, ActionRead, progress, "UPDATE messages SET seen=1 WHERE id=?", func(c *Conn, _ Folders, uids []uint32) error {
		return c.Seen(uids, true)
	})
}

// Trash moves messages of an account to the mailbox's trash at the user's click: one line in the log, which brings them
// all back. Nothing here removes a message for good: the mailbox empties its own trash.
func (m *Mailer) Trash(ctx context.Context, a Account, ids []int64, progress func(done, total int)) error {
	return m.change(ctx, a, ids, ActionTrash, progress, "UPDATE messages SET inbox=0 WHERE id=?", func(c *Conn, folders Folders, uids []uint32) error {
		if folders.Trash == "" {
			return ErrNoTrash
		}
		return c.Move(uids, folders.Trash)
	})
}

// change makes a change to many messages in steps, so a stop keeps what was done, and that is what the log then holds.
func (m *Mailer) change(ctx context.Context, a Account, ids []int64, action string, progress func(done, total int), update string, do func(*Conn, Folders, []uint32) error) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := m.Store.batchRows(ids)
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
	sel, err := c.Select(folders.Inbox)
	if err != nil {
		return err
	}
	rows = slices.DeleteFunc(rows, func(r batchRow) bool { return r.validity != sel.Validity }) // those of an earlier numbering cannot be found
	entry := int64(0)
	for i := 0; i < len(rows); i += storeBatch {
		if err := ctx.Err(); err != nil {
			return err
		}
		part := rows[i:min(i+storeBatch, len(rows))]
		uids := make([]uint32, len(part))
		for k, r := range part {
			uids[k] = r.uid
		}
		if err := do(c, folders, uids); err != nil {
			return err
		}
		if entry == 0 {
			if entry, err = m.Store.logChange(Message{Account: a.ID}, action, ""); err != nil {
				return err
			}
		}
		if err := m.Store.logBatch(entry, part, update); err != nil {
			return err
		}
		if progress != nil {
			progress(i+len(part), len(rows))
		}
	}
	return nil
}

// undoRead marks messages unread again.
func (m *Mailer) undoRead(ctx context.Context, c *Conn, folders Folders, rows []batchRow, progress func(done, total int)) error {
	if _, err := c.Select(folders.Inbox); err != nil {
		return err
	}
	for i := 0; i < len(rows); i += storeBatch {
		if err := ctx.Err(); err != nil {
			return err
		}
		part := rows[i:min(i+storeBatch, len(rows))]
		uids := make([]uint32, len(part))
		for k, r := range part {
			uids[k] = r.uid
		}
		if err := c.Seen(uids, false); err != nil {
			return err
		}
		for _, r := range part {
			if _, err := m.Store.db.Exec("UPDATE messages SET seen=0 WHERE id=?", r.id); err != nil {
				return err
			}
		}
		if progress != nil {
			progress(i+len(part), len(rows))
		}
	}
	return nil
}

// undoTrash moves messages back from the trash to the inbox. They have other UIDs in each place, so they are found by
// Message-ID, and what the mailbox already emptied out of its trash stays gone.
func (m *Mailer) undoTrash(ctx context.Context, c *Conn, folders Folders, rows []batchRow, progress func(done, total int)) error {
	if folders.Trash == "" {
		return ErrNoTrash
	}
	if _, err := c.Select(folders.Trash); err != nil {
		return err
	}
	var back []batchRow
	for i, r := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		if progress != nil {
			progress(i/2, len(rows))
		}
		if r.messageID == "" {
			continue
		}
		uids, err := c.FindByID(r.messageID)
		if err != nil {
			return err
		}
		if len(uids) == 0 {
			continue
		}
		if err := c.Move(uids[:1], folders.Inbox); err != nil {
			return err
		}
		back = append(back, r)
	}
	sel, err := c.Select(folders.Inbox)
	if err != nil {
		return err
	}
	for i, r := range back {
		if progress != nil {
			progress(len(rows)/2+i*len(rows)/(2*len(back)), len(rows))
		}
		uids, err := c.FindByID(r.messageID)
		if err != nil {
			return err
		}
		if len(uids) == 0 {
			continue
		}
		if _, err := m.Store.db.Exec("UPDATE messages SET uid=?, validity=?, inbox=1 WHERE id=?", uids[0], sel.Validity, r.id); err != nil {
			return err
		}
	}
	return nil
}

// Undo reverses a change of the log on the server.
func (m *Mailer) Undo(ctx context.Context, a Account, entryID int64, progress func(done, total int)) error {
	e, err := m.Store.entry(entryID)
	if err != nil {
		return err
	}
	if e.Undone {
		return nil
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
	if e.Action == ActionRead || e.Action == ActionTrash {
		ids, err := m.Store.logItems(e.ID)
		if err != nil {
			return err
		}
		rows, err := m.Store.batchRows(ids)
		if err != nil {
			return err
		}
		undo := m.undoRead
		if e.Action == ActionTrash {
			undo = m.undoTrash
		}
		if err := undo(ctx, c, folders, rows, progress); err != nil {
			return err
		}
		return m.Store.markUndone(e.ID)
	}
	msg, err := m.Store.Message(e.Message)
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
			err = c.Move(uids[:1], folders.Inbox)
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
