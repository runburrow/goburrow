package store

import (
	"context"
	"database/sql"
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

var ErrBadCredentials = errors.New("invalid email or password")

func (s *Store) UserCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(ctx context.Context, email, password string) (*User, error) {
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
	u := &User{Email: email, CreatedAt: time.Now().UTC()}
	res, err := s.db.ExecContext(ctx, `INSERT INTO users (email, password_hash, created_at) VALUES (?, ?, ?)`, email, string(hash), ms(u.CreatedAt))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, errors.New("a user with that email already exists")
		}
		return nil, err
	}
	u.ID, _ = res.LastInsertId()
	return u, nil
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

func (s *Store) CreateSession(ctx context.Context, userID int64, ttl time.Duration) (*Session, error) {
	sess := &Session{Token: randomHex(32), CSRF: randomHex(32), ExpiresAt: time.Now().Add(ttl).UTC()}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token, user_id, csrf, expires_at) VALUES (?, ?, ?, ?)`,
		sess.Token, userID, sess.CSRF, ms(sess.ExpiresAt))
	if err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *Store) SessionByToken(ctx context.Context, token string) (*Session, error) {
	var sess Session
	var exp, created int64
	err := s.db.QueryRowContext(ctx, `
		SELECT s.token, s.csrf, s.expires_at, u.id, u.email, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token = ? AND s.expires_at > ?`, token, ms(time.Now())).
		Scan(&sess.Token, &sess.CSRF, &exp, &sess.User.ID, &sess.User.Email, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	sess.ExpiresAt, sess.User.CreatedAt = fromMS(exp), fromMS(created)
	return &sess, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
	return err
}

func (s *Store) PruneSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, ms(time.Now()))
	return err
}
