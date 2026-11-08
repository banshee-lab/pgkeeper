package backup

import (
	"slices"
	"sync"
	"uuid"

	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/events"
)

type trackedJob struct {
	job       domain.Job
	announced bool // visible to List/Subscribe only after its activity row is persisted
}

// JobTracker owns job state, event sequencing and publication. Every state
// change and its event happen under one lock, and Subscribe captures its
// snapshot under that same lock, so a subscriber sees each change exactly
// once: either inside the snapshot or as an event with Seq > snapshot Seq.
type JobTracker struct {
	mu   sync.Mutex
	jobs map[string]*trackedJob
	seq  uint64
	hub  *events.Hub[events.JobEvent]
}

func NewJobTracker(hub *events.Hub[events.JobEvent]) *JobTracker {
	if hub == nil {
		hub = events.NewHub[events.JobEvent]()
	}
	return &JobTracker{jobs: make(map[string]*trackedJob), hub: hub}
}

// publishLocked assigns the next sequence number and publishes (t.mu held).
// Hub.Publish never blocks and never calls back into the tracker.
func (t *JobTracker) publishLocked(typ events.JobEventType, j domain.Job, a domain.Activity) {
	t.seq++
	t.hub.Publish(events.JobEvent{Seq: t.seq, Type: typ, Job: j, Activity: a})
}

// TryStart reserves the backup (not yet visible); false if it already has a job.
func (t *JobTracker) TryStart(j domain.Job) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.jobs[j.BackupName]; ok {
		return false
	}
	t.jobs[j.BackupName] = &trackedJob{job: j}
	return true
}

// Announce makes the reserved job visible and publishes job.started. Call it
// only after the activity row has been persisted.
func (t *JobTracker) Announce(name string, act domain.Activity) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tj, ok := t.jobs[name]
	if !ok || tj.announced || tj.job.ActivityID != act.ID {
		return
	}
	tj.announced = true
	t.publishLocked(events.JobStarted, tj.job, act)
}

func (t *JobTracker) SetPhase(name string, act domain.Activity, p domain.Phase) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tj, ok := t.jobs[name]
	if !ok || !tj.announced || tj.job.ActivityID != act.ID {
		return
	}
	tj.job.Phase = p
	t.publishLocked(events.JobPhase, tj.job, act)
}

// Finish removes the job and publishes job.finished (persisted) or
// job-abandoned (the final row could not be stored). Call it only after the
// persistence attempt.
func (t *JobTracker) Finish(name string, act domain.Activity, persisted bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tj, ok := t.jobs[name]
	if !ok || tj.job.ActivityID != act.ID {
		return
	}
	delete(t.jobs, name)
	if !tj.announced {
		return
	}
	tj.job.Phase = domain.PhaseCleanup
	typ := events.JobFinished
	if !persisted {
		typ = events.JobAbandoned
	}
	t.publishLocked(typ, tj.job, act)
}

// Release drops the job without an event, only if it still belongs to that
// activity (idempotent; never removes a newer run of the same backup).
func (t *JobTracker) Release(name string, id uuid.UUID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if tj, ok := t.jobs[name]; ok && tj.job.ActivityID == id {
		delete(t.jobs, name)
	}
}

func (t *JobTracker) listLocked() []domain.Job {
	out := make([]domain.Job, 0, len(t.jobs))
	for _, tj := range t.jobs {
		if tj.announced {
			out = append(out, tj.job)
		}
	}
	slices.SortFunc(out, func(a, b domain.Job) int { return a.StartedAt.Compare(b.StartedAt) })
	return out
}

func (t *JobTracker) List() []domain.Job {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.listLocked()
}

// Subscribe returns the running jobs at sequence Seq plus a channel that
// receives exactly the events with Seq greater than that.
func (t *JobTracker) Subscribe() (events.JobSnapshot, <-chan events.JobEvent, func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ch, cancel := t.hub.Subscribe(nil)
	return events.JobSnapshot{Jobs: t.listLocked(), Seq: t.seq}, ch, cancel
}
// Current returns the job of the project, announced or not.
func (t *JobTracker) Current(name string) (domain.Job, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if tj, ok := t.jobs[name]; ok {
		return tj.job, true
	}
	return domain.Job{}, false
}
