package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestParseUserIDsRejectsTextTokens(t *testing.T) {
	if _, err := parseUserIDs("TELEGRAM_ADMIN_USER_IDS", "42,admin"); err == nil {
		t.Fatal("parseUserIDs() error = nil, want error")
	}
}

func TestLoadSubscriptionDefaults(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("TELEGRAM_ADMIN_USER_IDS", "")
	t.Setenv("TELEGRAM_ALLOWED_USER_IDS", "")
	t.Setenv("RATES_CACHE_TTL", "")
	t.Setenv("ALLOWED_USERS_FILE", "")
	t.Setenv("SUBSCRIPTIONS_FILE", "")
	t.Setenv("NEW_RATE_SUBSCRIPTIONS_FILE", "")
	t.Setenv("ALERTS_FILE", "")
	t.Setenv("SUBSCRIPTION_TIMEZONE", "")

	cfg, err := Load(filepath.Join(t.TempDir(), ".env"))
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.SubscriptionsFile != "data/subscriptions.json" {
		t.Fatalf("SubscriptionsFile = %q, want data/subscriptions.json", cfg.SubscriptionsFile)
	}
	if cfg.NewRateSubsFile != "data/new_rate_subscriptions.json" {
		t.Fatalf("NewRateSubsFile = %q, want data/new_rate_subscriptions.json", cfg.NewRateSubsFile)
	}
	if cfg.AlertsFile != "data/alerts.json" {
		t.Fatalf("AlertsFile = %q, want data/alerts.json", cfg.AlertsFile)
	}
	if cfg.AllowedUsersFile != "data/allowed_users.json" {
		t.Fatalf("AllowedUsersFile = %q, want data/allowed_users.json", cfg.AllowedUsersFile)
	}
	if cfg.Location == nil || cfg.Location.String() != "Asia/Tashkent" {
		t.Fatalf("Location = %v, want Asia/Tashkent", cfg.Location)
	}
}

func TestLoadRejectsUnknownTimezone(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("TELEGRAM_ADMIN_USER_IDS", "")
	t.Setenv("TELEGRAM_ALLOWED_USER_IDS", "")
	t.Setenv("SUBSCRIPTION_TIMEZONE", "Asia/Tashkennt")

	if _, err := Load(filepath.Join(t.TempDir(), ".env")); err == nil {
		t.Fatal("Load() error = nil, want error for unknown timezone")
	}

	t.Setenv("SUBSCRIPTION_TIMEZONE", "local")
	cfg, err := Load(filepath.Join(t.TempDir(), ".env"))
	if err != nil {
		t.Fatalf("Load() with local timezone: %v", err)
	}
	if cfg.Location != time.Local {
		t.Fatalf("Location = %v, want time.Local", cfg.Location)
	}

	t.Setenv("SUBSCRIPTION_TIMEZONE", "Europe/Moscow")
	if cfg, err = Load(filepath.Join(t.TempDir(), ".env")); err != nil || cfg.Location.String() != "Europe/Moscow" {
		t.Fatalf("Load() = %v, %v, want Europe/Moscow", cfg.Location, err)
	}
}
