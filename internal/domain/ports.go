package domain

import (
	"context"
	"io"
	"time"

	"uuid"
)

// ActivityFilter controls ListFiltered.
type ActivityFilter struct {
	BackupName string   // empty = all backups
	Status     Status   // empty = all statuses
	Since      time.Time // zero = no lower bound; started_at >= Since
	Cursor     string    // opaque (started_at DESC, id); empty = from newest
	Limit      int       // 1–100, already clamped by the handler
}

type ActivityRepository interface {
	Create(ctx context.Context, a *Activity) error
	Finish(ctx context.Context, id uuid.UUID, status Status, finishedAt time.Time, errMsg string) error
	Get(ctx context.Context, id uuid.UUID) (*Activity, error)
	ListByBackup(ctx context.Context, name string, limit int) ([]Activity, error)
	ListRecent(ctx context.Context, limit int) ([]Activity, error)
	LastByBackup(ctx context.Context, names []string) (map[string]Activity, error)
	LastSuccessByBackup(ctx context.Context, names []string) (map[string]time.Time, error)
	// ListFiltered returns at most f.Limit activities ordered by (started_at DESC, id DESC).
	// nextCursor is empty when there are no further pages.
	ListFiltered(ctx context.Context, f ActivityFilter) (items []Activity, nextCursor string, err error)
	MarkRunningAsInterrupted(ctx context.Context, now time.Time) (int, error)
}

type SessionRepository interface {
	CreateSession(ctx context.Context, s *Session) error
	GetSession(ctx context.Context, idHash string, now time.Time) (*Session, error)
	DeleteSession(ctx context.Context, idHash string) error
	DeleteExpired(ctx context.Context, now time.Time) error
	SaveAuthState(ctx context.Context, s *AuthState) error
	ConsumeAuthState(ctx context.Context, state string, now time.Time) (*AuthState, error)
}

type DumpKind string
const (DumpDatabase DumpKind = "database"; DumpRoles DumpKind = "roles")

type DumpSpec struct {
	Kind                       DumpKind
	Container, Database        string
	Host, Username             string
	Port                       int
}

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
	LogTail  string
}
type Notifier interface {
	NotifyFailure(ctx context.Context, e NotifyEvent) error
}

type BackupRunner interface {
	Run(ctx context.Context, name string, by Trigger) (*Activity, error)
	Running() []Job
}

type Scheduler interface {
	Start() error
	Stop(ctx context.Context) error
	NextRun(name string) (time.Time, bool)
}

type BackupCatalog interface {
	List() []BackupConfig
	Get(name string) (BackupConfig, bool)
}