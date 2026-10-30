# Phase 2: Configuration loading and failure notifications (Telegram)

**Goal:** load/merge/validate TOML config into `domain` types, and implement the Telegram `Notifier`. Wire both into `main`.

## 1. Config package (`internal/config`)

### 1.1 Raw model (TOML decoding)

```go
type raw struct {
    Global   rawGlobal            `toml:"global"`
    Telegram *rawTelegram         `toml:"telegram"`
    Auth     *rawAuth             `toml:"auth"`
    Restic   rawRestic            `toml:"restic"`
    Backup   map[string]rawBackup `toml:"backup"`
}

type rawGlobal struct {
    Timezone string   `toml:"timezone"`
    Include  []string `toml:"include"`
    TempPath string   `toml:"temp_path"`
    LogPath  string   `toml:"log_path"`
    Listen   string   `toml:"listen"`
    DBPath   string   `toml:"db_path"`
}
type rawTelegram struct { BotToken string `toml:"bot_token"`; ChatID string `toml:"chat_id"` }
type rawAuth struct {
    Issuer, ClientID, Secret, RedirectURL string // toml: issuer, client_id, secret, redirect_url
    Scopes []string; SessionTTL string; CookieSecure *bool; PostLoginRedirect string
}
// Restic keys use hyphens in TOML: toml:"keep-daily", ...
// Pointers (*string, *int) so "unset" differs from zero and per-backup override is detectable.
type rawRestic struct {
    Repository *string `toml:"repository"`; Password *string `toml:"password"`
    KeepDaily, KeepWeekly, KeepMonthly, KeepYearly *int // keep-daily ...
}
type rawBackup struct {
    Cron, Container, Database string
    Username *string; Port *int; Host *string; ResticTag *string; Roles *bool
    rawRestic `toml:",inline"` // flat override keys
}
```
Use `toml.NewDecoder(r).DisallowUnknownFields()` so typos fail loudly.

### 1.2 Loading algorithm (`config.Load(path) (*Config, error)`)

1. Read main file; decode into `raw`.
2. For each glob in `global.include` (resolved relative to the main file's directory; sorted lexicographically): decode each match into its own `raw`, **ignoring its `include`**, and merge:
   - `backup.<name>` from includes added; duplicate name (main or other file) -> error naming both files.
   - `global`, `telegram`, `auth`, `restic`: allowed in includes only if not already set; a second definition -> error naming both files.
   - A glob with zero matches is not an error; an unreadable match is.
3. **Env expansion** on every string value (walk the struct via reflection, or expand on a generic `map[string]any` before decoding into typed structs; the latter is simpler: decode to `map[string]any`, expand strings recursively, re-marshal and decode into typed structs):
   - `${VAR}` -> `os.LookupEnv`; unset -> error `config: environment variable VAR is not set (in <file> at key <path>)`.
   - `${VAR:-default}` -> default when unset or empty.
   - `$$` is not special; only the `${...}` form is recognised.
4. Apply defaults, merge restic (backup value overrides global, key by key), validate, and return `Config`.

```go
type Config struct {
    Global   domain.GlobalConfig
    Telegram *TelegramConfig // nil = disabled
    Auth     *AuthConfig     // nil = disabled
    Backups  []domain.BackupConfig // sorted by name
}
```
`*Config` implements `domain.BackupCatalog` (`List`, `Get`).

### 1.3 Defaults

| Key | Default |
|---|---|
| `global.timezone` | `time.Local` |
| `global.temp_path` | `/var/tmp` |
| `global.log_path` | `/var/log` |
| `global.listen` | `:8080` |
| `global.db_path` | `/var/lib/pgkeeper/pgkeeper.db` |
| `backup.username` / `port` / `host` | `postgres` / `5432` / `localhost` |
| `backup.restic_tag` | the backup label |
| `backup.roles` | `false` |
| `auth.scopes` | `["openid","profile","email"]` |
| `auth.session_ttl` | `12h` |
| `auth.cookie_secure` | `true` |
| `auth.post_login_redirect` | `/` |

### 1.4 Validation (collect **all** errors, return `errors.Join`)

| Rule | Detail |
|---|---|
| timezone | `time.LoadLocation` succeeds |
| paths | `temp_path`, `log_path` exist or can be created (`MkdirAll`) and are writable (try creating a temp file); `db_path` parent writable |
| backup label | matches `^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$` |
| required | `cron`, `container`, `database` non-empty |
| cron | parses with `cron.NewParser(Minute\|Hour\|Dom\|Month\|Dow\|Descriptor)`; the same parser is used by the scheduler in phase 3 (export `config.CronParser`) |
| strings safe | `container`, `database`, `username`, `host` do not start with `-` and contain no NUL/newline |
| port | 1..65535 |
| restic | after merge: `repository` and `password` non-empty (for every backup); `keep-*` >= 0 |
| restic_tag | non-empty, no whitespace or commas |
| telegram | if section present: `bot_token` and `chat_id` non-empty |
| auth | if section present: `issuer`, `client_id`, `secret`, `redirect_url` non-empty; `redirect_url` and `issuer` valid absolute URLs; `session_ttl` parses via `time.ParseDuration` |
| duplicates | two backups with identical `(container, database)` produce a **warning** (log), not an error |
| backups | at least one `[backup.*]` (warning only: the service still starts so the API is reachable) |

Errors are prefixed with file and key, e.g. `backup.main_db.cron: invalid cron expression "foo"`.

### 1.5 Redaction

`Config.LogSafe()` returns a printable summary (backups, cron, container, tag, repo with credentials stripped) used for the startup log. Never log `Secret` values (the type already redacts).

## 2. Notifications (`internal/notify`)

### 2.1 Telegram notifier

```go
type Telegram struct {
    token, chatID string
    client        *http.Client // Timeout: 10s
    baseURL       string       // default https://api.telegram.org (overridable in tests)
    logger        *slog.Logger
}
func NewTelegram(cfg TelegramConfig, logger *slog.Logger) *Telegram
func (t *Telegram) NotifyFailure(ctx context.Context, e domain.NotifyEvent) error
```

- Request: `POST {baseURL}/bot{token}/sendMessage`, JSON `{"chat_id": "...", "text": "...", "disable_web_page_preview": true}`. **No `parse_mode`** (plain text avoids escaping bugs with SQL/log content).
- Retry: up to 3 attempts, backoff 1 s / 3 s, only on network errors, `429` (honour `parameters.retry_after`, capped at 30 s) and `5xx`. `4xx` other than 429 -> fail immediately.
- **Token safety:** `net/http` errors embed the URL. Wrap errors: `fmt.Errorf("telegram sendMessage failed: %s", redact(err.Error(), token))`. Never log the URL.
- A notification failure must never fail the backup: the caller logs a warning and moves on.

### 2.2 Message format

Telegram text limit is 4096 chars; truncate the log tail first (keep the **end**), then hard-truncate the whole message.

```
❌ Backup FAILED: main_db
Database:  app (container postgres-main)
Started:   2026-10-30 03:00:00 -03
Duration:  41s
Trigger:   schedule
Phase/Err: dump: pg_dump exited with status 1
Activity:  0192f3b2-7c1e-7a55-9c0a-3b5f0d1e2a44

Last log lines:
<up to ~15 lines / 2000 chars>
```
Times formatted in `global.timezone`. The phase is included in the error string by the runner (`"<phase>: <message>"`, phase 3).

### 2.3 Helpers

- `notify.Noop{}` implements `Notifier` when `[telegram]` is absent (logs at debug).
- `notify.Multi` (optional, trivial) to support more channels later.
- `notify.TailLog(path string, maxLines, maxBytes int) string`: reads the end of a file efficiently (seek from end).

## 3. Wiring in `main`

```go
cfg, err := config.Load(*configPath)   // exit(1) with all validation errors printed
notifier := notify.Noop{} ; if cfg.Telegram != nil { notifier = notify.NewTelegram(...) }
```
Add flags: `-config` (default `/etc/pgkeeper/pgkeeper.toml`, env `PGKEEPER_CONFIG`), `-check` (validate config, print summary, exit 0/1: handy for CI and Docker healthchecks), `-notify-test` (send a test Telegram message and exit).
Replace the stub catalog and `listen`/`db_path` from phase 1 with config values. `GET /api/v1/backups` and `GET /api/v1/backups/{name}` can now be implemented (without `running`/`next_run`/`last_activity`, finished in phases 3 and 5), or kept at 501 until phase 5, your call during implementation.

## 4. Tests

- Config: table tests with fixtures under `internal/config/testdata/`: minimal valid, full valid, includes (glob, duplicates, ordering), env expansion (set, unset, default, nested, in includes), restic override precedence, every validation rule (one failing fixture each), unknown key rejected, multiple errors aggregated.
- Env: use `t.Setenv`.
- Telegram: `httptest.Server` as `baseURL`: success, 429 with retry_after, 500 then success, 400 no retry, token absent from returned error and logs, message truncation boundaries (exactly 4096, multi-byte runes: truncate by runes, not bytes).
- `TailLog`: empty file, fewer lines than max, huge single line, no trailing newline.

## 5. Definition of done

- `pgkeeper -check -config examples/pgkeeper.toml` validates the sample config and prints a redacted summary.
- Invalid configs report **all** problems at once with file/key context.
- `-notify-test` delivers a Telegram message with a real bot (manual check).
