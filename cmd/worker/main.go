package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/block-beast/platform/internal/config"
)

func main() {
	cfg := config.Load()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ticker := time.NewTicker(cfg.WorkerPollInterval)
	defer ticker.Stop()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger.Info("worker started", "poll_interval", cfg.WorkerPollInterval)

	for {
		select {
		case <-ctx.Done():
			logger.Info("worker stopped")
			return
		case <-ticker.C:
			logger.Debug("worker tick", "tasks", []string{"settlement", "rebate", "ranking", "deposit-confirmation", "outbox"})
		}
	}
}
