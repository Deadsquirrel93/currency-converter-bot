package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	TelegramToken    string
	AllowedUsers     map[int64]struct{}
	DefaultFrom      string
	DefaultTo        string
	CacheFile        string
	UserSettingsFile string
	CacheTTL         time.Duration
	CBRDailyURL      string
	TelegramAPI      string
}

func Load(path string) (Config, error) {
	_ = loadDotEnv(path)

	cfg := Config{
		TelegramToken:    strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
		AllowedUsers:     map[int64]struct{}{},
		DefaultFrom:      upperOrDefault(os.Getenv("DEFAULT_FROM"), "USD"),
		DefaultTo:        upperOrDefault(os.Getenv("DEFAULT_TO"), "RUB"),
		CacheFile:        valueOrDefault(os.Getenv("RATES_CACHE_FILE"), "data/rates_cache.json"),
		UserSettingsFile: valueOrDefault(os.Getenv("USER_SETTINGS_FILE"), "data/user_settings.json"),
		CBRDailyURL:      valueOrDefault(os.Getenv("CBR_DAILY_URL"), "https://www.cbr.ru/scripts/XML_daily.asp"),
		TelegramAPI:      strings.TrimRight(valueOrDefault(os.Getenv("TELEGRAM_API_BASE"), "https://api.telegram.org"), "/"),
	}

	if cfg.TelegramToken == "" {
		return Config{}, errors.New("TELEGRAM_BOT_TOKEN is required")
	}

	users, err := parseAllowedUsers(os.Getenv("TELEGRAM_ALLOWED_USER_IDS"))
	if err != nil {
		return Config{}, err
	}
	if len(users) == 0 {
		return Config{}, errors.New("TELEGRAM_ALLOWED_USER_IDS must contain at least one telegram user id")
	}
	cfg.AllowedUsers = users

	ttlRaw := strings.TrimSpace(os.Getenv("RATES_CACHE_TTL"))
	if ttlRaw == "" {
		cfg.CacheTTL = time.Hour
	} else {
		ttl, err := time.ParseDuration(ttlRaw)
		if err != nil {
			return Config{}, fmt.Errorf("parse RATES_CACHE_TTL: %w", err)
		}
		cfg.CacheTTL = ttl
	}

	return cfg, nil
}

func (c Config) IsAllowed(userID int64) bool {
	_, ok := c.AllowedUsers[userID]
	return ok
}

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key != "" {
			_ = os.Setenv(key, value)
		}
	}

	return scanner.Err()
}

func parseAllowedUsers(raw string) (map[int64]struct{}, error) {
	result := map[int64]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse TELEGRAM_ALLOWED_USER_IDS value %q: %w", part, err)
		}
		result[id] = struct{}{}
	}
	return result, nil
}

func valueOrDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func upperOrDefault(value, fallback string) string {
	return strings.ToUpper(valueOrDefault(value, fallback))
}
