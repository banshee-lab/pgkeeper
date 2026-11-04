package api

import (
	"cmp"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"slices"
	"strconv"
	"time"
	"uuid"

	"github.com/go-chi/chi/v5"

	"github.com/banshee-lab/pgkeeper/internal/auth"
	"github.com/banshee-lab/pgkeeper/internal/backup"
	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/events"
	"github.com/banshee-lab/pgkeeper/internal/logstore"
)

const maxLimit = 10

type Pinger interface{ PingContext(ctx context.Context) error }

type Deps struct {
	DB         Pinger
	Activities domain.ActivityRepository
	Snapshots  domain.SnapshotRepository // optional: latest_snapshot is null without it
	Catalog    domain.BackupCatalog
	Runner     domain.BackupRunner
	Scheduler  domain.Scheduler // optional: next_run is null without it
	Jobs       JobFeed          // optional: defaults to Runner when it implements JobFeed
	JobHub     *events.Hub[events.JobEvent]
	LogHub     *events.Hub[logstore.LogLine]
	Auth       *auth.Service // nil when auth is disabled
	AppCtx     context.Context
	Heartbeat  time.Duration // SSE ping interval, default 15s
	Logger     *slog.Logger
	// Timezone is the effective IANA timezone name used by the scheduler.
	// Empty string means local time (reported as "Local" in the API).
	Timezone string
}

type Handler struct{ Deps }

func NewHandler(d Deps) *Handler {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.AppCtx == nil {
		d.AppCtx = context.Background()
	}
	if d.Heartbeat <= 0 {
		d.Heartbeat = 15 * time.Second
	}
	if d.JobHub == nil {
		d.JobHub = events.NewHub[events.JobEvent]()
	}
	if d.LogHub == nil {
		d.LogHub = events.NewHub[logstore.LogLine]()
	}
	d.Jobs = resolveJobFeed(d)
	return &Handler{Deps: d}
}

func (h *Handler) internalError(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.Logger.Error(what, "err", err, "path", r.URL.Path)
	writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	if err := h.DB.PingContext(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "database unreachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	if h.Auth == nil {
		// Auth disabled: 200 {auth_enabled:false, authenticated:true, user:null}.
		m := ToMeDTO(false, nil)
		m.Authenticated = true
		writeJSON(w, http.StatusOK, m)
		return
	}
	writeJSON(w, http.StatusOK, ToMeDTO(true, auth.SessionFrom(r.Context())))
}

// ---- backups ----

func parseLimit(r *http.Request) (int, bool) {
	s := r.URL.Query().Get("limit")
	if s == "" {
		return maxLimit, true
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, false
	}
	return min(n, maxLimit), true
}

// effectiveTZ returns the timezone name to include in BackupDTO responses.
func (h *Handler) effectiveTZ() string {
	if h.Timezone != "" {
		return h.Timezone
	}
	return "Local"
}

func (h *Handler) backupViews(ctx context.Context, bs []domain.BackupConfig) ([]BackupDTO, error) {
	names := make([]string, len(bs))
	for i, b := range bs {
		names[i] = b.Name
	}
	last, err := h.Activities.LastByBackup(ctx, names)
	if err != nil {
		return nil, err
	}
	success, err := h.Activities.LastSuccessByBackup(ctx, names)
	if err != nil {
		return nil, err
	}
	var snaps map[string]domain.SnapshotInfo
	if h.Snapshots != nil {
		if snaps, err = h.Snapshots.LatestByBackup(ctx, names); err != nil {
			return nil, err
		}
	}
	tz := h.effectiveTZ()
	out := make([]BackupDTO, 0, len(bs))
	for _, b := range bs {
		var next *time.Time
		if h.Scheduler != nil {
			if t, ok := h.Scheduler.NextRun(b.Name); ok {
				next = &t
			}
		}
		var la *domain.Activity
		if a, ok := last[b.Name]; ok {
			la = &a
		}
		// running and last_activity come from the same persisted snapshot
		// (one SQLite query), never from a separate in-memory read.
		running := la != nil && la.Status == domain.StatusRunning
		lsAt, hasSuccess := success[b.Name]
		dto := ToBackupDTO(b, running, next, la, lsAt, hasSuccess, tz)
		if sn, ok := snaps[b.Name]; ok {
			s := ToSnapshotDTO(sn)
			dto.LatestSnapshot = &s
		}
		out = append(out, dto)
	}
	return out, nil
}

func (h *Handler) ListBackups(w http.ResponseWriter, r *http.Request) {
	bs := h.Catalog.List()
	slices.SortFunc(bs, func(a, b domain.BackupConfig) int { return cmp.Compare(a.Name, b.Name) })
	items, err := h.backupViews(r.Context(), bs)
	if err != nil {
		h.internalError(w, r, "list backups", err)
		return
	}
	writeJSON(w, http.StatusOK, ListDTO[BackupDTO]{Items: items})
}

func (h *Handler) GetBackup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	b, ok := h.Catalog.Get(name)
	if !ok {
		writeError(w, http.StatusNotFound, CodeBackupNotFound, "backup '"+name+"' does not exist")
		return
	}
	items, err := h.backupViews(r.Context(), []domain.BackupConfig{b})
	if err != nil {
		h.internalError(w, r, "get backup", err)
		return
	}
	writeJSON(w, http.StatusOK, items[0])
}

func (h *Handler) ListBackupActivities(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if _, ok := h.Catalog.Get(name); !ok {
		writeError(w, http.StatusNotFound, CodeBackupNotFound, "backup '"+name+"' does not exist")
		return
	}
	lim, ok := parsePageLimit(r)
	if !ok {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "limit must be an integer between 1 and 100")
		return
	}
	filt, ok := parseActivityFilter(r)
	if !ok {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid filter parameter")
		return
	}
	filt.BackupName = name
	filt.Limit = lim
	items, next, err := h.Activities.ListFiltered(r.Context(), filt)
	if err != nil {
		h.internalError(w, r, "list backup activities", err)
		return
	}
	resp := PagedListDTO[ActivityDTO]{Items: ToActivityDTOs(items)}
	if next != "" {
		resp.Next = &next
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) RunBackup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	// CSRF defence: a body, if present, must be JSON (it is otherwise ignored).
	if r.ContentLength != 0 {
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" {
			writeError(w, http.StatusBadRequest, CodeInvalidRequest, "Content-Type must be application/json")
			return
		}
	}
	// AppCtx, not r.Context(): the execution must outlive this request.
	act, err := h.Runner.Run(h.AppCtx, name, domain.TriggerManual)
	switch {
	case errors.Is(err, domain.ErrBackupUnknown):
		writeError(w, http.StatusNotFound, CodeBackupNotFound, "backup '"+name+"' does not exist")
	case errors.Is(err, domain.ErrBackupRunning):
		writeError(w, http.StatusConflict, CodeBackupRunning, "backup '"+name+"' is already running")
	case errors.Is(err, backup.ErrShuttingDown):
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "server is shutting down")
	case err != nil:
		h.internalError(w, r, "run backup", err)
	default:
		writeJSON(w, http.StatusAccepted, ToActivityDTO(*act))
	}
}

// ---- activities ----

func (h *Handler) ListActivities(w http.ResponseWriter, r *http.Request) {
	lim, ok := parsePageLimit(r)
	if !ok {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "limit must be an integer between 1 and 100")
		return
	}
	filt, ok := parseActivityFilter(r)
	if !ok {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid filter parameter")
		return
	}
	filt.Limit = lim
	items, next, err := h.Activities.ListFiltered(r.Context(), filt)
	if err != nil {
		h.internalError(w, r, "list activities", err)
		return
	}
	resp := PagedListDTO[ActivityDTO]{Items: ToActivityDTOs(items)}
	if next != "" {
		resp.Next = &next
	}
	writeJSON(w, http.StatusOK, resp)
}

// loadActivity parses {id} and loads the row, writing 400/404/500 itself.
func (h *Handler) loadActivity(w http.ResponseWriter, r *http.Request) (*domain.Activity, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid activity id")
		return nil, false
	}
	a, err := h.Activities.Get(r.Context(), id)
	if errors.Is(err, domain.ErrNotFound) {
		writeError(w, http.StatusNotFound, CodeActivityNotFound, "activity does not exist")
		return nil, false
	}
	if err != nil {
		h.internalError(w, r, "get activity", err)
		return nil, false
	}
	return a, true
}

func (h *Handler) GetActivity(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadActivity(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, ToActivityDTO(*a))
}

func (h *Handler) GetActivityLog(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadActivity(w, r)
	if !ok {
		return
	}
	// The path comes only from the DB row, never from user input.
	f, err := os.Open(a.LogPath)
	if errors.Is(err, os.ErrNotExist) {
		if a.Status == domain.StatusRunning {
			// The runner creates the file just after the run starts: an empty
			// log is the correct content for that window.
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			return
		}
		writeError(w, http.StatusNotFound, CodeLogNotFound, "log file not found")
		return
	}
	if err != nil {
		h.internalError(w, r, "open log", err)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		h.internalError(w, r, "stat log", err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	// R-9: download=1 triggers attachment download with a sanitized filename.
	if r.URL.Query().Get("download") == "1" {
		fn := safeFilename(a.BackupName, fmtTime(a.StartedAt))
		w.Header().Set("Content-Disposition", `attachment; filename="`+fn+`"`)
	}
	w.WriteHeader(http.StatusOK)
	// Snapshot: never read past the size at open time (file may still grow).
	_, _ = io.Copy(w, io.LimitReader(f, fi.Size()))
}

// ---- jobs ----

func (h *Handler) ListJobs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, ListDTO[JobDTO]{Items: ToJobDTOs(h.Runner.Running())})
}