package settlement

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestServiceCancelRoundRefundsAcceptedBets(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)

	accountID := uuid.NewString()
	walletID := uuid.NewString()
	gameTypeID := uuid.NewString()
	roundID := uuid.NewString()
	betID := uuid.NewString()
	_, err = pool.Exec(ctx, `INSERT INTO users (id, display_name) VALUES ($1, $2)`, accountID, "refund test player")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO wallets (id, user_id, currency, available_minor) VALUES ($1, $2, 'USDT', 7500)`, walletID, accountID)
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO game_types (id, code, name, rules) VALUES ($1, $2, 'refund test game', '{}')`, gameTypeID, "test-"+gameTypeID)
	if err != nil {
		t.Fatalf("create game type: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO rounds (id, game_type_id, sequence, status, bet_closes_at) VALUES ($1, $2, 1, 'closed', $3)`, roundID, gameTypeID, time.Now().UTC().Add(-time.Second))
	if err != nil {
		t.Fatalf("create round: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO bets (id, client_request_id, round_id, user_id, wallet_id, selection, stake_minor, status) VALUES ($1, 'refund-request', $2, $3, $4, '{}', 2500, 'accepted')`, betID, roundID, accountID, walletID)
	if err != nil {
		t.Fatalf("create accepted bet: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM outbox_events WHERE aggregate_id = $1`, roundID)
		_, _ = pool.Exec(ctx, `DELETE FROM ledger_entries WHERE wallet_id = $1`, walletID)
		_, _ = pool.Exec(ctx, `DELETE FROM bets WHERE id = $1`, betID)
		_, _ = pool.Exec(ctx, `DELETE FROM wallets WHERE id = $1`, walletID)
		_, _ = pool.Exec(ctx, `DELETE FROM rounds WHERE id = $1`, roundID)
		_, _ = pool.Exec(ctx, `DELETE FROM game_types WHERE id = $1`, gameTypeID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, accountID)
	})

	service := NewService(pool)
	refunded, err := service.CancelRound(ctx, roundID)
	if err != nil {
		t.Fatalf("cancel round: %v", err)
	}
	if refunded != 1 {
		t.Fatalf("refunded bets = %d, want 1", refunded)
	}

	var availableMinor int64
	var betStatus, roundStatus string
	err = pool.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id = $1`, walletID).Scan(&availableMinor)
	if err != nil {
		t.Fatalf("read wallet: %v", err)
	}
	err = pool.QueryRow(ctx, `SELECT status FROM bets WHERE id = $1`, betID).Scan(&betStatus)
	if err != nil {
		t.Fatalf("read bet: %v", err)
	}
	err = pool.QueryRow(ctx, `SELECT status FROM rounds WHERE id = $1`, roundID).Scan(&roundStatus)
	if err != nil {
		t.Fatalf("read round: %v", err)
	}
	if availableMinor != 10_000 || betStatus != "refunded" || roundStatus != "cancelled" {
		t.Fatalf("wallet = %d, bet = %q, round = %q", availableMinor, betStatus, roundStatus)
	}
	assertCount(t, ctx, pool, `SELECT count(*) FROM ledger_entries WHERE wallet_id = $1 AND entry_type = 'refund'`, walletID, 1)
	assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events WHERE aggregate_id = $1 AND event_type = $2`, []any{roundID, events.RoundCancelled}, 1)

	refunded, err = service.CancelRound(ctx, roundID)
	if err != nil {
		t.Fatalf("repeat cancel round: %v", err)
	}
	if refunded != 0 {
		t.Fatalf("repeat refunded bets = %d, want 0", refunded)
	}
	assertCount(t, ctx, pool, `SELECT count(*) FROM ledger_entries WHERE wallet_id = $1 AND entry_type = 'refund'`, walletID, 1)
}

func assertCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, argument any, want int) {
	t.Helper()
	var count int
	var err error
	if arguments, ok := argument.([]any); ok {
		err = pool.QueryRow(ctx, query, arguments...).Scan(&count)
	} else {
		err = pool.QueryRow(ctx, query, argument).Scan(&count)
	}
	if err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != want {
		t.Fatalf("row count = %d, want %d", count, want)
	}
}
