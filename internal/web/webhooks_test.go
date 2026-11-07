package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mozthewah/opensourcedsentryalternative/internal/store"
)

// receiver is a fake chat webhook that records what Burrow posts to it.
type receiver struct {
	*httptest.Server
	mu    sync.Mutex
	got   []map[string]any
	reply int
}

func newReceiver(t *testing.T) *receiver {
	rc := &receiver{reply: http.StatusOK}
	rc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		rc.mu.Lock()
		rc.got = append(rc.got, body)
		code := rc.reply
		rc.mu.Unlock()
		w.WriteHeader(code)
	}))
	t.Cleanup(rc.Close)
	return rc
}

func (rc *receiver) kinds() []string {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	var out []string
	for _, b := range rc.got {
		if bw, ok := b["burrow"].(map[string]any); ok {
			out = append(out, bw["kind"].(string))
		}
	}
	return out
}

// waitFor polls until the background deliveries arrive.
func (rc *receiver) waitFor(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if k := rc.kinds(); len(k) >= n {
			return k
		}
		time.Sleep(10 * time.Millisecond)
	}
	return rc.kinds()
}

func settingsHarness(t *testing.T, platform string) (*harness, string, int64, string) {
	t.Helper()
	h := newHarness(t)
	h.post("/setup", url.Values{"email": {"fox@burrow.test"}, "password": {"long-enough-pw"}, "password2": {"long-enough-pw"}})
	h.get("/projects/new")
	h.post("/projects/new", url.Values{"name": {"Obby"}, "platform": {platform}})
	p, err := h.st.ProjectBySlug(t.Context(), "obby")
	if err != nil {
		t.Fatal(err)
	}
	return h, "/projects/obby/settings", p.ID, p.PublicKey
}

func TestRobloxSettingsShowReportSnippet(t *testing.T) {
	h, settings, id, key := settingsHarness(t, "roblox")
	_, body := h.get(settings)
	reportURL := h.ts.URL + "/api/" + strconv.FormatInt(id, 10) + "/report"
	for _, want := range []string{
		"Roblox (Luau) setup", "ScriptContext.Error", "BurrowClientError", "Allow HTTP Requests",
		`local URL = "` + reportURL + `"`, `local KEY = "` + key + `"`,
		`id="report-url">` + reportURL + `<`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page missing %q", want)
		}
	}
	// Other platforms keep their SDK snippet and still get the report API.
	h2, settings2, _, _ := settingsHarness(t, "python")
	_, body2 := h2.get(settings2)
	if strings.Contains(body2, "ScriptContext") || !strings.Contains(body2, "sentry_sdk.init") || !strings.Contains(body2, "Report API") {
		t.Error("python settings should show the Sentry snippet and the report API, not Roblox")
	}
}

func TestWebhookSettingsFlow(t *testing.T) {
	h, settings, id, key := settingsHarness(t, "roblox")
	hook := newReceiver(t)

	if _, body := h.get(settings); !strings.Contains(body, "No webhooks yet") {
		t.Error("empty state missing")
	}
	// Bad input is refused with a reason.
	h.get(settings)
	if _, body := h.post("/projects/obby/webhooks", url.Values{"url": {"ftp://nope"}, "on_new": {"1"}}); !strings.Contains(body, "doesn't look right") {
		t.Error("invalid URL accepted")
	}
	if _, body := h.post("/projects/obby/webhooks", url.Values{"url": {hook.URL}}); !strings.Contains(body, "Pick at least one") {
		t.Error("webhook with no trigger accepted")
	}
	// Regressions only.
	if _, body := h.post("/projects/obby/webhooks", url.Values{"url": {hook.URL}, "on_regression": {"1"}}); !strings.Contains(body, "Webhook added") || !strings.Contains(body, "Regressions only") {
		t.Fatal("webhook not added")
	}
	hooks, _ := h.st.ListWebhooks(t.Context(), id)
	if len(hooks) != 1 {
		t.Fatalf("webhooks = %d", len(hooks))
	}
	whPath := "/projects/obby/webhooks/" + strconv.FormatInt(hooks[0].ID, 10)

	// Send test: delivered, then a failing receiver says why.
	if _, body := h.post(whPath+"/test", url.Values{}); !strings.Contains(body, "Test alert delivered") {
		t.Error("test delivery not confirmed")
	}
	hook.mu.Lock()
	hook.reply = http.StatusNotFound
	hook.mu.Unlock()
	if _, body := h.post(whPath+"/test", url.Values{}); !strings.Contains(body, "answered HTTP 404") {
		t.Error("failed test not explained")
	}
	hook.mu.Lock()
	hook.reply = http.StatusOK
	hook.mu.Unlock()
	// Unknown result codes in the URL show nothing.
	if _, body := h.get(settings + "?flash=webhook-tested&result=<b>hi</b>"); strings.Contains(body, "hi</b>") || strings.Contains(body, "&lt;b&gt;hi") {
		t.Error("arbitrary result text rendered")
	}

	// A new issue does not fire a regressions-only webhook; a regression does.
	report := func() {
		req, _ := http.NewRequest(http.MethodPost, h.ts.URL+"/api/"+strconv.FormatInt(id, 10)+"/report", strings.NewReader(`{"message":"Workspace.Door:3: boom","stack":"Workspace.Door:3"}`))
		req.Header.Set("X-Burrow-Key", key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("report: %v %v", err, resp)
		}
		resp.Body.Close()
	}
	report()
	issues, _ := h.st.ListIssues(t.Context(), id, store.IssueFilter{})
	h.get("/issues/" + strconv.FormatInt(issues[0].ID, 10))
	h.post("/issues/"+strconv.FormatInt(issues[0].ID, 10)+"/status", url.Values{"status": {"resolved"}})
	report()
	kinds := hook.waitFor(t, 3)
	if strings.Join(kinds, ",") != "test,test,regression" {
		t.Errorf("deliveries = %v, want two tests then one regression", kinds)
	}

	// Another project can't touch this webhook.
	h.post("/projects/new", url.Values{"name": {"Other"}, "platform": {"go"}})
	if code, _ := h.post("/projects/other/webhooks/"+strconv.FormatInt(hooks[0].ID, 10)+"/delete", url.Values{}); code != http.StatusNotFound {
		t.Errorf("cross-project delete: %d", code)
	}
	if _, body := h.post(whPath+"/delete", url.Values{}); !strings.Contains(body, "Webhook removed") {
		t.Error("remove not confirmed")
	}
	if hooks, _ := h.st.ListWebhooks(t.Context(), id); len(hooks) != 0 {
		t.Error("webhook still stored")
	}
}
