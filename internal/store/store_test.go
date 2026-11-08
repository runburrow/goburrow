package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

var ctx = context.Background()

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustUser(t *testing.T, s *Store, email string, admin bool) *User {
	t.Helper()
	u, err := s.CreateUser(ctx, email, "long-enough-pw", admin)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// An install created before teams existed must upgrade cleanly: its users
// become admins and owners of a Default team that holds its projects.
func TestMigrateV1ToTeams(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE schema_version (version INTEGER NOT NULL)`,
		`INSERT INTO schema_version VALUES (1)`,
		migrations[0],
		`INSERT INTO users (email, password_hash, created_at) VALUES ('old@x.co', 'h', 1)`,
		`INSERT INTO projects (name, slug, platform, public_key, created_at) VALUES ('Legacy', 'legacy', 'go', 'k', 1)`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	u, err := s.UserByID(ctx, 1)
	if err != nil || !u.IsAdmin {
		t.Fatalf("existing user should be admin: %+v %v", u, err)
	}
	team, err := s.TeamBySlug(ctx, "default")
	if err != nil {
		t.Fatal("default team missing:", err)
	}
	if role, _ := s.TeamRole(ctx, team.ID, u.ID); role != RoleOwner {
		t.Errorf("existing user should own default team, got %q", role)
	}
	if team.CreatorID != u.ID {
		t.Errorf("existing owner should become the default team's creator, got %d", team.CreatorID)
	}
	p, _ := s.ProjectBySlug(ctx, "legacy")
	if p.TeamID != team.ID {
		t.Errorf("legacy project not moved into default team")
	}
}

func TestFreshInstallHasNoDefaultTeam(t *testing.T) {
	s := open(t)
	teams, _ := s.ListTeams(ctx, 0, true)
	if len(teams) != 0 {
		t.Errorf("fresh install should start with no teams, got %d", len(teams))
	}
}

func TestSetupIsOneShot(t *testing.T) {
	s := open(t)
	u, err := s.Setup(ctx, "first@x.co", "long-enough-pw", "Core")
	if err != nil || !u.IsAdmin {
		t.Fatalf("setup: %+v %v", u, err)
	}
	team, _ := s.TeamBySlug(ctx, "core")
	if role, _ := s.TeamRole(ctx, team.ID, u.ID); role != RoleOwner {
		t.Error("setup user should own their first team")
	}
	if _, err := s.Setup(ctx, "second@x.co", "long-enough-pw", "Core"); err == nil {
		t.Error("second setup must fail")
	}
}

func TestLastOwnerGuard(t *testing.T) {
	s := open(t)
	a, b := mustUser(t, s, "a@x.co", false), mustUser(t, s, "b@x.co", false)
	team, _ := s.CreateTeam(ctx, "T", a.ID)
	if err := s.SetMemberRole(ctx, team.ID, a.ID, RoleMember); !errors.Is(err, ErrLastOwner) {
		t.Errorf("demoting the only owner: %v", err)
	}
	if err := s.RemoveMember(ctx, team.ID, a.ID); !errors.Is(err, ErrLastOwner) {
		t.Errorf("removing the only owner: %v", err)
	}
	tok, _ := s.CreateInvite(ctx, team.ID, RoleOwner, "", a.ID)
	if _, err := s.AcceptInvite(ctx, tok, b); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMemberRole(ctx, team.ID, a.ID, RoleMember); !errors.Is(err, ErrCreator) {
		t.Errorf("demoting the creator: %v", err)
	}
	if err := s.TransferCreator(ctx, team.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMemberRole(ctx, team.ID, a.ID, RoleMember); err != nil {
		t.Errorf("with two owners, demoting one should work: %v", err)
	}
	if err := s.RemoveMember(ctx, team.ID, b.ID); !errors.Is(err, ErrLastOwner) {
		t.Errorf("b is now the only owner: %v", err)
	}
}

func TestTeamCreator(t *testing.T) {
	s := open(t)
	a, b, c := mustUser(t, s, "a@x.co", false), mustUser(t, s, "b@x.co", false), mustUser(t, s, "c@x.co", false)
	team, _ := s.CreateTeam(ctx, "T", a.ID)
	for _, u := range []*User{b, c} {
		tok, _ := s.CreateInvite(ctx, team.ID, RoleOwner, "", a.ID)
		if _, err := s.AcceptInvite(ctx, tok, u); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := s.TeamBySlug(ctx, team.Slug); got.CreatorID != a.ID {
		t.Fatalf("creator %d, want %d", got.CreatorID, a.ID)
	}
	// Other owners can't demote or remove the creator, and neither can they.
	if err := s.SetMemberRole(ctx, team.ID, a.ID, RoleMember); !errors.Is(err, ErrCreator) {
		t.Errorf("demote creator: %v", err)
	}
	if err := s.RemoveMember(ctx, team.ID, a.ID); !errors.Is(err, ErrCreator) {
		t.Errorf("remove creator: %v", err)
	}
	if err := s.SetMemberRole(ctx, team.ID, a.ID, RoleOwner); err != nil {
		t.Errorf("setting the creator to owner is a no-op: %v", err)
	}
	members, _ := s.TeamMembers(ctx, team.ID)
	if !members[0].IsCreator || members[0].User.ID != a.ID || members[0].Label() != RoleCreator || members[1].Label() != RoleOwner {
		t.Errorf("creator should be listed first and labelled: %+v", members)
	}
	teams, _ := s.ListTeams(ctx, a.ID, false)
	if teams[0].MyRole != RoleCreator {
		t.Errorf("MyRole for the creator: %q", teams[0].MyRole)
	}

	// Handing it on: the new creator is promoted, the old one stays owner.
	s.SetMemberRole(ctx, team.ID, c.ID, RoleMember)
	if err := s.TransferCreator(ctx, team.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.TeamRole(ctx, team.ID, c.ID); r != RoleOwner {
		t.Errorf("new creator's role %q", r)
	}
	if err := s.RemoveMember(ctx, team.ID, a.ID); err != nil {
		t.Errorf("former creator can leave: %v", err)
	}
	outsider := mustUser(t, s, "d@x.co", false)
	if err := s.TransferCreator(ctx, team.ID, outsider.ID); !errors.Is(err, ErrNotMember) {
		t.Errorf("transfer to a non-member: %v", err)
	}

	// Deleting the creator's account passes the team to the longest-standing owner.
	if err := s.DeleteUser(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.TeamByID(ctx, team.ID); got.CreatorID != b.ID {
		t.Errorf("creator after deleting theirs: %d, want %d", got.CreatorID, b.ID)
	}
}

func TestLastAdminGuard(t *testing.T) {
	s := open(t)
	a, b := mustUser(t, s, "a@x.co", true), mustUser(t, s, "b@x.co", false)
	if err := s.SetAdmin(ctx, a.ID, false); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("demote last admin: %v", err)
	}
	if err := s.DeleteUser(ctx, a.ID); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("delete last admin: %v", err)
	}
	if err := s.DeleteUser(ctx, b.ID); err != nil {
		t.Errorf("deleting a non-admin: %v", err)
	}
}

func TestInvites(t *testing.T) {
	s := open(t)
	owner := mustUser(t, s, "o@x.co", false)
	team, _ := s.CreateTeam(ctx, "T", owner.ID)

	// Single use.
	tok, _ := s.CreateInvite(ctx, team.ID, RoleMember, "", owner.ID)
	u1 := mustUser(t, s, "u1@x.co", false)
	if _, err := s.AcceptInvite(ctx, tok, u1); err != nil {
		t.Fatal(err)
	}
	u2 := mustUser(t, s, "u2@x.co", false)
	if _, err := s.AcceptInvite(ctx, tok, u2); !errors.Is(err, ErrInviteUsed) {
		t.Errorf("reused invite: %v", err)
	}

	// Email-locked: wrong email is refused and the invite is NOT burned.
	tok, _ = s.CreateInvite(ctx, team.ID, RoleMember, "U2@x.co", owner.ID)
	if _, err := s.AcceptInvite(ctx, tok, u1); !errors.Is(err, ErrInviteEmail) {
		t.Errorf("wrong email: %v", err)
	}
	if _, err := s.AcceptInvite(ctx, tok, u2); err != nil {
		t.Errorf("right email (case-insensitive) should still work after a refused attempt: %v", err)
	}

	// Expired.
	tok, _ = s.CreateInvite(ctx, team.ID, RoleMember, "", owner.ID)
	s.db.Exec(`UPDATE invites SET expires_at = ? WHERE used_at IS NULL`, ms(time.Now().Add(-time.Minute)))
	if _, err := s.InviteByToken(ctx, tok); !errors.Is(err, ErrInviteUsed) {
		t.Errorf("expired invite: %v", err)
	}

	// Re-inviting an owner as member must not demote them.
	tok, _ = s.CreateInvite(ctx, team.ID, RoleMember, "", owner.ID)
	if _, err := s.AcceptInvite(ctx, tok, owner); err != nil {
		t.Fatal(err)
	}
	if role, _ := s.TeamRole(ctx, team.ID, owner.ID); role != RoleOwner {
		t.Errorf("owner demoted by member invite: %q", role)
	}

	// Tokens are stored hashed.
	var stored string
	s.db.QueryRow(`SELECT token_hash FROM invites ORDER BY id DESC LIMIT 1`).Scan(&stored)
	if stored == tok || len(stored) != 64 {
		t.Errorf("token should be stored as a sha256 hash")
	}
}

func TestSignUpWithInviteIsAtomic(t *testing.T) {
	s := open(t)
	owner := mustUser(t, s, "o@x.co", false)
	team, _ := s.CreateTeam(ctx, "T", owner.ID)
	tok, _ := s.CreateInvite(ctx, team.ID, RoleMember, "", owner.ID)

	if _, err := s.SignUpWithInvite(ctx, tok, "new@x.co", "short"); err == nil {
		t.Fatal("weak password accepted")
	}
	if _, err := s.SignUpWithInvite(ctx, tok, "o@x.co", "long-enough-pw"); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("taken email: %v", err)
	}
	if n, _ := s.UserCount(ctx); n != 1 {
		t.Fatalf("failed signups must not leave users behind, have %d", n)
	}
	u, err := s.SignUpWithInvite(ctx, tok, "new@x.co", "long-enough-pw")
	if err != nil {
		t.Fatalf("invite should survive failed attempts: %v", err)
	}
	if role, _ := s.TeamRole(ctx, team.ID, u.ID); role != RoleMember {
		t.Errorf("role = %q", role)
	}
}

func TestProjectVisibility(t *testing.T) {
	s := open(t)
	a, b := mustUser(t, s, "a@x.co", false), mustUser(t, s, "b@x.co", false)
	admin := mustUser(t, s, "root@x.co", true)
	ta, _ := s.CreateTeam(ctx, "A", a.ID)
	tb, _ := s.CreateTeam(ctx, "B", b.ID)
	s.CreateProject(ctx, ta.ID, "alpha", "go")
	s.CreateProject(ctx, tb.ID, "beta", "go")

	names := func(uid int64, isAdmin bool) []string {
		ps, _ := s.ListProjects(ctx, uid, isAdmin, 0)
		var out []string
		for _, p := range ps {
			out = append(out, p.Name)
		}
		return out
	}
	if got := names(a.ID, false); len(got) != 1 || got[0] != "alpha" {
		t.Errorf("a sees %v", got)
	}
	if got := names(admin.ID, true); len(got) != 2 {
		t.Errorf("admin sees %v", got)
	}
}

func TestChangePasswordEndsOtherSessions(t *testing.T) {
	s := open(t)
	u := mustUser(t, s, "a@x.co", false)
	keep, _ := s.CreateSession(ctx, u.ID, time.Hour)
	other, _ := s.CreateSession(ctx, u.ID, time.Hour)
	if err := s.ChangePassword(ctx, u.ID, "wrong-password", "brand-new-password", keep.Token); err == nil {
		t.Fatal("wrong current password accepted")
	}
	if err := s.ChangePassword(ctx, u.ID, "long-enough-pw", "brand-new-password", keep.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionByToken(ctx, keep.Token); err != nil {
		t.Error("current session should survive")
	}
	if _, err := s.SessionByToken(ctx, other.Token); !errors.Is(err, ErrNotFound) {
		t.Error("other sessions should be logged out")
	}
	if _, err := s.Authenticate(ctx, "a@x.co", "brand-new-password"); err != nil {
		t.Error("new password should work")
	}
}
