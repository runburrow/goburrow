package ingest

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/runburrow/goburrow/internal/store"
)

func reportServer(t *testing.T) (*httptest.Server, *store.Store, *store.Project) {
	t.Helper()
	ts, st, p, _ := reportServerWithHandler(t)
	return ts, st, p
}

func reportServerWithHandler(t *testing.T) (*httptest.Server, *store.Store, *store.Project, *Handler) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p, err := st.CreateProject(context.Background(), "Obby", "roblox")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h := &Handler{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, st, p, h
}

func postReport(t *testing.T, url, key, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-Burrow-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestReportRobloxError(t *testing.T) {
	ts, st, p := reportServer(t)
	url := ts.URL + "/api/" + itoa(p.ID) + "/report"
	body := `{"message":"ServerScriptService.Shop:42: attempt to index nil with 'Price'",
		"stack":"ServerScriptService.Shop:42 function buy\nServerScriptService.Main:5",
		"platform":"roblox","environment":"production","tags":{"place_id":"123","job_id":"abc"}}`
	if code, out := postReport(t, url, p.PublicKey, body); code != http.StatusOK || !strings.Contains(out, `"ids"`) {
		t.Fatalf("report: %d %s", code, out)
	}
	// The same error from another server groups into the same issue.
	if code, out := postReport(t, url, p.PublicKey, strings.Replace(body, `"abc"`, `"def"`, 1)); code != http.StatusOK {
		t.Fatalf("second report: %d %s", code, out)
	}
	issues, err := st.ListIssues(context.Background(), p.ID, store.IssueFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues = %d, want 1", len(issues))
	}
	is := issues[0]
	if is.Title != "Error: attempt to index nil with 'Price'" || is.Culprit != "ServerScriptService.Shop in buy" || is.TimesSeen != 2 {
		t.Errorf("issue = %q / %q / %d", is.Title, is.Culprit, is.TimesSeen)
	}
}

func TestReportBatchAndMessages(t *testing.T) {
	ts, st, p := reportServer(t)
	url := ts.URL + "/api/" + itoa(p.ID) + "/report"
	body := `{"reports":[{"message":"Player 41 has no data","level":"warning"},{"message":"Player 97 has no data","level":"warn"}]}`
	if code, out := postReport(t, url, p.PublicKey, body); code != http.StatusOK || strings.Count(out, ",")+1 != 2 {
		t.Fatalf("batch: %d %s", code, out)
	}
	issues, _ := st.ListIssues(context.Background(), p.ID, store.IssueFilter{})
	if len(issues) != 1 || issues[0].Level != "warning" || issues[0].TimesSeen != 2 {
		t.Fatalf("issues = %+v", issues)
	}
}

func TestReportRejects(t *testing.T) {
	ts, _, p := reportServer(t)
	url := ts.URL + "/api/" + itoa(p.ID) + "/report"
	for name, c := range map[string]struct {
		key, body string
		want      int
	}{
		"no key":     {"", `{"message":"x"}`, http.StatusUnauthorized},
		"wrong key":  {"nope", `{"message":"x"}`, http.StatusUnauthorized},
		"no message": {p.PublicKey, `{"level":"error"}`, http.StatusBadRequest},
		"not json":   {p.PublicKey, `message=x`, http.StatusBadRequest},
		"empty list": {p.PublicKey, `[]`, http.StatusBadRequest},
	} {
		if code, out := postReport(t, url, c.key, c.body); code != c.want {
			t.Errorf("%s: %d %s, want %d", name, code, out, c.want)
		}
	}
	// Clients that can't set headers can pass the key in the query string.
	if code, out := postReport(t, url+"?key="+p.PublicKey, "", `{"message":"x"}`); code != http.StatusOK {
		t.Errorf("query key: %d %s", code, out)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
