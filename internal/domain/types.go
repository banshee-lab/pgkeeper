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
    PhaseQueued  Phase = "queued" // waiting for another backup using the same restic repository
    PhaseDump    Phase = "dump"
    PhaseRoles   Phase = "roles"
    PhaseRestic  Phase = "restic"
    PhaseCleanup Phase = "cleanup"

    // Restore phases (PhaseQueued, PhaseRestic and PhaseRoles are shared with backups).
    PhaseSnapshot  Phase = "snapshot"  // resolving the snapshot to restore
    PhaseProvision Phase = "provision" // starting the ephemeral container
    PhaseRestore   Phase = "restore"   // loading the database dump
)

// ActivityKind tells what an activity row records.
type ActivityKind string

const (
    KindBackup      ActivityKind = "backup"
    KindTestRestore ActivityKind = "test_restore" // restore into an ephemeral container
    KindRestore     ActivityKind = "restore"      // restore into a live / custom container
)

// Label is the human-readable name shown in the UI and notifications.
func (k ActivityKind) Label() string {
    switch k {
    case KindTestRestore:
        return "Test Restore"
    case KindRestore:
        return "Container Restore"
    default:
        return "Backup"
    }
}

// IsRestore reports whether the kind is one of the restore kinds.
func (k ActivityKind) IsRestore() bool { return k == KindTestRestore || k == KindRestore }

// Secret redacts itself in logs, fmt and JSON.
type Secret string
func (Secret) String() string               { return "***" }
func (Secret) LogValue() slog.Value         { return slog.StringValue("***") }
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"***"`), nil }
func (s Secret) Reveal() string             { return string(s) }