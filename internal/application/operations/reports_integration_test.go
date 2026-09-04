package operations

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBetReportsIncludeRoundRoomOddsAndCancellationRefunds(t *testing.T) {
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

	userID, walletID, roundID, betID, ledgerID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	const gameTypeID = "09000000-0000-4000-8000-000000000001"
	const roomID = "94000000-0000-4000-8000-000000000001"
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,login_name,display_name) VALUES($1,$2,'report player')`, userID, "report-"+userID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO wallets(id,user_id,currency,available_minor) VALUES($1,$2,'POINTS',900)`, walletID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at,result_at) VALUES($1,$2,7654321,'open',$3,$4)`, roundID, gameTypeID, time.Now().UTC().Add(time.Hour), time.Now().UTC().Add(time.Hour+time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO bets(id,client_request_id,round_id,user_id,wallet_id,game_room_id,play_mode,selection,stake_minor,status,payout_multiplier_snapshot,payout_divisor_snapshot) VALUES($1,'report-bet',$2,$3,$4,$5,'road','{"pick":"odd"}',100,'accepted',1940,1000)`, betID, roundID, userID, walletID, roomID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO ledger_entries(id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor) VALUES($1,$2,'bet_cancel',$3,'bet_refund',100,1000)`, ledgerID, walletID, betID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM ledger_entries WHERE id=$1`, ledgerID)
		_, _ = pool.Exec(ctx, `DELETE FROM bets WHERE id=$1`, betID)
		_, _ = pool.Exec(ctx, `DELETE FROM rounds WHERE id=$1`, roundID)
		_, _ = pool.Exec(ctx, `DELETE FROM wallets WHERE id=$1`, walletID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	})

	service := NewService(pool)
	bets, err := service.ListAdminBets(ctx, BetQuery{User: "report-" + userID, Limit: 10})
	if err != nil || len(bets) != 1 {
		t.Fatalf("admin bets = %+v, err = %v", bets, err)
	}
	if bets[0].RoundSequence != 7654321 || bets[0].GameRoomCode != "hash_rate_1940" || bets[0].PlayMode != "road" || bets[0].PayoutRate != "1.94" {
		t.Fatalf("admin bet context = %+v", bets[0])
	}
	current, err := service.CurrentBets(ctx, "report-"+userID, "hash_9", 10)
	if err != nil || len(current) != 1 || current[0].GameRoomCode != "hash_rate_1940" || current[0].PayoutRate != "1.94" {
		t.Fatalf("current bets = %+v, err = %v", current, err)
	}
	refunds, err := service.ListRefundClearances(ctx, "report-"+userID, "", time.Time{}, time.Time{}, 10, 0)
	if err != nil || len(refunds) != 1 {
		t.Fatalf("refunds = %+v, err = %v", refunds, err)
	}
	if refunds[0].BetID != betID || refunds[0].BalanceAfterMinor != 1000 || refunds[0].RoundSequence == nil || *refunds[0].RoundSequence != 7654321 || refunds[0].GameRoomCode != "hash_rate_1940" {
		t.Fatalf("refund context = %+v", refunds[0])
	}
}
