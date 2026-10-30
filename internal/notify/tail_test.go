package notify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTailLog(t *testing.T) {
	cases := []struct {
		name, content string
		lines, bytes  int
		want          string
	}{
		{"empty", "", 5, 100, ""},
		{"fewer lines than max", "a\nb\n", 5, 100, "a\nb"},
		{"more lines than max", "1\n2\n3\n4\n", 2, 100, "3\n4"},
		{"no trailing newline", "a\nb", 5, 100, "a\nb"},
		{"huge single line keeps tail", strings.Repeat("x", 1000) + "END", 5, 10, strings.Repeat("x", 7) + "END"},
		{"byte window drops partial first line", "aaaa\nbb\ncc\n", 5, 8, "bb\ncc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "x.log")
			if err := os.WriteFile(p, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := TailLog(p, tc.lines, tc.bytes); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
	if TailLog("/nonexistent/file", 5, 100) != "" {
		t.Fatal("missing file must return empty")
	}
}