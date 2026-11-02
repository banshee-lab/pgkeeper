package logstore

import (
	"path/filepath"
	"strings"
	"testing"

	"uuid"
)

func TestPartialLinesAndCRLF(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d", "l.log")
	w, err := Open(p, uuid.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("hel"))
	w.Write([]byte("lo\r\nwor"))
	w.Write([]byte("ld\n"))
	w.Logf("INFO", "done %d", 1)
	w.Close()

	lines, err := ReadLines(p, 0)
	if err != nil || len(lines) != 3 {
		t.Fatalf("lines=%v err=%v", lines, err)
	}
	if !strings.HasSuffix(lines[0].Text, "[pg] hello") || !strings.HasSuffix(lines[1].Text, "[pg] world") ||
		!strings.HasSuffix(lines[2].Text, "INFO done 1") {
		t.Fatalf("unexpected: %v", lines)
	}
	if after, _ := ReadLines(p, 2); len(after) != 1 || after[0].N != 3 {
		t.Fatalf("afterLine failed: %v", after)
	}
}

func TestLongLineTruncated(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.log")
	w, _ := Open(p, uuid.New(), nil)
	w.Write([]byte(strings.Repeat("a", maxLine+500) + "\n"))
	w.Close()
	lines, _ := ReadLines(p, 0)
	if len(lines) != 1 || !strings.HasSuffix(lines[0].Text, truncMarker) || len(lines[0].Text) > maxLine+100 {
		t.Fatalf("not truncated: %d", len(lines[0].Text))
	}
}