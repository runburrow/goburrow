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
	db, err := sql.Open("sqlite", dsnFor(path))
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

// dsnFor builds the SQLite DSN (pragmas included) for a database file.
func dsnFor(path string) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	// Write transactions take the lock up front, avoiding SQLITE_BUSY on
	// read->write lock upgrades under concurrent ingestion.
	q.Add("_txlock", "immediate")
	return "file:" + path + "?" + q.Encode()
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

	// 2: per-project daily activity (projects list barcode). Lets
	// ProjectActivity answer from this index alone (project_id, received_at)
	// instead of scanning every recent event across all projects.
	`CREATE INDEX events_by_project_received ON events(project_id, received_at);`,

	// 3: remember that an issue came back after being resolved, until the
	// user next sets its status.
	`ALTER TABLE issues ADD COLUMN regressed INTEGER NOT NULL DEFAULT 0;`,

	// 4: per-project alert webhooks, managed in project settings.
	`CREATE TABLE webhooks (
		id            INTEGER PRIMARY KEY,
		project_id    INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		url           TEXT NOT NULL,
		on_new        INTEGER NOT NULL DEFAULT 1,
		on_regression INTEGER NOT NULL DEFAULT 1,
		created_at    INTEGER NOT NULL
	);
	CREATE INDEX webhooks_by_project ON webhooks(project_id, id);`,

	// 5: session tokens are now stored as SHA-256 hashes, so a copy of the
	// database can't be used to log in. Existing sessions can't be
	// converted, so everyone logs in once more.
	`DELETE FROM sessions;`,

	// 6: multi-user + teams (from the teams branch, where it was migration 2). Existing installs keep working: everyone who
	// already had an account was effectively an admin, so they become
	// instance admins and owners of a "Default" team holding all projects.
	`ALTER TABLE users ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0;
	UPDATE users SET is_admin = 1;
	CREATE TABLE teams (
		id         INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		slug       TEXT NOT NULL UNIQUE,
		created_at INTEGER NOT NULL
	);
	CREATE TABLE team_members (
		team_id    INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
		user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		role       TEXT NOT NULL CHECK (role IN ('owner', 'member')),
		created_at INTEGER NOT NULL,
		PRIMARY KEY (team_id, user_id)
	);
	CREATE INDEX team_members_by_user ON team_members(user_id);
	CREATE TABLE invites (
		id         INTEGER PRIMARY KEY,
		token_hash TEXT NOT NULL UNIQUE,
		team_id    INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
		role       TEXT NOT NULL CHECK (role IN ('owner', 'member')),
		email      TEXT NOT NULL DEFAULT '' COLLATE NOCASE,
		created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		used_at    INTEGER
	);
	ALTER TABLE projects ADD COLUMN team_id INTEGER REFERENCES teams(id) ON DELETE CASCADE;
	CREATE INDEX projects_by_team ON projects(team_id);
	INSERT INTO teams (name, slug, created_at)
		SELECT 'Default', 'default', CAST(strftime('%s', 'now') AS INTEGER) * 1000
		WHERE EXISTS (SELECT 1 FROM users) OR EXISTS (SELECT 1 FROM projects);
	UPDATE projects SET team_id = (SELECT id FROM teams WHERE slug = 'default');
	INSERT INTO team_members (team_id, user_id, role, created_at)
		SELECT t.id, u.id, 'owner', u.created_at FROM teams t, users u WHERE t.slug = 'default';`,

	// 7: per-project email alert recipients, managed in project settings.
	`CREATE TABLE email_alerts (
		id            INTEGER PRIMARY KEY,
		project_id    INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		address       TEXT NOT NULL COLLATE NOCASE,
		on_new        INTEGER NOT NULL DEFAULT 1,
		on_regression INTEGER NOT NULL DEFAULT 1,
		created_at    INTEGER NOT NULL,
		UNIQUE (project_id, address)
	);`,
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
	TeamID    int64 // 0: no team; only instance admins see it
	Name      string
	Slug      string
	Platform  string
	PublicKey string
	CreatedAt time.Time

	// Populated by ListProjects only.
	TeamName        string
	UnresolvedCount int
	LastSeen        time.Time
}

// reservedSlugs collide with fixed routes (/projects/new, /teams/new).
var reservedSlugs = map[string]bool{"new": true}

// insertWithSlug derives a slug from name and retries with -2, -3... until
// insert succeeds without a UNIQUE violation on the slug column.
func insertWithSlug(name, fallback string, insert func(slug string) error) (string, error) {
	base := Slugify(name)
	if base == "" {
		base = fallback
	}
	if reservedSlugs[base] {
		base += "-" + fallback
	}
	for i := 0; i < 100; i++ {
		slug := base
		if i > 0 {
			slug = fmt.Sprintf("%s-%d", base, i+1)
		}
		err := insert(slug)
		if err == nil {
			return slug, nil
		}
		if strings.Contains(err.Error(), "UNIQUE") && strings.Contains(err.Error(), "slug") {
			continue
		}
		return "", err
	}
	return "", errors.New("could not allocate a unique slug")
}

func (s *Store) CreateProject(ctx context.Context, teamID int64, name, platform string) (*Project, error) {
	p := &Project{TeamID: teamID, Name: name, Platform: platform, PublicKey: randomHex(16), CreatedAt: time.Now().UTC()}
	slug, err := insertWithSlug(name, "project", func(slug string) error {
		res, err := s.db.ExecContext(ctx,
			`INSERT INTO projects (team_id, name, slug, platform, public_key, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			nullID(teamID), name, slug, platform, p.PublicKey, ms(p.CreatedAt))
		if err == nil {
			p.ID, _ = res.LastInsertId()
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	p.Slug = slug
	return p, nil
}

const projectCols = `id, COALESCE(team_id, 0), name, slug, platform, public_key, created_at`

func scanProject(sc interface{ Scan(...any) error }) (*Project, error) {
	var p Project
	var created int64
	if err := sc.Scan(&p.ID, &p.TeamID, &p.Name, &p.Slug, &p.Platform, &p.PublicKey, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.CreatedAt = fromMS(created)
	return &p, nil
}

// nullID stores 0 as NULL, for optional foreign keys.
func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func (s *Store) ProjectByID(ctx context.Context, id int64) (*Project, error) {
	return scanProject(s.db.QueryRowContext(ctx, `SELECT `+projectCols+` FROM projects WHERE id = ?`, id))
}

func (s *Store) ProjectBySlug(ctx context.Context, slug string) (*Project, error) {
	return scanProject(s.db.QueryRowContext(ctx, `SELECT `+projectCols+` FROM projects WHERE slug = ?`, slug))
}

// ListProjects returns the projects visible to a user: those of teams they
// belong to, or every project for instance admins. teamID > 0 narrows it to
// one team.
func (s *Store) ListProjects(ctx context.Context, userID int64, admin bool, teamID int64) ([]*Project, error) {
	where, args := []string{"1=1"}, []any{}
	if !admin {
		where = append(where, "p.team_id IN (SELECT team_id FROM team_members WHERE user_id = ?)")
		args = append(args, userID)
	}
	if teamID > 0 {
		where = append(where, "p.team_id = ?")
		args = append(args, teamID)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, COALESCE(p.team_id, 0), p.name, p.slug, p.platform, p.public_key, p.created_at,
		       COALESCE(t.name, ''),
		       (SELECT COUNT(*) FROM issues i WHERE i.project_id = p.id AND i.status = 'unresolved'),
		       COALESCE((SELECT MAX(last_seen) FROM issues i WHERE i.project_id = p.id), 0)
		FROM projects p LEFT JOIN teams t ON t.id = p.team_id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY t.name COLLATE NOCASE, p.name COLLATE NOCASE`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Project
	for rows.Next() {
		var p Project
		var created, last int64
		if err := rows.Scan(&p.ID, &p.TeamID, &p.Name, &p.Slug, &p.Platform, &p.PublicKey, &created, &p.TeamName, &p.UnresolvedCount, &last); err != nil {
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
