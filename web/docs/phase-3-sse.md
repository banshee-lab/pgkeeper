# Phase 3 — SSE handling (live updates)

Prereq: phases 1–2. Contract: `GET /api/v1/jobs/stream` and `GET /api/v1/activities/{id}/log/stream`.

## Goal
Everything reacts in real time without polling: sidebar bullets, dashboard, project page, toasts, and a live console for the running backup.

## 1. Connection store (`connection.svelte.ts`)
- [ ] Opens **one** `EventSource("/api/v1/jobs/stream")` when `session.status === "ready"`; closes on logout/expiry/unmount.
- [ ] `state`: `connecting` (initial / `readyState===0`), `connected` (`onopen`), `disconnected` (`onerror` while the browser retries). Sidebar text: **Connected / Connecting… / Disconnected – retrying**.
- [ ] `subscribe(type, handler)` registry with typed payloads (`snapshot`, `job.started`, `job.phase`, `job.finished`); malformed JSON is ignored and logged, never thrown.
- [ ] If `readyState === CLOSED` (browser gave up, e.g. 401/wrong content-type): probe `GET /api/v1/me` → 401 → `session.expire()`; otherwise reopen with exponential backoff (1 s → 30 s cap, jitter).
- [ ] Optional liveness: if the backend sends keepalive comments (see `backend_plan.md` R-3), no client work; otherwise rely on the browser reconnect.

## 2. Reducers (pure functions + store methods)
| Event | Effects |
|---|---|
| `snapshot` (on **every** connect) | `jobs.applySnapshot(items)`; refetch `/backups` and `/activities?limit=10`; if a project page is open refetch its activities; reconcile selected activity |
| `job.started` | `jobs.add`; `backup.running = true`; upsert a synthetic `Activity` (`status: running`, `finished_at: null`, `duration_ms: null`, `log_url` = `/api/v1/activities/{id}/log`) into `recent` and `perBackup`; toast (deduped by `activity_id`) |
| `job.phase` | update `jobs[activity_id].phase` |
| `job.finished` | `jobs.remove`; `backup.running = false`; `backup.last_activity = activity`; upsert `activity` into `recent` and `perBackup`; toast success/error with a **View** action to `/backups/{name}?activity={id}` |
- Unknown events/fields are ignored (forward compatible).
- Toast copy: started → `Backup "{name}" started ({Scheduled|Manual})`; finished → `Backup "{name}" completed in {duration}` / `Backup "{name}" failed: {error}`. Duration/auto-dismiss: info 4 s, success 4 s, error persists 10 s.
- Own manual run: the `POST /run` response already toasts "Backup started"; the later `job.started` for the same `activity_id` does not toast again.

## 3. Log stream store (`log-stream.svelte.ts`)
- [ ] `createLogStream(activityId)` opens `EventSource("/api/v1/activities/{id}/log/stream")`.
- [ ] `log` event → append `data.line` (strip ANSI); batch via `requestAnimationFrame`; track `lastEventId` (line number) to ignore duplicates after reconnect (the browser sends `Last-Event-ID` automatically).
- [ ] `end` event → set final `status`/`error`, **call `source.close()` immediately** (prevents auto-reconnect), state `ended`.
- [ ] `error` handling: `connecting` while the browser retries; if `readyState === CLOSED` before `end` → state `failed`, fall back to a single `GET /log` to get the complete text, then show it as final.
- [ ] Memory cap: 20,000 lines (ring buffer) with truncation notice.
- [ ] `close()` is idempotent and always called on selection change, route change, logout, unmount.

## 4. Project page integration
- [ ] Selected activity is `running` → create stream and render live in `OutputPanel` (header shows **Live** badge with pulsing dot; text "Reconnecting…" when applicable). Selected activity not running → `GET /log` once (phase 2 behaviour); **no stream, no polling**.
- [ ] Auto-follow rules (FR-P7):
  - Opening a project whose backup is running → select the running activity, stream.
  - `job.started` for the open project while the user has no manual selection or is following → select it automatically (`replaceState` the query param).
  - User manually selects another activity → stop following; show chip **"Running — view live output"** that re-enables following.
- [ ] `job.finished` for the followed activity: the stream's `end` arrives; keep the console content as-is (do not refetch), update header to final status, enable Copy/Download, stop the live badge. Activity row updated from the event payload.
- [ ] `job.phase` shown next to the running row/header (`dump → roles → restic → cleanup`).
- [ ] Durations: enable the 1 s `clock` tick **only while** `jobs.size > 0`; otherwise 30 s.
- [ ] Console UX: auto-scroll on by default; auto-pauses when the user scrolls up; "Jump to latest" button; toggle in header.
- [ ] RunButton: disabled when `backup.running` (now driven by SSE too, so a scheduled run disables it live).

## 5. Dashboard & sidebar integration
- [ ] Running panel, Running-now stat, attention panel, recent panel, last-backup stat and sidebar bullets all derive from the stores updated by reducers — no component fetches by itself.
- [ ] Running panel rows show `phase` and live elapsed time; links open the project with `?activity=`.
- [ ] Attention panel: a `job.finished` success removes the project; an error adds/updates it.

## 6. Dev simulator (mock mode)
- [ ] `FakeEventSource` (tests) and a scripted simulator for `VITE_MOCK_API=true`: emits `snapshot`, then every ~20 s a `job.started` → `job.phase` ×4 → `job.finished` (alternating success/error); log stream emits a line every 400 ms then `end`.

## 7. Tests
- Unit (reducers): snapshot replaces state; started→phase→finished sequence updates backups/jobs/activities/attention; duplicate `job.started` (same id) is idempotent; finished for unknown job still updates `last_activity`.
- Unit (log stream, with `FakeEventSource`): lines appended in order; duplicate line ids ignored; `end` closes the source; error then CLOSED falls back to one `GET /log`; 20k cap; ANSI stripped.
- Component: connection indicator in the three states; toast on `job.started` (once per activity); live console appears for the open project's running activity and shows streamed lines; switching selection closes the stream (assert `close()` called); running-chip behaviour when the user picks another activity.
- Network assertions: with no running job and no selection, **zero** requests to `/log` and zero log-stream connections; with a finished selected activity exactly one `GET /log`; at most 2 `EventSource` instances alive.
- Reconnect: simulate drop → `Disconnected`, reconnect → `snapshot` → refetches fired once.

## 8. Acceptance criteria
- [ ] A scheduled or manual run elsewhere instantly flips the sidebar bullet, dashboard counters and panels, and shows a toast.
- [ ] Open project + running backup → running activity auto-selected, console streams live lines, closes cleanly on `end`.
- [ ] Idle system produces no log requests and no polling timers other than the local clock.
- [ ] Killing the backend shows **Disconnected**; restoring it recovers state without a page reload.
- [ ] Session expiry (401) during SSE ends in the login screen.
- [ ] `npm run check`, `npm run test:unit -- --run`, `npm run build` green; Playwright smoke test for login screen → dashboard (mock mode) → project → run.
