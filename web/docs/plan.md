# pgkeeper web — Master Plan

Frontend for **pgkeeper**, a PostgreSQL backup scheduler (`pg_dump` via `docker exec` → restic).
This document replaces the original prompt and is the source of truth. Phase documents implement it; `backend_plan.md` lists what the API still needs.

| Doc                          | Purpose                                          |
| ---------------------------- | ------------------------------------------------ |
| `plan.md`                    | Requirements, decisions, rules (this file)       |
| `phase-1-layout.md`          | Shell: auth screen, header, sidebar, dashboard   |
| `phase-2-project-details.md` | Project page: details, activity, output, run now |
| `phase-3-sse.md`             | Live updates: jobs stream, log stream, toasts    |
| `backend_plan.md`            | Required / recommended backend changes           |

---

## 1. Constraints (non-negotiable)

1. **UI components come exclusively from `@runwisp/ui`** (`^1.8.0`). No other component library. Extra dependencies are allowed for non-UI needs only (e.g. `cronstrue`).
2. The base project (`npx sv create`, SvelteKit 3, Svelte 5 runes, Tailwind v4, TypeScript strict) is the starting point. Keep its dev tooling: **yarn**, Vitest browser mode (`vitest-browser-svelte` + Playwright). Do not introduce jsdom/Testing Library.
3. The sample project in `archive.tar.gz` (Git Clone Manager) is the **visual and structural reference**, not code to copy blindly. Its `UI-NOTES.md` is the component inventory.
4. Backend contract = `openapi.yaml` (pgkeeper API 1.0.0). Anything the contract lacks goes to `backend_plan.md`, and the frontend must degrade gracefully without it.
5. **No polling for data.** Updates come from SSE. Only local clock ticks (elapsed time / relative time) run on timers.

## 2. Technical decisions

| Topic         | Decision                                                                                                                               | Why                                          |
| ------------- | -------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------- |
| Rendering     | SPA: `ssr = false`, `prerender = false`                                                                                                | Cookie-session app, SSE, no SEO needs        |
| Adapter       | Replace `adapter-auto` with `@sveltejs/adapter-static` (`fallback: "index.html"`, `strict: false`)                                     | SPA fallback; served same-origin by pgkeeper |
| Origin        | Same-origin only; all URLs relative (`/api/v1/...`, `/auth/...`)                                                                       | Cookie `pgkeeper_session`, no CORS           |
| Dev proxy     | Vite proxies `/api`, `/auth`, `/healthz` → `PGKEEPER_URL` (default `http://localhost:8080`)                                            | Real backend in dev                          |
| Mock mode     | `VITE_MOCK_API=true` serves in-memory fixtures (and a simulated SSE in phase 3)                                                        | Work without backend                         |
| Aliases       | Use SvelteKit `$lib` (leave the base `imports` map untouched)                                                                          | Avoids `.ts` extension friction of `#lib/*`  |
| State         | Svelte 5 runes stores in `*.svelte.ts` (module singletons exposing getters)                                                            | Matches sample, no extra lib                 |
| Theme         | `ThemeToggle` from runwisp (Auto/Light/Dark, persisted as `runwisp:theme`); pre-paint script in `app.html` toggles `.dark` on `<html>` | Package owns the store; it is not exported   |
| Icons         | None added; text glyphs + `StatusDot`                                                                                                  | No icon lib in runwisp                       |
| Cron display  | `cronstrue` for human text, raw cron always shown too                                                                                  | Readability                                  |
| Time          | Browser locale/timezone via `Intl`; clock skew corrected using the `Date` response header                                              | No backend change needed                     |
| Package check | Re-verify runwisp exports at install (`Dropdown` is used by the sample but absent from its UI-NOTES table)                             | Notes were verified against 1.7.0            |

## 3. Functional requirements

### Auth

- **FR-A1** Bootstrap with `GET /api/v1/me`.
  - `200` and (`auth_enabled=false` or `authenticated=true`) → app.
  - `401`, or `200` with `auth_enabled && !authenticated` → **Login screen**.
  - Network/5xx → full-page error with Retry.
- **FR-A2** Login screen: brand, short text, single button "Sign in" → `location.assign('/auth/login?redirect_to=<current path+search>')`. `redirect_to` must be a relative path starting with a single `/`. No automatic redirect: the screen is always shown.
- **FR-A3** Any later `401` flips the session to "expired": SSE closed, Login screen shown with "Your session expired".
- **FR-A4** Logout: `POST /auth/logout` (204) → clear stores, close SSE, show Login screen (no full reload).
- **FR-A5** If `auth_enabled=false`: hide the user menu logout; header shows only the theme toggle.

### Header

- **FR-H1** Fixed, floating top bar (always visible while scrolling), above the sidebar and content.
- **FR-H2** Left: mobile menu button (<768px), logo + "pgkeeper". Right: user (name, falling back to email/subject) with a menu containing **Log out**, and the **theme changer** (Auto/Dark/Light).

### Sidebar

- **FR-S1** Responsive: persistent column ≥768px; off-canvas drawer with backdrop <768px (closes on navigation, Esc, backdrop click).
- **FR-S2** Top: "Dashboard" link. Below: filter input (client-side) and the project list.
- **FR-S3** Each project row: status bullet + name; active row highlighted.
  - green = last activity `success`, red = `error`, gray = never run, **pulsing primary = running now** (an addition to the requested three, so a running backup is never shown as stale).
  - The bullet has an accessible text equivalent (`title` + visually hidden text). Color is never the only signal.
- **FR-S4** Footer: connection indicator — green **Connected**, pulsing amber **Connecting…**, red **Disconnected** (retrying). Reflects the jobs SSE stream.

### Dashboard (`/`)

- **FR-D1** Stat row (4 cells): **Projects** (count), **Running now** (count), **Recently failed** (projects whose latest activity is `error`; highlighted when > 0), **Last backup** (relative time since the most recent finished activity: `12s ago`, `5m ago`, `3h ago`, `2d ago`).
- **FR-D2** **Running now** panel: running jobs with project, trigger, phase, live elapsed time; each links to the project with that activity selected.
- **FR-D3** **Needs attention** panel: projects whose latest activity failed — name, error summary, time since failure, link to the failed activity, and a **Retry** button (`POST /run`).
- **FR-D4** **Recent activity** panel: last 10 activities across projects (status, project, trigger, relative time, duration), each linking to `/backups/{name}?activity={id}`.
- **FR-D5** Empty/first-run states for zero projects, nothing running, nothing failing.

### Project page (`/backups/[name]`)

- **FR-P1** Header: project name, status bullet, **Backup now** button (disabled while running; spinner while the request is in flight).
- **FR-P2** **Details panel** (read-only): database, container, host:port, username, cron (+ human text), next run (relative + absolute), roles dump yes/no, restic repository, tag, retention (daily/weekly/monthly/yearly). **Secrets are never shown**: the API already redacts, and the UI additionally masks any `user:password@` userinfo in URLs as a defense in depth.
- **FR-P3** **Activity panel**: latest ≤10 activities: status (dot + label), start date/time, duration (live while running), trigger (Manual/Scheduled), error text when failed. The running one is pinned/marked.
- **FR-P4** **Output panel**: shows the log of the selected activity. The selected activity is unmistakable: highlighted row in the list **and** a header in the panel repeating its status, start time and trigger. Copy and Download actions. Empty state "Select an activity to view its output".
- **FR-P5** Deep link `?activity=<uuid>` selects an activity on load and is kept in sync with selection (`replaceState`, no history spam).
- **FR-P6** Finished activity → log loaded once via `GET /log`. Running activity → live stream (phase 3). **No log request is made unless an activity is selected; no stream is opened unless the selected activity is running.**
- **FR-P7** While a backup of the open project is running, its activity is shown at the top and auto-selected, its console streams live. The user may select another activity; the live one then stays available via a "Running — view live output" chip and is not auto-re-selected until the next run starts.

### Live behaviour (SSE)

- **FR-L1** One global `EventSource` on `/api/v1/jobs/stream`, opened after authentication, closed on logout/expiry.
- **FR-L2** `snapshot` replaces the running-jobs set and triggers a resync of `/backups` and `/activities` (also on every reconnect).
- **FR-L3** `job.started` → temporary toast ("Backup *name* started · scheduled/manual"), project bullet → running, dashboard counters/panels update, synthetic running activity row inserted.
- **FR-L4** `job.phase` → update phase shown in panels.
- **FR-L5** `job.finished` → toast (success/error), `backup.last_activity` and `running=false` updated from the payload, dashboard panels recomputed, activity list updated. No refetch needed.
- **FR-L6** Per-activity log stream: `GET /api/v1/activities/{id}/log/stream`, events `log` (`{line}`, SSE id = line number) and `end` (`{status,error?}`). Close the source on `end` (otherwise `EventSource` reconnects). Support resume via the browser's automatic `Last-Event-ID`.

## 4. Non-functional rules

- **Accessibility**: keyboard navigable, visible focus, `aria-live="polite"` on status regions, `role="log"` on the console with `aria-live="off"`, dialogs/drawer trap focus and close on Esc.
- **Performance**: log lines appended in batches (rAF); keep at most **20,000 lines** in memory (drop oldest, show "older lines truncated" notice); auto-scroll to bottom with a pause toggle that also pauses when the user scrolls up.
- **Resilience**: every request has typed errors (`ApiError{status, code, message}`); user-facing messages come from the API error envelope. `409 backup_running` and `503 unavailable` have dedicated copy.
- **Connections budget**: max 2 SSE connections per tab (jobs + one log). Opening a new log stream closes the previous.
- **Security**: no `{@html}` for logs or API data; strip ANSI escape sequences before rendering; never put tokens in URLs.
- **Resync rule**: after any SSE reconnect, the snapshot is authoritative; local derived state is rebuilt from it plus a REST refetch.
- **Code quality**: `npm run check` and `npm run test:unit -- --run` must pass in every phase; ESLint/Prettier are optional additions, not required.

## 5. API usage map

| Need                                                 | Endpoint                                           | When                            |
| ---------------------------------------------------- | -------------------------------------------------- | ------------------------------- |
| Auth state                                           | `GET /api/v1/me`                                   | Bootstrap, after 401 probes     |
| Project list + last status + running flag + next run | `GET /api/v1/backups`                              | Bootstrap, SSE resync           |
| Project details                                      | `GET /api/v1/backups/{name}`                       | Opening a project page          |
| Project activities                                   | `GET /api/v1/backups/{name}/activities?limit=10`   | Opening a project page, resync  |
| Recent activity (all)                                | `GET /api/v1/activities?limit=10`                  | Dashboard load, resync          |
| Run now                                              | `POST /api/v1/backups/{name}/run` → 202 `Activity` | Button / Retry                  |
| Finished log                                         | `GET /api/v1/activities/{id}/log`                  | Selecting a finished activity   |
| Deep-link activity                                   | `GET /api/v1/activities/{id}`                      | `?activity=` not in loaded list |
| Live jobs                                            | `GET /api/v1/jobs/stream` (SSE)                    | Always, once authenticated      |
| Live log                                             | `GET /api/v1/activities/{id}/log/stream` (SSE)     | Selected activity is running    |
| Login / logout                                       | `GET /auth/login`, `POST /auth/logout`             | Login screen / menu             |

`GET /api/v1/jobs` is **not** used in normal flow (the SSE `snapshot` replaces it); it is a fallback only if the stream is down at bootstrap.

All dashboard figures derive client-side from `/backups` + `/activities` + jobs. No aggregate endpoint is required (an optional one is proposed in `backend_plan.md`).

## 6. State architecture

```
session.svelte.ts      me, status: loading|ready|unauthenticated|expired|error, user, authEnabled
backups.svelte.ts      Map<name, Backup>; list (sorted by name); statusOf(backup); applyJobStarted/Finished
jobs.svelte.ts         Map<activity_id, Job>; byBackup(name); applySnapshot/Started/Phase/Finished
activities.svelte.ts   recent[] (≤10, all backups); perBackup cache; upsert(activity)
connection.svelte.ts   state: connecting|connected|disconnected; owns the jobs EventSource; subscribe(type, handler)
log-stream.svelte.ts   createLogStream(activityId): lines, status, state; close()
clock.svelte.ts        now (1 s tick, only active while something is running) + 30 s tick otherwise
```

Derived status for a backup: `running` (jobs has it, or `backup.running`) → `success|error` from `last_activity.status` → `never` when `last_activity` is null.

## 7. Routes

| Route             | Content                                                    |
| ----------------- | ---------------------------------------------------------- |
| `/`               | Dashboard                                                  |
| `/backups/[name]` | Project page, optional `?activity=<uuid>`                  |
| (layout)          | Login screen replaces the whole shell when unauthenticated |
| `+error.svelte`   | Not found / unexpected errors, inside the shell            |

Unknown `[name]` → API `404 backup_not_found` → "Project not found" state with link to the dashboard.

## 8. Visual design (from the sample)

Compact "operator console", not a metrics-card dashboard.

- Palette and fonts from `@runwisp/ui/theme.css` only (`--rw-surface`, `--rw-surface-raised`, `--rw-outline`, `--rw-on-surface`, `--rw-on-surface-muted`, `--rw-primary`, `--rw-danger*`). TASA Orbiter for prose, Geist Mono for brand, nav and operational labels. Body 0.8125rem.
- Header 3.5rem, `surface-raised`, bottom border; floating = `position: fixed`, full width, subtle backdrop blur/shadow; content offset by header height.
- Sidebar 14.5rem, `surface-raised`, right border; active row tinted with `color-mix(in srgb, var(--rw-on-surface) 5%, transparent)`; drawer below 768px.
- Flat panels: transparent background, 1px `--rw-outline` top rule, radius ≤ 0.2rem, no shadows.
- Stat row: four cells separated by hairlines, large mono number (1.45rem), small mono label; 2×2 grid on mobile; the failed cell turns danger-tinted when > 0.
- Max content width 80rem. Project page: details (two-column `dl`) on top; below, **activity list (left) + output console (right)**, stacking on <1024px.
- Console: runwisp `CodeBlock` (`language="text"`, `copyable`), mono, scrollable region with fixed max height; "Live" badge with pulsing `StatusDot` while streaming.
- Light/dark both first-class: use tokens only, no hard-coded colors (fallbacks allowed on `--rw-danger*`).
- Tailwind v4: `app.css` imports `@runwisp/ui/theme.css` and has `@source "../node_modules/@runwisp/ui";`.

## 9. Target structure

```
src/
  app.html                    pre-paint theme script
  app.css                     theme import, @source, shell tokens
  lib/
    api/        client.ts  types.ts  mock.ts  errors.ts
    stores/     session / backups / jobs / activities / connection / log-stream / clock (.svelte.ts)
    format.ts   relativeShort, duration, dateTime, triggerLabel, stripAnsi, redactUrl, cronText
    components/ AppShell  Header  Sidebar  BackupNav  ConnectionStatus  LoginScreen
                StatTile  StatusBullet  RunningPanel  AttentionPanel  RecentPanel
                DetailsPanel  ActivityList  OutputPanel  RunButton
  routes/
    +layout.ts  +layout.svelte  +page.svelte  +error.svelte
    backups/[name]/+page.svelte
  test/FakeEventSource.ts     scripted SSE double for unit/browser tests
```

## 10. Phases

1. **Phase 1 — Shell**: tooling switch, auth screen, header, sidebar, dashboard over REST (static data, manual refresh). Connection indicator wired to a stub store.
2. **Phase 2 — Project page**: details, activities, output (finished logs), run now, deep links.
3. **Phase 3 — SSE**: jobs stream, reducers, toasts, live log console, reconnection, dev simulator.

Each phase ends shippable and green (`check` + unit tests).

## 11. Assumptions (please confirm or correct)

1. pgkeeper serves the built SPA same-origin and falls back to `index.html` for non-API routes (see `backend_plan.md` B-1).
2. The theme toggle sits beside the user menu rather than inside it, because runwisp's `Dropdown` only supports plain items and `ThemeToggle` is its own dropdown.
3. "Recently failed" = projects whose **latest** activity failed (the API has no time-window filter yet).
4. "Last backup" = most recent `finished_at` across all projects' latest activities.
5. Backup configuration is static for the process lifetime (no add/remove at runtime). If it is hot-reloaded, see `backend_plan.md` R-7.
6. Dev backend runs on `http://localhost:8080`.
