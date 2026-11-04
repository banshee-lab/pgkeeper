package restic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// maxLine bounds how much unterminated output is buffered before it is
// flushed as a line.
const maxLine = 1 << 20

// backupMsg is the subset of `restic backup --json` messages pgkeeper reads.
type backupMsg struct {
	MessageType  string `json:"message_type"`
	FilesNew     int64  `json:"files_new"`
	FilesChanged int64  `json:"files_changed"`
	FilesSame    int64  `json:"files_unmodified"`
	DataAdded    int64  `json:"data_added"`
	SnapshotID   string `json:"snapshot_id"`
	Message      string `json:"message"`
	Item         string `json:"item"`
	Error        *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// backupOutput turns the JSON stream of `restic backup --json` into readable
// log lines and remembers the final summary. Progress ("status") messages are
// dropped, anything that is not JSON is passed through untouched. Callers set
// dst before use; it must not be shared between concurrent writers.
type backupOutput struct {
	dst     io.Writer
	buf     []byte
	summary *backupMsg
}

var _ io.WriteCloser = (*backupOutput)(nil)

func (b *backupOutput) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	for {
		i := bytes.IndexByte(b.buf, '\n')
		if i < 0 {
			break
		}
		b.line(b.buf[:i])
		b.buf = b.buf[i+1:]
	}
	if len(b.buf) > maxLine {
		b.line(b.buf)
		b.buf = nil
	}
	return len(p), nil
}

// Close flushes an unterminated last line.
func (b *backupOutput) Close() error {
	if len(b.buf) > 0 {
		b.line(b.buf)
		b.buf = nil
	}
	return nil
}

func (b *backupOutput) emit(s string) { _, _ = io.WriteString(b.dst, s+"\n") }

func (b *backupOutput) line(raw []byte) {
	l := strings.TrimSpace(string(raw))
	if l == "" {
		return
	}
	if l[0] != '{' {
		b.emit(l)
		return
	}
	var m backupMsg
	if err := json.Unmarshal([]byte(l), &m); err != nil || m.MessageType == "" {
		b.emit(l)
		return
	}
	switch m.MessageType {
	case "status":
		// progress noise
	case "summary":
		b.summary = &m
		b.emit(fmt.Sprintf("snapshot %s saved: %d new, %d changed, %d unmodified files, %d bytes added",
			m.SnapshotID, m.FilesNew, m.FilesChanged, m.FilesSame, m.DataAdded))
	case "error":
		msg := m.Message
		if m.Error != nil {
			msg = m.Error.Message
		}
		if m.Item != "" {
			msg = m.Item + ": " + msg
		}
		b.emit("error: " + msg)
	case "exit_error":
		b.emit("error: " + m.Message)
	default:
		b.emit(l)
	}
}
