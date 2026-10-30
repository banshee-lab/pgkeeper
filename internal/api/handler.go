package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

type Pinger interface{ PingContext(ctx context.Context) error }

type Deps struct {
	DB         Pinger
	Activities domain.ActivityRepository
	Sessions   domain.SessionRepository
	Catalog    domain.BackupCatalog
	Runner     domain.BackupRunner
	Logger     *slog.Logger
}

type Handler struct{ Deps }

func NewHandler(d Deps) *Handler { return &Handler{Deps: d} }

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	if err := h.DB.PingContext(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "database unreachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func notImplemented(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "not implemented")
}

func (h *Handler) AuthLogin(w http.ResponseWriter, r *http.Request)          { notImplemented(w, r) }
func (h *Handler) AuthCallback(w http.ResponseWriter, r *http.Request)       { notImplemented(w, r) }
func (h *Handler) AuthLogout(w http.ResponseWriter, r *http.Request)         { notImplemented(w, r) }
func (h *Handler) Me(w http.ResponseWriter, r *http.Request)                 { notImplemented(w, r) }
func (h *Handler) ListBackups(w http.ResponseWriter, r *http.Request)       { notImplemented(w, r) }
func (h *Handler) GetBackup(w http.ResponseWriter, r *http.Request)          { notImplemented(w, r) }
func (h *Handler) ListBackupActivities(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }
func (h *Handler) RunBackup(w http.ResponseWriter, r *http.Request)          { notImplemented(w, r) }
func (h *Handler) ListActivities(w http.ResponseWriter, r *http.Request)     { notImplemented(w, r) }
func (h *Handler) GetActivity(w http.ResponseWriter, r *http.Request)        { notImplemented(w, r) }
func (h *Handler) GetActivityLog(w http.ResponseWriter, r *http.Request)     { notImplemented(w, r) }
func (h *Handler) StreamActivityLog(w http.ResponseWriter, r *http.Request)  { notImplemented(w, r) }
func (h *Handler) ListJobs(w http.ResponseWriter, r *http.Request)           { notImplemented(w, r) }
func (h *Handler) StreamJobs(w http.ResponseWriter, r *http.Request)         { notImplemented(w, r) }