// Package maintenance runs the scheduled restic prune and check tasks over
// every distinct repository in use (the global [restic] repository plus any
// per-backup override).
package maintenance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/restic"
)

var ErrShuttingDown = errors.New("maintenance is shutting down")

// Source returns the current backups and maintenance settings. It is called
// for every run, so a reloaded configuration applies to the next one.
type Source func() ([]domain.BackupConfig, domain.MaintenanceConfig)

type Deps struct {
	Source   Source
	Restic   domain.ResticMaintainer
	Locker   domain.RepoLocker // shared with the backup runner
	Notifier domain.Notifier
	Logger   *slog.Logger
	Clock    func() time.Time // default time.Now
}

type Runner struct {
	Deps
	mu       sync.Mutex // guards running and stopping
	running  map[domain.MaintenanceKind]bool
	stopping bool
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
}

var _ domain.MaintenanceRunner = (*Runner)(nil)

func New(d Deps) *Runner {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Clock == nil {
		d.Clock = time.Now
	}
	if d.Notifier == nil {
		d.Notifier = noNotify{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{Deps: d, running: map[domain.MaintenanceKind]bool{}, ctx: ctx, cancel: cancel}
}

type noNotify struct{}

func (noNotify) NotifyFailure(context.Context, domain.NotifyEvent) error { return nil }

// Repos returns every distinct repository, global one first and then the
// backups in the given order. Repositories are identified by their exact
// repository string, the same key the backup gate and the restic client use
// for locking, so one repository is never handled twice per run (the first
// definition, and therefore its password, wins).
func Repos(mc domain.MaintenanceConfig, backups []domain.BackupConfig) []domain.ResticConfig {
	seen := map[string]bool{}
	var out []domain.ResticConfig
	add := func(c domain.ResticConfig) {
		if c.Repository == "" || seen[c.Repository] {
			return
		}
		seen[c.Repository] = true
		out = append(out, c)
	}
	if mc.Repo != nil {
		add(*mc.Repo)
	}
	for _, b := range backups {
		add(b.Restic)
	}
	return out
}

// Trigger starts the task in the background. It returns
// domain.ErrMaintenanceRunning when the previous run of the same task has not
// finished (the tick is skipped, never queued).
func (r *Runner) Trigger(kind domain.MaintenanceKind) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping {
		return ErrShuttingDown
	}
	if r.running[kind] {
		return domain.ErrMaintenanceRunning
	}
	r.running[kind] = true
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer func() {
			r.mu.Lock()
			delete(r.running, kind)
			r.mu.Unlock()
		}()
		if err := r.Run(r.ctx, kind); err != nil {
			r.Logger.Error("restic maintenance finished with errors", "task", kind, "err", err)
		}
	}()
	return nil
}

// Run executes the task synchronously over all repositories, one at a time. A
// failing repository does not stop the others; the errors are joined.
func (r *Runner) Run(ctx context.Context, kind domain.MaintenanceKind) error {
	backups, mc := r.Source()
	repos := Repos(mc, backups)
	if len(repos) == 0 {
		r.Logger.Info("restic maintenance: no repositories configured", "task", kind)
		return nil
	}
	r.Logger.Info("restic maintenance started", "task", kind, "repositories", len(repos))
	var errs []error
	for _, repo := range repos {
		if ctx.Err() != nil {
			errs = append(errs, fmt.Errorf("interrupted: %w", ctx.Err()))
			break
		}
		if err := r.runRepo(ctx, kind, repo, mc); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", restic.RedactRepo(repo.Repository), err))
		}
	}
	return errors.Join(errs...)
}

func (r *Runner) runRepo(ctx context.Context, kind domain.MaintenanceKind, repo domain.ResticConfig, mc domain.MaintenanceConfig) error {
	name := restic.RedactRepo(repo.Repository)
	lg := &taskLog{log: r.Logger.With("task", kind, "repository", name)}
	owner := "maintenance:" + string(kind)

	// Same gate as the backups: prune/check take turns with the restic phase
	// of every run using this repository (FIFO), so nobody fails on a lock
	// held by a sibling in this process.
	release, err := r.Locker.AcquireRepo(ctx, repo.Repository, owner, func(holder string, ahead int) {
		lg.Logf("INFO", "repository is in use by %q (%d ahead in queue), waiting", holder, ahead)
	})
	if err != nil {
		return r.fail(ctx, kind, name, r.Clock(), lg, fmt.Errorf("waiting for repository: %w", err))
	}
	defer release()

	start := r.Clock()
	lg.Logf("INFO", "%s started", kind)
	switch kind {
	case domain.MaintenancePrune:
		err = r.Restic.Prune(ctx, repo, lg)
	case domain.MaintenanceCheck:
		err = r.Restic.CheckData(ctx, repo, mc.ReadPercent(), lg)
	default:
		err = fmt.Errorf("unknown maintenance task %q", kind)
	}
	switch {
	case err == nil:
		lg.Logf("INFO", "%s finished elapsed=%s", kind, r.Clock().Sub(start).Round(time.Second))
		return nil
	case errors.Is(err, domain.ErrRepoMissing):
		// Nothing to maintain yet: the first backup initialises it.
		lg.Logf("WARN", "%s skipped: repository does not exist yet", kind)
		return nil
	default:
		return r.fail(ctx, kind, name, start, lg, err)
	}
}

// fail logs the failure and notifies; the error is returned unchanged.
func (r *Runner) fail(ctx context.Context, kind domain.MaintenanceKind, repo string, start time.Time, lg *taskLog, err error) error {
	end := r.Clock().UTC()
	lg.Logf("ERROR", "%s failed: %v", kind, err)
	if ctx.Err() != nil {
		return err // shutting down: no alert for an interrupted run
	}
	nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	nerr := r.Notifier.NotifyFailure(nctx, domain.NotifyEvent{
		Title:    "Restic " + string(kind),
		Backup:   domain.BackupConfig{Name: repo},
		Activity: domain.Activity{StartedAt: start.UTC(), FinishedAt: &end, Error: err.Error()},
		LogTail:  lg.Tail(),
	})
	if nerr != nil {
		r.Logger.Warn("notify maintenance failure", "task", kind, "repository", repo, "err", nerr)
	}
	return err
}

// Shutdown cancels running tasks (restic is interrupted and drops its repository
// lock) and waits for them until ctx expires.
func (r *Runner) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	r.stopping = true
	r.mu.Unlock()
	r.cancel()
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("maintenance shutdown: %w", ctx.Err())
	}
}

// taskLog sends restic output to the application log and keeps the tail for
// failure notifications. It satisfies the Logf contract of restic.CLI.
type taskLog struct {
	log  *slog.Logger
	mu   sync.Mutex
	tail []string
}

var _ io.Writer = (*taskLog)(nil)

func (l *taskLog) Write(p []byte) (int, error) {
	l.Logf("INFO", "%s", strings.TrimSuffix(string(p), "\n"))
	return len(p), nil
}

func (l *taskLog) Logf(level, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	switch level {
	case "ERROR":
		l.log.Error(msg)
	case "WARN":
		l.log.Warn(msg)
	default:
		l.log.Info(msg)
	}
	l.mu.Lock()
	l.tail = append(l.tail, level+" "+msg)
	if len(l.tail) > 15 {
		l.tail = l.tail[len(l.tail)-15:]
	}
	l.mu.Unlock()
}

func (l *taskLog) Tail() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.tail, "\n")
}
