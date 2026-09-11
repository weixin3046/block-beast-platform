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
	sequence := time.Now().UnixNano()
	const gameTypeID = "09000000-0000-4000-8000-000000000001"
	const roomID = "94000000-0000-4000-8000-000000000001"
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,login_name,display_name) VALUES($1,$2,'report player')`, userID, "report-"+userID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO wallets(id,user_id,currency,available_minor,frozen_minor) VALUES($1,$2,'POINTS',900,125)`, walletID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at,result_at) VALUES($1,$2,$5,'open',$3,$4)`, roundID, gameTypeID, time.Now().UTC().Add(time.Hour), time.Now().UTC().Add(time.Hour+time.Second), sequence); err != nil {
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
	if bets[0].Balance != nil || bets[0].FrozenBalance != nil {
		t.Fatalf("wallet balances = %+v", bets[0])
	}
	if bets[0].RoundSequence != sequence || bets[0].GameRoomCode != "hash_rate_1940" || bets[0].PlayMode != "road" || bets[0].PayoutRate != "1.94" {
		t.Fatalf("admin bet context = %+v", bets[0])
	}
	// Missing debit snapshots must not fall back to the live wallet.
	for i, balance := range []int64{800, 700} {
		placementID := uuid.NewString()
		if _, err = pool.Exec(ctx, `INSERT INTO bet_placements(id,bet_id,user_id,round_id,client_request_id,currency,selection,stake_minor) VALUES($1,$2,$3,$4,$1::uuid::text,'POINTS','{"pick":"odd"}',100)`, placementID, betID, userID, roundID); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO ledger_entries(id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor,bet_placement_id,occurred_at) VALUES($1,$2,'bet',$3,'bet_debit',-100,$4,$5,$6)`, uuid.NewString(), walletID, betID, balance, placementID, time.Now().Add(time.Duration(i-2)*time.Hour)); err != nil {
			t.Fatal(err)
		}
		bets, err = service.ListAdminBets(ctx, BetQuery{User: "report-" + userID, Limit: 10})
		if err != nil || len(bets) != 1 {
			t.Fatalf("bets=%+v err=%v", bets, err)
		}
		want := []string{"0.800", "0.700"}[i]
		if bets[0].Balance == nil || *bets[0].Balance != want || bets[0].FrozenBalance == nil || *bets[0].FrozenBalance != "0.125" {
			t.Fatalf("debit snapshot=%+v", bets[0])
		}
	}
	// Subsequent wallet changes and the newer refund cannot alter the debit snapshot.
	if _, err = pool.Exec(ctx, `UPDATE wallets SET available_minor=5000,frozen_minor=0 WHERE id=$1`, walletID); err != nil {
		t.Fatal(err)
	}
	bets, err = service.ListAdminBets(ctx, BetQuery{User: "report-" + userID, Limit: 10})
	if err != nil || len(bets) != 1 {
		t.Fatalf("bets=%+v err=%v", bets, err)
	}
	if bets[0].Balance == nil || *bets[0].Balance != "0.700" || bets[0].FrozenBalance == nil || *bets[0].FrozenBalance != "0.125" {
		t.Fatalf("snapshot changed=%+v", bets[0])
	}
	current, err := service.CurrentBets(ctx, "report-"+userID, "hash_9", 10)
	if err != nil || len(current) != 1 || current[0].GameRoomCode != "hash_rate_1940" || current[0].PayoutRate != "1.94" {
		t.Fatalf("current bets = %+v, err = %v", current, err)
	}
	refunds, err := service.ListRefundClearances(ctx, "report-"+userID, "", time.Time{}, time.Time{}, 10, 0)
	if err != nil || len(refunds) != 1 {
		t.Fatalf("refunds = %+v, err = %v", refunds, err)
	}
	if refunds[0].BetID != betID || refunds[0].BalanceAfterMinor != 1000 || refunds[0].RoundSequence == nil || *refunds[0].RoundSequence != sequence || refunds[0].GameRoomCode != "hash_rate_1940" {
		t.Fatalf("refund context = %+v", refunds[0])
	}
	// The dashboard loss metric counts only real lost orders, once per merged order.
	from := time.Date(2040, 1, 2, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	for _, tc := range []struct {
		name, status       string
		simulated, virtual bool
		created            time.Time
		want               int64
	}{
		{"lost merged order", "lost", false, false, from, 900},
		{"won", "won", false, false, from, 0},
		{"pending", "accepted", false, false, from, 0},
		{"cancelled", "cancelled", false, false, from, 0},
		{"refunded", "refunded", false, false, from, 0},
		{"voided", "voided", false, false, from, 0},
		{"simulated", "lost", true, false, from, 0},
		{"virtual", "lost", true, true, from, 0},
		{"before range", "lost", false, false, from.Add(-time.Second), 0},
		{"exclusive end", "lost", false, false, to, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `UPDATE bets SET status=$2,is_simulated=$3,created_at=$4,stake_minor=900,placement_count=9 WHERE id=$1`, betID, tc.status, tc.simulated, tc.created); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE users SET is_virtual=$2 WHERE id=$1`, userID, tc.virtual); err != nil {
				t.Fatal(err)
			}
			board, err := service.Dashboard(ctx, "report-"+userID, from, to, 10)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, fund := range board.Global {
				if fund.Currency == "POINTS" {
					found = true
					if fund.BetLossMinor != tc.want || fund.BetLoss == "" {
						t.Fatalf("global loss: %+v", fund)
					}
				}
			}
			if !tc.virtual {
				if !found || len(board.Players) != 1 || len(board.Players[0].Funds) != 1 {
					t.Fatalf("board: %+v", board)
				}
				fund := board.Players[0].Funds[0]
				if fund.BetLossMinor != tc.want || (tc.want == 900 && fund.BetLoss != "0.900") {
					t.Fatalf("player loss: %+v", fund)
				}
			} else if len(board.Players) != 0 {
				t.Fatal("virtual player included")
			}
		})
	}

}
