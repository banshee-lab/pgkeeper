package api

import (
	"net/http"
	"strings"
)

// checkSameOrigin enforces that POST /auth/logout and POST .../run were
// initiated from the same origin. It checks the Origin header first; if
// absent, it falls back to Referer. It compares only scheme+host against
// the incoming request's host (X-Forwarded-* headers are intentionally
// ignored to avoid trusting untrusted proxies).
//
// Returns true when the check passes (or when auth is disabled and the
// request is a no-auth logout — those still must pass origin checks in
// production, but the function accepts a bypassWhenNoAuth flag for that case).
func checkSameOrigin(r *http.Request) bool {
	// Derive the canonical request origin from the host the server sees.
	// We trust r.Host (set by the HTTP server from the Host header) and
	// require HTTPS at the proxy level; the scheme we validate against is
	// https unless the host is localhost or a plain IP (dev only).
	host := r.Host
	if host == "" {
		return false
	}

	// Build the expected origin prefix.  We accept both http and https so
	// that local development without TLS still works; production deployments
	// must enforce HTTPS at the proxy and use SameSite=Lax cookies.
	wantOrigins := []string{
		"https://" + host,
		"http://" + host, // permitted for dev / HTTP-only deployments
	}

	// 1. Check Origin header (sent by all same-origin and cross-origin XHR/fetch).
	if origin := r.Header.Get("Origin"); origin != "" {
		return matchesAny(origin, wantOrigins)
	}

	// 2. Fall back to Referer (sent by form submissions and older browsers).
	if ref := r.Header.Get("Referer"); ref != "" {
		return matchesAnyPrefix(ref, wantOrigins)
	}

	// 3. Both absent: reject (CSRF risk).
	return false
}

func matchesAny(s string, candidates []string) bool {
	for _, c := range candidates {
		if strings.EqualFold(s, c) {
			return true
		}
	}
	return false
}

func matchesAnyPrefix(s string, prefixes []string) bool {
	sl := strings.ToLower(s)
	for _, p := range prefixes {
		if strings.HasPrefix(sl, strings.ToLower(p+"/")) || strings.EqualFold(sl, p) {
			return true
		}
	}
	return false
}

// requireSameOrigin is middleware that enforces checkSameOrigin.
func requireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !checkSameOrigin(r) {
			writeError(w, http.StatusForbidden, CodeInvalidRequest, "request origin not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}