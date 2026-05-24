package config

import (
	"path/filepath"
	"testing"
)

func TestConfigIsAllowedAllowsEveryoneWhenWhitelistEmpty(t *testing.T) {
	cfg := Config{AllowedUsers: map[int64]struct{}{}}
	if !cfg.IsAllowed(42) {
		t.Fatal("IsAllowed() = false, want true for empty whitelist")
	}
}

func TestConfigIsAllowedChecksWhitelistWhenConfigured(t *testing.T) {
	cfg := Config{AllowedUsers: map[int64]struct{}{42: {}}}
	if !cfg.IsAllowed(42) {
		t.Fatal("IsAllowed(42) = false, want true")
	}
	if cfg.IsAllowed(7) {
		t.Fatal("IsAllowed(7) = true, want false")
	}
}

func TestConfigIsAllowedAllowsAdmins(t *testing.T) {
	cfg := Config{AdminUsers: map[int64]struct{}{42: {}}, AllowedUsers: map[int64]struct{}{}}
	if !cfg.IsAllowed(42) {
		t.Fatal("IsAllowed(42) = false, want true for admin")
	}
	if cfg.IsAllowed(7) {
		t.Fatal("IsAllowed(7) = true, want false when admins enable restricted mode")
	}
}

func TestLoadSubscriptionDefaults(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("TELEGRAM_ADMIN_USER_IDS", "")
	t.Setenv("TELEGRAM_ALLOWED_USER_IDS", "")
	t.Setenv("RATES_CACHE_TTL", "")
	t.Setenv("ALLOWED_USERS_FILE", "")
	t.Setenv("SUBSCRIPTIONS_FILE", "")
	t.Setenv("SUBSCRIPTION_TIMEZONE", "")

	cfg, err := Load(filepath.Join(t.TempDir(), ".env"))
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.SubscriptionsFile != "data/subscriptions.json" {
		t.Fatalf("SubscriptionsFile = %q, want data/subscriptions.json", cfg.SubscriptionsFile)
	}
	if cfg.AllowedUsersFile != "data/allowed_users.json" {
		t.Fatalf("AllowedUsersFile = %q, want data/allowed_users.json", cfg.AllowedUsersFile)
	}
	if cfg.SubscriptionTimezone != "Asia/Tashkent" {
		t.Fatalf("SubscriptionTimezone = %q, want Asia/Tashkent", cfg.SubscriptionTimezone)
	}
}
