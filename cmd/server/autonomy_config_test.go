package main

import (
	"strings"
	"testing"
)

func TestAutonomyRequiresProductionCodexAndExplicitBoolean(t *testing.T) {
	for _, flag := range []string{"true", "maybe"} {
		values := map[string]string{"APP_ENV": "production", "DATABASE_URL": "postgres://private@database/conselho", "APP_PASSWORD": "private-test-password-123456", "PUBLIC_BASE_URL": "https://chris.example.test", "AUTONOMY_ENABLED": flag}
		if _, _, err := loadConfig(func(key string) string { return values[key] }); err == nil {
			t.Fatalf("autonomy %q accepted without a configured Codex runner", flag)
		}
	}
	if _, _, err := loadConfig(func(key string) string {
		if key == "AUTONOMY_ENABLED" {
			return "true"
		}
		return ""
	}); err == nil {
		t.Fatal("autonomy accepted in development")
	}
}

func TestAutonomyAttemptLimitCannotExhaustOwnerCapacity(t *testing.T) {
	for _, limit := range []string{"0", "-1", "21", "30", "invalid"} {
		values := map[string]string{"APP_ENV": "production", "DATABASE_URL": "postgres://private@database/conselho", "APP_PASSWORD": "private-test-password-123456", "PUBLIC_BASE_URL": "https://chris.example.test", "CODEX_ENABLED": "true", "CODEX_RUNNER_URL": "http://runner:8081", "CODEX_RUNNER_TOKEN": strings.Repeat("s", 40), "AUTONOMY_ENABLED": "true", "AUTONOMY_DAILY_LIMIT": limit}
		if _, _, err := loadConfig(func(key string) string { return values[key] }); err == nil {
			t.Fatalf("unsafe automatic limit %q accepted", limit)
		}
	}
}
