package domain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"time"
)

func RepositoryID(repository string) string {
	sum := sha256.Sum256([]byte(repository))
	return hex.EncodeToString(sum[:16])
}

type RepositoryInfo struct {
	ID            string
	Name          string
	IsMain        bool
	Repository    string
	Config        ResticConfig
	Backups       []string
	NextPrune     *time.Time
	NextCheck     *time.Time
	Current       *RepositoryActivity
	TotalSize     int64
	SnapshotCount int
}

type RepositoryActivity struct {
	ID           string
	RepositoryID string
	Kind         MaintenanceKind
	TriggeredBy  Trigger
	Status       Status
	StartedAt    time.Time
	FinishedAt   *time.Time
	Error        string
	LogPath      string
}

type RepositoryActivityStore interface {
	CreateRepositoryActivity(context.Context, *RepositoryActivity) error
	FinishRepositoryActivity(context.Context, string, Status, time.Time, string) error
	GetRepositoryActivity(context.Context, string) (*RepositoryActivity, error)
	ListRepositoryActivities(context.Context, string, int) ([]RepositoryActivity, error)
	CurrentRepositoryActivities(context.Context, string) ([]RepositoryActivity, error)
	MarkRepositoryActivitiesInterrupted(context.Context, time.Time) (int, error)
}

type RepositoryMaintenanceRunner interface {
	TriggerRepository(string, MaintenanceKind) (*RepositoryActivity, error)
	RepositoryRunning(string) bool
}

type RepositoryInspector interface {
	RepositoryStats(context.Context, ResticConfig) (int64, int, error)
	RepositorySnapshots(context.Context, ResticConfig) ([]RepositorySnapshot, error)
	RepositorySnapshotStats(context.Context, ResticConfig, string) (RepositorySnapshotStats, error)
	RepositoryTree(context.Context, ResticConfig, string, string) ([]RepositoryNode, error)
	DownloadSnapshotFile(context.Context, ResticConfig, string, string, io.Writer) error
}

type RepositorySnapshot struct {
	ID       string                 `json:"id"`
	ShortID  string                 `json:"short_id"`
	Parent   string                 `json:"parent,omitempty"`
	Time     time.Time              `json:"time"`
	Hostname string                 `json:"hostname"`
	Paths    []string               `json:"paths"`
	Tags     []string               `json:"tags"`
	Summary  *ResticSnapshotSummary `json:"summary,omitempty"`
}

type ResticSnapshotSummary struct {
	FilesNew     int64 `json:"files_new"`
	FilesChanged int64 `json:"files_changed"`
	DataAdded    int64 `json:"data_added"`
}

type RepositorySnapshotStats struct {
	Available    bool  `json:"available"`
	FilesAdded   int64 `json:"files_added"`
	FilesChanged int64 `json:"files_changed"`
	BytesAdded   int64 `json:"bytes_added"`
}

type RepositoryNode struct {
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	Type    string    `json:"type"`
	Size    int64     `json:"size,omitempty"`
	ModTime time.Time `json:"mtime,omitempty"`
}
