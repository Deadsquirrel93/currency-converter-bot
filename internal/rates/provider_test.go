package rates

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDecodeCBRWindows1251XML(t *testing.T) {
	body := string([]byte{
		'<', '?', 'x', 'm', 'l', ' ', 'v', 'e', 'r', 's', 'i', 'o', 'n', '=', '"', '1', '.', '0', '"', ' ', 'e', 'n', 'c', 'o', 'd', 'i', 'n', 'g', '=', '"', 'w', 'i', 'n', 'd', 'o', 'w', 's', '-', '1', '2', '5', '1', '"', '?', '>',
		'<', 'V', 'a', 'l', 'C', 'u', 'r', 's', '>',
		'<', 'V', 'a', 'l', 'u', 't', 'e', '>',
		'<', 'C', 'h', 'a', 'r', 'C', 'o', 'd', 'e', '>', 'U', 'S', 'D', '<', '/', 'C', 'h', 'a', 'r', 'C', 'o', 'd', 'e', '>',
		'<', 'N', 'o', 'm', 'i', 'n', 'a', 'l', '>', '1', '<', '/', 'N', 'o', 'm', 'i', 'n', 'a', 'l', '>',
		'<', 'N', 'a', 'm', 'e', '>', 0xc4, 0xee, 0xeb, 0xeb, 0xe0, 0xf0, '<', '/', 'N', 'a', 'm', 'e', '>',
		'<', 'V', 'a', 'l', 'u', 'e', '>', '9', '0', ',', '1', '2', '3', '4', '<', '/', 'V', 'a', 'l', 'u', 'e', '>',
		'<', '/', 'V', 'a', 'l', 'u', 't', 'e', '>',
		'<', '/', 'V', 'a', 'l', 'C', 'u', 'r', 's', '>',
	})

	var parsed cbrValCurs
	decoder := xml.NewDecoder(strings.NewReader(body))
	decoder.CharsetReader = charsetReader
	if err := decoder.Decode(&parsed); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(parsed.Valutes) != 1 {
		t.Fatalf("decoded %d valutes, want 1", len(parsed.Valutes))
	}
	if parsed.Valutes[0].Name != "Доллар" {
		t.Fatalf("Name = %q, want %q", parsed.Valutes[0].Name, "Доллар")
	}
}

func TestProviderFallsBackToNextSourceURL(t *testing.T) {
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer failed.Close()

	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<ValCurs>
	<Valute>
		<CharCode>USD</CharCode>
		<Nominal>1</Nominal>
		<Name>Доллар США</Name>
		<Value>90,1234</Value>
	</Valute>
</ValCurs>`))
	}))
	defer ok.Close()

	provider := NewProvider(failed.URL+","+ok.URL, t.TempDir()+"/rates.json", time.Hour)
	provider.fetchRetries = 1

	snapshot, err := provider.Get(context.Background())
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if snapshot.Source != ok.URL {
		t.Fatalf("Source = %q, want %q", snapshot.Source, ok.URL)
	}
	if snapshot.Rates["USD"].Value != 90.1234 {
		t.Fatalf("USD value = %v, want 90.1234", snapshot.Rates["USD"].Value)
	}
}
