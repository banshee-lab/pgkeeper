# Phase 4: Restic integration

**Goal:** after the dump phases, store the dump file(s) in the restic repository under the configured tag, apply retention, and clean up. Restic ships in the same image as the pgkeeper binary (phase 6); locally it must be on `PATH`.

## 1. Client (`internal/restic`)

Implements `domain.ResticClient` by running the `restic` binary (no shell).

```go
type CLI struct {
    Bin string // default "restic"
}
```

### 1.1 Environment (never argv)

Each call builds the env from the **backup's merged restic config**:
```
RESTIC_REPOSITORY=<repository>
RESTIC_PASSWORD=<password>        // domain.Secret.Reveal()
RESTIC_CACHE_DIR=<temp_path>/pgkeeper/.restic-cache   // optional but avoids root-owned cache surprises
```
Start from a **minimal** inherited env (`PATH`, `HOME`, `TMPDIR`, plus any `AWS_*`, `AZURE_*`, `B2_*`, `GOOGLE_*`, `RESTIC_*`, `RCLONE_*` present in the pgkeeper process env) so cloud backends configured via environment keep working. Backend credentials are therefore provided through container env, not through the TOML (documented).

### 1.2 Operations

| Method | Command |
|---|---|
| `EnsureRepo` | `restic cat config` (exit 0 = exists). If it fails with "repository does not exist" / "unable to open config file" -> `restic init`. Any other error (wrong password, network) is **returned**, never auto-init. |
| `Backup` | `restic backup --tag <tag> --host <backup-name> --json? no` + file args (see below) |
| `Forget` | `restic forget --tag <tag> --host <backup-name> --keep-daily N --keep-weekly N --keep-monthly N --keep-yearly N --prune` |

`Backup` details:
- `cmd.Dir = <tmpDir>`; arguments are the **relative file names** (`app.sql`, `app_roles.sql`), so snapshots record stable paths per backup. Absolute paths are used if you prefer; the stable directory `<temp_path>/pgkeeper/<backup-name>/` already gives restic a consistent parent-snapshot match.
- One invocation with all files -> **one snapshot** containing database and (optionally) roles dumps.
- Flags: `--tag <tag>`, `--host <backup-name>`, `--one-file-system` not needed. Add `--no-scan`? Not needed for 2 files; keep default output for the log.
- stdout and stderr are both streamed to the execution log (`logw`), line by line, prefixed `[restic] `. Password is not in output; additionally pass the log writer through a redactor that replaces the password and repository credentials if they appear.

`Forget`:
- Build keep flags only for values `> 0`. If **all four are 0**, skip forget entirely and log `retention not configured, skipping forget`.
- `--host` and `--tag` scope the policy to this backup only, so backups sharing one repository do not prune each other.
- `--prune` runs inside the same command (restic >= 0.12 is fast enough for this use). Prune failure after a successful backup: mark the execution **error** (retention is part of the job), with message `restic: forget: ...`; the snapshot is already stored.

### 1.3 Locking and concurrency

Restic repos use their own locks; concurrent runs by different backups against one repository are safe for `backup`, but `prune` takes an exclusive lock and fails if other operations hold locks. Implement a **per-repository mutex** inside `CLI` (`map[repoKey]*sync.Mutex`, key = repository string) so pgkeeper serialises restic calls per repo. Add `--retry-lock 5m` (restic >= 0.16) to ride out locks held by other tools. Document the minimum restic version (0.16) and check it at startup (`restic version`; warn if lower or missing).

### 1.4 Redaction helper

`redact.New(secrets ...string) io.Writer wrapper`: replaces occurrences with `***`; also strips `user:pass@` from URLs (`rest:https://user:pass@host/` -> `rest:https://***@host/`). Reused for the API's `repository` field (`restic.RedactRepo(string) string`, used by DTO mapping in phase 5).

## 2. Runner integration

Replace the `NoopRestic` in the `phases` function:

```go
// phase: restic
jobs.SetPhase(name, domain.PhaseRestic)
if err := restic.EnsureRepo(ctx, cfg.Restic, log); err != nil { return fmt.Errorf("restic: init: %w", err) }
files := []string{cfg.Database + ".sql"}
if cfg.Roles { files = append(files, cfg.Database+"_roles.sql") }
if err := restic.Backup(ctx, cfg.Restic, cfg.ResticTag, cfg.Name, tmpDir, files, log); err != nil { return fmt.Errorf("restic: backup: %w", err) }
if err := restic.Forget(ctx, cfg.Restic, cfg.ResticTag, cfg.Name, log); err != nil { return fmt.Errorf("restic: forget: %w", err) }
```
Cleanup phase (`defer`): `os.RemoveAll(tmpDir)` always; log `cleanup: removed <tmpDir>`; a cleanup failure is logged as a warning and does **not** change the result.

Startup preflight (non-fatal, logged): for each distinct repository, run `EnsureRepo` once? Not by default (it would create repos at boot). Provide `pgkeeper -check-restic` flag to run `restic cat config` for every distinct repository and report.

## 3. Tests

- Unit: build of argv/env for each operation (table tests): tag, host, keep flags omitted when 0, all-zero skips forget, password only in env, never in argv.
- Fake restic binary: a tiny shell/Go test helper placed first on `PATH` that records args/env to a file and exits with a configured code; covers: init when missing, error on wrong password (no init), forget failing after backup, stderr streamed to log, redaction of password and URL credentials.
- Per-repo mutex: two goroutines on one repo are serialised; different repos are parallel.
- Integration (`-tags=integration`, requires `restic`): temp local repo, run real dump -> restic backup -> `restic snapshots --json` shows tag/host/paths; retention keeps expected count after N runs; restore the snapshot and compare the SQL file.

## 4. Definition of done

- A successful run produces exactly one snapshot with the configured tag/host containing the database dump (and roles dump when enabled).
- Retention keeps the configured number of daily/weekly/monthly/yearly snapshots per backup tag, without touching other tags in the same repo.
- Temp files never remain after a run (success, failure or cancel).
- No secret appears in logs, argv (`ps`), activity errors or Telegram messages.
