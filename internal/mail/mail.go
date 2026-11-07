// Package mail sends Burrow's email over SMTP: alert emails, team invites
// and test messages. Every message is multipart (plain text and HTML, with
// the fox mark inlined), so it reads well in any client.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// TLS modes for the connection to the SMTP server.
const (
	TLSImplicit = "tls"      // TLS from the first byte, usually port 465
	TLSStart    = "starttls" // upgrade a plain connection, usually port 587; required
	TLSNone     = "none"     // no encryption: only for a relay on this machine
)

// Config is the SMTP server Burrow sends through.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string // "Burrow <burrow@example.com>" or a bare address
	TLS      string // TLSImplicit, TLSStart or TLSNone; empty picks by port
}

// Mailer sends messages through one SMTP server.
type Mailer struct {
	cfg  Config
	from *mail.Address
}

// New checks cfg and returns a Mailer. It doesn't connect; see Check.
func New(cfg Config) (*Mailer, error) {
	if cfg.Host == "" {
		return nil, errors.New("SMTP host is required")
	}
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	if cfg.TLS == "" {
		cfg.TLS = TLSStart
		if cfg.Port == 465 {
			cfg.TLS = TLSImplicit
		}
	}
	switch cfg.TLS {
	case TLSImplicit, TLSStart, TLSNone:
	default:
		return nil, fmt.Errorf("SMTP TLS mode %q: use tls, starttls or none", cfg.TLS)
	}
	if cfg.From == "" {
		return nil, errors.New("a from address is required (BURROW_SMTP_FROM)")
	}
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("from address %q: %w", cfg.From, err)
	}
	if from.Name == "" {
		from.Name = "Burrow"
	}
	return &Mailer{cfg: cfg, from: from}, nil
}

// Describe is a one-line summary of the setup for the UI, without secrets.
func (m *Mailer) Describe() string {
	return fmt.Sprintf("%s:%d (%s), from %s", m.cfg.Host, m.cfg.Port, m.cfg.TLS, m.from.Address)
}

// Message is one email. Text is required; HTML is optional.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
	// Alerts are automatic: they ask other servers not to auto-reply, so
	// an out-of-office can't loop with Burrow.
	Automatic bool
}

// ValidAddress reports whether s is a single, plain email address.
func ValidAddress(s string) bool {
	if strings.ContainsAny(s, "\r\n<>,;\"") || len(s) > 254 {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && strings.Contains(s, "@")
}

// Send delivers msg. It honours ctx's deadline for the whole exchange.
func (m *Mailer) Send(ctx context.Context, msg Message) error {
	if !ValidAddress(msg.To) {
		return fmt.Errorf("not a valid email address: %q", msg.To)
	}
	raw, err := m.build(msg)
	if err != nil {
		return err
	}
	c, err := m.dial(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Mail(m.from.Address); err != nil {
		return err
	}
	if err := c.Rcpt(msg.To); err != nil {
		return err
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

// Check connects and logs in without sending anything, so a bad setup
// shows up at startup rather than with the first alert.
func (m *Mailer) Check(ctx context.Context) error {
	c, err := m.dial(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Quit()
}

func (m *Mailer) dial(ctx context.Context) (*smtp.Client, error) {
	addr := net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port))
	d := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var err error
	tlsCfg := &tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12}
	if m.cfg.TLS == TLSImplicit {
		conn, err = (&tls.Dialer{NetDialer: d, Config: tlsCfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}
	_ = conn.SetDeadline(deadline)
	c, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := c.Hello(helloName(m.from.Address)); err != nil {
		c.Close()
		return nil, err
	}
	if m.cfg.TLS == TLSStart {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			c.Close()
			return nil, errors.New("the SMTP server doesn't offer STARTTLS; set BURROW_SMTP_TLS=tls for port 465, or none for a local relay")
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			c.Close()
			return nil, err
		}
	}
	if m.cfg.Username != "" {
		// net/smtp refuses PLAIN auth over an unencrypted connection to
		// anything but localhost, so a password is never sent in the clear.
		if err := c.Auth(smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)); err != nil {
			c.Close()
			return nil, err
		}
	}
	return c, nil
}

// helloName is the name Burrow gives in EHLO: the from address's domain.
func helloName(from string) string {
	if _, domain, ok := strings.Cut(from, "@"); ok && domain != "" {
		return domain
	}
	return "localhost"
}

// noCRLF keeps header values on one line (no header injection).
func noCRLF(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(s)), " ")
}

func (m *Mailer) build(msg Message) ([]byte, error) {
	var b bytes.Buffer
	h := textproto.MIMEHeader{}
	h.Set("From", m.from.String())
	h.Set("To", msg.To)
	h.Set("Subject", mime.QEncoding.Encode("utf-8", noCRLF(msg.Subject)))
	h.Set("Date", time.Now().Format(time.RFC1123Z))
	h.Set("Message-ID", fmt.Sprintf("<%s@%s>", randomID(), helloName(m.from.Address)))
	h.Set("MIME-Version", "1.0")
	if msg.Automatic {
		h.Set("Auto-Submitted", "auto-generated")
		h.Set("X-Auto-Response-Suppress", "All")
	}

	var body bytes.Buffer
	if msg.HTML == "" {
		h.Set("Content-Type", "text/plain; charset=utf-8")
		h.Set("Content-Transfer-Encoding", "quoted-printable")
		if err := writeQP(&body, msg.Text); err != nil {
			return nil, err
		}
	} else {
		// multipart/related wraps the alternatives and the inline fox mark,
		// which the HTML references as cid:fox.
		related := multipart.NewWriter(&body)
		h.Set("Content-Type", `multipart/related; type="multipart/alternative"; boundary=`+related.Boundary())
		altBuf := &bytes.Buffer{}
		alt := multipart.NewWriter(altBuf)
		for _, part := range []struct{ ctype, content string }{{"text/plain", msg.Text}, {"text/html", msg.HTML}} {
			pw, err := alt.CreatePart(textproto.MIMEHeader{
				"Content-Type":              {part.ctype + "; charset=utf-8"},
				"Content-Transfer-Encoding": {"quoted-printable"},
			})
			if err != nil {
				return nil, err
			}
			if err := writeQP(pw, part.content); err != nil {
				return nil, err
			}
		}
		alt.Close()
		pw, err := related.CreatePart(textproto.MIMEHeader{"Content-Type": {"multipart/alternative; boundary=" + alt.Boundary()}})
		if err != nil {
			return nil, err
		}
		pw.Write(altBuf.Bytes())
		img, err := related.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {"image/png"},
			"Content-Transfer-Encoding": {"base64"},
			"Content-ID":                {"<fox>"},
			"Content-Disposition":       {`inline; filename="burrow.png"`},
		})
		if err != nil {
			return nil, err
		}
		writeBase64(img, foxPNG)
		related.Close()
	}

	for _, k := range []string{"From", "To", "Subject", "Date", "Message-ID", "MIME-Version", "Auto-Submitted", "X-Auto-Response-Suppress", "Content-Type", "Content-Transfer-Encoding"} {
		if v := h.Get(k); v != "" {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	b.WriteString("\r\n")
	b.Write(body.Bytes())
	return b.Bytes(), nil
}

func writeQP(w interface{ Write([]byte) (int, error) }, s string) error {
	qp := quotedprintable.NewWriter(w)
	if _, err := qp.Write([]byte(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n"))); err != nil {
		return err
	}
	return qp.Close()
}

func writeBase64(w interface{ Write([]byte) (int, error) }, data []byte) {
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 76 {
		w.Write([]byte(enc[:76] + "\r\n"))
		enc = enc[76:]
	}
	w.Write([]byte(enc + "\r\n"))
}

func randomID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// foxPNG is the fox mark for dark backgrounds, small enough to inline in
// every message (the header band is always dark).
//
//go:embed fox-mark.png
var foxPNG []byte

// ErrorReason turns a send error into a short reason for the UI and a
// stable code for redirects.
func ErrorReason(err error) (code, text string) {
	var tp *textproto.Error
	var ne net.Error
	switch {
	case err == nil:
		return "ok", ""
	case errors.As(err, &tp) && (tp.Code == 535 || tp.Code == 534 || tp.Code == 530):
		return "auth", "The SMTP server refused the username or password."
	case errors.As(err, &tp) && tp.Code >= 550 && tp.Code < 560:
		return "rejected", "The SMTP server refused that recipient."
	case errors.As(err, &tp):
		return "smtp", fmt.Sprintf("The SMTP server answered %d.", tp.Code)
	case errors.As(err, &ne) && ne.Timeout(), errors.Is(err, context.DeadlineExceeded):
		return "timeout", "The SMTP server didn't answer in time."
	case strings.Contains(err.Error(), "STARTTLS"):
		return "tls", "The SMTP server doesn't offer STARTTLS. Set BURROW_SMTP_TLS to match it."
	case strings.Contains(strings.ToLower(err.Error()), "certificate"), strings.Contains(err.Error(), "tls:"):
		return "tls", "The TLS connection to the SMTP server failed. Check BURROW_SMTP_TLS and the port."
	}
	return "unreachable", "Couldn't reach the SMTP server. Check the host and port."
}
