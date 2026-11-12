package notify

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

const (
	maxMessageRunes = 4096
	maxTailRunes    = 2000
	maxTailLines    = 15
	timeLayout      = "2006-01-02 15:04:05 -07"
)

func formatFailure(e domain.NotifyEvent, loc *time.Location) string {
	a := e.Activity
	dur := "n/a"
	if d := a.Duration(); d != nil {
		dur = d.Round(time.Second).String()
	}
	icon, word := "❌", "FAILED"
	if a.Status == domain.StatusWarning {
		icon, word = "⚠️", "WARNING"
	}
	if e.Title != "" {
		head := fmt.Sprintf("%s %s %s: %s\nStarted:   %s\nDuration:  %s\nError:     %s",
			icon, e.Title, word, e.Backup.Name, a.StartedAt.In(loc).Format(timeLayout), dur, a.Error)
		return assemble(head, e.LogTail)
	}
	head := fmt.Sprintf(
		"%s %s %s: %s\nDatabase:  %s (container %s)\nStarted:   %s\nDuration:  %s\nTrigger:   %s\nPhase/Err: %s\nActivity:  %s",
		icon, a.EffectiveKind().Label(), word, e.Backup.Name, a.Database, e.Backup.Container,
		a.StartedAt.In(loc).Format(timeLayout), dur, a.TriggeredBy, a.Error, a.ID)
	return assemble(head, e.LogTail)
}

// assemble appends the log tail, truncating the TAIL first (keeping its end),
// then hard-truncates the whole message. All counting is in runes.
func assemble(head, tail string) string {
	tail = keepEndRunes(lastLines(tail, maxTailLines), maxTailRunes)
	build := func(t string) string {
		if t == "" {
			return head
		}
		return head + "\n\nLast log lines:\n" + t
	}
	msg := build(tail)
	if over := utf8.RuneCountInString(msg) - maxMessageRunes; over > 0 && tail != "" {
		rs := []rune(tail)
		if over+1 >= len(rs) {
			tail = ""
		} else {
			tail = "…" + string(rs[over+1:])
		}
		msg = build(tail)
	}
	return truncateRunes(msg, maxMessageRunes)
}

func lastLines(s string, n int) string {
	s = strings.TrimRight(s, "\r\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func keepEndRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[len(rs)-n:])
}

// truncateRunes returns s unchanged if it has <= n runes; otherwise the first
// n-1 runes plus "…" (n runes total). Never splits a multi-byte rune.
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}