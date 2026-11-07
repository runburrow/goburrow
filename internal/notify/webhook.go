// Package notify sends alerts when issues are created or regress.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/runburrow/goburrow/internal/event"
	"github.com/runburrow/goburrow/internal/mail"
	"github.com/runburrow/goburrow/internal/store"
)

// Dispatcher POSTs a JSON payload to each of a project's webhooks. The
// payload carries both "text" (Slack, Mattermost, Rocket.Chat) and
// "content" (Discord) so it works with popular chat webhooks out of the
// box, plus structured fields for anything custom.
//
// Webhooks are set per project in its settings. FallbackURL (from
// BURROW_WEBHOOK_URL) is used only for projects that have none of their own.
type Dispatcher struct {
	Store        *store.Store
	FallbackURL  string
	BaseURL      string       // public dashboard URL for links
	AllowPrivate bool         // permit webhooks on private/local networks (see ssrf.go)
	Mail         *mail.Mailer // nil when SMTP isn't configured
	Log          *slog.Logger
	Client       *http.Client

	sem    chan struct{}
	emails *emailLimit
}

func NewDispatcher(st *store.Store, fallbackURL, baseURL string, allowPrivate bool, log *slog.Logger) *Dispatcher {
	return &Dispatcher{
		Store: st, FallbackURL: fallbackURL, BaseURL: strings.TrimRight(baseURL, "/"), AllowPrivate: allowPrivate, Log: log,
		Client: newClient(allowPrivate),
		sem:    make(chan struct{}, 8), // bound concurrent deliveries
		emails: newEmailLimit(emailsPerHour, time.Now),
	}
}

type payload struct {
	Text    string `json:"text"`
	Content string `json:"content"`
	// AllowedMentions stops Discord pinging anyone, whatever the text says.
	AllowedMentions struct {
		Parse []string `json:"parse"`
	} `json:"allowed_mentions"`
	Burrow struct {
		Kind        string `json:"kind"` // "new_issue" | "regression" | "test"
		Project     string `json:"project"`
		IssueID     int64  `json:"issue_id,omitempty"`
		Title       string `json:"title"`
		Culprit     string `json:"culprit,omitempty"`
		Level       string `json:"level,omitempty"`
		Environment string `json:"environment,omitempty"`
		Release     string `json:"release,omitempty"`
		URL         string `json:"url"`
	} `json:"burrow"`
}

// targets returns the webhook URLs and email addresses to alert for this
// kind of event in project p. The server-wide fallback webhook is only used
// when the project has no alert destinations of its own.
func (d *Dispatcher) targets(ctx context.Context, p *store.Project, regressed bool) (urls, emails []string) {
	hooks, err := d.Store.ListWebhooks(ctx, p.ID)
	if err != nil {
		d.Log.Error("list webhooks", "project", p.Slug, "err", err)
		return nil, nil
	}
	addrs, err := d.Store.ListEmailAlerts(ctx, p.ID)
	if err != nil {
		d.Log.Error("list email alerts", "project", p.Slug, "err", err)
		return nil, nil
	}
	if len(hooks) == 0 && len(addrs) == 0 {
		if d.FallbackURL != "" {
			return []string{d.FallbackURL}, nil
		}
		return nil, nil
	}
	wants := func(onNew, onRegression bool) bool { return (regressed && onRegression) || (!regressed && onNew) }
	for _, h := range hooks {
		if wants(h.OnNew, h.OnRegression) {
			urls = append(urls, h.URL)
		}
	}
	if d.Mail != nil {
		for _, a := range addrs {
			if wants(a.OnNew, a.OnRegression) {
				emails = append(emails, a.Address)
			}
		}
	}
	return urls, emails
}

func (d *Dispatcher) IssueEvent(ctx context.Context, p *store.Project, issueID int64, ev *event.Event, res *store.IngestResult) {
	urls, emails := d.targets(ctx, p, res.Regressed)
	d.emailAlert(ctx, p, issueID, ev, res, emails)
	if len(urls) == 0 {
		return
	}
	var pl payload
	pl.Burrow.Kind = "new_issue"
	verb := "New issue"
	if res.Regressed {
		pl.Burrow.Kind, verb = "regression", "Regression"
	}
	pl.Burrow.Project = p.Slug
	pl.Burrow.IssueID = issueID
	pl.Burrow.Title = ev.Title()
	pl.Burrow.Culprit = ev.Culprit()
	pl.Burrow.Level = ev.Level
	pl.Burrow.Environment = ev.Environment
	pl.Burrow.Release = ev.Release
	pl.Burrow.URL = fmt.Sprintf("%s/issues/%d", d.BaseURL, issueID)
	pl.Text = fmt.Sprintf("🦊 %s in %s: %s\n%s", verb, inert(p.Name), inert(pl.Burrow.Title), pl.Burrow.URL)
	pl.Content = pl.Text
	pl.AllowedMentions.Parse = []string{}
	body, _ := json.Marshal(pl)

	for _, u := range urls {
		select {
		case d.sem <- struct{}{}:
		default:
			d.Log.Warn("webhook backlog full, dropping alert", "issue", issueID)
			return
		}
		// Deliver in the background: ingestion latency must not depend on
		// a third-party webhook being up.
		go func(u string) {
			defer func() { <-d.sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := d.post(ctx, u, body); err != nil {
				d.Log.Warn("webhook delivery failed", "issue", issueID, "err", err)
			}
		}(u)
	}
}

// Test sends a sample alert to u right away and reports how it went, for
// the "Send test" button in project settings.
func (d *Dispatcher) Test(ctx context.Context, p *store.Project, u string) error {
	var pl payload
	pl.Burrow.Kind = "test"
	pl.Burrow.Project = p.Slug
	pl.Burrow.Title = "Test alert"
	pl.Burrow.URL = fmt.Sprintf("%s/projects/%s", d.BaseURL, p.Slug)
	pl.Text = fmt.Sprintf("🦊 Test alert from Burrow for %s. New issues and regressions will look like this.\n%s", inert(p.Name), pl.Burrow.URL)
	pl.Content = pl.Text
	pl.AllowedMentions.Parse = []string{}
	body, _ := json.Marshal(pl)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return d.post(ctx, u, body)
}

// zeroWidth breaks chat syntax without changing what people read.
const zeroWidth = "\u200b"

// inert defuses chat markup in text that came from events, which anyone
// holding a project key can send: @channel/@everyone/@here mentions (Slack,
// Mattermost, Discord), Slack's <!channel> and <url|label> forms, and
// Markdown links. The structured "burrow" fields keep the original text.
var inertReplacer = strings.NewReplacer("@", "@"+zeroWidth, "<", "<"+zeroWidth, "](", "]"+zeroWidth+"(")

func inert(s string) string { return inertReplacer.Replace(s) }

// ErrStatus is returned when the webhook answered with a non-2xx status.
type ErrStatus struct{ Code int }

func (e ErrStatus) Error() string { return fmt.Sprintf("webhook answered HTTP %d", e.Code) }

func (d *Dispatcher) post(ctx context.Context, u string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Burrow-Webhook/1")
	resp, err := d.Client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return ue.Err
		}
		return err
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrStatus{resp.StatusCode}
	}
	return nil
}
