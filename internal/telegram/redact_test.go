package telegram

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"currency-converter-bot/internal/config"
)

const testToken = "fake-token-for-tests"

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestPostErrorDoesNotContainToken(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer server.Close()
	defer close(release)

	bot := New(config.Config{TelegramToken: testToken, TelegramAPI: server.URL}, nil, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := bot.post(ctx, "getUpdates", map[string]any{}, nil)
	if err == nil {
		t.Fatal("post() error = nil, want timeout error")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("post() error leaks token: %v", err)
	}
	if !strings.Contains(err.Error(), "/bot***/getUpdates") {
		t.Fatalf("post() error = %q, want redacted URL", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("post() error = %v, must still match context.DeadlineExceeded", err)
	}
}

func TestRunLogsDoNotContainToken(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	unreachable := server.URL
	server.Close()

	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	bot := New(config.Config{TelegramToken: testToken, TelegramAPI: unreachable}, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bot.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), "get updates failed") {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("no get updates failure logged, logs:\n%s", logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	out := logs.String()
	if strings.Contains(out, testToken) {
		t.Fatalf("logs leak token:\n%s", out)
	}
	if !strings.Contains(out, "bot***") {
		t.Fatalf("logs must contain redacted URL, got:\n%s", out)
	}
}

func TestRedactSecret(t *testing.T) {
	if got := redactSecret(nil, testToken); got != nil {
		t.Fatalf("redactSecret(nil) = %v, want nil", got)
	}
	plain := errors.New("plain")
	if got := redactSecret(plain, testToken); got != plain {
		t.Fatalf("redactSecret must return errors without the secret unchanged")
	}
	if got := redactSecret(plain, ""); got != plain {
		t.Fatalf("redactSecret with empty secret must return err unchanged")
	}

	wrapped := redactSecret(errors.Join(context.Canceled, errors.New("token "+testToken)), testToken)
	if strings.Contains(wrapped.Error(), testToken) {
		t.Fatalf("redactSecret() = %q, leaks token", wrapped)
	}
	if !errors.Is(wrapped, context.Canceled) {
		t.Fatal("redactSecret() must keep context.Canceled matchable")
	}
}
