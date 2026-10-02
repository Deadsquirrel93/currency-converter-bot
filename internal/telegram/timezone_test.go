package telegram

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"currency-converter-bot/internal/config"
	"currency-converter-bot/internal/rates"
)

func TestParseTimezone(t *testing.T) {
	valid := map[string]string{
		"Europe/Moscow":           "Europe/Moscow",
		"europe/moscow":           "Europe/Moscow",
		"EUROPE/MOSCOW":           "Europe/Moscow",
		"america/new_york":        "America/New_York",
		"America/Argentina/Salta": "America/Argentina/Salta",
		"мск":                     "Europe/Moscow",
		"MSK":                     "Europe/Moscow",
		"utc":                     "UTC",
		"GMT":                     "UTC",
		"+3":                      "UTC+03:00",
		"UTC+3":                   "UTC+03:00",
		"utc -4":                  "UTC-04:00",
		"GMT+05:30":               "UTC+05:30",
		"+0545":                   "UTC+05:45",
		"−2":                      "UTC-02:00",
		"+0":                      "UTC",
		"+14":                     "UTC+14:00",
		"-12":                     "UTC-12:00",
	}
	for input, want := range valid {
		got, err := parseTimezone(input)
		if err != nil || got != want {
			t.Fatalf("parseTimezone(%q) = %q, %v, want %q", input, got, err, want)
		}
		if _, err := loadTimezone(got); err != nil {
			t.Fatalf("loadTimezone(%q) after parseTimezone(%q): %v", got, input, err)
		}
	}

	for _, input := range []string{"", "Local", "local", "Mars/Olympus", "Europe", "../etc/passwd", "/etc/localtime", "3", "+15", "-13", "UTC+3:5", "+3:", "+123", strings.Repeat("A/", 40)} {
		if got, err := parseTimezone(input); err == nil {
			t.Fatalf("parseTimezone(%q) = %q, want error", input, got)
		}
	}
}

func TestLoadTimezoneFixedOffset(t *testing.T) {
	location, err := loadTimezone("UTC+05:30")
	if err != nil {
		t.Fatal(err)
	}
	_, offset := time.Date(2026, 5, 10, 0, 0, 0, 0, location).Zone()
	if offset != 5*3600+30*60 {
		t.Fatalf("offset = %d, want 19800", offset)
	}
	if _, err := loadTimezone(""); err == nil {
		t.Fatal("empty zone must not load as UTC")
	}
}

func lastReply(t *testing.T, fake *fakeTelegram) string {
	t.Helper()
	calls := fake.methodCalls("sendMessage")
	if len(calls) == 0 {
		t.Fatal("no messages sent")
	}
	return fmt.Sprint(calls[len(calls)-1].Payload["text"])
}

func TestTimezoneCommand(t *testing.T) {
	fake, url := newFakeTelegram(t, nil)
	bot := newHardeningBot(t, config.Config{}, url)
	bot.botUsername = "rates_bot"
	bot.saveLanguage(1, languageRussian)

	steps := []struct {
		update update
		want   string
	}{
		{privateMessage(1, "/tz"), "Часовой пояс: Asia/Tashkent (по умолчанию)"},
		{privateMessage(1, "/tz europe/moscow"), "Готово: часовой пояс Europe/Moscow"},
		{privateMessage(1, "/tz"), "Часовой пояс: Europe/Moscow, сейчас"},
		{privateMessage(1, "/settings"), "Часовой пояс: Europe/Moscow"},
		{privateMessage(1, "/tz Mars/Olympus"), "Не знаю такой часовой пояс"},
		{privateMessage(1, "/tz"), "Europe/Moscow"},
		{privateMessage(1, "/reset"), "Настройки сброшены"},
		{privateMessage(1, "/tz"), "Europe/Moscow"},
		{groupMessage(1, "/tz@rates_bot UTC+3"), "UTC+03:00"},
		{groupMessage(1, "/tz@other_bot UTC+4"), "UTC+03:00"},
		{privateMessage(1, "/tz default"), "Asia/Tashkent (по умолчанию)"},
		{privateMessage(1, "/lang en"), "Language changed"},
		{privateMessage(1, "/tz UTC-4"), "Done: time zone UTC-04:00"},
		{privateMessage(1, "/help"), "/tz Europe/Moscow"},
	}
	for _, step := range steps {
		bot.handleUpdate(t.Context(), step.update)
		if got := lastReply(t, fake); !strings.Contains(got, step.want) {
			t.Fatalf("%q: reply = %q, want it to contain %q", step.update.Message.Text, got, step.want)
		}
	}

	bot.handleUpdate(t.Context(), privateMessage(1, "/delete"))
	if got := bot.getSession(1).Timezone; got != "" {
		t.Fatalf("/delete kept time zone %q", got)
	}
}

func TestSubscriptionsFireInEachUsersTimezone(t *testing.T) {
	bot, messages := newTestBotWithServers(t)
	for userID, zone := range map[int64]string{2: "Europe/Moscow", 3: "Pacific/Kiritimati"} {
		s := bot.getSession(userID)
		s.Timezone = zone
		bot.setSession(userID, s)
	}
	bot.subscriptions = map[int64]dailySubscription{
		1: {ChatID: 100, From: "USD", To: "RUB", Time: "09:00"}, // default: Asia/Tashkent, UTC+5
		2: {ChatID: 200, From: "USD", To: "RUB", Time: "09:00"}, // UTC+3
		3: {ChatID: 300, From: "USD", To: "RUB", Time: "09:00"}, // UTC+14
	}
	sentTo := func() []int64 {
		ids := chatIDs(messages())
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		return ids
	}

	// 09:00 on May 11 in Kiritimati is 19:00 UTC on May 10.
	bot.sendDueSubscriptions(t.Context(), time.Date(2026, 5, 10, 3, 59, 0, 0, time.UTC))
	if got := sentTo(); len(got) != 1 || got[0] != 300 {
		t.Fatalf("03:59 UTC: sent to %v, want [300] (13:59 on May 10 in Kiritimati)", got)
	}
	bot.sendDueSubscriptions(t.Context(), time.Date(2026, 5, 10, 4, 0, 0, 0, time.UTC))
	if got := sentTo(); fmt.Sprint(got) != "[100 300]" {
		t.Fatalf("04:00 UTC: sent to %v, want [100 300]", got)
	}
	bot.sendDueSubscriptions(t.Context(), time.Date(2026, 5, 10, 6, 0, 0, 0, time.UTC))
	if got := sentTo(); fmt.Sprint(got) != "[100 200 300]" {
		t.Fatalf("06:00 UTC: sent to %v, want [100 200 300]", got)
	}
	bot.sendDueSubscriptions(t.Context(), time.Date(2026, 5, 10, 19, 0, 0, 0, time.UTC))
	if got := sentTo(); fmt.Sprint(got) != "[100 200 300 300]" {
		t.Fatalf("19:00 UTC: sent to %v, want Kiritimati again on its May 11", got)
	}

	for userID, want := range map[int64]string{1: "2026-05-10", 2: "2026-05-10", 3: "2026-05-11"} {
		if sub, _ := bot.getUserSubscription(userID); sub.LastSentDate != want {
			t.Fatalf("user %d LastSentDate = %q, want %q", userID, sub.LastSentDate, want)
		}
	}
}

func TestSubscriptionRetryUsesUsersDate(t *testing.T) {
	fake, url := newFakeTelegram(t, func(call apiCall, n int) (int, string) {
		return http.StatusInternalServerError, `{"ok":false,"error_code":500,"description":"Internal Server Error"}`
	})
	bot := newHardeningBot(t, config.Config{}, url)
	s := bot.getSession(1)
	s.Timezone = "Pacific/Kiritimati"
	bot.setSession(1, s)
	bot.subscriptions = map[int64]dailySubscription{1: {ChatID: 1, From: "USD", To: "RUB", Time: "09:00"}}

	start := time.Date(2026, 5, 10, 19, 0, 0, 0, time.UTC) // 09:00 May 11 in Kiritimati
	bot.sendDueSubscriptions(t.Context(), start)
	if retry := bot.subRetry[1]; retry.Date != "2026-05-11" {
		t.Fatalf("retry date = %q, want the user's date 2026-05-11", retry.Date)
	}
	bot.sendDueSubscriptions(t.Context(), start.Add(4*time.Minute))
	bot.sendDueSubscriptions(t.Context(), start.Add(5*time.Minute))
	if got := len(fake.methodCalls("sendMessage")); got != 2 {
		t.Fatalf("sendMessage calls = %d, want 2 (first try and one retry after 5 minutes)", got)
	}
}

func TestRateHistoryUsesUsersDays(t *testing.T) {
	var mu sync.Mutex
	var dates []time.Time
	cbr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if raw := r.URL.Query().Get("date_req"); raw != "" {
			if date, err := time.Parse("02/01/2006", raw); err == nil {
				mu.Lock()
				dates = append(dates, date)
				mu.Unlock()
			}
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<ValCurs><Valute><CharCode>USD</CharCode><Nominal>1</Nominal><Name>USD</Name><Value>90,5</Value></Valute></ValCurs>`))
	}))
	t.Cleanup(cbr.Close)
	fake, url := newFakeTelegram(t, nil)
	cfg := config.Config{TelegramToken: "42:" + testToken, TelegramAPI: url, DefaultFrom: "USD", DefaultTo: "RUB", Location: time.UTC}
	bot := New(cfg, rates.NewProvider(cbr.URL, t.TempDir()+"/rates.json", time.Hour), slog.New(slog.DiscardHandler))
	bot.saveLanguage(1, languageRussian)
	s := bot.getSession(1)
	s.Timezone = "Pacific/Kiritimati"
	bot.setSession(1, s)

	bot.handleUpdate(t.Context(), privateMessage(1, "/rate"))
	if got := lastReply(t, fake); !strings.Contains(got, "Динамика USD -> RUB") {
		t.Fatalf("/rate reply = %q", got)
	}

	today := startOfLocalDay(time.Now().In(time.FixedZone("", 14*3600)))
	mu.Lock()
	defer mu.Unlock()
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
	if len(dates) != 29 {
		t.Fatalf("requested %d past dates, want 29", len(dates))
	}
	first, last := dates[0].Format("2006-01-02"), dates[len(dates)-1].Format("2006-01-02")
	if want := today.AddDate(0, 0, -1).Format("2006-01-02"); last != want {
		t.Fatalf("latest requested date = %s, want Kiritimati's yesterday %s", last, want)
	}
	if want := today.AddDate(0, 0, -29).Format("2006-01-02"); first != want {
		t.Fatalf("earliest requested date = %s, want %s", first, want)
	}
}

func TestRebaseLastSentDate(t *testing.T) {
	zone := func(name string) *time.Location {
		location, err := loadTimezone(name)
		if err != nil {
			t.Fatal(err)
		}
		return location
	}
	moscow, tashkent, newYork, kiritimati := zone("Europe/Moscow"), zone("Asia/Tashkent"), zone("America/New_York"), zone("Pacific/Kiritimati")
	sub := dailySubscription{Time: "09:00", LastSentDate: "2026-05-10"}

	tests := []struct {
		name     string
		from, to *time.Location
		want     string
	}{
		{"moscow to tashkent", moscow, tashkent, "2026-05-10"},
		{"tashkent to moscow: next slot only 2h later", tashkent, moscow, "2026-05-10"},
		{"moscow to new york: next slot only 7h later", moscow, newYork, "2026-05-10"},
		{"new york to moscow", newYork, moscow, "2026-05-10"},
		{"tashkent to kiritimati", tashkent, kiritimati, "2026-05-10"},
		{"kiritimati to tashkent", kiritimati, tashkent, "2026-05-10"},
		{"kiritimati to new york", kiritimati, newYork, "2026-05-09"},
	}
	for _, tt := range tests {
		if got := rebaseLastSentDate(sub, tt.from, tt.to); got != tt.want {
			t.Fatalf("%s: got %s, want %s", tt.name, got, tt.want)
		}
	}
	if got := rebaseLastSentDate(dailySubscription{Time: "09:00"}, moscow, tashkent); got != "" {
		t.Fatalf("never sent: got %q, want empty", got)
	}
}

func TestTimezoneChangeDoesNotRepeatSubscription(t *testing.T) {
	fake, url := newFakeTelegram(t, nil)
	bot := newHardeningBot(t, config.Config{}, url)
	bot.saveLanguage(1, languageRussian)
	bot.subscriptions = map[int64]dailySubscription{1: {ChatID: 1, From: "USD", To: "RUB", Time: "09:00"}}
	daily := func() int {
		n := 0
		for _, call := range fake.methodCalls("sendMessage") {
			if strings.HasPrefix(fmt.Sprint(call.Payload["text"]), "Ежедневный курс") {
				n++
			}
		}
		return n
	}

	bot.sendDueSubscriptions(t.Context(), time.Date(2026, 5, 10, 4, 0, 0, 0, time.UTC)) // 09:00 Tashkent
	bot.handleUpdate(t.Context(), privateMessage(1, "/tz Europe/Moscow"))
	if got := lastReply(t, fake); !strings.Contains(got, "Подписка будет приходить в 09:00") {
		t.Fatalf("/tz reply = %q, want subscription note", got)
	}
	bot.sendDueSubscriptions(t.Context(), time.Date(2026, 5, 10, 6, 0, 0, 0, time.UTC)) // 09:00 Moscow, 2h later
	if got := daily(); got != 1 {
		t.Fatalf("daily messages on May 10 = %d, want 1", got)
	}
	bot.sendDueSubscriptions(t.Context(), time.Date(2026, 5, 11, 6, 0, 0, 0, time.UTC)) // 09:00 Moscow May 11
	if got := daily(); got != 2 {
		t.Fatalf("daily messages after May 11 09:00 Moscow = %d, want 2", got)
	}
}

func TestUnknownStoredTimezoneFallsBackToDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"1":{"language":"ru","from":"USD","to":"RUB","timezone":"Mars/Olympus"},"2":{"language":"ru","from":"USD","to":"RUB","timezone":"UTC+03:00"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	bot := New(config.Config{UserSettingsFile: path, Location: mustLocation(t, "Asia/Tashkent")}, nil, slog.New(slog.DiscardHandler))
	if got := bot.userLocation(1).String(); got != "Asia/Tashkent" {
		t.Fatalf("unknown stored zone: location = %q, want the default", got)
	}
	if got := bot.userLocation(2).String(); got != "UTC+03:00" {
		t.Fatalf("stored offset: location = %q, want UTC+03:00", got)
	}
}

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return location
}
