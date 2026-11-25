package web

// The read-only JSON API under /api/v1, for dashboards (Grafana's Infinity
// data source, for one) and scripts. Requests carry an API token made on
// the Account page as "Authorization: Bearer brw_...". A token acts as its
// user without Instance Admin, so it sees exactly the projects of that
// user's teams. Nothing here changes data. /api/v1/openapi.json describes it.

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/runburrow/goburrow/internal/store"
)

//go:embed openapi.json
var openAPISpec []byte

// apiMaxDays bounds activity queries; older events are usually pruned anyway.
const apiMaxDays = 90

func (s *Server) registerAPI(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/openapi.json", s.apiSpec)
	mux.HandleFunc("GET /api/v1/projects", s.apiAuthed(s.apiProjects))
	mux.HandleFunc("GET /api/v1/projects/{slug}", s.apiAuthed(s.apiProject))
	mux.HandleFunc("GET /api/v1/projects/{slug}/issues", s.apiAuthed(s.apiIssues))
	mux.HandleFunc("GET /api/v1/projects/{slug}/activity", s.apiAuthed(s.apiProjectActivity))
	mux.HandleFunc("GET /api/v1/activity", s.apiAuthed(s.apiActivity))
	mux.HandleFunc("GET /api/v1/issues/{id}", s.apiAuthed(s.apiIssue))
}

type apiUserKey struct{}

func apiUser(r *http.Request) *store.User {
	u, _ := r.Context().Value(apiUserKey{}).(*store.User)
	return u
}

func (s *Server) apiAuthed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || strings.TrimSpace(secret) == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="burrow"`)
			apiError(w, http.StatusUnauthorized, "Send an API token as \"Authorization: Bearer <token>\". Make one on your Account page.")
			return
		}
		u, err := s.Store.UserByAPIToken(r.Context(), strings.TrimSpace(secret))
		if errors.Is(err, store.ErrNotFound) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="burrow", error="invalid_token"`)
			apiError(w, http.StatusUnauthorized, "That API token isn't valid. It may have been revoked.")
			return
		} else if err != nil {
			s.apiServerError(w, r, err)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), apiUserKey{}, u)))
	}
}

// ---------------------------------------------------------------- responses

type apiProjectJSON struct {
	ID         int64      `json:"id"`
	Slug       string     `json:"slug"`
	Name       string     `json:"name"`
	Platform   string     `json:"platform"`
	Team       string     `json:"team"`
	Unresolved int        `json:"unresolved"`
	LastSeen   *time.Time `json:"last_seen"`
	CreatedAt  time.Time  `json:"created_at"`
	URL        string     `json:"url"`
}

type apiProjectDetailJSON struct {
	apiProjectJSON
	Issues apiStatusCountsJSON `json:"issues"`
}

type apiStatusCountsJSON struct {
	Unresolved int `json:"unresolved"`
	Resolved   int `json:"resolved"`
	Ignored    int `json:"ignored"`
	All        int `json:"all"`
}

type apiIssueJSON struct {
	ID        int64     `json:"id"`
	Project   string    `json:"project"`
	Title     string    `json:"title"`
	Culprit   string    `json:"culprit"`
	Level     string    `json:"level"`
	Status    string    `json:"status"`
	Regressed bool      `json:"regressed"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	TimesSeen int       `json:"times_seen"`
	URL       string    `json:"url"`
}

type apiIssueDetailJSON struct {
	apiIssueJSON
	Activity []apiDayJSON `json:"activity"`
}

type apiDayJSON struct {
	Date    string    `json:"date"` // UTC day, YYYY-MM-DD
	Time    time.Time `json:"time"` // start of that day, for time-series tools
	Project string    `json:"project,omitempty"`
	Events  int       `json:"events"`
}

func (s *Server) projectJSON(p *store.Project, teamName string) apiProjectJSON {
	out := apiProjectJSON{ID: p.ID, Slug: p.Slug, Name: p.Name, Platform: p.Platform, Team: teamName,
		Unresolved: p.UnresolvedCount, CreatedAt: p.CreatedAt, URL: s.origin() + "/projects/" + p.Slug}
	if !p.LastSeen.IsZero() {
		t := p.LastSeen
		out.LastSeen = &t
	}
	return out
}

func (s *Server) issueJSON(i *store.Issue, projectSlug string) apiIssueJSON {
	return apiIssueJSON{ID: i.ID, Project: projectSlug, Title: i.Title, Culprit: i.Culprit, Level: i.Level,
		Status: string(i.Status), Regressed: i.Regressed, FirstSeen: i.FirstSeen, LastSeen: i.LastSeen,
		TimesSeen: i.TimesSeen, URL: s.origin() + "/issues/" + strconv.FormatInt(i.ID, 10)}
}

// days turns oldest-first daily counts ending today into dated rows.
func (s *Server) days(counts []int, project string) []apiDayJSON {
	start := s.now().Truncate(24*time.Hour).AddDate(0, 0, -(len(counts) - 1))
	out := make([]apiDayJSON, len(counts))
	for i, n := range counts {
		d := start.AddDate(0, 0, i)
		out[i] = apiDayJSON{Date: d.Format(time.DateOnly), Time: d, Project: project, Events: n}
	}
	return out
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func apiError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) apiServerError(w http.ResponseWriter, r *http.Request, err error) {
	s.Log.Error("api request failed", "path", r.URL.Path, "err", err)
	apiError(w, http.StatusInternalServerError, "Something went wrong on our end. Check the server logs.")
}

func (s *Server) apiNotFound(w http.ResponseWriter, r *http.Request) {
	apiError(w, http.StatusNotFound, "No such API endpoint. See /api/v1/openapi.json.")
}

// ---------------------------------------------------------------- access

// apiLoadProject loads the project named in the path if the token's user is in
// its team. Outsiders get the same 404 as a missing project.
func (s *Server) apiLoadProject(w http.ResponseWriter, r *http.Request) (*store.Project, bool) {
	p, err := s.Store.ProjectBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "No project with that slug, or your token can't see it.")
		return nil, false
	} else if err != nil {
		s.apiServerError(w, r, err)
		return nil, false
	}
	if ok, err := s.apiCanSee(r, p); err != nil {
		s.apiServerError(w, r, err)
		return nil, false
	} else if !ok {
		apiError(w, http.StatusNotFound, "No project with that slug, or your token can't see it.")
		return nil, false
	}
	return p, true
}

func (s *Server) apiCanSee(r *http.Request, p *store.Project) (bool, error) {
	if p.TeamID == 0 {
		return false, nil // teamless projects are for Instance Admins only
	}
	role, err := s.Store.TeamRole(r.Context(), p.TeamID, apiUser(r).ID)
	return role != "", err
}

// visibleProjects is every project the token's user can see, with team
// names and counts filled in.
func (s *Server) visibleProjects(r *http.Request) ([]*store.Project, error) {
	return s.Store.ListProjects(r.Context(), apiUser(r).ID, false, 0)
}

// ---------------------------------------------------------------- handlers

func (s *Server) apiSpec(w http.ResponseWriter, r *http.Request) {
	var spec map[string]any
	if err := json.Unmarshal(openAPISpec, &spec); err != nil {
		s.apiServerError(w, r, err)
		return
	}
	if o := s.origin(); o != "" {
		spec["servers"] = []map[string]string{{"url": o + "/api/v1", "description": "This Burrow"}}
	}
	if info, ok := spec["info"].(map[string]any); ok && s.Version != "" {
		info["version"] = s.Version
	}
	w.Header().Set("Access-Control-Allow-Origin", "*") // public and harmless; lets browser API explorers load it
	writeJSON(w, http.StatusOK, spec)
}

func (s *Server) apiProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.visibleProjects(r)
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	out := make([]apiProjectJSON, 0, len(projects))
	for _, p := range projects {
		out = append(out, s.projectJSON(p, p.TeamName))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) apiProject(w http.ResponseWriter, r *http.Request) {
	p, ok := s.apiLoadProject(w, r)
	if !ok {
		return
	}
	// ListProjects fills in the team name and counts.
	all, err := s.Store.ListProjects(r.Context(), apiUser(r).ID, false, p.TeamID)
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	for _, q := range all {
		if q.ID == p.ID {
			p = q
		}
	}
	sc, err := s.Store.IssueStatusCounts(r.Context(), p.ID)
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apiProjectDetailJSON{
		apiProjectJSON: s.projectJSON(p, p.TeamName),
		Issues:         apiStatusCountsJSON{Unresolved: sc.Unresolved, Resolved: sc.Resolved, Ignored: sc.Ignored, All: sc.All},
	})
}

func (s *Server) apiIssues(w http.ResponseWriter, r *http.Request) {
	p, ok := s.apiLoadProject(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := store.IssueFilter{Query: q.Get("query"), Sort: q.Get("sort")}
	if st := q.Get("status"); st != "" && st != "all" {
		f.Status = store.IssueStatus(st)
		if !f.Status.Valid() {
			apiError(w, http.StatusBadRequest, "status must be unresolved, resolved, ignored or all.")
			return
		}
	}
	switch f.Sort {
	case "", "last_seen", "first_seen", "times_seen":
	default:
		apiError(w, http.StatusBadRequest, "sort must be last_seen, first_seen or times_seen.")
		return
	}
	for _, l := range strings.Split(q.Get("level"), ",") {
		if l = strings.TrimSpace(l); l != "" {
			f.Levels = append(f.Levels, l)
		}
	}
	var bad bool
	f.Limit, bad = intParam(q.Get("limit"), 50, 1, 200)
	if bad {
		apiError(w, http.StatusBadRequest, "limit must be a number from 1 to 200.")
		return
	}
	f.Offset, bad = intParam(q.Get("offset"), 0, 0, 1<<31-1)
	if bad {
		apiError(w, http.StatusBadRequest, "offset must be a number, 0 or more.")
		return
	}
	issues, err := s.Store.ListIssues(r.Context(), p.ID, f)
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	out := make([]apiIssueJSON, 0, len(issues))
	for _, i := range issues {
		out = append(out, s.issueJSON(i, p.Slug))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) apiIssue(w http.ResponseWriter, r *http.Request) {
	notFound := func() { apiError(w, http.StatusNotFound, "No issue with that id, or your token can't see it.") }
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		notFound()
		return
	}
	issue, err := s.Store.IssueByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		notFound()
		return
	} else if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	p, err := s.Store.ProjectByID(r.Context(), issue.ProjectID)
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	if ok, err := s.apiCanSee(r, p); err != nil {
		s.apiServerError(w, r, err)
		return
	} else if !ok {
		notFound()
		return
	}
	days, bad := intParam(r.URL.Query().Get("days"), store.ActivityDays, 1, apiMaxDays)
	if bad {
		apiError(w, http.StatusBadRequest, "days must be a number from 1 to 90.")
		return
	}
	counts, err := s.Store.DailyCounts(r.Context(), issue.ID, days, s.now())
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apiIssueDetailJSON{apiIssueJSON: s.issueJSON(issue, p.Slug), Activity: s.days(counts, "")})
}

func (s *Server) apiProjectActivity(w http.ResponseWriter, r *http.Request) {
	p, ok := s.apiLoadProject(w, r)
	if !ok {
		return
	}
	s.writeActivity(w, r, []*store.Project{p})
}

func (s *Server) apiActivity(w http.ResponseWriter, r *http.Request) {
	projects, err := s.visibleProjects(r)
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	s.writeActivity(w, r, projects)
}

// writeActivity answers with one row per project per UTC day, oldest
// first: the long format Grafana groups into one series per project.
func (s *Server) writeActivity(w http.ResponseWriter, r *http.Request, projects []*store.Project) {
	days, bad := intParam(r.URL.Query().Get("days"), store.ActivityDays, 1, apiMaxDays)
	if bad {
		apiError(w, http.StatusBadRequest, "days must be a number from 1 to 90.")
		return
	}
	ids := make([]int64, len(projects))
	for i, p := range projects {
		ids[i] = p.ID
	}
	counts, err := s.Store.ProjectActivity(r.Context(), ids, days, s.now())
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	perProject := make([][]apiDayJSON, len(projects))
	for i, p := range projects {
		perProject[i] = s.days(counts[p.ID], p.Slug)
	}
	out := make([]apiDayJSON, 0, len(projects)*days)
	for d := 0; d < days; d++ {
		for i := range projects {
			out = append(out, perProject[i][d])
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// intParam parses an optional whole-number query parameter. bad reports a
// value that is present but not a number within [lo, hi].
func intParam(v string, def, lo, hi int) (n int, bad bool) {
	if v == "" {
		return def, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		return def, true
	}
	return n, false
}
