package rates

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
	setFetchRetries(t, 1)

	snapshot, err := provider.Get(context.Background())
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if snapshot.Rates["USD"].Value != 90.1234 {
		t.Fatalf("USD value = %v, want 90.1234", snapshot.Rates["USD"].Value)
	}
}

func TestProviderGetsSnapshotForDate(t *testing.T) {
	var gotDateReq string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotDateReq = r.URL.Query().Get("date_req")
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<ValCurs>
	<Valute>
		<CharCode>USD</CharCode>
		<Nominal>1</Nominal>
		<Name>Доллар США</Name>
		<Value>91,2500</Value>
	</Valute>
</ValCurs>`))
	}))
	defer server.Close()

	provider := NewProvider(server.URL+"?existing=1", t.TempDir()+"/rates.json", time.Hour)
	setFetchRetries(t, 1)

	snapshot, err := provider.GetForDate(context.Background(), time.Date(2026, 5, 10, 14, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("GetForDate() error = %v", err)
	}
	if gotDateReq != "10/05/2026" {
		t.Fatalf("date_req = %q, want 10/05/2026", gotDateReq)
	}
	if snapshot.Rates["USD"].Value != 91.25 {
		t.Fatalf("USD value = %v, want 91.25", snapshot.Rates["USD"].Value)
	}
}

func TestGetForDateCachesPastDatesOnly(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<ValCurs><Valute><CharCode>USD</CharCode><Nominal>1</Nominal><Name>USD</Name><Value>90,5</Value></Valute></ValCurs>`))
	}))
	defer server.Close()

	provider := NewProvider(server.URL, t.TempDir()+"/rates.json", time.Hour)
	past := time.Now().AddDate(0, 0, -3)
	for range 3 {
		if _, err := provider.GetForDate(t.Context(), past); err != nil {
			t.Fatalf("GetForDate(past): %v", err)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("past date fetched %d times, want 1 (cached)", got)
	}

	future := time.Now().AddDate(0, 0, 1)
	for range 2 {
		if _, err := provider.GetForDate(t.Context(), future); err != nil {
			t.Fatalf("GetForDate(future): %v", err)
		}
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("requests = %d, want future date fetched every time", got)
	}
}

func TestHistoryCacheIsBounded(t *testing.T) {
	provider := NewProvider("http://unused", t.TempDir()+"/rates.json", time.Hour)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range maxHistoryEntries + 10 {
		provider.rememberHistory(start.AddDate(0, 0, i).Format("2006-01-02"), Snapshot{})
	}
	if len(provider.history) != maxHistoryEntries {
		t.Fatalf("history size = %d, want %d", len(provider.history), maxHistoryEntries)
	}
	if _, ok := provider.history["2026-01-01"]; ok {
		t.Fatal("oldest entries must be evicted first")
	}
}

func TestFetchLatestReadsRateDateAndBypassesCache(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		date, value := "28.09.2026", "81,0000"
		if n > 1 {
			date, value = "29.09.2026", "81,5000"
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<ValCurs Date="` + date + `" name="Foreign Currency Market"><Valute><CharCode>USD</CharCode><Nominal>1</Nominal><Name>USD</Name><Value>` + value + `</Value></Valute></ValCurs>`))
	}))
	defer server.Close()

	provider := NewProvider(server.URL, t.TempDir()+"/rates.json", time.Hour)
	setFetchRetries(t, 1)

	first, err := provider.Get(context.Background())
	if err != nil || first.Date != "2026-09-28" {
		t.Fatalf("Get() = %+v, %v, want date 2026-09-28", first, err)
	}
	latest, err := provider.FetchLatest(context.Background())
	if err != nil || latest.Date != "2026-09-29" || latest.Rates["USD"].Value != 81.5 {
		t.Fatalf("FetchLatest() = %+v, %v, want the fresh 2026-09-29 document", latest, err)
	}
	cached, err := provider.Get(context.Background())
	if err != nil || cached.Date != "2026-09-29" || calls.Load() != 2 {
		t.Fatalf("Get() after FetchLatest = %+v, %v (calls %d), want the refreshed cache", cached, err, calls.Load())
	}
}

func TestParseCBRDate(t *testing.T) {
	for raw, want := range map[string]string{"29.09.2026": "2026-09-29", " 01.01.2027 ": "2027-01-01", "": "", "2026-09-29": ""} {
		if got := parseCBRDate(raw); got != want {
			t.Fatalf("parseCBRDate(%q) = %q, want %q", raw, got, want)
		}
	}
}

// setFetchRetries lowers the retries for one test so failing sources do not
// sleep between attempts.
func setFetchRetries(t *testing.T, n int) {
	t.Helper()
	previous := fetchRetries
	fetchRetries = n
	t.Cleanup(func() { fetchRetries = previous })
}

func TestParseCBRDecimal(t *testing.T) {
	for raw, want := range map[string]float64{"90,1234": 90.1234, " 1,5\n": 1.5, "100": 100} {
		if got, err := parseCBRDecimal(raw); err != nil || got != want {
			t.Fatalf("parseCBRDecimal(%q) = %v, %v, want %v", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "90,12abc", "1,2,3", "abc"} {
		if _, err := parseCBRDecimal(raw); err == nil {
			t.Fatalf("parseCBRDecimal(%q) error = nil, want an error", raw)
		}
	}
}

func TestParseSourceURLs(t *testing.T) {
	got := parseSourceURLs(" https://a , ,https://b ")
	if len(got) != 2 || got[0] != "https://a" || got[1] != "https://b" {
		t.Fatalf("parseSourceURLs() = %q", got)
	}
}
