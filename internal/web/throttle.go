package web

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// throttle is a small in-memory fixed-window limiter for failed logins.
// It's per-process: fine for Burrow's single-binary deployment model.
type throttle struct {
	mu     sync.Mutex
	window time.Duration
	hits   map[string]*bucket
	now    func() time.Time
}

type bucket struct {
	count int
	reset time.Time
}

func newThrottle(window time.Duration) *throttle {
	return &throttle{window: window, hits: map[string]*bucket{}, now: time.Now}
}

// blocked reports whether key has reached limit failures in the window.
func (t *throttle) blocked(key string, limit int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	b, ok := t.hits[key]
	return ok && t.now().Before(b.reset) && b.count >= limit
}

func (t *throttle) fail(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	b, ok := t.hits[key]
	if !ok || !now.Before(b.reset) {
		b = &bucket{reset: now.Add(t.window)}
		t.hits[key] = b
	}
	b.count++
	if len(t.hits) > 50000 { // bound memory under a spray attack
		for k, v := range t.hits {
			if !now.Before(v.reset) {
				delete(t.hits, k)
			}
		}
	}
}

func (t *throttle) clear(key string) {
	t.mu.Lock()
	delete(t.hits, key)
	t.mu.Unlock()
}

// Limits: per account (stops guessing one password) and per client IP
// (stops spraying many accounts). Behind a reverse proxy every client
// shares the proxy's IP, so the IP limit is generous.
const (
	loginLimitPerEmail = 10
	loginLimitPerIP    = 100
)

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func emailKey(email string) string { return "email:" + strings.ToLower(strings.TrimSpace(email)) }
