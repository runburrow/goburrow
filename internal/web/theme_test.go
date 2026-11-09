package web

import (
	"net/url"
	"strings"
	"testing"
)

func TestThemeChoice(t *testing.T) {
	h := newHarness(t)
	h.post("/setup", url.Values{"email": {"a@b.co"}, "password": {"long-enough-pw"}, "password2": {"long-enough-pw"}})

	_, body := h.get("/account")
	if strings.Contains(body, "data-theme") || strings.Contains(body, "theme-barcode.css") {
		t.Fatal("the default look should carry no theme attribute or stylesheet")
	}
	if code, _ := h.post("/account/theme", url.Values{"theme": {"barcode"}, "csrf": {"wrong"}}); code != 403 {
		t.Errorf("bad CSRF accepted: %d", code)
	}
	if _, body = h.post("/account/theme", url.Values{"theme": {"barcode"}}); !strings.Contains(body, `<html lang="en" data-theme="barcode">`) ||
		!strings.Contains(body, "/static/theme-barcode.css?v=") || !strings.Contains(body, `content="#000000"`) {
		t.Error("picking Barcode should set the attribute, stylesheet and theme colour")
	}
	if code, css := h.get("/static/theme-barcode.css"); code != 200 || !strings.Contains(css, `[data-theme="barcode"]`) {
		t.Errorf("theme stylesheet: %d", code)
	}

	// The cookie belongs to the browser, so the login page wears it too.
	h.get("/account")
	h.post("/logout", url.Values{})
	if _, body = h.get("/login"); !strings.Contains(body, `data-theme="barcode"`) {
		t.Error("the theme should survive logging out")
	}
	h.post("/login", url.Values{"email": {"a@b.co"}, "password": {"long-enough-pw"}})

	// Anything that isn't a known theme goes back to the default.
	h.get("/account")
	if _, body = h.post("/account/theme", url.Values{"theme": {`"><script>`}}); strings.Contains(body, "data-theme") {
		t.Error("an unknown theme should fall back to the default")
	}
}
