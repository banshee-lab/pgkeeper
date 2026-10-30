package notify

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
)

// TailLog returns up to maxLines lines / maxBytes bytes from the end of the
// file, reading only the tail. Returns "" if the file is missing or empty.
// If the window starts mid-line, the partial first line is dropped, unless
// the window contains no newline (huge single line): then the raw tail is kept.
func TailLog(path string, maxLines, maxBytes int) string {
	if maxLines <= 0 || maxBytes <= 0 {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return ""
	}

	size := fi.Size()
	start := max(size-int64(maxBytes), 0)
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return ""
	}

	if start > 0 {
		prev := make([]byte, 1)
		if _, err := f.ReadAt(prev, start-1); err == nil && prev[0] != '\n' {
			if i := bytes.IndexByte(buf, '\n'); i >= 0 {
				buf = buf[i+1:]
			}
		}
	}
	if len(buf) == 0 {
		return ""
	}

	s := strings.ToValidUTF8(string(buf), "") // window may start inside a multi-byte rune
	s = strings.TrimRight(s, "\r\n")
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}