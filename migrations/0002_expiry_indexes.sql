-- +goose Up
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);
CREATE INDEX idx_auth_states_expires_at ON auth_states(expires_at);

-- +goose Down
DROP INDEX idx_auth_states_expires_at;
DROP INDEX idx_sessions_expires_at;
