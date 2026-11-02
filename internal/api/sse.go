package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type sseWriter struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

// newSSE sets the SSE headers, disables the write deadline and flushes the
// header. It writes a 500 itself and returns false if streaming is unsupported.
func newSSE(w http.ResponseWriter) (*sseWriter, bool) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // ErrNotSupported (e.g. recorders) is fine

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	if err := rc.Flush(); err != nil {
		h.Del("Cache-Control")
		h.Del("Connection")
		h.Del("X-Accel-Buffering")
		writeError(w, http.StatusInternalServerError, CodeInternal, "streaming unsupported")
		return nil, false
	}
	return &sseWriter{w: w, rc: rc}, true
}

func (s *sseWriter) event(name, id string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if id != "" {
		_, err = fmt.Fprintf(s.w, "event: %s\nid: %s\ndata: %s\n\n", name, id, b)
	} else {
		_, err = fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, b)
	}
	if err != nil {
		return err
	}
	return s.rc.Flush()
}

func (s *sseWriter) comment(text string) error {
	if _, err := fmt.Fprintf(s.w, ": %s\n\n", text); err != nil {
		return err
	}
	return s.rc.Flush()
}