package telegram

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"currency-converter-bot/internal/config"
	"currency-converter-bot/internal/rates"
)

type sentMessage struct {
	ChatID int64  `json:"chat_id"`
	Text   string `json:"text"`
}

// newTestBotWithServers returns a bot wired to fake CBR and Telegram servers
// and a function listing the messages sent so far.
func newTestBotWithServers(t *testing.T) (*Bot, func() []sentMessage) {
	t.Helper()
	cbr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<ValCurs><Valute><CharCode>USD</CharCode><Nominal>1</Nominal><Name>USD</Name><Value>90,5</Value></Valute></ValCurs>`))
	}))
	t.Cleanup(cbr.Close)

	var mu sync.Mutex
	var messages []sentMessage
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload sentMessage
		_ = json.NewDecoder(r.Body).Decode(&payload)
		mu.Lock()
		messages = append(messages, payload)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(tg.Close)

	cfg := config.Config{
		TelegramToken:        testToken,
		TelegramAPI:          tg.URL,
		DefaultFrom:          "USD",
		DefaultTo:            "RUB",
		SubscriptionTimezone: "Asia/Tashkent",
	}
	provider := rates.NewProvider(cbr.URL, t.TempDir()+"/rates.json", time.Hour)
	bot := New(cfg, provider, slog.New(slog.DiscardHandler))
	return bot, func() []sentMessage {
		mu.Lock()
		defer mu.Unlock()
		return append([]sentMessage(nil), messages...)
	}
}

func chatIDs(messages []sentMessage) []int64 {
	ids := make([]int64, 0, len(messages))
	for _, m := range messages {
		ids = append(ids, m.ChatID)
	}
	return ids
}

func TestSubscriptionsFireInConfiguredTimezone(t *testing.T) {
	bot, messages := newTestBotWithServers(t)
	sent := func() []int64 { return chatIDs(messages()) }
	if got := bot.subscriptionLocation.String(); got != "Asia/Tashkent" {
		t.Fatalf("subscription location = %q, want Asia/Tashkent", got)
	}

	bot.subscriptions = map[int64]dailySubscription{
		// 09:00 in Tashkent is 04:00 UTC.
		1: {ChatID: 100, From: "USD", To: "RUB", Time: "09:00"},
		// 00:30 in Tashkent on May 11 is still May 10 in UTC.
		2: {ChatID: 200, From: "USD", To: "RUB", Time: "00:30", LastSentDate: "2026-05-10"},
	}

	bot.sendDueSubscriptions(t.Context(), time.Date(2026, 5, 10, 3, 59, 0, 0, time.UTC))
	if got := sent(); len(got) != 0 {
		t.Fatalf("08:59 Tashkent: sent to %v, want nothing", got)
	}

	bot.sendDueSubscriptions(t.Context(), time.Date(2026, 5, 10, 4, 0, 0, 0, time.UTC))
	if got := sent(); len(got) != 1 || got[0] != 100 {
		t.Fatalf("09:00 Tashkent: sent to %v, want [100]", got)
	}
	if sub, _ := bot.getUserSubscription(1); sub.LastSentDate != "2026-05-10" {
		t.Fatalf("LastSentDate = %q, want 2026-05-10", sub.LastSentDate)
	}

	bot.sendDueSubscriptions(t.Context(), time.Date(2026, 5, 10, 19, 30, 0, 0, time.UTC))
	if got := sent(); len(got) != 2 || got[1] != 200 {
		t.Fatalf("00:30 Tashkent May 11: sent to %v, want [100 200]", got)
	}
	if sub, _ := bot.getUserSubscription(2); sub.LastSentDate != "2026-05-11" {
		t.Fatalf("LastSentDate = %q, want Tashkent date 2026-05-11", sub.LastSentDate)
	}
}

func TestSubscribeWithPassedTimeSendsRateNow(t *testing.T) {
	bot, messages := newTestBotWithServers(t)
	bot.saveLanguage(42, languageRussian)

	// 00:00 has always passed, so the first daily message would only come tomorrow.
	bot.setSubscription(t.Context(), 100, 42, "/subscribe 00:00 USD RUB")

	got := messages()
	if len(got) != 2 {
		t.Fatalf("sent %d messages, want confirmation and current rate: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Text, "следующий — завтра") {
		t.Fatalf("confirmation = %q, want note about tomorrow", got[0].Text)
	}
	if !strings.Contains(got[1].Text, "1 USD = 90,50 RUB") {
		t.Fatalf("second message = %q, want current rate", got[1].Text)
	}
	sub, ok := bot.getUserSubscription(42)
	if !ok || sub.LastSentDate != subscriptionDate(time.Now().In(bot.subscriptionLocation)) {
		t.Fatalf("subscription = %+v, want LastSentDate today so the scheduler does not repeat it", sub)
	}
}

func TestDeleteSettingsRemovesSubscription(t *testing.T) {
	bot, _ := newTestBotWithServers(t)
	bot.saveLanguage(42, languageEnglish)
	bot.setUserSubscription(42, dailySubscription{ChatID: 100, From: "USD", To: "RUB", Time: "09:00"})

	bot.deleteSettings(t.Context(), 100, 42)

	if _, ok := bot.getUserSubscription(42); ok {
		t.Fatal("/delete must remove the subscription")
	}
	if got := bot.getSession(42).Language; got != "" {
		t.Fatalf("language = %q, want it removed", got)
	}
}
