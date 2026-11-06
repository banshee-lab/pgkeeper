package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/api"
	"github.com/banshee-lab/pgkeeper/internal/auth"
	"github.com/banshee-lab/pgkeeper/internal/backup"
	"github.com/banshee-lab/pgkeeper/internal/config"
	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/events"
	"github.com/banshee-lab/pgkeeper/internal/executor"
	"github.com/banshee-lab/pgkeeper/internal/logstore"
	"github.com/banshee-lab/pgkeeper/internal/maintenance"
	"github.com/banshee-lab/pgkeeper/internal/reload"
	"github.com/banshee-lab/pgkeeper/internal/restic"
	"github.com/banshee-lab/pgkeeper/internal/scheduler"
	"github.com/banshee-lab/pgkeeper/internal/store"
)

// Run wires everything. If runOnce != "" it runs that backup once and returns
// (nil on success, error otherwise) without starting the scheduler or HTTP server.
func Run(ctx context.Context, cfg *config.Config, configPath string, notifier domain.Notifier, log *slog.Logger, runOnce string) error {
	db, err := store.Open(cfg.Global.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := store.Migrate(db); err != nil {
		return err
	}

	activities := store.NewActivityStore(db)
	repositoryActivities := store.NewRepositoryActivityStore(db)
	interruptedRepos, err := repositoryActivities.MarkRepositoryActivitiesInterrupted(ctx, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("mark interrupted repository activities: %w", err)
	}
	if interruptedRepos > 0 {
		log.Info("marked interrupted repository activities", "count", interruptedRepos)
	}
	snapshots := store.NewSnapshotStore(db)
	n, err := activities.MarkRunningAsInterrupted(ctx, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("mark interrupted: %w", err)
	}
	log.Info("marked interrupted activities", "count", n)

	docker := executor.DockerCLI{}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	if err := docker.Check(cctx); err != nil {
		log.Warn("docker check failed (continuing)", "err", err)
	}
	cancel()

	resticCLI := restic.New(cfg.Global.TempPath)
	vctx, vcancel := context.WithTimeout(ctx, 5*time.Second)
	if v, err := resticCLI.CheckVersion(vctx); err != nil {
		log.Warn("restic check failed (continuing)", "version", v, "err", err)
	} else {
		log.Info("restic available", "version", v)
	}
	vcancel()

	jobHub := events.NewHub[events.JobEvent]()
	logHub := events.NewHub[logstore.LogLine]()

	// holder lets a config reload swap the backup catalog under every consumer.
	holder := config.NewHolder(cfg)

	runner := backup.NewRunner(backup.Deps{
		Catalog:    holder,
		Global:     cfg.Global,
		Activities: activities,
		Snapshots:  snapshots,
		Exec:       docker,
		Restic:     resticCLI,
		Notifier:   notifier,
		JobHub:     jobHub,
		LogHub:     logHub,
		Logger:     log,
	})

	if runOnce != "" {
		return runOneShot(ctx, runner, activities, runOnce, log)
	}

	// OIDC: fail fast. Auth configured but unusable must never silently open the API.
	var authSvc *auth.Service
	if cfg.Auth != nil {
		sessions := store.NewSessionStore(db)
		authSvc, err = auth.New(ctx, *cfg.Auth, sessions)
		if err != nil {
			return fmt.Errorf("auth: %w", err)
		}
		if !cfg.Auth.CookieSecure {
			log.Warn("auth.cookie_secure=false: session cookie is sent over plain HTTP (local development only)")
		}
		go authSvc.RunCleanup(ctx, time.Hour, log)
	}

	// Scheduled restic prune/check over every distinct repository. They share
	// the runner's per-repository gate, so they queue with backups.
	maint := maintenance.New(maintenance.Deps{
		Source: func() ([]domain.BackupConfig, domain.MaintenanceConfig) {
			c := holder.Current()
			return c.Backups, c.Maintenance
		},
		Restic:     resticCLI,
		Locker:     runner,
		Notifier:   notifier,
		Logger:     log,
		Activities: repositoryActivities,
		TempPath:   cfg.Global.TempPath,
		LogHub:     logHub,
	})

	sched, err := scheduler.New(cfg.Global.Location, cfg.Backups, runner, log)
	if err != nil {
		return err
	}
	sched.SetMaintenance(maint)
	if err := sched.Reload(cfg.Backups, cfg.Maintenance); err != nil {
		return err
	}
	if err := sched.Start(); err != nil {
		return err
	}

	// `pgkeeper -reload` (or `kill -HUP`) re-reads the config file.
	watchReload(ctx, configPath, holder, sched, log)
	pidFile := reload.PIDFile(cfg.Global.TempPath)
	if err := reload.WritePID(pidFile); err != nil {
		log.Warn("cannot write PID file; `pgkeeper -reload` will not find this process (use kill -HUP)", "path", pidFile, "err", err)
	} else {
		defer reload.RemovePID(pidFile)
	}

	h := api.NewHandler(api.Deps{
		DB:         db,
		Activities: activities,
		Snapshots:  snapshots,
		Catalog:    holder,
		Runner:     runner,
		Scheduler:  sched,
		JobHub:     jobHub,
		LogHub:     logHub,
		Auth:       authSvc,
		AppCtx:     ctx,
		Logger:     log,
		RepositorySource: func() ([]domain.BackupConfig, domain.MaintenanceConfig) {
			c := holder.Current()
			return c.Backups, c.Maintenance
		},
		RepositoryInspector:   resticCLI,
		RepositoryActivities:  repositoryActivities,
		RepositoryMaintenance: maint,
	})
	srv := api.NewServer(cfg.Global.Listen, api.NewRouter(h))

	// Request contexts derive from baseCtx so open SSE streams can be ended on
	// shutdown (http.Server.Shutdown does not cancel them by itself).
	baseCtx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()
	srv.BaseContext = func(net.Listener) context.Context { return baseCtx }

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	var srvErr error
	select {
	case srvErr = <-errCh:
	case <-ctx.Done():
	}

	sctx, scancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer scancel()
	if err := sched.Stop(sctx); err != nil {
		log.Warn("scheduler stop", "err", err)
	}
	if err := maint.Shutdown(sctx); err != nil {
		log.Warn("maintenance shutdown", "err", err)
	}
	if err := runner.Shutdown(sctx); err != nil {
		log.Warn("runner shutdown", "err", err)
	}
	baseCancel() // ends SSE handlers
	hctx, hcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer hcancel()
	if err := srv.Shutdown(hctx); err != nil {
		log.Warn("http shutdown", "err", err)
	}
	return srvErr
}

func runOneShot(ctx context.Context, r *backup.Runner, acts domain.ActivityRepository, name string, log *slog.Logger) error {
	act, err := r.Run(ctx, name, domain.TriggerManual)
	if err != nil {
		return err
	}
	// Blocks until the job ends; on SIGINT (ctx cancel) running jobs are cancelled.
	if err := r.Shutdown(ctx); err != nil {
		return err
	}
	got, err := acts.Get(context.Background(), act.ID)
	if err != nil {
		return err
	}
	if got.Status != domain.StatusSuccess {
		return fmt.Errorf("backup %s failed: %s (log: %s)", name, got.Error, got.LogPath)
	}
	log.Info("backup finished", "backup", name, "activity", got.ID, "log", got.LogPath)
	return nil
}
