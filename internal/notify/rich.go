package notify

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/runburrow/goburrow/internal/mail"
)

// Chat services render rich messages when they get their own format:
// Discord embeds, and Slack-style attachments (Slack, Mattermost,
// Rocket.Chat). Burrow recognises them by the webhook URL and sends a
// branded card: posted as Burrow with the fox, a severity colour bar, the
// issue title linked, the culprit as code, and the key facts as fields.
// Anything else gets the plain JSON payload, unchanged, so custom
// endpoints keep working.

// Webhook formats.
const (
	FormatDiscord     = "discord"
	FormatSlack       = "slack"
	FormatAttachments = "attachments" // Mattermost, Rocket.Chat
	FormatJSON        = "json"
)

// brandIcon is the fox the chat services fetch for the avatar and footer.
// It has to be on the public internet, so a Burrow on a LAN uses the copy
// on goburrow.net.
const brandIcon = "https://goburrow.net/icon-192.png"

var chatHooksPath = regexp.MustCompile(`^/hooks/[A-Za-z0-9]+(/[A-Za-z0-9]+)?/?$`)

// WebhookFormat says which format Burrow sends to u.
func WebhookFormat(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return FormatJSON
	}
	host := strings.ToLower(p.Hostname())
	switch {
	case (host == "discord.com" || host == "discordapp.com" || strings.HasSuffix(host, ".discord.com")) && strings.HasPrefix(p.Path, "/api/webhooks/"):
		if strings.HasSuffix(strings.TrimRight(p.Path, "/"), "/slack") {
			return FormatSlack // Discord's Slack-compatible endpoint
		}
		return FormatDiscord
	case host == "hooks.slack.com":
		return FormatSlack
	case chatHooksPath.MatchString(p.Path):
		return FormatAttachments
	}
	return FormatJSON
}

// WebhookFormatLabel names the format for the settings page.
func WebhookFormatLabel(u string) string {
	switch WebhookFormat(u) {
	case FormatDiscord:
		return "Discord embed"
	case FormatSlack:
		return "Slack message"
	case FormatAttachments:
		return "Mattermost or Rocket.Chat message"
	}
	return "JSON"
}

// card is what every format shows about one alert.
type card struct {
	Kind        string // new_issue, regression, test
	Headline    string // "Regression · Shop": the card's first line
	Project     string
	Title       string
	Culprit     string
	Level       string
	Environment string
	Release     string
	TimesSeen   int
	Today       int
	URL         string
	Note        string // test alerts explain themselves
}

func cardFor(a mail.Alert, kind string) card {
	verb := "New issue"
	if a.Regression {
		verb, kind = "Regression", "regression"
	}
	c := card{Kind: kind, Headline: verb + " · " + a.Project, Project: a.Project, Title: a.Title, Culprit: a.Culprit,
		Level: a.Level, Environment: a.Environment, Release: a.Release, TimesSeen: a.TimesSeen, URL: a.IssueURL}
	if n := len(a.Activity); n > 0 {
		c.Today = a.Activity[n-1]
	}
	if a.Test {
		c.Kind, c.Headline = "test", "Test alert · "+a.Project
		c.Note = "This is a test. Real alerts look like this."
	}
	return c
}

// colour follows the dashboard: red only for fatal and regressions, amber
// for warnings, and the muted ink otherwise. Rust stays on the fox.
func (c card) colour() int {
	switch {
	case c.Kind == "regression" || c.Level == "fatal":
		return 0xe5322d
	case c.Level == "warning":
		return 0xe3b552
	}
	return 0xa9a79e
}

func (c card) events() string {
	if c.Kind == "test" {
		return ""
	}
	s := fmt.Sprintf("%d in all", c.TimesSeen)
	if c.Today > 0 && c.Today != c.TimesSeen {
		s += fmt.Sprintf(", %d today", c.Today)
	}
	return s
}

func (c card) titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// codeSpan wraps s in backticks for Markdown, dropping any backticks in it
// so event text can't break out of the span.
func codeSpan(s string) string {
	if s == "" {
		return ""
	}
	return "`" + strings.ReplaceAll(s, "`", "'") + "`"
}

// slackEscape escapes the three characters Slack's markup reserves, then
// defuses mentions and links (see inert).
func slackEscape(s string) string {
	return inert(strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s))
}

// iconFor is the fox the chat service should fetch: this Burrow's own copy
// when its address is public, otherwise goburrow.net's.
func iconFor(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "https" {
		return brandIcon
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if isPrivate(ip) {
			return brandIcon
		}
	} else if !strings.Contains(host, ".") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".lan") || strings.HasSuffix(host, ".internal") {
		return brandIcon
	}
	return strings.TrimRight(baseURL, "/") + "/static/icon-192.png"
}

func (d *Dispatcher) richPayload(format string, c card) []byte {
	icon := iconFor(d.BaseURL)
	type field struct {
		Name   string `json:"name"`
		Value  string `json:"value"`
		Inline bool   `json:"inline"`
	}
	var facts [][2]string
	for _, f := range [][2]string{{"Level", c.titleCase(c.Level)}, {"Environment", c.Environment}, {"Release", c.Release}, {"Events", c.events()}} {
		if f[1] != "" {
			facts = append(facts, f)
		}
	}
	switch format {
	case FormatDiscord:
		// The sender (Burrow, with the fox avatar) is the branding; the card
		// itself carries only the issue. Embeds never ping anyone, and
		// allowed_mentions makes sure of it.
		var fields []field
		for _, f := range facts {
			fields = append(fields, field{Name: f[0], Value: clip(f[1], 1024), Inline: true})
		}
		desc := codeSpan(clip(c.Culprit, 1000))
		if c.Note != "" {
			desc = strings.TrimSpace(c.Note + "\n" + desc)
		}
		body, _ := json.Marshal(map[string]any{
			"username":   "Burrow",
			"avatar_url": icon,
			"embeds": []map[string]any{{
				"author":      map[string]any{"name": clip(c.Headline, 256)},
				"title":       clip(c.Title, 256),
				"url":         c.URL,
				"description": desc,
				"color":       c.colour(),
				"fields":      fields,
				"timestamp":   time.Now().UTC().Format(time.RFC3339),
			}},
			"allowed_mentions": map[string]any{"parse": []string{}},
		})
		return body
	default: // FormatSlack, FormatAttachments: Slack-style attachments
		type sfield struct {
			Title string `json:"title"`
			Value string `json:"value"`
			Short bool   `json:"short"`
		}
		var fields []sfield
		for _, f := range facts {
			fields = append(fields, sfield{Title: f[0], Value: slackEscape(f[1]), Short: true})
		}
		text := codeSpan(slackEscape(clip(c.Culprit, 1000)))
		if c.Note != "" {
			text = strings.TrimSpace(slackEscape(c.Note) + "\n" + text)
		}
		// No top-level text: it would repeat the card. Notifications use
		// the fallback line instead.
		fallback := slackEscape(c.Headline) + ": " + slackEscape(clip(c.Title, 200))
		body, _ := json.Marshal(map[string]any{
			"username": "Burrow",
			"icon_url": icon,
			"attachments": []map[string]any{{
				"fallback":    fallback,
				"color":       fmt.Sprintf("#%06x", c.colour()),
				"author_name": slackEscape(c.Headline),
				"title":       slackEscape(clip(c.Title, 256)),
				"title_link":  c.URL,
				"text":        text,
				"fields":      fields,
				"ts":          time.Now().Unix(),
				"mrkdwn_in":   []string{"text"},
			}},
		})
		return body
	}
}
