package api

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"
)

func TestRoutesMatchOpenAPI(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile("../../docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	spec := map[string]bool{}
	for path, item := range doc.Paths.Map() {
		for method := range item.Operations() {
			spec[method+" "+path] = true
		}
	}

	router := NewRouter(NewHandler(Deps{DB: okPinger{}, Logger: slog.Default()}))
	routes := map[string]bool{}
	err = chi.Walk(router.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[method+" "+route] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for k := range routes {
		if !spec[k] {
			t.Errorf("route not in spec: %s", k)
		}
	}
	for k := range spec {
		if !routes[k] {
			t.Errorf("spec operation not registered: %s", k)
		}
	}
}