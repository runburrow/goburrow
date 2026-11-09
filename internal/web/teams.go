package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/runburrow/goburrow/internal/mail"
	"github.com/runburrow/goburrow/internal/store"
)

// ------------------------------------------------------------------- teams

func (s *Server) teamList(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	teams, err := s.Store.ListTeams(r.Context(), sess.User.ID, sess.User.IsAdmin)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "teams", "Teams", teams)
}

func (s *Server) teamNewPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "team_new", "New team", map[string]string{})
}

// Any signed-in user may create a team; they become its owner. Teams are
// cheap and only grant access to their own projects.
func (s *Server) teamCreate(w http.ResponseWriter, r *http.Request) {
	name := r.PostFormValue("name")
	t, err := s.Store.CreateTeam(r.Context(), name, sessionFrom(r.Context()).User.ID)
	if err != nil {
		s.render(w, r, http.StatusBadRequest, "team_new", "New team", map[string]string{"Error": err.Error(), "Name": name})
		return
	}
	http.Redirect(w, r, "/teams/"+t.Slug, http.StatusSeeOther)
}

func (s *Server) teamDetail(w http.ResponseWriter, r *http.Request) {
	s.renderTeam(w, r, http.StatusOK, "", "")
}

// renderTeam renders the team page. inviteURL is set right after creating
// an invite: the raw token is only ever shown in that one response.
func (s *Server) renderTeam(w http.ResponseWriter, r *http.Request, code int, inviteURL, errMsg string, emailNote ...string) {
	t, a, ok := s.loadTeam(w, r, accessMember)
	if !ok {
		return
	}
	sess := sessionFrom(r.Context())
	members, err := s.Store.TeamMembers(r.Context(), t.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	projects, err := s.Store.ListProjects(r.Context(), sess.User.ID, true, t.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data := map[string]any{
		"Team":      t,
		"Members":   members,
		"Projects":  projects,
		"CanManage": a >= accessOwner,
		// The creator (or an instance admin, in Instance Admin) can hand
		// the creator role to someone else and delete the team.
		"IsCreator": sess.User.IsAdmin || t.CreatorID == sess.User.ID,
		"InviteURL": inviteURL,
		"Error":     errMsg,
		"Me":        sess.User.ID,
		"InviteTTL": int(store.InviteTTL.Hours() / 24),
		"CanEmail":  s.Notify != nil && s.Notify.Mail != nil,
	}
	if len(emailNote) > 0 {
		data["EmailNote"] = emailNote[0]
	}
	if a >= accessOwner {
		invites, err := s.Store.PendingInvites(r.Context(), t.ID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		data["Invites"] = invites
	}
	s.render(w, r, code, "team", t.Name, data)
}

func (s *Server) teamRename(w http.ResponseWriter, r *http.Request) {
	t, _, ok := s.loadTeam(w, r, accessOwner)
	if !ok {
		return
	}
	if err := s.Store.RenameTeam(r.Context(), t.ID, r.PostFormValue("name")); err != nil {
		s.renderTeam(w, r, http.StatusBadRequest, "", err.Error())
		return
	}
	http.Redirect(w, r, "/teams/"+t.Slug+"?flash=renamed", http.StatusSeeOther)
}

func (s *Server) teamDelete(w http.ResponseWriter, r *http.Request) {
	t, _, ok := s.loadTeam(w, r, accessOwner)
	if !ok {
		return
	}
	if sess := sessionFrom(r.Context()); !sess.User.IsAdmin && t.CreatorID != sess.User.ID {
		s.render(w, r, http.StatusForbidden, "error", "Not allowed", "Only the team's creator can delete the team.")
		return
	}
	if r.PostFormValue("confirm") != t.Slug {
		s.renderTeam(w, r, http.StatusBadRequest, "", "Type the team's slug exactly to confirm deleting it.")
		return
	}
	if err := s.Store.DeleteTeam(r.Context(), t.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/teams", http.StatusSeeOther)
}

func (s *Server) memberRole(w http.ResponseWriter, r *http.Request) {
	t, _, ok := s.loadTeam(w, r, accessOwner)
	if !ok {
		return
	}
	uid, _ := strconv.ParseInt(r.PathValue("user"), 10, 64)
	err := s.Store.SetMemberRole(r.Context(), t.ID, uid, store.Role(r.PostFormValue("role")))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	} else if errors.Is(err, store.ErrLastOwner) || errors.Is(err, store.ErrCreator) {
		s.renderTeam(w, r, http.StatusConflict, "", err.Error())
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/teams/"+t.Slug, http.StatusSeeOther)
}

// teamTransferCreator hands the creator role to another member. Only the
// creator and instance admins may; other owners can't touch the creator.
func (s *Server) teamTransferCreator(w http.ResponseWriter, r *http.Request) {
	t, _, ok := s.loadTeam(w, r, accessMember)
	if !ok {
		return
	}
	sess := sessionFrom(r.Context())
	if !sess.User.IsAdmin && t.CreatorID != sess.User.ID {
		s.render(w, r, http.StatusForbidden, "error", "Not allowed", "Only the team's creator can hand the creator role to someone else.")
		return
	}
	uid, _ := strconv.ParseInt(r.PostFormValue("user"), 10, 64)
	if uid == t.CreatorID {
		http.Redirect(w, r, "/teams/"+t.Slug, http.StatusSeeOther)
		return
	}
	err := s.Store.TransferCreator(r.Context(), t.ID, uid)
	if errors.Is(err, store.ErrNotMember) {
		s.renderTeam(w, r, http.StatusBadRequest, "", err.Error())
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/teams/"+t.Slug+"?flash=creator", http.StatusSeeOther)
}

// memberRemove: owners can remove anyone; anyone can remove themselves
// (leave the team).
func (s *Server) memberRemove(w http.ResponseWriter, r *http.Request) {
	uid, _ := strconv.ParseInt(r.PathValue("user"), 10, 64)
	self := uid == sessionFrom(r.Context()).User.ID
	need := accessOwner
	if self {
		need = accessMember
	}
	t, _, ok := s.loadTeam(w, r, need)
	if !ok {
		return
	}
	err := s.Store.RemoveMember(r.Context(), t.ID, uid)
	if errors.Is(err, store.ErrLastOwner) || errors.Is(err, store.ErrCreator) {
		s.renderTeam(w, r, http.StatusConflict, "", err.Error())
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	if self {
		http.Redirect(w, r, "/teams", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/teams/"+t.Slug, http.StatusSeeOther)
}

// ----------------------------------------------------------------- invites

func (s *Server) inviteCreate(w http.ResponseWriter, r *http.Request) {
	t, _, ok := s.loadTeam(w, r, accessOwner)
	if !ok {
		return
	}
	email := strings.TrimSpace(r.PostFormValue("email"))
	if email != "" && !mail.ValidAddress(email) {
		s.renderTeam(w, r, http.StatusBadRequest, "", "That email address doesn't look right.")
		return
	}
	role := store.Role(r.PostFormValue("role"))
	sess := sessionFrom(r.Context())
	token, err := s.Store.CreateInvite(r.Context(), t.ID, role, email, sess.User.ID)
	if err != nil {
		s.renderTeam(w, r, http.StatusBadRequest, "", err.Error())
		return
	}
	u := *s.BaseURL
	u.Path = strings.TrimRight(u.Path, "/") + "/invite/" + token
	// With email set up and an address given, Burrow sends the link too.
	// The link is still shown once, so a failed send isn't a dead end.
	note := ""
	if email != "" && s.Notify != nil && s.Notify.Mail != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		err := s.Notify.Mail.Send(ctx, mail.InviteMessage(email, mail.Invite{
			Team: t.Name, Role: string(role), InvitedBy: sess.User.Email, URL: u.String(), Expires: time.Now().Add(store.InviteTTL),
		}))
		cancel()
		if err != nil {
			_, why := mail.ErrorReason(err)
			s.Log.Warn("invite email failed", "team", t.Slug, "err", err)
			note = "Couldn't email it: " + why
		} else {
			note = "Also emailed to " + email + "."
		}
	}
	// Rendered directly rather than redirected: the token must not end up
	// in a URL, browser history, or access logs.
	s.renderTeam(w, r, http.StatusOK, u.String(), "", note)
}

func (s *Server) inviteRevoke(w http.ResponseWriter, r *http.Request) {
	t, _, ok := s.loadTeam(w, r, accessOwner)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("invite"), 10, 64)
	if err := s.Store.RevokeInvite(r.Context(), t.ID, id); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/teams/"+t.Slug, http.StatusSeeOther)
}

func (s *Server) invitePage(w http.ResponseWriter, r *http.Request) {
	s.renderInvite(w, r, http.StatusOK, "", "")
}

func (s *Server) renderInvite(w http.ResponseWriter, r *http.Request, code int, errMsg, email string) {
	inv, err := s.Store.InviteByToken(r.Context(), r.PathValue("token"))
	if errors.Is(err, store.ErrInviteUsed) {
		s.render(w, r, http.StatusNotFound, "error", "Invite not valid", "This invite link has expired, was revoked, or has already been used. Ask a team admin for a new one.")
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	if email == "" {
		email = inv.Email
	}
	s.render(w, r, code, "invite", "Join "+inv.TeamName, map[string]any{
		"Invite": inv, "Token": r.PathValue("token"), "Error": errMsg, "Email": email,
	})
}

// inviteAccept handles both cases: a signed-in user joining, and a new
// person creating their account from the invite.
func (s *Server) inviteAccept(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if sess := sessionFrom(r.Context()); sess != nil {
		inv, err := s.Store.AcceptInvite(r.Context(), token, &sess.User)
		if errors.Is(err, store.ErrInviteEmail) {
			s.renderInvite(w, r, http.StatusForbidden, "This invite is for a different email address than the account you're signed in with.", "")
			return
		} else if err != nil {
			s.renderInvite(w, r, http.StatusBadRequest, err.Error(), "")
			return
		}
		t, err := s.Store.TeamByID(r.Context(), inv.TeamID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		http.Redirect(w, r, "/teams/"+t.Slug+"?flash=joined", http.StatusSeeOther)
		return
	}

	email, pw, pw2 := strings.TrimSpace(r.PostFormValue("email")), r.PostFormValue("password"), r.PostFormValue("password2")
	if pw != pw2 {
		s.renderInvite(w, r, http.StatusBadRequest, "The passwords don't match.", email)
		return
	}
	u, err := s.Store.SignUpWithInvite(r.Context(), token, email, pw)
	switch {
	case errors.Is(err, store.ErrEmailTaken):
		s.renderInvite(w, r, http.StatusConflict, "An account with that email already exists. Log in first, then open this link again.", email)
		return
	case errors.Is(err, store.ErrInviteEmail):
		s.renderInvite(w, r, http.StatusForbidden, "This invite was issued for a different email address.", email)
		return
	case err != nil:
		s.renderInvite(w, r, http.StatusBadRequest, err.Error(), email)
		return
	}
	s.startSession(w, r, u.ID, "/?flash=welcome")
}

// ----------------------------------------------------------------- account

func (s *Server) accountPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "account", "Your account", s.accountData(r, ""))
}

func (s *Server) accountData(r *http.Request, errMsg string) map[string]any {
	data := map[string]any{"Error": errMsg}
	if s.Notify != nil && s.Notify.Mail != nil {
		data["MailSetup"] = s.Notify.Mail.Describe()
	}
	if msg := emailResultMessage(r.URL.Query().Get("result")); msg != nil && r.URL.Query().Get("flash") == "test-email" {
		data["TestResult"] = msg
	}
	return data
}

// accountTheme sets the look for this browser. Anything but a known theme
// goes back to the default.
func (s *Server) accountTheme(w http.ResponseWriter, r *http.Request) {
	theme := r.PostFormValue("theme")
	if !themes[theme] {
		theme = ""
	}
	s.setThemeCookie(w, theme)
	http.Redirect(w, r, "/account#appearance", http.StatusSeeOther)
}

// accountTestEmail sends the "Burrow can send email" message to the admin
// who pressed the button, to check the SMTP setup end to end.
func (s *Server) accountTestEmail(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if s.Notify == nil || s.Notify.Mail == nil {
		http.Redirect(w, r, "/account?flash=test-email&result=off#email", http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	err := s.Notify.Mail.Send(ctx, mail.CheckMessage(mail.Check{Server: s.origin(), Setup: s.Notify.Mail.Describe(), To: sess.User.Email}))
	code, _ := mail.ErrorReason(err)
	if err != nil {
		s.Log.Info("test email failed", "to", sess.User.Email, "err", err)
	}
	http.Redirect(w, r, "/account?flash=test-email&result="+code+"#email", http.StatusSeeOther)
}

func (s *Server) accountPassword(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	cur, next, next2 := r.PostFormValue("current"), r.PostFormValue("password"), r.PostFormValue("password2")
	if next != next2 {
		s.render(w, r, http.StatusBadRequest, "account", "Your account", s.accountData(r, "The new passwords don't match."))
		return
	}
	if err := s.Store.ChangePassword(r.Context(), sess.User.ID, cur, next, sess.Token); err != nil {
		s.render(w, r, http.StatusBadRequest, "account", "Your account", s.accountData(r, err.Error()))
		return
	}
	http.Redirect(w, r, "/account?flash=password", http.StatusSeeOther)
}

// ------------------------------------------------------------------- admin

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	s.renderAdminUsers(w, r, http.StatusOK, "")
}

func (s *Server) renderAdminUsers(w http.ResponseWriter, r *http.Request, code int, errMsg string) {
	users, err := s.Store.ListUsers(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, code, "admin_users", "Users", map[string]any{
		"Users": users, "Me": sessionFrom(r.Context()).User.ID, "Error": errMsg,
	})
}

func (s *Server) adminSetAdmin(w http.ResponseWriter, r *http.Request) {
	uid, _ := strconv.ParseInt(r.PathValue("user"), 10, 64)
	err := s.Store.SetAdmin(r.Context(), uid, r.PostFormValue("admin") == "1")
	if errors.Is(err, store.ErrLastAdmin) {
		s.renderAdminUsers(w, r, http.StatusConflict, "Burrow needs at least one instance admin. Make someone else one first.")
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (s *Server) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	uid, _ := strconv.ParseInt(r.PathValue("user"), 10, 64)
	if uid == sessionFrom(r.Context()).User.ID {
		s.renderAdminUsers(w, r, http.StatusBadRequest, "You can't delete your own account from here.")
		return
	}
	// The checkbox is required in the form; checking it here keeps the
	// safeguard without JavaScript or client-side validation.
	if r.PostFormValue("confirm") != "delete" {
		http.Redirect(w, r, "/admin/users?flash=delete-confirm", http.StatusSeeOther)
		return
	}
	err := s.Store.DeleteUser(r.Context(), uid)
	if errors.Is(err, store.ErrLastAdmin) {
		s.renderAdminUsers(w, r, http.StatusConflict, "Burrow needs at least one instance admin.")
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/users?flash=deleted", http.StatusSeeOther)
}

// instanceAdmin switches an instance admin's session to Instance Admin for
// store.ElevateFor, or back. Until then they act as a regular user, so
// overriding a team's creator or managing accounts is always deliberate.
func (s *Server) instanceAdmin(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := sessionFrom(r.Context())
		if !sess.CanElevate {
			s.notFound(w, r)
			return
		}
		var until time.Time
		if on {
			// Real time, not s.now(): the store expires it against the clock.
			until = time.Now().Add(store.ElevateFor)
		}
		if err := s.Store.SetElevated(r.Context(), sess.Token, until); err != nil {
			s.serverError(w, r, err)
			return
		}
		s.Log.Info("instance admin mode", "user", sess.User.Email, "on", on)
		next := "/"
		if ref, err := url.Parse(r.Referer()); err == nil && (ref.Host == "" || ref.Host == r.Host) && strings.HasPrefix(ref.Path, "/") && !strings.HasPrefix(ref.Path, "//") {
			next = ref.Path
		}
		if !on && strings.HasPrefix(next, "/admin/") {
			next = "/"
		}
		http.Redirect(w, r, next, http.StatusSeeOther)
	}
}
