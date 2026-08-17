package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	chainapp "github.com/block-beast/platform/internal/application/chain"
	"github.com/block-beast/platform/internal/application/leaderboard"
	"github.com/block-beast/platform/internal/application/outbox"
	"github.com/block-beast/platform/internal/application/pqpaassets"
	"github.com/block-beast/platform/internal/application/redpacket"
	"github.com/block-beast/platform/internal/application/settlement"
	"github.com/block-beast/platform/internal/application/uploads"
	"github.com/block-beast/platform/internal/config"
	"github.com/block-beast/platform/internal/domain/events"
	"github.com/block-beast/platform/internal/domain/game"
	"github.com/block-beast/platform/internal/platform/natsjs"
	"github.com/block-beast/platform/internal/platform/pqpa"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errWithdrawalProviderUnavailable = errors.New("withdrawal provider is unavailable")

func main() {
	cfg := config.Load()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := cfg.ValidateWorker(); err != nil {
		logger.Error("invalid worker configuration", "error", err)
		return
	}
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
	var withdrawalSender *chainapp.Service
	if cfg.PQPAAPIURL != "" && cfg.PQPAAPIKey != "" && cfg.PQPAAPISecret != "" {
		withdrawalSender = chainapp.NewService(pool).WithWithdrawalProvider(pqpa.NewClient(cfg.PQPAAPIURL, cfg.PQPAAPIKey, cfg.PQPAAPISecret, nil))
	}
	for _, subject := range []string{"game.>", "wallet.>", "chain.>", "chat.>"} {
		durable := "worker-" + strings.ReplaceAll(strings.TrimSuffix(subject, ".>"), ".", "-")
		if err := eventConsumer.Subscribe(subject, durable, processEvent(logger, withdrawalSender)); err != nil {
			logger.Error("worker failed to subscribe", "subject", subject, "error", err)
			return
		}
	}
	processor := outbox.NewProcessor(events.NewPostgresOutbox(pool), publisher)
	roundRepository := game.NewPostgresRepository(pool)
	settlementService := settlement.NewService(pool)
	uploadMaintenance := uploads.NewService(pool, nil, cfg.UploadMaxBytes, cfg.UploadURLTTL)
	redPacketService := redpacket.NewService(pool, cfg.RedPacketTTL)
	leaderboardService := leaderboard.NewService(pool)
	leaderboardTicker := time.NewTicker(cfg.LeaderboardRefresh)
	defer leaderboardTicker.Stop()
	resultSource := settlement.NewCompositeResultSourceWithWebSocket(cfg.TronGridAPIKey, cfg.TronGridGRPCEndpoint, cfg.OkxRESTURL, cfg.OkxWebSocketURL)
	defer resultSource.Close()
	ticker := time.NewTicker(cfg.WorkerPollInterval)
	defer ticker.Stop()
	logger.Info("worker started", "poll_interval", cfg.WorkerPollInterval)
	processDueRounds(ctx, logger, roundRepository)
	ensureScheduledRounds(ctx, logger, roundRepository, resultSource)
	settleDueRounds(ctx, logger, settlementService, resultSource)
	processPending(logger, processor)
	reconcileWithdrawals(ctx, logger, withdrawalSender)
	expirePendingUploads(ctx, logger, uploadMaintenance)
	refreshDailyLeaderboard(ctx, logger, leaderboardService)
	refundExpiredRedPackets(ctx, logger, redPacketService)
	lastStats := natsjs.ConsumerStats{}
	var assetSync *pqpaassets.Service
	var assetTicker *time.Ticker
	if cfg.PQPAAPIURL != "" && cfg.PQPAAPIKey != "" && cfg.PQPAAPISecret != "" {
		client := pqpa.NewClient(cfg.PQPAAPIURL, cfg.PQPAAPIKey, cfg.PQPAAPISecret, nil)
		assetSync = pqpaassets.NewService(pool, pqpa.AssetProvider{Client: client})
		assetTicker = time.NewTicker(cfg.PQPAAssetSyncInterval)
		defer assetTicker.Stop()
		syncPQPAAssets(ctx, logger, assetSync)
	}

	for {
		select {
		case <-ctx.Done():
			logger.Info("worker stopped", "consumer_stats", eventConsumer.Stats())
			return
		case <-ticker.C:
			ensureScheduledRounds(ctx, logger, roundRepository, resultSource)
			processDueRounds(ctx, logger, roundRepository)
			settleDueRounds(ctx, logger, settlementService, resultSource)
			processPending(logger, processor)
			reconcileWithdrawals(ctx, logger, withdrawalSender)
			expirePendingUploads(ctx, logger, uploadMaintenance)
			refundExpiredRedPackets(ctx, logger, redPacketService)
			lastStats = logConsumerStats(logger, eventConsumer, lastStats)
		case <-assetTick(assetTicker):
			syncPQPAAssets(ctx, logger, assetSync)
		case <-leaderboardTicker.C:
			refreshDailyLeaderboard(ctx, logger, leaderboardService)
		}
	}
}

func ensureScheduledRounds(ctx context.Context, logger *slog.Logger, repository *game.PostgresRepository, source *settlement.CompositeResultSource) {
	tronHeight, tronBlockAt, err := source.CurrentTronBlock(ctx)
	if err != nil {
		logger.Error("TRON block height query failed; hash scheduling skipped", "error", err)
	}
	created, err := repository.EnsureScheduledRounds(ctx, time.Now().UTC(), tronHeight, tronBlockAt)
	if err != nil {
		logger.Error("automatic round scheduling failed", "error", err)
		return
	}
	if created > 0 {
		logger.Info("automatic rounds scheduled", "count", created)
	}
}

func refundExpiredRedPackets(ctx context.Context, logger *slog.Logger, service *redpacket.Service) {
	refunded, err := service.RefundExpired(ctx, 100)
	if err != nil {
		logger.Error("expired red packet refund failed", "error", err)
		return
	}
	if refunded > 0 {
		logger.Info("expired red packets refunded", "count", refunded)
	}
}

func refreshDailyLeaderboard(ctx context.Context, logger *slog.Logger, service *leaderboard.Service) {
	entries, err := service.RefreshDaily(ctx, time.Now().UTC())
	if err != nil {
		logger.Error("daily leaderboard refresh failed", "error", err)
		return
	}
	logger.Info("daily leaderboard refreshed", "entries", entries)
}

func expirePendingUploads(ctx context.Context, logger *slog.Logger, service *uploads.Service) {
	expired, err := service.ExpirePending(ctx, 100)
	if err != nil {
		logger.Error("pending upload expiration failed", "error", err)
		return
	}
	if expired > 0 {
		logger.Info("pending uploads expired", "count", expired)
	}
}

func reconcileWithdrawals(ctx context.Context, logger *slog.Logger, service *chainapp.Service) {
	if service == nil {
		return
	}
	result, err := service.ReconcileWithdrawals(ctx, 100)
	if err != nil {
		logger.Error("PQPA withdrawal reconciliation failed", "checked", result.Checked, "error", err)
		return
	}
	if result.Checked > 0 {
		logger.Info("PQPA withdrawals reconciled",
			"checked", result.Checked,
			"confirmed", result.Confirmed,
			"failed", result.Failed)
	}
}

func assetTick(ticker *time.Ticker) <-chan time.Time {
	if ticker == nil {
		return nil
	}
	return ticker.C
}

func syncPQPAAssets(ctx context.Context, logger *slog.Logger, service *pqpaassets.Service) {
	if service == nil {
		return
	}
	count, err := service.Sync(ctx)
	if err != nil {
		logger.Error("PQPA asset sync failed", "error", err)
		return
	}
	logger.Info("PQPA assets synchronized", "count", count)
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

// processEvent 分派需要异步副作用的领域事件。资金类事件必须在副作用
// 完成后才能确认；通知类事件由 Realtime 直接转发，Worker 可安全确认。
func processEvent(logger *slog.Logger, withdrawals *chainapp.Service) natsjs.Handler {
	return func(ctx context.Context, event events.Event) error {
		if event.Type == events.WithdrawalApproved {
			if withdrawals == nil {
				return errWithdrawalProviderUnavailable
			}
			var payload struct {
				WithdrawalID string `json:"withdrawal_id"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return fmt.Errorf("decode approved withdrawal event: %w", err)
			}
			if payload.WithdrawalID == "" {
				return errors.New("approved withdrawal event is missing withdrawal_id")
			}
			if err := withdrawals.SendApprovedWithdrawal(ctx, payload.WithdrawalID); err != nil {
				return fmt.Errorf("send approved withdrawal %s: %w", payload.WithdrawalID, err)
			}
			logger.Info("PQPA withdrawal sent", "withdrawal_id", payload.WithdrawalID)
			return nil
		}
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
