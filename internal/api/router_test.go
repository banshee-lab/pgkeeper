package api

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"testing"
)

type okPinger struct{}

func (okPinger) PingContext(context.Context) error { return nil }

func TestRouterBasics(t *testing.T) {
	srv := NewRouter(NewHandler(Deps{DB: okPinger{}, Logger: slog.Default()}))
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/healthz", 200},
		{"POST", "/healthz", 405},
		{"GET", "/auth/login", 404},    // auth disabled
		{"GET", "/auth/callback", 404}, // auth disabled
		{"POST", "/auth/logout", 204},
		{"GET", "/api/v1/me", 200}, // auth disabled -> open
		{"GET", "/nope", 404},
	} {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}