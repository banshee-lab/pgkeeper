# Backend

pgkeeper is a Go service that schedules PostgreSQL dumps from Docker containers,
stores snapshots in restic, records executions in SQLite, and exposes a REST API
and server-sent event (SSE) streams. The backend can run without the web
frontend.

## Architecture

`cmd/pgkeeper` parses command-line options, loads configuration, configures
logging and signal handling, and starts the application. `internal/app` wires
the implementations together, applies SQLite migrations, and manages startup
and graceful shutdown.

The main runtime components are:

| Package | Responsibility |
| --- | --- |
| `internal/config` | Strict TOML loading, include merging, environment expansion, defaults, and validation |
| `internal/domain` | Core types and interfaces shared by implementations |
| `internal/store` | SQLite persistence for backup activities, OIDC sessions, and login state |
| `internal/scheduler` | Cron scheduling for configured backups |
| `internal/backup` | Asynchronous execution, overlap prevention, phase tracking, and shutdown |
| `internal/executor` | PostgreSQL dump commands executed through the Docker CLI |
| `internal/restic` | Restic CLI invocation, repository checks, snapshots, retention, and secret redaction |
| `internal/logstore` and `internal/events` | Per-execution log files, live log tails, in-memory event streams, and log redaction |
| `internal/notify` | Failure notifications, including Telegram |
| `internal/auth` | Optional OIDC login and server-side session handling |
| `internal/api` | HTTP routes, handlers, authentication middleware, SSE streams, and origin enforcement |

Application logs are structured JSON written to standard output. Execution logs
are separate files under `global.log_path`. Subprocess stderr (pg_dump output)
is passed through a line-level redactor before being written to the log file;
secrets known to pgkeeper (the restic repository password and any URL-embedded
credentials) are replaced with `***`. PostgreSQL passwords are managed inside
the containers and are not supplied to pgkeeper.

## Backup lifecycle

At startup, pgkeeper migrates the SQLite database and marks activities left in
`running` state by a previous process as interrupted. It checks Docker and
restic availability and logs warnings if those checks fail; these checks do not
prevent startup.

A scheduled tick or manual API request starts a backup asynchronously. Only one
run per configured backup can execute at a time; a conflicting manual request
returns `409`, while an overlapping scheduled tick is skipped. Each run:

1. Creates an activity record and execution log.
2. Runs `pg_dump` inside the configured container using `docker exec`. If
   `roles = true`, it also runs `pg_dumpall --roles-only`.
3. Writes dump files to a private per-backup directory under
   `global.temp_path`; empty dumps are treated as errors.
4. Waits its turn for the restic repository (see below), ensures it exists,
   creates one snapshot for the run, records the snapshot summary, then applies
   configured retention when at least one retention value is set.
5. Removes temporary dump files, records the final activity status, and
   publishes completion events. On failure it also attempts a Telegram
   notification if Telegram is configured.

Backups that share a restic repository take turns: the restic phase (repository
check/init, snapshot and retention) of runs targeting the same repository is
queued in FIFO order, while their dumps still run in parallel. A waiting run
reports the `queued` phase and logs which backup it waits for; it is neither
skipped nor failed. Restic commands also wait up to 5 minutes for repository
locks held by other processes (for example a prune started elsewhere) before
failing with a "repository is locked by another process" error.

After each successful snapshot pgkeeper stores its id, timestamp, added and
changed file counts and added bytes per backup (taken from `restic backup
--json`). The latest one is returned as `latest_snapshot` (null until the first
snapshot) by the backup list and detail endpoints and shown on the project
details page.

Commands are invoked directly with argument lists, not through a shell.
PostgreSQL authentication must already work non-interactively inside each
container (for example via `.pgpass`); pgkeeper does not manage database
passwords. Restic runs in the pgkeeper process environment and uses
`RESTIC_PASSWORD` for its repository password.

The scheduler timezone is `global.timezone` (system local time by default).
The resolved IANA timezone name is returned as the `timezone` field in the
backup list and detail responses.

## Scheduled restic maintenance

After every successful snapshot pgkeeper runs `restic forget --prune` for that
backup's tag and host with the configured `keep-*` retention (skipped when none
is set). Set `skip-prune = true` in `[restic]`, or in a single `[backup.*]` to
override it, to forget only: forgetting is fast and just drops snapshot
references, and the space is then reclaimed only by the scheduled prune below.
Use it together with `prune-cron` (a warning is logged at startup and on reload
when `skip-prune` is set without it).

Two optional global tasks are configured in `[restic]` (`prune-cron`,
`check-cron` and `check-read-percent` are not valid inside `[backup.*]`); an
empty cron disables the task:

| Key | Default | Meaning |
| --- | --- | --- |
| `prune-cron` | unset | When to run `restic prune`. |
| `check-cron` | unset | When to run `restic check --read-data-subset=N%`. |
| `skip-prune` | `false` | Skip `--prune` in the forget run after each backup (also valid per backup). |
| `check-read-percent` | `80` | Share of pack data read back by the check (1-100; 100 uses `--read-data`). |

Both tasks cover every distinct repository: the global `[restic]` repository
and each per-backup `repository` override. A repository is handled once per
run however many backups use it (the first definition, and so its password,
wins), one repository at a time. A repository that does not exist yet is
skipped; failures are logged and sent through the configured notifier, and one
failing repository does not stop the others.

Locking: prune and check take the same per-repository queue as the restic
phase of backups, so they wait for running backups and backups wait for them
(a backup can therefore stay `queued` for as long as a check runs; schedule
them off-hours). Across processes restic waits up to 5 minutes for locks held
elsewhere (`--retry-lock 5m`) and then fails with the "locked by another
process" error. Stale locks are never removed automatically. A task whose
previous run is still going is skipped, and shutdown interrupts a running task
so restic releases its lock.

## Reloading the configuration

Run `pgkeeper -reload` (with the same `-config`/`PGKEEPER_CONFIG`) to make the
running service re-read its file. The file is validated first; an invalid one
is reported and nothing is signalled. The command sends `SIGHUP` to the PID in
`<global.temp_path>/pgkeeper/.pgkeeper.pid` (`kill -HUP <pid>` works too) and
the daemon logs `config reloaded` or `config reload failed`; on failure the
running configuration stays active.

A reload applies backups (added, removed, changed) and their cron schedules,
retention and restic settings, and the maintenance schedules. Runs already in
progress finish with the settings they started with. Changes to `global.*`
paths, `listen`, `timezone`, `[telegram]`, `[auth]` and to environment
variables need a restart; they are reported in the log and ignored.

## Configuration and persistence

The default configuration file is `/etc/pgkeeper/pgkeeper.toml`. Use
`-config PATH` or `PGKEEPER_CONFIG` to select another file. Configuration is
strict TOML: unknown keys are errors. `${VAR}` references in TOML string values
are expanded from the process environment; `${VAR:-default}` uses the default
when the variable is unset or empty. See [Environment variables](environment.md)
and [the example configuration](../examples/pgkeeper.toml).

`global.db_path` is the SQLite database path; migrations are embedded in the
binary and applied automatically at startup. `global.log_path` holds one log
file per activity. `global.temp_path` holds temporary dumps and restic cache
data. Activity history has no automatic retention policy.

## HTTP API

The default listen address is `:8080`. `/healthz` is unauthenticated and checks
SQLite connectivity. When `[auth]` is configured, the `/api/v1` routes require
the `pgkeeper_session` cookie created by OIDC login. If `[auth]` is absent, the
API is open; production deployments should configure OIDC and restrict network
access.

`POST /auth/logout`, backup runs, repository maintenance, and force unlock
enforce a same-origin check. The server compares the `Origin` header (or
`Referer` when `Origin` is absent) against the `Host` of the incoming request.
Requests with a missing or cross-origin source header receive `403`. Reverse
proxies must preserve the `Host` header and must not strip `Origin` or
`Referer`.

The API includes:

| Route | Purpose |
| --- | --- |
| `GET /healthz` | Liveness/readiness check |
| `GET /auth/login`, `GET /auth/callback`, `POST /auth/logout` | OIDC session flow |
| `GET /api/v1/me` | Current authentication state |
| `GET /api/v1/backups`, `GET /api/v1/backups/{name}` | Configured backup details, including `timezone` and `last_success_at` |
| `GET /api/v1/backups/{name}/activities` | Paginated activity history for one backup |
| `POST /api/v1/backups/{name}/run` | Start a manual backup |
| `GET /api/v1/activities`, `GET /api/v1/activities/{id}` | Paginated activity list and single-activity detail |
| `GET /api/v1/activities/{id}/log` | Read an activity log; add `?download=1` for a file download |
| `GET /api/v1/activities/{id}/log/stream` | Replay and follow an activity log over SSE |
| `GET /api/v1/jobs`, `GET /api/v1/jobs/stream` | Current jobs and job-event SSE stream |
| `GET /api/v1/repositories`, `GET /api/v1/repositories/{id}` | Distinct configured repositories, next maintenance times, current restic stats |
| `GET /api/v1/repositories/{id}/activities` | Prune, check, and unlock history |
| `POST /api/v1/repositories/{id}/activities` | Start a manual prune or check (`{"kind":"prune"}` or `{"kind":"check"}`) |
| `POST /api/v1/repositories/{id}/unlock` | Force-remove repository locks; requires same-origin and no pgkeeper activity using the repository |
| `GET /api/v1/repositories/{id}/snapshots` | List restic snapshots |
| `GET /api/v1/repositories/{id}/snapshots/{snapshot}/summary` | Derive snapshot stats from `restic diff` |
| `GET /api/v1/repositories/{id}/snapshots/{snapshot}/tree` | Browse a snapshot; accepts an optional `path` query parameter |
| `GET /api/v1/repositories/{id}/snapshots/{snapshot}/download?path=…` | Stream one selected file from a snapshot |
| `GET /api/v1/repositories/{id}/activities/{activity}/log`, `…/log/stream` | Read or follow maintenance console output |

Repository IDs are stable opaque hashes of the configured repository location;
credential-bearing URLs are never returned. Each repository includes `is_main`
to identify the global `[restic]` repository and a sorted `backups` list of
projects that use it. Repositories shared by multiple projects appear once.
Next prune/check times reflect the next configured maintenance schedule.
Unlock uses restic's `--remove-all`; it must only be used after confirming no
external restic process is active.

Activity list endpoints accept `limit` (1–100, default 10), `status`
(`running`/`success`/`error`), `since` (RFC3339 lower bound on `started_at`),
and `cursor` (opaque next-page token). A `next` field in the response carries
the cursor for the next page when one exists.

SSE streams send `retry: 3000` as the first line so that browsers reconnect
after 3 seconds on disconnect. When auth is enabled and the session expires
while a stream is open, the server closes the connection without further events;
clients must reconnect and obtain a fresh session.

Manual starts return `202` when accepted, `409` if that backup is already
running, and `503` during shutdown. The complete request and response schema is
in [openapi.yaml](openapi.yaml).

## Running and operating

Build and run the backend from the repository root:

```sh
go build -o bin/pgkeeper ./cmd/pgkeeper
./bin/pgkeeper -config /etc/pgkeeper/pgkeeper.toml