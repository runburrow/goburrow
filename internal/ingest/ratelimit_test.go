package ingest

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRateLimiterBucket(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := NewRateLimiter(60) // one a second, a minute's burst
	l.Now = func() time.Time { return now }
	if ok, _ := l.Allow(1, 60); !ok {
		t.Fatal("a full bucket should take a minute's worth")
	}
	if ok, wait := l.Allow(1, 1); ok || wait < 900*time.Millisecond || wait > time.Second {
		t.Fatalf("empty bucket: ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.Allow(2, 1); !ok {
		t.Fatal("projects have separate buckets")
	}
	now = now.Add(10 * time.Second)
	if ok, _ := l.Allow(1, 10); !ok {
		t.Fatal("bucket should refill over time")
	}
	var off *RateLimiter
	if ok, _ := off.Allow(1, 1e6); !ok {
		t.Fatal("a nil limiter allows everything")
	}
}

func TestReportRateLimited(t *testing.T) {
	ts, _, p, h := reportServerWithHandler(t)
	h.Limit = NewRateLimiter(2)
	url := ts.URL + "/api/" + itoa(p.ID) + "/report"
	for i := 0; i < 2; i++ {
		if code, out := postReport(t, url, p.PublicKey, `{"message":"x"}`); code != http.StatusOK {
			t.Fatalf("within limit: %d %s", code, out)
		}
	}
	code, out := postReport(t, url, p.PublicKey, `{"message":"x"}`)
	if code != http.StatusTooManyRequests || !strings.Contains(out, "2 events a minute") {
		t.Fatalf("over limit: %d %s", code, out)
	}
}
