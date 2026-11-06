package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/banshee-lab/pgkeeper/internal/config"
	"github.com/banshee-lab/pgkeeper/internal/scheduler"
)

// watchReload re-reads the config file on every SIGHUP until ctx ends. The
// signal is registered before it returns, so a SIGHUP can never hit the
// default action (terminate) once the daemon is up.
func watchReload(ctx context.Context, path string, holder *config.Holder, sched *scheduler.Cron, log *slog.Logger) {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		defer signal.Stop(hup)
		for {
			select {
			case <-ctx.Done():
				return
			case <-hup:
				if err := applyReload(path, holder, sched, log); err != nil {
					log.Error("config reload failed, keeping the running configuration", "err", err)
				}
			}
		}
	}()
}

// applyReload loads and validates the file, then swaps the backup catalog and
// the cron schedules. Settings that cannot change at runtime (listener, paths,
// timezone, telegram, auth) keep their running values and are reported.
// Running backups and maintenance tasks continue with the old settings.
func applyReload(path string, holder *config.Holder, sched *scheduler.Cron, log *slog.Logger) error {
	next, err := config.Load(path)
	if err != nil {
		return err
	}
	for _, w := range next.Warnings {
		log.Warn(w)
	}
	cur := holder.Current()
	if keys := config.RestartRequired(cur, next); len(keys) > 0 {
		log.Warn("these settings changed but need a restart to apply; keeping the running values", "settings", keys)
	}
	next = config.KeepRunning(cur, next)

	holder.Swap(next)
	if err := sched.Reload(next.Backups, next.Maintenance); err != nil {
		holder.Swap(cur)
		return fmt.Errorf("apply schedules: %w", err)
	}
	log.Info("config reloaded", "backups", len(next.Backups), "summary", next.LogSafe())
	return nil
}
