package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/restic"
)

type memActs struct {
	domain.ActivityRepository // unimplemented methods panic if called
	mu                        sync.Mutex
	m                         map[uuid.UUID]domain.Activity
}

func (a *memActs) Create(_ context.Context, x *domain.Activity) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.m[x.ID] = *x
	return nil
}

func (a *memActs) Finish(_ context.Context, id uuid.UUID, st domain.Status, at time.Time, msg string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	x := a.m[id]
	x.Status, x.FinishedAt, x.Error = st, &at, msg
	a.m[id] = x
	return nil
}

func (a *memActs) Get(_ context.Context, id uuid.UUID) (*domain.Activity, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	x, ok := a.m[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &x, nil
}

type catalog map[string]domain.BackupConfig

func (c catalog) Get(n string) (domain.BackupConfig, bool) { b, ok := c[n]; return b, ok }
func (c catalog) List() []domain.BackupConfig {
	var o []domain.BackupConfig
	for _, b := range c {
		o = append(o, b)
	}
	return o
}

type fakeExec func(ctx context.Context, s domain.DumpSpec, out, logw io.Writer) error

func (f fakeExec) Dump(ctx context.Context, s domain.DumpSpec, out, logw io.Writer) error {
	return f(ctx, s, out, logw)
}

type fakeNotifier struct {
	mu    sync.Mutex
	calls []domain.NotifyEvent
}

func (n *fakeNotifier) NotifyFailure(_ context.Context, e domain.NotifyEvent) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, e)
	return errors.New("telegram down") // must not affect the result
}

func okDump(_ context.Context, _ domain.DumpSpec, out, _ io.Writer) error {
	_, err := out.Write([]byte("SELECT 1;\n"))
	return err
}

func setup(t *testing.T, ex fakeExec, roles bool) (*Runner, *memActs, *fakeNotifier) {
	t.Helper()
	acts := &memActs{m: map[uuid.UUID]domain.Activity{}}
	n := &fakeNotifier{}
	cat := catalog{
		"a": {Name: "a", Container: "c", Database: "app", Host: "localhost", Username: "postgres", Port: 5432, ResticTag: "a", Roles: roles},
		"b": {Name: "b", Container: "c", Database: "other", Host: "localhost", Username: "postgres", Port: 5432, ResticTag: "b"},
	}
	r := NewRunner(Deps{
		Catalog: cat, Global: domain.GlobalConfig{TempPath: t.TempDir(), LogPath: t.TempDir()},
		Activities: acts, Exec: ex, Restic: restic.Noop{}, Notifier: n,
	})
	return r, acts, n
}

func TestSuccess(t *testing.T) {
	r, acts, n := setup(t, okDump, true)
	act, err := r.Run(context.Background(), "a", domain.TriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	r.wg.Wait()
	got, _ := acts.Get(context.Background(), act.ID)
	if got.Status != domain.StatusSuccess || got.FinishedAt == nil || got.Error != "" {
		t.Fatalf("unexpected: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(r.Global.TempPath, "pgkeeper", "a")); !os.IsNotExist(err) {
		t.Fatal("temp dir not cleaned")
	}
	b, _ := os.ReadFile(act.LogPath)
	if !strings.Contains(string(b), "phase dump: done") || !strings.Contains(string(b), "phase roles: done") {
		t.Fatalf("log: %s", b)
	}
	if len(n.calls) != 0 {
		t.Fatal("no notification expected on success")
	}
}

func TestLogsBackupLifecycle(t *testing.T) {
	r, _, _ := setup(t, okDump, false)
	var logs bytes.Buffer
	r.Logger = slog.New(slog.NewJSONHandler(&logs, nil))

	act, err := r.Run(context.Background(), "a", domain.TriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	r.wg.Wait()

	for _, want := range []string{
		"backup run started",
		"backup phase started",
		"backup phase completed",
		"backup run completed",
		act.ID.String(),
		"\"trigger\":\"manual\"",
		"\"phase\":\"dump\"",
	} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("logs missing %q:\n%s", want, logs.String())
		}
	}
}

func TestDumpFailureNotifies(t *testing.T) {
	r, acts, n := setup(t, func(context.Context, domain.DumpSpec, io.Writer, io.Writer) error {
		return errors.New("boom")
	}, false)
	act, _ := r.Run(context.Background(), "a", domain.TriggerSchedule)
	r.wg.Wait()
	got, _ := acts.Get(context.Background(), act.ID)
	if got.Status != domain.StatusError || got.Error != "dump: boom" {
		t.Fatalf("unexpected: %+v", got)
	}
	if len(n.calls) != 1 || n.calls[0].LogTail == "" {
		t.Fatalf("notifier calls: %+v", n.calls)
	}
}

func TestEmptyDumpFails(t *testing.T) {
	r, acts, _ := setup(t, func(context.Context, domain.DumpSpec, io.Writer, io.Writer) error { return nil }, false)
	act, _ := r.Run(context.Background(), "a", domain.TriggerManual)
	r.wg.Wait()
	if got, _ := acts.Get(context.Background(), act.ID); got.Error != "dump: output file is empty" {
		t.Fatalf("got %q", got.Error)
	}
}

func TestRolesFailureFailsRun(t *testing.T) {
	r, acts, _ := setup(t, func(ctx context.Context, s domain.DumpSpec, out, w io.Writer) error {
		if s.Kind == domain.DumpRoles {
			return errors.New("no roles")
		}
		return okDump(ctx, s, out, w)
	}, true)
	act, _ := r.Run(context.Background(), "a", domain.TriggerManual)
	r.wg.Wait()
	if got, _ := acts.Get(context.Background(), act.ID); got.Status != domain.StatusError || got.Error != "roles: no roles" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestConcurrency(t *testing.T) {
	started, release := make(chan struct{}, 2), make(chan struct{})
	r, _, _ := setup(t, func(ctx context.Context, s domain.DumpSpec, out, w io.Writer) error {
		started <- struct{}{}
		<-release
		return okDump(ctx, s, out, w)
	}, false)

	if _, err := r.Run(context.Background(), "a", domain.TriggerManual); err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := r.Run(context.Background(), "a", domain.TriggerManual); !errors.Is(err, domain.ErrBackupRunning) {
		t.Fatalf("want ErrBackupRunning, got %v", err)
	}
	if _, err := r.Run(context.Background(), "b", domain.TriggerManual); err != nil {
		t.Fatalf("different backup must run in parallel: %v", err)
	}
	<-started
	close(release)
	r.wg.Wait()
	if len(r.Running()) != 0 {
		t.Fatal("jobs not released")
	}
	if _, err := r.Run(context.Background(), "nope", domain.TriggerManual); !errors.Is(err, domain.ErrBackupUnknown) {
		t.Fatalf("want ErrBackupUnknown, got %v", err)
	}
}

func TestShutdownCancelsAndPersists(t *testing.T) {
	started := make(chan struct{})
	r, acts, _ := setup(t, func(ctx context.Context, _ domain.DumpSpec, _, _ io.Writer) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}, false)
	act, _ := r.Run(context.Background(), "a", domain.TriggerManual)
	<-started

	sctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = r.Shutdown(sctx)

	got, _ := acts.Get(context.Background(), act.ID)
	if got.Status != domain.StatusError || !strings.HasPrefix(got.Error, "interrupted:") {
		t.Fatalf("unexpected: %+v", got)
	}
	if _, err := r.Run(context.Background(), "a", domain.TriggerManual); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("want ErrShuttingDown, got %v", err)
	}
}
