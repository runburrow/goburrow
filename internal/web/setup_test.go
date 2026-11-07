package web

import (
	"net/url"
	"strings"
	"testing"
)

func TestEveryPlatformHasFilledInSetup(t *testing.T) {
	const dsn, reportURL, key = "https://k@burrow.test/3", "https://burrow.test/api/3/report", "k3y"
	var all []platform
	for _, g := range platformGroups {
		all = append(all, g.Platforms...)
	}
	for _, p := range all {
		if _, ok := setups[p.ID]; !ok {
			t.Errorf("%s: no setup steps", p.ID)
			continue
		}
		s := setupFor(p.ID, dsn, reportURL, key)
		all := s.Note
		for _, st := range s.Steps {
			all += st.Label + st.Code
		}
		if strings.Contains(all, "__") {
			t.Errorf("%s: unreplaced placeholder", p.ID)
		}
		if s.SDK && !strings.Contains(all, dsn) {
			t.Errorf("%s: SDK setup doesn't include the DSN", p.ID)
		}
		if !s.SDK && (!strings.Contains(all, reportURL) || !strings.Contains(all, key)) {
			t.Errorf("%s: report API setup missing URL or key", p.ID)
		}
	}
	// Unknown or legacy platform values fall back to the generic steps.
	if got := setupFor("cobol", dsn, reportURL, key); len(got.Steps) == 0 {
		t.Error("unknown platform has no steps")
	}
	if platformName("react-native") != "React Native" || platformName("") != "Other" {
		t.Error("platformName")
	}
	if s := setupFor("roblox", dsn, reportURL, key); !s.Steps[0].Tall || s.Steps[0].Download != "Burrow.lua" || s.Steps[1].Tall {
		t.Error("the Roblox module should scroll in place and be downloadable; short steps shouldn't")
	}
}

func TestReachWarning(t *testing.T) {
	mustURL := func(s string) *url.URL { u, _ := url.Parse(s); return u }
	cases := []struct {
		base, reach string
		want        string // "" none, "note", "error"
	}{
		{"https://errors.example.com", "public", ""},
		{"http://errors.example.com", "public", "error"},
		{"https://192.168.1.5", "public", "error"},
		{"https://burrow.lan", "public", "error"},
		{"http://127.0.0.1:8000", "public", "error"},
		{"http://192.168.1.5:8000", "https", "note"},
		{"https://192.168.1.5", "https", ""},
		{"http://localhost:8000", "", "note"},
		{"http://127.0.0.1:8000", "network", "note"},
		{"http://192.168.1.5:8000", "", ""},
	}
	for _, c := range cases {
		w := reachWarning(mustURL(c.base), c.reach, "Roblox (Luau)")
		got := ""
		if w != nil {
			got = "note"
			if w["Error"] == true {
				got = "error"
			}
		}
		if got != c.want {
			t.Errorf("reachWarning(%s, %q) = %q, want %q", c.base, c.reach, got, c.want)
		}
	}
}

func TestInlineCode(t *testing.T) {
	cases := map[string]string{
		"call `Burrow.capture()` now": "call <code>Burrow.capture()</code> now",
		"<b>`x`</b>":                  "&lt;b&gt;<code>x</code>&lt;/b&gt;",
		"one ` unmatched":             "one ` unmatched",
		"plain":                       "plain",
	}
	for in, want := range cases {
		if got := string(inlineCode(in)); got != want {
			t.Errorf("inlineCode(%q) = %q, want %q", in, got, want)
		}
	}
}
