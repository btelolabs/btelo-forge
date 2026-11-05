package mailtest

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
)

// Delivered is a letter an SMTP server took.
type Delivered struct {
	From string
	To   []string
	Data string
}

// SMTP is a small outgoing server, on this computer so that no test can reach anyone.
type SMTP struct {
	ln       net.Listener
	mu       sync.Mutex
	Password string
	Letters  []Delivered
	Refuse   string // a recipient it does not take
}

// NewSMTP starts one that takes the password "app password".
func NewSMTP(t *testing.T) *SMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &SMTP{ln: ln, Password: "app password"}
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

// Addr is where it listens.
func (s *SMTP) Addr() string { return s.ln.Addr().String() }

// Taken are the letters it took.
func (s *SMTP) Taken() []Delivered {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Delivered(nil), s.Letters...)
}

func (s *SMTP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := func(line string) { fmt.Fprintf(c, "%s\r\n", line) }
	w("220 fake ready")
	var letter Delivered
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		word, rest, _ := strings.Cut(line, " ")
		switch strings.ToUpper(word) {
		case "EHLO":
			w("250-fake")
			w("250 AUTH PLAIN")
		case "AUTH":
			plain, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(rest, "PLAIN "))
			if strings.HasSuffix(string(plain), "\x00"+s.Password) {
				w("235 welcome")
			} else {
				w("535 bad credentials")
			}
		case "MAIL":
			letter = Delivered{From: address(rest)}
			w("250 ok")
		case "RCPT":
			to := address(rest)
			if s.Refuse != "" && to == s.Refuse {
				w("550 no such user")
				continue
			}
			letter.To = append(letter.To, to)
			w("250 ok")
		case "DATA":
			w("354 go")
			var data strings.Builder
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if line == ".\r\n" {
					break
				}
				data.WriteString(strings.TrimPrefix(line, "."))
			}
			letter.Data = data.String()
			s.mu.Lock()
			s.Letters = append(s.Letters, letter)
			s.mu.Unlock()
			w("250 queued")
		case "QUIT":
			w("221 bye")
			return
		default:
			w("500 unknown")
		}
	}
}

func address(field string) string {
	from, to := strings.Index(field, "<"), strings.Index(field, ">")
	if from < 0 || to < from {
		return ""
	}
	return field[from+1 : to]
}
