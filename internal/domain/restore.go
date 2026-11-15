package domain

import (
	"context"
	"io"
	"time"
)

// RestoreRequest describes a restore into a live / custom container.
type RestoreRequest struct {
	// SnapshotID is a restic snapshot of the project (full or short ID).
	SnapshotID string
	// Container and Database select the target. Empty values default to the
	// project's own container and database.
	Container string
	Database  string
	// IncludeRoles restores the roles dump before the database. Existing roles
	// are overwritten.
	IncludeRoles bool
	// DatabaseURL targets a remote server (postgresql://...) instead of a
	// container; it is mutually exclusive with Container. When both Container
	// and DatabaseURL are empty, a project backed up from a database-url is
	// restored into that URL. Needs the tools-image.
	DatabaseURL Secret
	// Conn is DatabaseURL as resolved by the runner; callers leave it unset.
	Conn *PGConn
}

// RestoreSnapshot is a restorable snapshot of a project.
type RestoreSnapshot struct {
	ID       string
	ShortID  string
	Time     time.Time
	HasRoles bool // the snapshot contains a roles dump
}

// RestoreRunner starts restores in the background. Like BackupRunner.Run, the
// returned activity is already persisted and the work continues asynchronously.
// A project runs one backup or restore at a time: ErrBackupRunning or
// ErrRestoreRunning is returned while another one is in progress.
type RestoreRunner interface {
	// TestRestore restores a snapshot into a throw-away container. An empty
	// snapshotID selects the latest snapshot; scheduled test restores (by ==
	// TriggerSchedule) always use the latest snapshot and ignore snapshotID.
	TestRestore(ctx context.Context, name string, by Trigger, snapshotID string) (*Activity, error)
	// RestoreToContainer restores the requested snapshot into a live container.
	RestoreToContainer(ctx context.Context, name string, by Trigger, req RestoreRequest) (*Activity, error)
	// RestoreSnapshots lists the project's restorable snapshots, newest first.
	RestoreSnapshots(ctx context.Context, name string) ([]RestoreSnapshot, error)
}

// SQLTarget identifies a PostgreSQL server reached through `docker exec`, or,
// when Conn is set, over the network from a tools container built from Image.
type SQLTarget struct {
	Container string
	Host      string
	Port      int
	Username  string
	Database  string // database psql connects to

	Conn  *PGConn
	Image string
}

// RestoreExecutor runs the container-side steps of a restore.
type RestoreExecutor interface {
	// StartEphemeral starts a detached, isolated PostgreSQL container.
	StartEphemeral(ctx context.Context, name, image string, logw io.Writer) error
	// RemoveContainer force-removes the container and its anonymous volumes.
	RemoveContainer(ctx context.Context, name string, logw io.Writer) error
	// WaitReady blocks until the server accepts connections or timeout expires.
	WaitReady(ctx context.Context, t SQLTarget, timeout time.Duration, logw io.Writer) error
	// Psql runs a SQL script (read from in) and stops at the first error.
	Psql(ctx context.Context, t SQLTarget, in io.Reader, logw io.Writer) error
}
