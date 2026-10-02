package telegram

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"currency-converter-bot/internal/config"
	"currency-converter-bot/internal/rates"
)

// fakeCBR serves a latest document that tests can replace (a new publication)
// and documents for date_req requests.
type fakeCBR struct {
	mu          sync.Mutex
	latestDate  string // "29.09.2026"
	latestUSD   string // "81,5000"
	byDate      map[string]string
	latestCalls int
}

func newFakeCBR(t *testing.T, latestDate, latestUSD string) (*fakeCBR, string) {
	t.Helper()
	cbr := &fakeCBR{latestDate: latestDate, latestUSD: latestUSD, byDate: map[string]string{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cbr.mu.Lock()
		date, usd := cbr.latestDate, cbr.latestUSD
		if raw := r.URL.Query().Get("date_req"); raw != "" {
			parsed, _ := time.Parse("02/01/2006", raw)
			date, usd = parsed.Format("02.01.2006"), "80,0000"
			if value, ok := cbr.byDate[raw]; ok {
				usd = value
			}
		} else {
			cbr.latestCalls++
		}
		cbr.mu.Unlock()
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<ValCurs Date="%s" name="Foreign Currency Market"><Valute><CharCode>USD</CharCode><Nominal>1</Nominal><Name>USD</Name><Value>%s</Value></Valute><Valute><CharCode>EUR</CharCode><Nominal>1</Nominal><Name>EUR</Name><Value>95,0000</Value></Valute></ValCurs>`, date, usd)
	}))
	t.Cleanup(server.Close)
	return cbr, server.URL
}

func (c *fakeCBR) publish(date, usd string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.latestDate, c.latestUSD = date, usd
}

func (c *fakeCBR) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latestCalls
}

func newNewRateBot(t *testing.T, cbrURL, telegramURL, subsFile string, cfg config.Config) *Bot {
	t.Helper()
	cfg.TelegramToken = "42:" + testToken
	cfg.TelegramAPI = telegramURL
	cfg.DefaultFrom, cfg.DefaultTo = "USD", "RUB"
	cfg.Location = mustLocation(t, "Asia/Tashkent")
	cfg.NewRateSubsFile = subsFile
	provider := rates.NewProvider(cbrURL, t.TempDir()+"/rates.json", time.Hour)
	return New(cfg, provider, slog.New(slog.DiscardHandler))
}

func sentTexts(fake *fakeTelegram) []string {
	var texts []string
	for _, call := range fake.methodCalls("sendMessage") {
		texts = append(texts, fmt.Sprint(call.Payload["text"]))
	}
	return texts
}

func msk(day, hour, minute int) time.Time {
	return time.Date(2026, 9, day, hour, minute, 0, 0, moscowTime)
}

func TestNextRateCheck(t *testing.T) {
	tests := []struct {
		now, want time.Time
	}{
		{msk(28, 10, 0), msk(28, 11, 0)},
		{msk(28, 15, 0), msk(28, 15, 30)}, // the window starts before the hourly check
		{msk(28, 15, 30), msk(28, 15, 40)},
		{msk(28, 19, 55), msk(28, 20, 5)},
		{msk(28, 20, 0), msk(28, 21, 0)},
		{msk(28, 23, 30), msk(29, 0, 30)},
	}
	for _, tt := range tests {
		if got := nextRateCheck(tt.now); !got.Equal(tt.want) {
			t.Fatalf("nextRateCheck(%s) = %s, want %s", tt.now.Format("15:04"), got.In(moscowTime).Format("02 15:04"), tt.want.Format("02 15:04"))
		}
	}
}

func TestNewRateIsAnnouncedOncePerDate(t *testing.T) {
	cbr, cbrURL := newFakeCBR(t, "26.09.2026", "81,0000")
	cbr.byDate["28/09/2026"] = "81,0000" // in force on Monday, set on Saturday
	fake, url := newFakeTelegram(t, nil)
	subsFile := filepath.Join(t.TempDir(), "new_rate.json")
	bot := newNewRateBot(t, cbrURL, url, subsFile, config.Config{})
	bot.saveLanguage(1, languageRussian)
	bot.setNewRateSubscriptionState(1, newRateSubscription{ChatID: 100, From: "USD", To: "RUB", LastRateDate: "2026-09-26"})

	for minute := 0; minute <= 60; minute++ {
		now := msk(28, 15, 0).Add(time.Duration(minute) * time.Minute)
		if minute == 25 {
			cbr.publish("29.09.2026", "81,5000")
		}
		bot.watchRates(t.Context(), now)
	}

	texts := sentTexts(fake)
	if len(texts) != 1 {
		t.Fatalf("sent %d messages, want exactly one: %q", len(texts), texts)
	}
	for _, want := range []string{"ЦБ установил курс USD -> RUB на 29.09.2026", "1 USD = 81,50 RUB", "К текущему курсу: +0,5 RUB (+0,62%)"} {
		if !strings.Contains(texts[0], want) {
			t.Fatalf("message = %q, want it to contain %q", texts[0], want)
		}
	}
	// 15:00, then the window: 15:30, 15:40, 15:50, 16:00.
	if got := cbr.calls(); got != 5 {
		t.Fatalf("latest rates requested %d times in an hour, want 5", got)
	}

	reloaded := newNewRateBot(t, cbrURL, url, subsFile, config.Config{})
	if sub, ok := reloaded.getNewRateSubscription(1); !ok || sub.LastRateDate != "2026-09-29" {
		t.Fatalf("stored subscription = %+v, want LastRateDate 2026-09-29", sub)
	}
}

func TestNewRateNotAnnouncedOnceInForce(t *testing.T) {
	cbr, cbrURL := newFakeCBR(t, "29.09.2026", "81,5000")
	fake, url := newFakeTelegram(t, nil)
	bot := newNewRateBot(t, cbrURL, url, "", config.Config{})
	bot.setNewRateSubscriptionState(1, newRateSubscription{ChatID: 100, From: "USD", To: "RUB", LastRateDate: "2026-09-26"})

	// A restart after midnight: the rates dated today are no longer news.
	bot.watchRates(t.Context(), msk(29, 0, 30))
	if got := len(sentTexts(fake)); got != 0 {
		t.Fatalf("sent %d messages for rates already in force, want 0", got)
	}
	if cbr.calls() != 1 {
		t.Fatalf("latest rates requested %d times, want 1", cbr.calls())
	}
}

func TestNewRateWithoutSubscribersDoesNotPollCBR(t *testing.T) {
	cbr, cbrURL := newFakeCBR(t, "29.09.2026", "81,5000")
	_, url := newFakeTelegram(t, nil)
	bot := newNewRateBot(t, cbrURL, url, "", config.Config{})
	for minute := 0; minute < 120; minute++ {
		bot.watchRates(t.Context(), msk(28, 15, 0).Add(time.Duration(minute)*time.Minute))
	}
	if got := cbr.calls(); got != 0 {
		t.Fatalf("CBR requested %d times without subscribers, want 0", got)
	}
}

func TestNewRateSubscriptionWithoutKnownDateIsInitializedSilently(t *testing.T) {
	_, cbrURL := newFakeCBR(t, "29.09.2026", "81,5000")
	fake, url := newFakeTelegram(t, nil)
	bot := newNewRateBot(t, cbrURL, url, "", config.Config{})
	bot.setNewRateSubscriptionState(1, newRateSubscription{ChatID: 100, From: "USD", To: "RUB"})

	bot.watchRates(t.Context(), msk(28, 16, 0))
	if got := len(sentTexts(fake)); got != 0 {
		t.Fatalf("sent %d messages, want 0", got)
	}
	if sub, _ := bot.getNewRateSubscription(1); sub.LastRateDate != "2026-09-29" {
		t.Fatalf("LastRateDate = %q, want 2026-09-29", sub.LastRateDate)
	}
}

func TestNewRateDeliveryErrors(t *testing.T) {
	t.Run("unreachable chat removes the subscription", func(t *testing.T) {
		_, cbrURL := newFakeCBR(t, "29.09.2026", "81,5000")
		fake, url := newFakeTelegram(t, func(call apiCall, n int) (int, string) {
			return http.StatusForbidden, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`
		})
		bot := newNewRateBot(t, cbrURL, url, "", config.Config{})
		bot.setNewRateSubscriptionState(1, newRateSubscription{ChatID: 100, From: "USD", To: "RUB", LastRateDate: "2026-09-26"})

		bot.watchRates(t.Context(), msk(28, 16, 0))
		bot.watchRates(t.Context(), msk(28, 16, 1))
		if _, ok := bot.getNewRateSubscription(1); ok {
			t.Fatal("subscription to a chat that blocked the bot must be removed")
		}
		if got := len(fake.methodCalls("sendMessage")); got != 1 {
			t.Fatalf("sendMessage calls = %d, want 1", got)
		}
	})

	t.Run("transient errors back off and give up on the date", func(t *testing.T) {
		_, cbrURL := newFakeCBR(t, "29.09.2026", "81,5000")
		fake, url := newFakeTelegram(t, func(call apiCall, n int) (int, string) {
			return http.StatusInternalServerError, `{"ok":false,"error_code":500,"description":"Internal Server Error"}`
		})
		bot := newNewRateBot(t, cbrURL, url, "", config.Config{})
		bot.setNewRateSubscriptionState(1, newRateSubscription{ChatID: 100, From: "USD", To: "RUB", LastRateDate: "2026-09-26"})

		for minute := 0; minute < 3*60; minute++ {
			bot.watchRates(t.Context(), msk(28, 16, 0).Add(time.Duration(minute)*time.Minute))
		}
		// First try + retries after 5, 15 and 45 minutes.
		if got := len(fake.methodCalls("sendMessage")); got != 4 {
			t.Fatalf("sendMessage calls = %d, want 4", got)
		}
		if sub, ok := bot.getNewRateSubscription(1); !ok || sub.LastRateDate != "2026-09-29" {
			t.Fatalf("subscription = %+v, want kept and marked for 2026-09-29", sub)
		}
	})
}

func TestNewRateSkipsDisallowedUsers(t *testing.T) {
	_, cbrURL := newFakeCBR(t, "29.09.2026", "81,5000")
	fake, url := newFakeTelegram(t, nil)
	bot := newNewRateBot(t, cbrURL, url, "", config.Config{AdminUsers: map[int64]struct{}{1: {}}})
	bot.setNewRateSubscriptionState(2, newRateSubscription{ChatID: 200, From: "USD", To: "RUB", LastRateDate: "2026-09-26"})

	bot.watchRates(t.Context(), msk(28, 16, 0))
	if got := len(sentTexts(fake)); got != 0 {
		t.Fatalf("sent %d messages to a user without access, want 0", got)
	}
	if _, ok := bot.getNewRateSubscription(2); !ok {
		t.Fatal("subscription must be kept while access is revoked")
	}
	bot.addAllowedUserIDs([]int64{2})
	bot.watchRates(t.Context(), msk(28, 16, 1))
	if got := len(sentTexts(fake)); got != 1 {
		t.Fatalf("sent %d messages after access was granted, want 1", got)
	}
}

func TestNewRateSubscriptionCommands(t *testing.T) {
	today := time.Now().In(moscowTime)
	cbr, cbrURL := newFakeCBR(t, today.Format("02.01.2006"), "81,0000")
	cbr.byDate[today.Format("02/01/2006")] = "81,0000"
	fake, url := newFakeTelegram(t, nil)
	bot := newNewRateBot(t, cbrURL, url, filepath.Join(t.TempDir(), "new_rate.json"), config.Config{})
	bot.botUsername = "rates_bot"
	bot.saveLanguage(1, languageRussian)

	steps := []struct {
		update update
		want   string
	}{
		{privateMessage(1, "/subscription"), "Подписки выключены"},
		{privateMessage(1, "/subscribe new XYZ RUB"), "Не знаю валюту XYZ"},
		{privateMessage(1, "/subscribe new USD RUB"), "как только ЦБ установит новый"},
		{privateMessage(1, "/subscribe 09:00 EUR RUB"), "каждый день в 09:00"},
		{privateMessage(1, "/subscription"), "Новый курс ЦБ сразу после установки:\nПара: USD -> RUB"},
		{privateMessage(1, "/unsubscribe new"), "Подписка на новый курс отключена"},
		{privateMessage(1, "/subscription"), "Ежедневная подписка"},
		{privateMessage(1, "/unsubscribe what"), "Укажите, что отключить"},
		{privateMessage(1, "/unsubscribe"), "Подписки отключены"},
		{privateMessage(1, "/help"), "/subscribe new [USD RUB]"},
	}
	for _, step := range steps {
		before := len(sentTexts(fake))
		bot.handleUpdate(t.Context(), step.update)
		replies := strings.Join(sentTexts(fake)[before:], "\n---\n")
		if !strings.Contains(replies, step.want) {
			t.Fatalf("%q: replies = %q, want them to contain %q", step.update.Message.Text, replies, step.want)
		}
	}
	if _, ok := bot.getUserSubscription(1); ok {
		t.Fatal("/unsubscribe must disable the daily subscription too")
	}

	// Tomorrow's rates are already out: subscribing sends them right away.
	cbr.publish(today.AddDate(0, 0, 1).Format("02.01.2006"), "81,5000")
	before := len(sentTexts(fake))
	bot.handleUpdate(t.Context(), groupMessage(1, "/subscribe@rates_bot new"))
	texts := sentTexts(fake)[before:]
	if len(texts) != 2 || !strings.Contains(texts[0], "уже установлен") || !strings.Contains(texts[1], "К текущему курсу: +0,5 RUB (+0,62%)") {
		t.Fatalf("messages = %q, want confirmation and the next rate", texts)
	}
	sub, ok := bot.getNewRateSubscription(1)
	if !ok || sub.ChatID != -100 || sub.LastRateDate != today.AddDate(0, 0, 1).Format("2006-01-02") {
		t.Fatalf("group subscription = %+v, want chat -100 and tomorrow marked as sent", sub)
	}
	// The scheduler must not repeat it.
	bot.watchRates(t.Context(), time.Now())
	if got := len(sentTexts(fake)); got != before+2 {
		t.Fatalf("scheduler sent %d more messages, want 0", got-before-2)
	}

	bot.handleUpdate(t.Context(), privateMessage(1, "/delete"))
	if _, ok := bot.getNewRateSubscription(1); ok {
		t.Fatal("/delete must remove the new rate subscription")
	}
}

func TestNewRateTextInEnglishAndWithoutPreviousRates(t *testing.T) {
	latest := rates.Snapshot{Date: "2026-09-29", Rates: map[string]rates.Rate{
		"RUB": {Code: "RUB", Nominal: 1, Value: 1},
		"USD": {Code: "USD", Nominal: 1, Value: 81.5},
	}}
	text, err := newRateText("USD", "RUB", latest, rates.Snapshot{}, languageEnglish)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"The Bank of Russia has set the USD -> RUB rate for 29.09.2026", "1 USD = 81,50 RUB", "Change from the current rate: no data"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text = %q, want it to contain %q", text, want)
		}
	}
	if _, err := newRateText("GBP", "RUB", latest, rates.Snapshot{}, languageEnglish); err == nil {
		t.Fatal("unknown currency must be an error")
	}
}

func TestRateReplyShowsRateDate(t *testing.T) {
	snapshot := rates.Snapshot{Date: "2026-09-29", Rates: map[string]rates.Rate{
		"RUB": {Code: "RUB", Nominal: 1, Value: 1},
		"USD": {Code: "USD", Nominal: 1, Value: 81.5},
	}}
	reply, err := rateReply("USD", "RUB", snapshot, languageRussian)
	if err != nil || !strings.HasPrefix(reply, "Курс ЦБ на 29.09.2026:\n1 USD = 81,50 RUB") {
		t.Fatalf("reply = %q, %v", reply, err)
	}
}

func TestRateHistoryCountsFromPublishedNextDay(t *testing.T) {
	_, cbrURL := newFakeCBR(t, "29.09.2026", "81,5000")
	_, url := newFakeTelegram(t, nil)
	bot := newNewRateBot(t, cbrURL, url, "", config.Config{})
	current := rates.Snapshot{Date: "2026-09-29", Rates: map[string]rates.Rate{
		"RUB": {Code: "RUB", Nominal: 1, Value: 1},
		"USD": {Code: "USD", Nominal: 1, Value: 81.5},
	}}
	// The fake CBR answers 80,0000 for every past date.
	tests := []struct {
		name          string
		localNow      time.Time
		wantFirst     string
		wantYesterday string
	}{
		{"published tomorrow's rates", msk(28, 18, 0), "2026-09-29", "2026-09-28"},
		{"rates already in force", msk(29, 10, 0), "2026-09-29", "2026-09-28"},
		{"user already a day ahead", time.Date(2026, 9, 30, 1, 0, 0, 0, time.FixedZone("", 14*3600)), "2026-09-30", "2026-09-29"},
	}
	for _, tt := range tests {
		snapshots := bot.subscriptionHistoricalSnapshots(t.Context(), current, tt.localNow)
		if got := snapshots[0].Date.Format("2006-01-02"); got != tt.wantFirst {
			t.Fatalf("%s: first date = %s, want %s", tt.name, got, tt.wantFirst)
		}
		history := subscriptionRateHistoryFromSnapshots("USD", "RUB", snapshots)
		if history.Yesterday == nil || history.Yesterday.Date.Format("2006-01-02") != tt.wantYesterday || history.Yesterday.Rate != 80 {
			t.Fatalf("%s: yesterday = %+v, want %s at 80", tt.name, history.Yesterday, tt.wantYesterday)
		}
	}
}
