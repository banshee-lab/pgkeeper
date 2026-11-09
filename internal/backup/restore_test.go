package backup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

type fakeRestoreExec struct {
	mu      sync.Mutex
	calls   []string
	scripts []string
	targets []domain.SQLTarget
	psqlErr func(n int, script string) error // n is the 1-based psql call number
	block   chan struct{}                    // WaitReady waits for it when set
}

func (f *fakeRestoreExec) rec(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakeRestoreExec) snapshot() ([]string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...), append([]string(nil), f.scripts...)
}

func (f *fakeRestoreExec) StartEphemeral(_ context.Context, _, image string, _ io.Writer) error {
	f.rec("start:" + image)
	return nil
}

func (f *fakeRestoreExec) RemoveContainer(_ context.Context, _ string, _ io.Writer) error {
	f.rec("remove")
	return nil
}

func (f *fakeRestoreExec) WaitReady(ctx context.Context, _ domain.SQLTarget, _ time.Duration, _ io.Writer) error {
	f.rec("ready")
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (f *fakeRestoreExec) Psql(_ context.Context, t domain.SQLTarget, in io.Reader, _ io.Writer) error {
	b, _ := io.ReadAll(in)
	f.mu.Lock()
	f.scripts = append(f.scripts, string(b))
	f.targets = append(f.targets, t)
	n := len(f.scripts)
	f.mu.Unlock()
	if strings.HasPrefix(t.Container, "pgkeeper-restore-") {
		f.rec("psql:ephemeral:" + t.Database)
	} else {
		f.rec("psql:" + t.Container + ":" + t.Database)
	}
	if f.psqlErr != nil {
		return f.psqlErr(n, string(b))
	}
	return nil
}

type fakeInspector struct{ snaps []domain.RepositorySnapshot }

func (f fakeInspector) RepositoryStats(context.Context, domain.ResticConfig) (int64, int, error) {
	return 0, 0, nil
}

func (f fakeInspector) RepositorySnapshots(context.Context, domain.ResticConfig) ([]domain.RepositorySnapshot, error) {
	return f.snaps, nil
}

func (f fakeInspector) RepositorySnapshotStats(context.Context, domain.ResticConfig, string) (domain.RepositorySnapshotStats, error) {
	return domain.RepositorySnapshotStats{}, nil
}

// RepositoryTree serves a tree whose real location differs from the snapshot
// "paths" hint, like restic does in practice.
func (f fakeInspector) RepositoryTree(_ context.Context, _ domain.ResticConfig, _, p string) ([]domain.RepositoryNode, error) {
	dir := func(path string) domain.RepositoryNode { return domain.RepositoryNode{Path: path, Type: "dir"} }
	file := func(path string) domain.RepositoryNode { return domain.RepositoryNode{Path: path, Type: "file", Size: 1} }
	switch p {
	case "":
		return []domain.RepositoryNode{dir("/data")}, nil
	case "/data":
		return []domain.RepositoryNode{dir("/data/a")}, nil
	case "/data/a":
		return []domain.RepositoryNode{file("/data/a/app.sql"), file("/data/a/app_roles.sql")}, nil
	}
	return nil, nil
}

func (f fakeInspector) DownloadSnapshotFile(_ context.Context, _ domain.ResticConfig, _, p string, dst io.Writer) error {
	if !strings.HasPrefix(p, "/data/a/") {
		return errors.New("path " + p + " not found in snapshot")
	}
	body := "CREATE TABLE t (id int);\n"
	if strings.HasSuffix(p, "_roles.sql") {
		body = "CREATE ROLE app_user;\nALTER ROLE app_user WITH LOGIN;\n"
	}
	_, err := io.WriteString(dst, body)
	return err
}

func restoreSetup(t *testing.T, ex *fakeRestoreExec) (*Runner, *memActs, *fakeNotifier) {
	t.Helper()
	r, acts, n := setup(t, okDump, true)
	c := r.Catalog.(catalog)
	cfg := c["a"]
	cfg.RestoreImage = "postgres:18"
	c["a"] = cfg
	paths := []string{"/tmp/pgkeeper/a/app.sql", "/tmp/pgkeeper/a/app_roles.sql"}
	now := time.Now().UTC()
	r.RestoreExec = ex
	r.Inspector = fakeInspector{snaps: []domain.RepositorySnapshot{
		{ID: "aaaaaaaa11111111", ShortID: "aaaaaaaa", Time: now.Add(-time.Hour), Hostname: "a", Tags: []string{"a"}, Paths: paths},
		{ID: "bbbbbbbb22222222", ShortID: "bbbbbbbb", Time: now, Hostname: "a", Tags: []string{"a"}, Paths: paths},
		{ID: "cccccccc33333333", ShortID: "cccccccc", Time: now.Add(time.Hour), Hostname: "other", Tags: []string{"a"}, Paths: paths},
	}}
	return r, acts, n
}

func TestTestRestoreSucceedsAndRemovesContainer(t *testing.T) {
	ex := &fakeRestoreExec{}
	r, acts, n := restoreSetup(t, ex)

	act, err := r.TestRestore(context.Background(), "a", domain.TriggerSchedule)
	if err != nil {
		t.Fatal(err)
	}
	r.wg.Wait()

	got, _ := acts.Get(context.Background(), act.ID)
	if got.Status != domain.StatusSuccess || got.Kind != domain.KindTestRestore || got.TriggeredBy != domain.TriggerSchedule {
		t.Fatalf("unexpected: %+v", got)
	}
	calls, scripts := ex.snapshot()
	want := []string{"start:postgres:18", "ready", "psql:ephemeral:postgres", "psql:ephemeral:postgres", "psql:ephemeral:app", "remove"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if !strings.Contains(scripts[0], "to_regrole('app_user')") {
		t.Errorf("roles script not prepared: %s", scripts[0])
	}
	if scripts[1] != "CREATE DATABASE \"app\";\n" {
		t.Errorf("create script = %q", scripts[1])
	}
	if !strings.Contains(scripts[2], "CREATE TABLE t") {
		t.Errorf("dump script = %q", scripts[2])
	}
	if len(n.calls) != 0 {
		t.Fatal("no notification expected on success")
	}
	if entries, _ := os.ReadDir(filepath.Join(r.Global.TempPath, "pgkeeper", "restore")); len(entries) != 0 {
		t.Fatalf("temp dir not cleaned: %v", entries)
	}
	b, _ := os.ReadFile(act.LogPath)
	if !strings.Contains(string(b), "phase restore: done") || !strings.Contains(string(b), "roles that already exist") {
		t.Fatalf("log: %s", b)
	}
}

func TestTestRestoreFailureStillRemovesContainerAndNotifies(t *testing.T) {
	ex := &fakeRestoreExec{psqlErr: func(n int, _ string) error {
		if n == 3 {
			return errors.New("boom")
		}
		return nil
	}}
	r, acts, n := restoreSetup(t, ex)

	act, err := r.TestRestore(context.Background(), "a", domain.TriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	r.wg.Wait()

	got, _ := acts.Get(context.Background(), act.ID)
	if got.Status != domain.StatusError || !strings.Contains(got.Error, "boom") {
		t.Fatalf("unexpected: %+v", got)
	}
	calls, _ := ex.snapshot()
	if calls[len(calls)-1] != "remove" {
		t.Fatalf("container not removed: %v", calls)
	}
	if len(n.calls) != 1 || n.calls[0].Activity.Kind != domain.KindTestRestore {
		t.Fatalf("notifications: %+v", n.calls)
	}
}

func TestTestRestoreNeedsImage(t *testing.T) {
	r, _, _ := restoreSetup(t, &fakeRestoreExec{})
	c := r.Catalog.(catalog)
	cfg := c["a"]
	cfg.RestoreImage = ""
	c["a"] = cfg
	if _, err := r.TestRestore(context.Background(), "a", domain.TriggerManual); !errors.Is(err, domain.ErrRestoreInvalid) {
		t.Fatalf("want ErrRestoreInvalid, got %v", err)
	}
	if _, err := r.TestRestore(context.Background(), "nope", domain.TriggerManual); !errors.Is(err, domain.ErrBackupUnknown) {
		t.Fatalf("want ErrBackupUnknown, got %v", err)
	}
}

func TestRestoreToContainerRecreatesTargetDatabase(t *testing.T) {
	ex := &fakeRestoreExec{}
	r, acts, _ := restoreSetup(t, ex)

	act, err := r.RestoreToContainer(context.Background(), "a", domain.TriggerManual,
		domain.RestoreRequest{SnapshotID: "aaaaaaaa", Container: "other-pg", Database: "copy"})
	if err != nil {
		t.Fatal(err)
	}
	r.wg.Wait()

	got, _ := acts.Get(context.Background(), act.ID)
	if got.Status != domain.StatusSuccess || got.Kind != domain.KindRestore {
		t.Fatalf("unexpected: %+v", got)
	}
	calls, scripts := ex.snapshot()
	if strings.Join(calls, ",") != "psql:other-pg:postgres,psql:other-pg:copy" {
		t.Fatalf("calls = %v", calls)
	}
	if !strings.Contains(scripts[0], `DROP DATABASE IF EXISTS "copy";`) || !strings.Contains(scripts[0], `CREATE DATABASE "copy";`) {
		t.Errorf("prepare script = %q", scripts[0])
	}
}

func TestRestoreToContainerWithRolesAndValidation(t *testing.T) {
	ex := &fakeRestoreExec{}
	r, acts, _ := restoreSetup(t, ex)

	for _, req := range []domain.RestoreRequest{
		{SnapshotID: "not-hex"},
		{SnapshotID: "aaaaaaaa", Database: "bad;name"},
		{SnapshotID: "aaaaaaaa", Container: "bad name"},
	} {
		if _, err := r.RestoreToContainer(context.Background(), "a", domain.TriggerManual, req); !errors.Is(err, domain.ErrRestoreInvalid) {
			t.Errorf("%+v: want ErrRestoreInvalid, got %v", req, err)
		}
	}

	act, err := r.RestoreToContainer(context.Background(), "a", domain.TriggerManual,
		domain.RestoreRequest{SnapshotID: "bbbbbbbb22222222", IncludeRoles: true})
	if err != nil {
		t.Fatal(err)
	}
	r.wg.Wait()
	if got, _ := acts.Get(context.Background(), act.ID); got.Status != domain.StatusSuccess {
		t.Fatalf("unexpected: %+v", got)
	}
	calls, _ := ex.snapshot()
	if len(calls) != 3 || calls[0] != "psql:c:postgres" { // roles first, on the project's own container
		t.Fatalf("calls = %v", calls)
	}

	// A snapshot of another host is not restorable.
	act, err = r.RestoreToContainer(context.Background(), "a", domain.TriggerManual, domain.RestoreRequest{SnapshotID: "cccccccc"})
	if err != nil {
		t.Fatal(err)
	}
	r.wg.Wait()
	if got, _ := acts.Get(context.Background(), act.ID); got.Status != domain.StatusError || !strings.Contains(got.Error, "does not belong") {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestRestoreSnapshotsNewestFirst(t *testing.T) {
	r, _, _ := restoreSetup(t, &fakeRestoreExec{})
	got, err := r.RestoreSnapshots(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ShortID != "bbbbbbbb" || got[1].ShortID != "aaaaaaaa" || !got[0].HasRoles {
		t.Fatalf("got %+v", got)
	}
}

func TestRestoreBlocksOtherRunsOfTheProject(t *testing.T) {
	ex := &fakeRestoreExec{block: make(chan struct{})}
	r, _, _ := restoreSetup(t, ex)

	if _, err := r.TestRestore(context.Background(), "a", domain.TriggerManual); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if calls, _ := ex.snapshot(); len(calls) >= 2 { // start + ready: the restore is in flight
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("restore did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := r.TestRestore(context.Background(), "a", domain.TriggerManual); !errors.Is(err, domain.ErrRestoreRunning) {
		t.Fatalf("want ErrRestoreRunning, got %v", err)
	}
	if _, err := r.Run(context.Background(), "a", domain.TriggerManual); !errors.Is(err, domain.ErrRestoreRunning) {
		t.Fatalf("backup during restore: want ErrRestoreRunning, got %v", err)
	}
	close(ex.block)
	r.wg.Wait()
}

func withToolsImage(r *Runner, image string, conn *domain.PGConn) {
	c := r.Catalog.(catalog)
	cfg := c["a"]
	cfg.ToolsImage = image
	cfg.Conn = conn
	c["a"] = cfg
}

func TestRestoreToDatabaseURL(t *testing.T) {
	ex := &fakeRestoreExec{}
	r, acts, _ := restoreSetup(t, ex)
	withToolsImage(r, "postgres:18", nil)

	act, err := r.RestoreToContainer(context.Background(), "a", domain.TriggerManual, domain.RestoreRequest{
		SnapshotID: "bbbbbbbb22222222", IncludeRoles: true,
		DatabaseURL: "postgresql://u:s3cret@db.example.com:6543/target?sslmode=require",
	})
	if err != nil {
		t.Fatal(err)
	}
	r.wg.Wait()

	got, _ := acts.Get(context.Background(), act.ID)
	if got.Status != domain.StatusSuccess || got.Kind != domain.KindRestore {
		t.Fatalf("unexpected: %+v", got)
	}
	calls, scripts := ex.snapshot()
	want := []string{"psql::postgres", "psql::postgres", "psql::target"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if !strings.Contains(scripts[1], `DROP DATABASE IF EXISTS "target"`) || !strings.Contains(scripts[1], `CREATE DATABASE "target"`) {
		t.Errorf("prepare script = %q", scripts[1])
	}
	for _, tgt := range ex.targets {
		if tgt.Conn == nil || tgt.Conn.Host != "db.example.com" || tgt.Conn.Port != 6543 || tgt.Image != "postgres:18" {
			t.Fatalf("target = %+v", tgt)
		}
	}
	b, _ := os.ReadFile(act.LogPath)
	if strings.Contains(string(b), "s3cret") {
		t.Fatalf("log leaks the password: %s", b)
	}
	if !strings.Contains(string(b), "db.example.com:6543") {
		t.Fatalf("log does not name the target: %s", b)
	}
}

func TestRestoreToDatabaseURLNeedsToolsImage(t *testing.T) {
	r, _, _ := restoreSetup(t, &fakeRestoreExec{})
	_, err := r.RestoreToContainer(context.Background(), "a", domain.TriggerManual, domain.RestoreRequest{
		SnapshotID: "bbbbbbbb22222222", DatabaseURL: "postgresql://u@db.example.com/target"})
	if !errors.Is(err, domain.ErrRestoreInvalid) || !strings.Contains(err.Error(), "tools-image") {
		t.Fatalf("want ErrRestoreInvalid about tools-image, got %v", err)
	}
}

func TestRestoreToDatabaseURLRejectsBadRequests(t *testing.T) {
	r, _, _ := restoreSetup(t, &fakeRestoreExec{})
	withToolsImage(r, "postgres:18", nil)
	for name, req := range map[string]domain.RestoreRequest{
		"container and url": {SnapshotID: "bbbbbbbb22222222", Container: "pg", DatabaseURL: "postgresql://db/x"},
		"bad url":           {SnapshotID: "bbbbbbbb22222222", DatabaseURL: "mysql://u:s3cret@db/x"},
		"bad database":      {SnapshotID: "bbbbbbbb22222222", DatabaseURL: "postgresql://db/x", Database: "x\";drop"},
	} {
		_, err := r.RestoreToContainer(context.Background(), "a", domain.TriggerManual, req)
		if !errors.Is(err, domain.ErrRestoreInvalid) {
			t.Errorf("%s: want ErrRestoreInvalid, got %v", name, err)
		} else if strings.Contains(err.Error(), "s3cret") {
			t.Errorf("%s: error leaks the password: %v", name, err)
		}
	}
}

func TestRestoreDefaultsToSourceURL(t *testing.T) {
	ex := &fakeRestoreExec{}
	r, acts, _ := restoreSetup(t, ex)
	src := domain.PGConn{Host: "src.example.com", Port: 5432, User: "backup", Database: "app"}
	withToolsImage(r, "postgres:18", &src)

	act, err := r.RestoreToContainer(context.Background(), "a", domain.TriggerManual, domain.RestoreRequest{SnapshotID: "bbbbbbbb22222222"})
	if err != nil {
		t.Fatal(err)
	}
	r.wg.Wait()
	if got, _ := acts.Get(context.Background(), act.ID); got.Status != domain.StatusSuccess {
		t.Fatalf("unexpected: %+v", got)
	}
	if len(ex.targets) == 0 || ex.targets[0].Conn == nil || ex.targets[0].Conn.Host != "src.example.com" {
		t.Fatalf("targets = %+v", ex.targets)
	}
}
