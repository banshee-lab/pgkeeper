package domain

import "errors"

var (
	ErrNotFound           = errors.New("not found")
	ErrBackupRunning      = errors.New("backup already running")
	ErrBackupUnknown      = errors.New("unknown backup")
	ErrRepoMissing        = errors.New("restic repository does not exist")
	ErrMaintenanceRunning = errors.New("maintenance task already running")
	ErrRestoreRunning     = errors.New("restore already running")
	// ErrRestoreInvalid wraps problems with a restore request or its configuration.
	ErrRestoreInvalid = errors.New("invalid restore request")
	// ErrRestoreUnavailable means the restore machinery is not wired in.
	ErrRestoreUnavailable = errors.New("restore unavailable")
)
