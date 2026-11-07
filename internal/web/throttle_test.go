package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestLoginThrottle(t *testing.T) {
	h := newHarness(t)
	h.post("/setup", url.Values{"email": {"fox@burrow.test"}, "password": {"long-enough-pw"}, "password2": {"long-enough-pw"}})
	h.post("/logout", url.Values{})
	h.get("/login")
	for i := 0; i < throttleMaxFailures; i++ {
		if code, _ := h.post("/login", url.Values{"email": {"fox@burrow.test"}, "password": {"wrong-password"}}); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i, code)
		}
	}
	// The right password is refused too while the window is open.
	code, body := h.post("/login", url.Values{"email": {"fox@burrow.test"}, "password": {"long-enough-pw"}})
	if code != http.StatusTooManyRequests || !strings.Contains(body, "Too many failed attempts. Try again in 15 minutes.") {
		t.Fatalf("throttled login: %d %q", code, body[:200])
	}
	// The address limit is much higher (one office or proxy carries many
	// people), so another account from the same address isn't blocked yet.
	if code, _ := h.post("/login", url.Values{"email": {"other@burrow.test"}, "password": {"x"}}); code != http.StatusUnauthorized {
		t.Errorf("same address, other email: %d, want 401", code)
	}
}

func TestLoginThrottleWindowExpires(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	th := newLoginThrottle(func() time.Time { return now })
	keys := []string{"ip:1.2.3.4", "email:a@b.co"}
	for i := 0; i < throttleMaxFailures; i++ {
		th.fail(keys)
	}
	if b, _ := th.blocked(keys); !b {
		t.Fatal("should be blocked")
	}
	now = now.Add(throttleWindow + time.Second)
	if b, _ := th.blocked(keys); b {
		t.Fatal("should have expired")
	}
	th.fail(keys)
	th.success(keys)
	if b, _ := th.blocked(keys); b || len(th.failures) != 0 {
		t.Fatal("success should clear failures")
	}
}

func TestThrottleKeysBehindProxy(t *testing.T) {
	r, _ := http.NewRequest("POST", "/login", nil)
	r.RemoteAddr = "10.0.0.2:5555"                          // the proxy
	r.Header.Add("X-Forwarded-For", "6.6.6.6, 203.0.113.7") // first entry is client-forged
	if got := throttleKeys(r, "", false)[0]; got != "ip:10.0.0.2" {
		t.Errorf("without behindProxy: %s", got)
	}
	if got := throttleKeys(r, "", true)[0]; got != "ip:203.0.113.7" {
		t.Errorf("behindProxy should use the entry the proxy appended: %s", got)
	}
}
