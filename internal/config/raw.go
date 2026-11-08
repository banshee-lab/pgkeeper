package config

// raw mirrors the TOML files. Section pointers let Load tell "not defined"
// from "defined but empty" when merging includes.
type raw struct {
	Global   *rawGlobal           `toml:"global"`
	Telegram *rawTelegram         `toml:"telegram"`
	Auth     *rawAuth             `toml:"auth"`
	Restic   *rawRestic           `toml:"restic"`
	Restore  *rawRestore          `toml:"restore"`
	Backup   map[string]rawBackup `toml:"backup"`
}

// rawRestore is the global [restore] section.
type rawRestore struct {
	RestoreImage *string `toml:"restore-image"`
}

type rawGlobal struct {
	Timezone string   `toml:"timezone"`
	Include  []string `toml:"include"`
	TempPath string   `toml:"temp-path"`
	LogPath  string   `toml:"log-path"`
	Listen   string   `toml:"listen"`
	DBPath   string   `toml:"db-path"`
}

type rawTelegram struct {
	BotToken string `toml:"bot-token"`
	ChatID   string `toml:"chat-id"`
}

type rawAuth struct {
	Issuer            string   `toml:"issuer"`
	ClientID          string   `toml:"client-id"`
	Secret            string   `toml:"secret"`
	RedirectURL       string   `toml:"redirect-url"`
	Scopes            []string `toml:"scopes"`
	SessionTTL        string   `toml:"session-ttl"`
	CookieSecure      *bool    `toml:"cookie-secure"`
	PostLoginRedirect string   `toml:"post-login-redirect"`
}

// Pointers: unset != zero, so per-backup overrides are detectable.
type rawRestic struct {
	Repository  *string `toml:"repository"`
	Password    *string `toml:"password"`
	KeepDaily   *int    `toml:"keep-daily"`
	KeepWeekly  *int    `toml:"keep-weekly"`
	KeepMonthly *int    `toml:"keep-monthly"`
	KeepYearly  *int    `toml:"keep-yearly"`
	SkipPrune   *bool   `toml:"skip-prune"`

	// Global-only maintenance settings (rejected as unknown keys in [backup.*]).
	PruneCron        *string `toml:"prune-cron"`
	CheckCron        *string `toml:"check-cron"`
	CheckReadPercent *int    `toml:"check-read-percent"`
}

type rawBackup struct {
	Cron      string  `toml:"cron"`
	Container string  `toml:"container"`
	Database  string  `toml:"database"`
	Username  *string `toml:"username"`
	Port      *int    `toml:"port"`
	Host      *string `toml:"host"`
	ResticTag *string `toml:"restic-tag"`
	Roles     *bool   `toml:"roles"`

	// Test restores: cron schedule and optional image override.
	RestoreCron  *string `toml:"restore-cron"`
	RestoreImage *string `toml:"restore-image"`

	// Flat restic overrides (same keys as [restic]).
	Repository  *string `toml:"repository"`
	Password    *string `toml:"password"`
	KeepDaily   *int    `toml:"keep-daily"`
	KeepWeekly  *int    `toml:"keep-weekly"`
	KeepMonthly *int    `toml:"keep-monthly"`
	KeepYearly  *int    `toml:"keep-yearly"`
	SkipPrune   *bool   `toml:"skip-prune"`

	// Prefixed restic overrides avoid colliding with other backup settings.
	ResticRepository  *string `toml:"restic-repository"`
	ResticPassword    *string `toml:"restic-password"`
	ResticKeepDaily   *int    `toml:"restic-keep-daily"`
	ResticKeepWeekly  *int    `toml:"restic-keep-weekly"`
	ResticKeepMonthly *int    `toml:"restic-keep-monthly"`
	ResticKeepYearly  *int    `toml:"restic-keep-yearly"`
	ResticSkipPrune   *bool   `toml:"restic-skip-prune"`
}

func (b rawBackup) restic() rawRestic {
	return rawRestic{
		Repository:  firstPtr(b.ResticRepository, b.Repository),
		Password:    firstPtr(b.ResticPassword, b.Password),
		KeepDaily:   firstPtr(b.ResticKeepDaily, b.KeepDaily),
		KeepWeekly:  firstPtr(b.ResticKeepWeekly, b.KeepWeekly),
		KeepMonthly: firstPtr(b.ResticKeepMonthly, b.KeepMonthly),
		KeepYearly:  firstPtr(b.ResticKeepYearly, b.KeepYearly),
		SkipPrune:   firstPtr(b.ResticSkipPrune, b.SkipPrune),
	}
}

func firstPtr[T any](vals ...*T) *T {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}
