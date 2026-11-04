package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sync"
	"time"
	"uuid"

	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/events"
	"github.com/banshee-lab/pgkeeper/internal/logstore"
	"github.com/banshee-lab/pgkeeper/internal/notify"
)

var ErrShuttingDown = errors.New("runner is shutting down")

type Deps struct {
	Catalog    domain.BackupCatalog
	Global     domain.GlobalConfig
	Activities domain.ActivityRepository
	Snapshots  domain.SnapshotRepository // optional: the latest snapshot is not recorded without it
	Exec       domain.Executor
	Restic     domain.ResticClient
	Notifier   domain.Notifier
	JobHub     *events.Hub[events.JobEvent]
	LogHub     *events.Hub[logstore.LogLine]
	Logger     *slog.Logger
	Clock      func() time.Time // default time.Now
	NewID      func() uuid.UUID // default uuid.NewV7

	// Final-result persistence policy: FinishAttempts tries (default 3) with
	// FinishBackoff between them (default 500ms).
	FinishAttempts int
	FinishBackoff  time.Duration
}

type Runner struct {
	Deps
	jobs     *JobTracker
	gate     repoGate // serialises the restic phase per repository
	wg       sync.WaitGroup
	mu       sync.Mutex // guards stopping
	stopping bool
	ctx      context.Context // lifetime of running jobs
	cancel   context.CancelFunc
}

var _ domain.BackupRunner = (*Runner)(nil)

func NewRunner(d Deps) *Runner {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Clock == nil {
		d.Clock = time.Now
	}
	if d.NewID == nil {
		d.NewID = func() uuid.UUID {
			id := uuid.NewV7()
			return id
		}
	}
	if d.JobHub == nil {
		d.JobHub = events.NewHub[events.JobEvent]()
	}
	if d.FinishAttempts <= 0 {
		d.FinishAttempts = 3
	}
	if d.FinishBackoff <= 0 {
		d.FinishBackoff = 500 * time.Millisecond
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{Deps: d, jobs: NewJobTracker(d.JobHub), ctx: ctx, cancel: cancel}
}

func (r *Runner) Running() []domain.Job { return r.jobs.List() }

// SubscribeJobs returns the running jobs and the events after them with no
// gap or overlap (see JobTracker.Subscribe).
func (r *Runner) SubscribeJobs() (events.JobSnapshot, <-chan events.JobEvent, func()) {
	return r.jobs.Subscribe()
}

var unsafeRe = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

func safeName(s string) string {
	s = unsafeRe.ReplaceAllString(s, "_")
	if s == "" || s == "." || s == ".." {
		return "_"
	}
	return s
}

// Run registers the execution and starts it in the background. ctx is only used for the DB insert.
func (r *Runner) Run(ctx context.Context, name string, by domain.Trigger) (*domain.Activity, error) {
	cfg, ok := r.Catalog.Get(name)
	if !ok {
		return nil, domain.ErrBackupUnknown
	}

	r.mu.Lock()
	if r.stopping {
		r.mu.Unlock()
		return nil, ErrShuttingDown
	}
	r.wg.Add(1)
	r.mu.Unlock()

	id := r.NewID()
	now := r.Clock().UTC()
	act := domain.Activity{
		ID: id, BackupName: name, Database: cfg.Database, TriggeredBy: by,
		Status: domain.StatusRunning, StartedAt: now,
		LogPath: filepath.Join(r.Global.LogPath, safeName(cfg.Database), id.String()+".log"),
	}
	job := domain.Job{ActivityID: id, BackupName: name, Database: cfg.Database, TriggeredBy: by, StartedAt: now}

	if !r.jobs.TryStart(job) {
		r.wg.Done()
		return nil, domain.ErrBackupRunning
	}
	if err := r.Activities.Create(ctx, &act); err != nil {
		r.jobs.Release(name, id)
		r.wg.Done()
		return nil, fmt.Errorf("create activity: %w", err)
	}
	r.Logger.Info("backup run started",
		"activity", act.ID,
		"backup", cfg.Name,
		"database", cfg.Database,
		"trigger", by,
		"log_path", act.LogPath)
	// The row is persisted: now make the job visible and publish job.started.
	r.jobs.Announce(name, act)

	go r.execute(cfg, act)
	out := act
	return &out, nil
}

type execLog interface {
	io.Writer
	Logf(level, format string, args ...any)
}

type discardLog struct{}

func (discardLog) Write(p []byte) (int, error) { return len(p), nil }
func (discardLog) Logf(string, string, ...any) {}

// redactExecLog routes subprocess stderr (Write) through a RedactWriter so
// that restic passwords and URL credentials cannot appear in log files.
// Runner-level messages (Logf) bypass redaction as the runner controls them.
type redactExecLog struct {
	base   *logstore.Writer
	stderr *logstore.RedactWriter
}

func (l *redactExecLog) Write(p []byte) (int, error)            { return l.stderr.Write(p) }
func (l *redactExecLog) Logf(level, format string, args ...any) { l.base.Logf(level, format, args...) }

func (r *Runner) execute(cfg domain.BackupConfig, act domain.Activity) {
	defer r.wg.Done()
	defer r.jobs.Release(cfg.Name, act.ID) // safety net; idempotent, never touches a newer run
	ctx := r.ctx

	var lg execLog = discardLog{}
	closeLog := func() {}
	if w, err := logstore.Open(act.LogPath, act.ID, r.LogHub); err != nil {
		r.Logger.Error("open execution log",
			"activity", act.ID,
			"backup", cfg.Name,
			"path", act.LogPath,
			"err", err)
	} else {
		// Wrap the subprocess-stderr path with a line-level redactor so that
		// pg_dump / docker output cannot leak the restic password or embedded
		// URL credentials. The runner's own Logf messages bypass this because
		// they are constructed by code we control.
		red := logstore.StringRedactor(cfg.Restic.Password.Reveal())
		rw := logstore.NewRedactWriter(w, red)
		lg = &redactExecLog{base: w, stderr: rw}
		closeLog = func() {
			_ = rw.Close()
			_ = w.Close()
		}
	}
	defer closeLog()

	lg.Logf("INFO", "execution %s started (backup=%s database=%s trigger=%s)", act.ID, cfg.Name, cfg.Database, act.TriggeredBy)
	runErr := r.runPhases(ctx, cfg, act, lg)

	finished := r.Clock().UTC()
	act.FinishedAt = &finished
	act.Status = domain.StatusSuccess
	if runErr != nil {
		act.Status = domain.StatusError
		act.Error = runErr.Error()
		if ctx.Err() != nil {
			act.Error = "interrupted: " + act.Error
		}
	}
	if runErr != nil {
		lg.Logf("ERROR", "execution finished: status=error err=%s", act.Error)
		r.Logger.Error("backup run failed",
			"activity", act.ID,
			"backup", cfg.Name,
			"database", cfg.Database,
			"trigger", act.TriggeredBy,
			"duration", finished.Sub(act.StartedAt),
			"log_path", act.LogPath,
			"err", act.Error)
	} else {
		lg.Logf("INFO", "execution finished: status=success elapsed=%s", finished.Sub(act.StartedAt).Round(time.Millisecond))
		r.Logger.Info("backup run completed",
			"activity", act.ID,
			"backup", cfg.Name,
			"database", cfg.Database,
			"trigger", act.TriggeredBy,
			"duration", finished.Sub(act.StartedAt),
			"log_path", act.LogPath)
	}
	closeLog() // flush before the notifier reads the tail

	// Persist first, publish second. If persistence keeps failing, no
	// success-shaped job.finished is published: subscribers get an
	// "abandoned" event and close without a final status.
	persisted := r.persistFinal(ctx, cfg, act)
	r.jobs.Finish(cfg.Name, act, persisted)

	if runErr != nil {
		nctx, ncancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer ncancel()
		err := r.Notifier.NotifyFailure(nctx, domain.NotifyEvent{
			Backup: cfg, Activity: act, LogTail: notify.TailLog(act.LogPath, 15, 2000),
		})
		if err != nil {
			r.Logger.Warn("notify failure", "backup", cfg.Name, "err", err)
		}
	}
}

// persistFinal stores the final activity row with a bounded retry, even
// during shutdown. It reports whether the row was stored.
func (r *Runner) persistFinal(ctx context.Context, cfg domain.BackupConfig, act domain.Activity) bool {
	base := context.WithoutCancel(ctx)
	var err error
	for attempt := 1; attempt <= r.FinishAttempts; attempt++ {
		fctx, cancel := context.WithTimeout(base, 10*time.Second)
		err = r.Activities.Finish(fctx, act.ID, act.Status, *act.FinishedAt, act.Error)
		cancel()
		if err == nil {
			return true
		}
		if errors.Is(err, domain.ErrNotFound) {
			break // retrying cannot help
		}
		r.Logger.Warn("persist backup result failed",
			"activity", act.ID,
			"backup", cfg.Name,
			"attempt", attempt,
			"err", err)
		if attempt < r.FinishAttempts {
			time.Sleep(r.FinishBackoff)
		}
	}
	r.Logger.Error("persist backup result failed; final status not recorded (row stays 'running' until restart)",
		"activity", act.ID,
		"backup", cfg.Name,
		"status", act.Status,
		"err", err)
	return false
}

func (r *Runner) setPhase(cfg domain.BackupConfig, act domain.Activity, p domain.Phase) {
	r.jobs.SetPhase(cfg.Name, act, p)
}

func (r *Runner) runPhases(ctx context.Context, cfg domain.BackupConfig, act domain.Activity, lg execLog) (err error) {
	tmpDir := filepath.Join(r.Global.TempPath, "pgkeeper", cfg.Name)

	// Deferred LIFO: cleanup runs first, then panic recovery.
	defer func() {
		if p := recover(); p != nil {
			r.Logger.Error("panic in execution", "backup", cfg.Name, "panic", p, "stack", string(debug.Stack()))
			lg.Logf("ERROR", "internal panic: %v", p)
			err = errors.New("internal panic")
		}
	}()
	defer func() {
		lg.Logf("INFO", "phase %s: removing %s", domain.PhaseCleanup, tmpDir)
		if e := os.RemoveAll(tmpDir); e != nil {
			r.Logger.Warn("backup temporary files cleanup failed",
				"backup", cfg.Name,
				"activity", act.ID,
				"path", tmpDir,
				"err", e)
			lg.Logf("WARN", "cleanup failed: %v", e)
		}
	}()

	if err := os.RemoveAll(tmpDir); err != nil {
		return fmt.Errorf("prepare temp dir: %w", err)
	}
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return fmt.Errorf("prepare temp dir: %w", err)
	}

	dbName := safeName(cfg.Database)
	files := []string{dbName + ".sql"}
	if err := r.dump(ctx, cfg, act, lg, domain.PhaseDump, domain.DumpDatabase, filepath.Join(tmpDir, files[0])); err != nil {
		return err
	}
	if cfg.Roles {
		rolesFile := dbName + "_roles.sql"
		files = append(files, rolesFile)
		if err := r.dump(ctx, cfg, act, lg, domain.PhaseRoles, domain.DumpRoles, filepath.Join(tmpDir, rolesFile)); err != nil {
			return err
		}
	}
	return r.resticPhase(ctx, cfg, act, lg, tmpDir, files)
}

func (r *Runner) dump(ctx context.Context, cfg domain.BackupConfig, act domain.Activity, lg execLog,
	phase domain.Phase, kind domain.DumpKind, path string) (resultErr error) {

	r.setPhase(cfg, act, phase)
	r.Logger.Info("backup phase started",
		"activity", act.ID,
		"backup", cfg.Name,
		"phase", phase,
		"container", cfg.Container,
		"database", cfg.Database,
		"host", cfg.Host,
		"port", cfg.Port)
	lg.Logf("INFO", "phase %s: start", phase)
	start := time.Now()
	defer func() {
		if resultErr != nil {
			r.Logger.Error("backup phase failed",
				"activity", act.ID,
				"backup", cfg.Name,
				"phase", phase,
				"duration", time.Since(start),
				"err", resultErr)
		}
	}()

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("%s: %w", phase, err)
	}
	spec := domain.DumpSpec{Kind: kind, Container: cfg.Container, Database: cfg.Database,
		Host: cfg.Host, Username: cfg.Username, Port: cfg.Port}
	dumpErr := r.Exec.Dump(ctx, spec, f, lg)
	closeErr := f.Close()
	if dumpErr != nil {
		return fmt.Errorf("%s: %w", phase, dumpErr)
	}
	if closeErr != nil {
		return fmt.Errorf("%s: %w", phase, closeErr)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s: %w", phase, err)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("%s: output file is empty", phase)
	}
	lg.Logf("INFO", "phase %s: done size=%d bytes elapsed=%s", phase, fi.Size(), time.Since(start).Round(time.Millisecond))
	r.Logger.Info("backup phase completed",
		"activity", act.ID,
		"backup", cfg.Name,
		"phase", phase,
		"size_bytes", fi.Size(),
		"duration", time.Since(start))
	return nil
}

func (r *Runner) resticPhase(ctx context.Context, cfg domain.BackupConfig, act domain.Activity, lg execLog,
	dir string, files []string) (resultErr error) {

	// Backups sharing a repository take turns (FIFO): EnsureRepo, Backup and
	// Forget run as one unit, so there is no check-then-init race and restic
	// never hits a lock held by a sibling run. Dumps still run in parallel.
	queuedAt := time.Now()
	release, err := r.gate.acquire(ctx, cfg.Restic.Repository, cfg.Name, func(holder string, ahead int) {
		r.setPhase(cfg, act, domain.PhaseQueued)
		r.Logger.Info("backup waiting for shared repository",
			"activity", act.ID, "backup", cfg.Name, "held_by", holder, "ahead", ahead)
		lg.Logf("INFO", "phase %s: repository is in use by backup %q (%d ahead in queue), waiting", domain.PhaseQueued, holder, ahead)
	})
	if err != nil {
		return fmt.Errorf("restic: waiting for repository: %w", err)
	}
	defer release()
	if waited := time.Since(queuedAt); waited >= time.Second {
		lg.Logf("INFO", "phase %s: repository acquired after %s", domain.PhaseQueued, waited.Round(time.Millisecond))
	}

	r.setPhase(cfg, act, domain.PhaseRestic)
	r.Logger.Info("backup phase started",
		"activity", act.ID,
		"backup", cfg.Name,
		"phase", domain.PhaseRestic,
		"files", files)
	start := time.Now()
	defer func() {
		if resultErr != nil {
			r.Logger.Error("backup phase failed",
				"activity", act.ID,
				"backup", cfg.Name,
				"phase", domain.PhaseRestic,
				"duration", time.Since(start),
				"err", resultErr)
		}
	}()
	lg.Logf("INFO", "phase %s: start", domain.PhaseRestic)
	if err := r.Restic.EnsureRepo(ctx, cfg.Restic, lg); err != nil {
		return fmt.Errorf("restic: %w", err)
	}
	snapAt := r.Clock().UTC()
	snap, err := r.Restic.Backup(ctx, cfg.Restic, cfg.ResticTag, cfg.Name, dir, files, lg)
	if err != nil {
		return fmt.Errorf("restic: %w", err)
	}
	r.recordSnapshot(ctx, cfg, act, lg, snap, snapAt)
	if err := r.Restic.Forget(ctx, cfg.Restic, cfg.ResticTag, cfg.Name, lg); err != nil {
		return fmt.Errorf("restic: %w", err)
	}
	lg.Logf("INFO", "phase %s: done elapsed=%s", domain.PhaseRestic, time.Since(start).Round(time.Millisecond))
	r.Logger.Info("backup phase completed",
		"activity", act.ID,
		"backup", cfg.Name,
		"phase", domain.PhaseRestic,
		"duration", time.Since(start))
	return nil
}

// recordSnapshot stores the latest snapshot of the backup. A storage problem
// is logged but never fails a backup that already succeeded.
func (r *Runner) recordSnapshot(ctx context.Context, cfg domain.BackupConfig, act domain.Activity, lg execLog,
	snap *domain.SnapshotInfo, at time.Time) {

	if r.Snapshots == nil {
		return
	}
	if snap == nil {
		lg.Logf("WARN", "restic reported no snapshot summary; latest snapshot not updated")
		return
	}
	info := *snap
	info.Timestamp = at
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := r.Snapshots.SaveLatest(sctx, cfg.Name, info); err != nil {
		r.Logger.Warn("save latest snapshot failed", "activity", act.ID, "backup", cfg.Name, "snapshot", info.ID, "err", err)
		lg.Logf("WARN", "could not record snapshot %s: %v", info.ID, err)
	}
}

// Shutdown stops accepting runs and waits for running ones until ctx expires,
// then cancels them (they finish as "error: interrupted…").
func (r *Runner) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	r.stopping = true
	r.mu.Unlock()

	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()

	select {
	case <-done:
		r.cancel()
		return nil
	case <-ctx.Done():
		r.cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
		}
		return fmt.Errorf("runner shutdown: %w", ctx.Err())
	}
}