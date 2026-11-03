package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckSameOrigin(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		origin string
		referer string
		want   bool
	}{
		{"matching origin https", "example.com", "https://example.com", "", true},
		{"matching origin http", "example.com", "http://example.com", "", true},
		{"cross-origin", "example.com", "https://evil.com", "", false},
		{"origin with path ignored (still matches)", "example.com", "https://example.com", "", true},
		{"no origin, matching referer", "example.com", "", "https://example.com/page", true},
		{"no origin, cross-origin referer", "example.com", "", "https://evil.com/x", false},
		{"no origin, no referer", "example.com", "", "", false},
		{"empty host", "", "https://example.com", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.referer != "" {
				req.Header.Set("Referer", tc.referer)
			}
			if got := checkSameOrigin(req); got != tc.want {
				t.Errorf("checkSameOrigin = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRequireSameOriginMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h := requireSameOrigin(inner)

	// passing
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.Host = "pgkeeper.local"
	req.Header.Set("Origin", "https://pgkeeper.local")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rec.Code)
	}

	// failing
	req2 := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req2.Host = "pgkeeper.local"
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec2.Code)
	}
}