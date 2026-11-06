package config

import (
	"reflect"
	"sync/atomic"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

// Holder keeps the active Config and lets it be replaced atomically on
// reload. It implements domain.BackupCatalog, so consumers always see the
// current configuration.
type Holder struct{ p atomic.Pointer[Config] }

var _ domain.BackupCatalog = (*Holder)(nil)

func NewHolder(c *Config) *Holder {
	h := &Holder{}
	h.p.Store(c)
	return h
}

// Current returns the active configuration; callers must not modify it.
func (h *Holder) Current() *Config { return h.p.Load() }

// Swap installs c and returns the previous configuration.
func (h *Holder) Swap(c *Config) *Config { return h.p.Swap(c) }

func (h *Holder) List() []domain.BackupConfig { return h.Current().List() }

func (h *Holder) Get(name string) (domain.BackupConfig, bool) { return h.Current().Get(name) }

// RestartRequired lists the settings that differ between the running
// configuration and a freshly loaded one but cannot be applied without a
// restart (listener, paths, timezone, telegram, auth).
func RestartRequired(cur, next *Config) []string {
	var out []string
	g, n := cur.Global, next.Global
	for _, c := range []struct {
		key      string
		old, new string
	}{
		{"global.timezone", g.Location.String(), n.Location.String()},
		{"global.temp_path", g.TempPath, n.TempPath},
		{"global.log_path", g.LogPath, n.LogPath},
		{"global.listen", g.Listen, n.Listen},
		{"global.db_path", g.DBPath, n.DBPath},
	} {
		if c.old != c.new {
			out = append(out, c.key)
		}
	}
	if !reflect.DeepEqual(cur.Telegram, next.Telegram) {
		out = append(out, "telegram")
	}
	if !reflect.DeepEqual(cur.Auth, next.Auth) {
		out = append(out, "auth")
	}
	return out
}

// KeepRunning returns next with the settings that cannot change at runtime
// taken from cur.
func KeepRunning(cur, next *Config) *Config {
	n := *next
	n.Global = cur.Global
	n.Telegram = cur.Telegram
	n.Auth = cur.Auth
	return &n
}
