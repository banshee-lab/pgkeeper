package api

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

const (
	maxPageLimit     = 100
	defaultPageLimit = 10
)

// parsePageLimit returns the clamped limit (1–100) or (0, false) for bad input.
func parsePageLimit(r *http.Request) (int, bool) {
	s := r.URL.Query().Get("limit")
	if s == "" {
		return defaultPageLimit, true
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n < 1 {
		return 0, false
	}
	if n > maxPageLimit {
		n = maxPageLimit
	}
	return n, true
}

// parseActivityFilter builds an ActivityFilter from query params.
// Returns (filter, true) or ("", false) if a param is malformed.
func parseActivityFilter(r *http.Request) (domain.ActivityFilter, bool) {
	q := r.URL.Query()
	f := domain.ActivityFilter{}

	if st := q.Get("status"); st != "" {
		switch domain.Status(st) {
		case domain.StatusRunning, domain.StatusSuccess, domain.StatusError, domain.StatusWarning:
			f.Status = domain.Status(st)
		default:
			return f, false
		}
	}

	if since := q.Get("since"); since != "" {
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			return f, false
		}
		f.Since = t.UTC()
	}

	if cursor := q.Get("cursor"); cursor != "" {
		// Validate it decodes; actual use happens in the store.
		if _, err := base64.RawURLEncoding.DecodeString(cursor); err != nil {
			return f, false
		}
		f.Cursor = cursor
	}

	return f, true
}

// unsafeFilenameRe matches characters not safe in Content-Disposition filenames.
var unsafeFilenameRe = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

// safeFilename produces a filename like "mybackup_2026-11-03T03-00-00Z.log".
// backup names are already validated (labelRe); started_at is always RFC3339.
func safeFilename(backupName, startedAt string) string {
	name := unsafeFilenameRe.ReplaceAllString(backupName, "_")
	ts := strings.NewReplacer(":", "-").Replace(startedAt)
	return name + "_" + ts + ".log"
}