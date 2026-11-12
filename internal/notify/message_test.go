package notify

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

func TestTruncateRunesBoundaries(t *testing.T) {
	exact := strings.Repeat("é", 4096)
	if truncateRunes(exact, 4096) != exact {
		t.Fatal("exactly 4096 runes must be unchanged")
	}
	got := truncateRunes(exact+"é", 4096)
	if utf8.RuneCountInString(got) != 4096 || !strings.HasSuffix(got, "…") || !utf8.ValidString(got) {
		t.Fatalf("bad truncation: %d runes", utf8.RuneCountInString(got))
	}
}

func TestMessageKeepsEndOfLogAndFits(t *testing.T) {
	ev := domain.NotifyEvent{
		Backup:   domain.BackupConfig{Name: "main_db", Container: "pg"},
		Activity: domain.Activity{Database: "app", TriggeredBy: domain.TriggerSchedule, Error: "dump: pg_dump exited with status 1", StartedAt: time.Now()},
		LogTail:  strings.Repeat("ñ", 10000) + "LAST",
	}
	msg := formatFailure(ev, time.UTC)
	if utf8.RuneCountInString(msg) > 4096 || !strings.HasSuffix(msg, "LAST") || !utf8.ValidString(msg) {
		t.Fatalf("runes=%d", utf8.RuneCountInString(msg))
	}
	if !strings.HasPrefix(msg, "❌ Backup FAILED: main_db") {
		t.Fatal(msg)
	}
}

func TestHugeErrorHardTruncated(t *testing.T) {
	ev := domain.NotifyEvent{Activity: domain.Activity{Error: strings.Repeat("e", 9000)}}
	if n := utf8.RuneCountInString(formatFailure(ev, time.UTC)); n != 4096 {
		t.Fatalf("got %d runes", n)
	}
}
func TestFormatFailureTitle(t *testing.T) {
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	end := start.Add(90 * time.Second)
	got := formatFailure(domain.NotifyEvent{
		Title:    "Restic prune",
		Backup:   domain.BackupConfig{Name: "/srv/restic"},
		Activity: domain.Activity{StartedAt: start, FinishedAt: &end, Error: "boom"},
		LogTail:  "line",
	}, time.UTC)
	for _, want := range []string{"❌ Restic prune FAILED: /srv/restic", "Duration:  1m30s", "Error:     boom", "line"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestFormatFailureWarningLooksDifferent(t *testing.T) {
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	end := start.Add(time.Minute)
	a := domain.Activity{Kind: domain.KindTestRestore, StartedAt: start, FinishedAt: &end, Status: domain.StatusWarning,
		Error: "restore ok, metadata validation found 1 mismatch"}
	got := formatFailure(domain.NotifyEvent{Backup: domain.BackupConfig{Name: "a"}, Activity: a}, time.UTC)
	if !strings.Contains(got, "⚠️") || !strings.Contains(got, "WARNING: a") || strings.Contains(got, "FAILED") || strings.Contains(got, "❌") {
		t.Fatalf("warning message:\n%s", got)
	}
	a.Status = domain.StatusError
	if got := formatFailure(domain.NotifyEvent{Backup: domain.BackupConfig{Name: "a"}, Activity: a}, time.UTC); !strings.Contains(got, "❌") || !strings.Contains(got, "FAILED: a") {
		t.Fatalf("failed message:\n%s", got)
	}
}
