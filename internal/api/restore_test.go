package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"uuid"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

// spyRestorer records the arguments of TestRestore.
type spyRestorer struct {
	calls []string // snapshot IDs passed to TestRestore
	by    []domain.Trigger
}

func (s *spyRestorer) TestRestore(_ context.Context, _ string, by domain.Trigger, snapshotID string) (*domain.Activity, error) {
	s.calls = append(s.calls, snapshotID)
	s.by = append(s.by, by)
	return &domain.Activity{ID: uuid.New(), Database: "db", Kind: domain.KindTestRestore,
		Status: domain.StatusRunning, StartedAt: time.Now().UTC()}, nil
}
func (s *spyRestorer) RestoreToContainer(context.Context, string, domain.Trigger, domain.RestoreRequest) (*domain.Activity, error) {
	return nil, nil
}
func (s *spyRestorer) RestoreSnapshots(context.Context, string) ([]domain.RestoreSnapshot, error) {
	return nil, nil
}

func TestRunTestRestoreSnapshotSelection(t *testing.T) {
	cases := []struct {
		name, ctype, body, want string
		status                  int
	}{
		{"no body means latest", "", "", "", http.StatusAccepted},
		{"empty object means latest", "application/json", `{}`, "", http.StatusAccepted},
		{"selected snapshot", "application/json", `{"snapshot_id":"aaaaaaaa"}`, "aaaaaaaa", http.StatusAccepted},
		{"unknown field rejected", "application/json", `{"foo":1}`, "", http.StatusBadRequest},
		{"malformed json rejected", "application/json", `{`, "", http.StatusBadRequest},
		{"non-json body rejected", "text/plain", `{"snapshot_id":"aaaaaaaa"}`, "", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := &spyRestorer{}
			a := newAPI(t, func(d *Deps) { d.Restorer = spy })
			rec := a.do(http.MethodPost, "/api/v1/backups/alpha/restore/test", tc.ctype, tc.body)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body)
			}
			if tc.status != http.StatusAccepted {
				if len(spy.calls) != 0 {
					t.Fatalf("restorer must not be called, got %v", spy.calls)
				}
				return
			}
			if len(spy.calls) != 1 || spy.calls[0] != tc.want || spy.by[0] != domain.TriggerManual {
				t.Fatalf("calls = %v by = %v, want [%q] manual", spy.calls, spy.by, tc.want)
			}
		})
	}
}
