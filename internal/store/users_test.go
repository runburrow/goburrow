package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestFirstUserOnlyOnce(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// Simultaneous setup submissions: exactly one wins.
	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = st.Setup(context.Background(), "admin@burrow.test", "long-enough-pw", "")
		}(i)
	}
	wg.Wait()
	won := 0
	for _, err := range errs {
		if err == nil {
			won++
		} else if !errors.Is(err, ErrSetupDone) {
			t.Errorf("unexpected error: %v", err)
		}
	}
	if n, _ := st.UserCount(context.Background()); won != 1 || n != 1 {
		t.Fatalf("winners = %d, users = %d; want 1 and 1", won, n)
	}
}

func TestSessionTokensStoredHashed(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, _ := st.Setup(ctx, "admin@burrow.test", "long-enough-pw", "")
	sess, err := st.CreateSession(ctx, u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	st.db.QueryRowContext(ctx, `SELECT token FROM sessions`).Scan(&stored)
	if stored == sess.Token || stored != hashToken(sess.Token) {
		t.Fatal("the sessions table should hold only the token's hash")
	}
	got, err := st.SessionByToken(ctx, sess.Token)
	if err != nil || got.Token != sess.Token || got.User.ID != u.ID {
		t.Fatalf("lookup by token: %v", err)
	}
	if _, err := st.SessionByToken(ctx, stored); !errors.Is(err, ErrNotFound) {
		t.Fatal("the stored hash must not work as a token")
	}
	st.DeleteSession(ctx, sess.Token)
	if _, err := st.SessionByToken(ctx, sess.Token); !errors.Is(err, ErrNotFound) {
		t.Fatal("logout should delete the session")
	}
}

func TestInstanceAdminSwitch(t *testing.T) {
	s := open(t)
	root, err := s.Setup(ctx, "root@x.co", "long-enough-pw", "T")
	if err != nil {
		t.Fatal(err)
	}
	bob := mustUser(t, s, "bob@x.co", false)
	rs, _ := s.CreateSession(ctx, root.ID, time.Hour)
	bs, _ := s.CreateSession(ctx, bob.ID, time.Hour)

	got, _ := s.SessionByToken(ctx, rs.Token)
	if !got.CanElevate || got.User.IsAdmin {
		t.Fatalf("admins start as regular users: %+v", got)
	}
	s.SetElevated(ctx, rs.Token, time.Now().Add(time.Hour))
	if got, _ = s.SessionByToken(ctx, rs.Token); !got.User.IsAdmin || got.ElevatedUntil.IsZero() {
		t.Errorf("switched on: %+v", got)
	}
	s.SetElevated(ctx, rs.Token, time.Now().Add(-time.Second))
	if got, _ = s.SessionByToken(ctx, rs.Token); got.User.IsAdmin {
		t.Error("Instance Admin should run out")
	}
	s.SetElevated(ctx, bs.Token, time.Now().Add(time.Hour))
	if got, _ = s.SessionByToken(ctx, bs.Token); got.CanElevate || got.User.IsAdmin {
		t.Error("a non-admin's session was switched on")
	}
}
