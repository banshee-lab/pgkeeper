package domain

import (
	"time"

	"uuid"
)

type Activity struct {
    ID          uuid.UUID
    BackupName  string
    Database    string
    TriggeredBy Trigger
    Status      Status
    StartedAt   time.Time
    FinishedAt  *time.Time
    Error       string
    LogPath     string
}

func (a Activity) Duration() *time.Duration {
	if a.FinishedAt == nil {
		return nil
	}
	d := a.FinishedAt.Sub(a.StartedAt)
	return &d
}

type Job struct { // in-memory only
    ActivityID  uuid.UUID
    BackupName  string
    Database    string
    TriggeredBy Trigger
    Phase       Phase
    StartedAt   time.Time
}