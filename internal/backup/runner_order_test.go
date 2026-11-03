package backup

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/events"
	"github.com/banshee-lab/pgkeeper/internal/restic"
)

// flakyActs fails the first `failures` Finish calls.
type flakyActs struct {
	*memActs
	fmu      sync.Mutex
	failures int
	attempts int
}

func (f *flakyActs) Finish(ctx context.Context, id uuid.UUID, st domain.Status, at time.Time, msg string) error {
	f.fmu.Lock()
	f.attempts++
	fail := f.failures > 0
	if fail {
		f.failures--
	}
	f.fmu.Unlock()
	if fail {
		return errors.New("database is locked")
	}
	return f.memActs.Finish(ctx, id, st, at, msg)
}

func newOrderRunner(t *testing.T, acts domain.ActivityRepository, ex fakeExec) *Runner {
	t.Helper()
	return NewRunner(Deps{
		Catalog: catalog{"a": {Name: "a", Container: "c", Database: "app", Host: "localhost",
			Username: "postgres", Port: 5432, ResticTag: "a"}},
		Global:     domain.GlobalConfig{TempPath: t.TempDir(), LogPath: t.TempDir()},
		Activities: acts, Exec: ex, Restic: restic.Noop{}, Notifier: &fakeNotifier{},
		FinishAttempts: 3, FinishBackoff: time.Millisecond,
	})
}

func collectUntil(t *testing.T, ch <-chan events.JobEvent, stop ...events.JobEventType) []events.JobEvent {
	t.Helper()
	var got []events.JobEvent
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			got = append(got, ev)
			for _, s := range stop {
				if ev.Type == s {
					return got
				}
			}
		case <-timeout:
			t.Fatalf("timed out; events so far: %+v", got)
		}
	}
}

func TestActivityIsPersistedBeforeEvents(t *testing.T) {
	acts := &memActs{m: map[uuid.UUID]domain.Activity{}}
	execStarted, releaseExec := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseExec) }) }
	ex := fakeExec(func(ctx context.Context, spec domain.DumpSpec, out, logw io.Writer) error {
		close(execStarted)
		<-releaseExec
		return okDump(ctx, spec, out, logw)
	})
	r := newOrderRunner(t, acts, ex)
	defer func() {
		release()
		r.wg.Wait()
	}()
	_, ch, cancel := r.SubscribeJobs()
	defer cancel()

	act, err := r.Run(context.Background(), "a", domain.TriggerManual)
	if err != nil {
		t.Fatal(err)
	}

	var started events.JobEvent
	select {
	case started = <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job.started")
	}
	if started.Type != events.JobStarted {
		t.Fatalf("first event = %s, want job.started", started.Type)
	}
	select {
	case <-execStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("dump executor did not start")
	}
	got, err := acts.Get(context.Background(), act.ID)
	if err != nil {
		t.Fatalf("job.started published before the activity row existed: %v", err)
	}
	if got.Status != domain.StatusRunning {
		t.Errorf("job.started with persisted status %s", got.Status)
	}

	release()
	finished := collectUntil(t, ch, events.JobFinished)
	if last := finished[len(finished)-1]; last.Type != events.JobFinished {
		t.Fatalf("last event = %s, want job.finished", last.Type)
	}
	got, err = acts.Get(context.Background(), act.ID)
	if err != nil {
		t.Fatalf("job.finished published before the activity row existed: %v", err)
	}
	if got.Status != domain.StatusSuccess || got.FinishedAt == nil {
		t.Errorf("job.finished before final row persisted: %+v", got)
	}
}

func TestFinalPersistFailureEmitsNoFinishedEvent(t *testing.T) {
	acts := &flakyActs{memActs: &memActs{m: map[uuid.UUID]domain.Activity{}}, failures: 100}
	r := newOrderRunner(t, acts, okDump)
	_, ch, cancel := r.SubscribeJobs()
	defer cancel()

	if _, err := r.Run(context.Background(), "a", domain.TriggerManual); err != nil {
		t.Fatal(err)
	}
	got := collectUntil(t, ch, events.JobAbandoned, events.JobFinished)
	if last := got[len(got)-1]; last.Type != events.JobAbandoned {
		t.Fatalf("want job abandoned, got %s", last.Type)
	}
	r.wg.Wait()
	if acts.attempts != 3 {
		t.Errorf("attempts = %d, want 3 (bounded retry)", acts.attempts)
	}
	if len(r.Running()) != 0 {
		t.Error("job must be released after abandonment")
	}
}

func TestFinalPersistRetrySucceeds(t *testing.T) {
	acts := &flakyActs{memActs: &memActs{m: map[uuid.UUID]domain.Activity{}}, failures: 2}
	r := newOrderRunner(t, acts, okDump)
	_, ch, cancel := r.SubscribeJobs()
	defer cancel()

	act, _ := r.Run(context.Background(), "a", domain.TriggerManual)
	got := collectUntil(t, ch, events.JobAbandoned, events.JobFinished)
	if last := got[len(got)-1]; last.Type != events.JobFinished {
		t.Fatalf("want job finished after retry, got %s", last.Type)
	}
	if a, _ := acts.Get(context.Background(), act.ID); a.Status != domain.StatusSuccess {
		t.Fatalf("persisted = %+v", a)
	}
	r.wg.Wait()
}

func TestSnapshotBoundaryAroundRunningJob(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	acts := &memActs{m: map[uuid.UUID]domain.Activity{}}
	r := newOrderRunner(t, acts, func(ctx context.Context, s domain.DumpSpec, out, w io.Writer) error {
		close(entered)
		<-release
		return okDump(ctx, s, out, w)
	})
	act, err := r.Run(context.Background(), "a", domain.TriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	<-entered

	snap, ch, cancel := r.SubscribeJobs()
	defer cancel()
	if len(snap.Jobs) != 1 || snap.Jobs[0].ActivityID != act.ID {
		t.Fatalf("snapshot = %+v", snap.Jobs)
	}
	close(release)

	for _, ev := range collectUntil(t, ch, events.JobFinished) {
		if ev.Seq <= snap.Seq {
			t.Errorf("event %s (seq %d) duplicates state already in the snapshot (%d)", ev.Type, ev.Seq, snap.Seq)
		}
		if ev.Type == events.JobStarted {
			t.Error("job.started must not be replayed after the snapshot")
		}
	}
	r.wg.Wait()
}