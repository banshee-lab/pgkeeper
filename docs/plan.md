# pgkeeper: Project Plan

Backend service (Go 1.27) that schedules `pg_dump` / `pg_dumpall` runs inside Postgres containers via `docker exec`, stores the dumps in a restic repository, records every execution (with log file) in SQLite, notifies failures via Telegram, and exposes a REST + SSE API protected by OIDC for a future frontend.

> Working name: `pgkeeper` (module `pgkeeper`). Rename freely.

## 1. Confirmed decisions

| Topic         | Decision                                                                                                                  |
| ------------- | ------------------------------------------------------------------------------------------------------------------------- |
| Docker access | Local socket mounted (`/var/run/docker.sock`)                                                                             |
| Auth          | OIDC authorization code flow, backend-driven, server-side sessions (cookie)                                               |
| HTTP router   | `go-chi/chi` v5                                                                                                           |
| SQLite        | `modernc.org/sqlite` (pure Go, no CGO)                                                                                    |
| Cron          | `robfig/cron/v3`                                                                                                          |
| Config        | TOML, `${ENV}` expansion, `include` globs                                                                                 |
| Phases        | 1 setup/interfaces/API skeleton/entities, 2 config + Telegram, 3 backup + scheduling, 4 restic, 5 REST/SSE/OIDC, 6 Docker |

## 2. Assumptions to confirm (I did not ask to avoid over-asking; change any you disagree with)

1. **Docker CLI, not Docker SDK.** The image must include `docker-cli`. An `Executor` interface allows swapping to the Engine API later.
2. **`roles` is a TOML boolean** (`roles = true`)
3. **Restic overrides are flat keys** inside `[backup.<name>]` (`repository`, `password`, `keep-daily`, ...), using the same exact keys using restic_ as prefix
4. **One execution = one activity = one log file**, even when roles are enabled (database dump + roles dump + restic are *phases* of the same execution). Result is `error` if any phase fails.
5. **Roles command uses the backup's `host`, `port`, `username`** instead of the hardcoded `localhost/5432/postgres` from the example (identical to the defaults).
6. **Log file path** = `<log_path>/<database>/<execution-uuid>.log`.
7. **Temp files** = `<temp_path>/pgkeeper/<backup-name>/<database>.sql` and `<database>_roles.sql`. Per-backup directory (instead of a bare `<database>.sql`) avoids collisions when two backups share a database name, and keeps stable paths inside restic snapshots. Deleted after every run (success or fail).
8. **Overlap policy:** a backup never runs twice concurrently. A scheduled tick while running is skipped (warning in app log, no activity). A manual trigger while running returns `409`.
9. **Restic repo is auto-initialised** if it does not exist.
10. **One restic snapshot per execution** containing both files (database + roles), tagged with `restic_tag`, `--host <backup-name>`.
11. **`pg_dump --no-password`** requires auth that works non-interactively inside the container (trust, `.pgpass`, or `PGPASSWORD` in the container env). pgkeeper never handles Postgres passwords.
12. **Config is loaded at startup only** (no hot reload; restart to apply). SIGHUP reload is a possible later improvement.
13. **Notifications only on failure** (as specified).
14. **No activity retention/pruning** in v1 (table grows slowly; API caps reads to 10).
15. **Extra config keys I had to add** (needed, not in your sample):
    - `[global] listen` (default `:8080`), `[global] db_path` (default `/var/lib/pgkeeper/pgkeeper.db`)
    - `[auth] redirect_url` (required when auth enabled), optional `scopes`, `session_ttl`, `cookie_secure`, `post_login_redirect`
16. **Crashed runs:** on startup, activities still `running` are marked `error` ("interrupted by restart").
17. **Graceful shutdown:** stop scheduler, wait up to 30 s for running jobs, then cancel them (marked `error`).

## 3. Architecture

```
                    +-----------------------------+
 TOML files ------> |  config  (load/merge/validate)
                    +--------------+--------------+
                                   |
        +--------------------------v--------------------------+
        |                       app (wiring)                   |
        +--+--------------+-----------------+-----------+-----+
           |              |                 |           |
   +-------v----+  +------v-------+  +------v-----+ +---v--------+
   | scheduler  |->| backup.Runner|->| executor   | | api (chi)  |
   | (cron)     |  |  (phases)    |  | docker exec| | REST + SSE |
   +------------+  +--+---+---+---+  +------------+ | OIDC       |
                      |   |   |                      +---+--------+
              +-------+   |   +--------+                 |
              v           v            v                 v
        +-----------+ +---------+ +-----------+    +-----------+
        | store     | | restic  | | notify    |    | events    |
        | (sqlite)  | | client  | | (telegram)|    | hub (SSE) |
        +-----------+ +---------+ +-----------+    +-----------+
              ^                                          ^
              +------------- logstore (file + live tail) +
```

### Package layout

```
cmd/pgkeeper/main.go
internal/
  app/          wiring, lifecycle, graceful shutdown
  domain/       entities, enums, ports (interfaces)
  config/       loader, include, env expansion, defaults, validation
  store/        sqlite repositories + embedded migrations
  scheduler/    cron wrapper
  backup/       Runner (orchestrates phases), per-backup lock, job tracker
  executor/     docker exec implementation
  restic/       restic CLI wrapper
  notify/       Notifier + telegram
  logstore/     per-execution log writer + live subscribers
  events/       in-memory pub/sub hub (job events, log lines)
  api/          router, middleware, handlers, SSE, DTOs
  auth/         OIDC login, session store middleware
migrations/     *.sql (embedded)
docs/           plan.md, api.md, openapi.yaml, phases/
```

## 4. Final configuration reference

```toml
[global]
timezone  = "America/Argentina/Buenos_Aires"  # optional, default system local
include   = ["conf/*.toml"]                   # optional; globs relative to the main file
temp_path = "/var/tmp"                        # optional
log_path  = "/var/log"                        # optional
listen    = ":8080"                           # optional (added)
db_path   = "/var/lib/pgkeeper/pgkeeper.db"   # optional (added)

[telegram]                                    # optional; notifications disabled if absent
bot_token = "${TG_BOT_TOKEN}"
chat_id   = "${TG_CHAT_ID}"

[auth]                                        # optional; API open if absent
issuer       = "https://idp.example.com/realms/x"
client_id    = "pgkeeper"
secret       = "${OIDC_SECRET}"
redirect_url = "https://pgkeeper.example.com/auth/callback"  # added, required
# scopes = ["openid","profile","email"]   # optional
# session_ttl = "12h"                     # optional
# cookie_secure = true                    # optional
# post_login_redirect = "/"               # optional

[restic]                                      # global defaults
repository  = "/srv/restic"
password    = "${RESTIC_PASSWORD}"
keep-daily  = 7
keep-weekly = 4
keep-monthly = 12
keep-yearly = 2

[backup.main_db]                              # label = "main_db"
cron       = "@daily"
container  = "postgres-main"
database   = "app"
username   = "postgres"     # optional, default postgres
port       = 5432           # optional, default 5432
host       = "localhost"    # optional, default localhost
restic_tag = "main_db"      # optional, default = label
roles      = false          # optional, default false
# any [restic] key may be repeated here to override it:
# repository = "..."; keep-daily = 14
```

Rules:

- `${VAR}` is expanded in **every string value** after TOML parsing (not on raw text). Unset variable = startup error. `${VAR:-default}` is supported.
- Included files may define any section; duplicate sections (other than distinct `backup.<name>`) or duplicate backup names are errors. `include` inside included files is ignored (no recursion).
- Backup label: `^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`.
- `container`, `database`, `username`, `host` must not start with `-` (prevents option injection into `docker exec` / `pg_dump`).

## 5. Data model (SQLite)

```sql
CREATE TABLE activities (
  id            TEXT PRIMARY KEY,                      -- UUID v7
  backup_name   TEXT NOT NULL,
  database_name TEXT NOT NULL,
  triggered_by  TEXT NOT NULL CHECK (triggered_by IN ('schedule','manual')),
  status        TEXT NOT NULL CHECK (status IN ('running','success','error')),
  started_at    TEXT NOT NULL,                         -- RFC 3339 UTC
  finished_at   TEXT,
  error         TEXT,
  log_path      TEXT NOT NULL
) STRICT;
CREATE INDEX idx_activities_backup_started ON activities(backup_name, started_at DESC);
CREATE INDEX idx_activities_started        ON activities(started_at DESC);

CREATE TABLE sessions (
  id_hash    TEXT PRIMARY KEY,                         -- sha256 of cookie value
  subject    TEXT NOT NULL,
  email      TEXT, name TEXT,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
) STRICT;

CREATE TABLE auth_states (                              -- login in progress
  state          TEXT PRIMARY KEY,
  nonce          TEXT NOT NULL,
  pkce_verifier  TEXT NOT NULL,
  redirect_to    TEXT NOT NULL,
  expires_at     TEXT NOT NULL
) STRICT;
```

Duration is **derived** (`finished_at - started_at`), never stored.

## 6. Execution flow

1. Trigger (cron tick or `POST /backups/{name}/run`) -> `Runner.Run(ctx, name, trigger)`.
2. Acquire per-backup lock (fail fast if held).
3. Insert activity `running`, open log file, publish `job.started`.
4. Phase `dump`: `docker exec ... pg_dump` -> `<tmp>/<db>.sql`; verify exit code and non-empty file.
5. Phase `roles` (if enabled): `docker exec ... pg_dumpall --roles-only` -> `<tmp>/<db>_roles.sql`.
6. Phase `restic` (phase 4): ensure repo, `restic backup` (one snapshot), `restic forget --prune` per retention.
7. Always: delete temp dir; update activity (`success`/`error`, `finished_at`, `error`); publish `job.finished`; on error send Telegram notification.

Commands run via `exec.CommandContext` with an **argv slice (no shell)**; stdout is wired directly to the output file, stderr to the execution log. No `-i`/`-t` on `docker exec` (a TTY would corrupt the dump).

## 7. Cross-cutting

- Logging: `log/slog` JSON (app log, stdout). Execution logs are separate files.
- Secrets: `domain.Secret` type redacts on `String()`/`slog.LogValue`/JSON. Restic password passed via env `RESTIC_PASSWORD`, never argv. Telegram token stripped from HTTP errors.
- Testing: stdlib `testing`, table tests, fake `Executor`/`Notifier`/`Restic` implementations, in-memory SQLite for store tests, `httptest` for API.
- Lint/CI: `golangci-lint`, `go vet`, `go test -race ./...`.
- Dependencies: `chi/v5`, `modernc.org/sqlite`, `robfig/cron/v3`, `pelletier/go-toml/v2`, `google/uuid`, `pressly/goose/v3`, `coreos/go-oidc/v3`, `golang.org/x/oauth2`.

## 8. Phases

| Phase | Scope                                                                        | Doc                          |
| ----- | ---------------------------------------------------------------------------- | ---------------------------- |
| 1     | Project setup, domain interfaces, API skeleton, SQLite entities + migrations | [phase-1](phases/phase-1.md) |
| 2     | Config loading/validation, Telegram failure notifications                    | [phase-2](phases/phase-2.md) |
| 3     | Backup runner, docker exec, scheduler, activity + logs                       | [phase-3](phases/phase-3.md) |
| 4     | Restic integration                                                           | [phase-4](phases/phase-4.md) |
| 5     | REST endpoints, SSE, OIDC                                                    | [phase-5](phases/phase-5.md) |
| 6     | Dockerfile, compose, CI                                                      | [phase-6](phases/phase-6.md) |

## 9. Out of scope (v1)

Restore workflows, hot config reload, multi-user roles/RBAC, activity retention, non-Docker targets, Prometheus metrics (easy add-on later), frontend.
