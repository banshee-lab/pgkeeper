package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/events"
	"github.com/banshee-lab/pgkeeper/internal/logstore"
)

// ---- GET /api/v1/jobs/stream ----

func (h *Handler) StreamJobs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Subscribe first, then snapshot: nothing can fall between the two.
	// Duplicates are harmless (clients key by activity_id).
	ch, cancel := h.JobHub.Subscribe(nil)
	defer cancel()

	sse, ok := newSSE(w)
	if !ok {
		return
	}
	if err := sse.event("snapshot", "", ListDTO[JobDTO]{Items: ToJobDTOs(h.Runner.Running())}); err != nil {
		return
	}

	tick := time.NewTicker(h.Heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if sse.comment("ping") != nil {
				return
			}
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if h.sendJobEvent(ctx, sse, ev) != nil {
				return
			}
		}
	}
}

func (h *Handler) sendJobEvent(ctx context.Context, sse *sseWriter, ev events.JobEvent) error {
	switch ev.Type {
	case events.JobStarted:
		return sse.event("job.started", "", ToJobDTO(ev.Job))
	case events.JobPhase:
		return sse.event("job.phase", "", ToJobDTO(ev.Job))
	case events.JobFinished:
		act := ev.Activity
		// Re-read so finished_at / duration_ms are the persisted values.
		if a, err := h.Activities.Get(ctx, act.ID); err == nil {
			act = *a
		}
		return sse.event("job.finished", "", JobFinishedDTO{Job: ToJobDTO(ev.Job), Activity: ToActivityDTO(act)})
	}
	return nil
}

// ---- GET /api/v1/activities/{id}/log/stream ----

func lastEventID(r *http.Request) int {
	s := r.Header.Get("Last-Event-ID")
	if s == "" {
		s = r.URL.Query().Get("last_event_id")
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// logFollower sends each log line at most once, in order. `last` is the
// highest line number already sent.
type logFollower struct {
	sse  *sseWriter
	path string
	last int
}

func (f *logFollower) send(n int, text string) error {
	if n <= f.last {
		return nil
	}
	if err := f.sse.event("log", strconv.Itoa(n), LogLineDTO{Line: text}); err != nil {
		return err
	}
	f.last = n
	return nil
}

// catchUp sends every line in the file after f.last.
func (f *logFollower) catchUp() error {
	lines, err := logstore.ReadLines(f.path, f.last)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, l := range lines {
		if err := f.send(l.N, l.Text); err != nil {
			return err
		}
	}
	return nil
}

// live handles a hub line: duplicates are skipped, gaps (dropped by the hub
// for slow clients) are refilled from the file.
func (f *logFollower) live(l logstore.LogLine) error {
	if l.N <= f.last {
		return nil
	}
	if l.N > f.last+1 {
		if err := f.catchUp(); err != nil {
			return err
		}
	}
	return f.send(l.N, l.Text)
}

func (h *Handler) StreamActivityLog(w http.ResponseWriter, r *http.Request) {
	act, ok := h.loadActivity(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	var (
		logCh  <-chan logstore.LogLine
		doneCh <-chan events.JobEvent
	)
	if act.Status == domain.StatusRunning {
		id := act.ID
		// Subscribe BEFORE reading the file so no line is lost in between.
		lc, cancelLog := h.LogHub.Subscribe(func(l logstore.LogLine) bool { return l.ActivityID == id })
		defer cancelLog()
		jc, cancelJob := h.JobHub.Subscribe(func(e events.JobEvent) bool {
			return e.Type == events.JobFinished && e.Activity.ID == id
		})
		defer cancelJob()
		logCh, doneCh = lc, jc
		// It may have finished between the first read and the subscriptions.
		if cur, err := h.Activities.Get(ctx, id); err == nil {
			act = cur
		}
	}

	sse, ok := newSSE(w)
	if !ok {
		return
	}
	f := &logFollower{sse: sse, path: act.LogPath, last: lastEventID(r)}
	if f.catchUp() != nil {
		return
	}

	if act.Status == domain.StatusRunning {
		tick := time.NewTicker(h.Heartbeat)
		defer tick.Stop()
		for finished := false; !finished; {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if sse.comment("ping") != nil {
					return
				}
				// Safety net if the finished event was dropped.
				if cur, err := h.Activities.Get(ctx, act.ID); err == nil && cur.Status != domain.StatusRunning {
					finished = true
				}
			case l, ok := <-logCh:
				if !ok {
					return
				}
				if f.live(l) != nil {
					return
				}
			case <-doneCh:
				finished = true
			}
		}
		// Drain what is already buffered, then fill any dropped lines from the file.
		for drained := false; !drained; {
			select {
			case l, ok := <-logCh:
				if !ok {
					drained = true
					break
				}
				if f.live(l) != nil {
					return
				}
			default:
				drained = true
			}
		}
		if f.catchUp() != nil {
			return
		}
	}

	end := LogEndDTO{Status: string(act.Status), Error: act.Error}
	if cur, err := h.Activities.Get(ctx, act.ID); err == nil {
		end = LogEndDTO{Status: string(cur.Status), Error: cur.Error}
	}
	if end.Status == string(domain.StatusRunning) { // DB unreadable: never emit a non-final status
		end = LogEndDTO{Status: string(domain.StatusError), Error: "final status unavailable"}
	}
	_ = sse.event("end", "", end)
}