package restic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

const (
	testPass = "hunter2"
	testRepo = "rest:https://u:pw@host/"
)

func testCfg() domain.ResticConfig {
	return domain.ResticConfig{Repository: testRepo, Password: testPass, KeepDaily: 7, KeepWeekly: 4}
}

// fake is a shell script standing in for restic. It records args, cwd and
// RESTIC_* env per subcommand and replays out/err/exit files set by the test.
type fake struct {
	t   *testing.T
	dir string
	cli *CLI
}

func newFake(t *testing.T) *fake {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fake needs a POSIX sh")
	}
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
D=%q
echo "$*" >> "$D/args"
pwd > "$D/pwd.$1"
env | grep '^RESTIC_' | sort > "$D/env.$1"
if [ -f "$D/out.$1" ]; then cat "$D/out.$1"; fi
if [ -f "$D/err.$1" ]; then cat "$D/err.$1" >&2; fi
if [ -f "$D/exit.$1" ]; then exit "$(cat "$D/exit.$1")"; fi
exit 0
`, dir)
	bin := filepath.Join(dir, "restic")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cli := &CLI{
		Bin:      bin,
		TempPath: t.TempDir(),
		Environ: func() []string {
			return []string{"PATH=/usr/bin:/bin", "HOME=/tmp", "AWS_ACCESS_KEY_ID=akid",
				"AWS_SECRET_ACCESS_KEY=topsecret", "FOO=bar"}
		},
	}
	return &fake{t: t, dir: dir, cli: cli}
}

func (f *fake) set(cmd, kind, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, kind+"."+cmd), []byte(content+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fake) read(name string) string {
	b, _ := os.ReadFile(filepath.Join(f.dir, name))
	return strings.TrimSpace(string(b))
}

func (f *fake) calls() []string {
	s := f.read("args")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func (f *fake) called(prefix string) bool {
	return slices.ContainsFunc(f.calls(), func(l string) bool { return strings.HasPrefix(l, prefix) })
}

type logfCap struct{ lines []string }

func (l *logfCap) Write(p []byte) (int, error) { return len(p), nil }
func (l *logfCap) Logf(level, format string, args ...any) {
	l.lines = append(l.lines, level+" "+fmt.Sprintf(format, args...))
}

func TestForgetArgs(t *testing.T) {
	cases := []struct {
		name string
		cfg  domain.ResticConfig
		want []string
	}{
		{"all", domain.ResticConfig{KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 12, KeepYearly: 2},
			[]string{"forget", "--tag", "t", "--host", "h", "--retry-lock", "5m",
				"--keep-daily", "7", "--keep-weekly", "4", "--keep-monthly", "12", "--keep-yearly", "2", "--prune"}},
		{"only daily", domain.ResticConfig{KeepDaily: 3},
			[]string{"forget", "--tag", "t", "--host", "h", "--retry-lock", "5m", "--keep-daily", "3", "--prune"}},
		{"yearly only", domain.ResticConfig{KeepYearly: 1},
			[]string{"forget", "--tag", "t", "--host", "h", "--retry-lock", "5m", "--keep-yearly", "1", "--prune"}},
		{"none", domain.ResticConfig{}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := forgetArgs(tc.cfg, "t", "h"); !slices.Equal(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestBackupArgs(t *testing.T) {
	got := backupArgs("t", "h", []string{"app.sql", "app_roles.sql"})
	want := []string{"backup", "--json", "--tag", "t", "--host", "h", "--retry-lock", "5m", "--", "app.sql", "app_roles.sql"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestBuildEnv(t *testing.T) {
	environ := []string{"PATH=/bin", "HOME=/h", "TMPDIR=/t", "FOO=bar", "AWS_X=1", "RCLONE_Y=2",
		"RESTIC_PASSWORD_FILE=/p", "RESTIC_PASSWORD=old", "RESTIC_REPOSITORY=old"}
	env := buildEnv(testCfg(), "/cache", environ)
	for _, want := range []string{"PATH=/bin", "HOME=/h", "TMPDIR=/t", "AWS_X=1", "RCLONE_Y=2",
		"RESTIC_REPOSITORY=" + testRepo, "RESTIC_PASSWORD=" + testPass, "RESTIC_CACHE_DIR=/cache"} {
		if !slices.Contains(env, want) {
			t.Errorf("missing %q in %v", want, env)
		}
	}
	for _, bad := range []string{"FOO=bar", "RESTIC_PASSWORD_FILE=/p", "RESTIC_PASSWORD=old", "RESTIC_REPOSITORY=old"} {
		if slices.Contains(env, bad) {
			t.Errorf("unexpected %q in %v", bad, env)
		}
	}
}

func TestEnsureRepo(t *testing.T) {
	cases := []struct {
		name, exit, stderr string
		wantInit, wantErr  bool
	}{
		{"exists", "", "", false, false},
		{"missing exit 10", "10", "Fatal: repository does not exist", true, false},
		{"missing old restic", "1", "Fatal: unable to open config file: stat /x/config: no such file or directory", true, false},
		{"wrong password", "12", "Fatal: wrong password or no key found", false, true},
		{"permission denied", "1", "Fatal: unable to open config file: open /x/config: permission denied", false, true},
		{"network", "1", "Fatal: dial tcp: lookup host: no such host", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			if tc.exit != "" {
				f.set("cat", "exit", tc.exit)
				f.set("cat", "err", tc.stderr)
			}
			err := f.cli.EnsureRepo(context.Background(), testCfg(), io.Discard)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if f.called("init") != tc.wantInit {
				t.Fatalf("init called=%v want %v (calls %v)", f.called("init"), tc.wantInit, f.calls())
			}
			if err != nil && !strings.Contains(err.Error(), "init:") {
				t.Fatalf("error should be prefixed with init: %v", err)
			}
		})
	}
}

func TestBackupStreamsAndRedacts(t *testing.T) {
	f := newFake(t)
	f.set("backup", "out", "stdout line")
	f.set("backup", "err", "uploading with hunter2 to rest:https://u:pw@host/\nlast")
	work := t.TempDir()
	var lg bytes.Buffer
	files := []string{"app.sql", "app_roles.sql"}
	if _, err := f.cli.Backup(context.Background(), testCfg(), "tag1", "name1", work, files, &lg); err != nil {
		t.Fatal(err)
	}

	out := lg.String()
	for _, want := range []string{"[restic] stdout line", "[restic] uploading with *** to rest:https://***@host/", "[restic] last"} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, testPass) || strings.Contains(out, "pw@") {
		t.Errorf("secret leaked in log:\n%s", out)
	}

	args := f.read("args")
	if want := "backup --json --tag tag1 --host name1 --retry-lock 5m -- app.sql app_roles.sql"; args != want {
		t.Errorf("args = %q, want %q", args, want)
	}
	if strings.Contains(args, testPass) || strings.Contains(args, "pw@") {
		t.Errorf("secret in argv: %s", args)
	}
	env := f.read("env.backup")
	if !strings.Contains(env, "RESTIC_PASSWORD="+testPass) || !strings.Contains(env, "RESTIC_REPOSITORY="+testRepo) {
		t.Errorf("env missing password/repository:\n%s", env)
	}
	gotDir, _ := filepath.EvalSymlinks(f.read("pwd.backup"))
	wantDir, _ := filepath.EvalSymlinks(work)
	if gotDir != wantDir {
		t.Errorf("cwd = %q, want %q", gotDir, wantDir)
	}
}

func TestBackupUsesLogf(t *testing.T) {
	f := newFake(t)
	f.set("backup", "err", "hello")
	lg := &logfCap{}
	if _, err := f.cli.Backup(context.Background(), testCfg(), "t", "h", t.TempDir(), []string{"a.sql"}, lg); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(lg.lines, "INFO [restic] hello") {
		t.Fatalf("lines: %v", lg.lines)
	}
}

func TestBackupFailure(t *testing.T) {
	f := newFake(t)
	f.set("backup", "exit", "1")
	f.set("backup", "err", "Fatal: boom hunter2")
	_, err := f.cli.Backup(context.Background(), testCfg(), "t", "h", t.TempDir(), []string{"a.sql"}, io.Discard)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "backup:") || !strings.Contains(msg, "status 1") || !strings.Contains(msg, "Fatal: boom ***") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(msg, testPass) {
		t.Fatalf("password leaked in error: %v", err)
	}
}

func TestForgetSkippedWhenNoRetention(t *testing.T) {
	f := newFake(t)
	var lg bytes.Buffer
	cfg := domain.ResticConfig{Repository: testRepo, Password: testPass}
	if err := f.cli.Forget(context.Background(), cfg, "t", "h", &lg); err != nil {
		t.Fatal(err)
	}
	if f.called("forget") {
		t.Fatal("restic must not be called")
	}
	if !strings.Contains(lg.String(), "retention not configured, skipping forget") {
		t.Fatalf("log: %s", lg.String())
	}
}

func TestForgetFailure(t *testing.T) {
	f := newFake(t)
	f.set("forget", "exit", "1")
	f.set("forget", "err", "Fatal: prune failed")
	err := f.cli.Forget(context.Background(), testCfg(), "t", "h", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "forget:") || !strings.Contains(err.Error(), "prune failed") {
		t.Fatalf("unexpected: %v", err)
	}
	want := "forget --tag t --host h --retry-lock 5m --keep-daily 7 --keep-weekly 4 --prune"
	if got := f.read("args"); got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

func TestRepoLock(t *testing.T) {
	c := &CLI{}
	ctx := context.Background()

	un1, err := c.lock(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}

	// same repo is serialised
	got := make(chan struct{})
	go func() {
		un, err := c.lock(ctx, "r1")
		if err == nil {
			close(got)
			un()
		}
	}()
	select {
	case <-got:
		t.Fatal("second lock on the same repo was not blocked")
	case <-time.After(50 * time.Millisecond):
	}

	// a different repo is independent
	tctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	un2, err := c.lock(tctx, "r2")
	if err != nil {
		t.Fatalf("different repo must not block: %v", err)
	}
	un2()

	// a cancelled context aborts the wait
	cctx, ccancel := context.WithCancel(ctx)
	ccancel()
	if _, err := c.lock(cctx, "r1"); err == nil {
		t.Fatal("expected context error")
	}

	un1()
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("waiter was not released")
	}
}

func TestBackupParsesSummary(t *testing.T) {
	f := newFake(t)
	f.set("backup", "out", `{"message_type":"status","percent_done":0.5}
{"message_type":"error","error":{"message":"read denied"},"during":"archival","item":"x.sql"}
{"message_type":"summary","files_new":2,"files_changed":1,"files_unmodified":4,"data_added":1234,"total_bytes_processed":99,"snapshot_id":"abc123de"}`)
	var lg bytes.Buffer
	snap, err := f.cli.Backup(context.Background(), testCfg(), "t", "h", t.TempDir(), []string{"a.sql"}, &lg)
	if err != nil {
		t.Fatal(err)
	}
	if snap == nil || snap.ID != "abc123de" || snap.FilesAdded != 2 || snap.FilesChanged != 1 || snap.BytesAdded != 1234 {
		t.Fatalf("snapshot = %+v", snap)
	}
	out := lg.String()
	if strings.Contains(out, "percent_done") || strings.Contains(out, "message_type") {
		t.Errorf("raw JSON leaked into the log:\n%s", out)
	}
	for _, want := range []string{"[restic] error: x.sql: read denied", "[restic] snapshot abc123de saved: 2 new, 1 changed, 4 unmodified files, 1234 bytes added"} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q:\n%s", want, out)
		}
	}
}

func TestBackupWithoutSummary(t *testing.T) {
	f := newFake(t)
	f.set("backup", "out", "plain text, no json")
	snap, err := f.cli.Backup(context.Background(), testCfg(), "t", "h", t.TempDir(), []string{"a.sql"}, io.Discard)
	if err != nil || snap != nil {
		t.Fatalf("snap=%+v err=%v", snap, err)
	}
}

func TestLockedRepositoryError(t *testing.T) {
	for _, tc := range []struct{ name, exit, stderr string }{
		{"exit code", "11", "Fatal: unable to create lock in backend: repository is already locked by PID 7"},
		{"message only", "1", "Fatal: repository is already locked exclusively by PID 7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			f.set("backup", "exit", tc.exit)
			f.set("backup", "err", tc.stderr)
			_, err := f.cli.Backup(context.Background(), testCfg(), "t", "h", t.TempDir(), []string{"a.sql"}, io.Discard)
			var ee *ExitError
			if !errors.As(err, &ee) || !ee.Locked() {
				t.Fatalf("want a lock ExitError, got %v", err)
			}
			if !strings.Contains(err.Error(), "locked by another process, gave up after waiting 5m") {
				t.Fatalf("unclear error: %v", err)
			}
		})
	}
}

func TestCheckWaitsForLock(t *testing.T) {
	f := newFake(t)
	if _, err := f.cli.Check(context.Background(), testCfg()); err != nil {
		t.Fatal(err)
	}
	if got := f.read("args"); got != "cat config --retry-lock 5m" {
		t.Fatalf("args = %q", got)
	}
}
