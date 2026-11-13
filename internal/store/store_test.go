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

func TestSnapshotStoreKeepsLatestPerBackup(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	ss, ctx := NewSnapshotStore(db), context.Background()
	at := time.Now().UTC().Truncate(time.Second)

	if got, err := ss.LatestByBackup(ctx, []string{"a"}); err != nil || len(got) != 0 {
		t.Fatalf("empty store: %v %v", got, err)
	}
	for _, sn := range []domain.SnapshotInfo{
		{ID: "old", Timestamp: at.Add(-time.Hour), FilesAdded: 9, FilesChanged: 9, BytesAdded: 9},
		{ID: "new", Timestamp: at, FilesAdded: 2, FilesChanged: 1, BytesAdded: 1 << 40},
	} {
		if err := ss.SaveLatest(ctx, "a", sn); err != nil {
			t.Fatal(err)
		}
	}
	if err := ss.SaveLatest(ctx, "b", domain.SnapshotInfo{ID: "other", Timestamp: at}); err != nil {
		t.Fatal(err)
	}
	got, err := ss.LatestByBackup(ctx, []string{"a", "nope"})
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v err %v", got, err)
	}
	g := got["a"]
	if g.ID != "new" || !g.Timestamp.Equal(at) || g.FilesAdded != 2 || g.FilesChanged != 1 || g.BytesAdded != 1<<40 {
		t.Fatalf("got %+v", g)
	}
}

func TestRestoreActivitiesDoNotAffectBackupAggregates(t *testing.T) {
	as, _ := newDB(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)

	b := act("b1", base)
	if err := as.Create(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := as.Finish(ctx, b.ID, domain.StatusSuccess, base.Add(time.Second), ""); err != nil {
		t.Fatal(err)
	}
	r := act("b1", base.Add(time.Minute))
	r.Kind = domain.KindTestRestore
	if err := as.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := as.Finish(ctx, r.ID, domain.StatusSuccess, base.Add(2*time.Minute), ""); err != nil {
		t.Fatal(err)
	}

	last, _ := as.LastByBackup(ctx, []string{"b1"})
	if last["b1"].ID != b.ID {
		t.Fatalf("last backup activity = %+v, want the backup", last["b1"])
	}
	if last["b1"].Kind != domain.KindBackup {
		t.Fatalf("kind = %q", last["b1"].Kind)
	}
	ok, _ := as.LastSuccessByBackup(ctx, []string{"b1"})
	if !ok["b1"].Equal(base.Add(time.Second)) {
		t.Fatalf("last success = %v, restores must not count", ok["b1"])
	}
	got, err := as.Get(ctx, r.ID)
	if err != nil || got.Kind != domain.KindTestRestore {
		t.Fatalf("%+v %v", got, err)
	}
	if list, _ := as.ListByBackup(ctx, "b1", 10); len(list) != 2 {
		t.Fatalf("activity list must include restores, got %d", len(list))
	}
}

func TestWarningStatusPersistsAndSurvivesMigration(t *testing.T) {
	as, _ := newDB(t)
	ctx := context.Background()
	a := act("b1", time.Now().UTC().Truncate(time.Second))
	if err := as.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := as.Finish(ctx, a.ID, domain.StatusWarning, time.Now(), "restore ok, metadata validation found 1 mismatch"); err != nil {
		t.Fatal(err)
	}
	got, err := as.Get(ctx, a.ID)
	if err != nil || got.Status != domain.StatusWarning || got.Error == "" {
		t.Fatalf("%+v %v", got, err)
	}
	// An interrupted-run sweep must not touch finished warnings.
	if n, _ := as.MarkRunningAsInterrupted(ctx, time.Now()); n != 0 {
		t.Fatalf("interrupted=%d", n)
	}
}
