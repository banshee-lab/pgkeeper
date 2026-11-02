package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/api"
	"github.com/banshee-lab/pgkeeper/internal/backup"
	"github.com/banshee-lab/pgkeeper/internal/config"
	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/events"
	"github.com/banshee-lab/pgkeeper/internal/executor"
	"github.com/banshee-lab/pgkeeper/internal/logstore"
	"github.com/banshee-lab/pgkeeper/internal/restic"
	"github.com/banshee-lab/pgkeeper/internal/scheduler"
	"github.com/banshee-lab/pgkeeper/internal/store"
)

// Run wires everything. If runOnce != "" it runs that backup once and returns
// (nil on success, error otherwise) without starting the scheduler or HTTP server.
func Run(ctx context.Context, cfg *config.Config, notifier domain.Notifier, log *slog.Logger, runOnce string) error {
	db, err := store.Open(cfg.Global.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := store.Migrate(db); err != nil {
		return err
	}

	activities := store.NewActivityStore(db)
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

	runner := backup.NewRunner(backup.Deps{
		Catalog:    cfg,
		Global:     cfg.Global,
		Activities: activities,
		Exec:       docker,
		Restic:     resticCLI,
		Notifier:   notifier,
		JobHub:     events.NewHub[events.JobEvent](),
		LogHub:     events.NewHub[logstore.LogLine](),
		Logger:     log,
	})

	if runOnce != "" {
		return runOneShot(ctx, runner, activities, runOnce, log)
	}

	sched, err := scheduler.New(cfg.Global.Location, cfg.Backups, runner, log)
	if err != nil {
		return err
	}
	if err := sched.Start(); err != nil {
		return err
	}

	h := api.NewHandler(api.Deps{
		DB:         db,
		Activities: activities,
		Sessions:   store.NewSessionStore(db),
		Catalog:    cfg,
		Runner:     runner,
		Logger:     log,
	})
	srv := api.NewServer(cfg.Global.Listen, api.NewRouter(h))

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
	if err := runner.Shutdown(sctx); err != nil {
		log.Warn("runner shutdown", "err", err)
	}
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