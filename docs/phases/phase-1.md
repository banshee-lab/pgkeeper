# Phase 1: Project setup, interfaces, API skeleton, SQLite entities

**Goal:** a compiling, tested skeleton: module layout, domain types and ports, SQLite schema + repositories, chi router with every final route registered (returning `501` except `/healthz`), consistent error format. No config loading, no backups yet.

## 1. Tasks

### 1.1 Repository bootstrap
```
go mod init pgkeeper        # go.mod: go 1.27
```
Dependencies:
```
go get github.com/go-chi/chi/v5
go get modernc.org/sqlite
go get github.com/pressly/goose/v3
go get github.com/google/uuid
go get github.com/robfig/cron/v3
go get github.com/pelletier/go-toml/v2
```
(oidc/oauth2 are added in phase 5.)

Files: `cmd/pgkeeper/main.go`, `Makefile` (`build`, `test`, `lint`, `run`), `.golangci.yml`, `.gitignore`, `.editorconfig`, `README.md` stub, `docs/` (copy plan/api/openapi).

`main.go` (phase 1): parse `-config` flag (unused yet), build slog JSON logger, open SQLite, run migrations, start HTTP server on `:8080`, graceful shutdown on `SIGINT/SIGTERM` (`signal.NotifyContext`).

### 1.2 Domain (`internal/domain`)

`types.go`
```go
package domain

type Status string
const (
    StatusRunning Status = "running"
    StatusSuccess Status = "success"
    StatusError   Status = "error"
)

type Trigger string
const (
    TriggerSchedule Trigger = "schedule"
    TriggerManual   Trigger = "manual"
)

type Phase string
const (
    PhaseDump    Phase = "dump"
    PhaseRoles   Phase = "roles"
    PhaseRestic  Phase = "restic"
    PhaseCleanup Phase = "cleanup"
)

// Secret redacts itself in logs, fmt and JSON.
type Secret string
func (Secret) String() string               { return "***" }
func (Secret) LogValue() slog.Value         { return slog.StringValue("***") }
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"***"`), nil }
func (s Secret) Reveal() string             { return string(s) }
```

`activity.go`
```go
type Activity struct {
    ID          uuid.UUID
    BackupName  string
    Database    string
    TriggeredBy Trigger
    Status      Status
    StartedAt   time.Time
    FinishedAt  *time.Time
    Error       string
    LogPath     string
}

func (a Activity) Duration() *time.Duration // nil while running

type Job struct { // in-memory only
    ActivityID  uuid.UUID
    BackupName  string
    Database    string
    TriggeredBy Trigger
    Phase       Phase
    StartedAt   time.Time
}
```

`backup.go` (resolved config, filled by phase 2)
```go
type ResticConfig struct {
    Repository  string
    Password    Secret
    KeepDaily, KeepWeekly, KeepMonthly, KeepYearly int // 0 = unset
}

type BackupConfig struct {
    Name, Cron, Container, Database, Username, Host string
    Port       int
    ResticTag  string
    Roles      bool
    Restic     ResticConfig // already merged with global
}

type GlobalConfig struct {
    Location *time.Location
    TempPath, LogPath, Listen, DBPath string
}
```

`session.go`: `Session{IDHash, Subject, Email, Name string; CreatedAt, ExpiresAt time.Time}`, `AuthState{State, Nonce, PKCEVerifier, RedirectTo string; ExpiresAt time.Time}`.

`errors.go`: `ErrNotFound`, `ErrBackupRunning`, `ErrBackupUnknown` (sentinel errors used by API mapping).

### 1.3 Ports (`internal/domain/ports.go`)

```go
type ActivityRepository interface {
    Create(ctx context.Context, a *Activity) error
    Finish(ctx context.Context, id uuid.UUID, status Status, finishedAt time.Time, errMsg string) error
    Get(ctx context.Context, id uuid.UUID) (*Activity, error)            // ErrNotFound
    ListByBackup(ctx context.Context, name string, limit int) ([]Activity, error)
    ListRecent(ctx context.Context, limit int) ([]Activity, error)
    LastByBackup(ctx context.Context, names []string) (map[string]Activity, error)
    MarkRunningAsInterrupted(ctx context.Context, now time.Time) (int, error)
}

type SessionRepository interface {
    CreateSession(ctx context.Context, s *Session) error
    GetSession(ctx context.Context, idHash string, now time.Time) (*Session, error) // ErrNotFound if missing/expired
    DeleteSession(ctx context.Context, idHash string) error
    DeleteExpired(ctx context.Context, now time.Time) error
    SaveAuthState(ctx context.Context, s *AuthState) error
    ConsumeAuthState(ctx context.Context, state string, now time.Time) (*AuthState, error) // single use
}

type DumpKind string
const (DumpDatabase DumpKind = "database"; DumpRoles DumpKind = "roles")

type DumpSpec struct {
    Kind                       DumpKind
    Container, Database        string
    Host, Username             string
    Port                       int
}

// Executor runs a dump inside a container; dump bytes -> out, diagnostics -> logw.
type Executor interface {
    Dump(ctx context.Context, spec DumpSpec, out io.Writer, logw io.Writer) error
}

type ResticClient interface {
    EnsureRepo(ctx context.Context, cfg ResticConfig, logw io.Writer) error
    Backup(ctx context.Context, cfg ResticConfig, tag, host string, dir string, files []string, logw io.Writer) error
    Forget(ctx context.Context, cfg ResticConfig, tag, host string, logw io.Writer) error
}

type NotifyEvent struct {
    Backup   BackupConfig
    Activity Activity
    LogTail  string // last lines, already truncated
}
type Notifier interface {
    NotifyFailure(ctx context.Context, e NotifyEvent) error
}

type BackupRunner interface {
    // Run starts an execution asynchronously. Returns the new activity
    // or ErrBackupRunning / ErrBackupUnknown.
    Run(ctx context.Context, name string, by Trigger) (*Activity, error)
    Running() []Job
}

type Scheduler interface {
    Start() error
    Stop(ctx context.Context) error
    NextRun(name string) (time.Time, bool)
}

type BackupCatalog interface { // read-only view of loaded config
    List() []BackupConfig
    Get(name string) (BackupConfig, bool)
}
```
Add `//go:generate`-free hand-written fakes under `internal/domain/fakes` (or per-package `_test`) as they are needed in later phases.

### 1.4 SQLite store (`internal/store`)

- `store.Open(path string) (*sql.DB, error)`: `modernc.org/sqlite` driver name `"sqlite"`, DSN with pragmas:
  `file:<path>?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)`.
  `db.SetMaxOpenConns(1)` for the writer pool (simple and avoids `SQLITE_BUSY`); revisit if reads contend.
- `migrations/0001_init.sql` (goose, embedded with `//go:embed`): the three tables and indexes from `plan.md` §5.
- `store.Migrate(db)` using `goose.SetBaseFS` + `goose.Up`.
- Implementations: `store.ActivityStore`, `store.SessionStore` satisfying the ports. Times stored as RFC 3339 UTC strings (`time.RFC3339Nano`), parsed back to UTC.
- `ListRecent`/`ListByBackup` clamp `limit` to `[1,10]` inside the store as a second defence.
- `MarkRunningAsInterrupted`: `UPDATE ... SET status='error', finished_at=?, error='interrupted by restart' WHERE status='running'`.

### 1.5 API skeleton (`internal/api`)

- `router.go`: `chi.NewRouter()` with middleware: `RequestID`, `RealIP`, structured request logger (slog), `Recoverer`, `Timeout(30s)` **except** SSE routes (mount SSE routes outside the timeout group).
- Register **all** routes from `api.md`:

```go
r.Get("/healthz", h.Health)
r.Route("/auth", func(r chi.Router) {
    r.Get("/login", h.AuthLogin); r.Get("/callback", h.AuthCallback); r.Post("/logout", h.AuthLogout)
})
r.Route("/api/v1", func(r chi.Router) {
    r.Get("/me", h.Me)
    r.Get("/backups", h.ListBackups)
    r.Get("/backups/{name}", h.GetBackup)
    r.Get("/backups/{name}/activities", h.ListBackupActivities)
    r.Post("/backups/{name}/run", h.RunBackup)
    r.Get("/activities", h.ListActivities)
    r.Get("/activities/{id}", h.GetActivity)
    r.Get("/activities/{id}/log", h.GetActivityLog)
    r.Get("/activities/{id}/log/stream", h.StreamActivityLog)
    r.Get("/jobs", h.ListJobs)
    r.Get("/jobs/stream", h.StreamJobs)
})
```
- `errors.go`: `writeError(w, status, code, msg)` producing the contract error JSON; `writeJSON`.
- `dto.go`: response structs with `json` tags exactly as in `api.md` (`ActivityDTO`, `BackupDTO`, `JobDTO`, `MeDTO`) + mapping funcs from domain types (`duration_ms` computed here).
- Handlers: `Health` pings DB; **all others return `501 not_implemented`**.
- `Handler` struct takes ports via constructor (`activities`, `sessions`, `catalog`, `runner`, `...`) so later phases only fill bodies.
- `server.go`: `http.Server{ReadHeaderTimeout: 10s, IdleTimeout: 120s}` (no `WriteTimeout`, SSE needs long writes).

## 2. Tests

- `store`: in-memory (`file::memory:?cache=shared`) or temp-file DB; CRUD round-trips, ordering (newest first), limit clamp, `MarkRunningAsInterrupted`, expired session lookup, single-use `ConsumeAuthState`.
- `domain`: `Secret` never leaks via `fmt.Sprintf("%v")`, `slog`, JSON; `Activity.Duration`.
- `api`: `httptest` table test: every route exists and returns `501` with the contract error body; `/healthz` returns `200`; unknown route `404`.
- Contract check (nice to have): test that parses `docs/openapi.yaml` (`kin-openapi`) and asserts each registered chi route (`chi.Walk`) exists in the spec and vice versa.

## 3. Definition of done

- `make build test lint` green, `go test -race ./...` green.
- `./pgkeeper` starts, creates the SQLite file, applies migration, serves `/healthz`.
- Every route in `openapi.yaml` is registered.
