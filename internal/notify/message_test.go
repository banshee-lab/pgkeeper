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