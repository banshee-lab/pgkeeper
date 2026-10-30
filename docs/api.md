# pgkeeper: API Contract (v1)

Machine-readable spec: [`openapi.yaml`](openapi.yaml). This document is the human-readable contract; both must stay in sync.

## Conventions

- Base path: `/api/v1`. Auth routes live under `/auth`. Health at `/healthz`.
- JSON, `snake_case` keys, UTF-8. Timestamps RFC 3339 UTC (`2026-10-30T12:00:00Z`). Durations in milliseconds.
- **Authentication:** when `[auth]` is configured, all `/api/v1/*` routes require a valid session cookie (`pgkeeper_session`, HttpOnly, SameSite=Lax, Secure by default). Unauthenticated -> `401`. When `[auth]` is absent the API is open.
- SSE works with the cookie (browser `EventSource` with `withCredentials` when cross-origin).
- Mutating requests (`POST`) must send `Content-Type: application/json` (or no body); this plus SameSite=Lax is the CSRF defence.
- List endpoints are capped: `limit` is 1-10, default 10; values above 10 are clamped to 10.
- Secrets (restic password, OIDC secret, Telegram token, credentials embedded in repository URLs) are **never** returned.

### Error format

```json
{ "error": { "code": "backup_not_found", "message": "backup 'foo' does not exist" } }
```

| HTTP | code | When |
|---|---|---|
| 400 | `invalid_request` | bad parameter (e.g. `limit=abc`, malformed UUID) |
| 401 | `unauthenticated` | missing/expired session |
| 404 | `backup_not_found` / `activity_not_found` / `log_not_found` | unknown resource |
| 409 | `backup_running` | manual run while that backup is already running |
| 500 | `internal_error` | unexpected |
| 501 | `not_implemented` | skeleton phase only |

## Schemas

**Activity**
```json
{
  "id": "0192f3b2-7c1e-7a55-9c0a-3b5f0d1e2a44",
  "backup_name": "main_db",
  "database": "app",
  "triggered_by": "schedule",          // "schedule" | "manual"
  "status": "success",                 // "running" | "success" | "error"
  "started_at": "2026-10-30T03:00:00Z",
  "finished_at": "2026-10-30T03:02:41Z",   // null while running
  "duration_ms": 161000,                   // null while running
  "error": null,                           // string when status = "error"
  "log_url": "/api/v1/activities/0192f3b2-.../log"
}
```

**Backup** (config view)
```json
{
  "name": "main_db",
  "cron": "@daily",
  "container": "postgres-main",
  "database": "app",
  "username": "postgres",
  "host": "localhost",
  "port": 5432,
  "roles": false,
  "restic": {
    "repository": "/srv/restic",
    "tag": "main_db",
    "keep_daily": 7, "keep_weekly": 4, "keep_monthly": 12, "keep_yearly": 2
  },
  "running": false,
  "next_run": "2026-10-31T03:00:00Z",
  "last_activity": { "...Activity or null..." }
}
```

**Job** (currently running execution)
```json
{
  "activity_id": "0192f3b2-...",
  "backup_name": "main_db",
  "database": "app",
  "triggered_by": "manual",
  "status": "running",
  "phase": "dump",                    // "dump" | "roles" | "restic" | "cleanup"
  "started_at": "2026-10-30T12:00:00Z"
}
```

## Endpoints

### Health
`GET /healthz` -> `200 {"status":"ok"}` (public, no auth). Checks DB ping.

### Auth
| Method | Path | Description |
|---|---|---|
| GET | `/auth/login?redirect_to=/path` | 302 to the IdP (code flow + PKCE + state + nonce). `redirect_to` must be a relative path. `404` if auth disabled. |
| GET | `/auth/callback` | IdP redirect target. Validates state/nonce, creates session, sets cookie, 302 to `redirect_to` (default `post_login_redirect`). |
| POST | `/auth/logout` | Deletes session, clears cookie. `204`. |
| GET | `/api/v1/me` | `200 {"auth_enabled":true,"authenticated":true,"user":{"subject":"...","email":"...","name":"..."}}`. With auth disabled: `{"auth_enabled":false,"authenticated":false,"user":null}`. With auth enabled and no session: `401`. |

### Backups
| Method | Path | Response |
|---|---|---|
| GET | `/api/v1/backups` | `200 {"items":[Backup,...]}` sorted by name |
| GET | `/api/v1/backups/{name}` | `200 Backup` / `404 backup_not_found` |
| GET | `/api/v1/backups/{name}/activities?limit=10` | `200 {"items":[Activity,...]}` newest first, max 10 |
| POST | `/api/v1/backups/{name}/run` | `202 Activity` (status `running`) / `404` / `409 backup_running` |

### Activities
| Method | Path | Response |
|---|---|---|
| GET | `/api/v1/activities?limit=10` | `200 {"items":[Activity,...]}` across all backups, newest first, max 10 |
| GET | `/api/v1/activities/{id}` | `200 Activity` / `404` |
| GET | `/api/v1/activities/{id}/log` | `200 text/plain; charset=utf-8` full log (for a running activity: content so far). `404 log_not_found` if file missing. |

### Jobs
| Method | Path | Response |
|---|---|---|
| GET | `/api/v1/jobs` | `200 {"items":[Job,...]}` executions currently running |

## Server-Sent Events

Common: `Content-Type: text/event-stream`, `Cache-Control: no-cache`, heartbeat comment (`: ping`) every 15 s, `X-Accel-Buffering: no`. Client reconnects via `EventSource`; `Last-Event-ID` is honoured where noted.

### `GET /api/v1/jobs/stream`
Live job state for all backups.

| event | data | notes |
|---|---|---|
| `snapshot` | `{"items":[Job,...]}` | sent once on connect |
| `job.started` | `Job` | |
| `job.phase` | `Job` | phase changed |
| `job.finished` | `{"job": Job, "activity": Activity}` | `activity.status` = `success`/`error` |

### `GET /api/v1/activities/{id}/log/stream`
Streams the execution log line by line.

| event | id | data |
|---|---|---|
| `log` | line number (1-based) | `{"line":"2026-10-30T12:00:01Z INFO dump started"}` |
| `end` | - | `{"status":"success"}` or `{"status":"error","error":"..."}`; server then closes |

Behaviour: replays existing lines from the file, then follows live. If the activity is already finished, it replays everything, sends `end`, and closes. `Last-Event-ID: N` resumes after line N. `404` if the activity is unknown.

## Example: manual run

```
POST /api/v1/backups/main_db/run
-> 202
{ "id":"0192...","backup_name":"main_db","status":"running","triggered_by":"manual", ... }

GET  /api/v1/activities/0192.../log/stream   (EventSource)
event: log
id: 1
data: {"line":"2026-10-30T12:00:00Z INFO starting dump"}
...
event: end
data: {"status":"success"}
```
