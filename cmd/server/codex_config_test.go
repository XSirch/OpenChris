package main

import (
	"strings"
	"testing"
)

func TestCodexConfigurationRequiresPrivateRunnerAndBoundedQuota(t *testing.T) {
	values := map[string]string{"APP_ENV": "production", "APP_PASSWORD": "production-private-codex-test", "PUBLIC_BASE_URL": "https://conselho.example.test", "DATABASE_URL": "postgres://owner:private-test@localhost/conselho?sslmode=disable", "CODEX_ENABLED": "true", "CODEX_RUNNER_URL": "http://conselho-codex:8081", "CODEX_RUNNER_TOKEN": strings.Repeat("x", 40)}
	cfg, _, err := loadConfig(func(k string) string { return values[k] })
	if err != nil || !cfg.Codex.Enabled || cfg.Codex.DailyLimit != 30 {
		t.Fatal(cfg.Codex, err)
	}
	for _, bad := range []map[string]string{{"CODEX_ENABLED": "maybe"}, {"CODEX_RUNNER_TOKEN": ""}, {"CODEX_RUNNER_URL": "http://user:password@runner"}, {"CODEX_DAILY_LIMIT": "31"}, {"CODEX_DAILY_LIMIT": "0"}, {"APP_ENV": "development"}} {
		if _, _, err := loadConfig(func(k string) string {
			if v, ok := bad[k]; ok {
				return v
			}
			return values[k]
		}); err == nil {
			t.Fatal("accepted invalid Codex configuration", bad)
		}
	}
}
