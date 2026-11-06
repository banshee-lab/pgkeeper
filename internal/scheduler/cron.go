package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/banshee-lab/pgkeeper/internal/config"
	"github.com/banshee-lab/pgkeeper/internal/domain"
)

// entry is a registered cron entry and the spec it was created from, so a
// reload only touches entries whose schedule changed.
type entry struct {
	id   cron.EntryID
	spec string
}

type Cron struct {
	c      *cron.Cron
	runner domain.BackupRunner
	log    *slog.Logger

	mu      sync.RWMutex // guards the fields below
	backups map[string]entry
	maint   domain.MaintenanceRunner
	tasks   map[domain.MaintenanceKind]entry
}

var _ domain.Scheduler = (*Cron)(nil)

func New(loc *time.Location, backups []domain.BackupConfig, runner domain.BackupRunner, logger *slog.Logger) (*Cron, error) {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Cron{
		c:       cron.New(cron.WithLocation(loc), cron.WithParser(config.CronParser)),
		backups: make(map[string]entry, len(backups)),
		tasks:   map[domain.MaintenanceKind]entry{},
		runner:  runner,
		log:     logger,
	}
	for _, b := range backups {
		if err := s.addBackup(b); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// addBackup registers one backup; the caller holds mu (or owns s exclusively).
func (s *Cron) addBackup(b domain.BackupConfig) error {
	name := b.Name
	id, err := s.c.AddFunc(b.Cron, func() { s.tick(name) })
	if err != nil {
		return fmt.Errorf("backup %q: invalid cron %q: %w", name, b.Cron, err)
	}
	s.backups[name] = entry{id: id, spec: b.Cron}
	s.log.Info("backup schedule registered", "backup", name, "cron", b.Cron)
	return nil
}

func (s *Cron) addTask(kind domain.MaintenanceKind, spec string) error {
	id, err := s.c.AddFunc(spec, func() { s.maintTick(kind) })
	if err != nil {
		return fmt.Errorf("restic %s: invalid cron %q: %w", kind, spec, err)
	}
	s.tasks[kind] = entry{id: id, spec: spec}
	s.log.Info("restic maintenance schedule registered", "task", kind, "cron", spec)
	return nil
}

// SetMaintenance sets the runner behind the prune/check schedules. Call
// Reload afterwards to register them.
func (s *Cron) SetMaintenance(m domain.MaintenanceRunner) {
	s.mu.Lock()
	s.maint = m
	s.mu.Unlock()
}

// Reload makes the registered schedules match backups and mc without a
// restart. All cron expressions are validated first; on error nothing
// changes. Entries with an unchanged schedule are kept as they are, removed
// or changed ones are replaced. Runs already in progress are not affected.
func (s *Cron) Reload(backups []domain.BackupConfig, mc domain.MaintenanceConfig) error {
	for _, b := range backups {
		if _, err := config.CronParser.Parse(b.Cron); err != nil {
			return fmt.Errorf("backup %q: invalid cron %q: %w", b.Name, b.Cron, err)
		}
	}
	want := map[domain.MaintenanceKind]string{}
	for kind, spec := range map[domain.MaintenanceKind]string{
		domain.MaintenancePrune: mc.PruneCron,
		domain.MaintenanceCheck: mc.CheckCron,
	} {
		if spec == "" {
			continue
		}
		if _, err := config.CronParser.Parse(spec); err != nil {
			return fmt.Errorf("restic %s: invalid cron %q: %w", kind, spec, err)
		}
		want[kind] = spec
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	keep := make(map[string]string, len(backups))
	for _, b := range backups {
		keep[b.Name] = b.Cron
	}
	for name, e := range s.backups {
		if spec, ok := keep[name]; !ok || spec != e.spec {
			s.c.Remove(e.id)
			delete(s.backups, name)
			if !ok {
				s.log.Info("backup schedule removed", "backup", name)
			}
		}
	}
	var errs []error
	for _, b := range backups {
		if _, ok := s.backups[b.Name]; !ok {
			errs = append(errs, s.addBackup(b))
		}
	}

	if s.maint == nil && len(want) > 0 {
		s.log.Warn("restic maintenance schedules ignored: no maintenance runner")
		want = nil
	}
	for kind, e := range s.tasks {
		if spec, ok := want[kind]; !ok || spec != e.spec {
			s.c.Remove(e.id)
			delete(s.tasks, kind)
			if !ok {
				s.log.Info("restic maintenance schedule removed", "task", kind)
			}
		}
	}
	for kind, spec := range want {
		if _, ok := s.tasks[kind]; !ok {
			errs = append(errs, s.addTask(kind, spec))
		}
	}
	return errors.Join(errs...)
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

func (s *Cron) maintTick(kind domain.MaintenanceKind) {
	s.mu.RLock()
	m := s.maint
	s.mu.RUnlock()
	if m == nil {
		return
	}
	err := m.Trigger(kind)
	switch {
	case err == nil:
	case errors.Is(err, domain.ErrMaintenanceRunning):
		s.log.Warn("skipping scheduled restic maintenance, previous still running", "task", kind)
	default:
		s.log.Error("scheduled restic maintenance failed to start", "task", kind, "err", err)
	}
}

func (s *Cron) Start() error {
	s.mu.RLock()
	n := len(s.backups)
	s.mu.RUnlock()
	s.c.Start()
	s.log.Info("scheduler started", "backup_count", n)
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
	s.mu.RLock()
	e, ok := s.backups[name]
	s.mu.RUnlock()
	if !ok {
		return time.Time{}, false
	}
	next := s.c.Entry(e.id).Next
	if next.IsZero() {
		return time.Time{}, false
	}
	return next.UTC(), true
}
