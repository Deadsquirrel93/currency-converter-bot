package config

import "testing"

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
