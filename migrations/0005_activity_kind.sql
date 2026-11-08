-- +goose Up
-- Restores (test and live) are tracked as activities next to backups.
ALTER TABLE activities ADD COLUMN kind TEXT NOT NULL DEFAULT 'backup'
  CHECK (kind IN ('backup','test_restore','restore'));

-- +goose Down
ALTER TABLE activities DROP COLUMN kind;
