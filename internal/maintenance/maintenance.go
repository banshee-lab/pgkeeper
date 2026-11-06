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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"uuid"

	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/events"
	"github.com/banshee-lab/pgkeeper/internal/logstore"
	"github.com/banshee-lab/pgkeeper/internal/restic"
)

var ErrShuttingDown = errors.New("maintenance is shutting down")

// Source returns the current backups and maintenance settings. It is called
// for every run, so a reloaded configuration applies to the next one.
type Source func() ([]domain.BackupConfig, domain.MaintenanceConfig)

type Deps struct {
	Source     Source
	Restic     domain.ResticMaintainer
	Locker     domain.RepoLocker // shared with the backup runner
	Notifier   domain.Notifier
	Logger     *slog.Logger
	Clock      func() time.Time // default time.Now
	Activities domain.RepositoryActivityStore
	TempPath   string
	LogHub     *events.Hub[logstore.LogLine]
}

type Runner struct {
	Deps
	mu          sync.Mutex // guards running and stopping
	running     map[domain.MaintenanceKind]bool
	repoRunning map[string]bool
	stopping    bool
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
}

var _ domain.MaintenanceRunner = (*Runner)(nil)
var _ domain.RepositoryMaintenanceRunner = (*Runner)(nil)

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
	if d.LogHub == nil {
		d.LogHub = events.NewHub[logstore.LogLine]()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{Deps: d, running: map[domain.MaintenanceKind]bool{}, repoRunning: map[string]bool{}, ctx: ctx, cancel: cancel}
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
		var err error
		if r.Activities == nil {
			err = r.runRepo(ctx, kind, repo, mc)
		} else {
			err = r.runRepositoryActivity(ctx, kind, repo, mc, domain.TriggerSchedule, nil, nil)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", restic.RedactRepo(repo.Repository), err))
		}
	}
	return errors.Join(errs...)
}

func (r *Runner) TriggerRepository(id string, kind domain.MaintenanceKind) (*domain.RepositoryActivity, error) {
	if kind != domain.MaintenancePrune && kind != domain.MaintenanceCheck && kind != domain.MaintenanceUnlock {
		return nil, fmt.Errorf("unsupported repository activity %q", kind)
	}
	backups, mc := r.Source()
	var selected *domain.ResticConfig
	for _, repo := range Repos(mc, backups) {
		if domain.RepositoryID(repo.Repository) == id {
			repoCopy := repo
			selected = &repoCopy
			break
		}
	}
	if selected == nil {
		return nil, domain.ErrRepoMissing
	}
	if r.Activities == nil {
		return nil, errors.New("repository activity store unavailable")
	}
	var reservedRelease func()
	if kind == domain.MaintenanceUnlock {
		locker, ok := r.Locker.(interface {
			TryAcquireRepo(string, string) (func(), bool)
		})
		if !ok {
			return nil, errors.New("repository unlock requires a try-lock capable repository gate")
		}
		var acquired bool
		reservedRelease, acquired = locker.TryAcquireRepo(selected.Repository, "maintenance:unlock")
		if !acquired {
			return nil, domain.ErrMaintenanceRunning
		}
	}
	r.mu.Lock()
	if r.stopping {
		r.mu.Unlock()
		if reservedRelease != nil {
			reservedRelease()
		}
		return nil, ErrShuttingDown
	}
	if r.repoRunning[id] {
		r.mu.Unlock()
		if reservedRelease != nil {
			reservedRelease()
		}
		return nil, domain.ErrMaintenanceRunning
	}
	r.repoRunning[id] = true
	r.wg.Add(1)
	r.mu.Unlock()

	now := r.Clock().UTC()
	act := &domain.RepositoryActivity{
		ID: uuid.NewV7().String(), RepositoryID: id, Kind: kind,
		TriggeredBy: domain.TriggerManual, Status: domain.StatusRunning,
		StartedAt: now, LogPath: filepath.Join(r.TempPath, "pgkeeper", "repository-activities", id+"-"+uuid.NewV7().String()+".log"),
	}
	if err := r.Activities.CreateRepositoryActivity(context.Background(), act); err != nil {
		r.mu.Lock()
		delete(r.repoRunning, id)
		r.mu.Unlock()
		if reservedRelease != nil {
			reservedRelease()
		}
		r.wg.Done()
		return nil, err
	}
	result := *act
	go func() {
		defer r.wg.Done()
		defer func() {
			r.mu.Lock()
			delete(r.repoRunning, id)
			r.mu.Unlock()
		}()
		if err := r.runRepositoryActivity(r.ctx, kind, *selected, mc, domain.TriggerManual, act, reservedRelease); err != nil {
			r.Logger.Error("repository maintenance failed", "repository", id, "kind", kind, "err", err)
		}
	}()
	return &result, nil
}

func (r *Runner) RepositoryRunning(id string) bool {
	if runner, ok := r.Locker.(interface{ Running() []domain.Job }); ok {
		backups, _ := r.Source()
		repositories := make(map[string]string, len(backups))
		for _, backup := range backups {
			repositories[backup.Name] = domain.RepositoryID(backup.Restic.Repository)
		}
		for _, job := range runner.Running() {
			if repositories[job.BackupName] == id {
				return true
			}
		}
	}
	if r.Activities != nil {
		items, err := r.Activities.CurrentRepositoryActivities(context.Background(), id)
		if err == nil && len(items) > 0 {
			return true
		}
		if err != nil {
			return true
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.repoRunning[id]
}

func (r *Runner) runRepositoryActivity(ctx context.Context, kind domain.MaintenanceKind, repo domain.ResticConfig, mc domain.MaintenanceConfig, trigger domain.Trigger, act *domain.RepositoryActivity, reservedRelease func()) error {
	if act == nil {
		id := domain.RepositoryID(repo.Repository)
		now := r.Clock().UTC()
		act = &domain.RepositoryActivity{ID: uuid.NewV7().String(), RepositoryID: id, Kind: kind, TriggeredBy: trigger, Status: domain.StatusRunning, StartedAt: now, LogPath: filepath.Join(r.TempPath, "pgkeeper", "repository-activities", id+"-"+uuid.NewV7().String()+".log")}
		if r.Activities != nil {
			if err := r.Activities.CreateRepositoryActivity(context.Background(), act); err != nil {
				return err
			}
		}
	}
	logID, err := uuid.Parse(act.ID)
	if err != nil {
		if reservedRelease != nil {
			reservedRelease()
		}
		return err
	}
	logw, err := logstore.Open(act.LogPath, logID, r.LogHub)
	if err != nil {
		if reservedRelease != nil {
			reservedRelease()
		}
		finished := r.Clock().UTC()
		act.Status, act.FinishedAt, act.Error = domain.StatusError, &finished, err.Error()
		if r.Activities != nil {
			_ = r.Activities.FinishRepositoryActivity(context.Background(), act.ID, act.Status, finished, act.Error)
		}
		return err
	}
	logw.Logf("INFO", "%s started", kind)
	release := reservedRelease
	if release == nil {
		release, err = r.Locker.AcquireRepo(ctx, repo.Repository, "maintenance:"+string(kind), func(holder string, ahead int) {
			logw.Logf("INFO", "repository busy by %q; waiting (%d ahead)", holder, ahead)
		})
	}
	if err == nil {
		defer release()
		switch kind {
		case domain.MaintenancePrune:
			err = r.Restic.Prune(ctx, repo, logw)
		case domain.MaintenanceCheck:
			err = r.Restic.CheckData(ctx, repo, mc.ReadPercent(), logw)
		case domain.MaintenanceUnlock:
			err = r.Restic.Unlock(ctx, repo, logw)
		}
		release()
	}
	if errors.Is(err, domain.ErrRepoMissing) {
		logw.Logf("WARN", "%s skipped: repository does not exist yet", kind)
		err = nil
	}
	status := domain.StatusSuccess
	message := ""
	if err != nil {
		status, message = domain.StatusError, err.Error()
		logw.Logf("ERROR", "%s failed: %v", kind, err)
	} else {
		logw.Logf("INFO", "%s finished", kind)
	}
	_ = logw.Close()
	finished := r.Clock().UTC()
	act.Status, act.FinishedAt, act.Error = status, &finished, message
	if r.Activities != nil {
		if finishErr := r.Activities.FinishRepositoryActivity(context.Background(), act.ID, status, finished, message); finishErr != nil {
			return errors.Join(err, finishErr)
		}
	}
	if err != nil && ctx.Err() == nil {
		lines, readErr := logstore.ReadLines(act.LogPath, 0)
		if readErr == nil && len(lines) > 15 {
			lines = lines[len(lines)-15:]
		}
		var tail []string
		for _, line := range lines {
			tail = append(tail, line.Text)
		}
		nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if notifyErr := r.Notifier.NotifyFailure(nctx, domain.NotifyEvent{
			Title:    "Restic " + string(kind),
			Backup:   domain.BackupConfig{Name: restic.RedactRepo(repo.Repository)},
			Activity: domain.Activity{StartedAt: act.StartedAt, FinishedAt: &finished, Error: message},
			LogTail:  strings.Join(tail, "\n"),
		}); notifyErr != nil {
			r.Logger.Warn("notify repository maintenance failure", "task", kind, "repository", restic.RedactRepo(repo.Repository), "err", notifyErr)
		}
	}
	return err
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
