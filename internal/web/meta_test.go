package web

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHeadTagsAndIcons(t *testing.T) {
	h := newHarness(t)
	_, body := h.get("/setup")
	for _, want := range []string{
		`<meta name="robots" content="noindex, nofollow">`,
		`<meta property="og:image" content="` + h.ts.URL + `/static/og.png">`, // absolute, for link previews
		`<meta name="twitter:card" content="summary_large_image">`,
		`<link rel="apple-touch-icon" href="/static/apple-touch-icon.png">`,
		`<link rel="manifest" href="/static/manifest.webmanifest">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("head missing %s", want)
		}
	}
	for path, ctype := range map[string]string{
		"/static/og.png":               "image/png",
		"/static/apple-touch-icon.png": "image/png",
		"/static/manifest.webmanifest": "application/manifest+json",
		"/static/favicon.svg":          "image/svg+xml",
	} {
		resp, err := http.Get(h.ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), ctype) {
			t.Errorf("%s: %d %s", path, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
}
