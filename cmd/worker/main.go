package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
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
	eventConsumer, err := natsjs.NewConsumer(cfg.NATSURL, natsjs.ConsumerConfig{Logger: logger})
	if err != nil {
		logger.Error("worker failed to start event consumer", "error", err)
		return
	}
	defer eventConsumer.Close()
	for _, subject := range []string{"game.>", "wallet.>", "chain.>"} {
		durable := "worker-" + strings.ReplaceAll(strings.TrimSuffix(subject, ".>"), ".", "-")
		if err := eventConsumer.Subscribe(subject, durable, logEvent(logger)); err != nil {
			logger.Error("worker failed to subscribe", "subject", subject, "error", err)
			return
		}
	}
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
	lastStats := natsjs.ConsumerStats{}

	for {
		select {
		case <-ctx.Done():
			logger.Info("worker stopped", "consumer_stats", eventConsumer.Stats())
			return
		case <-ticker.C:
			processDueRounds(ctx, logger, roundRepository)
			settleDueRounds(ctx, logger, settlementService, resultSource)
			processPending(logger, processor)
			lastStats = logConsumerStats(logger, eventConsumer, lastStats)
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

// logEvent 是业务处理器落地前的占位处理器：确认事件已到达并记录日志。
func logEvent(logger *slog.Logger) natsjs.Handler {
	return func(_ context.Context, event events.Event) error {
		logger.Info("event consumed", "event_id", event.ID, "event_type", event.Type)
		return nil
	}
}

// logConsumerStats 在计数器发生变化时输出监控快照，避免空转刷日志。
func logConsumerStats(logger *slog.Logger, consumer *natsjs.Consumer, last natsjs.ConsumerStats) natsjs.ConsumerStats {
	current := consumer.Stats()
	if current != last {
		logger.Info("consumer stats",
			"received", current.Received,
			"processed", current.Processed,
			"retried", current.Retried,
			"dead_lettered", current.DeadLettered)
	}
	return current
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
