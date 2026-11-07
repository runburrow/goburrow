package notify

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mozthewah/opensourcedsentryalternative/internal/store"
)

func TestTargetsFallbackOnlyWithoutProjectWebhooks(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, _ := st.CreateProject(ctx, "Shop", "go")
	d := NewDispatcher(st, "https://fallback.test/hook", "https://burrow.test", slog.New(slog.NewTextHandler(io.Discard, nil)))

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
		if ValidURL(u) != want {
			t.Errorf("ValidURL(%q) = %v", u, !want)
		}
	}
}
