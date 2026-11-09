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
		{"global.temp-path", cfg.Global.TempPath},
		{"global.log-path", cfg.Global.LogPath},
		{"global.db-path", filepath.Dir(cfg.Global.DBPath)},
	} {
		if err := checkWritableDir(p.dir); err != nil {
			add(keyErr(gf, p.key, "%v", err))
		}
	}

	toolsImage := strings.TrimSpace(g.ToolsImage)
	if toolsImage != "" {
		if err := checkImage(toolsImage); err != nil {
			add(keyErr(gf, "global.tools-image", "%v (%q)", err, toolsImage))
		}
	}
	cfg.ToolsImage = toolsImage

	// ---- telegram ----
	if t := r.Telegram; t != nil {
		f := l.src["telegram"]
		if t.BotToken == "" {
			add(keyErr(f, "telegram.bot-token", "must not be empty"))
		}
		if t.ChatID == "" {
			add(keyErr(f, "telegram.chat-id", "must not be empty"))
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
			{"issuer", a.Issuer}, {"client-id", a.ClientID},
			{"secret", a.Secret}, {"redirect-url", a.RedirectURL},
		} {
			if q.v == "" {
				add(keyErr(f, "auth."+q.k, "must not be empty"))
			}
		}
		for _, q := range []struct{ k, v string }{{"issuer", a.Issuer}, {"redirect-url", a.RedirectURL}} {
			if q.v != "" {
				if err := absURL(q.v); err != nil {
					add(keyErr(f, "auth."+q.k, "%v (%q)", err, q.v))
				}
			}
		}
		if a.SessionTTL != "" {
			d, err := time.ParseDuration(a.SessionTTL)
			if err != nil || d <= 0 {
				add(keyErr(f, "auth.session-ttl", "invalid duration %q", a.SessionTTL))
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

	// ---- global restore ----
	globalRestoreImage := ""
	if r.Restore != nil {
		globalRestoreImage = strings.TrimSpace(deref(r.Restore.RestoreImage, ""))
		if globalRestoreImage != "" {
			if err := checkImage(globalRestoreImage); err != nil {
				add(keyErr(l.srcOr("restore"), "restore.restore-image", "%v (%q)", err, globalRestoreImage))
			}
		}
	}

	// ---- backups ----
	seen := map[string]string{} // container\x00database -> backup name
	for _, name := range slices.Sorted(maps.Keys(r.Backup)) {
		b := r.Backup[name]
		file := l.backupSrc[name]
		p := "backup." + name + "."

		errs = append(errs, checkResticAliases(file, p, b)...)
		if !labelRe.MatchString(name) {
			add(keyErr(file, "backup."+name, "invalid label (must match %s)", labelRe))
		}
		dbURL := strings.TrimSpace(deref(b.DatabaseURL, ""))
		required := []struct{ k, v string }{{"cron", b.Cron}}
		if dbURL == "" {
			required = append(required, struct{ k, v string }{"container", b.Container},
				struct{ k, v string }{"database", b.Database})
		}
		for _, q := range required {
			if q.v == "" {
				add(keyErr(file, p+q.k, "must not be empty"))
			}
		}
		if b.Cron != "" {
			if _, err := CronParser.Parse(b.Cron); err != nil {
				add(keyErr(file, p+"cron", "invalid cron expression %q: %v", b.Cron, err))
			}
		}

		restoreCron := strings.TrimSpace(deref(b.RestoreCron, ""))
		restoreImage := strings.TrimSpace(deref(b.RestoreImage, ""))
		if restoreCron != "" {
			if _, err := CronParser.Parse(restoreCron); err != nil {
				add(keyErr(file, p+"restore-cron", "invalid cron expression %q: %v", restoreCron, err))
			}
		}
		if restoreImage != "" {
			if err := checkImage(restoreImage); err != nil {
				add(keyErr(file, p+"restore-image", "%v (%q)", err, restoreImage))
			}
		}
		if restoreImage == "" {
			restoreImage = globalRestoreImage
		}
		if restoreCron != "" && restoreImage == "" {
			add(keyErr(file, p+"restore-image", "required when restore-cron is set (set [restore].restore-image or override it here)"))
		}

		br := b.restic()
		bc := domain.BackupConfig{
			Name: name, Cron: b.Cron, Container: b.Container, Database: b.Database,
			Username:  deref(b.Username, "postgres"),
			Host:      deref(b.Host, "localhost"),
			Port:      deref(b.Port, 5432),
			Roles:     deref(b.Roles, false),
			ResticTag: deref(b.ResticTag, name),

			RestoreCron:  restoreCron,
			RestoreImage: restoreImage,
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

		if dbURL != "" {
			bc.Conn = resolveDatabaseURL(file, p, b, dbURL, &bc, add)
		}
		bc.ToolsImage = toolsImage

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
			add(keyErr(file, p+"restic-tag", "must be non-empty and contain no whitespace or commas (got %q)", bc.ResticTag))
		}
		if bc.Restic.Repository == "" {
			add(keyErr(file, p+"repository", "required (set it in [restic] or override it here)"))
		}
		if bc.Restic.Password == "" {
			add(keyErr(file, p+"password", "required (set it in [restic] or override it here)"))
		}
		errs = append(errs, checkBackupKeeps(file, p, b)...)

		if bc.Conn != nil {
			if toolsImage == "" {
				cfg.Warnings = append(cfg.Warnings, "backup "+name+" uses database-url but global.tools-image is not set: "+
					"its backups will fail until a tools image (a postgres image or any image with pg_dump/psql) is configured")
			}
			key := "url\x00" + bc.Conn.Redacted()
			if other, dup := seen[key]; dup {
				cfg.Warnings = append(cfg.Warnings, "backups "+other+" and "+name+
					" target the same database URL ("+bc.Conn.Redacted()+")")
			} else {
				seen[key] = name
			}
		} else if bc.Container != "" && bc.Database != "" {
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

// resolveDatabaseURL validates a project's database-url and copies the
// connection details into bc. Errors never echo the URL (it may carry a
// password). It returns nil when the URL is unusable.
func resolveDatabaseURL(file, p string, b rawBackup, dbURL string, bc *domain.BackupConfig, add func(error)) *domain.PGConn {
	if b.Container != "" {
		add(keyErr(file, p+"container", "cannot be set together with database-url"))
	}
	for _, f := range []struct {
		k   string
		set bool
	}{{"host", b.Host != nil}, {"port", b.Port != nil}, {"username", b.Username != nil}} {
		if f.set {
			add(keyErr(file, p+f.k, "cannot be set together with database-url (it is taken from the URL)"))
		}
	}
	conn, err := domain.ParsePGURL(dbURL)
	if err != nil {
		add(keyErr(file, p+"database-url", "%v", err))
		return nil
	}
	switch {
	case b.Database != "" && conn.Database != "" && b.Database != conn.Database:
		add(keyErr(file, p+"database", "does not match the database in database-url"))
	case b.Database == "" && conn.Database == "":
		add(keyErr(file, p+"database-url", "must name a database (…/dbname) or set database"))
	}
	if conn.Database == "" {
		conn.Database = b.Database
	}
	bc.Database = conn.Database
	bc.Host, bc.Port, bc.Username = conn.Host, conn.Port, conn.User
	return &conn
}
