package web

// Interactive docs for the Read API at /api/v1/docs: Scalar
// (github.com/scalar/scalar, MIT) rendering /api/v1/openapi.json. Like the
// spec, the page is public; "Try it" requests carry whatever API token the
// reader types in as "Authorization: Bearer brw_...".
//
// Scalar is vendored, not loaded from a CDN, so the page works on an
// air-gapped Burrow and never tells a third party who reads the docs. It is
// embedded gzipped (about 1.4 MB instead of 4.9 MB). To update it:
//
//	npm pack @scalar/api-reference@<version>
//	tar xzf scalar-api-reference-<version>.tgz
//	gzip -9 -c package/dist/browser/standalone.js > internal/web/apidocs/scalar-standalone.js.gz
//
// then set scalarVersion below. The version is in the script's URL, so
// browsers can cache it for good.

import (
	"bytes"
	"compress/gzip"
	"embed"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const scalarVersion = "1.77.0"

//go:embed apidocs
var apiDocsFS embed.FS

// docsCSP keeps the docs page to this server: no fonts, scripts, images or
// requests from anywhere else. Scalar injects its own <style> elements, so
// styles need 'unsafe-inline'; scripts stay 'self' only.
const docsCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; form-action 'self'; base-uri 'none'"

func (s *Server) registerDocs(mux *http.ServeMux) error {
	t, err := template.New("docs.html").Funcs(funcs).ParseFS(templateFS, "templates/partials.html")
	if err != nil {
		return err
	}
	if t, err = t.ParseFS(apiDocsFS, "apidocs/docs.html"); err != nil {
		return err
	}
	mux.HandleFunc("GET /api/v1/docs", s.apiDocsPage(t))
	mux.HandleFunc("GET /api/v1/docs/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/api/v1/docs", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /api/v1/docs/docs.js", docsAsset("apidocs/docs.js", "text/javascript; charset=utf-8", "public, max-age=3600"))
	mux.HandleFunc("GET /api/v1/docs/docs.css", docsAsset("apidocs/docs.css", "text/css; charset=utf-8", "public, max-age=3600"))
	mux.HandleFunc("GET /api/v1/docs/scalar-"+scalarVersion+".js", scalarScript)
	return nil
}

// docsHeaders sets what securityHeaders gives the rest of the UI, which
// skips everything under /api/.
func docsHeaders(h http.Header) {
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Content-Security-Policy", docsCSP)
}

func (s *Server) apiDocsPage(t *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		err := t.ExecuteTemplate(&buf, "docs.html", map[string]any{
			"Asset":         s.assetTag,
			"Theme":         themeFrom(r),
			"ScalarVersion": scalarVersion,
		})
		if err != nil {
			s.Log.Error("render docs", "err", err)
			http.Error(w, "template error", http.StatusInternalServerError)
			return
		}
		docsHeaders(w.Header())
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(buf.Bytes())
	}
}

func docsAsset(name, contentType, cache string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := apiDocsFS.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		docsHeaders(w.Header())
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", cache)
		_, _ = w.Write(b)
	}
}

// scalarScript serves the embedded gzip as is to every browser that
// accepts it (all of them), and unpacks it for the odd client that doesn't.
func scalarScript(w http.ResponseWriter, r *http.Request) {
	gz, err := apiDocsFS.ReadFile("apidocs/scalar-standalone.js.gz")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	docsHeaders(h)
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("Vary", "Accept-Encoding")
	if acceptsGzip(r.Header.Get("Accept-Encoding")) {
		h.Set("Content-Encoding", "gzip")
		_, _ = w.Write(gz)
		return
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		http.Error(w, "bad embedded script", http.StatusInternalServerError)
		return
	}
	_, _ = io.Copy(w, zr)
}

// acceptsGzip reports whether an Accept-Encoding header allows gzip,
// either by name or through "*", and not with q=0.
func acceptsGzip(ae string) bool {
	for _, part := range strings.Split(ae, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "gzip" && name != "*" {
			continue
		}
		q := 1.0
		for _, p := range strings.Split(params, ";") {
			if k, v, ok := strings.Cut(strings.TrimSpace(p), "="); ok && strings.EqualFold(k, "q") {
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					q = f
				}
			}
		}
		return q > 0
	}
	return false
}
