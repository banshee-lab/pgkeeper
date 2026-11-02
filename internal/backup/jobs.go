package backup

import (
	"slices"
	"sync"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

type JobTracker struct {
	mu   sync.Mutex
	jobs map[string]*domain.Job
}

func NewJobTracker() *JobTracker { return &JobTracker{jobs: make(map[string]*domain.Job)} }

// TryStart atomically registers j; false if that backup already has a job.
func (t *JobTracker) TryStart(j domain.Job) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.jobs[j.BackupName]; ok {
		return false
	}
	t.jobs[j.BackupName] = &j
	return true
}

func (t *JobTracker) SetPhase(name string, p domain.Phase) (domain.Job, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	j, ok := t.jobs[name]
	if !ok {
		return domain.Job{}, false
	}
	j.Phase = p
	return *j, true
}

func (t *JobTracker) Release(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.jobs, name)
}

func (t *JobTracker) List() []domain.Job {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]domain.Job, 0, len(t.jobs))
	for _, j := range t.jobs {
		out = append(out, *j)
	}
	slices.SortFunc(out, func(a, b domain.Job) int { return a.StartedAt.Compare(b.StartedAt) })
	return out
}