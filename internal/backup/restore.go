package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

var _ domain.RestoreRunner = (*Runner)(nil)

const (
	// The ephemeral container runs the image's default superuser on the
	// default port; it has no network, so psql connects over loopback inside it.
	ephemeralHost = "localhost"
	ephemeralPort = 5432
	ephemeralUser = "postgres"
	readyTimeout  = 90 * time.Second
)

var (
	dbNameRe      = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.$-]{0,62}$`)
	containerRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	snapshotIDRe  = regexp.MustCompile(`^[0-9a-fA-F]{8,64}$`)
	errNoSnapshot = errors.New("no snapshot of this project found in the restic repository (has a backup completed?)")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrRestoreInvalid, fmt.Sprintf(format, args...))
}

// restorable is a project snapshot together with the paths of its dump files.
type restorable struct {
	domain.RestoreSnapshot
	dbPath    string
	rolesPath string
	metaPath  string // empty for legacy backups made before metadata capture
}

func (r *Runner) restoreConfig(name string) (domain.BackupConfig, error) {
	cfg, ok := r.Catalog.Get(name)
	if !ok {
		return domain.BackupConfig{}, domain.ErrBackupUnknown
	}
	if r.RestoreExec == nil || r.Inspector == nil {
		return domain.BackupConfig{}, domain.ErrRestoreUnavailable
	}
	return cfg, nil
}

// TestRestore restores a snapshot into an ephemeral container built from the
// project's restore image (else the global [restore].restore-image). An empty
// snapshotID selects the latest snapshot. Scheduled test restores always use
// the latest snapshot, whatever snapshotID says.
func (r *Runner) TestRestore(ctx context.Context, name string, by domain.Trigger, snapshotID string) (*domain.Activity, error) {
	cfg, err := r.restoreConfig(name)
	if err != nil {
		return nil, err
	}
	if cfg.RestoreImage == "" {
		return nil, invalid("no restore image configured for %q (set restore-image in [restore] or in the project)", name)
	}
	if by == domain.TriggerSchedule {
		snapshotID = ""
	}
	if snapshotID != "" && !snapshotIDRe.MatchString(snapshotID) {
		return nil, invalid("snapshot_id must be a restic snapshot ID")
	}
	req := domain.RestoreRequest{SnapshotID: snapshotID}
	return r.start(ctx, cfg, by, domain.KindTestRestore, func(ctx context.Context, act domain.Activity, lg execLog) error {
		return r.runRestore(ctx, cfg, act, lg, req, true)
	})
}

// RestoreToContainer restores one snapshot into a live container, or into a
// remote server when req.DatabaseURL is set, replacing the target database.
func (r *Runner) RestoreToContainer(ctx context.Context, name string, by domain.Trigger, req domain.RestoreRequest) (*domain.Activity, error) {
	cfg, err := r.restoreConfig(name)
	if err != nil {
		return nil, err
	}
	var secrets []string
	switch {
	case req.DatabaseURL != "" && req.Container != "":
		return nil, invalid("container and database_url are mutually exclusive")
	case req.DatabaseURL != "":
		conn, perr := domain.ParsePGURL(req.DatabaseURL.Reveal())
		if perr != nil {
			return nil, invalid("invalid database_url: %v", perr)
		}
		req.Conn = &conn
		if req.Database == "" {
			req.Database = conn.Database
		}
	case req.Container == "" && cfg.Conn != nil:
		// A project backed up from a database-url is restored into it by default.
		req.Conn = cfg.Conn
	}
	if req.Conn != nil {
		if cfg.ToolsImage == "" {
			return nil, invalid("tools-image is not configured: it is required to restore into a database URL (set tools-image in [global])")
		}
		if req.Conn.Password != "" {
			secrets = append(secrets, req.Conn.Password.Reveal())
		}
	} else if req.Container == "" {
		req.Container = cfg.Container
	}
	if req.Database == "" {
		req.Database = cfg.Database
	}
	switch {
	case !snapshotIDRe.MatchString(req.SnapshotID):
		return nil, invalid("snapshot_id must be a restic snapshot ID")
	case req.Conn == nil && !containerRe.MatchString(req.Container):
		return nil, invalid("invalid target container name %q", req.Container)
	case !dbNameRe.MatchString(req.Database):
		return nil, invalid("invalid target database name %q (letters, digits, '_', '.', '$', '-')", req.Database)
	}
	return r.start(ctx, cfg, by, domain.KindRestore, func(ctx context.Context, act domain.Activity, lg execLog) error {
		return r.runRestore(ctx, cfg, act, lg, req, false)
	}, secrets...)
}

// RestoreSnapshots lists the project's snapshots that contain a database
// dump, newest first.
func (r *Runner) RestoreSnapshots(ctx context.Context, name string) ([]domain.RestoreSnapshot, error) {
	cfg, err := r.restoreConfig(name)
	if err != nil {
		return nil, err
	}
	snaps, err := r.projectSnapshots(ctx, cfg)
	if err != nil {
		return nil, err
	}
	out := make([]domain.RestoreSnapshot, len(snaps))
	for i, s := range snaps {
		out[i] = s.RestoreSnapshot
	}
	return out, nil
}

// projectSnapshots returns the snapshots written by the project's backups
// (same tag, host = project name) that contain the database dump.
func (r *Runner) projectSnapshots(ctx context.Context, cfg domain.BackupConfig) ([]restorable, error) {
	all, err := r.Inspector.RepositorySnapshots(ctx, cfg.Restic)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	dbFile := safeName(cfg.Database) + ".sql"
	rolesFile := safeName(cfg.Database) + "_roles.sql"
	metaFile := safeName(cfg.Database) + ".meta.json"
	var out []restorable
	for _, s := range all {
		if s.Hostname != cfg.Name || !slices.Contains(s.Tags, cfg.ResticTag) {
			continue
		}
		dbPath := findPath(s.Paths, dbFile)
		if dbPath == "" {
			continue
		}
		rolesPath := findPath(s.Paths, rolesFile)
		out = append(out, restorable{
			RestoreSnapshot: domain.RestoreSnapshot{ID: s.ID, ShortID: s.ShortID, Time: s.Time, HasRoles: rolesPath != ""},
			dbPath:          dbPath,
			rolesPath:       rolesPath,
			metaPath:        findPath(s.Paths, metaFile),
		})
	}
	slices.SortStableFunc(out, func(a, b restorable) int { return b.Time.Compare(a.Time) })
	return out, nil
}

func findPath(paths []string, file string) string {
	for _, p := range paths {
		if path.Base(p) == file {
			return p
		}
	}
	return ""
}

func pickSnapshot(snaps []restorable, id string, latest bool) (restorable, error) {
	if len(snaps) == 0 {
		return restorable{}, errNoSnapshot
	}
	if latest {
		return snaps[0], nil
	}
	for _, s := range snaps {
		if s.ID == id || s.ShortID == id {
			return s, nil
		}
	}
	return restorable{}, fmt.Errorf("snapshot %s does not belong to this project", id)
}

// runRestore is the body of a test (ephemeral) or live restore.
func (r *Runner) runRestore(ctx context.Context, cfg domain.BackupConfig, act domain.Activity, lg execLog,
	req domain.RestoreRequest, ephemeral bool) (err error) {

	short := act.ID.String()[:8]
	tmpDir := filepath.Join(r.Global.TempPath, "pgkeeper", "restore", safeName(cfg.Name)+"-"+short)

	// Deferred LIFO: container removal and temp cleanup run first, then panic recovery.
	defer func() {
		if p := recover(); p != nil {
			r.Logger.Error("panic in restore", "backup", cfg.Name, "panic", p, "stack", string(debug.Stack()))
			lg.Logf("ERROR", "internal panic: %v", p)
			err = errors.New("internal panic")
		}
	}()
	defer func() {
		lg.Logf("INFO", "phase %s: removing %s", domain.PhaseCleanup, tmpDir)
		if e := os.RemoveAll(tmpDir); e != nil {
			r.Logger.Warn("restore temporary files cleanup failed", "backup", cfg.Name, "activity", act.ID, "path", tmpDir, "err", e)
			lg.Logf("WARN", "cleanup failed: %v", e)
		}
	}()

	step := func(phase domain.Phase, fn func() error) error {
		r.setPhase(cfg, act, phase)
		lg.Logf("INFO", "phase %s: start", phase)
		r.Logger.Info("restore phase started", "activity", act.ID, "backup", cfg.Name, "phase", phase)
		start := time.Now()
		if e := fn(); e != nil {
			r.Logger.Error("restore phase failed", "activity", act.ID, "backup", cfg.Name,
				"phase", phase, "duration", time.Since(start), "err", e)
			return fmt.Errorf("%s: %w", phase, e)
		}
		lg.Logf("INFO", "phase %s: done elapsed=%s", phase, time.Since(start).Round(time.Millisecond))
		return nil
	}

	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return fmt.Errorf("prepare temp dir: %w", err)
	}
	dbLocal := filepath.Join(tmpDir, "database.sql")
	rolesLocal := filepath.Join(tmpDir, "roles.sql")
	rolesPrepared := filepath.Join(tmpDir, "roles.prepared.sql")
	metaLocal := filepath.Join(tmpDir, "backup.meta.json")
	var metaFetchErr error // reported as a validation warning, never fails the restore

	// ---- snapshot ----
	var snap restorable
	withRoles := req.IncludeRoles
	if err := step(domain.PhaseSnapshot, func() error {
		snaps, e := r.projectSnapshots(ctx, cfg)
		if e != nil {
			return e
		}
		if snap, e = pickSnapshot(snaps, req.SnapshotID, ephemeral && req.SnapshotID == ""); e != nil {
			return e
		}
		if ephemeral {
			withRoles = snap.rolesPath != "" // a test restore also proves the roles dump loads
		} else if withRoles && snap.rolesPath == "" {
			return fmt.Errorf("snapshot %s has no roles dump (the project needs roles = true)", snap.ShortID)
		}
		lg.Logf("INFO", "restoring snapshot %s taken %s (roles=%t)", snap.ShortID, snap.Time.UTC().Format(time.RFC3339), withRoles)
		switch {
		case !r.Global.Metadata || r.Meta == nil:
			lg.Logf("INFO", "metadata validation disabled")
		case snap.metaPath == "":
			lg.Logf("INFO", "metadata validation skipped: snapshot %s has no metadata file (legacy backup)", snap.ShortID)
		}
		return nil
	}); err != nil {
		return err
	}

	validate := r.Global.Metadata && r.Meta != nil && snap.metaPath != ""

	// ---- restic: fetch the dumps ----
	if err := step(domain.PhaseRestic, func() error {
		release, e := r.gate.acquire(ctx, cfg.Restic.Repository, cfg.Name, func(holder string, ahead int) {
			r.setPhase(cfg, act, domain.PhaseQueued)
			lg.Logf("INFO", "phase %s: repository is in use by backup %q (%d ahead in queue), waiting", domain.PhaseQueued, holder, ahead)
		})
		if e != nil {
			return fmt.Errorf("waiting for repository: %w", e)
		}
		defer release()
		r.setPhase(cfg, act, domain.PhaseRestic)
		// The snapshot's "paths" metadata is only a hint: restic dump needs the
		// path as stored in the tree, so look the files up there.
		dbRemote, e := r.locateFile(ctx, cfg, snap.ID, path.Base(snap.dbPath))
		if e != nil {
			return e
		}
		if e := r.fetch(ctx, cfg, snap.ID, dbRemote, dbLocal, lg); e != nil {
			return e
		}
		if validate {
			if metaRemote, e := r.locateFile(ctx, cfg, snap.ID, path.Base(snap.metaPath)); e != nil {
				metaFetchErr = e
			} else if e := r.fetch(ctx, cfg, snap.ID, metaRemote, metaLocal, lg); e != nil {
				metaFetchErr = e
			}
		}
		if withRoles {
			rolesRemote, e := r.locateFile(ctx, cfg, snap.ID, path.Base(snap.rolesPath))
			if e != nil {
				return e
			}
			return r.fetch(ctx, cfg, snap.ID, rolesRemote, rolesLocal, lg)
		}
		return nil
	}); err != nil {
		return err
	}

	// ---- target ----
	target := domain.SQLTarget{Container: req.Container, Host: cfg.Host, Port: cfg.Port, Username: cfg.Username, Database: "postgres"}
	where := "container " + strconv.Quote(req.Container)
	if req.Conn != nil {
		target = domain.SQLTarget{Conn: req.Conn, Image: cfg.ToolsImage, Database: "postgres"}
		where = "server " + req.Conn.Host + ":" + strconv.Itoa(req.Conn.Port) + " (as " + req.Conn.User + ")"
	}
	dbName := req.Database
	if ephemeral {
		dbName = cfg.Database
		if !dbNameRe.MatchString(dbName) {
			dbName = "restore_test"
		}
		cname := "pgkeeper-restore-" + safeName(cfg.Name) + "-" + short
		target = domain.SQLTarget{Container: cname, Host: ephemeralHost, Port: ephemeralPort, Username: ephemeralUser, Database: "postgres"}
		// Registered before the start so a half-started container is removed too.
		defer r.removeContainer(ctx, cfg, act, lg, cname)
		if err := step(domain.PhaseProvision, func() error {
			lg.Logf("INFO", "starting ephemeral container %s from image %s", cname, cfg.RestoreImage)
			if e := r.RestoreExec.StartEphemeral(ctx, cname, cfg.RestoreImage, lg); e != nil {
				return e
			}
			return r.RestoreExec.WaitReady(ctx, target, readyTimeout, lg)
		}); err != nil {
			return err
		}
	} else {
		lg.Logf("INFO", "target: %s database=%s", where, dbName)
		if req.Conn != nil {
			lg.Logf("INFO", "connecting through a tools container from image %s", cfg.ToolsImage)
		}
	}

	// ---- roles ----
	if withRoles {
		if err := step(domain.PhaseRoles, func() error {
			n, e := r.prepareRolesFile(rolesLocal, rolesPrepared)
			if e != nil {
				return e
			}
			lg.Logf("INFO", "%d role definitions found; roles that already exist on the target are overwritten (attributes and passwords updated)", n)
			return r.psqlFile(ctx, target, rolesPrepared, lg)
		}); err != nil {
			return err
		}
	}

	// ---- database ----
	if err := step(domain.PhaseRestore, func() error {
		var sb strings.Builder
		if !ephemeral {
			lg.Logf("WARN", "dropping and recreating database %q on %s: its current data is replaced", dbName, where)
			fmt.Fprintf(&sb, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid();\n", dbName)
			fmt.Fprintf(&sb, "DROP DATABASE IF EXISTS \"%s\";\n", dbName)
		}
		fmt.Fprintf(&sb, "CREATE DATABASE \"%s\";\n", dbName)
		if e := r.RestoreExec.Psql(ctx, target, strings.NewReader(sb.String()), lg); e != nil {
			return fmt.Errorf("prepare database: %w", e)
		}
		target.Database = dbName
		return r.psqlFile(ctx, target, dbLocal, lg)
	}); err != nil {
		return err
	}
	if !validate {
		return nil
	}
	return r.validateRestore(ctx, cfg, act, lg, target, metaLocal, metaFetchErr)
}

// locateFile walks the snapshot tree breadth-first and returns the stored path
// of the file with the given name.
func (r *Runner) locateFile(ctx context.Context, cfg domain.BackupConfig, snapshotID, file string) (string, error) {
	queue := []string{""} // "" is the snapshot root
	for visited := 0; len(queue) > 0 && visited < 200; visited++ {
		dir := queue[0]
		queue = queue[1:]
		nodes, err := r.Inspector.RepositoryTree(ctx, cfg.Restic, snapshotID, dir)
		if err != nil {
			return "", fmt.Errorf("list snapshot contents: %w", err)
		}
		for _, n := range nodes {
			switch {
			case n.Type == "file" && path.Base(n.Path) == file:
				return n.Path, nil
			case n.Type == "dir":
				queue = append(queue, n.Path)
			}
		}
	}
	return "", fmt.Errorf("%s not found in snapshot %s", file, snapshotID[:8])
}

// fetch downloads one file of a snapshot into local.
func (r *Runner) fetch(ctx context.Context, cfg domain.BackupConfig, snapshotID, remote, local string, lg execLog) error {
	f, err := os.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	dlErr := r.Inspector.DownloadSnapshotFile(ctx, cfg.Restic, snapshotID, remote, f)
	closeErr := f.Close()
	if dlErr != nil {
		return dlErr
	}
	if closeErr != nil {
		return closeErr
	}
	fi, err := os.Stat(local)
	if err != nil {
		return err
	}
	if fi.Size() == 0 {
		return fmt.Errorf("%s is empty in the snapshot", path.Base(remote))
	}
	lg.Logf("INFO", "downloaded %s (%d bytes)", path.Base(remote), fi.Size())
	return nil
}

func (r *Runner) prepareRolesFile(src, dst string) (int, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	n, perr := prepareRoles(in, out)
	if cerr := out.Close(); perr == nil {
		perr = cerr
	}
	return n, perr
}

func (r *Runner) psqlFile(ctx context.Context, t domain.SQLTarget, file string, lg execLog) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	return r.RestoreExec.Psql(ctx, t, f, lg)
}

// removeContainer force-removes the ephemeral container even when the
// restore was cancelled; a failure is logged but does not change the result.
func (r *Runner) removeContainer(ctx context.Context, cfg domain.BackupConfig, act domain.Activity, lg execLog, name string) {
	lg.Logf("INFO", "phase %s: removing container %s", domain.PhaseCleanup, name)
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := r.RestoreExec.RemoveContainer(rctx, name, lg); err != nil {
		r.Logger.Warn("remove restore container failed", "activity", act.ID, "backup", cfg.Name, "container", name, "err", err)
		lg.Logf("WARN", "could not remove container %s: %v", name, err)
	}
}
