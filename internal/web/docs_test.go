package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// rawGet fetches path with no cookies and no automatic gzip handling.
func (h *harness) rawGet(path string, hdr map[string]string) (*http.Response, []byte) {
	h.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.ts.URL+path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	tr := &http.Transport{DisableCompression: true}
	resp, err := (&http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestAPIDocsPage(t *testing.T) {
	h := newHarness(t)
	h.post("/setup", url.Values{"email": {"root@x.co"}, "password": {"long-enough-pw"}, "password2": {"long-enough-pw"}, "team": {"Core"}})

	// Public like the spec: no session needed.
	resp, body := h.rawGet("/api/v1/docs", nil)
	page := string(body)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("docs page: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	for _, want := range []string{
		`id="burrow-api-docs" data-openapi="/api/v1/openapi.json"`,
		`src="/api/v1/docs/scalar-` + scalarVersion + `.js"`,
		`src="/api/v1/docs/docs.js?v=`,
		`href="/account#api"`,
		`class="fox fox-mark"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("docs page lacks %q", want)
		}
	}
	if strings.Contains(page, "<script>") || strings.Contains(page, "cdn.") {
		t.Error("docs page has an inline script or a CDN reference")
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'self';", "connect-src 'self'", "font-src 'self' data:", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Errorf("missing security headers: %v", resp.Header)
	}

	// The barcode theme carries over from the cookie.
	resp, body = h.rawGet("/api/v1/docs", map[string]string{"Cookie": themeCookie + "=barcode"})
	if !strings.Contains(string(body), `data-theme="barcode"`) {
		t.Error("barcode theme not applied to docs page")
	}

	// A trailing slash redirects instead of 404ing.
	if resp, _ = h.rawGet("/api/v1/docs/", nil); resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != "/api/v1/docs" {
		t.Errorf("trailing slash: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// The init script asks for Bearer auth and keeps Scalar off the network.
	resp, body = h.rawGet("/api/v1/docs/docs.js", nil)
	js := string(body)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/javascript") {
		t.Fatalf("docs.js: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	for _, want := range []string{`preferredSecurityScheme: "bearerAuth"`, "withDefaultFonts: false", "persistAuth: false", "agent: { disabled: true }", "telemetry: false"} {
		if !strings.Contains(js, want) {
			t.Errorf("docs.js lacks %q", want)
		}
	}
	if resp, _ = h.rawGet("/api/v1/docs/docs.css", nil); resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/css") {
		t.Errorf("docs.css: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	// The spec it renders names the bearer scheme Scalar is told to prefer.
	var spec struct {
		Components struct {
			SecuritySchemes map[string]struct{ Type, Scheme string } `json:"securitySchemes"`
		} `json:"components"`
	}
	h.api("/api/v1/openapi.json", "", &spec)
	if s := spec.Components.SecuritySchemes["bearerAuth"]; s.Type != "http" || s.Scheme != "bearer" {
		t.Errorf("spec bearerAuth = %+v", s)
	}
}

func TestAPIDocsScalarScript(t *testing.T) {
	h := newHarness(t)
	path := "/api/v1/docs/scalar-" + scalarVersion + ".js"

	// Browsers get the embedded gzip as is.
	resp, gz := h.rawGet(path, map[string]string{"Accept-Encoding": "gzip, deflate, br"})
	if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip: %d %q", resp.StatusCode, resp.Header.Get("Content-Encoding"))
	}
	if !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") || resp.Header.Get("Vary") != "Accept-Encoding" {
		t.Errorf("cache headers: %v", resp.Header)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	unzipped, _ := io.ReadAll(zr)
	if len(unzipped) < 1<<20 || !bytes.Contains(unzipped, []byte("createApiReference")) {
		t.Fatalf("embedded script looks wrong: %d bytes", len(unzipped))
	}

	// Clients without gzip get it unpacked.
	resp, plain := h.rawGet(path, map[string]string{"Accept-Encoding": "identity"})
	if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "" || !bytes.Equal(plain, unzipped) {
		t.Errorf("identity: %d %q, %d bytes", resp.StatusCode, resp.Header.Get("Content-Encoding"), len(plain))
	}

	// Another version's URL is a JSON 404 like the rest of /api/v1.
	if resp, _ := h.rawGet("/api/v1/docs/scalar-0.0.1.js", nil); resp.StatusCode != 404 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Errorf("unknown version: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func TestAcceptsGzip(t *testing.T) {
	for ae, want := range map[string]bool{
		"":                    false,
		"identity":            false,
		"gzip":                true,
		"GZIP":                true,
		"deflate, gzip;q=1.0": true,
		"gzip;q=0":            false,
		"gzip; q=0.0":         false,
		"gzip;q=0.5":          true,
		"*":                   true,
		"br, *;q=0":           false,
	} {
		if got := acceptsGzip(ae); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", ae, got, want)
		}
	}
}

func TestAccountLinksAPIDocs(t *testing.T) {
	h := newHarness(t)
	h.post("/setup", url.Values{"email": {"root@x.co"}, "password": {"long-enough-pw"}, "password2": {"long-enough-pw"}, "team": {"Core"}})
	if _, body := h.get("/account"); !strings.Contains(body, `<a href="/api/v1/docs">interactive API docs</a>`) {
		t.Error("account page doesn't link the API docs")
	}
}
