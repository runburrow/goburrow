package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mozthewah/opensourcedsentryalternative/internal/event"
	"github.com/mozthewah/opensourcedsentryalternative/internal/store"
)

// ------------------------------------------------------------ view models

// IssueView wraps store.Issue with 14-day activity barcode fields and 1-based
// track numbering for the Night Tape issues list and issue detail views.
// Embedding *store.Issue keeps all existing template expressions (.ID, .Title,
// .Culprit, .Level, .Status, .FirstSeen, .LastSeen, .TimesSeen) working.
type IssueView struct {
	*store.Issue
	TrackLabel      string                     // zero-padded 2-digit track label ("01", "02", ...)
	Activity        [store.ActivityDays]int    // 14 daily event counts, oldest->newest (index 13 = today UTC)
	Bars            [store.ActivityDays]string // 14 height classes ("h0", "h5", ..., "h100") matching barHeight
	ActivityTotal   int                        // sum of events across the 14-day window
	ActivitySummary string                     // accessible summary for barcode aria-label
}

// ProjectView wraps store.Project with 14-day activity barcode fields and
// 1-based track numbering for the Night Tape projects page.
// Embedding *store.Project keeps all existing template expressions (.ID, .Name,
// .Slug, .Platform, .PublicKey, .CreatedAt, .UnresolvedCount, .LastSeen) working.
type ProjectView struct {
	*store.Project
	TrackLabel      string                     // zero-padded 2-digit track label ("01", "02", ...)
	Activity        [store.ActivityDays]int    // 14 daily event counts across all issues in project, oldest->newest
	Bars            [store.ActivityDays]string // 14 height classes ("h0", "h5", ..., "h100")
	ActivityTotal   int                        // sum of events across the 14-day window
	ActivitySummary string                     // accessible summary for barcode aria-label
}

func buildActivityFields(counts []int) (act [store.ActivityDays]int, bars [store.ActivityDays]string, total int, summary string) {
	peak := 0
	for i := 0; i < store.ActivityDays && i < len(counts); i++ {
		c := counts[i]
		act[i] = c
		total += c
		if c > peak {
			peak = c
		}
	}
	for i, c := range act {
		bars[i] = barHeight(c, peak)
	}
	summary = activitySummary(act)
	return act, bars, total, summary
}

// -------------------------------------------------------------- auth pages

func (s *Server) setupPage(w http.ResponseWriter, r *http.Request) {
	if n, err := s.Store.UserCount(r.Context()); err != nil {
		s.serverError(w, r, err)
		return
	} else if n > 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "setup", "Welcome to Burrow", map[string]string{})
}

func (s *Server) setupSubmit(w http.ResponseWriter, r *http.Request) {
	// Only the very first account can be created this way. There's a small
	// race if two people submit setup simultaneously; the UNIQUE email and
	// the count check make the worst case "two admins", which is acceptable
	// for a first-run screen.
	if n, err := s.Store.UserCount(r.Context()); err != nil {
		s.serverError(w, r, err)
		return
	} else if n > 0 {
		http.Error(w, "setup already completed", http.StatusForbidden)
		return
	}
	email, pw, pw2 := r.PostFormValue("email"), r.PostFormValue("password"), r.PostFormValue("password2")
	if pw != pw2 {
		s.render(w, r, http.StatusBadRequest, "setup", "Welcome to Burrow", map[string]string{"Error": "Passwords don't match.", "Email": email})
		return
	}
	u, err := s.Store.CreateUser(r.Context(), email, pw)
	if err != nil {
		s.render(w, r, http.StatusBadRequest, "setup", "Welcome to Burrow", map[string]string{"Error": err.Error(), "Email": email})
		return
	}
	s.startSession(w, r, u.ID, "/projects/new")
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if n, err := s.Store.UserCount(r.Context()); err == nil && n == 0 {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "login", "Log in", map[string]string{"Next": r.URL.Query().Get("next")})
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	email := r.PostFormValue("email")
	next := r.PostFormValue("next")
	u, err := s.Store.Authenticate(r.Context(), email, r.PostFormValue("password"))
	if errors.Is(err, store.ErrBadCredentials) {
		s.render(w, r, http.StatusUnauthorized, "login", "Log in", map[string]string{"Error": err.Error(), "Email": email, "Next": next})
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.startSession(w, r, u.ID, safeNext(next))
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, userID int64, dest string) {
	sess, err := s.Store.CreateSession(r.Context(), userID, sessionTTL)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.setSessionCookie(w, sess)
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// safeNext only allows local redirects, preventing open-redirect abuse.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, `/\`) {
		return "/"
	}
	return next
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if sess := sessionFrom(r.Context()); sess != nil {
		_ = s.Store.DeleteSession(r.Context(), sess.Token)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ---------------------------------------------------------------- projects

func (s *Server) projectList(w http.ResponseWriter, r *http.Request) {
	projects, err := s.Store.ListProjects(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if len(projects) == 0 {
		http.Redirect(w, r, "/projects/new", http.StatusSeeOther)
		return
	}
	pids := make([]int64, len(projects))
	for i, p := range projects {
		pids[i] = p.ID
	}
	actMap, err := s.Store.ProjectActivity(r.Context(), pids, store.ActivityDays, s.now())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	views := make([]ProjectView, len(projects))
	for i, p := range projects {
		act, bars, total, summary := buildActivityFields(actMap[p.ID])
		views[i] = ProjectView{
			Project:         p,
			TrackLabel:      trackNumber(i + 1),
			Activity:        act,
			Bars:            bars,
			ActivityTotal:   total,
			ActivitySummary: summary,
		}
	}
	s.render(w, r, http.StatusOK, "projects", "Projects", views)
}

var platforms = []struct{ ID, Name string }{
	{"javascript", "JavaScript (Browser)"},
	{"node", "Node.js"},
	{"python", "Python"},
	{"go", "Go"},
	{"ruby", "Ruby"},
	{"php", "PHP"},
	{"java", "Java / Kotlin"},
	{"dotnet", ".NET"},
	{"rust", "Rust"},
	{"other", "Other"},
}

func (s *Server) projectNewPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "project_new", "New project", map[string]any{"Platforms": platforms})
}

func (s *Server) projectCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	platform := r.PostFormValue("platform")
	if name == "" || len(name) > 100 {
		s.render(w, r, http.StatusBadRequest, "project_new", "New project", map[string]any{"Platforms": platforms, "Error": "Name is required (max 100 characters)."})
		return
	}
	p, err := s.Store.CreateProject(r.Context(), name, platform)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/projects/"+p.Slug+"/settings?flash=created", http.StatusSeeOther)
}

func (s *Server) loadProject(w http.ResponseWriter, r *http.Request) (*store.Project, bool) {
	p, err := s.Store.ProjectBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return nil, false
	} else if err != nil {
		s.serverError(w, r, err)
		return nil, false
	}
	return p, true
}

func (s *Server) projectSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	s.render(w, r, http.StatusOK, "project_settings", p.Name+" · Settings", map[string]any{
		"Project": p,
		"DSN":     s.DSN(p),
	})
}

func (s *Server) projectTestEvent(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	if err := s.Ingest.Ingest(r.Context(), p, sampleEvent()); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/projects/"+p.Slug+"?flash=test-sent", http.StatusSeeOther)
}

func (s *Server) projectRotateKey(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	if err := s.Store.RotateProjectKey(r.Context(), p.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/projects/"+p.Slug+"/settings?flash=rotated", http.StatusSeeOther)
}

func (s *Server) projectDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	if r.PostFormValue("confirm") != p.Slug {
		http.Redirect(w, r, "/projects/"+p.Slug+"/settings?flash=delete-confirm", http.StatusSeeOther)
		return
	}
	if err := s.Store.DeleteProject(r.Context(), p.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ------------------------------------------------------------------ issues

const issuesPerPage = 50

func (s *Server) issueList(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	status := store.IssueStatus(q.Get("status"))
	if q.Get("status") == "" {
		status = store.StatusUnresolved
	} else if q.Get("status") == "all" {
		status = ""
	} else if !status.Valid() {
		status = store.StatusUnresolved
	}
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	sort := q.Get("sort")
	offset := (page - 1) * issuesPerPage
	issues, err := s.Store.ListIssues(r.Context(), p.ID, store.IssueFilter{
		Status: status,
		Query:  q.Get("q"),
		Sort:   sort,
		Limit:  issuesPerPage + 1, // one extra to know if there's a next page
		Offset: offset,
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	hasNext := len(issues) > issuesPerPage
	if hasNext {
		issues = issues[:issuesPerPage]
	}

	issueIDs := make([]int64, len(issues))
	for i, iss := range issues {
		issueIDs[i] = iss.ID
	}
	actMap, err := s.Store.IssueActivity(r.Context(), issueIDs, store.ActivityDays, s.now())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	counts, err := s.Store.IssueStatusCounts(r.Context(), p.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	p.UnresolvedCount = counts.Unresolved

	issueViews := make([]IssueView, len(issues))
	for i, iss := range issues {
		act, bars, total, summary := buildActivityFields(actMap[iss.ID])
		issueViews[i] = IssueView{
			Issue:           iss,
			TrackLabel:      trackNumber(offset + i + 1),
			Activity:        act,
			Bars:            bars,
			ActivityTotal:   total,
			ActivitySummary: summary,
		}
	}

	statusParam := string(status)
	if statusParam == "" {
		statusParam = "all"
	}
	pageURL := func(n int) string {
		v := url.Values{}
		v.Set("status", statusParam)
		if q.Get("q") != "" {
			v.Set("q", q.Get("q"))
		}
		if sort != "" {
			v.Set("sort", sort)
		}
		v.Set("page", strconv.Itoa(n))
		return "/projects/" + p.Slug + "?" + v.Encode()
	}
	data := map[string]any{
		"Project":    p,
		"Issues":     issueViews,
		"Status":     statusParam,
		"Query":      q.Get("q"),
		"Sort":       sort,
		"Page":       page,
		"Counts":     counts,
		"DayLabels":  dayLabels(),
		"TotalShown": len(issueViews),
	}
	if hasNext {
		data["NextURL"] = pageURL(page + 1)
	}
	if page > 1 {
		data["PrevURL"] = pageURL(page - 1)
	}
	s.render(w, r, http.StatusOK, "issues", p.Name, data)
}

func (s *Server) loadIssue(w http.ResponseWriter, r *http.Request) (*store.Issue, *store.Project, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return nil, nil, false
	}
	issue, err := s.Store.IssueByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return nil, nil, false
	} else if err != nil {
		s.serverError(w, r, err)
		return nil, nil, false
	}
	p, err := s.Store.ProjectByID(r.Context(), issue.ProjectID)
	if err != nil {
		s.serverError(w, r, err)
		return nil, nil, false
	}
	return issue, p, true
}

func (s *Server) issueDetail(w http.ResponseWriter, r *http.Request) {
	issue, p, ok := s.loadIssue(w, r)
	if !ok {
		return
	}
	evID, _ := strconv.ParseInt(r.URL.Query().Get("event"), 10, 64)
	se, err := s.Store.EventForIssue(r.Context(), issue.ID, evID)
	if errors.Is(err, store.ErrNotFound) {
		se = nil // issue exists but its events were pruned
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	recent, err := s.Store.ListIssueEvents(r.Context(), issue.ID, 15)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	daily, err := s.Store.DailyCounts(r.Context(), issue.ID, store.ActivityDays, s.now())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	act, bars, actTotal, actSummary := buildActivityFields(daily)
	iv := &IssueView{
		Issue:           issue,
		TrackLabel:      "01",
		Activity:        act,
		Bars:            bars,
		ActivityTotal:   actTotal,
		ActivitySummary: actSummary,
	}
	data := map[string]any{
		"Issue":           iv,
		"Project":         p,
		"Recent":          recent,
		"Activity":        act,
		"Bars":            bars,
		"ActivityTotal":   actTotal,
		"ActivitySummary": actSummary,
		"DayLabels":       dayLabels(),
	}
	if se != nil {
		older, newer, err := s.Store.AdjacentEvents(r.Context(), se)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		data["Event"] = se.Event
		data["Older"], data["Newer"] = older, newer
	}
	s.render(w, r, http.StatusOK, "issue", issue.Title, data)
}

func (s *Server) issueSetStatus(w http.ResponseWriter, r *http.Request) {
	issue, _, ok := s.loadIssue(w, r)
	if !ok {
		return
	}
	st := store.IssueStatus(r.PostFormValue("status"))
	if !st.Valid() {
		http.Error(w, "invalid status", http.StatusBadRequest)
		return
	}
	if err := s.Store.SetIssueStatus(r.Context(), issue.ID, st); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/issues/"+strconv.FormatInt(issue.ID, 10), http.StatusSeeOther)
}

func (s *Server) issueDelete(w http.ResponseWriter, r *http.Request) {
	issue, p, ok := s.loadIssue(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteIssue(r.Context(), issue.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/projects/"+p.Slug, http.StatusSeeOther)
}

// sampleEvent is what "Send test event" ingests: realistic enough to show
// off every panel of the issue page.
func sampleEvent() *event.Event {
	yes, no := true, false
	now := time.Now().UTC()
	return &event.Event{
		EventID:     event.NewEventID(),
		Timestamp:   now,
		Platform:    "python",
		Level:       "error",
		Environment: "production",
		Release:     "burrow-demo@1.0.0",
		ServerName:  "demo-host-1",
		Transaction: "POST /api/checkout",
		Exceptions: []event.Exception{{
			Type:      "ZeroDivisionError",
			Value:     "division by zero",
			Module:    "builtins",
			Mechanism: &event.Mechanism{Type: "generic", Handled: &no},
			Stacktrace: &event.Stacktrace{Frames: []event.Frame{
				{Filename: "flask/app.py", Function: "dispatch_request", Lineno: 1469, Module: "flask.app", InApp: &no,
					ContextLine: "        return self.ensure_sync(self.view_functions[rule.endpoint])(**view_args)"},
				{Filename: "shop/views.py", Function: "checkout", Lineno: 42, Module: "shop.views", InApp: &yes,
					PreContext:  []string{"@app.post(\"/api/checkout\")", "def checkout():", "    cart = load_cart(request.user)"},
					ContextLine: "    per_item = cart.total / cart.item_count",
					PostContext: []string{"    return charge(cart, per_item)", ""},
					Vars:        map[string]any{"cart": "<Cart id=1337 items=0>"}},
			}},
		}},
		Breadcrumbs: []event.Breadcrumb{
			{Timestamp: now.Add(-3 * time.Second), Category: "navigation", Message: "/cart -> /checkout", Level: "info"},
			{Timestamp: now.Add(-2 * time.Second), Category: "http", Type: "http", Level: "info", Data: map[string]any{"method": "POST", "url": "/api/checkout", "status_code": 500}},
			{Timestamp: now.Add(-1 * time.Second), Category: "query", Message: "SELECT * FROM cart_items WHERE cart_id = 1337", Level: "info"},
		},
		Tags: []event.Tag{{Key: "browser", Value: "Firefox 132"}, {Key: "os", Value: "Linux"}, {Key: "burrow.test", Value: "true"}},
		User: map[string]any{"id": "42", "email": "fox@example.com", "ip_address": "203.0.113.7"},
		Request: map[string]any{
			"method": "POST", "url": "https://shop.example.com/api/checkout",
			"headers": map[string]any{"User-Agent": "Mozilla/5.0 (X11; Linux x86_64; rv:132.0) Firefox/132.0"},
		},
		Contexts: map[string]any{
			"runtime": map[string]any{"name": "CPython", "version": "3.13.0"},
			"os":      map[string]any{"name": "Linux", "version": "6.8"},
		},
		SDK: &event.SDK{Name: "burrow.test-event", Version: "1"},
	}
}
