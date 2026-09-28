package telegram

import (
	"fmt"
	"strings"
	"testing"

	"currency-converter-bot/internal/config"
)

func TestParseConversionInputEvaluatesExpressions(t *testing.T) {
	tests := []struct {
		text     string
		language string
		amount   float64
		from     string
	}{
		{"100+50 usd", languageRussian, 150, "USD"},
		{"(12*3)+5", languageRussian, 41, "USD"},
		{"1 000 / 4 eur", languageRussian, 250, "EUR"},
		{"1,000 / 4", languageEnglish, 250, "USD"},
		{"100 usd x 9", languageRussian, 900, "USD"},
		{"70 х 5 литров молока", languageRussian, 350, "USD"},
	}
	for _, tt := range tests {
		request, err := parseConversionInput(tt.text, session{Language: tt.language, From: "USD", To: "RUB"})
		if err != nil {
			t.Fatalf("parseConversionInput(%q): %v", tt.text, err)
		}
		if request.Amount != tt.amount || request.From != tt.from {
			t.Fatalf("parseConversionInput(%q) = %v %s, want %v %s", tt.text, request.Amount, request.From, tt.amount, tt.from)
		}
	}
}

func TestAmountErrorTextForExpressions(t *testing.T) {
	tests := []struct {
		text   string
		ru, en string
	}{
		{"1/0", "деление на ноль", "divides by zero"},
		{"50 - 100", "отрицательной", "negative"},
		{strings.Repeat("(", 11) + "1" + strings.Repeat(")", 11) + "+1", "Слишком сложное выражение", "too complex"},
	}
	for _, tt := range tests {
		_, err := parseConversionInput(tt.text, session{})
		if got := amountErrorText(err, languageRussian); !strings.Contains(got, tt.ru) {
			t.Fatalf("%q: russian text = %q, want %q", tt.text, got, tt.ru)
		}
		if got := amountErrorText(err, languageEnglish); !strings.Contains(got, tt.en) {
			t.Fatalf("%q: english text = %q, want %q", tt.text, got, tt.en)
		}
	}
}

func TestExpressionsInPrivateChatsAndGroups(t *testing.T) {
	fake, url := newFakeTelegram(t, nil)
	bot := newHardeningBot(t, config.Config{}, url)
	bot.botUsername = "rates_bot"
	bot.saveLanguage(1, languageRussian)

	steps := []struct {
		update update
		want   string
	}{
		{privateMessage(1, "100+50 usd"), "150,00 USD = <b>13 575,00 RUB</b>"},
		{privateMessage(1, "(12*3)+5"), "41,00 USD"},
		{privateMessage(1, "1 000 / 4"), "250,00 USD"},
		{privateMessage(1, "10 / 0"), "деление на ноль"},
		{privateMessage(1, "2 кофе по 350"), "2 x 350"},
		{privateMessage(1, "12-05-2024"), "несколько чисел"},
		{groupMessage(1, "@rates_bot 100 + 50"), "150,00 USD"},
	}
	for _, step := range steps {
		before := len(fake.methodCalls("sendMessage"))
		bot.handleUpdate(t.Context(), step.update)
		calls := fake.methodCalls("sendMessage")
		if len(calls) != before+1 {
			t.Fatalf("%q: sent %d messages, want 1", step.update.Message.Text, len(calls)-before)
		}
		if text := fmt.Sprint(calls[len(calls)-1].Payload["text"]); !strings.Contains(text, step.want) {
			t.Fatalf("%q: reply = %q, want it to contain %q", step.update.Message.Text, text, step.want)
		}
	}
}
