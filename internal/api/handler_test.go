package api

import (
	"cmp"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/events"
	"github.com/banshee-lab/pgkeeper/internal/logstore"
)

// ---- fakes ----

type fakeActs struct {
	mu    sync.Mutex
	items map[uuid.UUID]domain.Activity
}

func newFakeActs() *fakeActs { return &fakeActs{items: map[uuid.UUID]domain.Activity{}} }

func (f *fakeActs) put(a domain.Activity) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[a.ID] = a
}

func (f *fakeActs) finish(id uuid.UUID, st domain.Status, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.items[id]
	now := time.Now().UTC()
	a.Status, a.FinishedAt, a.Error = st, &now, msg
	f.items[id] = a
}

func (f *fakeActs) sorted(keep func(domain.Activity) bool, limit int) []domain.Activity {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Activity
	for _, a := range f.items {
		if keep(a) {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b domain.Activity) int { return b.StartedAt.Compare(a.StartedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (f *fakeActs) Create(_ context.Context, a *domain.Activity) error { f.put(*a); return nil }
func (f *fakeActs) Finish(_ context.Context, id uuid.UUID, st domain.Status, _ time.Time, msg string) error {
	f.finish(id, st, msg)
	return nil
}
func (f *fakeActs) Get(_ context.Context, id uuid.UUID) (*domain.Activity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.items[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &a, nil
}
func (f *fakeActs) ListByBackup(_ context.Context, name string, limit int) ([]domain.Activity, error) {
	return f.sorted(func(a domain.Activity) bool { return a.BackupName == name }, limit), nil
}
func (f *fakeActs) ListRecent(_ context.Context, limit int) ([]domain.Activity, error) {
	return f.sorted(func(domain.Activity) bool { return true }, limit), nil
}
func (f *fakeActs) LastByBackup(_ context.Context, names []string) (map[string]domain.Activity, error) {
	out := map[string]domain.Activity{}
	for _, n := range names {
		if l := f.sorted(func(a domain.Activity) bool { return a.BackupName == n }, 1); len(l) == 1 {
			out[n] = l[0]
		}
	}
	return out, nil
}
func (f *fakeActs) MarkRunningAsInterrupted(context.Context, time.Time) (int, error) { return 0, nil }

type fakeRunner struct {
	runErr error
	act    domain.Activity
	jobs   []domain.Job
}

func (f *fakeRunner) Run(_ context.Context, name string, by domain.Trigger) (*domain.Activity, error) {
	if f.runErr != nil {
		return nil, f.runErr
	}
	a := f.act
	a.BackupName, a.TriggeredBy = name, by
	return &a, nil
}
func (f *fakeRunner) Running() []domain.Job { return f.jobs }

type fakeCatalog []domain.BackupConfig

func (c fakeCatalog) List() []domain.BackupConfig { return slices.Clone(c) }
func (c fakeCatalog) Get(name string) (domain.BackupConfig, bool) {
	for _, b := range c {
		if b.Name == name {
			return b, true
		}
	}
	return domain.BackupConfig{}, false
}

type fakeSched struct{}

func (fakeSched) Start() error                { return nil }
func (fakeSched) Stop(context.Context) error  { return nil }
func (fakeSched) NextRun(name string) (time.Time, bool) {
	if name == "alpha" {
		return time.Date(2026, 11, 3, 3, 0, 0, 0, time.UTC), true
	}
	return time.Time{}, false
}

// ---- harness ----

type testAPI struct {
	router http.Handler
	acts   *fakeActs
	runner *fakeRunner
	jobHub *events.Hub[events.JobEvent]
	logHub *events.Hub[logstore.LogLine]
	logDir string
}

func newAPI(t *testing.T, mut ...func(*Deps)) *testAPI {
	t.Helper()
	a := &testAPI{
		acts:   newFakeActs(),
		jobHub: events.NewHub[events.JobEvent](),
		logHub: events.NewHub[logstore.LogLine](),
		logDir: t.TempDir(),
	}
	a.runner = &fakeRunner{act: domain.Activity{
		ID: uuid.New(), Database: "db", Status: domain.StatusRunning, StartedAt: time.Now().UTC(),
	}}
	d := Deps{
		DB: okPinger{}, Activities: a.acts, Runner: a.runner, Scheduler: fakeSched{},
		Catalog: fakeCatalog{
			{Name: "beta", Cron: "@daily", Database: "db"},
			{Name: "alpha", Cron: "@daily", Database: "db"},
		},
		JobHub: a.jobHub, LogHub: a.logHub, Heartbeat: time.Minute,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, m := range mut {
		m(&d)
	}
	a.router = NewRouter(NewHandler(d))
	return a
}

func (a *testAPI) do(method, target, ctype, body string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rd)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	rec := httptest.NewRecorder()
	a.router.ServeHTTP(rec, req)
	return rec
}

func (a *testAPI) addActivity(backupName string, started time.Time, st domain.Status) domain.Activity {
	id := uuid.New()
	act := domain.Activity{
		ID: id, BackupName: backupName, Database: "db", TriggeredBy: domain.TriggerManual,
		Status: st, StartedAt: started, LogPath: filepath.Join(a.logDir, id.String()+".log"),
	}
	if st != domain.StatusRunning {
		fin := started.Add(time.Second)
		act.FinishedAt = &fin
	}
	if st == domain.StatusError {
		act.Error = "dump: boom"
	}
	a.acts.put(act)
	return act
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	return decode[errorBody](t, rec).Error.Code
}

// ---- tests ----

func TestListBackups(t *testing.T) {
	a := newAPI(t)
	a.addActivity("alpha", time.Now().Add(-time.Hour).UTC(), domain.StatusSuccess)
	a.runner.jobs = []domain.Job{{ActivityID: uuid.New(), BackupName: "beta", Phase: domain.PhaseDump}}

	rec := a.do("GET", "/api/v1/backups", "", "")
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	items := decode[ListDTO[BackupDTO]](t, rec).Items
	if len(items) != 2 || items[0].Name != "alpha" || items[1].Name != "beta" {
		t.Fatalf("not sorted by name: %+v", items)
	}
	if items[0].LastActivity == nil || items[0].Running || items[0].NextRun == nil || *items[0].NextRun != "2026-11-03T03:00:00Z" {
		t.Errorf("alpha = %+v", items[0])
	}
	if !items[1].Running || items[1].NextRun != nil || items[1].LastActivity != nil {
		t.Errorf("beta = %+v", items[1])
	}
}

func TestBackupNotFound(t *testing.T) {
	a := newAPI(t)
	for _, p := range []string{"/api/v1/backups/zzz", "/api/v1/backups/zzz/activities"} {
		if rec := a.do("GET", p, "", ""); rec.Code != 404 || errCode(t, rec) != CodeBackupNotFound {
			t.Errorf("%s = %d %s", p, rec.Code, rec.Body.String())
		}
	}
}

func TestLimitClamping(t *testing.T) {
	a := newAPI(t)
	base := time.Now().UTC()
	for i := range 12 {
		a.addActivity("alpha", base.Add(time.Duration(i)*time.Minute), domain.StatusSuccess)
	}
	for _, p := range []string{"/api/v1/backups/alpha/activities", "/api/v1/activities"} {
		for _, tc := range []struct {
			q    string
			want int // 0 = expect 400
		}{
			{"", 10}, {"?limit=3", 3}, {"?limit=11", 10}, {"?limit=10", 10},
			{"?limit=0", 0}, {"?limit=-1", 0}, {"?limit=abc", 0},
		} {
			rec := a.do("GET", p+tc.q, "", "")
			if tc.want == 0 {
				if rec.Code != 400 || errCode(t, rec) != CodeInvalidRequest {
					t.Errorf("%s%s = %d", p, tc.q, rec.Code)
				}
				continue
			}
			items := decode[ListDTO[ActivityDTO]](t, rec).Items
			if rec.Code != 200 || len(items) != tc.want {
				t.Errorf("%s%s = %d, %d items, want %d", p, tc.q, rec.Code, len(items), tc.want)
			}
			if !slices.IsSortedFunc(items, func(x, y ActivityDTO) int { return cmp.Compare(y.StartedAt, x.StartedAt) }) {
				t.Errorf("%s%s not newest-first", p, tc.q)
			}
		}
	}
}

func TestRunBackup(t *testing.T) {
	for _, tc := range []struct {
		name         string
		err          error
		ctype, body  string
		wantStatus   int
		wantCode     string
	}{
		{"ok no body", nil, "", "", 202, ""},
		{"ok json body ignored", nil, "application/json", `{"x":1}`, 202, ""},
		{"unknown", domain.ErrBackupUnknown, "", "", 404, CodeBackupNotFound},
		{"running", domain.ErrBackupRunning, "", "", 409, CodeBackupRunning},
		{"non-json body", nil, "text/plain", "x", 400, CodeInvalidRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newAPI(t)
			a.runner.runErr = tc.err
			rec := a.do("POST", "/api/v1/backups/alpha/run", tc.ctype, tc.body)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
			}
			if tc.wantCode != "" && errCode(t, rec) != tc.wantCode {
				t.Errorf("code = %s", errCode(t, rec))
			}
			if tc.wantStatus == 202 {
				if d := decode[ActivityDTO](t, rec); d.Status != "running" || d.BackupName != "alpha" || d.TriggeredBy != "manual" {
					t.Errorf("%+v", d)
				}
			}
		})
	}
}

func TestActivityAndLog(t *testing.T) {
	a := newAPI(t)
	if rec := a.do("GET", "/api/v1/activities/not-a-uuid", "", ""); rec.Code != 400 {
		t.Errorf("bad id = %d", rec.Code)
	}
	if rec := a.do("GET", "/api/v1/activities/"+uuid.New().String(), "", ""); rec.Code != 404 || errCode(t, rec) != CodeActivityNotFound {
		t.Errorf("unknown = %d", rec.Code)
	}

	act := a.addActivity("alpha", time.Now().UTC(), domain.StatusError)
	rec := a.do("GET", "/api/v1/activities/"+act.ID.String(), "", "")
	if d := decode[ActivityDTO](t, rec); rec.Code != 200 || d.Error == nil || d.DurationMS == nil || *d.DurationMS != 1000 {
		t.Errorf("get = %d %+v", rec.Code, d)
	}

	logURL := "/api/v1/activities/" + act.ID.String() + "/log"
	if rec := a.do("GET", logURL, "", ""); rec.Code != 404 || errCode(t, rec) != CodeLogNotFound {
		t.Errorf("missing log = %d", rec.Code)
	}
	if err := os.WriteFile(act.LogPath, []byte("l1\nl2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec = a.do("GET", logURL, "", "")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" || rec.Body.String() != "l1\nl2\n" {
		t.Errorf("log = %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
}

func TestListJobs(t *testing.T) {
	a := newAPI(t)
	a.runner.jobs = []domain.Job{
		{ActivityID: uuid.New(), BackupName: "alpha", Phase: domain.PhaseDump, StartedAt: time.Now()},
		{ActivityID: uuid.New(), BackupName: "beta", Phase: domain.PhaseRestic, StartedAt: time.Now()},
	}
	items := decode[ListDTO[JobDTO]](t, a.do("GET", "/api/v1/jobs", "", "")).Items
	if len(items) != 2 || items[0].Status != "running" || items[1].Phase != "restic" {
		t.Errorf("%+v", items)
	}
}