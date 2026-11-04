package store

import (
	"context"
	"database/sql"
	"strings"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

// SnapshotStore keeps the latest restic snapshot of each backup (one row per backup).
type SnapshotStore struct{ db *sql.DB }

func NewSnapshotStore(db *sql.DB) *SnapshotStore { return &SnapshotStore{db: db} }

var _ domain.SnapshotRepository = (*SnapshotStore)(nil)

func (s *SnapshotStore) SaveLatest(ctx context.Context, backupName string, sn domain.SnapshotInfo) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO backup_snapshots (backup_name, snapshot_id, taken_at, files_added, files_changed, bytes_added)
		 VALUES (?,?,?,?,?,?)
		 ON CONFLICT(backup_name) DO UPDATE SET
		   snapshot_id=excluded.snapshot_id, taken_at=excluded.taken_at,
		   files_added=excluded.files_added, files_changed=excluded.files_changed,
		   bytes_added=excluded.bytes_added`,
		backupName, sn.ID, fmtTime(sn.Timestamp), sn.FilesAdded, sn.FilesChanged, sn.BytesAdded)
	return err
}

func (s *SnapshotStore) LatestByBackup(ctx context.Context, names []string) (map[string]domain.SnapshotInfo, error) {
	res := map[string]domain.SnapshotInfo{}
	if len(names) == 0 {
		return res, nil
	}
	args := make([]any, len(names))
	for i, n := range names {
		args[i] = n
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(names)), ",")
	rows, err := s.db.QueryContext(ctx,
		`SELECT backup_name, snapshot_id, taken_at, files_added, files_changed, bytes_added
		 FROM backup_snapshots WHERE backup_name IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			name, ts string
			sn       domain.SnapshotInfo
		)
		if err := rows.Scan(&name, &sn.ID, &ts, &sn.FilesAdded, &sn.FilesChanged, &sn.BytesAdded); err != nil {
			return nil, err
		}
		if sn.Timestamp, err = parseTime(ts); err != nil {
			return nil, err
		}
		res[name] = sn
	}
	return res, rows.Err()
}
