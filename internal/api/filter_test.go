package api

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestParsePageLimit(t *testing.T) {
	cases := []struct{ q string; want int; ok bool }{
		{"", 10, true},
		{"?limit=5", 5, true},
		{"?limit=100", 100, true},
		{"?limit=101", 100, true},
		{"?limit=0", 0, false},
		{"?limit=-1", 0, false},
		{"?limit=abc", 0, false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", "/"+tc.q, nil)
		n, ok := parsePageLimit(req)
		if ok != tc.ok || (ok && n != tc.want) {
			t.Errorf("q=%q: got (%d,%v), want (%d,%v)", tc.q, n, ok, tc.want, tc.ok)
		}
	}
}

func TestParseActivityFilterStatus(t *testing.T) {
	for _, st := range []string{"running", "success", "error"} {
		req := httptest.NewRequest("GET", "/?status="+st, nil)
		f, ok := parseActivityFilter(req)
		if !ok || string(f.Status) != st {
			t.Errorf("status=%s: got (%v,%v)", st, f.Status, ok)
		}
	}
	req := httptest.NewRequest("GET", "/?status=bogus", nil)
	if _, ok := parseActivityFilter(req); ok {
		t.Error("bogus status should fail")
	}
}

func TestParseActivityFilterSince(t *testing.T) {
	ts := "2026-01-02T15:04:05Z"
	req := httptest.NewRequest("GET", "/?since="+ts, nil)
	f, ok := parseActivityFilter(req)
	if !ok || f.Since.IsZero() {
		t.Errorf("since=%s: got %v, ok=%v", ts, f.Since, ok)
	}
	want, _ := time.Parse(time.RFC3339, ts)
	if !f.Since.Equal(want) {
		t.Errorf("since mismatch: got %v want %v", f.Since, want)
	}

	// bad format
	req2 := httptest.NewRequest("GET", "/?since=not-a-date", nil)
	if _, ok := parseActivityFilter(req2); ok {
		t.Error("bad since should fail")
	}
}

func TestSafeFilename(t *testing.T) {
	cases := []struct{ name, at, want string }{
		{"my-backup", "2026-11-03T03:00:00Z", "my-backup_2026-11-03T03-00-00Z.log"},
		{"a/b", "2026-01-01T00:00:00Z", "a_b_2026-01-01T00-00-00Z.log"},
	}
	for _, tc := range cases {
		if got := safeFilename(tc.name, tc.at); got != tc.want {
			t.Errorf("safeFilename(%q,%q) = %q, want %q", tc.name, tc.at, got, tc.want)
		}
	}
}