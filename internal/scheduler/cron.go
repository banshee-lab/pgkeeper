package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/banshee-lab/pgkeeper/internal/config"
	"github.com/banshee-lab/pgkeeper/internal/domain"
)

type Cron struct {
	c      *cron.Cron
	ids    map[string]cron.EntryID
	runner domain.BackupRunner
	log    *slog.Logger
}

var _ domain.Scheduler = (*Cron)(nil)

func New(loc *time.Location, backups []domain.BackupConfig, runner domain.BackupRunner, logger *slog.Logger) (*Cron, error) {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Cron{
		c:      cron.New(cron.WithLocation(loc), cron.WithParser(config.CronParser)),
		ids:    make(map[string]cron.EntryID, len(backups)),
		runner: runner,
		log:    logger,
	}
	for _, b := range backups {
		name := b.Name
		id, err := s.c.AddFunc(b.Cron, func() { s.tick(name) })
		if err != nil {
			return nil, fmt.Errorf("backup %q: invalid cron %q: %w", name, b.Cron, err)
		}
		s.ids[name] = id
		s.log.Info("backup schedule registered", "backup", name, "cron", b.Cron)
	}
	return s, nil
}

func (s *Cron) tick(name string) {
	_, err := s.runner.Run(context.Background(), name, domain.TriggerSchedule)
	switch {
	case err == nil:
	case errors.Is(err, domain.ErrBackupRunning):
		s.log.Warn("skipping scheduled run, previous still running", "backup", name)
	default:
		s.log.Error("scheduled run failed to start", "backup", name, "err", err)
	}
}

func (s *Cron) Start() error {
	s.c.Start()
	s.log.Info("scheduler started", "backup_count", len(s.ids))
	return nil
}

func (s *Cron) Stop(ctx context.Context) error {
	s.log.Info("scheduler stopping")
	select {
	case <-s.c.Stop().Done():
		s.log.Info("scheduler stopped")
		return nil
	case <-ctx.Done():
		s.log.Warn("scheduler stop timed out", "err", ctx.Err())
		return ctx.Err()
	}
}

func (s *Cron) NextRun(name string) (time.Time, bool) {
	id, ok := s.ids[name]
	if !ok {
		return time.Time{}, false
	}
	next := s.c.Entry(id).Next
	if next.IsZero() {
		return time.Time{}, false
	}
	return next.UTC(), true
}
