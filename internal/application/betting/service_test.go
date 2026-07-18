package betting

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/domain/wallet"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestServicePlaceBetIsAtomicAndIdempotent(t *testing.T) {
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
	_, err = pool.Exec(ctx, `INSERT INTO users (id, display_name) VALUES ($1, $2)`, accountID, "bet test player")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO wallets (id, user_id, currency, available_minor) VALUES ($1, $2, $3, $4)`, walletID, accountID, "USDT", 10_000)
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO game_types (id, code, name, rules) VALUES ($1, $2, $3, $4)`, gameTypeID, "test-"+gameTypeID, "test game", `{}`)
	if err != nil {
		t.Fatalf("create game type: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO rounds (id, game_type_id, sequence, status, bet_closes_at) VALUES ($1, $2, $3, 'open', $4)`, roundID, gameTypeID, 1, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("create round: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM outbox_events WHERE payload->>'user_id' = $1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM ledger_entries WHERE wallet_id = $1`, walletID)
		_, _ = pool.Exec(ctx, `DELETE FROM bets WHERE wallet_id = $1`, walletID)
		_, _ = pool.Exec(ctx, `DELETE FROM wallets WHERE id = $1`, walletID)
		_, _ = pool.Exec(ctx, `DELETE FROM rounds WHERE id = $1`, roundID)
		_, _ = pool.Exec(ctx, `DELETE FROM game_types WHERE id = $1`, gameTypeID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, accountID)
	})

	service := NewService(pool)
	request := PlaceBetRequest{
		ClientRequestID: "request-101",
		RoundID:         roundID,
		AccountID:       accountID,
		Currency:        "USDT",
		Selection:       json.RawMessage(`{"color":"red"}`),
		StakeMinor:      2_500,
	}
	first, err := service.PlaceBet(ctx, request)
	if err != nil {
		t.Fatalf("place bet: %v", err)
	}
	second, err := service.PlaceBet(ctx, request)
	if err != nil {
		t.Fatalf("repeat place bet: %v", err)
	}
	if first.BetID != second.BetID {
		t.Fatal("same client request ID must return the original bet")
	}
	if second.Currency != "USDT" || second.Status != "accepted" {
		t.Fatalf("repeated bet = %#v, want complete accepted bet", second)
	}
	found, err := service.Find(ctx, first.BetID)
	if err != nil {
		t.Fatalf("find placed bet: %v", err)
	}
	if found.BetID != first.BetID || found.Status != "accepted" || found.Currency != "USDT" {
		t.Fatalf("found bet = %#v", found)
	}

	assertCount(t, ctx, pool, `SELECT count(*) FROM bets WHERE wallet_id = $1`, walletID, 1)
	assertCount(t, ctx, pool, `SELECT count(*) FROM ledger_entries WHERE wallet_id = $1 AND entry_type = 'bet_debit'`, walletID, 1)
	assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events WHERE aggregate_id = $1 AND event_type = 'game.bet.placed'`, first.BetID, 1)

	var availableMinor int64
	err = pool.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id = $1`, walletID).Scan(&availableMinor)
	if err != nil {
		t.Fatalf("read wallet balance: %v", err)
	}
	if availableMinor != 7_500 {
		t.Fatalf("available balance = %d, want 7500", availableMinor)
	}

	_, err = service.PlaceBet(ctx, PlaceBetRequest{
		ClientRequestID: "request-102",
		RoundID:         roundID,
		AccountID:       accountID,
		Currency:        "USDT",
		Selection:       json.RawMessage(`{"color":"blue"}`),
		StakeMinor:      7_501,
	})
	if !errors.Is(err, wallet.ErrInsufficientFunds) {
		t.Fatalf("error = %v, want insufficient funds", err)
	}
	assertCount(t, ctx, pool, `SELECT count(*) FROM bets WHERE wallet_id = $1`, walletID, 1)
}

func assertCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, argument any, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(ctx, query, argument).Scan(&got); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if got != want {
		t.Fatalf("row count = %d, want %d", got, want)
	}
}
