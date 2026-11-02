package restic

import (
	"bytes"
	"testing"
)

func TestRedactRepo(t *testing.T) {
	cases := map[string]string{
		"rest:https://user:pass@host/": "rest:https://***@host/",
		"/srv/restic":                  "/srv/restic",
		"s3:s3.amazonaws.com/bucket":   "s3:s3.amazonaws.com/bucket",
	}
	for in, want := range cases {
		if got := RedactRepo(in); got != want {
			t.Errorf("RedactRepo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRedactor(t *testing.T) {
	x := NewRedactor("hunter2", "")
	got := x.Redact("pw hunter2 at rest:https://u:p@h/")
	if want := "pw *** at rest:https://***@h/"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRedactWriterSplitSecret(t *testing.T) {
	var buf bytes.Buffer
	w := NewRedactor("hunter2").Writer(&buf)
	_, _ = w.Write([]byte("abc hun"))
	_, _ = w.Write([]byte("ter2 def\nrest"))
	_ = w.Close()
	if want := "abc *** def\nrest\n"; buf.String() != want {
		t.Fatalf("got %q want %q", buf.String(), want)
	}
}