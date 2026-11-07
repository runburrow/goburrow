package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID        int64
	Email     string
	IsAdmin   bool // instance admin: manages all users and teams
	CreatedAt time.Time
}

type Session struct {
	Token     string
	CSRF      string
	User      User
	ExpiresAt time.Time
}

var (
	ErrBadCredentials = errors.New("invalid email or password")
	ErrSetupDone      = errors.New("setup already completed")
	ErrLastAdmin      = errors.New("there must be at least one admin")
	ErrEmailTaken     = errors.New("an account with that email already exists")
)

func validateCredentials(email, password string) error {
	if email == "" || !strings.Contains(email, "@") || len(email) > 254 {
		return errors.New("a valid email is required")
	}
	if len(password) < 10 {
		return errors.New("password must be at least 10 characters")
	}
	if len(password) > 72 { // bcrypt silently truncates beyond this
		return errors.New("password must be at most 72 bytes")
	}
	return nil
}

func hashPassword(password string) (string, error) {
	if len(password) < 10 || len(password) > 72 {
		return "", validateCredentials("x@x", password)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(h), err
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertUser(ctx context.Context, db execer, email, hash string, admin bool) (*User, error) {
	email = strings.TrimSpace(email)
	if err := validateCredentials(email, "0123456789"); err != nil {
		return nil, err
	}
	u := &User{Email: email, IsAdmin: admin, CreatedAt: time.Now().UTC()}
	res, err := db.ExecContext(ctx, `INSERT INTO users (email, password_hash, is_admin, created_at) VALUES (?, ?, ?, ?)`,
		email, hash, admin, ms(u.CreatedAt))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrEmailTaken
		}
		return nil, err
	}
	u.ID, _ = res.LastInsertId()
	return u, nil
}

func (s *Store) UserCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(ctx context.Context, email, password string, admin bool) (*User, error) {
	if err := validateCredentials(strings.TrimSpace(email), password); err != nil {
		return nil, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	return insertUser(ctx, s.db, email, hash, admin)
}

// Setup creates the first (admin) account and its first team atomically.
// It fails if any user already exists, which closes the race where two
// people hit the first-run page at once.
func (s *Store) Setup(ctx context.Context, email, password, teamName string) (*User, error) {
	if err := validateCredentials(strings.TrimSpace(email), password); err != nil {
		return nil, err
	}
	if strings.TrimSpace(teamName) == "" {
		teamName = "My team"
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, ErrSetupDone
	}
	u, err := insertUser(ctx, tx, email, hash, true)
	if err != nil {
		return nil, err
	}
	now := ms(time.Now())
	var teamID int64
	if _, err := insertWithSlug(teamName, "team", func(slug string) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO teams (name, slug, created_at) VALUES (?, ?, ?)`, strings.TrimSpace(teamName), slug, now)
		if err == nil {
			teamID, _ = res.LastInsertId()
		}
		return err
	}); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO team_members (team_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`, teamID, u.ID, now); err != nil {
		return nil, err
	}
	return u, tx.Commit()
}

// dummyHash makes failed lookups cost the same as wrong passwords, so login
// timing doesn't reveal which emails exist.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("burrow-timing-equalizer"), bcrypt.DefaultCost)

func (s *Store) Authenticate(ctx context.Context, email, password string) (*User, error) {
	var u User
	var hash string
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT id, email, is_admin, password_hash, created_at FROM users WHERE email = ?`, strings.TrimSpace(email)).
		Scan(&u.ID, &u.Email, &u.IsAdmin, &hash, &created)
	if errors.Is(err, sql.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return nil, ErrBadCredentials
	} else if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return nil, ErrBadCredentials
	}
	u.CreatedAt = fromMS(created)
	return &u, nil
}

func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	var u User
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT id, email, is_admin, created_at FROM users WHERE id = ?`, id).Scan(&u.ID, &u.Email, &u.IsAdmin, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	u.CreatedAt = fromMS(created)
	return &u, err
}

type UserListing struct {
	User
	Teams []string
}

func (s *Store) ListUsers(ctx context.Context) ([]UserListing, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.email, u.is_admin, u.created_at,
		       COALESCE((SELECT GROUP_CONCAT(t.name, char(31)) FROM team_members m JOIN teams t ON t.id = m.team_id WHERE m.user_id = u.id), '')
		FROM users u ORDER BY u.email COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserListing
	for rows.Next() {
		var ul UserListing
		var created int64
		var teams string
		if err := rows.Scan(&ul.ID, &ul.Email, &ul.IsAdmin, &created, &teams); err != nil {
			return nil, err
		}
		ul.CreatedAt = fromMS(created)
		if teams != "" {
			ul.Teams = strings.Split(teams, "\x1f")
		}
		out = append(out, ul)
	}
	return out, rows.Err()
}

func adminGuard(ctx context.Context, tx *sql.Tx, userID int64) error {
	var isAdmin bool
	var admins int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id = ? AND is_admin), 0), COUNT(*) FILTER (WHERE is_admin) FROM users`, userID).
		Scan(&isAdmin, &admins); err != nil {
		return err
	}
	if isAdmin && admins <= 1 {
		return ErrLastAdmin
	}
	return nil
}

func (s *Store) SetAdmin(ctx context.Context, userID int64, admin bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if !admin {
		if err := adminGuard(ctx, tx, userID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET is_admin = ? WHERE id = ?`, admin, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteUser removes the account, its sessions and memberships (cascade).
// Teams it solely owned remain and are manageable by admins.
func (s *Store) DeleteUser(ctx context.Context, userID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := adminGuard(ctx, tx, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// ChangePassword verifies the current password, sets the new one, and logs
// out every other session for this user.
func (s *Store) ChangePassword(ctx context.Context, userID int64, current, next, keepSession string) error {
	var email string
	if err := s.db.QueryRowContext(ctx, `SELECT email FROM users WHERE id = ?`, userID).Scan(&email); err != nil {
		return err
	}
	if _, err := s.Authenticate(ctx, email, current); err != nil {
		return errors.New("current password is incorrect")
	}
	if err := validateCredentials(email, next); err != nil {
		return err
	}
	hash, err := hashPassword(next)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND token != ?`, userID, hashToken(keepSession)); err != nil {
		return err
	}
	return tx.Commit()
}

// hashToken is what the database stores instead of a session or invite
// token, so a copy of the database can't be used to log in or join a team.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Store) CreateSession(ctx context.Context, userID int64, ttl time.Duration) (*Session, error) {
	sess := &Session{Token: randomHex(32), CSRF: randomHex(32), ExpiresAt: time.Now().Add(ttl).UTC()}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token, user_id, csrf, expires_at) VALUES (?, ?, ?, ?)`,
		hashToken(sess.Token), userID, sess.CSRF, ms(sess.ExpiresAt))
	if err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *Store) SessionByToken(ctx context.Context, token string) (*Session, error) {
	var sess Session
	var exp, created int64
	err := s.db.QueryRowContext(ctx, `
		SELECT s.csrf, s.expires_at, u.id, u.email, u.is_admin, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token = ? AND s.expires_at > ?`, hashToken(token), ms(time.Now())).
		Scan(&sess.CSRF, &exp, &sess.User.ID, &sess.User.Email, &sess.User.IsAdmin, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	sess.Token = token
	sess.ExpiresAt, sess.User.CreatedAt = fromMS(exp), fromMS(created)
	return &sess, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, hashToken(token))
	return err
}

func (s *Store) PruneSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, ms(time.Now()))
	return err
}
