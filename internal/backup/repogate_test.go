package backup

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/restic"
)

func TestRepoGateFIFOAndExclusive(t *testing.T) {
	var g repoGate
	ctx := context.Background()

	relA, err := g.acquire(ctx, "repo", "a", nil)
	if err != nil {
		t.Fatal(err)
	}
	// A different repository is independent.
	relOther, err := g.acquire(ctx, "other", "x", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer relOther()

	var (
		mu    sync.Mutex
		order []string
		wg    sync.WaitGroup
	)
	waiting := make(chan struct{}, 2)
	for _, owner := range []string{"b", "c"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, err := g.acquire(ctx, "repo", owner, func(holder string, ahead int) { waiting <- struct{}{} })
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			order = append(order, owner)
			mu.Unlock()
			rel()
		}()
		<-waiting // b is queued before c starts
	}
	mu.Lock()
	if len(order) != 0 {
		t.Fatalf("waiters ran while the gate was held: %v", order)
	}
	mu.Unlock()

	relA()
	relA() // idempotent
	wg.Wait()
	if len(order) != 2 || order[0] != "b" || order[1] != "c" {
		t.Fatalf("order = %v, want [b c]", order)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.repos["repo"]; ok {
		t.Fatal("idle repository left in the gate")
	}
}

func TestRepoGateCancelledWaiterLeavesQueue(t *testing.T) {
	var g repoGate
	rel, _ := g.acquire(context.Background(), "repo", "a", nil)

	ctx, cancel := context.WithCancel(context.Background())
	queued := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := g.acquire(ctx, "repo", "b", func(string, int) { close(queued) })
		done <- err
	}()
	<-queued
	cancel()
	if err := <-done; err == nil {
		t.Fatal("expected context error")
	}

	rel()
	// The cancelled waiter must not keep the gate.
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	rel2, err := g.acquire(ctx2, "repo", "c", nil)
	if err != nil {
		t.Fatalf("gate stuck after a cancelled waiter: %v", err)
	}
	rel2()
}

// gatedRestic records how many restic phases run at once per repository.
type gatedRestic struct {
	restic.Noop
	mu       sync.Mutex
	active   int
	maxSeen  int
	inEnsure chan struct{}
	release  chan struct{}
	snaps    map[string]*domain.SnapshotInfo
}

func (g *gatedRestic) EnsureRepo(_ context.Context, _ domain.ResticConfig, _ io.Writer) error {
	g.mu.Lock()
	g.active++
	g.maxSeen = max(g.maxSeen, g.active)
	g.mu.Unlock()
	g.inEnsure <- struct{}{}
	<-g.release
	return nil
}

func (g *gatedRestic) Backup(_ context.Context, _ domain.ResticConfig, tag, _ string, _ string, _ []string, _ io.Writer) (*domain.SnapshotInfo, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active--
	return g.snaps[tag], nil
}

type memSnaps struct {
	mu sync.Mutex
	m  map[string]domain.SnapshotInfo
}

func (s *memSnaps) SaveLatest(_ context.Context, name string, sn domain.SnapshotInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[name] = sn
	return nil
}

func (s *memSnaps) LatestByBackup(context.Context, []string) (map[string]domain.SnapshotInfo, error) {
	return nil, nil
}

func TestBackupsSharingARepositoryQueueAndRecordSnapshots(t *testing.T) {
	gr := &gatedRestic{
		inEnsure: make(chan struct{}, 2),
		release:  make(chan struct{}),
		snaps: map[string]*domain.SnapshotInfo{
			"a": {ID: "snap-a", FilesAdded: 3, FilesChanged: 1, BytesAdded: 100},
			"b": {ID: "snap-b", FilesAdded: 5, FilesChanged: 0, BytesAdded: 200},
		},
	}
	snaps := &memSnaps{m: map[string]domain.SnapshotInfo{}}
	acts := &memActs{m: map[uuid.UUID]domain.Activity{}}
	shared := domain.ResticConfig{Repository: "local:/shared", Password: "pw"}
	cat := catalog{
		"a": {Name: "a", Container: "c", Database: "app", Host: "h", Username: "u", Port: 5432, ResticTag: "a", Restic: shared},
		"b": {Name: "b", Container: "c", Database: "other", Host: "h", Username: "u", Port: 5432, ResticTag: "b", Restic: shared},
	}
	fixed := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	r := NewRunner(Deps{
		Catalog: cat, Global: domain.GlobalConfig{TempPath: t.TempDir(), LogPath: t.TempDir()},
		Activities: acts, Snapshots: snaps, Exec: fakeExec(okDump), Restic: gr, Notifier: &fakeNotifier{},
		Clock: func() time.Time { return fixed },
	})
	_, ch, cancel := r.SubscribeJobs()
	defer cancel()

	actA, err := r.Run(context.Background(), "a", domain.TriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	actB, err := r.Run(context.Background(), "b", domain.TriggerSchedule)
	if err != nil {
		t.Fatal(err)
	}

	// Exactly one run enters the restic phase; the other one queues.
	<-gr.inEnsure
	select {
	case <-gr.inEnsure:
		t.Fatal("two runs inside the restic phase of the same repository")
	case <-time.After(200 * time.Millisecond):
	}
	sawQueued := false
	for drained := false; !drained; {
		select {
		case ev := <-ch:
			sawQueued = sawQueued || ev.Job.Phase == domain.PhaseQueued
		default:
			drained = true
		}
	}
	if !sawQueued {
		t.Error("the waiting run never reported the queued phase")
	}

	close(gr.release) // lets both finish, one after the other
	<-gr.inEnsure
	r.wg.Wait()

	if gr.maxSeen != 1 {
		t.Fatalf("restic phases overlapped: max concurrent = %d", gr.maxSeen)
	}
	for _, a := range []*domain.Activity{actA, actB} {
		if got, _ := acts.Get(context.Background(), a.ID); got.Status != domain.StatusSuccess {
			t.Fatalf("%s: %+v", a.BackupName, got)
		}
	}
	snaps.mu.Lock()
	defer snaps.mu.Unlock()
	if got := snaps.m["a"]; got.ID != "snap-a" || got.FilesAdded != 3 || got.BytesAdded != 100 || !got.Timestamp.Equal(fixed) {
		t.Errorf("snapshot a = %+v", got)
	}
	if got := snaps.m["b"]; got.ID != "snap-b" || got.FilesAdded != 5 || got.BytesAdded != 200 {
		t.Errorf("snapshot b = %+v", got)
	}
}
