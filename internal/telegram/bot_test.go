package telegram

import (
	"strings"
	"testing"
	"time"

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

func TestSettingsText(t *testing.T) {
	text := settingsText(session{
		From:              "USD",
		To:                "RUB",
		Multiplier:        1000,
		ModifyFromPercent: 1.5,
		ModifyToPercent:   -2,
	}, rates.Snapshot{FetchedAt: time.Date(2026, 5, 10, 9, 30, 0, 0, time.UTC)})

	for _, want := range []string{
		"Пара: USD -> RUB",
		"Курсы обновлены: 2026-05-10 09:30:00 UTC",
		"Множитель входной суммы: 1 000",
		"Модификатор входной суммы: +1,5%",
		"Модификатор результата: -2%",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("settingsText() must contain %q, got:\n%s", want, text)
		}
	}
}
