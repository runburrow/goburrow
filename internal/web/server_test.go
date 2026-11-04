package web

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
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

	"github.com/mozthewah/opensourcedsentryalternative/internal/ingest"
	"github.com/mozthewah/opensourcedsentryalternative/internal/store"
)

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
	srv := &Server{Store: st, Ingest: &ingest.Handler{Store: st, Log: log}, Log: log, Version: "test"}
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
	if code, body := h.post("/projects/new", url.Values{"name": {"Web App"}, "platform": {"node"}}); code != 200 || !strings.Contains(body, "/1") {
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
	if i, _ := h.st.IssueByID(context.Background(), issue.ID); i.Status != store.StatusUnresolved {
		t.Errorf("regression should reopen, status = %s", i.Status)
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

	// Rotating the key invalidates the old DSN.
	h.post("/projects/web-app/rotate-key", url.Values{})
	if code, _ := h.ingest("/api/1/store/?sentry_key="+p.PublicKey, []byte(`{}`), nil); code != 401 {
		t.Errorf("old key still works after rotation: %d", code)
	}

	// Logout ends the session.
	h.post("/logout", url.Values{})
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
