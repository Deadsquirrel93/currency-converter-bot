package telegram

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"currency-converter-bot/internal/config"
)

func TestParseAlert(t *testing.T) {
	s := session{Language: languageRussian, From: "USD", To: "RUB"}
	valid := map[string]rateAlert{
		"USD RUB > 95":       {From: "USD", To: "RUB", Op: ">", Threshold: 95},
		"USD RUB>95":         {From: "USD", To: "RUB", Op: ">", Threshold: 95},
		"EUR < 90,5":         {From: "EUR", To: "RUB", Op: "<", Threshold: 90.5},
		"> 95":               {From: "USD", To: "RUB", Op: ">", Threshold: 95},
		"USD RUB >= 95":      {From: "USD", To: "RUB", Op: ">=", Threshold: 95},
		"USD RUB ≤ 90":       {From: "USD", To: "RUB", Op: "<=", Threshold: 90},
		"usd rub выше 95":    {From: "USD", To: "RUB", Op: ">", Threshold: 95},
		"EUR USD below 1,05": {From: "EUR", To: "USD", Op: "<", Threshold: 1.05},
		"RUB UZS > 1 000":    {From: "RUB", To: "UZS", Op: ">", Threshold: 1000},
	}
	for input, want := range valid {
		got, err := parseAlert(input, s)
		if err != nil || got != want {
			t.Fatalf("parseAlert(%q) = %+v, %v, want %+v", input, got, err, want)
		}
	}

	for input, want := range map[string]string{
		"USD RUB 95":    "Укажите пару",
		"USD RUB >":     "Порог должен быть",
		"USD RUB > -5":  "Порог должен быть",
		"USD RUB > 0":   "Порог должен быть",
		"USD RUB > abc": "Порог должен быть",
		"USD USD > 5":   "две разные валюты",
		"XYZ RUB > 5":   "Не знаю валюту XYZ",
	} {
		_, err := parseAlert(input, s)
		if got := errorText(err, languageRussian); !strings.Contains(got, want) {
			t.Fatalf("parseAlert(%q) error = %q, want it to contain %q", input, got, want)
		}
	}
}

func TestAlertTriggered(t *testing.T) {
	tests := []struct {
		op   string
		rate float64
		want bool
	}{
		{">", 95.01, true}, {">", 95, false},
		{">=", 95, true}, {">=", 94.99, false},
		{"<", 89.99, true}, {"<", 95, false},
		{"<=", 95, true}, {"<=", 95.01, false},
		{"?", 1, false},
	}
	for _, tt := range tests {
		if got := (rateAlert{Op: tt.op, Threshold: 95}).triggered(tt.rate); got != tt.want {
			t.Fatalf("%s 95 at %v = %v, want %v", tt.op, tt.rate, got, tt.want)
		}
	}
}

func TestAlertCommands(t *testing.T) {
	_, cbrURL := newFakeCBR(t, "26.09.2026", "81,0000")
	fake, url := newFakeTelegram(t, nil)
	bot := newNewRateBot(t, cbrURL, url, "", config.Config{AlertsFile: filepath.Join(t.TempDir(), "alerts.json")})
	bot.botUsername = "rates_bot"
	bot.saveLanguage(1, languageRussian)
	bot.saveLanguage(2, languageEnglish)

	steps := []struct {
		update update
		want   string
	}{
		{privateMessage(1, "/alerts"), "Алертов нет"},
		{privateMessage(1, "/alert"), "Укажите пару"},
		{privateMessage(1, "/alert USD RUB > 95"), "Готово: алерт #1 USD -> RUB > 95 (сейчас 1 USD = 81,00 RUB)"},
		{privateMessage(1, "/alert USD RUB > 80"), "Условие уже выполнено: сейчас 1 USD = 81,00 RUB"},
		{privateMessage(1, "/alert EUR RUB < 90,5"), "алерт #2 EUR -> RUB < 90,5"},
		{groupMessage(1, "/alert@rates_bot USD RUB < 50"), "алерт #3"},
		{privateMessage(1, "/alerts"), "#1 USD -> RUB > 95 (сейчас 81,00)\n#2 EUR -> RUB < 90,5 (сейчас 95,00)\n#3 USD -> RUB < 50"},
		{privateMessage(1, "/alert off 1"), "Алерт #1 удалён"},
		{privateMessage(1, "/alert off 1"), "Алерта #1 нет"},
		{privateMessage(1, "/alert off"), "Укажите номер алерта"},
		{privateMessage(2, "/alert USD RUB > 95"), "Done: alert #1 USD -> RUB > 95"},
		{privateMessage(1, "/help"), "/alert USD RUB > 95"},
	}
	for _, step := range steps {
		bot.handleUpdate(t.Context(), step.update)
		if got := lastReply(t, fake); !strings.Contains(got, step.want) {
			t.Fatalf("%d %q: reply = %q, want it to contain %q", step.update.Message.From.ID, step.update.Message.Text, got, step.want)
		}
	}

	calls := fake.methodCalls("sendMessage")
	var listMarkup string
	for _, call := range calls {
		if strings.HasPrefix(fmt.Sprint(call.Payload["text"]), "Ваши алерты:") {
			listMarkup = fmt.Sprint(call.Payload["reply_markup"])
		}
	}
	if !strings.Contains(listMarkup, "alert_off:1") || !strings.Contains(listMarkup, "alert_off:3") {
		t.Fatalf("/alerts markup = %s, want a remove button per alert", listMarkup)
	}
	if alerts := bot.userAlertList(1); len(alerts) != 2 || alerts[1].ChatID != -100 {
		t.Fatalf("alerts = %+v, want #2 and the group alert #3", alerts)
	}

	// The remove button works only for the alert's owner.
	press := func(userID int64, data string) string {
		bot.handleUpdate(t.Context(), update{CallbackQuery: &callbackQuery{ID: "q", From: &user{ID: userID}, Data: data}})
		answers := fake.methodCalls("answerCallbackQuery")
		return fmt.Sprint(answers[len(answers)-1].Payload["text"])
	}
	if got := press(2, "alert_off:2"); !strings.Contains(got, "already gone") {
		t.Fatalf("another user's button press = %q", got)
	}
	if got := press(1, "alert_off:2"); got != "Алерт #2 удалён" {
		t.Fatalf("button press = %q", got)
	}
	if got := press(1, "alert_off:x"); got != "Кнопка устарела" {
		t.Fatalf("bad button = %q", got)
	}

	for i := len(bot.userAlertList(1)); i < maxAlertsPerUser; i++ {
		bot.handleUpdate(t.Context(), privateMessage(1, fmt.Sprintf("/alert USD RUB > %d", 100+i)))
	}
	bot.handleUpdate(t.Context(), privateMessage(1, "/alert USD RUB > 200"))
	if got := lastReply(t, fake); !strings.Contains(got, "не больше 10 алертов") {
		t.Fatalf("limit reply = %q", got)
	}
	bot.handleUpdate(t.Context(), privateMessage(1, "/alert off all"))
	if got := lastReply(t, fake); got != "Все алерты удалены." || len(bot.userAlertList(1)) != 0 {
		t.Fatalf("/alert off all = %q, alerts left %d", got, len(bot.userAlertList(1)))
	}

	bot.handleUpdate(t.Context(), privateMessage(2, "/delete"))
	if len(bot.userAlertList(2)) != 0 {
		t.Fatal("/delete must remove alerts")
	}
}

func TestAlertFiresOnceOnNewRate(t *testing.T) {
	cbr, cbrURL := newFakeCBR(t, "26.09.2026", "81,0000")
	fake, url := newFakeTelegram(t, nil)
	alertsFile := filepath.Join(t.TempDir(), "alerts.json")
	bot := newNewRateBot(t, cbrURL, url, "", config.Config{AlertsFile: alertsFile})
	bot.saveLanguage(1, languageRussian)
	bot.addAlert(1, rateAlert{ChatID: 100, From: "USD", To: "RUB", Op: ">", Threshold: 81.2})
	bot.addAlert(1, rateAlert{ChatID: 100, From: "USD", To: "RUB", Op: "<", Threshold: 70})

	for minute := 0; minute <= 60; minute++ {
		if minute == 25 {
			cbr.publish("29.09.2026", "81,5000")
		}
		bot.watchRates(t.Context(), msk(28, 15, 0).Add(time.Duration(minute)*time.Minute))
	}

	texts := sentTexts(fake)
	if len(texts) != 1 || texts[0] != "Сработал алерт #1: USD -> RUB > 81,2\nКурс ЦБ на 29.09.2026: 1 USD = 81,50 RUB" {
		t.Fatalf("messages = %q, want one alert", texts)
	}
	if got := cbr.calls(); got != 5 {
		t.Fatalf("latest rates requested %d times, want 5 (alerts alone start polling)", got)
	}
	reloaded := newNewRateBot(t, cbrURL, url, "", config.Config{AlertsFile: alertsFile})
	if alerts := reloaded.userAlertList(1); len(alerts) != 1 || alerts[0].ID != 2 {
		t.Fatalf("stored alerts = %+v, want only #2", alerts)
	}
	reloaded.addAlert(1, rateAlert{ChatID: 100, From: "USD", To: "RUB", Op: "<", Threshold: 60})
	if alerts := reloaded.userAlertList(1); alerts[1].ID != 3 {
		t.Fatalf("new alert ID = %d, want 3 (IDs are not reused)", alerts[1].ID)
	}
}

func TestAlertDeliveryErrors(t *testing.T) {
	t.Run("unreachable chat removes the alerts for that chat", func(t *testing.T) {
		_, cbrURL := newFakeCBR(t, "29.09.2026", "81,5000")
		fake, url := newFakeTelegram(t, func(call apiCall, n int) (int, string) {
			return http.StatusForbidden, `{"ok":false,"error_code":403,"description":"Forbidden: bot was kicked from the group chat"}`
		})
		bot := newNewRateBot(t, cbrURL, url, "", config.Config{})
		bot.addAlert(1, rateAlert{ChatID: -100, From: "USD", To: "RUB", Op: ">", Threshold: 80})
		bot.addAlert(1, rateAlert{ChatID: -100, From: "USD", To: "RUB", Op: "<", Threshold: 50})
		bot.addAlert(1, rateAlert{ChatID: 1, From: "USD", To: "RUB", Op: "<", Threshold: 50})

		bot.watchRates(t.Context(), msk(28, 16, 0))
		if alerts := bot.userAlertList(1); len(alerts) != 1 || alerts[0].ChatID != 1 {
			t.Fatalf("alerts = %+v, want only the private chat alert", alerts)
		}
		if got := len(fake.methodCalls("sendMessage")); got != 1 {
			t.Fatalf("sendMessage calls = %d, want 1", got)
		}
	})

	t.Run("transient errors back off and then drop the alert", func(t *testing.T) {
		_, cbrURL := newFakeCBR(t, "29.09.2026", "81,5000")
		fake, url := newFakeTelegram(t, func(call apiCall, n int) (int, string) {
			return http.StatusInternalServerError, `{"ok":false,"error_code":500,"description":"Internal Server Error"}`
		})
		bot := newNewRateBot(t, cbrURL, url, "", config.Config{})
		bot.addAlert(1, rateAlert{ChatID: 1, From: "USD", To: "RUB", Op: ">", Threshold: 80})

		for minute := 0; minute < 3*60; minute++ {
			bot.watchRates(t.Context(), msk(28, 16, 0).Add(time.Duration(minute)*time.Minute))
		}
		if got := len(fake.methodCalls("sendMessage")); got != 4 {
			t.Fatalf("sendMessage calls = %d, want 4", got)
		}
		if len(bot.userAlertList(1)) != 0 {
			t.Fatal("undeliverable alert must be dropped after the last retry")
		}
	})
}

func TestAlertSkipsDisallowedUsers(t *testing.T) {
	_, cbrURL := newFakeCBR(t, "29.09.2026", "81,5000")
	fake, url := newFakeTelegram(t, nil)
	bot := newNewRateBot(t, cbrURL, url, "", config.Config{AdminUsers: map[int64]struct{}{1: {}}})
	bot.addAlert(2, rateAlert{ChatID: 2, From: "USD", To: "RUB", Op: ">", Threshold: 80})

	bot.watchRates(t.Context(), msk(28, 16, 0))
	if got := len(sentTexts(fake)); got != 0 || len(bot.userAlertList(2)) != 1 {
		t.Fatalf("sent %d messages to a user without access, want 0 and the alert kept", got)
	}
	bot.addAllowedUserIDs([]int64{2})
	bot.watchRates(t.Context(), msk(28, 16, 1))
	if got := len(sentTexts(fake)); got != 1 || len(bot.userAlertList(2)) != 0 {
		t.Fatalf("sent %d messages after access was granted, want 1 and the alert removed", got)
	}
}

func TestLoadAlertsDropsInvalidEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alerts.json")
	raw := `{"1":{"next_id":2,"alerts":[
		{"id":2,"chat_id":1,"from":"usd","to":"rub","op":">","threshold":95},
		{"id":5,"chat_id":1,"from":"USD","to":"RUB","op":"!","threshold":95},
		{"id":6,"chat_id":0,"from":"USD","to":"RUB","op":"<","threshold":95},
		{"id":7,"chat_id":1,"from":"USD","to":"RUB","op":"<","threshold":-1}
	]}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	bot := New(config.Config{AlertsFile: path}, nil, slog.New(slog.DiscardHandler))
	alerts := bot.userAlertList(1)
	if len(alerts) != 1 || alerts[0].From != "USD" || alerts[0].ID != 2 {
		t.Fatalf("alerts = %+v, want only the valid #2", alerts)
	}
	if next, _ := bot.addAlert(1, rateAlert{ChatID: 1, From: "USD", To: "RUB", Op: "<", Threshold: 1}); next.ID != 3 {
		t.Fatalf("next ID = %d, want 3", next.ID)
	}
}
