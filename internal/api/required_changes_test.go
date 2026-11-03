package api

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/banshee-lab/pgkeeper/internal/auth"
	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/events"
	"github.com/banshee-lab/pgkeeper/internal/logstore"
)

// ---- R-1 ----

func TestMeAuthDisabled(t *testing.T) {
	a := newAPI(t)
	rec := a.do("GET", "/api/v1/me", "", "")
	const want = `{"auth_enabled":false,"authenticated":true,"user":null}`
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("me = %d %s", rec.Code, rec.Body.String())
	}
}

// ---- R-2 ----

func TestRunningActivityLogBeforeFileExists(t *testing.T) {
	a := newAPI(t)
	running := a.addActivity("alpha", time.Now().UTC(), domain.StatusRunning)
	rec := a.do("GET", "/api/v1/activities/"+running.ID.String()+"/log", "", "")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" || rec.Body.Len() != 0 {
		t.Fatalf("running, no file = %d %q", rec.Code, rec.Body.String())
	}

	done := a.addActivity("alpha", time.Now().UTC(), domain.StatusSuccess)
	rec = a.do("GET", "/api/v1/activities/"+done.ID.String()+"/log", "", "")
	if rec.Code != 404 || errCode(t, rec) != CodeLogNotFound {
		t.Fatalf("completed, no file = %d %s", rec.Code, rec.Body.String())
	}
}

func TestLogStreamOpensBeforeFileExistsAndStaysOpen(t *testing.T) {
	a := newAPI(t)
	act := a.addActivity("alpha", time.Now().UTC(), domain.StatusRunning) // no log file yet
	srv := httptest.NewServer(a.router)
	defer srv.Close()

	resp, ch := openStream(t, srv.URL+"/api/v1/activities/"+act.ID.String()+"/log/stream", "")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	// The runner creates the file, writes, then publishes.
	appendLines(t, act.LogPath, "first")
	a.logHub.Publish(logstore.LogLine{ActivityID: act.ID, N: 1, Text: "first"})
	if e := next(t, ch); e.name != "log" || e.id != "1" {
		t.Fatalf("got %+v", e)
	}

	a.acts.finish(act.ID, domain.StatusSuccess, "")
	a.jobHub.Publish(events.JobEvent{Type: events.JobFinished, Activity: act})
	if e := next(t, ch); e.name != "end" {
		t.Fatalf("got %+v", e)
	}
	expectClosed(t, ch)
}

// ---- R-3 ----

func firstLines(t *testing.T, url string, n int) []string {
	t.Helper()
	resp, err := sseClient.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	var lines []string
	for len(lines) < n && sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

func TestSSERetryDirectiveFirst(t *testing.T) {
	a := newAPI(t)
	act := a.addActivity("alpha", time.Now().UTC(), domain.StatusRunning)
	srv := httptest.NewServer(a.router)
	defer srv.Close()

	jobs := firstLines(t, srv.URL+"/api/v1/jobs/stream", 4)
	if len(jobs) < 3 || jobs[0] != "retry: 3000" || jobs[1] != "" || jobs[2] != "event: snapshot" {
		t.Fatalf("jobs stream start = %q", jobs)
	}
	logs := firstLines(t, srv.URL+"/api/v1/activities/"+act.ID.String()+"/log/stream", 2)
	if len(logs) != 2 || logs[0] != "retry: 3000" || logs[1] != "" {
		t.Fatalf("log stream start = %q", logs)
	}
}

func withSession(h http.Handler, exp time.Time) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := &domain.Session{Subject: "u", ExpiresAt: exp}
		h.ServeHTTP(w, r.WithContext(auth.WithSession(r.Context(), s)))
	})
}

func TestJobsStreamClosesOnSessionExpiry(t *testing.T) {
	a := newAPI(t)
	srv := httptest.NewServer(withSession(a.router, time.Now().Add(300*time.Millisecond)))
	defer srv.Close()

	resp, ch := openStream(t, srv.URL+"/api/v1/jobs/stream", "")
	defer resp.Body.Close()
	if e := next(t, ch); e.name != "snapshot" {
		t.Fatalf("got %+v", e)
	}
	expectClosed(t, ch)
}

func TestLogStreamClosesOnSessionExpiry(t *testing.T) {
	a := newAPI(t)
	act := a.addActivity("alpha", time.Now().UTC(), domain.StatusRunning)
	appendLines(t, act.LogPath, "l1")
	srv := httptest.NewServer(withSession(a.router, time.Now().Add(300*time.Millisecond)))
	defer srv.Close()

	resp, ch := openStream(t, srv.URL+"/api/v1/activities/"+act.ID.String()+"/log/stream", "")
	defer resp.Body.Close()
	if e := next(t, ch); e.name != "log" {
		t.Fatalf("got %+v", e)
	}
	expectClosed(t, ch) // no "end": the activity is still running
}

// ---- R-4 ----

type fakeFeed struct {
	snap events.JobSnapshot
	ch   chan events.JobEvent
}

func (f *fakeFeed) SubscribeJobs() (events.JobSnapshot, <-chan events.JobEvent, func()) {
	return f.snap, f.ch, func() {}
}

func TestJobsStreamDiscardsEventsAtOrBeforeSnapshot(t *testing.T) {
	mk := func(seq uint64, typ events.JobEventType) events.JobEvent {
		return events.JobEvent{Seq: seq, Type: typ, Job: domain.Job{
			ActivityID: uuid.New(), BackupName: "alpha", StartedAt: time.Now().UTC()}}
	}
	stale4, stale5, fresh6, abandoned7 := mk(4, events.JobStarted), mk(5, events.JobStarted),
		mk(6, events.JobStarted), mk(7, events.JobAbandoned)
	feed := &fakeFeed{snap: events.JobSnapshot{Seq: 5}, ch: make(chan events.JobEvent, 8)}
	for _, ev := range []events.JobEvent{stale4, stale5, fresh6, abandoned7} {
		feed.ch <- ev
	}
	a := newAPI(t, func(d *Deps) { d.Jobs = feed })
	srv := httptest.NewServer(a.router)
	defer srv.Close()

	resp, ch := openStream(t, srv.URL+"/api/v1/jobs/stream", "")
	defer resp.Body.Close()
	if e := next(t, ch); e.name != "snapshot" {
		t.Fatalf("got %+v", e)
	}
	e := next(t, ch)
	if e.name != "job.started" || !strings.Contains(e.data, fresh6.Job.ActivityID.String()) {
		t.Fatalf("first live event = %+v, want seq 6 only", e)
	}
	expectClosed(t, ch) // abandoned job closes the stream; no job.finished is emitted
}

func TestBackupsRunningComesFromPersistedActivity(t *testing.T) {
	a := newAPI(t)
	a.addActivity("beta", time.Now().UTC(), domain.StatusRunning)
	// In-memory state that disagrees with SQLite must not influence the result.
	a.runner.jobs = []domain.Job{{ActivityID: uuid.New(), BackupName: "alpha", Phase: domain.PhaseDump}}

	items := decode[ListDTO[BackupDTO]](t, a.do("GET", "/api/v1/backups", "", "")).Items
	if items[0].Name != "alpha" || items[0].Running {
		t.Errorf("alpha must not be running: %+v", items[0])
	}
	if items[1].Name != "beta" || !items[1].Running || items[1].LastActivity == nil ||
		items[1].LastActivity.Status != "running" {
		t.Errorf("beta must be running with a running last_activity: %+v", items[1])
	}
}