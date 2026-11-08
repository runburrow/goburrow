package mail

import (
	"bytes"
	"fmt"
	htmltemplate "html/template"
	"strings"
	texttemplate "text/template"
	"time"
)

// The emails share the dashboard's look: a dark header band with the fox
// mark and wordmark, a warm-stone page, ink text, mono for code and
// figures, and the 14-day activity drawn as hairline bars. Layout is
// tables and inline styles so every client renders it; clients that
// support it switch to the dark palette.

// Alert is a new issue or a regression.
type Alert struct {
	Project     string
	Title       string
	Culprit     string
	Level       string // fatal, error, warning, info, debug
	Regression  bool
	Environment string
	Release     string
	TimesSeen   int
	Activity    []int // 14 daily counts, oldest first
	IssueURL    string
	SettingsURL string // where the recipient's alert is managed
	To          string
	Test        bool // a "Send test" from settings: says so at the top
}

// Invite is a team invite link.
type Invite struct {
	Team      string
	Role      string
	InvitedBy string
	URL       string
	Expires   time.Time
}

// Check is the "Send test email" message that proves SMTP works.
type Check struct {
	Server string // the dashboard's base URL
	Setup  string // Mailer.Describe()
	To     string
}

// AlertMessage renders an alert email.
func AlertMessage(a Alert) Message {
	verb := "New issue"
	if a.Regression {
		verb = "Regression"
	}
	subject := fmt.Sprintf("%s in %s: %s", verb, a.Project, a.Title)
	if a.Test {
		subject = "Test alert: " + subject
	}
	peak, total := 0, 0
	for _, n := range a.Activity {
		total += n
		if n > peak {
			peak = n
		}
	}
	bars := make([]int, len(a.Activity))
	for i, n := range a.Activity {
		if n > 0 {
			bars[i] = max(3, n*32/max(peak, 1))
		}
	}
	data := map[string]any{"A": a, "Verb": verb, "Bars": bars, "Total": total, "LevelColor": levelColor(a.Level), "LevelClass": levelClass(a.Level)}
	return Message{To: a.To, Subject: subject, Text: render(alertText, data), HTML: renderHTML(alertHTML, verb+" in "+a.Project, data), Automatic: true}
}

// InviteMessage renders a team invite email.
func InviteMessage(to string, in Invite) Message {
	data := map[string]any{"I": in, "Expires": in.Expires.UTC().Format("Monday 2 January at 15:04 UTC"), "Article": article(in.Role)}
	return Message{To: to, Subject: fmt.Sprintf("%s invited you to %s on Burrow", in.InvitedBy, in.Team), Text: render(inviteText, data), HTML: renderHTML(inviteHTML, "Join "+in.Team, data)}
}

// CheckMessage renders the SMTP test email.
func CheckMessage(c Check) Message {
	return Message{To: c.To, Subject: "Burrow can send email", Text: render(checkText, map[string]any{"C": c}), HTML: renderHTML(checkHTML, "Burrow can send email", map[string]any{"C": c})}
}

func article(role string) string {
	if role == "owner" {
		return "an admin"
	}
	return "a member"
}

// levelClass names the dark-mode colour for a level (see the layout's
// stylesheet): severity keeps its hue, plain levels follow the ink.
func levelClass(level string) string {
	switch level {
	case "fatal":
		return "red"
	case "warning":
		return "warn"
	case "info", "debug":
		return "muted"
	}
	return "ink"
}

func levelColor(level string) string {
	switch level {
	case "fatal":
		return "#bc2621"
	case "warning":
		return "#7d5200"
	case "info", "debug":
		return "#58564f"
	}
	return "#1a1916"
}

var funcs = htmltemplate.FuncMap{
	"title": func(s string) string {
		if s == "" {
			return s
		}
		return strings.ToUpper(s[:1]) + s[1:]
	},
	// dict builds the argument for the shared button template.
	"dict": func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	},
}

func render(t *texttemplate.Template, data any) string {
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return "Burrow couldn't render this email: " + err.Error()
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func renderHTML(t *htmltemplate.Template, preheader string, data map[string]any) string {
	data["Preheader"] = preheader
	var b bytes.Buffer
	if err := t.ExecuteTemplate(&b, "layout", data); err != nil {
		return "" // the plain-text part still goes out
	}
	return b.String()
}

// ------------------------------------------------------------- templates

const layoutHTML = `{{define "layout"}}<!doctype html>
<html lang="en"><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark"><meta name="supported-color-schemes" content="light dark">
<title>{{.Preheader}}</title>
<style>
  a { color: #1a1916; }
  @media (prefers-color-scheme: dark) {
    .page { background: #0b0b0a !important; }
    .card { background: #121211 !important; border-color: #2f2f2b !important; }
    .ink, .ink a { color: #f2efe6 !important; }
    .muted { color: #a9a79e !important; }
    .rule { border-color: #2f2f2b !important; }
    .code { background: #1c1c1a !important; color: #f2efe6 !important; }
    .bar { background: #a9a79e !important; }
    .btn { background: #f2efe6 !important; }
    .btn a { color: #0b0b0a !important; }
    .band { border: 1px solid #2f2f2b !important; border-bottom: 0 !important; }
    .sq-ink { background: #f2efe6 !important; }
    .sq-red { background: #e5322d !important; }
    .sq-warn { background: #e3b552 !important; }
    .sq-muted { background: #a9a79e !important; }
    .red { color: #ff7a73 !important; border-color: #e5322d !important; }
    .warn { color: #e3b552 !important; }
  }
</style>
</head>
<body class="page" style="margin:0;padding:0;background:#f2f1ee;">
<div style="display:none;max-height:0;overflow:hidden;opacity:0;">{{.Preheader}}</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" class="page" style="background:#f2f1ee;">
<tr><td align="center" style="padding:24px 12px;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:560px;">
  <tr><td class="band" style="background:#0b0b0a;border-radius:6px 6px 0 0;padding:14px 22px;">
    <table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr>
      <td style="vertical-align:middle;"><img src="cid:fox" width="32" height="32" alt="" style="display:block;border:0;"></td>
      <td style="vertical-align:middle;padding-left:10px;font:500 20px/1 'IBM Plex Mono',ui-monospace,Menlo,Consolas,monospace;color:#f2efe6;letter-spacing:-.03em;">burrow</td>
    </tr></table>
  </td></tr>
  <tr><td class="card" style="background:#faf9f6;border:1px solid #d5d2ca;border-top:0;border-radius:0 0 6px 6px;padding:26px 22px 24px;font:15px/1.55 system-ui,-apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#1a1916;">
    {{template "body" .}}
  </td></tr>
  <tr><td class="muted" style="padding:16px 4px 0;font:12px/1.5 system-ui,-apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#58564f;">
    {{template "footer" .}}
  </td></tr>
</table>
</td></tr></table>
</body></html>{{end}}`

const button = `{{define "button"}}<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="margin-top:22px;"><tr>
<td class="btn" style="background:#1a1916;border-radius:4px;"><a href="{{.URL}}" style="display:inline-block;padding:11px 18px;font:600 15px/1.2 system-ui,-apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#f2f1ee;text-decoration:none;">{{.Label}}</a></td>
</tr></table>{{end}}`

var alertHTML = htmltemplate.Must(htmltemplate.New("alert").Funcs(funcs).Parse(layoutHTML + button + `
{{define "body"}}{{$a := .A}}
{{if $a.Test}}<p class="muted" style="margin:0 0 18px;padding:10px 12px;border:1px dashed #86847e;border-radius:4px;color:#58564f;font-size:13px;">This is a test from Burrow's project settings. Real alerts for {{$a.Project}} look like this.</p>{{end}}
<p style="margin:0 0 10px;font-size:13px;">
  <span class="sq-{{.LevelClass}}" style="display:inline-block;width:9px;height:9px;background:{{.LevelColor}};vertical-align:0;margin-right:6px;"></span><strong class="{{.LevelClass}}" style="color:{{.LevelColor}};">{{title $a.Level}}</strong>
  {{if $a.Regression}}<span class="red" style="display:inline-block;margin-left:8px;padding:0 7px;border:1px solid #bc2621;border-radius:4px;color:#a81d19;font-weight:600;font-size:12px;line-height:1.6;">Regression</span>{{end}}
</p>
<h1 class="ink" style="margin:0;font-size:21px;line-height:1.3;font-weight:650;letter-spacing:-.01em;color:#1a1916;word-break:break-word;">{{$a.Title}}</h1>
{{if $a.Culprit}}<p class="muted" style="margin:6px 0 0;font:13px/1.5 'IBM Plex Mono',ui-monospace,Menlo,Consolas,monospace;color:#58564f;word-break:break-all;">{{$a.Culprit}}</p>{{end}}
<p class="muted" style="margin:14px 0 0;font-size:13px;color:#58564f;">{{.Verb}} in <strong class="ink" style="color:#1a1916;">{{$a.Project}}</strong>{{with $a.Environment}} · {{.}}{{end}}{{with $a.Release}} · <span style="font-family:'IBM Plex Mono',ui-monospace,Menlo,Consolas,monospace;">{{.}}</span>{{end}}</p>

<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" class="rule" style="margin-top:20px;border-top:1px solid #d5d2ca;"><tr>
  <td style="padding-top:14px;vertical-align:bottom;">
    <table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr>
      {{range .Bars}}<td style="width:7px;height:34px;vertical-align:bottom;padding-right:3px;">{{if .}}<div class="bar" style="width:2px;height:{{.}}px;background:#58564f;font-size:0;line-height:0;">&nbsp;</div>{{else}}<div class="bar" style="width:2px;height:1px;background:#d5d2ca;font-size:0;line-height:0;">&nbsp;</div>{{end}}</td>{{end}}
    </tr></table>
    <p class="muted" style="margin:6px 0 0;font-size:12px;color:#58564f;">Last 14 days</p>
  </td>
  <td align="right" style="padding-top:14px;vertical-align:bottom;">
    <span class="ink" style="font:500 22px/1 'IBM Plex Mono',ui-monospace,Menlo,Consolas,monospace;color:#1a1916;">{{$a.TimesSeen}}</span>
    <p class="muted" style="margin:6px 0 0;font-size:12px;color:#58564f;">{{if eq $a.TimesSeen 1}}event{{else}}events{{end}} in all</p>
  </td>
</tr></table>
{{template "button" (dict "URL" $a.IssueURL "Label" "Open the issue")}}
{{end}}
{{define "footer"}}You're getting this because {{.A.To}} is on the email alerts for {{.A.Project}}. <a class="muted" href="{{.A.SettingsURL}}" style="color:#58564f;">Change or remove it</a>.{{end}}
`))

var inviteHTML = htmltemplate.Must(htmltemplate.New("invite").Funcs(funcs).Parse(layoutHTML + button + `
{{define "body"}}{{$i := .I}}
<h1 class="ink" style="margin:0;font-size:21px;line-height:1.3;font-weight:650;color:#1a1916;">Join {{$i.Team}} on Burrow</h1>
<p class="ink" style="margin:12px 0 0;color:#1a1916;">{{$i.InvitedBy}} invited you to the team as {{.Article}}. You'll see its projects and issues, and get to work on them.</p>
<p class="muted" style="margin:10px 0 0;font-size:13px;color:#58564f;">The link works once and expires {{.Expires}}.</p>
{{template "button" (dict "URL" $i.URL "Label" "Accept the invite")}}
<p class="muted" style="margin:18px 0 0;font-size:12px;color:#58564f;word-break:break-all;">Or paste this into your browser: <span style="font-family:'IBM Plex Mono',ui-monospace,Menlo,Consolas,monospace;">{{$i.URL}}</span></p>
{{end}}
{{define "footer"}}Not expecting this? You can ignore it; the link expires on its own.{{end}}
`))

var checkHTML = htmltemplate.Must(htmltemplate.New("check").Funcs(funcs).Parse(layoutHTML + button + `
{{define "body"}}{{$c := .C}}
<h1 class="ink" style="margin:0;font-size:21px;line-height:1.3;font-weight:650;color:#1a1916;">Burrow can send email</h1>
<p class="ink" style="margin:12px 0 0;color:#1a1916;">If you're reading this, email alerts and invites will arrive too.</p>
<p class="code" style="margin:16px 0 0;padding:10px 12px;border-radius:4px;background:#e6e3dc;font:13px/1.5 'IBM Plex Mono',ui-monospace,Menlo,Consolas,monospace;color:#1a1916;word-break:break-all;">{{$c.Setup}}</p>
{{template "button" (dict "URL" $c.Server "Label" "Open Burrow")}}
{{end}}
{{define "footer"}}Sent by the Burrow at {{.C.Server}} because an admin pressed Send test email.{{end}}
`))

var alertText = texttemplate.Must(texttemplate.New("alert").Parse(`{{$a := .A}}{{if $a.Test}}This is a test from Burrow's project settings. Real alerts for {{$a.Project}} look like this.

{{end}}{{.Verb}} in {{$a.Project}}{{if $a.Regression}} (it was resolved, and happened again){{end}}

{{$a.Title}}
{{with $a.Culprit}}{{.}}
{{end}}
Level: {{$a.Level}}{{with $a.Environment}} · environment: {{.}}{{end}}{{with $a.Release}} · release: {{.}}{{end}}
Events: {{$a.TimesSeen}} in all, {{.Total}} in the last 14 days

Open the issue: {{$a.IssueURL}}

--
You're getting this because {{$a.To}} is on the email alerts for {{$a.Project}}.
Change or remove it: {{$a.SettingsURL}}
`))

var inviteText = texttemplate.Must(texttemplate.New("invite").Parse(`{{$i := .I}}{{$i.InvitedBy}} invited you to join {{$i.Team}} on Burrow as {{.Article}}.

Accept the invite: {{$i.URL}}

The link works once and expires {{.Expires}}.
Not expecting this? You can ignore it; the link expires on its own.
`))

var checkText = texttemplate.Must(texttemplate.New("check").Parse(`Burrow can send email.

If you're reading this, email alerts and invites will arrive too.

Setup: {{.C.Setup}}
Burrow: {{.C.Server}}

Sent because an admin pressed Send test email.
`))
