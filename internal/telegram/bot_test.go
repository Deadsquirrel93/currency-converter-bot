package telegram

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"currency-converter-bot/internal/config"
	"currency-converter-bot/internal/rates"
)

func TestParseModifierPercent(t *testing.T) {
	tests := map[string]float64{
		"50":   50,
		"+1.5": 1.5,
		"+1,5": 1.5,
		"-2":   -2,
		"0":    0,
	}

	for input, want := range tests {
		got, err := parseModifierPercent(input)
		if err != nil {
			t.Fatalf("parseModifierPercent(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("parseModifierPercent(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestParseModifierPercentRejectsInvalid(t *testing.T) {
	for _, input := range []string{"", "abc", "+", "NaN", "+Inf"} {
		if _, err := parseModifierPercent(input); err == nil {
			t.Fatalf("parseModifierPercent(%q) error = nil, want error", input)
		}
	}
}

func TestParseMultiplier(t *testing.T) {
	tests := map[string]float64{
		"1000": 1000,
		"10.5": 10.5,
		"10,5": 10.5,
		"1":    1,
	}

	for input, want := range tests {
		got, err := parseMultiplier(input)
		if err != nil {
			t.Fatalf("parseMultiplier(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("parseMultiplier(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestParseMultiplierRejectsInvalid(t *testing.T) {
	for _, input := range []string{"", "abc", "0", "-1", "NaN", "+Inf"} {
		if _, err := parseMultiplier(input); err == nil {
			t.Fatalf("parseMultiplier(%q) error = nil, want error", input)
		}
	}
}

func TestParseYesNo(t *testing.T) {
	tests := map[string]bool{
		"yes": true,
		"no":  false,
		"да":  true,
		"нет": false,
	}

	for input, want := range tests {
		got, err := parseYesNo(input)
		if err != nil {
			t.Fatalf("parseYesNo(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("parseYesNo(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestResolveCurrencyToken(t *testing.T) {
	tests := map[string]string{
		"usd":    "USD",
		"$":      "USD",
		"баксов": "USD",
		"€":      "EUR",
		"руб":    "RUB",
		"сум":    "UZS",
		"тенге":  "KZT",
	}

	for input, want := range tests {
		got, ok := resolveCurrencyToken(input)
		if !ok {
			t.Fatalf("resolveCurrencyToken(%q) ok = false", input)
		}
		if got != want {
			t.Fatalf("resolveCurrencyToken(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseConversionInputUsesInlineCurrencies(t *testing.T) {
	s := session{From: "EUR", To: "KZT", Multiplier: 1}

	tests := map[string]conversionInput{
		"100 usd to rub": {Amount: 100, AmountCount: 1, From: "USD", To: "RUB", Inline: true},
		"100$ в руб":     {Amount: 100, AmountCount: 1, From: "USD", To: "RUB", Inline: true},
		"250 евро":       {Amount: 250, AmountCount: 1, From: "EUR", To: "KZT", Inline: true},
		"100\n200":       {Amount: 300, AmountCount: 2, From: "EUR", To: "KZT"},
	}

	for input, want := range tests {
		got, err := parseConversionInput(input, s)
		if err != nil {
			t.Fatalf("parseConversionInput(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("parseConversionInput(%q) = %+v, want %+v", input, got, want)
		}
	}
}

func TestConversionSettingsForInlineInputIgnoresInputSettingsByDefault(t *testing.T) {
	settings := conversionSettingsForInput(session{
		Multiplier:        1000,
		ModifyFromPercent: 10,
		ModifyToPercent:   20,
	}, conversionInput{Inline: true})

	if settings.Multiplier != 1 {
		t.Fatalf("Multiplier = %v, want 1", settings.Multiplier)
	}
	if settings.UseModify {
		t.Fatal("UseModify = true, want false")
	}
}

func TestConversionSettingsForInlineInputCanUseModifiers(t *testing.T) {
	settings := conversionSettingsForInput(session{
		InlineModify:      true,
		Multiplier:        1000,
		ModifyFromPercent: 10,
		ModifyToPercent:   20,
	}, conversionInput{Inline: true})

	if settings.Multiplier != 1 {
		t.Fatalf("Multiplier = %v, want 1", settings.Multiplier)
	}
	if !settings.UseModify {
		t.Fatal("UseModify = false, want true")
	}
	if settings.ModifyFromPercent != 10 || settings.ModifyToPercent != 20 {
		t.Fatalf("settings = %+v", settings)
	}
}

func TestConversionSettingsForSessionPairKeepsInputSettings(t *testing.T) {
	settings := conversionSettingsForInput(session{
		Multiplier:        1000,
		ModifyFromPercent: 10,
		ModifyToPercent:   20,
	}, conversionInput{})

	if settings.Multiplier != 1000 || !settings.UseModify || settings.ModifyFromPercent != 10 || settings.ModifyToPercent != 20 {
		t.Fatalf("settings = %+v", settings)
	}
}

func TestParseRateRequest(t *testing.T) {
	s := session{From: "USD", To: "RUB", Multiplier: 1}

	tests := map[string]rateRequest{
		"/rate":               {From: "USD", To: "RUB"},
		"/rate eur":           {From: "EUR", To: "RUB"},
		"/rate $ в руб":       {From: "USD", To: "RUB"},
		"/rate usd for rub":   {From: "USD", To: "RUB"},
		"/rate сум евро":      {From: "UZS", To: "EUR"},
		"/rate@mybot eur rub": {From: "EUR", To: "RUB"},
	}

	for input, want := range tests {
		got, err := parseRateRequest(input, s)
		if err != nil {
			t.Fatalf("parseRateRequest(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("parseRateRequest(%q) = %+v, want %+v", input, got, want)
		}
	}
}

func TestParseRateRequestRejectsUnknownCurrencyCode(t *testing.T) {
	s := session{From: "USD", To: "RUB", Multiplier: 1}
	if _, err := parseRateRequest("/rate btc usd", s); err == nil {
		t.Fatal("parseRateRequest() error = nil, want error")
	}
}

func TestApplyPercent(t *testing.T) {
	if got := applyPercent(1000, 50); got != 1500 {
		t.Fatalf("applyPercent(1000, 50) = %v, want 1500", got)
	}
	if got := applyPercent(100, -10); got != 90 {
		t.Fatalf("applyPercent(100, -10) = %v, want 90", got)
	}
}

func TestFormatAmountWithSettings(t *testing.T) {
	got := formatAmountWithSettings(45, 45000, 45000, "UZS")
	want := "45,00 (45 000,00) UZS"
	if got != want {
		t.Fatalf("formatAmountWithSettings() = %q, want %q", got, want)
	}

	got = formatAmountWithSettings(100, 100, 150, "USD")
	want = "100,00 (150,00) USD"
	if got != want {
		t.Fatalf("formatAmountWithSettings() = %q, want %q", got, want)
	}

	got = formatAmountWithSettings(45, 45000, 67500, "UZS")
	want = "45,00 (45 000,00 -> 67 500,00) UZS"
	if got != want {
		t.Fatalf("formatAmountWithSettings() = %q, want %q", got, want)
	}
}

func TestConversionReplyCanSkipModifiers(t *testing.T) {
	snapshot := rates.Snapshot{Rates: map[string]rates.Rate{
		"RUB": {Code: "RUB", Nominal: 1, Value: 1},
		"UZS": {Code: "UZS", Nominal: 1, Value: 0.01},
		"USD": {Code: "USD", Nominal: 1, Value: 100},
	}}

	withoutModifiers, err := conversionReply(10000, 1, "UZS", "USD", 1, 50, 50, false, "", snapshot)
	if err != nil {
		t.Fatalf("conversionReply without modifiers: %v", err)
	}
	for _, want := range []string{
		"10 000,00 UZS = <b>1,00 USD</b>",
		"Курс: 1 UZS = 0,0001 USD",
		"100 UZS = 0,01 USD",
	} {
		if !strings.Contains(withoutModifiers, want) {
			t.Fatalf("conversionReply without modifiers must contain %q, got %q", want, withoutModifiers)
		}
	}

	withModifiers, err := conversionReply(10000, 1, "UZS", "USD", 1, 50, 50, true, "", snapshot)
	if err != nil {
		t.Fatalf("conversionReply with modifiers: %v", err)
	}
	for _, want := range []string{
		"10 000,00 (15 000,00) UZS = <b>2,25 USD</b>",
		"Курс: 1 UZS = 0,0001 USD",
		"100 UZS = 0,01 USD",
		"Расчет для 1 введенной единицы = 0,000225 USD",
	} {
		if !strings.Contains(withModifiers, want) {
			t.Fatalf("conversionReply with modifiers must contain %q, got %q", want, withModifiers)
		}
	}
}

func TestFormatConvertedAmountKeepsTinyValuesReadable(t *testing.T) {
	if got := formatConvertedAmount(0.000225); got != "0,000225" {
		t.Fatalf("formatConvertedAmount() = %q, want 0,000225", got)
	}
	if got := formatConvertedAmount(12.3); got != "12,30" {
		t.Fatalf("formatConvertedAmount() = %q, want 12,30", got)
	}
}

func TestFormatConvertedAmountUsesRoundMode(t *testing.T) {
	tests := map[string]string{
		"0": "12",
		"2": "12,35",
		"4": "12,3457",
		"6": "12,345679",
	}

	for mode, want := range tests {
		got := formatConvertedAmountForMode(12.3456789, mode)
		if got != want {
			t.Fatalf("formatConvertedAmountForMode(%q) = %q, want %q", mode, got, want)
		}
	}

	if got := formatConvertedAmountForMode(0.000225, "2"); got != "0,00" {
		t.Fatalf("formatConvertedAmountForMode tiny with round 2 = %q, want 0,00", got)
	}
}

func TestParseRoundMode(t *testing.T) {
	for _, input := range []string{"auto", "AUTO", "0", "2", "4", "6"} {
		if _, err := parseRoundMode(input); err != nil {
			t.Fatalf("parseRoundMode(%q): %v", input, err)
		}
	}
	if _, err := parseRoundMode("3"); err == nil {
		t.Fatal("parseRoundMode(3) error = nil, want error")
	}
}

func TestInlineConversionResultUsesHTMLMessage(t *testing.T) {
	result := inlineConversionResult("100,00 USD = <b>9 000,00 RUB</b>\nКурс: 1 USD = 90,00 RUB")
	if result.Type != "article" || result.ID == "" {
		t.Fatalf("inline result = %+v", result)
	}
	if result.Title != "100,00 USD = 9 000,00 RUB" {
		t.Fatalf("Title = %q", result.Title)
	}
	if result.InputMessageContent.MessageText == "" || result.InputMessageContent.ParseMode != "HTML" {
		t.Fatalf("InputMessageContent = %+v", result.InputMessageContent)
	}
}

func TestRateReply(t *testing.T) {
	snapshot := rates.Snapshot{
		FetchedAt: time.Date(2026, 5, 10, 9, 30, 0, 0, time.UTC),
		Rates: map[string]rates.Rate{
			"RUB": {Code: "RUB", Nominal: 1, Value: 1},
			"USD": {Code: "USD", Nominal: 1, Value: 100},
		},
	}

	got, err := rateReply("USD", "RUB", snapshot)
	if err != nil {
		t.Fatalf("rateReply(): %v", err)
	}
	for _, want := range []string{
		"1 USD = 100,00 RUB",
		"1 RUB = 0,01 USD",
		"Обновлено: 2026-05-10 09:30:00 UTC",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rateReply() must contain %q, got:\n%s", want, got)
		}
	}
}

func TestSettingsText(t *testing.T) {
	text := settingsText(session{
		From:              "USD",
		To:                "RUB",
		With:              []string{"EUR", "USD"},
		WithModify:        true,
		Multiplier:        1000,
		Round:             "4",
		ModifyFromPercent: 1.5,
		ModifyToPercent:   -2,
	}, rates.Snapshot{FetchedAt: time.Date(2026, 5, 10, 9, 30, 0, 0, time.UTC)})

	for _, want := range []string{
		"Пара: USD -> RUB",
		"Курсы обновлены: 2026-05-10 09:30:00 UTC",
		"Кнопки перевода: EUR, USD",
		"Модификаторы для кнопок: yes",
		"Модификаторы для явных валют: no",
		"Множитель входной суммы: 1 000",
		"Округление результата: 4",
		"Модификатор входной суммы: +1,5%",
		"Модификатор результата: -2%",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("settingsText() must contain %q, got:\n%s", want, text)
		}
	}
}

func TestBotCommands(t *testing.T) {
	commands := botCommands()
	got := map[string]bool{}
	for _, command := range commands {
		if command.Command == "" {
			t.Fatal("command must not be empty")
		}
		if command.Description == "" {
			t.Fatalf("description for %q must not be empty", command.Command)
		}
		got[command.Command] = true
	}

	for _, want := range []string{"start", "help", "settings", "from", "to", "swap", "rate", "reset", "delete", "with", "with_modify", "inline_modify", "multi", "round", "modify_from", "modify_to", "list"} {
		if !got[want] {
			t.Fatalf("botCommands() must contain %q", want)
		}
	}
}

func TestWithCallbackData(t *testing.T) {
	data, ok := withCallbackData(10000, session{
		From:              "UZS",
		With:              []string{"USD"},
		Multiplier:        1,
		WithModify:        true,
		ModifyFromPercent: 1.5,
		ModifyToPercent:   -2,
	}, "USD")
	if !ok {
		t.Fatal("withCallbackData() ok = false")
	}

	request, err := parseWithCallbackData(data)
	if err != nil {
		t.Fatalf("parseWithCallbackData(): %v", err)
	}
	if request.From != "UZS" || request.To != "USD" || request.Amount != 10000 || request.Multiplier != 1 || !request.UseModify || request.ModifyFromPercent != 1.5 || request.ModifyToPercent != -2 {
		t.Fatalf("request = %+v", request)
	}
}

func TestWithReplyMarkupCreatesMultipleButtons(t *testing.T) {
	markup := withReplyMarkup(10000, session{
		From:       "UZS",
		With:       []string{"USD", "EUR", "RUB"},
		Multiplier: 1,
	})
	if markup == nil {
		t.Fatal("withReplyMarkup() = nil")
	}
	if len(markup.InlineKeyboard) != 1 {
		t.Fatalf("rows = %d, want 1", len(markup.InlineKeyboard))
	}
	if len(markup.InlineKeyboard[0]) != 3 {
		t.Fatalf("buttons = %d, want 3", len(markup.InlineKeyboard[0]))
	}
	for i, want := range []string{"в USD", "в EUR", "в RUB"} {
		if markup.InlineKeyboard[0][i].Text != want {
			t.Fatalf("button %d text = %q, want %q", i, markup.InlineKeyboard[0][i].Text, want)
		}
	}
}

func TestBotPersistsSessions(t *testing.T) {
	settingsFile := filepath.Join(t.TempDir(), "user_settings.json")
	cfg := config.Config{
		DefaultFrom:      "USD",
		DefaultTo:        "RUB",
		UserSettingsFile: settingsFile,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	bot := New(cfg, nil, logger)
	bot.setSession(42, session{
		From:              "UZS",
		To:                "RUB",
		With:              []string{"USD", "EUR"},
		WithModify:        true,
		InlineModify:      true,
		Multiplier:        1000,
		Round:             "4",
		ModifyFromPercent: 1.5,
		ModifyToPercent:   -2,
	})

	raw, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", settingsFile, err)
	}
	if !strings.Contains(string(raw), `"multiplier": 1000`) {
		t.Fatalf("settings file must contain multiplier, got:\n%s", string(raw))
	}
	if !strings.Contains(string(raw), `"round": "4"`) {
		t.Fatalf("settings file must contain round, got:\n%s", string(raw))
	}
	if !strings.Contains(string(raw), `"inline_modify": true`) {
		t.Fatalf("settings file must contain inline_modify, got:\n%s", string(raw))
	}

	restarted := New(cfg, nil, logger)
	got := restarted.getSession(42)
	want := session{
		From:              "UZS",
		To:                "RUB",
		With:              []string{"USD", "EUR"},
		WithModify:        true,
		InlineModify:      true,
		Multiplier:        1000,
		Round:             "4",
		ModifyFromPercent: 1.5,
		ModifyToPercent:   -2,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("restored session = %+v, want %+v", got, want)
	}
}

func TestBotResetsSessionToDefaults(t *testing.T) {
	settingsFile := filepath.Join(t.TempDir(), "user_settings.json")
	cfg := config.Config{
		DefaultFrom:      "USD",
		DefaultTo:        "RUB",
		UserSettingsFile: settingsFile,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	bot := New(cfg, nil, logger)
	bot.setSession(42, session{
		From:              "UZS",
		To:                "EUR",
		With:              []string{"USD"},
		WithModify:        true,
		InlineModify:      true,
		Multiplier:        1000,
		Round:             "4",
		ModifyFromPercent: 1.5,
		ModifyToPercent:   -2,
	})
	bot.setSession(42, defaultSession(cfg.DefaultFrom, cfg.DefaultTo))

	got := bot.getSession(42)
	want := session{From: "USD", To: "RUB", Multiplier: 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reset session = %+v, want %+v", got, want)
	}

	raw, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", settingsFile, err)
	}
	for _, unwanted := range []string{`"with"`, `"round"`, `"inline_modify": true`, `"modify_from_percent": 1.5`, `"modify_to_percent": -2`, `"multiplier": 1000`} {
		if strings.Contains(string(raw), unwanted) {
			t.Fatalf("settings file must not contain %q after reset, got:\n%s", unwanted, string(raw))
		}
	}
}

func TestBotDeletesSession(t *testing.T) {
	settingsFile := filepath.Join(t.TempDir(), "user_settings.json")
	cfg := config.Config{
		DefaultFrom:      "USD",
		DefaultTo:        "RUB",
		UserSettingsFile: settingsFile,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	bot := New(cfg, nil, logger)
	bot.setSession(42, session{From: "UZS", To: "EUR", Multiplier: 1000})
	bot.deleteSession(42)

	got := bot.getSession(42)
	want := session{From: "USD", To: "RUB", Multiplier: 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("session after delete = %+v, want default %+v", got, want)
	}

	raw, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", settingsFile, err)
	}
	if strings.Contains(string(raw), `"42"`) {
		t.Fatalf("settings file must not contain deleted user, got:\n%s", string(raw))
	}

	restarted := New(cfg, nil, logger)
	got = restarted.getSession(42)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("restored session after delete = %+v, want default %+v", got, want)
	}
}

func TestBotLoadsLegacySingleWithCurrency(t *testing.T) {
	settingsFile := filepath.Join(t.TempDir(), "user_settings.json")
	if err := os.WriteFile(settingsFile, []byte(`{"42":{"from":"UZS","to":"RUB","with":"USD","multiplier":1}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", settingsFile, err)
	}
	cfg := config.Config{
		DefaultFrom:      "USD",
		DefaultTo:        "RUB",
		UserSettingsFile: settingsFile,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	bot := New(cfg, nil, logger)
	got := bot.getSession(42)
	if len(got.With) != 1 || got.With[0] != "USD" {
		t.Fatalf("With = %#v, want [USD]", got.With)
	}
}
