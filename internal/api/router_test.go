package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

type okPinger struct{}

func (okPinger) PingContext(context.Context) error { return nil }

func TestRoutes(t *testing.T) {
	srv := NewRouter(NewHandler(Deps{DB: okPinger{}, Logger: slog.Default()}))
	tests := []struct {
		method, path string
		want         int
	}{
		{"GET", "/healthz", 200},
		{"GET", "/auth/login", 501}, {"GET", "/auth/callback", 501}, {"POST", "/auth/logout", 501},
		{"GET", "/api/v1/me", 501},
		{"GET", "/api/v1/backups", 501}, {"GET", "/api/v1/backups/x", 501},
		{"GET", "/api/v1/backups/x/activities", 501}, {"POST", "/api/v1/backups/x/run", 501},
		{"GET", "/api/v1/activities", 501}, {"GET", "/api/v1/activities/1", 501},
		{"GET", "/api/v1/activities/1/log", 501}, {"GET", "/api/v1/activities/1/log/stream", 501},
		{"GET", "/api/v1/jobs", 501}, {"GET", "/api/v1/jobs/stream", 501},
		{"GET", "/nope", 404},
	}
	for _, tc := range tests {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
		_ = http.StatusOK
	}
}