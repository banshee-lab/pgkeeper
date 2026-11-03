package api

import (
	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/events"
)

// JobFeed returns a snapshot of the running jobs together with the events that
// follow it, with no gap and no overlap (events carry a sequence number and
// only those after JobSnapshot.Seq are delivered on the channel).
type JobFeed interface {
	SubscribeJobs() (events.JobSnapshot, <-chan events.JobEvent, func())
}

// resolveJobFeed picks the atomic feed: Deps.Jobs, else the runner when it
// implements JobFeed (backup.Runner does), else a best-effort fallback.
func resolveJobFeed(d Deps) JobFeed {
	if d.Jobs != nil {
		return d.Jobs
	}
	if f, ok := d.Runner.(JobFeed); ok {
		return f
	}
	return legacyFeed{runner: d.Runner, hub: d.JobHub}
}

// legacyFeed is NOT atomic. It only exists for runners that cannot provide a
// JobFeed (test fakes); production always uses backup.Runner.
type legacyFeed struct {
	runner domain.BackupRunner
	hub    *events.Hub[events.JobEvent]
}

func (f legacyFeed) SubscribeJobs() (events.JobSnapshot, <-chan events.JobEvent, func()) {
	ch, cancel := f.hub.Subscribe(nil)
	var jobs []domain.Job
	if f.runner != nil {
		jobs = f.runner.Running()
	}
	return events.JobSnapshot{Jobs: jobs}, ch, cancel
}