// Package store persists projects, issues, events and users in SQLite.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, keeps the binary cgo-free
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and applies
// any pending migrations.
func Open(path string) (*Store, error) {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	// Write transactions take the lock up front, avoiding SQLITE_BUSY on
	// read->write lock upgrades under concurrent ingestion.
	q.Add("_txlock", "immediate")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// migrations are append-only. Never edit a shipped migration; add a new one.
var migrations = []string{
	`CREATE TABLE users (
		id            INTEGER PRIMARY KEY,
		email         TEXT NOT NULL UNIQUE COLLATE NOCASE,
		password_hash TEXT NOT NULL,
		created_at    INTEGER NOT NULL
	);
	CREATE TABLE sessions (
		token      TEXT PRIMARY KEY,
		user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		csrf       TEXT NOT NULL,
		expires_at INTEGER NOT NULL
	);
	CREATE TABLE projects (
		id         INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		slug       TEXT NOT NULL UNIQUE,
		platform   TEXT NOT NULL DEFAULT '',
		public_key TEXT NOT NULL UNIQUE,
		created_at INTEGER NOT NULL
	);
	CREATE TABLE issues (
		id          INTEGER PRIMARY KEY,
		project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		fingerprint TEXT NOT NULL,
		title       TEXT NOT NULL,
		culprit     TEXT NOT NULL DEFAULT '',
		level       TEXT NOT NULL,
		status      TEXT NOT NULL DEFAULT 'unresolved',
		first_seen  INTEGER NOT NULL,
		last_seen   INTEGER NOT NULL,
		times_seen  INTEGER NOT NULL DEFAULT 0,
		UNIQUE (project_id, fingerprint)
	);
	CREATE INDEX issues_by_project ON issues(project_id, status, last_seen DESC);
	CREATE TABLE events (
		id          INTEGER PRIMARY KEY,
		event_id    TEXT NOT NULL,
		project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		issue_id    INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
		timestamp   INTEGER NOT NULL,
		received_at INTEGER NOT NULL,
		level       TEXT NOT NULL,
		release     TEXT NOT NULL DEFAULT '',
		environment TEXT NOT NULL DEFAULT '',
		data        TEXT NOT NULL,
		UNIQUE (project_id, event_id)
	);
	CREATE INDEX events_by_issue ON events(issue_id, received_at DESC, id DESC);
	CREATE INDEX events_by_received ON events(received_at);`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var v int
	err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (0)`); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE schema_version SET version = ?`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func ms(t time.Time) int64     { return t.UnixMilli() }
func fromMS(v int64) time.Time { return time.UnixMilli(v).UTC() }
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---------------------------------------------------------------- projects

type Project struct {
	ID        int64
	Name      string
	Slug      string
	Platform  string
	PublicKey string
	CreatedAt time.Time

	// Populated by ListProjects only.
	UnresolvedCount int
	LastSeen        time.Time
}

func (s *Store) CreateProject(ctx context.Context, name, platform string) (*Project, error) {
	base := Slugify(name)
	if base == "" {
		base = "project"
	}
	if base == "new" { // would collide with the /projects/new route
		base = "new-project"
	}
	p := &Project{Name: name, Platform: platform, PublicKey: randomHex(16), CreatedAt: time.Now().UTC()}
	for i := 0; i < 100; i++ {
		slug := base
		if i > 0 {
			slug = fmt.Sprintf("%s-%d", base, i+1)
		}
		res, err := s.db.ExecContext(ctx,
			`INSERT INTO projects (name, slug, platform, public_key, created_at) VALUES (?, ?, ?, ?, ?)`,
			name, slug, platform, p.PublicKey, ms(p.CreatedAt))
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") && strings.Contains(err.Error(), "slug") {
				continue
			}
			return nil, err
		}
		p.ID, _ = res.LastInsertId()
		p.Slug = slug
		return p, nil
	}
	return nil, errors.New("could not allocate a unique project slug")
}

const projectCols = `id, name, slug, platform, public_key, created_at`

func scanProject(sc interface{ Scan(...any) error }) (*Project, error) {
	var p Project
	var created int64
	if err := sc.Scan(&p.ID, &p.Name, &p.Slug, &p.Platform, &p.PublicKey, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.CreatedAt = fromMS(created)
	return &p, nil
}

func (s *Store) ProjectByID(ctx context.Context, id int64) (*Project, error) {
	return scanProject(s.db.QueryRowContext(ctx, `SELECT `+projectCols+` FROM projects WHERE id = ?`, id))
}

func (s *Store) ProjectBySlug(ctx context.Context, slug string) (*Project, error) {
	return scanProject(s.db.QueryRowContext(ctx, `SELECT `+projectCols+` FROM projects WHERE slug = ?`, slug))
}

func (s *Store) ListProjects(ctx context.Context) ([]*Project, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, p.name, p.slug, p.platform, p.public_key, p.created_at,
		       (SELECT COUNT(*) FROM issues i WHERE i.project_id = p.id AND i.status = 'unresolved'),
		       COALESCE((SELECT MAX(last_seen) FROM issues i WHERE i.project_id = p.id), 0)
		FROM projects p ORDER BY p.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Project
	for rows.Next() {
		var p Project
		var created, last int64
		if err := rows.Scan(&p.ID, &p.Name, &p.Slug, &p.Platform, &p.PublicKey, &created, &p.UnresolvedCount, &last); err != nil {
			return nil, err
		}
		p.CreatedAt = fromMS(created)
		if last > 0 {
			p.LastSeen = fromMS(last)
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

func (s *Store) DeleteProject(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	return err
}

func (s *Store) RotateProjectKey(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET public_key = ? WHERE id = ?`, randomHex(16), id)
	return err
}

// Slugify turns "My Cool App!" into "my-cool-app".
func Slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if len(out) > 50 {
		out = strings.TrimRight(out[:50], "-")
	}
	return out
}
