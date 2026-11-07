package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/runburrow/goburrow/internal/store"
)

// Permission levels within a team. Instance admins act as owners everywhere.
//
//	member: view projects & issues, triage (resolve/ignore), view DSN, send test events
//	owner:  everything a member can, plus manage the team, its members,
//	        invites, and its projects (create, delete, rotate keys, delete issues)
type access int

const (
	accessNone access = iota
	accessMember
	accessOwner
)

// teamAccess resolves what the current user may do in team teamID.
func (s *Server) teamAccess(r *http.Request, teamID int64) (access, error) {
	sess := sessionFrom(r.Context())
	if sess == nil {
		return accessNone, nil
	}
	if sess.User.IsAdmin {
		return accessOwner, nil
	}
	role, err := s.Store.TeamRole(r.Context(), teamID, sess.User.ID)
	switch {
	case err != nil:
		return accessNone, err
	case role == store.RoleOwner:
		return accessOwner, nil
	case role == store.RoleMember:
		return accessMember, nil
	}
	return accessNone, nil
}

// require writes the response for an insufficient-access request. Outsiders
// get 404 so project and team names can't be probed; members trying an
// owner action get 403 because they already know the thing exists.
func (s *Server) require(w http.ResponseWriter, r *http.Request, have, need access) bool {
	if have >= need {
		return true
	}
	if have == accessNone {
		s.notFound(w, r)
	} else {
		s.render(w, r, http.StatusForbidden, "error", "Not allowed", "Only team owners can do that. Ask one of them, or an admin.")
	}
	return false
}

func (s *Server) loadProject(w http.ResponseWriter, r *http.Request, need access) (*store.Project, access, bool) {
	p, err := s.Store.ProjectBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return nil, 0, false
	} else if err != nil {
		s.serverError(w, r, err)
		return nil, 0, false
	}
	a, err := s.teamAccess(r, p.TeamID)
	if err != nil {
		s.serverError(w, r, err)
		return nil, 0, false
	}
	if !s.require(w, r, a, need) {
		return nil, 0, false
	}
	return p, a, true
}

func (s *Server) loadIssue(w http.ResponseWriter, r *http.Request, need access) (*store.Issue, *store.Project, access, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return nil, nil, 0, false
	}
	issue, err := s.Store.IssueByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.render(w, r, http.StatusNotFound, "error", "Issue not found", "There's no issue with that number. It may have been deleted, or the link came from another Burrow server.")
		return nil, nil, 0, false
	} else if err != nil {
		s.serverError(w, r, err)
		return nil, nil, 0, false
	}
	p, err := s.Store.ProjectByID(r.Context(), issue.ProjectID)
	if err != nil {
		s.serverError(w, r, err)
		return nil, nil, 0, false
	}
	a, err := s.teamAccess(r, p.TeamID)
	if err != nil {
		s.serverError(w, r, err)
		return nil, nil, 0, false
	}
	if !s.require(w, r, a, need) {
		return nil, nil, 0, false
	}
	return issue, p, a, true
}

func (s *Server) loadTeam(w http.ResponseWriter, r *http.Request, need access) (*store.Team, access, bool) {
	t, err := s.Store.TeamBySlug(r.Context(), r.PathValue("team"))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return nil, 0, false
	} else if err != nil {
		s.serverError(w, r, err)
		return nil, 0, false
	}
	a, err := s.teamAccess(r, t.ID)
	if err != nil {
		s.serverError(w, r, err)
		return nil, 0, false
	}
	if !s.require(w, r, a, need) {
		return nil, 0, false
	}
	return t, a, true
}

// adminOnly wraps a handler (already behind authed) for instance admins.
func (s *Server) adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if sess := sessionFrom(r.Context()); sess == nil || !sess.User.IsAdmin {
			s.notFound(w, r)
			return
		}
		next(w, r)
	}
}
