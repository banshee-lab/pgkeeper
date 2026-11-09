package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	resticOK = "[restic]\nrepository = \"/srv/restic\"\npassword = \"pw\"\nkeep-daily = 7\n"
	std      = "cron = \"@daily\"\ncontainer = \"pg\"\ndatabase = \"app\"\n"
)

func globalSec(dir, extra string) string {
	return fmt.Sprintf("[global]\ntemp-path = %q\nlog-path = %q\ndb-path = %q\n%s\n",
		filepath.Join(dir, "tmp"), filepath.Join(dir, "log"), filepath.Join(dir, "db", "x.db"), extra)
}

func write(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadWith(t *testing.T, extraGlobal, body string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	return Load(write(t, filepath.Join(dir, "main.toml"), globalSec(dir, extraGlobal)+body))
}

func TestDefaults(t *testing.T) {
	cfg, err := loadWith(t, "", resticOK+"[backup.main_db]\n"+std)
	if err != nil {
		t.Fatal(err)
	}
	b := cfg.Backups[0]
	if b.Username != "postgres" || b.Port != 5432 || b.Host != "localhost" ||
		b.ResticTag != "main_db" || b.Roles || b.Restic.KeepDaily != 7 || cfg.Global.Listen != ":8080" {
		t.Fatalf("unexpected defaults: %+v", b)
	}
}

func TestHyphenatedConfigKeys(t *testing.T) {
	cfg, err := loadWith(t, "", resticOK+
		"[telegram]\nbot-token = \"token\"\nchat-id = \"chat\"\n"+
		"[auth]\nissuer = \"https://id.example\"\nclient-id = \"client\"\nsecret = \"secret\"\n"+
		"redirect-url = \"https://app.example/callback\"\nsession-ttl = \"1h\"\n"+
		"cookie-secure = false\npost-login-redirect = \"/backups\"\n"+
		"[restore]\nrestore-image = \"postgres:18\"\n"+
		"[backup.main_db]\n"+std+"restic-tag = \"database\"\nrestore-cron = \"@weekly\"\nrestore-image = \"postgres:17\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telegram == nil || cfg.Telegram.ChatID != "chat" {
		t.Fatalf("telegram hyphenated keys not parsed: %+v", cfg.Telegram)
	}
	if cfg.Auth == nil || cfg.Auth.ClientID != "client" || cfg.Auth.CookieSecure ||
		cfg.Auth.SessionTTL != time.Hour || cfg.Auth.PostLoginRedirect != "/backups" {
		t.Fatalf("auth hyphenated keys not parsed: %+v", cfg.Auth)
	}
	b := cfg.Backups[0]
	if b.ResticTag != "database" || b.RestoreCron != "@weekly" || b.RestoreImage != "postgres:17" {
		t.Fatalf("backup hyphenated keys not parsed: %+v", b)
	}
}

func TestResticOverridePrecedence(t *testing.T) {
	body := resticOK +
		"[backup.a]\n" + std + "restic-repository = \"/other\"\nrestic-keep-daily = 14\n" +
		"[backup.b]\n" + std +
		"[backup.c]\n" + std + "keep-daily = 9\n"
	cfg, err := loadWith(t, "", body)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := cfg.Get("a")
	b, _ := cfg.Get("b")
	c, _ := cfg.Get("c")
	if a.Restic.Repository != "/other" || a.Restic.KeepDaily != 14 || a.Restic.Password.Reveal() != "pw" {
		t.Fatalf("override failed: %+v", a.Restic)
	}
	if b.Restic.Repository != "/srv/restic" || b.Restic.KeepDaily != 7 {
		t.Fatalf("global lost: %+v", b.Restic)
	}
	if c.Restic.KeepDaily != 9 {
		t.Fatalf("unprefixed compatibility lost: %+v", c.Restic)
	}
}

func TestValidation(t *testing.T) {
	cases := []struct{ name, global, body, want string }{
		{"bad cron", "", resticOK + "[backup.a]\ncron = \"foo\"\ncontainer = \"pg\"\ndatabase = \"app\"\n", `backup.a.cron: invalid cron expression "foo"`},
		{"bad label", "", resticOK + "[backup.-bad]\n" + std, "invalid label"},
		{"missing container", "", resticOK + "[backup.a]\ncron = \"@daily\"\ndatabase = \"app\"\n", "backup.a.container: must not be empty"},
		{"option injection", "", resticOK + "[backup.a]\ncron = \"@daily\"\ncontainer = \"-x\"\ndatabase = \"app\"\n", "must not start with '-'"},
		{"bad username", "", resticOK + "[backup.a]\n" + std + "username = \"-u\"\n", "backup.a.username"},
		{"bad port", "", resticOK + "[backup.a]\n" + std + "port = 70000\n", "backup.a.port"},
		{"negative keep", "", resticOK + "[backup.a]\n" + std + "restic-keep-daily = -1\n", "backup.a.restic-keep-daily"},
		{"duplicate restic override", "", resticOK + "[backup.a]\n" + std + "keep-daily = 1\nrestic-keep-daily = 2\n", "cannot be set together"},
		{"bad tag", "", resticOK + "[backup.a]\n" + std + "restic-tag = \"a,b\"\n", "backup.a.restic-tag"},
		{"no restic", "", "[backup.a]\n" + std, "backup.a.repository"},
		{"telegram chat", "", resticOK + "[telegram]\nbot-token = \"x\"\n", "telegram.chat-id"},
		{"auth url", "", resticOK + "[auth]\nissuer = \"nope\"\nclient-id = \"c\"\nsecret = \"s\"\nredirect-url = \"https://x/cb\"\n", "auth.issuer"},
		{"auth ttl", "", resticOK + "[auth]\nissuer = \"https://i\"\nclient-id = \"c\"\nsecret = \"s\"\nredirect-url = \"https://x/cb\"\nsession-ttl = \"abc\"\n", "auth.session-ttl"},
		{"underscore config key", "temp_path = \"/tmp\"\n", resticOK, `unknown key "global.temp_path"`},
		{"timezone", "timezone = \"Mars/Base\"", resticOK, "global.timezone"},
		{"unknown key", "", resticOK + "[backup.a]\n" + std + "crno = \"x\"\n", `unknown key "backup.a.crno"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadWith(t, tc.global, tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestAllErrorsAggregated(t *testing.T) {
	_, err := loadWith(t, "timezone = \"Mars/Base\"",
		resticOK+"[backup.a]\ncron = \"foo\"\ncontainer = \"pg\"\ndatabase = \"app\"\nport = 0\n")
	if err == nil {
		t.Fatal("expected error")
	}
	for _, w := range []string{"global.timezone", "backup.a.cron", "backup.a.port"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("missing %q in %v", w, err)
		}
	}
}

func TestDuplicateTargetIsWarning(t *testing.T) {
	cfg, err := loadWith(t, "", resticOK+"[backup.a]\n"+std+"[backup.b]\n"+std)
	dups := 0
	for _, w := range cfg.Warnings {
		if strings.Contains(w, "target the same container/database") {
			dups++
		}
	}
	if err != nil || dups != 1 {
		t.Fatalf("err=%v warnings=%v", err, cfg.Warnings)
	}
}

func TestSkipPrune(t *testing.T) {
	has := func(cfg *Config) bool {
		for _, w := range cfg.Warnings {
			if strings.Contains(w, "skip-prune") {
				return true
			}
		}
		return false
	}
	// default: forget prunes, no warning
	cfg, err := loadWith(t, "", resticOK+"[backup.a]\n"+std)
	if err != nil || cfg.Backups[0].Restic.SkipPrune || has(cfg) {
		t.Fatalf("default must prune without warning: err=%v warnings=%v", err, cfg.Warnings)
	}
	// global skip-prune without a prune schedule warns
	cfg, err = loadWith(t, "", resticOK+"skip-prune = true\n[backup.a]\n"+std)
	if err != nil || !cfg.Backups[0].Restic.SkipPrune || !has(cfg) {
		t.Fatalf("skip-prune without prune-cron must warn: err=%v warnings=%v", err, cfg.Warnings)
	}
	// with a prune schedule: no warning
	cfg, err = loadWith(t, "", resticOK+"skip-prune = true\nprune-cron = \"@weekly\"\n[backup.a]\n"+std)
	if err != nil || !cfg.Backups[0].Restic.SkipPrune || has(cfg) {
		t.Fatalf("no warning expected with prune-cron: err=%v warnings=%v", err, cfg.Warnings)
	}
	// per-backup override of the global setting
	cfg, err = loadWith(t, "", resticOK+"skip-prune = true\nprune-cron = \"@weekly\"\n[backup.a]\n"+std+"skip-prune = false\n")
	if err != nil || cfg.Backups[0].Restic.SkipPrune {
		t.Fatalf("backup override ignored: err=%v", err)
	}
}

func TestRestoreSettings(t *testing.T) {
	body := resticOK + "[restore]\nrestore-image = \"pg18\"\n" +
		"[backup.a]\n" + std + "restore-cron = \"0 * * * *\"\nrestore-image = \"pg17\"\n" +
		"[backup.b]\n" + std + "restore-cron = \"@daily\"\n" +
		"[backup.c]\n" + std
	cfg, err := loadWith(t, "", body)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := cfg.Get("a")
	b, _ := cfg.Get("b")
	c, _ := cfg.Get("c")
	if a.RestoreCron != "0 * * * *" || a.RestoreImage != "pg17" {
		t.Fatalf("override: %+v", a)
	}
	if b.RestoreCron != "@daily" || b.RestoreImage != "pg18" {
		t.Fatalf("fallback to global image: %+v", b)
	}
	if c.RestoreCron != "" || c.RestoreImage != "pg18" {
		t.Fatalf("no schedule: %+v", c)
	}
}

func TestRestoreSettingsValidation(t *testing.T) {
	for name, body := range map[string]string{
		"cron without image": resticOK + "[backup.a]\n" + std + "restore-cron = \"@daily\"\n",
		"bad cron":           resticOK + "[restore]\nrestore-image = \"pg18\"\n[backup.a]\n" + std + "restore-cron = \"nope\"\n",
		"bad image":          resticOK + "[restore]\nrestore-image = \"-rm\"\n[backup.a]\n" + std,
		"unknown key":        resticOK + "[restore]\nimage = \"pg18\"\n[backup.a]\n" + std,
	} {
		if _, err := loadWith(t, "", body); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

const urlBackup = "[backup.r]\ncron = \"@daily\"\ndatabase-url = \"postgresql://u:hunter2@db.example.com:6543/orders?sslmode=require\"\n"

func TestDatabaseURLBackup(t *testing.T) {
	cfg, err := loadWith(t, "tools-image = \"postgres:18\"\n", resticOK+urlBackup)
	if err != nil {
		t.Fatal(err)
	}
	b := cfg.Backups[0]
	if b.Conn == nil || b.Container != "" || b.Host != "db.example.com" || b.Port != 6543 || b.Username != "u" || b.Database != "orders" {
		t.Fatalf("unexpected: %+v", b)
	}
	if b.ToolsImage != "postgres:18" || cfg.ToolsImage != "postgres:18" {
		t.Fatalf("tools image = %q / %q", b.ToolsImage, cfg.ToolsImage)
	}
	for _, w := range cfg.Warnings {
		if strings.Contains(w, "tools-image") {
			t.Errorf("unexpected warning: %s", w)
		}
	}
	if safe := cfg.LogSafe(); strings.Contains(safe, "hunter2") || !strings.Contains(safe, "postgresql://u@db.example.com:6543/orders") {
		t.Fatalf("LogSafe = %s", safe)
	}
}

func TestDatabaseURLExpandsEnvironment(t *testing.T) {
	t.Setenv("PGKEEPER_TEST_DB_PASSWORD", "hunter2")
	cfg, err := loadWith(t, "tools-image = \"postgres:18\"\n", resticOK+
		"[backup.r]\ncron = \"@daily\"\ndatabase-url = \"postgresql://u:${PGKEEPER_TEST_DB_PASSWORD}@db/orders\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Backups[0].Conn.Password.Reveal(); got != "hunter2" {
		t.Fatalf("password = %q", got)
	}
}

func TestDatabaseURLWithoutToolsImageWarns(t *testing.T) {
	cfg, err := loadWith(t, "", resticOK+urlBackup+"[backup.c]\n"+std)
	if err != nil {
		t.Fatal(err)
	}
	var found int
	for _, w := range cfg.Warnings {
		if strings.Contains(w, "backup r uses database-url") && strings.Contains(w, "tools-image") {
			found++
		}
		if strings.Contains(w, "backup c") && strings.Contains(w, "tools-image") {
			t.Errorf("container backup must not warn: %s", w)
		}
	}
	if found != 1 {
		t.Fatalf("warnings = %q", cfg.Warnings)
	}
}

func TestDatabaseURLValidation(t *testing.T) {
	head := "[backup.r]\ncron = \"@daily\"\n"
	for _, tc := range []struct{ name, global, body, want string }{
		{"with container", "", head + "container = \"pg\"\ndatabase-url = \"postgresql://u@h/db\"\n", "backup.r.container: cannot be set together with database-url"},
		{"with host", "", head + "host = \"h\"\ndatabase-url = \"postgresql://u@h/db\"\n", "backup.r.host: cannot be set together with database-url"},
		{"bad scheme", "", head + "database-url = \"mysql://u:hunter2@h/db\"\n", "backup.r.database-url: scheme"},
		{"bad param", "", head + "database-url = \"postgresql://u:hunter2@h/db?sslrootcert=/x\"\n", "unsupported parameter"},
		{"no database", "", head + "database-url = \"postgresql://u@h\"\n", "must name a database"},
		{"database mismatch", "", head + "database = \"other\"\ndatabase-url = \"postgresql://u@h/db\"\n", "backup.r.database: does not match"},
		{"bad tools image", "tools-image = \"bad image\"\n", "", "global.tools-image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadWith(t, tc.global, resticOK+tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "hunter2") {
				t.Fatalf("error leaks the password: %v", err)
			}
		})
	}
}

func TestDatabaseURLDatabaseKeyAllowed(t *testing.T) {
	cfg, err := loadWith(t, "", resticOK+"[backup.r]\ncron = \"@daily\"\ndatabase = \"orders\"\ndatabase-url = \"postgresql://u@h\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if b := cfg.Backups[0]; b.Database != "orders" || b.Conn.Database != "orders" {
		t.Fatalf("unexpected: %+v", b)
	}
}
