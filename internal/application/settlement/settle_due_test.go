package settlement

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/domain/events"
	"github.com/block-beast/platform/internal/domain/game"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fixedResultSource struct {
	outcome []string
}

func (source fixedResultSource) Outcome(_ context.Context, _ game.Round, _ game.Rules) ([]string, error) {
	return source.outcome, nil
}

func TestSettleDueRoundsSettlesClosedRoundsByRules(t *testing.T) {
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
	wonBetID := uuid.NewString()
	lostBetID := uuid.NewString()
	_, err = pool.Exec(ctx, `INSERT INTO users (id, display_name) VALUES ($1, 'settle due player')`, accountID)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO wallets (id, user_id, currency, available_minor) VALUES ($1, $2, 'USDT', 0)`, walletID, accountID)
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO game_types (id, code, name, rules) VALUES ($1, $2, 'settle due game', '{"outcomes":["red","black"],"payout_multiplier":2,"match_field":"color"}')`, gameTypeID, "test-"+gameTypeID)
	if err != nil {
		t.Fatalf("create game type: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO rounds (id, game_type_id, sequence, status, bet_closes_at) VALUES ($1, $2, 1, 'closed', $3)`, roundID, gameTypeID, time.Now().UTC().Add(-time.Second))
	if err != nil {
		t.Fatalf("create round: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO bets (id, client_request_id, round_id, user_id, wallet_id, selection, stake_minor, status) VALUES ($1, 'won-request', $2, $3, $4, '{"color":"red","size":"large"}', 1000, 'accepted')`, wonBetID, roundID, accountID, walletID)
	if err != nil {
		t.Fatalf("create winning bet: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO bets (id, client_request_id, round_id, user_id, wallet_id, selection, stake_minor, status) VALUES ($1, 'lost-request', $2, $3, $4, '{"color":"black"}', 500, 'accepted')`, lostBetID, roundID, accountID, walletID)
	if err != nil {
		t.Fatalf("create losing bet: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM outbox_events WHERE aggregate_id = $1`, roundID)
		_, _ = pool.Exec(ctx, `DELETE FROM ledger_entries WHERE wallet_id = $1`, walletID)
		_, _ = pool.Exec(ctx, `DELETE FROM bets WHERE round_id = $1`, roundID)
		_, _ = pool.Exec(ctx, `DELETE FROM rounds WHERE id = $1`, roundID)
		_, _ = pool.Exec(ctx, `DELETE FROM game_types WHERE id = $1`, gameTypeID)
		_, _ = pool.Exec(ctx, `DELETE FROM wallets WHERE id = $1`, walletID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, accountID)
	})

	service := NewService(pool)
	source := fixedResultSource{outcome: []string{"red"}}
	settled, err := service.SettleDueRounds(ctx, source, 100)
	if err != nil {
		t.Fatalf("settle due rounds: %v", err)
	}
	if len(settled) != 1 || settled[0].RoundID != roundID {
		t.Fatalf("settled rounds = %+v, want round %s", settled, roundID)
	}
	result := settled[0].Result
	if result.WonBetCount != 1 || result.LostBetCount != 1 || result.PayoutMinor != 2000 {
		t.Fatalf("result = %+v, want 1 won / 1 lost / payout 2000", result)
	}

	var availableMinor int64
	var wonStatus, lostStatus, roundStatus string
	if err := pool.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id = $1`, walletID).Scan(&availableMinor); err != nil {
		t.Fatalf("read wallet: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM bets WHERE id = $1`, wonBetID).Scan(&wonStatus); err != nil {
		t.Fatalf("read winning bet: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM bets WHERE id = $1`, lostBetID).Scan(&lostStatus); err != nil {
		t.Fatalf("read losing bet: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM rounds WHERE id = $1`, roundID).Scan(&roundStatus); err != nil {
		t.Fatalf("read round: %v", err)
	}
	if availableMinor != 2000 || wonStatus != "won" || lostStatus != "lost" || roundStatus != "settled" {
		t.Fatalf("wallet = %d, won bet = %q, lost bet = %q, round = %q", availableMinor, wonStatus, lostStatus, roundStatus)
	}
	assertCount(t, ctx, pool, `SELECT count(*) FROM ledger_entries WHERE wallet_id = $1 AND entry_type = 'settlement_credit'`, walletID, 1)
	assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events WHERE aggregate_id = $1 AND event_type = $2`, []any{roundID, events.RoundSettled}, 1)

	// 重复执行必须幂等：已结算轮次不再出现在到期列表中，不会重复派奖。
	settled, err = service.SettleDueRounds(ctx, source, 100)
	if err != nil {
		t.Fatalf("repeat settle due rounds: %v", err)
	}
	if len(settled) != 0 {
		t.Fatalf("repeat settled rounds = %+v, want none", settled)
	}
	if err := pool.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id = $1`, walletID).Scan(&availableMinor); err != nil {
		t.Fatalf("reread wallet: %v", err)
	}
	if availableMinor != 2000 {
		t.Fatalf("wallet after repeat = %d, want 2000", availableMinor)
	}
}

func TestSettleDueRoundsKeepsFailedRoundForRetry(t *testing.T) {
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

	gameTypeID := uuid.NewString()
	roundID := uuid.NewString()
	// 规则缺少 outcomes，结算必然失败，轮次应保持 closed 等待重试。
	_, err = pool.Exec(ctx, `INSERT INTO game_types (id, code, name, rules) VALUES ($1, $2, 'broken rules game', '{"payout_multiplier":2}')`, gameTypeID, "test-"+gameTypeID)
	if err != nil {
		t.Fatalf("create game type: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO rounds (id, game_type_id, sequence, status, bet_closes_at) VALUES ($1, $2, 1, 'closed', $3)`, roundID, gameTypeID, time.Now().UTC().Add(-time.Second))
	if err != nil {
		t.Fatalf("create round: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM rounds WHERE id = $1`, roundID)
		_, _ = pool.Exec(ctx, `DELETE FROM game_types WHERE id = $1`, gameTypeID)
	})

	service := NewService(pool)
	settled, err := service.SettleDueRounds(ctx, fixedResultSource{outcome: []string{"red"}}, 100)
	if err == nil {
		t.Fatal("broken rules should surface an error")
	}
	if len(settled) != 0 {
		t.Fatalf("settled rounds = %+v, want none", settled)
	}
	var roundStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM rounds WHERE id = $1`, roundID).Scan(&roundStatus); err != nil {
		t.Fatalf("read round: %v", err)
	}
	if roundStatus != "closed" {
		t.Fatalf("round status = %q, want closed for retry", roundStatus)
	}
}
