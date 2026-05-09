package telegram

import (
	"strings"
	"testing"
)

func TestSupportedCurrenciesText(t *testing.T) {
	text := supportedCurrenciesText()
	if len(supportedCurrencies) > 50 {
		t.Fatalf("supportedCurrencies has %d items, want <= 50", len(supportedCurrencies))
	}
	if len(text) > 4096 {
		t.Fatalf("supportedCurrenciesText is %d bytes, Telegram limit is 4096", len(text))
	}
	for _, code := range []string{"RUB", "USD", "EUR"} {
		if !isSupportedCurrency(code) {
			t.Fatalf("%s must be supported", code)
		}
		if !strings.Contains(text, code+" - ") {
			t.Fatalf("list text must contain %s", code)
		}
	}
}
