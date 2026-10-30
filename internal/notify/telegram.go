package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/config"
	"github.com/banshee-lab/pgkeeper/internal/domain"
)

const maxRetryAfter = 30 * time.Second

type Telegram struct {
	token, chatID string
	client        *http.Client
	baseURL       string // overridable in tests
	loc           *time.Location
	backoff       []time.Duration // waits between attempts; len+1 = attempts
	logger        *slog.Logger
}

var _ domain.Notifier = (*Telegram)(nil)

func NewTelegram(cfg config.TelegramConfig, loc *time.Location, logger *slog.Logger) *Telegram {
	if loc == nil {
		loc = time.Local
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Telegram{
		token:   cfg.BotToken.Reveal(),
		chatID:  cfg.ChatID,
		client:  &http.Client{Timeout: 10 * time.Second},
		baseURL: "https://api.telegram.org",
		loc:     loc,
		backoff: []time.Duration{time.Second, 3 * time.Second},
		logger:  logger,
	}
}

func (t *Telegram) NotifyFailure(ctx context.Context, e domain.NotifyEvent) error {
	return t.Send(ctx, formatFailure(e, t.loc))
}

type sendMessageRequest struct {
	ChatID                string `json:"chat_id"`
	Text                  string `json:"text"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview"`
}

// Send posts plain text (no parse_mode) with retries on network errors, 429, 5xx.
func (t *Telegram) Send(ctx context.Context, text string) error {
	body, err := json.Marshal(sendMessageRequest{ChatID: t.chatID, Text: text, DisableWebPagePreview: true})
	if err != nil {
		return fmt.Errorf("telegram sendMessage failed: %w", err)
	}
	attempts := len(t.backoff) + 1
	var lastErr error
	for i := 0; i < attempts; i++ {
		retry, wait, err := t.post(ctx, body)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retry || i == attempts-1 {
			break
		}
		if wait <= 0 {
			wait = t.backoff[i]
		}
		t.logger.Warn("telegram send failed, retrying", "attempt", i+1, "wait", wait.String(), "error", err.Error())
		if serr := sleep(ctx, wait); serr != nil {
			return fmt.Errorf("telegram sendMessage aborted: %w", serr)
		}
	}
	return lastErr
}

func (t *Telegram) post(ctx context.Context, body []byte) (retry bool, wait time.Duration, err error) {
	url := t.baseURL + "/bot" + t.token + "/sendMessage" // never log this
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false, 0, t.wrap(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return ctx.Err() == nil, 0, t.wrap(err) // retry network errors unless cancelled
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusOK {
		return false, 0, nil
	}

	var apiResp struct {
		Description string `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	_ = json.Unmarshal(data, &apiResp)
	err = t.wrap(fmt.Errorf("HTTP %d: %s", resp.StatusCode, apiResp.Description))

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		w := min(time.Duration(apiResp.Parameters.RetryAfter)*time.Second, maxRetryAfter)
		return true, w, err
	case resp.StatusCode >= 500:
		return true, 0, err
	}
	return false, 0, err // other 4xx: fail immediately
}

// wrap flattens the error to a string with the token removed (net/http errors embed the URL).
func (t *Telegram) wrap(err error) error {
	return fmt.Errorf("telegram sendMessage failed: %s", redact(err.Error(), t.token))
}

func redact(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "***")
}

func sleep(ctx context.Context, d time.Duration) error {
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-tm.C:
		return nil
	}
}