package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
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

	// Regular routes: 30s timeout.
	r.Group(func(r chi.Router) {
		r.Use(middleware.Timeout(30 * time.Second))
		r.Get("/healthz", h.Health)
		r.Route("/auth", func(r chi.Router) {
			r.Get("/login", h.AuthLogin)
			r.Get("/callback", h.AuthCallback)
			r.Post("/logout", h.AuthLogout)
		})
		r.Route("/api/v1", func(r chi.Router) {
			r.Get("/me", h.Me)
			r.Get("/backups", h.ListBackups)
			r.Get("/backups/{name}", h.GetBackup)
			r.Get("/backups/{name}/activities", h.ListBackupActivities)
			r.Post("/backups/{name}/run", h.RunBackup)
			r.Get("/activities", h.ListActivities)
			r.Get("/activities/{id}", h.GetActivity)
			r.Get("/activities/{id}/log", h.GetActivityLog)
			r.Get("/jobs", h.ListJobs)
		})
	})

	// SSE routes: no timeout.
	r.Get("/api/v1/activities/{id}/log/stream", h.StreamActivityLog)
	r.Get("/api/v1/jobs/stream", h.StreamJobs)

	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "route not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	})
	return r
}