# Phase 1 — Shell: auth screen, header, sidebar, dashboard

Prereq: `plan.md` read. Base project exists (`npx sv create`).

## Goal
A working, themed application frame backed by REST data: users can authenticate, see all projects with status bullets, and see the dashboard. No live updates yet (manual **Refresh** button only).

## 1. Tooling
- [ ] Swap `@sveltejs/adapter-auto` → `@sveltejs/adapter-static` (`fallback: "index.html"`, `strict: false`) in `svelte.config.js`.
- [ ] `vite.config.ts`: add `tailwindcss()` plugin, dev proxy (`/api`, `/auth`, `/healthz` → `process.env.PGKEEPER_URL ?? "http://localhost:8080"`), Vitest browser project config (existing stack).
- [ ] `npm i cronstrue` (used in phase 2, install now to settle `package.json`).
- [ ] `src/app.css`: `@import "@runwisp/ui/theme.css"; @source "../node_modules/@runwisp/ui";` plus shell styles; `src/app.html`: pre-paint theme script (reads `runwisp:theme`, toggles `.dark`).
- [ ] `src/routes/+layout.ts`: `ssr = false; prerender = false`.
- [ ] `.env.example` with `VITE_MOCK_API=true` and `PGKEEPER_URL`.
- [ ] Re-verify runwisp exports used: `Button, Input, Heading, PageHeader, Separator, StatusDot, ThemeToggle, ToastContainer, toast, Dropdown, Alert`.

## 2. API layer (`src/lib/api`)
- [ ] `types.ts`: TS types mirroring the OpenAPI schemas (`Backup`, `Activity`, `Job`, `Me`, `ResticView`, `ApiErrorBody`, enums `ActivityStatus`, `Trigger`, `Phase`).
- [ ] `client.ts`: `request<T>()` (JSON, `credentials` same-origin default, `Accept: application/json`), `ApiError`, 401 handling that calls `session.expire()` instead of redirecting, `Date`-header skew capture, functions: `getMe, listBackups, listActivities, runBackup(name)` (the rest in phase 2).
- [ ] `mock.ts`: fixtures (≥6 backups: success, error, never-run, running; ≥10 activities) behind `VITE_MOCK_API`.
- [ ] `format.ts`: `relativeShort` (`12s ago` / `5m ago` / `3h ago` / `2d ago`, `just now` < 5s, future → `in 3m`), `formatDuration`, `formatDateTime`, `triggerLabel` (schedule→Scheduled, manual→Manual), `statusLabel`.

## 3. Stores (`src/lib/stores`)
- [ ] `session.svelte.ts`: status machine per FR-A1…A5; `login(path)`, `logout()`, `expire()`.
- [ ] `backups.svelte.ts`: `load()`, sorted list, `statusOf()` → `running|success|error|never`.
- [ ] `activities.svelte.ts`: `recent` (≤10), `load()`.
- [ ] `jobs.svelte.ts`: empty set for now, with `applySnapshot` API stubbed; running count falls back to `backups.filter(b => b.running)`.
- [ ] `connection.svelte.ts`: exposes `state` (`"connecting"` default); real connection in phase 3.
- [ ] `clock.svelte.ts`: shared `now` ticking every 30 s (1 s will be enabled in phase 3 when jobs exist).

## 4. Components
- [ ] `LoginScreen` — centered card: logo, "pgkeeper", one-line description, **Sign in** button, optional "session expired" `Alert`.
- [ ] `AppShell` — grid: fixed header, sidebar, `<main>`; handles drawer state.
- [ ] `Header` — fixed/floating (FR-H1/H2): brand, mobile menu button, user `Dropdown` (**Log out**), `ThemeToggle`; hides logout when `auth_enabled=false`.
- [ ] `Sidebar` + `BackupNav` — Dashboard link, filter input, project list with `StatusBullet`, loading/error/empty states, active-route highlight, drawer behaviour (Esc, backdrop, close on navigate).
- [ ] `StatusBullet` — maps `running|success|error|never` → `StatusDot` tone (`primary`+pulse / `success` / `danger` / `muted`) with accessible label.
- [ ] `ConnectionStatus` — sidebar footer (Connected / Connecting… / Disconnected) bound to `connection.state`.
- [ ] `StatTile`, `RunningPanel`, `AttentionPanel`, `RecentPanel` — see FR-D1…D5. `AttentionPanel` Retry calls `runBackup`; handle 409 (toast "already running") and 503.
- [ ] `+layout.svelte`: bootstrap `session.load()`; on `ready` load backups + recent activities in parallel; render `LoginScreen` / error / shell; mount `<ToastContainer />`.
- [ ] `+page.svelte`: dashboard composed of the above, **Refresh** button re-runs the loads.
- [ ] `+error.svelte`.

## 5. Behaviour details
- Dashboard derivations (pure functions in `lib/dashboard.ts`, unit-tested):
  - projects = `backups.length`
  - running = running backups count
  - failed = backups where `last_activity?.status === "error"`
  - last backup = max `last_activity.finished_at` where not null
- Row links: `/backups/{encodeURIComponent(name)}` and `?activity={id}`.
- Sidebar active state from `page.params.name`.
- Relative-time cells re-render from `clock.now`.

## 6. Tests
- Unit: `relativeShort` boundaries (4s, 59s, 60s, 59m, 3h, 2d), `formatDuration`, dashboard derivations, `statusOf`, session state machine (200/401/network error/auth disabled).
- Component (browser mode): Login screen shown on 401 and button targets `/auth/login?redirect_to=…`; sidebar bullets for each status; drawer opens/closes; dashboard empty states; stat values from fixtures.
- Manual: light/dark/auto switching persists; header stays fixed on scroll; 375 px width usable.

## 7. Acceptance criteria
- [ ] `npm run check` and `npm run test:unit -- --run` green; `npm run build` emits `build/index.html`.
- [ ] Unauthenticated user sees the login screen (never a blank page); sign-in navigates to `/auth/login`.
- [ ] Header fixed with user menu (logout works, returns to login screen) and theme toggle (Auto/Light/Dark).
- [ ] Sidebar lists all projects with correct bullet colors; filter works; responsive drawer works.
- [ ] Dashboard shows the four stats and three panels with real data from `/backups` + `/activities`.
- [ ] Connection indicator is rendered (static "Connecting…" until phase 3).
- [ ] Mock mode runs the full shell without a backend.

## Out of scope
Project page content (phase 2), any SSE (phase 3), polling of any kind.
