package web

import (
	"strings"
	"testing"
)

func TestEveryPlatformHasFilledInSetup(t *testing.T) {
	const dsn, reportURL, key = "https://k@burrow.test/3", "https://burrow.test/api/3/report", "k3y"
	for _, p := range platforms {
		if p.ID == "roblox" {
			continue // its own template; covered by TestRobloxSettingsShowReportSnippet
		}
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
}
