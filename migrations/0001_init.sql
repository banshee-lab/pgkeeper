-- +goose Up
CREATE TABLE activities (
  id            TEXT PRIMARY KEY,
  backup_name   TEXT NOT NULL,
  database_name TEXT NOT NULL,
  triggered_by  TEXT NOT NULL CHECK (triggered_by IN ('schedule','manual')),
  status        TEXT NOT NULL CHECK (status IN ('running','success','error')),
  started_at    TEXT NOT NULL,
  finished_at   TEXT,
  error         TEXT,
  log_path      TEXT NOT NULL
) STRICT;
CREATE INDEX idx_activities_backup_started ON activities(backup_name, started_at DESC);
CREATE INDEX idx_activities_started        ON activities(started_at DESC);

CREATE TABLE sessions (
  id_hash    TEXT PRIMARY KEY,
  subject    TEXT NOT NULL,
  email      TEXT,
  name       TEXT,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
) STRICT;

CREATE TABLE auth_states (
  state         TEXT PRIMARY KEY,
  nonce         TEXT NOT NULL,
  pkce_verifier TEXT NOT NULL,
  redirect_to   TEXT NOT NULL,
  expires_at    TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE auth_states;
DROP TABLE sessions;
DROP TABLE activities;