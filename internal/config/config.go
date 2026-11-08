package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

type TelegramConfig struct {
	BotToken domain.Secret
	ChatID   string
}

type AuthConfig struct {
	Issuer            string
	ClientID          string
	Secret            domain.Secret
	RedirectURL       string
	Scopes            []string
	SessionTTL        time.Duration
	CookieSecure      bool
	PostLoginRedirect string
}

type Config struct {
	Global   domain.GlobalConfig
	Telegram *TelegramConfig       // nil = disabled
	Auth     *AuthConfig           // nil = disabled
	Backups  []domain.BackupConfig // sorted by name
	// Maintenance holds the global [restic] prune/check schedules.
	Maintenance domain.MaintenanceConfig
	Warnings []string              // non-fatal findings; the caller logs them
}

var _ domain.BackupCatalog = (*Config)(nil)

func (c *Config) List() []domain.BackupConfig { return slices.Clone(c.Backups) }

func (c *Config) Get(name string) (domain.BackupConfig, bool) {
	for _, b := range c.Backups {
		if b.Name == name {
			return b, true
		}
	}
	return domain.BackupConfig{}, false
}

var repoCredsRe = regexp.MustCompile(`(://[^/:@\s]*):[^@/\s]*@`)

// RedactRepository strips the password from credentials embedded in a
// repository URL (e.g. rest:https://user:pass@host/). Reused by the API in phase 5.
func RedactRepository(repo string) string {
	return repoCredsRe.ReplaceAllString(repo, "${1}:***@")
}

// LogSafe returns a printable summary with no secrets.
func (c *Config) LogSafe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "global: listen=%s db-path=%s timezone=%s temp-path=%s log-path=%s\n",
		c.Global.Listen, c.Global.DBPath, c.Global.Location, c.Global.TempPath, c.Global.LogPath)
	if c.Telegram != nil {
		fmt.Fprintf(&b, "telegram: enabled chat-id=%s\n", c.Telegram.ChatID)
	} else {
		b.WriteString("telegram: disabled\n")
	}
	if c.Auth != nil {
		fmt.Fprintf(&b, "auth: enabled issuer=%s client-id=%s redirect-url=%s\n",
			c.Auth.Issuer, c.Auth.ClientID, c.Auth.RedirectURL)
	} else {
		b.WriteString("auth: disabled (API is open)\n")
	}
	m := c.Maintenance
	fmt.Fprintf(&b, "restic maintenance: prune-cron=%q check-cron=%q check-read-percent=%d\n",
		m.PruneCron, m.CheckCron, m.ReadPercent())
	fmt.Fprintf(&b, "backups: %d\n", len(c.Backups))
	for _, bc := range c.Backups {
		fmt.Fprintf(&b, "  - %s: cron=%q container=%s database=%s restic-tag=%s roles=%t repo=%s restore-cron=%q restore-image=%q\n",
			bc.Name, bc.Cron, bc.Container, bc.Database, bc.ResticTag, bc.Roles,
			RedactRepository(bc.Restic.Repository), bc.RestoreCron, bc.RestoreImage)
	}
	return b.String()
}