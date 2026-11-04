// Package web serves the Burrow dashboard.
package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mozthewah/opensourcedsentryalternative/internal/ingest"
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
	Log     *slog.Logger
	BaseURL *url.URL // public URL, used to build DSNs and secure cookies
	Version string

	pages  map[string]*template.Template
	logins *throttle
}

// Handler builds the full HTTP handler: ingestion API + dashboard.
func (s *Server) Handler() (http.Handler, error) {
	if err := s.loadTemplates(); err != nil {
		return nil, err
	}
	s.logins = newThrottle(15 * time.Minute)
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
	mux.HandleFunc("POST /projects/{slug}/test-event", s.authed(s.projectTestEvent))
	mux.HandleFunc("POST /projects/{slug}/rotate-key", s.authed(s.projectRotateKey))
	mux.HandleFunc("POST /projects/{slug}/delete", s.authed(s.projectDelete))
	mux.HandleFunc("GET /issues/{id}", s.authed(s.issueDetail))
	mux.HandleFunc("POST /issues/{id}/status", s.authed(s.issueSetStatus))
	mux.HandleFunc("POST /issues/{id}/delete", s.authed(s.issueDelete))

	mux.HandleFunc("GET /teams", s.authed(s.teamList))
	mux.HandleFunc("GET /teams/new", s.authed(s.teamNewPage))
	mux.HandleFunc("POST /teams/new", s.authed(s.teamCreate))
	mux.HandleFunc("GET /teams/{team}", s.authed(s.teamDetail))
	mux.HandleFunc("POST /teams/{team}/rename", s.authed(s.teamRename))
	mux.HandleFunc("POST /teams/{team}/delete", s.authed(s.teamDelete))
	mux.HandleFunc("POST /teams/{team}/members/{user}/role", s.authed(s.memberRole))
	mux.HandleFunc("POST /teams/{team}/members/{user}/remove", s.authed(s.memberRemove))
	mux.HandleFunc("POST /teams/{team}/invites", s.authed(s.inviteCreate))
	mux.HandleFunc("POST /teams/{team}/invites/{invite}/revoke", s.authed(s.inviteRevoke))

	mux.HandleFunc("GET /invite/{token}", s.optionalAuth(s.invitePage))
	mux.HandleFunc("POST /invite/{token}", s.optionalAuth(s.inviteAccept))

	mux.HandleFunc("GET /account", s.authed(s.accountPage))
	mux.HandleFunc("POST /account/password", s.authed(s.accountPassword))

	mux.HandleFunc("GET /admin/users", s.authed(s.adminOnly(s.adminUsers)))
	mux.HandleFunc("POST /admin/users/{user}/admin", s.authed(s.adminOnly(s.adminSetAdmin)))
	mux.HandleFunc("POST /admin/users/{user}/delete", s.authed(s.adminOnly(s.adminDeleteUser)))

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
		if name == "layout" {
			continue
		}
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", path)
		if err != nil {
			return fmt.Errorf("template %s: %w", name, err)
		}
		s.pages[name] = t
	}
	return nil
}

// pageData is what every template receives.
type pageData struct {
	Section string // top-nav section to mark as current
	Title   string
	Session *store.Session
	Version string
	Flash   string
	Data    any
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, code int, page, title string, data any) {
	t, ok := s.pages[page]
	if !ok {
		http.Error(w, "unknown page "+page, http.StatusInternalServerError)
		return
	}
	pd := pageData{Section: navSection(r.URL.Path), Title: title, Session: sessionFrom(r.Context()), Version: s.Version, Data: data, Flash: r.URL.Query().Get("flash")}
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

func navSection(path string) string {
	switch {
	case strings.HasPrefix(path, "/teams"):
		return "teams"
	case strings.HasPrefix(path, "/admin"):
		return "admin"
	case strings.HasPrefix(path, "/account"):
		return "account"
	case path == "/" || strings.HasPrefix(path, "/projects") || strings.HasPrefix(path, "/issues"):
		return "projects"
	}
	return ""
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusNotFound, "error", "Not found", "We dug around but couldn't find that.")
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

// optionalAuth loads the session if there is one (enforcing CSRF on its
// POSTs) and otherwise falls back to the same-origin check used by the
// login form. Used by invite links, which work signed in or out.
func (s *Server) optionalAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var sess *store.Session
		if c, err := r.Cookie(sessionCookie); err == nil {
			sess, err = s.Store.SessionByToken(r.Context(), c.Value)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				s.serverError(w, r, err)
				return
			}
		}
		if r.Method == http.MethodPost {
			if !s.originOK(r) || (sess != nil && !constantTimeEq(r.PostFormValue("csrf"), sess.CSRF)) {
				http.Error(w, "invalid CSRF token. Reload the page and try again.", http.StatusForbidden)
				return
			}
		}
		if sess != nil {
			r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, sess))
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
