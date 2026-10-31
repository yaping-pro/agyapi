package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yaping-pro/agyapi/internal/config"
	"github.com/yaping-pro/agyapi/internal/server"
)

func main() {
	cfg := config.Load()
	flag.Parse()

	logLevel := slog.LevelInfo
	if cfg.Verbose {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})))

	slog.Info("Starting agyapi service",
		"host", cfg.Host,
		"port", cfg.Port,
		"backend", cfg.Backend,
		"default_model", cfg.DefaultModel,
		"max_concurrency", cfg.MaxConcurrency,
	)

	if cfg.Backend == "native" {
		slog.Info("native backend configured",
			"token_path", cfg.TokenPath,
			"project", cfg.Project,
			"upstream", cfg.UpstreamURL,
		)
		if _, err := os.Stat(cfg.TokenPath); err != nil {
			slog.Warn("oauth token file not found; native requests will fail until authenticated", "path", cfg.TokenPath, "error", err)
		}
	} else {
		slog.Info("print backend configured", "agy_path", cfg.AgyPath)
		if _, err := os.Stat(cfg.AgyPath); err != nil {
			slog.Warn("agy binary not found or inaccessible at configured path", "path", cfg.AgyPath, "error", err)
		} else {
			slog.Info("agy binary verified at path", "path", cfg.AgyPath)
		}
	}

	srv := server.NewServer(cfg)

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutting down server gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}
	slog.Info("Server stopped")
}
