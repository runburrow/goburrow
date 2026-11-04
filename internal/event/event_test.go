package event

import (
	"testing"
	"time"
)

var now = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func TestParseShapeVariants(t *testing.T) {
	// Older SDKs: bare exception list, tags as pairs, numeric timestamp,
	// message as string, release as number.
	ev, err := Parse([]byte(`{
		"event_id": "FC6D8C0C-43FC-4630-9A54-1BA9F2F8A9C3",
		"timestamp": 1793793600.5,
		"level": "warn",
		"release": 42,
		"message": "hello",
		"exception": [{"type": "TypeError", "value": "x is undefined"}],
		"tags": [["browser", "Firefox"], ["n", 3]],
		"breadcrumbs": [{"message": "clicked", "timestamp": "2026-11-04T11:59:00"}]
	}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if ev.EventID != "fc6d8c0c43fc46309a541ba9f2f8a9c3" {
		t.Errorf("event id not normalized: %q", ev.EventID)
	}
	if ev.Level != "warning" || ev.Release != "42" || ev.Message != "hello" {
		t.Errorf("got level=%q release=%q message=%q", ev.Level, ev.Release, ev.Message)
	}
	if got := ev.Timestamp.Unix(); got != 1793793600 {
		t.Errorf("timestamp = %d", got)
	}
	if len(ev.Tags) != 2 || ev.TagValue("n") != "3" {
		t.Errorf("tags = %+v", ev.Tags)
	}
	if len(ev.Breadcrumbs) != 1 || ev.Breadcrumbs[0].Timestamp.Minute() != 59 {
		t.Errorf("breadcrumbs = %+v", ev.Breadcrumbs)
	}
	if ev.Title() != "TypeError: x is undefined" {
		t.Errorf("title = %q", ev.Title())
	}
}

func TestParseModernShapes(t *testing.T) {
	ev, err := Parse([]byte(`{
		"timestamp": "2026-11-04T11:00:00.123Z",
		"logentry": {"message": "User %s not found", "params": ["bob"], "formatted": "User bob not found"},
		"exception": {"values": [
			{"type": "ValueError", "value": "root"},
			{"type": "RuntimeError", "value": "outer", "stacktrace": {"frames": [
				{"function": "main", "module": "app", "in_app": true},
				{"function": "helper", "module": "lib.util", "in_app": false}
			]}}
		]},
		"tags": {"b": "2", "a": "1"}
	}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Message != "User bob not found" || ev.MessageTemplate != "User %s not found" {
		t.Errorf("message=%q template=%q", ev.Message, ev.MessageTemplate)
	}
	if ev.PrimaryException().Type != "RuntimeError" {
		t.Errorf("primary should be the last (outermost) exception")
	}
	if f := ev.CrashFrame(); f == nil || f.Function != "main" {
		t.Errorf("crash frame should be innermost in-app frame, got %+v", f)
	}
	if ev.Culprit() != "app in main" {
		t.Errorf("culprit = %q", ev.Culprit())
	}
	if ev.Tags[0].Key != "a" {
		t.Errorf("map tags should be sorted: %+v", ev.Tags)
	}
	if len(ev.EventID) != 32 {
		t.Errorf("missing event id should be generated")
	}
}

func TestParseThreadStacktraceAndFutureClock(t *testing.T) {
	ev, err := Parse([]byte(`{
		"timestamp": 4102444800,
		"message": "captured with stack",
		"threads": {"values": [{"current": true, "stacktrace": {"frames": [{"function": "f", "filename": "a.go"}]}}]}
	}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if !ev.Timestamp.Equal(now) {
		t.Errorf("timestamp far in the future should clamp to received, got %v", ev.Timestamp)
	}
	if ev.Title() != "captured with stack" {
		t.Errorf("title should come from message, got %q", ev.Title())
	}
	if ev.CrashFrame() == nil || ev.CrashFrame().Function != "f" {
		t.Errorf("thread stack should be attached")
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse([]byte(`not json`), now); err == nil {
		t.Fatal("expected error")
	}
}

func TestTitleSyntheticMessageException(t *testing.T) {
	// Shape sent by @sentry/node captureMessage with attachStacktrace.
	ev, err := Parse([]byte(`{"message":"Inventory sync lagging by 87s",
		"exception":{"values":[{"value":"Inventory sync lagging by 87s","mechanism":{"type":"generic","handled":true},
		"stacktrace":{"frames":[{"filename":"app.js","function":"sync","in_app":true}]}}]}}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if got := ev.Title(); got != "Inventory sync lagging by 87s" {
		t.Errorf("title = %q", got)
	}
	other, _ := Parse([]byte(`{"message":"Inventory sync lagging by 142s",
		"exception":{"values":[{"value":"Inventory sync lagging by 142s","stacktrace":{"frames":[{"filename":"app.js","function":"sync","in_app":true}]}}]}}`), now)
	if Fingerprint(ev) != Fingerprint(other) {
		t.Error("synthetic message events should group by normalized message")
	}
}
