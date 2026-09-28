package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"currency-converter-bot/internal/config"
	"currency-converter-bot/internal/rates"
)

type apiCall struct {
	Method  string
	Payload map[string]any
}

// fakeTelegram records calls; reply decides the HTTP status and body per call.
type fakeTelegram struct {
	mu    sync.Mutex
	calls []apiCall
	reply func(call apiCall, n int) (int, string)
}

func newFakeTelegram(t *testing.T, reply func(call apiCall, n int) (int, string)) (*fakeTelegram, string) {
	t.Helper()
	fake := &fakeTelegram{reply: reply}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := apiCall{Method: r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]}
		_ = json.NewDecoder(r.Body).Decode(&call.Payload)
		fake.mu.Lock()
		fake.calls = append(fake.calls, call)
		n := len(fake.calls)
		fake.mu.Unlock()
		status, body := http.StatusOK, `{"ok":true}`
		if fake.reply != nil {
			status, body = fake.reply(call, n)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return fake, server.URL
}

func (f *fakeTelegram) methodCalls(method string) []apiCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []apiCall
	for _, call := range f.calls {
		if call.Method == method {
			result = append(result, call)
		}
	}
	return result
}

func newCBRServer(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<ValCurs><Valute><CharCode>USD</CharCode><Nominal>1</Nominal><Name>USD</Name><Value>90,5</Value></Valute></ValCurs>`))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func newHardeningBot(t *testing.T, cfg config.Config, telegramURL string) *Bot {
	t.Helper()
	cfg.TelegramToken = "42:" + testToken
	cfg.TelegramAPI = telegramURL
	cfg.DefaultFrom, cfg.DefaultTo = "USD", "RUB"
	cfg.SubscriptionTimezone = "Asia/Tashkent"
	provider := rates.NewProvider(newCBRServer(t), t.TempDir()+"/rates.json", time.Hour)
	return New(cfg, provider, slog.New(slog.DiscardHandler))
}

func privateMessage(userID int64, text string) update {
	return update{Message: &message{From: &user{ID: userID}, Chat: chat{ID: userID, Type: "private"}, Text: text}}
}

func groupMessage(userID int64, text string) update {
	return update{Message: &message{From: &user{ID: userID}, Chat: chat{ID: -100, Type: "supergroup"}, Text: text}}
}

func TestSubscriptionRemovedWhenChatUnreachable(t *testing.T) {
	fake, url := newFakeTelegram(t, func(call apiCall, n int) (int, string) {
		return http.StatusForbidden, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`
	})
	bot := newHardeningBot(t, config.Config{SubscriptionsFile: filepath.Join(t.TempDir(), "subs.json")}, url)
	bot.setUserSubscription(1, dailySubscription{ChatID: 1, From: "USD", To: "RUB", Time: "09:00"})

	bot.sendDueSubscriptions(t.Context(), time.Date(2026, 5, 10, 5, 0, 0, 0, time.UTC))

	if _, ok := bot.getUserSubscription(1); ok {
		t.Fatal("subscription to a chat that blocked the bot must be removed")
	}
	bot.sendDueSubscriptions(t.Context(), time.Date(2026, 5, 10, 5, 1, 0, 0, time.UTC))
	if got := len(fake.methodCalls("sendMessage")); got != 1 {
		t.Fatalf("sendMessage calls = %d, want 1 (no retries every minute)", got)
	}
}

func TestSubscriptionBacksOffOnTransientErrors(t *testing.T) {
	fake, url := newFakeTelegram(t, func(call apiCall, n int) (int, string) {
		return http.StatusInternalServerError, `{"ok":false,"error_code":500,"description":"Internal Server Error"}`
	})
	bot := newHardeningBot(t, config.Config{}, url)
	bot.subscriptions = map[int64]dailySubscription{1: {ChatID: 1, From: "USD", To: "RUB", Time: "09:00"}}

	start := time.Date(2026, 5, 10, 4, 0, 0, 0, time.UTC) // 09:00 Tashkent
	for minute := 0; minute < 4*60; minute++ {
		bot.sendDueSubscriptions(t.Context(), start.Add(time.Duration(minute)*time.Minute))
	}

	// First try + retries after 5, 15 and 45 minutes, then give up for the day.
	if got := len(fake.methodCalls("sendMessage")); got != 4 {
		t.Fatalf("sendMessage calls over 4 hours = %d, want 4", got)
	}
	sub, ok := bot.getUserSubscription(1)
	if !ok || sub.LastSentDate != "2026-05-10" {
		t.Fatalf("subscription = %+v, want kept and skipped until tomorrow", sub)
	}
}

func TestDisallowedUserDoesNotReceiveSubscription(t *testing.T) {
	bot := New(config.Config{AdminUsers: map[int64]struct{}{1: {}}, SubscriptionTimezone: "UTC"}, nil, slog.New(slog.DiscardHandler))
	bot.addAllowedUserIDs([]int64{2})
	bot.subscriptions = map[int64]dailySubscription{2: {ChatID: 2, From: "USD", To: "RUB", Time: "09:00"}}
	now := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)

	if _, ok := bot.dueSubscriptions(now)[2]; !ok {
		t.Fatal("allowed user's subscription must be due")
	}
	bot.removeAllowedUserIDs([]int64{2})
	if _, ok := bot.dueSubscriptions(now)[2]; ok {
		t.Fatal("subscription must stop after /disallow")
	}
}

func TestSubscriptionsFileMatchesMemoryAfterConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subs.json")
	bot := New(config.Config{SubscriptionsFile: path}, nil, slog.New(slog.DiscardHandler))

	var wg sync.WaitGroup
	for i := range 50 {
		userID := int64(i%5 + 1)
		wg.Go(func() {
			bot.setUserSubscription(userID, dailySubscription{ChatID: userID, From: "USD", To: "RUB", Time: "09:00"})
		})
		wg.Go(func() {
			bot.markSubscriptionSent(userID, dailySubscription{ChatID: userID, From: "USD", To: "RUB", Time: "09:00"}, fmt.Sprintf("2026-05-%02d", i%28+1))
		})
		if i%7 == 0 {
			wg.Go(func() { bot.removeUserSubscription(userID) })
		}
	}
	wg.Wait()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk map[int64]dailySubscription
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("subscriptions file is corrupted: %v", err)
	}
	if fmt.Sprint(onDisk) != fmt.Sprint(bot.subscriptions) {
		t.Fatalf("file state differs from memory:\nfile:   %v\nmemory: %v", onDisk, bot.subscriptions)
	}
}

func TestCorruptedSettingsFileIsQuarantinedNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user_settings.json")
	if err := os.WriteFile(path, []byte(`{"1": {"from": "USD"`), 0o600); err != nil {
		t.Fatal(err)
	}
	bot := New(config.Config{UserSettingsFile: path, DefaultFrom: "USD", DefaultTo: "RUB"}, nil, slog.New(slog.DiscardHandler))
	bot.saveLanguage(2, languageEnglish)

	matches, _ := filepath.Glob(path + ".corrupt-*")
	if len(matches) != 1 {
		t.Fatalf("corrupted file must be kept aside, found %v", matches)
	}
	if raw, _ := os.ReadFile(matches[0]); string(raw) != `{"1": {"from": "USD"` {
		t.Fatalf("quarantined content changed: %q", raw)
	}
}

func TestStoredFilesArePrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	path := filepath.Join(dir, "user_settings.json")
	bot := New(config.Config{UserSettingsFile: path, DefaultFrom: "USD", DefaultTo: "RUB"}, nil, slog.New(slog.DiscardHandler))
	bot.saveLanguage(1, languageRussian)

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("settings file perm = %v, want 0600", perm)
	}
	dirInfo, _ := os.Stat(dir)
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("data dir perm = %v, want 0700", perm)
	}
}

func TestUserLimiter(t *testing.T) {
	limiter := newUserLimiter(3, time.Minute)
	now := time.Now()
	for i := range 3 {
		if ok, _ := limiter.allow(1, now); !ok {
			t.Fatalf("event %d must be allowed", i+1)
		}
	}
	if ok, first := limiter.allow(1, now); ok || !first {
		t.Fatalf("4th event: allowed=%v first=%v, want rejected and first", ok, first)
	}
	if ok, first := limiter.allow(1, now); ok || first {
		t.Fatalf("5th event: allowed=%v first=%v, want rejected silently", ok, first)
	}
	if ok, _ := limiter.allow(2, now); !ok {
		t.Fatal("other users are limited separately")
	}
	if ok, _ := limiter.allow(1, now.Add(time.Minute)); !ok {
		t.Fatal("limit must reset after the window")
	}
}

func TestFloodIsCutOffWithOneWarning(t *testing.T) {
	fake, url := newFakeTelegram(t, nil)
	bot := newHardeningBot(t, config.Config{}, url)
	bot.saveLanguage(1, languageRussian)

	for range updatesPerMinute + 10 {
		bot.handleUpdate(t.Context(), privateMessage(1, "/help"))
	}

	calls := fake.methodCalls("sendMessage")
	if len(calls) != updatesPerMinute+1 {
		t.Fatalf("sendMessage calls = %d, want %d answers + 1 warning", len(calls), updatesPerMinute)
	}
	if text := calls[len(calls)-1].Payload["text"]; !strings.Contains(fmt.Sprint(text), "Слишком много запросов") {
		t.Fatalf("last message = %v, want rate limit warning", text)
	}
}

func TestBlockedUserIsAnsweredOncePerIntervalAndIgnoredInGroups(t *testing.T) {
	fake, url := newFakeTelegram(t, nil)
	bot := newHardeningBot(t, config.Config{AdminUsers: map[int64]struct{}{1: {}}}, url)

	for range 5 {
		bot.handleUpdate(t.Context(), privateMessage(7, "100"))
	}
	bot.handleUpdate(t.Context(), groupMessage(7, "/rate"))

	if got := len(fake.methodCalls("sendMessage")); got != 1 {
		t.Fatalf("sendMessage calls = %d, want a single access notice", got)
	}
}

func TestMessageTextInGroups(t *testing.T) {
	bot := New(config.Config{TelegramToken: "42:" + testToken}, nil, slog.New(slog.DiscardHandler))
	bot.botUsername = "RatesBot"
	group := chat{ID: -100, Type: "group"}
	tests := []struct {
		msg      message
		wantText string
		wantOK   bool
	}{
		{message{Chat: chat{ID: 1, Type: "private"}, Text: "100 usd"}, "100 usd", true},
		{message{Chat: group, Text: "100 usd"}, "", false},
		{message{Chat: group, Text: "/rate usd"}, "/rate usd", true},
		{message{Chat: group, Text: "/rate@ratesbot usd"}, "/rate@ratesbot usd", true},
		{message{Chat: group, Text: "/rate@OtherBot usd"}, "", false},
		{message{Chat: group, Text: "@RatesBot 100 usd"}, "100 usd", true},
		{message{Chat: group, Text: "100 usd", ReplyToMessage: &message{From: &user{ID: 42}}}, "100 usd", true},
		{message{Chat: group, Text: "100 usd", ReplyToMessage: &message{From: &user{ID: 7}}}, "", false},
	}
	for _, tt := range tests {
		text, ok := bot.messageText(&tt.msg)
		if text != tt.wantText || ok != tt.wantOK {
			t.Fatalf("messageText(%q in %s) = %q, %v; want %q, %v", tt.msg.Text, tt.msg.Chat.Type, text, ok, tt.wantText, tt.wantOK)
		}
	}
}

func TestPostRetriesOnceAfter429AndReturnsAPIError(t *testing.T) {
	fake, url := newFakeTelegram(t, func(call apiCall, n int) (int, string) {
		if n == 1 {
			return http.StatusTooManyRequests, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 1","parameters":{"retry_after":1}}`
		}
		return http.StatusBadRequest, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`
	})
	bot := newHardeningBot(t, config.Config{}, url)

	err := bot.sendMessage(t.Context(), 5, "hi")
	if got := len(fake.methodCalls("sendMessage")); got != 2 {
		t.Fatalf("sendMessage attempts = %d, want 2", got)
	}
	if !isChatUnreachable(err) {
		t.Fatalf("error = %v, want chat unreachable", err)
	}
	if !strings.Contains(err.Error(), "chat not found") || strings.Contains(err.Error(), testToken) {
		t.Fatalf("error = %q, want Telegram description without token", err)
	}
}

func TestAdminCommandsHiddenFromRegularUsers(t *testing.T) {
	fake, url := newFakeTelegram(t, nil)
	bot := newHardeningBot(t, config.Config{AdminUsers: map[int64]struct{}{1: {}}}, url)
	if err := bot.setBotCommands(t.Context()); err != nil {
		t.Fatal(err)
	}

	for _, call := range fake.methodCalls("setMyCommands") {
		commands := fmt.Sprint(call.Payload["commands"])
		_, scoped := call.Payload["scope"]
		if hasAdmin := strings.Contains(commands, "disallow"); hasAdmin != scoped {
			t.Fatalf("setMyCommands scope=%v has admin commands=%v: %v", scoped, hasAdmin, call.Payload)
		}
	}
	if got := len(fake.methodCalls("setMyCommands")); got != 6 {
		t.Fatalf("setMyCommands calls = %d, want 3 default + 3 for the admin", got)
	}
}

func TestRetryDelayGrowsAndHonorsRetryAfter(t *testing.T) {
	want := []time.Duration{3 * time.Second, 6 * time.Second, 12 * time.Second, 24 * time.Second, 48 * time.Second, time.Minute, time.Minute}
	for i, delay := range want {
		if got := retryDelay(i+1, context.DeadlineExceeded); got != delay {
			t.Fatalf("retryDelay(%d) = %v, want %v", i+1, got, delay)
		}
	}
	if got := retryDelay(1, &apiError{StatusCode: 429, RetryAfter: 90 * time.Second}); got != 90*time.Second {
		t.Fatalf("retryDelay with retry_after = %v, want 90s", got)
	}
}

func TestInputLimits(t *testing.T) {
	for _, raw := range []string{"-100", "-150", "1001"} {
		if _, err := parseModifierPercent(raw); err == nil {
			t.Fatalf("parseModifierPercent(%q) must fail", raw)
		}
	}
	if _, err := parseMultiplier("1e10"); err == nil {
		t.Fatal("parseMultiplier(1e10) must fail")
	}
	if _, err := parseWithCallbackData("w|USD|RUB|1e300|1|0"); err == nil {
		t.Fatal("forged callback amount must be rejected")
	}
}

func TestErrorTextIsLocalized(t *testing.T) {
	_, err := parseSubscription("/subscribe 25:00", session{})
	if got := errorText(err, languageEnglish); !strings.Contains(got, "HH:MM") || strings.ContainsAny(got, "абвгдеж") {
		t.Fatalf("english error = %q", got)
	}
	if got := errorText(err, languageRussian); !strings.Contains(got, "Время должно быть") {
		t.Fatalf("russian error = %q", got)
	}
	_, convErr := rates.Convert(1, "BGN", "RUB", rates.Snapshot{Rates: map[string]rates.Rate{"RUB": {Code: "RUB", Nominal: 1, Value: 1}}})
	if got := errorText(convErr, languageRussian); got != "Нет курса ЦБ РФ для BGN" {
		t.Fatalf("unknown currency text = %q", got)
	}
}

func TestAmountErrorText(t *testing.T) {
	_, err := parseConversionInput("2 кофе по 350", session{Language: languageRussian})
	if got := amountErrorText(err, languageRussian); !strings.Contains(got, "2 x 350") {
		t.Fatalf("ambiguous amount text = %q, want multiplication hint", got)
	}
}

func TestCommandRoutingEndToEnd(t *testing.T) {
	fake, url := newFakeTelegram(t, nil)
	bot := newHardeningBot(t, config.Config{AdminUsers: map[int64]struct{}{1: {}}}, url)
	bot.saveLanguage(1, languageEnglish)

	steps := []struct {
		userID int64
		text   string
		want   string
	}{
		{1, "/allow 5", "Added: 5"},
		{1, "/allowed", "Added by commands: 5"},
		{5, "/start", "Choose your language"},
		{5, "/lang en", "Language changed to English"},
		{5, "/from EUR", "Done: EUR -> RUB"},
		{5, "/swap", "Done: RUB -> EUR"},
		{5, "/whoami", "Access: allowed"},
		{5, "/allow 6", "only to an administrator"},
		{5, "/subscribe 25:00", "HH:MM"},
		{5, "/list", "USD"},
		{5, "/delete", "Your data has been deleted"},
		{1, "/disallow 5", "Removed from runtime whitelist: 5"},
		{5, "/help", "Bot access is restricted"},
	}
	for _, step := range steps {
		before := len(fake.methodCalls("sendMessage"))
		bot.handleUpdate(t.Context(), privateMessage(step.userID, step.text))
		calls := fake.methodCalls("sendMessage")
		if len(calls) != before+1 {
			t.Fatalf("%d %q: sent %d messages, want 1", step.userID, step.text, len(calls)-before)
		}
		if text := fmt.Sprint(calls[len(calls)-1].Payload["text"]); !strings.Contains(text, step.want) {
			t.Fatalf("%d %q: reply = %q, want it to contain %q", step.userID, step.text, text, step.want)
		}
	}
}
