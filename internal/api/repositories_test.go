package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/restic"
)

type repositoryInspectorStub struct {
	stats   domain.RepositorySnapshotStats
	treeErr error
}

func (s repositoryInspectorStub) RepositoryStats(context.Context, domain.ResticConfig) (int64, int, error) {
	return 0, 0, nil
}

func (s repositoryInspectorStub) RepositorySnapshots(context.Context, domain.ResticConfig) ([]domain.RepositorySnapshot, error) {
	return nil, nil
}

func (s repositoryInspectorStub) RepositorySnapshotStats(context.Context, domain.ResticConfig, string) (domain.RepositorySnapshotStats, error) {
	return s.stats, nil
}

func (s repositoryInspectorStub) RepositoryTree(context.Context, domain.ResticConfig, string, string) ([]domain.RepositoryNode, error) {
	return nil, s.treeErr
}

func (repositoryInspectorStub) DownloadSnapshotFile(context.Context, domain.ResticConfig, string, string, io.Writer) error {
	return nil
}

func TestListRepositories(t *testing.T) {
	globalRepo := domain.ResticConfig{Repository: "local:/main"}
	otherRepo := domain.ResticConfig{Repository: "local:/other"}
	handler := NewHandler(Deps{
		RepositorySource: func() ([]domain.BackupConfig, domain.MaintenanceConfig) {
			return []domain.BackupConfig{
				{Name: "zeta", Restic: otherRepo},
				{Name: "main-project", Restic: globalRepo},
				{Name: "alpha", Restic: otherRepo},
			}, domain.MaintenanceConfig{Repo: &globalRepo}
		},
	})
	rec := httptest.NewRecorder()
	handler.ListRepositories(rec, httptest.NewRequest("GET", "/api/v1/repositories", nil))

	if rec.Code != 200 {
		t.Fatalf("ListRepositories status = %d, want 200", rec.Code)
	}

	var response struct {
		Items []struct {
			ID      string   `json:"id"`
			IsMain  bool     `json:"is_main"`
			Backups []string `json:"backups"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode repository response: %v", err)
	}

	repositories := make(map[string]struct {
		IsMain  bool
		Backups []string
	}, len(response.Items))
	for _, repo := range response.Items {
		repositories[repo.ID] = struct {
			IsMain  bool
			Backups []string
		}{IsMain: repo.IsMain, Backups: repo.Backups}
	}

	main := repositories[domain.RepositoryID(globalRepo.Repository)]
	if !main.IsMain {
		t.Error("global repository is_main = false, want true")
	}
	if !slices.Equal(main.Backups, []string{"main-project"}) {
		t.Errorf("global repository backups = %v, want [main-project]", main.Backups)
	}

	other := repositories[domain.RepositoryID(otherRepo.Repository)]
	if other.IsMain {
		t.Error("project repository is_main = true, want false")
	}
	if !slices.Equal(other.Backups, []string{"alpha", "zeta"}) {
		t.Errorf("project repository backups = %v, want [alpha zeta]", other.Backups)
	}
}

func TestRepositorySnapshotStatsEndpoint(t *testing.T) {
	cfg := domain.ResticConfig{Repository: "local:/stats"}
	handler := NewHandler(Deps{
		RepositorySource: func() ([]domain.BackupConfig, domain.MaintenanceConfig) {
			return []domain.BackupConfig{{Name: "backup", Restic: cfg}}, domain.MaintenanceConfig{}
		},
		RepositoryInspector: repositoryInspectorStub{
			stats: domain.RepositorySnapshotStats{Available: true, FilesAdded: 2, FilesChanged: 1, BytesAdded: 4096},
		},
	})
	id := domain.RepositoryID(cfg.Repository)
	req := httptest.NewRequest("GET", "/api/v1/repositories/"+id+"/snapshots/abcdef0123456789/summary", nil)
	rec := httptest.NewRecorder()
	NewRouter(handler).ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("snapshot stats status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var response domain.RepositorySnapshotStats
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode snapshot stats: %v", err)
	}
	if !response.Available || response.FilesAdded != 2 || response.FilesChanged != 1 || response.BytesAdded != 4096 {
		t.Fatalf("snapshot stats = %+v", response)
	}
}

func TestRepositoryTreeMapsMissingSnapshotToNotFound(t *testing.T) {
	cfg := domain.ResticConfig{Repository: "local:/tree"}
	handler := NewHandler(Deps{
		RepositorySource: func() ([]domain.BackupConfig, domain.MaintenanceConfig) {
			return []domain.BackupConfig{{Name: "backup", Restic: cfg}}, domain.MaintenanceConfig{}
		},
		RepositoryInspector: repositoryInspectorStub{treeErr: restic.ErrSnapshotNotFound},
	})
	id := domain.RepositoryID(cfg.Repository)
	req := httptest.NewRequest("GET", "/api/v1/repositories/"+id+"/snapshots/abcdef0123456789/tree", nil)
	rec := httptest.NewRecorder()
	NewRouter(handler).ServeHTTP(rec, req)

	if rec.Code != 404 {
		t.Fatalf("snapshot tree status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"message":"snapshot not found"`) {
		t.Fatalf("snapshot tree response = %s, want snapshot not found", rec.Body.String())
	}
}
