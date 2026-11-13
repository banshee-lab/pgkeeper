-- +goose Up
-- Adds the 'warning' status (restore succeeded, metadata validation found
-- mismatches). SQLite cannot alter a CHECK constraint, so rebuild the table.
CREATE TABLE activities_new (
  id            TEXT PRIMARY KEY,
  backup_name   TEXT NOT NULL,
  database_name TEXT NOT NULL,
  triggered_by  TEXT NOT NULL CHECK (triggered_by IN ('schedule','manual')),
  status        TEXT NOT NULL CHECK (status IN ('running','success','error','warning')),
  started_at    TEXT NOT NULL,
  finished_at   TEXT,
  error         TEXT,
  log_path      TEXT NOT NULL,
  kind          TEXT NOT NULL DEFAULT 'backup' CHECK (kind IN ('backup','test_restore','restore'))
) STRICT;
INSERT INTO activities_new (id, backup_name, database_name, triggered_by, status, started_at, finished_at, error, log_path, kind)
  SELECT id, backup_name, database_name, triggered_by, status, started_at, finished_at, error, log_path, kind FROM activities;
DROP TABLE activities;
ALTER TABLE activities_new RENAME TO activities;
CREATE INDEX idx_activities_backup_started ON activities(backup_name, started_at DESC);
CREATE INDEX idx_activities_started        ON activities(started_at DESC);

-- +goose Down
CREATE TABLE activities_old (
  id            TEXT PRIMARY KEY,
  backup_name   TEXT NOT NULL,
  database_name TEXT NOT NULL,
  triggered_by  TEXT NOT NULL CHECK (triggered_by IN ('schedule','manual')),
  status        TEXT NOT NULL CHECK (status IN ('running','success','error')),
  started_at    TEXT NOT NULL,
  finished_at   TEXT,
  error         TEXT,
  log_path      TEXT NOT NULL,
  kind          TEXT NOT NULL DEFAULT 'backup' CHECK (kind IN ('backup','test_restore','restore'))
) STRICT;
INSERT INTO activities_old (id, backup_name, database_name, triggered_by, status, started_at, finished_at, error, log_path, kind)
  SELECT id, backup_name, database_name, triggered_by,
         CASE WHEN status = 'warning' THEN 'error' ELSE status END,
         started_at, finished_at, error, log_path, kind FROM activities;
DROP TABLE activities;
ALTER TABLE activities_old RENAME TO activities;
CREATE INDEX idx_activities_backup_started ON activities(backup_name, started_at DESC);
CREATE INDEX idx_activities_started        ON activities(started_at DESC);
