// Package ingest implements the Sentry-compatible event ingestion API so the
// official Sentry SDKs can report to Burrow unmodified.
package ingest

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mozthewah/opensourcedsentryalternative/internal/event"
	"github.com/mozthewah/opensourcedsentryalternative/internal/store"
)

const (
	maxCompressedBody   = 20 << 20 // 20 MiB on the wire
	maxDecompressedBody = 50 << 20 // 50 MiB after inflation (zip-bomb guard)
)

// Notifier is told about new and regressed issues (alerts hook point).
type Notifier interface {
	IssueEvent(ctx context.Context, project *store.Project, issueID int64, ev *event.Event, res *store.IngestResult)
}

type Handler struct {
	Store    *store.Store
	Log      *slog.Logger
	Notifier Notifier     // optional
	Limit    *RateLimiter // optional per-project events-per-minute cap
	Now      func() time.Time
}

// Register mounts the ingestion routes on mux.
func (h *Handler) Register(mux *http.ServeMux) {
	// Registered with and without the trailing slash: a mux redirect would
	// turn the SDK's POST into a GET and drop the event.
	for _, suffix := range []string{"", "/"} {
		mux.HandleFunc("/api/{project_id}/envelope"+suffix, h.cors(h.envelope))
		mux.HandleFunc("/api/{project_id}/store"+suffix, h.cors(h.store))
		mux.HandleFunc("/api/{project_id}/report"+suffix, h.cors(h.report))
	}
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now().UTC()
}

// cors lets browser SDKs post cross-origin. Auth is the DSN public key, not
// cookies, so a permissive policy is safe here.
func (h *Handler) cors(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Sentry-Auth, X-Burrow-Key, Authorization, Content-Encoding, sentry-trace, baggage")
		w.Header().Set("Access-Control-Expose-Headers", "X-Sentry-Error, X-Sentry-Rate-Limits, Retry-After")
		w.Header().Set("Access-Control-Max-Age", "3600")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			apiError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		next(w, r)
	}
}

func (h *Handler) envelope(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	env, err := ParseEnvelope(body)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	project, err := h.authenticate(r, env.Header.DSN)
	if err != nil {
		h.authError(w, err)
		return
	}
	events := 0
	for _, item := range env.Items {
		if item.Type == "event" {
			events++
		}
	}
	if !h.allow(w, project, events) {
		return
	}

	var lastID string
	for _, item := range env.Items {
		switch item.Type {
		case "event":
			id, err := h.ingestEvent(r.Context(), project, item.Payload)
			if err != nil {
				apiError(w, http.StatusBadRequest, err.Error())
				return
			}
			lastID = id
		default:
			// transactions, sessions, attachments, client_reports, replays,
			// profiles, check-ins...: accepted and dropped for now so SDKs
			// don't retry them forever. See ROADMAP.md.
		}
	}
	if lastID == "" {
		lastID = env.Header.EventID
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": lastID})
}

// store is the legacy single-event endpoint, still used by older SDKs.
func (h *Handler) store(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	project, err := h.authenticate(r, "")
	if err != nil {
		h.authError(w, err)
		return
	}
	if !h.allow(w, project, 1) {
		return
	}
	id, err := h.ingestEvent(r.Context(), project, body)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

// Ingest stores an already-normalized event for project. Exposed so the
// dashboard's "send test event" button uses the exact same pipeline.
func (h *Handler) Ingest(ctx context.Context, project *store.Project, ev *event.Event) error {
	received := h.now()
	res, err := h.Store.SaveEvent(ctx, project.ID, ev, received)
	if err != nil {
		return err
	}
	if h.Notifier != nil && !res.Duplicate && (res.NewIssue || res.Regressed) {
		h.Notifier.IssueEvent(ctx, project, res.IssueID, ev, res)
	}
	return nil
}

func (h *Handler) ingestEvent(ctx context.Context, project *store.Project, payload []byte) (string, error) {
	ev, err := event.Parse(payload, h.now())
	if err != nil {
		return "", err
	}
	if err := h.Ingest(ctx, project, ev); err != nil {
		h.Log.Error("save event", "project", project.Slug, "err", err)
		return "", errors.New("internal error")
	}
	return ev.EventID, nil
}

var (
	errNoKey       = errors.New("missing sentry_key")
	errNoReportKey = errors.New("missing project key: send it in the X-Burrow-Key header")
	errBadProject  = errors.New("invalid project or key")
)

// allow applies the rate limit. Over the limit it answers 429 with the
// headers the Sentry SDKs read to back off, and returns false.
func (h *Handler) allow(w http.ResponseWriter, project *store.Project, events int) bool {
	ok, wait := h.Limit.Allow(project.ID, events)
	if ok {
		return true
	}
	secs := int(wait/time.Second) + 1
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	w.Header().Set("X-Sentry-Rate-Limits", fmt.Sprintf("%d::key", secs))
	apiError(w, http.StatusTooManyRequests, fmt.Sprintf("rate limit: this project accepts %d events a minute; retry in %ds", h.Limit.PerMinute, secs))
	return false
}

func (h *Handler) authError(w http.ResponseWriter, err error) {
	if errors.Is(err, errNoKey) || errors.Is(err, errNoReportKey) || errors.Is(err, errBadProject) {
		apiError(w, http.StatusUnauthorized, err.Error())
		return
	}
	h.Log.Error("ingest auth", "err", err)
	apiError(w, http.StatusInternalServerError, "internal error")
}

// authenticate resolves the project from the URL and verifies the public
// key, which SDKs send in one of: X-Sentry-Auth header, Authorization header,
// sentry_key query param, or the envelope header's dsn.
func (h *Handler) authenticate(r *http.Request, envelopeDSN string) (*store.Project, error) {
	pid, err := strconv.ParseInt(r.PathValue("project_id"), 10, 64)
	if err != nil {
		return nil, errBadProject
	}
	key := keyFromAuthHeader(r.Header.Get("X-Sentry-Auth"))
	if key == "" {
		key = keyFromAuthHeader(r.Header.Get("Authorization"))
	}
	if key == "" {
		key = r.URL.Query().Get("sentry_key")
	}
	if key == "" && envelopeDSN != "" {
		if u, err := url.Parse(envelopeDSN); err == nil && u.User != nil {
			key = u.User.Username()
		}
	}
	if key == "" {
		return nil, errNoKey
	}
	return h.projectWithKey(r, pid, key)
}

// projectWithKey loads project pid and checks key against its public key.
func (h *Handler) projectWithKey(r *http.Request, pid int64, key string) (*store.Project, error) {
	p, err := h.Store.ProjectByID(r.Context(), pid)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errBadProject
	} else if err != nil {
		return nil, err
	}
	if !constantTimeEq(p.PublicKey, key) {
		return nil, errBadProject
	}
	return p, nil
}

// keyFromAuthHeader parses `Sentry sentry_key=abc, sentry_version=7, ...`.
func keyFromAuthHeader(v string) string {
	v = strings.TrimSpace(v)
	if len(v) < 7 || !strings.EqualFold(v[:7], "sentry ") {
		return ""
	}
	for _, part := range strings.Split(v[7:], ",") {
		k, val, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && strings.TrimSpace(k) == "sentry_key" {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

func constantTimeEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func readBody(r *http.Request) ([]byte, error) {
	var rd io.Reader = http.MaxBytesReader(nil, r.Body, maxCompressedBody)
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))) {
	case "", "identity":
	case "gzip", "x-gzip":
		gz, err := gzip.NewReader(rd)
		if err != nil {
			return nil, fmt.Errorf("bad gzip body: %w", err)
		}
		defer gz.Close()
		rd = gz
	case "deflate":
		// "deflate" is officially zlib-wrapped, but some clients send raw
		// deflate. Try zlib first and fall back.
		raw, err := io.ReadAll(rd)
		if err != nil {
			return nil, err
		}
		if zr, err := zlib.NewReader(bytes.NewReader(raw)); err == nil {
			defer zr.Close()
			rd = zr
		} else {
			rd = flate.NewReader(bytes.NewReader(raw))
		}
	default:
		return nil, fmt.Errorf("unsupported content-encoding %q", r.Header.Get("Content-Encoding"))
	}
	body, err := io.ReadAll(io.LimitReader(rd, maxDecompressedBody+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if len(body) > maxDecompressedBody {
		return nil, errors.New("payload too large")
	}
	return body, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("X-Sentry-Error", msg)
	writeJSON(w, code, map[string]string{"detail": msg})
}
