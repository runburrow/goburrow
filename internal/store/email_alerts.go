package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// EmailAlert is an address that gets one project's alerts by email.
type EmailAlert struct {
	ID           int64
	ProjectID    int64
	Address      string
	OnNew        bool
	OnRegression bool
	CreatedAt    time.Time
}

// MaxEmailAlertsPerProject keeps one project from fanning out without bound.
const MaxEmailAlertsPerProject = 10

var (
	ErrTooManyEmailAlerts = errors.New("too many email alerts")
	ErrEmailAlertExists   = errors.New("that address already gets this project's alerts")
)

func (s *Store) CreateEmailAlert(ctx context.Context, projectID int64, address string, onNew, onRegression bool) (*EmailAlert, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM email_alerts WHERE project_id = ?`, projectID).Scan(&n); err != nil {
		return nil, err
	}
	if n >= MaxEmailAlertsPerProject {
		return nil, ErrTooManyEmailAlerts
	}
	a := &EmailAlert{ProjectID: projectID, Address: address, OnNew: onNew, OnRegression: onRegression, CreatedAt: time.Now().UTC()}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO email_alerts (project_id, address, on_new, on_regression, created_at) VALUES (?, ?, ?, ?, ?)`,
		projectID, address, onNew, onRegression, ms(a.CreatedAt))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrEmailAlertExists
		}
		return nil, err
	}
	a.ID, _ = res.LastInsertId()
	return a, tx.Commit()
}

const emailAlertCols = `id, project_id, address, on_new, on_regression, created_at`

func (s *Store) ListEmailAlerts(ctx context.Context, projectID int64) ([]*EmailAlert, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+emailAlertCols+` FROM email_alerts WHERE project_id = ? ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*EmailAlert
	for rows.Next() {
		a, err := scanEmailAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// EmailAlertByID returns the project's email alert id; ErrNotFound if it
// belongs to another project.
func (s *Store) EmailAlertByID(ctx context.Context, projectID, id int64) (*EmailAlert, error) {
	return scanEmailAlert(s.db.QueryRowContext(ctx, `SELECT `+emailAlertCols+` FROM email_alerts WHERE id = ? AND project_id = ?`, id, projectID))
}

func (s *Store) DeleteEmailAlert(ctx context.Context, projectID, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM email_alerts WHERE id = ? AND project_id = ?`, id, projectID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanEmailAlert(sc interface{ Scan(...any) error }) (*EmailAlert, error) {
	var a EmailAlert
	var created int64
	if err := sc.Scan(&a.ID, &a.ProjectID, &a.Address, &a.OnNew, &a.OnRegression, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	a.CreatedAt = fromMS(created)
	return &a, nil
}
