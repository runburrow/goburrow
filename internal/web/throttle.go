package web

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// loginThrottle slows password guessing. After maxFailures failed logins
// within window, further attempts from the same address or against the same
// email are refused until the window passes. It is in-memory and per
// process, which is right for a single-binary deployment.
type loginThrottle struct {
	mu       sync.Mutex
	failures map[string][]time.Time
	now      func() time.Time
}

const (
	throttleMaxFailures = 10
	throttleWindow      = 15 * time.Minute
)

func newLoginThrottle(now func() time.Time) *loginThrottle {
	return &loginThrottle{failures: map[string][]time.Time{}, now: now}
}

// keys returns the throttle keys for a request: the client address and the
// email being tried. Only RemoteAddr is used, so a reverse proxy makes every
// client share one address; the email key still limits guessing per account.
func throttleKeys(r *http.Request, email string) []string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	keys := []string{"ip:" + host}
	if e := strings.ToLower(strings.TrimSpace(email)); e != "" {
		keys = append(keys, "email:"+e)
	}
	return keys
}

// blocked reports whether any key has hit the limit, and how long until the
// oldest counted failure expires.
func (t *loginThrottle) blocked(keys []string) (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	for _, k := range keys {
		recent := t.prune(k, now)
		if len(recent) >= throttleMaxFailures {
			return true, recent[0].Add(throttleWindow).Sub(now)
		}
	}
	return false, 0
}

func (t *loginThrottle) fail(keys []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	for _, k := range keys {
		t.failures[k] = append(t.prune(k, now), now)
	}
	// Keep the map from growing forever under a slow drip of bad logins.
	if len(t.failures) > 10000 {
		for k := range t.failures {
			if len(t.prune(k, now)) == 0 {
				delete(t.failures, k)
			}
		}
	}
}

func (t *loginThrottle) success(keys []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, k := range keys {
		delete(t.failures, k)
	}
}

// prune drops failures older than the window. Caller holds the lock.
func (t *loginThrottle) prune(k string, now time.Time) []time.Time {
	all := t.failures[k]
	i := 0
	for i < len(all) && now.Sub(all[i]) > throttleWindow {
		i++
	}
	if i > 0 {
		all = all[i:]
		if len(all) == 0 {
			delete(t.failures, k)
		} else {
			t.failures[k] = all
		}
	}
	return all
}
