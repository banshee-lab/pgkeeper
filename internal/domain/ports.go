package domain

import (
	"context"
	"io"
	"time"

	"uuid"
)

type ActivityRepository interface {
    Create(ctx context.Context, a *Activity) error
    Finish(ctx context.Context, id uuid.UUID, status Status, finishedAt time.Time, errMsg string) error
    Get(ctx context.Context, id uuid.UUID) (*Activity, error)            // ErrNotFound
    ListByBackup(ctx context.Context, name string, limit int) ([]Activity, error)
    ListRecent(ctx context.Context, limit int) ([]Activity, error)
    LastByBackup(ctx context.Context, names []string) (map[string]Activity, error)
    MarkRunningAsInterrupted(ctx context.Context, now time.Time) (int, error)
}

type SessionRepository interface {
    CreateSession(ctx context.Context, s *Session) error
    GetSession(ctx context.Context, idHash string, now time.Time) (*Session, error) // ErrNotFound if missing/expired
    DeleteSession(ctx context.Context, idHash string) error
    DeleteExpired(ctx context.Context, now time.Time) error
    SaveAuthState(ctx context.Context, s *AuthState) error
    ConsumeAuthState(ctx context.Context, state string, now time.Time) (*AuthState, error) // single use
}

type DumpKind string
const (DumpDatabase DumpKind = "database"; DumpRoles DumpKind = "roles")

type DumpSpec struct {
    Kind                       DumpKind
    Container, Database        string
    Host, Username             string
    Port                       int
}

// Executor runs a dump inside a container; dump bytes -> out, diagnostics -> logw.
type Executor interface {
    Dump(ctx context.Context, spec DumpSpec, out io.Writer, logw io.Writer) error
}

type ResticClient interface {
    EnsureRepo(ctx context.Context, cfg ResticConfig, logw io.Writer) error
    Backup(ctx context.Context, cfg ResticConfig, tag, host string, dir string, files []string, logw io.Writer) error
    Forget(ctx context.Context, cfg ResticConfig, tag, host string, logw io.Writer) error
}

type NotifyEvent struct {
    Backup   BackupConfig
    Activity Activity
    LogTail  string // last lines, already truncated
}
type Notifier interface {
    NotifyFailure(ctx context.Context, e NotifyEvent) error
}

type BackupRunner interface {
    // Run starts an execution asynchronously. Returns the new activity
    // or ErrBackupRunning / ErrBackupUnknown.
    Run(ctx context.Context, name string, by Trigger) (*Activity, error)
    Running() []Job
}

type Scheduler interface {
    Start() error
    Stop(ctx context.Context) error
    NextRun(name string) (time.Time, bool)
}

type BackupCatalog interface { // read-only view of loaded config
    List() []BackupConfig
    Get(name string) (BackupConfig, bool)
}