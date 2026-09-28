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
	TelegramToken        string
	AdminUsers           map[int64]struct{}
	AllowedUsers         map[int64]struct{}
	DefaultFrom          string
	DefaultTo            string
	CacheFile            string
	UserSettingsFile     string
	AllowedUsersFile     string
	SubscriptionsFile    string
	SubscriptionTimezone string
	CacheTTL             time.Duration
	CBRDailyURL          string
	TelegramAPI          string
}

func Load(path string) (Config, error) {
	_ = loadDotEnv(path)

	cfg := Config{
		TelegramToken:        strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
		AdminUsers:           map[int64]struct{}{},
		AllowedUsers:         map[int64]struct{}{},
		DefaultFrom:          upperOrDefault(os.Getenv("DEFAULT_FROM"), "USD"),
		DefaultTo:            upperOrDefault(os.Getenv("DEFAULT_TO"), "RUB"),
		CacheFile:            valueOrDefault(os.Getenv("RATES_CACHE_FILE"), "data/rates_cache.json"),
		UserSettingsFile:     valueOrDefault(os.Getenv("USER_SETTINGS_FILE"), "data/user_settings.json"),
		AllowedUsersFile:     valueOrDefault(os.Getenv("ALLOWED_USERS_FILE"), "data/allowed_users.json"),
		SubscriptionsFile:    valueOrDefault(os.Getenv("SUBSCRIPTIONS_FILE"), "data/subscriptions.json"),
		SubscriptionTimezone: valueOrDefault(os.Getenv("SUBSCRIPTION_TIMEZONE"), "Asia/Tashkent"),
		CBRDailyURL:          valueOrDefault(os.Getenv("CBR_DAILY_URLS"), valueOrDefault(os.Getenv("CBR_DAILY_URL"), "https://www.cbr.ru/scripts/XML_daily.asp")),
		TelegramAPI:          strings.TrimRight(valueOrDefault(os.Getenv("TELEGRAM_API_BASE"), "https://api.telegram.org"), "/"),
	}

	if cfg.TelegramToken == "" {
		return Config{}, errors.New("TELEGRAM_BOT_TOKEN is required")
	}

	if err := validateTimezone(cfg.SubscriptionTimezone); err != nil {
		return Config{}, err
	}

	admins, err := parseUserIDs("TELEGRAM_ADMIN_USER_IDS", os.Getenv("TELEGRAM_ADMIN_USER_IDS"))
	if err != nil {
		return Config{}, err
	}
	cfg.AdminUsers = admins

	users, err := parseUserIDs("TELEGRAM_ALLOWED_USER_IDS", os.Getenv("TELEGRAM_ALLOWED_USER_IDS"))
	if err != nil {
		return Config{}, err
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

func (c Config) IsAdmin(userID int64) bool {
	_, ok := c.AdminUsers[userID]
	return ok
}

func (c Config) IsAllowed(userID int64) bool {
	if c.IsAdmin(userID) {
		return true
	}
	if len(c.AllowedUsers) == 0 && len(c.AdminUsers) == 0 {
		return true
	}
	_, ok := c.AllowedUsers[userID]
	return ok
}

// validateTimezone fails fast on a mistyped SUBSCRIPTION_TIMEZONE instead of
// letting subscriptions silently fire in UTC.
func validateTimezone(name string) error {
	if strings.EqualFold(name, "local") {
		return nil
	}
	if _, err := time.LoadLocation(name); err != nil {
		return fmt.Errorf("parse SUBSCRIPTION_TIMEZONE: %w", err)
	}
	return nil
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

func parseUserIDs(envName, raw string) (map[int64]struct{}, error) {
	result := map[int64]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse %s value %q: %w", envName, part, err)
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
