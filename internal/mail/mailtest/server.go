// Package mailtest is a minimal in-process SMTP server for tests. It speaks
// enough SMTP for net/smtp (EHLO, AUTH PLAIN, MAIL, RCPT, DATA) and records
// what it receives. It is plain text and only listens on loopback.
package mailtest

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Mail is one received message.
type Mail struct {
	From, To string
	Data     string // the raw message, headers and body
}

type Server struct {
	Host string
	Port int
	// Password, if set, is required with AUTH PLAIN; a wrong one gets 535.
	User, Password string
	// RejectRcpt makes RCPT TO answer 550 for this address.
	RejectRcpt string

	ln   net.Listener
	mu   sync.Mutex
	mail []Mail
}

// Start runs a server until the test ends.
func Start(t *testing.T) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{ln: ln, Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port}
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

// Received returns the messages so far.
func (s *Server) Received() []Mail {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Mail(nil), s.mail...)
}

func (s *Server) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	reply := func(format string, a ...any) { fmt.Fprintf(c, format+"\r\n", a...) }
	reply("220 mailtest ready")
	var cur Mail
	authed := s.Password == ""
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			reply("250-mailtest")
			reply("250 AUTH PLAIN")
		case strings.HasPrefix(cmd, "AUTH PLAIN"):
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(line[len("AUTH PLAIN"):]))
			parts := strings.Split(string(raw), "\x00")
			if len(parts) == 3 && parts[1] == s.User && parts[2] == s.Password {
				authed = true
				reply("235 ok")
			} else {
				reply("535 bad credentials")
			}
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			if !authed {
				reply("530 authentication required")
				continue
			}
			cur = Mail{From: strings.Trim(line[len("MAIL FROM:"):], "<> ")}
			reply("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			to := strings.Trim(line[len("RCPT TO:"):], "<> ")
			if to == s.RejectRcpt {
				reply("550 no such user")
				continue
			}
			cur.To = to
			reply("250 ok")
		case cmd == "DATA":
			reply("354 go ahead")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(strings.TrimPrefix(l, "."))
			}
			cur.Data = b.String()
			s.mu.Lock()
			s.mail = append(s.mail, cur)
			s.mu.Unlock()
			reply("250 queued as " + strconv.Itoa(len(s.mail)))
		case cmd == "RSET", cmd == "NOOP":
			reply("250 ok")
		case cmd == "QUIT":
			reply("221 bye")
			return
		default:
			reply("502 not implemented")
		}
	}
}
