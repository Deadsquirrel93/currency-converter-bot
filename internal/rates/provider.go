package rates

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
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
	client    *http.Client
	sourceURL string
	cacheFile string
	cacheTTL  time.Duration
}

func NewProvider(sourceURL, cacheFile string, cacheTTL time.Duration) *Provider {
	return &Provider{
		client:    &http.Client{Timeout: 15 * time.Second},
		sourceURL: sourceURL,
		cacheFile: cacheFile,
		cacheTTL:  cacheTTL,
	}
}

func (p *Provider) Get(ctx context.Context) (Snapshot, error) {
	if cached, ok := p.readFreshCache(); ok {
		return cached, nil
	}

	snapshot, err := p.fetchCBR(ctx)
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

func (p *Provider) fetchCBR(ctx context.Context) (Snapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.sourceURL, nil)
	if err != nil {
		return Snapshot{}, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return Snapshot{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Snapshot{}, fmt.Errorf("CBR request failed: %s", resp.Status)
	}

	var parsed cbrValCurs
	if err := xml.NewDecoder(resp.Body).Decode(&parsed); err != nil {
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
		Source:    p.sourceURL,
		Rates:     rates,
	}, nil
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
