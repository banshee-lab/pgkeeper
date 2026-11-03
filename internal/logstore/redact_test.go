package logstore

import (
	"strings"
	"testing"
)

type fixedRedactor struct{ secret string }

func (f fixedRedactor) Redact(s string) string {
	return strings.ReplaceAll(s, f.secret, "***")
}

func TestRedactWriterCompleteLines(t *testing.T) {
	var buf strings.Builder
	rw := NewRedactWriter(&buf, fixedRedactor{secret: "hunter2"})

	// single write with two lines
	if _, err := rw.Write([]byte("password is hunter2\nsecond line\n")); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.Contains(got, "hunter2") {
		t.Errorf("secret leaked: %q", got)
	}
	if !strings.Contains(got, "***") {
		t.Errorf("redaction marker missing: %q", got)
	}
	if !strings.Contains(got, "second line") {
		t.Errorf("second line missing: %q", got)
	}
}

func TestRedactWriterSplitWrite(t *testing.T) {
	var buf strings.Builder
	rw := NewRedactWriter(&buf, fixedRedactor{secret: "s3cr3t"})

	// Write the secret across two Write calls (split mid-secret)
	part1 := "prefix s3c"
	part2 := "r3t suffix\n"
	if _, err := rw.Write([]byte(part1)); err != nil {
		t.Fatal(err)
	}
	if _, err := rw.Write([]byte(part2)); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.Contains(got, "s3cr3t") {
		t.Errorf("secret leaked across split write: %q", got)
	}
}

func TestRedactWriterPartialLineFlushOnClose(t *testing.T) {
	var buf strings.Builder
	rw := NewRedactWriter(&buf, fixedRedactor{secret: "tok"})

	if _, err := rw.Write([]byte("no newline tok")); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "" {
		t.Errorf("partial line emitted before Close: %q", got)
	}
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.Contains(got, "tok") {
		t.Errorf("secret leaked in partial flush: %q", got)
	}
}

func TestRedactWriterNilRedactor(t *testing.T) {
	var buf strings.Builder
	rw := NewRedactWriter(&buf, nil)
	rw.Write([]byte("plain text\n"))
	if got := buf.String(); got != "plain text\n" {
		t.Errorf("got %q", got)
	}
}