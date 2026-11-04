// Package notify sends alerts when issues are created or regress.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/mozthewah/opensourcedsentryalternative/internal/event"
	"github.com/mozthewah/opensourcedsentryalternative/internal/store"
)

// Webhook POSTs a JSON payload to a URL. The payload carries both "text"
// (Slack, Mattermost, Rocket.Chat) and "content" (Discord) so it works with
// popular chat webhooks out of the box, plus structured fields for anything
// custom.
type Webhook struct {
	URL     string
	BaseURL string // public dashboard URL for links
	Log     *slog.Logger
	Client  *http.Client

	sem chan struct{}
}

func NewWebhook(url, baseURL string, log *slog.Logger) *Webhook {
	return &Webhook{
		URL: url, BaseURL: strings.TrimRight(baseURL, "/"), Log: log,
		Client: &http.Client{Timeout: 10 * time.Second},
		sem:    make(chan struct{}, 4), // bound concurrent deliveries
	}
}

type payload struct {
	Text    string `json:"text"`
	Content string `json:"content"`
	Burrow  struct {
		Kind        string `json:"kind"` // "new_issue" | "regression"
		Project     string `json:"project"`
		IssueID     int64  `json:"issue_id"`
		Title       string `json:"title"`
		Culprit     string `json:"culprit"`
		Level       string `json:"level"`
		Environment string `json:"environment,omitempty"`
		Release     string `json:"release,omitempty"`
		URL         string `json:"url"`
	} `json:"burrow"`
}

func (w *Webhook) IssueEvent(_ context.Context, p *store.Project, issueID int64, ev *event.Event, res *store.IngestResult) {
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
	pl.Burrow.URL = fmt.Sprintf("%s/issues/%d", w.BaseURL, issueID)
	pl.Text = fmt.Sprintf("🦡 %s in %s: %s\n%s", verb, p.Name, pl.Burrow.Title, pl.Burrow.URL)
	pl.Content = pl.Text

	body, _ := json.Marshal(pl)
	select {
	case w.sem <- struct{}{}:
	default:
		w.Log.Warn("webhook backlog full, dropping alert", "issue", issueID)
		return
	}
	// Deliver in the background: ingestion latency must not depend on a
	// third-party webhook being up.
	go func() {
		defer func() { <-w.sem }()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
		if err != nil {
			w.Log.Error("webhook request", "err", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Burrow-Webhook/1")
		resp, err := w.Client.Do(req)
		if err != nil {
			w.Log.Warn("webhook delivery failed", "issue", issueID, "err", err)
			return
		}
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			w.Log.Warn("webhook non-2xx", "issue", issueID, "status", resp.StatusCode)
		}
	}()
}
