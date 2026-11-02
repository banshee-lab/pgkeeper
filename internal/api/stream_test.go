package api

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"uuid"

	"go.uber.org/goleak"

	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/events"
	"github.com/banshee-lab/pgkeeper/internal/logstore"
)

type sseEvent struct{ name, id, data string }

// No keep-alive: closing the body really closes the connection.
var sseClient = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

func openStream(t *testing.T, url, lastID string) (*http.Response, <-chan sseEvent) {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := sseClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan sseEvent, 64)
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(resp.Body)
		var ev sseEvent
		for sc.Scan() {
			switch line := sc.Text(); {
			case line == "":
				if ev.name != "" {
					ch <- ev
				}
				ev = sseEvent{}
			case strings.HasPrefix(line, ":"):
				ch <- sseEvent{name: "comment", data: line}
			case strings.HasPrefix(line, "event: "):
				ev.name = line[len("event: "):]
			case strings.HasPrefix(line, "id: "):
				ev.id = line[len("id: "):]
			case strings.HasPrefix(line, "data: "):
				ev.data = line[len("data: "):]
			}
		}
	}()
	return resp, ch
}

func next(t *testing.T, ch <-chan sseEvent) sseEvent {
	t.Helper()
	select {
	case e, ok := <-ch:
		if !ok {
			t.Fatal("stream closed unexpectedly")
		}
		return e
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for an SSE event")
	}
	return sseEvent{}
}

func expectClosed(t *testing.T, ch <-chan sseEvent) {
	t.Helper()
	select {
	case e, ok := <-ch:
		if ok {
			t.Fatalf("expected close, got %+v", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream not closed")
	}
}

func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestJobsStream(t *testing.T) {
	a := newAPI(t)
	job := domain.Job{ActivityID: uuid.New(), BackupName: "alpha", Database: "db",
		TriggeredBy: domain.TriggerManual, Phase: domain.PhaseDump, StartedAt: time.Now().UTC()}
	a.runner.jobs = []domain.Job{job}
	srv := httptest.NewServer(a.router)
	defer srv.Close()

	resp, ch := openStream(t, srv.URL+"/api/v1/jobs/stream", "")
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	if e := next(t, ch); e.name != "snapshot" || !strings.Contains(e.data, job.ActivityID.String()) {
		t.Fatalf("first event = %+v", e)
	}

	fin := job.StartedAt.Add(2 * time.Second)
	act := domain.Activity{ID: job.ActivityID, BackupName: "alpha", Database: "db",
		TriggeredBy: domain.TriggerManual, Status: domain.StatusSuccess, StartedAt: job.StartedAt, FinishedAt: &fin}
	a.acts.put(act)

	a.jobHub.Publish(events.JobEvent{Type: events.JobStarted, Job: job, Activity: domain.Activity{ID: job.ActivityID}})
	job.Phase = domain.PhaseRestic
	a.jobHub.Publish(events.JobEvent{Type: events.JobPhase, Job: job, Activity: domain.Activity{ID: job.ActivityID}})
	// Like the real runner, the finished event carries the activity (its ID is
	// what the handler uses to re-read the final row from the DB).
	a.jobHub.Publish(events.JobEvent{Type: events.JobFinished, Job: job, Activity: domain.Activity{ID: job.ActivityID}})

	for _, want := range []string{"job.started", "job.phase"} {
		if e := next(t, ch); e.name != want {
			t.Fatalf("got %s, want %s", e.name, want)
		}
	}
	e := next(t, ch)
	if e.name != "job.finished" || !strings.Contains(e.data, `"status":"success"`) || !strings.Contains(e.data, `"duration_ms":2000`) {
		t.Fatalf("finished = %+v", e)
	}
}

func TestStreamsHeartbeat(t *testing.T) {
	a := newAPI(t, func(d *Deps) { d.Heartbeat = 20 * time.Millisecond })
	srv := httptest.NewServer(a.router)
	defer srv.Close()
	resp, ch := openStream(t, srv.URL+"/api/v1/jobs/stream", "")
	defer resp.Body.Close()
	next(t, ch) // snapshot
	if e := next(t, ch); e.name != "comment" || e.data != ": ping" {
		t.Fatalf("got %+v", e)
	}
}

func TestLogStreamFinishedReplayAndResume(t *testing.T) {
	a := newAPI(t)
	act := a.addActivity("alpha", time.Now().UTC(), domain.StatusSuccess)
	appendLines(t, act.LogPath, "l1", "l2", "l3", "l4", "l5")
	srv := httptest.NewServer(a.router)
	defer srv.Close()

	resp, ch := openStream(t, srv.URL+"/api/v1/activities/"+act.ID.String()+"/log/stream", "3") // resume after line 3
	defer resp.Body.Close()
	for _, want := range []string{"4", "5"} {
		if e := next(t, ch); e.name != "log" || e.id != want {
			t.Fatalf("got %+v, want id %s", e, want)
		}
	}
	if e := next(t, ch); e.name != "end" || !strings.Contains(e.data, `"status":"success"`) {
		t.Fatalf("end = %+v", e)
	}
	expectClosed(t, ch)
}

func TestLogStreamLiveOverlapGapAndDuplicates(t *testing.T) {
	a := newAPI(t)
	act := a.addActivity("alpha", time.Now().UTC(), domain.StatusRunning)
	appendLines(t, act.LogPath, "l1", "l2", "l3")
	srv := httptest.NewServer(a.router)
	defer srv.Close()

	resp, ch := openStream(t, srv.URL+"/api/v1/activities/"+act.ID.String()+"/log/stream", "")
	defer resp.Body.Close()
	var ids []string
	for range 3 {
		ids = append(ids, next(t, ch).id)
	}

	pub := func(n int, text string) {
		a.logHub.Publish(logstore.LogLine{ActivityID: act.ID, N: n, Text: text})
	}
	pub(3, "l3") // duplicate of an already replayed line
	// Writer order: file first, then publish. Line 5 is "dropped" by the hub.
	appendLines(t, act.LogPath, "l4", "l5", "l6")
	pub(4, "l4")
	pub(6, "l6") // gap -> handler must refill 5 from the file
	pub(6, "l6") // duplicate

	for range 3 {
		ids = append(ids, next(t, ch).id)
	}

	a.acts.finish(act.ID, domain.StatusError, "restic: boom")
	a.jobHub.Publish(events.JobEvent{Type: events.JobFinished, Activity: act})
	e := next(t, ch)
	if e.name != "end" || !strings.Contains(e.data, `"status":"error"`) || !strings.Contains(e.data, "restic: boom") {
		t.Fatalf("end = %+v", e)
	}
	if got := strings.Join(ids, ","); got != "1,2,3,4,5,6" {
		t.Fatalf("ids = %s, want 1..6 without gaps or duplicates", got)
	}
	expectClosed(t, ch)
}

func TestLogStreamDisconnectReleasesSubscription(t *testing.T) {
	ignore := goleak.IgnoreCurrent()
	a := newAPI(t)
	act := a.addActivity("alpha", time.Now().UTC(), domain.StatusRunning)
	appendLines(t, act.LogPath, "l1")
	srv := httptest.NewServer(a.router)

	resp, ch := openStream(t, srv.URL+"/api/v1/activities/"+act.ID.String()+"/log/stream", "")
	next(t, ch) // replayed line 1 => subscriptions are in place
	resp.Body.Close()
	srv.Close() // waits for the handler to return

	// A leaked subscriber would overflow its 256-slot buffer and bump Dropped().
	for i := range 300 {
		a.logHub.Publish(logstore.LogLine{ActivityID: act.ID, N: i + 2, Text: "x"})
	}
	if d := a.logHub.Dropped(); d != 0 {
		t.Fatalf("subscription not released: %d dropped", d)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	goleak.VerifyNone(t, ignore)
}