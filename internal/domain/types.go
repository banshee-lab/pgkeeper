package domain

import "log/slog"

type Status string
const (
    StatusRunning Status = "running"
    StatusSuccess Status = "success"
    StatusError   Status = "error"
)

type Trigger string
const (
    TriggerSchedule Trigger = "schedule"
    TriggerManual   Trigger = "manual"
)

type Phase string
const (
    PhaseDump    Phase = "dump"
    PhaseRoles   Phase = "roles"
    PhaseRestic  Phase = "restic"
    PhaseCleanup Phase = "cleanup"
)

// Secret redacts itself in logs, fmt and JSON.
type Secret string
func (Secret) String() string               { return "***" }
func (Secret) LogValue() slog.Value         { return slog.StringValue("***") }
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"***"`), nil }
func (s Secret) Reveal() string             { return string(s) }