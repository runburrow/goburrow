package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/runburrow/goburrow/internal/event"
	"github.com/runburrow/goburrow/internal/store"
)

var apiTokenRe = regexp.MustCompile(`<code id="api-token">(brw_[0-9a-f]+)</code>`)

// apiToken creates an API token as h through the Account page.
func (h *harness) apiToken(name string) string {
	h.t.Helper()
	h.get("/account")
	code, body := h.post("/account/api-tokens", url.Values{"name": {name}})
	m := apiTokenRe.FindStringSubmatch(body)
	if code != 200 || m == nil {
		h.t.Fatalf("create API token: %d", code)
	}
	return m[1]
}

// api GETs path with an optional bearer token and decodes the JSON reply.
func (h *harness) api(path, token string, into any) (int, http.Header) {
	h.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.ts.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req) // no cookies: the token is the only credential
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		h.t.Fatalf("GET %s: Content-Type %q, body %.200s", path, ct, b)
	}
	if into != nil {
		if err := json.Unmarshal(b, into); err != nil {
			h.t.Fatalf("GET %s: %v\n%s", path, err, b)
		}
	}
	return resp.StatusCode, resp.Header
}

func TestReadAPI(t *testing.T) {
	admin := newHarness(t)
	admin.post("/setup", url.Values{"email": {"root@x.co"}, "password": {"long-enough-pw"}, "password2": {"long-enough-pw"}, "team": {"Core"}})
	admin.get("/projects/new")
	admin.post("/projects/new", url.Values{"name": {"API"}, "team": {"core"}, "platform": {"go"}})
	p, _ := admin.st.ProjectBySlug(context.Background(), "api")
	for _, lvl := range []string{"error", "error", "warning"} {
		ev := &event.Event{EventID: event.NewEventID(), Level: lvl, Message: "boom " + lvl, Timestamp: testNow}
		if _, err := admin.st.SaveEvent(context.Background(), p.ID, ev, testNow); err != nil {
			t.Fatal(err)
		}
	}
	member := admin.as()
	member.join(admin.invite("core", "member", ""), "bob@x.co")
	admin.post("/teams/new", url.Values{"name": {"Other"}})
	outsider := admin.as()
	outsider.join(admin.invite("other", "owner", ""), "carol@x.co")

	// The spec is public, and points at this server.
	var spec struct {
		OpenAPI string              `json:"openapi"`
		Servers []map[string]string `json:"servers"`
		Paths   map[string]any      `json:"paths"`
	}
	if code, _ := admin.api("/api/v1/openapi.json", "", &spec); code != 200 || !strings.HasPrefix(spec.OpenAPI, "3.1") ||
		len(spec.Servers) != 1 || spec.Servers[0]["url"] != admin.ts.URL+"/api/v1" {
		t.Fatalf("spec: %d %+v", code, spec)
	}
	for _, path := range []string{"/projects", "/projects/{slug}", "/projects/{slug}/issues", "/projects/{slug}/activity", "/activity", "/issues/{id}"} {
		if spec.Paths[path] == nil {
			t.Errorf("spec is missing %s", path)
		}
	}

	// Tokens are required, and must be real.
	if code, hdr := admin.api("/api/v1/projects", "", nil); code != 401 || !strings.HasPrefix(hdr.Get("WWW-Authenticate"), "Bearer") {
		t.Errorf("no token: %d", code)
	}
	if code, _ := admin.api("/api/v1/projects", "brw_nope", nil); code != 401 {
		t.Errorf("bad token: %d", code)
	}

	tok := member.apiToken("Grafana")
	if _, body := member.get("/account"); strings.Contains(body, tok) || !strings.Contains(body, "Grafana") {
		t.Errorf("token list should name the token but never show its secret again")
	}

	var projects []apiProjectJSON
	if code, _ := member.api("/api/v1/projects", tok, &projects); code != 200 || len(projects) != 1 ||
		projects[0].Slug != "api" || projects[0].Team != "Core" || projects[0].Unresolved != 2 || projects[0].LastSeen == nil {
		t.Fatalf("projects: %d %+v", code, projects)
	}

	var detail apiProjectDetailJSON
	if code, _ := member.api("/api/v1/projects/api", tok, &detail); code != 200 || detail.Issues.All != 2 || detail.Issues.Unresolved != 2 {
		t.Errorf("project: %d %+v", code, detail)
	}

	var issues []apiIssueJSON
	if code, _ := member.api("/api/v1/projects/api/issues?level=error&status=unresolved", tok, &issues); code != 200 || len(issues) != 1 ||
		issues[0].Level != "error" || issues[0].TimesSeen != 2 || issues[0].Project != "api" {
		t.Fatalf("issues: %d %+v", code, issues)
	}
	for _, q := range []string{"status=nope", "limit=0", "limit=x", "sort=title", "offset=-1"} {
		if code, _ := member.api("/api/v1/projects/api/issues?"+q, tok, nil); code != 400 {
			t.Errorf("issues?%s = %d, want 400", q, code)
		}
	}

	var issue apiIssueDetailJSON
	if code, _ := member.api(fmt.Sprintf("/api/v1/issues/%d?days=3", issues[0].ID), tok, &issue); code != 200 || len(issue.Activity) != 3 ||
		issue.Activity[2].Events != 2 || issue.Activity[2].Date != testNow.Format("2006-01-02") {
		t.Errorf("issue: %d %+v", code, issue)
	}

	var days []apiDayJSON
	if code, _ := member.api("/api/v1/projects/api/activity?days=7", tok, &days); code != 200 || len(days) != 7 || days[6].Events != 3 || days[6].Project != "api" {
		t.Errorf("project activity: %d %+v", code, days)
	}
	if code, _ := member.api("/api/v1/activity", tok, &days); code != 200 || len(days) != store.ActivityDays {
		t.Errorf("activity: %d %d rows", code, len(days))
	}
	if code, _ := member.api("/api/v1/activity?days=91", tok, nil); code != 400 {
		t.Errorf("days=91 = %d", code)
	}

	// An outsider's token sees nothing of Core, and gets the same 404 as
	// for something missing.
	other := outsider.apiToken("")
	if code, _ := outsider.api("/api/v1/projects", other, &projects); code != 200 || len(projects) != 0 {
		t.Errorf("outsider projects: %d %+v", code, projects)
	}
	for _, path := range []string{"/api/v1/projects/api", "/api/v1/projects/api/issues", "/api/v1/projects/api/activity", fmt.Sprintf("/api/v1/issues/%d", issues[0].ID), "/api/v1/projects/missing"} {
		if code, _ := outsider.api(path, other, nil); code != 404 {
			t.Errorf("outsider GET %s = %d, want 404", path, code)
		}
	}

	// Instance admins' tokens don't carry Instance Admin: an admin token
	// sees only the admin's own teams, even while the session is elevated.
	admin.elevate()
	rootTok := admin.apiToken("root")
	if code, _ := admin.api("/api/v1/projects", rootTok, &projects); code != 200 || len(projects) != 1 {
		t.Errorf("admin token projects: %d %+v", code, projects)
	}

	// Unknown API paths answer in JSON, and the API never accepts writes.
	if code, _ := member.api("/api/v1/nope", tok, nil); code != 404 {
		t.Errorf("unknown API path = %d", code)
	}
	req, _ := http.NewRequest(http.MethodPost, admin.ts.URL+"/api/v1/projects", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+tok)
	if resp, err := http.DefaultClient.Do(req); err != nil || (resp.StatusCode != 404 && resp.StatusCode != 405) {
		t.Errorf("POST to the API should be refused")
	} else {
		resp.Body.Close()
	}

	// Revoking: only your own tokens, and then the token is dead.
	mine, _ := member.st.ListAPITokens(context.Background(), member.uid("bob@x.co"))
	if len(mine) != 1 || mine[0].LastUsedAt.IsZero() || mine[0].Name != "Grafana" {
		t.Fatalf("token list: %+v", mine)
	}
	outsider.get("/account")
	if code, _ := outsider.post(fmt.Sprintf("/account/api-tokens/%d/revoke", mine[0].ID), url.Values{}); code != 404 {
		t.Errorf("revoking someone else's token = %d, want 404", code)
	}
	member.get("/account")
	if code, body := member.post(fmt.Sprintf("/account/api-tokens/%d/revoke", mine[0].ID), url.Values{}); code != 200 || !strings.Contains(body, "Token revoked") {
		t.Errorf("revoke = %d", code)
	}
	if code, _ := member.api("/api/v1/projects", tok, nil); code != 401 {
		t.Errorf("revoked token still works: %d", code)
	}

	// Ingestion on /api/<id>/... is unaffected by the /api/v1 routes.
	if code, _ := admin.ingest(fmt.Sprintf("/api/%d/envelope/", p.ID), envelope(`{"message":"still here"}`),
		map[string]string{"X-Sentry-Auth": "Sentry sentry_key=" + p.PublicKey}); code != 200 {
		t.Errorf("ingest after API routes: %d", code)
	}
}

func TestAPITokenLimit(t *testing.T) {
	h := newHarness(t)
	h.post("/setup", url.Values{"email": {"a@b.co"}, "password": {"long-enough-pw"}, "password2": {"long-enough-pw"}})
	uid := h.uid("a@b.co")
	for i := 0; i < store.MaxAPITokens; i++ {
		if _, _, err := h.st.CreateAPIToken(context.Background(), uid, "t"); err != nil {
			t.Fatal(err)
		}
	}
	h.get("/account")
	if code, body := h.post("/account/api-tokens", url.Values{"name": {"one more"}}); code != 400 || !strings.Contains(body, "Revoke one") {
		t.Errorf("token over the limit: %d", code)
	}
}
