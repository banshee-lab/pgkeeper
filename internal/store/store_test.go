package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"uuid"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

func newDB(t *testing.T) (*ActivityStore, *SessionStore) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	return NewActivityStore(db), NewSessionStore(db)
}

func act(name string, at time.Time) *domain.Activity {
	return &domain.Activity{ID: uuid.New(), BackupName: name, Database: "app",
		TriggeredBy: domain.TriggerManual, Status: domain.StatusRunning, StartedAt: at, LogPath: "/x.log"}
}

func TestActivityRoundTripOrderLimitInterrupted(t *testing.T) {
	as, _ := newDB(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)
	for i := range 15 {
		if err := as.Create(ctx, act("b1", base.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := as.ListByBackup(ctx, "b1", 999)
	if len(list) != 10 || !list[0].StartedAt.After(list[1].StartedAt) {
		t.Fatalf("clamp/order failed: %d", len(list))
	}
	if n, _ := as.MarkRunningAsInterrupted(ctx, time.Now()); n != 15 {
		t.Fatalf("interrupted=%d", n)
	}
	got, err := as.Get(ctx, list[0].ID)
	if err != nil || got.Status != domain.StatusError || got.Error != "interrupted by restart" || got.FinishedAt == nil {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := as.Get(ctx, uuid.New()); err != domain.ErrNotFound {
		t.Fatal(err)
	}
	last, _ := as.LastByBackup(ctx, []string{"b1", "nope"})
	if len(last) != 1 {
		t.Fatal(last)
	}
}

func TestSessionExpiryAndSingleUseState(t *testing.T) {
	_, ss := newDB(t)
	ctx, now := context.Background(), time.Now().UTC()
	_ = ss.CreateSession(ctx, &domain.Session{IDHash: "h", Subject: "s", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if _, err := ss.GetSession(ctx, "h", now); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.GetSession(ctx, "h", now.Add(2*time.Hour)); err != domain.ErrNotFound {
		t.Fatal(err)
	}
	_ = ss.SaveAuthState(ctx, &domain.AuthState{State: "st", Nonce: "n", PKCEVerifier: "v", RedirectTo: "/", ExpiresAt: now.Add(time.Minute)})
	if _, err := ss.ConsumeAuthState(ctx, "st", now); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.ConsumeAuthState(ctx, "st", now); err != domain.ErrNotFound {
		t.Fatal("state reused")
	}
}