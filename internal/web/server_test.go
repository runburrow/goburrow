package web

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mozthewah/opensourcedsentryalternative/internal/event"
	"github.com/mozthewah/opensourcedsentryalternative/internal/ingest"
	"github.com/mozthewah/opensourcedsentryalternative/internal/store"
)

// testNow is the fixed clock for rendered activity, so tests can't flake
// around UTC midnight.
var testNow = time.Date(2026, 3, 14, 12, 0, 0, 0, time.UTC)

type harness struct {
	t      *testing.T
	ts     *httptest.Server
	st     *store.Store
	client *http.Client
	csrf   string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := &Server{Store: st, Ingest: &ingest.Handler{Store: st, Log: log}, Log: log, Version: "test", Now: func() time.Time { return testNow }}
	ts := httptest.NewUnstartedServer(nil)
	ts.Start()
	t.Cleanup(ts.Close)
	srv.BaseURL, _ = url.Parse(ts.URL)
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = h
	jar, _ := cookiejar.New(nil)
	return &harness{t: t, ts: ts, st: st, client: &http.Client{Jar: jar}}
}

func (h *harness) get(path string) (int, string) {
	h.t.Helper()
	resp, err := h.client.Get(h.ts.URL + path)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if m := regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`).FindSubmatch(b); m != nil {
		h.csrf = string(m[1])
	}
	return resp.StatusCode, string(b)
}

func (h *harness) post(path string, form url.Values) (int, string) {
	h.t.Helper()
	if h.csrf != "" && form.Get("csrf") == "" {
		form.Set("csrf", h.csrf)
	}
	resp, err := h.client.PostForm(h.ts.URL+path, form)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (h *harness) ingest(path string, body []byte, headers map[string]string) (int, string) {
	h.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, h.ts.URL+path, bytes.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func gz(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

// envelope wraps an event like an SDK would. Newline-delimited items must be
// single-line JSON, so the fixture is compacted first.
func envelope(eventJSON string) []byte {
	return []byte("{}\n{\"type\":\"event\"}\n" + compact(eventJSON) + "\n{\"type\":\"transaction\"}\n{\"spans\":[]}\n")
}

func compact(s string) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(s)); err != nil {
		panic(err)
	}
	return buf.String()
}

func TestEndToEnd(t *testing.T) {
	h := newHarness(t)

	// First run redirects to setup.
	if code, body := h.get("/"); code != 200 || !strings.Contains(body, "Create account") {
		t.Fatalf("expected setup page, got %d", code)
	}
	if code, _ := h.post("/setup", url.Values{"email": {"a@b.co"}, "password": {"short"}, "password2": {"short"}}); code != 400 {
		t.Errorf("weak password accepted: %d", code)
	}
	h.post("/setup", url.Values{"email": {"a@b.co"}, "password": {"long-enough-pw"}, "password2": {"long-enough-pw"}})
	if code, _ := h.post("/setup", url.Values{"email": {"x@b.co"}, "password": {"long-enough-pw"}, "password2": {"long-enough-pw"}}); code != 403 {
		t.Errorf("second setup should be forbidden, got %d", code)
	}

	// Create a project.
	h.get("/projects/new")
	if code, _ := h.post("/projects/new", url.Values{"name": {"Web App"}, "platform": {"node"}, "csrf": {"wrong"}}); code != 403 {
		t.Errorf("bad CSRF accepted: %d", code)
	}
	if code, body := h.post("/projects/new", url.Values{"name": {"Web App"}, "platform": {"node"}}); code != 200 || !strings.Contains(body, "/1") || !strings.Contains(body, "data-wait-first-event") {
		t.Fatalf("create project: %d", code)
	}
	p, err := h.st.ProjectBySlug(context.Background(), "web-app")
	if err != nil {
		t.Fatal(err)
	}
	auth := map[string]string{"X-Sentry-Auth": "Sentry sentry_version=7, sentry_key=" + p.PublicKey}

	// Auth failures.
	if code, _ := h.ingest("/api/1/envelope/", envelope(`{}`), nil); code != 401 {
		t.Errorf("missing key: %d", code)
	}
	if code, _ := h.ingest("/api/1/envelope/", envelope(`{}`), map[string]string{"X-Sentry-Auth": "Sentry sentry_key=nope"}); code != 401 {
		t.Errorf("wrong key: %d", code)
	}
	if code, _ := h.ingest("/api/999/envelope/", envelope(`{}`), auth); code != 401 {
		t.Errorf("wrong project: %d", code)
	}

	ev := func(id, value string) string {
		return fmt.Sprintf(`{"event_id":"%s","level":"error","platform":"node","environment":"prod",
			"exception":{"values":[{"type":"TypeError","value":"%s","stacktrace":{"frames":[
				{"filename":"app.js","function":"handler","lineno":10,"in_app":true,
				 "pre_context":["a","b"],"context_line":"boom()","post_context":["c"]}]}}]},
			"breadcrumbs":{"values":[{"message":"click","category":"ui"}]},"tags":{"k":"v"},"user":{"id":"1"}}`, id, value)
	}

	// Envelope (gzip), legacy store (query auth), and envelope-header DSN auth.
	if code, body := h.ingest("/api/1/envelope/", gz(envelope(ev(strings.Repeat("a", 32), "x is undefined"))),
		map[string]string{"X-Sentry-Auth": auth["X-Sentry-Auth"], "Content-Encoding": "gzip"}); code != 200 || !strings.Contains(body, strings.Repeat("a", 32)) {
		t.Fatalf("envelope ingest: %d %s", code, body)
	}
	if code, _ := h.ingest("/api/1/store/?sentry_key="+p.PublicKey, []byte(ev(strings.Repeat("b", 32), "y is undefined")), nil); code != 200 {
		t.Fatalf("store ingest: %d", code)
	}
	dsnEnv := []byte(fmt.Sprintf("{\"dsn\":\"%s\"}\n{\"type\":\"event\"}\n%s\n", strings.Replace(h.ts.URL, "://", "://"+p.PublicKey+"@", 1)+"/1", compact(ev(strings.Repeat("c", 32), "z"))))
	if code, body := h.ingest("/api/1/envelope", dsnEnv, nil); code != 200 {
		t.Fatalf("dsn-in-envelope ingest (no trailing slash): %d %s", code, body)
	}
	// Duplicate event id: idempotent.
	h.ingest("/api/1/store/?sentry_key="+p.PublicKey, []byte(ev(strings.Repeat("b", 32), "y is undefined")), nil)

	issues, _ := h.st.ListIssues(context.Background(), p.ID, store.IssueFilter{})
	if len(issues) != 1 || issues[0].TimesSeen != 3 {
		t.Fatalf("want 1 issue seen 3 times (grouped, deduped, transaction dropped), got %d issues %+v", len(issues), issues)
	}
	issue := issues[0]

	// CORS preflight for browser SDKs.
	req, _ := http.NewRequest(http.MethodOptions, h.ts.URL+"/api/1/envelope/", nil)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("CORS preflight: %d", resp.StatusCode)
	}

	// Every page renders.
	for _, path := range []string{"/", "/projects/web-app", "/projects/web-app?status=all&q=Type&sort=times_seen",
		"/projects/web-app/settings", fmt.Sprintf("/issues/%d", issue.ID), "/projects/nope", "/issues/999"} {
		code, body := h.get(path)
		want := 200
		if strings.Contains(path, "nope") || strings.Contains(path, "999") {
			want = 404
		}
		if code != want {
			t.Errorf("GET %s = %d, want %d\n%s", path, code, want, body)
		}
	}
	_, body := h.get(fmt.Sprintf("/issues/%d", issue.ID))
	for _, want := range []string{"TypeError", "boom()", "click", "handler", "prod"} {
		if !strings.Contains(body, want) {
			t.Errorf("issue page missing %q", want)
		}
	}

	// Resolve, then a new event regresses it.
	h.post(fmt.Sprintf("/issues/%d/status", issue.ID), url.Values{"status": {"resolved"}})
	if i, _ := h.st.IssueByID(context.Background(), issue.ID); i.Status != store.StatusResolved {
		t.Fatalf("status = %s", i.Status)
	}
	h.ingest("/api/1/store/?sentry_key="+p.PublicKey, []byte(ev(strings.Repeat("d", 32), "again")), nil)
	if i, _ := h.st.IssueByID(context.Background(), issue.ID); i.Status != store.StatusUnresolved || !i.Regressed {
		t.Errorf("regression should reopen and flag Regressed, got %s regressed=%v", i.Status, i.Regressed)
	}
	if _, body := h.get("/projects/web-app"); !strings.Contains(body, `pill regression">Regression<`) {
		t.Errorf("issues list should show REGRESSION pill for a regressed issue")
	}
	if _, body := h.get(fmt.Sprintf("/issues/%d", issue.ID)); !strings.Contains(body, `pill regression">Regression<`) {
		t.Errorf("issue page should show REGRESSION pill for a regressed issue")
	}
	// A manual status change clears the flag.
	h.post(fmt.Sprintf("/issues/%d/status", issue.ID), url.Values{"status": {"ignored"}})
	if i, _ := h.st.IssueByID(context.Background(), issue.ID); i.Regressed {
		t.Errorf("Regressed should clear on manual status change")
	}
	h.post(fmt.Sprintf("/issues/%d/status", issue.ID), url.Values{"status": {"unresolved"}})
	if _, body := h.get(fmt.Sprintf("/issues/%d", issue.ID)); strings.Contains(body, `pill regression">Regression<`) {
		t.Errorf("REGRESSION pill should be gone after status change")
	}

	// Test-event button and its rendered issue page (exercises every panel).
	h.get("/projects/web-app/settings")
	h.post("/projects/web-app/test-event", url.Values{})
	issues, _ = h.st.ListIssues(context.Background(), p.ID, store.IssueFilter{Query: "ZeroDivision"})
	if len(issues) != 1 {
		t.Fatalf("test event issue missing")
	}
	if code, body := h.get(fmt.Sprintf("/issues/%d", issues[0].ID)); code != 200 || !strings.Contains(body, "unhandled") {
		t.Errorf("test event page: %d (handled=false should render as unhandled)", code)
	}

	// Rotating the key needs the confirmation box, even without JS.
	h.post("/projects/web-app/rotate-key", url.Values{})
	if code, _ := h.ingest("/api/1/store/?sentry_key="+p.PublicKey, []byte(`{}`), nil); code == 401 {
		t.Errorf("rotate-key without confirm should not rotate")
	}
	// Rotating the key invalidates the old DSN.
	h.post("/projects/web-app/rotate-key", url.Values{"confirm": {"rotate"}})
	if code, _ := h.ingest("/api/1/store/?sentry_key="+p.PublicKey, []byte(`{}`), nil); code != 401 {
		t.Errorf("old key still works after rotation: %d", code)
	}

	// Resolving returns to the list with a one-step undo; undo restores
	// without offering another undo, and a stale undo link offers nothing.
	if code, body := h.post(fmt.Sprintf("/issues/%d/status", issue.ID), url.Values{"status": {"resolved"}}); code != 200 || !strings.Contains(body, "Undo") || !strings.Contains(body, `name="undo" value="1"`) {
		t.Errorf("resolve should land on the issue list with an undo: %d", code)
	}
	if code, body := h.post(fmt.Sprintf("/issues/%d/status", issue.ID), url.Values{"status": {"unresolved"}, "undo": {"1"}}); code != 200 || !strings.Contains(body, "Restored.") || strings.Contains(body, "Undo") {
		t.Errorf("undo should restore without another undo: %d", code)
	}
	if _, body := h.get(fmt.Sprintf("/projects/web-app?flash=status&issue=%d&to=resolved&undo=unresolved", issue.ID)); strings.Contains(body, "Undo") {
		t.Errorf("stale undo link should not offer undo once the status has changed")
	}

	// Bulk status change from the list, scoped to the project.
	h.post("/projects/web-app/issues/status", url.Values{"status": {"ignored"}, "id": {fmt.Sprint(issue.ID)}, "back": {"/projects/web-app?status=unresolved"}})
	if i, _ := h.st.IssueByID(context.Background(), issue.ID); i.Status != store.StatusIgnored {
		t.Errorf("bulk ignore: status = %s", i.Status)
	}
	h.post("/projects/web-app/issues/status", url.Values{"status": {"unresolved"}, "id": {fmt.Sprint(issue.ID)}})

	// Level filter and search-aware tab counts.
	if _, body := h.get("/projects/web-app?level=error"); !strings.Contains(body, "TypeError") {
		t.Errorf("error-level filter should keep error issues")
	}
	if _, body := h.get("/projects/web-app?q=no-such-thing-at-all"); !strings.Contains(body, `class="tab-count">0<`) {
		t.Errorf("tab counts should follow the search")
	}

	// Deleting an issue needs the confirmation box, even without JS.
	h.post(fmt.Sprintf("/issues/%d/delete", issue.ID), url.Values{})
	if _, err := h.st.IssueByID(context.Background(), issue.ID); err != nil {
		t.Errorf("delete without confirm should keep the issue: %v", err)
	}
	h.post(fmt.Sprintf("/issues/%d/delete", issue.ID), url.Values{"confirm": {"delete"}})
	if _, err := h.st.IssueByID(context.Background(), issue.ID); err == nil {
		t.Errorf("confirmed delete should remove the issue")
	}

	// A missing issue says so specifically.
	if code, body := h.get("/issues/987654"); code != 404 || !strings.Contains(body, "no issue with that number") {
		t.Errorf("missing issue: %d, want issue-specific 404", code)
	}

	// Unknown routes get the designed 404; /login while signed in goes home.
	if code, body := h.get("/no-such-page"); code != 404 || !strings.Contains(body, "Back to projects") || !strings.Contains(body, "Log out") {
		t.Errorf("unknown route: %d, want designed 404 that keeps the signed-in navigation", code)
	}
	if _, body := h.get("/login"); strings.Contains(body, `action="/login"`) {
		t.Errorf("/login while signed in should redirect away from the form")
	}

	// Logout ends the session; a wrong password gets plain, friendly copy.
	h.post("/logout", url.Values{})
	if code, body := h.post("/login", url.Values{"email": {"nobody@example.com"}, "password": {"wrong-password-123"}}); code != 401 || !strings.Contains(body, "don&#39;t match") {
		t.Errorf("bad login: %d, want friendly error", code)
	}
	if code, body := h.get("/"); code != 200 || !strings.Contains(body, "Log in") {
		t.Errorf("expected login page after logout")
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"":                 "/",
		"/projects/x":      "/projects/x",
		"//evil.com":       "/",
		`/\evil.com`:       "/",
		"https://evil.com": "/",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNightTapeDataAndFuncs(t *testing.T) {
	// 1. Unit tests for template funcs
	labels := dayLabels()
	if len(labels) != 14 || labels[0] != "01" || labels[13] != "14" {
		t.Fatalf("unexpected dayLabels(): %v", labels)
	}
	if got := trackNumber(1); got != "01" {
		t.Errorf("trackNumber(1) = %q, want 01", got)
	}
	if got := trackNumber(51); got != "51" {
		t.Errorf("trackNumber(51) = %q, want 51", got)
	}
	if got := comma(1284); got != "1,284" {
		t.Errorf("comma(1284) = %q, want 1,284", got)
	}
	if got := comma(0); got != "0" {
		t.Errorf("comma(0) = %q, want 0", got)
	}
	if got := activitySummary([]int{}); got != "0 events in the last 14 days" {
		t.Errorf("activitySummary(empty) = %q", got)
	}
	var sampleAct [14]int
	sampleAct[0] = 3
	sampleAct[11] = 9 // day 12 (1-based)
	sampleAct[13] = 25
	if got := activitySummary(sampleAct); got != "37 events in the last 14 days, peak 25 on day 14" {
		t.Errorf("activitySummary(sampleAct) = %q", got)
	}
	if got := maxInt(sampleAct); got != 25 {
		t.Errorf("maxInt([14]int) = %d, want 25", got)
	}
	bh := barHeights(sampleAct)
	if len(bh) != 14 || bh[13] != "h100" || bh[1] != "h0" || bh[0] != "h15" {
		t.Errorf("barHeights(sampleAct) = %v", bh)
	}

	// 2. Handler view model and template execution verification
	h := newHarness(t)
	h.post("/setup", url.Values{"email": {"fox@burrow.test"}, "password": {"long-enough-pw"}, "password2": {"long-enough-pw"}})
	h.get("/projects/new")
	h.post("/projects/new", url.Values{"name": {"Night Tape App"}, "platform": {"go"}})

	ctx := context.Background()
	p, err := h.st.ProjectBySlug(ctx, "night-tape-app")
	if err != nil {
		t.Fatal(err)
	}

	now := testNow
	// Create 52 distinct issues to test pagination (track label 51 on page 2),
	// status counts, and 14-day activity bars.
	var firstIssueID int64
	for i := 1; i <= 52; i++ {
		ev := &event.Event{
			EventID:     event.NewEventID(),
			Timestamp:   now,
			Level:       "error",
			Fingerprint: []string{fmt.Sprintf("fp-issue-%03d", i)},
			Message:     fmt.Sprintf("Unique issue label %03d", i),
		}
		// Give issue 52 multiple events across days (index 11, label "12", is 2 days ago; index 13, label "14", is today)
		recv := now.Add(time.Duration(i) * time.Second)
		res, err := h.st.SaveEvent(ctx, p.ID, ev, recv)
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			firstIssueID = res.IssueID
		}
		if i == 52 {
			// Add 2 more events 2 days ago (day index 11, label "12")
			for j := 0; j < 2; j++ {
				evOld := &event.Event{
					EventID:     event.NewEventID(),
					Timestamp:   now.Add(-2 * 24 * time.Hour),
					Level:       "error",
					Fingerprint: ev.Fingerprint,
					Message:     ev.Message,
				}
				if _, err := h.st.SaveEvent(ctx, p.ID, evOld, now.Add(-2*24*time.Hour)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	// Resolve issue 1 and ignore issue 2
	if err := h.st.SetIssueStatus(ctx, firstIssueID, store.StatusResolved); err != nil {
		t.Fatal(err)
	}
	if err := h.st.SetIssueStatus(ctx, firstIssueID+1, store.StatusIgnored); err != nil {
		t.Fatal(err)
	}

	// Render issues list (all statuses, page 1 and page 2)
	if code, body := h.get("/projects/night-tape-app?status=all&page=1"); code != 200 || !strings.Contains(body, "Unique issue label 052") {
		t.Fatalf("page 1 failed: %d", code)
	}
	if code, body := h.get("/projects/night-tape-app?status=all&page=2"); code != 200 || !strings.Contains(body, "Unique issue label 001") {
		t.Fatalf("page 2 failed: %d", code)
	}

	// Render projects list and issue detail
	if code, body := h.get("/"); code != 200 || !strings.Contains(body, `count-hot">50<`) {
		t.Fatalf("projects list failed: %d\n%s", code, body)
	}
	if code, body := h.get(fmt.Sprintf("/issues/%d", firstIssueID+51)); code != 200 || !strings.Contains(body, "h100") {
		t.Fatalf("issue detail failed: %d", code)
	}

	// Verify custom template expressions against the exact handler data shapes
	checkTpl := template.Must(template.New("check").Funcs(funcs).Parse(`
		{{range .Data.Issues}}[track={{.TrackLabel}} total={{.ActivityTotal}} bar11={{index .Bars 11}} bar13={{index .Bars 13}} sum={{.ActivitySummary}}]{{end}}
		counts={{.Data.Counts.Unresolved}}/{{.Data.Counts.Resolved}}/{{.Data.Counts.Ignored}}/{{.Data.Counts.All}}
		days={{len .Data.DayLabels}}
	`))
	issuesPage2, err := h.st.ListIssues(ctx, p.ID, store.IssueFilter{Limit: 50, Offset: 50})
	if err != nil || len(issuesPage2) != 2 {
		t.Fatalf("ListIssues page 2: %v len=%d", err, len(issuesPage2))
	}
	actMap, err := h.st.IssueActivity(ctx, []int64{firstIssueID + 51, issuesPage2[0].ID}, store.ActivityDays, now)
	if err != nil {
		t.Fatal(err)
	}
	counts, err := h.st.IssueStatusCounts(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	act52, bars52, total52, sum52 := buildActivityFields(actMap[firstIssueID+51])
	iv52 := IssueView{
		Issue:           &store.Issue{ID: firstIssueID + 51, Title: "52"},
		TrackLabel:      trackNumber(51),
		Activity:        act52,
		Bars:            bars52,
		ActivityTotal:   total52,
		ActivitySummary: sum52,
	}
	var buf strings.Builder
	err = checkTpl.Execute(&buf, pageData{
		Data: map[string]any{
			"Issues":    []IssueView{iv52},
			"Counts":    counts,
			"DayLabels": dayLabels(),
		},
	})
	if err != nil {
		t.Fatalf("template check execute failed: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"track=51",
		"total=3",
		"bar11=h100",
		"bar13=h50",
		"sum=3 events in the last 14 days, peak 2 on day 12",
		"counts=50/1/1/52",
		"days=14",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("template output missing %q; got:\n%s", want, out)
		}
	}
}
