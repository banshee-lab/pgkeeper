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
}

type GlobalConfig struct {
	Location                          *time.Location
	TempPath, LogPath, Listen, DBPath string
}
