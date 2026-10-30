package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestSecretNeverLeaks(t *testing.T) {
	s := Secret("hunter2")
	if got := fmt.Sprintf("%v %s %+v", s, s, s); strings.Contains(got, "hunter2") {
		t.Fatal(got)
	}
	b, _ := json.Marshal(struct{ P Secret }{s})
	if strings.Contains(string(b), "hunter2") {
		t.Fatal(string(b))
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("x", "p", s)
	if strings.Contains(buf.String(), "hunter2") {
		t.Fatal(buf.String())
	}
}