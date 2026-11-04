package domain

import "time"

// SnapshotInfo describes the restic snapshot created by a backup run.
type SnapshotInfo struct {
	ID           string
	Timestamp    time.Time // when the snapshot was taken (start of the restic backup)
	FilesAdded   int64     // new files in the snapshot
	FilesChanged int64
	BytesAdded   int64 // data added to the repository by this snapshot
}
