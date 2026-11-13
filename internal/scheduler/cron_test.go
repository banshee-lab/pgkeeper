package scheduler

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

type countRunner struct{ ch chan string }

func (c countRunner) Run(_ context.Context, name string, _ domain.Trigger) (*domain.Activity, error) {
	c.ch <- name
	return nil, nil
}
func (c countRunner) Running() []domain.Job { return nil }

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestInvalidCronRejected(t *testing.T) {
	_, err := New(time.UTC, []domain.BackupConfig{{Name: "x", Cron: "nope"}}, countRunner{}, quiet)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFiresAndNextRun(t *testing.T) {
	r := countRunner{ch: make(chan string, 4)}
	s, err := New(time.UTC, []domain.BackupConfig{{Name: "x", Cron: "@every 1s"}}, r, quiet)
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Stop(context.Background())

	if _, ok := s.NextRun("x"); !ok {
		t.Fatal("NextRun missing")
	}
	if _, ok := s.NextRun("nope"); ok {
		t.Fatal("unknown backup must not have NextRun")
	}
	select {
	case got := <-r.ch:
		if got != "x" {
			t.Fatalf("got %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cron did not fire")
	}
}

type fakeMaint struct{ ch chan domain.MaintenanceKind }

func (f fakeMaint) Trigger(k domain.MaintenanceKind) error { f.ch <- k; return nil }

func TestReloadSwapsSchedules(t *testing.T) {
	s, err := New(time.UTC, []domain.BackupConfig{
		{Name: "keep", Cron: "@daily"}, {Name: "gone", Cron: "@daily"}, {Name: "chg", Cron: "@daily"},
	}, countRunner{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Stop(context.Background())
	keepID := s.backups["keep"].id

	err = s.Reload([]domain.BackupConfig{
		{Name: "keep", Cron: "@daily"}, {Name: "chg", Cron: "@hourly"}, {Name: "new", Cron: "@daily"},
	}, domain.MaintenanceConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if s.backups["keep"].id != keepID {
		t.Fatal("unchanged schedule must be kept as is")
	}
	if _, ok := s.NextRun("gone"); ok {
		t.Fatal("removed backup still scheduled")
	}
	if _, ok := s.NextRun("new"); !ok {
		t.Fatal("new backup not scheduled")
	}
	if s.backups["chg"].spec != "@hourly" {
		t.Fatal("changed schedule not applied")
	}
}

func TestReloadInvalidChangesNothing(t *testing.T) {
	s, _ := New(time.UTC, []domain.BackupConfig{{Name: "a", Cron: "@daily"}}, countRunner{}, quiet)
	err := s.Reload([]domain.BackupConfig{{Name: "a", Cron: "@hourly"}, {Name: "b", Cron: "bad"}}, domain.MaintenanceConfig{})
	if err == nil {
		t.Fatal("expected error")
	}
	if s.backups["a"].spec != "@daily" || len(s.backups) != 1 {
		t.Fatal("failed reload must leave the schedules untouched")
	}
	if err := s.Reload(nil, domain.MaintenanceConfig{PruneCron: "nope"}); err == nil {
		t.Fatal("invalid maintenance cron must be rejected")
	}
}

func TestMaintenanceScheduleFires(t *testing.T) {
	s, _ := New(time.UTC, nil, countRunner{}, quiet)
	m := fakeMaint{ch: make(chan domain.MaintenanceKind, 4)}
	s.SetMaintenance(m)
	if err := s.Reload(nil, domain.MaintenanceConfig{PruneCron: "@every 1s"}); err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Stop(context.Background())
	select {
	case k := <-m.ch:
		if k != domain.MaintenancePrune {
			t.Fatalf("kind = %s", k)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("maintenance cron did not fire")
	}
	// disabling removes it
	if err := s.Reload(nil, domain.MaintenanceConfig{}); err != nil {
		t.Fatal(err)
	}
	if len(s.tasks) != 0 {
		t.Fatal("maintenance schedule not removed")
	}
}

type fakeRestorer struct{ ch chan string }

func (f fakeRestorer) TestRestore(_ context.Context, name string, by domain.Trigger, snapshotID string) (*domain.Activity, error) {
	if by != domain.TriggerSchedule || snapshotID != "" {
		return nil, domain.ErrRestoreInvalid
	}
	f.ch <- name
	return nil, nil
}
func (f fakeRestorer) RestoreToContainer(context.Context, string, domain.Trigger, domain.RestoreRequest) (*domain.Activity, error) {
	return nil, nil
}
func (f fakeRestorer) RestoreSnapshots(context.Context, string) ([]domain.RestoreSnapshot, error) {
	return nil, nil
}

func TestScheduledTestRestoreFiresAndReloads(t *testing.T) {
	bk := []domain.BackupConfig{{Name: "x", Cron: "@daily", RestoreCron: "@every 1s", RestoreImage: "pg18"}}
	s, err := New(time.UTC, bk, countRunner{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	r := fakeRestorer{ch: make(chan string, 4)}
	s.SetRestorer(r)
	if err := s.Reload(bk, domain.MaintenanceConfig{}); err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Stop(context.Background())

	select {
	case got := <-r.ch:
		if got != "x" {
			t.Fatalf("got %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("restore cron did not fire")
	}

	// Removing restore-cron unregisters the schedule.
	if err := s.Reload([]domain.BackupConfig{{Name: "x", Cron: "@daily"}}, domain.MaintenanceConfig{}); err != nil {
		t.Fatal(err)
	}
	if len(s.tests) != 0 {
		t.Fatalf("test restore schedule not removed: %v", s.tests)
	}
	if err := s.Reload([]domain.BackupConfig{{Name: "x", Cron: "@daily", RestoreCron: "nope"}}, domain.MaintenanceConfig{}); err == nil {
		t.Fatal("invalid restore-cron must be rejected")
	}
}
