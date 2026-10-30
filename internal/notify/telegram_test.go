package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/config"
)

func newTG(url string, logs io.Writer) *Telegram {
	tg := NewTelegram(config.TelegramConfig{BotToken: "123:SECRET", ChatID: "42"}, time.UTC,
		slog.New(slog.NewTextHandler(logs, nil)))
	tg.baseURL = url
	tg.backoff = []time.Duration{time.Millisecond, time.Millisecond}
	return tg
}

func TestSendSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot123:SECRET/sendMessage" {
			t.Errorf("path %s", r.URL.Path)
		}
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		if _, ok := m["parse_mode"]; ok || m["chat_id"] != "42" || m["disable_web_page_preview"] != true {
			t.Errorf("body %v", m)
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	if err := newTG(srv.URL, io.Discard).Send(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
}

func TestRetries(t *testing.T) {
	cases := []struct {
		name      string
		first     func(http.ResponseWriter)
		wantErr   bool
		wantCalls int32
	}{
		{"500 then success", func(w http.ResponseWriter) { w.WriteHeader(500) }, false, 2},
		{"429 retry_after then success", func(w http.ResponseWriter) {
			w.WriteHeader(429)
			w.Write([]byte(`{"ok":false,"parameters":{"retry_after":1}}`))
		}, false, 2},
		{"400 no retry", func(w http.ResponseWriter) {
			w.WriteHeader(400)
			w.Write([]byte(`{"ok":false,"description":"Bad Request: chat not found"}`))
		}, true, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					tc.first(w)
					return
				}
				w.Write([]byte(`{"ok":true}`))
			}))
			defer srv.Close()
			err := newTG(srv.URL, io.Discard).Send(context.Background(), "x")
			if (err != nil) != tc.wantErr || calls.Load() != tc.wantCalls {
				t.Fatalf("err=%v calls=%d", err, calls.Load())
			}
		})
	}
}

func TestTokenNeverLeaks(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // connection refused: net/http embeds the URL in the error

	var logs bytes.Buffer
	err := newTG(url, &logs).Send(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "SECRET") || strings.Contains(logs.String(), "SECRET") {
		t.Fatalf("token leaked: err=%v logs=%s", err, logs.String())
	}
}