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
)

func (s *Store) UserCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateFirstUser creates the admin account during first-run setup. The
// check and the insert share one write transaction, so two simultaneous
// setup submissions can't both create an admin: the second gets
// ErrSetupDone.
func (s *Store) CreateFirstUser(ctx context.Context, email, password string) (*User, error) {
	email = strings.TrimSpace(email)
	if email == "" || !strings.Contains(email, "@") {
		return nil, errors.New("a valid email is required")
	}
	if len(password) < 10 {
		return nil, errors.New("password must be at least 10 characters")
	}
	if len(password) > 72 {
		return nil, errors.New("password must be at most 72 bytes")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil) // immediate: takes the write lock now
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
	u := &User{Email: email, CreatedAt: time.Now().UTC()}
	res, err := tx.ExecContext(ctx, `INSERT INTO users (email, password_hash, created_at) VALUES (?, ?, ?)`, email, string(hash), ms(u.CreatedAt))
	if err != nil {
		return nil, err
	}
	u.ID, _ = res.LastInsertId()
	return u, tx.Commit()
}

// dummyHash makes failed lookups cost the same as wrong passwords, so login
// timing doesn't reveal which emails exist.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("burrow-timing-equalizer"), bcrypt.DefaultCost)

func (s *Store) Authenticate(ctx context.Context, email, password string) (*User, error) {
	var u User
	var hash string
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT id, email, password_hash, created_at FROM users WHERE email = ?`, strings.TrimSpace(email)).
		Scan(&u.ID, &u.Email, &hash, &created)
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

// hashToken is what the sessions table stores instead of the token itself.
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
		SELECT s.csrf, s.expires_at, u.id, u.email, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token = ? AND s.expires_at > ?`, hashToken(token), ms(time.Now())).
		Scan(&sess.CSRF, &exp, &sess.User.ID, &sess.User.Email, &created)
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
