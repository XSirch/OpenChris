package main

import (
	"strings"
	"testing"
)

func TestEnabledRealIntegrationsRequireCompleteConfiguration(t *testing.T) {
	base := map[string]string{
		"APP_ENV": "production", "APP_PASSWORD": "production-config-private-test",
		"PUBLIC_BASE_URL": "https://chris.example.test",
		"DATABASE_URL":    "postgres://owner:unique-test-password@localhost/conselho?sslmode=disable",
	}
	for _, flags := range []map[string]string{
		{"TELEGRAM_ENABLED": "true"}, {"PLUGGY_ENABLED": "true"},
		{"TELEGRAM_ENABLED": "perhaps"}, {"PLUGGY_ENABLED": "yes"},
		{"TELEGRAM_ENABLED": "true", "TELEGRAM_BOT_TOKEN": "123:test", "TELEGRAM_ALLOWED_USER_ID": "abc"},
	} {
		lookup := func(k string) string {
			if v, ok := flags[k]; ok {
				return v
			}
			return base[k]
		}
		if _, _, err := loadConfig(lookup); err == nil {
			t.Errorf("incomplete or malformed integration configuration accepted: %v", flags)
		}
	}
}
func TestDisabledRealIntegrationsDoNotBlockManualCore(t *testing.T) {
	values := map[string]string{"APP_ENV": "production", "APP_PASSWORD": "production-config-private-test", "PUBLIC_BASE_URL": "https://chris.example.test", "DATABASE_URL": "postgres://owner:unique-test-password@localhost/conselho?sslmode=disable", "TELEGRAM_ENABLED": "false", "PLUGGY_ENABLED": "false"}
	if _, _, err := loadConfig(func(k string) string { return values[k] }); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteRealIntegrationConfigurationBuildsExpectedWebhooks(t *testing.T) {
	values := map[string]string{
		"APP_ENV": "production", "APP_PASSWORD": "production-config-private-test",
		"PUBLIC_BASE_URL":  "https://chris.example.test/",
		"DATABASE_URL":     "postgres://owner:unique-test-password@localhost/conselho?sslmode=disable",
		"TELEGRAM_ENABLED": "true", "TELEGRAM_BOT_TOKEN": "123456789:synthetic_test_token",
		"TELEGRAM_ALLOWED_USER_ID": "456", "TELEGRAM_ALLOWED_CHAT_ID": "456",
		"TELEGRAM_WEBHOOK_SECRET": strings.Repeat("t", 40),
		"PLUGGY_ENABLED":          "true", "PLUGGY_CLIENT_ID": "synthetic-client",
		"PLUGGY_CLIENT_SECRET": "synthetic-secret", "PLUGGY_WEBHOOK_SECRET": strings.Repeat("p", 40),
		"PLUGGY_SYNC_INTERVAL": "6h",
	}
	cfg, _, err := loadConfig(func(k string) string { return values[k] })
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Telegram.Enabled || !cfg.Pluggy.Enabled || cfg.Demo || cfg.Telegram.AllowedUserID != 456 || cfg.Telegram.AllowedChatID != 456 {
		t.Fatal("real configuration not enabled as requested")
	}
	if cfg.Telegram.PublicURL != "https://chris.example.test/webhooks/telegram" || cfg.Pluggy.PublicURL != "https://chris.example.test/webhooks/pluggy" {
		t.Fatal("webhook origins do not match the public origin")
	}
}
