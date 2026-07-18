package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/block-beast/platform/internal/application/outbox"
	"github.com/block-beast/platform/internal/config"
	"github.com/block-beast/platform/internal/domain/events"
	"github.com/block-beast/platform/internal/platform/natsjs"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	cfg := config.Load()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, cfg.PostgresDSN)
	if err != nil {
		logger.Error("worker failed to connect to PostgreSQL", "error", err)
		return
	}
	defer pool.Close()
	publisher, err := natsjs.Connect(cfg.NATSURL)
	if err != nil {
		logger.Error("worker failed to connect to NATS", "error", err)
		return
	}
	defer publisher.Close()
	processor := outbox.NewProcessor(events.NewPostgresOutbox(pool), publisher)
	ticker := time.NewTicker(cfg.WorkerPollInterval)
	defer ticker.Stop()
	logger.Info("worker started", "poll_interval", cfg.WorkerPollInterval)
	processPending(logger, processor)

	for {
		select {
		case <-ctx.Done():
			logger.Info("worker stopped")
			return
		case <-ticker.C:
			processPending(logger, processor)
		}
	}
}

func processPending(logger *slog.Logger, processor *outbox.Processor) {
	published, err := processor.ProcessPending(100)
	if err != nil {
		logger.Error("outbox processing failed", "published", published, "error", err)
		return
	}
	if published > 0 {
		logger.Info("outbox events published", "count", published)
	}
}
