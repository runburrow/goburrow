package web

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/runburrow/goburrow/internal/event"
	"github.com/runburrow/goburrow/internal/notify"
	"github.com/runburrow/goburrow/internal/store"
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
	ActivityFrom    string                     // first day of the window, e.g. "Oct 25"
}

// activityFrom labels the oldest day of the activity window ("Oct 25"), so the
// barcode axis reads as dates instead of day indices.
func activityFrom(now time.Time) string {
	return now.UTC().AddDate(0, 0, -(store.ActivityDays - 1)).Format("Jan 2")
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
	// Only the very first account can be created this way; Setup re-checks
	// inside its transaction, so simultaneous submissions can't both win.
	// The first account is an instance admin and owns the first team.
	if n, err := s.Store.UserCount(r.Context()); err != nil {
		s.serverError(w, r, err)
		return
	} else if n > 0 {
		http.Error(w, "setup already completed", http.StatusForbidden)
		return
	}
	email, pw, pw2, team := r.PostFormValue("email"), r.PostFormValue("password"), r.PostFormValue("password2"), r.PostFormValue("team")
	fail := func(msg string) {
		s.render(w, r, http.StatusBadRequest, "setup", "Welcome to Burrow", map[string]string{"Error": msg, "Email": email, "Team": team})
	}
	if pw != pw2 {
		fail("Passwords don't match.")
		return
	}
	u, err := s.Store.Setup(r.Context(), email, pw, team)
	if errors.Is(err, store.ErrSetupDone) {
		http.Error(w, "setup already completed", http.StatusForbidden)
		return
	} else if err != nil {
		fail(err.Error())
		return
	}
	s.startSession(w, r, u.ID, "/projects/new")
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if n, err := s.Store.UserCount(r.Context()); err == nil && n == 0 {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		if _, err := s.Store.SessionByToken(r.Context(), c.Value); err == nil {
			http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
			return
		}
	}
	s.render(w, r, http.StatusOK, "login", "Log in", map[string]string{"Next": r.URL.Query().Get("next")})
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	email := r.PostFormValue("email")
	next := r.PostFormValue("next")
	keys := throttleKeys(r, email, s.BehindProxy)
	if blocked, wait := s.throttle.blocked(keys); blocked {
		mins := int(math.Ceil(wait.Minutes()))
		if mins < 1 {
			mins = 1
		}
		s.render(w, r, http.StatusTooManyRequests, "login", "Log in", map[string]string{
			"Error": fmt.Sprintf("Too many failed attempts. Try again in %d minute%s.", mins, map[bool]string{true: "", false: "s"}[mins == 1]), "Email": email, "Next": next})
		return
	}
	u, err := s.Store.Authenticate(r.Context(), email, r.PostFormValue("password"))
	if errors.Is(err, store.ErrBadCredentials) {
		s.throttle.fail(keys)
		s.render(w, r, http.StatusUnauthorized, "login", "Log in", map[string]string{"Error": "That email and password don't match. Check both and try again.", "Email": email, "Next": next})
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.throttle.success(keys)
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

// projectGroup is one team's projects on the projects page.
type projectGroup struct {
	Team     string
	Projects []ProjectView
}

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
	if len(projects) == 0 {
		if len(teams) > 0 {
			http.Redirect(w, r, "/projects/new", http.StatusSeeOther)
			return
		}
		// A member of teams with no projects yet: nothing to list, and
		// nothing they may create. The page says so.
		s.render(w, r, http.StatusOK, "projects", "Projects", map[string]any{"Groups": []projectGroup{}, "CanCreate": false})
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
	// Grouped by team (ListProjects sorts by team) so someone in several
	// teams sees structure, not a flat pile.
	var groups []projectGroup
	for i, p := range projects {
		act, bars, total, summary := buildActivityFields(actMap[p.ID])
		v := ProjectView{
			Project:         p,
			TrackLabel:      trackNumber(i + 1),
			Activity:        act,
			Bars:            bars,
			ActivityTotal:   total,
			ActivitySummary: summary,
			ActivityFrom:    activityFrom(s.now()),
		}
		if len(groups) == 0 || groups[len(groups)-1].Team != p.TeamName {
			groups = append(groups, projectGroup{Team: p.TeamName})
		}
		groups[len(groups)-1].Projects = append(groups[len(groups)-1].Projects, v)
	}
	s.render(w, r, http.StatusOK, "projects", "Projects", map[string]any{
		"Groups":       groups,
		"Count":        len(projects),
		"CanCreate":    len(teams) > 0,
		"MultiTeam":    len(groups) > 1,
		"ActivityFrom": activityFrom(s.now()),
	})
}

// creatableTeams lists the teams the current user may create projects in:
// the ones they own, or all of them for instance admins.
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
		"Groups": platformGroups, "Teams": teams, "Error": msg, "Selected": r.FormValue("team"),
	})
}

func (s *Server) projectCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	platform := r.PostFormValue("platform")
	if name == "" || len(name) > 100 {
		s.renderProjectNew(w, r, http.StatusBadRequest, "Name is required (max 100 characters).")
		return
	}
	// With only one team to choose from the form has no picker, so an
	// empty choice means that team.
	slug := r.PostFormValue("team")
	if slug == "" {
		if teams, err := s.creatableTeams(r); err == nil && len(teams) == 1 {
			slug = teams[0].Slug
		}
	}
	team, err := s.Store.TeamBySlug(r.Context(), slug)
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
	// Land on the issue list: its first-run state has the DSN, a test-event
	// button, and waits for the first event to arrive.
	http.Redirect(w, r, "/projects/"+p.Slug+"?flash=created", http.StatusSeeOther)
}

func (s *Server) projectSettings(w http.ResponseWriter, r *http.Request) {
	p, a, ok := s.loadProject(w, r, accessMember)
	if !ok {
		return
	}
	hooks, err := s.Store.ListWebhooks(r.Context(), p.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	setup := setupFor(p.Platform, s.DSN(p), s.ReportURL(p), p.PublicKey)
	s.render(w, r, http.StatusOK, "project_settings", p.Name+" · Settings", map[string]any{
		"Project":      p,
		"CanManage":    a >= accessOwner,
		"DSN":          s.DSN(p),
		"ReportURL":    s.ReportURL(p),
		"Setup":        setup,
		"Reach":        reachWarning(s.BaseURL, setup.Reach, platformName(p.Platform)),
		"PlatformName": platformName(p.Platform),
		"Webhooks":     hooks,
		"MaxWebhooks":  store.MaxWebhooksPerProject,
		"Fallback":     s.Notify != nil && s.Notify.FallbackURL != "",
		"TestResult":   testResultMessage(r.URL.Query().Get("result")),
	})
}

// projectSetupDownload serves a setup step as a file, for code too long to
// copy comfortably (the Roblox module).
func (s *Server) projectSetupDownload(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.loadProject(w, r, accessMember)
	if !ok {
		return
	}
	for _, st := range setupFor(p.Platform, s.DSN(p), s.ReportURL(p), p.PublicKey).Steps {
		if st.Download != "" && st.Download == r.PathValue("file") {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="`+st.Download+`"`)
			w.Header().Set("Cache-Control", "no-store") // it contains the project key
			_, _ = w.Write([]byte(st.Code))
			return
		}
	}
	s.notFound(w, r)
}

// reachWarning checks the server's public address against where a
// platform's code runs, so a setup that can't work says so on the page
// instead of failing silently. It returns nil when there's nothing to say.
func reachWarning(base *url.URL, reach, platform string) map[string]any {
	if base == nil {
		return nil
	}
	addr := base.Scheme + "://" + base.Host
	platform, _, _ = strings.Cut(platform, " (") // "Roblox (Luau)" reads as "Roblox"
	public, https := isPublicHost(base.Hostname()), base.Scheme == "https"
	switch reach {
	case "public":
		if public && https {
			return nil
		}
		return map[string]any{"Error": true, "Text": fmt.Sprintf("%s servers can't reach `%s`. They only connect to public https:// addresses, so nothing will arrive. Deploy Burrow at one, set `BURROW_BASE_URL` to it and restart; the code on this page updates to match.", platform, addr)}
	case "https":
		if https {
			return nil
		}
		return map[string]any{"Text": fmt.Sprintf("Released apps block plain-HTTP requests, so `%s` will only work in development builds. Set `BURROW_BASE_URL` to an https:// address before you ship.", addr)}
	}
	if h := base.Hostname(); h == "localhost" || isLoopback(h) {
		return map[string]any{"Text": fmt.Sprintf("`%s` only works from this machine. If your app runs anywhere else, set `BURROW_BASE_URL` to the address it can reach and restart; the code on this page updates to match.", addr)}
	}
	return nil
}

// isPublicHost reports whether host looks reachable from the internet: a
// public IP, or a dotted name that isn't a local-only suffix.
func isPublicHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() ||
			(ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&0xc0 == 64))
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if !strings.Contains(host, ".") {
		return false // "localhost", "nas", other single-label LAN names
	}
	for _, suffix := range []string{".local", ".lan", ".home", ".internal", ".localhost", ".test", ".home.arpa"} {
		if strings.HasSuffix(host, suffix) {
			return false
		}
	}
	return true
}

func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// --------------------------------------------------------------- webhooks

func (s *Server) webhookCreate(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.loadProject(w, r, accessOwner)
	if !ok {
		return
	}
	back := "/projects/" + p.Slug + "/settings"
	u := strings.TrimSpace(r.PostFormValue("url"))
	onNew, onRegression := r.PostFormValue("on_new") == "1", r.PostFormValue("on_regression") == "1"
	switch err := notify.CheckURL(u, s.Notify.AllowPrivate); {
	case errors.Is(err, notify.ErrPrivateAddress):
		http.Redirect(w, r, back+"?flash=webhook-private#webhooks", http.StatusSeeOther)
		return
	case err != nil:
		http.Redirect(w, r, back+"?flash=webhook-invalid#webhooks", http.StatusSeeOther)
		return
	case !onNew && !onRegression:
		http.Redirect(w, r, back+"?flash=webhook-no-trigger#webhooks", http.StatusSeeOther)
		return
	}
	if _, err := s.Store.CreateWebhook(r.Context(), p.ID, u, onNew, onRegression); errors.Is(err, store.ErrTooManyWebhooks) {
		http.Redirect(w, r, back+"?flash=webhook-limit#webhooks", http.StatusSeeOther)
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, back+"?flash=webhook-added#webhooks", http.StatusSeeOther)
}

func (s *Server) loadWebhook(w http.ResponseWriter, r *http.Request) (*store.Project, *store.Webhook, bool) {
	p, _, ok := s.loadProject(w, r, accessOwner)
	if !ok {
		return nil, nil, false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return nil, nil, false
	}
	wh, err := s.Store.WebhookByID(r.Context(), p.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return nil, nil, false
	} else if err != nil {
		s.serverError(w, r, err)
		return nil, nil, false
	}
	return p, wh, true
}

func (s *Server) webhookDelete(w http.ResponseWriter, r *http.Request) {
	p, wh, ok := s.loadWebhook(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteWebhook(r.Context(), p.ID, wh.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/projects/"+p.Slug+"/settings?flash=webhook-removed#webhooks", http.StatusSeeOther)
}

func (s *Server) webhookTest(w http.ResponseWriter, r *http.Request) {
	p, wh, ok := s.loadWebhook(w, r)
	if !ok {
		return
	}
	result := "ok"
	if err := s.Notify.Test(r.Context(), p, wh.URL); err != nil {
		var status notify.ErrStatus
		switch {
		case errors.As(err, &status):
			result = strconv.Itoa(status.Code)
		case errors.Is(err, context.DeadlineExceeded):
			result = "timeout"
		default:
			result = "unreachable"
		}
		s.Log.Info("webhook test failed", "project", p.Slug, "webhook", wh.ID, "err", err)
	}
	http.Redirect(w, r, fmt.Sprintf("/projects/%s/settings?flash=webhook-tested&result=%s#webhook-%d", p.Slug, result, wh.ID), http.StatusSeeOther)
}

// testResultMessage turns a webhook test result code into the sentence the
// settings page shows. Only known codes render, so the query string can't
// put arbitrary text on the page.
func testResultMessage(code string) map[string]any {
	switch code {
	case "":
		return nil
	case "ok":
		return map[string]any{"OK": true, "Text": "Test alert delivered. Check the channel it posts to."}
	case "timeout":
		return map[string]any{"Text": "The webhook didn't answer within 10 seconds. Check the URL and that the receiving service is up."}
	case "unreachable":
		return map[string]any{"Text": "Couldn't reach the webhook. Check the URL, and that this server can reach that host."}
	}
	if n, err := strconv.Atoi(code); err == nil && n >= 100 && n <= 599 {
		return map[string]any{"Text": fmt.Sprintf("The webhook answered HTTP %d, so the alert wasn't accepted. Check the URL is complete and still active.", n)}
	}
	return nil
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
	// The checkbox is required in the form; enforcing it here keeps the
	// safeguard when JavaScript and client-side validation are unavailable.
	if r.PostFormValue("confirm") != "rotate" {
		http.Redirect(w, r, "/projects/"+p.Slug+"/settings?flash=rotate-confirm", http.StatusSeeOther)
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
	level := q.Get("level")
	levels := levelsAtLeast(level)
	if levels == nil {
		level = ""
	}
	offset := (page - 1) * issuesPerPage
	issues, err := s.Store.ListIssues(r.Context(), p.ID, store.IssueFilter{
		Status: status,
		Query:  q.Get("q"),
		Levels: levels,
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
	totals, err := s.Store.IssueStatusCounts(r.Context(), p.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	p.UnresolvedCount = totals.Unresolved
	// Tab counts follow the search and level filter so they match the list.
	counts, err := s.Store.FilteredStatusCounts(r.Context(), p.ID, store.IssueFilter{Query: q.Get("q"), Levels: levels})
	if err != nil {
		s.serverError(w, r, err)
		return
	}

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
		if level != "" {
			v.Set("level", level)
		}
		v.Set("page", strconv.Itoa(n))
		return "/projects/" + p.Slug + "?" + v.Encode()
	}
	tabURLs := map[string]string{}
	for _, st := range []string{"unresolved", "resolved", "ignored", "all"} {
		v := url.Values{"status": {st}}
		for _, k := range []string{"q", "sort", "level"} {
			if q.Get(k) != "" && (k != "level" || level != "") {
				v.Set(k, q.Get(k))
			}
		}
		tabURLs[st] = "/projects/" + p.Slug + "?" + v.Encode()
	}
	clear := url.Values{"status": {statusParam}}
	if sort != "" {
		clear.Set("sort", sort)
	}
	if level != "" {
		clear.Set("level", level)
	}
	data := map[string]any{
		"Project":      p,
		"CanManage":    a >= accessOwner,
		"TabURL":       tabURLs,
		"ClearURL":     "/projects/" + p.Slug + "?" + clear.Encode(),
		"Issues":       issueViews,
		"Status":       statusParam,
		"Query":        q.Get("q"),
		"Sort":         sort,
		"Level":        level,
		"Page":         page,
		"Counts":       counts,
		"Total":        totals.All,
		"ListURL":      r.URL.RequestURI(),
		"DayLabels":    dayLabels(),
		"ActivityFrom": activityFrom(s.now()),
		"DSN":          s.DSN(p),
		"TotalShown":   len(issueViews),
	}
	if hasNext {
		data["NextURL"] = pageURL(page + 1)
	}
	switch q.Get("flash") {
	case "status":
		// Offer undo only while the issue is still in the state we moved it
		// to, so a reloaded or shared URL can't flip it back unexpectedly.
		id, _ := strconv.ParseInt(q.Get("issue"), 10, 64)
		undo := store.IssueStatus(q.Get("undo"))
		if changed, err := s.Store.IssueByID(r.Context(), id); err == nil && changed.ProjectID == p.ID &&
			string(changed.Status) == q.Get("to") && undo.Valid() && undo != changed.Status {
			data["Changed"] = changed
			data["Undo"] = string(undo)
		}
	case "bulk":
		if n, _ := strconv.Atoi(q.Get("n")); n > 0 {
			data["BulkN"] = n
			data["BulkTo"] = q.Get("to")
			// Offer Undo for the issues still where the bulk action put them.
			to := store.IssueStatus(q.Get("to"))
			var undo []string
			for id, prev := range parseWas(q["was"]) {
				if is, err := s.Store.IssueByID(r.Context(), id); err == nil && is.ProjectID == p.ID && is.Status == to {
					undo = append(undo, fmt.Sprintf("%d.%s", id, prev))
				}
			}
			data["BulkUndo"] = undo
		}
	case "bulk-restored":
		n, _ := strconv.Atoi(q.Get("n"))
		data["Restored"] = n
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
		"CanManage":       a >= accessOwner,
		"Recent":          recent,
		"Activity":        act,
		"Bars":            bars,
		"ActivityTotal":   actTotal,
		"ActivitySummary": actSummary,
		"DayLabels":       dayLabels(),
		"ActivityFrom":    activityFrom(s.now()),
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
	s.render(w, r, http.StatusOK, "issue", shortTitle(issue.Title, 70), data)
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
	prev := issue.Status
	if err := s.Store.SetIssueStatus(r.Context(), issue.ID, st); err != nil {
		s.serverError(w, r, err)
		return
	}
	issueURL := "/issues/" + strconv.FormatInt(issue.ID, 10)
	switch {
	case prev == st:
		http.Redirect(w, r, issueURL, http.StatusSeeOther)
	case r.PostFormValue("undo") == "1":
		http.Redirect(w, r, issueURL+"?flash=restored", http.StatusSeeOther)
	case st == store.StatusUnresolved:
		http.Redirect(w, r, issueURL+"?flash=reopened", http.StatusSeeOther)
	default:
		// Resolving or ignoring ends the visit: back to the remaining
		// unresolved issues, with a one-step undo at the top.
		p, err := s.Store.ProjectByID(r.Context(), issue.ProjectID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		v := url.Values{"flash": {"status"}, "issue": {strconv.FormatInt(issue.ID, 10)}, "to": {string(st)}, "undo": {string(prev)}}
		http.Redirect(w, r, "/projects/"+p.Slug+"?"+v.Encode(), http.StatusSeeOther)
	}
}

func (s *Server) issueDelete(w http.ResponseWriter, r *http.Request) {
	issue, p, _, ok := s.loadIssue(w, r, accessOwner)
	if !ok {
		return
	}
	if r.PostFormValue("confirm") != "delete" {
		http.Redirect(w, r, "/issues/"+strconv.FormatInt(issue.ID, 10)+"?flash=delete-confirm", http.StatusSeeOther)
		return
	}
	if err := s.Store.DeleteIssue(r.Context(), issue.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/projects/"+p.Slug, http.StatusSeeOther)
}

// shortTitle keeps browser tabs readable for long exception messages.
func shortTitle(t string, max int) string {
	r := []rune(t)
	if len(r) <= max {
		return t
	}
	return strings.TrimSpace(string(r[:max-1])) + "…"
}

// levelsAtLeast maps the issue list's level filter to the levels it keeps.
func levelsAtLeast(min string) []string {
	switch min {
	case "error":
		return []string{"fatal", "error"}
	case "warning":
		return []string{"fatal", "error", "warning"}
	}
	return nil
}

// issueBulkStatus changes the status of the issues ticked on the list page.
// It is a plain form post, so bulk triage works without JavaScript.
func (s *Server) issueBulkStatus(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.loadProject(w, r, accessMember)
	if !ok {
		return
	}
	st := store.IssueStatus(r.PostFormValue("status"))
	if !st.Valid() {
		http.Error(w, "invalid status", http.StatusBadRequest)
		return
	}
	n := 0
	var was []string // "id.previous-status" for each issue that changed, for undo
	for _, raw := range r.PostForm["id"] {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			continue
		}
		issue, err := s.Store.IssueByID(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) || (err == nil && issue.ProjectID != p.ID) {
			continue
		} else if err != nil {
			s.serverError(w, r, err)
			return
		}
		if err := s.Store.SetIssueStatus(r.Context(), id, st); err != nil {
			s.serverError(w, r, err)
			return
		}
		n++
		if issue.Status != st && len(was) < maxBulkUndo {
			was = append(was, fmt.Sprintf("%d.%s", id, issue.Status))
		}
	}
	v := url.Values{}
	if n == 0 {
		v.Set("flash", "bulk-none")
	} else {
		v.Set("flash", "bulk")
		v.Set("n", strconv.Itoa(n))
		v.Set("to", string(st))
		v["was"] = was
	}
	http.Redirect(w, r, bulkBack(r, p, v), http.StatusSeeOther)
}

// maxBulkUndo caps how many issues one Undo can restore; the list shows
// 50 per page, so a whole page always fits.
const maxBulkUndo = 100

// bulkBack is the issue list URL to return to after a bulk action: the
// list the form came from (its filters and tab), with v's flash fields.
func bulkBack(r *http.Request, p *store.Project, v url.Values) string {
	base := "/projects/" + p.Slug
	back, err := url.Parse(safeNext(r.PostFormValue("back")))
	if err != nil || back.Path != base {
		back = &url.URL{Path: base}
	}
	q := back.Query()
	for _, k := range []string{"flash", "n", "to", "issue", "undo", "page", "was"} {
		q.Del(k)
	}
	for k, vals := range v {
		q[k] = vals
	}
	back.RawQuery = q.Encode()
	return back.String()
}

// parseWas reads "id.status" pairs from a bulk flash or undo form.
func parseWas(raw []string) map[int64]store.IssueStatus {
	out := map[int64]store.IssueStatus{}
	for _, pair := range raw {
		idStr, st, ok := strings.Cut(pair, ".")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if ok && err == nil && store.IssueStatus(st).Valid() && len(out) < maxBulkUndo {
			out[id] = store.IssueStatus(st)
		}
	}
	return out
}

// issueBulkRestore undoes a bulk status change: each issue goes back to its
// previous status, but only if it's still in the status the bulk action
// set, so a stale or replayed Undo can't overwrite later triage.
func (s *Server) issueBulkRestore(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.loadProject(w, r, accessMember)
	if !ok {
		return
	}
	to := store.IssueStatus(r.PostFormValue("to"))
	if !to.Valid() {
		http.Error(w, "invalid status", http.StatusBadRequest)
		return
	}
	n := 0
	for id, prev := range parseWas(r.PostForm["was"]) {
		issue, err := s.Store.IssueByID(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) || (err == nil && (issue.ProjectID != p.ID || issue.Status != to)) {
			continue
		} else if err != nil {
			s.serverError(w, r, err)
			return
		}
		if err := s.Store.SetIssueStatus(r.Context(), id, prev); err != nil {
			s.serverError(w, r, err)
			return
		}
		n++
	}
	http.Redirect(w, r, bulkBack(r, p, url.Values{"flash": {"bulk-restored"}, "n": {strconv.Itoa(n)}}), http.StatusSeeOther)
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
		Release:     "sample-app@1.0.0",
		ServerName:  "sample-host-1",
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
