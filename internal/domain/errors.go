package domain

import "errors"

var (
	ErrNotFound      = errors.New("not found")
	ErrBackupRunning = errors.New("backup already running")
	ErrBackupUnknown = errors.New("unknown backup")
)