package store

import (
	"context"
	"strings"
	"time"
)

// ActivityDays is the width of the activity "barcode": one bar per UTC day,
// oldest first, ending with today.
const ActivityDays = 14

const msPerDay = 24 * 60 * 60 * 1000

// maxIDsPerQuery bounds the IN (...) list so we never approach SQLite's
// bound-variable limit. A page of issues (<=200) is always a single query.
const maxIDsPerQuery = 500

// activityWindow returns [start, end) in unix ms for the last `days` UTC days
// ending with the day containing now.
func activityWindow(now time.Time, days int) (startMS, endMS int64) {
	start := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -(days - 1))
	end := start.AddDate(0, 0, days)
	return ms(start), ms(end)
}

// IssueActivity returns, for each id in issueIDs, the number of events
// received on each of the last `days` UTC days (oldest first, today last,
// zero-filled). Every requested id is present in the result, even with no
// events. It issues one query per maxIDsPerQuery ids (one for any page of
// issues) and is served from the events_by_issue index alone.
func (s *Store) IssueActivity(ctx context.Context, issueIDs []int64, days int, now time.Time) (map[int64][]int, error) {
	return s.activity(ctx, "issue_id", issueIDs, days, now)
}

// ProjectActivity is IssueActivity aggregated per project: daily counts of
// ALL events in the project (every issue regardless of status, including
// resolved/ignored ones, since the events did happen). One query per
// maxIDsPerQuery ids, served from events_by_project_received.
func (s *Store) ProjectActivity(ctx context.Context, projectIDs []int64, days int, now time.Time) (map[int64][]int, error) {
	return s.activity(ctx, "project_id", projectIDs, days, now)
}

// activity is shared by IssueActivity/ProjectActivity. col is a fixed
// identifier chosen by this package, never user input.
func (s *Store) activity(ctx context.Context, col string, ids []int64, days int, now time.Time) (map[int64][]int, error) {
	if days <= 0 {
		days = ActivityDays
	}
	out := make(map[int64][]int, len(ids))
	uniq := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, seen := out[id]; !seen {
			out[id] = make([]int, days)
			uniq = append(uniq, id)
		}
	}
	startMS, endMS := activityWindow(now, days)
	for from := 0; from < len(uniq); from += maxIDsPerQuery {
		chunk := uniq[from:min(from+maxIDsPerQuery, len(uniq))]
		args := make([]any, 0, len(chunk)+3)
		args = append(args, startMS)
		for _, id := range chunk {
			args = append(args, id)
		}
		args = append(args, startMS, endMS)
		rows, err := s.db.QueryContext(ctx,
			`SELECT `+col+`, (received_at - ?) / 86400000 AS d, COUNT(*) FROM events
			 WHERE `+col+` IN (`+strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")+`)
			   AND received_at >= ? AND received_at < ?
			 GROUP BY `+col+`, d`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var d, c int
			if err := rows.Scan(&id, &d, &c); err != nil {
				rows.Close()
				return nil, err
			}
			if counts, ok := out[id]; ok && d >= 0 && d < days {
				counts[d] = c
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// StatusCounts is the number of issues in a project per status.
type StatusCounts struct {
	Unresolved int
	Resolved   int
	Ignored    int
	All        int
}

// IssueStatusCounts returns per-status issue totals for a project in one
// query (served by issues_by_project).
func (s *Store) IssueStatusCounts(ctx context.Context, projectID int64) (StatusCounts, error) {
	return s.FilteredStatusCounts(ctx, projectID, IssueFilter{})
}

// FilteredStatusCounts is IssueStatusCounts restricted to issues matching the
// filter's query and levels (its Status is ignored), so status tabs agree
// with the list underneath them.
func (s *Store) FilteredStatusCounts(ctx context.Context, projectID int64, f IssueFilter) (StatusCounts, error) {
	var sc StatusCounts
	where, args := issueWhere(projectID, f)
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM issues WHERE `+strings.Join(where, " AND ")+` GROUP BY status`, args...)
	if err != nil {
		return sc, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return sc, err
		}
		switch IssueStatus(status) {
		case StatusUnresolved:
			sc.Unresolved = n
		case StatusResolved:
			sc.Resolved = n
		case StatusIgnored:
			sc.Ignored = n
		}
		sc.All += n
	}
	return sc, rows.Err()
}
