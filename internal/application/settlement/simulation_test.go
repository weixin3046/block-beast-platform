package settlement

import (
	"context"
	"encoding/json"
	"github.com/block-beast/platform/internal/application/betting"
	"github.com/block-beast/platform/internal/domain/game"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
	"time"
)

// A simulated stake must never become a wallet payout or a refundable deposit.
func TestSimulationNeverMovesWallet(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, action := range []string{"win", "lose", "refund", "cancel"} {
		t.Run(action, func(t *testing.T) {
			user, w, r := uuid.NewString(), uuid.NewString(), uuid.NewString()
			exec := func(q string, args ...any) {
				t.Helper()
				if _, e := p.Exec(ctx, q, args...); e != nil {
					t.Fatal(e)
				}
			}
			exec("INSERT INTO users(id,display_name,is_virtual) VALUES($1,'simulation test',true)", user)
			exec("INSERT INTO wallets(id,user_id,currency) VALUES($1,$2,'POINTS')", w, user)
			exec("INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at) VALUES($1,'09000000-0000-4000-8000-000000000001',$2,'open',$3)", r, time.Now().UnixNano(), time.Now().Add(time.Hour))
			defer func() {
				p.Exec(ctx, "DELETE FROM outbox_events WHERE aggregate_id=$1 OR payload->>'user_id'=$2 OR aggregate_id IN (SELECT id::text FROM bets WHERE round_id=$1)", r, user)
				p.Exec(ctx, "DELETE FROM ledger_entries WHERE wallet_id=$1", w)
				p.Exec(ctx, "DELETE FROM bets WHERE round_id=$1", r)
				p.Exec(ctx, "DELETE FROM rounds WHERE id=$1", r)
				p.Exec(ctx, "DELETE FROM wallets WHERE id=$1", w)
				p.Exec(ctx, "DELETE FROM users WHERE id=$1", user)
			}()
			bets := betting.NewService(p)
			b, e := bets.PlaceBet(ctx, betting.PlaceBetRequest{ClientRequestID: uuid.NewString(), RoundID: r, AccountID: user, Currency: "POINTS", GameRoomID: "94000000-0000-4000-8000-000000000001", PlayMode: "road", Selection: json.RawMessage(`{"pick":"odd"}`), StakeMinor: 1000})
			if e != nil {
				t.Fatal(e)
			}
			hook := &taskHookSpy{}
			svc := NewService(p).WithTaskHook(hook)
			for range 2 {
				switch action {
				case "cancel":
					_, e = bets.CancelBet(ctx, b.BetID, user)
				case "refund":
					_, e = svc.CancelRound(ctx, r)
				default:
					exec("UPDATE rounds SET status='closed' WHERE id=$1 AND status='open'", r)
					var raw []byte
					if e = p.QueryRow(ctx, "SELECT rules FROM game_types WHERE id='09000000-0000-4000-8000-000000000001'").Scan(&raw); e != nil {
						t.Fatal(e)
					}
					rules, e2 := game.ParseRules(raw)
					if e2 != nil {
						t.Fatal(e2)
					}
					outcome := []string{"1", "small", "odd"}
					if action == "lose" {
						outcome = []string{"2", "small", "even"}
					}
					_, e = svc.SettleRound(ctx, r, outcome, rules)
				}
				if e != nil {
					t.Fatal(e)
				}
			}
			var balance, entries, payout int64
			if e = p.QueryRow(ctx, "SELECT available_minor,(SELECT count(*) FROM ledger_entries WHERE wallet_id=$1) FROM wallets WHERE id=$1", w).Scan(&balance, &entries); e != nil {
				t.Fatal(e)
			}
			if balance != 0 || entries != 0 || hook.calls != 0 {
				t.Fatalf("simulation leaked: balance=%d ledger=%d tasks=%d", balance, entries, hook.calls)
			}
			if action == "win" {
				if e = p.QueryRow(ctx, "SELECT payout_minor FROM bets WHERE id=$1", b.BetID).Scan(&payout); e != nil || payout != 1940 {
					t.Fatalf("simulated result=%d %v", payout, e)
				}
			}
			if action == "cancel" {
				var n int
				if e = p.QueryRow(ctx, "SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='game.bet.cancelled'", b.BetID).Scan(&n); e != nil || n != 1 {
					t.Fatalf("cancel event count=%d %v", n, e)
				}
			}
		})
	}
}
