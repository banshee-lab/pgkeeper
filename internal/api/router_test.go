package api

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

type okPinger struct{}

func (okPinger) PingContext(context.Context) error { return nil }

func TestRouterBasics(t *testing.T) {
	srv := NewRouter(NewHandler(Deps{DB: okPinger{}, Logger: slog.Default()}))
	for _, tc := range []struct {
		method, path string
		want         int
		bodyContains string
	}{
		{"GET", "/healthz", 200, ""},
		{"POST", "/healthz", 405, ""},
		{"GET", "/auth/login", 404, ""},    // auth disabled
		{"GET", "/auth/callback", 404, ""}, // auth disabled
		{"POST", "/auth/logout", 204, ""},
		{"GET", "/api/v1/me", 200, ""}, // auth disabled -> open
		{"GET", "/", 200, "<!doctype html>"},
		{"GET", "/backups/example", 200, "<!doctype html>"}, // SPA fallback
		{"GET", "/api/v1/nope", 404, `"code":"not_found"`},  // API misses remain JSON 404s
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, nil)
		if tc.method == "POST" {
			req.Header.Set("Origin", "http://"+req.Host)
		}
		srv.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
		if tc.bodyContains != "" && !strings.Contains(rec.Body.String(), tc.bodyContains) {
			t.Errorf("%s %s body does not contain %q", tc.method, tc.path, tc.bodyContains)
		}
	}
}
