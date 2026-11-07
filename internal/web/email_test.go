package web

import (
	"context"
	"fmt"
	"mime"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/runburrow/goburrow/internal/event"
	"github.com/runburrow/goburrow/internal/mail"
	"github.com/runburrow/goburrow/internal/mail/mailtest"
	"github.com/runburrow/goburrow/internal/store"
)

// withMail points the harness's Burrow at a fake SMTP server.
func withMail(t *testing.T, h *harness) *mailtest.Server {
	t.Helper()
	srv := mailtest.Start(t)
	m, err := mail.New(mail.Config{Host: srv.Host, Port: srv.Port, From: "Burrow <alerts@burrow.test>", TLS: mail.TLSNone})
	if err != nil {
		t.Fatal(err)
	}
	h.srv.Notify.Mail = m
	return srv
}

func subjects(srv *mailtest.Server) []string {
	var out []string
	for _, m := range srv.Received() {
		for _, line := range strings.Split(m.Data, "\r\n") {
			if s, ok := strings.CutPrefix(line, "Subject: "); ok {
				dec, _ := new(mime.WordDecoder).DecodeHeader(s)
				out = append(out, m.To+": "+dec)
			}
		}
	}
	return out
}

func waitForMail(t *testing.T, srv *mailtest.Server, n int) []string {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if len(srv.Received()) >= n {
			break
		}
	}
	return subjects(srv)
}

func TestEmailAlertsFlow(t *testing.T) {
	ctx := context.Background()
	admin := newHarness(t)
	admin.post("/setup", url.Values{"email": {"root@x.co"}, "password": {"long-enough-pw"}, "password2": {"long-enough-pw"}, "team": {"Core"}})
	admin.get("/projects/new")
	admin.post("/projects/new", url.Values{"name": {"Shop"}, "team": {"core"}, "platform": {"go"}})
	p, _ := admin.st.ProjectBySlug(ctx, "shop")

	// Without SMTP the panel says how to turn it on, and nothing can be added.
	if _, body := admin.get("/projects/shop/settings"); !strings.Contains(body, "Email isn&#39;t set up on this server") && !strings.Contains(body, "Email isn't set up on this server") {
		t.Fatal("settings should explain email is off")
	}
	srv := withMail(t, admin)

	admin.get("/projects/shop/settings")
	if _, body := admin.post("/projects/shop/email-alerts", url.Values{"address": {"not an address"}, "on_new": {"1"}}); !strings.Contains(body, "doesn't look like an email address") {
		t.Error("invalid address accepted")
	}
	if _, body := admin.post("/projects/shop/email-alerts", url.Values{"address": {"dev@example.com"}, "on_new": {"1"}}); !strings.Contains(body, "Address added") {
		t.Fatal("address not added")
	}
	admin.post("/projects/shop/email-alerts", url.Values{"address": {"oncall@example.com"}, "on_regression": {"1"}})
	if _, body := admin.post("/projects/shop/email-alerts", url.Values{"address": {"DEV@example.com"}, "on_new": {"1"}}); !strings.Contains(body, "already gets this project") {
		t.Error("duplicate address (any case) accepted")
	}
	alerts, _ := admin.st.ListEmailAlerts(ctx, p.ID)
	if len(alerts) != 2 {
		t.Fatalf("alerts = %d", len(alerts))
	}

	// Send test: a sample alert, marked as a test.
	if _, body := admin.post(fmt.Sprintf("/projects/shop/email-alerts/%d/test", alerts[0].ID), url.Values{}); !strings.Contains(body, "Sent. It can take a minute") {
		t.Error("test send not confirmed")
	}
	if got := subjects(srv); len(got) != 1 || !strings.HasPrefix(got[0], "dev@example.com: Test alert: New issue in Shop") {
		t.Fatalf("test email = %v", got)
	}

	// A new issue emails the new-issue address only; its regression emails
	// the regression address only.
	ev := func() *event.Event {
		return &event.Event{EventID: event.NewEventID(), Level: "error", Message: "payment declined"}
	}
	admin.Ingest(p, ev())
	got := waitForMail(t, srv, 2)
	if len(got) != 2 || got[1] != "dev@example.com: New issue in Shop: payment declined" {
		t.Fatalf("after new issue: %v", got)
	}
	issues, _ := admin.st.ListIssues(ctx, p.ID, store.IssueFilter{})
	admin.st.SetIssueStatus(ctx, issues[0].ID, store.StatusResolved)
	admin.Ingest(p, ev())
	got = waitForMail(t, srv, 3)
	if len(got) != 3 || got[2] != "oncall@example.com: Regression in Shop: payment declined" {
		t.Fatalf("after regression: %v", got)
	}

	// Members see a count, not the addresses, and can't change the list.
	member := admin.as()
	member.join(admin.invite("core", "member", "bob@x.co"), "bob@x.co")
	_, body := member.get("/projects/shop/settings")
	if strings.Contains(body, "oncall@example.com") || !strings.Contains(body, "2 addresses get these alerts") {
		t.Error("member should see a count only")
	}
	if code, _ := member.post("/projects/shop/email-alerts", url.Values{"address": {"bob@x.co"}, "on_new": {"1"}}); code != 403 {
		t.Errorf("member adding an address: %d", code)
	}

	if _, body := admin.post(fmt.Sprintf("/projects/shop/email-alerts/%d/delete", alerts[1].ID), url.Values{}); !strings.Contains(body, "Address removed") {
		t.Error("remove not confirmed")
	}
}

func TestInviteEmailAndTestEmail(t *testing.T) {
	admin := newHarness(t)
	admin.post("/setup", url.Values{"email": {"root@x.co"}, "password": {"long-enough-pw"}, "password2": {"long-enough-pw"}, "team": {"Core"}})
	srv := withMail(t, admin)

	// An invite with an address is emailed too, and the link is still shown.
	admin.get("/teams/core")
	_, body := admin.post("/teams/core/invites", url.Values{"role": {"member"}, "email": {"otter@example.com"}})
	m := inviteRe.FindStringSubmatch(body)
	if m == nil || !strings.Contains(body, "Also emailed to otter@example.com.") {
		t.Fatal("invite should be shown and emailed")
	}
	got := srv.Received()
	if len(got) != 1 || got[0].To != "otter@example.com" || !strings.Contains(got[0].Data, "/invite/"+m[1]) {
		t.Fatalf("invite email: %+v", subjects(srv))
	}
	if s := subjects(srv); !strings.HasSuffix(s[0], "root@x.co invited you to Core on Burrow") {
		t.Errorf("invite subject: %v", s)
	}

	// Admins can send a test email to themselves; others can't reach it.
	if _, body := admin.get("/account"); !strings.Contains(body, "Send test email") || !strings.Contains(body, "(none), from alerts@burrow.test") {
		t.Error("account page should show the setup and the button")
	}
	if _, body := admin.post("/account/test-email", url.Values{}); !strings.Contains(body, "Sent. It can take a minute") {
		t.Error("test email not confirmed")
	}
	if s := subjects(srv); len(s) != 2 || s[1] != "root@x.co: Burrow can send email" {
		t.Errorf("test email: %v", s)
	}
	member := admin.as()
	member.join(admin.invite("core", "member", "bob@x.co"), "bob@x.co")
	if _, body := member.get("/account"); strings.Contains(body, "Send test email") {
		t.Error("only admins see the test email button")
	}
	if code, _ := member.post("/account/test-email", url.Values{}); code != 404 {
		t.Errorf("member test email: %d, want 404", code)
	}
}
