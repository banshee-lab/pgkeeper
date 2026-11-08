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
	RestoreImage *string `toml:"restore_image"`
}

type rawGlobal struct {
	Timezone string   `toml:"timezone"`
	Include  []string `toml:"include"`
	TempPath string   `toml:"temp_path"`
	LogPath  string   `toml:"log_path"`
	Listen   string   `toml:"listen"`
	DBPath   string   `toml:"db_path"`
}

type rawTelegram struct {
	BotToken string `toml:"bot_token"`
	ChatID   string `toml:"chat_id"`
}

type rawAuth struct {
	Issuer            string   `toml:"issuer"`
	ClientID          string   `toml:"client_id"`
	Secret            string   `toml:"secret"`
	RedirectURL       string   `toml:"redirect_url"`
	Scopes            []string `toml:"scopes"`
	SessionTTL        string   `toml:"session_ttl"`
	CookieSecure      *bool    `toml:"cookie_secure"`
	PostLoginRedirect string   `toml:"post_login_redirect"`
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
	ResticTag *string `toml:"restic_tag"`
	Roles     *bool   `toml:"roles"`

	// Test restores: cron schedule and optional image override.
	RestoreCron  *string `toml:"restore_cron"`
	RestoreImage *string `toml:"restore_image"`

	// Flat restic overrides (same keys as [restic]).
	Repository  *string `toml:"repository"`
	Password    *string `toml:"password"`
	KeepDaily   *int    `toml:"keep-daily"`
	KeepWeekly  *int    `toml:"keep-weekly"`
	KeepMonthly *int    `toml:"keep-monthly"`
	KeepYearly  *int    `toml:"keep-yearly"`
	SkipPrune   *bool   `toml:"skip-prune"`
}

func (b rawBackup) restic() rawRestic {
	return rawRestic{
		Repository: b.Repository, Password: b.Password,
		KeepDaily: b.KeepDaily, KeepWeekly: b.KeepWeekly,
		KeepMonthly: b.KeepMonthly, KeepYearly: b.KeepYearly, SkipPrune: b.SkipPrune,
	}
}