package main

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"openchris/conselho/internal/codex"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "health" {
		client := http.Client{Timeout: 2 * time.Second}
		response, err := client.Get("http://127.0.0.1:8081/healthz")
		if err != nil {
			os.Exit(1)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	key, err := base64.StdEncoding.DecodeString(os.Getenv("CODEX_AUTH_KEY"))
	if err != nil || len(key) != 32 {
		slog.Error("runner configuration rejected", "code", "invalid_key")
		os.Exit(1)
	}
	binary := os.Getenv("CODEX_BIN")
	if binary == "" {
		binary = "/usr/local/bin/codex"
	}
	state := os.Getenv("CODEX_STATE_DIR")
	if state == "" {
		state = "/state"
	}
	runner, err := codex.NewRunner(codex.RunnerConfig{Binary: binary, StateDir: state, TempDir: os.TempDir(), Key: key, Token: os.Getenv("CODEX_RUNNER_TOKEN")})
	if err != nil {
		slog.Error("runner startup rejected", "code", codex.Code(err))
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(os.Args) > 1 && os.Args[1] == "recover" {
		if err = runner.Recover(ctx); err != nil {
			slog.Error("incident recovery failed", "code", codex.Code(err))
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "login" {
		if err = runner.Login(ctx, os.Stdin, os.Stdout); err != nil {
			slog.Error("account login failed", "code", codex.Code(err))
			os.Exit(1)
		}
		return
	}
	server := &http.Server{Addr: ":8081", Handler: runner, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 190 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("runner listener failed", "code", "listener_failed")
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(shutdown)
}
