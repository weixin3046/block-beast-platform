package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/block-beast/platform/internal/application/outbox"
	"github.com/block-beast/platform/internal/application/settlement"
	"github.com/block-beast/platform/internal/config"
	"github.com/block-beast/platform/internal/domain/events"
	"github.com/block-beast/platform/internal/domain/game"
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
	roundRepository := game.NewPostgresRepository(pool)
	settlementService := settlement.NewService(pool)
	resultSource := settlement.NewHashResultSource()
	ticker := time.NewTicker(cfg.WorkerPollInterval)
	defer ticker.Stop()
	logger.Info("worker started", "poll_interval", cfg.WorkerPollInterval)
	processDueRounds(ctx, logger, roundRepository)
	settleDueRounds(ctx, logger, settlementService, resultSource)
	processPending(logger, processor)

	for {
		select {
		case <-ctx.Done():
			logger.Info("worker stopped")
			return
		case <-ticker.C:
			processDueRounds(ctx, logger, roundRepository)
			settleDueRounds(ctx, logger, settlementService, resultSource)
			processPending(logger, processor)
		}
	}
}

type dueRoundCloser interface {
	CloseDue(ctx context.Context, now time.Time, limit int) ([]string, error)
}

func processDueRounds(ctx context.Context, logger *slog.Logger, repository dueRoundCloser) {
	closed, err := repository.CloseDue(ctx, time.Now().UTC(), 100)
	if err != nil {
		logger.Error("due round closure failed", "error", err)
		return
	}
	if len(closed) > 0 {
		logger.Info("due rounds closed", "count", len(closed))
	}
}

func settleDueRounds(ctx context.Context, logger *slog.Logger, service *settlement.Service, source settlement.ResultSource) {
	settled, err := service.SettleDueRounds(ctx, source, 100)
	for _, item := range settled {
		logger.Info("round settled",
			"round_id", item.RoundID,
			"outcome", item.Result.Outcome,
			"won_bets", item.Result.WonBetCount,
			"lost_bets", item.Result.LostBetCount,
			"payout_minor", item.Result.PayoutMinor)
	}
	if err != nil {
		logger.Error("due round settlement failed", "settled", len(settled), "error", err)
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
