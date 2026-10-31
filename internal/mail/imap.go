// Package mail keeps a copy of the user's mailbox on this computer and organises it with a local model. It speaks IMAP,
// and only the part of it that reads, labels, archives and saves drafts: there is no way here to send or delete a
// message, which TestNothingCanSendOrDelete holds to.
package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// ErrLogin means the server refused the address and password.
var ErrLogin = errors.New("the mail server did not accept the address and password")

// maxLiteral bounds what one response may hold, so a server that sends something else cannot fill the memory.
const maxLiteral = 64 << 20

// ioLimit is how long the server has to answer a command.
const ioLimit = 90 * time.Second

// Conn is a connection to an IMAP server.
type Conn struct {
	nc    net.Conn
	r     *bufio.Reader
	tag   int
	caps  map[string]bool
	inbox string // the selected mailbox
}

// Dial connects to the server over TLS. config may be nil. A server on this computer, as Proton Mail Bridge is, starts in
// the clear, moves to TLS with STARTTLS and has a certificate of its own, which is fine as nothing leaves the computer.
func Dial(ctx context.Context, address string, config *tls.Config) (*Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() && config == nil {
		return dialLocal(ctx, address)
	}
	if config == nil {
		config = &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 20 * time.Second}, Config: config}
	nc, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("could not reach the mail server: %w", err)
	}
	c := &Conn{nc: nc, r: bufio.NewReaderSize(nc, 64<<10), caps: map[string]bool{}}
	c.deadline()
	greeting, err := c.readResponse()
	if err != nil {
		nc.Close()
		return nil, err
	}
	if len(greeting) < 2 || greeting[0] != "*" || !strings.EqualFold(atom(greeting[1]), "OK") {
		nc.Close()
		return nil, errors.New("the server did not greet as an IMAP server")
	}
	return c, nil
}

func dialLocal(ctx context.Context, address string) (*Conn, error) {
	var d net.Dialer
	nc, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("could not reach the mail server: %w", err)
	}
	c := &Conn{nc: nc, r: bufio.NewReaderSize(nc, 64<<10), caps: map[string]bool{}}
	c.deadline()
	if _, err := c.readResponse(); err != nil {
		nc.Close()
		return nil, err
	}
	if err := c.run("STARTTLS", nil); err != nil {
		nc.Close()
		return nil, err
	}
	secure := tls.Client(nc, &tls.Config{InsecureSkipVerify: true}) // a loopback address, and a certificate made on this computer
	if err := secure.HandshakeContext(ctx); err != nil {
		nc.Close()
		return nil, err
	}
	c.nc, c.r = secure, bufio.NewReaderSize(secure, 64<<10)
	return c, nil
}

// Close ends the connection.
func (c *Conn) Close() error {
	c.deadline()
	c.run("LOGOUT", nil)
	return c.nc.Close()
}

func (c *Conn) deadline() { c.nc.SetDeadline(time.Now().Add(ioLimit)) }

// Has reports whether the server named the capability.
func (c *Conn) Has(name string) bool { return c.caps[name] }

// Login signs in, and says who is asking where the server wants to know, as 163 does.
func (c *Conn) Login(user, password string) error {
	if err := c.run("LOGIN "+quote(user)+" "+quote(password), nil); err != nil {
		var no *refused
		if errors.As(err, &no) {
			return fmt.Errorf("%w: %s", ErrLogin, no.text)
		}
		return err
	}
	if err := c.capabilities(); err != nil {
		return err
	}
	if c.Has("ID") {
		// Only a courtesy: a server that refuses it still works.
		c.run(`ID ("name" "Btelo Forge" "version" "1")`, nil)
	}
	return nil
}

func (c *Conn) capabilities() error {
	return c.run("CAPABILITY", func(fields []any) {
		if len(fields) > 2 && strings.EqualFold(atom(fields[1]), "CAPABILITY") {
			for _, f := range fields[2:] {
				c.caps[strings.ToUpper(atom(f))] = true
			}
		}
	})
}

// Gmail reports whether the server has Gmail's labels and ids.
func (c *Conn) Gmail() bool { return c.Has("X-GM-EXT-1") }

// Folders are the mailboxes that have a use here, by name on the server; "" when the server has none.
type Folders struct {
	Inbox, Archive, Drafts string
}

// FindFolders asks the server which of its mailboxes are which.
func (c *Conn) FindFolders() (Folders, error) {
	f := Folders{Inbox: "INBOX"}
	var archive, drafts string // named so, on a server that does not say what its folders are for
	err := c.run(`LIST "" "*"`, func(fields []any) {
		if len(fields) < 5 || !strings.EqualFold(atom(fields[1]), "LIST") {
			return
		}
		attrs, _ := fields[2].([]any)
		name := atom(fields[4])
		switch strings.ToLower(name) {
		case "archive", "archives":
			archive = name
		case "drafts", "draft":
			drafts = name
		}
		for _, a := range attrs {
			switch strings.ToLower(atom(a)) {
			case `\archive`, `\all`:
				if f.Archive == "" || strings.EqualFold(atom(a), `\all`) {
					f.Archive = name
				}
			case `\drafts`:
				f.Drafts = name
			}
		}
	})
	if f.Archive == "" {
		f.Archive = archive
	}
	if f.Drafts == "" {
		f.Drafts = drafts
	}
	return f, err
}

// Selected is what the server says of a mailbox when it is opened.
type Selected struct {
	Validity uint32 // what UIDs mean; when it changes, UIDs known before mean nothing
	Exists   int
}

// Select opens a mailbox read and write.
func (c *Conn) Select(name string) (Selected, error) {
	var s Selected
	err := c.run("SELECT "+quote(name), func(fields []any) {
		if len(fields) >= 3 && strings.EqualFold(atom(fields[2]), "EXISTS") {
			s.Exists, _ = strconv.Atoi(atom(fields[1]))
		}
		for i, f := range fields {
			if code, ok := f.([]any); ok && i > 0 && len(code) >= 2 && strings.EqualFold(atom(code[0]), "UIDVALIDITY") {
				v, _ := strconv.ParseUint(atom(code[1]), 10, 32)
				s.Validity = uint32(v)
			}
		}
	})
	if err == nil {
		c.inbox = name
	}
	return s, err
}

// SearchSince lists the UIDs of the selected mailbox's messages from a day on, oldest first.
func (c *Conn) SearchSince(day time.Time) ([]uint32, error) {
	return c.search("SINCE " + day.Format("2-Jan-2006"))
}

// FindByID lists the UIDs of the selected mailbox's messages with a Message-ID.
func (c *Conn) FindByID(messageID string) ([]uint32, error) {
	return c.search("HEADER Message-ID " + quote(messageID))
}

func (c *Conn) search(criteria string) ([]uint32, error) {
	var out []uint32
	err := c.run("UID SEARCH "+criteria, func(fields []any) {
		if len(fields) >= 2 && strings.EqualFold(atom(fields[1]), "SEARCH") {
			for _, f := range fields[2:] {
				if v, err := strconv.ParseUint(atom(f), 10, 32); err == nil {
					out = append(out, uint32(v))
				}
			}
		}
	})
	return out, err
}

// Fetched is one message as the server gave it.
type Fetched struct {
	UID      uint32
	Flags    []string
	Labels   []string // Gmail's
	GmailID  string
	ThreadID string
	Raw      []byte // the start of the message, when it was asked for
}

// bodyPart is how much of a message is fetched: enough for its text, and attachments stay on the server.
const bodyPart = 256 << 10

// Fetch reads messages by UID, with their first bodyPart bytes when body is set. each is called for every message.
func (c *Conn) Fetch(uids []uint32, body bool, each func(Fetched)) error {
	if len(uids) == 0 {
		return nil
	}
	items := []string{"UID", "FLAGS"}
	if c.Gmail() {
		items = append(items, "X-GM-MSGID", "X-GM-THRID", "X-GM-LABELS")
	}
	if body {
		items = append(items, fmt.Sprintf("BODY.PEEK[]<0.%d>", bodyPart))
	}
	return c.run(fmt.Sprintf("UID FETCH %s (%s)", uidSet(uids), strings.Join(items, " ")), func(fields []any) {
		if len(fields) < 4 || !strings.EqualFold(atom(fields[2]), "FETCH") {
			return
		}
		list, _ := fields[3].([]any)
		var m Fetched
		for i := 0; i+1 < len(list); i += 2 {
			key, value := strings.ToUpper(atom(list[i])), list[i+1]
			switch {
			case key == "UID":
				v, _ := strconv.ParseUint(atom(value), 10, 32)
				m.UID = uint32(v)
			case key == "FLAGS":
				m.Flags = atoms(value)
			case key == "X-GM-LABELS":
				m.Labels = atoms(value)
			case key == "X-GM-MSGID":
				m.GmailID = atom(value)
			case key == "X-GM-THRID":
				m.ThreadID = atom(value)
			case strings.HasPrefix(key, "BODY[") && value != nil:
				m.Raw = []byte(atom(value))
			}
		}
		if m.UID != 0 {
			each(m)
		}
	})
}

// AddLabel and RemoveLabel put a Gmail label on a message of the selected mailbox, or take it off.
func (c *Conn) AddLabel(uid uint32, label string) error    { return c.label(uid, "+", label) }
func (c *Conn) RemoveLabel(uid uint32, label string) error { return c.label(uid, "-", label) }

func (c *Conn) label(uid uint32, sign, label string) error {
	return c.run(fmt.Sprintf("UID STORE %d %sX-GM-LABELS (%s)", uid, sign, quote(label)), nil)
}

// Move moves a message of the selected mailbox to another, which a mail server does in one step.
func (c *Conn) Move(uid uint32, to string) error {
	if !c.Has("MOVE") {
		return errors.New("this mail server cannot move messages")
	}
	return c.run(fmt.Sprintf("UID MOVE %d %s", uid, quote(to)), nil)
}

// Append saves a message in a mailbox, marked as a draft.
func (c *Conn) Append(mailbox string, message []byte) error {
	c.deadline()
	c.tag++
	tag := "a" + strconv.Itoa(c.tag)
	if _, err := fmt.Fprintf(c.nc, "%s APPEND %s (\\Draft \\Seen) {%d}\r\n", tag, quote(mailbox), len(message)); err != nil {
		return err
	}
	for {
		fields, err := c.readResponse()
		if err != nil {
			return err
		}
		if len(fields) == 0 {
			continue
		}
		if atom(fields[0]) == "+" {
			break
		}
		if atom(fields[0]) == tag {
			return status(fields)
		}
	}
	if _, err := c.nc.Write(append(append([]byte(nil), message...), "\r\n"...)); err != nil {
		return err
	}
	return c.finish(tag, nil)
}

func (c *Conn) run(command string, untagged func([]any)) error {
	c.deadline()
	c.tag++
	tag := "a" + strconv.Itoa(c.tag)
	if _, err := io.WriteString(c.nc, tag+" "+command+"\r\n"); err != nil {
		return err
	}
	return c.finish(tag, untagged)
}

func (c *Conn) finish(tag string, untagged func([]any)) error {
	for {
		c.deadline()
		fields, err := c.readResponse()
		if err != nil {
			return err
		}
		if len(fields) == 0 {
			continue
		}
		switch atom(fields[0]) {
		case "*":
			if len(fields) > 1 && strings.EqualFold(atom(fields[1]), "CAPABILITY") {
				for _, f := range fields[2:] {
					c.caps[strings.ToUpper(atom(f))] = true
				}
			}
			if untagged != nil {
				untagged(fields)
			}
		case tag:
			return status(fields)
		}
	}
}

// refused is a server's NO or BAD to a command.
type refused struct{ text string }

func (r *refused) Error() string { return "the mail server refused: " + r.text }

func status(fields []any) error {
	if len(fields) > 1 && strings.EqualFold(atom(fields[1]), "OK") {
		return nil
	}
	var words []string
	for _, f := range fields[2:] {
		if _, list := f.([]any); !list {
			words = append(words, atom(f))
		}
	}
	return &refused{strings.Join(words, " ")}
}

// readResponse reads one line of a response into its parts: an atom or string is a string, NIL is nil, a parenthesised
// or bracketed list is a []any, and a literal is a string however long.
func (c *Conn) readResponse() ([]any, error) {
	var stack [][]any
	line := []any{}
	for {
		b, err := c.r.ReadByte()
		if err != nil {
			return nil, err
		}
		switch b {
		case ' ':
		case '\r':
		case '\n':
			if len(stack) > 0 {
				return nil, errors.New("the server's response ended inside a list")
			}
			return line, nil
		case '(', '[':
			// A bracket inside an atom, as in BODY[], is read by the atom below.
			stack = append(stack, line)
			line = []any{}
		case ')', ']':
			if len(stack) == 0 {
				return nil, errors.New("the server's response has an unmatched bracket")
			}
			inner := line
			line = stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			line = append(line, inner)
		case '"':
			s, err := c.readQuoted()
			if err != nil {
				return nil, err
			}
			line = append(line, s)
		case '{':
			s, err := c.readLiteral()
			if err != nil {
				return nil, err
			}
			line = append(line, s)
		default:
			c.r.UnreadByte()
			a, err := c.readAtom()
			if err != nil {
				return nil, err
			}
			if a == "NIL" {
				line = append(line, nil)
			} else {
				line = append(line, a)
			}
		}
	}
}

func (c *Conn) readQuoted() (string, error) {
	var out []byte
	for {
		b, err := c.r.ReadByte()
		if err != nil {
			return "", err
		}
		switch b {
		case '"':
			return string(out), nil
		case '\\':
			if b, err = c.r.ReadByte(); err != nil {
				return "", err
			}
		}
		out = append(out, b)
	}
}

func (c *Conn) readLiteral() (string, error) {
	size, err := c.r.ReadString('}')
	if err != nil {
		return "", err
	}
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSuffix(size, "}"), "+"))
	if err != nil || n < 0 || n > maxLiteral {
		return "", errors.New("the server sent a block of an impossible size")
	}
	if _, err := c.r.ReadString('\n'); err != nil {
		return "", err
	}
	c.deadline()
	out := make([]byte, n)
	if _, err := io.ReadFull(c.r, out); err != nil {
		return "", err
	}
	return string(out), nil
}

// readAtom reads a word. BODY[]<0> and flags like \Seen are single words; a closing bracket ends a word only when no
// opening one is inside it.
func (c *Conn) readAtom() (string, error) {
	var out []byte
	depth := 0
	for {
		b, err := c.r.ReadByte()
		if err != nil {
			return "", err
		}
		switch b {
		case ' ', '\r', '\n', '(', ')':
			c.r.UnreadByte()
			return string(out), nil
		case '[':
			depth++
		case ']':
			if depth == 0 {
				c.r.UnreadByte()
				return string(out), nil
			}
			depth--
		}
		out = append(out, b)
	}
}

func atom(v any) string {
	s, _ := v.(string)
	return s
}

func atoms(v any) []string {
	list, _ := v.([]any)
	var out []string
	for _, a := range list {
		out = append(out, atom(a))
	}
	return out
}

// quote makes a quoted string of text; a line break cannot be in one, and would start a command of its own.
func quote(text string) string {
	text = strings.NewReplacer("\r", "", "\n", "").Replace(text)
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(text) + `"`
}

func uidSet(uids []uint32) string {
	parts := make([]string, len(uids))
	for i, u := range uids {
		parts[i] = strconv.FormatUint(uint64(u), 10)
	}
	return strings.Join(parts, ",")
}
