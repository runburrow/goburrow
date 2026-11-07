package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/runburrow/goburrow/internal/store"
)

func TestTargetsFallbackOnlyWithoutProjectWebhooks(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, _ := st.CreateProject(ctx, 0, "Shop", "go")
	d := NewDispatcher(st, "https://fallback.test/hook", "https://burrow.test", true, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if got := d.targets(ctx, p, false); !slices.Equal(got, []string{"https://fallback.test/hook"}) {
		t.Errorf("no project webhooks: targets = %v, want the fallback", got)
	}
	st.CreateWebhook(ctx, p.ID, "https://chat.test/new", true, false)
	st.CreateWebhook(ctx, p.ID, "https://chat.test/both", true, true)
	if got := d.targets(ctx, p, false); !slices.Equal(got, []string{"https://chat.test/new", "https://chat.test/both"}) {
		t.Errorf("new issue: targets = %v", got)
	}
	if got := d.targets(ctx, p, true); !slices.Equal(got, []string{"https://chat.test/both"}) {
		t.Errorf("regression: targets = %v", got)
	}
	for u, want := range map[string]bool{"https://hooks.slack.com/x": true, "http://10.0.0.2:9000/h": true, "ftp://x": false, "hooks.slack.com/x": false, "": false} {
		if (CheckURL(u, true) == nil) != want {
			t.Errorf("CheckURL(%q, allowPrivate) ok = %v", u, !want)
		}
	}
}

func TestCheckURLRefusesPrivateByDefault(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1:8000/", "http://localhost/hook", "http://10.0.0.2/", "http://192.168.1.5:9000/x", "http://169.254.169.254/latest/meta-data", "http://[::1]/", "http://100.64.0.1/", "http://0.0.0.0/"} {
		if err := CheckURL(u, false); !errors.Is(err, ErrPrivateAddress) {
			t.Errorf("CheckURL(%q) = %v, want ErrPrivateAddress", u, err)
		}
		if err := CheckURL(u, true); err != nil {
			t.Errorf("CheckURL(%q, allowPrivate) = %v", u, err)
		}
	}
	if err := CheckURL("https://8.8.8.8/hook", false); err != nil {
		t.Errorf("public IP refused: %v", err)
	}
}

func TestClientRefusesPrivateAndRedirects(t *testing.T) {
	// A receiver on loopback: refused when private is off, reachable when on.
	rc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer rc.Close()
	body := []byte(`{"text":"hi"}`)
	d := &Dispatcher{Client: newClient(false), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := d.post(context.Background(), rc.URL+"/", body); !errors.Is(err, ErrPrivateAddress) {
		t.Errorf("loopback delivery with private off: %v, want ErrPrivateAddress", err)
	}
	d.Client = newClient(true)
	if err := d.post(context.Background(), rc.URL+"/", body); err != nil {
		t.Errorf("loopback delivery with private on: %v", err)
	}
	if err := d.post(context.Background(), rc.URL+"/redirect", body); err == nil {
		t.Error("redirect was followed")
	}
}

func TestInertDefusesChatMarkup(t *testing.T) {
	got := inert("boom @channel <!here> [click](https://evil.test) a@b.co")
	for _, bad := range []string{"@channel", "<!here>", "](", "a@b"} {
		if strings.Contains(got, bad) {
			t.Errorf("inert left %q in %q", bad, got)
		}
	}
	if strings.ReplaceAll(got, zeroWidth, "") != "boom @channel <!here> [click](https://evil.test) a@b.co" {
		t.Error("inert changed visible text")
	}
}
