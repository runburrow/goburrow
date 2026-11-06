package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mozthewah/opensourcedsentryalternative/internal/event"
)

type IssueStatus string

const (
	StatusUnresolved IssueStatus = "unresolved"
	StatusResolved   IssueStatus = "resolved"
	StatusIgnored    IssueStatus = "ignored"
)

func (s IssueStatus) Valid() bool {
	return s == StatusUnresolved || s == StatusResolved || s == StatusIgnored
}

type Issue struct {
	ID          int64
	ProjectID   int64
	Fingerprint string
	Title       string
	Culprit     string
	Level       string
	Status      IssueStatus
	FirstSeen   time.Time
	LastSeen    time.Time
	TimesSeen   int
	Regressed   bool // reopened by a new event after being resolved; cleared on the next status change
}

// IngestResult describes what happened to an incoming event.
type IngestResult struct {
	IssueID   int64
	NewIssue  bool // first event for this fingerprint
	Regressed bool // issue was resolved and has come back
	Duplicate bool // event_id already stored; nothing changed
}

// SaveEvent stores ev and rolls it into its issue, creating or reopening the
// issue as needed. It is idempotent on (project, event_id) so SDK retries
// don't inflate counts.
func (s *Store) SaveEvent(ctx context.Context, projectID int64, ev *event.Event, received time.Time) (*IngestResult, error) {
	data, err := json.Marshal(ev)
	if err != nil {
		return nil, err
	}
	fp := event.Fingerprint(ev)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var existing int64
	err = tx.QueryRowContext(ctx, `SELECT issue_id FROM events WHERE project_id = ? AND event_id = ?`, projectID, ev.EventID).Scan(&existing)
	if err == nil {
		return &IngestResult{IssueID: existing, Duplicate: true}, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	res := &IngestResult{}
	var prevStatus string
	err = tx.QueryRowContext(ctx, `SELECT id, status FROM issues WHERE project_id = ? AND fingerprint = ?`, projectID, fp).Scan(&res.IssueID, &prevStatus)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		r, err := tx.ExecContext(ctx, `
			INSERT INTO issues (project_id, fingerprint, title, culprit, level, status, first_seen, last_seen, times_seen)
			VALUES (?, ?, ?, ?, ?, 'unresolved', ?, ?, 1)`,
			projectID, fp, ev.Title(), ev.Culprit(), ev.Level, ms(received), ms(received))
		if err != nil {
			return nil, err
		}
		res.IssueID, _ = r.LastInsertId()
		res.NewIssue = true
	case err != nil:
		return nil, err
	default:
		res.Regressed = prevStatus == string(StatusResolved)
		// Title/culprit track the latest event so the list reflects reality
		// (e.g. a message template's newest rendering).
		_, err = tx.ExecContext(ctx, `
			UPDATE issues SET
				times_seen = times_seen + 1,
				last_seen  = MAX(last_seen, ?),
				title      = ?,
				culprit    = ?,
				level      = ?,
				status     = CASE WHEN status = 'resolved' THEN 'unresolved' ELSE status END,
				regressed  = CASE WHEN status = 'resolved' THEN 1 ELSE regressed END
			WHERE id = ?`,
			ms(received), ev.Title(), ev.Culprit(), ev.Level, res.IssueID)
		if err != nil {
			return nil, err
		}
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO events (event_id, project_id, issue_id, timestamp, received_at, level, release, environment, data)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.EventID, projectID, res.IssueID, ms(ev.Timestamp), ms(received), ev.Level, ev.Release, ev.Environment, string(data))
	if err != nil {
		return nil, err
	}
	return res, tx.Commit()
}

const issueCols = `id, project_id, fingerprint, title, culprit, level, status, first_seen, last_seen, times_seen, regressed`

func scanIssue(sc interface{ Scan(...any) error }) (*Issue, error) {
	var i Issue
	var first, last int64
	var regressed int
	var status string
	if err := sc.Scan(&i.ID, &i.ProjectID, &i.Fingerprint, &i.Title, &i.Culprit, &i.Level, &status, &first, &last, &i.TimesSeen, &regressed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	i.Status = IssueStatus(status)
	i.Regressed = regressed != 0
	i.FirstSeen, i.LastSeen = fromMS(first), fromMS(last)
	return &i, nil
}

func (s *Store) IssueByID(ctx context.Context, id int64) (*Issue, error) {
	return scanIssue(s.db.QueryRowContext(ctx, `SELECT `+issueCols+` FROM issues WHERE id = ?`, id))
}

type IssueFilter struct {
	Status IssueStatus // empty = any
	Query  string      // substring match on title/culprit
	Sort   string      // "last_seen" (default), "first_seen", "times_seen"
	Limit  int
	Offset int
}

func (s *Store) ListIssues(ctx context.Context, projectID int64, f IssueFilter) ([]*Issue, error) {
	where := []string{"project_id = ?"}
	args := []any{projectID}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, string(f.Status))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + escapeLike(q) + "%"
		where = append(where, `(title LIKE ? ESCAPE '\' OR culprit LIKE ? ESCAPE '\')`)
		args = append(args, like, like)
	}
	order := "last_seen DESC"
	switch f.Sort {
	case "first_seen":
		order = "first_seen DESC"
	case "times_seen":
		order = "times_seen DESC, last_seen DESC"
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+issueCols+` FROM issues WHERE `+strings.Join(where, " AND ")+` ORDER BY `+order+`, id DESC LIMIT ? OFFSET ?`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Issue
	for rows.Next() {
		i, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func (s *Store) SetIssueStatus(ctx context.Context, id int64, st IssueStatus) error {
	if !st.Valid() {
		return errors.New("invalid status")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE issues SET status = ?, regressed = 0 WHERE id = ?`, string(st), id)
	return err
}

func (s *Store) DeleteIssue(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM issues WHERE id = ?`, id)
	return err
}

// ------------------------------------------------------------------ events

type StoredEvent struct {
	ID         int64
	IssueID    int64
	ReceivedAt time.Time
	Event      *event.Event
}

// EventForIssue returns the event with row id eventID belonging to issueID,
// or the latest event when eventID is 0.
func (s *Store) EventForIssue(ctx context.Context, issueID, eventID int64) (*StoredEvent, error) {
	var row *sql.Row
	if eventID == 0 {
		row = s.db.QueryRowContext(ctx, `SELECT id, issue_id, received_at, data FROM events WHERE issue_id = ? ORDER BY received_at DESC, id DESC LIMIT 1`, issueID)
	} else {
		row = s.db.QueryRowContext(ctx, `SELECT id, issue_id, received_at, data FROM events WHERE issue_id = ? AND id = ?`, issueID, eventID)
	}
	var se StoredEvent
	var recv int64
	var data string
	if err := row.Scan(&se.ID, &se.IssueID, &recv, &data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	se.ReceivedAt = fromMS(recv)
	se.Event = &event.Event{}
	if err := json.Unmarshal([]byte(data), se.Event); err != nil {
		return nil, err
	}
	return &se, nil
}

// AdjacentEvents returns the row ids of the next-older and next-newer events
// of the same issue (0 if none).
func (s *Store) AdjacentEvents(ctx context.Context, se *StoredEvent) (older, newer int64, err error) {
	recv := ms(se.ReceivedAt)
	err = s.db.QueryRowContext(ctx, `
		SELECT id FROM events WHERE issue_id = ? AND (received_at < ? OR (received_at = ? AND id < ?))
		ORDER BY received_at DESC, id DESC LIMIT 1`, se.IssueID, recv, recv, se.ID).Scan(&older)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, err
	}
	err = s.db.QueryRowContext(ctx, `
		SELECT id FROM events WHERE issue_id = ? AND (received_at > ? OR (received_at = ? AND id > ?))
		ORDER BY received_at ASC, id ASC LIMIT 1`, se.IssueID, recv, recv, se.ID).Scan(&newer)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, err
	}
	return older, newer, nil
}

type EventSummary struct {
	ID          int64
	EventID     string
	ReceivedAt  time.Time
	Level       string
	Release     string
	Environment string
}

func (s *Store) ListIssueEvents(ctx context.Context, issueID int64, limit int) ([]EventSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, event_id, received_at, level, release, environment FROM events
		WHERE issue_id = ? ORDER BY received_at DESC, id DESC LIMIT ?`, issueID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventSummary
	for rows.Next() {
		var e EventSummary
		var recv int64
		if err := rows.Scan(&e.ID, &e.EventID, &recv, &e.Level, &e.Release, &e.Environment); err != nil {
			return nil, err
		}
		e.ReceivedAt = fromMS(recv)
		out = append(out, e)
	}
	return out, rows.Err()
}

// DailyCounts returns per-day event counts for the issue over the last n
// days (oldest first, ending with the UTC day containing now), for the
// activity chart.
func (s *Store) DailyCounts(ctx context.Context, issueID int64, days int, now time.Time) ([]int, error) {
	if days <= 0 {
		days = ActivityDays
	}
	m, err := s.IssueActivity(ctx, []int64{issueID}, days, now)
	if err != nil {
		return nil, err
	}
	return m[issueID], nil
}

// Prune deletes events received before cutoff and any issues left without
// events. Issue counters (times_seen) are kept as lifetime totals.
func (s *Store) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM events WHERE received_at < ?`, ms(cutoff))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := tx.ExecContext(ctx, `DELETE FROM issues WHERE last_seen < ? AND NOT EXISTS (SELECT 1 FROM events e WHERE e.issue_id = issues.id)`, ms(cutoff)); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}
