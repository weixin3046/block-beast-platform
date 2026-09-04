package betting_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/application/betting"
	"github.com/block-beast/platform/internal/application/settlement"
	"github.com/block-beast/platform/internal/domain/game"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestHashBetUsesRoomConfigSnapshotAtSettlement(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	const (
		gameTypeID = "09000000-0000-4000-8000-000000000001"
		room194ID  = "94000000-0000-4000-8000-000000000001"
		room195ID  = "95000000-0000-4000-8000-000000000001"
	)
	userID, walletID, roundID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	sequence := time.Now().UnixNano()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,login_name,display_name) VALUES($1,$2,'hash snapshot player')`, userID, "hash-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets(id,user_id,currency,available_minor) VALUES($1,$2,'POINTS',1000)`, walletID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at,result_at) VALUES($1,$2,$3,'open',now()+interval '1 hour',now()+interval '1 hour 5 seconds')`, roundID, gameTypeID, sequence); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE hash_room_currency_configs SET guess_multiplier=9350 WHERE room_id=$1 AND currency='POINTS'`, room194ID)
		_, _ = pool.Exec(ctx, `DELETE FROM outbox_events WHERE aggregate_id=$1 OR payload->>'round_id'=$1`, roundID)
		_, _ = pool.Exec(ctx, `DELETE FROM commission_entries WHERE source_bet_id IN(SELECT id FROM bets WHERE round_id=$1)`, roundID)
		_, _ = pool.Exec(ctx, `DELETE FROM ledger_entries WHERE wallet_id=$1`, walletID)
		_, _ = pool.Exec(ctx, `DELETE FROM bets WHERE round_id=$1`, roundID)
		_, _ = pool.Exec(ctx, `DELETE FROM rounds WHERE id=$1`, roundID)
		_, _ = pool.Exec(ctx, `DELETE FROM wallets WHERE id=$1`, walletID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	})

	service := betting.NewService(pool)
	placed, err := service.PlaceBet(ctx, betting.PlaceBetRequest{
		ClientRequestID: "hash-snapshot-1", RoundID: roundID, AccountID: userID,
		Currency: "POINTS", GameRoomID: room194ID, PlayMode: "guess",
		Selection: json.RawMessage(`{"pick":"5"}`), StakeMinor: 10,
	})
	if err != nil {
		t.Fatalf("place hash bet: %v", err)
	}
	if placed.GameRoomID != room194ID || placed.GameRoomCode != "hash_rate_1940" || placed.GameRoomName == "" || placed.PlayMode != "guess" || placed.RoundSequence != sequence || placed.PayoutRate != "9.35" || placed.BalanceAfterBetMinor == nil || *placed.BalanceAfterBetMinor != 990 {
		t.Fatalf("placed hash bet = %+v", placed)
	}

	_, err = service.PlaceBet(ctx, betting.PlaceBetRequest{
		ClientRequestID: "hash-other-room", RoundID: roundID, AccountID: userID,
		Currency: "POINTS", GameRoomID: room195ID, PlayMode: "guess",
		Selection: json.RawMessage(`{"pick":"5"}`), StakeMinor: 1,
	})
	if !errors.Is(err, betting.ErrHashRoomConflict) {
		t.Fatalf("other room error = %v, want ErrHashRoomConflict", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE hash_room_currency_configs SET guess_multiplier=1000 WHERE room_id=$1 AND currency='POINTS'`, room194ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE rounds SET status='closed' WHERE id=$1`, roundID); err != nil {
		t.Fatal(err)
	}
	var rawRules json.RawMessage
	if err := pool.QueryRow(ctx, `SELECT rules FROM game_types WHERE id=$1`, gameTypeID).Scan(&rawRules); err != nil {
		t.Fatal(err)
	}
	rules, err := game.ParseRules(rawRules)
	if err != nil {
		t.Fatal(err)
	}
	result, err := settlement.NewService(pool).SettleRound(ctx, roundID, []string{"5", "big", "odd"}, rules)
	if err != nil {
		t.Fatalf("settle hash bet: %v", err)
	}
	if result.PayoutMinor != 93 {
		t.Fatalf("payout = %d, want snapshot payout 93", result.PayoutMinor)
	}
	var balance int64
	if err := pool.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE id=$1`, walletID).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if balance != 1083 {
		t.Fatalf("balance = %d, want 1083", balance)
	}
	trend, err := game.NewPostgresRepository(pool).HashTrend(ctx, "hash_9", 1)
	if err != nil {
		t.Fatalf("load hash trend: %v", err)
	}
	if len(trend.Items) != 1 || trend.Items[0].Sequence != sequence || trend.Items[0].Digit != 5 || trend.Items[0].Size != "big" || trend.Items[0].Parity != "odd" {
		t.Fatalf("hash trend = %+v", trend)
	}
	if trend.Summary.DigitOmissions["5"] != 0 || trend.Summary.SizeStreak.Count != 1 || trend.Summary.ParityStreak.Count != 1 {
		t.Fatalf("hash trend summary = %+v", trend.Summary)
	}
}
