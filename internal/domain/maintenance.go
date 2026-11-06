package domain

import (
	"context"
	"io"
)

// DefaultCheckReadPercent is the share of repository data `restic check`
// reads back when [restic].check-read-percent is not set.
const DefaultCheckReadPercent = 80

// MaintenanceKind identifies a scheduled repository maintenance task.
type MaintenanceKind string

const (
	MaintenancePrune  MaintenanceKind = "prune"
	MaintenanceCheck  MaintenanceKind = "check"
	MaintenanceUnlock MaintenanceKind = "unlock"
)

// MaintenanceConfig holds the global [restic] maintenance settings. An empty
// cron disables the matching task.
type MaintenanceConfig struct {
	PruneCron        string
	CheckCron        string
	CheckReadPercent int           // 1-100; DefaultCheckReadPercent when unset
	Repo             *ResticConfig // global [restic] repository; nil when [restic] does not define one
}

// ReadPercent returns the effective read percentage.
func (m MaintenanceConfig) ReadPercent() int {
	if m.CheckReadPercent < 1 || m.CheckReadPercent > 100 {
		return DefaultCheckReadPercent
	}
	return m.CheckReadPercent
}

// ResticMaintainer is implemented by the restic client for repository-wide
// maintenance and lock cleanup. Maintenance methods return an error wrapping
// ErrRepoMissing when the repository does not exist.
type ResticMaintainer interface {
	Prune(ctx context.Context, cfg ResticConfig, logw io.Writer) error
	// CheckData runs `restic check` reading readPercent% of the pack data.
	CheckData(ctx context.Context, cfg ResticConfig, readPercent int, logw io.Writer) error
	Unlock(ctx context.Context, cfg ResticConfig, logw io.Writer) error
}

// MaintenanceRunner starts maintenance tasks in the background.
type MaintenanceRunner interface {
	// Trigger returns ErrMaintenanceRunning if the same task is still running.
	Trigger(kind MaintenanceKind) error
}

// RepoLocker serialises work on one restic repository inside this process
// (FIFO, shared with backup runs). onWait, if not nil, is called once when the
// repository is busy.
type RepoLocker interface {
	AcquireRepo(ctx context.Context, repo, owner string, onWait func(holder string, ahead int)) (release func(), err error)
}
