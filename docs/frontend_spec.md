# pgkeeper – Frontend Spec

Developer / maintainer guide for the web UI in `web/`. Based on the repo at `banshee-lab/pgkeeper` (HEAD `39b8fbb`). Backend details are in `backend_spec.md`.

## 1. What it is

A static single-page app (SvelteKit, Svelte 5 runes, TypeScript) that is **embedded into the Go binary** and served by the backend. It shows backup projects, live job progress, run history with logs, restic repositories (maintenance, snapshot browser) and lets users trigger backups, restores and maintenance.

| Item | Value |
| --- | --- |
| Framework | SvelteKit 3 + Svelte 5 (runes mode forced for all non-`node_modules` files) |
| Build | Vite 8, `@sveltejs/adapter-static` with `fallback: 'index.html'` (SPA) |
| Rendering | `ssr = false`, `prerender = false` (`src/routes/+layout.ts`) |
| Styling | Tailwind CSS 4 (`@tailwindcss/vite`, forms + typography plugins) |
| UI kit | `@runwisp/ui` (Button, Heading, Input, Alert, toast, ToastContainer, …) |
| Other deps | `cronstrue` (human-readable cron) |
| Tests | Vitest 4 (browser mode via Playwright for `*.svelte.spec.ts`, node for `*.spec.ts`), Playwright e2e |
| Package manager | **Yarn 1.22.22** (`engine-strict=true` in `.npmrc`; CI uses Node 26, Dockerfile uses Node 22) |
| Import aliases | `#lib` → `src/lib/index.ts`, `#lib/*` → `src/lib/*` (`package.json#imports` + `tsconfig` paths) |

## 2. Project structure

```
web/
  package.json, yarn.lock, .npmrc, .env.example
  vite.config.ts            Dev proxy, adapter-static, vitest projects
  playwright.config.ts      e2e: `yarn build && yarn preview` on :4173
  tsconfig.json
  embed.go                  //go:embed all:build  -> package web (used by internal/api/router.go)
  static/                   favicon.png, robots.txt
  src/
    app.html, app.css, app.d.ts
    routes/
      +layout.ts            ssr=false, prerender=false
      +layout.svelte        Bootstrap: session -> data -> SSE; auth gate; AppShell
      layout.css            Tailwind import + plugins
      +page.svelte          Dashboard (/)
      +error.svelte
      backups/[name]/+page.svelte        Project detail (/backups/:name?activity=:id)
      repositories/[id]/+page.svelte     Repository detail (/repositories/:id?activity=:id)
      page.svelte.e2e.ts    Playwright tests
    lib/
      api/
        client.ts           fetch wrapper + every endpoint function (+ mock switch)
        types.ts            TS mirrors of backend DTOs
        mock.ts             Mock data (used when VITE_MOCK_API=true)
      stores/               Svelte 5 rune-based singleton stores (*.svelte.ts)
      components/           UI components (+ *.svelte.spec.ts)
      dashboard.ts          Pure functions deriving dashboard data + route hrefs
      format.ts             Pure formatters (time, duration, bytes, cron, URL redaction, ANSI strip)
      vitest-examples/      Template leftovers (Welcome, greet) – safe to delete
```

## 3. Where to find things

| Topic | Location |
| --- | --- |
| App bootstrap / auth gate | `src/routes/+layout.svelte` |
| Dashboard | `src/routes/+page.svelte` + `AttentionPanel`, `RunningPanel`, `RecentPanel`, `NextSchedulesPanel`, `StatTile` |
| Project (backup) page | `src/routes/backups/[name]/+page.svelte` + `DetailsPanel`, `ActivityList`, `OutputPanel`, `RunButton`, `RestoreButton` |
| Repository page (maintenance + snapshot browser) | `src/routes/repositories/[id]/+page.svelte` (single large file, ~540 lines) |
| HTTP calls | `src/lib/api/client.ts` |
| API types | `src/lib/api/types.ts` (must follow `docs/openapi.yaml` / `internal/api/dto.go`) |
| Live updates (jobs SSE) | `src/lib/stores/connection.svelte.ts` |
| Live log tail (SSE) | `src/lib/stores/log-stream.svelte.ts` |
| Session / login state | `src/lib/stores/session.svelte.ts` |
| Layout shell | `AppShell`, `Header`, `Sidebar`, `BackupNav`, `ConnectionStatus` components |
| Restore UI | `src/lib/components/RestoreButton.svelte` |
| Theme / CSS variables | `src/app.css` (uses `@runwisp/ui/theme.css`, `--header-height`, `--sidebar-width`, `.panel`, `.shell-*`) |
| Embedding into the Go binary | `web/embed.go`, `internal/api/router.go` |

## 4. Architecture

### 4.1 Boot flow (`+layout.svelte`)

1. `onMount`: start `clock` (shared ticking `now` for relative times) and call `bootstrap()`.
2. `session.load()` → `GET /api/v1/me`.
3. Status → UI:
   - `loading` – "Checking session…"
   - `unauthenticated` / `expired` – `LoginScreen` (button redirects to `/auth/login?redirect_to=<safe path>`)
   - `error` – retry screen
   - `ready` – render `AppShell` + page
4. When `ready`: `Promise.all([backups.load(), activities.load()])`, then `connection.connect()` (opens the jobs SSE stream).
5. Any `401` from `request()` calls the registered unauthorized handler → `session.expire()` (clears stores, shows login with "expired" notice). `getMe` is exempt (`expireOnUnauthorized: false`).
6. Logout: `connection.close()` → `POST /auth/logout` → clear stores.

`loginRedirectUrl()` only accepts same-site absolute paths (rejects `//…` and `/\…`) to prevent open redirects (backend validates again).

### 4.2 State management

No external state library. Each store is a module-level object using `$state` with getters (`src/lib/stores/*.svelte.ts`):

| Store | Holds | Notes |
| --- | --- | --- |
| `session` | status, user, authEnabled, error | `expire()` also clears the other stores |
| `backups` | `Backup[]` sorted by name, load state | `load()`, `loadOne(name)`, `statusOf()` |
| `activities` | `recent` (backup + repository activities merged, top 10), `repositories`, `perBackup` map | `load()`, `loadFor(name)`, `upsert(activity)`, `getById()` |
| `jobs` | running jobs by `activity_id` | updated from SSE only |
| `connection` | SSE connection state + subscribe API | exponential backoff 1 s → 30 s |
| `logStream` | lines (cap 20 000), state, truncation | single active stream |
| `clock` | `now` ticker | started/stopped in layout |

### 4.3 Real-time updates

**Jobs stream** (`connection.svelte.ts`): `EventSource('/api/v1/jobs/stream')` with named events:

| Event | Handler effect |
| --- | --- |
| `snapshot` | replace `jobs`, re-fetch `backups` and `activities` |
| `job.started` | add job, mark backup `running`, upsert a synthetic running activity |
| `job.phase` | update job phase |
| `job.finished` (`{job, activity}`) | remove job, update backup `last_activity`, upsert activity, refetch the backup if success |

On `CLOSED` it schedules a reconnect that first probes `/api/v1/me` (401 → `session.expire()`), because the backend closes streams when the session expires. Components can also `connection.subscribe(type, handler)`.

**Log stream** (`log-stream.svelte.ts`): `EventSource('/api/v1/activities/{id}/log/stream')`, events `log` (`{id, line}`, de-duplicated by id) and `end` (`{status, error?}`). On a hard error it falls back to `GET …/log` (plain text) and renders the final log. ANSI sequences are stripped (`format.stripAnsi`).

The repository page opens its own `EventSource` for maintenance logs (`…/repositories/{id}/activities/{activity}/log/stream`) and also polls `listRepositoryActivities` every 3 s (`setInterval`) to refresh state.

### 4.4 API layer (`src/lib/api/client.ts`)

- `request<T>()`: `fetch` with `credentials: 'same-origin'`, JSON `Accept`, maps non-2xx to `ApiError(status, code, message)` using the backend envelope `{error:{code,message}}`; network failures → `ApiError(0, 'network_error', …)`. It also computes `serverClockOffset` from the `Date` header; use `serverNow()` instead of `Date.now()` for server-relative timing. `requestText()` is the plain-text variant (logs).
- Endpoint helpers: `getMe, listBackups, getBackup, listBackupActivities, listActivities, getActivity, getActivityLog, runBackup, listRestoreSnapshots, startTestRestore, startContainerRestore, logoutRequest, listRepositories, getRepository, listRepositoryActivities, listRepositorySnapshots, getRepositorySnapshotStats, listSnapshotTree, startRepositoryActivity, forceRepositoryUnlock`.
- List endpoints return `{items: T[], next?}`; helpers unwrap `items`.
- **Mock mode:** `const mockEnabled = import.meta.env.VITE_MOCK_API === 'true'`. Several helpers (me, backups, activities, logs, run) return data from `mock.ts` instead of hitting the backend. Not every function has a mock branch (repository/restore calls hit the network), so the dev server needs a real backend anyway for those pages.

### 4.5 Routes

| URL | File | Behavior |
| --- | --- | --- |
| `/` | `+page.svelte` | Stats (projects, running, recently failed, last backup), running panel, attention panel, recent activity, next schedules, manual refresh |
| `/backups/:name` | `backups/[name]/+page.svelte` | Project details, last 10 activities, selected activity log (live if running), Run + Restore buttons. Selected activity is in `?activity=<id>` (kept in sync with `goto(..., {replaceState})`). Defaults to the running activity, else the newest. |
| `/repositories/:id` | `repositories/[id]/+page.svelte` | Repo info (size, snapshot count, next prune/check), buttons **Run check / Run prune / Force unlock**, maintenance activity history + log, snapshot list (client-side tag filter, 5 per page, per-snapshot stats), file browser with per-file download link |
| anything else | SPA fallback served by the backend | `+error.svelte` |

Dynamic params come from `page.params` (`$app/state`). Because the backend serves `index.html` for unknown GET paths, deep links and reloads work.

### 4.6 Key components

- `RestoreButton.svelte` – native `<dialog>`; step 1 choose **Test restore** (immediate `POST …/restore/test`) or **Restore to container**; step 2 form (container, database, snapshot picker from `restore/snapshots`, "include roles" only enabled if the snapshot `has_roles`). After starting it upserts the activity, navigates to `/backups/:name?activity=:id` and toasts. A `409` is shown as "already running". The container restore **drops the target database** – keep a clear warning in the dialog when editing.
- `RunButton.svelte` – `POST …/run`, handles 409/503.
- `OutputPanel.svelte` – log viewer; `ActivityList.svelte` – activity picker; `DetailsPanel.svelte` – config summary (restic repo displayed through `redactUrl`).
- `BackupNav.svelte` / `Sidebar.svelte` / `Header.svelte` / `AppShell.svelte` – navigation shell; sidebar becomes a drawer below 768 px (`aria-label` "Open navigation" / "Close navigation", dialog "Primary navigation" – the e2e test depends on these labels).
- `StatusBullet`, `StatTile`, `NextRun`, `ConnectionStatus` – small presentational pieces.

### 4.7 Helpers

- `format.ts`: `relativeShort`, `nextRunUpdateDelay`, `formatDuration`, `formatBytes`, `formatDateTime`, `triggerLabel`, `statusLabel`, `cronText` (cronstrue), `redactUrl` (hides credentials in repository URLs), `stripAnsi`.
- `dashboard.ts`: `deriveDashboard` (counts, failed list, last backup), `deriveSchedules` (merges next backup runs and repository prune/check times), `activityHref`, `repositoryActivityHref`, `repositoryLabel`, `projectHref`.

## 5. Types and the backend contract

`src/lib/api/types.ts` mirrors `internal/api/dto.go`. Notable points:

- `ActivityKind = 'backup' | 'test_restore' | 'restore'`; `kind`/`label` may be absent in older payloads → treat as backup.
- `Phase` = `queued | dump | roles | restic | cleanup | snapshot | provision | restore` (same as `domain.Phase`).
- `Backup.latest_snapshot`, `last_activity`, `next_run`, `last_success_at` can be `null`.
- `Job.status` is always `'running'`.
- `RepositorySnapshot` fields come straight from restic (`id`, `short_id`, `time`, `hostname`, `paths`, `tags`, optional `summary`).
- **Field-name mismatch to remember:** `RepositorySnapshotStats` / `Snapshot` use `files_added, files_changed, bytes_added`, while restic's raw summary uses `files_new, files_changed, data_added`; the repository page converts between them in `load()`.
- `RepositoryActivity.kind` is typed `string` (backend: `prune | check | unlock`).
- When the backend DTOs change, update `types.ts`, `client.ts`, `mock.ts`, and the relevant stores/tests together.

## 6. Development

Requirements: Node (22+ recommended; CI uses 26), Yarn 1.22.x, and for the full app a Go toolchain.

```sh
cd web
yarn install --frozen-lockfile
yarn dev            # Vite dev server; proxies /api, /auth, /healthz to the backend
yarn build          # outputs web/build (embedded by the Go binary)
yarn preview
yarn check          # svelte-kit sync + svelte-check (types)
yarn test:ci        # vitest --run (unit + browser component tests)
yarn test:e2e       # playwright install && playwright test (builds + previews on :4173)
yarn test           # unit + e2e
```

- **Dev proxy target:** env `PGKEEPER_URL` (default `http://localhost:8080`), only in `development` mode (`vite.config.ts`). Run a backend with `make run CONFIG=./local/pgkeeper.toml` (or `docker compose up --build` from the repo root) in parallel.
- **Mock API:** `.env.example` contains `VITE_MOCK_API=true`. Vite only loads `.env`, `.env.local`, `.env.[mode]`, so the example does nothing until copied to `.env`. **Never leave `VITE_MOCK_API=true` in an `.env` used for a production build**: it is inlined at build time and would ship the mock data path.
- **Same-origin matters in dev:** mutating requests need a matching `Origin`/`Host`. Using the Vite proxy from the browser works; calling the backend from a different origin returns `403`.
- Auth in dev: the local config has no `[auth]`, so `/api/v1/me` reports `auth_enabled: false` and the app skips login. To test login you need an OIDC provider and `cookie_secure = false` on plain HTTP.

### Testing layout

| Pattern | Runner | Environment |
| --- | --- | --- |
| `src/**/*.svelte.{test,spec}.ts` | Vitest project `client` | real Chromium via `@vitest/browser-playwright` |
| other `src/**/*.{test,spec}.ts` | Vitest project `server` | Node |
| `src/**/*.e2e.ts` | Playwright | `yarn build && yarn preview` |

Existing specs: `format.spec.ts`, `dashboard.spec.ts`, stores (`activities`, `backups`, `connection`, `session`), components (`LoginScreen`, `StatusBullet`). `vitest` is configured with `expect.requireAssertions: true` (every test must assert). The e2e test relies on mock data (it expects "billing-prod" and a stats tile showing `11`), so it only passes with `VITE_MOCK_API=true` (copy `.env.example` to `.env`). CI runs `yarn test:ci` only, so e2e is not executed there.

### Building into the Go binary

- `make web-build` runs `cd web && yarn build`; `make build` then runs `go build`. The Dockerfile builds the frontend in a separate Node stage and copies `web/build` into the Go build stage.
- `web/build` is git-ignored; Go builds fail without that directory (`//go:embed all:build`). For backend-only work create an empty one: `mkdir -p web/build && touch web/build/.gitkeep`.

## 7. Conventions to follow

- Svelte 5 runes only (`$state`, `$derived`, `$props`, `$effect`); no legacy `export let` / stores.
- Singleton stores in `src/lib/stores/*.svelte.ts` expose getters + methods (see `backups.svelte.ts`) and are cleared on session loss.
- Keep components presentational where possible; put pure logic in `format.ts` / `dashboard.ts` so it can be unit-tested in Node.
- Use `@runwisp/ui` components and the theme tokens (`text-on-surface-muted`, `border-outline`, `text-danger-soft-text`, …) rather than hard-coded colors; global bits live in `app.css`.
- Use `#lib/...` imports, not relative `../../`.
- Use `serverNow()` / `clock.now` for time-relative UI, not `Date.now()`.
- All network access goes through `client.ts` (except the `EventSource` URLs in stores and the plain download `<a href>` in the repository page).
- Any string from the backend that may carry credentials (repository URLs) goes through `redactUrl` before display; the backend already redacts, this is defense in depth.
- Accessibility hooks already used by tests: `aria-label="Backup statistics"`, nav drawer labels, `aria-live` on loading text.

## 8. Common change recipes

| Task | Touch |
| --- | --- |
| New API field | backend `dto.go` + `docs/openapi.yaml` → `types.ts` → `mock.ts` → UI |
| New endpoint call | add a function to `client.ts` (with `request<T>`), then use it from a store or page |
| New SSE event | backend `stream.go` → handler map in `connection.svelte.ts` (`JobsEventType`) |
| New phase/kind label | `types.ts` unions, `format.ts` label helpers, `RunningPanel`/`ActivityList` display |
| New page | `src/routes/<path>/+page.svelte`, link from `BackupNav`/`Sidebar`/dashboard helpers (`dashboard.ts` href functions) |
| New component test | `Name.svelte.spec.ts` next to the component (browser project) |

## 9. Known rough edges

- `repositories/[id]/+page.svelte` is a ~540-line page mixing data loading, SSE, polling, snapshot paging and the file browser; extracting components/stores would help (`RepositoryActivityStore`-like store, `SnapshotBrowser` component).
- The dashboard loads repository activities with one request per repository (`activities.load()` → `listRepositoryActivities` for each repo), so cost grows with the number of repositories; every jobs-stream `snapshot` event triggers a full reload.
- `listActivities()` fetches `limit=10`; the dashboard "Recent activity" is capped at 10 and merged client-side with repository activities.
- `src/lib/vitest-examples/` and `web/README.md` are unmodified `sv create` template files.
- Mock mode only partially covers the API (see 4.4).
- Node versions differ between CI (26) and the Docker build (22.23.2). `.npmrc` sets `engine-strict=true` but `package.json` has no `engines` field, so it currently enforces nothing.
