package backup

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/metadata"
)

// captureMetadata runs the catalog query on target, inside the given exported
// snapshot when snapshotID is set.
func (r *Runner) captureMetadata(ctx context.Context, t domain.SQLTarget, snapshotID string, lg execLog) (*metadata.Metadata, error) {
	script, err := metadata.Script(snapshotID)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := r.Meta.PsqlOutput(ctx, t, strings.NewReader(script), &out, lg); err != nil {
		return nil, err
	}
	return metadata.Parse(out.Bytes(), r.Clock())
}

// dumpDatabase writes the database dump and, when metadata is enabled, the
// <dump>.meta.json next to it. Session A (the exported snapshot) stays open
// until both are done and is always closed, also on error. It reports whether
// a metadata file was written.
func (r *Runner) dumpDatabase(ctx context.Context, cfg domain.BackupConfig, act domain.Activity, lg execLog, dumpPath, metaPath string) (bool, error) {
	if !r.Global.Metadata || r.Meta == nil || (cfg.Conn != nil && cfg.ToolsImage == "") {
		return false, r.dump(ctx, cfg, act, lg, domain.PhaseDump, domain.DumpDatabase, dumpPath, "")
	}
	target := domain.SQLTarget{Container: cfg.Container, Host: cfg.Host, Port: cfg.Port, Username: cfg.Username,
		Database: cfg.Database, Conn: cfg.Conn, Image: cfg.ToolsImage}
	r.setPhase(cfg, act, domain.PhaseDump)
	sess, err := r.Meta.OpenSnapshot(ctx, target, lg)
	if err != nil {
		return false, fmt.Errorf("%s: metadata snapshot: %w", domain.PhaseDump, err)
	}
	defer func() {
		if e := sess.Close(); e != nil {
			lg.Logf("WARN", "closing snapshot session: %v", e)
		}
	}()
	lg.Logf("INFO", "metadata: exported snapshot %s", sess.ID())
	if err := r.dump(ctx, cfg, act, lg, domain.PhaseDump, domain.DumpDatabase, dumpPath, sess.ID()); err != nil {
		return false, err
	}
	start := time.Now()
	m, err := r.captureMetadata(ctx, target, sess.ID(), lg)
	if err != nil {
		return false, fmt.Errorf("%s: metadata: %w", domain.PhaseDump, err)
	}
	b, err := metadata.Marshal(m)
	if err != nil {
		return false, fmt.Errorf("%s: metadata: %w", domain.PhaseDump, err)
	}
	if err := os.WriteFile(metaPath, b, 0o600); err != nil {
		return false, fmt.Errorf("%s: metadata: %w", domain.PhaseDump, err)
	}
	lg.Logf("INFO", "metadata: captured %d tables, %d sequences in %s", len(m.TableRows), len(m.Sequences), time.Since(start).Round(time.Millisecond))
	return true, nil
}

// validateRestore compares the restored database with the metadata captured
// at backup time. Differences are returned as *domain.ValidationWarning.
func (r *Runner) validateRestore(ctx context.Context, cfg domain.BackupConfig, act domain.Activity, lg execLog,
	target domain.SQLTarget, metaLocal string, fetchErr error) error {

	r.setPhase(cfg, act, domain.PhaseValidate)
	lg.Logf("INFO", "phase %s: start", domain.PhaseValidate)
	start := time.Now()
	unreadable := func(e error) error {
		lg.Logf("WARN", "backup metadata unusable: %v", e)
		return &domain.ValidationWarning{Mismatches: []domain.Mismatch{{
			Section: "metadata", Object: "backup metadata file", Expected: "readable metadata file", Actual: e.Error()}}}
	}
	if fetchErr != nil {
		return unreadable(fetchErr)
	}
	raw, err := os.ReadFile(metaLocal)
	if err != nil {
		return unreadable(err)
	}
	expected, err := metadata.Unmarshal(raw)
	if err != nil {
		return unreadable(err)
	}
	actual, err := r.captureMetadata(ctx, target, "", lg)
	if err != nil {
		return fmt.Errorf("%s: %w", domain.PhaseValidate, err)
	}
	mm := metadata.Diff(expected, actual)
	if len(mm) == 0 {
		lg.Logf("INFO", "phase %s: done metadata matches (%d tables, %d sequences) elapsed=%s",
			domain.PhaseValidate, len(expected.TableRows), len(expected.Sequences), time.Since(start).Round(time.Millisecond))
		return nil
	}
	for _, m := range mm {
		lg.Logf("WARN", "metadata mismatch: %s", m)
	}
	return &domain.ValidationWarning{Mismatches: mm}
}
