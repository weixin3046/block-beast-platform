package leaderboard

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
	"time"
)

func TestSelfOutsideLimitAndCurrencyScope(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set")
	}
	ctx := context.Background()
	p, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := p.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	now := time.Date(2089, 3, 14, 12, 0, 0, 0, shanghai)
	s := NewService(p)
	s.now = func() time.Time { return now }
	for _, period := range []string{"today", "yesterday", "this_week", "last_week"} {
		kind, shift := "daily", 0
		if period == "yesterday" {
			shift = -1
		}
		if period == "this_week" || period == "last_week" {
			kind = "weekly"
		}
		if period == "last_week" {
			shift = -7
		}
		start, end := periodBounds(kind, now)
		start = start.AddDate(0, 0, shift)
		end = end.AddDate(0, 0, shift)
		id := uuid.NewString()
		exec(`INSERT INTO leaderboard_periods(id,period_type,starts_at,ends_at) VALUES($1,$2,$3,$4)`, id, kind, start, end)
		exec(`INSERT INTO leaderboard_reward_rules(period_type,currency,rank_from,rank_to,reward_currency,reward_minor,enabled) VALUES($1,'POINTS',3,3,'USDT',1500000,true)`, kind)
		var viewer string
		for rank := 1; rank <= 3; rank++ {
			user := uuid.NewString()
			viewer = user
			exec(`INSERT INTO users(id,display_name) VALUES($1,'self fixture')`, user)
			exec(`INSERT INTO leaderboard_entries(period_id,currency,user_id,public_user_id,display_name,avatar_url,is_virtual,effective_stake_minor,first_effective_at,rank,total_payout_minor,available_minor) SELECT $1,'POINTS',id,public_id,display_name,'',false,$3,$4,$5,0,1234 FROM users WHERE id=$2`, id, user, 4000-rank*1000, start, rank)
		}
		board, e := s.ListForUser(ctx, period, "POINTS", 1, viewer)
		if e != nil {
			t.Fatal(e)
		}
		if len(board.Items) != 1 || board.Self == nil || board.Self.Rank != 3 || board.Self.Available == nil || *board.Self.Available != "1.234" || board.Self.Reward == nil || board.Self.Reward.Currency != "USDT" || board.Self.Reward.AmountMinor != 1500000 {
			t.Fatalf("board=%+v self=%+v", board, board.Self)
		}
		other, e := s.ListForUser(ctx, period, "USDT", 1, viewer)
		if e != nil || other.Self != nil {
			t.Fatalf("currency leak: %+v %v", other, e)
		}
		absent, e := s.ListForUser(ctx, period, "POINTS", 1, uuid.NewString())
		if e != nil || absent.Self != nil {
			t.Fatalf("absent: %+v %v", absent, e)
		}
		exec(`DELETE FROM leaderboard_entries WHERE period_id=$1`, id)
		exec(`DELETE FROM leaderboard_periods WHERE id=$1`, id)
		exec(`DELETE FROM leaderboard_reward_rules WHERE period_type=$1 AND currency='POINTS' AND rank_from=3 AND rank_to=3`, kind)
	}
}
