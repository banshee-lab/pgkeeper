# pgkeeper – Backend Spec

Developer / maintainer guide for the Go backend. Based on the repo at `banshee-lab/pgkeeper` (HEAD `39b8fbb`).

## 1. What it is

A single Go binary that:

1. Schedules PostgreSQL dumps (`pg_dump` run **inside an existing Docker container** via `docker exec`).
2. Stores the dumps as **restic** snapshots.
3. Records every run ("activity") in **SQLite**, with one log file per run.
4. Exposes a REST API + SSE streams, optional OIDC auth, and serves the embedded SvelteKit frontend.
5. Can restore snapshots (test restore in an ephemeral container, or restore into a live container).
6. Runs scheduled restic maintenance (`prune`, `check`) and sends Telegram alerts on failure.

The backend runs without the frontend (CI builds with an empty `web/build/`).

| Item | Value |
| --- | --- |
| Module | `github.com/banshee-lab/pgkeeper` |
| Go | `1.27.3` (`go.mod`) |
| Router | `go-chi/chi/v5` (+ `httprate`) |
| DB | SQLite via `modernc.org/sqlite` (pure Go, no CGO), migrations via `pressly/goose/v3` |
| Config | `pelletier/go-toml/v2` (strict) |
| Cron | `robfig/cron/v3` |
| Auth | `coreos/go-oidc/v3` + `golang.org/x/oauth2` |
| Runtime deps (external binaries) | `docker` CLI, `restic` (>= 0.16, needed for `--retry-lock`) |

## 2. Repository layout

```
cmd/pgkeeper/main.go      CLI flags, logging, signal handling, calls app.Run
internal/
  app/                    Wiring + lifecycle (app.go), SIGHUP reload (reload.go), -check-restic (checkrestic.go)
  config/                 TOML load/merge/expand/validate, Holder (atomic swap on reload)
  domain/                 Core types + ALL interfaces (ports). No implementation logic.
  backup/                 Runner (backup + restore orchestration), JobTracker, repoGate, roles rewriting
  executor/               docker CLI wrapper: pg_dump, psql, ephemeral containers
  restic/                 restic CLI wrapper, env building, secret redaction, snapshot browsing
  maintenance/            Scheduled/manual prune + check + unlock per repository
  scheduler/              robfig/cron wiring for backups, test restores, maintenance
  store/                  SQLite persistence (activities, repository_activities, sessions, snapshots)
  logstore/               Per-run log file writer, line redaction, ReadLines
  events/                 Generic in-memory pub/sub Hub + JobEvent types
  notify/                 Telegram notifier, Noop, Multi, message formatting
  auth/                   OIDC service, sessions, cookies (+ authtest fakes)
  api/                    HTTP handlers, router, DTOs, SSE, same-origin check, errors
  reload/                 PID file + SIGHUP sender for `pgkeeper -reload`
migrations/               Goose SQL migrations (embedded via migrations/embed.go)
schemas/pgkeeper.schema.json   JSON schema for the TOML config (editor support)
docs/                     backend.md, environment.md, production.md, openapi.yaml
examples/pgkeeper.toml    Full example config
local/pgkeeper.toml       Dev config used by compose.yaml (paths under /tmp/pgk)
web/                      SvelteKit frontend (see frontend_spec.md); web/embed.go embeds web/build
Dockerfile, compose.yaml, Makefile, .github/workflows/
```

## 3. Where to find things (quick map)

| Topic | Location |
| --- | --- |
| Process entry, CLI flags | `cmd/pgkeeper/main.go` |
| Dependency wiring, startup/shutdown order | `internal/app/app.go` |
| **Config parsing** | `internal/config/` – `load.go` (read, include, strict decode), `env.go` (`${VAR}` expansion), `raw.go` (TOML shapes), `resolve.go` (defaults, merging global→per-backup, validation), `validate.go` (regexes, cron parser, safety checks), `holder.go` (hot-reload swap, restart-required keys) |
| Config structs used at runtime | `internal/domain/backup.go` (`BackupConfig`, `ResticConfig`, `GlobalConfig`), `domain/maintenance.go` |
| **Backup flow** | `internal/backup/runner.go` (`Run` → `start` → `execute` → `runPhases` → `dump` → `resticPhase`) |
| Concurrency / overlap prevention | `internal/backup/jobs.go` (`JobTracker`, one job per backup name), `repogate.go` (FIFO per-repository lock) |
| **pg_dump / docker exec** | `internal/executor/docker.go` (`buildArgs`, `Dump`, `Check`) |
| **Restic** | `internal/restic/cli.go` (all commands), `env.go` (env allowlist), `redact.go`, `summary.go` (parse `backup --json`), `diff.go` (snapshot stats) |
| **Restore** | `internal/backup/restore.go` (flow), `restore_roles.go` (CREATE ROLE rewrite), `internal/executor/restore.go` (docker run/psql/rm) |
| **Scheduled prune/check/unlock** | `internal/maintenance/maintenance.go` |
| Cron schedules | `internal/scheduler/cron.go` |
| **HTTP endpoints** | `internal/api/router.go` (routes), `handler.go`, `repositories.go`, `restore.go`, `auth_handlers.go`, `stream.go` + `sse.go` |
| API DTOs / JSON shape | `internal/api/dto.go` |
| API errors | `internal/api/errors.go` (codes must match `docs/openapi.yaml`) |
| Same-origin (CSRF) check | `internal/api/origin.go` |
| Auth (OIDC) | `internal/auth/service.go` |
| DB schema | `migrations/*.sql`; access code in `internal/store/` |
| Notifications | `internal/notify/telegram.go`, `message.go` |
| Log files + redaction | `internal/logstore/` |
| Config reload | `internal/app/reload.go`, `internal/reload/reload.go` |
| API contract | `docs/openapi.yaml` |

## 4. Architecture

### 4.1 Design rules (from `.github/copilot-instructions.md` + code)

- **Interfaces live in `internal/domain/ports.go`** (`Executor`, `ResticClient`, `Notifier`, `ActivityRepository`, `Scheduler`, `BackupCatalog`, …) plus `restore.go`, `repository.go`, `maintenance.go`. Concrete types are wired in `internal/app`. Tests use fakes.
- **Single config resolution path:** `config.Load` does expansion → include merge → strict decode → defaults → validation. Don't add parallel parsing. Update `examples/pgkeeper.toml` and `schemas/pgkeeper.schema.json` when config keys change.
- **External programs:** always `exec.CommandContext` with argument slices, never a shell. Keep secrets out of argv and logs: use `domain.Secret` (prints/marshals as `***`) and the redaction helpers.
- **SQLite schema changes** = new Goose migration in `migrations/` (never edit existing ones).
- **API changes:** keep `docs/openapi.yaml` in sync. JSON is `snake_case`; errors use the envelope `{"error":{"code","message"}}`.
- Restic integration tests carry the `integration` build tag.

### 4.2 Startup sequence (`app.Run`)

1. `store.Open` (SQLite, WAL, `MaxOpenConns=1`) → `store.Migrate` (goose, embedded).
2. Mark leftover `running` rows (activities and repository activities) as interrupted.
3. Docker check (warn-only), `CleanupOrphans` (removes containers labelled `pgkeeper.restore=true`), restic version check (warn-only).
4. Create event hubs (`JobHub`, `LogHub`), `config.Holder`, `backup.Runner`.
5. If `-run NAME`: run that one backup synchronously and exit (0 = success).
6. If `[auth]` set: `auth.New` (OIDC discovery; **fails startup** if unusable) + hourly session cleanup.
7. Create `maintenance.Runner`, `scheduler.Cron` (backups, test restores, maintenance), start it.
8. Start SIGHUP watcher, write PID file `<temp_path>/pgkeeper/.pgkeeper.pid`.
9. Build `api.Handler`/router, `ListenAndServe` on `global.listen`.
10. Shutdown (SIGINT/SIGTERM): stop scheduler → maintenance → runner (waits up to 30s, then cancels) → cancel SSE base context → HTTP shutdown (10s).

### 4.3 CLI flags (`cmd/pgkeeper/main.go`)

| Flag | Behavior |
| --- | --- |
| `-config PATH` | Config file (default `$PGKEEPER_CONFIG` or `/etc/pgkeeper/pgkeeper.toml`) |
| `-check` | Validate config, print redacted summary (`Config.LogSafe`), exit |
| `-check-restic` | `restic cat config` for every distinct repo (never runs `init`), exit |
| `-reload` | Validate config, then SIGHUP the running daemon via PID file |
| `-notify-test` | Send a Telegram test message, exit |
| `-run NAME` | Run one backup now and exit |

## 5. Configuration

Strict TOML (unknown keys are errors). `${NAME}` is required; `${NAME:-default}` falls back when unset **or empty**. Expansion happens on string values after parsing, in the main file and includes. `.env` files are **not** loaded by pgkeeper.

**Includes:** `global.include = ["conf/*.toml"]` (globs relative to the main file). Included files cannot include further. A section (`[global]`, `[telegram]`, `[auth]`, `[restic]`, `[restore]`) may be defined in only one file; each `[backup.NAME]` must be unique across files.

### Sections and keys

| Section | Keys |
| --- | --- |
| `[global]` | `timezone` (IANA, default system local), `include`, `temp_path` (`/var/tmp`), `log_path` (`/var/log`), `listen` (`:8080`), `db_path` (`/var/lib/pgkeeper/pgkeeper.db`). Directories are created and write-checked at load. |
| `[telegram]` | `bot_token`, `chat_id` (both required if section present) |
| `[auth]` | `issuer`, `client_id`, `secret`, `redirect_url` (required, issuer/redirect must be absolute URLs); `scopes` (default `openid profile email`), `session_ttl` (default `12h`), `cookie_secure` (default `true`), `post_login_redirect` (default `/`) |
| `[restic]` | `repository`, `password`, `keep-daily/weekly/monthly/yearly`, `skip-prune`, and global-only `prune-cron`, `check-cron`, `check-read-percent` (1–100, default 80) |
| `[restore]` | `restore_image` (default image for test restores) |
| `[global]` also: `tools-image` (image with pg_dump/pg_dumpall/psql for remote databases) |
| `[backup.NAME]` | `cron` (required) and either `container` + `database`, or `database-url` (mutually exclusive with `container`/`host`/`port`/`username`; `database` optional); `username` (`postgres`), `host` (`localhost`), `port` (`5432`), `restic_tag` (defaults to NAME), `roles` (`false`), `restore_cron`, `restore_image`; per-backup restic overrides: `repository`, `password`, `keep-*`, `skip-prune` |

Validation highlights (`resolve.go`/`validate.go`): backup name regex `^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`; cron parsed with standard 5-field + descriptors (`@daily`, `@every 1m`); container/database/user/host must not start with `-` or contain NUL/newline; tag must have no whitespace/commas; `restore_cron` needs an image (project or global); warning when two backups target the same container/database; warning for `skip-prune` without `prune-cron`.

Merge rule: per-backup restic values override `[restic]` via `firstSet` (pointer-based, so `0` is a valid override).

**Hot reload** (SIGHUP / `pgkeeper -reload`): swaps backups, cron schedules, retention, restic settings and maintenance schedules. Needs a restart (logged, ignored): `global.*`, `[telegram]`, `[auth]`, env vars (`config.RestartRequired` / `KeepRunning`). Runs in progress finish with old settings.

Env vars used by the binary: only `PGKEEPER_CONFIG`. Restic subprocesses inherit `PATH, HOME, TMPDIR` and any `AWS_*, AZURE_*, B2_*, GOOGLE_*, RESTIC_*, RCLONE_*` (see `restic/env.go`); `RESTIC_REPOSITORY/PASSWORD/CACHE_DIR` are always overridden from config.

## 6. Backup lifecycle (`internal/backup/runner.go`)

Trigger: cron tick (`scheduler.tick`, `TriggerSchedule`) or `POST /api/v1/backups/{name}/run` (`TriggerManual`).

`Runner.start`:
1. Reject during shutdown (`ErrShuttingDown` → 503).
2. `JobTracker.TryStart` – **one job per backup name** (backup *or* restore). Conflict → `ErrBackupRunning`/`ErrRestoreRunning` (API 409; scheduled ticks are skipped with a warning).
3. Insert `activities` row (`status=running`, log path `<log_path>/<safe database>/<uuid>.log`; IDs are UUIDv7).
4. Announce job on `JobHub`, run `execute` in a goroutine.

`execute` → `runPhases`:
1. Private temp dir `<temp_path>/pgkeeper/<backup name>` (0700, recreated, always removed at end).
2. **dump** phase: `docker exec <container> pg_dump --no-owner --disable-triggers --format=plain -d <db> --host --port --username --no-password` → `<db>.sql` (`O_EXCL`, 0600). Empty output = error.
3. **roles** phase (only if `roles = true`): `pg_dumpall --roles-only` → `<db>_roles.sql`.
4. **queued → restic** phase (`resticPhase`): acquire per-repository FIFO gate (`repogate.go`) → `EnsureRepo` (`cat config`, `init` if missing) → `Backup` (`restic backup --json --tag <tag> --host <backup name>`) → record snapshot summary in `backup_snapshots` → `Forget` (`forget --tag --host --keep-* [--prune]`, skipped if no keep values).
5. Final status persisted with retries (`FinishAttempts=3`); `JobFinished` (or `JobAbandoned` if persist failed) published; on error, Telegram failure notification with the log tail.

Notes:
- Dumps run in parallel across backups; only the restic phase is serialized per repository (same gate as prune/check/restore fetch).
- Restic identifies a project's snapshots by **tag = `restic_tag`** and **host = backup name**. Changing either orphans old snapshots from the restore listing.
- `docker exec` is invoked **without** a password; PostgreSQL auth must already work inside the container (e.g. `.pgpass`/trust).
- Phases are defined in `domain/types.go`: `queued, dump, roles, restic, cleanup, snapshot, provision, restore`.
- Restic commands use `--retry-lock 5m`; exit code 11 / "already locked" is classified by `ExitError.Locked()`. Stale locks are never removed automatically.

## 7. Restic layer (`internal/restic/`)

`CLI` implements `domain.ResticClient` and `domain.RepositoryInspector` (and `ResticMaintainer`).

| Method | restic command |
| --- | --- |
| `Check` | `cat config` (exists / missing / error) |
| `EnsureRepo` | `Check` then `init` |
| `Backup` | `backup --json` (summary parsed in `summary.go`) |
| `Forget` | `forget --tag --host --keep-* [--prune]` |
| `Prune` | `prune` |
| `CheckData` | `check --read-data-subset=N%` (or `--read-data` at 100) |
| `Unlock` | `unlock --remove-all` |
| `RepositoryStats` | `stats --json --mode raw-data` |
| `RepositorySnapshots` | `snapshots --json` |
| `RepositorySnapshotStats` | `diff --json <parent> <snap>` |
| `RepositoryTree` | `ls --json` |
| `DownloadSnapshotFile` | `dump` (streamed to the HTTP response or a file) |
| `CheckVersion` | `version`; rejects < 0.16 |

Other details:
- Cache dir: `<temp_path>/pgkeeper/.restic-cache`.
- Per-repository in-process mutex in `CLI.lock` for restic calls.
- Secrets redacted from output (`redact.go`, `secretsFor`: repo password, URL-embedded password, any env var whose name contains `SECRET/PASSWORD/TOKEN/KEY`). `RedactRepo` strips credentials from URLs for logs/API.
- Snapshot IDs and paths from API input are validated (`validSnapshotID`, `validSnapshotPath`, `ValidateSnapshotFile`).
- **Repository ID** = first 16 bytes of SHA-256 of the repository string, hex (`domain.RepositoryID`). Repositories shared by several backups appear once; `is_main` marks the global `[restic]` repo.

## 8. Restore (`internal/backup/restore.go`, `internal/executor/restore.go`)

Both modes are *activities* (`kind = test_restore | restore`) and share the one-job-per-project lock with backups.

Flow (`runRestore`): **snapshot** (resolve; pick latest for test restore) → **restic** (queue on the gate, locate file via BFS over `restic ls` up to 200 dirs, `restic dump` into `<temp>/pgkeeper/restore/<name>-<id8>/`) → [**provision**, test only] → [**roles**] → **restore** → cleanup.

- **Test restore** (`POST …/restore/test`, or `restore_cron`): `docker run --detach --rm --network none --label pgkeeper.restore=true -e POSTGRES_HOST_AUTH_METHOD=trust <image>` named `pgkeeper-restore-<name>-<id8>`, wait for `pg_isready` (90 s), load roles (if the snapshot has them) and database, always `docker rm --force --volumes`. Needs `restore_image`.
- **Container restore** (`POST …/restore/container`): target container/database default to the project's. **Drops and recreates the target database** (terminates connections first). `include_roles` requires a roles dump in the snapshot.
- Roles script is rewritten (`restore_roles.go`): each `CREATE ROLE x;` becomes a `DO $pgkeeper$ … IF to_regrole(...) IS NULL THEN CREATE ROLE … ELSE RAISE NOTICE … END IF` block, so existing roles are kept and the following `ALTER ROLE` overwrites attributes/passwords.
- **Restore to database URL**: `POST …/restore/container` with `database_url` (exclusive with `container`) runs `docker run --rm -i --entrypoint psql …` from `tools-image`; connection via `PG*` env vars, never arguments. Rejected with 400 when `tools-image` is unset.
- Input validation regexes: `snapshotIDRe` (8–64 hex), `containerRe`, `dbNameRe`.
- psql invoked as `docker exec -i <container> psql -X -q -v ON_ERROR_STOP=1 --no-password …`.
- Startup `CleanupOrphans` removes leftover labelled containers after a crash.

## 9. Scheduler and maintenance

**Scheduler** (`scheduler/cron.go`): one `robfig/cron` instance in `global.timezone`. Entries: backup `cron`, test restore `restore_cron`, maintenance `prune-cron` / `check-cron`. `Reload` validates everything first (all-or-nothing), keeps unchanged entries, replaces changed ones. `NextRun` / `NextMaintenance` feed the API.

**Maintenance** (`maintenance/maintenance.go`): operates on every *distinct* repository (global + per-backup overrides; first definition's password wins), one at a time, via the same per-repo gate as backups. Missing repositories are skipped. Failures are logged + notified; one failing repo doesn't stop the others. A task whose previous run is still going is skipped. Manual triggers go through `TriggerRepository` and create `repository_activities` rows (kinds `prune`, `check`, `unlock`) with their own log files and SSE log stream.

Retention after each backup runs `forget --prune` unless `skip-prune = true` (then only `prune-cron` reclaims space).

## 10. HTTP API (`internal/api/`)

Server: `api.NewServer` (no WriteTimeout – SSE; `ReadHeaderTimeout` 10s). Middleware: RequestID, RealIP, request logger, Recoverer, 30 s `Timeout` on non-SSE routes, **no compression** (breaks SSE). Auth endpoints rate-limited to 20 req/min/IP (`429 rate_limited`).

Static frontend: any unmatched `GET`/`HEAD` outside `/api`, `/auth`, `/healthz` is served from the embedded `web/build` with SPA fallback to `/` (`router.go` `NotFound`). Unknown `/api/*` returns JSON 404.

### Routes

| Method & path | Handler | Notes |
| --- | --- | --- |
| `GET /healthz` | `Health` | Pings SQLite; public |
| `GET /auth/login`, `GET /auth/callback` | `AuthLogin`, `AuthCallback` | OIDC code+PKCE flow, rate-limited |
| `POST /auth/logout` | `AuthLogout` | same-origin required |
| `GET /api/v1/me` | `Me` | When auth is off returns `authenticated: true`, `auth_enabled: false` |
| `GET /api/v1/backups`, `GET …/{name}` | `ListBackups`, `GetBackup` | includes `running`, `next_run`, `last_success_at`, `last_activity`, `latest_snapshot`, `timezone` |
| `GET …/backups/{name}/activities` | `ListBackupActivities` | paginated |
| `POST …/backups/{name}/run` | `RunBackup` | same-origin; 202 / 404 / 409 / 503 |
| `GET …/backups/{name}/restore/snapshots` | `ListRestoreSnapshots` | newest first, `has_roles` |
| `POST …/backups/{name}/restore/test` | `RunTestRestore` | same-origin |
| `POST …/backups/{name}/restore/container` | `RunContainerRestore` | same-origin; JSON body `{snapshot_id, container, database, database_url, include_roles}`, 4 KB cap, unknown fields rejected |
| `GET /api/v1/activities`, `GET …/{id}` | `ListActivities`, `GetActivity` | filters: `limit` (1–100, default 10), `status`, `since` (RFC3339), `cursor` |
| `GET …/activities/{id}/log` | `GetActivityLog` | `?download=1` for attachment |
| `GET …/activities/{id}/log/stream` | `StreamActivityLog` | SSE: events `log`, `end`; supports `Last-Event-ID` |
| `GET …/jobs`, `GET …/jobs/stream` | `ListJobs`, `StreamJobs` | SSE events: `snapshot`, `job.started`, `job.phase`, `job.finished` |
| `GET …/repositories`, `GET …/{id}` | `ListRepositories`, `GetRepository` | detail adds restic stats (size, snapshot count) |
| `GET/POST …/repositories/{id}/activities` | `ListRepositoryActivities`, `RunRepositoryActivity` | POST body `{"kind":"prune"|"check"}`, same-origin |
| `GET …/repositories/{id}/activities/{activity}/log` and `/log/stream` | `RepositoryActivityLog`, `StreamRepositoryActivityLog` | |
| `POST …/repositories/{id}/unlock` | `ForceRepositoryUnlock` | same-origin; 409 if pgkeeper itself uses the repo |
| `GET …/repositories/{id}/snapshots` | `RepositorySnapshots` | |
| `GET …/snapshots/{snapshot}/summary` | `RepositorySnapshotStats` | via `restic diff` against parent |
| `GET …/snapshots/{snapshot}/tree?path=` | `RepositoryTree` | |
| `GET …/snapshots/{snapshot}/download?path=` | `DownloadSnapshotFile` | streams one file |

### Conventions

- **Errors:** `{"error":{"code","message"}}`. Codes in `errors.go`: `invalid_request, unauthenticated, backup_not_found, activity_not_found, log_not_found, backup_running, restore_running, internal_error, not_implemented, not_found, method_not_allowed, unavailable, rate_limited`.
- **Auth:** with `[auth]`, `/api/v1/*` requires cookie `pgkeeper_session` (HttpOnly, SameSite=Lax, `Secure` per `cookie_secure`). Session IDs are stored **hashed** (SHA-256) in SQLite. **Without `[auth]` the API is fully open.**
- **Same-origin check** (`origin.go`): mutating routes compare `Origin` (or `Referer`) with request `Host` (http or https). Reverse proxies must preserve `Host`, `Origin`, `Referer`.
- **SSE** (`sse.go`, `stream.go`): `retry: 3000` first, 15 s heartbeat, closed on session expiry and on `JobAbandoned`; log streams replay from file then follow the hub, refilling gaps if a slow client was dropped.
- **Handler dependencies** that are optional (`Restorer`, `Scheduler`, `Snapshots`, repo deps) produce `503 unavailable` or `null` fields when absent – useful for tests.
- **Known doc drift:** `.github/copilot-instructions.md` and `README.md` reference `docs/api.md` and `docs/plan.md`, which are not in the repo. `docs/openapi.yaml` is the only API contract.

## 11. Persistence (SQLite)

Open pragmas: WAL, `busy_timeout=5000`, `foreign_keys=1`, `synchronous=NORMAL`, `MaxOpenConns=1`. Timestamps stored as UTC text (`2006-01-02T15:04:05.000000000Z07:00`).

| Table | Migration | Purpose |
| --- | --- | --- |
| `activities` | 0001 (+0005 adds `kind`) | backup/restore runs: `id, backup_name, database_name, kind, triggered_by, status, started_at, finished_at, error, log_path` |
| `sessions` | 0001 | OIDC sessions (`id_hash`, subject, email, name, expiry) |
| `auth_states` | 0001 | transient login state (state, nonce, PKCE verifier, redirect) |
| `backup_snapshots` | 0003 | latest snapshot per backup (id, taken_at, files_added/changed, bytes_added) |
| `repository_activities` | 0004 | prune / check / unlock runs |

Indexes: 0002 adds expiry indexes. There is **no retention** for activity history. Activities left `running` by a dead process are set to interrupted at startup.

## 12. Logging, events, notifications

- **App logs:** JSON (`slog`) on stdout.
- **Execution logs:** one file per activity under `log_path` (format `<RFC3339> LEVEL message`); subprocess stderr passes through a line redactor (restic password and URL credentials → `***`). `logstore.ReadLines` serves the API; `logstore.Writer` also publishes lines to `LogHub` for live streams.
- **Events:** `events.Hub[T]` is a generic in-memory fan-out (slow subscribers can drop events – SSE code compensates). `JobEvent{Seq,Type,Job,Activity}` with strictly increasing `Seq`.
- **Notifications:** failures of backups, restores, and maintenance → `Notifier.NotifyFailure`. Implementations: `Telegram` (with retry/backoff, token redaction), `Noop`, `Multi`.

## 13. Build, run, test

```sh
make build                      # builds web (yarn build) then go build -> bin/pgkeeper
make run CONFIG=./local/pgkeeper.toml
make test | race | vet | lint | check
make integration                # restic integration test, needs restic on PATH
go test ./internal/config -run '^TestDefaults$'   # single test
```

- `go build` requires `web/build/` to exist (`//go:embed all:build`). For backend-only work: `mkdir -p web/build && touch web/build/.gitkeep` (this is what CI does).
- Tests are plain Go tests with fakes (`api`, `backup`, `config`, `maintenance`, `restic`, `store`, `scheduler`, `notify`, …). `go.uber.org/goleak` is used for goroutine-leak checks. `internal/auth/authtest` provides a fake IdP and session store.
- **Dev stack:** `docker compose up --build` (`compose.yaml`) starts `pg-test` (postgres:17-alpine), fixes volume permissions, and runs pgkeeper with `local/pgkeeper.toml` (`@every 1m` backup, `skip-prune` + `prune-cron` every 5 min, no `[auth]`). Set `DOCKER_GID` (default 998) to the host docker socket group (`stat -c '%g' /var/run/docker.sock`).
- **Docker image:** multi-stage (Node build → Go build with `CGO_ENABLED=0` → `alpine:3.22` + `docker-cli restic tzdata`), runs as UID 10001, entrypoint `pgkeeper`, env `PGKEEPER_CONFIG=/etc/pgkeeper/pgkeeper.toml`, volume `/var/lib/pgkeeper`. Needs `group_add` for the docker socket GID.
- **CI:** `.github/workflows/pr_test.yml` (Go tests with an empty `web/build`, and frontend tests), `publish.yml` (builds/pushes the image to `ghcr.io/banshee-lab/pgkeeper`).

## 14. Security notes for maintainers

- The Docker socket gives pgkeeper control of the host daemon; only deploy on trusted hosts.
- Never put secrets in argv; restic gets them through environment variables only.
- All user-controlled identifiers that reach a command line or SQL (container, database, snapshot ID, paths, role names) are regex-validated; the restore SQL embeds database names in quotes/literals, so keep `dbNameRe` strict.
- Auth fails closed at startup (bad OIDC config aborts), but a missing `[auth]` section means an open API.
- Logout and every state-changing endpoint depend on the same-origin check, not CSRF tokens.

## 15. Common change recipes

| Task | Touch |
| --- | --- |
| New config key | `config/raw.go` → `resolve.go` (default/validation) → `domain` struct → consumer → `examples/pgkeeper.toml`, `schemas/pgkeeper.schema.json`, `docs/backend.md`, tests in `config/config_test.go` |
| New API endpoint | `api/router.go`, handler, DTO in `dto.go`, error mapping, `docs/openapi.yaml`, contract tests (`api/contract_test.go`, `contract_body_test.go`) and frontend `web/src/lib/api/` |
| New DB column/table | new `migrations/000N_*.sql` (+ store code + domain type) |
| New backup phase | `domain.Phase`, `Runner.runPhases`, `setPhase`, frontend `Phase` type in `web/src/lib/api/types.ts` |
| New notifier | implement `domain.Notifier`, wire in `cmd/pgkeeper/main.go` (use `notify.Multi` for several) |
| New restic operation | method on `restic.CLI` with an argv builder (+ unit test in `cli_test.go`), expose through a `domain` interface |

## 16. Known inconsistencies spotted while reading the code

- `examples/pgkeeper.toml` uses `restic_keep-daily = 14` under `[backup.reports]`. The strict parser (`rawBackup`) only accepts `keep-daily`, so this key would be rejected as unknown. Verify with `pgkeeper -check` before reusing the example.
- In `resticPhase` the snapshot is stored and recorded in `backup_snapshots` *before* `Forget` runs, so a failed retention step marks the activity `error` even though the snapshot exists.
- `handler.go` still contains an older `parseLimit` (max 10) that is unused; the active paginated handlers use `parsePageLimit` (max 100).
- `README.md` / copilot instructions link to missing `docs/api.md` and `docs/plan.md`.
