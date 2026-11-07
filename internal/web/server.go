// Package web serves the Burrow dashboard.
package web

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mozthewah/opensourcedsentryalternative/internal/ingest"
	"github.com/mozthewah/opensourcedsentryalternative/internal/notify"
	"github.com/mozthewah/opensourcedsentryalternative/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

const (
	sessionCookie = "burrow_session"
	sessionTTL    = 30 * 24 * time.Hour
)

type Server struct {
	Store   *store.Store
	Ingest  *ingest.Handler
	Notify  *notify.Dispatcher // per-project webhooks; "Send test" uses it
	Log     *slog.Logger
	BaseURL *url.URL // public URL, used to build DSNs and secure cookies
	Version string
	// BehindProxy makes login throttling key on the client address the
	// reverse proxy appended to X-Forwarded-For instead of the proxy's own.
	BehindProxy bool
	Now         func() time.Time // optional clock override for tests

	pages    map[string]*template.Template
	assetTag string // hash of the static files, busts browser caches on every change
	throttle *loginThrottle
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Handler builds the full HTTP handler: ingestion API + dashboard.
func (s *Server) Handler() (http.Handler, error) {
	if err := s.loadTemplates(); err != nil {
		return nil, err
	}
	s.assetTag = hashStatic()
	s.throttle = newLoginThrottle(s.now)
	mux := http.NewServeMux()
	s.Ingest.Register(mux)

	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheForever(http.FileServerFS(static))))
	mux.HandleFunc("GET /healthz", s.healthz)

	mux.HandleFunc("GET /setup", s.setupPage)
	mux.HandleFunc("POST /setup", s.sameOrigin(s.setupSubmit))
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.sameOrigin(s.loginSubmit))
	mux.HandleFunc("POST /logout", s.authed(s.logout))

	mux.HandleFunc("GET /{$}", s.authed(s.projectList))
	mux.HandleFunc("GET /projects/new", s.authed(s.projectNewPage))
	mux.HandleFunc("POST /projects/new", s.authed(s.projectCreate))
	mux.HandleFunc("GET /projects/{slug}", s.authed(s.issueList))
	mux.HandleFunc("GET /projects/{slug}/settings", s.authed(s.projectSettings))
	mux.HandleFunc("GET /projects/{slug}/setup/{file}", s.authed(s.projectSetupDownload))
	mux.HandleFunc("POST /projects/{slug}/test-event", s.authed(s.projectTestEvent))
	mux.HandleFunc("POST /projects/{slug}/rotate-key", s.authed(s.projectRotateKey))
	mux.HandleFunc("POST /projects/{slug}/delete", s.authed(s.projectDelete))
	mux.HandleFunc("POST /projects/{slug}/webhooks", s.authed(s.webhookCreate))
	mux.HandleFunc("POST /projects/{slug}/webhooks/{id}/test", s.authed(s.webhookTest))
	mux.HandleFunc("POST /projects/{slug}/webhooks/{id}/delete", s.authed(s.webhookDelete))
	mux.HandleFunc("GET /issues/{id}", s.authed(s.issueDetail))
	mux.HandleFunc("POST /issues/{id}/status", s.authed(s.issueSetStatus))
	mux.HandleFunc("POST /issues/{id}/delete", s.authed(s.issueDelete))
	mux.HandleFunc("POST /projects/{slug}/issues/status", s.authed(s.issueBulkStatus))
	mux.HandleFunc("POST /projects/{slug}/issues/restore", s.authed(s.issueBulkRestore))
	mux.HandleFunc("/", s.withSession(s.notFound)) // unknown routes get the designed 404, not net/http's plain text

	return s.securityHeaders(mux), nil
}

func (s *Server) loadTemplates() error {
	s.pages = map[string]*template.Template{}
	entries, err := fs.Glob(templateFS, "templates/*.html")
	if err != nil {
		return err
	}
	for _, path := range entries {
		name := strings.TrimSuffix(strings.TrimPrefix(path, "templates/"), ".html")
		if name == "layout" || name == "partials" {
			continue
		}
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", "templates/partials.html", path)
		if err != nil {
			return fmt.Errorf("template %s: %w", name, err)
		}
		s.pages[name] = t
	}
	return nil
}

// navigationData avoids inspecting Data: project lists are slices and error data is a string.
type navigationData struct {
	Page    string
	Project *store.Project
}

// pageData is what every template receives.
type pageData struct {
	Title   string
	Session *store.Session
	Version string
	Asset   string
	Origin  string // public scheme://host, for absolute URLs (link previews)
	Flash   string
	Nav     navigationData
	Data    any
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, code int, page, title string, data any) {
	t, ok := s.pages[page]
	if !ok {
		http.Error(w, "unknown page "+page, http.StatusInternalServerError)
		return
	}
	pd := pageData{Title: title, Session: sessionFrom(r.Context()), Version: s.Version, Asset: s.assetTag, Origin: s.origin(), Data: data, Flash: r.URL.Query().Get("flash")}
	pd.Nav.Page = page
	if m, ok := data.(map[string]any); ok {
		pd.Nav.Project, _ = m["Project"].(*store.Project)
	}
	var buf strings.Builder
	if err := t.ExecuteTemplate(&buf, "layout.html", pd); err != nil {
		s.Log.Error("render", "page", page, "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(buf.String()))
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusNotFound, "error", "Not found", "Dug here, found nothing. The page may have moved, or the link is wrong.")
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.Log.Error("request failed", "path", r.URL.Path, "err", err)
	s.render(w, r, http.StatusInternalServerError, "error", "Error", "Something went wrong on our end. Check the server logs.")
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Store.Ping(ctx); err != nil {
		http.Error(w, "db unavailable", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte("ok"))
}

// ---------------------------------------------------------------- middleware

type ctxKey struct{}

func sessionFrom(ctx context.Context) *store.Session {
	s, _ := ctx.Value(ctxKey{}).(*store.Session)
	return s
}

// authed requires a logged-in session and, for state-changing requests, a
// valid CSRF token. With zero users it redirects to first-run setup.
func (s *Server) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			s.redirectToLogin(w, r)
			return
		}
		sess, err := s.Store.SessionByToken(r.Context(), c.Value)
		if errors.Is(err, store.ErrNotFound) {
			s.redirectToLogin(w, r)
			return
		} else if err != nil {
			s.serverError(w, r, err)
			return
		}
		if r.Method == http.MethodPost {
			if !s.originOK(r) || !constantTimeEq(r.PostFormValue("csrf"), sess.CSRF) {
				http.Error(w, "invalid CSRF token. Reload the page and try again.", http.StatusForbidden)
				return
			}
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, sess)))
	}
}

// withSession attaches the session when there is a valid one, without
// requiring it, so public pages like the 404 keep the app navigation.
func (s *Server) withSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookie); err == nil {
			if sess, err := s.Store.SessionByToken(r.Context(), c.Value); err == nil {
				r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, sess))
			}
		}
		next(w, r)
	}
}

func (s *Server) redirectToLogin(w http.ResponseWriter, r *http.Request) {
	if n, err := s.Store.UserCount(r.Context()); err == nil && n == 0 {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
}

// sameOrigin guards unauthenticated POST forms (login/setup) against
// cross-site submission.
func (s *Server) sameOrigin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.originOK(r) {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *Server) originOK(r *http.Request) bool {
	src := r.Header.Get("Origin")
	if src == "" {
		src = r.Header.Get("Referer")
	}
	if src == "" {
		// Non-browser clients and privacy-stripped requests. The CSRF token
		// (for authed routes) and SameSite cookies still protect us.
		return true
	}
	u, err := url.Parse(src)
	if err != nil {
		return false
	}
	if u.Host == r.Host {
		return true
	}
	return s.BaseURL != nil && u.Host == s.BaseURL.Host
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "same-origin")
			h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; frame-ancestors 'none'; form-action 'self'")
		}
		if s.BaseURL != nil && s.BaseURL.Scheme == "https" {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

func cacheForever(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		next.ServeHTTP(w, r)
	})
}

func constantTimeEq(a, b string) bool {
	if len(a) != len(b) || a == "" {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func (s *Server) setSessionCookie(w http.ResponseWriter, sess *store.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sess.Token,
		Path:     "/",
		Expires:  sess.ExpiresAt,
		HttpOnly: true,
		Secure:   s.BaseURL != nil && s.BaseURL.Scheme == "https",
		SameSite: http.SameSiteLaxMode,
	})
}

// DSN builds the client DSN for a project: scheme://key@host/project_id
func (s *Server) DSN(p *store.Project) string {
	u := *s.BaseURL
	u.User = url.User(p.PublicKey)
	u.Path = strings.TrimRight(u.Path, "/") + fmt.Sprintf("/%d", p.ID)
	return u.String()
}

// ReportURL is the report API endpoint for p.
func (s *Server) ReportURL(p *store.Project) string {
	u := *s.BaseURL
	u.Path = strings.TrimRight(u.Path, "/") + fmt.Sprintf("/api/%d/report", p.ID)
	return u.String()
}

// origin is the public scheme://host[/path] the dashboard is served at.
func (s *Server) origin() string {
	if s.BaseURL == nil {
		return ""
	}
	return strings.TrimRight(s.BaseURL.String(), "/")
}

func init() {
	// Go's MIME table doesn't know web app manifests.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// hashStatic fingerprints the embedded static files. Release builds change
// Version anyway, but dev builds all say "dev", so the URL needs the content.
func hashStatic() string {
	h := sha256.New()
	_ = fs.WalkDir(staticFS, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := staticFS.ReadFile(p)
		h.Write([]byte(p))
		h.Write(b)
		return err
	})
	return hex.EncodeToString(h.Sum(nil))[:12]
}
