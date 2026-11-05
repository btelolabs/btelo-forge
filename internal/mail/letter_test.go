package mail

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	stdmail "net/mail"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/mail/mailtest"
)

func sending(t *testing.T, s *mailtest.Server) (*Mailer, Account, *mailtest.SMTP) {
	t.Helper()
	out := mailtest.NewSMTP(t)
	m := testMailer(t)
	m.DialSMTP = func(ctx context.Context, address string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", address)
	}
	a := accountOf("a", s)
	a.SMTP = out.Addr()
	return m, a, out
}

func TestALetterGoesOutAndIsFiled(t *testing.T) {
	s := mailtest.New(t, false)
	m, a, out := sending(t, s)
	l := Letter{From: a.Email, To: "安 <ann@a.com>；bob@b.com", Cc: "cat@c.com", Bcc: "hidden@h.com", Subject: "周五的晚饭", Text: "七点见\n再联系"}
	if err := m.Send(context.Background(), a, l, time.Now()); err != nil {
		t.Fatal(err)
	}
	taken := out.Taken()
	if len(taken) != 1 || taken[0].From != a.Email {
		t.Fatalf("%+v", taken)
	}
	if want := []string{"ann@a.com", "bob@b.com", "cat@c.com", "hidden@h.com"}; !slices.Equal(taken[0].To, want) {
		t.Fatalf("to %v", taken[0].To)
	}
	if strings.Contains(strings.ToLower(taken[0].Data), "hidden@h.com") {
		t.Fatal("the Bcc can be seen in the message")
	}
	got := Parse([]byte(taken[0].Data))
	if got.Subject != "周五的晚饭" || got.Text != "七点见\n再联系" || got.Address != a.Email || got.MessageID == "" {
		t.Fatalf("%+v", got)
	}

	// A server that does not file what it sends gets a copy, which the next sync brings here apart from the inbox.
	if !s.Asked(`APPEND "Sent" (\Seen)`) {
		t.Fatal("no copy was filed")
	}
	if _, err := m.Sync(context.Background(), a, nil); err != nil {
		t.Fatal(err)
	}
	sent, _ := m.Store.List(Filter{Box: BoxSent})
	if len(sent) != 1 || sent[0].Subject != "周五的晚饭" || sent[0].Inbox {
		t.Fatalf("%+v", sent)
	}
	if inbox, _ := m.Store.List(Filter{Inbox: true}); len(inbox) != 0 {
		t.Fatalf("%+v", inbox)
	}
	if n, _ := m.Store.CountUnorganised(nil, "model"); n != 0 {
		t.Fatal("a model would read what the owner sent")
	}
}

func TestGmailFilesItsOwnCopy(t *testing.T) {
	s := mailtest.New(t, true)
	m, a, out := sending(t, s)
	if err := m.Send(context.Background(), a, Letter{From: a.Email, To: "ann@a.com", Subject: "x", Text: "y"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(out.Taken()) != 1 || s.Asked("APPEND") {
		t.Fatalf("%d, %v", len(out.Taken()), s.Commands)
	}
}

func TestAMissedCopySaysTheLetterWent(t *testing.T) {
	s := mailtest.New(t, false)
	s.NoSent = true
	m, a, out := sending(t, s)
	err := m.Send(context.Background(), a, Letter{From: a.Email, To: "ann@a.com", Subject: "x"}, time.Now())
	if !errors.Is(err, ErrNotFiled) || len(out.Taken()) != 1 {
		t.Fatal(err, out.Taken())
	}
}

func TestWhatCannotBeSentSaysWhy(t *testing.T) {
	s := mailtest.New(t, false)
	m, a, out := sending(t, s)
	ctx := context.Background()
	if err := m.Send(ctx, a, Letter{From: a.Email, Text: "y"}, time.Now()); !errors.Is(err, ErrNoRecipient) {
		t.Fatal(err)
	}
	if err := m.Send(ctx, a, Letter{From: a.Email, To: "not an address"}, time.Now()); err == nil {
		t.Fatal("a bad address went")
	}
	out.Refuse = "gone@a.com"
	if err := m.Send(ctx, a, Letter{From: a.Email, To: "ann@a.com, gone@a.com"}, time.Now()); err == nil || !strings.Contains(err.Error(), "gone@a.com") {
		t.Fatal(err)
	}
	out.Refuse, out.Password = "", "another"
	if err := m.Send(ctx, a, Letter{From: a.Email, To: "ann@a.com"}, time.Now()); !errors.Is(err, ErrLogin) {
		t.Fatal(err)
	}
	if len(out.Taken()) != 0 || s.Asked("APPEND") {
		t.Fatal("something went")
	}
	a.SMTP = ""
	if err := m.Send(ctx, a, Letter{From: a.Email, To: "ann@a.com"}, time.Now()); err == nil {
		t.Fatal("no outgoing server")
	}
}

func TestAFileGoesWithALetter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "报告.txt")
	content := bytes.Repeat([]byte("the report\n"), 100)
	os.WriteFile(path, content, 0o644)
	raw, err := Letter{From: "me@a.com", To: "ann@a.com", Subject: "s", Text: "正文", Files: []string{path}}.Build(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := Parse(raw); got.Text != "正文" {
		t.Fatalf("%+v", got)
	}
	msg, err := stdmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	r := multipart.NewReader(msg.Body, params["boundary"])
	r.NextPart()
	part, err := r.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if part.FileName() != "报告.txt" {
		t.Fatal(part.FileName())
	}
	data, _ := io.ReadAll(base64Part(part))
	if !bytes.Equal(data, content) {
		t.Fatalf("%d bytes", len(data))
	}

	big := filepath.Join(dir, "big")
	os.WriteFile(big, make([]byte, maxLetter+1), 0o644)
	if _, err := (Letter{From: "me@a.com", To: "a@a.com", Files: []string{big}}).Build(time.Now()); !errors.Is(err, ErrTooLarge) {
		t.Fatal(err)
	}
}

func base64Part(r io.Reader) io.Reader { return (&partHeader{encoding: "base64"}).decoded(r) }

func TestAHeaderCannotBeAddedThroughALineBreak(t *testing.T) {
	raw, err := Letter{From: "a@a.com", To: "b@b.com", Subject: "hi\r\nBcc: y@y.com", InReplyTo: "<1@x>\r\nBcc: z@z.com", Text: "t"}.Build(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "\r\nBcc:") {
		t.Fatalf("%s", raw)
	}
}

func TestAnAnswerGoesToTheRightPeople(t *testing.T) {
	m := Message{From: "Ann", Address: "ann@a.com", To: "me@x.com, Bob <bob@b.com>", Cc: "cat@c.com, ME@x.com", Subject: "Dinner",
		MessageID: "<2@x>", InReplyTo: "<1@x>", Text: "Tonight?\nAt seven?", Box: BoxInbox, Date: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)}
	one := m.Answer("me@x.com", false)
	if one.To != "ann@a.com" || one.Cc != "" || one.Subject != "Re: Dinner" || one.InReplyTo != "<2@x>" || one.References != "<1@x> <2@x>" {
		t.Fatalf("%+v", one)
	}
	if !strings.Contains(one.Text, "> Tonight?\n> At seven?") {
		t.Fatal(one.Text)
	}
	if all := m.Answer("me@x.com", true); all.Cc != "bob@b.com, cat@c.com" {
		t.Fatal(all.Cc)
	}
	m.ReplyTo, m.Subject = "list@a.com", "RE: Dinner"
	if got := m.Answer("me@x.com", false); got.To != "list@a.com" || got.Subject != "RE: Dinner" {
		t.Fatalf("%+v", got)
	}
	m.Box = BoxSent
	if got := m.Answer("me@x.com", false); got.To != m.To {
		t.Fatalf("%+v", got)
	}
	if f := m.Forwarded("me@x.com"); f.To != "" || f.Subject != "Fwd: RE: Dinner" || !strings.Contains(f.Text, "Tonight?") || f.InReplyTo != "" {
		t.Fatalf("%+v", f)
	}
}

func TestAStarIsTheSameHereAndOnTheServer(t *testing.T) {
	s := mailtest.New(t, true)
	s.Add("A <a@a.com>", "one", "x")
	s.Add("A <a@a.com>", "two", "x")
	archived := s.Add("A <a@a.com>", "elsewhere", "x")
	archived.Box = "Other"
	m := testMailer(t)
	a := accountOf("a", s)
	ctx := context.Background()
	if _, err := m.Sync(ctx, a, nil); err != nil {
		t.Fatal(err)
	}
	starred := func() []string {
		t.Helper()
		list, err := m.Store.List(Filter{Starred: true})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, msg := range list {
			out = append(out, msg.Subject)
		}
		slices.Sort(out)
		return out
	}

	// Here to the server.
	list, _ := m.Store.List(Filter{Inbox: true})
	one := list[slices.IndexFunc(list, func(x Message) bool { return x.Subject == "one" })]
	m.Store.Star(one.ID, true)
	if err := m.PushStars(ctx, a); err != nil {
		t.Fatal(err)
	}
	if !s.Messages[0].Flagged || s.Messages[1].Flagged {
		t.Fatal("the server was not told")
	}

	// The server to here, a message outside the inbox with it.
	s.Messages[1].Flagged, archived.Flagged = true, true
	m.Sync(ctx, a, nil)
	if got := starred(); !slices.Equal(got, []string{"elsewhere", "one", "two"}) {
		t.Fatal(got)
	}
	if inbox, _ := m.Store.List(Filter{Inbox: true}); len(inbox) != 2 {
		t.Fatalf("a star brought mail into the inbox: %d", len(inbox))
	}

	// A star taken off on the server goes here, in the inbox and outside it, and the other way.
	s.Messages[0].Flagged, archived.Flagged = false, false
	m.Sync(ctx, a, nil)
	if got := starred(); !slices.Equal(got, []string{"two"}) {
		t.Fatal(got)
	}
	m.Store.Star(list[slices.IndexFunc(list, func(x Message) bool { return x.Subject == "two" })].ID, false)
	if got := starred(); len(got) != 0 {
		t.Fatal(got)
	}
	m.Sync(ctx, a, nil) // what is set here goes first, whatever the server last said
	if s.Messages[1].Flagged || len(starred()) != 0 {
		t.Fatal("the star came back")
	}
}

func TestAStarSetWhileOfflineGoesLater(t *testing.T) {
	s := mailtest.New(t, false)
	s.Add("A <a@a.com>", "one", "x")
	m := testMailer(t)
	a := accountOf("a", s)
	ctx := context.Background()
	m.Sync(ctx, a, nil)
	list, _ := m.Store.List(Filter{})
	m.Store.Star(list[0].ID, true)
	password := m.Password
	m.Password = func(string) (string, error) { return "", errors.New("offline") }
	if err := m.PushStars(ctx, a); err == nil {
		t.Fatal("no failure")
	}
	m.Password = password
	m.Sync(ctx, a, nil)
	if !s.Messages[0].Flagged {
		t.Fatal("the star never went")
	}
	if got, _ := m.Store.List(Filter{Starred: true}); len(got) != 1 {
		t.Fatal("the star went away")
	}
}

// beforeBoxes are the columns of a database of the version before the boxes.
func beforeBoxes() string {
	return strings.NewReplacer(`box TEXT NOT NULL DEFAULT 'inbox', cc TEXT NOT NULL DEFAULT '', reply_to TEXT NOT NULL DEFAULT '',`, "",
		"star_dirty INTEGER NOT NULL DEFAULT 0, -- starred differs from the server's until it is told", "",
		"UNIQUE (account, box, validity, uid)", "UNIQUE (account, validity, uid)").Replace(messageColumns)
}

func TestAnOldDatabaseGetsItsBoxes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mail.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"DROP TABLE messages", "CREATE TABLE messages (" + beforeBoxes() + ")",
		"INSERT INTO messages (account, validity, uid, subject, tier, organised, starred, pick, html, paged) VALUES ('a', 1, 1, 'old', 'must', 'model', 1, 'noise', '<p>', 1)",
		"INSERT INTO log (at, message, account, subject, action) VALUES (1, 1, 'a', 'old', 'read')"} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	s.Close()
	if s, err = OpenStore(path); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	list, err := s.List(Filter{Inbox: true})
	if err != nil || len(list) != 1 {
		t.Fatal(len(list), err)
	}
	if m := list[0]; m.Box != BoxInbox || m.Organised != "model" || m.Tier != TierNoise || !m.Starred {
		t.Fatalf("%+v", m)
	}
	var dirty int
	s.db.QueryRow("SELECT star_dirty FROM messages").Scan(&dirty)
	if dirty != 1 {
		t.Fatal("the server would never hear of the star")
	}
	if b, err := s.Body(list[0].ID); err != nil || b.HTML != "<p>" {
		t.Fatal(b, err)
	}
	// One UID in two folders is two messages.
	if err := s.put("a", BoxSent, 1, Fetched{UID: 1, Raw: []byte("Subject: sent\r\n\r\nx")}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.List(Filter{Box: BoxSent}); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
}
