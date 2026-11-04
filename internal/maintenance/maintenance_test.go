package maintenance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type call struct {
	kind    domain.MaintenanceKind
	repo    string
	percent int
}

type fakeRestic struct {
	mu    sync.Mutex
	calls []call
	errs  map[string]error // by repository
	block chan struct{}    // when set, calls wait for it or ctx
}

func (f *fakeRestic) do(ctx context.Context, c call) error {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	err := f.errs[c.repo]
	f.mu.Unlock()
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (f *fakeRestic) Prune(ctx context.Context, cfg domain.ResticConfig, _ io.Writer) error {
	return f.do(ctx, call{kind: domain.MaintenancePrune, repo: cfg.Repository})
}

func (f *fakeRestic) CheckData(ctx context.Context, cfg domain.ResticConfig, pct int, _ io.Writer) error {
	return f.do(ctx, call{kind: domain.MaintenanceCheck, repo: cfg.Repository, percent: pct})
}

func (f *fakeRestic) got() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls...)
}

type fakeLocker struct {
	mu     sync.Mutex
	events []string
}

func (l *fakeLocker) AcquireRepo(_ context.Context, repo, owner string, _ func(string, int)) (func(), error) {
	l.mu.Lock()
	l.events = append(l.events, "acquire "+repo+" "+owner)
	l.mu.Unlock()
	return func() {
		l.mu.Lock()
		l.events = append(l.events, "release "+repo)
		l.mu.Unlock()
	}, nil
}

type fakeNotifier struct {
	mu     sync.Mutex
	events []domain.NotifyEvent
}

func (n *fakeNotifier) NotifyFailure(_ context.Context, e domain.NotifyEvent) error {
	n.mu.Lock()
	n.events = append(n.events, e)
	n.mu.Unlock()
	return nil
}

func rc(repo string) domain.ResticConfig {
	return domain.ResticConfig{Repository: repo, Password: "pw"}
}

func bk(name, repo string) domain.BackupConfig {
	return domain.BackupConfig{Name: name, Restic: rc(repo)}
}

func TestReposDedupe(t *testing.T) {
	g := rc("/global")
	mc := domain.MaintenanceConfig{Repo: &g}
	got := Repos(mc, []domain.BackupConfig{bk("a", "/global"), bk("b", "/own"), bk("c", "/own"), bk("d", "/global")})
	if len(got) != 2 || got[0].Repository != "/global" || got[1].Repository != "/own" {
		t.Fatalf("Repos = %+v", got)
	}
	if got := Repos(domain.MaintenanceConfig{}, []domain.BackupConfig{bk("a", "/x")}); len(got) != 1 {
		t.Fatalf("no global repo: %+v", got)
	}
}

func setup(mc domain.MaintenanceConfig, backups ...domain.BackupConfig) (*Runner, *fakeRestic, *fakeLocker, *fakeNotifier) {
	fr, fl, fn := &fakeRestic{errs: map[string]error{}}, &fakeLocker{}, &fakeNotifier{}
	r := New(Deps{
		Source:   func() ([]domain.BackupConfig, domain.MaintenanceConfig) { return backups, mc },
		Restic:   fr,
		Locker:   fl,
		Notifier: fn,
		Logger:   quiet,
	})
	return r, fr, fl, fn
}

func TestRunEachRepoOnceUnderGate(t *testing.T) {
	g := rc("/global")
	r, fr, fl, _ := setup(domain.MaintenanceConfig{Repo: &g, CheckReadPercent: 50},
		bk("a", "/global"), bk("b", "/own"), bk("c", "/own"))
	if err := r.Run(context.Background(), domain.MaintenanceCheck); err != nil {
		t.Fatal(err)
	}
	want := []call{{domain.MaintenanceCheck, "/global", 50}, {domain.MaintenanceCheck, "/own", 50}}
	got := fr.got()
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("calls = %+v", got)
	}
	wantEv := "acquire /global maintenance:check|release /global|acquire /own maintenance:check|release /own"
	if s := strings.Join(fl.events, "|"); s != wantEv {
		t.Fatalf("gate events = %s", s)
	}
}

func TestDefaultReadPercent(t *testing.T) {
	r, fr, _, _ := setup(domain.MaintenanceConfig{}, bk("a", "/r"))
	if err := r.Run(context.Background(), domain.MaintenanceCheck); err != nil {
		t.Fatal(err)
	}
	if got := fr.got(); got[0].percent != 80 {
		t.Fatalf("percent = %d", got[0].percent)
	}
}

func TestMissingRepoIsSkippedNotFailure(t *testing.T) {
	r, fr, _, fn := setup(domain.MaintenanceConfig{}, bk("a", "/new"))
	fr.errs["/new"] = fmt.Errorf("prune: %w", domain.ErrRepoMissing)
	if err := r.Run(context.Background(), domain.MaintenancePrune); err != nil {
		t.Fatal(err)
	}
	if len(fn.events) != 0 {
		t.Fatal("a missing repo must not alert")
	}
}

func TestFailureContinuesAndNotifiesRedacted(t *testing.T) {
	r, fr, _, fn := setup(domain.MaintenanceConfig{},
		bk("a", "rest:https://user:secret@host/r"), bk("b", "/ok"))
	fr.errs["rest:https://user:secret@host/r"] = errors.New("boom")
	err := r.Run(context.Background(), domain.MaintenancePrune)
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("want redacted error, got %v", err)
	}
	if len(fr.got()) != 2 {
		t.Fatal("second repository must still run")
	}
	if len(fn.events) != 1 {
		t.Fatalf("notifications = %d", len(fn.events))
	}
	e := fn.events[0]
	if e.Title != "Restic prune" || strings.Contains(e.Backup.Name, "secret") || e.Activity.Error == "" {
		t.Fatalf("event = %+v", e)
	}
}

func TestTriggerSkipsWhileRunningAndShutdownCancels(t *testing.T) {
	r, fr, _, _ := setup(domain.MaintenanceConfig{}, bk("a", "/r"))
	fr.block = make(chan struct{})
	if err := r.Trigger(domain.MaintenancePrune); err != nil {
		t.Fatal(err)
	}
	if err := r.Trigger(domain.MaintenancePrune); !errors.Is(err, domain.ErrMaintenanceRunning) {
		t.Fatalf("want ErrMaintenanceRunning, got %v", err)
	}
	// a different task is independent
	if err := r.Trigger(domain.MaintenanceCheck); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := r.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.Trigger(domain.MaintenancePrune); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("want ErrShuttingDown, got %v", err)
	}
}

func TestNoRepositories(t *testing.T) {
	r, fr, _, _ := setup(domain.MaintenanceConfig{})
	if err := r.Run(context.Background(), domain.MaintenancePrune); err != nil || len(fr.got()) != 0 {
		t.Fatalf("err=%v calls=%v", err, fr.got())
	}
}
