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