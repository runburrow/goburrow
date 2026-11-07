package mail_test

import (
	"context"
	"io"
	stdmail "net/mail"
	"strings"
	"testing"
	"time"

	"mime"
	"mime/multipart"

	"github.com/runburrow/goburrow/internal/mail"
	"github.com/runburrow/goburrow/internal/mail/mailtest"
)

func mailer(t *testing.T, srv *mailtest.Server, user, pw string) *mail.Mailer {
	t.Helper()
	m, err := mail.New(mail.Config{Host: srv.Host, Port: srv.Port, Username: user, Password: pw, From: "alerts@burrow.test", TLS: mail.TLSNone})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSendAlertIsMultipartWithInlineFox(t *testing.T) {
	srv := mailtest.Start(t)
	srv.User, srv.Password = "burrow", "s3cret"
	m := mailer(t, srv, "burrow", "s3cret")
	msg := mail.AlertMessage(mail.Alert{
		Project: "Checkout", Title: "TypeError: Cannot read 'total'", Culprit: "cart/summary.ts in computeTotals",
		Level: "error", Regression: true, Environment: "production", Release: "shop@2.5.4", TimesSeen: 52,
		Activity: []int{0, 0, 1, 3, 0, 0, 9, 4, 0, 0, 0, 2, 5, 28}, IssueURL: "https://burrow.test/issues/3",
		SettingsURL: "https://burrow.test/projects/checkout/settings#email-alerts", To: "dev@example.com",
	})
	if err := m.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	got := srv.Received()
	if len(got) != 1 || got[0].To != "dev@example.com" || got[0].From != "alerts@burrow.test" {
		t.Fatalf("received %+v", got)
	}
	parsed, err := stdmail.ReadMessage(strings.NewReader(got[0].Data))
	if err != nil {
		t.Fatal(err)
	}
	subject, _ := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	if subject != "Regression in Checkout: TypeError: Cannot read 'total'" {
		t.Errorf("subject = %q", subject)
	}
	if parsed.Header.Get("Auto-Submitted") != "auto-generated" {
		t.Error("alerts should be marked auto-generated so autoresponders stay quiet")
	}
	// multipart/related → (multipart/alternative: text, html) + fox PNG
	_, params, _ := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	mr := multipart.NewReader(parsed.Body, params["boundary"])
	var types []string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		types = append(types, p.Header.Get("Content-Type"))
		if strings.HasPrefix(p.Header.Get("Content-Type"), "multipart/alternative") {
			_, ap, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
			ar := multipart.NewReader(p, ap["boundary"])
			for {
				q, err := ar.NextPart()
				if err == io.EOF {
					break
				}
				b, _ := io.ReadAll(q) // quoted-printable is decoded by the reader
				types = append(types, q.Header.Get("Content-Type"))
				if strings.HasPrefix(q.Header.Get("Content-Type"), "text/html") {
					for _, want := range []string{"cid:fox", "Regression", "cart/summary.ts in computeTotals", "https://burrow.test/issues/3", "prefers-color-scheme: dark"} {
						if !strings.Contains(string(b), want) {
							t.Errorf("html part missing %q", want)
						}
					}
				}
			}
		}
	}
	if len(types) != 4 || !strings.HasPrefix(types[0], "multipart/alternative") ||
		types[1] != "text/plain; charset=utf-8" || types[2] != "text/html; charset=utf-8" || types[3] != "image/png" {
		t.Errorf("parts = %v, want alternative(text, html) + png", types)
	}
}

func TestHeaderInjectionAndAddresses(t *testing.T) {
	srv := mailtest.Start(t)
	m := mailer(t, srv, "", "")
	msg := mail.Message{To: "dev@example.com", Subject: "boom\r\nBcc: victim@example.com", Text: "x"}
	if err := m.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if data := srv.Received()[0].Data; strings.Contains(data, "\r\nBcc:") {
		t.Fatal("subject newline became a header")
	}
	for _, bad := range []string{"a@b.co\r\nBcc: x@y.z", "Name <a@b.co>", "a@b.co, c@d.co", "nope", ""} {
		if mail.ValidAddress(bad) {
			t.Errorf("ValidAddress(%q) = true", bad)
		}
		if err := m.Send(context.Background(), mail.Message{To: bad, Subject: "x", Text: "x"}); err == nil {
			t.Errorf("sent to %q", bad)
		}
	}
	if !mail.ValidAddress("dev+alerts@example.com") {
		t.Error("plus addressing should be valid")
	}
}

func TestErrorReasons(t *testing.T) {
	srv := mailtest.Start(t)
	srv.User, srv.Password = "burrow", "right"
	srv.RejectRcpt = "gone@example.com"
	ctx := context.Background()
	if code, _ := mail.ErrorReason(mailer(t, srv, "burrow", "wrong").Send(ctx, mail.Message{To: "a@b.co", Subject: "x", Text: "x"})); code != "auth" {
		t.Errorf("wrong password: %s", code)
	}
	if code, _ := mail.ErrorReason(mailer(t, srv, "burrow", "right").Send(ctx, mail.Message{To: "gone@example.com", Subject: "x", Text: "x"})); code != "rejected" {
		t.Errorf("rejected recipient: %s", code)
	}
	dead, _ := mail.New(mail.Config{Host: "127.0.0.1", Port: 1, From: "a@b.co", TLS: mail.TLSNone})
	ctx2, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if code, _ := mail.ErrorReason(dead.Check(ctx2)); code != "unreachable" {
		t.Errorf("closed port: %s", code)
	}
	// STARTTLS is required by default; the test server doesn't offer it.
	strict, _ := mail.New(mail.Config{Host: srv.Host, Port: srv.Port, From: "a@b.co"})
	if code, _ := mail.ErrorReason(strict.Check(ctx)); code != "tls" {
		t.Errorf("no STARTTLS offered: %s", code)
	}
}

func TestNewValidates(t *testing.T) {
	for _, c := range []mail.Config{{}, {Host: "smtp.example.com"}, {Host: "smtp.example.com", From: "not an address"}, {Host: "smtp.example.com", From: "a@b.co", TLS: "ssl"}} {
		if _, err := mail.New(c); err == nil {
			t.Errorf("New(%+v) accepted", c)
		}
	}
	m, err := mail.New(mail.Config{Host: "smtp.example.com", Port: 465, From: "a@b.co"})
	if err != nil || !strings.Contains(m.Describe(), "(tls)") {
		t.Errorf("port 465 should default to implicit TLS: %v %v", err, m)
	}
}
