package events

import "github.com/banshee-lab/pgkeeper/internal/domain"

type JobEventType string

const (
	JobStarted  JobEventType = "started"
	JobPhase    JobEventType = "phase"
	JobFinished JobEventType = "finished"
)

type JobEvent struct {
	Type     JobEventType
	Job      domain.Job
	Activity domain.Activity
}