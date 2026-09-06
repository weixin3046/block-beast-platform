package betting_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/application/betting"
	"github.com/block-beast/platform/internal/application/operations"
	"github.com/block-beast/platform/internal/application/settlement"
	"github.com/block-beast/platform/internal/domain/game"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Removing merge lookup, charging the aggregate on append, or paying each
// increment separately must break this real PostgreSQL transaction test.
func TestHashMergedPlacementsLifecycle(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := p.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	const gt = "09000000-0000-4000-8000-000000000001"
	const room = "94000000-0000-4000-8000-000000000001"
	var oldLimit int64
	if err := p.QueryRow(ctx, `SELECT road_max_stake_minor FROM hash_room_currency_configs WHERE room_id=$1 AND currency='POINTS'`, room).Scan(&oldLimit); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE hash_room_currency_configs SET road_max_stake_minor=100000 WHERE room_id=$1 AND currency='POINTS'`, room)
	defer p.Exec(context.Background(), `UPDATE hash_room_currency_configs SET road_max_stake_minor=$2 WHERE room_id=$1 AND currency='POINTS'`, room, oldLimit)
	user, wid, round := uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO users(id,display_name) VALUES($1,'merge test')`, user)
	exec(`INSERT INTO wallets(id,user_id,currency,available_minor) VALUES($1,$2,'POINTS',1000000)`, wid, user)
	exec(`INSERT INTO wallets(id,user_id,currency,available_minor) VALUES($1,$2,'USDT',1000000)`, uuid.NewString(), user)
	exec(`INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at) VALUES($1,$2,$3,'open',now()+interval '1 hour')`, round, gt, time.Now().UnixNano())
	s := betting.NewService(p)
	// The migration preserves the exact legacy request JSON. A retry of an
	// already accepted order must not fail because harmless extras existed.
	legacyID, legacyKey := uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO bets(id,client_request_id,round_id,user_id,wallet_id,game_room_id,play_mode,selection,stake_minor,status) VALUES($1,$2,$3,$4,$5,$6,'road','{"pick":"small","extra":1}',1,'cancelled')`, legacyID, legacyKey, round, user, wid, room)
	exec(`INSERT INTO bet_placements(id,bet_id,user_id,round_id,client_request_id,currency,game_room_id,play_mode,selection,stake_minor) VALUES($1,$1,$2,$3,$4,'POINTS',$5,'road','{"pick":"small","extra":1}',1)`, legacyID, user, round, legacyKey, room)
	legacy, e := s.PlaceBet(ctx, betting.PlaceBetRequest{AccountID: user, RoundID: round, Currency: "POINTS", GameRoomID: room, PlayMode: "road", Selection: json.RawMessage(`{"pick":"small","extra":1}`), StakeMinor: 1, ClientRequestID: legacyKey})
	if e != nil || legacy.BetID != legacyID {
		t.Fatalf("legacy retry=%+v %v", legacy, e)
	}
	req := betting.PlaceBetRequest{AccountID: user, RoundID: round, Currency: "POINTS", GameRoomID: room, PlayMode: "road", Selection: json.RawMessage(`{"pick":"odd"}`), StakeMinor: 10000}
	var first betting.PlacedBet
	requests := make([]betting.PlaceBetRequest, 3)
	for i := range 3 {
		req.ClientRequestID = uuid.NewString()
		requests[i] = req
		got, e := s.PlaceBet(ctx, req)
		if e != nil {
			t.Fatal(e)
		}
		if i == 0 {
			first = got
		}
		if got.BetID != first.BetID || got.StakeMinor != int64(i+1)*10000 || got.PlacementCount != int64(i+1) || got.BalanceAfterBetMinor == nil || *got.BalanceAfterBetMinor != 1000000-int64(i+1)*10000 {
			t.Fatalf("append %d: %+v", i, got)
		}
	}
	count := func(q string, arg any, want int) {
		t.Helper()
		var n int
		if e := p.QueryRow(ctx, q, arg).Scan(&n); e != nil || n != want {
			t.Fatalf("count=%d want=%d err=%v", n, want, e)
		}
	}
	count(`SELECT count(*) FROM bets WHERE user_id=$1 AND status='accepted'`, user, 1)
	count(`SELECT count(*) FROM ledger_entries WHERE business_id=$1 AND entry_type='bet_debit'`, first.BetID, 3)
	mine, e := s.ListUserBets(ctx, user, "accepted", 50, 0)
	if e != nil || len(mine) != 1 || mine[0].Stake != "30.000" {
		t.Fatalf("mine=%+v %v", mine, e)
	}
	admin, e := operations.NewService(p).ListAdminBets(ctx, operations.BetQuery{User: publicID(t, ctx, p, user), Status: "accepted"})
	if e != nil || len(admin) != 1 || admin[0].PlacementCount != 3 {
		t.Fatalf("admin=%+v %v", admin, e)
	}
	for _, r := range requests {
		got, e := s.PlaceBet(ctx, r)
		if e != nil || got.StakeMinor != 30000 || got.BetID != first.BetID {
			t.Fatalf("retry=%+v %v", got, e)
		}
	}
	changed := requests[0]
	changed.StakeMinor++
	if _, e = s.PlaceBet(ctx, changed); !errors.Is(e, betting.ErrRequestConflict) {
		t.Fatalf("changed request=%v", e)
	}
	// Different currencies and picks must remain different canonical orders.
	other := req
	other.ClientRequestID = uuid.NewString()
	other.Currency = "USDT"
	other.StakeMinor = 100000
	otherBet, e := s.PlaceBet(ctx, other)
	if e != nil || otherBet.BetID == first.BetID {
		t.Fatalf("currency=%+v %v", otherBet, e)
	}
	other.Currency = "POINTS"
	other.StakeMinor = 10000
	other.ClientRequestID = uuid.NewString()
	other.Selection = json.RawMessage(`{"pick":"even"}`)
	otherBet, e = s.PlaceBet(ctx, other)
	if e != nil || otherBet.BetID == first.BetID {
		t.Fatalf("pick=%+v %v", otherBet, e)
	}
	// Retry and a new placement race: only the new request adds funds once.
	next := requests[0]
	next.ClientRequestID = uuid.NewString()
	errs := make(chan error, 6)
	for range 6 {
		go func() { _, e := s.PlaceBet(ctx, next); errs <- e }()
	}
	for range 6 {
		if e := <-errs; e != nil {
			t.Fatal(e)
		}
	}
	got, e := s.Find(ctx, first.BetID)
	if e != nil || got.StakeMinor != 40000 || got.PlacementCount != 4 {
		t.Fatalf("concurrent=%+v %v", got, e)
	}
	cancelled, e := s.CancelBet(ctx, first.BetID, user)
	if e != nil || cancelled.StakeMinor != 40000 || cancelled.BalanceAfterRefundMinor == nil || *cancelled.BalanceAfterRefundMinor != 990000 {
		t.Fatalf("cancel=%+v %v", cancelled, e)
	}
	if _, e = s.CancelBet(ctx, first.BetID, user); e != nil {
		t.Fatal(e)
	}
	count(`SELECT count(*) FROM ledger_entries WHERE business_id=$1 AND business_type='bet_cancel'`, first.BetID, 1)
	replay, e := s.PlaceBet(ctx, next)
	if e != nil || replay.Status != "cancelled" {
		t.Fatalf("cancelled retry=%+v %v", replay, e)
	}
	// New request after cancellation creates a new order. Three tiny stakes
	// settle as floor(30*1.94)=58, not 3*floor(10*1.94)=57.
	next.StakeMinor = 10
	var winning betting.PlacedBet
	for range 3 {
		next.ClientRequestID = uuid.NewString()
		winning, e = s.PlaceBet(ctx, next)
		if e != nil {
			t.Fatal(e)
		}
	}
	if winning.BetID == first.BetID || winning.StakeMinor != 30 {
		t.Fatalf("new order=%+v", winning)
	}
	exec(`UPDATE rounds SET status='closed' WHERE id=$1`, round)
	var raw json.RawMessage
	if e = p.QueryRow(ctx, `SELECT rules FROM game_types WHERE id=$1`, gt).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	rules, e := game.ParseRules(raw)
	if e != nil {
		t.Fatal(e)
	}
	for range 2 {
		if _, e = settlement.NewService(p).SettleRound(ctx, round, []string{"1", "odd", "small"}, rules); e != nil {
			t.Fatal(e)
		}
	}
	winning, e = s.Find(ctx, winning.BetID)
	if e != nil || winning.PayoutMinor != 58 || winning.Status != "won" || winning.BalanceAfterSettlementMinor == nil || *winning.BalanceAfterSettlementMinor != 990028 {
		t.Fatalf("settled=%+v %v", winning, e)
	}
	count(`SELECT count(*) FROM ledger_entries WHERE business_id=$1 AND business_type='settlement'`, winning.BetID, 1)
	if _, e = s.CancelBet(ctx, winning.BetID, user); !errors.Is(e, betting.ErrBetCancellationClosed) {
		t.Fatalf("late cancel=%v", e)
	}
	// Leave no pending fixtures for background settlement tests.
}

func publicID(t *testing.T, ctx context.Context, p *pgxpool.Pool, user string) string {
	t.Helper()
	var id string
	if e := p.QueryRow(ctx, `SELECT public_id::text FROM users WHERE id=$1`, user).Scan(&id); e != nil {
		t.Fatal(e)
	}
	return id
}

// Simulated merges must never create a debit, payout credit, or refund credit.
func TestHashMergedVirtualBalances(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
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
	user, wid := uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO users(id,display_name,is_virtual) VALUES($1,'virtual merge',true)`, user)
	exec(`INSERT INTO wallets(id,user_id,currency) VALUES($1,$2,'POINTS')`, wid, user)
	for _, action := range []string{"cancel", "settle"} {
		round := uuid.NewString()
		exec(`INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at) VALUES($1,'09000000-0000-4000-8000-000000000001',$2,'open',now()+interval '1 hour')`, round, time.Now().UnixNano())
		var bet betting.PlacedBet
		for range 3 {
			bet, e = betting.NewService(p).PlaceBet(ctx, betting.PlaceBetRequest{AccountID: user, RoundID: round, Currency: "POINTS", ClientRequestID: uuid.NewString(), GameRoomID: "94000000-0000-4000-8000-000000000001", PlayMode: "road", Selection: json.RawMessage(`{"pick":"odd"}`), StakeMinor: 10})
			if e != nil {
				t.Fatal(e)
			}
		}
		if bet.StakeMinor != 30 || bet.PlacementCount != 3 || bet.BalanceAfterBetMinor != nil {
			t.Fatalf("virtual=%+v", bet)
		}
		if action == "cancel" {
			if _, e = betting.NewService(p).CancelBet(ctx, bet.BetID, user); e != nil {
				t.Fatal(e)
			}
		} else {
			exec(`UPDATE rounds SET status='closed' WHERE id=$1`, round)
			var raw json.RawMessage
			if e = p.QueryRow(ctx, `SELECT rules FROM game_types WHERE id='09000000-0000-4000-8000-000000000001'`).Scan(&raw); e != nil {
				t.Fatal(e)
			}
			rules, e := game.ParseRules(raw)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = settlement.NewService(p).SettleRound(ctx, round, []string{"1", "odd", "small"}, rules); e != nil {
				t.Fatal(e)
			}
			got, e := betting.NewService(p).Find(ctx, bet.BetID)
			if e != nil || got.PayoutMinor != 58 {
				t.Fatalf("virtual payout=%+v %v", got, e)
			}
		}
	}
	var balance int64
	var entries int
	if e = p.QueryRow(ctx, `SELECT available_minor,(SELECT count(*) FROM ledger_entries WHERE wallet_id=$1) FROM wallets WHERE id=$1`, wid).Scan(&balance, &entries); e != nil || balance != 0 || entries != 0 {
		t.Fatalf("balance=%d ledger=%d %v", balance, entries, e)
	}
}
