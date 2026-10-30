# Phase 5: REST API, SSE and OIDC

**Goal:** fill in every handler stubbed in phase 1, following `api.md` / `openapi.yaml` exactly, and add OIDC authentication (authorization code flow with server-side sessions).

## 1. Handler dependencies

```go
type Handler struct {
    cfg        domain.GlobalConfig
    catalog    domain.BackupCatalog
    activities domain.ActivityRepository
    runner     domain.BackupRunner
    scheduler  domain.Scheduler
    jobHub     *events.Hub[backup.JobEvent]
    logHub     *events.Hub[logstore.LogLine]
    auth       *auth.Service // nil when auth disabled
    db         *sql.DB       // for /healthz
    logger     *slog.Logger
}
```

## 2. Endpoint implementation notes

| Endpoint | Implementation |
|---|---|
| `GET /backups` | `catalog.List()`; one query `LastByBackup(names)`; `runner.Running()` for `running`; `scheduler.NextRun` for `next_run` (UTC). Sorted by name. |
| `GET /backups/{name}` | same for one; `404 backup_not_found`. |
| `GET /backups/{name}/activities` | validate backup exists (404), parse `limit` (`strconv.Atoi`; non-numeric or `<1` -> 400; `>10` -> 10), `ListByBackup`. |
| `POST /backups/{name}/run` | `runner.Run(appCtx, name, TriggerManual)`: `ErrBackupUnknown` -> 404, `ErrBackupRunning` -> 409 (`backup_running`), success -> `202` + Activity DTO. Do **not** pass `r.Context()`. Ignore request body. |
| `GET /activities` | `ListRecent(limit)`. |
| `GET /activities/{id}` | `uuid.Parse` (400 on failure), `Get` (404). |
| `GET /activities/{id}/log` | resolve activity (404), open `activity.LogPath` (404 `log_not_found` if missing), `Content-Type: text/plain; charset=utf-8`, `io.Copy`. Path comes **only** from the DB row, never from user input. For running activities a snapshot of the file is returned. |
| `GET /jobs` | `runner.Running()` -> Job DTOs (sorted by `started_at`). |

DTO rules: `duration_ms` = `finished_at - started_at` (null while running); `log_url` = `/api/v1/activities/{id}/log`; `repository` field passed through `restic.RedactRepo`; `password` never mapped.

## 3. SSE

### 3.1 Helper (`internal/api/sse.go`)

```go
type sseWriter struct { w http.ResponseWriter; fl http.Flusher }
func newSSE(w http.ResponseWriter) (*sseWriter, bool) // checks http.Flusher, sets headers:
//   Content-Type: text/event-stream; Cache-Control: no-cache; Connection: keep-alive; X-Accel-Buffering: no
func (s *sseWriter) event(name, id string, data any) error // marshals JSON, writes "event:/id:/data:" + blank line, flush
func (s *sseWriter) comment(text string) error             // ": ping"
```
- Use `http.NewResponseController(w)` to `SetWriteDeadline(time.Time{})` (disable write timeout) per request.
- Heartbeat: `time.Ticker` 15 s -> `: ping`.
- Always `select` on `r.Context().Done()` and on the hub channel; call `cancel()` of the subscription on exit.
- SSE routes are registered **outside** the `Timeout` middleware group and outside any response-compression middleware.

### 3.2 `GET /jobs/stream`
1. Subscribe to `jobHub` **first**, then read `runner.Running()` and send `snapshot` (avoids missing events between read and subscribe; duplicates are harmless because clients key by `activity_id`).
2. Loop: forward `job.started`, `job.phase`, `job.finished` (`finished` carries `{job, activity}`; the activity is re-read from DB so `finished_at/duration` are final).

### 3.3 `GET /activities/{id}/log/stream`
1. Resolve the activity (404 / 400).
2. `Last-Event-ID` header (or `?last_event_id=` fallback) -> `after` line number (default 0).
3. If activity is `running`: subscribe to `logHub` filtered by activity id **before** reading the file; replay file lines `> after`; then emit buffered/live lines with `N > lastSent`; when the `job.finished` for this activity arrives (or the log channel signals end), drain remaining, send `end` with final status/error from DB, close.
4. If already finished: replay all lines `> after`, send `end`, close.
5. Event ids are the 1-based line number so browser reconnects resume transparently.

Edge case: the log channel can be dropped for slow clients (bounded buffer). On detecting a gap (`N != lastSent+1`) re-read the missing range from the file.

## 4. OIDC (`internal/auth`)

Libraries: `github.com/coreos/go-oidc/v3/oidc`, `golang.org/x/oauth2`.

### 4.1 Service

```go
type Service struct {
    provider *oidc.Provider
    verifier *oidc.IDTokenVerifier
    oauth    oauth2.Config   // ClientID, ClientSecret, RedirectURL, Endpoint, Scopes
    sessions domain.SessionRepository
    cfg      config.AuthConfig
}
func New(ctx context.Context, cfg config.AuthConfig, sessions domain.SessionRepository) (*Service, error)
```
- `oidc.NewProvider(ctx, issuer)` is performed at startup with retry (3 attempts, backoff). If the IdP is unreachable at boot, fail fast with a clear error (auth configured but unusable must not silently open the API).

### 4.2 Flow

**`GET /auth/login`**
1. `redirect_to` validation: must start with `/`, not `//`, no `\`, no scheme/host (open-redirect protection); otherwise use `post_login_redirect`.
2. Generate `state` (32 random bytes, base64url), `nonce`, PKCE `verifier` (`oauth2.GenerateVerifier()`).
3. Persist `AuthState{state, nonce, verifier, redirect_to, expires_at: now+10m}`.
4. 302 to `oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))`.

**`GET /auth/callback`**
1. Reject if `error` query param present (400 with the code, no echo of description into HTML).
2. `ConsumeAuthState(state)` (single use, unexpired) -> else 400 `invalid_request`.
3. `oauth.Exchange(ctx, code, oauth2.VerifierOption(verifier))`.
4. Extract `id_token`, `verifier.Verify`, check `nonce` equality.
5. Read claims `sub`, `email`, `name` (fallback `preferred_username`).
6. Create session: random 32-byte token -> cookie value; store `sha256(token)` hex as `id_hash`; `expires_at = now + session_ttl`.
7. `Set-Cookie: pgkeeper_session=<token>; Path=/; HttpOnly; SameSite=Lax; Secure (cfg.cookie_secure); Max-Age=ttl`.
8. 302 to `redirect_to`.

**`POST /auth/logout`**: delete session row, expire cookie, `204`. (Optional RP-initiated logout at the IdP `end_session_endpoint` can be added later.)

### 4.3 Middleware

`auth.Require(next)` applied to the whole `/api/v1` group (except nothing; `/healthz` and `/auth/*` live outside):
- Auth disabled -> pass through (inject no user).
- Read cookie -> hash -> `GetSession(now)`; missing/expired -> `401 unauthenticated` JSON (never redirect for API calls; the frontend handles navigation to `/auth/login`).
- Put the user in `context` for handlers (`/api/v1/me`).
- Background goroutine: `DeleteExpired` hourly (+ expired `auth_states`).

`GET /api/v1/me`: with auth disabled returns the `auth_enabled:false` body without requiring a session; with auth enabled it sits behind `Require`.

### 4.4 Security checklist
- Cookie flags as above; `cookie_secure=false` only for local HTTP dev (log a warning).
- `state`, `nonce`, PKCE all verified; auth state is single-use and expires in 10 min.
- Session ids high-entropy and stored hashed.
- CSRF: SameSite=Lax + JSON-only mutation (`POST /run` rejects `Content-Type` other than `application/json` when a body is present) + no state-changing `GET`.
- CORS: not enabled by default (same-origin frontend assumed). Optional `[global] cors_origins` can be added if the frontend is served elsewhere (then `Access-Control-Allow-Credentials: true` and exact origin match, never `*`).
- Rate limit `/auth/callback` and `/auth/login` lightly (e.g. `httprate` 20/min/IP) to reduce state-table abuse.

## 5. Tests

- Handlers with `httptest` + fakes: every status code in `openapi.yaml` (200/202/400/401/404/409), limit clamping (`0`, `-1`, `abc`, `11`, absent), ordering, DTO shape (golden JSON files).
- Contract test from phase 1 now also validates **response bodies** against `openapi.yaml` (`kin-openapi` `openapi3filter`).
- SSE: use a real `httptest.Server`, read the stream with a scanner:
  - jobs stream: snapshot first, then events for a fake runner execution;
  - log stream: replay of a finished log, live follow of a running one with the replay/live overlap race (inject lines while replaying; assert no gaps/duplicates), `Last-Event-ID` resume, client disconnect releases the subscription (goroutine leak check with `goleak`), heartbeat (shorten the interval in tests).
- OIDC: mock IdP (`github.com/zitadel/oidc` test server or a small in-test OIDC provider serving discovery/JWKS/token): full login, wrong state, replayed state, bad nonce, expired token, session expiry, logout, open-redirect attempts (`//evil.com`, `https://evil`, `/\evil`).
- Race detector on everything (`go test -race`).

## 6. Definition of done

- All routes in `openapi.yaml` behave as documented; contract test passes in both directions.
- Manual run from the API produces live log lines over SSE and a `job.finished` event.
- With `[auth]` configured, unauthenticated `curl` gets `401`, browser login round-trip works against a real IdP (Keycloak recommended for local testing, add to `docker-compose.dev.yml` in phase 6).
