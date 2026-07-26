package leaderboard

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestListDailyValidatesFiltersBeforeDatabaseAccess(t *testing.T) {
	service := &Service{}
	if _, err := service.ListDaily(context.Background(), time.Now(), "BTC", "profit", 10); !errors.Is(err, ErrInvalidCurrency) {
		t.Fatalf("currency error = %v", err)
	}
	if _, err := service.ListDaily(context.Background(), time.Now(), "USDT", "unknown", 10); !errors.Is(err, ErrInvalidMetric) {
		t.Fatalf("metric error = %v", err)
	}
}

func TestRefreshAndListDailyLeaderboard(t *testing.T) {
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
	user1, user2 := uuid.NewString(), uuid.NewString()
	wallet1, wallet2 := uuid.NewString(), uuid.NewString()
	gameTypeID, roundID := uuid.NewString(), uuid.NewString()
	now := time.Date(2040, 7, 26, 12, 0, 0, 0, time.UTC)
	_, err = pool.Exec(ctx, `INSERT INTO users(id,display_name,login_name) VALUES ($1,'first',$3),($2,'second',$4)`, user1, user2, "rank-"+user1, "rank-"+user2)
	if err == nil {
		_, err = pool.Exec(ctx, `INSERT INTO wallets(id,user_id,currency) VALUES ($1,$2,'USDT'),($3,$4,'USDT')`, wallet1, user1, wallet2, user2)
	}
	if err == nil {
		_, err = pool.Exec(ctx, `INSERT INTO game_types(id,code,name,rules) VALUES ($1,$2,'ranking test','{}')`, gameTypeID, "rank-"+gameTypeID)
	}
	if err == nil {
		_, err = pool.Exec(ctx, `INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at,settled_at) VALUES ($1,$2,1,'settled',$3,$3)`, roundID, gameTypeID, now)
	}
	if err == nil {
		_, err = pool.Exec(ctx, `
			INSERT INTO bets(id,client_request_id,round_id,user_id,wallet_id,selection,stake_minor,status,payout_minor,settled_at)
			VALUES ($1,'r1',$3,$4,$5,'{}',100,'won',250,$7),($2,'r2',$3,$6,$8,'{}',200,'lost',0,$7)`,
			uuid.NewString(), uuid.NewString(), roundID, user1, wallet1, user2, now, wallet2)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM leaderboard_daily WHERE user_id IN ($1,$2)`, user1, user2)
		_, _ = pool.Exec(ctx, `DELETE FROM bets WHERE round_id=$1`, roundID)
		_, _ = pool.Exec(ctx, `DELETE FROM rounds WHERE id=$1`, roundID)
		_, _ = pool.Exec(ctx, `DELETE FROM game_types WHERE id=$1`, gameTypeID)
		_, _ = pool.Exec(ctx, `DELETE FROM wallets WHERE id IN ($1,$2)`, wallet1, wallet2)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, user1, user2)
	})
	service := NewService(pool)
	service.now = func() time.Time { return now }
	count, err := service.RefreshDaily(ctx, now)
	if err != nil || count != 2 {
		t.Fatalf("refresh count = %d, err = %v", count, err)
	}
	entries, err := service.ListDaily(ctx, now, "USDT", "profit", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].UserID != user1 || entries[0].NetProfitMinor != 150 || entries[1].NetProfitMinor != -200 {
		t.Fatalf("entries = %+v", entries)
	}
}
