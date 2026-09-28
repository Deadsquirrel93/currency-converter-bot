package telegram

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"currency-converter-bot/internal/config"
	"currency-converter-bot/internal/rates"
)

func TestSubscriptionsFireInConfiguredTimezone(t *testing.T) {
	cbr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<ValCurs><Valute><CharCode>USD</CharCode><Nominal>1</Nominal><Name>USD</Name><Value>90,5</Value></Valute></ValCurs>`))
	}))
	defer cbr.Close()

	var mu sync.Mutex
	var sentTo []int64
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			ChatID int64 `json:"chat_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		mu.Lock()
		sentTo = append(sentTo, payload.ChatID)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer tg.Close()
	sent := func() []int64 {
		mu.Lock()
		defer mu.Unlock()
		return append([]int64(nil), sentTo...)
	}

	cfg := config.Config{
		TelegramToken:        testToken,
		TelegramAPI:          tg.URL,
		DefaultFrom:          "USD",
		DefaultTo:            "RUB",
		SubscriptionTimezone: "Asia/Tashkent",
	}
	provider := rates.NewProvider(cbr.URL, t.TempDir()+"/rates.json", time.Hour)
	bot := New(cfg, provider, slog.New(slog.DiscardHandler))
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
