package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mozthewah/opensourcedsentryalternative/internal/store"
)

// -------------------------------------------------------------- auth pages

func (s *Server) setupPage(w http.ResponseWriter, r *http.Request) {
	if n, err := s.Store.UserCount(r.Context()); err != nil {
		s.serverError(w, r, err)
		return
	} else if n > 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "setup", "Set up Burrow", map[string]string{})
}

func (s *Server) setupSubmit(w http.ResponseWriter, r *http.Request) {
	if n, err := s.Store.UserCount(r.Context()); err != nil {
		s.serverError(w, r, err)
		return
	} else if n > 0 {
		http.Error(w, "setup already completed", http.StatusForbidden)
		return
	}
	email, pw, pw2, team := r.PostFormValue("email"), r.PostFormValue("password"), r.PostFormValue("password2"), r.PostFormValue("team")
	fail := func(code int, msg string) {
		s.render(w, r, code, "setup", "Set up Burrow", map[string]string{"Error": msg, "Email": email, "Team": team})
	}
	if pw != pw2 {
		fail(http.StatusBadRequest, "The passwords don't match.")
		return
	}
	u, err := s.Store.Setup(r.Context(), email, pw, team)
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
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
	fail := func(code int, msg string) {
		s.render(w, r, code, "login", "Log in", map[string]string{"Error": msg, "Email": email, "Next": next})
	}
	ek, ik := emailKey(email), "ip:"+clientIP(r)
	if s.logins.blocked(ek, loginLimitPerEmail) || s.logins.blocked(ik, loginLimitPerIP) {
		fail(http.StatusTooManyRequests, "Too many failed attempts. Wait 15 minutes and try again.")
		return
	}
	u, err := s.Store.Authenticate(r.Context(), email, r.PostFormValue("password"))
	if errors.Is(err, store.ErrBadCredentials) {
		s.logins.fail(ek)
		s.logins.fail(ik)
		fail(http.StatusUnauthorized, "That email and password don't match an account.")
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.logins.clear(ek)
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
	sess := sessionFrom(r.Context())
	projects, err := s.Store.ListProjects(r.Context(), sess.User.ID, sess.User.IsAdmin, 0)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	teams, err := s.creatableTeams(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if len(projects) == 0 && len(teams) > 0 {
		http.Redirect(w, r, "/projects/new", http.StatusSeeOther)
		return
	}
	// Group by team so a multi-team user sees structure, not a flat pile.
	type group struct {
		Team     string
		Projects []*store.Project
	}
	var groups []group
	for _, p := range projects {
		if len(groups) == 0 || groups[len(groups)-1].Team != p.TeamName {
			groups = append(groups, group{Team: p.TeamName})
		}
		groups[len(groups)-1].Projects = append(groups[len(groups)-1].Projects, p)
	}
	s.render(w, r, http.StatusOK, "projects", "Projects", map[string]any{
		"Groups":    groups,
		"CanCreate": len(teams) > 0,
		"MultiTeam": len(groups) > 1,
	})
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

// creatableTeams lists the teams the current user may create projects in.
func (s *Server) creatableTeams(r *http.Request) ([]*store.Team, error) {
	sess := sessionFrom(r.Context())
	all, err := s.Store.ListTeams(r.Context(), sess.User.ID, sess.User.IsAdmin)
	if err != nil {
		return nil, err
	}
	var out []*store.Team
	for _, t := range all {
		if sess.User.IsAdmin || t.MyRole == store.RoleOwner {
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *Server) projectNewPage(w http.ResponseWriter, r *http.Request) {
	s.renderProjectNew(w, r, http.StatusOK, "")
}

func (s *Server) renderProjectNew(w http.ResponseWriter, r *http.Request, code int, msg string) {
	teams, err := s.creatableTeams(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, code, "project_new", "New project", map[string]any{
		"Platforms": platforms, "Teams": teams, "Error": msg, "Selected": r.FormValue("team"),
	})
}

func (s *Server) projectCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	platform := r.PostFormValue("platform")
	if name == "" || len(name) > 100 {
		s.renderProjectNew(w, r, http.StatusBadRequest, "Give the project a name (100 characters max).")
		return
	}
	team, err := s.Store.TeamBySlug(r.Context(), r.PostFormValue("team"))
	if errors.Is(err, store.ErrNotFound) {
		s.renderProjectNew(w, r, http.StatusBadRequest, "Pick a team for the project.")
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	a, err := s.teamAccess(r, team.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if !s.require(w, r, a, accessOwner) {
		return
	}
	p, err := s.Store.CreateProject(r.Context(), team.ID, name, platform)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/projects/"+p.Slug+"/settings?flash=created", http.StatusSeeOther)
}

func (s *Server) projectSettings(w http.ResponseWriter, r *http.Request) {
	p, a, ok := s.loadProject(w, r, accessMember)
	if !ok {
		return
	}
	team, err := s.Store.TeamByID(r.Context(), p.TeamID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "project_settings", p.Name+" settings", map[string]any{
		"Project":   p,
		"Team":      team,
		"DSN":       s.DSN(p),
		"CanManage": a >= accessOwner,
	})
}

func (s *Server) projectTestEvent(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.loadProject(w, r, accessMember)
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
	p, _, ok := s.loadProject(w, r, accessOwner)
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
	p, _, ok := s.loadProject(w, r, accessOwner)
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
	p, a, ok := s.loadProject(w, r, accessMember)
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
	issues, err := s.Store.ListIssues(r.Context(), p.ID, store.IssueFilter{
		Status: status,
		Query:  q.Get("q"),
		Sort:   sort,
		Limit:  issuesPerPage + 1, // one extra to know if there's a next page
		Offset: (page - 1) * issuesPerPage,
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	hasNext := len(issues) > issuesPerPage
	if hasNext {
		issues = issues[:issuesPerPage]
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
		"Project":   p,
		"Issues":    issues,
		"Status":    statusParam,
		"Query":     q.Get("q"),
		"Sort":      sort,
		"Page":      page,
		"CanManage": a >= accessOwner,
	}
	if hasNext {
		data["NextURL"] = pageURL(page + 1)
	}
	if page > 1 {
		data["PrevURL"] = pageURL(page - 1)
	}
	s.render(w, r, http.StatusOK, "issues", p.Name, data)
}

func (s *Server) issueDetail(w http.ResponseWriter, r *http.Request) {
	issue, p, a, ok := s.loadIssue(w, r, accessMember)
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
	data := map[string]any{"Issue": issue, "Project": p, "Stored": se, "CanManage": a >= accessOwner}
	if se != nil {
		older, newer, err := s.Store.AdjacentEvents(r.Context(), se)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		data["Event"] = se.Event
		data["Older"], data["Newer"] = older, newer
	}
	recent, err := s.Store.ListIssueEvents(r.Context(), issue.ID, 15)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	daily, err := s.Store.DailyCounts(r.Context(), issue.ID, 14, time.Now())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data["Recent"] = recent
	data["Daily"] = daily
	s.render(w, r, http.StatusOK, "issue", issue.Title, data)
}

func (s *Server) issueSetStatus(w http.ResponseWriter, r *http.Request) {
	issue, _, _, ok := s.loadIssue(w, r, accessMember)
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
	issue, p, _, ok := s.loadIssue(w, r, accessOwner)
	if !ok {
		return
	}
	if err := s.Store.DeleteIssue(r.Context(), issue.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/projects/"+p.Slug, http.StatusSeeOther)
}
