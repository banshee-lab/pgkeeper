package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
)

func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()
			next.ServeHTTP(ww, r)
			log.LogAttrs(r.Context(), slog.LevelInfo, "http request",
				slog.String("request_id", middleware.GetReqID(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", ww.Status()),
				slog.Duration("duration", time.Since(start)))
		})
	}
}

func NewRouter(h *Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, requestLogger(h.Logger), middleware.Recoverer)

	// Light brute-force / state-table-abuse protection: 20 req/min/IP.
	authLimit := httprate.Limit(20, time.Minute,
		httprate.WithKeyFuncs(httprate.KeyByIP),
		httprate.WithLimitHandler(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusTooManyRequests, CodeRateLimited, "too many requests")
		}))

	// Public routes (no session required), 30s timeout.
	r.Group(func(r chi.Router) {
		r.Use(middleware.Timeout(30 * time.Second))
		r.Get("/healthz", h.Health)
		r.With(authLimit).Get("/auth/login", h.AuthLogin)
		r.With(authLimit).Get("/auth/callback", h.AuthCallback)
		r.Post("/auth/logout", h.AuthLogout)
	})

	// API: session required when auth is enabled.
	r.Group(func(r chi.Router) {
		r.Use(h.requireAuth)

		r.Group(func(r chi.Router) {
			r.Use(middleware.Timeout(30 * time.Second))
			r.Get("/api/v1/me", h.Me)
			r.Get("/api/v1/backups", h.ListBackups)
			r.Get("/api/v1/backups/{name}", h.GetBackup)
			r.Get("/api/v1/backups/{name}/activities", h.ListBackupActivities)
			r.Post("/api/v1/backups/{name}/run", h.RunBackup)
			r.Get("/api/v1/activities", h.ListActivities)
			r.Get("/api/v1/activities/{id}", h.GetActivity)
			r.Get("/api/v1/activities/{id}/log", h.GetActivityLog)
			r.Get("/api/v1/jobs", h.ListJobs)
		})

		// SSE: outside the Timeout group, no compression middleware anywhere.
		r.Get("/api/v1/activities/{id}/log/stream", h.StreamActivityLog)
		r.Get("/api/v1/jobs/stream", h.StreamJobs)
	})

	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, CodeNotFound, "route not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	})
	return r
}