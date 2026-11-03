package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/auth"
	"github.com/banshee-lab/pgkeeper/internal/auth/authtest"
	"github.com/banshee-lab/pgkeeper/internal/config"
)

func TestAuthFlowThroughRouter(t *testing.T) {
	idp := authtest.NewIdP(t, "pgkeeper")
	svc, err := auth.New(context.Background(), config.AuthConfig{
		Issuer: idp.URL, ClientID: "pgkeeper", Secret: "s3cret",
		RedirectURL: "http://app.test/auth/callback", Scopes: []string{"openid", "profile", "email"},
		SessionTTL: time.Hour, PostLoginRedirect: "/", CookieSecure: false,
	}, authtest.NewMemSessions())
	if err != nil {
		t.Fatal(err)
	}
	a := newAPI(t, func(d *Deps) { d.Auth = svc; d.Logger = slog.New(slog.NewTextHandler(io.Discard, nil)) })

	do := func(method, target string, c *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		if method == http.MethodPost {
			req.Header.Set("Origin", "http://"+req.Host)
		}
		if c != nil {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		a.router.ServeHTTP(rec, req)
		return rec
	}

	// No session: every /api/v1 route (SSE included) is 401 JSON, never a redirect.
	for _, p := range []string{"/api/v1/backups", "/api/v1/me", "/api/v1/jobs/stream"} {
		if rec := do("GET", p, nil); rec.Code != 401 || errCode(t, rec) != CodeUnauthenticated {
			t.Errorf("%s = %d", p, rec.Code)
		}
	}
	if rec := do("GET", "/healthz", nil); rec.Code != 200 {
		t.Errorf("healthz must stay public, got %d", rec.Code)
	}

	// IdP error is rejected without echoing free text.
	if rec := do("GET", "/auth/callback?error=access_denied&error_description=<script>", nil); rec.Code != 400 || strings.Contains(rec.Body.String(), "script") {
		t.Errorf("error callback = %d %s", rec.Code, rec.Body.String())
	}

	// Login round trip.
	rec := do("GET", "/auth/login?redirect_to=/backups", nil)
	if rec.Code != 302 {
		t.Fatalf("login = %d", rec.Code)
	}
	code, state := idp.Authorize(t, rec.Header().Get("Location"))
	rec = do("GET", "/auth/callback?code="+code+"&state="+state, nil)
	if rec.Code != 302 || rec.Header().Get("Location") != "/backups" {
		t.Fatalf("callback = %d -> %s (%s)", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != auth.CookieName || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie = %+v", cookies)
	}
	session := cookies[0]

	// Replaying the same callback fails.
	if rec := do("GET", "/auth/callback?code="+code+"&state="+state, nil); rec.Code != 400 {
		t.Errorf("replayed callback = %d", rec.Code)
	}

	rec = do("GET", "/api/v1/me", session)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"authenticated":true`) || !strings.Contains(rec.Body.String(), "u@example.com") {
		t.Fatalf("me = %d %s", rec.Code, rec.Body.String())
	}

	// Logout.
	if rec := do("POST", "/auth/logout", session); rec.Code != 204 {
		t.Fatalf("logout = %d", rec.Code)
	}
	if rec := do("GET", "/api/v1/me", session); rec.Code != 401 {
		t.Errorf("me after logout = %d", rec.Code)
	}
}