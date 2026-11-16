package main

import (
	"strings"
	"testing"
)

func TestProductionConfigurationHasNoLocalFallbacks(t *testing.T) {
	base := map[string]string{"APP_ENV": "production", "DATABASE_URL": "postgres://private@database/conselho", "APP_PASSWORD": "private-test-password-123456", "PUBLIC_BASE_URL": "https://chris.example.test/", "DEMO_ONLY": "true", "DEMO_PASSWORD": "demo-conselho", "TELEGRAM_SECRET": "unused", "BENCH_TOKEN": "unused"}
	for _, missing := range []string{"DATABASE_URL", "APP_PASSWORD", "PUBLIC_BASE_URL"} {
		t.Run(missing, func(t *testing.T) {
			_, _, err := loadConfig(func(key string) string {
				if key == missing {
					return ""
				}
				return base[key]
			})
			if err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("production silently used fallback for %s: %v", missing, err)
			}
		})
	}
	cfg, dsn, err := loadConfig(func(key string) string { return base[key] })
	if err != nil || !cfg.Production || cfg.Demo || cfg.TelegramSecret != "" || cfg.BenchToken != "" || dsn != base["DATABASE_URL"] || cfg.PublicURL != "https://chris.example.test" {
		t.Fatalf("production configuration: %+v %q %v", cfg, dsn, err)
	}
}

func TestProductionRejectsDefaultCredentialsHTTPAndUnknownEnvironment(t *testing.T) {
	for _, changes := range []map[string]string{
		{"APP_PASSWORD": "demo-conselho"},
		{"APP_PASSWORD": "SUBSTITUA_POR_UMA_SENHA_ALEATORIA"},
		{"PUBLIC_BASE_URL": "http://chris.example.test"},
		{"APP_ENV": "prodution"},
	} {
		values := map[string]string{"APP_ENV": "production", "DATABASE_URL": "postgres://private@database/conselho", "APP_PASSWORD": "private-test-password-123456", "PUBLIC_BASE_URL": "https://chris.example.test"}
		for key, value := range changes {
			values[key] = value
		}
		if _, _, err := loadConfig(func(key string) string { return values[key] }); err == nil {
			t.Fatalf("unsafe configuration accepted: %v", changes)
		}
	}
}

func TestProductionRejectsExplicitDemoDatabase(t *testing.T) {
	for _, dsn := range []string{
		"postgres://conselho:synthetic-only@127.0.0.1:18432/conselho_demo?sslmode=disable",
		"postgres://conselho:synthetic-only@database/conselho_private",
		"postgres://private:unique-password@database/conselho_demo",
	} {
		values := map[string]string{"APP_ENV": "production", "DATABASE_URL": dsn, "APP_PASSWORD": "private-test-password-123456", "PUBLIC_BASE_URL": "https://chris.example.test"}
		if _, _, err := loadConfig(func(key string) string { return values[key] }); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
			t.Fatalf("production accepted a demonstration database: %v", err)
		}
	}
}

func TestDevelopmentKeepsExplicitlyLocalFixtures(t *testing.T) {
	cfg, dsn, err := loadConfig(func(string) string { return "" })
	if err != nil || cfg.Production || !cfg.Demo || cfg.Password != "demo-conselho" || cfg.TelegramSecret != "synthetic-webhook" || !strings.Contains(dsn, "127.0.0.1:18432/conselho_demo") {
		t.Fatalf("local development configuration: %+v %q %v", cfg, dsn, err)
	}
}
