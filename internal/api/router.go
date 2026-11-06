package api

import (
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"

	"github.com/banshee-lab/pgkeeper/web"
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
	webFS, err := fs.Sub(web.Assets, "build")
	if err != nil {
		panic(err)
	}
	webFiles := http.FileServer(http.FS(webFS))

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
		// R-12: logout mutates the session cookie; require same-origin.
		r.With(requireSameOrigin).Post("/auth/logout", h.AuthLogout)
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
			r.Get("/api/v1/repositories", h.ListRepositories)
			r.Get("/api/v1/repositories/{id}", h.GetRepository)
			r.Get("/api/v1/repositories/{id}/activities", h.ListRepositoryActivities)
			r.With(requireSameOrigin).Post("/api/v1/repositories/{id}/activities", h.RunRepositoryActivity)
			r.Get("/api/v1/repositories/{id}/activities/{activity}/log", h.RepositoryActivityLog)
			r.With(requireSameOrigin).Post("/api/v1/repositories/{id}/unlock", h.ForceRepositoryUnlock)
			r.Get("/api/v1/repositories/{id}/snapshots", h.RepositorySnapshots)
			r.Get("/api/v1/repositories/{id}/snapshots/{snapshot}/summary", h.RepositorySnapshotStats)
			r.Get("/api/v1/repositories/{id}/snapshots/{snapshot}/tree", h.RepositoryTree)
			r.Get("/api/v1/repositories/{id}/snapshots/{snapshot}/download", h.DownloadSnapshotFile)
			// R-12: run mutates state (starts a job); require same-origin.
			r.With(requireSameOrigin).Post("/api/v1/backups/{name}/run", h.RunBackup)
			r.Get("/api/v1/activities", h.ListActivities)
			r.Get("/api/v1/activities/{id}", h.GetActivity)
			r.Get("/api/v1/activities/{id}/log", h.GetActivityLog)
			r.Get("/api/v1/jobs", h.ListJobs)
		})

		// SSE: outside the Timeout group, no compression middleware anywhere.
		r.Get("/api/v1/activities/{id}/log/stream", h.StreamActivityLog)
		r.Get("/api/v1/jobs/stream", h.StreamJobs)
		r.Get("/api/v1/repositories/{id}/activities/{activity}/log/stream", h.StreamRepositoryActivityLog)
	})

	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api" || strings.HasPrefix(req.URL.Path, "/api/") ||
			req.URL.Path == "/auth" || strings.HasPrefix(req.URL.Path, "/auth/") ||
			req.URL.Path == "/healthz" || strings.HasPrefix(req.URL.Path, "/healthz/") {
			writeError(w, http.StatusNotFound, CodeNotFound, "route not found")
			return
		}
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			writeError(w, http.StatusNotFound, CodeNotFound, "route not found")
			return
		}

		name := strings.TrimPrefix(req.URL.Path, "/")
		if name != "" {
			if !fs.ValidPath(name) {
				writeError(w, http.StatusNotFound, CodeNotFound, "route not found")
				return
			}
			_, err := fs.Stat(webFS, name)
			switch {
			case err == nil:
				webFiles.ServeHTTP(w, req)
				return
			case !errors.Is(err, fs.ErrNotExist):
				h.Logger.Error("stat embedded web asset", "path", name, "err", err)
				writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
				return
			}
		}

		fallbackReq := req.Clone(req.Context())
		fallbackURL := *req.URL
		fallbackURL.Path = "/"
		fallbackReq.URL = &fallbackURL
		webFiles.ServeHTTP(w, fallbackReq)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	})
	return r
}
