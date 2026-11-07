package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/runburrow/goburrow/internal/event"
)

func TestIssueAndProjectActivity(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_activity.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, 0, "Test Project", "go")
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	// Create 2 issues by ingesting events
	ev1 := &event.Event{
		EventID:   event.NewEventID(),
		Timestamp: now,
		Level:     "error",
		Exceptions: []event.Exception{{
			Type:  "Error1",
			Value: "boom 1",
		}},
	}
	res1, err := st.SaveEvent(ctx, p.ID, ev1, now)
	if err != nil {
		t.Fatalf("SaveEvent 1 failed: %v", err)
	}

	ev2 := &event.Event{
		EventID:   event.NewEventID(),
		Timestamp: now,
		Level:     "error",
		Exceptions: []event.Exception{{
			Type:  "Error2",
			Value: "boom 2",
		}},
	}
	res2, err := st.SaveEvent(ctx, p.ID, ev2, now)
	if err != nil {
		t.Fatalf("SaveEvent 2 failed: %v", err)
	}

	// Add more events across different days for issue 1
	// Day 13 (today = day index 13): 2 events
	ev1_today2 := &event.Event{
		EventID:   event.NewEventID(),
		Timestamp: now,
		Level:     "error",
		Exceptions: []event.Exception{{
			Type:  "Error1",
			Value: "boom 1",
		}},
	}
	if _, err := st.SaveEvent(ctx, p.ID, ev1_today2, now.Add(time.Hour)); err != nil {
		t.Fatalf("SaveEvent today failed: %v", err)
	}

	// Day 12 (yesterday, index 12): 1 event
	ev1_yesterday := &event.Event{
		EventID:   event.NewEventID(),
		Timestamp: now.Add(-24 * time.Hour),
		Level:     "error",
		Exceptions: []event.Exception{{
			Type:  "Error1",
			Value: "boom 1",
		}},
	}
	if _, err := st.SaveEvent(ctx, p.ID, ev1_yesterday, now.Add(-24*time.Hour)); err != nil {
		t.Fatalf("SaveEvent yesterday failed: %v", err)
	}

	// Day 0 (13 days ago, oldest bar in 14-day window): 3 events
	for i := 0; i < 3; i++ {
		ev := &event.Event{
			EventID:   event.NewEventID(),
			Timestamp: now.Add(-13 * 24 * time.Hour),
			Level:     "error",
			Exceptions: []event.Exception{{
				Type:  "Error1",
				Value: "boom 1",
			}},
		}
		if _, err := st.SaveEvent(ctx, p.ID, ev, now.Add(-13*24*time.Hour).Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("SaveEvent day 0 failed: %v", err)
		}
	}

	// Outside window (14 days ago = index -1): 5 events (should NOT appear in 14-day window)
	ev_old := &event.Event{
		EventID:   event.NewEventID(),
		Timestamp: now.Add(-14 * 24 * time.Hour),
		Level:     "error",
		Exceptions: []event.Exception{{
			Type:  "Error1",
			Value: "boom 1",
		}},
	}
	if _, err := st.SaveEvent(ctx, p.ID, ev_old, now.Add(-14*24*time.Hour)); err != nil {
		t.Fatalf("SaveEvent old failed: %v", err)
	}

	// Outside window (future event on day 14 = tomorrow): should NOT appear in 14-day window
	ev_future := &event.Event{
		EventID:   event.NewEventID(),
		Timestamp: now.Add(24 * time.Hour),
		Level:     "error",
		Exceptions: []event.Exception{{
			Type:  "Error1",
			Value: "boom 1",
		}},
	}
	if _, err := st.SaveEvent(ctx, p.ID, ev_future, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("SaveEvent future failed: %v", err)
	}

	// Exact midnight UTC boundaries for day 0 and day 13
	day0Midnight := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -13)
	// 1ms before day 0 start -> outside window
	ev_before_day0 := &event.Event{
		EventID:   event.NewEventID(),
		Timestamp: day0Midnight.Add(-time.Millisecond),
		Level:     "error",
		Exceptions: []event.Exception{{
			Type:  "Error2",
			Value: "boom 2",
		}},
	}
	if _, err := st.SaveEvent(ctx, p.ID, ev_before_day0, day0Midnight.Add(-time.Millisecond)); err != nil {
		t.Fatalf("SaveEvent before day0 failed: %v", err)
	}
	// Exact day 0 midnight -> inside day 0
	ev_at_day0 := &event.Event{
		EventID:   event.NewEventID(),
		Timestamp: day0Midnight,
		Level:     "error",
		Exceptions: []event.Exception{{
			Type:  "Error2",
			Value: "boom 2",
		}},
	}
	if _, err := st.SaveEvent(ctx, p.ID, ev_at_day0, day0Midnight); err != nil {
		t.Fatalf("SaveEvent at day0 failed: %v", err)
	}

	// IssueActivity check
	// Issue 1: day 0 has 3, day 12 has 1, day 13 has 2. All other days 0. Total length 14.
	// Issue 2: day 0 has 1 (at midnight), day 13 has 1. All other days 0. Total length 14.
	// Empty issue (e.g. 9999): all 14 zeros.
	actMap, err := st.IssueActivity(ctx, []int64{res1.IssueID, res2.IssueID, 9999}, 14, now)
	if err != nil {
		t.Fatalf("IssueActivity failed: %v", err)
	}

	if len(actMap) != 3 {
		t.Fatalf("expected 3 entries in actMap, got %d", len(actMap))
	}

	act1 := actMap[res1.IssueID]
	if len(act1) != 14 {
		t.Fatalf("expected 14 days for issue 1, got %d", len(act1))
	}
	if act1[0] != 3 {
		t.Errorf("expected day 0 count 3, got %d", act1[0])
	}
	if act1[12] != 1 {
		t.Errorf("expected day 12 count 1, got %d", act1[12])
	}
	if act1[13] != 2 {
		t.Errorf("expected day 13 count 2, got %d", act1[13])
	}
	for d := 1; d <= 11; d++ {
		if act1[d] != 0 {
			t.Errorf("expected day %d count 0, got %d", d, act1[d])
		}
	}

	act2 := actMap[res2.IssueID]
	if len(act2) != 14 {
		t.Fatalf("expected 14 days for issue 2, got %d", len(act2))
	}
	if act2[0] != 1 {
		t.Errorf("expected day 0 count 1 for issue 2, got %d", act2[0])
	}
	if act2[13] != 1 {
		t.Errorf("expected day 13 count 1 for issue 2, got %d", act2[13])
	}
	for d := 1; d < 13; d++ {
		if act2[d] != 0 {
			t.Errorf("expected day %d count 0 for issue 2, got %d", d, act2[d])
		}
	}

	// DailyCounts check (delegates to IssueActivity)
	daily1, err := st.DailyCounts(ctx, res1.IssueID, 14, now)
	if err != nil {
		t.Fatalf("DailyCounts failed: %v", err)
	}
	if len(daily1) != 14 || daily1[0] != 3 || daily1[12] != 1 || daily1[13] != 2 {
		t.Errorf("unexpected DailyCounts for issue 1: %v", daily1)
	}

	actEmpty := actMap[9999]
	if len(actEmpty) != 14 {
		t.Fatalf("expected 14 days for empty issue, got %d", len(actEmpty))
	}
	for d := 0; d < 14; d++ {
		if actEmpty[d] != 0 {
			t.Errorf("expected day %d count 0 for empty issue, got %d", d, actEmpty[d])
		}
	}

	// ProjectActivity check
	// Project 1 sum: day 0 has 4 (3 from issue1 + 1 from issue2), day 12 has 1, day 13 has 3 (2 from issue1 + 1 from issue2).
	// Empty project: all zeros.
	pEmpty, err := st.CreateProject(ctx, 0, "Empty Project", "other")
	if err != nil {
		t.Fatalf("CreateProject empty failed: %v", err)
	}

	projMap, err := st.ProjectActivity(ctx, []int64{p.ID, pEmpty.ID}, 14, now)
	if err != nil {
		t.Fatalf("ProjectActivity failed: %v", err)
	}

	pAct1 := projMap[p.ID]
	if len(pAct1) != 14 {
		t.Fatalf("expected 14 days for project 1, got %d", len(pAct1))
	}
	if pAct1[0] != 4 {
		t.Errorf("project day 0: expected 4, got %d", pAct1[0])
	}
	if pAct1[12] != 1 {
		t.Errorf("project day 12: expected 1, got %d", pAct1[12])
	}
	if pAct1[13] != 3 {
		t.Errorf("project day 13: expected 3, got %d", pAct1[13])
	}

	pActEmpty := projMap[pEmpty.ID]
	if len(pActEmpty) != 14 {
		t.Fatalf("expected 14 days for empty project, got %d", len(pActEmpty))
	}
	for d := 0; d < 14; d++ {
		if pActEmpty[d] != 0 {
			t.Errorf("empty project day %d: expected 0, got %d", d, pActEmpty[d])
		}
	}

	// Test IssueStatusCounts
	sc, err := st.IssueStatusCounts(ctx, p.ID)
	if err != nil {
		t.Fatalf("IssueStatusCounts failed: %v", err)
	}
	if sc.Unresolved != 2 || sc.All != 2 || sc.Resolved != 0 || sc.Ignored != 0 {
		t.Errorf("unexpected status counts: %+v", sc)
	}

	// Resolve issue 1 and re-check status counts
	if err := st.SetIssueStatus(ctx, res1.IssueID, StatusResolved); err != nil {
		t.Fatalf("SetIssueStatus failed: %v", err)
	}
	sc, err = st.IssueStatusCounts(ctx, p.ID)
	if err != nil {
		t.Fatalf("IssueStatusCounts failed: %v", err)
	}
	if sc.Unresolved != 1 || sc.Resolved != 1 || sc.All != 2 {
		t.Errorf("unexpected status counts after resolve: %+v", sc)
	}
}
