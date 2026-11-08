# Copilot instructions for pgkeeper

## Build, test, and lint

- Build: `make build`
- Run pgkeeper with the default config: `make run`; override with `make run CONFIG=/path/to/pgkeeper.toml`
- Run all unit tests: `make test`
- Run tests with the race detector: `make race`
- Run one test: `go test ./internal/config -run '^TestDefaults$'`
- Run the opt-in restic integration test: `make integration` (requires `restic` on `PATH`)
- Static checks: `make vet` and `make lint`; `make check` runs unit tests and `go vet`
- Remove the built binary: `make clean`

## Architecture

`cmd/pgkeeper` handles flags, signals, logging, and config loading. `internal/app` owns application wiring and lifecycle: it opens and migrates SQLite, builds the runner, scheduler, and HTTP API, and coordinates graceful shutdown. Core types and dependency interfaces live in `internal/domain`; the implementations are separated into `config`, `store`, `backup`, `executor`, `restic`, `notify`, `scheduler`, `logstore`, `events`, `auth`, and `api`.

The backup flow is shared by scheduled and manual triggers: `scheduler` or an API request calls the asynchronous `backup.Runner`, which prevents overlapping runs for a backup, records activity state in SQLite, and coordinates Docker dumps, restic snapshots, execution logs, notifications, and job/log events. The API exposes REST endpoints and SSE streams over this state. For the design decisions and full configuration reference, see `docs/plan.md`; for the API contract, see `docs/api.md` and `docs/openapi.yaml`.

## Repository-specific conventions

- Keep business-facing dependencies expressed as interfaces in `internal/domain`; wire concrete implementations in `internal/app` and use fakes for unit tests.
- Config is TOML decoded strictly: unknown keys are errors. `config.Load` handles environment expansion, includes, defaults, and validation; preserve that single resolution path and update `examples/pgkeeper.toml` when changing the supported config.
- Launch external programs with `exec.CommandContext` and argument slices, never through a shell. Keep secrets out of command arguments and logs; use `domain.Secret` and existing redaction helpers.
- SQLite schema changes belong in new Goose SQL migrations under `migrations/`; those files are embedded by `migrations/embed.go` and applied by `store.Migrate`.
- When changing API behavior or payloads, keep the API contract `docs/openapi.yaml` aligned. API JSON uses `snake_case` and the documented error envelope.
- Restic integration tests use the `integration` build tag; ordinary `go test ./...` excludes them.
