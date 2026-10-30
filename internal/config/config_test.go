package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	resticOK = "[restic]\nrepository = \"/srv/restic\"\npassword = \"pw\"\nkeep-daily = 7\n"
	std      = "cron = \"@daily\"\ncontainer = \"pg\"\ndatabase = \"app\"\n"
)

func globalSec(dir, extra string) string {
	return fmt.Sprintf("[global]\ntemp_path = %q\nlog_path = %q\ndb_path = %q\n%s\n",
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

func TestResticOverridePrecedence(t *testing.T) {
	body := resticOK + "[backup.a]\n" + std + "repository = \"/other\"\nkeep-daily = 14\n" + "[backup.b]\n" + std
	cfg, err := loadWith(t, "", body)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := cfg.Get("a")
	b, _ := cfg.Get("b")
	if a.Restic.Repository != "/other" || a.Restic.KeepDaily != 14 || a.Restic.Password.Reveal() != "pw" {
		t.Fatalf("override failed: %+v", a.Restic)
	}
	if b.Restic.Repository != "/srv/restic" || b.Restic.KeepDaily != 7 {
		t.Fatalf("global lost: %+v", b.Restic)
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
		{"negative keep", "", resticOK + "[backup.a]\n" + std + "keep-daily = -1\n", "backup.a.keep-daily"},
		{"bad tag", "", resticOK + "[backup.a]\n" + std + "restic_tag = \"a,b\"\n", "backup.a.restic_tag"},
		{"no restic", "", "[backup.a]\n" + std, "backup.a.repository"},
		{"telegram chat", "", resticOK + "[telegram]\nbot_token = \"x\"\n", "telegram.chat_id"},
		{"auth url", "", resticOK + "[auth]\nissuer = \"nope\"\nclient_id = \"c\"\nsecret = \"s\"\nredirect_url = \"https://x/cb\"\n", "auth.issuer"},
		{"auth ttl", "", resticOK + "[auth]\nissuer = \"https://i\"\nclient_id = \"c\"\nsecret = \"s\"\nredirect_url = \"https://x/cb\"\nsession_ttl = \"abc\"\n", "auth.session_ttl"},
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
	if err != nil || len(cfg.Warnings) != 1 {
		t.Fatalf("err=%v warnings=%v", err, cfg.Warnings)
	}
}

func TestIncludes(t *testing.T) {
	dir := t.TempDir()
	main := write(t, filepath.Join(dir, "main.toml"),
		globalSec(dir, `include = ["conf/*.toml", "nothing/*.toml"]`)+resticOK+"[backup.main_db]\n"+std)
	write(t, filepath.Join(dir, "conf", "b.toml"), "[backup.b]\n"+std)
	write(t, filepath.Join(dir, "conf", "a.toml"), "[backup.a]\n"+std)

	cfg, err := Load(main)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, b := range cfg.List() {
		names = append(names, b.Name)
	}
	if strings.Join(names, ",") != "a,b,main_db" {
		t.Fatalf("order: %v", names)
	}

	write(t, filepath.Join(dir, "conf", "c.toml"), "[backup.main_db]\n"+std)
	_, err = Load(main)
	if err == nil || !strings.Contains(err.Error(), "main.toml") || !strings.Contains(err.Error(), "c.toml") {
		t.Fatalf("duplicate backup must name both files, got %v", err)
	}
	os.Remove(filepath.Join(dir, "conf", "c.toml"))

	write(t, filepath.Join(dir, "conf", "d.toml"), resticOK)
	_, err = Load(main)
	if err == nil || !strings.Contains(err.Error(), "[restic] is already defined") {
		t.Fatalf("duplicate section must fail, got %v", err)
	}
}

func TestEnvExpansion(t *testing.T) {
	t.Setenv("PGK_TEST_REPO", "/from/env")
	body := "[restic]\nrepository = \"${PGK_TEST_REPO}\"\npassword = \"${PGK_TEST_UNSET_PW:-fallback}\"\n[backup.a]\n" + std
	cfg, err := loadWith(t, "", body)
	if err != nil {
		t.Fatal(err)
	}
	b := cfg.Backups[0]
	if b.Restic.Repository != "/from/env" || b.Restic.Password.Reveal() != "fallback" {
		t.Fatalf("%+v", b.Restic)
	}

	_, err = loadWith(t, "", resticOK+"[backup.a]\n"+std+"host = \"${PGK_TEST_MISSING}\"\n")
	if err == nil || !strings.Contains(err.Error(), "environment variable PGK_TEST_MISSING is not set") ||
		!strings.Contains(err.Error(), "backup.a.host") {
		t.Fatalf("got %v", err)
	}
}

func TestEnvExpansionInInclude(t *testing.T) {
	dir := t.TempDir()
	main := write(t, filepath.Join(dir, "main.toml"), globalSec(dir, `include = ["conf/*.toml"]`)+resticOK)
	write(t, filepath.Join(dir, "conf", "x.toml"), "[backup.a]\n"+std+"host = \"${PGK_TEST_MISSING_INC}\"\n")
	_, err := Load(main)
	if err == nil || !strings.Contains(err.Error(), "x.toml") {
		t.Fatalf("got %v", err)
	}
}

func TestRedactRepository(t *testing.T) {
	got := RedactRepository("rest:https://user:s3cret@host:8000/repo")
	if strings.Contains(got, "s3cret") || !strings.Contains(got, "user:***@") {
		t.Fatal(got)
	}
	if RedactRepository("/srv/restic") != "/srv/restic" {
		t.Fatal("plain path must be unchanged")
	}
}