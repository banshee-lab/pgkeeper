-- +goose Up
CREATE TABLE repository_activities (
  id            TEXT PRIMARY KEY,
  repository_id TEXT NOT NULL,
  kind          TEXT NOT NULL CHECK (kind IN ('prune','check','unlock')),
  triggered_by  TEXT NOT NULL CHECK (triggered_by IN ('schedule','manual')),
  status        TEXT NOT NULL CHECK (status IN ('running','success','error')),
  started_at    TEXT NOT NULL,
  finished_at   TEXT,
  error         TEXT,
  log_path      TEXT NOT NULL
) STRICT;
CREATE INDEX idx_repository_activities_repo_started
  ON repository_activities(repository_id, started_at DESC);

-- +goose Down
DROP TABLE repository_activities;
