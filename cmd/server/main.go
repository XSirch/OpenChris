package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"openchris/conselho/internal/httpapp"
	"openchris/conselho/internal/memory"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func loadConfig(getenv func(string) string) (httpapp.Config, string, error) {
	environment := getenv("APP_ENV")
	if environment != "" && environment != "development" && environment != "test" && environment != "production" {
		return httpapp.Config{}, "", errors.New("APP_ENV must be development, test or production")
	}
	production := environment == "production"
	cfg := httpapp.Config{Production: production, PublicURL: strings.TrimRight(getenv("PUBLIC_BASE_URL"), "/"), SessionTTL: 12 * time.Hour}
	dsn := getenv("DATABASE_URL")
	if production {
		cfg.Password = getenv("APP_PASSWORD")
		if dsn == "" {
			return cfg, "", errors.New("DATABASE_URL is required in production")
		}
		databaseConfig, err := pgx.ParseConfig(dsn)
		if err != nil {
			return cfg, "", errors.New("DATABASE_URL must be a valid PostgreSQL connection string")
		}
		if databaseConfig.Password == "synthetic-only" || databaseConfig.Database == "conselho_demo" {
			return cfg, "", errors.New("DATABASE_URL must use a separate database and unique credentials")
		}
	} else {
		withDefault := func(key, fallback string) string {
			if value := getenv(key); value != "" {
				return value
			}
			return fallback
		}
		cfg.Demo = withDefault("DEMO_ONLY", "true") == "true"
		cfg.Password = withDefault("APP_PASSWORD", withDefault("DEMO_PASSWORD", "demo-conselho"))
		cfg.TelegramSecret = withDefault("TELEGRAM_SECRET", "synthetic-webhook")
		cfg.BenchToken = withDefault("BENCH_TOKEN", "stack-benchmark-local")
		dsn = withDefault("DATABASE_URL", "postgres://conselho:synthetic-only@127.0.0.1:18432/conselho_demo?sslmode=disable")
	}
	telegramEnabled, err := integrationEnabled(getenv("TELEGRAM_ENABLED"))
	if err != nil {
		return cfg, "", errors.New("TELEGRAM_ENABLED must be true or false")
	}
	pluggyEnabled, err := integrationEnabled(getenv("PLUGGY_ENABLED"))
	if err != nil {
		return cfg, "", errors.New("PLUGGY_ENABLED must be true or false")
	}
	cfg.Telegram.Enabled = telegramEnabled
	cfg.Telegram.BotToken = getenv("TELEGRAM_BOT_TOKEN")
	cfg.Telegram.WebhookSecret = getenv("TELEGRAM_WEBHOOK_SECRET")
	cfg.Telegram.PublicURL = cfg.PublicURL + "/webhooks/telegram"
	cfg.Telegram.AllowedUserID, err = integrationIdentity(getenv("TELEGRAM_ALLOWED_USER_ID"), telegramEnabled)
	if err != nil {
		return cfg, "", errors.New("TELEGRAM_ALLOWED_USER_ID must be a positive integer")
	}
	cfg.Telegram.AllowedChatID, err = integrationIdentity(getenv("TELEGRAM_ALLOWED_CHAT_ID"), telegramEnabled)
	if err != nil {
		return cfg, "", errors.New("TELEGRAM_ALLOWED_CHAT_ID must be a positive integer")
	}
	cfg.Pluggy.Enabled = pluggyEnabled
	cfg.Pluggy.ClientID = getenv("PLUGGY_CLIENT_ID")
	cfg.Pluggy.ClientSecret = getenv("PLUGGY_CLIENT_SECRET")
	cfg.Pluggy.WebhookSecret = getenv("PLUGGY_WEBHOOK_SECRET")
	cfg.Pluggy.PublicURL = cfg.PublicURL + "/webhooks/pluggy"
	cfg.Pluggy.SyncInterval = 6 * time.Hour
	cfg.Codex.Enabled, err = integrationEnabled(getenv("CODEX_ENABLED"))
	if err != nil {
		return cfg, "", errors.New("CODEX_ENABLED must be true or false")
	}
	cfg.Codex.RunnerURL = getenv("CODEX_RUNNER_URL")
	cfg.Codex.RunnerToken = getenv("CODEX_RUNNER_TOKEN")
	cfg.Codex.PublicURL = cfg.PublicURL
	cfg.Codex.DailyLimit = 30
	if raw := getenv("CODEX_DAILY_LIMIT"); raw != "" {
		cfg.Codex.DailyLimit, err = strconv.Atoi(raw)
		if err != nil || cfg.Codex.DailyLimit < 1 || cfg.Codex.DailyLimit > 30 {
			return cfg, "", errors.New("CODEX_DAILY_LIMIT must be between 1 and 30")
		}
	}
	cfg.AutonomyEnabled, err = integrationEnabled(getenv("AUTONOMY_ENABLED"))
	if err != nil {
		return cfg, "", errors.New("AUTONOMY_ENABLED must be true or false")
	}
	cfg.AutonomyDailyLimit = 20
	if raw := getenv("AUTONOMY_DAILY_LIMIT"); raw != "" {
		cfg.AutonomyDailyLimit, err = strconv.Atoi(raw)
		if err != nil || cfg.AutonomyDailyLimit < 1 || cfg.AutonomyDailyLimit > 20 {
			return cfg, "", errors.New("AUTONOMY_DAILY_LIMIT must be between 1 and 20")
		}
	}
	if raw := getenv("PLUGGY_SYNC_INTERVAL"); raw != "" {
		interval, parseErr := time.ParseDuration(raw)
		if parseErr != nil || interval < time.Hour || interval > 24*time.Hour {
			return cfg, "", errors.New("PLUGGY_SYNC_INTERVAL must be between 1h and 24h")
		}
		cfg.Pluggy.SyncInterval = interval
	}
	if err := cfg.Validate(); err != nil {
		return cfg, "", err
	}
	return cfg, dsn, nil
}

func integrationEnabled(value string) (bool, error) {
	switch strings.TrimSpace(value) {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, errors.New("invalid integration switch")
	}
}
func integrationIdentity(value string, enabled bool) (int64, error) {
	if !enabled {
		return 0, nil
	}
	id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid private identity")
	}
	return id, nil
}

func main() {
	cfg, dsn, err := loadConfig(os.Getenv)
	if err != nil {
		slog.Error("configuration rejected", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 15*time.Second)
	store, err := memory.Open(startup, dsn)
	cancel()
	if err != nil {
		slog.Error("database startup failed", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	application := httpapp.New(store, cfg)
	go application.RunIntegrations(ctx, func(err error) { slog.Error("integration worker failed", "code", "integration_failed") })
	server := &http.Server{Addr: env("LISTEN_ADDR", "127.0.0.1:18480"), Handler: application, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 8192}
	if cfg.Demo && !cfg.Production {
		go store.Run(ctx, func(err error) { slog.Error("synthetic worker failed", "code", "processor_failed") })
	}
	go func() {
		mode := "development"
		if cfg.Production {
			mode = "production"
		}
		slog.Info("server ready", "addr", server.Addr, "mode", mode)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http server failed", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	server.Shutdown(shutdown)
}
