package api

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"os"
	"testing"
	"time"
	"uuid"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

// Complements TestRoutesMatchOpenAPI (routes) by validating real response
// bodies, headers and status codes against docs/openapi.yaml.
func TestResponsesMatchOpenAPI(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile("../../docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	rt, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}

	a := newAPI(t, func(d *Deps) {
		d.Snapshots = fakeSnaps{"alpha": {ID: "abc123de", Timestamp: time.Now().UTC(), FilesAdded: 1, FilesChanged: 2, BytesAdded: 3}}
	})
	done := a.addActivity("alpha", time.Now().Add(-time.Hour).UTC(), domain.StatusError)
	a.addActivity("alpha", time.Now().UTC(), domain.StatusRunning)
	if err := os.WriteFile(done.LogPath, []byte("l1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.runner.jobs = []domain.Job{{ActivityID: uuid.New(), BackupName: "beta", Database: "db",
		TriggeredBy: domain.TriggerSchedule, Phase: domain.PhaseDump, StartedAt: time.Now().UTC()}}

	missing := uuid.New().String()
	cases := []struct{ method, path string }{
		{"GET", "/healthz"},
		{"GET", "/api/v1/me"},
		{"GET", "/api/v1/backups"},
		{"GET", "/api/v1/backups/alpha"},
		{"GET", "/api/v1/backups/nope"},
		{"GET", "/api/v1/backups/alpha/activities"},
		{"GET", "/api/v1/backups/alpha/activities?limit=abc"},
		{"POST", "/api/v1/backups/alpha/run"},
		{"GET", "/api/v1/activities"},
		{"GET", "/api/v1/activities/" + done.ID.String()},
		{"GET", "/api/v1/activities/" + missing},
		{"GET", "/api/v1/activities/" + done.ID.String() + "/log"},
		{"GET", "/api/v1/jobs"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		rec := httptest.NewRecorder()
		a.router.ServeHTTP(rec, req)

		route, params, err := rt.FindRoute(req)
		if err != nil {
			t.Errorf("%s %s: not in spec: %v", c.method, c.path, err)
			continue
		}
		err = openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{
			RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route},
			Status:                 rec.Code,
			Header:                 rec.Header(),
			Body:                   io.NopCloser(bytes.NewReader(rec.Body.Bytes())),
			Options:                &openapi3filter.Options{IncludeResponseStatus: true},
		})
		if err != nil {
			t.Errorf("%s %s -> %d violates the spec: %v\nbody: %s", c.method, c.path, rec.Code, err, rec.Body.String())
		}
	}
}