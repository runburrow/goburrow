package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Webhook is an alert destination for one project.
type Webhook struct {
	ID           int64
	ProjectID    int64
	URL          string
	OnNew        bool // fire when an issue is first seen
	OnRegression bool // fire when a resolved issue happens again
	CreatedAt    time.Time
}

// MaxWebhooksPerProject keeps one noisy project from fanning out without bound.
const MaxWebhooksPerProject = 10

var ErrTooManyWebhooks = errors.New("too many webhooks")

func (s *Store) CreateWebhook(ctx context.Context, projectID int64, url string, onNew, onRegression bool) (*Webhook, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhooks WHERE project_id = ?`, projectID).Scan(&n); err != nil {
		return nil, err
	}
	if n >= MaxWebhooksPerProject {
		return nil, ErrTooManyWebhooks
	}
	wh := &Webhook{ProjectID: projectID, URL: url, OnNew: onNew, OnRegression: onRegression, CreatedAt: time.Now().UTC()}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO webhooks (project_id, url, on_new, on_regression, created_at) VALUES (?, ?, ?, ?, ?)`,
		projectID, url, onNew, onRegression, ms(wh.CreatedAt))
	if err != nil {
		return nil, err
	}
	wh.ID, _ = res.LastInsertId()
	return wh, nil
}

func (s *Store) ListWebhooks(ctx context.Context, projectID int64) ([]*Webhook, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, url, on_new, on_regression, created_at FROM webhooks WHERE project_id = ? ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Webhook
	for rows.Next() {
		wh, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, wh)
	}
	return out, rows.Err()
}

// WebhookByID returns the project's webhook id; ErrNotFound if it belongs
// to another project.
func (s *Store) WebhookByID(ctx context.Context, projectID, id int64) (*Webhook, error) {
	return scanWebhook(s.db.QueryRowContext(ctx,
		`SELECT id, project_id, url, on_new, on_regression, created_at FROM webhooks WHERE id = ? AND project_id = ?`, id, projectID))
}

func (s *Store) DeleteWebhook(ctx context.Context, projectID, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM webhooks WHERE id = ? AND project_id = ?`, id, projectID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanWebhook(sc interface{ Scan(...any) error }) (*Webhook, error) {
	var wh Webhook
	var created int64
	if err := sc.Scan(&wh.ID, &wh.ProjectID, &wh.URL, &wh.OnNew, &wh.OnRegression, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	wh.CreatedAt = fromMS(created)
	return &wh, nil
}
