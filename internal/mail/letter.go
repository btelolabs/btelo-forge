package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	smtptext "net/textproto"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// maxLetter is how much a letter may hold: what most servers take, which base64 makes a third larger.
const maxLetter = 25 << 20

// sendLimit is how long a server has to take a letter, which may be large.
const sendLimit = 5 * time.Minute

// ErrTooLarge means the files of a letter are more than a mail server takes.
var ErrTooLarge = errors.New("the files of this letter are too large to send")

// ErrNoRecipient means a letter has nobody to go to.
var ErrNoRecipient = errors.New("this letter has no recipient")

// ErrNotFiled means a letter went out and only the copy for the sent folder failed, so it must not be sent again.
var ErrNotFiled = errors.New("the letter was sent, but could not be filed in the sent folder")

// Letter is mail the user writes. To, Cc and Bcc are as typed: addresses apart by commas or semicolons.
type Letter struct {
	From, To, Cc, Bcc string
	Subject, Text     string
	InReplyTo         string
	References        string
	Files             []string
}

// addresses reads a field of addresses, in the separators a person types in either language.
func addresses(field string) ([]*mail.Address, error) {
	field = strings.NewReplacer("；", ",", "，", ",", ";", ",", "\r", "", "\n", "").Replace(field)
	var parts []string
	for part := range strings.SplitSeq(field, ",") {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return nil, nil
	}
	return mail.ParseAddressList(strings.Join(parts, ","))
}

// Recipients are everyone the letter goes to, Bcc among them.
func (l Letter) Recipients() ([]string, error) {
	var out []string
	for _, field := range []string{l.To, l.Cc, l.Bcc} {
		list, err := addresses(field)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", field, err)
		}
		for _, a := range list {
			if !slices.Contains(out, a.Address) {
				out = append(out, a.Address)
			}
		}
	}
	if len(out) == 0 {
		return nil, ErrNoRecipient
	}
	return out, nil
}

func addressHeader(b *bytes.Buffer, name, field string) error {
	list, err := addresses(field)
	if err != nil || len(list) == 0 {
		return err
	}
	parts := make([]string, len(list))
	for i, a := range list {
		parts[i] = a.String()
	}
	fmt.Fprintf(b, "%s: %s\r\n", name, strings.Join(parts, ",\r\n "))
	return nil
}

func wrapped(b *bytes.Buffer, data []byte) {
	text := base64.StdEncoding.EncodeToString(data)
	for len(text) > 76 {
		b.WriteString(text[:76] + "\r\n")
		text = text[76:]
	}
	b.WriteString(text + "\r\n")
}

// Build makes the message. Bcc is left out: it is what no recipient may see. A header cannot hold a line break, which would
// start another.
func (l Letter) Build(at time.Time) ([]byte, error) {
	size := len(l.Text)
	type file struct {
		name string
		data []byte
	}
	var files []file
	for _, path := range l.Files {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if size += len(data); size > maxLetter {
			return nil, ErrTooLarge
		}
		files = append(files, file{filepath.Base(path), data})
	}
	clean := strings.NewReplacer("\r", "", "\n", "")
	id := make([]byte, 12)
	rand.Read(id)
	_, domain, _ := strings.Cut(l.From, "@")
	var b bytes.Buffer
	if err := addressHeader(&b, "From", l.From); err != nil {
		return nil, err
	}
	if err := addressHeader(&b, "To", l.To); err != nil {
		return nil, err
	}
	if err := addressHeader(&b, "Cc", l.Cc); err != nil {
		return nil, err
	}
	fmt.Fprintf(&b, "Subject: %s\r\nDate: %s\r\n", mime.BEncoding.Encode("utf-8", clean.Replace(l.Subject)), at.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", hex.EncodeToString(id), clean.Replace(domain))
	if l.InReplyTo != "" {
		fmt.Fprintf(&b, "In-Reply-To: %s\r\n", clean.Replace(l.InReplyTo))
	}
	if l.References != "" {
		fmt.Fprintf(&b, "References: %s\r\n", clean.Replace(l.References))
	}
	b.WriteString("MIME-Version: 1.0\r\n")
	text := []byte(strings.ReplaceAll(strings.ReplaceAll(l.Text, "\r\n", "\n"), "\n", "\r\n"))
	if len(files) == 0 {
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n")
		wrapped(&b, text)
		return b.Bytes(), nil
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", w.Boundary())
	part, err := w.CreatePart(smtptext.MIMEHeader{"Content-Type": {"text/plain; charset=utf-8"}, "Content-Transfer-Encoding": {"base64"}})
	if err != nil {
		return nil, err
	}
	var encoded bytes.Buffer
	wrapped(&encoded, text)
	part.Write(encoded.Bytes())
	for _, f := range files {
		kind := mime.TypeByExtension(filepath.Ext(f.name))
		if kind == "" {
			kind = "application/octet-stream"
		}
		part, err := w.CreatePart(smtptext.MIMEHeader{"Content-Type": {kind}, "Content-Transfer-Encoding": {"base64"},
			"Content-Disposition": {mime.FormatMediaType("attachment", map[string]string{"filename": f.name})}})
		if err != nil {
			return nil, err
		}
		encoded.Reset()
		wrapped(&encoded, f.data)
		part.Write(encoded.Bytes())
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	b.Write(body.Bytes())
	return b.Bytes(), nil
}

// subjectWith puts a prefix before a subject that has none of its kind.
func subjectWith(prefix, subject string) string {
	if strings.HasPrefix(strings.ToLower(subject), strings.ToLower(prefix)) {
		return subject
	}
	return prefix + " " + subject
}

func quoted(text string) string {
	return "> " + strings.ReplaceAll(strings.TrimSpace(text), "\n", "\n> ")
}

// Answer begins a reply to a message, to its sender or, with all, to everyone on it but the owner. A reply to what the owner
// sent goes to the same people.
func (m Message) Answer(me string, all bool) Letter {
	l := Letter{From: me, Subject: subjectWith("Re:", m.Subject), InReplyTo: m.MessageID,
		References: strings.TrimSpace(m.InReplyTo + " " + m.MessageID)}
	switch {
	case m.Box == BoxSent:
		l.To = m.To
	case m.ReplyTo != "":
		l.To = m.ReplyTo
	default:
		l.To = m.Address
	}
	if all {
		var others []string
		first, _ := addresses(l.To)
		for _, field := range []string{m.To, m.Cc} {
			list, err := addresses(field)
			if err != nil {
				continue
			}
			for _, a := range list {
				taken := slices.ContainsFunc(first, func(f *mail.Address) bool { return strings.EqualFold(f.Address, a.Address) })
				if !taken && !strings.EqualFold(a.Address, me) && !slices.Contains(others, a.Address) {
					others = append(others, a.Address)
				}
			}
		}
		l.Cc = strings.Join(others, ", ")
	}
	l.Text = fmt.Sprintf("\n\n%s, %s:\n%s\n", m.Date.Format("2006-01-02 15:04"), m.From, quoted(m.Text))
	return l
}

// Forwarded begins a letter that passes a message's text on. Its attachments stay on the server, so they do not go with it.
func (m Message) Forwarded(me string) Letter {
	return Letter{From: me, Subject: subjectWith("Fwd:", m.Subject), Text: fmt.Sprintf("\n\n---------- Forwarded message ----------\nFrom: %s <%s>\nDate: %s\nSubject: %s\nTo: %s\n\n%s\n",
		m.From, m.Address, m.Date.Format("2006-01-02 15:04"), m.Subject, m.To, strings.TrimSpace(m.Text))}
}

func (m *Mailer) reachSMTP(ctx context.Context, address string) (net.Conn, error) {
	if m.DialSMTP != nil {
		return m.DialSMTP(ctx, address)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: 20 * time.Second}
	if port != "465" {
		return dialer.DialContext(ctx, "tcp", address)
	}
	d := tls.Dialer{NetDialer: &dialer, Config: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}}
	return d.DialContext(ctx, "tcp", address)
}

// deliver hands a message to the account's outgoing server. Only a server on this computer, as Proton Mail Bridge is, may
// go without TLS or have a certificate of its own.
func (m *Mailer) deliver(ctx context.Context, a Account, recipients []string, raw []byte) error {
	host, _, err := net.SplitHostPort(a.SMTP)
	if err != nil {
		return errors.New("this mailbox has no outgoing server")
	}
	password, err := m.Password(a.ID)
	if err != nil {
		return err
	}
	nc, err := m.reachSMTP(ctx, a.SMTP)
	if err != nil {
		return fmt.Errorf("could not reach the outgoing mail server: %w", err)
	}
	defer nc.Close()
	nc.SetDeadline(time.Now().Add(sendLimit))
	defer context.AfterFunc(ctx, func() { nc.Close() })()
	c, err := smtp.NewClient(nc, host)
	if err != nil {
		return err
	}
	defer c.Close()
	ip := net.ParseIP(host)
	local := ip != nil && ip.IsLoopback()
	if _, secure := nc.(*tls.Conn); !secure {
		if has, _ := c.Extension("STARTTLS"); has {
			if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: local}); err != nil {
				return err
			}
		} else if !local {
			return errors.New("the outgoing mail server does not offer an encrypted connection")
		}
	}
	if err := c.Auth(smtp.PlainAuth("", a.Email, password, host)); err != nil {
		var refused *smtptext.Error
		if errors.As(err, &refused) && refused.Code/100 == 5 {
			return fmt.Errorf("%w: %s", ErrLogin, refused.Msg)
		}
		return err
	}
	if err := c.Mail(a.Email); err != nil {
		return err
	}
	for _, to := range recipients {
		if err := c.Rcpt(to); err != nil {
			return fmt.Errorf("%s: %w", to, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(raw); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// Send sends a letter at the user's click and puts a copy in the sent folder, which Gmail does by itself.
func (m *Mailer) Send(ctx context.Context, a Account, l Letter, at time.Time) error {
	recipients, err := l.Recipients()
	if err != nil {
		return err
	}
	raw, err := l.Build(at)
	if err != nil {
		return err
	}
	if err := m.deliver(ctx, a, recipients, raw); err != nil {
		return err
	}
	// The letter has gone: what follows may not be stopped by a click, and must not read as a failure to send.
	c, err := m.connect(context.WithoutCancel(ctx), a)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotFiled, err)
	}
	defer c.Close()
	if c.Gmail() {
		return nil
	}
	folders, err := c.FindFolders()
	if err == nil && folders.Sent == "" {
		err = errors.New("this mailbox has no sent folder")
	}
	if err == nil {
		err = c.Append(folders.Sent, `\Seen`, raw)
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotFiled, err)
	}
	return nil
}

// SaveDraft saves a letter in the account's drafts.
func (m *Mailer) SaveDraft(ctx context.Context, a Account, l Letter, at time.Time) error {
	raw, err := l.Build(at)
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
	if folders.Drafts == "" {
		return errors.New("this mailbox has no drafts folder")
	}
	return c.Append(folders.Drafts, `\Draft \Seen`, raw)
}
