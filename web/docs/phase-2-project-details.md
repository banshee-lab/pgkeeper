# Phase 2 — Project details page

Prereq: phase 1 merged. Route: `/backups/[name]` with optional `?activity=<uuid>`.

## Goal
Full project page over REST: details, latest activities, output of a selected finished activity, and **Backup now**. Running activities are visible, but their live streaming is phase 3 (phase 2 shows a one-off snapshot of the log for them).

## 1. API additions (`src/lib/api/client.ts`)
- [ ] `getBackup(name)`, `listBackupActivities(name, limit=10)`, `getActivity(id)`, `getActivityLog(id): Promise<string>` (text/plain, no JSON parsing), `runBackup(name)` (already from phase 1).
- [ ] Error mapping: `404 backup_not_found` / `activity_not_found` / `log_not_found`, `409 backup_running`, `503 unavailable`, `400 invalid_request`.
- [ ] Mock fixtures for details, per-backup activities and logs (include one ANSI-colored log and one long log of ~5k lines).

## 2. Store changes
- [ ] `backups.svelte.ts`: `loadOne(name)` merges into the shared map (keeps sidebar bullets consistent).
- [ ] `activities.svelte.ts`: `perBackup: Map<name, Activity[]>`; `upsert(activity)` updates both `recent` and `perBackup`; `loadFor(name)`.
- [ ] After a successful `runBackup`, upsert the returned `Activity` (status `running`), mark `backup.running = true`, select it.

## 3. Components
- [ ] `RunButton` — primary **Backup now**; disabled when `running`; `loading` while the POST is in flight; on `409` toast "Already running" and select the running activity; on `503` toast "Server is shutting down"; success toast "Backup started".
- [ ] `DetailsPanel` — `dl` grid per FR-P2:
  - Source: database, container, `host:port`, username.
  - Schedule: cron (mono) + `cronstrue` text, next run (relative + absolute title), roles dump (Yes/No).
  - Destination: restic repository, tag, retention (`daily / weekly / monthly / yearly`, show `—` for unset).
  - `redactUrl()` masks any `scheme://user:pass@` userinfo; unit-tested. No field named password/secret/token is ever rendered.
- [ ] `ActivityList` — rows for ≤10 activities: `StatusBullet`, status label, start `formatDateTime`, duration (`—` while running, live elapsed from `clock.now` once phase 3 enables the 1 s tick), trigger label, error text clamped to 2 lines with full text in `title`. Selected row: highlighted background + left accent border + `aria-current="true"`. Running row labelled "Running".
- [ ] `OutputPanel`:
  - Header: "Output" + selected activity summary (status dot, status, start time, trigger, duration) — this is the "which activity is shown" indicator.
  - Body: runwisp `CodeBlock` (`language="text"`, `copyable`) in a bounded scroll area.
  - States: no selection ("Select an activity to view its output"), loading, `log_not_found` ("No log available"), error with Retry, empty log.
  - Actions: **Copy**, **Download** (fetch text → `Blob` → `<a download="{backup}-{started_at}.log">`; no extra backend support needed).
  - Logs pass through `stripAnsi`; shown as plain text. Cap rendering at 20,000 lines with a truncation notice.
- [ ] Running activity selected in phase 2: fetch `/log` once, show a notice "Live streaming is added in phase 3", and a manual **Reload** button (user-initiated, not a poll).

## 4. Page logic (`routes/backups/[name]/+page.svelte`)
1. On `name` change: load backup + activities in parallel (`Promise.all`), reset selection, abort stale requests (token/`AbortController`).
2. Selection source of truth = `?activity=`: read on load, write with `goto(url, { replaceState: true, keepFocus: true, noScroll: true })`.
3. If `?activity` is not in the list: `getActivity(id)`; if it belongs to another backup → ignore selection and show a notice; if `404` → "Activity not found" and clear the param.
4. Select behaviour:
   - finished (`success|error`) → `getActivityLog(id)` once;
   - running → snapshot fetch (see above);
   - no selection → **no request**.
5. Default selection on first load without `?activity`: the running activity if any, else the most recent activity (decision: shows something useful immediately; its log is one request).
6. Layout: header (name, bullet, RunButton) → `DetailsPanel` → grid `ActivityList | OutputPanel` (stack <1024px; on mobile selecting an activity scrolls the output into view).
7. States: loading skeleton text, `404 backup_not_found` ("Project not found" + dashboard link), generic error with Retry.
8. Page title: `{name} · pgkeeper`.

## 5. Tests
- Unit: `redactUrl` (`postgres://u:p@h/db`, `s3:https://key:secret@host`, no userinfo, malformed), `cronText` (valid/invalid cron fall back to raw), log truncation, download filename.
- Component: details panel renders all fields and never a secret; activity list selection highlight + `aria-current`; output header reflects the selected activity; Backup now disabled while running; 409 path selects the running activity; deep-link selection on load; unknown `name` → not-found state.
- Network assertion: opening a page with no selection fires no `/log` request (stub `fetch` and assert call counts).

## 6. Acceptance criteria
- [ ] Opening a project shows details, ≤10 activities and an output panel; the selected activity is clearly marked in both places.
- [ ] Selecting finished activities loads their log exactly once each; switching quickly never shows a stale log (request-id guard).
- [ ] Backup now starts a backup, selects the new activity, updates the sidebar bullet and the project header; 409/503 produce clear messages.
- [ ] Deep links (`?activity=`) from the dashboard open the right selection.
- [ ] Copy and Download work; secrets never rendered; ANSI codes never leak into the console.
- [ ] Responsive layout verified at 375 / 768 / 1280 px, light and dark.
- [ ] `npm run check` and `npm run test:unit -- --run` green.

## Out of scope
Live log streaming, job events, toasts for scheduled runs (phase 3).
