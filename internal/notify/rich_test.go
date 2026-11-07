package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/runburrow/goburrow/internal/mail"
	"github.com/runburrow/goburrow/internal/store"
)

func TestWebhookFormat(t *testing.T) {
	for u, want := range map[string]string{
		"https://discord.com/api/webhooks/123/abc":                  FormatDiscord,
		"https://canary.discord.com/api/webhooks/123/abc":           FormatDiscord,
		"https://discordapp.com/api/webhooks/123/abc":               FormatDiscord,
		"https://discord.com/api/webhooks/123/abc/slack":            FormatSlack,
		"https://hooks.slack.com/services/T0/B0/xyz":                FormatSlack,
		"https://chat.example.com/hooks/k3j4h5g6f7d8s9a0q1w2e3r4t5": FormatAttachments,
		"https://rocket.example.com/hooks/abc123/def456":            FormatAttachments,
		"https://example.com/my/endpoint":                           FormatJSON,
		"https://evil.example/api/webhooks/1/2":                     FormatJSON, // only Discord's own host
		"not a url":                                                 FormatJSON,
	} {
		if got := WebhookFormat(u); got != want {
			t.Errorf("WebhookFormat(%q) = %s, want %s", u, got, want)
		}
	}
}

func sampleCard(regression bool) card {
	return cardFor(mail.Alert{
		Project: "Shop", Title: "TypeError: boom @everyone <!channel>", Culprit: "cart/`summary`.ts in total",
		Level: "error", Regression: regression, Environment: "production", Release: "shop@2.5.4",
		TimesSeen: 64, Activity: []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 28}, IssueURL: "https://burrow.test/issues/3",
	}, "new_issue")
}

func TestDiscordEmbedIsBranded(t *testing.T) {
	d := &Dispatcher{BaseURL: "http://192.168.1.5:8000"}
	raw := d.richPayload(FormatDiscord, sampleCard(true))
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["username"] != "Burrow" || got["avatar_url"] != brandIcon {
		t.Errorf("should post as Burrow with the fox from goburrow.net on a LAN install: %v %v", got["username"], got["avatar_url"])
	}
	if c, _ := got["content"].(string); !strings.Contains(c, "Regression in Shop") {
		t.Errorf("content = %q", c)
	}
	if am := got["allowed_mentions"].(map[string]any)["parse"].([]any); len(am) != 0 {
		t.Errorf("allowed_mentions = %v", am)
	}
	e := got["embeds"].([]any)[0].(map[string]any)
	if e["color"].(float64) != 0xe5322d || e["url"] != "https://burrow.test/issues/3" {
		t.Errorf("regression should be red and link the issue: %v %v", e["color"], e["url"])
	}
	if e["footer"].(map[string]any)["text"] != "Burrow" || e["author"].(map[string]any)["name"] != "Shop" {
		t.Errorf("footer/author = %v / %v", e["footer"], e["author"])
	}
	if e["description"] != "`cart/'summary'.ts in total`" {
		t.Errorf("culprit should be one safe code span: %q", e["description"])
	}
	fields := e["fields"].([]any)
	if len(fields) != 4 || fields[3].(map[string]any)["value"] != "64 in all, 28 today" {
		t.Errorf("fields = %v", fields)
	}
}

func TestSlackAttachmentIsBrandedAndEscaped(t *testing.T) {
	d := &Dispatcher{BaseURL: "https://errors.example.com"}
	raw := d.richPayload(FormatSlack, sampleCard(false))
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["username"] != "Burrow" || got["icon_url"] != "https://errors.example.com/static/icon-192.png" {
		t.Errorf("a public Burrow serves its own fox: %v %v", got["username"], got["icon_url"])
	}
	a := got["attachments"].([]any)[0].(map[string]any)
	if a["color"] != "#a9a79e" || a["footer"] != "Burrow" || a["title_link"] != "https://burrow.test/issues/3" || a["author_name"] != "Burrow · Shop" {
		t.Errorf("attachment = %v", a)
	}
	for _, bad := range []string{"<!channel>", "@everyone"} {
		if strings.Contains(string(raw), bad) {
			t.Errorf("payload still contains %q", bad)
		}
	}
}

func TestTestCardExplainsItself(t *testing.T) {
	c := cardFor(mail.Alert{Project: "Shop", Title: "Error: sample", Level: "error", Test: true, IssueURL: "https://x"}, "test")
	if c.Headline != "Test alert for Shop" || !strings.Contains(c.Note, "will look like this") || c.events() != "" {
		t.Errorf("test card = %+v", c)
	}
}

// A delivery to a Mattermost-style /hooks/ URL arrives as the branded card.
func TestSendTestDeliversRichCard(t *testing.T) {
	var got []byte
	rc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
	}))
	defer rc.Close()
	d := &Dispatcher{BaseURL: "http://burrow.lan:8000", Client: newClient(true), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p := &store.Project{Name: "Shop", Slug: "shop"}
	if err := d.Test(context.Background(), p, rc.URL+"/hooks/k3j4h5g6f7d8s9a0q1w2e3r4t5"); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatal(err)
	}
	a := body["attachments"].([]any)[0].(map[string]any)
	if body["username"] != "Burrow" || a["footer"] != "Burrow" || !strings.Contains(a["text"].(string), "will look like this") {
		t.Errorf("test card = %s", got)
	}
	// A custom endpoint still gets the plain JSON with the burrow block.
	if err := d.Test(context.Background(), p, rc.URL+"/my/endpoint"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"burrow":{"kind":"test"`) || strings.Contains(string(got), "attachments") {
		t.Errorf("custom endpoint payload = %s", got)
	}
}
