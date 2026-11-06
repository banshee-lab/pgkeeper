package api

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/logstore"
	"github.com/banshee-lab/pgkeeper/internal/restic"
)

type repositoryDTO struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	IsMain        bool     `json:"is_main"`
	Backups       []string `json:"backups"`
	NextPrune     *string  `json:"next_prune"`
	NextCheck     *string  `json:"next_check"`
	TotalSize     int64    `json:"total_size,omitempty"`
	SnapshotCount int      `json:"snapshot_count,omitempty"`
}

func (h *Handler) repositoryConfigs() []domain.RepositoryInfo {
	if h.RepositorySource == nil {
		return nil
	}
	backups, mc := h.RepositorySource()
	byID := make(map[string]*domain.RepositoryInfo)
	add := func(cfg domain.ResticConfig, backup string) {
		if cfg.Repository == "" {
			return
		}
		id := domain.RepositoryID(cfg.Repository)
		info := byID[id]
		if info == nil {
			info = &domain.RepositoryInfo{ID: id, Name: restic.RedactRepo(cfg.Repository), Repository: cfg.Repository, Config: cfg, Backups: []string{}}
			byID[id] = info
		}
		if backup == "" {
			info.IsMain = true
		}
		if backup != "" && !slices.Contains(info.Backups, backup) {
			info.Backups = append(info.Backups, backup)
		}
	}
	if mc.Repo != nil {
		add(*mc.Repo, "")
	}
	for _, b := range backups {
		add(b.Restic, b.Name)
	}
	out := make([]domain.RepositoryInfo, 0, len(byID))
	for _, info := range byID {
		slices.Sort(info.Backups)
		if h.Scheduler != nil {
			if t, ok := h.Scheduler.NextMaintenance(domain.MaintenancePrune); ok {
				info.NextPrune = &t
			}
			if t, ok := h.Scheduler.NextMaintenance(domain.MaintenanceCheck); ok {
				info.NextCheck = &t
			}
		}
		out = append(out, *info)
	}
	slices.SortFunc(out, func(a, b domain.RepositoryInfo) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func (h *Handler) repository(id string) (domain.RepositoryInfo, bool) {
	for _, repo := range h.repositoryConfigs() {
		if repo.ID == id {
			return repo, true
		}
	}
	return domain.RepositoryInfo{}, false
}

func (h *Handler) ListRepositories(w http.ResponseWriter, _ *http.Request) {
	items := h.repositoryConfigs()
	out := make([]repositoryDTO, 0, len(items))
	for _, repo := range items {
		dto := repositoryDTO{ID: repo.ID, Name: repo.Name, IsMain: repo.IsMain, Backups: repo.Backups}
		dto.NextPrune, dto.NextCheck = timePtr(repo.NextPrune), timePtr(repo.NextCheck)
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, ListDTO[repositoryDTO]{Items: out})
}

func (h *Handler) GetRepository(w http.ResponseWriter, r *http.Request) {
	repo, ok := h.repository(chi.URLParam(r, "id"))
	if !ok {
		writeError(w, http.StatusNotFound, CodeNotFound, "repository not found")
		return
	}
	if h.RepositoryInspector == nil {
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "repository inspection unavailable")
		return
	}
	size, count, err := h.RepositoryInspector.RepositoryStats(r.Context(), repo.Config)
	if err != nil {
		h.internalError(w, r, "get repository stats", err)
		return
	}
	dto := repositoryDTO{ID: repo.ID, Name: repo.Name, IsMain: repo.IsMain, Backups: repo.Backups, TotalSize: size, SnapshotCount: count}
	dto.NextPrune, dto.NextCheck = timePtr(repo.NextPrune), timePtr(repo.NextCheck)
	writeJSON(w, http.StatusOK, dto)
}

func (h *Handler) RepositorySnapshots(w http.ResponseWriter, r *http.Request) {
	repo, ok := h.repository(chi.URLParam(r, "id"))
	if !ok {
		writeError(w, http.StatusNotFound, CodeNotFound, "repository not found")
		return
	}
	if h.RepositoryInspector == nil {
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "repository inspection unavailable")
		return
	}
	items, err := h.RepositoryInspector.RepositorySnapshots(r.Context(), repo.Config)
	if err != nil {
		h.internalError(w, r, "list repository snapshots", err)
		return
	}
	writeJSON(w, http.StatusOK, ListDTO[domain.RepositorySnapshot]{Items: items})
}

func (h *Handler) RepositorySnapshotStats(w http.ResponseWriter, r *http.Request) {
	repo, ok := h.repository(chi.URLParam(r, "id"))
	if !ok {
		writeError(w, http.StatusNotFound, CodeNotFound, "repository not found")
		return
	}
	if h.RepositoryInspector == nil {
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "repository inspection unavailable")
		return
	}
	stats, err := h.RepositoryInspector.RepositorySnapshotStats(r.Context(), repo.Config, chi.URLParam(r, "snapshot"))
	switch {
	case errors.Is(err, restic.ErrInvalidSnapshotRequest):
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid snapshot")
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, "snapshot not found")
	case err != nil:
		h.internalError(w, r, "get snapshot statistics", err)
	default:
		writeJSON(w, http.StatusOK, stats)
	}
}

func (h *Handler) RepositoryTree(w http.ResponseWriter, r *http.Request) {
	repo, ok := h.repository(chi.URLParam(r, "id"))
	if !ok {
		writeError(w, http.StatusNotFound, CodeNotFound, "repository not found")
		return
	}
	if h.RepositoryInspector == nil {
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "repository inspection unavailable")
		return
	}
	items, err := h.RepositoryInspector.RepositoryTree(r.Context(), repo.Config, chi.URLParam(r, "snapshot"), r.URL.Query().Get("path"))
	if err != nil {
		switch {
		case errors.Is(err, restic.ErrInvalidSnapshotRequest):
			writeError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid snapshot or path")
		case errors.Is(err, restic.ErrSnapshotNotFound):
			writeError(w, http.StatusNotFound, CodeNotFound, "snapshot not found")
		default:
			h.internalError(w, r, "list snapshot tree", err)
		}
		return
	}
	writeJSON(w, http.StatusOK, ListDTO[domain.RepositoryNode]{Items: items})
}

func (h *Handler) DownloadSnapshotFile(w http.ResponseWriter, r *http.Request) {
	repo, ok := h.repository(chi.URLParam(r, "id"))
	if !ok {
		writeError(w, http.StatusNotFound, CodeNotFound, "repository not found")
		return
	}
	if h.RepositoryInspector == nil {
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "repository inspection unavailable")
		return
	}
	file := r.URL.Query().Get("path")
	if file == "" {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "path is required")
		return
	}
	if err := restic.ValidateSnapshotFile(chi.URLParam(r, "snapshot"), file); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid snapshot or path")
		return
	}
	name := path.Base(file)
	if name == "." || name == "/" || strings.ContainsAny(name, "\r\n") {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid file path")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	if err := h.RepositoryInspector.DownloadSnapshotFile(r.Context(), repo.Config, chi.URLParam(r, "snapshot"), file, w); err != nil {
		h.Logger.Error("download snapshot file", "err", err, "repository", repo.ID)
	}
}

type repositoryActivityDTO struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	TriggeredBy string  `json:"triggered_by"`
	Status      string  `json:"status"`
	StartedAt   string  `json:"started_at"`
	FinishedAt  *string `json:"finished_at"`
	Error       *string `json:"error"`
}

func toRepositoryActivity(a domain.RepositoryActivity) repositoryActivityDTO {
	out := repositoryActivityDTO{ID: a.ID, Kind: string(a.Kind), TriggeredBy: string(a.TriggeredBy), Status: string(a.Status), StartedAt: fmtTime(a.StartedAt), FinishedAt: timePtr(a.FinishedAt)}
	if a.Error != "" {
		out.Error = &a.Error
	}
	return out
}

func (h *Handler) ListRepositoryActivities(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.repository(id); !ok {
		writeError(w, http.StatusNotFound, CodeNotFound, "repository not found")
		return
	}
	if h.RepositoryActivities == nil {
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "activity history unavailable")
		return
	}
	items, err := h.RepositoryActivities.ListRepositoryActivities(r.Context(), id, 100)
	if err != nil {
		h.internalError(w, r, "list repository activities", err)
		return
	}
	out := make([]repositoryActivityDTO, 0, len(items))
	for _, item := range items {
		out = append(out, toRepositoryActivity(item))
	}
	writeJSON(w, http.StatusOK, ListDTO[repositoryActivityDTO]{Items: out})
}

func (h *Handler) RunRepositoryActivity(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.repository(id); !ok {
		writeError(w, http.StatusNotFound, CodeNotFound, "repository not found")
		return
	}
	if h.RepositoryMaintenance == nil {
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "repository maintenance unavailable")
		return
	}
	var body struct {
		Kind domain.MaintenanceKind `json:"kind"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil || (body.Kind != domain.MaintenancePrune && body.Kind != domain.MaintenanceCheck) {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "kind must be prune or check")
		return
	}
	act, err := h.RepositoryMaintenance.TriggerRepository(id, body.Kind)
	switch {
	case errors.Is(err, domain.ErrMaintenanceRunning):
		writeError(w, http.StatusConflict, CodeBackupRunning, "repository activity is already running")
	case errors.Is(err, domain.ErrRepoMissing):
		writeError(w, http.StatusNotFound, CodeNotFound, "repository not found")
	case err != nil:
		h.internalError(w, r, "start repository activity", err)
	default:
		writeJSON(w, http.StatusAccepted, toRepositoryActivity(*act))
	}
}

func (h *Handler) ForceRepositoryUnlock(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.repository(id); !ok {
		writeError(w, http.StatusNotFound, CodeNotFound, "repository not found")
		return
	}
	if h.RepositoryMaintenance == nil {
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "repository maintenance unavailable")
		return
	}
	if h.RepositoryMaintenance.RepositoryRunning(id) {
		writeError(w, http.StatusConflict, CodeBackupRunning, "repository is currently in use by pgkeeper")
		return
	}
	act, err := h.RepositoryMaintenance.TriggerRepository(id, domain.MaintenanceUnlock)
	switch {
	case errors.Is(err, domain.ErrMaintenanceRunning):
		writeError(w, http.StatusConflict, CodeBackupRunning, "repository activity is already running")
	case errors.Is(err, domain.ErrRepoMissing):
		writeError(w, http.StatusNotFound, CodeNotFound, "repository not found")
	case err != nil:
		h.internalError(w, r, "unlock repository", err)
	default:
		writeJSON(w, http.StatusAccepted, toRepositoryActivity(*act))
	}
}

func (h *Handler) RepositoryActivityLog(w http.ResponseWriter, r *http.Request) {
	act, ok := h.loadRepositoryActivity(w, r)
	if !ok {
		return
	}
	f, err := os.Open(act.LogPath)
	if errors.Is(err, os.ErrNotExist) && act.Status == domain.StatusRunning {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		return
	}
	if err != nil {
		h.internalError(w, r, "open repository activity log", err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.Copy(w, f)
}

func (h *Handler) loadRepositoryActivity(w http.ResponseWriter, r *http.Request) (*domain.RepositoryActivity, bool) {
	repoID, activityID := chi.URLParam(r, "id"), chi.URLParam(r, "activity")
	if _, ok := h.repository(repoID); !ok {
		writeError(w, http.StatusNotFound, CodeNotFound, "repository not found")
		return nil, false
	}
	if h.RepositoryActivities == nil {
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "activity history unavailable")
		return nil, false
	}
	act, err := h.RepositoryActivities.GetRepositoryActivity(r.Context(), activityID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && act.RepositoryID != repoID) {
		writeError(w, http.StatusNotFound, CodeNotFound, "activity not found")
		return nil, false
	}
	if err != nil {
		h.internalError(w, r, "get repository activity", err)
		return nil, false
	}
	return act, true
}

func (h *Handler) StreamRepositoryActivityLog(w http.ResponseWriter, r *http.Request) {
	act, ok := h.loadRepositoryActivity(w, r)
	if !ok {
		return
	}
	guard := newSessionGuard(r.Context())
	expired, stopExpiry := guard.timer()
	defer stopExpiry()
	logCh, cancelLog := h.LogHub.Subscribe(func(line logstore.LogLine) bool { return line.ActivityID.String() == act.ID })
	defer cancelLog()
	if current, err := h.RepositoryActivities.GetRepositoryActivity(r.Context(), act.ID); err == nil {
		act = current
	}
	sse, ok := newSSE(w)
	if !ok {
		return
	}
	after := lastEventID(r)
	follower := &logFollower{sse: sse, path: act.LogPath, last: after}
	tick := time.NewTicker(400 * time.Millisecond)
	defer tick.Stop()
	if err := follower.catchUp(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return
	}
	for {
		current, err := h.RepositoryActivities.GetRepositoryActivity(r.Context(), act.ID)
		if err != nil || current.Status != domain.StatusRunning {
			_ = follower.catchUp()
			status, message := string(domain.StatusError), "final status unavailable"
			if err == nil {
				status, message = string(current.Status), current.Error
			}
			_ = sse.event("end", "", LogEndDTO{Status: status, Error: message})
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-expired:
			return
		case <-tick.C:
			if !guard.valid() || sse.comment("ping") != nil {
				return
			}
		case line, open := <-logCh:
			if !open || !guard.valid() || follower.live(line) != nil {
				return
			}
		}
	}
}
