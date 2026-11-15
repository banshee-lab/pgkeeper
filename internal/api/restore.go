package api

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/banshee-lab/pgkeeper/internal/backup"
	"github.com/banshee-lab/pgkeeper/internal/domain"
)

// TestRestoreRequestDTO is the optional body of POST .../restore/test.
// An empty or omitted SnapshotID restores the latest snapshot.
type TestRestoreRequestDTO struct {
	SnapshotID string `json:"snapshot_id"`
}

// RestoreSnapshotDTO is a restorable snapshot of a project.
type RestoreSnapshotDTO struct {
	ID        string `json:"id"`
	ShortID   string `json:"short_id"`
	Timestamp string `json:"timestamp"`
	HasRoles  bool   `json:"has_roles"`
}

// ContainerRestoreRequestDTO is the body of POST .../restore/container.
// DatabaseURL (postgresql://...) restores into a remote server instead of a
// container; the two are mutually exclusive.
type ContainerRestoreRequestDTO struct {
	SnapshotID   string `json:"snapshot_id"`
	Container    string `json:"container"`
	Database     string `json:"database"`
	DatabaseURL  string `json:"database_url"`
	IncludeRoles bool   `json:"include_roles"`
}

// restoreError writes the HTTP response for an error of the restore runner.
func (h *Handler) restoreError(w http.ResponseWriter, r *http.Request, name, what string, err error) {
	switch {
	case errors.Is(err, domain.ErrBackupUnknown):
		writeError(w, http.StatusNotFound, CodeBackupNotFound, "backup '"+name+"' does not exist")
	case errors.Is(err, domain.ErrBackupRunning):
		writeError(w, http.StatusConflict, CodeBackupRunning, "backup '"+name+"' is running")
	case errors.Is(err, domain.ErrRestoreRunning):
		writeError(w, http.StatusConflict, CodeRestoreRunning, "a restore of '"+name+"' is already running")
	case errors.Is(err, domain.ErrRestoreInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, strings.TrimPrefix(err.Error(), domain.ErrRestoreInvalid.Error()+": "))
	case errors.Is(err, domain.ErrRestoreUnavailable):
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "restore is not available")
	case errors.Is(err, backup.ErrShuttingDown):
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "server is shutting down")
	default:
		h.internalError(w, r, what, err)
	}
}

// ListRestoreSnapshots lists the project's restorable snapshots, newest first.
func (h *Handler) ListRestoreSnapshots(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if h.Restorer == nil {
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "restore is not available")
		return
	}
	snaps, err := h.Restorer.RestoreSnapshots(r.Context(), name)
	if err != nil {
		h.restoreError(w, r, name, "list restore snapshots", err)
		return
	}
	items := make([]RestoreSnapshotDTO, 0, len(snaps))
	for _, s := range snaps {
		items = append(items, RestoreSnapshotDTO{ID: s.ID, ShortID: s.ShortID, Timestamp: fmtTime(s.Time), HasRoles: s.HasRoles})
	}
	writeJSON(w, http.StatusOK, ListDTO[RestoreSnapshotDTO]{Items: items})
}

// RunTestRestore starts a manual test restore into an ephemeral container.
func (h *Handler) RunTestRestore(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	// CSRF defence: a body, if present, must be JSON. The body is optional;
	// without it (or with an empty snapshot_id) the latest snapshot is used.
	if r.ContentLength != 0 && !isJSON(r) {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "Content-Type must be application/json")
		return
	}
	var body TestRestoreRequestDTO
	if r.ContentLength != 0 {
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid request body")
			return
		}
	}
	if h.Restorer == nil {
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "restore is not available")
		return
	}
	// AppCtx, not r.Context(): the execution must outlive this request.
	act, err := h.Restorer.TestRestore(h.AppCtx, name, domain.TriggerManual, body.SnapshotID)
	if err != nil {
		h.restoreError(w, r, name, "run test restore", err)
		return
	}
	writeJSON(w, http.StatusAccepted, ToActivityDTO(*act))
}

// RunContainerRestore starts a manual restore into a live / custom container.
func (h *Handler) RunContainerRestore(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !isJSON(r) {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "Content-Type must be application/json")
		return
	}
	var body ContainerRestoreRequestDTO
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid request body")
		return
	}
	if h.Restorer == nil {
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "restore is not available")
		return
	}
	act, err := h.Restorer.RestoreToContainer(h.AppCtx, name, domain.TriggerManual, domain.RestoreRequest{
		SnapshotID: body.SnapshotID, Container: body.Container, Database: body.Database, IncludeRoles: body.IncludeRoles,
		DatabaseURL: domain.Secret(body.DatabaseURL),
	})
	if err != nil {
		h.restoreError(w, r, name, "run container restore", err)
		return
	}
	writeJSON(w, http.StatusAccepted, ToActivityDTO(*act))
}

func isJSON(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == "application/json"
}
