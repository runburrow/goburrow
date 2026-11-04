// Package event models error events in the Sentry wire format and normalizes
// the many shape variants SDKs send into one predictable structure.
//
// Reference: https://develop.sentry.dev/sdk/data-model/event-payloads/
package event

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Event is the normalized form of an incoming event. It is what Burrow
// stores and renders; raw SDK payloads are never rendered directly.
type Event struct {
	EventID     string    `json:"event_id"`
	Timestamp   time.Time `json:"timestamp"`
	Platform    string    `json:"platform,omitempty"`
	Level       string    `json:"level"`
	Logger      string    `json:"logger,omitempty"`
	Transaction string    `json:"transaction,omitempty"`
	ServerName  string    `json:"server_name,omitempty"`
	Release     string    `json:"release,omitempty"`
	Dist        string    `json:"dist,omitempty"`
	Environment string    `json:"environment,omitempty"`
	Message     string    `json:"message,omitempty"`
	// MessageTemplate is the unformatted message (e.g. "User %s failed"),
	// used for grouping so that parameters don't split issues.
	MessageTemplate string         `json:"message_template,omitempty"`
	Exceptions      []Exception    `json:"exceptions,omitempty"`
	Breadcrumbs     []Breadcrumb   `json:"breadcrumbs,omitempty"`
	Tags            []Tag          `json:"tags,omitempty"`
	User            map[string]any `json:"user,omitempty"`
	Request         map[string]any `json:"request,omitempty"`
	Contexts        map[string]any `json:"contexts,omitempty"`
	Extra           map[string]any `json:"extra,omitempty"`
	Fingerprint     []string       `json:"fingerprint,omitempty"`
	SDK             *SDK           `json:"sdk,omitempty"`
}

type Tag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type SDK struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

type Exception struct {
	Type       string      `json:"type,omitempty"`
	Value      string      `json:"value,omitempty"`
	Module     string      `json:"module,omitempty"`
	Mechanism  *Mechanism  `json:"mechanism,omitempty"`
	Stacktrace *Stacktrace `json:"stacktrace,omitempty"`
}

type Mechanism struct {
	Type    string `json:"type,omitempty"`
	Handled *bool  `json:"handled,omitempty"`
}

type Stacktrace struct {
	// Frames are ordered oldest call first, crash site last (Sentry convention).
	Frames []Frame `json:"frames"`
}

type Frame struct {
	Filename    string         `json:"filename,omitempty"`
	AbsPath     string         `json:"abs_path,omitempty"`
	Function    string         `json:"function,omitempty"`
	Module      string         `json:"module,omitempty"`
	Package     string         `json:"package,omitempty"`
	Lineno      int            `json:"lineno,omitempty"`
	Colno       int            `json:"colno,omitempty"`
	InApp       *bool          `json:"in_app,omitempty"`
	ContextLine string         `json:"context_line,omitempty"`
	PreContext  []string       `json:"pre_context,omitempty"`
	PostContext []string       `json:"post_context,omitempty"`
	Vars        map[string]any `json:"vars,omitempty"`
}

// IsInApp reports whether the frame is explicitly marked as application code.
func (f Frame) IsInApp() bool { return f.InApp != nil && *f.InApp }

type Breadcrumb struct {
	Timestamp time.Time      `json:"timestamp,omitempty"`
	Type      string         `json:"type,omitempty"`
	Category  string         `json:"category,omitempty"`
	Message   string         `json:"message,omitempty"`
	Level     string         `json:"level,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

// raw mirrors the wire format loosely. Fields that SDKs send in more than one
// shape are kept as json.RawMessage and decoded by hand.
type raw struct {
	EventID     string          `json:"event_id"`
	Timestamp   json.RawMessage `json:"timestamp"`
	Platform    string          `json:"platform"`
	Level       string          `json:"level"`
	Logger      string          `json:"logger"`
	Transaction string          `json:"transaction"`
	ServerName  string          `json:"server_name"`
	Release     json.RawMessage `json:"release"`
	Dist        json.RawMessage `json:"dist"`
	Environment string          `json:"environment"`
	Message     json.RawMessage `json:"message"`
	LogEntry    json.RawMessage `json:"logentry"`
	Exception   json.RawMessage `json:"exception"`
	Threads     json.RawMessage `json:"threads"`
	Breadcrumbs json.RawMessage `json:"breadcrumbs"`
	Tags        json.RawMessage `json:"tags"`
	User        map[string]any  `json:"user"`
	Request     map[string]any  `json:"request"`
	Contexts    map[string]any  `json:"contexts"`
	Extra       map[string]any  `json:"extra"`
	Fingerprint []any           `json:"fingerprint"`
	SDK         *SDK            `json:"sdk"`
	Type        string          `json:"type"`
}

// Parse decodes a raw Sentry event payload and normalizes it. received is
// used as the timestamp when the event does not carry a usable one.
func Parse(payload []byte, received time.Time) (*Event, error) {
	var r raw
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("decode event: %w", err)
	}

	ev := &Event{
		EventID:     normalizeEventID(r.EventID),
		Platform:    r.Platform,
		Level:       normalizeLevel(r.Level),
		Logger:      r.Logger,
		Transaction: r.Transaction,
		ServerName:  r.ServerName,
		Release:     scalarString(r.Release),
		Dist:        scalarString(r.Dist),
		Environment: r.Environment,
		User:        r.User,
		Request:     r.Request,
		Contexts:    r.Contexts,
		Extra:       r.Extra,
		SDK:         r.SDK,
	}

	ev.Timestamp = parseTimestamp(r.Timestamp)
	if ev.Timestamp.IsZero() {
		ev.Timestamp = received
	}
	// Clamp absurd client clocks so the issue list stays sane.
	if ev.Timestamp.After(received.Add(time.Hour)) {
		ev.Timestamp = received
	}

	ev.Message, ev.MessageTemplate = parseMessage(r.LogEntry, r.Message)
	ev.Exceptions = parseExceptions(r.Exception)
	attachThreadStacktrace(ev, r.Threads)
	ev.Breadcrumbs = parseBreadcrumbs(r.Breadcrumbs)
	ev.Tags = parseTags(r.Tags)

	for _, f := range r.Fingerprint {
		ev.Fingerprint = append(ev.Fingerprint, anyToString(f))
	}

	return ev, nil
}

// Title is the human-readable issue title for this event.
func (e *Event) Title() string {
	// A type-less exception (e.g. the synthetic stack the JS SDKs attach to
	// captureMessage) never outranks the message itself.
	if ex := e.PrimaryException(); ex != nil && (ex.Type != "" || e.Message == "") && (ex.Type != "" || ex.Value != "") {
		t := ex.Type
		if t == "" {
			t = "Error"
		}
		if ex.Value != "" {
			return truncate(t+": "+firstLine(ex.Value), 200)
		}
		return truncate(t, 200)
	}
	if e.Message != "" {
		return truncate(firstLine(e.Message), 200)
	}
	return "<unlabeled event>"
}

// Culprit is a short pointer to where the error happened.
func (e *Event) Culprit() string {
	if e.Transaction != "" {
		return truncate(e.Transaction, 200)
	}
	if f := e.CrashFrame(); f != nil {
		loc := f.Module
		if loc == "" {
			loc = f.Filename
		}
		if f.Function != "" {
			if loc != "" {
				return truncate(loc+" in "+f.Function, 200)
			}
			return truncate(f.Function, 200)
		}
		return truncate(loc, 200)
	}
	return truncate(e.Logger, 200)
}

// PrimaryException returns the exception that "caused" the event. Sentry
// orders chained exceptions oldest (root cause) first, so the last one is
// the one that was actually raised.
func (e *Event) PrimaryException() *Exception {
	if len(e.Exceptions) == 0 {
		return nil
	}
	return &e.Exceptions[len(e.Exceptions)-1]
}

// CrashFrame returns the innermost in-app frame of the primary exception,
// falling back to the innermost frame overall.
func (e *Event) CrashFrame() *Frame {
	ex := e.PrimaryException()
	if ex == nil || ex.Stacktrace == nil || len(ex.Stacktrace.Frames) == 0 {
		return nil
	}
	frames := ex.Stacktrace.Frames
	for i := len(frames) - 1; i >= 0; i-- {
		if frames[i].IsInApp() {
			return &frames[i]
		}
	}
	return &frames[len(frames)-1]
}

// TagValue returns the value of tag key, or "".
func (e *Event) TagValue(key string) string {
	for _, t := range e.Tags {
		if t.Key == key {
			return t.Value
		}
	}
	return ""
}

// NewEventID returns a random 32-char hex id like the SDKs generate.
func NewEventID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func normalizeEventID(id string) string {
	id = strings.ToLower(strings.ReplaceAll(id, "-", ""))
	if len(id) != 32 {
		return NewEventID()
	}
	if _, err := hex.DecodeString(id); err != nil {
		return NewEventID()
	}
	return id
}

func normalizeLevel(l string) string {
	switch strings.ToLower(l) {
	case "fatal", "critical":
		return "fatal"
	case "warning", "warn":
		return "warning"
	case "info", "log":
		return "info"
	case "debug":
		return "debug"
	default:
		return "error"
	}
}

// parseTimestamp accepts unix seconds (number, possibly fractional) or an
// RFC 3339 string (with or without a zone; zone-less means UTC).
func parseTimestamp(r json.RawMessage) time.Time {
	r = bytes.TrimSpace(r)
	if len(r) == 0 || string(r) == "null" {
		return time.Time{}
	}
	if r[0] == '"' {
		var s string
		if json.Unmarshal(r, &s) != nil {
			return time.Time{}
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return fromUnixFloat(f)
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC()
			}
		}
		return time.Time{}
	}
	f, err := strconv.ParseFloat(string(r), 64)
	if err != nil {
		return time.Time{}
	}
	return fromUnixFloat(f)
}

func fromUnixFloat(f float64) time.Time {
	if f <= 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return time.Time{}
	}
	sec, frac := math.Modf(f)
	return time.Unix(int64(sec), int64(frac*1e9)).UTC()
}

// parseMessage handles `logentry` / `message` which may each be a plain
// string or an object {formatted, message, params}.
func parseMessage(logentry, message json.RawMessage) (formatted, template string) {
	for _, r := range []json.RawMessage{logentry, message} {
		r = bytes.TrimSpace(r)
		if len(r) == 0 || string(r) == "null" {
			continue
		}
		if r[0] == '"' {
			var s string
			if json.Unmarshal(r, &s) == nil && s != "" {
				return s, s
			}
			continue
		}
		var obj struct {
			Formatted string `json:"formatted"`
			Message   string `json:"message"`
			Params    []any  `json:"params"`
		}
		if json.Unmarshal(r, &obj) != nil {
			continue
		}
		f, t := obj.Formatted, obj.Message
		if f == "" {
			f = t
		}
		if t == "" {
			t = f
		}
		if f != "" {
			return f, t
		}
	}
	return "", ""
}

// parseExceptions handles both {"values": [...]} and bare [...] forms.
func parseExceptions(r json.RawMessage) []Exception {
	r = bytes.TrimSpace(r)
	if len(r) == 0 || string(r) == "null" {
		return nil
	}
	var list []Exception
	if r[0] == '[' {
		_ = json.Unmarshal(r, &list)
	} else {
		var wrapped struct {
			Values []Exception `json:"values"`
		}
		_ = json.Unmarshal(r, &wrapped)
		list = wrapped.Values
	}
	out := list[:0]
	for _, ex := range list {
		if ex.Type == "" && ex.Value == "" && ex.Stacktrace == nil {
			continue
		}
		out = append(out, ex)
	}
	return out
}

// attachThreadStacktrace: some SDKs (notably for messages captured with
// attach_stacktrace, and some native SDKs) put the stack on the crashing
// thread instead of the exception. Borrow it when the exception has none.
func attachThreadStacktrace(ev *Event, r json.RawMessage) {
	r = bytes.TrimSpace(r)
	if len(r) == 0 || string(r) == "null" {
		return
	}
	type thread struct {
		Crashed    bool        `json:"crashed"`
		Current    bool        `json:"current"`
		Stacktrace *Stacktrace `json:"stacktrace"`
	}
	var threads []thread
	if r[0] == '[' {
		_ = json.Unmarshal(r, &threads)
	} else {
		var wrapped struct {
			Values []thread `json:"values"`
		}
		_ = json.Unmarshal(r, &wrapped)
		threads = wrapped.Values
	}
	var pick *Stacktrace
	for _, t := range threads {
		if t.Stacktrace == nil || len(t.Stacktrace.Frames) == 0 {
			continue
		}
		if t.Crashed || t.Current || pick == nil {
			pick = t.Stacktrace
			if t.Crashed {
				break
			}
		}
	}
	if pick == nil {
		return
	}
	if ex := ev.PrimaryException(); ex != nil {
		if ex.Stacktrace == nil || len(ex.Stacktrace.Frames) == 0 {
			ex.Stacktrace = pick
		}
		return
	}
	// Message event with a thread stack: model it as a pseudo-exception-less
	// event but keep the frames for display/grouping.
	ev.Exceptions = []Exception{{Stacktrace: pick, Mechanism: &Mechanism{Type: "thread"}}}
}

func parseBreadcrumbs(r json.RawMessage) []Breadcrumb {
	r = bytes.TrimSpace(r)
	if len(r) == 0 || string(r) == "null" {
		return nil
	}
	type rawCrumb struct {
		Timestamp json.RawMessage `json:"timestamp"`
		Type      string          `json:"type"`
		Category  string          `json:"category"`
		Message   string          `json:"message"`
		Level     string          `json:"level"`
		Data      map[string]any  `json:"data"`
	}
	var list []rawCrumb
	if r[0] == '[' {
		_ = json.Unmarshal(r, &list)
	} else {
		var wrapped struct {
			Values []rawCrumb `json:"values"`
		}
		_ = json.Unmarshal(r, &wrapped)
		list = wrapped.Values
	}
	out := make([]Breadcrumb, 0, len(list))
	for _, c := range list {
		out = append(out, Breadcrumb{
			Timestamp: parseTimestamp(c.Timestamp),
			Type:      c.Type,
			Category:  c.Category,
			Message:   c.Message,
			Level:     c.Level,
			Data:      c.Data,
		})
	}
	// Keep payloads bounded; SDKs default to 100 anyway.
	if len(out) > 200 {
		out = out[len(out)-200:]
	}
	return out
}

// parseTags handles {"k": "v"} and [["k", "v"], ...] forms.
func parseTags(r json.RawMessage) []Tag {
	r = bytes.TrimSpace(r)
	if len(r) == 0 || string(r) == "null" {
		return nil
	}
	var out []Tag
	if r[0] == '[' {
		var pairs [][]any
		if json.Unmarshal(r, &pairs) == nil {
			for _, p := range pairs {
				if len(p) == 2 {
					out = append(out, Tag{Key: anyToString(p[0]), Value: anyToString(p[1])})
				}
			}
		}
	} else {
		var m map[string]any
		if json.Unmarshal(r, &m) == nil {
			for k, v := range m {
				out = append(out, Tag{Key: k, Value: anyToString(v)})
			}
			sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
		}
	}
	return out
}

func scalarString(r json.RawMessage) string {
	r = bytes.TrimSpace(r)
	if len(r) == 0 || string(r) == "null" {
		return ""
	}
	var v any
	d := json.NewDecoder(bytes.NewReader(r))
	d.UseNumber()
	if d.Decode(&v) != nil {
		return ""
	}
	return anyToString(v)
}

func anyToString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
