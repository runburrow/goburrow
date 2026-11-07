package notify

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/runburrow/goburrow/internal/event"
	"github.com/runburrow/goburrow/internal/mail"
	"github.com/runburrow/goburrow/internal/store"
)

// emailsPerHour caps alert emails to one address. Past it, alerts for that
// address are dropped (and logged) until the hour rolls on: a storm of new
// issues shouldn't bury an inbox, and the dashboard has the full picture.
const emailsPerHour = 20

func (d *Dispatcher) emailAlert(ctx context.Context, p *store.Project, issueID int64, ev *event.Event, res *store.IngestResult, to []string) {
	if len(to) == 0 || d.Mail == nil {
		return
	}
	a, err := d.alertFor(ctx, p, issueID, ev, res.Regressed)
	if err != nil {
		d.Log.Error("email alert", "issue", issueID, "err", err)
		return
	}
	for _, addr := range to {
		if !d.emails.allow(addr) {
			d.Log.Warn("email alert dropped: hourly limit for this address", "to", addr, "issue", issueID)
			continue
		}
		select {
		case d.sem <- struct{}{}:
		default:
			d.Log.Warn("alert backlog full, dropping email", "issue", issueID)
			return
		}
		a.To = addr
		msg := mail.AlertMessage(a)
		// Sent in the background, like webhooks: ingestion never waits on
		// a mail server.
		go func() {
			defer func() { <-d.sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := d.Mail.Send(ctx, msg); err != nil {
				d.Log.Warn("email alert failed", "to", msg.To, "issue", issueID, "err", err)
			}
		}()
	}
}

// alertFor gathers what an alert email shows about an issue.
func (d *Dispatcher) alertFor(ctx context.Context, p *store.Project, issueID int64, ev *event.Event, regressed bool) (mail.Alert, error) {
	a := mail.Alert{
		Project: p.Name, Title: ev.Title(), Culprit: ev.Culprit(), Level: ev.Level, Regression: regressed,
		Environment: ev.Environment, Release: ev.Release, TimesSeen: 1,
		IssueURL:    fmt.Sprintf("%s/issues/%d", d.BaseURL, issueID),
		SettingsURL: fmt.Sprintf("%s/projects/%s/settings#email-alerts", d.BaseURL, p.Slug),
	}
	if is, err := d.Store.IssueByID(ctx, issueID); err == nil {
		a.TimesSeen = is.TimesSeen
	}
	counts, err := d.Store.DailyCounts(ctx, issueID, store.ActivityDays, time.Now())
	if err != nil {
		return a, err
	}
	a.Activity = counts
	return a, nil
}

// TestEmail sends a sample alert to addr right away, for the "Send test"
// button next to an email alert. It isn't rate-limited: an owner pressed it.
func (d *Dispatcher) TestEmail(ctx context.Context, p *store.Project, addr string) error {
	if d.Mail == nil {
		return fmt.Errorf("email isn't set up on this server")
	}
	a := mail.Alert{
		Project: p.Name, Title: "Error: this is what an alert looks like", Culprit: "sample/alert.go in example",
		Level: "error", Environment: "production", TimesSeen: 3, Activity: []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 2},
		IssueURL:    fmt.Sprintf("%s/projects/%s", d.BaseURL, p.Slug),
		SettingsURL: fmt.Sprintf("%s/projects/%s/settings#email-alerts", d.BaseURL, p.Slug),
		To:          addr, Test: true,
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return d.Mail.Send(ctx, mail.AlertMessage(a))
}

// emailLimit is a per-address sliding window.
type emailLimit struct {
	mu   sync.Mutex
	max  int
	now  func() time.Time
	sent map[string][]time.Time
}

func newEmailLimit(max int, now func() time.Time) *emailLimit {
	return &emailLimit{max: max, now: now, sent: map[string][]time.Time{}}
}

func (l *emailLimit) allow(addr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	recent := l.sent[addr][:0]
	for _, t := range l.sent[addr] {
		if now.Sub(t) < time.Hour {
			recent = append(recent, t)
		}
	}
	if len(recent) >= l.max {
		l.sent[addr] = recent
		return false
	}
	l.sent[addr] = append(recent, now)
	return true
}
