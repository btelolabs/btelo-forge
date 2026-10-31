package mail

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"
)

// Parsed is what is kept of a message.
type Parsed struct {
	MessageID string
	InReplyTo string
	From      string // the name, or the address when it has none
	Address   string
	To        string
	Subject   string
	Date      time.Time
	Text      string
}

// decoder reads header words in any charset a mailbox holds, of which old Chinese mail has several.
var decoder = mime.WordDecoder{CharsetReader: func(charset string, in io.Reader) (io.Reader, error) {
	enc, err := charsetOf(charset)
	if err != nil {
		return nil, err
	}
	return enc.NewDecoder().Reader(in), nil
}}

func charsetOf(name string) (encoding.Encoding, error) {
	enc, err := htmlindex.Get(strings.TrimSpace(name))
	if err != nil {
		return nil, fmt.Errorf("unknown charset %q", name)
	}
	return enc, nil
}

// Parse reads a message, which may be cut short at bodyPart. What cannot be understood is left out, not an error: one
// odd message must not stop the rest of a mailbox from arriving.
func Parse(raw []byte) Parsed {
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return Parsed{Subject: "", Text: ""}
	}
	var p Parsed
	h := m.Header
	p.MessageID = strings.TrimSpace(h.Get("Message-Id"))
	p.InReplyTo = strings.TrimSpace(h.Get("In-Reply-To"))
	p.Subject = headerText(h.Get("Subject"))
	p.To = headerText(h.Get("To"))
	p.Date, _ = mail.ParseDate(h.Get("Date"))
	parser := mail.AddressParser{WordDecoder: &decoder}
	if a, err := parser.Parse(h.Get("From")); err == nil {
		p.From, p.Address = a.Name, a.Address
	} else {
		p.From = headerText(h.Get("From"))
	}
	if p.From == "" {
		p.From = p.Address
	}
	plain, rich := "", ""
	walk(textproto(h), m.Body, 0, &plain, &rich)
	p.Text = strings.TrimSpace(plain)
	if p.Text == "" {
		p.Text = strings.TrimSpace(htmlToText(rich))
	}
	return p
}

func headerText(s string) string {
	if out, err := decoder.DecodeHeader(s); err == nil {
		return strings.TrimSpace(out)
	}
	return strings.TrimSpace(s)
}

type partHeader struct{ kind, encoding string }

func textproto(h mail.Header) partHeader {
	return partHeader{h.Get("Content-Type"), h.Get("Content-Transfer-Encoding")}
}

// maxParts bounds how deep a message's parts are followed: a message is someone else's input.
const maxParts = 12

// walk finds the first plain and the first HTML text in a message's parts.
func walk(h partHeader, body io.Reader, depth int, plain, rich *string) {
	if depth > maxParts {
		return
	}
	kind, params, err := mime.ParseMediaType(h.kind)
	if err != nil {
		kind = "text/plain"
	}
	if strings.HasPrefix(kind, "multipart/") {
		r := multipart.NewReader(body, params["boundary"])
		for {
			part, err := r.NextRawPart()
			if err != nil {
				return // a message cut short ends here
			}
			if !strings.EqualFold(firstWord(part.Header.Get("Content-Disposition")), "attachment") {
				walk(partHeader{part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding")}, part, depth+1, plain, rich)
			}
		}
	}
	target := plain
	switch kind {
	case "text/plain":
	case "text/html":
		target = rich
	default:
		return
	}
	if *target != "" {
		return
	}
	var r io.Reader = body
	switch strings.ToLower(strings.TrimSpace(h.encoding)) {
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, lineless{body})
	case "quoted-printable":
		r = quotedprintable.NewReader(body)
	}
	data, _ := io.ReadAll(r) // what was read before a cut is kept
	*target = decodeText(data, params["charset"])
}

func firstWord(s string) string {
	word, _, _ := strings.Cut(s, ";")
	return strings.TrimSpace(word)
}

// lineless drops the line breaks of base64 text, which the standard reader takes only at block ends.
type lineless struct{ r io.Reader }

func (l lineless) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	k := 0
	for _, b := range p[:n] {
		if b != '\r' && b != '\n' {
			p[k] = b
			k++
		}
	}
	return k, err
}

func decodeText(data []byte, charset string) string {
	if charset != "" && !strings.EqualFold(charset, "utf-8") && !strings.EqualFold(charset, "us-ascii") {
		if enc, err := charsetOf(charset); err == nil {
			if out, err := enc.NewDecoder().Bytes(data); err == nil {
				return string(out)
			}
		}
	}
	return strings.ToValidUTF8(string(data), "�")
}

var (
	htmlHidden = regexp.MustCompile(`(?is)<(script|style|head)\b.*?</(script|style|head)\s*>`)
	htmlBreak  = regexp.MustCompile(`(?i)<(br|/p|/div|/tr|/li|/h[1-6])\b[^>]*>`)
	htmlTag    = regexp.MustCompile(`(?s)<[^>]*>`)
	blankLines = regexp.MustCompile(`\n[ \t]*\n[ \t\n]*`)
)

// htmlToText keeps the words of a page. Pictures and links do not load: a message is never rendered, so nothing in it
// can tell the sender that it was opened.
func htmlToText(s string) string {
	s = htmlHidden.ReplaceAllString(s, "")
	s = htmlBreak.ReplaceAllString(s, "\n")
	s = htmlTag.ReplaceAllString(s, "")
	return blankLines.ReplaceAllString(html.UnescapeString(s), "\n\n")
}

// Snippet is the start of a text on one line.
func Snippet(text string, runes int) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= runes {
		return text
	}
	return string([]rune(text)[:runes]) + "…"
}

// Reply is a draft written in answer to a message.
type Reply struct {
	From, To  string
	Subject   string
	InReplyTo string
	Text      string
}

// Build makes the message that is saved as a draft.
func (r Reply) Build(at time.Time) []byte {
	subject := r.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	id := make([]byte, 12)
	rand.Read(id)
	_, domain, _ := strings.Cut(r.From, "@")
	clean := strings.NewReplacer("\r", "", "\n", "") // a header cannot hold a line break, which would start another
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\n", clean.Replace(r.From), clean.Replace(r.To),
		mime.BEncoding.Encode("utf-8", clean.Replace(subject)), at.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", hex.EncodeToString(id), clean.Replace(domain))
	if r.InReplyTo != "" {
		fmt.Fprintf(&b, "In-Reply-To: %s\r\nReferences: %s\r\n", clean.Replace(r.InReplyTo), clean.Replace(r.InReplyTo))
	}
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	body := base64.StdEncoding.EncodeToString([]byte(strings.ReplaceAll(r.Text, "\n", "\r\n")))
	for len(body) > 76 {
		b.WriteString(body[:76] + "\r\n")
		body = body[76:]
	}
	b.WriteString(body + "\r\n")
	return b.Bytes()
}
