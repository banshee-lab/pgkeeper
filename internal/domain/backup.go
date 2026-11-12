package domain

import "time"

type ResticConfig struct {
	Repository                                     string
	Password                                       Secret
	KeepDaily, KeepWeekly, KeepMonthly, KeepYearly int // 0 = unset
	// SkipPrune makes the per-backup forget leave the data alone (no --prune);
	// space is then reclaimed only by the scheduled prune.
	SkipPrune bool
}

type BackupConfig struct {
	Name, Cron, Container, Database, Username, Host string
	Port                                            int
	ResticTag                                       string
	Roles                                           bool
	Restic                                          ResticConfig // already merged with global

	// RestoreCron schedules automatic test restores; empty disables them.
	RestoreCron string
	// RestoreImage is the image of the ephemeral test-restore container: the
	// project override, else the global [restore].restore-image.
	RestoreImage string

	// Conn is the parsed database-url of a project that backs up a remote
	// PostgreSQL server instead of a container; nil for container projects.
	// For such a project Container is empty and Host, Port, Username and
	// Database come from the URL.
	Conn *PGConn
	// ToolsImage is the global tools-image: the image of the one-shot
	// container that runs pg_dump / psql against a remote server. Empty
	// when not configured (remote backups and restores are then unavailable).
	ToolsImage string
}

type GlobalConfig struct {
	Location                          *time.Location
	TempPath, LogPath, Listen, DBPath string

	// Metadata enables backup metadata capture (<db>.meta.json) and restore
	// validation against it. Default true.
	Metadata bool
}
