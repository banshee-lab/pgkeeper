package logstore

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/banshee-lab/pgkeeper/internal/events"
)

const (
	maxLine     = 64 << 10
	truncMarker = " …[truncated]"
)

type LogLine struct {
	ActivityID uuid.UUID
	N          int // 1-based line number in the file
	Text       string
}

// Writer is a line-oriented io.Writer (used for stderr) plus a Logf for app messages.
type Writer struct {
	mu     sync.Mutex
	f      *os.File
	hub    *events.Hub[LogLine]
	id     uuid.UUID
	lineNo int
	buf    []byte
	trunc  bool
	closed bool
	now    func() time.Time
}

func Open(path string, id uuid.UUID, hub *events.Hub[LogLine]) (*Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o640)
	if err != nil {
		return nil, err
	}
	return &Writer{f: f, hub: hub, id: id, now: time.Now}, nil
}

// Logf writes "<RFC3339> LEVEL message". Embedded newlines become separate lines.
func (w *Writer) Logf(level, format string, args ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	for _, l := range strings.Split(fmt.Sprintf(format, args...), "\n") {
		w.emit(level, limit(strings.TrimRight(l, "\r")))
	}
}

// Write splits p on '\n'; partial lines are kept until completed. Lines are prefixed "[pg] ".
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		chunk := p
		if i >= 0 {
			chunk, p = p[:i], p[i+1:]
		} else {
			p = nil
		}
		w.appendPartial(chunk)
		if i >= 0 {
			w.flushLine()
		}
	}
	return n, nil
}

func (w *Writer) appendPartial(chunk []byte) {
	room := max(maxLine-len(w.buf), 0)
	if len(chunk) > room {
		chunk = chunk[:room]
		w.trunc = true
	}
	w.buf = append(w.buf, chunk...)
}

func (w *Writer) flushLine() {
	text := strings.TrimRight(string(w.buf), "\r")
	text = strings.ToValidUTF8(text, "")
	if w.trunc {
		text += truncMarker
	}
	w.buf, w.trunc = w.buf[:0], false
	w.emit("INFO", "[pg] "+text)
}

func (w *Writer) emit(level, text string) {
	w.lineNo++
	line := w.now().UTC().Format(time.RFC3339) + " " + level + " " + text
	_, _ = w.f.WriteString(line + "\n")
	w.hub.Publish(LogLine{ActivityID: w.id, N: w.lineNo, Text: line})
}

// Close flushes a pending partial line, syncs and closes. Idempotent.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	if len(w.buf) > 0 {
		w.flushLine()
	}
	w.closed = true
	_ = w.f.Sync()
	return w.f.Close()
}

func limit(s string) string {
	if len(s) > maxLine {
		return strings.ToValidUTF8(s[:maxLine], "") + truncMarker
	}
	return s
}

// ReadLines returns lines with N > afterLine. ActivityID is left zero; callers fill it.
func ReadLines(path string, afterLine int) ([]LogLine, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 2*maxLine)
	var out []LogLine
	n := 0
	for sc.Scan() {
		n++
		if n > afterLine {
			out = append(out, LogLine{N: n, Text: sc.Text()})
		}
	}
	return out, sc.Err()
}