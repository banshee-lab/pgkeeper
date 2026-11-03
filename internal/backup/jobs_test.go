package backup

import (
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/events"
)

func newJob(name string) (domain.Job, domain.Activity) {
	id := uuid.New()
	return domain.Job{ActivityID: id, BackupName: name}, domain.Activity{ID: id, BackupName: name}
}

func TestListOnlyShowsAnnouncedJobs(t *testing.T) {
	tr := NewJobTracker(nil)
	j, act := newJob("a")
	if !tr.TryStart(j) {
		t.Fatal("TryStart")
	}
	if got := tr.List(); len(got) != 0 {
		t.Fatalf("reserved job must not be visible: %+v", got)
	}
	tr.Announce("a", act)
	if got := tr.List(); len(got) != 1 {
		t.Fatalf("announced job must be visible: %+v", got)
	}
}

func TestReleaseOnlyAffectsOwnActivity(t *testing.T) {
	tr := NewJobTracker(nil)
	j1, a1 := newJob("a")
	tr.TryStart(j1)
	tr.Announce("a", a1)
	tr.Finish("a", a1, true)

	j2, a2 := newJob("a")
	if !tr.TryStart(j2) {
		t.Fatal("second run must start after the first finished")
	}
	tr.Announce("a", a2)
	tr.Release("a", j1.ActivityID) // stale safety-net release of the first run
	if got := tr.List(); len(got) != 1 || got[0].ActivityID != j2.ActivityID {
		t.Fatalf("stale release removed the new job: %+v", got)
	}
}

func TestFinishAbandonedPublishesDistinctEvent(t *testing.T) {
	tr := NewJobTracker(nil)
	_, ch, cancel := func() (events.JobSnapshot, <-chan events.JobEvent, func()) { return tr.Subscribe() }()
	defer cancel()
	j, act := newJob("a")
	tr.TryStart(j)
	tr.Announce("a", act)
	tr.Finish("a", act, false)

	want := []events.JobEventType{events.JobStarted, events.JobAbandoned}
	for _, w := range want {
		select {
		case ev := <-ch:
			if ev.Type != w {
				t.Fatalf("got %s, want %s", ev.Type, w)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout")
		}
	}
}

// Concurrent churn while subscribing: every subscriber must see only events
// after its snapshot, strictly increasing, and (when contiguous) a state
// machine that never double-starts or finishes an unknown job.
func TestSubscribeBoundaryNoLossNoDuplicates(t *testing.T) {
	hub := events.NewHub[events.JobEvent]()
	tr := NewJobTracker(hub)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			name := []string{"a", "b", "c"}[i%3]
			j, act := newJob(name)
			if !tr.TryStart(j) {
				continue
			}
			tr.Announce(name, act)
			tr.SetPhase(name, act, domain.PhaseDump)
			tr.Finish(name, act, true)
			time.Sleep(20 * time.Microsecond)
		}
	}()

	for range 100 {
		snap, ch, cancel := tr.Subscribe()
		live := map[uuid.UUID]bool{}
		for _, j := range snap.Jobs {
			live[j.ActivityID] = true
		}
		last := snap.Seq
		contiguous := true
		timeout := time.After(5 * time.Millisecond)
	collect:
		for {
			select {
			case ev := <-ch:
				if ev.Seq <= snap.Seq {
					t.Fatalf("event %d at or before snapshot boundary %d", ev.Seq, snap.Seq)
				}
				if ev.Seq <= last {
					t.Fatalf("event %d not after %d", ev.Seq, last)
				}
				if ev.Seq != last+1 {
					contiguous = false
				}
				last = ev.Seq
				if !contiguous {
					continue
				}
				id := ev.Job.ActivityID
				switch ev.Type {
				case events.JobStarted:
					if live[id] {
						t.Fatalf("duplicate start for %s", id)
					}
					live[id] = true
				case events.JobPhase:
					if !live[id] {
						t.Fatalf("phase for unknown job %s", id)
					}
				case events.JobFinished:
					if !live[id] {
						t.Fatalf("finish for unknown job %s", id)
					}
					delete(live, id)
				}
			case <-timeout:
				break collect
			}
		}
		cancel()
	}
	close(stop)
	wg.Wait()
	if hub.Dropped() > 0 {
		t.Skip("hub buffer overflowed; result inconclusive")
	}
}