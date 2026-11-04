-- +goose Up
CREATE TABLE backup_snapshots (
  backup_name   TEXT PRIMARY KEY,
  snapshot_id   TEXT NOT NULL,
  taken_at      TEXT NOT NULL,
  files_added   INTEGER NOT NULL,
  files_changed INTEGER NOT NULL,
  bytes_added   INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE backup_snapshots;
