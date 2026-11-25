package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// APITokenPrefix starts every API token, so a leaked one is easy to spot
// (and to search for in secret scanners).
const APITokenPrefix = "brw_"

// MaxAPITokens caps how many tokens one user can hold.
const MaxAPITokens = 25

// ErrTooManyTokens is returned when a user already holds MaxAPITokens.
var ErrTooManyTokens = errors.New("you already have the most API tokens allowed; revoke one first")

// APIToken is a read-only credential for the JSON API. It acts as its user,
// without Instance Admin: it sees the projects of that user's teams.
type APIToken struct {
	ID         int64
	UserID     int64
	Name       string
	Hint       string // the last characters, to tell tokens apart
	CreatedAt  time.Time
	LastUsedAt time.Time // zero: never used
}

// CreateAPIToken makes a token for userID and returns it with its secret,
// which is shown once: only its hash is stored.
func (s *Store) CreateAPIToken(ctx context.Context, userID int64, name string) (*APIToken, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "API token"
	}
	if len(name) > 100 {
		name = name[:100]
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE user_id = ?`, userID).Scan(&n); err != nil {
		return nil, "", err
	}
	if n >= MaxAPITokens {
		return nil, "", ErrTooManyTokens
	}
	secret := APITokenPrefix + randomHex(24)
	t := &APIToken{UserID: userID, Name: name, Hint: secret[len(secret)-4:], CreatedAt: time.Now().UTC()}
	res, err := s.db.ExecContext(ctx, `INSERT INTO api_tokens (user_id, name, token_hash, hint, created_at) VALUES (?, ?, ?, ?, ?)`,
		userID, t.Name, hashToken(secret), t.Hint, ms(t.CreatedAt))
	if err != nil {
		return nil, "", err
	}
	t.ID, _ = res.LastInsertId()
	return t, secret, nil
}

// ListAPITokens returns userID's tokens, newest first.
func (s *Store) ListAPITokens(ctx context.Context, userID int64) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, name, hint, created_at, last_used_at
		FROM api_tokens WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		var t APIToken
		var created, used int64
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.Hint, &created, &used); err != nil {
			return nil, err
		}
		t.CreatedAt = fromMS(created)
		if used > 0 {
			t.LastUsedAt = fromMS(used)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteAPIToken revokes one of userID's tokens. Someone else's token
// reads as not found.
func (s *Store) DeleteAPIToken(ctx context.Context, userID, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// apiTokenTouchEvery limits last-used writes to one a minute per token, so
// a dashboard refreshing every few seconds doesn't write on every read.
const apiTokenTouchEvery = time.Minute

// UserByAPIToken resolves a token secret to its user, as a regular user
// (IsAdmin is always false: tokens never carry Instance Admin), and records
// that the token was used.
func (s *Store) UserByAPIToken(ctx context.Context, secret string) (*User, error) {
	if !strings.HasPrefix(secret, APITokenPrefix) {
		return nil, ErrNotFound
	}
	var u User
	var id, created int64
	err := s.db.QueryRowContext(ctx, `
		SELECT t.id, u.id, u.email, u.created_at
		FROM api_tokens t JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = ?`, hashToken(secret)).Scan(&id, &u.ID, &u.Email, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	u.CreatedAt = fromMS(created)
	now := time.Now()
	_, _ = s.db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ? AND last_used_at < ?`,
		ms(now), id, ms(now.Add(-apiTokenTouchEvery)))
	return &u, nil
}
