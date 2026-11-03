package logstore

import (
	"bytes"
	"io"
	"strings"
)

// Redactor is implemented by any type that can redact a string.
// restic.Redactor satisfies this interface.
type Redactor interface {
	Redact(s string) string
}

// StringRedactor is a Redactor that masks a single secret.
// An empty StringRedactor is a no-op.
type StringRedactor string

func (s StringRedactor) Redact(text string) string {
	if s == "" {
		return text
	}
	return strings.ReplaceAll(text, string(s), "***")
}

// RedactWriter wraps an io.Writer and applies a Redactor to every complete
// line before forwarding it. It handles split writes and partial lines
// correctly. Close flushes any pending partial line.
type RedactWriter struct {
	r   Redactor
	w   io.Writer
	buf []byte
}

// NewRedactWriter creates a RedactWriter. r may be nil (pass-through).
func NewRedactWriter(w io.Writer, r Redactor) *RedactWriter {
	return &RedactWriter{r: r, w: w}
}

func (rw *RedactWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			rw.buf = append(rw.buf, p...)
			break
		}
		rw.buf = append(rw.buf, p[:i]...)
		p = p[i+1:]
		if err := rw.flush(); err != nil {
			return n, err
		}
	}
	return n, nil
}

func (rw *RedactWriter) flush() error {
	line := string(rw.buf)
	rw.buf = rw.buf[:0]
	if rw.r != nil {
		line = rw.r.Redact(line)
	}
	_, err := io.WriteString(rw.w, line+"\n")
	return err
}

// Close flushes any buffered partial line.
func (rw *RedactWriter) Close() error {
	if len(rw.buf) > 0 {
		return rw.flush()
	}
	return nil
}