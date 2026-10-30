package api

import (
	"encoding/json"
	"testing"
	"time"

	"uuid"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

func TestActivityDTO(t *testing.T) {
	start := time.Date(2026, 10, 30, 3, 0, 0, 0, time.UTC)
	fin := start.Add(161 * time.Second)
	a := domain.Activity{ID: uuid.New(), StartedAt: start, FinishedAt: &fin,
		Status: domain.StatusSuccess, TriggeredBy: domain.TriggerSchedule}
	d := ToActivityDTO(a)
	if *d.DurationMS != 161000 || *d.FinishedAt != "2026-10-30T03:02:41Z" || d.Error != nil {
		t.Fatalf("%+v", d)
	}
	a.FinishedAt = nil
	b, _ := json.Marshal(ToActivityDTO(a))
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if v, ok := m["duration_ms"]; !ok || v != nil {
		t.Fatalf("duration_ms must be present and null: %s", b)
	}
}

func TestRedactRepo(t *testing.T) {
	for in, want := range map[string]string{
		"/srv/restic":                      "/srv/restic",
		"rest:https://u:p@host:8000/repo":  "rest:https://***@host:8000/repo",
		"s3:s3.amazonaws.com/bucket":       "s3:s3.amazonaws.com/bucket",
	} {
		if got := redactRepo(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}