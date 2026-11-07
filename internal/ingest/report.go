package ingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mozthewah/opensourcedsentryalternative/internal/event"
	"github.com/mozthewah/opensourcedsentryalternative/internal/store"
)

// The report API is Burrow's own, SDK-free way in: one JSON POST per error
// (or a batch of them), authenticated with the project key. It exists for
// runtimes no Sentry SDK covers, such as Roblox game servers, shell scripts
// and cron jobs.
//
//	POST /api/{project_id}/report
//	X-Burrow-Key: <project key>      (or Authorization: Bearer <key>, or ?key=)
//	{"message": "...", "stack": "...", "level": "error", "tags": {...}}
//
// The body is one report, a JSON array of reports, or {"reports": [...]}.

const maxBatch = 100

// Report is one error as the report API receives it. Only message (or
// error.value) is required.
type Report struct {
	Message     string            `json:"message"`
	Type        string            `json:"type"`  // e.g. "TypeError"; defaults to "Error" when there is a stack
	Stack       string            `json:"stack"` // stack trace text, any supported format
	Level       string            `json:"level"`
	Platform    string            `json:"platform"`
	Environment string            `json:"environment"`
	Release     string            `json:"release"`
	ServerName  string            `json:"server_name"`
	Logger      string            `json:"logger"`
	Transaction string            `json:"transaction"` // shown as the issue's culprit when set
	Tags        map[string]string `json:"tags"`
	User        map[string]any    `json:"user"`
	Extra       map[string]any    `json:"extra"`
	Fingerprint []string          `json:"fingerprint"`
	EventID     string            `json:"event_id"`
	Timestamp   json.RawMessage   `json:"timestamp"` // unix seconds or RFC 3339
}

func (h *Handler) report(w http.ResponseWriter, r *http.Request) {
	project, err := h.authenticateReport(r)
	if err != nil {
		h.authError(w, err)
		return
	}
	body, err := readBody(r)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	reports, err := decodeReports(body)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	now := h.now()
	ids := make([]string, 0, len(reports))
	for i, rep := range reports {
		ev, err := rep.toEvent(now)
		if err != nil {
			if len(reports) > 1 {
				err = fmt.Errorf("report %d: %w", i, err)
			}
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := h.Ingest(r.Context(), project, ev); err != nil {
			h.Log.Error("save report", "project", project.Slug, "err", err)
			apiError(w, http.StatusInternalServerError, "internal error")
			return
		}
		ids = append(ids, ev.EventID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ids": ids})
}

func decodeReports(body []byte) ([]Report, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, errors.New("empty body: send a JSON report")
	}
	var reports []Report
	switch body[0] {
	case '[':
		if err := json.Unmarshal(body, &reports); err != nil {
			return nil, fmt.Errorf("decode reports: %w", err)
		}
	case '{':
		var wrapped struct {
			Reports *[]Report `json:"reports"`
		}
		if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Reports != nil {
			reports = *wrapped.Reports
			break
		}
		var one Report
		if err := json.Unmarshal(body, &one); err != nil {
			return nil, fmt.Errorf("decode report: %w", err)
		}
		reports = []Report{one}
	default:
		return nil, errors.New("body must be a JSON object or array")
	}
	if len(reports) == 0 {
		return nil, errors.New("no reports in body")
	}
	if len(reports) > maxBatch {
		return nil, fmt.Errorf("too many reports: send at most %d per request", maxBatch)
	}
	return reports, nil
}

func (rep Report) toEvent(received time.Time) (*event.Event, error) {
	msg := strings.TrimSpace(rep.Message)
	if msg == "" {
		return nil, errors.New("message is required")
	}
	// Reuse the Sentry parser for the fields both formats share, so levels,
	// timestamps and ids are normalized the same way.
	base, _ := json.Marshal(map[string]any{
		"event_id": rep.EventID, "level": rep.Level, "timestamp": rep.Timestamp,
	})
	ev, err := event.Parse(base, received)
	if err != nil {
		return nil, err
	}
	ev.Platform = rep.Platform
	ev.Environment = rep.Environment
	ev.Release = rep.Release
	ev.ServerName = rep.ServerName
	ev.Logger = rep.Logger
	ev.Transaction = rep.Transaction
	ev.User = rep.User
	ev.Extra = rep.Extra
	ev.Fingerprint = rep.Fingerprint
	ev.SDK = &event.SDK{Name: "burrow.report", Version: "1"}
	for k, v := range rep.Tags {
		ev.Tags = append(ev.Tags, event.Tag{Key: k, Value: v})
	}
	sort.Slice(ev.Tags, func(i, j int) bool { return ev.Tags[i].Key < ev.Tags[j].Key })

	st := event.ParseStackText(rep.Stack)
	if st == nil && rep.Type == "" {
		// A plain message, like a log line.
		ev.Message = msg
		if rep.Stack != "" {
			ev.Extra = withExtra(ev.Extra, "stack", rep.Stack)
		}
		return ev, nil
	}
	typ := rep.Type
	if typ == "" {
		typ = "Error"
	}
	if st != nil {
		msg = event.StripLocationPrefix(msg)
	} else if rep.Stack != "" {
		ev.Extra = withExtra(ev.Extra, "stack", rep.Stack)
	}
	handled := false
	ev.Exceptions = []event.Exception{{
		Type: typ, Value: msg, Stacktrace: st,
		Mechanism: &event.Mechanism{Type: "report", Handled: &handled},
	}}
	return ev, nil
}

func withExtra(m map[string]any, k string, v any) map[string]any {
	if m == nil {
		m = map[string]any{}
	}
	m[k] = v
	return m
}

// authenticateReport checks the project key for the report API. The key may
// come from X-Burrow-Key, Authorization: Bearer, or the key query parameter
// (for clients that cannot set headers).
func (h *Handler) authenticateReport(r *http.Request) (*store.Project, error) {
	pid, err := strconv.ParseInt(r.PathValue("project_id"), 10, 64)
	if err != nil {
		return nil, errBadProject
	}
	key := strings.TrimSpace(r.Header.Get("X-Burrow-Key"))
	if key == "" {
		if v := r.Header.Get("Authorization"); len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
			key = strings.TrimSpace(v[7:])
		}
	}
	if key == "" {
		key = r.URL.Query().Get("key")
	}
	if key == "" {
		return nil, errNoReportKey
	}
	return h.projectWithKey(r, pid, key)
}
