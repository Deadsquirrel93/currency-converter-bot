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

func TestParseUserIDsAllowsCommonSeparatorsAndInlineComments(t *testing.T) {
	ids, err := parseUserIDs("TELEGRAM_ADMIN_USER_IDS", "42, 7;9 | 11 # admins")
	if err != nil {
		t.Fatalf("parseUserIDs(): %v", err)
	}
	for _, id := range []int64{42, 7, 9, 11} {
		if _, ok := ids[id]; !ok {
			t.Fatalf("parseUserIDs() missing %d in %#v", id, ids)
		}
	}
	if len(ids) != 4 {
		t.Fatalf("parseUserIDs() returned %d ids, want 4: %#v", len(ids), ids)
	}
}

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
