// Package mailtest is a small IMAP server for the tests of the mail, which never reach a real one.
package mailtest

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Message is a message of the fake server.
type Message struct {
	UID    uint32
	Raw    string
	Seen   bool
	Labels []string
	Box    string
}

// Server is a small IMAP server. Every command it is sent is kept, so a test can check what was and was not asked.
type Server struct {
	t        *testing.T
	ln       net.Listener
	mu       sync.Mutex
	Messages []*Message
	next     uint32
	validity uint32
	Password string
	gmail    bool
	Commands []string
	Drafts   []string
	Secured  bool     // STARTTLS was done
	Plain    bool     // LIST does not say what folders are for
	Users    []string // who LOGIN was asked for
	OnlyUser string   // the one user LOGIN accepts, when set
}

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	cert, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func New(t *testing.T, gmail bool) *Server {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{t: t, ln: ln, next: 1, validity: 7, Password: "app password", gmail: gmail}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *Server) Add(from, subject, body string) *Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := &Message{UID: s.next, Box: "INBOX", Raw: fmt.Sprintf("From: %s\r\nSubject: %s\r\nMessage-ID: <%d@x>\r\nDate: Mon, 26 Oct 2026 10:00:00 +0000\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", from, subject, s.next, body)}
	s.next++
	s.Messages = append(s.Messages, m)
	return m
}

func (s *Server) Asked(word string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.Commands {
		if strings.Contains(c, word) {
			return true
		}
	}
	return false
}

var fetchItems = regexp.MustCompile(`^UID FETCH (\S+) \((.*)\)$`)

func (s *Server) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := func(format string, a ...any) { fmt.Fprintf(c, format+"\r\n", a...) }
	w("* OK fake ready")
	box := "INBOX"
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		tag, cmd, _ := strings.Cut(line, " ")
		s.mu.Lock()
		s.Commands = append(s.Commands, cmd)
		s.mu.Unlock()
		word, rest, _ := strings.Cut(cmd, " ")
		switch word {
		case "CAPABILITY":
			caps := "IMAP4rev1 MOVE"
			if s.gmail {
				caps += " X-GM-EXT-1"
			}
			w("* CAPABILITY %s", caps)
			w("%s OK done", tag)
		case "STARTTLS":
			w("%s OK begin", tag)
			secure := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{selfSigned(s.t)}})
			if err := secure.Handshake(); err != nil {
				return
			}
			c = secure
			r = bufio.NewReader(secure)
			w = func(format string, a ...any) { fmt.Fprintf(secure, format+"\r\n", a...) }
			s.mu.Lock()
			s.Secured = true
			s.mu.Unlock()
		case "LOGIN":
			s.mu.Lock()
			s.Users = append(s.Users, rest)
			only := s.OnlyUser
			s.mu.Unlock()
			if !strings.Contains(rest, `"`+s.Password+`"`) || (only != "" && !strings.HasPrefix(rest, `"`+only+`"`)) {
				w("%s NO [AUTHENTICATIONFAILED] Invalid credentials", tag)
				continue
			}
			w("%s OK welcome", tag)
		case "LIST":
			w(`* LIST (\HasNoChildren) "/" "INBOX"`)
			if s.Plain {
				w(`* LIST (\HasNoChildren) "/" "Archive"`)
				w(`* LIST (\HasNoChildren) "/" "Drafts"`)
			} else {
				w(`* LIST (\All \HasNoChildren) "/" "Archive"`)
				w(`* LIST (\Drafts \HasNoChildren) "/" "Drafts"`)
			}
			w("%s OK done", tag)
		case "SELECT":
			box = strings.Trim(rest, `"`)
			s.mu.Lock()
			n := 0
			for _, m := range s.Messages {
				if m.Box == box || box == "Archive" {
					n++
				}
			}
			s.mu.Unlock()
			w("* %d EXISTS", n)
			w("* OK [UIDVALIDITY %d] valid", s.validity)
			w("%s OK [READ-WRITE] selected", tag)
		case "UID":
			s.uid(tag, rest, box, w)
		case "APPEND":
			open := strings.LastIndex(rest, "{")
			n, _ := strconv.Atoi(strings.TrimSuffix(rest[open+1:], "}"))
			w("+ go")
			data := make([]byte, n+2)
			if _, err := readFull(r, data); err != nil {
				return
			}
			s.mu.Lock()
			s.Drafts = append(s.Drafts, string(data[:n]))
			s.mu.Unlock()
			w("%s OK appended", tag)
		case "LOGOUT":
			w("* BYE")
			w("%s OK bye", tag)
			return
		default:
			w("%s BAD unknown", tag)
		}
	}
}

func readFull(r *bufio.Reader, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		k, err := r.Read(b[n:])
		n += k
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func (s *Server) uid(tag, rest, box string, w func(string, ...any)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	word, _, _ := strings.Cut(rest, " ")
	in := func(m *Message) bool { return m.Box == box || box == "Archive" }
	switch word {
	case "SEARCH":
		var uids []string
		for _, m := range s.Messages {
			if in(m) {
				if strings.Contains(rest, "HEADER") && !strings.Contains(rest, fmt.Sprintf("<%d@x>", m.UID)) {
					continue
				}
				uids = append(uids, strconv.Itoa(int(m.UID)))
			}
		}
		w("* SEARCH %s", strings.Join(uids, " "))
		w("%s OK done", tag)
	case "FETCH":
		f := fetchItems.FindStringSubmatch("UID " + rest)
		if f == nil {
			w("%s BAD fetch", tag)
			return
		}
		for i, m := range s.Messages {
			if !slices.Contains(strings.Split(f[1], ","), strconv.Itoa(int(m.UID))) {
				continue
			}
			flags := ""
			if m.Seen {
				flags = `\Seen`
			}
			line := fmt.Sprintf("* %d FETCH (UID %d FLAGS (%s)", i+1, m.UID, flags)
			if s.gmail {
				line += fmt.Sprintf(` X-GM-MSGID %d X-GM-THRID %d X-GM-LABELS ("\\Inbox" "%s")`, 1000+m.UID, 2000+m.UID, strings.Join(m.Labels, `" "`))
			}
			if strings.Contains(f[2], "BODY.PEEK") {
				line += fmt.Sprintf(" BODY[]<0> {%d}\r\n%s", len(m.Raw), m.Raw)
			}
			w("%s)", line)
		}
		w("%s OK done", tag)
	case "STORE":
		parts := strings.SplitN(rest, " ", 4)
		uid, _ := strconv.Atoi(parts[1])
		for _, m := range s.Messages {
			if int(m.UID) != uid {
				continue
			}
			switch {
			case parts[2] == "-X-GM-LABELS" && strings.Contains(parts[3], `Inbox`):
				m.Box = "Archive"
			case parts[2] == "+X-GM-LABELS" && strings.Contains(parts[3], `Inbox`):
				m.Box = "INBOX"
			}
		}
		w("%s OK stored", tag)
	case "MOVE":
		parts := strings.SplitN(rest, " ", 3)
		uid, _ := strconv.Atoi(parts[1])
		for _, m := range s.Messages {
			if int(m.UID) == uid {
				m.Box = strings.Trim(parts[2], `"`)
			}
		}
		w("%s OK moved", tag)
	default:
		w("%s BAD uid", tag)
	}
}

// NewPlain is a server that starts in the clear and has STARTTLS, as Proton Mail Bridge does.
func NewPlain(t *testing.T) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{t: t, ln: ln, next: 1, validity: 7, Password: "app password"}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

// Addr is the server's address.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Move puts a message in another mailbox, as another program would.
func (s *Server) Move(i int, box string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Messages[i].Box = box
}
