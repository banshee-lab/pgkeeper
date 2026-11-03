package events

import "github.com/banshee-lab/pgkeeper/internal/domain"

type JobEventType string

const (
	JobStarted  JobEventType = "started"
	JobPhase    JobEventType = "phase"
	JobFinished JobEventType = "finished"
	// JobAbandoned: the execution ended but its final activity row could not
	// be persisted. It carries no final status; streams close on it.
	JobAbandoned JobEventType = "abandoned"
)

type JobEvent struct {
	// Seq is assigned by the job tracker under the same lock that mutates job
	// state; strictly increasing. 0 means "unsequenced" (tests, fakes).
	Seq      uint64
	Type     JobEventType
	Job      domain.Job
	Activity domain.Activity
}

// JobSnapshot is the running-job set at sequence Seq.
type JobSnapshot struct {
	Jobs []domain.Job
	Seq  uint64
}