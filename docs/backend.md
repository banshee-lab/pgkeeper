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
| `internal/logstore` and `internal/events` | Per-execution log files, live log tails, and in-memory event streams |
| `internal/notify` | Failure notifications, including Telegram |
| `internal/auth` | Optional OIDC login and server-side session handling |
| `internal/api` | HTTP routes, handlers, authentication middleware, and SSE streams |

Application logs are structured JSON written to standard output. Execution logs
are separate files under `global.log_path`.

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
4. Ensures the restic repository exists, creates one snapshot for the run, then
   applies configured retention when at least one retention value is set.
5. Removes temporary dump files, records the final activity status, and
   publishes completion events. On failure it also attempts a Telegram
   notification if Telegram is configured.

Commands are invoked directly with argument lists, not through a shell.
PostgreSQL authentication must already work non-interactively inside each
container (for example via `.pgpass`); pgkeeper does not manage database
passwords. Restic runs in the pgkeeper process environment and uses
`RESTIC_PASSWORD` for its repository password.

The scheduler timezone is `global.timezone` (system local time by default).
Configuration is loaded only at startup; restart the service after changing
configuration or its environment.

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

The API includes:

| Route | Purpose |
| --- | --- |
| `GET /healthz` | Liveness/readiness check |
| `GET /auth/login`, `GET /auth/callback`, `POST /auth/logout` | OIDC session flow |
| `GET /api/v1/me` | Current authentication state |
| `GET /api/v1/backups`, `GET /api/v1/backups/{name}` | Configured backup details |
| `GET /api/v1/backups/{name}/activities` | Recent activity for one backup |
| `POST /api/v1/backups/{name}/run` | Start a manual backup |
| `GET /api/v1/activities`, `GET /api/v1/activities/{id}` | Recent activity and its details |
| `GET /api/v1/activities/{id}/log` | Read an activity log |
| `GET /api/v1/activities/{id}/log/stream` | Replay and follow an activity log over SSE |
| `GET /api/v1/jobs`, `GET /api/v1/jobs/stream` | Current jobs and job-event SSE stream |

Activity list limits are capped at 10. Manual starts return `202` when accepted,
`409` if that backup is already running, and `503` during shutdown. The complete
request and response schema is in [openapi.yaml](openapi.yaml).

## Running and operating

Build and run the backend from the repository root:

```sh
go build -o bin/pgkeeper ./cmd/pgkeeper
./bin/pgkeeper -config /etc/pgkeeper/pgkeeper.toml
```

Useful one-shot commands (they load and validate the selected configuration
first):

```sh
./bin/pgkeeper -config /etc/pgkeeper/pgkeeper.toml -check
./bin/pgkeeper -config /etc/pgkeeper/pgkeeper.toml -check-restic
./bin/pgkeeper -config /etc/pgkeeper/pgkeeper.toml -notify-test
./bin/pgkeeper -config /etc/pgkeeper/pgkeeper.toml -run main_db
```

`-check` prints a secret-redacted configuration summary. `-check-restic`
checks the restic version and each distinct repository without initializing
missing repositories. `-run NAME` runs one backup and exits instead of starting
the HTTP server and scheduler. `-notify-test` sends a test Telegram message and
requires `[telegram]`.

For production setup, see [Production deployment](production.md).
