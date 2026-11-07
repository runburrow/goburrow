package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type Role string

const (
	RoleOwner  Role = "owner"
	RoleMember Role = "member"
)

func (r Role) Valid() bool { return r == RoleOwner || r == RoleMember }

var (
	ErrLastOwner   = errors.New("a team needs at least one owner. Promote someone else first")
	ErrInviteUsed  = errors.New("this invite link is invalid, expired, or already used")
	ErrInviteEmail = errors.New("this invite was issued for a different email address")
)

type Team struct {
	ID        int64
	Name      string
	Slug      string
	CreatedAt time.Time

	// Populated by ListTeams only.
	MyRole       Role // "" when the viewer is an admin but not a member
	MemberCount  int
	ProjectCount int
}

type Member struct {
	User     User
	Role     Role
	JoinedAt time.Time
}

type Invite struct {
	ID        int64
	TeamID    int64
	Role      Role
	Email     string
	CreatedBy string // email of the creator, "" if they were deleted
	CreatedAt time.Time
	ExpiresAt time.Time

	TeamName string // populated by InviteByToken
	TeamSlug string
}

// CreateTeam creates a team with owner as its first owner.
func (s *Store) CreateTeam(ctx context.Context, name string, ownerID int64) (*Team, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return nil, errors.New("team name is required (max 100 characters)")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	t := &Team{Name: name, CreatedAt: time.Now().UTC()}
	t.Slug, err = insertWithSlug(name, "team", func(slug string) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO teams (name, slug, created_at) VALUES (?, ?, ?)`, name, slug, ms(t.CreatedAt))
		if err == nil {
			t.ID, _ = res.LastInsertId()
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO team_members (team_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
		t.ID, ownerID, ms(t.CreatedAt)); err != nil {
		return nil, err
	}
	return t, tx.Commit()
}

func (s *Store) TeamBySlug(ctx context.Context, slug string) (*Team, error) {
	return s.scanTeam(s.db.QueryRowContext(ctx, `SELECT id, name, slug, created_at FROM teams WHERE slug = ?`, slug))
}

func (s *Store) TeamByID(ctx context.Context, id int64) (*Team, error) {
	return s.scanTeam(s.db.QueryRowContext(ctx, `SELECT id, name, slug, created_at FROM teams WHERE id = ?`, id))
}

func (s *Store) scanTeam(row *sql.Row) (*Team, error) {
	var t Team
	var created int64
	if err := row.Scan(&t.ID, &t.Name, &t.Slug, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	t.CreatedAt = fromMS(created)
	return &t, nil
}

// ListTeams returns the teams a user belongs to, or all teams for admins.
func (s *Store) ListTeams(ctx context.Context, userID int64, admin bool) ([]*Team, error) {
	filter := `WHERE m.role IS NOT NULL`
	if admin {
		filter = ``
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.name, t.slug, t.created_at, COALESCE(m.role, ''),
		       (SELECT COUNT(*) FROM team_members x WHERE x.team_id = t.id),
		       (SELECT COUNT(*) FROM projects p WHERE p.team_id = t.id)
		FROM teams t LEFT JOIN team_members m ON m.team_id = t.id AND m.user_id = ?
		`+filter+` ORDER BY t.name COLLATE NOCASE`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Team
	for rows.Next() {
		var t Team
		var created int64
		var role string
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &created, &role, &t.MemberCount, &t.ProjectCount); err != nil {
			return nil, err
		}
		t.CreatedAt, t.MyRole = fromMS(created), Role(role)
		out = append(out, &t)
	}
	return out, rows.Err()
}

// TeamRole returns the user's role in the team, or "" if not a member.
func (s *Store) TeamRole(ctx context.Context, teamID, userID int64) (Role, error) {
	var role string
	err := s.db.QueryRowContext(ctx, `SELECT role FROM team_members WHERE team_id = ? AND user_id = ?`, teamID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return Role(role), err
}

func (s *Store) TeamMembers(ctx context.Context, teamID int64) ([]Member, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.email, u.is_admin, u.created_at, m.role, m.created_at
		FROM team_members m JOIN users u ON u.id = m.user_id
		WHERE m.team_id = ? ORDER BY m.role = 'owner' DESC, u.email COLLATE NOCASE`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		var uc, mc int64
		var role string
		if err := rows.Scan(&m.User.ID, &m.User.Email, &m.User.IsAdmin, &uc, &role, &mc); err != nil {
			return nil, err
		}
		m.User.CreatedAt, m.Role, m.JoinedAt = fromMS(uc), Role(role), fromMS(mc)
		out = append(out, m)
	}
	return out, rows.Err()
}

// ownerGuard fails with ErrLastOwner if removing/demoting userID would leave
// the team with no owners. Must run inside the same transaction as the change.
func ownerGuard(ctx context.Context, tx *sql.Tx, teamID, userID int64) error {
	var isOwner bool
	var owners int
	err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(user_id = ? AND role = 'owner'), 0), COUNT(*) FILTER (WHERE role = 'owner')
		FROM team_members WHERE team_id = ?`, userID, teamID).Scan(&isOwner, &owners)
	if err != nil {
		return err
	}
	if isOwner && owners <= 1 {
		return ErrLastOwner
	}
	return nil
}

func (s *Store) SetMemberRole(ctx context.Context, teamID, userID int64, role Role) error {
	if !role.Valid() {
		return errors.New("invalid role")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if role != RoleOwner {
		if err := ownerGuard(ctx, tx, teamID, userID); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE team_members SET role = ? WHERE team_id = ? AND user_id = ?`, string(role), teamID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) RemoveMember(ctx context.Context, teamID, userID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ownerGuard(ctx, tx, teamID, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM team_members WHERE team_id = ? AND user_id = ?`, teamID, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteTeam deletes the team and, via cascade, its projects and issues.
func (s *Store) DeleteTeam(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM teams WHERE id = ?`, id)
	return err
}

func (s *Store) RenameTeam(ctx context.Context, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return errors.New("team name is required (max 100 characters)")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE teams SET name = ? WHERE id = ?`, name, id)
	return err
}

// ----------------------------------------------------------------- invites

const InviteTTL = 7 * 24 * time.Hour

// Invite tokens, like session tokens, are stored as SHA-256 hashes (see
// hashToken): a leaked database backup doesn't hand out working links.

// CreateInvite returns the raw token. It is shown once and never stored.
func (s *Store) CreateInvite(ctx context.Context, teamID int64, role Role, email string, createdBy int64) (string, error) {
	if !role.Valid() {
		return "", errors.New("invalid role")
	}
	email = strings.TrimSpace(email)
	if email != "" && !strings.Contains(email, "@") {
		return "", errors.New("that doesn't look like an email address")
	}
	token := randomHex(24)
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO invites (token_hash, team_id, role, email, created_by, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		hashToken(token), teamID, string(role), email, createdBy, ms(now), ms(now.Add(InviteTTL)))
	return token, err
}

func (s *Store) PendingInvites(ctx context.Context, teamID int64) ([]Invite, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, i.team_id, i.role, i.email, COALESCE(u.email, ''), i.created_at, i.expires_at
		FROM invites i LEFT JOIN users u ON u.id = i.created_by
		WHERE i.team_id = ? AND i.used_at IS NULL AND i.expires_at > ?
		ORDER BY i.created_at DESC`, teamID, ms(time.Now()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invite
	for rows.Next() {
		var inv Invite
		var role string
		var c, e int64
		if err := rows.Scan(&inv.ID, &inv.TeamID, &role, &inv.Email, &inv.CreatedBy, &c, &e); err != nil {
			return nil, err
		}
		inv.Role, inv.CreatedAt, inv.ExpiresAt = Role(role), fromMS(c), fromMS(e)
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (s *Store) RevokeInvite(ctx context.Context, teamID, inviteID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM invites WHERE id = ? AND team_id = ?`, inviteID, teamID)
	return err
}

// InviteByToken returns a usable (unexpired, unused) invite.
func (s *Store) InviteByToken(ctx context.Context, token string) (*Invite, error) {
	var inv Invite
	var role string
	var c, e int64
	err := s.db.QueryRowContext(ctx, `
		SELECT i.id, i.team_id, i.role, i.email, i.created_at, i.expires_at, t.name, t.slug
		FROM invites i JOIN teams t ON t.id = i.team_id
		WHERE i.token_hash = ? AND i.used_at IS NULL AND i.expires_at > ?`, hashToken(token), ms(time.Now())).
		Scan(&inv.ID, &inv.TeamID, &role, &inv.Email, &c, &e, &inv.TeamName, &inv.TeamSlug)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInviteUsed
	} else if err != nil {
		return nil, err
	}
	inv.Role, inv.CreatedAt, inv.ExpiresAt = Role(role), fromMS(c), fromMS(e)
	return &inv, nil
}

// AcceptInvite atomically consumes the invite and adds the user to the team.
// If they're already a member, they keep the higher of the two roles.
func (s *Store) AcceptInvite(ctx context.Context, token string, user *User) (*Invite, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	inv, err := acceptInviteTx(ctx, tx, token, user)
	if err != nil {
		return nil, err
	}
	return inv, tx.Commit()
}

func acceptInviteTx(ctx context.Context, tx *sql.Tx, token string, user *User) (*Invite, error) {
	var inv Invite
	var role string
	now := time.Now().UTC()
	err := tx.QueryRowContext(ctx, `
		UPDATE invites SET used_at = ?
		WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?
		RETURNING id, team_id, role, email`, ms(now), hashToken(token), ms(now)).
		Scan(&inv.ID, &inv.TeamID, &role, &inv.Email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInviteUsed
	} else if err != nil {
		return nil, err
	}
	inv.Role = Role(role)
	if inv.Email != "" && !strings.EqualFold(inv.Email, user.Email) {
		return nil, ErrInviteEmail
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO team_members (team_id, user_id, role, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (team_id, user_id) DO UPDATE SET role = CASE WHEN excluded.role = 'owner' THEN 'owner' ELSE role END`,
		inv.TeamID, user.ID, role, ms(now))
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

// SignUpWithInvite creates a user and consumes the invite in one
// transaction, so a failed signup never burns the link (and vice versa).
func (s *Store) SignUpWithInvite(ctx context.Context, token, email, password string) (*User, error) {
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	u, err := insertUser(ctx, tx, email, hash, false)
	if err != nil {
		return nil, err
	}
	if _, err := acceptInviteTx(ctx, tx, token, u); err != nil {
		return nil, err
	}
	return u, tx.Commit()
}
