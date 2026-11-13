package backup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/metadata"
	"github.com/banshee-lab/pgkeeper/internal/restic"
)

const testSnapID = "00000003-0000001B-1"

func metaJSON(rows string) string {
	return `{"pg_version":"16.2","database":"app","objects":{"tables":1,"indexes":1,"views":0,"matviews":0,"sequences":1,"functions":0,"constraints":1,"triggers":0},"table_rows":{"public.t":` +
		rows + `},"sequences":{"public.t_id_seq":` + rows + `}}`
}

type fakeMeta struct {
	mu        sync.Mutex
	events    *[]string
	out       string
	openErr   error
	queryErr  error
	scripts   []string
	targets   []domain.SQLTarget
	sessClose int
}

func (f *fakeMeta) rec(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	*f.events = append(*f.events, s)
}

type fakeSession struct{ f *fakeMeta }

func (s fakeSession) ID() string { return testSnapID }
func (s fakeSession) Close() error {
	s.f.rec("close")
	s.f.mu.Lock()
	s.f.sessClose++
	s.f.mu.Unlock()
	return nil
}

func (f *fakeMeta) OpenSnapshot(context.Context, domain.SQLTarget, io.Writer) (domain.SnapshotSession, error) {
	f.rec("open")
	if f.openErr != nil {
		return nil, f.openErr
	}
	return fakeSession{f}, nil
}

func (f *fakeMeta) PsqlOutput(_ context.Context, t domain.SQLTarget, in io.Reader, out, _ io.Writer) error {
	b, _ := io.ReadAll(in)
	f.mu.Lock()
	f.scripts = append(f.scripts, string(b))
	f.targets = append(f.targets, t)
	f.mu.Unlock()
	if strings.Contains(string(b), "SET TRANSACTION SNAPSHOT") {
		f.rec("query:snapshot")
	} else {
		f.rec("query")
	}
	if f.queryErr != nil {
		return f.queryErr
	}
	_, err := io.WriteString(out, "BEGIN\n"+f.out+"\nCOMMIT\n")
	return err
}

type recRestic struct {
	restic.Noop
	files []string
	meta  []byte
}

func (r *recRestic) Backup(_ context.Context, _ domain.ResticConfig, _, _, dir string, files []string, _ io.Writer) (*domain.SnapshotInfo, error) {
	r.files = append(r.files, files...)
	r.meta, _ = os.ReadFile(filepath.Join(dir, "app.meta.json"))
	return nil, nil
}

func backupWithMeta(t *testing.T, fm *fakeMeta, dumpErr error) (*Runner, *memActs, *recRestic, domain.Activity) {
	t.Helper()
	events := &[]string{}
	fm.events = events
	r, acts, _ := setup(t, func(_ context.Context, s domain.DumpSpec, out, _ io.Writer) error {
		if s.Kind == domain.DumpDatabase {
			*events = append(*events, "dump:"+s.SnapshotID)
			if dumpErr != nil {
				return dumpErr
			}
		} else if s.SnapshotID != "" {
			t.Errorf("roles dump must not use the snapshot")
		}
		_, err := io.WriteString(out, "SELECT 1;\n")
		return err
	}, true)
	rr := &recRestic{}
	r.Restic, r.Meta = rr, fm
	r.Global.Metadata = true
	act, err := r.Run(context.Background(), "a", domain.TriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	r.wg.Wait()
	got, _ := acts.Get(context.Background(), act.ID)
	return r, acts, rr, *got
}

func TestBackupCapturesMetadataFromExportedSnapshot(t *testing.T) {
	fm := &fakeMeta{out: metaJSON("5")}
	_, _, rr, got := backupWithMeta(t, fm, nil)
	if got.Status != domain.StatusSuccess {
		t.Fatalf("unexpected: %+v", got)
	}
	want := "open,dump:" + testSnapID + ",query:snapshot,close"
	if strings.Join(*fm.events, ",") != want {
		t.Fatalf("events = %v, want %s", *fm.events, want)
	}
	if fm.targets[0].Database != "app" || fm.targets[0].Container != "c" {
		t.Fatalf("target: %+v", fm.targets[0])
	}
	if strings.Join(rr.files, ",") != "app.sql,app.meta.json,app_roles.sql" {
		t.Fatalf("files = %v", rr.files)
	}
	m, err := metadata.Unmarshal(rr.meta)
	if err != nil || m.TableRows["public.t"] != 5 {
		t.Fatalf("meta file: %v %s", err, rr.meta)
	}
}

func TestBackupClosesSnapshotOnDumpError(t *testing.T) {
	fm := &fakeMeta{out: metaJSON("5")}
	_, _, _, got := backupWithMeta(t, fm, errors.New("boom"))
	if got.Status != domain.StatusError || fm.sessClose != 1 {
		t.Fatalf("status=%s closes=%d", got.Status, fm.sessClose)
	}
	if strings.Join(*fm.events, ",") != "open,dump:"+testSnapID+",close" {
		t.Fatalf("events = %v", *fm.events)
	}
}

func TestBackupClosesSnapshotOnCaptureError(t *testing.T) {
	fm := &fakeMeta{queryErr: errors.New("psql down")}
	_, _, rr, got := backupWithMeta(t, fm, nil)
	if got.Status != domain.StatusError || !strings.Contains(got.Error, "metadata") || fm.sessClose != 1 || len(rr.files) != 0 {
		t.Fatalf("status=%s err=%q closes=%d files=%v", got.Status, got.Error, fm.sessClose, rr.files)
	}
}

func TestBackupSnapshotOpenError(t *testing.T) {
	fm := &fakeMeta{openErr: errors.New("no snapshot")}
	_, _, _, got := backupWithMeta(t, fm, nil)
	if got.Status != domain.StatusError || !strings.Contains(got.Error, "no snapshot") {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestBackupMetadataDisabled(t *testing.T) {
	fm := &fakeMeta{events: &[]string{}}
	r, acts, _ := setup(t, okDump, false)
	rr := &recRestic{}
	r.Restic, r.Meta = rr, fm // r.Global.Metadata stays false
	act, _ := r.Run(context.Background(), "a", domain.TriggerManual)
	r.wg.Wait()
	if got, _ := acts.Get(context.Background(), act.ID); got.Status != domain.StatusSuccess {
		t.Fatalf("unexpected: %+v", got)
	}
	if len(*fm.events) != 0 || strings.Join(rr.files, ",") != "app.sql" {
		t.Fatalf("events=%v files=%v", *fm.events, rr.files)
	}
}

// metaInspector serves a snapshot that also contains app.meta.json.
type metaInspector struct {
	fakeInspector
	meta string
}

func (m metaInspector) RepositoryTree(ctx context.Context, c domain.ResticConfig, id, p string) ([]domain.RepositoryNode, error) {
	nodes, err := m.fakeInspector.RepositoryTree(ctx, c, id, p)
	if p == "/data/a" {
		nodes = append(nodes, domain.RepositoryNode{Path: "/data/a/app.meta.json", Type: "file", Size: 1})
	}
	return nodes, err
}

func (m metaInspector) DownloadSnapshotFile(ctx context.Context, c domain.ResticConfig, id, p string, dst io.Writer) error {
	if strings.HasSuffix(p, ".meta.json") {
		_, err := io.WriteString(dst, m.meta)
		return err
	}
	return m.fakeInspector.DownloadSnapshotFile(ctx, c, id, p, dst)
}

func expectedMeta(t *testing.T, rows string) string {
	t.Helper()
	m, err := metadata.Parse([]byte(metaJSON(rows)), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := metadata.Marshal(m)
	return string(b)
}

func metaRestoreSetup(t *testing.T, expected string, actual *fakeMeta) (*Runner, *memActs, *fakeNotifier) {
	t.Helper()
	r, acts, n := restoreSetup(t, &fakeRestoreExec{})
	paths := []string{"/tmp/pgkeeper/a/app.sql", "/tmp/pgkeeper/a/app.meta.json", "/tmp/pgkeeper/a/app_roles.sql"}
	snaps := []domain.RepositorySnapshot{{ID: "bbbbbbbb22222222", ShortID: "bbbbbbbb", Time: time.Now().UTC(), Hostname: "a", Tags: []string{"a"}, Paths: paths}}
	r.Inspector = metaInspector{fakeInspector: fakeInspector{snaps: snaps}, meta: expected}
	actual.events = &[]string{}
	r.Meta = actual
	r.Global.Metadata = true
	return r, acts, n
}

func runTestRestore(t *testing.T, r *Runner, acts *memActs) (domain.Activity, string) {
	t.Helper()
	act, err := r.TestRestore(context.Background(), "a", domain.TriggerManual, "")
	if err != nil {
		t.Fatal(err)
	}
	r.wg.Wait()
	got, _ := acts.Get(context.Background(), act.ID)
	log, _ := os.ReadFile(act.LogPath)
	return *got, string(log)
}

func TestRestoreValidationMatchIsSuccess(t *testing.T) {
	fm := &fakeMeta{out: metaJSON("5")}
	r, acts, n := metaRestoreSetup(t, expectedMeta(t, "5"), fm)
	got, log := runTestRestore(t, r, acts)
	if got.Status != domain.StatusSuccess || len(n.calls) != 0 {
		t.Fatalf("unexpected: %+v notifications=%d\n%s", got, len(n.calls), log)
	}
	if len(fm.scripts) != 1 || strings.Contains(fm.scripts[0], "SET TRANSACTION SNAPSHOT") {
		t.Fatalf("restore capture must read the restored database without a snapshot: %v", fm.scripts)
	}
	if !strings.HasPrefix(fm.targets[0].Container, "pgkeeper-restore-") || fm.targets[0].Database != "app" {
		t.Fatalf("validation must run against the temp container: %+v", fm.targets[0])
	}
	if !strings.Contains(log, "phase validate: done metadata matches") {
		t.Fatalf("log: %s", log)
	}
}

func TestRestoreValidationMismatchIsWarning(t *testing.T) {
	fm := &fakeMeta{out: metaJSON("4")}
	r, acts, n := metaRestoreSetup(t, expectedMeta(t, "5"), fm)
	got, log := runTestRestore(t, r, acts)
	if got.Status != domain.StatusWarning || !strings.Contains(got.Error, "section=table_rows object=public.t expected=5 actual=4") {
		t.Fatalf("unexpected: %+v", got)
	}
	if len(n.calls) != 1 || n.calls[0].Activity.Status != domain.StatusWarning {
		t.Fatalf("a warning must raise the failure alert: %+v", n.calls)
	}
	if !strings.Contains(log, "WARN") || !strings.Contains(log, "metadata mismatch: section=sequences object=public.t_id_seq expected=5 actual=4") ||
		!strings.Contains(log, "status=warning") {
		t.Fatalf("log: %s", log)
	}
}

func TestRestoreValidationCaptureErrorIsFailed(t *testing.T) {
	fm := &fakeMeta{queryErr: errors.New("psql down")}
	r, acts, _ := metaRestoreSetup(t, expectedMeta(t, "5"), fm)
	if got, _ := runTestRestore(t, r, acts); got.Status != domain.StatusError || !strings.Contains(got.Error, "validate") {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestRestoreUnreadableMetadataIsWarning(t *testing.T) {
	fm := &fakeMeta{out: metaJSON("5")}
	r, acts, _ := metaRestoreSetup(t, "{not json", fm)
	got, _ := runTestRestore(t, r, acts)
	if got.Status != domain.StatusWarning || !strings.Contains(got.Error, "section=metadata") {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestRestoreLegacyDumpSkipsValidation(t *testing.T) {
	fm := &fakeMeta{events: &[]string{}, out: metaJSON("5")}
	r, acts, n := restoreSetup(t, &fakeRestoreExec{}) // snapshots without a metadata file
	r.Meta, r.Global.Metadata = fm, true
	got, log := runTestRestore(t, r, acts)
	if got.Status != domain.StatusSuccess || len(n.calls) != 0 || len(fm.scripts) != 0 {
		t.Fatalf("unexpected: %+v", got)
	}
	if !strings.Contains(log, "INFO") || !strings.Contains(log, "no metadata file (legacy backup)") {
		t.Fatalf("log: %s", log)
	}
}

func TestLiveRestoreIsValidatedInTarget(t *testing.T) {
	fm := &fakeMeta{out: metaJSON("4")}
	r, acts, _ := metaRestoreSetup(t, expectedMeta(t, "5"), fm)
	act, err := r.RestoreToContainer(context.Background(), "a", domain.TriggerManual,
		domain.RestoreRequest{SnapshotID: "bbbbbbbb22222222", Container: "target", Database: "copy"})
	if err != nil {
		t.Fatal(err)
	}
	r.wg.Wait()
	got, _ := acts.Get(context.Background(), act.ID)
	if got.Status != domain.StatusWarning || fm.targets[0].Container != "target" || fm.targets[0].Database != "copy" {
		t.Fatalf("status=%s target=%+v", got.Status, fm.targets)
	}
}
