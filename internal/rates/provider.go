package rates

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Rate struct {
	Code    string  `json:"code"`
	Nominal int     `json:"nominal"`
	Value   float64 `json:"value"`
	Name    string  `json:"name,omitempty"`
}

type Snapshot struct {
	FetchedAt time.Time       `json:"fetched_at"`
	Source    string          `json:"source"`
	Rates     map[string]Rate `json:"rates"`
}

type Provider struct {
	client       *http.Client
	sourceURLs   []string
	cacheFile    string
	cacheTTL     time.Duration
	fetchRetries int
}

func NewProvider(sourceURL, cacheFile string, cacheTTL time.Duration) *Provider {
	return &Provider{
		client:       &http.Client{Timeout: 15 * time.Second},
		sourceURLs:   parseSourceURLs(sourceURL),
		cacheFile:    cacheFile,
		cacheTTL:     cacheTTL,
		fetchRetries: 3,
	}
}

func (p *Provider) Get(ctx context.Context) (Snapshot, error) {
	if cached, ok := p.readFreshCache(); ok {
		return cached, nil
	}

	snapshot, err := p.fetchAnyCBR(ctx)
	if err == nil {
		_ = p.writeCache(snapshot)
		return snapshot, nil
	}

	if cached, ok := p.readAnyCache(); ok {
		return cached, nil
	}

	return Snapshot{}, err
}

func Convert(amount float64, from, to string, snapshot Snapshot) (float64, error) {
	fromRate, ok := snapshot.Rates[strings.ToUpper(from)]
	if !ok {
		return 0, fmt.Errorf("unknown currency %s", from)
	}
	toRate, ok := snapshot.Rates[strings.ToUpper(to)]
	if !ok {
		return 0, fmt.Errorf("unknown currency %s", to)
	}

	amountRUB := amount * fromRate.Value / float64(fromRate.Nominal)
	return amountRUB / (toRate.Value / float64(toRate.Nominal)), nil
}

func (p *Provider) readFreshCache() (Snapshot, bool) {
	snapshot, ok := p.readAnyCache()
	if !ok {
		return Snapshot{}, false
	}
	if time.Since(snapshot.FetchedAt) > p.cacheTTL {
		return Snapshot{}, false
	}
	return snapshot, true
}

func (p *Provider) readAnyCache() (Snapshot, bool) {
	raw, err := os.ReadFile(p.cacheFile)
	if err != nil {
		return Snapshot{}, false
	}
	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return Snapshot{}, false
	}
	if len(snapshot.Rates) == 0 {
		return Snapshot{}, false
	}
	return snapshot, true
}

func (p *Provider) writeCache(snapshot Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(p.cacheFile), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.cacheFile, raw, 0o644)
}

func (p *Provider) fetchAnyCBR(ctx context.Context) (Snapshot, error) {
	var failures []string
	for _, sourceURL := range p.sourceURLs {
		for attempt := 1; attempt <= p.fetchRetries; attempt++ {
			snapshot, err := p.fetchCBR(ctx, sourceURL)
			if err == nil {
				return snapshot, nil
			}
			failures = append(failures, fmt.Sprintf("%s attempt %d: %v", sourceURL, attempt, err))
			if errorsIsContext(ctx.Err()) {
				return Snapshot{}, ctx.Err()
			}
			sleepBeforeRetry(ctx, attempt)
		}
	}
	return Snapshot{}, fmt.Errorf("all CBR sources failed: %s", strings.Join(failures, "; "))
}

func (p *Provider) fetchCBR(ctx context.Context, sourceURL string) (Snapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return Snapshot{}, err
	}
	req.Header.Set("User-Agent", "currency-converter-bot/1.0")
	resp, err := p.client.Do(req)
	if err != nil {
		return Snapshot{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Snapshot{}, fmt.Errorf("CBR request failed: %s", resp.Status)
	}

	var parsed cbrValCurs
	decoder := xml.NewDecoder(resp.Body)
	decoder.CharsetReader = charsetReader
	if err := decoder.Decode(&parsed); err != nil {
		return Snapshot{}, err
	}

	rates := map[string]Rate{
		"RUB": {Code: "RUB", Nominal: 1, Value: 1, Name: "Russian Ruble"},
	}
	for _, item := range parsed.Valutes {
		value, err := parseCBRDecimal(item.Value)
		if err != nil {
			return Snapshot{}, fmt.Errorf("parse %s rate: %w", item.CharCode, err)
		}
		rates[strings.ToUpper(item.CharCode)] = Rate{
			Code:    strings.ToUpper(item.CharCode),
			Nominal: item.Nominal,
			Value:   value,
			Name:    item.Name,
		}
	}

	return Snapshot{
		FetchedAt: time.Now().UTC(),
		Source:    sourceURL,
		Rates:     rates,
	}, nil
}

func parseSourceURLs(raw string) []string {
	var result []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	if len(result) == 0 {
		return []string{"https://www.cbr.ru/scripts/XML_daily.asp"}
	}
	return result
}

func errorsIsContext(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded
}

func sleepBeforeRetry(ctx context.Context, attempt int) {
	if attempt <= 0 {
		return
	}
	timer := time.NewTimer(time.Duration(attempt) * 300 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

type cbrValCurs struct {
	Valutes []cbrValute `xml:"Valute"`
}

type cbrValute struct {
	CharCode string `xml:"CharCode"`
	Nominal  int    `xml:"Nominal"`
	Name     string `xml:"Name"`
	Value    string `xml:"Value"`
}

func parseCBRDecimal(value string) (float64, error) {
	var result float64
	_, err := fmt.Sscan(strings.ReplaceAll(value, ",", "."), &result)
	return result, err
}

func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "windows-1251", "cp1251":
		raw, err := io.ReadAll(input)
		if err != nil {
			return nil, err
		}
		return strings.NewReader(decodeWindows1251(raw)), nil
	case "utf-8", "utf8":
		return input, nil
	default:
		return nil, fmt.Errorf("unsupported XML charset %q", charset)
	}
}

func decodeWindows1251(raw []byte) string {
	runes := make([]rune, 0, len(raw))
	for _, b := range raw {
		switch {
		case b < 0x80:
			runes = append(runes, rune(b))
		case b >= 0xC0:
			runes = append(runes, rune(0x0410+int(b)-0xC0))
		default:
			runes = append(runes, windows1251Table[b-0x80])
		}
	}
	return string(runes)
}

var windows1251Table = [...]rune{
	'\u0402', '\u0403', '\u201A', '\u0453', '\u201E', '\u2026', '\u2020', '\u2021',
	'\u20AC', '\u2030', '\u0409', '\u2039', '\u040A', '\u040C', '\u040B', '\u040F',
	'\u0452', '\u2018', '\u2019', '\u201C', '\u201D', '\u2022', '\u2013', '\u2014',
	'\u0098', '\u2122', '\u0459', '\u203A', '\u045A', '\u045C', '\u045B', '\u045F',
	'\u00A0', '\u040E', '\u045E', '\u0408', '\u00A4', '\u0490', '\u00A6', '\u00A7',
	'\u0401', '\u00A9', '\u0404', '\u00AB', '\u00AC', '\u00AD', '\u00AE', '\u0407',
	'\u00B0', '\u00B1', '\u0406', '\u0456', '\u0491', '\u00B5', '\u00B6', '\u00B7',
	'\u0451', '\u2116', '\u0454', '\u00BB', '\u0458', '\u0405', '\u0455', '\u0457',
}
