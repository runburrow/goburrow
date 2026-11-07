package mail

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"
)

// TestPreview writes the emails as HTML files when PREVIEW_DIR is set.
func TestPreview(t *testing.T) {
	dir := os.Getenv("PREVIEW_DIR")
	if dir == "" {
		t.Skip("set PREVIEW_DIR to write previews")
	}
	fox := "data:image/png;base64," + base64.StdEncoding.EncodeToString(foxPNG)
	write := func(name string, m Message) {
		os.WriteFile(dir+"/"+name+".html", []byte(strings.ReplaceAll(m.HTML, "cid:fox", fox)), 0o644)
		os.WriteFile(dir+"/"+name+".txt", []byte("Subject: "+m.Subject+"\n\n"+m.Text), 0o644)
	}
	write("alert", AlertMessage(Alert{Project: "Checkout API", Title: "TypeError: Cannot read properties of undefined (reading 'total')", Culprit: "src/cart/summary.ts in computeTotals", Level: "error", Regression: true, Environment: "production", Release: "shop@2.5.4", TimesSeen: 64, Activity: []int{0, 2, 1, 3, 0, 0, 9, 4, 0, 1, 0, 2, 5, 28}, IssueURL: "https://burrow.example.com/issues/3", SettingsURL: "https://burrow.example.com/projects/checkout-api/settings#email-alerts", To: "dev@example.com"}))
	write("alert-fatal-test", AlertMessage(Alert{Project: "Lava Obby", Title: "Error: attempt to index nil with 'Price'", Culprit: "ServerScriptService.Shop in buy", Level: "fatal", Environment: "production", TimesSeen: 1, Activity: []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}, IssueURL: "https://burrow.example.com/issues/9", SettingsURL: "https://burrow.example.com/projects/lava-obby/settings#email-alerts", To: "dev@example.com", Test: true}))
	write("invite", InviteMessage("otter@example.com", Invite{Team: "Payments", Role: "member", InvitedBy: "sean@goburrow.net", URL: "https://burrow.example.com/invite/4f9c2a0e1b7d", Expires: time.Date(2026, 11, 14, 18, 30, 0, 0, time.UTC)}))
	write("check", CheckMessage(Check{Server: "https://burrow.example.com", Setup: "smtp.example.com:587 (starttls), from alerts@example.com", To: "sean@goburrow.net"}))
}
