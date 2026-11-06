package restic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

type diffStatsWriter struct {
	buf       []byte
	source    string
	target    string
	stats     domain.RepositorySnapshotStats
	foundStat bool
}

func (w *diffStatsWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		if err := w.consume(w.buf[:i]); err != nil {
			return 0, err
		}
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) > maxLine {
		return 0, fmt.Errorf("restic diff output line exceeds %d bytes", maxLine)
	}
	return len(p), nil
}

func (w *diffStatsWriter) finish() error {
	if len(bytes.TrimSpace(w.buf)) > 0 {
		if err := w.consume(w.buf); err != nil {
			return err
		}
	}
	if !w.foundStat {
		return fmt.Errorf("restic diff output did not include statistics")
	}
	return nil
}

func (w *diffStatsWriter) consume(line []byte) error {
	if len(bytes.TrimSpace(line)) == 0 {
		return nil
	}
	var message struct {
		MessageType    string `json:"message_type"`
		SourceSnapshot string `json:"source_snapshot"`
		TargetSnapshot string `json:"target_snapshot"`
		ChangedFiles   int64  `json:"changed_files"`
		Added          struct {
			Files int64 `json:"files"`
			Bytes int64 `json:"bytes"`
		} `json:"added"`
	}
	if err := json.Unmarshal(line, &message); err != nil {
		return fmt.Errorf("decode restic diff output: %w", err)
	}
	if message.MessageType != "statistics" {
		return nil
	}
	if strings.ToLower(message.SourceSnapshot) != strings.ToLower(w.source) ||
		strings.ToLower(message.TargetSnapshot) != strings.ToLower(w.target) {
		return fmt.Errorf("restic diff returned statistics for an unexpected snapshot pair")
	}
	w.stats = domain.RepositorySnapshotStats{
		Available:    true,
		FilesAdded:   message.Added.Files,
		FilesChanged: message.ChangedFiles,
		BytesAdded:   message.Added.Bytes,
	}
	w.foundStat = true
	return nil
}
