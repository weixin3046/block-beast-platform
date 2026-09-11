package leaderboard

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
	"time"
)

func TestLeaderboardMetricsSnapshot(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	user, wallet, round, bet, period := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	laterLoss := uuid.NewString()
	loser, loserWallet, loserBet := uuid.NewString(), uuid.NewString(), uuid.NewString()
	lowerProfit, lowerProfitWallet, lowerProfitBet := uuid.NewString(), uuid.NewString(), uuid.NewString()
	at := time.Date(2033, 1, 2, 12, 0, 0, 0, shanghai)
	start, end := periodBounds("daily", at)
	exec(`INSERT INTO users(id,display_name) VALUES($1,'metrics test')`, user)
	exec(`INSERT INTO users(id,display_name) VALUES($1,'losing test')`, loser)
	exec(`INSERT INTO users(id,display_name) VALUES($1,'lower profit test')`, lowerProfit)
	defer func() {
		pool.Exec(ctx, `DELETE FROM leaderboard_entries WHERE period_id=$1`, period)
		pool.Exec(ctx, `DELETE FROM leaderboard_periods WHERE id=$1`, period)
		pool.Exec(ctx, `DELETE FROM bets WHERE id=$1`, bet)
		pool.Exec(ctx, `DELETE FROM bets WHERE id=$1`, laterLoss)
		pool.Exec(ctx, `DELETE FROM bets WHERE id=$1`, loserBet)
		pool.Exec(ctx, `DELETE FROM bets WHERE id=$1`, lowerProfitBet)
		pool.Exec(ctx, `DELETE FROM rounds WHERE id=$1`, round)
		pool.Exec(ctx, `DELETE FROM wallets WHERE id=$1`, wallet)
		pool.Exec(ctx, `DELETE FROM wallets WHERE id=$1`, loserWallet)
		pool.Exec(ctx, `DELETE FROM wallets WHERE id=$1`, lowerProfitWallet)
		pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, user)
		pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, loser)
		pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, lowerProfit)
	}()
	exec(`INSERT INTO wallets(id,user_id,currency,available_minor) VALUES($1,$2,'USDT',1985000000)`, wallet, user)
	exec(`INSERT INTO wallets(id,user_id,currency,available_minor) VALUES($1,$2,'USDT',0)`, loserWallet, loser)
	exec(`INSERT INTO wallets(id,user_id,currency,available_minor) VALUES($1,$2,'USDT',2500000000)`, lowerProfitWallet, lowerProfit)
	exec(`INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at,result_at) VALUES($1,'09000000-0000-4000-8000-000000000001',9988776655,'open',$2,$2)`, round, at)
	exec(`INSERT INTO bets(id,client_request_id,round_id,user_id,wallet_id,selection,stake_minor,status,payout_minor,created_at) VALUES($1::uuid,$1::text,$2,$3,$4,'{}',1000000000,'won',1985000000,$5)`, bet, round, user, wallet, at)
	// A later loss must not erase the earlier profitable result from the board.
	exec(`INSERT INTO bets(id,client_request_id,round_id,user_id,wallet_id,selection,stake_minor,status,payout_minor,created_at) VALUES($1::uuid,$1::text,$2,$3,$4,'{"pick":"later-loss"}',1000000000,'lost',0,$5)`, laterLoss, round, user, wallet, at)
	exec(`INSERT INTO bets(id,client_request_id,round_id,user_id,wallet_id,selection,stake_minor,status,payout_minor,created_at) VALUES($1::uuid,$1::text,$2,$3,$4,'{}',1000000000,'lost',0,$5)`, loserBet, round, loser, loserWallet, at)
	// This player has a larger payout but a smaller profit, so must rank second.
	exec(`INSERT INTO bets(id,client_request_id,round_id,user_id,wallet_id,selection,stake_minor,status,payout_minor,created_at) VALUES($1::uuid,$1::text,$2,$3,$4,'{}',2000000000,'won',2500000000,$5)`, lowerProfitBet, round, lowerProfit, lowerProfitWallet, at)
	exec(`INSERT INTO leaderboard_periods(id,period_type,starts_at,ends_at) VALUES($1,'daily',$2,$3)`, period, start, end)
	s := NewService(pool)
	s.now = func() time.Time { return at }
	if err = s.refreshPeriod(ctx, "daily", start); err != nil {
		t.Fatal(err)
	}
	b, err := s.List(ctx, "today", "USDT", 10)
	if err != nil || len(b.Items) != 2 || b.Items[0].Rank != 1 || b.Items[1].Rank != 2 {
		t.Fatal(b, err)
	}
	e := b.Items[0]
	if b.Decimals != 6 || e.TotalBet != "1000.000000" || e.TotalPayout == nil || *e.TotalPayout != "1985.000000" || e.NetWin == nil || *e.NetWin != "985.000000" || e.Available == nil || *e.Available != "1985.000000" || b.Items[1].NetWin == nil || *b.Items[1].NetWin != "500.000000" {
		t.Fatal(b)
	}
	exec(`UPDATE leaderboard_periods SET status='frozen' WHERE id=$1`, period)
	exec(`UPDATE wallets SET available_minor=1 WHERE id=$1`, wallet)
	if err = s.refreshPeriod(ctx, "daily", start); err != nil {
		t.Fatal(err)
	}
	b, err = s.List(ctx, "today", "USDT", 10)
	if err != nil || *b.Items[0].Available != "1985.000000" {
		t.Fatal("snapshot changed", b, err)
	}
}
