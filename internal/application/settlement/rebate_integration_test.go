package settlement

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/application/betting"
	"github.com/block-beast/platform/internal/application/rebate"
	"github.com/block-beast/platform/internal/domain/game"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Exercise the actual placing and settling transactions, including concurrent
// rounds sharing beneficiaries and retries after a committed settlement.
func TestRebateSnapshotConcurrentSettlement(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := p.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	parents := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	for i, id := range parents {
		exec(`INSERT INTO users(id,display_name,agent_level) VALUES($1,'rebate parent',$2)`, id, i+1)
		exec(`INSERT INTO wallets(id,user_id,currency) VALUES($1,$2,'POINTS')`, uuid.NewString(), id)
		if i > 0 {
			exec(`INSERT INTO agent_relations(user_id,parent_user_id) VALUES($1,$2)`, parents[i-1], id)
		}
	}
	var raw []byte
	if err := p.QueryRow(ctx, `SELECT rules FROM game_types WHERE id='09000000-0000-4000-8000-000000000001'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	rules, err := game.ParseRules(raw)
	if err != nil {
		t.Fatal(err)
	}
	var rounds, bets []string
	for _, mode := range []string{"road", "dodge"} {
		user, round := uuid.NewString(), uuid.NewString()
		// Independent game types avoid unrelated open rounds and permit concurrent settlement.
		gameID := uuid.NewString()
		exec(`INSERT INTO game_types(id,code,name,rules) SELECT $1::uuid,$1::uuid::text,'rebate isolated',rules FROM game_types WHERE id='09000000-0000-4000-8000-000000000001'`, gameID)
		exec(`INSERT INTO game_room_types(room_id,game_type_id) VALUES('94000000-0000-4000-8000-000000000001',$1)`, gameID)
		exec(`INSERT INTO users(id,display_name) VALUES($1,'rebate player')`, user)
		exec(`INSERT INTO agent_relations(user_id,parent_user_id) VALUES($1,$2)`, user, parents[0])
		exec(`INSERT INTO wallets(id,user_id,currency,available_minor) VALUES($1,$2,'POINTS',100000)`, uuid.NewString(), user)
		exec(`INSERT INTO rounds(id,game_type_id,sequence,status,bet_closes_at) VALUES($1,$2,1,'open',now()+interval '1 hour')`, round, gameID)
		pick := "odd"
		if mode == "dodge" {
			pick = "2"
		}
		selection, _ := json.Marshal(map[string]string{"pick": pick})
		bet, err := betting.NewService(p).PlaceBet(ctx, betting.PlaceBetRequest{AccountID: user, RoundID: round, ClientRequestID: uuid.NewString(), Currency: "POINTS", StakeMinor: 1000, GameRoomID: "94000000-0000-4000-8000-000000000001", PlayMode: mode, Selection: selection})
		if err != nil {
			t.Fatal(err)
		}
		bets = append(bets, bet.BetID)
		rounds = append(rounds, round)
		exec(`UPDATE rounds SET status='closed' WHERE id=$1`, round)
	}
	// Neither a later grade change nor the old direct commission rate may
	// replace the accepted bets' level/rate snapshots.
	exec(`UPDATE users SET agent_level=6 WHERE id=$1`, parents[0])
	outcome := []string{"1", "odd", "small"}
	errs := make(chan error, len(rounds))
	for _, round := range rounds {
		go func() {
			_, err := NewService(p).SettleRound(ctx, round, outcome, rules)
			if err == nil {
				_, err = NewService(p).SettleRound(ctx, round, outcome, rules)
			}
			errs <- err
		}()
	}
	for range rounds {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	// Road uses 1,000 stake; dodge uses 80 net winnings, rounded per recipient.
	for i, want := range []int64{15, 2, 4} {
		var got int64
		if err := p.QueryRow(ctx, `SELECT available_minor FROM wallets WHERE user_id=$1 AND currency='POINTS'`, parents[i]).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("level %d balance=%d want=%d", i+1, got, want)
		}
	}
	var count int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM rebate_allocations WHERE bet_id=ANY($1::uuid[])`, bets).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("allocation count=%d want=4", count)
	}
	if err := p.QueryRow(ctx, `SELECT count(*) FROM ledger_entries l JOIN commission_entries c ON c.id::text=l.business_id WHERE c.source_bet_id=ANY($1::uuid[]) AND l.business_type='commission'`, bets).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("ledger count=%d want=4", count)
	}
	// Pagination does not truncate totals; viewer scoping cannot expose a
	// different beneficiary, and public amounts never carry minor units.
	records, err := rebate.NewService(p).ListRecords(ctx, rebate.RecordQuery{ViewerID: parents[0], Currency: "POINTS", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if records.Total != 2 || len(records.Items) != 1 || len(records.Summary) != 1 || records.Summary[0].PaidAmount != "0.015" || records.Summary[0].SourceBets != 2 {
		t.Fatalf("records=%+v", records)
	}
	var otherID int64
	if err = p.QueryRow(ctx, `SELECT public_id FROM users WHERE id=$1`, parents[1]).Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	foreign, err := rebate.NewService(p).ListRecords(ctx, rebate.RecordQuery{ViewerID: parents[0], BeneficiaryUserID: otherID})
	if err != nil || foreign.Total != 0 {
		t.Fatalf("foreign income leak: %+v %v", foreign, err)
	}
}
