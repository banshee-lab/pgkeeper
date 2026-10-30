package domain

import "time"

type ResticConfig struct {
    Repository  string
    Password    Secret
    KeepDaily, KeepWeekly, KeepMonthly, KeepYearly int // 0 = unset
}

type BackupConfig struct {
    Name, Cron, Container, Database, Username, Host string
    Port       int
    ResticTag  string
    Roles      bool
    Restic     ResticConfig // already merged with global
}

type GlobalConfig struct {
    Location *time.Location
    TempPath, LogPath, Listen, DBPath string
}