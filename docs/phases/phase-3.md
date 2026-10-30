# Phase 3: Backup execution and scheduling

**Goal:** run `pg_dump` (and optionally roles) inside containers via `docker exec`, record activities and logs, fire jobs from cron, notify on failure. Restic is stubbed (phase 4): the runner calls a `ResticClient` that is a no-op/fake until then, and the temp files are deleted after the dump phases.

## 1. Executor (`internal/executor`)

`DockerCLI` implements `domain.Executor` using `os/exec` (no shell):

```go
func (d DockerCLI) Dump(ctx context.Context, s domain.DumpSpec, out io.Writer, logw io.Writer) error {
    args := buildArgs(s)
    cmd := exec.CommandContext(ctx, "docker", args...)
    cmd.Stdout = out          // *os.File of the temp dump
    cmd.Stderr = logw         // pg_dump diagnostics go to the execution log
    cmd.WaitDelay = 10 * time.Second
    return cmd.Run()
}
```

`buildArgs`:

| Kind | argv |
|---|---|
| database | `exec <container> pg_dump --no-owner --disable-triggers --format=plain -d <db> --host=<host> -p <port> --username=<user> --no-password` |
| roles | `exec <container> pg_dumpall --host=<host> -p <port> --username=<user> --no-password --roles-only` |

Notes:
- **No `-i`/`-t`** on `docker exec` (TTY would mangle the output).
- Because argv is passed directly, there is no shell injection; config validation (phase 2) already rejects values starting with `-`.
- Errors: wrap `*exec.ExitError` as `fmt.Errorf("docker exec %s exited with status %d", kind, code)`; classify "No such container" (stderr contains it) so the log is clear. Context cancel -> kill process (default behaviour of `CommandContext`).
- Startup check: `docker version` (or `docker info`) once, log a warning if the socket is not reachable; do not abort startup (API must still work).

## 2. Log store (`internal/logstore`)

Per-execution log file: `<log_path>/<database>/<activity-uuid>.log`.

```go
type Writer struct { // io.Writer, line-oriented
    f      *os.File
    hub    *events.Hub[LogLine]
    id     uuid.UUID
    lineNo int
    buf    []byte  // partial line
    mu     sync.Mutex
}
func Open(path string, id uuid.UUID, hub *events.Hub[LogLine]) (*Writer, error)
func (w *Writer) Logf(level, format string, args ...any) // "2026-10-30T12:00:01Z INFO message"
func (w *Writer) Write(p []byte) (int, error)            // splits on \n, prefixes stderr lines with "[pg] " timestamped
func (w *Writer) Close() error
```

- Directory created with `0750`, file `0640`.
- Every complete line: write to file, `fsync` not required per line (flush on close), publish `LogLine{ActivityID, N, Text}` to the hub.
- Lines longer than 64 KiB are truncated with a marker (protects SSE and memory).
- `logstore.ReadLines(path, afterLine int) ([]LogLine, error)` for replay (phase 5).
- **Replay/live race handling** (used by SSE in phase 5): subscribe to the hub first, buffer live lines, read the file, emit file lines, then emit buffered lines with `N > lastFileLine`.

## 3. Events hub (`internal/events`)

Generic in-memory pub/sub:

```go
type Hub[T any] struct { ... }
func (h *Hub[T]) Subscribe(filter func(T) bool) (<-chan T, cancel func())
func (h *Hub[T]) Publish(v T)
```
- Each subscriber has a **bounded buffered channel** (e.g. 256); on overflow the event is dropped for that slow subscriber and a counter increments (never block the backup).
- Two hubs: `LogHub` (log lines) and `JobHub` (`JobEvent{Type: started|phase|finished, Job, Activity}`).

## 4. Runner (`internal/backup`)

```go
type Runner struct {
    catalog   domain.BackupCatalog
    global    domain.GlobalConfig
    activities domain.ActivityRepository
    exec      domain.Executor
    restic    domain.ResticClient
    notifier  domain.Notifier
    jobs      *JobTracker      // in-memory map[backupName]*domain.Job + mutex
    jobHub    *events.Hub[JobEvent]
    logHub    *events.Hub[logstore.LogLine]
    wg        sync.WaitGroup   // for graceful shutdown
    clock     func() time.Time
    newID     func() uuid.UUID // uuid.NewV7 (injectable for tests)
}
```

`Run(ctx, name, by)`:
1. `cfg, ok := catalog.Get(name)`; else `ErrBackupUnknown`.
2. `jobs.TryStart(name)`: atomic; if present -> `ErrBackupRunning`.
3. Create activity `running` (log path computed), insert in DB. If this fails, release lock and return the error.
4. `wg.Add(1)`; `go r.execute(runCtx, cfg, activity)` where `runCtx` derives from the **application** context (not the HTTP request context, which ends with the response). Return the activity immediately (HTTP `202`).

`execute`:
```go
defer wg.Done(); defer jobs.Release(name)
log := logstore.Open(...)
defer log.Close()
var runErr error
func() {
    defer cleanupTemp()                       // phase "cleanup"
    runErr = r.phases(ctx, cfg, act, log)     // returns error wrapped with phase name
}()
status := success / error ; finishedAt := now
activities.Finish(...)      // use context.WithoutCancel + 10 s timeout so shutdown still records
jobHub.Publish(finished)
if runErr != nil { notify (best effort, own timeout) }
```

`phases`:
1. `tmpDir = <temp_path>/pgkeeper/<backup-name>`; `os.RemoveAll` then `MkdirAll(0700)`.
2. **dump:** `jobs.SetPhase(PhaseDump)` (+ publish `job.phase`), create `<tmpDir>/<database>.sql` (`0600`), `exec.Dump(...)`; on error return `fmt.Errorf("dump: %w", err)`. After success `Stat`: size 0 -> error `dump: output file is empty`. Log size and duration.
3. **roles** (if `cfg.Roles`): same with `<database>_roles.sql`.
4. **restic:** `restic.EnsureRepo/Backup/Forget` (phase 4; in this phase the wired client is a `NoopRestic` that logs "restic disabled (phase 4)").
5. Every phase logs start/end with elapsed time. Command lines are logged **without** secrets (there are none in argv).

Failure semantics: first failing phase stops the execution; roles failure fails the whole execution (database dump file is discarded together with the temp dir). Panics inside `execute` are recovered, logged, and recorded as `error: internal panic`.

Notification payload: `NotifyEvent{Backup: cfg, Activity: finishedActivity, LogTail: notify.TailLog(path, 15, 2000)}`.

Startup: `activities.MarkRunningAsInterrupted(now)` before scheduler start; log the count.

`Runner.Shutdown(ctx)`: `wg.Wait()` bounded by ctx (30 s); on timeout cancel the app context so running jobs end as `error: interrupted`.

## 5. Scheduler (`internal/scheduler`)

```go
type Cron struct {
    c      *cron.Cron   // cron.New(cron.WithLocation(loc), cron.WithParser(config.CronParser))
    ids    map[string]cron.EntryID
    runner domain.BackupRunner
}
func New(loc *time.Location, backups []domain.BackupConfig, runner domain.BackupRunner, logger *slog.Logger) (*Cron, error)
func (s *Cron) Start() error                       // c.Start()
func (s *Cron) Stop(ctx context.Context) error     // <-c.Stop().Done() bounded by ctx
func (s *Cron) NextRun(name string) (time.Time, bool) // c.Entry(id).Next
```
- One entry per backup: `AddFunc(cfg.Cron, func(){ runner.Run(appCtx, name, TriggerSchedule) })`.
- `ErrBackupRunning` on a tick -> `logger.Warn("skipping scheduled run, previous still running")`, no activity.
- Other errors logged with backup name.
- DST/timezone: all schedules use `global.timezone`; `NextRun` returned in UTC by the API layer.
- Misfire policy: cron only fires while the process is up; missed runs are **not** replayed (documented behaviour).

## 6. Wiring

`internal/app.Run(ctx, cfg)`: open DB -> migrate -> mark interrupted -> build hubs, executor, restic (noop), notifier, runner, scheduler -> start scheduler -> start HTTP server -> wait for ctx -> `scheduler.Stop` -> `runner.Shutdown(30s)` -> `server.Shutdown(10s)` -> close DB.
Additional flag: `-run <backup-name>`: run one backup immediately and exit with code 0/1 (useful for testing before the API is finished).

## 7. Tests

- Executor `buildArgs`: exact argv for both kinds, special characters preserved as single args.
- Fake `Executor` (writes bytes / returns error / blocks until ctx cancel) + fake notifier + in-memory activity repo:
  - success path: activity `running` -> `success`, files cleaned, log file content, events order (`started`, `phase`..., `finished`).
  - dump failure: status `error`, error message `dump: ...`, notifier called once with log tail.
  - roles failure fails the whole run; empty dump file fails.
  - concurrent `Run` of the same backup -> second gets `ErrBackupRunning`; different backups run in parallel.
  - context cancel (shutdown) -> activity `error`, still persisted.
  - notifier failure does not change the result.
- Scheduler: inject a fake clock or use `@every 1s` with a counting runner; invalid cron rejected at construction; `NextRun`.
- Integration (opt-in, `-tags=integration`): spin up `postgres:17` with Docker, seed data, run real `DockerCLI`, check the dump restores with `psql`.
- Log writer: partial lines across `Write` calls, CRLF, long-line truncation, concurrent writers (`-race`).

## 8. Definition of done

- `pgkeeper -run main_db` against a real container produces `<temp>/...` dump during run, an activity row, and a log file `<log_path>/<db>/<uuid>.log`.
- Failing dump (wrong container name) produces an `error` activity and a Telegram message.
- Scheduled runs fire at the configured times in the configured timezone.
