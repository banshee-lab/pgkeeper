package domain

import "errors"

var (
	ErrNotFound           = errors.New("not found")
	ErrBackupRunning      = errors.New("backup already running")
	ErrBackupUnknown      = errors.New("unknown backup")
	ErrRepoMissing        = errors.New("restic repository does not exist")
	ErrMaintenanceRunning = errors.New("maintenance task already running")
)
