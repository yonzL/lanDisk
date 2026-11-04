package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newAdmin(t *testing.T) (*Store, User) {
	t.Helper()
	st := openTemp(t)
	ctx := context.Background()
	created, err := st.CreateAdmin(ctx, "hash", true)
	if err != nil || !created {
		t.Fatalf("create admin: created=%v err=%v", created, err)
	}
	u, err := st.UserByUsername(ctx, AdminUsername)
	if err != nil {
		t.Fatalf("read admin: %v", err)
	}
	return st, u
}

func TestSessionSurvivesAnyIdlePeriod(t *testing.T) {
	st, u := newAdmin(t)
	ctx := context.Background()
	now := time.Now()

	if err := st.CreateSession(ctx, "long", u.ID, now); err != nil {
		t.Fatalf("create session: %v", err)
	}
	sess, ok, err := st.TouchSession(ctx, "long", now.Add(10*time.Minute))
	if err != nil || !ok {
		t.Fatalf("touch: ok=%v err=%v", ok, err)
	}
	if !sess.MustChange {
		t.Fatal("must_change not carried on the session")
	}
	// A year of silence must not end the session: the admin stays signed in until
	// they log out.
	if _, ok, err := st.TouchSession(ctx, "long", now.Add(365*24*time.Hour)); err != nil || !ok {
		t.Fatalf("session expired after idle time: ok=%v err=%v", ok, err)
	}
}

func TestSetPasswordDropsSessions(t *testing.T) {
	st, u := newAdmin(t)
	ctx := context.Background()
	now := time.Now()

	if err := st.CreateSession(ctx, "victim", u.ID, now); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := st.SetPassword(ctx, u.ID, "new-hash", false); err != nil {
		t.Fatalf("set password: %v", err)
	}
	if _, ok, err := st.TouchSession(ctx, "victim", now); err != nil || ok {
		t.Fatalf("session survived a password change: ok=%v err=%v", ok, err)
	}
}

func TestSetCredentialsRenamesAndDropsSessions(t *testing.T) {
	st, u := newAdmin(t)
	ctx := context.Background()
	if err := st.CreateSession(ctx, "old", u.ID, time.Now()); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := st.SetCredentials(ctx, u.ID, "ops", "new-hash", false); err != nil {
		t.Fatalf("set credentials: %v", err)
	}
	renamed, err := st.SingleAdmin(ctx)
	if err != nil {
		t.Fatalf("read admin: %v", err)
	}
	if renamed.Username != "ops" || renamed.PassHash != "new-hash" {
		t.Fatalf("admin = %+v, want ops/new-hash", renamed)
	}
	if _, ok, _ := st.TouchSession(ctx, "old", time.Now()); ok {
		t.Fatal("old session survived a credential change")
	}
	if _, err := st.UserByUsername(ctx, AdminUsername); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old username still resolves: %v", err)
	}
}

func TestSetCredentialsRejectsDuplicateUsername(t *testing.T) {
	st, u := newAdmin(t)
	ctx := context.Background()
	// A second account exists only in tests; the uniqueness rule must still hold.
	if _, err := st.db.Exec("INSERT INTO users(username, pass_hash, must_change) VALUES('taken', 'h', 0)"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCredentials(ctx, u.ID, "taken", "h2", false); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate username = %v, want ErrConflict", err)
	}
}
