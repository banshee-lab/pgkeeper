package config

import (
	"cmp"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

// resolve applies defaults, merges restic settings and validates everything,
// collecting all errors.
func (l *loader) resolve() (*Config, []error) {
	var errs []error
	add := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	r := &l.merged
	cfg := &Config{}

	// ---- global ----
	g := rawGlobal{}
	if r.Global != nil {
		g = *r.Global
	}
	gf := l.srcOr("global")
	cfg.Global = domain.GlobalConfig{
		Location: time.Local,
		TempPath: cmp.Or(g.TempPath, "/var/tmp"),
		LogPath:  cmp.Or(g.LogPath, "/var/log"),
		Listen:   cmp.Or(g.Listen, ":8080"),
		DBPath:   cmp.Or(g.DBPath, "/var/lib/pgkeeper/pgkeeper.db"),
	}
	if g.Timezone != "" {
		loc, err := time.LoadLocation(g.Timezone)
		if err != nil {
			add(keyErr(gf, "global.timezone", "unknown timezone %q", g.Timezone))
		} else {
			cfg.Global.Location = loc
		}
	}
	for _, p := range []struct{ key, dir string }{
		{"global.temp_path", cfg.Global.TempPath},
		{"global.log_path", cfg.Global.LogPath},
		{"global.db_path", filepath.Dir(cfg.Global.DBPath)},
	} {
		if err := checkWritableDir(p.dir); err != nil {
			add(keyErr(gf, p.key, "%v", err))
		}
	}

	// ---- telegram ----
	if t := r.Telegram; t != nil {
		f := l.src["telegram"]
		if t.BotToken == "" {
			add(keyErr(f, "telegram.bot_token", "must not be empty"))
		}
		if t.ChatID == "" {
			add(keyErr(f, "telegram.chat_id", "must not be empty"))
		}
		cfg.Telegram = &TelegramConfig{BotToken: domain.Secret(t.BotToken), ChatID: t.ChatID}
	}

	// ---- auth ----
	if a := r.Auth; a != nil {
		f := l.src["auth"]
		ac := &AuthConfig{
			Issuer: a.Issuer, ClientID: a.ClientID, Secret: domain.Secret(a.Secret),
			RedirectURL: a.RedirectURL, Scopes: a.Scopes,
			SessionTTL:        12 * time.Hour,
			CookieSecure:      deref(a.CookieSecure, true),
			PostLoginRedirect: cmp.Or(a.PostLoginRedirect, "/"),
		}
		if len(ac.Scopes) == 0 {
			ac.Scopes = []string{"openid", "profile", "email"}
		}
		for _, q := range []struct{ k, v string }{
			{"issuer", a.Issuer}, {"client_id", a.ClientID},
			{"secret", a.Secret}, {"redirect_url", a.RedirectURL},
		} {
			if q.v == "" {
				add(keyErr(f, "auth."+q.k, "must not be empty"))
			}
		}
		for _, q := range []struct{ k, v string }{{"issuer", a.Issuer}, {"redirect_url", a.RedirectURL}} {
			if q.v != "" {
				if err := absURL(q.v); err != nil {
					add(keyErr(f, "auth."+q.k, "%v (%q)", err, q.v))
				}
			}
		}
		if a.SessionTTL != "" {
			d, err := time.ParseDuration(a.SessionTTL)
			if err != nil || d <= 0 {
				add(keyErr(f, "auth.session_ttl", "invalid duration %q", a.SessionTTL))
			} else {
				ac.SessionTTL = d
			}
		}
		cfg.Auth = ac
	}

	// ---- global restic ----
	gr := rawRestic{}
	if r.Restic != nil {
		gr = *r.Restic
		errs = append(errs, checkKeeps(l.src["restic"], "restic.", gr)...)
	}
	cfg.Maintenance = resolveMaintenance(l.srcOr("restic"), gr, add)

	// ---- backups ----
	seen := map[string]string{} // container\x00database -> backup name
	for _, name := range slices.Sorted(maps.Keys(r.Backup)) {
		b := r.Backup[name]
		file := l.backupSrc[name]
		p := "backup." + name + "."

		if !labelRe.MatchString(name) {
			add(keyErr(file, "backup."+name, "invalid label (must match %s)", labelRe))
		}
		for _, q := range []struct{ k, v string }{
			{"cron", b.Cron}, {"container", b.Container}, {"database", b.Database},
		} {
			if q.v == "" {
				add(keyErr(file, p+q.k, "must not be empty"))
			}
		}
		if b.Cron != "" {
			if _, err := CronParser.Parse(b.Cron); err != nil {
				add(keyErr(file, p+"cron", "invalid cron expression %q: %v", b.Cron, err))
			}
		}

		br := b.restic()
		bc := domain.BackupConfig{
			Name: name, Cron: b.Cron, Container: b.Container, Database: b.Database,
			Username:  deref(b.Username, "postgres"),
			Host:      deref(b.Host, "localhost"),
			Port:      deref(b.Port, 5432),
			Roles:     deref(b.Roles, false),
			ResticTag: deref(b.ResticTag, name),
			Restic: domain.ResticConfig{
				Repository:  firstSet(br.Repository, gr.Repository),
				Password:    domain.Secret(firstSet(br.Password, gr.Password)),
				KeepDaily:   firstSet(br.KeepDaily, gr.KeepDaily),
				KeepWeekly:  firstSet(br.KeepWeekly, gr.KeepWeekly),
				KeepMonthly: firstSet(br.KeepMonthly, gr.KeepMonthly),
				KeepYearly:  firstSet(br.KeepYearly, gr.KeepYearly),
				SkipPrune:   firstSet(br.SkipPrune, gr.SkipPrune),
			},
		}

		for _, q := range []struct {
			k, v     string
			required bool
		}{
			{"container", bc.Container, false}, {"database", bc.Database, false},
			{"username", bc.Username, true}, {"host", bc.Host, true},
		} {
			if q.v == "" {
				if q.required {
					add(keyErr(file, p+q.k, "must not be empty"))
				}
				continue
			}
			if err := checkSafe(q.v); err != nil {
				add(keyErr(file, p+q.k, "%v", err))
			}
		}
		if bc.Port < 1 || bc.Port > 65535 {
			add(keyErr(file, p+"port", "must be between 1 and 65535 (got %d)", bc.Port))
		}
		if bc.ResticTag == "" ||
			strings.IndexFunc(bc.ResticTag, unicode.IsSpace) >= 0 ||
			strings.Contains(bc.ResticTag, ",") {
			add(keyErr(file, p+"restic_tag", "must be non-empty and contain no whitespace or commas (got %q)", bc.ResticTag))
		}
		if bc.Restic.Repository == "" {
			add(keyErr(file, p+"repository", "required (set it in [restic] or override it here)"))
		}
		if bc.Restic.Password == "" {
			add(keyErr(file, p+"password", "required (set it in [restic] or override it here)"))
		}
		errs = append(errs, checkKeeps(file, p, br)...)

		if bc.Container != "" && bc.Database != "" {
			key := bc.Container + "\x00" + bc.Database
			if other, dup := seen[key]; dup {
				cfg.Warnings = append(cfg.Warnings, "backups "+other+" and "+name+
					" target the same container/database ("+bc.Container+"/"+bc.Database+")")
			} else {
				seen[key] = name
			}
		}
		cfg.Backups = append(cfg.Backups, bc)
	}
	if len(cfg.Backups) == 0 {
		cfg.Warnings = append(cfg.Warnings, "no [backup.*] sections defined; the service will start without jobs")
	}
	if cfg.Maintenance.PruneCron == "" {
		for _, b := range cfg.Backups {
			if r := b.Restic; r.SkipPrune && r.KeepDaily+r.KeepWeekly+r.KeepMonthly+r.KeepYearly > 0 {
				cfg.Warnings = append(cfg.Warnings, "backup "+b.Name+" has skip-prune = true but restic.prune-cron is not set: "+
					"forgotten snapshots are never pruned, so repository space is not reclaimed")
				break
			}
		}
	}
	return cfg, errs
}
// resolveMaintenance validates the global prune/check settings. A task whose
// cron is empty is disabled; the read percentage defaults to 80.
func resolveMaintenance(file string, gr rawRestic, add func(error)) domain.MaintenanceConfig {
	m := domain.MaintenanceConfig{
		PruneCron:        strings.TrimSpace(deref(gr.PruneCron, "")),
		CheckCron:        strings.TrimSpace(deref(gr.CheckCron, "")),
		CheckReadPercent: deref(gr.CheckReadPercent, domain.DefaultCheckReadPercent),
	}
	for _, c := range []struct{ key, spec string }{
		{"restic.prune-cron", m.PruneCron}, {"restic.check-cron", m.CheckCron},
	} {
		if c.spec == "" {
			continue
		}
		if _, err := CronParser.Parse(c.spec); err != nil {
			add(keyErr(file, c.key, "invalid cron expression %q: %v", c.spec, err))
		}
	}
	if m.CheckReadPercent < 1 || m.CheckReadPercent > 100 {
		add(keyErr(file, "restic.check-read-percent", "must be between 1 and 100 (got %d)", m.CheckReadPercent))
	}
	if gr.Repository != nil && gr.Password != nil && *gr.Repository != "" && *gr.Password != "" {
		m.Repo = &domain.ResticConfig{
			Repository:  *gr.Repository,
			Password:    domain.Secret(*gr.Password),
			KeepDaily:   deref(gr.KeepDaily, 0),
			KeepWeekly:  deref(gr.KeepWeekly, 0),
			KeepMonthly: deref(gr.KeepMonthly, 0),
			KeepYearly:  deref(gr.KeepYearly, 0),
			SkipPrune:   deref(gr.SkipPrune, false),
		}
	}
	return m
}
